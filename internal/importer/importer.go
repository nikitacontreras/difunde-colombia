// Package importer carga los datasets oficiales descargados por
// scrape_telco_colombia.py en la base (baseline). Nunca se ejecuta
// scraping en cada request; es un comando aparte: server import-data ./data
package importer

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/encoding/charmap"
)

type Report struct {
	Files          []FileReport
	CoverageRows   int
	OfficialRows   int
	MobileSiteRows int
}

type FileReport struct {
	Path    string
	Kind    string
	Rows    int
	Skipped int
	Error   string
}

// ImportData procesa todos los CSV bajo dir y carga las tablas baseline.
func ImportData(ctx context.Context, pool *pgxpool.Pool, dir string, h3res int) (Report, error) {
	var rep Report
	if err := resetTables(ctx, pool); err != nil {
		return rep, err
	}

	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := strings.ToLower(d.Name())
		if !d.IsDir() && (strings.HasSuffix(name, ".csv") || strings.HasSuffix(name, ".csv.gz") || strings.HasSuffix(name, ".gz")) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return rep, err
	}

	for _, path := range files {
		fr := FileReport{Path: path}
		switch classify(path) {
		case "coverage":
			n, sk, err := importCoverage(ctx, pool, path)
			fr.Kind, fr.Rows, fr.Skipped = "coverage", n, sk
			rep.CoverageRows += n
			if err != nil {
				fr.Error = err.Error()
			}
		case "official_sites":
			n, sk, err := importOfficialSites(ctx, pool, path)
			fr.Kind, fr.Rows, fr.Skipped = "official_sites", n, sk
			rep.OfficialRows += n
			if err != nil {
				fr.Error = err.Error()
			}
		case "mobile_sites":
			n, sk, err := importMobileSites(ctx, pool, path, h3res)
			fr.Kind, fr.Rows, fr.Skipped = "mobile_sites", n, sk
			rep.MobileSiteRows += n
			if err != nil {
				fr.Error = err.Error()
			}
		case "opencellid":
			n, sk, err := importOpenCellID(ctx, pool, path, h3res)
			fr.Kind, fr.Rows, fr.Skipped = "opencellid", n, sk
			rep.MobileSiteRows += n
			if err != nil {
				fr.Error = err.Error()
			}
		default:
			fr.Kind = "ignored"
		}
		rep.Files = append(rep.Files, fr)
	}
	return rep, nil
}

func classify(path string) string {
	name := strings.ToLower(filepath.Base(path))
	base := strings.ToLower(path)
	switch {
	case strings.Contains(name, "cobertura") && strings.Contains(base, "postdata"):
		return "coverage"
	case strings.Contains(name, "infraestructura"):
		return "official_sites"
	case strings.Contains(name, "opencellid") || strings.Contains(name, "732") || strings.HasPrefix(name, "732"):
		return "opencellid"
	case strings.Contains(name, "anten"):
		return "mobile_sites"
	case strings.Contains(base, "cali"):
		return "mobile_sites"
	}
	return ""
}

func resetTables(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `TRUNCATE official_coverage, official_sites, mobile_sites`)
	return err
}

// ---- Parsing helpers ----

// decodeCSV lee el archivo y decodifica a UTF-8 (windows-1252 si aplica).
func decodeCSV(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(data) {
		return data, nil
	}
	out, err := charmap.Windows1252.NewDecoder().Bytes(data)
	if err != nil {
		return nil, fmt.Errorf("decodificar %s: %w", path, err)
	}
	return out, nil
}

func firstLine(data []byte) []byte {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return data[:i]
	}
	return data
}

// detectDelimiter elige ';' o ',' según la primera línea no vacía.
func detectDelimiter(line []byte) rune {
	semi := bytes.Count(line, []byte(";"))
	comma := bytes.Count(line, []byte(","))
	if semi >= comma {
		return ';'
	}
	return ','
}

func newReader(data []byte, delim rune) *csv.Reader {
	rd := csv.NewReader(bytes.NewReader(data))
	rd.Comma = delim
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	return rd
}

// parseFloatLenient convierte "0,025764" -> 0.025764, "121,3510001" -> 121.3510001.
func parseFloatLenient(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "N/A") || s == "-" {
		return 0, false
	}
	if strings.Contains(s, ",") && !strings.Contains(s, ".") {
		s = strings.ReplaceAll(s, ",", ".")
	}
	s = strings.ReplaceAll(s, " ", "")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func parseIntLenient(s string) (int, bool) {
	f, ok := parseFloatLenient(s)
	if !ok {
		return 0, false
	}
	return int(f), true
}

var spaceRe = regexp.MustCompile(`\s+`)

func normHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(".", "", "-", "", "/", "", "(", "", ")", "", "#", "").Replace(s)
	s = spaceRe.ReplaceAllString(s, "_")
	s = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n").Replace(s)
	return s
}

// findHeaderRow localiza la fila de cabecera que contiene TODAS las columnas
// requeridas (los archivos de Cali tienen filas de cabecera múltiples/rotas).
func findHeaderRow(data []byte, delim rune, needCols ...string) (int, map[string]int) {
	rd := newReader(data, delim)
	for row := 0; row < 8; row++ {
		rec, err := rd.Read()
		if err != nil {
			return -1, nil
		}
		idx := map[string]int{}
		for i, h := range rec {
			idx[normHeader(h)] = i
		}
		ok := true
		for _, c := range needCols {
			if _, has := idx[c]; !has {
				ok = false
				break
			}
		}
		if ok {
			return row + 1, idx
		}
	}
	return -1, nil
}

func colAt(rec []string, idx map[string]int, name string) string {
	if i, ok := idx[name]; ok && i >= 0 && i < len(rec) {
		return rec[i]
	}
	return ""
}

func isNA(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || strings.EqualFold(s, "N/A") || s == "-" || s == "0"
}
