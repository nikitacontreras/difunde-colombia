# Checklist — Colombia Difunde

Estado global: **funcional, probado y desplegable**. Todas las tareas marcadas
`[x]` están implementadas y verificadas. Las `[ ]` son pendientes opcionales o
mejoras futuras.

Fecha de última verificación: 2026-08-11.

---

## 1. Arquitectura y estructura del repositorio

- [x] Backend en Go (binario único `cmd/server`) con frontend embebido (`embed`).
- [x] Persistencia PostgreSQL + PostGIS con esquema por migraciones versionadas.
- [x] Capas separadas y sin dependencias circulares:
  - [x] `internal/asn` — resolución local IP → ASN (sin servicios externos).
  - [x] `internal/config` — configuración 100% por variables de entorno.
  - [x] `internal/db` — migraciones SQL embebidas, aplicadas al arrancar.
  - [x] `internal/geo` — celdas H3 con resolución configurable.
  - [x] `internal/importer` — carga batch de datasets oficiales.
  - [x] `internal/observe` — normalización de operadores y validación de payload.
  - [x] `internal/server` — API HTTP, rate limiting, seguridad, agregación.
  - [x] `internal/state` — clasificación de estado explicable (sin ML).
  - [x] `internal/store` — contrato de persistencia con impl. PostgreSQL y en memoria.
  - [x] `web` — frontend PWA (probes + reporte + mapa).
- [x] `scrape_telco_colombia.py` descarga los datasets oficiales a `data/`.
- [x] `data/` contiene la corrida real del scraping (cobertura, infraestructura, antenas Cali).

## 2. Pipeline de datos oficiales (baseline)

- [x] Scraper descarga de CRC/Postdata y Datos Abiertos Cali.
- [x] `server import-data DIR`:
  - [x] Detecta formato por nombre de archivo (`postdata*cobertura`, `*infraestructura`, `*anten*`/`*cali*`).
  - [x] Decodifica UTF-8/windows-1252, delimitador `;` o `,`, cabeceras con BOM y filas rotas.
  - [x] Cobertura: solo el último trimestre disponible (60.797 filas reales, de 761.892).
  - [x] Infraestructura: solo el último trimestre disponible (7.503 filas reales, de 136.809).
  - [x] Antenas de Cali: filtro de coordenadas válidas dentro de Colombia (975 sitios reales).
  - [x] Rechaza coordenadas fuera de rango y valores no numéricos con conteo de `skipped`.
  - [x] `TRUNCATE` al inicio para que el baseline sea un snapshot, no histórico.
  - [x] Bugs corregidos durante la verificación:
    - [x] Placeholders `$N` no contiguos en `insertMobileSitesBatch` → `SQLSTATE 42P18` (PostgreSQL 18).
    - [x] Desajuste de columnas (la geometría aterrizaba en `longitude`).
- [x] `server load-mapping CSV` carga mappings ASN → operador verificados (`upsert`).

## 3. Backend — API y lógica

### 3.1 Ingesta
- [x] `POST /o` — observación individual con formato compacto (`x,y,a,r,j,n,ok,f,q,e,br,bd,sd,c,op,k1,k4,t,u`).
- [x] `POST /sync` — lote de observaciones (≤ `MAX_SYNC_ITEMS`), tolera filas inválidas.
- [x] `POST /o/update` — seguimiento: señal de llamada (`yes/no/unknown`) y operador reportado.
- [x] Validación estricta del payload:
  - [x] lat/lon en rango, RTT/jitter ≥ 0, ratio de éxito en [0,1].
  - [x] `call_signal` y `effective_type` en listas cerradas.
  - [x] timestamp `t` no futuro ni demasiado antiguo (≤ 24 h).
  - [x] body con tamaño y contenido acotados.
- [x] Derivación por servidor: celda H3, ASN (local), operador (mappings), estimación de transferencia.

### 3.2 Probes
- [x] `GET /p` — probe pasivo (latencia del cliente).
- [x] `GET /probe/1k` y `/probe/4k` — cuerpos fijos para estimar throughput.
- [x] `Cache-Control: no-store` para que la latencia no sea servida por caché.

### 3.3 Agregación y mapa
- [x] `GET /cells` — agregación por celda H3:
  - [x] `n` muestras, `r` RTT mediana, `j` jitter mediana, `q` ratio de éxito.
  - [x] `o` operador dominante, `t` última observación, `p` sitios oficiales en la celda.
  - [x] `s` estado y `c` confianza (ver §4).
  - [x] Filtros `bbox`, `window` (15m/1h/3h), `operator`.
  - [x] Límite de área de consulta y bbox default = Colombia continental.
  - [x] ETag (304 cuando no cambia) y `Cache-Control` público acotado.
