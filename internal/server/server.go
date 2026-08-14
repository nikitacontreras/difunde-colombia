// Package server expone la API HTTP y sirve el frontend.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"colombia-difunde/internal/asn"
	"colombia-difunde/internal/config"
	coveragedata "colombia-difunde/internal/coverage"
	"colombia-difunde/internal/geo"
	"colombia-difunde/internal/observe"
	"colombia-difunde/internal/push"
	"colombia-difunde/internal/state"
	"colombia-difunde/internal/store"
	"colombia-difunde/web"
)

type Server struct {
	cfg      config.Config
	store    store.Store
	asn      asn.Resolver
	ops      *observe.OperatorResolver
	cells    geo.CellResolver
	coverage coveragedata.Catalog
	mux      *http.ServeMux
	limits   map[string]*rateLimiter
	cache    *responseCache
	fp       *fingerprintTracker

	probe1k []byte
	probe4k []byte

	keys *push.KeyProvider
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
			"/p":         newRateLimiter(cfg.Rate.P, time.Minute),
			"/probe":     newRateLimiter(cfg.Rate.Probe, time.Minute),
			"/o":         newRateLimiter(cfg.Rate.Observe, time.Minute),
			"/sync":      newRateLimiter(cfg.Rate.Sync, time.Minute),
			"/cells":     newRateLimiter(cfg.Rate.Cells, time.Minute),
			"/update":    newRateLimiter(cfg.Rate.Update, time.Minute),
			"/report":    newRateLimiter(cfg.Rate.Report, time.Minute),
			"/coverage":  newRateLimiter(120, time.Minute),
			"/subscribe": newRateLimiter(6, time.Minute),
			"/recent":    newRateLimiter(30, time.Minute),
		},
	}
	catalog, err := coveragedata.LoadCatalog("")
	if err != nil {
		slog.Warn("load coverage catalog", "err", err)
	}
	s.coverage = catalog
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
	s.mux.HandleFunc("GET /admin", s.handleAdmin)
	s.mux.HandleFunc("GET /admin/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusMovedPermanently)
	})
	s.mux.HandleFunc("POST /admin/session", s.handleAdminSession)
	s.mux.HandleFunc("POST /admin/logout", s.handleAdminLogout)
	s.mux.HandleFunc("GET /admin/api/overview", s.handleAdminOverview)
	s.mux.HandleFunc("GET /admin/api/observations", s.handleAdminObservations)
	s.mux.HandleFunc("GET /admin.css", s.handleAsset("admin.css"))
	s.mux.HandleFunc("GET /admin.js", s.handleAsset("admin.js"))
	s.mux.HandleFunc("GET /map", s.handleMap)
	s.mux.HandleFunc("GET /app.js", s.handleAsset("app.js"))
	s.mux.HandleFunc("GET /map.js", s.handleAsset("map.js"))
	s.mux.HandleFunc("GET /app.css", s.handleAsset("app.css"))
	s.mux.HandleFunc("GET /sw.js", s.handleAsset("sw.js"))
	s.mux.HandleFunc("GET /manifest.webmanifest", s.handleAsset("manifest.webmanifest"))
	s.mux.HandleFunc("GET /cobertura_municipios.geojson", s.handleAsset("cobertura_municipios.geojson"))
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
	s.mux.HandleFunc("GET /coverage/providers", s.handleCoverageProviders)
	s.mux.HandleFunc("GET /coverage/overlays", s.handleCoverageOverlays)
	s.mux.HandleFunc("GET /coverage/synthesis", s.handleCoverageSynthesis)
	s.mux.HandleFunc("GET /coverage/point", s.handleCoveragePoint)
	s.mux.HandleFunc("GET /coverage/status", s.handleCoverageStatus)
	s.mux.HandleFunc("GET /resources", s.handleResources)
	s.mux.HandleFunc("POST /report", s.handleReport)
	s.mux.HandleFunc("POST /resources/update-details", s.handleUpdateResourceDetails)
	s.mux.HandleFunc("POST /resources/moderate", s.handleModerateResource)
	s.mux.HandleFunc("GET /api/sismos", s.handleSismosProxy)
	s.mux.HandleFunc("GET /api/sismos/recent", s.handleSismosRecent)
	s.mux.HandleFunc("GET /api/push/vapid", s.handlePushVapid)
	s.mux.HandleFunc("POST /api/push/subscribe", s.handlePushSubscribe)
	s.mux.HandleFunc("POST /api/push/unsubscribe", s.handlePushUnsubscribe)
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

