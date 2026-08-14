# Referencia Sismos SGC

Esto resume lo que encontre en las fuentes oficiales del Servicio Geologico Colombiano para sismos casi en tiempo real.

## Fuente principal

- Visor oficial: `https://www.sgc.gov.co/sismos`
- Base usada por el visor: `https://api.sgc.gov.co/`

## Endpoints vistos en el visor

- Feed de resumen de eventos recientes:
  - `https://api.sgc.gov.co/feed/v1.0.1/summary/five_days_all.json`
  - `https://api.sgc.gov.co/feed/v1.0.1/summary/five_days_2.json`
  - `https://api.sgc.gov.co/feed/v1.0.1/summary/thirty_days_important.json`
  - `https://api.sgc.gov.co/feed/v1.0.1/summary/sixty_days_4.json`
- Busqueda paginada:
  - `POST https://api.sgc.gov.co/api/events/search/?page=1`
- Exportacion de datos:
  - `POST https://api.sgc.gov.co/api/events/download/`

## Catalogo estructurado

El SGC tambien expone un catalogo ArcGIS con campos utiles para consumir sismos de forma estructurada:

- Servicio: `https://srvags.sgc.gov.co/arcgis/rest/services/catalogo_sismos/catalogo_de_sismos_2/FeatureServer/0?f=pjson`
- Campos clave:
  - `ESP_FECHA`
  - `ESP_MAGNITUD`
  - `ESP_LATITUD`
  - `ESP_LONGITUD`
  - `ESP_PROFUNDIDAD`

## Lo importante para realtime

- El bundle del visor refresca los eventos cada 60 segundos.
- El flujo parece pensado para consumo desde el propio front del SGC.
- `api.sgc.gov.co` devuelve `403 Forbidden` incluso con `Origin` y `Referer`. Sus headers (`x-amzn-errortype: ForbiddenException`, `x-amz-apigw-id`) indican AWS API Gateway con WAF/IAM: bloqueo ineludible por HTTP simple.
- El catálogo ArcGIS (`catalogo_de_sismos_2`, capas 0 y 1) esta CONGELADO: la fecha maxima es `2020-12-30`. No sirve para eventos recientes.

## Fuente funcional (usada en colombia.difunde.co)

`apicatalogador.sgc.gov.co` es el catálogo sísmico oficial del SGC (Django/openresty) y responde sin bloqueo:

- Busqueda paginada: `POST https://apicatalogador.sgc.gov.co/api/events/search/?page=N`
- Body JSON con filtros (hora local de Colombia, UTC-5; lat/lon planos; 100 eventos por página):
  ```json
  {"local_time_after":"2026-07-29 00:00","local_time_before":"2026-08-13 23:59",
   "lat_min":4.3,"lat_max":5.5,"lon_min":-77.0,"lon_max":-75.5}
  ```
- Respuesta: `{"count":..,"next":"...?page=2","results":{"success":true,"results":[{id,status,place,closer_towns,local_time,utc_time,magnitude,mag_type,depth,latitude,longitude,event_type,...}]}}`
- Endpoint de detalle usado por el visor: `GET events/{id}/detail.json`

El backend expone el proxy en `/api/sismos?lat=..&lon=..&rad=..&days=..` (ver `handleSismosProxy`), con cache en memoria de 2 min.

## Alertas por notificación (Web Push)

El servidor hace polling del catálogo cada `SISMO_POLL_INTERVAL` (default 3 min) sobre la ventana `SISMO_WINDOW` (default 6 h). El primer ciclo solo siembra el historial en `sismo_events` sin notificar; los siguientes notifican solo eventos nuevos (dedup por `id`) con magnitud >= `SISMO_MIN_MAG` (default 0 = todos).

- `GET /api/sismos/recent` -> últimos 20 sismos detectados (de la BD local).
- `GET /api/push/vapid` -> `{"public_key":"..."}` (claves VAPID generadas una vez y persistidas en `app_settings`).
- `POST /api/push/subscribe` / `POST /api/push/unsubscribe` -> alta/baja de suscripciones (endpoint + p256dh + auth).
- Las notificaciones se envían con VAPID; suscripciones 404/410 se borran automáticamente.

## Ejemplo de referencia

```bash
curl -X POST 'https://apicatalogador.sgc.gov.co/api/events/search/?page=1' \
  -H 'Content-Type: application/json' \
  -A 'Mozilla/5.0' \
  -d '{"local_time_after":"2026-08-07 00:00","local_time_before":"2026-08-12 23:59"}'
```

Esto responde `200` con eventos del 2026 (incluido el M7.4 del 2026-08-10 en San José del Palmar).
