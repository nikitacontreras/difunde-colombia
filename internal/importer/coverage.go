package importer

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// importCoverage carga crc_cobertura_movil.csv.
// Solo se importa el último trimestre disponible (snapshot vigente de
// cobertura reportada); el resto del histórico se descarta para no inflar
// la base con 760k filas sin valor para el baseline.
func importCoverage(ctx context.Context, pool *pgxpool.Pool, path string) (int, int, error) {
	data, err := decodeCSV(path)
	if err != nil {
		return 0, 0, err
	}
	delim := detectDelimiter(firstLine(data))
	rd := newReader(data, delim)
	header, err := rd.Read()
	if err != nil {
		return 0, 0, err
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[normHeader(h)] = i
	}
	for _, c := range []string{"anno", "trimestre", "id_municipio", "municipio", "tecnologia", "nivel_senal", "area_cob_claro"} {
		if _, ok := idx[c]; !ok {
			return 0, 0, fmt.Errorf("faltan columnas de cobertura en %s (col %q)", path, c)
		}
	}

	maxY, maxQ := -1, -1
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, err
		}
		if y, ok := parseIntLenient(colAt(rec, idx, "anno")); ok {
			if y > maxY {
				maxY, maxQ = y, -1
			}
			if y == maxY {
				if q, ok := parseIntLenient(colAt(rec, idx, "trimestre")); ok && q > maxQ {
					maxQ = q
				}
			}
		}
	}

	rd = newReader(data, delim)
	if _, err := rd.Read(); err != nil {
		return 0, 0, err
	}
	var rows, skipped int
	var batch [][]any
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := insertCoverageBatch(ctx, pool, batch); err != nil {
			return err
		}
		rows += len(batch)
		batch = batch[:0]
		return nil
	}

	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return rows, skipped, err
		}
		y, _ := parseIntLenient(colAt(rec, idx, "anno"))
		q, _ := parseIntLenient(colAt(rec, idx, "trimestre"))
		if y != maxY || q != maxQ {
			skipped++
			continue
		}
		dane, _ := parseIntLenient(colAt(rec, idx, "id_municipio"))
		area, _ := parseFloatLenient(colAt(rec, idx, "area_cpob"))
		pcts := [4]float64{}
		for i, op := range []string{"area_cob_claro", "area_cob_movistar", "area_cob_tigo", "area_cob_wom"} {
			v, ok := parseFloatLenient(colAt(rec, idx, op))
			if ok {
				pcts[i] = v
			}
		}
		lvl, _ := parseIntLenient(colAt(rec, idx, "nivel_senal"))
		batch = append(batch, []any{
			"crc_cobertura_movil", fmt.Sprintf("%dQ%d", y, q), y, q,
			dane, colAt(rec, idx, "municipio"), colAt(rec, idx, "tecnologia"), lvl,
			area, pcts[0], pcts[1], pcts[2], pcts[3],
		})
		if len(batch) >= 2000 {
			if err := flush(); err != nil {
				return rows, skipped, err
			}
		}
	}
	if err := flush(); err != nil {
		return rows, skipped, err
	}
	return rows, skipped, nil
}

func insertCoverageBatch(ctx context.Context, pool *pgxpool.Pool, batch [][]any) error {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO official_coverage
		(source, source_date, year, quarter, dane_code, municipality, technology,
		 signal_level, area_km2, area_pct_claro, area_pct_movistar, area_pct_tigo, area_pct_wom)
		VALUES `)
	args := make([]any, 0, len(batch)*13)
	for i, row := range batch {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(")
		for j := 1; j <= 13; j++ {
			if j > 1 {
				sb.WriteString(",")
			}
			sb.WriteString("$" + strconv.Itoa(len(args)+j))
		}
		sb.WriteString(")")
		args = append(args, row...)
	}
	_, err := pool.Exec(ctx, sb.String(), args...)
	return err
}
