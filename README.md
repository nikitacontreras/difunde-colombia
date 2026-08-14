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
- `scrape_movistar_cobertura.py` — extrae el mapa público de Movistar
  (tecnologías, departamentos, municipios, localidades y KML).
- `scrape_claro_cobertura.py` — extrae el mapa público de Claro
  (mosaicos PNG por tecnología + municipios/localidades).
- `scrape_tigo_cobertura.py` — extrae el mapa público de Tigo
  (mosaicos PNG + departamentos/ciudades/localidades).
- `scrape_wom_cobertura.py` — extrae el mapa público de WOM
  (WMTS GeoServer + CSV si está disponible).
- `SGC_SISMOS_API.md` — referencia local para sismos del SGC y sus endpoints.

### Cómo se usa la IP del cliente

La IP se extrae de `RemoteAddr`, o de `X-Forwarded-For`/`CF-Connecting-IP` solo
si el origen está en `TRUSTED_PROXIES` (CIDRs). La IP se usa para resolver
ASN/operador, rate limit y se persiste en `observations.client_ip`. Fuera de
`TRUSTED_PROXIES`, las cabeceras son ignoradas para no confiar en valores
spoofeados.

> Detrás de Docker (proxy del host → contenedor), el peer que ve el contenedor
> es el gateway del bridge (p. ej. `172.26.0.1`). Para que la IP real llegue a
> `client_ip`, incluye el rango del bridge en `TRUSTED_PROXIES` (en
> `docker-compose.yml` ya se confía `172.16.0.0/12`, que cubre todos los
> subnets por defecto de Docker).

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

# 2b) cobertura pública de Movistar (metadata + KML)
python scrape_movistar_cobertura.py --out data/movistar_cobertura

# 2c) cobertura pública de Claro (tiles PNG + árbol administrativo)
python scrape_claro_cobertura.py --out data/claro_cobertura

# 2d) cobertura pública de Tigo (tiles PNG + catálogos administrativos)
python scrape_tigo_cobertura.py --out data/tigo_cobertura

# 2e) cobertura pública de WOM (WMTS + CSV si responde)
python scrape_wom_cobertura.py --out data/wom_cobertura

# opcional: también descarga los PNG de los overlays del KML
python scrape_movistar_cobertura.py --out data/movistar_cobertura --download-tiles

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
| GET | `/coverage/providers` | Catálogo normalizado de capas públicas por operador y tecnología. |
| GET | `/coverage/overlays?provider=movistar&technology=LTE&bbox=LON1,LAT1,LON2,LAT2` | Overlays KML visibles de Movistar, filtrados por viewport. |
| GET | `/resources?kind=logistica` | Recursos aprobados; admite alcance puntual o por ciudad. |
| POST | `/report` | Publica un recurso pendiente (`location_scope=point|city`). |
| GET | `/admin` | Centro de operaciones oculto; requiere `X-Admin-Key`. |
| GET | `/admin/api/observations` | Histórico paginado y filtrable para administración. |
| GET/POST | `/admin/api/resources` | Lista completa y alta rápida de recursos. |
| PUT | `/admin/api/resources/{id}` | Edición integral de un recurso. |
| GET | `/p` | Probe pasivo (mide latencia del cliente). |
| GET | `/probe/1k`, `/probe/4k` | Cuerpos para estimar throughput. |
| GET | `/`, `/map` | Frontend. |

El agregado por celda incluye: `n` (muestras), `r` (RTT mediana), `j` (jitter
mediana), `q` (ratio de éxito), `o` (operador dominante), `s` (estado), `c`
(confianza), `t` (última observación), `p` (sitios oficiales en la celda).

### Acceso administrativo

Define `ADMIN_KEY` y configura el proxy o una extensión del navegador para
añadir `X-Admin-Key` a **todas** las peticiones del panel, incluidos
`/admin.css`, `/admin.js` y `/admin/api/*`. No hay formulario de login ni
cookie admin: si el header falta o es incorrecto, esas rutas responden `404`.

Los ofrecimientos sin punto fijo usan `location_scope: "city"` junto con
`municipality` y, opcionalmente, `department`; su geometría queda nula. Los
recursos con `location_scope: "point"` conservan `lat` y `lon` y aparecen como
marcadores en el mapa.

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
- Panel administrativo oculto con comparación constante de `X-Admin-Key` y
  respuestas `404` para accesos inválidos.
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
- El mapa público de Movistar se scrapea aparte porque expone un KML nacional
  por tecnología, más el árbol de departamentos/municipios/localidades vía
  API. El scraper deja todo el rastro en `data/movistar_cobertura/`.
- Claro no publica KML: su mapa usa `ImageMapType` con mosaicos PNG directos y
  el árbol administrativo sale por un endpoint JSON del mapa embebido. El
  scraper deja el rastro en `data/claro_cobertura/`.
- Tigo tampoco publica KML en su mapa accesible: usa mosaicos PNG directos y
  catálogos de departamentos/ciudades/localidades en texto plano. El scraper
  deja el rastro en `data/tigo_cobertura/`.
- WOM tampoco publica KML: el mapa usa WMTS de GeoServer y referencia un CSV
  de cobertura que puede no estar disponible de forma pública permanente. El
  scraper deja el rastro en `data/wom_cobertura/`.
- El visor ya normaliza esos manifests en `/coverage/providers`. Movistar se
  pinta como `imageOverlay` por viewport; Claro y Tigo quedan como mosaicos XYZ
  directos; WOM queda catalogado como WMTS hasta que añadamos un adaptador.
- El inventario de antenas de Cali es puntual (coordenadas).
- La detección de "móvil" por ASN es probabilística/configurada, no una
  verdad absoluta: un mismo ASN puede servir infraestructura fija y móvil.
- `Geolocation` requiere contexto seguro; en `localhost` funciona, en despliegue
  real conviene HTTPS (el frontend es PWA e instala offline).
- El visor de sismos del SGC refresca cada 60 segundos, asi que el objetivo
  realista es polling casi en tiempo real, no websocket abierto.
