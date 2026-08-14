#!/usr/bin/env python3
from __future__ import annotations

import argparse
import html as html_lib
import json
import re
import ssl
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen

PUBLIC_PAGE = "https://www.claro.com.co/personas/servicios/servicios-moviles/cobertura/"
MAP_PAGE = "https://minisitiosclaro.claro.com.co/MapasDeCobertura"
JS_URL = f"{MAP_PAGE}/Scripts/Claro.com.cobertura.min.js"
API_URL = f"{MAP_PAGE}/Cobertura/getMunip_Localid"
UA = "Mozilla/5.0 (Codex Claro Coverage Scraper)"
SSL_CONTEXT = ssl._create_unverified_context()

SELECT_RE = re.compile(r'<select[^>]*id="([^"]+)"[^>]*>(.*?)</select>', re.S | re.I)
OPTION_RE = re.compile(r'<option value="([^"]*)">(.*?)</option>', re.S | re.I)
BASE_URL_RE = re.compile(r"var\s+(ulrMapGSM|ulrMapUMTS|ulrMapLTE|ulrMap5G)\s*=\s*'([^']+)'")
DATE_RE = re.compile(r"\$\(\"#FechaActualizacion\"\)\.text\('([^']+)'\)")


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


def fetch_text(url: str) -> str:
    req = Request(
        url,
        headers={
            "User-Agent": UA,
            "Accept": "*/*",
            "Referer": MAP_PAGE,
        },
    )
    with urlopen(req, context=SSL_CONTEXT, timeout=120) as resp:
        return resp.read().decode("utf-8", errors="replace")


def post_form(url: str, payload: dict[str, Any]) -> str:
    body = urlencode(payload).encode("utf-8")
    req = Request(
        url,
        data=body,
        headers={
            "User-Agent": UA,
            "Accept": "application/json, text/javascript, */*; q=0.01",
            "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
            "Referer": MAP_PAGE,
            "X-Requested-With": "XMLHttpRequest",
        },
        method="POST",
    )
    with urlopen(req, context=SSL_CONTEXT, timeout=120) as resp:
        return resp.read().decode("utf-8", errors="replace")


def parse_select_options(html_text: str, select_id: str) -> list[dict[str, str]]:
    match = re.search(rf'<select[^>]*id="{re.escape(select_id)}"[^>]*>(.*?)</select>', html_text, re.S | re.I)
    if not match:
        return []
    options_block = match.group(1)
    options: list[dict[str, str]] = []
    for value, label in OPTION_RE.findall(options_block):
        value = html_lib.unescape(value).strip()
        label = html_lib.unescape(label).strip()
        if not value:
            continue
        options.append({"value": value, "label": label})
    return options


def extract_base_urls(html_text: str) -> dict[str, str]:
    base_urls: dict[str, str] = {}
    for key, value in BASE_URL_RE.findall(html_text):
        base_urls[key] = value
    return base_urls


def extract_update_date(html_text: str) -> str:
    match = DATE_RE.search(html_text)
    return match.group(1) if match else ""


def parse_json_array(text: str) -> list[dict[str, Any]]:
    payload = json.loads(text)
    if isinstance(payload, list):
        return payload
    if isinstance(payload, dict):
        for key in ("data", "items", "result"):
            value = payload.get(key)
            if isinstance(value, list):
                return value
    return []


def fetch_municipalities(dept_id: str) -> list[dict[str, Any]]:
    return parse_json_array(post_form(API_URL, {"id": dept_id, "option": "1"}))


def fetch_localities(city_id: str) -> list[dict[str, Any]]:
    return parse_json_array(post_form(API_URL, {"id": city_id, "option": "2"}))


