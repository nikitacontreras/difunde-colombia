package web

import "embed"

//go:embed index.html map.html admin.html map.js app.css app.js admin.css admin.js sw.js manifest.webmanifest cobertura_municipios.geojson
var assets embed.FS

// Cache invalidation token: 2026-08-14 11:30
var (
	IndexHTML = mustRead("index.html")
	MapHTML   = mustRead("map.html")
	AdminHTML = mustRead("admin.html")
)

var ctypes = map[string]string{
	"app.js":                       "application/javascript; charset=utf-8",
	"map.js":                       "application/javascript; charset=utf-8",
	"admin.js":                     "application/javascript; charset=utf-8",
	"app.css":                      "text/css; charset=utf-8",
	"admin.css":                    "text/css; charset=utf-8",
	"sw.js":                        "application/javascript; charset=utf-8",
	"manifest.webmanifest":         "application/manifest+json; charset=utf-8",
	"cobertura_municipios.geojson": "application/geo+json; charset=utf-8",
}

// Asset devuelve un asset embebido y su content-type.
func Asset(name string) ([]byte, string, bool) {
	data, err := assets.ReadFile(name)
	if err != nil {
		return nil, "", false
	}
	ct, ok := ctypes[name]
	if !ok {
		ct = "application/octet-stream"
	}
	return data, ct, true
}

func mustRead(name string) []byte {
	data, err := assets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return data
}
