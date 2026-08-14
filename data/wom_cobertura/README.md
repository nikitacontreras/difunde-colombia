# WOM cobertura

Este directorio guarda la extracción pública del mapa de cobertura de WOM
Colombia.

Fuentes:
- [Página pública](https://movilpt.co/cobertura)
- [WMTS / GeoServer](https://mi.movilpt.co/geoserverwom/gwc/service/wmts)

## Qué expone WOM

WOM no publica un KML como Movistar. El mapa usa WMTS de GeoServer con capas
PNG por tecnología:
- `cobertura3G:3G`
- `cobertura:4G`
- `cobertura5G:5G`

Además, el WMTS expone capas regionales auxiliares para 4G y una capa de apoyo
de área urbana censal. El scraper deja todas esas capas documentadas en
`raw/wmts_layers.json`.

La página también referencia un CSV de cobertura en:
- `https://movilpt.co/media/coveragemap/Coveragemap.csv`

En la corrida actual ese CSV no respondió con contenido público, así que el
scraper guarda el error y sigue con WMTS. Si WOM lo vuelve a publicar, el mismo
script lo capturará automáticamente.

## Qué deja el scraper

- `manifest.json`: resumen general, URLs base y capas detectadas.
- `raw/page.html`: HTML público de la página.
- `raw/wmts_getcapabilities.xml`: capacidades WMTS.
- `raw/wmts_layers.json`: capas y tile matrix sets normalizados.
- `raw/coverage.csv.json` o `raw/coverage.csv.error.json`: snapshot del CSV o
  el error de acceso.

## Uso

```bash
python scrape_wom_cobertura.py --out data/wom_cobertura
```

## Cómo usarlo en el aplicativo

- Si tu visor soporta WMTS, usa directamente el `wmts_url` del `manifest.json`.
- Si necesitas más control, usa las `ResourceURL` de `raw/wmts_layers.json`.
- Para un frontend ligero, lo mejor es cachear el WMTS en backend y no pegarle
  al proveedor en cada visita.

## Notas

- La actualización visible en la página es `Agosto 14 de 2026`.
- El CSV de cobertura no está garantizado como público permanente.
- WOM sí expone el mapa como servicio geoespacial reutilizable, pero no como
  KML listo para `KmlLayer`.
