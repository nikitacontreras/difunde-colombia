// Package config centraliza toda la configuración del sistema.
// Cada dependencia, threshold y límite debe poder ajustarse por
// variables de entorno sin tocar código.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port string
	// DatabaseURL es la conexión PostgreSQL/PostGIS.
	DatabaseURL string
	// TrustedProxies son CIDRs cuyas cabeceras X-Forwarded-For / CF-Connecting-IP
	// pueden ser confiadas. Fuera de estos CIDRs, la IP se toma de RemoteAddr.
	TrustedProxies []*net.IPNet
	// H3Resolution para agregación espacial.
	H3Resolution int
	// MaxBodyBytes limita el cuerpo de las peticiones.
	MaxBodyBytes int64
	// MaxSyncItems limita el número de observaciones por POST /sync.
	MaxSyncItems int

	// AsnDB es la ruta opcional a un CSV local de IP -> ASN
	// (formato GeoLite2-ASN o start,end,asn,name,isp).
	AsnDB string
	// AsnMappingCSV es la ruta opcional a un CSV asn,operator,mobile,confidence,source.
	AsnMappingCSV string
	// AsnMappingFromDB carga los mappings desde la tabla asn_operator_mapping.
	AsnMappingFromDB bool

	// AllowOrigin para CORS (opcional; deployment preferido: same-origin).
	AllowOrigin string

	Probe ProbeConfig
	State StateConfig
	Rate  RateConfig
	HTTP  HTTPConfig
}

type ProbeConfig struct {
	// PTimeout es el timeout por probe GET /p.
	PTimeout time.Duration
	// ProbeTimeout timeout para /probe/1k y /probe/4k.
	ProbeTimeout time.Duration
	// InitialProbes cantidad de probes /p iniciales.
	InitialProbes int
	// MinSuccessRatioForAdaptive: por debajo de este ratio de éxito se detiene el probe adaptativo.
	MinSuccessRatioForAdaptive float64
	// Adaptive1kThreshold: si /probe/1k tarda más, se detiene.
	Adaptive1kThreshold time.Duration
	// Adaptive4kThreshold: si /probe/4k tarda más, se detiene.
	Adaptive4kThreshold time.Duration
}

type StateConfig struct {
	// MinSamples: por debajo -> SIN DATOS SUFICIENTES.
	MinSamples int
	// HighConfidenceMinSamples para confianza ALTA.
	HighConfidenceMinSamples int
	// OperativeMaxRTT ms para considerar OPERATIVO.
	OperativeMaxRTT float64
	// JitterElevated ms por encima del cual se considera degradación.
	JitterElevated float64
	// DegradedMinRTT ms por encima del cual se considera degradado.
	DegradedMinRTT float64
	// DegradedMaxSuccess: ratio por debajo del cual se considera degradado.
	DegradedMaxSuccess float64
	// ProbableAffectMinSamples: mínimo de muestras para declarar AFECTACIÓN PROBABLE.
	ProbableAffectMinSamples int
}

type RateConfig struct {
	// Limits por endpoint (requests por minuto por IP). Amplios por CGNAT.
	P       int
	Probe   int
	Observe int
	Sync    int
	Cells   int
	Update  int
	Report  int
}

type HTTPConfig struct {
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Port:             getenv("PORT", "8080"),
		DatabaseURL:      getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/colombia_difunde?sslmode=disable"),
		H3Resolution:     getenvInt("H3_RESOLUTION", 8),
		MaxBodyBytes:     getenvInt64("MAX_BODY_BYTES", 64*1024),
		MaxSyncItems:     getenvInt("MAX_SYNC_ITEMS", 100),
		AsnDB:            defaultAsnDB(),
		AsnMappingCSV:    os.Getenv("ASN_MAPPING_CSV"),
		AsnMappingFromDB: getenvBool("ASN_MAPPING_FROM_DB", true),
		AllowOrigin:      os.Getenv("ALLOW_ORIGIN"),
		Probe: ProbeConfig{
			PTimeout:                   getenvDuration("PROBE_P_TIMEOUT", 5*time.Second),
			ProbeTimeout:               getenvDuration("PROBE_TIMEOUT", 8*time.Second),
			InitialProbes:              getenvInt("PROBE_INITIAL_PROBES", 4),
			MinSuccessRatioForAdaptive: getenvFloat("PROBE_MIN_SUCCESS_RATIO", 0.4),
			Adaptive1kThreshold:        getenvDuration("PROBE_1K_THRESHOLD", 2*time.Second),
			Adaptive4kThreshold:        getenvDuration("PROBE_4K_THRESHOLD", 3*time.Second),
		},
		State: StateConfig{
			MinSamples:               getenvInt("STATE_MIN_SAMPLES", 3),
			HighConfidenceMinSamples: getenvInt("STATE_HIGH_CONF_SAMPLES", 10),
			OperativeMaxRTT:          getenvFloat("STATE_OPERATIVE_MAX_RTT_MS", 900),
			JitterElevated:           getenvFloat("STATE_JITTER_ELEVATED_MS", 400),
			DegradedMinRTT:           getenvFloat("STATE_DEGRADED_MIN_RTT_MS", 1500),
			DegradedMaxSuccess:       getenvFloat("STATE_DEGRADED_MAX_SUCCESS", 0.8),
			ProbableAffectMinSamples: getenvInt("STATE_PROBABLE_AFFECT_MIN_SAMPLES", 5),
		},
		Rate: RateConfig{
			P:       getenvInt("RATE_P_PER_MIN", 240),
			Probe:   getenvInt("RATE_PROBE_PER_MIN", 240),
			Observe: getenvInt("RATE_OBSERVE_PER_MIN", 60),
			Sync:    getenvInt("RATE_SYNC_PER_MIN", 20),
			Cells:   getenvInt("RATE_CELLS_PER_MIN", 120),
			Update:  getenvInt("RATE_UPDATE_PER_MIN", 60),
			Report:  getenvInt("RATE_REPORT_PER_MIN", 20),
		},
		HTTP: HTTPConfig{
			ReadTimeout:  getenvDuration("HTTP_READ_TIMEOUT", 10*time.Second),
			WriteTimeout: getenvDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:  getenvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		},
	}

	proxies, err := parseCIDRs(getenv("TRUSTED_PROXIES", "127.0.0.1/32,::1/128"))
	if err != nil {
		return cfg, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}
	cfg.TrustedProxies = proxies

	if cfg.H3Resolution < 4 || cfg.H3Resolution > 11 {
		return cfg, fmt.Errorf("H3_RESOLUTION fuera de rango [4,11]: %d", cfg.H3Resolution)
	}
	return cfg, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// defaultAsnDB devuelve la ruta de la base IP->ASN. Si ASN_DB no está
// definida y existe la base filtrada para Colombia, la usa por defecto.
func defaultAsnDB() string {
	if v := os.Getenv("ASN_DB"); v != "" {
		return v
	}
	const def = "data/asn/ip2asn-co.csv"
	if _, err := os.Stat(def); err == nil {
		return def
	}
	return ""
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvInt64(k string, def int64) int64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func getenvFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func getenvBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func getenvDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func parseCIDRs(list string) ([]*net.IPNet, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	var out []*net.IPNet
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(part)
		if err != nil {
			// Permitir una IP simple como /32.
			ip := net.ParseIP(part)
			if ip == nil {
				return nil, fmt.Errorf("CIDR inválido %q", part)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			ipnet = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
		}
		out = append(out, ipnet)
	}
	return out, nil
}