- [x] `GET /sites` — antenas oficiales dentro del bbox.
- [x] `GET /coverage` — baseline oficial de cobertura municipal (filtros `municipality`, `operator`, `technology`).
- [x] `GET /coverage/sites` — sitios oficiales por operador y municipio (agregado con `SUM` + tecnologías `OR`).

### 3.4 Recursos humanitarios
- [x] `GET /resources?kind=...` — recursos (centros de acopio, hospitales, agua, energía, etc.).
- [x] `POST /report` — alta de recursos validados (kind cerrado, coordenadas en rango).

### 3.5 Operador (ASN → operador)
- [x] Resolución local desde CSV o desde tabla `asn_operator_mapping` (con `load-mapping`).
- [x] `NormalizeOperator`: normaliza acentos/puntos/`&` y mapea solo nombres inequívocos.
- [x] `unknown` nunca se afirma: cualquier ASN sin mapping → `desconocido`.
- [x] El operador reportado por el usuario (`op` en `/o/update`) corrige asignaciones dudosas.
- [x] `mobile` es probabilístico/configurado, no una verdad absoluta.

## 4. Clasificación de estado (explicable, sin ML)

- [x] `SIN_DATOS` — muestras < `STATE_MIN_SAMPLES` (nunca se dice "sin señal" sin datos).
- [x] `OPERATIVO` — éxito alto y RTT/jitter por debajo de umbrales.
- [x] `DEGRADADO` — éxito reducido o RTT/jitter elevados.
- [x] `AFECTACION_PROBABLE` — condiciones degradadas + baseline oficial espera servicio + muestras suficientes.
- [x] Confianza `ALTA/MEDIA/BAJA` según número de muestras.
- [x] Umbrales centralizados en config (`STATE_*`), sin valores mágicos en código.
- [x] Validación con tests unitarios (límites de cada estado).

## 5. Frontend (PWA)

- [x] `index.html` + `app.js`: pide ubicación, mide probes (`/p` ×4 + `/probe/1k`/`/4k` adaptativo), envía `/o`.
- [x] `navigator.connection` como señal opcional (effectiveType, RTT, downlink, saveData).
- [x] Offline: cola en IndexedDB y reenvío con `/sync` al volver (`online`).
- [x] Seguimiento de usuario: señal de llamada y operador corroboado (`/o/update`).
- [x] `map.html`: mapa con celdas por estado (colores), filtro por operador/ventana, capa de sitios oficiales. Usa **Leaflet** (~145 KB, ~42 KB gzip) en vez de Maplibre (~1.3 MB) para mantener el peso mínimo.
- [x] Carga ultraligera: `/` sirve ~20 KB crudos / ~5 KB gzip (HTML+CSS+JS+manifest). El mapa es la única página con dependencia externa (Leaflet + tiles OSM por CDN).
- [x] Service Worker, manifest, instalable, caché offline.
- [x] Errores y estados claros para el usuario (GPS denegado, precisión baja, offline).

## 6. Seguridad y privacidad

- [x] La IP del cliente **nunca se persiste ni se loguea**; solo se usa en memoria para ASN.
- [x] `X-Forwarded-For`/`CF-Connecting-IP` solo se confían desde CIDRs en `TRUSTED_PROXIES`.
- [x] Rate limiting por IP por endpoint (`RATE_*_PER_MIN`), amplio por CGNAT.
- [x] `MAX_BODY_BYTES` y `MAX_SYNC_ITEMS` acotan las peticiones.
- [x] Headers de seguridad: `nosniff`, `no-referrer`, CSP estricta, `Service-Worker-Allowed`.
- [x] Sin dependencias de servicios externos en el hot path (ASN/IP y operador locales).
- [x] Sin scraping por petición: el baseline se carga por lote.

## 7. Persistencia (PostgreSQL/PostGIS)

- [x] Migración `0001_init.sql`: observaciones, recursos, índices espaciales y de operador.
- [x] Migración `0002_official_data.sql`: cobertura oficial, sitios oficiales, antenas, mappings.
- [x] Geometrías `geometry(Point, 4326)` con índice GiST y celdas H3 indexadas.
- [x] Agregación con `percentile_cont` (medianas) y `mode()` para operador dominante.
- [x] Inserción por lotes (`pgx.Batch`) para observaciones y baseline.

## 8. Despliegue

