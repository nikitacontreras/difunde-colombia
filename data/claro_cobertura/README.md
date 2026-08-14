# Claro cobertura

Este directorio guarda la extracción pública del mapa de cobertura de Claro
Colombia.

Fuentes:
- [Página pública](https://www.claro.com.co/personas/servicios/servicios-moviles/cobertura/)
- [Mapa embebido](https://minisitiosclaro.claro.com.co/MapasDeCobertura)
- [Bundle JS del mapa](https://minisitiosclaro.claro.com.co/MapasDeCobertura/Scripts/Claro.com.cobertura.min.js)

Lo importante para el aplicativo:
- Claro no publica un KML como Movistar.
- La capa del mapa se sirve como mosaicos PNG con URL base por tecnología:
  - GSM: `https://minisitiosclaro.claro.com.co/AdminCobertura/mapImages/cob280726gsm/`
  - 3G: `https://minisitiosclaro.claro.com.co/AdminCobertura/mapImages/cob280726umts/`
  - 4G: `https://minisitiosclaro.claro.com.co/AdminCobertura/mapImages/cob280726lte/`
  - 5G: `https://minisitiosclaro.claro.com.co/AdminCobertura/mapImages/5G3Q2026/`
- El formato de tile es `Z{zoom}/{y}/{x}.png`.

Ejecución:
```bash
python scrape_claro_cobertura.py --out data/claro_cobertura
```

Resultado:
- `manifest.json` con las rutas base y los catálogos descargados.
- `raw/page.html` y `raw/scripts/Claro.com.cobertura.min.js` como referencia.
- `raw/departments.json`, `raw/municipalities/`, `raw/localities/` con el árbol administrativo.
