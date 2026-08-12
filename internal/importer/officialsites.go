package importer

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"colombia-difunde/internal/observe"
)

// importOfficialSites carga crc_infraestructura_redes_acceso_movil.csv
// (número de sitios por operador/municipio/tecnología).
// Igual que cobertura: solo el último trimestre disponible.
func importOfficialSites(ctx context.Context, pool *pgxpool.Pool, path string) (int, int, error) {
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
	for _, c := range []string{"anno", "trimestre", "desc_empresa", "codigo_dane_municipio", "sitios"} {
		if _, ok := idx[c]; !ok {
			return 0, 0, fmt.Errorf("faltan columnas de infraestructura en %s (col %q)", path, c)
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
		y, _ := parseIntLenient(colAt(rec, idx, "anno"))
		q, _ := parseIntLenient(colAt(rec, idx, "trimestre"))
		if y > maxY || (y == maxY && q > maxQ) {
			maxY, maxQ = y, q
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
		if err := insertOfficialSitesBatch(ctx, pool, batch); err != nil {
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
		dane, _ := parseIntLenient(colAt(rec, idx, "codigo_dane_municipio"))
		empresa := colAt(rec, idx, "desc_empresa")
		op := observe.NormalizeOperator(empresa)
		if op == observe.OpDesconocido {
			op = observe.OpOtro
		}
		sitios, _ := parseIntLenient(colAt(rec, idx, "sitios"))
		own, _ := parseIntLenient(colAt(rec, idx, "sitio_propio_coubicacion"))
		batch = append(batch, []any{
			"crc_infraestructura_redes_acceso_movil", fmt.Sprintf("%dQ%d", y, q), y, q,
			dane, colAt(rec, idx, "desc_municipio"), op, sitios, own,
			techFlag(rec, idx, "tecnologia_estacion_base_2g"),
			techFlag(rec, idx, "tecnologia_estacion_base_3g"),
			techFlag(rec, idx, "tecnologia_estacion_base_4g"),
			techFlag(rec, idx, "tecnologia_estacion_base_5g"),
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

func techFlag(rec []string, idx map[string]int, name string) bool {
	v, ok := parseIntLenient(colAt(rec, idx, name))
	return ok && v > 0
}

func insertOfficialSitesBatch(ctx context.Context, pool *pgxpool.Pool, batch [][]any) error {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO official_sites
		(source, source_date, year, quarter, dane_code, municipality, operator,
		 sitios, own_co_location, tech_2g, tech_3g, tech_4g, tech_5g)
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
