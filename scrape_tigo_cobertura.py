#!/usr/bin/env python3
from __future__ import annotations

import argparse
import csv
import json
import re
import ssl
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.request import Request, urlopen

PUBLIC_PAGE = "https://www.tigo.com.co/mapas-de-cobertura"
MAP_PAGE = "https://coberturadigital-uat-co.tigocloud.net/"
SCRIPT_URL = f"{MAP_PAGE}scripts/script.js"
DATE_URL = f"{MAP_PAGE}scripts/dateUpdate.txt"
DEPARTMENTS_URL = f"{MAP_PAGE}scripts/department.txt"
CITIES_URL = f"{MAP_PAGE}scripts/cities.txt"
ADMINS_URL = f"{MAP_PAGE}scripts/admins.txt"
UA = "Mozilla/5.0 (Codex Tigo Coverage Scraper)"
SSL_CONTEXT = ssl._create_unverified_context()

BASE_RE = re.compile(r'return\s+"([^"]+)"\s+\+\s+"Z"\s+\+\s+zoom\s+\+\s+"\/"\s+\+\s+normalizedCoord\.y\s+\+\s+"\/"\s+\+\s+normalizedCoord\.x\s+\+\s+"\.(png|PNG)"')


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


def parse_csv_text(text: str, field_names: list[str]) -> list[dict[str, str]]:
    rows: list[dict[str, str]] = []
    reader = csv.reader(text.splitlines())
    for raw_row in reader:
        if not raw_row:
            continue
        row = [item.strip() for item in raw_row]
        row[0] = row[0].lstrip("\ufeff")
        if len(row) < len(field_names):
            continue
        rows.append({field_names[idx]: row[idx] for idx in range(len(field_names))})
    return rows


def extract_base_templates(script_text: str) -> dict[str, list[str]]:
    templates: dict[str, list[str]] = {
        "3G": [],
        "4G": [],
        "5G": [],
    }

    lines = script_text.splitlines()
    for line in lines:
        line = line.strip()
        if 'return "3G/ciudades/' in line:
            templates["3G"].append("3G/ciudades/Z{zoom}/{y}/{x}.png")
        elif 'return "3G/Z' in line:
            templates["3G"].append("3G/Z{zoom}/{y}/{x}.png")
        elif 'return "4G/ciudades/' in line:
            templates["4G"].append("4G/ciudades/Z{zoom}/{y}/{x}.png")
        elif 'return "4G/Z' in line:
            templates["4G"].append("4G/Z{zoom}/{y}/{x}.png")
        elif 'return "5G/ciudades/' in line:
            templates["5G"].append("5G/ciudades/Z{zoom}/{y}/{x}.png")
        elif 'return "5G/Z' in line:
            templates["5G"].append("5G/Z{zoom}/{y}/{x}.png")

    # Keep only unique templates in their original order.
    for key, values in templates.items():
        seen: set[str] = set()
        unique: list[str] = []
        for value in values:
            if value in seen:
                continue
            unique.append(value)
            seen.add(value)
        templates[key] = unique
    return templates


def main() -> int:
    parser = argparse.ArgumentParser(description="Scrape Tigo coverage tiles and public index files.")
    parser.add_argument("--out", default="data/tigo_cobertura", help="Output directory.")
    args = parser.parse_args()

    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)

    page_html = fetch_text(MAP_PAGE)
    script_js = fetch_text(SCRIPT_URL)
    date_update = fetch_text(DATE_URL).strip()
    departments_text = fetch_text(DEPARTMENTS_URL)
    cities_text = fetch_text(CITIES_URL)
    admins_text = fetch_text(ADMINS_URL)

    write_text(out_dir / "raw" / "page.html", page_html)
    write_text(out_dir / "raw" / "scripts" / "script.js", script_js)
    write_text(out_dir / "raw" / "scripts" / "dateUpdate.txt", date_update + "\n")
    write_text(out_dir / "raw" / "scripts" / "department.txt", departments_text)
    write_text(out_dir / "raw" / "scripts" / "cities.txt", cities_text)
    write_text(out_dir / "raw" / "scripts" / "admins.txt", admins_text)

    departments = parse_csv_text(departments_text, ["department_id", "department_name", "lat", "lng"])
    cities = parse_csv_text(cities_text, ["department_id", "city_id", "city_name", "lat", "lng"])
    admins = parse_csv_text(admins_text, ["admin_id", "admin_name", "department_id", "city_id", "lat", "lng"])
    templates = extract_base_templates(script_js)

    departments_by_id = {row["department_id"]: row for row in departments}
    cities_by_department: dict[str, list[dict[str, str]]] = {}
    for city in cities:
        cities_by_department.setdefault(city["department_id"], []).append(city)
    admins_by_department: dict[str, list[dict[str, str]]] = {}
    for admin in admins:
        admins_by_department.setdefault(admin["department_id"], []).append(admin)

    write_json(out_dir / "raw" / "departments.json", departments)
    write_json(out_dir / "raw" / "cities.json", cities)
    write_json(out_dir / "raw" / "admins.json", admins)

    summary = {
        "departments": len(departments),
        "cities": len(cities),
        "admins": len(admins),
    }

    manifest = {
        "generated_at": utc_now(),
        "source": {
            "public_page": PUBLIC_PAGE,
            "map_page": MAP_PAGE,
            "script_url": SCRIPT_URL,
            "date_url": DATE_URL,
            "departments_url": DEPARTMENTS_URL,
            "cities_url": CITIES_URL,
            "admins_url": ADMINS_URL,
        },
        "page_update": date_update,
        "downloaded": {
            "page_html": "raw/page.html",
            "script_js": "raw/scripts/script.js",
            "date_update": "raw/scripts/dateUpdate.txt",
            "departments": "raw/scripts/department.txt",
            "cities": "raw/scripts/cities.txt",
            "admins": "raw/scripts/admins.txt",
        },
        "technologies": [
            {
                "id": "3G",
                "tile_url_templates": templates["3G"],
            },
            {
                "id": "4G",
                "tile_url_templates": templates["4G"],
            },
            {
                "id": "5G",
                "tile_url_templates": templates["5G"],
            },
        ],
        "departments": departments,
        "cities": cities,
        "admins": admins,
        "summary": summary,
        "notes": [
            "Tigo usa mosaicos PNG directos y archivos de texto para catalogos administrativos.",
            "La capa ciudad/ciudades es un overlay separado del mosaico general.",
        ],
    }

    write_json(out_dir / "manifest.json", manifest)
    write_json(out_dir / "raw" / "cities_by_department.json", cities_by_department)
    write_json(out_dir / "raw" / "admins_by_department.json", admins_by_department)
    write_json(out_dir / "raw" / "departments_by_id.json", departments_by_id)

    print(
        "Scraped Tigo coverage: "
        f"{summary['departments']} departments, "
        f"{summary['cities']} cities, "
        f"{summary['admins']} admin/locality rows into {out_dir}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
