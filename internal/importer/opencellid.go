package importer

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"colombia-difunde/internal/geo"
	"colombia-difunde/internal/observe"
)

// importOpenCellID importa celdas desde un export de OpenCellID (formato CSV o .csv.gz).
func importOpenCellID(ctx context.Context, pool *pgxpool.Pool, path string, h3res int) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	var r io.Reader = file
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(file)
		if err != nil {
			return 0, 0, err
		}
		defer gz.Close()
		r = gz
	}

	reader := csv.NewReader(r)
	// OpenCellID es un CSV sin cabecera usualmente, pero si tiene una línea con "radio", la saltamos.
	firstRow, err := reader.Read()
	if err != nil {
		return 0, 0, err
	}

	isHeader := false
	if len(firstRow) > 0 && (firstRow[0] == "radio" || firstRow[0] == "radio,mcc,net") {
		isHeader = true
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

	processRow := func(rec []string) {
		if len(rec) < 8 {
			skipped++
			return
		}
		// Columnas: radio,mcc,net,area,cell,unit,lon,lat,...
		mcc := rec[1]
		if mcc != "732" { // Ignoramos si no es Colombia
			skipped++
			return
		}

		lon, err1 := strconv.ParseFloat(rec[6], 64)
		lat, err2 := strconv.ParseFloat(rec[7], 64)
		if err1 != nil || err2 != nil {
			skipped++
			return
		}
		if lat < minLat || lat > maxLat || lon < minLon || lon > maxLon {
			skipped++
			return
		}

		net := rec[2]
		op := mapMNC(net)
		radio := rec[0] // GSM, UMTS, LTE, etc.
		lac := rec[3]
		cid := rec[4]

		addr := fmt.Sprintf("LAC-%s CID-%s", lac, cid)

		batch = append(batch, []any{
			source, "opencellid", h3.Cell(lat, lon),
			op, fmt.Sprintf("MNC-%s", net), addr, radio,
			lat, lon,
		})

		if len(batch) >= 2000 {
			if err := flush(); err != nil {
				// Ignoramos errores de lote individual para no abortar toda la importación
				skipped += len(batch)
				batch = batch[:0]
			}
		}
	}

	if !isHeader {
		processRow(firstRow)
	}

	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			break // detenemos si hay error de formato intermedio
		}
		processRow(rec)
	}

	_ = flush()

	return rows, skipped, nil
}

func mapMNC(mnc string) string {
	switch mnc {
	case "101", "1", "001", "01", "2", "002", "02":
		return "comcel"
	case "123", "003", "3", "03":
		return "movistar"
	case "103", "111", "020", "142", "104", "4", "004", "04":
		return "colombia movil"
	case "360", "130", "24":
		return "wom"
	default:
		return observe.OpOtro
	}
}
