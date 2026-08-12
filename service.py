#!/usr/bin/env python3
from __future__ import annotations

import argparse
import ipaddress
import json
import os
import ssl
import time
from functools import lru_cache
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import quote, urlparse
from urllib.request import Request, urlopen

import certifi


APP_NAME = "Colombia Difunde"
APP_VERSION = "0.1.0"
DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 8000
LOOKUP_TIMEOUT_SECONDS = 8
LOOKUP_URL = "https://api.ipapi.is/?q={query}"
SSL_CONTEXT = ssl.create_default_context(cafile=certifi.where())
TRUST_PROXY_HEADERS = os.environ.get("TRUST_PROXY_HEADERS", "0") == "1"

MOBILE_KEYWORDS = {
    "4g",
    "5g",
    "cell",
    "cellular",
    "claro",
    "lte",
    "mobile",
    "movil",
    "movistar",
    "tigo",
    "telefonica",
    "telefónica",
    "une",
    "virgin",
    "wom",
    "wireless",
}


def json_response(handler: BaseHTTPRequestHandler, status: int, payload: Any) -> None:
    body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    handler.send_response(status)
    handler.send_header("Content-Type", "application/json; charset=utf-8")
    handler.send_header("Content-Length", str(len(body)))
    handler.send_header("Cache-Control", "no-store")
    handler.send_header("X-Content-Type-Options", "nosniff")
    handler.send_header("Referrer-Policy", "no-referrer")
    handler.send_header("Permissions-Policy", "geolocation=(self)")
    handler.end_headers()
    handler.wfile.write(body)


def text_response(handler: BaseHTTPRequestHandler, status: int, body: str, content_type: str = "text/html; charset=utf-8") -> None:
    raw = body.encode("utf-8")
    handler.send_response(status)
    handler.send_header("Content-Type", content_type)
    handler.send_header("Content-Length", str(len(raw)))
    handler.send_header("Cache-Control", "no-store")
    handler.send_header("X-Content-Type-Options", "nosniff")
    handler.send_header("Referrer-Policy", "no-referrer")
    handler.send_header("Permissions-Policy", "geolocation=(self)")
    handler.send_header(
        "Content-Security-Policy",
        "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "
        "script-src 'self' 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'self'",
    )
    handler.end_headers()
    handler.wfile.write(raw)


def now_ms() -> float:
    return time.perf_counter() * 1000.0


def normalize_ip(value: str) -> str:
    return value.strip().strip("[]")


def is_public_ip(value: str) -> bool:
    try:
        ip = ipaddress.ip_address(normalize_ip(value))
    except ValueError:
        return False
    return not (
        ip.is_private
        or ip.is_loopback
        or ip.is_link_local
        or ip.is_multicast
        or ip.is_reserved
        or ip.is_unspecified
    )


def pick_client_ip(headers: dict[str, str], remote_addr: str | None) -> tuple[str, str]:
    if TRUST_PROXY_HEADERS:
        forwarded_headers = [
            "cf-connecting-ip",
            "true-client-ip",
            "x-real-ip",
            "x-forwarded-for",
        ]

        for header in forwarded_headers:
            raw = headers.get(header)
            if not raw:
                continue
            candidates = [normalize_ip(part) for part in raw.split(",")]
            for candidate in candidates:
                if is_public_ip(candidate):
                    return candidate, header

    if remote_addr:
        remote_addr = normalize_ip(remote_addr)
        if remote_addr:
            return remote_addr, "client_address"

    return "unknown", "unknown"


def mobile_like(org: str | None, company: str | None, is_datacenter: bool) -> tuple[bool, str]:
    if is_datacenter:
        return False, "asn looks like datacenter infrastructure"

    text = " ".join(part for part in [org or "", company or ""] if part).lower()
    matches = [keyword for keyword in MOBILE_KEYWORDS if keyword in text]

    if matches:
        return True, f"matched keywords: {', '.join(sorted(set(matches)))}"

    if "isp" in text or "internet" in text or "telecom" in text:
        return False, "looks like an isp, but not obviously mobile"

    return False, "no mobile keywords found"


