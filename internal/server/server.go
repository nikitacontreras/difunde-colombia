// Package server expone la API HTTP y sirve el frontend.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"colombia-difunde/internal/asn"
	"colombia-difunde/internal/config"
	"colombia-difunde/internal/geo"
	"colombia-difunde/internal/observe"
	"colombia-difunde/internal/state"
	"colombia-difunde/internal/store"
	"colombia-difunde/web"
)

type Server struct {
	cfg    config.Config
	store  store.Store
	asn    asn.Resolver
	ops    *observe.OperatorResolver
	cells  geo.CellResolver
	mux    *http.ServeMux
	limits map[string]*rateLimiter
	cache  *responseCache
	fp     *fingerprintTracker

	probe1k []byte
	probe4k []byte
}

func New(cfg config.Config, st store.Store, ar asn.Resolver, ops *observe.OperatorResolver, cr geo.CellResolver) *Server {
	s := &Server{
		cfg:     cfg,
		store:   st,
		asn:     ar,
		ops:     ops,
		cells:   cr,
		mux:     http.NewServeMux(),
		probe1k: makeProbeBody(1024),
		probe4k: makeProbeBody(4096),
		cache:   newResponseCache(256),
		fp:      newFingerprintTracker(32, time.Hour),
		limits: map[string]*rateLimiter{
			"/p":      newRateLimiter(cfg.Rate.P, time.Minute),
			"/probe":  newRateLimiter(cfg.Rate.Probe, time.Minute),
			"/o":      newRateLimiter(cfg.Rate.Observe, time.Minute),
			"/sync":   newRateLimiter(cfg.Rate.Sync, time.Minute),
			"/cells":  newRateLimiter(cfg.Rate.Cells, time.Minute),
			"/update": newRateLimiter(cfg.Rate.Update, time.Minute),
			"/report": newRateLimiter(cfg.Rate.Report, time.Minute),
		},
	}
	s.routes()
	return s
}

func makeProbeBody(n int) []byte {
	b := make([]byte, n)
	copy(b, "#probe colombia-difunde\n")
	for i := 0; i < len(b); i++ {
		if b[i] == 0 {
			b[i] = 'x'
		}
	}
	return b
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /map", s.handleMap)
	s.mux.HandleFunc("GET /app.js", s.handleAsset("app.js"))
	s.mux.HandleFunc("GET /map.js", s.handleAsset("map.js"))
	s.mux.HandleFunc("GET /app.css", s.handleAsset("app.css"))
	s.mux.HandleFunc("GET /sw.js", s.handleAsset("sw.js"))
	s.mux.HandleFunc("GET /manifest.webmanifest", s.handleAsset("manifest.webmanifest"))
	s.mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	s.mux.HandleFunc("GET /p", s.handleP)
	s.mux.HandleFunc("GET /probe/{size}", s.handleProbe)
	s.mux.HandleFunc("POST /o", s.handleObserve)
	s.mux.HandleFunc("POST /o/update", s.handleUpdate)
	s.mux.HandleFunc("POST /sync", s.handleSync)
	s.mux.HandleFunc("GET /cells", s.handleCells)
	s.mux.HandleFunc("GET /sites", s.handleSites)
	s.mux.HandleFunc("GET /coverage", s.handleCoverage)
	s.mux.HandleFunc("GET /coverage/sites", s.handleCoverageSites)
	s.mux.HandleFunc("GET /resources", s.handleResources)
	s.mux.HandleFunc("POST /report", s.handleReport)
	s.mux.HandleFunc("POST /resources/update-details", s.handleUpdateResourceDetails)
	s.mux.HandleFunc("POST /resources/moderate", s.handleModerateResource)
	s.mux.HandleFunc("GET /api/sismos", s.handleSismosProxy)
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
}

func (s *Server) Handler() http.Handler {
	return bodyLimit(s.cfg.MaxBodyBytes)(s.middleware(s.mux))
}

