#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import ssl
import xml.etree.ElementTree as ET
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urljoin, urlparse
from urllib.request import Request, urlopen

API_BASE = "https://movistar-mapas-dot-modified-wonder-87620.appspot.com/api/maps"
EMBED_URL = "https://movistar-mapas-dot-modified-wonder-87620.appspot.com/mapa-cobertura"
PUBLIC_PAGE = "https://www.movistar.com.co/mapa-de-cobertura-movil"
UA = "Mozilla/5.0 (Codex Movistar Coverage Scraper)"
SSL_CONTEXT = ssl._create_unverified_context()
NS = {"kml": "http://www.opengis.net/kml/2.2"}


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def ensure_parent(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)


def write_json(path: Path, payload: Any) -> None:
    ensure_parent(path)
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True), encoding="utf-8")


def read_url(url: str, method: str = "GET", payload: dict[str, Any] | None = None) -> bytes:
    body = None
    headers = {
        "User-Agent": UA,
        "Accept": "*/*",
    }
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = Request(url, data=body, headers=headers, method=method)
    with urlopen(req, context=SSL_CONTEXT, timeout=120) as resp:
        return resp.read()


def fetch_json(url: str, method: str = "GET", payload: dict[str, Any] | None = None) -> dict[str, Any]:
    raw = read_url(url, method=method, payload=payload)
    return json.loads(raw.decode("utf-8", errors="replace"))


def fetch_bytes(url: str) -> bytes:
    return read_url(url)


def storage_relative_path(url: str) -> Path:
    parsed = urlparse(url)
    path = parsed.path.lstrip("/")
    for prefix in ("movistar-public/", "movistar-formularios/"):
        if prefix in path:
            path = path.split(prefix, 1)[1]
    return Path(path)


def local_path_for_url(out_dir: Path, url: str) -> Path:
    rel = storage_relative_path(url)
    return out_dir / "raw" / rel


def first_text(node: ET.Element, path: str) -> str:
    value = node.findtext(path, namespaces=NS)
    return value.strip() if value else ""


def download_if_needed(url: str, path: Path, force: bool) -> None:
    if path.exists() and not force:
        return
    ensure_parent(path)
    data = fetch_bytes(url)
    path.write_bytes(data)


def scrape_kml_tree(url: str, out_dir: Path, download_tiles: bool, force: bool, seen: dict[str, dict[str, Any]]) -> dict[str, Any]:
    canonical = url.split("#", 1)[0]
    if canonical in seen:
        return seen[canonical]

    node: dict[str, Any] = {"url": canonical}
    local_path = local_path_for_url(out_dir, canonical)
    node["local_path"] = str(local_path.relative_to(out_dir))

    try:
        download_if_needed(canonical, local_path, force)
        root = ET.fromstring(local_path.read_bytes())
    except (HTTPError, URLError, OSError, ET.ParseError) as exc:
        node["error"] = str(exc)
        seen[canonical] = node
        return node

    title = first_text(root, ".//kml:Document/kml:name") or first_text(root, ".//kml:Folder/kml:name")
    if title:
        node["name"] = title
    description = first_text(root, ".//kml:Document/kml:description") or first_text(root, ".//kml:Folder/kml:description")
    if description:
        node["description"] = description

    links: list[dict[str, Any]] = []
    for link in root.findall(".//kml:NetworkLink", NS):
        href = first_text(link, "kml:Link/kml:href")
        if not href:
            continue
        child_url = urljoin(canonical, href)
        links.append(scrape_kml_tree(child_url, out_dir, download_tiles, force, seen))
    if links:
        node["network_links"] = links

    overlays: list[dict[str, Any]] = []
    for overlay in root.findall(".//kml:GroundOverlay", NS):
        href = first_text(overlay, "kml:Icon/kml:href")
        bbox = {
            "north": first_text(overlay, "kml:LatLonBox/kml:north") or first_text(overlay, "kml:Region/kml:LatLonAltBox/kml:north"),
            "south": first_text(overlay, "kml:LatLonBox/kml:south") or first_text(overlay, "kml:Region/kml:LatLonAltBox/kml:south"),
            "east": first_text(overlay, "kml:LatLonBox/kml:east") or first_text(overlay, "kml:Region/kml:LatLonAltBox/kml:east"),
            "west": first_text(overlay, "kml:LatLonBox/kml:west") or first_text(overlay, "kml:Region/kml:LatLonAltBox/kml:west"),
        }
        item: dict[str, Any] = {}
        title = first_text(overlay, "kml:name")
        if title:
            item["name"] = title
        if href:
            overlay_url = urljoin(canonical, href)
            item["url"] = overlay_url
            item["href"] = href
            item["local_path"] = str(local_path_for_url(out_dir, overlay_url).relative_to(out_dir))
            if download_tiles and "undefined" not in overlay_url:
                try:
                    download_if_needed(overlay_url, out_dir / item["local_path"], force)
                except (HTTPError, URLError, OSError) as exc:
                    item["error"] = str(exc)
        if any(bbox.values()):
            item["bbox"] = bbox
        overlays.append(item)
    if overlays:
        node["ground_overlays"] = overlays
        node["ground_overlay_count"] = len(overlays)

    seen[canonical] = node
    return node


