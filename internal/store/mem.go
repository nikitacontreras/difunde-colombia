package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"colombia-difunde/internal/observe"
)

// MemStore es un Store en memoria para pruebas y para arrancar
// sin PostgreSQL (modo degradado explícito).
type MemStore struct {
	mu          sync.Mutex
	obs         []Observation
	nextID      int64
	sites       []Site
	resources   []Resource
	coverage    []CoverageRow
	official    []OfficialSitesRow
	validations map[string]bool
	sismos      []SismoEvent
	subs        []PushSubscription
	settings    map[string]string
	synthMuni   []CoverageSynthesisRow
	synthCells  map[string][]CoverageCellRow
	synthMeta   *CoverageMeta
}

func NewMemStore() *MemStore {
	return &MemStore{
		validations: make(map[string]bool),
		settings:    make(map[string]string),
		synthCells:  make(map[string][]CoverageCellRow),
	}
}

func (m *MemStore) InsertObservation(ctx context.Context, o Observation) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	o.ID = m.nextID
	m.obs = append(m.obs, o)
	return m.nextID, nil
}

func (m *MemStore) InsertObservations(ctx context.Context, obs []Observation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range obs {
		m.nextID++
		o.ID = m.nextID
		m.obs = append(m.obs, o)
	}
	return nil
}

func (m *MemStore) UpdateObservation(ctx context.Context, id int64, callSignal, operatorUser *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.obs {
		if m.obs[i].ID == id {
			if callSignal != nil {
				m.obs[i].CallSignal = *callSignal
			}
			if operatorUser != nil {
				m.obs[i].OperatorUser = *operatorUser
			}
			return nil
		}
	}
	return fmt.Errorf("observación %d no encontrada", id)
}