// allow verifica rate limit por IP + bucket. La IP vive solo en memoria.
func (s *Server) allow(bucket string, r *http.Request) bool {
	ip, ok := clientIP(r, s.cfg.TrustedProxies)
	if !ok {
		return true
	}
	if s.bucketUsesFingerprint(bucket) {
		if fp := clientFingerprint(r); fp != "" {
			if !s.fp.allow(ip.String(), fp) {
				return false
			}
		}
	}
	if rl, ok := s.limits[bucket]; ok {
		key := bucket + ":" + ip.String()
		if sid := clientSessionID(r); sid != "" {
			key += ":" + sid
		}
		return rl.allow(key)
	}
	return true
}

func (s *Server) bucketUsesFingerprint(bucket string) bool {
	switch bucket {
	case "/o", "/sync", "/update", "/report":
		return true
	default:
		return false
	}
}

func clientFingerprint(r *http.Request) string {
	fp := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Client-Fingerprint")))
	if len(fp) != 8 {
		return ""
	}
	for _, c := range fp {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return fp
}

func (s *Server) invalidateCaches() {
	s.cache.clear()
}

func (s *Server) adminAllowed(r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("ADMIN_KEY"))
	if expected == "" {
		return false
	}
	token := strings.TrimSpace(r.Header.Get("X-Admin-Key"))
	return token != "" && token == expected
}

func (s *Server) serveCachedJSON(w http.ResponseWriter, r *http.Request, key string, ttl time.Duration, cacheControl string, compute func() ([]byte, int, error)) (bool, int) {
	if item, ok := s.cache.get(key); ok {
		writeCachedJSON(w, r, item)
		return true, 0
	}
	body, status, err := compute()
	if err != nil {
		return false, status
	}
	item := cacheEntry(body, cacheControl, ttl)
	s.cache.set(key, item)
	writeCachedJSON(w, r, item)
	return true, 0
}

// ---- Sonda / probes ----

func (s *Server) handleP(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/p", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/probe", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	size := r.PathValue("size")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	switch size {
	case "1k":
		w.Write(s.probe1k)
	case "4k":
		w.Write(s.probe4k)
	default:
		http.Error(w, "bad probe", http.StatusNotFound)
	}
}

// ---- Observaciones ----