@lru_cache(maxsize=1024)
def lookup_ip(ip: str) -> dict[str, Any]:
    query = quote(ip, safe="")
    url = LOOKUP_URL.format(query=query)
    request = Request(url, headers={"User-Agent": f"{APP_NAME}/{APP_VERSION}", "Accept": "application/json"})
    with urlopen(request, context=SSL_CONTEXT, timeout=LOOKUP_TIMEOUT_SECONDS) as response:
        data = json.loads(response.read().decode("utf-8"))
    return data


def inspect_ip(ip: str) -> dict[str, Any]:
    started = now_ms()
    data = lookup_ip(ip)
    lookup_ms = round(now_ms() - started, 2)

    org = data.get("asn_org") or ""
    company = data.get("company_name") or ""
    is_datacenter = bool(data.get("is_datacenter"))
    flagged, reason = mobile_like(org, company, is_datacenter)

    return {
        "ip": data.get("ip", ip),
        "asn": {
            "number": data.get("asn_num"),
            "org": org or None,
            "company": company or None,
            "country": data.get("cc"),
        },
        "network_flags": {
            "is_datacenter": is_datacenter,
            "is_vpn": bool(data.get("is_vpn")),
            "is_proxy": bool(data.get("is_proxy")),
            "is_tor": bool(data.get("is_tor")),
            "is_abuser": bool(data.get("is_abuser")),
            "is_bogon": bool(data.get("is_bogon")),
        },
        "classification": {
            "flagged_as_mobile_like": flagged,
            "reason": reason,
        },
        "geolocation_from_ip": {
            "lat": data.get("lat"),
            "lon": data.get("lon"),
        },
        "lookup_ms": lookup_ms,
        "raw": data,
    }


def safe_float(value: Any, field: str) -> float:
    try:
        return float(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"Invalid {field}") from exc


def safe_int(value: Any, field: str) -> int:
    try:
        return int(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"Invalid {field}") from exc


def parse_json_body(handler: BaseHTTPRequestHandler) -> dict[str, Any]:
    length = safe_int(handler.headers.get("Content-Length", "0"), "content_length")
    if length > 64 * 1024:
        raise ValueError("Request too large")
    raw = handler.rfile.read(length) if length else b""
    if not raw:
        return {}
    try:
        parsed = json.loads(raw.decode("utf-8"))
    except json.JSONDecodeError as exc:
        raise ValueError("Body must be valid JSON") from exc
    if not isinstance(parsed, dict):
        raise ValueError("Body must be a JSON object")
    return parsed


def compute_latency(samples: list[float]) -> dict[str, Any]:
    values = [round(float(sample), 2) for sample in samples if isinstance(sample, (int, float))]
    values = [sample for sample in values if sample >= 0]
    if not values:
        return {"samples": [], "min": None, "median": None, "max": None, "avg": None, "grade": "unknown"}

    ordered = sorted(values)
    middle = len(ordered) // 2
    if len(ordered) % 2:
        median = ordered[middle]
    else:
        median = round((ordered[middle - 1] + ordered[middle]) / 2.0, 2)

    avg = round(sum(ordered) / len(ordered), 2)
    fastest = ordered[0]
    slowest = ordered[-1]

    if median < 150:
        grade = "excellent"
    elif median < 350:
        grade = "good"
    elif median < 800:
        grade = "slow"
    else:
        grade = "very_slow"

    return {
        "samples": ordered,
        "min": fastest,
        "median": median,
        "max": slowest,
        "avg": avg,
        "grade": grade,
    }


