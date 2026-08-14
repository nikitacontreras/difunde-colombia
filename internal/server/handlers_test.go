package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"colombia-difunde/internal/asn"
	"colombia-difunde/internal/config"
	"colombia-difunde/internal/geo"
	"colombia-difunde/internal/observe"
	"colombia-difunde/internal/store"
)

func testProofOfWork(kind, name string) string {
	for nonce := 0; ; nonce++ {
		value := strconv.Itoa(nonce)
		sum := sha256.Sum256([]byte(kind + name + value))
		if sum[0] == 0 {
			return value
		}
	}
}

func testResourceOwnerToken(seed byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32))
}

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

func TestAdminHeaderAndHistory(t *testing.T) {
	t.Setenv("ADMIN_KEY", "sekret")
	s, m := newTestServer()
	now := time.Now().UTC()

	if _, err := m.InsertObservation(t.Context(), store.Observation{
		ObservedAt:    now.Add(-2 * time.Minute),
		ReceivedAt:    now.Add(-90 * time.Second),
		Latitude:      3.4516,
		Longitude:     -76.532,
		H3Cell:        "8c1c1",
		Operator:      "movistar",
		HttpRTTMedian: 240,
		SuccessRatio:  0.88,
		CallSignal:    "yes",
		EffectiveType: "4g",
		ClientIP:      "203.0.113.10",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InsertObservation(t.Context(), store.Observation{
		ObservedAt:    now.Add(-1 * time.Minute),
		ReceivedAt:    now.Add(-50 * time.Second),
		Latitude:      3.4521,
		Longitude:     -76.531,
		H3Cell:        "8c1c2",
		Operator:      "claro",
		HttpRTTMedian: 520,
		SuccessRatio:  0.63,
		CallSignal:    "no",
		EffectiveType: "3g",
		ClientIP:      "203.0.113.11",
	}); err != nil {
		t.Fatal(err)
	}
	resourceID, err := m.InsertResource(t.Context(), store.Resource{
		Kind:    "refugio",
		Name:    "Refugio Central",
		Address: "Calle 10 # 20-30",
		Lat:     3.45,
		Lon:     -76.53,
		Status:  "pending",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/admin", "/admin.css", "/admin.js", "/admin/api/overview", "/admin/api/resources"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "198.51.100.7:1234"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s sin header = %d, want 404", path, w.Code)
		}

		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "198.51.100.7:1234"
		req.Header.Set("X-Admin-Key", "incorrecta")
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s con header inválido = %d, want 404", path, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Admin-Key", "sekret")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Centro de operaciones") {
		t.Fatalf("admin con header = %d, body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin.css", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Admin-Key", "sekret")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("admin.css con header = %d, cache=%q", w.Code, w.Header().Get("Cache-Control"))
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/api/overview", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Admin-Key", "sekret")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("overview con header = %d, body=%s", w.Code, w.Body.String())
	}
	var overview struct {
		ObservationsTotal int `json:"observations_total"`
		ResourcesPending  int `json:"resources_pending"`
		ResourcesTotal    int `json:"resources_total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.ObservationsTotal != 2 || overview.ResourcesPending != 1 || overview.ResourcesTotal != 1 {
		t.Fatalf("overview inesperado: %+v", overview)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/api/observations?limit=1&q=movistar", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Admin-Key", "sekret")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("histórico admin = %d, body=%s", w.Code, w.Body.String())
	}
	var page struct {
		Total int `json:"total"`
		Items []struct {
			Operator string `json:"operator"`
			ClientIP string `json:"client_ip"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Operator != "movistar" || page.Items[0].ClientIP != "203.0.113.10" {
		t.Fatalf("histórico admin inesperado: %+v", page)
	}

	req = httptest.NewRequest(http.MethodPost, "/resources/moderate", strings.NewReader(fmt.Sprintf(`{"id":%d,"status":"approved"}`, resourceID)))
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Admin-Key", "sekret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("moderar recurso con header = %d, body=%s", w.Code, w.Body.String())
	}

	cityPayload := `{
		"kind":"logistica",
		"name":"Camiones voluntarios Pereira",
		"phone":"3001234567",
		"location_scope":"city",
		"municipality":"Pereira",
		"department":"Risaralda",
		"status":"approved",
		"details":{"intent":"offer","availability":"Hoy"}
	}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/resources", strings.NewReader(cityPayload))
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Admin-Key", "sekret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear oferta por ciudad = %d, body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/api/resources", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Admin-Key", "sekret")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/resources = %d", w.Code)
	}
	var resources []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resources); err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Fatalf("recursos admin = %d, want 2: %+v", len(resources), resources)
	}
	var cityResource map[string]any
	for _, resource := range resources {
		if resource["LocationScope"] == "city" {
			cityResource = resource
		}
	}
	if cityResource == nil || cityResource["Municipality"] != "Pereira" || cityResource["Kind"] != "logistica" || cityResource["Status"] != "approved" {
		t.Fatalf("oferta por ciudad inesperada: %+v", cityResource)
	}
}

func TestReportCityWideLogisticsOffer(t *testing.T) {
	t.Setenv("ADMIN_KEY", "sekret")
	s, _ := newTestServer()
	kind := "logistica"
	name := "Voluntarios con camioneta"
	payload := fmt.Sprintf(`{
		"kind":%q,
		"name":%q,
		"phone":"3001234567",
		"location_scope":"city",
		"municipality":"Pereira",
		"department":"Risaralda",
		"nonce":%q,
		"owner_token":%q,
		"details":{"intent":"offer","description":"Transporte de insumos"}
	}`, kind, name, testProofOfWork(kind, name), testResourceOwnerToken(1))

	req := httptest.NewRequest(http.MethodPost, "/report", strings.NewReader(payload))
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /report city = %d, body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/api/resources", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Admin-Key", "sekret")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET admin resources = %d", w.Code)
	}
	var resources []store.Resource
	if err := json.Unmarshal(w.Body.Bytes(), &resources); err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Kind != "logistica" || resources[0].LocationScope != "city" || resources[0].Municipality != "Pereira" || resources[0].Lat != 0 || resources[0].Lon != 0 {
		t.Fatalf("oferta logística por ciudad inesperada: %+v", resources)
	}
}

func TestResourceDeepLinkAndOwnerEdit(t *testing.T) {
	t.Setenv("ADMIN_KEY", "sekret")
	s, _ := newTestServer()
	kind := "centro_acopio"
	name := "Centro Comunitario Norte"
	ownerToken := testResourceOwnerToken(7)
	payload := fmt.Sprintf(`{
		"kind":%q,
		"name":%q,
		"address":"Calle 10 # 20-30",
		"lat":4.65,
		"lon":-74.08,
		"nonce":%q,
		"owner_token":%q,
		"details":{"intent":"request","needs":["Agua"]}
	}`, kind, name, testProofOfWork(kind, name), ownerToken)

	req := httptest.NewRequest(http.MethodPost, "/report", strings.NewReader(payload))
	req.RemoteAddr = "198.51.100.12:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear recurso = %d, body=%s", w.Code, w.Body.String())
	}
	id := w.Header().Get("X-Resource-ID")
	if id == "" || strings.Contains(w.Body.String(), "OwnerTokenHash") || strings.Contains(w.Body.String(), ownerToken) {
		t.Fatalf("respuesta de creación insegura: id=%q body=%s", id, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/resources/update-details", strings.NewReader(`{"id":`+id+`,"details":{"description":"cambio anónimo"}}`))
	req.RemoteAddr = "198.51.100.12:1234"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cambio anónimo de contenido = %d, want 400", w.Code)
	}

	for _, token := range []string{"", testResourceOwnerToken(8)} {
		req = httptest.NewRequest(http.MethodGet, "/resources/"+id, nil)
		req.RemoteAddr = "198.51.100.12:1234"
		if token != "" {
			req.Header.Set(resourceEditTokenHeader, token)
		}
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET pendiente con token %q = %d, want 404", token, w.Code)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/resources/"+id, nil)
	req.RemoteAddr = "198.51.100.12:1234"
	req.Header.Set(resourceEditTokenHeader, ownerToken)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET pendiente propio = %d, body=%s", w.Code, w.Body.String())
	}

	editPayload := `{
		"kind":"centro_acopio",
		"name":"Centro Comunitario Norte Actualizado",
		"address":"Carrera 11 # 21-31",
		"phone":"3001234567",
		"lat":4.651,
		"lon":-74.081,
		"details":{"intent":"request","responsible":"Ana Ruiz","needs":["Agua","Cobijas"]}
	}`
	req = httptest.NewRequest(http.MethodPut, "/resources/"+id, strings.NewReader(editPayload))
	req.RemoteAddr = "198.51.100.12:1234"
	req.Header.Set(resourceEditTokenHeader, testResourceOwnerToken(9))
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("PUT con token incorrecto = %d, want 404", w.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "/resources/"+id, strings.NewReader(editPayload))
	req.RemoteAddr = "198.51.100.12:1234"
	req.Header.Set(resourceEditTokenHeader, ownerToken)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT propio = %d, body=%s", w.Code, w.Body.String())
	}
	var edited store.Resource
	if err := json.Unmarshal(w.Body.Bytes(), &edited); err != nil {
		t.Fatal(err)
	}
	if edited.Status != "pending" || edited.Name != "Centro Comunitario Norte Actualizado" || edited.Details["responsible"] != "Ana Ruiz" {
		t.Fatalf("recurso editado inesperado: %+v", edited)
	}
	if strings.Contains(w.Body.String(), "OwnerTokenHash") || strings.Contains(w.Body.String(), ownerToken) {
		t.Fatalf("respuesta de edición expone credencial: %s", w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/resources/moderate", strings.NewReader(`{"id":`+id+`,"status":"approved"}`))
	req.RemoteAddr = "198.51.100.12:1234"
	req.Header.Set("X-Admin-Key", "sekret")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("aprobar recurso = %d, body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/resources/"+id, nil)
	req.RemoteAddr = "198.51.100.12:1234"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Ana Ruiz") {
		t.Fatalf("GET público aprobado = %d, body=%s", w.Code, w.Body.String())
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

func TestCoverageProviderCatalogEndpoint(t *testing.T) {
	s, _ := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/coverage/providers", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /coverage/providers = %d, body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Providers []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Technologies []struct {
				ID         string `json:"id"`
				RenderType string `json:"render_type"`
			} `json:"technologies"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Providers) < 4 {
		t.Fatalf("se esperaban al menos 4 proveedores, got %d", len(payload.Providers))
	}
	foundMovistar := false
	for _, p := range payload.Providers {
		if p.ID == "movistar" {
			foundMovistar = true
			if len(p.Technologies) == 0 || p.Technologies[0].RenderType != "image-overlays" {
				t.Fatalf("movistar sin tecnologías renderizables: %+v", p)
			}
		}
	}
	if !foundMovistar {
		t.Fatal("no se encontró Movistar en el catálogo de cobertura")
	}
}

func TestCoverageOverlayEndpoint(t *testing.T) {
	s, _ := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/coverage/overlays?provider=movistar&technology=LTE&bbox=-70.8,-3.8,-70.4,-3.5", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /coverage/overlays = %d, body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Count    int `json:"count"`
		Overlays []struct {
			URL string `json:"url"`
		} `json:"overlays"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count == 0 || len(payload.Overlays) == 0 {
		t.Fatalf("overlays Movistar LTE vacíos: %+v", payload)
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
