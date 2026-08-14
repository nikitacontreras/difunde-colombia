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
	ID                  int64
	ReceivedAt          time.Time
	ObservedAt          time.Time
	ClientIP            string
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

// ObservationHistoryFilter controla la paginación y filtros del panel admin.
type ObservationHistoryFilter struct {
	Limit    int
	Offset   int
	Operator string
	Query    string
	From     *time.Time
	To       *time.Time
}

// ObservationHistoryRow es una fila lista para el panel admin.
type ObservationHistoryRow struct {
	ID                  int64     `json:"id"`
	ReceivedAt          time.Time `json:"received_at"`
	ObservedAt          time.Time `json:"observed_at"`
	Latitude            float64   `json:"latitude"`
	Longitude           float64   `json:"longitude"`
	Accuracy            float64   `json:"accuracy"`
	H3Cell              string    `json:"h3_cell"`
	ASN                 *int      `json:"asn,omitempty"`
	Operator            string    `json:"operator"`
	Mobile              bool      `json:"mobile"`
	HttpRTTMin          float64   `json:"http_rtt_min"`
	HttpRTTMedian       float64   `json:"http_rtt_median"`
	Jitter              float64   `json:"jitter"`
	SuccessRatio        float64   `json:"success_ratio"`
	Samples             int       `json:"samples"`
	FailedRequests      int       `json:"failed_requests"`
	EffectiveType       string    `json:"effective_type"`
	BrowserRTT          float64   `json:"browser_rtt"`
	BrowserDownlink     float64   `json:"browser_downlink"`
	SaveData            bool      `json:"save_data"`
	CallSignal          string    `json:"call_signal"`
	OperatorUser        string    `json:"operator_user"`
	Probe1kMs           float64   `json:"probe_1k_ms"`
	Probe4kMs           float64   `json:"probe_4k_ms"`
	TransferEstimateBps float64   `json:"transfer_estimate_bps"`
	ClientIP            string    `json:"client_ip"`
}

type ObservationHistoryPage struct {
	Items  []ObservationHistoryRow `json:"items"`
	Total  int                     `json:"total"`
	Limit  int                     `json:"limit"`
	Offset int                     `json:"offset"`
}

// AdminOverview resume el estado operacional para el panel admin.
type AdminOverview struct {
	ObservationsTotal    int        `json:"observations_total"`
	Observations24h      int        `json:"observations_24h"`
	Observations7d       int        `json:"observations_7d"`
	ObservationsRisk24h  int        `json:"observations_risk_24h"`
	ObservationsSaveData int        `json:"observations_save_data_24h"`
	LatestObservationAt  *time.Time `json:"latest_observation_at,omitempty"`
	ResourcesTotal       int        `json:"resources_total"`
	ResourcesPending     int        `json:"resources_pending"`
	ResourcesApproved    int        `json:"resources_approved"`
	ResourcesRejected    int        `json:"resources_rejected"`
	ResourcesCityScope   int        `json:"resources_city_scope"`
	ResourcesPointScope  int        `json:"resources_point_scope"`
	ResourcesLogistics   int        `json:"resources_logistics"`
	ResourcesOffers      int        `json:"resources_offers"`
	ResourcesRequests    int        `json:"resources_requests"`
	LatestResourceAt     *time.Time `json:"latest_resource_at,omitempty"`
	ActiveOperatorsCount int        `json:"active_operators_count"`
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
	Lat, Lon     float64
	Operator     string
	Source       string
	Neighborhood string
	Address      string
	SourceDate   string
}

type Resource struct {
	ID            int64
	Kind          string
	Name          string
	Address       string
	Phone         string
	Lat, Lon      float64
	LocationScope string // 'point' o 'city'
	Municipality  string
	Department    string
	Details       map[string]any
	Status        string // 'pending', 'approved', 'rejected'
	ReportedAt    time.Time
}

