// Package observe contiene estadísticas, validación y clasificación
// de operadores para las observaciones de conectividad.
package observe

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Operadores contemplados.
const (
	OpClaro       = "claro"
	OpMovistar    = "movistar"
	OpTigo        = "tigo"
	OpWom         = "wom"
	OpOtro        = "otro"
	OpDesconocido = "desconocido"
)

// OperatorMapping vincula un ASN a un operador.
// `mobile` es una clasificación probabilística/configurada, no una verdad
// absoluta (un ASN puede servir infraestructura fija y móvil).
type OperatorMapping struct {
	Operator   string
	Mobile     bool
	Confidence float64
	Source     string
}

// OperatorResolver resuelve ASN -> operador usando mappings cargados.
// Si un ASN no está mapeado, devuelve "desconocido".
type OperatorResolver struct {
	mappings map[int]OperatorMapping
}

func NewOperatorResolver() *OperatorResolver {
	return &OperatorResolver{mappings: map[int]OperatorMapping{}}
}

func (r *OperatorResolver) Add(asn int, m OperatorMapping) {
	r.mappings[asn] = m
}

func (r *OperatorResolver) Resolve(asn int) (operator string, mobile bool, confidence float64) {
	if m, ok := r.mappings[asn]; ok {
		return m.Operator, m.Mobile, m.Confidence
	}
	return OpDesconocido, false, 0
}

func (r *OperatorResolver) Size() int { return len(r.mappings) }

// Entries devuelve una copia de los mappings (asn -> mapping).
func (r *OperatorResolver) Entries() map[int]OperatorMapping {
	out := make(map[int]OperatorMapping, len(r.mappings))
	for k, v := range r.mappings {
		out[k] = v
	}
	return out
}

// OperatorMappingRow es el formato usado por la tabla asn_operator_mapping.
type OperatorMappingRow struct {
	ASN        int
	Operator   string
	Mobile     bool
	Confidence float64
	Source     string
}

func (r *OperatorResolver) AddRow(row OperatorMappingRow) {
	r.mappings[row.ASN] = OperatorMapping{
		Operator:   row.Operator,
		Mobile:     row.Mobile,
		Confidence: row.Confidence,
		Source:     row.Source,
	}
}

// LoadOperatorMappingsCSV carga mappings desde un CSV:
//
//	asn,operator,mobile,confidence,source
//
// El archivo debe estar verificado; la infraestructura existe para cargar
// mappings validados posteriormente, NO afirmaciones no verificadas.
func LoadOperatorMappingsCSV(path string) (*OperatorResolver, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("abrir mapping %s: %w", path, err)
	}
	defer f.Close()
	r := NewOperatorResolver()
	rd := csv.NewReader(f)
	rd.FieldsPerRecord = -1
	header, err := rd.Read()
	if err != nil {
		return nil, fmt.Errorf("leer header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[normalizeHeader(h)] = i
	}
	if _, ok := idx["asn"]; !ok {
		return nil, fmt.Errorf("falta columna asn en %s", path)
	}
	if _, ok := idx["operator"]; !ok {
		return nil, fmt.Errorf("falta columna operator en %s", path)
	}
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		asn, err := strconv.Atoi(strings.TrimSpace(rec[idx["asn"]]))
		if err != nil {
			continue
		}
		raw := strings.TrimSpace(rec[idx["operator"]])
		op := NormalizeOperator(raw)
		if op == OpDesconocido && !strings.EqualFold(raw, OpDesconocido) {
			op = OpOtro
		}
		m := OperatorMapping{
			Operator:   op,
			Confidence: 0.5,
			Source:     col(idx, rec, "source"),
		}
		if raw := col(idx, rec, "mobile"); raw != "" {
			m.Mobile, _ = strconv.ParseBool(raw)
		}
		if raw := col(idx, rec, "confidence"); raw != "" {
			m.Confidence, _ = strconv.ParseFloat(raw, 64)
		}
		r.mappings[asn] = m
	}
	return r, nil
}

// LoadOperatorMappingsDB carga mappings desde la tabla asn_operator_mapping.
// rowsIter permite desacoplar pgx del paquete observe.
func LoadOperatorMappingsDB(ctx context.Context, rowsIter func(context.Context) ([]OperatorMappingRow, error)) (*OperatorResolver, error) {
	rows, err := rowsIter(ctx)
	if err != nil {
		return nil, fmt.Errorf("cargar mappings: %w", err)
	}
	r := NewOperatorResolver()
	for _, row := range rows {
		r.AddRow(row)
	}
	return r, nil
}

func normalizeHeader(h string) string {
	return strings.ToLower(strings.NewReplacer(" ", "_", ".", "", "-", "_").Replace(strings.TrimSpace(h)))
}

// NormalizeOperator normaliza un nombre de operador a tokens internos.
// Solo mapea nombres inequívocos; cualquier otra cosa -> desconocido.
func NormalizeOperator(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", ".", "", "&", "", ",", "").Replace(s)
	switch {
	case strings.Contains(s, "comcel"), strings.Contains(s, "claro"):
		return OpClaro
	case strings.Contains(s, "telefonica"), strings.Contains(s, "movistar"), strings.Contains(s, "colombia telecomunicaciones"):
		return OpMovistar
	case strings.Contains(s, "une"):
		return "une"
	case strings.Contains(s, "colombia movil"), strings.Contains(s, "tigo"):
		return OpTigo
	case strings.Contains(s, "wom"):
		return OpWom
	case strings.Contains(s, "directv"):
		return "directv"
	case strings.Contains(s, "avantel"):
		return "avantel"
	case strings.Contains(s, "etb"):
		return "etb"
	case strings.Contains(s, "partners"):
		return OpWom
	}
	if s == "att" || s == "at&t" {
		return OpDesconocido
	}
	if len(s) > 1 && s != "otro" && s != "desconocido" && s != "n/a" && s != "-" {
		return s
	}
	return OpDesconocido
}

func col(idx map[string]int, rec []string, name string) string {
	if i, ok := idx[name]; ok && i < len(rec) {
		return strings.TrimSpace(rec[i])
	}
	return ""
}