- [x] `Dockerfile` multi-etapa (Go 1.26 + build-base por libh3 en C; imagen final Alpine mínima).
- [x] `docker-compose.yml`: PostGIS + servidor con healthchecks y volúmenes.
- [x] `.env.example` con todas las variables y defaults.
- [x] `.gitignore` y `.dockerignore`.
- [x] README con arquitectura, endpoints, arranque local y despliegue.
- [x] Imagen Docker construida y verificada (sirvió frontend + API contra PostGIS).

## 9. Pruebas

### 9.1 Automáticas
- [x] `go test ./...` — 11 paquetes, todos verdes.
- [x] `go vet ./...` — sin hallazgos.
- [x] `gofmt` — sin diferencias.
- [x] Unitarias:
  - [x] `internal/asn` — resolución por rangos (IPv4/IPv6) y formato GeoLite2.
  - [x] `internal/geo` — celdas H3 y centros.
  - [x] `internal/observe` — normalización de operadores, validación de payload, estadísticas.
  - [x] `internal/state` — clasificación en los límites de cada estado.
  - [x] `internal/server/ip` — extracción de IP con proxies confiables.
- [x] De integración (servidor con store en memoria):
  - [x] `POST /o` 201 + `X-Obs-ID`; payloads inválidos → 400.
  - [x] `/sync` → 204 y agregación visible en `/cells` (con ETag).
  - [x] Probes `/p` (204), `/probe/1k`/`/4k` (bytes exactos).
  - [x] `/o/update` → 204.
  - [x] Página `/` servida con el botón principal.
  - [x] Rate limiting (429 tras el límite, ventana nueva resetea).
  - [x] `/coverage` y `/coverage/sites` con filtros y agregación.
  - [x] Gzip: `GET /` con `Accept-Encoding: gzip` → 200, `Content-Encoding: gzip`, `Content-Length` correcto (regresión: el middleware re-entraba al mismo gzip writer y paniqueaba; se corrigen el cableado del writer, el `WriteHeader` doble y se añade `Flush`).
- [x] Store en memoria: agregación con ventana temporal y filtro por operador; sync en lote.

### 9.2 End-to-end (PostgreSQL/PostGIS real en Docker)
- [x] Migraciones aplicadas automáticamente al arrancar.
- [x] `import-data` con los datasets reales (60.797 cobertura, 7.503 infraestructura, 975 antenas).
- [x] `POST /o` → 201; `/sync` → 204; datos visibles en `/cells`.
- [x] Transición de estado observada: `SIN_DATOS` → `OPERATIVO` al superar el umbral de muestras.
- [x] `p` (sitios oficiales en la celda) > 0 con el baseline importado.
- [x] Operador corregido por usuario (`tigo`) persistido tras `/o/update`.
- [x] `/coverage` con datos reales de Cali y `/coverage/sites` agregado por operador.
- [x] Contenedor Docker del servidor verificado contra el mismo PostGIS.

## 10. Pendientes / mejoras futuras (opcionales)

- [ ] Eliminar o marcar como legado `service.py` (MVP en Python que consulta una API externa de IP/ASN por petición; contradice el diseño actual).
- [ ] Vincular la cobertura oficial municipal a las celdas (requiere geometría de municipios DANE para cruce espacial) y usarla en `BaselineExpected`.
- [ ] Caché en memoria de `/cells` (hoy la ETag evita re-envíos, pero la consulta se repite).
- [ ] Despliegue HTTPS real (el frontend pide geolocalización; requiere contexto seguro).
- [ ] Dataset ASN local de producción (GeoLite2-ASN) y mappings ASN→operador validados.
- [ ] Agregar `go test` con la base real en CI (Testcontainers).
- [ ] E2E automatizado de `import-data` con fixtures.
- [ ] Capa de cobertura en `map.html` (pintar municipios con cobertura declarada por operador).
- [ ] Paginación o agregación por departamento en `/coverage`.

---

## Comandos útiles

```bash
# Pruebas y calidad
go test ./...
go vet ./...

# Levantar el stack completo
docker compose up -d --build

# Importar baseline y mappings
DATABASE_URL="postgres://colombia:colombia@localhost:5432/colombia?sslmode=disable" \
  go run ./cmd/server import-data ./data
DATABASE_URL="postgres://colombia:colombia@localhost:5432/colombia?sslmode=disable" \
  go run ./cmd/server load-mapping ./data/asn_operator_mapping.csv

# Servir
DATABASE_URL="postgres://colombia:colombia@localhost:5432/colombia?sslmode=disable" \
  go run ./cmd/server serve
```
