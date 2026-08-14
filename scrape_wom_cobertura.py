#!/usr/bin/env python3
from __future__ import annotations

import argparse
import csv
import html as html_lib
import json
import re
import ssl
import xml.etree.ElementTree as ET
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

PUBLIC_PAGE = "https://movilpt.co/cobertura"
CSV_URL = "https://movilpt.co/media/coveragemap/Coveragemap.csv"
UA = "Mozilla/5.0 (Codex WOM Coverage Scraper)"
SSL_CONTEXT = ssl._create_unverified_context()
NS_WMTS = {"wmts": "http://www.opengis.net/wmts/1.0", "ows": "http://www.opengis.net/ows/1.1"}


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def ensure_parent(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)


def write_json(path: Path, payload: Any) -> None:
    ensure_parent(path)
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True), encoding="utf-8")


def write_text(path: Path, payload: str) -> None:
    ensure_parent(path)
    path.write_text(payload, encoding="utf-8")


def fetch_bytes(url: str) -> bytes:
    req = Request(
        url,
        headers={
            "User-Agent": UA,
            "Accept": "*/*",
            "Referer": PUBLIC_PAGE,
        },
    )
    with urlopen(req, context=SSL_CONTEXT, timeout=120) as resp:
        return resp.read()


def fetch_text(url: str) -> str:
    return fetch_bytes(url).decode("utf-8", errors="replace")


def extract_update_date(html_text: str) -> str:
    match = re.search(r"Actualización\s+([A-Za-zÁÉÍÓÚáéíóúñÑ]+\s+\d+\s+de\s+\d{4})", html_text)
    if match:
        return match.group(1).strip()
    match = re.search(r"Actualizaci[oó]n\s+([A-Za-zÁÉÍÓÚáéíóúñÑ]+\s+\d+\s+de\s+\d{4})", html_text)
    return match.group(1).strip() if match else ""


def extract_wmts_url(html_text: str) -> str:
    match = re.search(r'id="urlconfig"[^>]*value="([^"]+)"', html_text, re.I)
    return html_lib.unescape(match.group(1)).strip() if match else ""


def parse_wmts_capabilities(xml_text: str) -> dict[str, Any]:
    root = ET.fromstring(xml_text.encode("utf-8") if isinstance(xml_text, str) else xml_text)

    def text(node: ET.Element | None, path: str) -> str:
        if node is None:
            return ""
        value = node.findtext(path, namespaces=NS_WMTS)
        return value.strip() if value else ""

    layers: list[dict[str, Any]] = []
    for layer in root.findall(".//wmts:Contents/wmts:Layer", NS_WMTS):
        identifier = text(layer, "ows:Identifier")
        title = text(layer, "ows:Title")
        abstract = text(layer, "ows:Abstract")
        bbox = layer.find("ows:WGS84BoundingBox", NS_WMTS)
        formats = [item.text.strip() for item in layer.findall("wmts:Format", NS_WMTS) if item.text and item.text.strip()]
        styles = []
        for style in layer.findall("wmts:Style", NS_WMTS):
            styles.append(
                {
                    "identifier": text(style, "ows:Identifier"),
                    "is_default": style.attrib.get("isDefault", "").lower() == "true",
                }
            )
        tile_matrix_sets = [
            text(link, "wmts:TileMatrixSet")
            for link in layer.findall("wmts:TileMatrixSetLink", NS_WMTS)
            if text(link, "wmts:TileMatrixSet")
        ]
        resource_urls = []
        for resource in layer.findall("wmts:ResourceURL", NS_WMTS):
            resource_urls.append(
                {
                    "format": resource.attrib.get("format", ""),
                    "resource_type": resource.attrib.get("resourceType", ""),
                    "template": resource.attrib.get("template", ""),
                }
            )
        layers.append(
            {
                "identifier": identifier,
                "title": title,
                "abstract": abstract,
                "bbox": {
                    "lower": text(bbox, "ows:LowerCorner"),
                    "upper": text(bbox, "ows:UpperCorner"),
                }
                if bbox is not None
                else {},
                "formats": formats,
                "styles": styles,
                "tile_matrix_sets": tile_matrix_sets,
                "resource_urls": resource_urls,
            }
        )

    matrix_sets: list[dict[str, Any]] = []
    for matrix_set in root.findall(".//wmts:Contents/wmts:TileMatrixSet", NS_WMTS):
        identifier = text(matrix_set, "ows:Identifier")
        supported_crs = text(matrix_set, "ows:SupportedCRS")
        matrices = matrix_set.findall("wmts:TileMatrix", NS_WMTS)
        matrix_sets.append(
            {
                "identifier": identifier,
                "supported_crs": supported_crs,
                "tile_matrices": len(matrices),
            }
        )

    return {
        "layers": layers,
        "matrix_sets": matrix_sets,
    }


