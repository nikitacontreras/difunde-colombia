#!/usr/bin/env python3
from __future__ import annotations

import argparse
import csv
import json
import re
import ssl
from dataclasses import dataclass, asdict
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable, Optional
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

try:
    import pandas as pd
except Exception:  # pragma: no cover - optional dependency
    pd = None


UA = "Mozilla/5.0 (Codex Telco Scraper)"
SSL_CONTEXT = ssl._create_unverified_context()

POSTDATA_COVERAGE_URL = (
    "https://www.postdata.gov.co/sites/default/files/"
    "Datos_Cobertura_Movil_1T_2023-4T_2025.csv"
)
POSTDATA_INFRA_URL = "https://www.postdata.gov.co/sites/default/files/F3_RES175_1.csv"
POSTDATA_SITIOS_HTML_URL = "https://www.postdata.gov.co/mapa/sitios-de-infraestructura-movil"
POSTDATA_WMS_CAPS_URL = (
    "https://gis.postdata.gov.co/geoserver/infraestructura_movil/wms"
    "?service=WMS&request=GetCapabilities"
)
POSTDATA_WFS_CAPS_URL = (
    "https://gis.postdata.gov.co/geoserver/infraestructura_movil/wfs"
    "?service=WFS&request=GetCapabilities"
)
CKAN_PACKAGE_SHOW_URL = (
    "https://datos.cali.gov.co/api/3/action/package_show"
    "?id=registro-de-inventario-de-antenas-de-telecomunicaciones-en-cali"
)


@dataclass
class DownloadedFile:
    source_id: str
    source_type: str
    url: str
    path: str
    note: str = ""
    status: str = "ok"


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def slugify(value: str) -> str:
    value = re.sub(r"[^A-Za-z0-9._-]+", "_", value.strip())
    value = re.sub(r"_+", "_", value)
    return value.strip("._-") or "file"


def resource_filename(name: str, url: str, fmt: str = "") -> str:
    raw_name = Path(name or Path(url.split("?")[0]).name).name
    stem = Path(raw_name).stem if Path(raw_name).suffix else raw_name
    ext = Path(url.split("?")[0]).suffix.lower()
    if not ext:
        ext = {".xlsx": ".xlsx", ".csv": ".csv"}.get((fmt or "").lower(), "")
    return f"{slugify(stem)}{ext or ''}"


def ensure_parent(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)


def fetch_bytes(url: str) -> bytes:
    req = Request(url, headers={"User-Agent": UA, "Accept": "*/*"})
    with urlopen(req, context=SSL_CONTEXT, timeout=120) as resp:
        return resp.read()


def fetch_text(url: str) -> str:
    return fetch_bytes(url).decode("utf-8", errors="replace")


def fetch_json(url: str):
    return json.loads(fetch_text(url))


def write_file(path: Path, data: bytes) -> None:
    ensure_parent(path)
    path.write_bytes(data)


def download(url: str, path: Path) -> Path:
    ensure_parent(path)
    req = Request(url, headers={"User-Agent": UA, "Accept": "*/*"})
    with urlopen(req, context=SSL_CONTEXT, timeout=120) as resp, path.open("wb") as fh:
        fh.write(resp.read())
    return path


def save_text(url: str, path: Path) -> Path:
    write_file(path, fetch_text(url).encode("utf-8"))
    return path


def maybe_convert_xlsx_to_csv(xlsx_path: Path, normalized_dir: Path) -> list[Path]:
    if pd is None:
        return []

    outputs: list[Path] = []
    try:
        sheets = pd.read_excel(xlsx_path, sheet_name=None, engine="openpyxl")
    except Exception:
        return []

    for idx, (sheet_name, df) in enumerate(sheets.items(), start=1):
        safe_sheet = slugify(sheet_name)
        out_path = normalized_dir / f"{xlsx_path.stem}__sheet_{idx}__{safe_sheet}.csv"
        ensure_parent(out_path)
        df.to_csv(out_path, index=False)
        outputs.append(out_path)

    return outputs


