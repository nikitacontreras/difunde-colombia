package observe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeOperator(t *testing.T) {
	cases := map[string]string{
		"COMCEL":                              OpClaro,
		"COMUNICACION CELULAR S A COMCEL S A": OpClaro,
		"Claro Colombia":                      OpClaro,
		"TELEFONICA":                          OpMovistar,
		"TELEFONICA MOVILES COLOMBIA":         OpMovistar,
		"COLOMBIA TELECOMUNICACIONES":         OpMovistar,
		"TIGO":                                OpTigo,
		"COLOMBIA MOVIL":                      OpTigo,
		"WOM":                                 OpWom,
		"UNE EPM":                             "une",
		"Directv":                             "directv",
		"AT&T":                                OpDesconocido,
		"":                                    OpDesconocido,
	}
	for in, want := range cases {
		if got := NormalizeOperator(in); got != want {
			t.Errorf("NormalizeOperator(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOperatorResolverCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mapping.csv")
	content := "asn,operator,mobile,confidence,source\n" +
		"3816,movistar,true,0.9,verified\n" +
		"13490,claro,true,0.8,verified\n" +
		"12345,algo raro,false,0.3,unverified\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := LoadOperatorMappingsCSV(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	op, mobile, conf := r.Resolve(3816)
	if op != OpMovistar || !mobile || conf != 0.9 {
		t.Errorf("Resolve(3816) = %q mobile=%v conf=%v", op, mobile, conf)
	}
	op, mobile, _ = r.Resolve(13490)
	if op != OpClaro || !mobile {
		t.Errorf("Resolve(13490) = %q mobile=%v", op, mobile)
	}
	// ASN sin mapping -> desconocido, no mobile.
	op, mobile, _ = r.Resolve(99999)
	if op != OpDesconocido || mobile {
		t.Errorf("Resolve(99999) = %q mobile=%v", op, mobile)
	}
	// Nombres raros deben conservarse, no caer en otro de forma genérica
	op, _, _ = r.Resolve(12345)
	if op != "algo raro" {
		t.Errorf("Resolve(12345) = %q, want 'algo raro'", op)
	}
}

func TestOperatorResolverAddRow(t *testing.T) {
	r := NewOperatorResolver()
	r.AddRow(OperatorMappingRow{ASN: 3816, Operator: OpMovistar, Mobile: true, Confidence: 0.9, Source: "t"})
	op, mobile, _ := r.Resolve(3816)
	if op != OpMovistar || !mobile {
		t.Errorf("AddRow: %q mobile=%v", op, mobile)
	}
}
