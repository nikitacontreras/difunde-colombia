# Tigo cobertura

Este directorio guarda la extracción pública del mapa de cobertura de Tigo
Colombia.

Fuentes:
- [Página pública](https://www.tigo.com.co/mapas-de-cobertura)
- [Mapa público accesible](https://coberturadigital-uat-co.tigocloud.net/)

Lo importante para el aplicativo:
- Tigo no publica un KML directo en este mapa.
- La capa se sirve como mosaicos PNG con rutas relativas:
  - `3G/Z{zoom}/{y}/{x}.png`
  - `3G/ciudades/Z{zoom}/{y}/{x}.png`
  - `4G/Z{zoom}/{y}/{x}.png`
  - `4G/ciudades/Z{zoom}/{y}/{x}.png`
  - `5G/Z{zoom}/{y}/{x}.png`
  - `5G/ciudades/Z{zoom}/{y}/{x}.png`
- Los catálogos administrativos viven en `scripts/department.txt`,
  `scripts/cities.txt`, `scripts/admins.txt` y `scripts/dateUpdate.txt`.

Ejecución:
```bash
python scrape_tigo_cobertura.py --out data/tigo_cobertura
```

Resultado:
- `manifest.json` con las rutas base y los catálogos descargados.
- `raw/page.html` y `raw/scripts/script.js` como referencia.
- `raw/departments.json`, `raw/cities.json`, `raw/admins.json` con los datos
  normalizados.