def scrape(out_dir: Path, force: bool, workers: int) -> dict[str, Any]:
    page_html = fetch_text(MAP_PAGE)
    js_bundle = fetch_text(JS_URL)

    write_text(out_dir / "raw" / "page.html", page_html)
    write_text(out_dir / "raw" / "scripts" / "Claro.com.cobertura.min.js", js_bundle)

    departments = parse_select_options(page_html, "lstDepartamento")
    base_urls = extract_base_urls(page_html)
    update_date = extract_update_date(page_html)

    write_json(
        out_dir / "raw" / "departments.json",
        {"source": MAP_PAGE, "items": departments},
    )
    write_json(
        out_dir / "manifest.json",
        {
            "generated_at": utc_now(),
            "source": {
                "public_page": PUBLIC_PAGE,
                "map_page": MAP_PAGE,
                "js_url": JS_URL,
                "api_url": API_URL,
            },
            "page_update": update_date,
            "base_urls": base_urls,
            "downloaded": {
                "page_html": "raw/page.html",
                "js_bundle": "raw/scripts/Claro.com.cobertura.min.js",
                "departments": "raw/departments.json",
            },
            "technologies": [
                {
                    "id": "GSM",
                    "name": "2G / GSM",
                    "tile_url_template": f"{base_urls.get('ulrMapGSM', '')}Z{{zoom}}/{{y}}/{{x}}.png",
                },
                {
                    "id": "UMTS",
                    "name": "3G / UMTS",
                    "tile_url_template": f"{base_urls.get('ulrMapUMTS', '')}Z{{zoom}}/{{y}}/{{x}}.png",
                },
                {
                    "id": "LTE",
                    "name": "4G / LTE",
                    "tile_url_template": f"{base_urls.get('ulrMapLTE', '')}Z{{zoom}}/{{y}}/{{x}}.png",
                },
                {
                    "id": "5G",
                    "name": "5G",
                    "tile_url_template": f"{base_urls.get('ulrMap5G', '')}Z{{zoom}}/{{y}}/{{x}}.png",
                },
            ],
            "municipalities": [],
            "localities": [],
        },
    )

    municipality_rows: list[dict[str, Any]] = []
    locality_rows: list[dict[str, Any]] = []

    for dept in departments:
        dept_id = dept["value"]
        dept_dir = out_dir / "raw" / "municipalities" / dept_id
        dept_dir.mkdir(parents=True, exist_ok=True)

        try:
            municipalities = fetch_municipalities(dept_id)
            municipality_rows.extend(
                {
                    "department_id": dept_id,
                    "department_name": dept["label"],
                    "municipality_id": item.get("id", ""),
                    "municipality_name": item.get("descripcion", ""),
                }
                for item in municipalities
            )
            write_json(
                dept_dir / "municipalities.json",
                {
                    "department_id": dept_id,
                    "department_name": dept["label"],
                    "count": len(municipalities),
                    "items": municipalities,
                },
            )
        except (HTTPError, URLError, OSError, json.JSONDecodeError) as exc:
            write_json(
                dept_dir / "error.json",
                {
                    "department_id": dept_id,
                    "department_name": dept["label"],
                    "error": str(exc),
                },
            )
            continue

        for muni in municipalities:
            city_id = str(muni.get("id", "")).strip()
            if not city_id:
                continue
            city_dir = out_dir / "raw" / "localities" / dept_id
            city_dir.mkdir(parents=True, exist_ok=True)
            out_path = city_dir / f"{city_id}.json"
            try:
                localities = fetch_localities(city_id)
                locality_rows.extend(
                    {
                        "department_id": dept_id,
                        "department_name": dept["label"],
                        "municipality_id": city_id,
                        "municipality_name": muni.get("descripcion", ""),
                        "locality_id": item.get("id", ""),
                        "locality_name": item.get("descripcion", ""),
                    }
                    for item in localities
                )
                write_json(
                    out_path,
                    {
                        "department_id": dept_id,
                        "department_name": dept["label"],
                        "municipality_id": city_id,
                        "municipality_name": muni.get("descripcion", ""),
                        "count": len(localities),
                        "items": localities,
                    },
                )
            except (HTTPError, URLError, OSError, json.JSONDecodeError) as exc:
                write_json(
                    out_path,
                    {
                        "department_id": dept_id,
                        "department_name": dept["label"],
                        "municipality_id": city_id,
                        "municipality_name": muni.get("descripcion", ""),
                        "error": str(exc),
                    },
                )

    manifest_path = out_dir / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    manifest["municipalities"] = municipality_rows
    manifest["localities"] = locality_rows
    manifest["summary"] = {
        "departments": len(departments),
        "municipalities": len(municipality_rows),
        "localities": len(locality_rows),
    }
    write_json(manifest_path, manifest)
    write_json(out_dir / "raw" / "municipalities.json", municipality_rows)
    write_json(out_dir / "raw" / "localities.json", locality_rows)

    return manifest


def main() -> int:
    parser = argparse.ArgumentParser(description="Scrape Claro cobertura public map tiles and municipality lists.")
    parser.add_argument("--out", default="data/claro_cobertura", help="Output directory.")
    parser.add_argument("--workers", type=int, default=6, help="Reserved for future parallelization.")
    parser.add_argument("--force", action="store_true", help="Reserved for future compatibility.")
    args = parser.parse_args()

    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)

    manifest = scrape(out_dir, force=args.force, workers=args.workers)
    print(
        "Scraped Claro coverage: "
        f"{manifest['summary']['departments']} departments, "
        f"{manifest['summary']['municipalities']} municipalities, "
        f"{manifest['summary']['localities']} localities into {out_dir}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