def build_index_html() -> str:
    return """
<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light">
  <title>Colombia Difunde</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f4f6f8;
      --panel: #ffffff;
      --text: #102133;
      --muted: #5f6b78;
      --accent: #0f766e;
      --accent-strong: #0b5f59;
      --border: #d8e1e8;
      --good: #0f766e;
      --warn: #b45309;
      --bad: #b91c1c;
      --mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      --sans: Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    * { box-sizing: border-box; }
    html, body { margin: 0; min-height: 100%; background: var(--bg); color: var(--text); font-family: var(--sans); }
    body {
      padding: 20px;
      background:
        radial-gradient(circle at top left, rgba(15,118,110,.12), transparent 26%),
        radial-gradient(circle at top right, rgba(15,23,42,.08), transparent 22%),
        linear-gradient(180deg, #f8fbfc 0%, #eef3f6 100%);
    }
    .shell { max-width: 960px; margin: 0 auto; }
    .hero {
      display: grid;
      gap: 18px;
      margin-bottom: 18px;
      padding: 22px;
      border: 1px solid rgba(16,33,51,.08);
      border-radius: 20px;
      background: rgba(255,255,255,.82);
      box-shadow: 0 18px 50px rgba(16,33,51,.08);
      backdrop-filter: blur(10px);
    }
    .eyebrow {
      font-size: 12px;
      letter-spacing: .12em;
      text-transform: uppercase;
      color: var(--muted);
      margin: 0;
    }
    h1 {
      margin: 0;
      font-size: clamp(2rem, 4vw, 3.5rem);
      line-height: 1.02;
      letter-spacing: -0.04em;
      max-width: 12ch;
    }
    .lede {
      margin: 0;
      max-width: 62ch;
      color: var(--muted);
      font-size: 1rem;
      line-height: 1.55;
    }
    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: 12px;
      align-items: center;
    }
    button {
      appearance: none;
      border: 0;
      border-radius: 999px;
      background: var(--accent);
      color: white;
      font: inherit;
      font-weight: 700;
      padding: 14px 18px;
      cursor: pointer;
      box-shadow: 0 12px 24px rgba(15,118,110,.2);
      transition: transform .15s ease, background .15s ease, opacity .15s ease;
    }
    button:hover { background: var(--accent-strong); transform: translateY(-1px); }
    button:disabled { opacity: .6; cursor: wait; transform: none; }
    .hint {
      color: var(--muted);
      font-size: .92rem;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(12, minmax(0, 1fr));
      gap: 16px;
    }
    .card {
      grid-column: span 6;
      background: rgba(255,255,255,.9);
      border: 1px solid rgba(16,33,51,.08);
      border-radius: 18px;
      padding: 18px;
      box-shadow: 0 12px 30px rgba(16,33,51,.06);
      min-height: 120px;
    }
    .card.wide { grid-column: span 12; }
    .label {
      margin: 0 0 8px;
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: .08em;
      color: var(--muted);
    }
    .value {
      margin: 0;
      font-size: 1.05rem;
      line-height: 1.5;
      word-break: break-word;
    }
    .value code, pre {
      font-family: var(--mono);
      font-size: .92rem;
    }
    pre {
      margin: 0;
      white-space: pre-wrap;
      word-break: break-word;
      line-height: 1.5;
    }
    .badge {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      border-radius: 999px;
      padding: 8px 12px;
      font-size: .9rem;
      font-weight: 700;
      width: fit-content;
    }
    .badge.good { background: rgba(15,118,110,.12); color: var(--good); }
    .badge.warn { background: rgba(180,83,9,.12); color: var(--warn); }
    .badge.bad { background: rgba(185,28,28,.12); color: var(--bad); }
    .footer {
      margin: 18px 0 4px;
      color: var(--muted);
      font-size: .9rem;
      line-height: 1.5;
    }
    .sr-only {
      position: absolute;
      width: 1px;
      height: 1px;
      padding: 0;
      margin: -1px;
      overflow: hidden;
      clip: rect(0, 0, 0, 0);
      white-space: nowrap;
      border: 0;
    }
    @media (max-width: 720px) {
      body { padding: 12px; }
      .hero { padding: 18px; }
      .card { grid-column: span 12; }
    }
  </style>
</head>
<body>
  <main class="shell">
    <section class="hero">
      <p class="eyebrow">Colombia Difunde v__VERSION__</p>
      <h1>Diagnóstico ligero de red, IP pública y ubicación</h1>
      <p class="lede">
        Esta interfaz pide ubicación al usuario, mide la latencia con pings mínimos y consulta ASN/IP
        para detectar si la conexión parece de un operador móvil o una red no residencial.
        Está pensada para funcionar con poco peso en 2G/3G.
      </p>
      <div class="actions">
        <button id="start">Ejecutar diagnóstico</button>
        <span class="hint" id="hint">No se usan frameworks ni WebRTC en el MVP.</span>
      </div>
    </section>

    <section class="grid" aria-live="polite">
      <div class="card">
        <p class="label">Estado</p>
        <p class="value" id="status">Listo para iniciar.</p>
      </div>
      <div class="card">
        <p class="label">Conexión</p>
        <p class="value" id="connection">Sin medir todavía.</p>
      </div>
      <div class="card">
        <p class="label">IP pública</p>
        <p class="value" id="ip">Pendiente.</p>
      </div>
      <div class="card">
        <p class="label">ASN / Org</p>
        <p class="value" id="asn">Pendiente.</p>
      </div>
      <div class="card wide">
        <p class="label">Latencia</p>
        <p class="value" id="latency">Pendiente.</p>
      </div>
      <div class="card wide">
        <p class="label">Ubicación</p>
        <pre id="location">Pendiente.</pre>
      </div>
      <div class="card wide">
        <p class="label">Detalles crudos</p>
        <pre id="raw">Pendiente.</pre>
      </div>
    </section>

    <p class="footer">
      La ubicación es obligatoria para el flujo del front. Si el navegador niega geolocalización, el diagnóstico se detiene.
      La detección de ASN móvil es heurística y se apoya en los metadatos públicos de IP.
    </p>
  </main>

  <script>
    const $ = (id) => document.getElementById(id);
    const startButton = $('start');

    function setStatus(message) {
      $('status').textContent = message;
    }

    function formatNetworkInfo() {
      const connection = getConnection();
      if (!connection) {
        return 'navigator.connection no disponible';
      }
      const parts = [];
      if (connection.effectiveType) parts.push(`effectiveType=${connection.effectiveType}`);
      if (typeof connection.rtt === 'number') parts.push(`rtt=${connection.rtt}ms`);
      if (typeof connection.downlink === 'number') parts.push(`downlink=${connection.downlink}Mbps`);
      if (typeof connection.saveData === 'boolean') parts.push(`saveData=${connection.saveData}`);
      return parts.length ? parts.join(' · ') : 'NetworkInformation disponible, pero sin datos útiles';
    }

    function getConnection() {
      return navigator.connection || navigator.mozConnection || navigator.webkitConnection || null;
    }

    async function getLocation() {
      if (!navigator.geolocation) {
        throw new Error('Este navegador no expone geolocalización.');
      }

      return await new Promise((resolve, reject) => {
        navigator.geolocation.getCurrentPosition(
          (position) => resolve({
            latitude: position.coords.latitude,
            longitude: position.coords.longitude,
            accuracy_m: position.coords.accuracy,
            altitude_m: position.coords.altitude,
            altitude_accuracy_m: position.coords.altitudeAccuracy,
            heading: position.coords.heading,
            speed_mps: position.coords.speed,
            timestamp: position.timestamp,
          }),
          (error) => reject(new Error(error.message || 'No se pudo obtener la ubicación.')),
          { enableHighAccuracy: false, timeout: 10000, maximumAge: 60000 }
        );
      });
    }

    async function pingOnce(index) {
      const started = performance.now();
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 8000);
      try {
        const response = await fetch(`/api/ping?i=${index}&t=${Date.now()}`, {
          method: 'GET',
          cache: 'no-store',
          signal: controller.signal,
          headers: { 'Accept': 'application/json' },
        });
        if (!response.ok) {
          throw new Error(`Ping ${index} devolvió ${response.status}`);
        }
        return performance.now() - started;
      } finally {
        clearTimeout(timeout);
      }
    }

    async function measureLatency() {
      const samples = [];
      for (let i = 0; i < 3; i += 1) {
        samples.push(await pingOnce(i + 1));
      }
      return samples;
    }

    function renderResult(result) {
      const connectionSummary = result.connection_summary || {};
      const connectionText = [
        connectionSummary.effectiveType ? `effectiveType=${connectionSummary.effectiveType}` : null,
        typeof connectionSummary.rtt === 'number' ? `rtt=${connectionSummary.rtt}ms` : null,
        typeof connectionSummary.downlink === 'number' ? `downlink=${connectionSummary.downlink}Mbps` : null,
        typeof connectionSummary.saveData === 'boolean' ? `saveData=${connectionSummary.saveData}` : null,
        connectionSummary.type ? `type=${connectionSummary.type}` : null,
      ].filter(Boolean).join(' · ') || 'Sin datos de NetworkInformation';
      $('connection').textContent = connectionText;
      $('ip').innerHTML = `<code>${escapeHtml(result.ip.value)}</code> <span class="hint">(${escapeHtml(result.ip.source)})</span>`;
      const asnNumber = result.asn && result.asn.number != null ? result.asn.number : 'n/a';
      const asnOrg = result.asn && result.asn.org != null ? result.asn.org : 'n/a';
      $('asn').innerHTML = `<code>${escapeHtml(asnNumber)}</code> · ${escapeHtml(asnOrg)}`;

      const latency = result.latency;
      const badgeClass = latency.grade === 'excellent' ? 'good' : latency.grade === 'good' ? 'good' : latency.grade === 'slow' ? 'warn' : 'bad';
      $('latency').innerHTML = `
        <div class="badge ${badgeClass}">Grado: ${latency.grade}</div>
        <div style="margin-top: 10px;">Mediana: <code>${latency.median ?? 'n/a'} ms</code> ·
        Min: <code>${latency.min ?? 'n/a'} ms</code> · Max: <code>${latency.max ?? 'n/a'} ms</code> ·
        Promedio: <code>${latency.avg ?? 'n/a'} ms</code></div>
        <div style="margin-top: 10px;">Samples: <code>${latency.samples.map((n) => n.toFixed(2)).join(', ')}</code></div>
      `;

      $('location').textContent = JSON.stringify(result.location, null, 2);
      $('raw').textContent = JSON.stringify({
        classification: result.classification,
        network_flags: result.network_flags,
        ip_lookup_ms: result.lookup_ms,
        client_ms: result.client_ms,
        server_processing_ms: result.server_processing_ms,
      }, null, 2);
    }

    function escapeHtml(value) {
      return String(value)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
    }

    async function run() {
      startButton.disabled = true;
      setStatus('Pidiendo ubicación al navegador...');
      $('hint').textContent = formatNetworkInfo();

      try {
        const location = await getLocation();
        setStatus('Ubicación obtenida. Midiendo latencia...');

        const samples = await measureLatency();
        setStatus('Consultando IP y ASN...');

        const connection = getConnection();
        const payload = {
          location,
          latency_samples_ms: samples,
          connection: {
            effectiveType: connection ? connection.effectiveType : null,
            rtt: connection ? connection.rtt : null,
            downlink: connection ? connection.downlink : null,
            saveData: connection ? connection.saveData : null,
            type: connection ? connection.type : null,
          },
          user_agent: navigator.userAgent,
        };

        const clientStarted = performance.now();
        const response = await fetch('/api/inspect', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'Accept': 'application/json',
          },
          body: JSON.stringify(payload),
        });
        const clientMs = performance.now() - clientStarted;

        if (!response.ok) {
          let message = `El servidor devolvió ${response.status}`;
          try {
            const errorBody = await response.json();
            if (errorBody?.error) message = errorBody.error;
          } catch (_) {
            /* ignore */
          }
          throw new Error(message);
        }

        const result = await response.json();
        renderResult({ ...result, client_ms: clientMs });
        setStatus('Diagnóstico completado.');
      } catch (error) {
        setStatus(error?.message || 'No se pudo completar el diagnóstico.');
      } finally {
        startButton.disabled = false;
      }
    }

    startButton.addEventListener('click', run);
  </script>
</body>
</html>
    """.replace("__VERSION__", APP_VERSION)


