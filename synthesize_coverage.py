#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Convierte la cobertura publica de los 4 operadores (Movistar, Claro, Tigo,
WOM) en datos consultables: por municipio (ratio de cobertura por operador y
tecnologia) y por celda H3 (presencia por punto).

Salidas (en --out):
  coverage_municipality.csv  dane_code, department, municipality, operator,
                             technology, covered_ratio, covered_km2, area_km2
  coverage_cells.csv         h3, operator, technology (solo celdas cubiertas)
  manifest-synthesis.json    metadatos de la corrida
  rasters/<op>_<tech>.tif    rasters binarios 0/1 (para debug)

Uso:
  python3 synthesize_coverage.py --out data/synthesis [--zoom 8] [--h3-res 7]
      [--jobs 8] [--limits data/limits/colombia_adm2.geojson]
      [--official-csv data/raw/postdata/crc_cobertura_movil.csv]
      [--skip-download] [--limit N] [--only movistar,claro]
"""

import argparse
import concurrent.futures as cf
import csv
import json
import logging
import math
import os
import re
import sys
import time
import unicodedata

import numpy as np
import requests
from PIL import Image

log = logging.getLogger("synthesize")

UA = "colombia-difunde-synthesizer/1.0"
ROOT = os.path.dirname(os.path.abspath(__file__))

# ---- grids ----
# Todas las capas se ensamblan en una grilla WGS84 de 360/65536 grados por
# pixel (~0.0055 deg, ~600 m). Es identica al WMTS 4326 nivel 7 y al XYZ z8,
# asi que la alineacion entre proveedores es directa.
GLOBAL_PS = 360.0 / 65536.0  # 0.0054931640625 deg/px

# Colombia continental (con margen).
B_W, B_S, B_E, B_N = -80.0, -5.0, -66.0, 12.5

# Resoluciones objetivo.
XYZ_ZOOM = 8          # Claro/Tigo
WMTS_LEVEL = 7        # WOM (== 360/65536 deg/px)
H3_RES = 7

# Mapeo tecnologia del operador -> familia (2G/3G/4G/5G).
FAMILY = {
    "gsm": "2G", "2g": "2G",
    "umts": "3G", "3g": "3G",
    "lte": "4G", "4g": "4G",
    "rssi": "5G", "5g": "5G",
}

BACKEND = {
    "claro": "minisitiosclaro.claro.com.co",
    "tigo": "coberturadigital-uat-co.tigocloud.net",
    "wom": "mi.movilpt.co",
}

# ---------- utilidades ----------


def norm_name(s):
    if not s:
        return ""
    s = unicodedata.normalize("NFD", str(s))
    s = "".join(c for c in s if unicodedata.category(c) != "Mn")
    s = s.upper()
    s = re.sub(r"[^A-Z0-9]+", " ", s).strip()
    for w in ("CIUDAD DE ", "MUNICIPIO DE ", "EL ", "LA ", "LOS ", "LAS ", "SAN ", "SANTA ", "SANTIAGO DE "):
        pass
    return s


def xyz_range(west, east, south, north, z):
    n = 2 ** z
    x0 = int((west + 180.0) / 360.0 * n)
    x1 = int(math.floor((east + 180.0) / 360.0 * n))
    def lat_to_y(lat):
        r = math.radians(lat)
        return (1.0 - math.asinh(math.tan(r)) / math.pi) / 2.0 * n
    y0 = int(math.floor(lat_to_y(north)))
    y1 = int(math.floor(lat_to_y(south)))
    return x0, x1, y0, y1


def xyz_bounds(x, y, z):
    n = 2 ** z
    w = x / n * 360.0 - 180.0
    e = (x + 1) / n * 360.0 - 180.0
    def y_to_lat(yy):
        return math.degrees(math.atan(math.sinh(math.pi * (1 - 2 * yy / n))))
    nlat = y_to_lat(y)
    slat = y_to_lat(y + 1)
    return w, slat, e, nlat


def wmts_level_bbox(lat, lon):
    """BBox (w,s,e,n) del tile WMTS 4326 nivel `level` que contiene (lat,lon)."""
    span = 180.0 / 2 ** WMTS_LEVEL
    col = int((lon + 180.0) / span)
    row = int((90.0 - lat) / span)
    w = -180.0 + col * span
    n = 90.0 - row * span
    return w, n - span, w + span, n, col, row


# ---------- conversion a binario ----------


def png_to_binary(data):
    """data: bytes de un PNG -> array bool 0/1 (cubierto)."""
    im = Image.open(__import__("io").BytesIO(data)).convert("RGBA")
    a = np.asarray(im).astype(np.int16)
    r, g, b, al = a[..., 0], a[..., 1], a[..., 2], a[..., 3]
    covered = (al > 128) & ~((r > 230) & (g > 230) & (b > 230))
    return covered


def fetch(url, timeout=30, retries=3):
    last = None
    for i in range(retries):
        try:
            r = requests.get(url, timeout=timeout, headers={"User-Agent": UA})
            if r.status_code == 200:
                return r.content
            if r.status_code in (404, 403):
                return None
            last = f"http {r.status_code}"
        except Exception as e:
            last = str(e)
        time.sleep(1.5 * (i + 1))
    log.warning("fallo %s (%s)", url, last)
    return None


# ---------- grilla ----------


class Grid:
    """Grilla WGS84 con top-left (west, north), tamano de pixel global PS."""

    def __init__(self):
        self.ps = GLOBAL_PS
        self.west, self.north = B_W, B_N
        self.cols = int(math.ceil((B_E - B_W) / self.ps))
        self.rows = int(math.ceil((B_N - B_S) / self.ps))
        log.info("grid %dx%d px (%dx%d km)", self.cols, self.rows,
                 round(self.cols * self.ps * 111, 0), round(self.rows * self.ps * 111, 0))

    def empty(self):
        return np.zeros((self.rows, self.cols), dtype=np.bool_)

    def paste_bbox(self, arr, bbox, img_bytes):
        """Pega la imagen (binaria) en el bbox (w,s,e,n) con resample suave."""
        w, s, e, n = bbox
        x0 = (w - self.west) / self.ps
        y0 = (self.north - n) / self.ps
        x1 = (e - self.west) / self.ps
        y1 = (self.north - s) / self.ps
        if x1 <= x0 or y1 <= y0:
            return
        ix0, iy0 = max(0, int(math.floor(x0))), max(0, int(math.floor(y0)))
        ix1, iy1 = min(self.cols, int(math.ceil(x1))), min(self.rows, int(math.ceil(y1)))
        if ix1 <= ix0 or iy1 <= iy0:
            return
        import io
        from PIL import Image
        a = np.asarray(Image.open(io.BytesIO(img_bytes)).convert("RGBA")).astype(int)
        covered = (a[..., 3] > 128) & ~((a[..., 0] > 230) & (a[..., 1] > 230) & (a[..., 2] > 230))
        fm = covered.astype(np.float32)
        tw, th = ix1 - ix0, iy1 - iy0
        if fm.shape != (th, tw):
            im = Image.fromarray((fm * 255).astype(np.uint8)).resize((tw, th), Image.LANCZOS)
            fm = np.asarray(im).astype(np.float32) / 255.0
        arr[iy0:iy1, ix0:ix1] |= fm > 0.5


# ---------- descarga con cache ----------


class TileCache:
    def __init__(self, out_root):
        self.root = os.path.join(out_root, "tiles")

    def _path(self, provider, tech, z, x, y):
        d = os.path.join(self.root, provider, tech, str(z), str(x))
        os.makedirs(d, exist_ok=True)
        return os.path.join(d, f"{y}.png")

    def get_or_fetch(self, provider, tech, z, x, y, url):
        p = self._path(provider, tech, z, x, y)
        if os.path.exists(p):
            return p
        data = fetch(url)
        if data is None:
            return None
        with open(p, "wb") as f:
            f.write(data)
        return p


# ---------- ensamblado por proveedor ----------


def build_movistar(grid, cache, out_root, only, skip_download):
    """Overlays KML de Movistar: img por bbox desde kml-manifest.json."""
    root = os.path.join(ROOT, "data", "movistar_cobertura")
    manifest = json.load(open(os.path.join(root, "manifest.json")))
    out = {}
    techs = [t for t in manifest["technologies"] if not only or t["id"].lower() in only]
    for t in techs:
        fam = FAMILY.get(t["id"].lower())
        kp = os.path.join(root, t["kml_manifest_path"])
        if not os.path.exists(kp):
            log.warning("movistar %s sin kml-manifest", t["id"])
            continue
        km = json.load(open(kp))
        ovs = [o for r in km.get("roots", []) for o in r.get("ground_overlays", [])]
        if not ovs:
            log.warning("movistar %s: 0 overlays, se omite", t["id"])
            continue
        arr = grid.empty()
        n = 0
        for ov in ovs:
            bbox = ov.get("bbox") or {}
            try:
                b = (float(bbox["west"]), float(bbox["south"]),
                     float(bbox["east"]), float(bbox["north"]))
            except Exception:
                continue
            img = cache.get_or_fetch("movistar", fam or t["id"], 0, 0, 0, ov["url"])
            if not img:
                continue
            grid.paste_bbox(arr, b, open(img, "rb").read())
            n += 1
        log.info("movistar %s (%s): %d overlays -> %d px cubiertos",
                 t["id"], fam, n, int(arr.sum()))
        if n:
            out[t["id"]] = (fam, arr)
    return out


def build_claro(grid, cache, out_root, only, skip_download):
    root = os.path.join(ROOT, "data", "claro_cobertura")
    manifest = json.load(open(os.path.join(root, "manifest.json")))
    base = manifest["base_urls"]
    out = {}
    x0, x1, y0, y1 = xyz_range(B_W, B_E, B_S, B_N, XYZ_ZOOM)
    for key, url_key in (("GSM", "ulrMapGSM"), ("UMTS", "ulrMapUMTS"),
                         ("LTE", "ulrMapLTE"), ("5G", "ulrMap5G")):
        if only and key.lower() not in only:
            continue
        baseurl = base.get(url_key)
        if not baseurl:
            continue
        arr = grid.empty()
        n = 0
        for x in range(x0, x1 + 1):
            for y in range(y0, y1 + 1):
                u = f"{baseurl.rstrip('/')}/Z{XYZ_ZOOM}/{y}/{x}.png"
                img = cache.get_or_fetch("claro", key, XYZ_ZOOM, x, y, u)
                if not img:
                    continue
                w, s, e, nn = xyz_bounds(x, y, XYZ_ZOOM)
                grid.paste_bbox(arr, (w, s, e, nn), open(img, "rb").read())
                n += 1
        log.info("claro %s: %d tiles -> %d px", key, n, int(arr.sum()))
        if n:
            out[key] = (FAMILY[key.lower()], arr)
    return out


def build_tigo(grid, cache, out_root, only, skip_download):
    root = os.path.join(ROOT, "data", "tigo_cobertura")
    manifest = json.load(open(os.path.join(root, "manifest.json")))
    out = {}
    x0, x1, y0, y1 = xyz_range(B_W, B_E, B_S, B_N, XYZ_ZOOM)
    for t in manifest["technologies"]:
        key = t["id"].lower()
        if only and key not in only:
            continue
        templates = t.get("tile_url_templates") or []
        general = next((tp for tp in templates if "/ciudades/" not in tp), None)
        if not general:
            continue
        baseurl = manifest["source"]["map_page"].rstrip("/")
        arr = grid.empty()
        n = 0
        for x in range(x0, x1 + 1):
            for y in range(y0, y1 + 1):
                rel = general.replace("{zoom}", str(XYZ_ZOOM)).replace("{y}", str(y)).replace("{x}", str(x))
                u = f"{baseurl}/{rel.lstrip('/')}"
                img = cache.get_or_fetch("tigo", key.upper(), XYZ_ZOOM, x, y, u)
                if not img:
                    continue
                w, s, e, nn = xyz_bounds(x, y, XYZ_ZOOM)
                grid.paste_bbox(arr, (w, s, e, nn), open(img, "rb").read())
                n += 1
        log.info("tigo %s: %d tiles -> %d px", key, n, int(arr.sum()))
        if n:
            out[key] = (FAMILY[key], arr)
    return out


def build_wom(grid, cache, out_root, only, skip_download):
    root = os.path.join(ROOT, "data", "wom_cobertura")
    manifest = json.load(open(os.path.join(root, "manifest.json")))
    out = {}
    base = "https://" + BACKEND["wom"]
    span = 180.0 / 2 ** WMTS_LEVEL
    for fam, layer_id in (("3G", "cobertura3G:3G"), ("4G", "cobertu:4G"), ("5G", "cobertura5G:5G")):
        if only and fam.lower() not in only:
            continue
        arr = grid.empty()
        n = 0
        for lat in np.arange(B_S, B_N, span):
            for lon in np.arange(B_W, B_E, span):
                w, s, e, nn, col, row = wmts_level_bbox(lat + span / 2, lon + span / 2)
                u = (f"{base}/geoserverwom/gwc/service/wmts/rest/{layer_id}/default/"
                     f"EPSG:4326/EPSG:4326:{WMTS_LEVEL}/{row:03d}/{col:03d}?format=image/png8")
                img = cache.get_or_fetch("wom", fam, WMTS_LEVEL, col, row, u)
                if not img:
                    continue
                grid.paste_bbox(arr, (w, s, e, nn), open(img, "rb").read())
                n += 1
        log.info("wom %s: %d tiles -> %d px", fam, n, int(arr.sum()))
        if n:
            out[fam] = (fam, arr)
    return out


BUILDERS = {
    "movistar": build_movistar,
    "claro": build_claro,
    "tigo": build_tigo,
    "wom": build_wom,
}


# ---------- analisis municipal ----------

def load_municipalities(limits_path, official_csv):
    import geopandas as gpd
    g = gpd.read_file(limits_path)
    g = g[["shapeName", "geometry"]].copy()
    g.columns = ["name", "geometry"]
    g = g[g.geometry.is_valid]

    # dane_code + departamento desde el baseline oficial (ultimo trimestre).
    codes = {}
    if official_csv and os.path.exists(official_csv):
        seen = set()
        with open(official_csv, encoding="utf-8", errors="replace") as f:
            for row in csv.reader(f, delimiter=";"):
                if len(row) < 7 or row[0] == "\ufeffANNO" or not row[0].strip().isdigit():
                    continue
                year, quarter = row[0], row[1]
                if not seen:
                    try:
                        q = (int(year), int(quarter))
                    except Exception:
                        continue
                    seen.add("q")
                dept, muni, dane = row[3], row[5], row[4]
                if not muni or not dane:
                    continue
                codes.setdefault(norm_name(muni), (dane, norm_name(dept), muni))

    g["dane_code"] = g["name"].map(lambda n: (codes.get(norm_name(n), (None, None, None)))[0])
    g["department"] = g["name"].map(lambda n: (codes.get(norm_name(n), (None, None, None)))[1])
    g["canonical"] = g["name"].map(lambda n: (codes.get(norm_name(n), (None, None, n)))[2])
    g = g[g["dane_code"].notna() & (g["dane_code"] != "")]
    g["dane_code"] = g["dane_code"].astype(str)
    g["area_km2"] = g.geometry.to_crs("EPSG:3857").area / 1e6
    log.info("municipios: %d poligonos con dane_code", len(g))
    return g


def municipal_stats(g, rasters):
    from rasterstats import zonal_stats
    rows = []
    for op, fam, tif in rasters:
        stats = zonal_stats(g, tif, stats=["mean"], nodata=255, all_touched=False)
        for i, st in enumerate(stats):
            ratio = st["mean"] if st and st["mean"] is not None else None
            if ratio is None:
                continue
            rows.append({
                "dane_code": g.iloc[i]["dane_code"],
                "department": g.iloc[i]["department"] or "",
                "municipality": g.iloc[i]["canonical"] or g.iloc[i]["name"],
                "operator": op,
                "technology": fam,
                "covered_ratio": round(float(ratio), 5),
                "covered_km2": round(float(ratio) * g.iloc[i]["area_km2"], 3),
                "area_km2": round(g.iloc[i]["area_km2"], 3),
            })
    return rows


def cell_stats(colombia_geom, rasters, h3_res):
    import h3
    from shapely.geometry import Point
    from shapely.prepared import prep
    prep_geom = prep(colombia_geom)
    # Muestreo por grilla: hex res 7 (~2.4 km borde) => paso ~1.9 km.
    step = 1.9 / 111.0
    cells = set()
    lat = B_S
    while lat <= B_N:
        lon = B_W
        while lon <= B_E:
            if prep_geom.contains(Point(lon, lat)):
                cells.add(h3.latlng_to_cell(lat, lon, h3_res))
            lon += step
        lat += step
    cells = list(cells)
    log.info("celdas H3 res %d: %d", h3_res, len(cells))

    lats = []
    lons = []
    for c in cells:
        lat, lon = h3.cell_to_latlng(c)
        lats.append(lat)
        lons.append(lon)
    lats = np.array(lats)
    lons = np.array(lons)

    rows = []
    for op, fam, tif in rasters:
        import rasterio
        with rasterio.open(tif) as src:
            arr = src.read(1)
        col = ((lons - B_W) / GLOBAL_PS).astype(int)
        row = ((B_N - lats) / GLOBAL_PS).astype(int)
        ok = (col >= 0) & (col < arr.shape[1]) & (row >= 0) & (row < arr.shape[0])
        cov = arr[row[ok], col[ok]]
        covered_cells = [cells[i] for i in np.where(ok)[0] if cov[i]]
        for c in covered_cells:
            rows.append((c, op, fam))
        log.info("celdas cubiertas %s %s: %d", op, fam, len(covered_cells))
    return rows


# ---------- main ----------

def write_tif(arr, path):
    import rasterio
    profile = {
        "driver": "GTiff", "width": arr.shape[1], "height": arr.shape[0],
        "count": 1, "dtype": "uint8", "crs": "EPSG:4326",
        "transform": rasterio.transform.from_origin(B_W, B_N, GLOBAL_PS, GLOBAL_PS),
        "compress": "lzw", "nodata": 255,
    }
    with rasterio.open(path, "w", **profile) as dst:
        dst.write(np.where(arr, 1, 0).astype("uint8"), 1)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=os.path.join(ROOT, "data", "synthesis"))
    ap.add_argument("--limits", default=os.path.join(ROOT, "data", "limits", "colombia_adm2.geojson"))
    ap.add_argument("--official-csv", default=os.path.join(ROOT, "data", "raw", "postdata", "crc_cobertura_movil.csv"))
    ap.add_argument("--skip-download", action="store_true")
    ap.add_argument("--only", default="")
    ap.add_argument("--no-cells", action="store_true")
    ap.add_argument("--limit", type=int, default=0, help="debug: numero de municipios")
    ap.add_argument("--jobs", type=int, default=8)
    args = ap.parse_args()

    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    only = {s.strip().lower() for s in args.only.split(",")} if args.only else set()

    os.makedirs(args.out, exist_ok=True)
    cache = TileCache(args.out)

    grid = Grid()
    built = {}
    for op, fn in BUILDERS.items():
        if only and op not in only:
            continue
        t0 = time.time()
        try:
            built[op] = fn(grid, cache, args.out, set(), args.skip_download)
        except Exception as e:
            log.exception("build %s fallo: %s", op, e)
        log.info("build %s en %.1fs", op, time.time() - t0)

    rasters = []
    tifdir = os.path.join(args.out, "rasters")
    os.makedirs(tifdir, exist_ok=True)
    for op, techs in built.items():
        for tech, (fam, arr) in techs.items():
            tif = os.path.join(tifdir, f"{op}_{fam}.tif")
            write_tif(arr, tif)
            rasters.append((op, fam, tif))
            del arr

    if not rasters:
        log.error("sin rasters; no se genera nada")
        return 1

    g = load_municipalities(args.limits, args.official_csv)
    if args.limit:
        g = g.head(args.limit)

    log.info("analisis municipal...")
    muni = municipal_stats(g, rasters)
    muni_path = os.path.join(args.out, "coverage_municipality.csv")
    with open(muni_path, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=list(muni[0].keys()))
        w.writeheader()
        w.writerows(muni)
    log.info("coverage_municipality.csv: %d filas", len(muni))

    cells_path = None
    if not args.no_cells:
        import geopandas as gpd
        gfull = gpd.read_file(args.limits)
        union = gfull.geometry.union_all()
        log.info("analisis de celdas H3...")
        cells = cell_stats(union, rasters, H3_RES)
        cells_path = os.path.join(args.out, "coverage_cells.csv")
        with open(cells_path, "w", newline="", encoding="utf-8") as f:
            wr = csv.writer(f)
            wr.writerow(["h3", "operator", "technology"])
            wr.writerows(cells)
        log.info("coverage_cells.csv: %d filas", len(cells))

    meta = {
        "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "zoom": XYZ_ZOOM, "wmts_level": WMTS_LEVEL, "h3_res": H3_RES,
        "pixel_deg": GLOBAL_PS, "municipalities": len(muni),
        "operators": {op: {t: {"technology": fam, "tiles": None}
                           for t, (fam, a) in b.items()} for op, b in built.items()},
    }
    for op in built:
        src = os.path.join(ROOT, "data", f"{op}_cobertura", "manifest.json")
        if os.path.exists(src):
            m = json.load(open(src))
            meta["operators"][op]["generated_at"] = m.get("generated_at")
    with open(os.path.join(args.out, "manifest-synthesis.json"), "w") as f:
        json.dump(meta, f, indent=1, ensure_ascii=False)
    log.info("listo: %s", args.out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