func (s *Server) handleObserve(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/o", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	payload, err := decodePayload(r)
	if err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	v, err := observe.ValidatePayload(payload, time.Now())
	if err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	obs := s.buildObservation(r, v)
	id, err := s.store.InsertObservation(r.Context(), obs)
	if err != nil {
		slog.Error("insert observation", "err", err)
		http.Error(w, "storage error", http.StatusServiceUnavailable)
		return
	}
	s.invalidateCaches()
	if v.WantID {
		w.Header().Set("X-Obs-ID", strconv.FormatInt(id, 10))
		if obs.Operator != "" && obs.Operator != observe.OpDesconocido {
			w.Header().Set("X-Obs-Op", obs.Operator)
		}
		w.WriteHeader(http.StatusCreated)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/update", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	var body struct {
		ID         int64   `json:"id"`
		CallSignal *string `json:"c"`
		Operator   *string `json:"op"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	var callSignal, operatorUser *string
	if body.CallSignal != nil {
		c := strings.ToLower(strings.TrimSpace(*body.CallSignal))
		if c != "yes" && c != "no" && c != "unknown" {
			http.Error(w, "bad call signal", http.StatusBadRequest)
			return
		}
		callSignal = &c
	}
	if body.Operator != nil {
		op := observe.NormalizeOperator(*body.Operator)
		if op != observe.OpDesconocido {
			operatorUser = &op
		}
	}
	if err := s.store.UpdateObservation(r.Context(), body.ID, callSignal, operatorUser); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.invalidateCaches()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/sync", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	var raws []json.RawMessage
	if err := dec.Decode(&raws); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if len(raws) > s.cfg.MaxSyncItems {
		http.Error(w, "too many items", http.StatusRequestEntityTooLarge)
		return
	}
	now := time.Now()
	obs := make([]store.Observation, 0, len(raws))
	for _, raw := range raws {
		var p observe.Payload
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		v, err := observe.ValidatePayload(&p, now)
		if err != nil {
			continue
		}
		obs = append(obs, s.buildObservation(r, v))
	}
	if len(obs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.InsertObservations(r.Context(), obs); err != nil {
		slog.Error("sync insert", "err", err)
		http.Error(w, "storage error", http.StatusServiceUnavailable)
		return
	}
	s.invalidateCaches()
	w.WriteHeader(http.StatusNoContent)
}

// buildObservation convierte el payload validado en una observación
// con los datos derivados por el servidor (ASN, operador, celda H3).
func (s *Server) buildObservation(r *http.Request, v observe.Validation) store.Observation {
	o := store.Observation{
		ReceivedAt:      time.Now().UTC(),
		ObservedAt:      v.ObservedAt.UTC(),
		Latitude:        v.Lat,
		Longitude:       v.Lon,
		Accuracy:        v.Accuracy,
		H3Cell:          s.cells.Cell(v.Lat, v.Lon),
		HttpRTTMedian:   v.RTTMedian,
		Jitter:          v.Jitter,
		SuccessRatio:    v.SuccessRatio,
		Samples:         v.Samples,
		FailedRequests:  v.Failed,
		EffectiveType:   v.EffectiveType,
		BrowserRTT:      v.BrowserRTT,
		BrowserDownlink: v.BrowserDownLk,
		SaveData:        v.SaveData,
		CallSignal:      v.CallSignal,
		OperatorUser:    v.OperatorUser,
		Probe1kMs:       v.Probe1kMs,
		Probe4kMs:       v.Probe4kMs,
		HttpRTTMin:      v.RTTMedian, // el cliente no envía min; mediana como referencia
	}
	if v.Probe1kMs >= 0 || v.Probe4kMs >= 0 {
		o.TransferEstimateBps = transferEstimate(v.Probe1kMs, v.Probe4kMs)
	}
	if ip, ok := clientIP(r, s.cfg.TrustedProxies); ok {
		o.ClientIP = ip.String()
		if info, found := s.asn.Lookup(ip); found {
			asnVal := info.ASN
			o.ASN = &asnVal
			o.Operator, o.Mobile, _ = s.ops.Resolve(asnVal)
			if o.Operator == observe.OpDesconocido && v.OperatorUser != "" {
				o.Operator = v.OperatorUser
			}
		}
	}
	if o.Operator == "" {
		o.Operator = observe.OpDesconocido
	}
	if o.OperatorUser != "" {
		o.Operator = o.OperatorUser
	}
	return o
}

// transferEstimate es una estimación EXTREMADAMENTE aproximada de transferencia
// (bits por segundo) a partir de la duración y bytes conocidos de los probes.
// NO es un speedtest convencional.
func transferEstimate(probe1kMs, probe4kMs float64) float64 {
	if probe4kMs > 0 {
		return 4096 * 8 / (probe4kMs / 1000)
	}
	if probe1kMs > 0 {
		return 1024 * 8 / (probe1kMs / 1000)
	}
	return 0
}

func decodePayload(r *http.Request) (*observe.Payload, error) {
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	var p observe.Payload
	if err := dec.Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ---- Agregación / mapa ----

type windowSpec struct {
	label  string
	dur    time.Duration
	maxAge int
}

var windows = map[string]windowSpec{
	"15m": {"15m", 15 * time.Minute, 60},
	"1h":  {"1h", time.Hour, 300},
	"3h":  {"3h", 3 * time.Hour, 600},
}

func (s *Server) handleCells(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/cells", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	f, win, ok := parseCellFilter(r, s.cfg)
	if !ok {
		http.Error(w, "bad bbox/operator/window", http.StatusBadRequest)
		return
	}
	key := cacheKey("cells",
		win.label,
		strconv.FormatFloat(f.MinLon, 'f', 6, 64),
		strconv.FormatFloat(f.MinLat, 'f', 6, 64),
		strconv.FormatFloat(f.MaxLon, 'f', 6, 64),
		strconv.FormatFloat(f.MaxLat, 'f', 6, 64),
		f.Operator,
	)
	cacheControl := fmt.Sprintf("public, max-age=%d", win.maxAge)
	if handled, status := s.serveCachedJSON(w, r, key, time.Duration(win.maxAge)*time.Second, cacheControl, func() ([]byte, int, error) {
		aggs, err := s.store.Cells(r.Context(), f)
		if err != nil {
			slog.Error("cells", "err", err)
			return nil, http.StatusServiceUnavailable, err
		}
		siteCounts, err := s.store.SitesByCell(r.Context())
		if err != nil {
			slog.Error("sitesByCell", "err", err)
			return nil, http.StatusServiceUnavailable, err
		}

		th := state.Thresholds{
			MinSamples:               s.cfg.State.MinSamples,
			HighConfidenceMinSamples: s.cfg.State.HighConfidenceMinSamples,
			OperativeMaxRTT:          s.cfg.State.OperativeMaxRTT,
			JitterElevated:           s.cfg.State.JitterElevated,
			DegradedMinRTT:           s.cfg.State.DegradedMinRTT,
			DegradedMaxSuccess:       s.cfg.State.DegradedMaxSuccess,
			ProbableAffectMinSamples: s.cfg.State.ProbableAffectMinSamples,
		}

		type cellJSON struct {
			H string  `json:"h"`
			X float64 `json:"x"`
			Y float64 `json:"y"`
			N int     `json:"n"`
			R float64 `json:"r"`
			J float64 `json:"j"`
			Q float64 `json:"q"`
			O string  `json:"o"`
			S string  `json:"s"`
			C string  `json:"c"`
			T int64   `json:"t"`
			P int     `json:"p"` // sitios móviles oficiales en la celda
		}

		out := make([]cellJSON, 0, len(aggs))
		for _, a := range aggs {
			lat, lon, err := s.cells.CellCenter(a.Cell)
			if err != nil {
				continue
			}
			res := state.Classify(state.Input{
				SampleCount:      a.Count,
				MedianRTT:        a.MedianRTT,
				Jitter:           a.MedianJitter,
				SuccessRatio:     a.SuccessRatio,
				BaselineExpected: siteCounts[a.Cell] > 0,
			}, th)
			out = append(out, cellJSON{
				H: a.Cell, X: round5(lon), Y: round5(lat), N: a.Count,
				R: round1(a.MedianRTT), J: round1(a.MedianJitter), Q: round3(a.SuccessRatio),
				O: a.TopOperator, S: res.State, C: res.Confidence,
				T: a.LastObserved.Unix(), P: siteCounts[a.Cell],
			})
		}
		body, err := json.Marshal(out)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return body, 0, nil
	}); handled {
		return
	} else if status != 0 {
		if status == http.StatusServiceUnavailable {
			http.Error(w, "storage error", status)
		} else {
			http.Error(w, "error", status)
		}
		return
	}
}

func (s *Server) handleSites(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/cells", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	f, _, ok := parseCellFilter(r, s.cfg)
	if !ok {
		http.Error(w, "bad bbox", http.StatusBadRequest)
		return
	}
	key := cacheKey("sites",
		strconv.FormatFloat(f.MinLon, 'f', 6, 64),
		strconv.FormatFloat(f.MinLat, 'f', 6, 64),
		strconv.FormatFloat(f.MaxLon, 'f', 6, 64),
		strconv.FormatFloat(f.MaxLat, 'f', 6, 64),
	)
	if handled, status := s.serveCachedJSON(w, r, key, time.Hour, "public, max-age=3600", func() ([]byte, int, error) {
		sites, err := s.store.Sites(r.Context(), f)
		if err != nil {
			return nil, http.StatusServiceUnavailable, err
		}
		type siteJSON struct {
			X  float64 `json:"x"`
			Y  float64 `json:"y"`
			O  string  `json:"o"`
			Nd string  `json:"nd"`
			Ad string  `json:"ad"`
		}
		out := make([]siteJSON, 0, len(sites))
		for _, st := range sites {
			out = append(out, siteJSON{X: round5(st.Lon), Y: round5(st.Lat), O: st.Operator,
				Nd: st.Neighborhood, Ad: st.Address})
		}
		body, err := json.Marshal(out)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return body, 0, nil
	}); handled {
		return
	} else if status != 0 {
		if status == http.StatusServiceUnavailable {
			http.Error(w, "storage error", status)
		} else {
			http.Error(w, "error", status)
		}
		return
	}
}

// parseCellFilter valida bbox/operator/window y devuelve un CellFilter.
func parseCellFilter(r *http.Request, cfg config.Config) (store.CellFilter, windowSpec, bool) {
	f := store.CellFilter{}
	win, ok := windows[r.URL.Query().Get("window")]
	if !ok {
		win = windows["15m"]
	}
	f.Window = win.dur

	if bbox := r.URL.Query().Get("bbox"); bbox != "" {
		parts := strings.Split(bbox, ",")
		if len(parts) != 4 {
			return f, win, false
		}
		vals := make([]float64, 4)
		for i, p := range parts {
			v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return f, win, false
			}
			vals[i] = v
		}
		f.MinLon, f.MinLat, f.MaxLon, f.MaxLat = vals[0], vals[1], vals[2], vals[3]
		if f.MinLon >= f.MaxLon || f.MinLat >= f.MaxLat {
			return f, win, false
		}
		// Recortar el bbox a los límites de Colombia: la app es solo-Colombia y
		// esto acota el área de consulta sin rechazar vistas amplias (zoom bajo).
		f.MinLon = math.Max(f.MinLon, -79.1)
		f.MinLat = math.Max(f.MinLat, -4.4)
		f.MaxLon = math.Min(f.MaxLon, -66.0)
		f.MaxLat = math.Min(f.MaxLat, 12.6)
		if f.MinLon >= f.MaxLon || f.MinLat >= f.MaxLat {
			return f, win, false
		}
	} else {
		// Default: Colombia continental.
		f.MinLon, f.MinLat, f.MaxLon, f.MaxLat = -79.1, -4.4, -66.0, 12.6
	}

	if op := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("operator"))); op != "" {
		switch op {
		case observe.OpClaro, observe.OpMovistar, observe.OpTigo, observe.OpWom, observe.OpOtro, observe.OpDesconocido:
			f.Operator = op
		default:
			return f, win, false
		}
	}
	return f, win, true
}

// handleCoverage expone el baseline oficial de cobertura municipal.
// Filtros: municipality (substring), operator, technology.
func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/cells", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	municipality := strings.TrimSpace(r.URL.Query().Get("municipality"))
	operator := strings.TrimSpace(r.URL.Query().Get("operator"))
	technology := strings.TrimSpace(r.URL.Query().Get("technology"))
	key := cacheKey("coverage", strings.ToLower(municipality), strings.ToLower(operator), strings.ToLower(technology))
	if handled, status := s.serveCachedJSON(w, r, key, time.Hour, "public, max-age=3600", func() ([]byte, int, error) {
		rows, err := s.store.Coverage(r.Context(), municipality, operator, technology)
		if err != nil {
			slog.Error("coverage", "err", err)
			return nil, http.StatusServiceUnavailable, err
		}
		body, err := json.Marshal(rows)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return body, 0, nil
	}); handled {
		return
	} else if status != 0 {
		if status == http.StatusServiceUnavailable {
			http.Error(w, "storage error", status)
		} else {
			http.Error(w, "error", status)
		}
		return
	}
}

// handleCoverageSites expone el número oficial de sitios por operador y municipio.
func (s *Server) handleCoverageSites(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/cells", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	municipality := strings.TrimSpace(r.URL.Query().Get("municipality"))
	key := cacheKey("coverage-sites", strings.ToLower(municipality))
	if handled, status := s.serveCachedJSON(w, r, key, time.Hour, "public, max-age=3600", func() ([]byte, int, error) {
		rows, err := s.store.OfficialSites(r.Context(), municipality)
		if err != nil {
			slog.Error("officialSites", "err", err)
			return nil, http.StatusServiceUnavailable, err
		}
		body, err := json.Marshal(rows)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return body, 0, nil
	}); handled {
		return
	} else if status != 0 {
		if status == http.StatusServiceUnavailable {
			http.Error(w, "storage error", status)
		} else {
			http.Error(w, "error", status)
		}
		return
	}
}

// ---- Recursos humanitarios ----

var resourceKinds = map[string]bool{
	"centro_acopio": true, "hospital": true, "refugio": true,
	"agua": true, "energia": true, "internet": true,
	"via_bloqueada": true, "afectacion_estructural": true,
	"olla_comunitaria": true,
}

func (s *Server) handleResources(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/cells", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	if kind != "" && !resourceKinds[kind] {
		http.Error(w, "bad kind", http.StatusBadRequest)
		return
	}
	f := store.CellFilter{}
	if bbox := r.URL.Query().Get("bbox"); bbox != "" {
		parts := strings.Split(bbox, ",")
		if len(parts) != 4 {
			http.Error(w, "bad bbox", http.StatusBadRequest)
			return
		}
		vals := make([]float64, 4)
		for i, p := range parts {
			v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				http.Error(w, "bad bbox", http.StatusBadRequest)
				return
			}
			vals[i] = v
		}
		f.MinLon, f.MinLat, f.MaxLon, f.MaxLat = vals[0], vals[1], vals[2], vals[3]
	}
	isAdmin := s.adminAllowed(r)
	if isAdmin {
		res, err := s.store.Resources(r.Context(), f, kind)
		if err != nil {
			http.Error(w, "storage error", http.StatusServiceUnavailable)
			return
		}
		body, err := json.Marshal(res)
		if err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(body)
		return
	}

	key := cacheKey("resources",
		kind,
		strconv.FormatFloat(f.MinLon, 'f', 6, 64),
		strconv.FormatFloat(f.MinLat, 'f', 6, 64),
		strconv.FormatFloat(f.MaxLon, 'f', 6, 64),
		strconv.FormatFloat(f.MaxLat, 'f', 6, 64),
		"public",
	)
	if handled, status := s.serveCachedJSON(w, r, key, time.Minute, "public, max-age=60", func() ([]byte, int, error) {
		res, err := s.store.Resources(r.Context(), f, kind)
		if err != nil {
			return nil, http.StatusServiceUnavailable, err
		}
		var filtered []store.Resource
		for _, resItem := range res {
			if resItem.Status == "approved" {
				filtered = append(filtered, resItem)
			}
		}
		body, err := json.Marshal(filtered)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return body, 0, nil
	}); handled {
		return
	} else if status != 0 {
		if status == http.StatusServiceUnavailable {
			http.Error(w, "storage error", status)
		} else {
			http.Error(w, "error", status)
		}
		return
	}
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/report", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	var body struct {
		Kind    string         `json:"kind"`
		Name    string         `json:"name"`
		Address string         `json:"address"`
		Phone   string         `json:"phone"`
		Lat     float64        `json:"lat"`
		Lon     float64        `json:"lon"`
		Details map[string]any `json:"details"`
		Nonce   string         `json:"nonce"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if !resourceKinds[body.Kind] {
		http.Error(w, "bad kind", http.StatusBadRequest)
		return
	}
	if body.Lat < -90 || body.Lat > 90 || body.Lon < -180 || body.Lon > 180 {
		http.Error(w, "bad coords", http.StatusBadRequest)
		return
	}

	// Validación de Proof of Work (PoW) local anti-spam
	if body.Nonce == "" {
		http.Error(w, "missing proof of work", http.StatusBadRequest)
		return
	}
	// Validar que SHA256(kind + name + nonce) empiece por "00"
	plain := body.Kind + body.Name + body.Nonce
	sum := sha256.Sum256([]byte(plain))
	hexStr := hex.EncodeToString(sum[:])
	if !strings.HasPrefix(hexStr, "00") {
		http.Error(w, "invalid proof of work", http.StatusBadRequest)
		return
	}

	id, err := s.store.InsertResource(r.Context(), store.Resource{
		Kind: body.Kind, Name: body.Name, Address: body.Address, Phone: body.Phone,
		Lat: body.Lat, Lon: body.Lon, Details: body.Details, Status: "pending",
	})
	if err != nil {
		http.Error(w, "storage error", http.StatusServiceUnavailable)
		return
	}
	s.invalidateCaches()
	w.Header().Set("X-Resource-ID", strconv.FormatInt(id, 10))
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleUpdateResourceDetails(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/update", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	var body struct {
		ID      int64          `json:"id"`
		Details map[string]any `json:"details"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	var isVote bool
	var voteType string
	if _, ok := body.Details["confirms"]; ok {
		isVote = true
		voteType = "confirm"
	} else if _, ok := body.Details["dismisses"]; ok {
		isVote = true
		voteType = "disprove"
	}

	if isVote {
		ipStr := ""
		if ip, ok := clientIP(r, s.cfg.TrustedProxies); ok {
			ipStr = ip.String()
		}
		ua := r.Header.Get("User-Agent")
		h := sha256.New()
		h.Write([]byte(ipStr + ua))
		fingerprint := fmt.Sprintf("%x", h.Sum(nil))

		// Try to record the vote
		inserted, err := s.store.InsertResourceValidation(r.Context(), body.ID, voteType, ipStr, ua, fingerprint)
		if err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		if !inserted {
			http.Error(w, "ya registraste una validación para este punto", http.StatusConflict)
			return
		}
	}

	err := s.store.UpdateResourceDetails(r.Context(), body.ID, body.Details)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.invalidateCaches()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleModerateResource(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/update", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	if !s.adminAllowed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		ID     int64  `json:"id"`
		Status string `json:"status"` // 'approved', 'rejected'
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if body.Status != "approved" && body.Status != "rejected" && body.Status != "pending" {
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	err := s.store.UpdateResourceStatus(r.Context(), body.ID, body.Status)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.invalidateCaches()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// ---- Páginas ----

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; connect-src 'self'; script-src 'self' 'unsafe-inline' https://unpkg.com; style-src 'self' 'unsafe-inline' https://unpkg.com; img-src 'self' data: blob: https://*.openstreetmap.org https://tile.openstreetmap.org https://*.tile.openstreetmap.org; object-src 'none'; base-uri 'self'; worker-src 'self' blob:")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(web.MapHTML)
}

func (s *Server) handleMap(w http.ResponseWriter, r *http.Request) {
	target := "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

func (s *Server) handleAsset(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch name {
		case "sw.js":
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Service-Worker-Allowed", "/")
		default:
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		data, ctype, ok := web.Asset(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveAsset(w, r, data, ctype)
	}
}

func serveAsset(w http.ResponseWriter, r *http.Request, data []byte, ctype string) {
	w.Header().Set("Content-Type", ctype)
	http.ServeContent(w, r, "asset", time.Now(), strings.NewReader(string(data)))
}

// round1/3/5 truncan a 1/3/5 decimales para reducir bytes.
func round1(v float64) float64 { return float64(int(v*10)) / 10 }
func round3(v float64) float64 { return float64(int(v*1000)) / 1000 }
func round5(v float64) float64 { return float64(int(v*100000)) / 100000 }

func (s *Server) handleSismosProxy(w http.ResponseWriter, r *http.Request) {
	muni := r.URL.Query().Get("muni")
	if muni == "" {
		muni = "27660"
	}
	url := fmt.Sprintf("https://srvags.sgc.gov.co/arcgis/rest/services/catalogo_sismos/catalogo_de_sismos_2/FeatureServer/0/query?where=MUN_CODIGO%%3D%%27%s%%27&outFields=ESP_MAGNITUD,ESP_PROFUNDIDAD,ESP_FECHA_LONG,ESP_LATITUD,ESP_LONGITUD&orderByFields=ESP_FECHA_LONG+DESC&resultRecordCount=15&f=json", muni)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Set standard user agent to avoid any potential blocks
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