def fetch_postdata_sources(base_dir: Path) -> list[DownloadedFile]:
    raw_dir = base_dir / "raw" / "postdata"
    normalized_dir = base_dir / "normalized"
    raw_dir.mkdir(parents=True, exist_ok=True)
    normalized_dir.mkdir(parents=True, exist_ok=True)

    downloaded: list[DownloadedFile] = []

    coverage_path = raw_dir / "crc_cobertura_movil.csv"
    download(POSTDATA_COVERAGE_URL, coverage_path)
    downloaded.append(
        DownloadedFile(
            source_id="crc_cobertura_movil",
            source_type="csv",
            url=POSTDATA_COVERAGE_URL,
            path=str(coverage_path),
            note="Cobertura movil reportada por operador/municipio/tecnologia.",
        )
    )

    infra_path = raw_dir / "crc_infraestructura_redes_acceso_movil.csv"
    download(POSTDATA_INFRA_URL, infra_path)
    downloaded.append(
        DownloadedFile(
            source_id="crc_infraestructura_redes_acceso_movil",
            source_type="csv",
            url=POSTDATA_INFRA_URL,
            path=str(infra_path),
            note="Numero de sitios por operador/municipio/tecnologia.",
        )
    )

    sitios_html = raw_dir / "postdata_sitios_mapa.html"
    save_text(POSTDATA_SITIOS_HTML_URL, sitios_html)
    downloaded.append(
        DownloadedFile(
            source_id="postdata_sitios_mapa",
            source_type="html",
            url=POSTDATA_SITIOS_HTML_URL,
            path=str(sitios_html),
            note="Pagina del mapa de sitios moviles.",
        )
    )

    wms_caps = raw_dir / "postdata_sitios_wms_capabilities.xml"
    save_text(POSTDATA_WMS_CAPS_URL, wms_caps)
    downloaded.append(
        DownloadedFile(
            source_id="postdata_sitios_wms_capabilities",
            source_type="xml",
            url=POSTDATA_WMS_CAPS_URL,
            path=str(wms_caps),
            note="Capabilities del WMS de sitios moviles.",
        )
    )

    wfs_caps = raw_dir / "postdata_sitios_wfs_capabilities.xml"
    save_text(POSTDATA_WFS_CAPS_URL, wfs_caps)
    downloaded.append(
        DownloadedFile(
            source_id="postdata_sitios_wfs_capabilities",
            source_type="xml",
            url=POSTDATA_WFS_CAPS_URL,
            path=str(wfs_caps),
            note="Capabilities del WFS de sitios moviles.",
        )
    )

    return downloaded


def fetch_cali_dataset(base_dir: Path) -> list[DownloadedFile]:
    raw_dir = base_dir / "raw" / "cali"
    normalized_dir = base_dir / "normalized" / "cali"
    raw_dir.mkdir(parents=True, exist_ok=True)
    normalized_dir.mkdir(parents=True, exist_ok=True)

    meta = fetch_json(CKAN_PACKAGE_SHOW_URL)
    result = meta["result"]
    meta_path = raw_dir / "package_show.json"
    write_file(meta_path, json.dumps(meta, ensure_ascii=False, indent=2).encode("utf-8"))

    downloaded: list[DownloadedFile] = []
    for resource in result.get("resources", []):
        url = resource.get("url")
        if not url:
            continue

        name = resource.get("name") or Path(url).name
        source_id = resource.get("id") or slugify(name)
        fmt = resource.get("format") or ""
        local_name = resource_filename(name, url, fmt)
        ext = Path(local_name).suffix.lower() or Path(url.split("?")[0]).suffix.lower() or ".bin"
        local_path = raw_dir / local_name
        download(url, local_path)

        note = resource.get("description") or ""
        downloaded.append(
            DownloadedFile(
                source_id=source_id,
                source_type=(fmt or "file").lower(),
                url=url,
                path=str(local_path),
                note=note,
            )
        )

        if ext == ".xlsx":
            csv_exports = maybe_convert_xlsx_to_csv(local_path, normalized_dir)
            for csv_path in csv_exports:
                downloaded.append(
                    DownloadedFile(
                        source_id=f"{source_id}:{csv_path.stem}",
                        source_type="csv",
                        url=url,
                        path=str(csv_path),
                        note="Converted from XLSX.",
                    )
                )

    return downloaded


def build_manifest(base_dir: Path, files: Iterable[DownloadedFile]) -> Path:
    manifest = {
        "generated_at": now_iso(),
        "notes": [
            "No public real-time on/off API for individual antennas was found.",
            "WFS GetFeature on the public GeoServer returned 401 Unauthorized in a live test.",
        ],
        "files": [asdict(item) for item in files],
    }
    manifest_path = base_dir / "manifest.json"
    write_file(manifest_path, json.dumps(manifest, ensure_ascii=False, indent=2).encode("utf-8"))
    return manifest_path


def main() -> int:
    parser = argparse.ArgumentParser(description="Download public Colombian telco sources.")
    parser.add_argument(
        "--outdir",
        default=str(Path(__file__).resolve().parent / "data"),
        help="Output directory for downloaded files.",
    )
    args = parser.parse_args()

    base_dir = Path(args.outdir).expanduser().resolve()
    base_dir.mkdir(parents=True, exist_ok=True)

    all_files: list[DownloadedFile] = []
    all_files.extend(fetch_postdata_sources(base_dir))
    all_files.extend(fetch_cali_dataset(base_dir))

    # Store a tiny source index for quick inspection.
    index_path = base_dir / "source_index.csv"
    ensure_parent(index_path)
    with index_path.open("w", newline="", encoding="utf-8") as fh:
        writer = csv.writer(fh)
        writer.writerow(["source_id", "source_type", "url", "path", "note", "status"])
        for item in all_files:
            writer.writerow([item.source_id, item.source_type, item.url, item.path, item.note, item.status])

    build_manifest(base_dir, all_files)
    print(f"Wrote {len(all_files)} files to {base_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
