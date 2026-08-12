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
- Desde este entorno, `api.sgc.gov.co` devolvio `403 Forbidden` incluso con `Origin` y `Referer`.

## Implicacion practica

- Si quieres datos frescos, este es el origen correcto para intentar primero.
- Si tu backend no puede leerlo por `403`, conviene hacer polling desde el front o usar un proxy solo si el acceso esta permitido.
- El catalogo ArcGIS puede servir como respaldo estructurado.

## Ejemplo de referencia

```bash
curl -X POST 'https://api.sgc.gov.co/api/events/search/?page=1' \
  -H 'Content-Type: application/json' \
  -d '{"local_time_after":"2026-08-07 00:00","local_time_before":"2026-08-12 23:59"}'
```

En pruebas desde este entorno esa llamada devolvio `403 Forbidden`.