func adminHeaderValid(r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("ADMIN_KEY"))
	if expected == "" {
		return false
	}
	token := strings.TrimSpace(r.Header.Get("X-Admin-Key"))
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func (s *Server) invalidateCaches() {
	s.cache.clear()
}

func (s *Server) adminAllowed(r *http.Request) bool {
	return adminHeaderValid(r) || adminSessionValid(r)
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

// handleCoverageSynthesis expone la cobertura derivada de los mapas
// públicos de operadores para un municipio (dane_code).
func (s *Server) handleCoverageSynthesis(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/coverage", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	dane := strings.TrimSpace(r.URL.Query().Get("dane"))
	if dane == "" {
		http.Error(w, "falta dane", http.StatusBadRequest)
		return
	}
	key := cacheKey("coverage-synthesis", dane)
	if handled, status := s.serveCachedJSON(w, r, key, time.Hour, "public, max-age=3600", func() ([]byte, int, error) {
		rows, err := s.store.CoverageSynthesis(r.Context(), dane)
		if err != nil {
			slog.Error("coverageSynthesis", "err", err)
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
		http.Error(w, "storage error", status)
		return
	}
}

// handleCoveragePoint expone los operadores/tecnologías que declaran
// cobertura en un punto (lat/lon) según los mapas públicos de operadores.
func (s *Server) handleCoveragePoint(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/coverage", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	lat, errLat := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, errLon := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	if errLat != nil || errLon != nil || lat < -10 || lat > 20 || lon < -90 || lon > -60 {
		http.Error(w, "lat/lon inválidos", http.StatusBadRequest)
		return
	}
	cell := (geo.H3{Res: 7}).Cell(lat, lon)
	if cell == "" {
		http.Error(w, "celda inválida", http.StatusBadRequest)
		return
	}
	key := cacheKey("coverage-point", cell)
	if handled, status := s.serveCachedJSON(w, r, key, time.Hour, "public, max-age=3600", func() ([]byte, int, error) {
		rows, err := s.store.CoveragePoint(r.Context(), cell)
		if err != nil {
			slog.Error("coveragePoint", "err", err)
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
		http.Error(w, "storage error", status)
		return
	}
}

// handleCoverageStatus expone la fecha y fuente de la última carga de síntesis.
func (s *Server) handleCoverageStatus(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/coverage", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	key := cacheKey("coverage-status")
	if handled, status := s.serveCachedJSON(w, r, key, 5*time.Minute, "public, max-age=300", func() ([]byte, int, error) {
		meta, err := s.store.CoverageMeta(r.Context())
		if err != nil {
			slog.Error("coverageStatus", "err", err)
			return nil, http.StatusServiceUnavailable, err
		}
		body, err := json.Marshal(meta)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return body, 0, nil
	}); handled {
		return
	} else if status != 0 {
		http.Error(w, "storage error", status)
		return
	}
}

// handleCoverageProviders expone el catálogo normalizado de capas públicas.
func (s *Server) handleCoverageProviders(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/coverage", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	key := cacheKey("coverage-providers")
	if handled, status := s.serveCachedJSON(w, r, key, time.Hour, "public, max-age=3600", func() ([]byte, int, error) {
		body, err := json.Marshal(s.coverage)
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

// handleCoverageOverlays devuelve los overlays de Movistar que cruzan el bbox
// visible. El frontend lo usa para evitar traer todo el KML nacional.
func (s *Server) handleCoverageOverlays(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/coverage", r) {
		tooManyRequests(w, time.Minute)
		return
	}
	providerID := strings.TrimSpace(r.URL.Query().Get("provider"))
	technologyID := strings.TrimSpace(r.URL.Query().Get("technology"))
	if providerID == "" || technologyID == "" {
		http.Error(w, "bad provider/technology", http.StatusBadRequest)
		return
	}

	provider, ok := s.coverage.ProviderByID(providerID)
	if !ok {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	tech, ok := provider.TechnologyByID(technologyID)
	if !ok {
		http.Error(w, "technology not found", http.StatusNotFound)
		return
	}

	bbox := parseCoverageBBox(r.URL.Query().Get("bbox"))
	key := cacheKey(
		"coverage-overlays",
		strings.ToLower(provider.ID),
		strings.ToLower(tech.ID),
		strconv.FormatFloat(bbox.West, 'f', 4, 64),
		strconv.FormatFloat(bbox.South, 'f', 4, 64),
		strconv.FormatFloat(bbox.East, 'f', 4, 64),
		strconv.FormatFloat(bbox.North, 'f', 4, 64),
	)
	if handled, status := s.serveCachedJSON(w, r, key, time.Hour, "public, max-age=3600", func() ([]byte, int, error) {
		overlays := s.coverage.MovistarOverlays(tech.ID, bbox)
		resp := struct {
			Provider   string                 `json:"provider"`
			Technology string                 `json:"technology"`
			RenderType string                 `json:"render_type"`
			Count      int                    `json:"count"`
			BBox       coveragedata.BBox      `json:"bbox"`
			Overlays   []coveragedata.Overlay `json:"overlays"`
		}{
			Provider:   provider.ID,
			Technology: tech.ID,
			RenderType: tech.RenderType,
			Count:      len(overlays),
			BBox:       bbox,
			Overlays:   overlays,
		}
		body, err := json.Marshal(resp)
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

func parseCoverageBBox(raw string) coveragedata.BBox {
	// Default Colombia continental.
	bbox := coveragedata.BBox{West: -79.1, South: -4.4, East: -66.0, North: 12.6}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return bbox
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return bbox
	}
	vals := make([]float64, 4)
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return bbox
		}
		vals[i] = v
	}
	bbox.West, bbox.South, bbox.East, bbox.North = vals[0], vals[1], vals[2], vals[3]
	return bbox
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

// ---- Admin ----

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; connect-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(web.AdminHTML)
}

func (s *Server) handleAdminSession(w http.ResponseWriter, r *http.Request) {
	if !adminHeaderValid(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	setAdminSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	clearAdminSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	if !s.adminAllowed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	overview, err := s.store.AdminOverview(r.Context())
	if err != nil {
		slog.Error("admin overview", "err", err)
		http.Error(w, "storage error", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleAdminObservations(w http.ResponseWriter, r *http.Request) {
	if !s.adminAllowed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	limit := 50
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit"))); err == nil && v > 0 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("offset"))); err == nil && v >= 0 {
		offset = v
	}
	op := strings.TrimSpace(r.URL.Query().Get("operator"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	var fromPtr, toPtr *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			fromPtr = &ts
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			toPtr = &ts
		}
	}

	page, err := s.store.ObservationHistory(r.Context(), store.ObservationHistoryFilter{
		Limit:    limit,
		Offset:   offset,
		Operator: op,
		Query:    query,
		From:     fromPtr,
		To:       toPtr,
	})
	if err != nil {
		slog.Error("admin observations", "err", err)
		http.Error(w, "storage error", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
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

const sgcCatalogURL = "https://apicatalogador.sgc.gov.co/api/events/search/"

// sismoEvent es un sismo procesado del catálogo del SGC.
type sismoEvent struct {
	ID          string  `json:"id"`
	Mag         float64 `json:"mag"`
	MagType     string  `json:"mag_type"`
	Depth       float64 `json:"depth"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	LocalTime   string  `json:"local_time"`
	UTCTime     string  `json:"utc_time"`
	Place       string  `json:"place"`
	CloserTowns string  `json:"closer_towns"`
	Status      string  `json:"status"`
	EventType   string  `json:"event_type"`
	DistKm      int     `json:"dist_km,omitempty"`
}

type sismoResult struct {
	Count   int          `json:"count"`
	RadKm   float64      `json:"rad_km"`
	Days    int          `json:"days"`
	Center  [2]float64   `json:"center"`
	Updated string       `json:"updated_at"`
	Source  string       `json:"source"`
	Events  []sismoEvent `json:"events"`
}

// sismoCache guarda la última respuesta del proxy para no golpear el catálogo del SGC.
var sismoCache = struct {
	sync.Mutex
	key     string
	resp    []byte
	expires time.Time
}{}

// haversineKm devuelve la distancia en kilómetros entre dos puntos.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * R * math.Asin(math.Sqrt(a))
}

type sgcRawEvent struct {
	ID          string  `json:"id"`
	Status      string  `json:"status"`
	Place       string  `json:"place"`
	CloserTowns string  `json:"closer_towns"`
	LocalTime   string  `json:"local_time"`
	UTCTime     string  `json:"utc_time"`
	Magnitude   float64 `json:"magnitude"`
	MagType     string  `json:"mag_type"`
	Depth       float64 `json:"depth"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	EventType   string  `json:"event_type"`
}

type sgcSearchResp struct {
	Next    *string `json:"next"`
	Results struct {
		Results []sgcRawEvent `json:"results"`
	} `json:"results"`
}

// fetchSgcPage consulta una página del catálogo del SGC con el body dado.
func fetchSgcPage(ctx context.Context, body string, page int) (sgcSearchResp, error) {
	client := &http.Client{Timeout: 25 * time.Second}
	var sr sgcSearchResp
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s?page=%d", sgcCatalogURL, page), strings.NewReader(body))
	if err != nil {
		return sr, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return sr, err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	resp.Body.Close()
	if err != nil {
		return sr, err
	}
	if resp.StatusCode != http.StatusOK {
		return sr, fmt.Errorf("catálogo SGC respondió HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, &sr); err != nil {
		return sr, err
	}
	return sr, nil
}

func fetchSgcEvents(ctx context.Context, body string, rad, lat, lon float64) ([]sismoEvent, error) {
	var events []sismoEvent
	for page := 1; page <= 10; page++ {
		sr, err := fetchSgcPage(ctx, body, page)
		if err != nil {
			return nil, err
		}
		for _, e := range sr.Results.Results {
			d := haversineKm(lat, lon, e.Latitude, e.Longitude)
			if d <= rad {
				events = append(events, sismoEvent{
					ID:          e.ID,
					Mag:         round1(e.Magnitude),
					MagType:     e.MagType,
					Depth:       round1(e.Depth),
					Lat:         round3(e.Latitude),
					Lon:         round3(e.Longitude),
					LocalTime:   e.LocalTime,
					UTCTime:     e.UTCTime,
					Place:       e.Place,
					CloserTowns: e.CloserTowns,
					Status:      e.Status,
					EventType:   e.EventType,
					DistKm:      int(d),
				})
			}
		}
		if sr.Next == nil {
			break
		}
	}
	return events, nil
}

// fetchAllSgcEvents trae todos los eventos de la ventana sin filtro espacial
// (Colombia entera) para el polling de notificaciones.
func fetchAllSgcEvents(ctx context.Context, body string) ([]sgcRawEvent, error) {
	var out []sgcRawEvent
	for page := 1; page <= 10; page++ {
		sr, err := fetchSgcPage(ctx, body, page)
		if err != nil {
			return nil, err
		}
		out = append(out, sr.Results.Results...)
		if sr.Next == nil {
			break
		}
	}
	return out, nil
}

func (s *Server) handleSismosProxy(w http.ResponseWriter, r *http.Request) {
	lat, lon := 4.9, -76.25 // San José del Palmar
	rad, days := 90.0, 15
	if v, err := strconv.ParseFloat(r.URL.Query().Get("lat"), 64); err == nil {
		lat = v
	}
	if v, err := strconv.ParseFloat(r.URL.Query().Get("lon"), 64); err == nil {
		lon = v
	}
	if v, err := strconv.ParseFloat(r.URL.Query().Get("rad"), 64); err == nil && v > 0 {
		rad = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}

	key := fmt.Sprintf("%.4f|%.4f|%.0f|%d", lat, lon, rad, days)
	sismoCache.Lock()
	if sismoCache.key == key && time.Since(sismoCache.expires) < 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Write(sismoCache.resp)
		sismoCache.Unlock()
		return
	}
	sismoCache.Unlock()

	// El catálogo del SGC reporta horas locales de Colombia (UTC-5).
	nowLocal := time.Now().UTC().Add(-5 * time.Hour)
	before := nowLocal.Format("2006-01-02 15:04")
	after := nowLocal.Add(-time.Duration(days) * 24 * time.Hour).Format("2006-01-02 15:04")

	dLat := rad / 111.0
	dLon := rad / (111.0 * math.Cos(lat*math.Pi/180))
	body := fmt.Sprintf(`{"local_time_after":"%s","local_time_before":"%s","lat_min":%.5f,"lat_max":%.5f,"lon_min":%.5f,"lon_max":%.5f}`,
		after, before, lat-dLat, lat+dLat, lon-dLon, lon+dLon)

	events, err := fetchSgcEvents(r.Context(), body, rad, lat, lon)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	sort.Slice(events, func(i, j int) bool { return events[i].LocalTime > events[j].LocalTime })
	if len(events) > 200 {
		events = events[:200]
	}

	resp := sismoResult{
		Count:   len(events),
		RadKm:   rad,
		Days:    days,
		Center:  [2]float64{round3(lat), round3(lon)},
		Updated: nowLocal.Format("2006-01-02 15:04:05"),
		Source:  "Datos oficiales del Servicio Geológico Colombiano",
		Events:  events,
	}
	out, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sismoCache.Lock()
	sismoCache.key = key
	sismoCache.resp = out
	sismoCache.expires = time.Now().Add(60 * time.Second)
	sismoCache.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Write(out)
}

// SetupPush carga (o genera) las claves VAPID y las deja listas para el envío.
func (s *Server) SetupPush(ctx context.Context) error {
	if !s.cfg.PushEnabled {
		slog.Info("push deshabilitado")
		return nil
	}
	kp, err := push.LoadKeys(ctx, s.store, s.cfg.VAPIDPublicKey, s.cfg.VAPIDPrivateKey, s.cfg.VAPIDSubject)
	if err != nil {
		return err
	}
	s.keys = kp
	slog.Info("push listo", "vapid", s.keys.PublicKey()[:16]+"...")
	return nil
}

// StartSismoPolling arranca el polling del catálogo del SGC. El primer ciclo
// solo siembra el historial sin notificar para evitar una ráfaga al arrancar.
func (s *Server) StartSismoPolling(ctx context.Context) {
	go s.sismoPollLoop(ctx)
}

func (s *Server) sismoPollLoop(ctx context.Context) {
	interval := s.cfg.SismoPollInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	first := true
	for {
		if err := s.pollSismosOnce(ctx, first); err != nil {
			slog.Warn("poll sismos", "err", err)
		}
		first = false
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (s *Server) pollSismosOnce(ctx context.Context, seed bool) error {
	nowLocal := time.Now().UTC().Add(-5 * time.Hour)
	window := s.cfg.SismoWindow
	if window <= 0 {
		window = 6 * time.Hour
	}
	body := fmt.Sprintf(`{"local_time_after":"%s","local_time_before":"%s"}`,
		nowLocal.Add(-window).Format("2006-01-02 15:04"), nowLocal.Format("2006-01-02 15:04"))

	raw, err := fetchAllSgcEvents(ctx, body)
	if err != nil {
		return err
	}

	events := make([]store.SismoEvent, 0, len(raw))
	for _, e := range raw {
		events = append(events, store.SismoEvent{
			ID:        e.ID,
			Mag:       round1(e.Magnitude),
			MagType:   e.MagType,
			Depth:     round1(e.Depth),
			Lat:       round3(e.Latitude),
			Lon:       round3(e.Longitude),
			Place:     e.Place,
			LocalTime: e.LocalTime,
			UTCTime:   e.UTCTime,
			EventType: e.EventType,
			Status:    e.Status,
		})
	}

	inserted, err := s.store.InsertSismoEvents(ctx, events)
	if err != nil {
		return fmt.Errorf("guardar sismos: %w", err)
	}
	slog.Info("poll sismos", "nuevos", len(inserted), "total_ventana", len(events))
	if seed || len(inserted) == 0 {
		return nil
	}

	var toNotify []store.SismoEvent
	for _, e := range inserted {
		if e.Mag >= s.cfg.SismoMinMag {
			toNotify = append(toNotify, e)
		}
	}
	if len(toNotify) > 5 {
		toNotify = toNotify[:5]
	}
	go s.notifySismos(ctx, toNotify)
	return nil
}

type sismoNotification struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	URL       string  `json:"url"`
	Mag       float64 `json:"mag"`
	Place     string  `json:"place"`
	LocalTime string  `json:"local_time"`
}

// notifySismos envía una notificación por cada sismo nuevo a todos los suscritos.
func (s *Server) notifySismos(ctx context.Context, events []store.SismoEvent) error {
	if len(events) == 0 || s.keys == nil {
		return nil
	}
	subs, err := s.store.ListPushSubscriptions(ctx)
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return nil
	}
	for _, ev := range events {
		payload, err := json.Marshal(sismoNotification{
			ID:        ev.ID,
			Title:     fmt.Sprintf("Sismo M%.1f · %s", ev.Mag, shortPlace(ev.Place)),
			Body:      fmt.Sprintf("%s · Profundidad %.0f km", formatLocalDateTime(ev.LocalTime), ev.Depth),
			URL:       "/map",
			Mag:       ev.Mag,
			Place:     ev.Place,
			LocalTime: ev.LocalTime,
		})
		if err != nil {
			return err
		}
		for _, sub := range subs {
			res, err := s.keys.Send(sub, payload)
			if err != nil {
				slog.Warn("push falló", "err", err)
				continue
			}
			if res.Gone {
				if err := s.store.DeletePushSubscription(ctx, sub.Endpoint); err != nil {
					slog.Warn("borrar suscripción inválida", "err", err)
				}
			}
		}
	}
	return nil
}

// shortPlace recorta el lugar a un municipio legible para el título.
func shortPlace(place string) string {
	place = strings.TrimSpace(place)
	if i := strings.Index(place, ","); i > 0 {
		return place[:i]
	}
	if len(place) > 60 {
		return place[:60]
	}
	return place
}

var esMonths = [12]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}

// formatLocalDateTime convierte "2026-08-10 07:34:27" a "10 ago 2026, 07:34".
func formatLocalDateTime(s string) string {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return s
	}
	return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), esMonths[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}

func (s *Server) handlePushVapid(w http.ResponseWriter, r *http.Request) {
	if s.keys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notificaciones deshabilitadas"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.keys.PublicKey()})
}

type pushSubscribeReq struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Device string `json:"device"`
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/subscribe", r) {
		tooManyRequests(w, 30*time.Second)
		return
	}
	var req pushSubscribeReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "json inválido"})
		return
	}
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	if !strings.HasPrefix(req.Endpoint, "https://") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endpoint inválido"})
		return
	}
	if req.Keys.P256dh == "" || req.Keys.Auth == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "faltan claves de suscripción"})
		return
	}
	if err := s.store.UpsertPushSubscription(r.Context(), store.PushSubscription{
		Endpoint: req.Endpoint,
		P256DH:   req.Keys.P256dh,
		Auth:     req.Keys.Auth,
		Device:   truncate(req.Device, 120),
	}); err != nil {
		slog.Error("upsert push sub", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudo guardar la suscripción"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/subscribe", r) {
		tooManyRequests(w, 30*time.Second)
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "json inválido"})
		return
	}
	if req.Endpoint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endpoint inválido"})
		return
	}
	if err := s.store.DeletePushSubscription(r.Context(), req.Endpoint); err != nil {
		slog.Error("borrar push sub", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudo borrar la suscripción"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSismosRecent(w http.ResponseWriter, r *http.Request) {
	if !s.allow("/recent", r) {
		tooManyRequests(w, 30*time.Second)
		return
	}
	events, err := s.store.RecentSismos(r.Context(), 20)
	if err != nil {
		slog.Error("recent sismos", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudieron leer los sismos"})
		return
	}
	if events == nil {
		events = []store.SismoEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
