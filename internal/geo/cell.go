// Package geo resuelve la celda espacial (H3) de una observación.
// Está detrás de una pequeña abstracción para poder cambiarlo sin
// tocar el resto del sistema.
package geo

import (
	"fmt"

	"github.com/uber/h3-go/v4"
)

type CellResolver interface {
	// Cell devuelve el identificador de la celda para una coordenada.
	Cell(lat, lon float64) string
	// CellCenter devuelve lat/lon del centro de una celda.
	CellCenter(cell string) (lat, lon float64, err error)
	// Resolution devuelve la resolución H3 configurada.
	Resolution() int
}

// H3 uses Uber H3 (pure-Go port, h3-go/v4).
type H3 struct {
	Res int
}

func (h H3) Cell(lat, lon float64) string {
	c, err := h3.LatLngToCell(h3.LatLng{Lat: lat, Lng: lon}, h.Res)
	if err != nil {
		return ""
	}
	return c.String()
}

func (h H3) CellCenter(cell string) (float64, float64, error) {
	c := h3.CellFromString(cell)
	if !c.IsValid() {
		return 0, 0, fmt.Errorf("celda inválida %q", cell)
	}
	ll, err := h3.CellToLatLng(c)
	if err != nil {
		return 0, 0, fmt.Errorf("celda inválida %q: %w", cell, err)
	}
	return ll.Lat, ll.Lng, nil
}

func (h H3) Resolution() int { return h.Res }

// ResolutionDescription devuelve una descripción humana del tamaño aproximado
// de la resolución seleccionada (solo para documentación).
func ResolutionDescription(res int) string {
	// Distancias de borde promedio (km) por resolución H3.
	edgeKm := map[int]float64{
		0: 1107, 1: 418, 2: 158, 3: 60, 4: 22.6, 5: 8.5,
		6: 3.2, 7: 1.2, 8: 0.46, 9: 0.17, 10: 0.065, 11: 0.024,
	}
	if km, ok := edgeKm[res]; ok {
		return fmt.Sprintf("Resolución H3 %d: borde ~%.3f km (~%.2f km²). Nivel urbano; no identifica una antena concreta.", res, km, km*km*0.7265)
	}
	return fmt.Sprintf("Resolución H3 %d.", res)
}