def gather_localities(tech_id: str, states_data: dict[str, Any]) -> tuple[list[dict[str, Any]], dict[str, int]]:
    localities: list[dict[str, Any]] = []
    stats = {"departments": 0, "cities": 0, "active_locality_cities": 0, "localities": 0}
    data = states_data.get("data", {})

    for department in sorted(data):
        stats["departments"] += 1
        cities = data[department]
        for city_name in sorted(cities):
            stats["cities"] += 1
            city = cities[city_name]
            entry: dict[str, Any] = {
                "department": department,
                "city": city_name,
                "id": city.get("id"),
                "idRegion": city.get("idRegion"),
                "ciudadNombre": city.get("ciudadNombre"),
                "coordenada": city.get("coordenada"),
                "localidadActiva": city.get("localidadActiva"),
                "tecnologia": city.get("tecnologia"),
                "localities": [],
            }
            if city.get("localidadActiva") == "S" and city.get("id") is not None:
                stats["active_locality_cities"] += 1
                try:
                    payload = fetch_json(
                        f"{API_BASE}/call-localities",
                        method="POST",
                        payload={"idCiudad": str(city["id"]), "tipoTecnologia": tech_id},
                    )
                    items = payload.get("data", []) or []
                    entry["localities"] = items
                    stats["localities"] += len(items)
                except (HTTPError, URLError, OSError, json.JSONDecodeError) as exc:
                    entry["locality_error"] = str(exc)
            localities.append(entry)
    return localities, stats


def scrape_technology(tech: dict[str, Any], out_dir: Path, download_tiles: bool, force: bool) -> dict[str, Any]:
    tech_id = tech.get("id_") or tech.get("id") or ""
    tech_name = tech.get("nombre") or tech.get("name") or tech_id
    if not tech_id:
        raise ValueError(f"technology entry without id: {tech!r}")

    tech_dir = out_dir / "raw" / "technologies" / tech_id
    tech_dir.mkdir(parents=True, exist_ok=True)

    call_kml = fetch_json(f"{API_BASE}/call-kml", method="POST", payload={"idTecno": tech_id})
    call_states = fetch_json(f"{API_BASE}/call-states", method="POST", payload={"idTipoTecnologia": tech_id})

    write_json(tech_dir / "call-kml.json", call_kml)
    write_json(tech_dir / "call-states.json", call_states)

    valid_kml_urls = [
        item.get("url_kml", "")
        for item in call_kml.get("data", [])
        if item.get("url_kml") and "undefined" not in item.get("url_kml", "")
    ]

    kml_seen: dict[str, dict[str, Any]] = {}
    kml_tree = [scrape_kml_tree(url, out_dir, download_tiles, force, kml_seen) for url in valid_kml_urls]
    write_json(tech_dir / "kml-manifest.json", {"roots": kml_tree, "urls": valid_kml_urls})

    localities, stats = gather_localities(tech_id, call_states)
    write_json(tech_dir / "localities.json", {"technology_id": tech_id, "technology_name": tech_name, "items": localities, "stats": stats})

    return {
        "id": tech_id,
        "name": tech_name,
        "call_kml_urls": valid_kml_urls,
        "call_kml_path": str((tech_dir / "call-kml.json").relative_to(out_dir)),
        "states_path": str((tech_dir / "call-states.json").relative_to(out_dir)),
        "localities_path": str((tech_dir / "localities.json").relative_to(out_dir)),
        "kml_manifest_path": str((tech_dir / "kml-manifest.json").relative_to(out_dir)),
        "departments": stats["departments"],
        "cities": stats["cities"],
        "active_locality_cities": stats["active_locality_cities"],
        "localities": stats["localities"],
        "download_tiles": download_tiles,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Scrape public Movistar coverage metadata and KML overlays.")
    parser.add_argument("--out", default="data/movistar_cobertura", help="Output directory.")
    parser.add_argument("--download-tiles", action="store_true", help="Download KML overlay PNG tiles too.")
    parser.add_argument("--force", action="store_true", help="Re-download files even if they already exist.")
    parser.add_argument("--tech", action="append", help="Only scrape a technology id_ (GSM, LTE, UMTS, RSSI). Can be repeated.")
    args = parser.parse_args()

    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)

    techs = fetch_json(f"{API_BASE}/get-all-technology")
    legend = fetch_json(f"{API_BASE}/call-type-technology")

    write_json(out_dir / "raw" / "api" / "get-all-technology.json", techs)
    write_json(out_dir / "raw" / "api" / "call-type-technology.json", legend)

    manifest: dict[str, Any] = {
        "generated_at": utc_now(),
        "source": {
            "public_page": PUBLIC_PAGE,
            "embed_page": EMBED_URL,
            "api_base": API_BASE,
        },
        "download_tiles": bool(args.download_tiles),
        "technologies": [],
    }

    wanted = set(args.tech or [])
    for tech in techs.get("data", []):
        tech_id = tech.get("id_") or tech.get("id")
        if wanted and tech_id not in wanted:
            continue
        summary = scrape_technology(tech, out_dir, args.download_tiles, args.force)
        summary["legend"] = [item for item in legend.get("data", []) if item.get("id_tecno") == tech_id]
        manifest["technologies"].append(summary)

    write_json(out_dir / "manifest.json", manifest)
    print(f"Scraped {len(manifest['technologies'])} technologies into {out_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
