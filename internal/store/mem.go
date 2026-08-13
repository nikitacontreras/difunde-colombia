package store

import (
	"context"
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
}

func NewMemStore() *MemStore {
	return &MemStore{
		validations: make(map[string]bool),
	}
}

func (m *MemStore) InsertObservation(ctx context.Context, o Observation) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	m.obs = append(m.obs, o)
	return m.nextID, nil
}

func (m *MemStore) InsertObservations(ctx context.Context, obs []Observation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range obs {
		m.nextID++
		m.obs = append(m.obs, o)
	}
	return nil
}

func (m *MemStore) UpdateObservation(ctx context.Context, id int64, callSignal, operatorUser *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.obs {
		// En MemStore no se guarda el id; usar posición es impreciso.
		// Para pruebas se usa índice id-1.
		if int64(i) == id-1 {
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
		out = append(out, r)
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
	m.resources = append(m.resources, r)
	return r.ID, nil
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