INDEX_HTML = build_index_html()


class AppHandler(BaseHTTPRequestHandler):
    server_version = f"{APP_NAME}/{APP_VERSION}"

    def log_message(self, format: str, *args: Any) -> None:  # noqa: A003
        if os.environ.get("COL_DIFUNDE_QUIET") == "1":
            return
        super().log_message(format, *args)

    def _headers(self) -> dict[str, str]:
        return {k.lower(): v for k, v in self.headers.items()}

    def _send_not_found(self) -> None:
        json_response(self, HTTPStatus.NOT_FOUND, {"error": "not_found"})

    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)

        if parsed.path == "/":
            text_response(self, HTTPStatus.OK, INDEX_HTML)
            return

        if parsed.path == "/api/ping":
            self.send_response(HTTPStatus.NO_CONTENT)
            self.send_header("Content-Length", "0")
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Permissions-Policy", "geolocation=(self)")
            self.end_headers()
            return

        if parsed.path == "/api/meta":
            json_response(
                self,
                HTTPStatus.OK,
                {
                    "name": APP_NAME,
                    "version": APP_VERSION,
                    "uptime_s": round(time.monotonic(), 2),
                    "endpoints": ["/", "/api/ping", "/api/inspect", "/api/meta"],
                },
            )
            return

        self._send_not_found()

    def do_POST(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        if parsed.path != "/api/inspect":
            self._send_not_found()
            return

        request_started = now_ms()
        try:
            payload = parse_json_body(self)
        except ValueError as exc:
            json_response(self, HTTPStatus.BAD_REQUEST, {"error": str(exc)})
            return

        location = payload.get("location")
        if not isinstance(location, dict):
            json_response(self, HTTPStatus.BAD_REQUEST, {"error": "location is required"})
            return

        try:
            lat = safe_float(location.get("latitude"), "latitude")
            lon = safe_float(location.get("longitude"), "longitude")
        except ValueError as exc:
            json_response(self, HTTPStatus.BAD_REQUEST, {"error": str(exc)})
            return

        accuracy = location.get("accuracy_m")
        if accuracy is not None:
            try:
                accuracy = safe_float(accuracy, "accuracy_m")
            except ValueError:
                accuracy = None

        remote_addr = self.client_address[0] if self.client_address else None
        headers = self._headers()
        client_ip, ip_source = pick_client_ip(headers, remote_addr)

        if client_ip == "unknown":
            json_response(self, HTTPStatus.BAD_REQUEST, {"error": "unable to determine client ip"})
            return

        try:
            ip_info = inspect_ip(client_ip)
        except Exception as exc:
            json_response(
                self,
                HTTPStatus.BAD_GATEWAY,
                {
                    "error": "ip lookup failed",
                    "detail": str(exc),
                    "ip": client_ip,
                },
            )
            return

        latency = compute_latency(payload.get("latency_samples_ms") or [])
        connection = payload.get("connection") if isinstance(payload.get("connection"), dict) else {}

        response = {
            "ok": True,
            "service": {
                "name": APP_NAME,
                "version": APP_VERSION,
            },
            "ip": {
                "value": ip_info["ip"],
                "source": ip_source,
            },
            "asn": ip_info["asn"],
            "network_flags": ip_info["network_flags"],
            "classification": ip_info["classification"],
            "location": {
                "latitude": lat,
                "longitude": lon,
                "accuracy_m": accuracy,
                "altitude_m": location.get("altitude_m"),
                "altitude_accuracy_m": location.get("altitude_accuracy_m"),
                "heading": location.get("heading"),
                "speed_mps": location.get("speed_mps"),
                "timestamp": location.get("timestamp"),
            },
            "connection_summary": {
                "effectiveType": connection.get("effectiveType"),
                "rtt": connection.get("rtt"),
                "downlink": connection.get("downlink"),
                "saveData": connection.get("saveData"),
                "type": connection.get("type"),
            },
            "latency": latency,
            "lookup_ms": ip_info["lookup_ms"],
            "server_processing_ms": round(now_ms() - request_started, 2),
            "server_received_at": int(time.time() * 1000),
            "raw": {
                "ipapi": {k: v for k, v in ip_info["raw"].items() if k != "raw"},
            },
        }
        json_response(self, HTTPStatus.OK, response)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Ultra-light Colombia Difunde service")
    parser.add_argument("--host", default=os.environ.get("HOST", DEFAULT_HOST), help="Bind host")
    parser.add_argument("--port", type=int, default=int(os.environ.get("PORT", DEFAULT_PORT)), help="Bind port")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    server = ThreadingHTTPServer((args.host, args.port), AppHandler)
    print(f"{APP_NAME} listening on http://{args.host}:{args.port}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
