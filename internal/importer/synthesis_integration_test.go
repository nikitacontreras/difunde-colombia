//go:build integration

package importer

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestImportSynthesisLocal valida el pipeline de síntesis contra un
// PostgreSQL real (no PostGIS). Requiere DATABASE_URL apuntando a una base
// donde ya se haya aplicado 0011_coverage_synthesis.sql.
func TestImportSynthesisLocal(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL no definida")
	}
	dir := os.Getenv("SYNTHESIS_DIR")
	if dir == "" {
		dir = "../../data/synthesis"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	muni, cells, err := ImportSynthesis(ctx, pool, dir)
	if err != nil {
		t.Fatal(err)
	}
	if muni == 0 || cells == 0 {
		t.Fatalf("ImportSynthesis devolvió vacío: muni=%d cells=%d", muni, cells)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM coverage_synthesis_meta`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("coverage_synthesis_meta = %d filas, want 1", n)
	}

	var dane string
	if err := pool.QueryRow(ctx, `SELECT dane_code FROM coverage_synthesis WHERE municipality ILIKE '%BOGOT%' LIMIT 1`).Scan(&dane); err != nil {
		t.Fatal(err)
	}
	t.Logf("Bogotá dane_code=%s", dane)
}
