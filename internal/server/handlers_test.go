package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"colombia-difunde/internal/asn"
	"colombia-difunde/internal/config"
	"colombia-difunde/internal/geo"
	"colombia-difunde/internal/observe"
	"colombia-difunde/internal/store"
)

func newTestServer() (*Server, *store.MemStore) {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	cfg.H3Resolution = 8
	cfg.AsnMappingFromDB = false
	cfg.DatabaseURL = ""
	m := store.NewMemStore()
	s := New(cfg, m, asn.EmptyResolver{}, observe.NewOperatorResolver(), geo.H3{Res: 8})
	return s, m
}

func TestPOSTObservation(t *testing.T) {
	s, _ := newTestServer()
	body := `{"x":3.4516,"y":-76.532,"a":18,"r":284,"j":61,"n":4,"ok":3,"f":1,"q":0.75,"e":"3g","br":350,"bd":0.7,"sd":0,"k1":900,"k4":3500,"t":` + fmt.Sprint(time.Now().Unix()) + `,"u":1}`
	req := httptest.NewRequest(http.MethodPost, "/o", strings.NewReader(body))
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /o = %d, body=%s", w.Code, w.Body.String())
	}
	id := w.Header().Get("X-Obs-ID")
	if id == "" {
		t.Fatal("falta X-Obs-ID")
	}
}

func TestPOSTObservationInvalid(t *testing.T) {
	s, _ := newTestServer()
	cases := []string{
		`{"x":91,"y":0,"r":100,"q":1}`,             // lat inválida
		`{"x":0,"y":0,"r":-1,"q":1}`,               // rtt inválida
		`{"x":0,"y":0,"r":100,"q":0,"c":"talvez"}`, // call signal inválido
		`not json`,
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/o", strings.NewReader(body))
		req.RemoteAddr = "198.51.100.7:1234"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q => %d, want 400", body, w.Code)
		}
	}
}

func TestSyncAndAggregation(t *testing.T) {
	s, _ := newTestServer()
	now := time.Now().Unix()
	items := []string{}
	for i := 0; i < 4; i++ {
		lat := 3.45 + float64(i)*0.0001
		items = append(items, fmt.Sprintf(`{"x":%f,"y":-76.53,"a":10,"r":200,"j":5,"n":4,"ok":4,"f":0,"q":1,"t":%d}`, lat, now))
	}
	body := "[" + strings.Join(items, ",") + "]"
	req := httptest.NewRequest(http.MethodPost, "/sync", strings.NewReader(body))
	req.RemoteAddr = "198.51.100.7:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("POST /sync = %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/cells?bbox=-76.6,3.4,-76.4,3.6&window=15m", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /cells = %d", w.Code)
	}
	var cells []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &cells); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, c := range cells {
		total += int(c["n"].(float64))
	}
	if total != 4 {
		t.Errorf("agregados total = %d, want 4", total)
	}
	if w.Header().Get("ETag") == "" {
		t.Error("falta ETag")
	}
}

func TestProbeEndpoints(t *testing.T) {
	s, _ := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("GET /p = %d", w.Code)
	}

	for _, path := range []string{"/probe/1k", "/probe/4k"} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "198.51.100.7:1234"
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, w.Code)
		}
		want := 1024
		if path == "/probe/4k" {
			want = 4096
		}
		if w.Body.Len() != want {
			t.Errorf("GET %s body = %d bytes, want %d", path, w.Body.Len(), want)
		}
	}
}

func TestFollowupUpdate(t *testing.T) {
	s, m := newTestServer()
	_ = m
	// Insertar observación directamente para obtener id.
	id, err := s.store.InsertObservation(t.Context(), store.Observation{
		ObservedAt: time.Now(), Latitude: 3.4516, Longitude: -76.532,
		H3Cell: "8c1c1", Operator: "desconocido",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"id":%d,"c":"yes","op":"tigo"}`, id)
	req := httptest.NewRequest(http.MethodPost, "/o/update", strings.NewReader(body))
	req.RemoteAddr = "198.51.100.7:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("POST /o/update = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestIndexPage(t *testing.T) {
	s, _ := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "COMPARTIR UBICACIÓN") {
		t.Error("página sin botón principal")
	}
}

func TestCoverageEndpoints(t *testing.T) {
	s, m := newTestServer()
	m.SetBaseline([]store.CoverageRow{
		{DaneCode: 76001, Municipality: "Cali", Technology: "4G", SignalLevel: 1, AreaKM2: 560,
			PctClaro: 95.5, PctMovistar: 88.2, PctTigo: 70.1, PctWom: 40.0},
		{DaneCode: 76001, Municipality: "Cali", Technology: "2G", SignalLevel: 1, AreaKM2: 560,
			PctClaro: 99.0, PctMovistar: 95.0, PctTigo: 90.0, PctWom: 0},
		{DaneCode: 11001, Municipality: "Bogotá D.C.", Technology: "4G", SignalLevel: 1, AreaKM2: 1775,
			PctClaro: 98.0, PctMovistar: 97.0, PctTigo: 80.0, PctWom: 75.0},
	}, []store.OfficialSitesRow{
		{DaneCode: 76001, Municipality: "Cali", Operator: "claro", Sites: 120, Tech4G: true},
		{DaneCode: 76001, Municipality: "Cali", Operator: "movistar", Sites: 95, Tech5G: true},
	})

	req := httptest.NewRequest(http.MethodGet, "/coverage?municipality=cali", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /coverage = %d", w.Code)
	}
	var rows []store.CoverageRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("cobertura Cali = %d filas, want 2", len(rows))
	}

	req = httptest.NewRequest(http.MethodGet, "/coverage?municipality=cali&operator=wom", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /coverage?operator=wom = %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	// WOM sin cobertura en 2G de Cali: solo debe quedar la fila 4G.
	if len(rows) != 1 || rows[0].Technology != "4G" {
		t.Errorf("cobertura wom Cali = %+v, want solo 4G", rows)
	}

	req = httptest.NewRequest(http.MethodGet, "/coverage/sites?municipality=cali", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /coverage/sites = %d", w.Code)
	}
	var sites []store.OfficialSitesRow
	if err := json.Unmarshal(w.Body.Bytes(), &sites); err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 || sites[0].Operator != "claro" || sites[0].Sites != 120 {
		t.Errorf("sitios oficiales Cali = %+v", sites)
	}
}

func TestRateLimit(t *testing.T) {
	_, _ = newTestServer()
	// Forzar límite mínimo para probar 429.
	rl := newRateLimiter(2, time.Minute)
	if !rl.allow("k") || !rl.allow("k") {
		t.Fatal("debería permitir los primeros 2")
	}
	if rl.allow("k") {
		t.Fatal("tercera request debería ser rechazada")
	}
	// Otro key sin afectar.
	if !rl.allow("k2") {
		t.Fatal("key distinta afectada")
	}
	// Ventana nueva permite de nuevo.
	rl.buckets["k"] = &rlBucket{count: 0, resetAt: time.Now().Add(-time.Minute)}
	if !rl.allow("k") {
		t.Fatal("ventana nueva debería permitir")
	}
}

func TestGzipIndex(t *testing.T) {
	// Regresión: el middleware gzip no debe re-entrar al mismo writer (antes
	// provocaba panic al servir / con Accept-Encoding: gzip).
	s, _ := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("falta Content-Encoding: gzip")
	}
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<html") {
		t.Fatalf("respuesta descomprimida inesperada: %.60q", out)
	}
}
