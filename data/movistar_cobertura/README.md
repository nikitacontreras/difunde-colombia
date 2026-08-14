# Movistar cobertura

Este directorio guarda la extracción pública del mapa de cobertura de Movistar
para Colombia.

Fuentes:
- [Página pública](https://www.movistar.com.co/mapa-de-cobertura-movil)
- [Mapa embebido](https://movistar-mapas-dot-modified-wonder-87620.appspot.com/mapa-cobertura)
- [API de mapas](https://movistar-mapas-dot-modified-wonder-87620.appspot.com/api/maps)

## Qué expone Movistar

Movistar no publica una tabla limpia por municipio como tal. La cobertura
nacional sale por tecnología como KML y el mapa embebido usa capas geográficas
anidadas:
- `call-kml` devuelve la URL del KML nacional por tecnología.
- `doc.kml` normalmente apunta a `predictions.kml`.
- `predictions.kml` contiene muchos `GroundOverlay` con imágenes PNG por tile.
- `call-states` devuelve departamentos y ciudades.
- `call-localities` devuelve las localidades activas por ciudad.

Eso significa que:
- Sí puedes usar la URL KML directamente en el aplicativo si tu visor soporta
  KML, por ejemplo Google Maps `KmlLayer`.
- Para un servicio propio suele ser mejor cachear los KML y las imágenes, porque
  la cobertura se sirve como overlays y no como geometrías simples.

## Qué deja el scraper

- `manifest.json`: resumen general por tecnología.
- `raw/api/get-all-technology.json`: catálogo de tecnologías.
- `raw/api/call-type-technology.json`: leyenda de niveles por tecnología.
- `raw/technologies/<TEC>/call-kml.json`: URLs del KML nacional por tecnología.
- `raw/technologies/<TEC>/call-states.json`: departamentos y municipios.
- `raw/technologies/<TEC>/localities.json`: localidades por ciudad.
- `raw/technologies/<TEC>/kml-manifest.json`: árbol KML y overlays.
- `raw/mapas-cobertura/...`: copias locales de los KML y, si se activa la
  opción, de los PNG.

## Estado de la extracción

Última corrida registrada en `manifest.json`:
- GSM: 33 departamentos, 1274 ciudades, 17 ciudades con localidades activas,
  140 localidades.
- LTE: 33 departamentos, 1274 ciudades, 17 ciudades con localidades activas,
  140 localidades.
- RSSI: 33 departamentos, 1273 ciudades, 17 ciudades con localidades activas,
  110 localidades.
- UMTS: 33 departamentos, 1274 ciudades, 17 ciudades con localidades activas,
  140 localidades.

## Uso

```bash
python scrape_movistar_cobertura.py --out data/movistar_cobertura
```

Si además quieres bajar los PNG que cuelgan de cada `GroundOverlay` del KML:

```bash
python scrape_movistar_cobertura.py --out data/movistar_cobertura --download-tiles
```

## Cómo usarlo en el aplicativo

- Si el frontend soporta KML, apunta al `url_kml` que sale en
  `raw/technologies/<TEC>/call-kml.json`.
- Si quieres controlar el render, usa los KML como fuente y procesa los
  `GroundOverlay`/tiles localmente.
- Si necesitas un mapa estable y rápido, sirve desde tu backend los archivos ya
  cacheados en `data/movistar_cobertura/` en vez de depender del proveedor en
  cada visita.

## Notas

- El mapa público de Movistar es útil como referencia oficial, pero no es un
  feed realtime.
- Los `url_kml` cambian con el tiempo, por eso conviene leerlos desde el
  scraper o desde `manifest.json` y no hardcodearlos.
- Para obtener una tabla municipal realmente usable en cruces espaciales, el
  siguiente paso es combinar estos overlays con los límites administrativos de
  Colombia.