def fetch_csv_snapshot() -> dict[str, Any]:
    try:
        raw = fetch_bytes(CSV_URL)
    except (HTTPError, URLError, OSError) as exc:
        return {
            "url": CSV_URL,
            "status": "error",
            "error": str(exc),
        }

    text = raw.decode("latin-1", errors="replace")
    rows: list[dict[str, str]] = []
    try:
        reader = csv.DictReader(text.splitlines())
        for row in reader:
            if not row:
                continue
            cleaned = {key.strip(): (value or "").strip() for key, value in row.items() if key}
            if cleaned:
                rows.append(cleaned)
    except csv.Error as exc:
        return {
            "url": CSV_URL,
            "status": "error",
            "error": f"CSV parse error: {exc}",
        }

    return {
        "url": CSV_URL,
        "status": "ok",
        "rows": rows,
        "row_count": len(rows),
        "columns": list(rows[0].keys()) if rows else [],
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Scrape WOM coverage page, CSV snapshot and WMTS capabilities.")
    parser.add_argument("--out", default="data/wom_cobertura", help="Output directory.")
    args = parser.parse_args()

    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)

    page_html = fetch_text(PUBLIC_PAGE)
    wmts_url = extract_wmts_url(page_html)
    update_date = extract_update_date(page_html)
    wmts_caps_url = f"{wmts_url}?REQUEST=GetCapabilities&SERVICE=WMTS" if wmts_url else ""

    write_text(out_dir / "raw" / "page.html", page_html)

    manifest: dict[str, Any] = {
        "generated_at": utc_now(),
        "source": {
            "public_page": PUBLIC_PAGE,
            "wmts_url": wmts_url,
            "csv_url": CSV_URL,
        },
        "page_update": update_date,
        "downloaded": {
            "page_html": "raw/page.html",
        },
        "coverage_csv": None,
        "wmts": None,
        "layers": [],
        "notes": [
            "WOM publica cobertura por WMTS/GeoServer, no como KML.",
            "El CSV de cobertura que referencia la página puede cambiar o estar caído; si no responde, el scraper deja el error guardado y sigue con WMTS.",
        ],
    }

    csv_snapshot = fetch_csv_snapshot()
    if csv_snapshot.get("status") == "ok":
        write_json(out_dir / "raw" / "coverage.csv.json", csv_snapshot)
    else:
        write_json(out_dir / "raw" / "coverage.csv.error.json", csv_snapshot)
    manifest["coverage_csv"] = {
        "url": csv_snapshot["url"],
        "status": csv_snapshot["status"],
        "row_count": csv_snapshot.get("row_count", 0),
        "columns": csv_snapshot.get("columns", []),
        "path": "raw/coverage.csv.json" if csv_snapshot.get("status") == "ok" else "raw/coverage.csv.error.json",
        "error": csv_snapshot.get("error", ""),
    }

    if wmts_caps_url:
        wmts_xml = fetch_text(wmts_caps_url)
        write_text(out_dir / "raw" / "wmts_getcapabilities.xml", wmts_xml)
        parsed = parse_wmts_capabilities(wmts_xml)
        write_json(out_dir / "raw" / "wmts_layers.json", parsed)

        coverage_layers = [
            layer
            for layer in parsed["layers"]
            if layer["identifier"].startswith("cobertura") or layer["identifier"].startswith("cobertu:")
        ]
        helper_layers = [
            layer for layer in parsed["layers"] if layer["identifier"].startswith("otros:")
        ]
        manifest["wmts"] = {
            "url": wmts_url,
            "capabilities_url": wmts_caps_url,
            "layers_path": "raw/wmts_layers.json",
            "capabilities_path": "raw/wmts_getcapabilities.xml",
        }
        manifest["layers"] = coverage_layers
        manifest["summary"] = {
            "coverage_layers": len(coverage_layers),
            "helper_layers": len(helper_layers),
            "tile_matrix_sets": len(parsed["matrix_sets"]),
        }
    else:
        manifest["wmts"] = {
            "url": "",
            "capabilities_url": "",
            "layers_path": "",
            "capabilities_path": "",
        }
        manifest["summary"] = {
            "coverage_layers": 0,
            "helper_layers": 0,
            "tile_matrix_sets": 0,
        }

    write_json(out_dir / "manifest.json", manifest)

    print(
        "Scraped WOM coverage: "
        f"{manifest['summary']['coverage_layers']} coverage layers, "
        f"{manifest['summary']['tile_matrix_sets']} tile matrix sets into {out_dir}"
    )
    if manifest["coverage_csv"]["status"] != "ok":
        print(f"CSV snapshot unavailable: {manifest['coverage_csv'].get('error', 'unknown error')}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
