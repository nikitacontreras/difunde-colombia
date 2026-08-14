#!/usr/bin/env python3
"""Genera web/cobertura_municipios.geojson (simplificado) con dane_code por
municipio, para consultas de cobertura por municipio desde el mapa sin cargar
el GeoJSON completo de 10MB en el cliente."""
import csv
import json
import os
import re
import sys
import unicodedata

import geopandas as gpd

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LIMITS = os.path.join(ROOT, "data", "limits", "colombia_adm2.geojson")
OFFICIAL = os.path.join(ROOT, "data", "raw", "postdata", "crc_cobertura_movil.csv")
OUT = os.path.join(ROOT, "web", "cobertura_municipios.geojson")
TOLERANCE = float(sys.argv[1]) if len(sys.argv) > 1 else 0.01


def norm_name(s):
    if not s:
        return ""
    s = unicodedata.normalize("NFD", str(s))
    s = "".join(c for c in s if unicodedata.category(c) != "Mn")
    s = re.sub(r"[^A-Z0-9]+", " ", s.upper()).strip()
    return s


def load_codes(path):
    codes = {}
    with open(path, encoding="utf-8", errors="replace") as f:
        for row in csv.reader(f, delimiter=";"):
            if len(row) < 7 or row[0] == "\ufeffANNO" or not row[0].strip().isdigit():
                continue
            dept, muni, dane = row[3], row[5], row[4]
            if not muni or not dane:
                continue
            codes.setdefault(norm_name(muni), (dane, dept, muni))
    return codes


def main():
    codes = load_codes(OFFICIAL)
    g = gpd.read_file(LIMITS)
    g = g[["shapeName", "geometry"]].copy()
    g.columns = ["name", "geometry"]
    g = g[g.geometry.is_valid].to_crs(epsg=3857)
    g["geometry"] = g.geometry.simplify(TOLERANCE * 111320.0, preserve_topology=True)
    g = g[~g.geometry.is_empty]
    g = g.to_crs(epsg=4326)

    features = []
    for name, geom, dane, dept, muni in zip(
        g["name"],
        g["geometry"],
        g["name"].map(lambda n: (codes.get(norm_name(n), (None, None, None)))[0]),
        g["name"].map(lambda n: (codes.get(norm_name(n), (None, None, None)))[1]),
        g["name"].map(lambda n: (codes.get(norm_name(n), (None, None, n)))[2]),
    ):
        if dane is None or not str(dane).strip().isdigit():
            continue
        features.append({
            "type": "Feature",
            "properties": {
                "dane": str(dane),
                "municipio": muni or name,
                "departamento": dept or "",
            },
            "geometry": geom.__geo_interface__,
        })

    with open(OUT, "w", encoding="utf-8") as f:
        json.dump({"type": "FeatureCollection", "features": features}, f, ensure_ascii=False, allow_nan=False)
    size = os.path.getsize(OUT) / 1e6
    print(f"{OUT}: {len(features)} municipios, {size:.2f} MB")


if __name__ == "__main__":
    main()
