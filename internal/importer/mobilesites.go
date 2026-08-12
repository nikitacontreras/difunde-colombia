package importer

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"colombia-difunde/internal/geo"
	"colombia-difunde/internal/observe"
)

// Límites de Colombia continental para filtrar coordenadas erróneas.
const (
	minLat = -4.5
	maxLat = 13.0
	minLon = -81.0
	maxLon = -66.0
)

// importMobileSites carga los inventarios de antenas de Cali (puntos).
// Detecta cabecera flexible (LATITUD/LONGITUD) y el operador desde
// EMPRESA/PROPIETARIO o desde las columnas booleanas por operador.
// Una torre compartida produce una fila por operador presente.
func importMobileSites(ctx context.Context, pool *pgxpool.Pool, path string, h3res int) (int, int, error) {
	data, err := decodeCSV(path)
	if err != nil {
		return 0, 0, err
	}
	delim := detectDelimiter(firstLine(data))
	startRow, idx := findHeaderRow(data, delim, "latitud", "longitud")
	if startRow < 0 {
		return 0, 0, fmt.Errorf("no se encontró cabecera latitud/longitud en %s", path)
	}

	rd := newReader(data, delim)
	for i := 0; i < startRow; i++ {
		if _, err := rd.Read(); err != nil {
			return 0, 0, err
		}
	}
	h3 := geo.H3{Res: h3res}
	source := filepath.Base(path)

	var rows, skipped int
	var batch [][]any
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := insertMobileSitesBatch(ctx, pool, batch); err != nil {
			return err
		}
		rows += len(batch)
		batch = batch[:0]
		return nil
	}

	operatorCols := []string{"comcel", "telefonica", "tigo", "wom", "une", "directv", "avantel", "otro"}

	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return rows, skipped, err
		}
		lat, ok1 := parseFloatLenient(colAt(rec, idx, "latitud"))
		lon, ok2 := parseFloatLenient(colAt(rec, idx, "longitud"))
		if !ok1 || !ok2 {
			skipped++
			continue
		}
		if lat < minLat || lat > maxLat || lon < minLon || lon > maxLon {
			skipped++
			continue
		}
		raw := colAt(rec, idx, "empresa")
		if raw == "" {
			raw = colAt(rec, idx, "propietario")
		}
		ops := operatorsFor(rec, idx, raw, operatorCols)
		addr := colAt(rec, idx, "direccion")
		barrio := colAt(rec, idx, "barrio")
		for _, op := range ops {
			batch = append(batch, []any{
				source, source, h3.Cell(lat, lon),
				op, strings.TrimSpace(raw), addr, barrio,
				lat, lon,
			})
		}
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

// operatorsFor determina los operadores de una fila de antena.
func operatorsFor(rec []string, idx map[string]int, raw string, cols []string) []string {
	if raw != "" {
		op := observe.NormalizeOperator(raw)
		if op != observe.OpDesconocido {
			return []string{op}
		}
	}
	var found []string
	for _, c := range cols {
		if v := colAt(rec, idx, c); v != "" && !isNA(v) {
			op := observe.NormalizeOperator(v)
			if op == observe.OpDesconocido {
				op = observe.OpOtro
			}
			found = append(found, op)
		}
	}
	if len(found) == 0 {
		return []string{observe.OpOtro}
	}
	return found
}

func insertMobileSitesBatch(ctx context.Context, pool *pgxpool.Pool, batch [][]any) error {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO mobile_sites
		(source, source_date, h3_cell, operator, operator_raw, address, neighborhood,
		 latitude, longitude, geom)
		VALUES `)
	args := make([]any, 0, len(batch)*9)
	for i, row := range batch {
		if i > 0 {
			sb.WriteString(",")
		}
		// geom = ST_SetSRID(ST_MakePoint(lon,lat),4326). Todos los
		// placeholders $1..$9 quedan referenciados (Postgres no infiere
		// el tipo de un parámetro no usado en el texto de la consulta).
		sb.WriteString("(")
		sb.WriteString(fmt.Sprintf("$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d, ST_SetSRID(ST_MakePoint($%d,$%d),4326)",
			len(args)+1, len(args)+2, len(args)+3, len(args)+4, len(args)+5,
			len(args)+6, len(args)+7, len(args)+8, len(args)+9,
			len(args)+9, len(args)+8))
		sb.WriteString(")")
		args = append(args, row...)
	}
	_, err := pool.Exec(ctx, sb.String(), args...)
	return err
}
