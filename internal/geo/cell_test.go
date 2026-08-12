package geo

import (
	"math"
	"testing"
)

func TestH3CellStable(t *testing.T) {
	h := H3{Res: 8}
	lat, lon := 3.4516, -76.532
	c1 := h.Cell(lat, lon)
	c2 := h.Cell(lat, lon)
	if c1 != c2 {
		t.Fatalf("celda no determinista: %s vs %s", c1, c2)
	}
	if len(c1) == 0 {
		t.Fatal("celda vacía")
	}
}

func TestH3CellCenterRoundtrip(t *testing.T) {
	h := H3{Res: 8}
	lat, lon := 3.4516, -76.532
	c := h.Cell(lat, lon)
	clat, clon, err := h.CellCenter(c)
	if err != nil {
		t.Fatalf("CellCenter: %v", err)
	}
	// El centro debe estar a menos de ~1 km de la coordenada original
	// (borde de resolución 8 ~ 0.46 km).
	d := haversine(lat, lon, clat, clon)
	if d > 1000 {
		t.Fatalf("centro muy lejos: %.0f m", d)
	}
	if h.Cell(clat, clon) != c {
		t.Fatalf("roundtrip celda falló: %s != %s", h.Cell(clat, clon), c)
	}
}

func TestResolutionDescription(t *testing.T) {
	if ResolutionDescription(8) == "" {
		t.Fatal("descripción vacía")
	}
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0
	la1, lo1, la2, lo2 := lat1*math.Pi/180, lon1*math.Pi/180, lat2*math.Pi/180, lon2*math.Pi/180
	dlat, dlon := la2-la1, lo2-lo1
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * R * math.Asin(math.Sqrt(a))
}