func (m *MemStore) ObservationHistory(ctx context.Context, f ObservationHistoryFilter) (ObservationHistoryPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	match := func(o Observation) bool {
		if f.Operator != "" && !strings.EqualFold(o.Operator, f.Operator) {
			return false
		}
		if f.From != nil && o.ObservedAt.Before(*f.From) {
			return false
		}
		if f.To != nil && o.ObservedAt.After(*f.To) {
			return false
		}
		if q := strings.ToLower(strings.TrimSpace(f.Query)); q != "" {
			fields := []string{
				strings.ToLower(o.Operator),
				strings.ToLower(o.OperatorUser),
				strings.ToLower(o.H3Cell),
				strings.ToLower(o.ClientIP),
				strings.ToLower(o.CallSignal),
				strings.ToLower(o.EffectiveType),
			}
			found := false
			for _, field := range fields {
				if strings.Contains(field, q) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	var filtered []Observation
	for _, o := range m.obs {
		if match(o) {
			filtered = append(filtered, o)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].ObservedAt.Equal(filtered[j].ObservedAt) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].ObservedAt.After(filtered[j].ObservedAt)
	})

	total := len(filtered)
	start := f.Offset
	if start > total {
		start = total
	}
	end := start + f.Limit
	if end > total {
		end = total
	}

	items := make([]ObservationHistoryRow, 0, end-start)
	for _, o := range filtered[start:end] {
		items = append(items, observationToHistoryRow(o))
	}

	return ObservationHistoryPage{
		Items:  items,
		Total:  total,
		Limit:  f.Limit,
		Offset: start,
	}, nil
}

func (m *MemStore) AdminOverview(ctx context.Context) (AdminOverview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var latest *time.Time
	now := time.Now()
	seenOps := map[string]struct{}{}
	out := AdminOverview{}
	for _, o := range m.obs {
		out.ObservationsTotal++
		if o.ObservedAt.After(now.Add(-24 * time.Hour)) {
			out.Observations24h++
			if o.HttpRTTMedian >= 800 || o.SuccessRatio < 0.6 || o.CallSignal == "no" {
				out.ObservationsRisk24h++
			}
			if o.SaveData {
				out.ObservationsSaveData++
			}
		}
		if o.ObservedAt.After(now.Add(-7 * 24 * time.Hour)) {
			out.Observations7d++
		}
		if latest == nil || o.ObservedAt.After(*latest) {
			t := o.ObservedAt
			latest = &t
		}
		if strings.TrimSpace(o.Operator) != "" {
			seenOps[o.Operator] = struct{}{}
		}
	}
	for _, r := range m.resources {
		out.ResourcesTotal++
		if out.LatestResourceAt == nil || r.ReportedAt.After(*out.LatestResourceAt) {
			t := r.ReportedAt
			out.LatestResourceAt = &t
		}
		if r.LocationScope == "city" {
			out.ResourcesCityScope++
		} else {
			out.ResourcesPointScope++
		}
		if r.Kind == "logistica" {
			out.ResourcesLogistics++
		}
		if intent, _ := r.Details["intent"].(string); intent == "offer" {
			out.ResourcesOffers++
		} else if intent == "request" {
			out.ResourcesRequests++
		}
		switch strings.ToLower(strings.TrimSpace(r.Status)) {
		case "approved":
			out.ResourcesApproved++
		case "rejected":
			out.ResourcesRejected++
		default:
			out.ResourcesPending++
		}
	}
	out.ActiveOperatorsCount = len(seenOps)
	out.LatestObservationAt = latest
	return out, nil
}

func observationToHistoryRow(o Observation) ObservationHistoryRow {
	return ObservationHistoryRow{
		ID:                  o.ID,
		ReceivedAt:          o.ReceivedAt,
		ObservedAt:          o.ObservedAt,
		Latitude:            o.Latitude,
		Longitude:           o.Longitude,
		Accuracy:            o.Accuracy,
		H3Cell:              o.H3Cell,
		ASN:                 o.ASN,
		Operator:            o.Operator,
		Mobile:              o.Mobile,
		HttpRTTMin:          o.HttpRTTMin,
		HttpRTTMedian:       o.HttpRTTMedian,
		Jitter:              o.Jitter,
		SuccessRatio:        o.SuccessRatio,
		Samples:             o.Samples,
		FailedRequests:      o.FailedRequests,
		EffectiveType:       o.EffectiveType,
		BrowserRTT:          o.BrowserRTT,
		BrowserDownlink:     o.BrowserDownlink,
		SaveData:            o.SaveData,
		CallSignal:          o.CallSignal,
		OperatorUser:        o.OperatorUser,
		Probe1kMs:           o.Probe1kMs,
		Probe4kMs:           o.Probe4kMs,
		TransferEstimateBps: o.TransferEstimateBps,
		ClientIP:            o.ClientIP,
	}
}

func (m *MemStore) Cells(ctx context.Context, f CellFilter) ([]CellAgg, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	agg := map[string]*CellAgg{}
	var order []string
	cutoff := time.Now().Add(-f.Window)
	for _, o := range m.obs {
		if o.ObservedAt.Before(cutoff) {
			continue
		}
		if o.Latitude < f.MinLat || o.Latitude > f.MaxLat || o.Longitude < f.MinLon || o.Longitude > f.MaxLon {
			continue
		}
		if f.Operator != "" && o.Operator != f.Operator {
			continue
		}
		c, ok := agg[o.H3Cell]
		if !ok {
			c = &CellAgg{Cell: o.H3Cell, EffectiveType: map[string]int{}}
			agg[o.H3Cell] = c
			order = append(order, o.H3Cell)
		}
		c.Count++
		c.MedianRTT += o.HttpRTTMedian
		c.MedianJitter += o.Jitter
		c.SuccessRatio += o.SuccessRatio
		if o.ObservedAt.After(c.LastObserved) {
			c.LastObserved = o.ObservedAt
		}
		switch o.CallSignal {
		case "yes":
			c.CallYes++
		case "no":
			c.CallNo++
		case "unknown":
			c.CallUnknown++
		}
		if o.EffectiveType != "" {
			c.EffectiveType[o.EffectiveType]++
		}
		if c.TopOperator == "" {
			c.TopOperator = o.Operator
		}
	}
	out := make([]CellAgg, 0, len(order))
	for _, cell := range order {
		c := agg[cell]
		n := float64(c.Count)
		c.MedianRTT /= n
		c.MedianJitter /= n
		c.SuccessRatio /= n
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out, nil
}

func (m *MemStore) Sites(ctx context.Context, f CellFilter) ([]Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Site
	for _, s := range m.sites {
		if s.Lat >= f.MinLat && s.Lat <= f.MaxLat && s.Lon >= f.MinLon && s.Lon <= f.MaxLon {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *MemStore) SitesByCell(ctx context.Context) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	// Sin H3 en MemStore; no hay baseline por celda.
	return out, nil
}

func (m *MemStore) SetSites(sites []Site) { m.sites = sites }

func (m *MemStore) Resources(ctx context.Context, f CellFilter, kind string) ([]Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Resource
	for _, r := range m.resources {
		if kind != "" && r.Kind != kind {
			continue
		}
		hasBounds := f.MinLon != 0 || f.MinLat != 0 || f.MaxLon != 0 || f.MaxLat != 0
		if hasBounds && r.LocationScope != "city" &&
			(r.Lon < f.MinLon || r.Lon > f.MaxLon || r.Lat < f.MinLat || r.Lat > f.MaxLat) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (m *MemStore) ResourceCounts(ctx context.Context) (ResourceCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := ResourceCounts{ByKind: map[string]int{}}
	for _, r := range m.resources {
		switch r.Status {
		case "approved":
			out.Approved++
			out.ByKind[r.Kind]++
		case "pending":
			out.Pending++
		}
	}
	return out, nil
}

func (m *MemStore) InsertResource(ctx context.Context, r Resource) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	r.ID = m.nextID
	if r.Status == "" {
		r.Status = "pending"
	}
	if r.LocationScope == "" {
		r.LocationScope = "point"
	}
	if r.ReportedAt.IsZero() {
		r.ReportedAt = time.Now().UTC()
	}
	m.resources = append(m.resources, r)
	return r.ID, nil
}

func (m *MemStore) UpdateResource(ctx context.Context, r Resource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.resources {
		if m.resources[i].ID == r.ID {
			r.ReportedAt = m.resources[i].ReportedAt
			m.resources[i] = r
			return nil
		}
	}
	return errors.New("resource not found")
}

func (m *MemStore) UpdateResourceStatus(ctx context.Context, id int64, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.resources {
		if r.ID == id {
			m.resources[i].Status = status
			break
		}
	}
	return nil
}

func (m *MemStore) UpdateResourceDetails(ctx context.Context, id int64, details map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.resources {
		if r.ID == id {
			if m.resources[i].Details == nil {
				m.resources[i].Details = make(map[string]any)
			}
			for k, v := range details {
				m.resources[i].Details[k] = v
			}
			break
		}
	}
	return nil
}

func (m *MemStore) InsertResourceValidation(ctx context.Context, resourceID int64, voteType, ip, userAgent, fingerprint string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.validations == nil {
		m.validations = make(map[string]bool)
	}
	key := fmt.Sprintf("%d:%s", resourceID, fingerprint)
	if m.validations[key] {
		return false, nil
	}
	m.validations[key] = true
	return true, nil
}

func (m *MemStore) Coverage(ctx context.Context, municipality, operator, technology string) ([]CoverageRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CoverageRow
	for _, c := range m.coverage {
		if municipality != "" && !strings.Contains(strings.ToLower(c.Municipality), strings.ToLower(municipality)) {
			continue
		}
		if technology != "" && c.Technology != technology {
			continue
		}
		if operator != "" {
			if (operator == observe.OpClaro && c.PctClaro <= 0) ||
				(operator == observe.OpMovistar && c.PctMovistar <= 0) ||
				(operator == observe.OpTigo && c.PctTigo <= 0) ||
				(operator == observe.OpWom && c.PctWom <= 0) {
				continue
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func (m *MemStore) CoverageMeta(ctx context.Context) (*CoverageMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.synthMeta, nil
}

func (m *MemStore) CoverageSynthesis(ctx context.Context, daneCode string) ([]CoverageSynthesisRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CoverageSynthesisRow
	for _, r := range m.synthMuni {
		if r.DaneCode == daneCode {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemStore) CoveragePoint(ctx context.Context, h3Cell string) ([]CoverageCellRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.synthCells[h3Cell], nil
}

// SetSynthesis carga el snapshot de síntesis de cobertura en memoria
// (usado por pruebas y por el modo degradado sin PostgreSQL).
func (m *MemStore) SetSynthesis(rows []CoverageSynthesisRow, cells map[string][]CoverageCellRow, meta *CoverageMeta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.synthMuni = rows
	m.synthCells = cells
	m.synthMeta = meta
}

func (m *MemStore) OfficialSites(ctx context.Context, municipality string) ([]OfficialSitesRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OfficialSitesRow
	for _, r := range m.official {
		if municipality != "" && !strings.Contains(strings.ToLower(r.Municipality), strings.ToLower(municipality)) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// SetBaseline carga el baseline oficial en memoria (solo para pruebas).
func (m *MemStore) SetBaseline(coverage []CoverageRow, official []OfficialSitesRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coverage = coverage
	m.official = official
}

func (m *MemStore) InsertSismoEvents(ctx context.Context, events []SismoEvent) ([]SismoEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]bool, len(m.sismos))
	for _, e := range m.sismos {
		seen[e.ID] = true
	}
	var inserted []SismoEvent
	now := time.Now().UTC()
	for _, e := range events {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		e.DetectedAt = now
		m.sismos = append(m.sismos, e)
		inserted = append(inserted, e)
	}
	return inserted, nil
}

func (m *MemStore) RecentSismos(ctx context.Context, limit int) ([]SismoEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SismoEvent, len(m.sismos))
	copy(out, m.sismos)
	sort.Slice(out, func(i, j int) bool { return out[i].DetectedAt.After(out[j].DetectedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) UpsertPushSubscription(ctx context.Context, sub PushSubscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.subs {
		if s.Endpoint == sub.Endpoint {
			m.subs[i] = sub
			return nil
		}
	}
	m.subs = append(m.subs, sub)
	return nil
}

func (m *MemStore) ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PushSubscription, len(m.subs))
	copy(out, m.subs)
	return out, nil
}

func (m *MemStore) DeletePushSubscription(ctx context.Context, endpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.subs {
		if s.Endpoint == endpoint {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *MemStore) GetSetting(ctx context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.settings[key]
	return v, ok, nil
}

func (m *MemStore) SetSetting(ctx context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[key] = value
	return nil
}
