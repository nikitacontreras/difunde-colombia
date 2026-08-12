# Colombia Difunde

Sistema para diagnosticar conectividad móvil en Colombia desde el navegador,
agregar observaciones por celda espacial (H3) y contrastarlas con datos
oficiales de cobertura e infraestructura. Sin servicios externos por petición:
la resolución IP→ASN y ASN→operador son locales, y el baseline se carga por
lote desde los datasets oficiales de CRC/Postdata y Cali.

## Arquitectura

- `cmd/server` — binario principal (API + frontend embebido).
  - `serve` (default): arranca la API, aplica migraciones y sirve el frontend.
  - `import-data DIR`: carga los CSV oficiales descargados en las tablas baseline.
  - `load-mapping CSV`: carga mappings ASN→operador verificados a `asn_operator_mapping`.
- `internal/server` — HTTP: validación, rate limiting, CORS/seguridad, agregación.
- `internal/observe` — resolución ASN→operador (CSV o tabla), normalización.
- `internal/asn` — resolución local IP→ASN (GeoLite2-ASN o rangos).
- `internal/geo` — celdas H3 (resolución configurable, default 8 ≈ 0.46 km).
- `internal/state` — clasificación explicable sin ML (OPERATIVO / DEGRADADO /
  AFECTACION_PROBABLE / SIN_DATOS).
- `internal/store` — persistencia PostgreSQL/PostGIS (`PGStore`) y en memoria
  (`MemStore`) para pruebas.
- `internal/importer` — importación batch de cobertura, infraestructura y
  sitios de antenas (solo el último trimestre disponible).
- `web/` — frontend: captura de ubicación, probes y reporte (PWA offline).
- `scrape_telco_colombia.py` — descarga los datasets oficiales a `data/`.
- `SGC_SISMOS_API.md` — referencia local para sismos del SGC y sus endpoints.

### Cómo se usa la IP del cliente

La IP se extrae de `RemoteAddr`, o de `X-Forwarded-For`/`CF-Connecting-IP` solo
si el origen está en `TRUSTED_PROXIES` (CIDRs). La IP solo se usa en memoria
para resolver ASN; no se persiste ni se loguea. Fuera de `TRUSTED_PROXIES`, las
cabeceras son ignoradas para no confiar en valores spoofeados.

## Requisitos

- Go ≥ 1.26
- PostgreSQL 16+ con PostGIS 3.x
- (Opcional) base local IP→ASN: `GeoLite2-ASN.csv` o `start_ip,end_ip,asn,name,isp`.
- (Opcional) mappings ASN→operador verificados (`asn,operator,mobile,confidence,source`).

## Arranque local

```bash
# 1) PostgreSQL + PostGIS (docker)
docker compose up -d db

# 2) datasets oficiales (scraper de Python)
python scrape_telco_colombia.py

# 3) importar baseline
DATABASE_URL="postgres://colombia:colombia@localhost:5432/colombia?sslmode=disable" \
  go run ./cmd/server import-data ./data

# 4) cargar mappings ASN->operador verificados (opcional)
DATABASE_URL="$DATABASE_URL" go run ./cmd/server load-mapping ./data/asn_operator_mapping.csv

# 5) servir
DATABASE_URL="postgres://colombia:colombia@localhost:5432/colombia?sslmode=disable" \
  go run ./cmd/server serve
```

O todo con Docker:

```bash
docker compose up -d --build
```

Ver `.env.example` para todas las variables. La configuración es 100% por
variables de entorno; sin ellas se usan los defaults.

## Endpoints

| Método | Ruta | Descripción |
| ------ | ---- | ----------- |
| POST | `/o` | Observación individual (compacta, validada). |
| POST | `/sync` | Lote de observaciones (≤ `MAX_SYNC_ITEMS`). |
| POST | `/o/update` | Seguimiento: señal de llamada y operador reportado. |
| GET | `/cells?bbox=LON1,LAT1,LON2,LAT2&window=15m&operator=claro` | Agregación por celda H3 con percentiles y estado. |
| GET | `/coverage?municipality=cali&operator=claro&technology=4G` | Baseline oficial de cobertura municipal (centros poblados). |
| GET | `/coverage/sites?municipality=cali` | Sitios oficiales por operador y municipio (agregados). |
| GET | `/p` | Probe pasivo (mide latencia del cliente). |
| GET | `/probe/1k`, `/probe/4k` | Cuerpos para estimar throughput. |
| GET | `/`, `/map` | Frontend. |

El agregado por celda incluye: `n` (muestras), `r` (RTT mediana), `j` (jitter
mediana), `q` (ratio de éxito), `o` (operador dominante), `s` (estado), `c`
(confianza), `t` (última observación), `p` (sitios oficiales en la celda).

## Clasificación de estado

Sin ML, con umbrales centralizados (`STATE_*`):

- `SIN_DATOS`: muestras < `STATE_MIN_SAMPLES`.
- `OPERATIVO`: ratio de éxito alto y RTT/jitter por debajo de los umbrales.
- `DEGRADADO`: éxito reducido o RTT/jitter elevados.
- `AFECTACION_PROBABLE`: condiciones degradadas + baseline oficial espera
  servicio en la zona (sitios de infraestructura presentes) + muestras suficientes.

Las observaciones reportadas con señal de llamada y operador del usuario se
usan para corregir asignaciones dudosas de operador (`/o/update`).

## Seguridad y límites

- Rate limiting por IP por endpoint (`RATE_*_PER_MIN`), amplio por CGNAT.
- `MAX_BODY_BYTES` y `MAX_SYNC_ITEMS` limitan las peticiones.
- Headers de seguridad (`nosniff`, `no-referrer`), ETag en agregados.
- Los agregados se cachean en memoria (TTL configurable) para no golpear la DB.
- La IP nunca se persiste; solo se conservan metadatos de red (ASN/operador).

## Pruebas

```bash
go test ./...
go vet ./...
```

## Notas de datos

- La cobertura y la infraestructura oficiales (CRC/Postdata) se importan por
  lote y solo se conserva el último trimestre disponible, para que el baseline
  sea un snapshot vigente y no un histórico de cientos de miles de filas.
- El inventario de antenas de Cali es puntual (coordenadas).
- La detección de "móvil" por ASN es probabilística/configurada, no una
  verdad absoluta: un mismo ASN puede servir infraestructura fija y móvil.
- `Geolocation` requiere contexto seguro; en `localhost` funciona, en despliegue
  real conviene HTTPS (el frontend es PWA e instala offline).
- El visor de sismos del SGC refresca cada 60 segundos, asi que el objetivo
  realista es polling casi en tiempo real, no websocket abierto.
