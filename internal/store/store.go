// Package store define el contrato de persistencia del sistema.
// La implementación SQL usa PostgreSQL/PostGIS; la implementación en
// memoria permite pruebas sin base de datos.
package store

import (
	"context"
	"time"
)

// Observation es la representación interna, con nombres claros.
// Se convierte inmediatamente desde el wire format compacto.
type Observation struct {
	ReceivedAt          time.Time
	ObservedAt          time.Time
	Latitude, Longitude float64
	Accuracy            float64
	H3Cell              string
	ASN                 *int
	Operator            string
	Mobile              bool
	HttpRTTMin          float64
	HttpRTTMedian       float64
	Jitter              float64
	SuccessRatio        float64
	Samples             int
	FailedRequests      int
	EffectiveType       string
	BrowserRTT          float64
	BrowserDownlink     float64
	SaveData            bool
	CallSignal          string
	OperatorUser        string
	Probe1kMs           float64
	Probe4kMs           float64
	TransferEstimateBps float64
}

type CellFilter struct {
	MinLat, MinLon float64
	MaxLat, MaxLon float64
	Operator       string
	Window         time.Duration
}

// CellAgg es un agregado espacial por celda H3.
type CellAgg struct {
	Cell          string
	Lat, Lon      float64
	Count         int
	MedianRTT     float64
	MedianJitter  float64
	SuccessRatio  float64
	TopOperator   string
	LastObserved  time.Time
	SiteCount     int
	CallYes       int
	CallNo        int
	CallUnknown   int
	EffectiveType map[string]int
}

type Site struct {
	Lat, Lon      float64
	Operator      string
	Source        string
	Neighborhood  string
	Address       string
	SourceDate    string
}

type Resource struct {
	ID         int64
	Kind       string
	Name       string
	Address    string
	Phone      string
	Lat, Lon   float64
	Details    map[string]any
	Status     string // 'pending', 'approved', 'rejected'
	ReportedAt time.Time
}

// CoverageRow es el snapshot oficial de cobertura reportada por
// operador/municipio/tecnología (último trimestre importado).
type CoverageRow struct {
	DaneCode     int
	Municipality string
	Technology   string
	SignalLevel  int
	AreaKM2      float64
	PctClaro     float64
	PctMovistar  float64
	PctTigo      float64
	PctWom       float64
}

// OfficialSitesRow es el número de sitios reportado por operador y
// municipio según la CRC (último trimestre importado). El CSV fuente
// tiene una fila por combinación tecnología×propio/coubicación; aquí se
// agrega por (municipio, operador): sitios = suma, tecnologías = OR.
type OfficialSitesRow struct {
	DaneCode     int
	Municipality string
	Operator     string
	Sites        int
	Tech2G       bool
	Tech3G       bool
	Tech4G       bool
	Tech5G       bool
}

type Store interface {
	InsertObservation(ctx context.Context, o Observation) (int64, error)
	UpdateObservation(ctx context.Context, id int64, callSignal, operatorUser *string) error
	InsertObservations(ctx context.Context, obs []Observation) error
	Cells(ctx context.Context, f CellFilter) ([]CellAgg, error)
	Sites(ctx context.Context, f CellFilter) ([]Site, error)
	SitesByCell(ctx context.Context) (map[string]int, error)
	Resources(ctx context.Context, f CellFilter, kind string) ([]Resource, error)
	InsertResource(ctx context.Context, r Resource) (int64, error)
	UpdateResourceStatus(ctx context.Context, id int64, status string) error
	UpdateResourceDetails(ctx context.Context, id int64, details map[string]any) error
	InsertResourceValidation(ctx context.Context, resourceID int64, voteType, ip, userAgent, fingerprint string) (bool, error)
	// Coverage y OfficialSites exponen el baseline oficial (municipal).
	// municipality se filtra como substring (case-insensitive); vacío = todo.
	Coverage(ctx context.Context, municipality, operator, technology string) ([]CoverageRow, error)
	OfficialSites(ctx context.Context, municipality string) ([]OfficialSitesRow, error)
}
