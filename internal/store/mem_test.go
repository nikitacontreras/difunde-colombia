package store

import (
	"context"
	"testing"
	"time"

	"colombia-difunde/internal/geo"
)

func memObs(lat, lon float64, op string, rtt float64, at time.Time) Observation {
	h := geo.H3{Res: 8}
	return Observation{
		ObservedAt:    at,
		Latitude:      lat,
		Longitude:     lon,
		H3Cell:        h.Cell(lat, lon),
		Operator:      op,
		HttpRTTMedian: rtt,
		Jitter:        10,
		SuccessRatio:  0.9,
		CallSignal:    "yes",
		EffectiveType: "4g",
	}
}

func TestMemStoreAggregation(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC()

	// 3 observaciones recientes en Cali, 1 antigua (fuera de ventana).
	for _, o := range []Observation{
		memObs(3.4516, -76.532, "movistar", 200, now.Add(-1*time.Minute)),
		memObs(3.4520, -76.531, "movistar", 300, now.Add(-2*time.Minute)),
		memObs(3.4400, -76.510, "claro", 500, now.Add(-3*time.Minute)),
		memObs(3.4516, -76.532, "movistar", 900, now.Add(-2*time.Hour)),
	} {
		if _, err := m.InsertObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	cells, err := m.Cells(ctx, CellFilter{
		MinLat: 3.4, MinLon: -76.6, MaxLat: 3.5, MaxLon: -76.4,
		Window: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Las dos primeras están ~45 m (misma celda H3 res 8); la tercera
	// está a ~2.5 km (celda distinta). La antigua queda fuera por ventana.
	if len(cells) != 2 {
		t.Fatalf("agregados = %d, want 2", len(cells))
	}
	var total int
	for _, c := range cells {
		total += c.Count
	}
	if total != 3 {
		t.Errorf("total observaciones agregadas = %d, want 3", total)
	}

	// Filtro por operador.
	cells, err = m.Cells(ctx, CellFilter{
		MinLat: 3.4, MinLon: -76.6, MaxLat: 3.5, MaxLon: -76.4,
		Window: 15 * time.Minute, Operator: "claro",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 || cells[0].TopOperator != "claro" {
		t.Fatalf("filtro operador falló: %+v", cells)
	}
}

func TestMemStoreSyncBatch(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC()
	var obs []Observation
	for i := 0; i < 5; i++ {
		obs = append(obs, memObs(3.45+float64(i)*0.0001, -76.53, "tigo", 300, now))
	}
	if err := m.InsertObservations(ctx, obs); err != nil {
		t.Fatal(err)
	}
	cells, err := m.Cells(ctx, CellFilter{MinLat: 3.4, MinLon: -76.6, MaxLat: 3.6, MaxLon: -76.4, Window: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, c := range cells {
		total += c.Count
	}
	if total != 5 {
		t.Errorf("sync total = %d, want 5", total)
	}
}

func TestMemStoreCoverageSynthesis(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	meta := &CoverageMeta{GeneratedAt: time.Now().UTC(), Source: "test", H3Res: 7}
	m.SetSynthesis(
		[]CoverageSynthesisRow{
			{DaneCode: "11001", Municipality: "Bogotá", Operator: "claro", Technology: "4G", CoveredRatio: 0.9},
			{DaneCode: "11001", Municipality: "Bogotá", Operator: "wom", Technology: "4G", CoveredRatio: 0.3},
		},
		map[string][]CoverageCellRow{
			"8766009b2ffffff": {{Operator: "claro", Technology: "4G"}},
		},
		meta,
	)

	rows, err := m.CoverageSynthesis(ctx, "11001")
	if err != nil || len(rows) != 2 {
		t.Fatalf("CoverageSynthesis = %d filas, err %v; want 2", len(rows), err)
	}
	if rows[0].Operator != "claro" || rows[1].Operator != "wom" {
		t.Errorf("órden de operadores incorrecto: %+v", rows)
	}

	cells, err := m.CoveragePoint(ctx, "8766009b2ffffff")
	if err != nil || len(cells) != 1 || cells[0].Operator != "claro" {
		t.Fatalf("CoveragePoint = %+v, err %v", cells, err)
	}

	got, err := m.CoverageMeta(ctx)
	if err != nil || got == nil || got.H3Res != 7 || got.Source != "test" {
		t.Fatalf("CoverageMeta = %+v, err %v", got, err)
	}

	if rows, err := m.CoverageSynthesis(ctx, "99999"); err != nil || len(rows) != 0 {
		t.Errorf("CoverageSynthesis desconocido = %d filas, err %v; want 0", len(rows), err)
	}
}