// ResourceCounts agrega los totales globales de recursos aprobados por
// kind y el número de pendientes; independiente del viewport del mapa.
type ResourceCounts struct {
	Approved int            `json:"approved"`
	Pending  int            `json:"pending,omitempty"`
	ByKind   map[string]int `json:"by_kind"`
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

// SismoEvent es un sismo del catálogo del SGC detectado por el polling.
type SismoEvent struct {
	ID         string    `json:"id"`
	Mag        float64   `json:"mag"`
	MagType    string    `json:"mag_type"`
	Depth      float64   `json:"depth"`
	Lat        float64   `json:"lat"`
	Lon        float64   `json:"lon"`
	Place      string    `json:"place"`
	LocalTime  string    `json:"local_time"`
	UTCTime    string    `json:"utc_time"`
	EventType  string    `json:"event_type"`
	Status     string    `json:"status"`
	DetectedAt time.Time `json:"detected_at"`
}

// CoverageSynthesisRow es la cobertura derivada de los mapas públicos de
// los operadores para un municipio/tecnología: fracción del área municipal
// cubierta según el mapa que cada operador publica (no es el baseline CRC).
type CoverageSynthesisRow struct {
	DaneCode     string  `json:"dane_code"`
	Department   string  `json:"department"`
	Municipality string  `json:"municipality"`
	Operator     string  `json:"operator"`
	Technology   string  `json:"technology"`
	CoveredRatio float64 `json:"covered_ratio"`
	CoveredKM2   float64 `json:"covered_km2"`
	AreaKM2      float64 `json:"area_km2"`
}

// CoverageCellRow es la cobertura declarada por un operador/tecnología en
// una celda H3 (res 7).
type CoverageCellRow struct {
	Operator   string `json:"operator"`
	Technology string `json:"technology"`
}

// CoverageMeta describe la última carga de síntesis de cobertura.
type CoverageMeta struct {
	GeneratedAt time.Time `json:"generated_at"`
	Source      string    `json:"source"`
	H3Res       int       `json:"h3_res"`
}

// PushSubscription es una suscripción Web Push (endpoint + claves).
type PushSubscription struct {
	Endpoint  string    `json:"endpoint"`
	P256DH    string    `json:"p256dh"`
	Auth      string    `json:"auth"`
	Device    string    `json:"device"`
	CreatedAt time.Time `json:"created_at"`
}

type Store interface {
	InsertObservation(ctx context.Context, o Observation) (int64, error)
	UpdateObservation(ctx context.Context, id int64, callSignal, operatorUser *string) error
	InsertObservations(ctx context.Context, obs []Observation) error
	ObservationHistory(ctx context.Context, f ObservationHistoryFilter) (ObservationHistoryPage, error)
	AdminOverview(ctx context.Context) (AdminOverview, error)
	Cells(ctx context.Context, f CellFilter) ([]CellAgg, error)
	Sites(ctx context.Context, f CellFilter) ([]Site, error)
	SitesByCell(ctx context.Context) (map[string]int, error)
	Resources(ctx context.Context, f CellFilter, kind string) ([]Resource, error)
	ResourceCounts(ctx context.Context) (ResourceCounts, error)
	InsertResource(ctx context.Context, r Resource) (int64, error)
	UpdateResource(ctx context.Context, r Resource) error
	UpdateResourceStatus(ctx context.Context, id int64, status string) error
	UpdateResourceDetails(ctx context.Context, id int64, details map[string]any) error
	InsertResourceValidation(ctx context.Context, resourceID int64, voteType, ip, userAgent, fingerprint string) (bool, error)
	// Coverage y OfficialSites exponen el baseline oficial (municipal).
	// municipality se filtra como substring (case-insensitive); vacío = todo.
	Coverage(ctx context.Context, municipality, operator, technology string) ([]CoverageRow, error)
	OfficialSites(ctx context.Context, municipality string) ([]OfficialSitesRow, error)

	// Síntesis de cobertura derivada de los mapas públicos de operadores.
	// CoverageSynthesis devuelve todas las filas de un municipio (dane_code).
	// CoveragePoint devuelve los operadores/tecnologías que declaran
	// cobertura en una celda H3 res 7.
	CoverageSynthesis(ctx context.Context, daneCode string) ([]CoverageSynthesisRow, error)
	CoveragePoint(ctx context.Context, h3Cell string) ([]CoverageCellRow, error)
	CoverageMeta(ctx context.Context) (*CoverageMeta, error)

	// Sismos y notificaciones push.
	// InsertSismoEvents guarda eventos nuevos (por id) y devuelve solo los insertados.
	InsertSismoEvents(ctx context.Context, events []SismoEvent) ([]SismoEvent, error)
	RecentSismos(ctx context.Context, limit int) ([]SismoEvent, error)
	UpsertPushSubscription(ctx context.Context, sub PushSubscription) error
	ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error)
	DeletePushSubscription(ctx context.Context, endpoint string) error
	GetSetting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}
