package importer

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ImportSynthesis carga los CSVs generados por synthesize_coverage.py
// (coverage_municipality.csv, coverage_cells.csv) junto al manifest y
// reemplaza por completo las tablas de síntesis de cobertura.
// Devuelve (filas municipales, celdas, error).
func ImportSynthesis(ctx context.Context, pool *pgxpool.Pool, dir string) (int, int, error) {
	if _, err := pool.Exec(ctx, `TRUNCATE coverage_synthesis, coverage_synthesis_cells`); err != nil {
		return 0, 0, fmt.Errorf("vaciar síntesis: %w", err)
	}

	muni, err := importSynthesisMunicipal(ctx, pool, filepath.Join(dir, "coverage_municipality.csv"))
	if err != nil {
		return 0, 0, err
	}
	cells, err := importSynthesisCells(ctx, pool, filepath.Join(dir, "coverage_cells.csv"))
	if err != nil {
		return 0, 0, err
	}

	meta := loadSynthesisManifest(filepath.Join(dir, "manifest-synthesis.json"))
	_, err = pool.Exec(ctx, `INSERT INTO coverage_synthesis_meta (id, generated_at, source, h3_res, updated_at)
		VALUES (1, $1, $2, $3, now())
		ON CONFLICT (id) DO UPDATE SET
			generated_at = EXCLUDED.generated_at,
			source = EXCLUDED.source,
			h3_res = EXCLUDED.h3_res,
			updated_at = now()`,
		meta.GeneratedAt, meta.Source, meta.H3Res)
	if err != nil {
		return muni, cells, fmt.Errorf("meta síntesis: %w", err)
	}
	return muni, cells, nil
}

type synthesisManifest struct {
	GeneratedAt time.Time `json:"generated_at"`
	Source      string    `json:"source"`
	H3Res       int       `json:"h3_res"`
}

func loadSynthesisManifest(path string) synthesisManifest {
	m := synthesisManifest{Source: "mapas públicos de operadores (synthesize_coverage.py)", H3Res: 7}
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	var raw struct {
		GeneratedAt string `json:"generated_at"`
		H3Res       int    `json:"h3_res"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return m
	}
	if raw.GeneratedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw.GeneratedAt); err == nil {
			m.GeneratedAt = t
		} else if t, err := time.Parse(time.RFC3339, raw.GeneratedAt); err == nil {
			m.GeneratedAt = t
		}
	}
	if raw.H3Res > 0 {
		m.H3Res = raw.H3Res
	}
	return m
}

func importSynthesisMunicipal(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("abrir %s: %w", path, err)
	}
	defer f.Close()

	rd := csv.NewReader(f)
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	header, err := rd.Read()
	if err != nil {
		return 0, fmt.Errorf("cabecera %s: %w", path, err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[normHeader(h)] = i
	}
	for _, c := range []string{"dane_code", "municipality", "operator", "technology", "covered_ratio", "area_km2"} {
		if _, ok := idx[c]; !ok {
			return 0, fmt.Errorf("falta columna %q en %s", c, path)
		}
	}

	cols := []string{"dane_code", "department", "municipality", "operator", "technology", "covered_ratio", "covered_km2", "area_km2"}
	// Algunos municipios tienen varios polígonos en los límites (misma
	// clave dane×operador×tecnología): se agregan sumando áreas y
	// recomputando la fracción cubierta (covered/area).
	acc := map[string]*[2]float64{}
	names := map[string][2]string{}
	order := []string{}
	push := func(key, department, municipality string, coveredKM2, areaKM2 float64) {
		a, ok := acc[key]
		if !ok {
			acc[key] = &[2]float64{}
			names[key] = [2]string{department, municipality}
			order = append(order, key)
			a = acc[key]
		}
		a[0] += coveredKM2
		a[1] += areaKM2
	}

	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("leer %s: %w", path, err)
		}
		dane := strings.TrimSpace(colAt(rec, idx, "dane_code"))
		if dane == "" {
			continue
		}
		if _, ok := parseFloatLenient(colAt(rec, idx, "covered_ratio")); !ok {
			continue
		}
		coveredKM2, _ := parseFloatLenient(colAt(rec, idx, "covered_km2"))
		areaKM2, _ := parseFloatLenient(colAt(rec, idx, "area_km2"))
		department := strings.TrimSpace(colAt(rec, idx, "department"))
		municipality := strings.TrimSpace(colAt(rec, idx, "municipality"))
		operator := strings.TrimSpace(colAt(rec, idx, "operator"))
		technology := strings.TrimSpace(colAt(rec, idx, "technology"))
		key := dane + "\x00" + operator + "\x00" + technology
		push(key, department, municipality, coveredKM2, areaKM2)
	}

	rows := make([][]any, 0, len(acc))
	for _, key := range order {
		a := acc[key]
		parts := strings.SplitN(key, "\x00", 5)
		ratio := 0.0
		if a[1] > 0 {
			ratio = a[0] / a[1]
		}
		rows = append(rows, []any{
			parts[0],
			names[key][0],
			names[key][1],
			parts[1],
			parts[2],
			ratio,
			a[0],
			a[1],
		})
	}
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"coverage_synthesis"}, cols, pgx.CopyFromRows(rows)); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func importSynthesisCells(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("abrir %s: %w", path, err)
	}
	defer f.Close()

	rd := csv.NewReader(f)
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	header, err := rd.Read()
	if err != nil {
		return 0, fmt.Errorf("cabecera %s: %w", path, err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[normHeader(h)] = i
	}
	for _, c := range []string{"h3", "operator", "technology"} {
		if _, ok := idx[c]; !ok {
			return 0, fmt.Errorf("falta columna %q en %s", c, path)
		}
	}

	cols := []string{"h3", "operator", "technology"}
	rows := make([][]any, 0, 32768)
	imported := 0
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		if _, err := pool.CopyFrom(ctx, pgx.Identifier{"coverage_synthesis_cells"}, cols, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		imported += len(rows)
		rows = rows[:0]
		return nil
	}

	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("leer %s: %w", path, err)
		}
		h3 := strings.TrimSpace(colAt(rec, idx, "h3"))
		if h3 == "" {
			continue
		}
		rows = append(rows, []any{
			h3,
			strings.TrimSpace(colAt(rec, idx, "operator")),
			strings.TrimSpace(colAt(rec, idx, "technology")),
		})
		if len(rows) >= 32768 {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	if err := flush(); err != nil {
		return 0, err
	}
	return imported, nil
}
