package observe

import (
	"fmt"
	"strings"
	"time"
)

// Compact wire payload (POST /o). Campos compactos para ahorrar bytes
// en redes 2G/3G. Se convierte inmediatamente a Observation en el servidor.
//
//	x  latitud (grados)
//	y  longitud (grados)
//	a  precisión GPS (m)
//	r  mediana RTT HTTP (ms)
//	j  jitter (ms)
//	n  número de muestras RTT
//	ok requests exitosos
//	f  requests fallidos
//	q  ratio de éxito (0..1)
//	e  effectiveType (navigator.connection, opcional)
//	br browser rtt (ms, opcional)
//	bd browser downlink (Mbps, opcional)
//	sd saveData (0/1, opcional)
//	c  señal para llamadas: "yes"|"no"|"unknown"|"" (opcional)
//	op operador reportado por el usuario (opcional, solo corroboración)
//	k1 duración /probe/1k (ms, -1 si no se ejecutó)
//	k4 duración /probe/4k (ms, -1 si no se ejecutó)
//	t  timestamp observado (epoch segundos)
//	u  solicitar id de seguimiento (0/1)
type Payload struct {
	Lat             float64 `json:"x"`
	Lon             float64 `json:"y"`
	Accuracy        float64 `json:"a"`
	RTTMedian       float64 `json:"r"`
	Jitter          float64 `json:"j"`
	Samples         int     `json:"n"`
	OK              int     `json:"ok"`
	Failed          int     `json:"f"`
	SuccessRatio    float64 `json:"q"`
	EffectiveType   string  `json:"e"`
	BrowserRTT      float64 `json:"br"`
	BrowserDownlink float64 `json:"bd"`
	SaveData        int     `json:"sd"`
	CallSignal      string  `json:"c"`
	OperatorUser    string  `json:"op"`
	Probe1kMs       float64 `json:"k1"`
	Probe4kMs       float64 `json:"k4"`
	ObservedAt      int64   `json:"t"`
	WantID          int     `json:"u"`
}

// Validation es el resultado ya validado y convertido.
type Validation struct {
	Lat, Lon, Accuracy        float64
	RTTMedian, Jitter         float64
	Samples, OK, Failed       int
	SuccessRatio              float64
	EffectiveType             string
	BrowserRTT, BrowserDownLk float64
	SaveData                  bool
	CallSignal                string
	OperatorUser              string
	Probe1kMs, Probe4kMs      float64
	ObservedAt                time.Time
	WantID                    bool
}

// ValidCallSignal tokens admitidos.
var validCallSignal = map[string]bool{"yes": true, "no": true, "unknown": true}

// ValidatePayload valida el payload compacto contra límites razonables.
// El cliente es tratado como NO confiable: lat/lon, precisiones, RTT, jitter,
// ratio, timestamps pasan por límites aquí. Los campos asn/operator/mobile
// NUNCA se aceptan del cliente (no existen en el wire format).
func ValidatePayload(p *Payload, now time.Time) (Validation, error) {
	v := Validation{}

	if p.Lat < -90 || p.Lat > 90 {
		return v, fmt.Errorf("latitud fuera de rango: %v", p.Lat)
	}
	if p.Lon < -180 || p.Lon > 180 {
		return v, fmt.Errorf("longitud fuera de rango: %v", p.Lon)
	}
	v.Lat, v.Lon = p.Lat, p.Lon

	if p.Accuracy < 0 || p.Accuracy > 10000 {
		return v, fmt.Errorf("precisión fuera de rango: %v", p.Accuracy)
	}
	v.Accuracy = p.Accuracy

	if p.RTTMedian < 0 || p.RTTMedian > 60000 {
		return v, fmt.Errorf("rtt mediana fuera de rango: %v", p.RTTMedian)
	}
	if p.Jitter < 0 || p.Jitter > 60000 {
		return v, fmt.Errorf("jitter fuera de rango: %v", p.Jitter)
	}
	v.RTTMedian, v.Jitter = p.RTTMedian, p.Jitter

	if p.Samples < 0 || p.Samples > 1000 {
		return v, fmt.Errorf("muestras fuera de rango: %v", p.Samples)
	}
	if p.OK < 0 || p.OK > 1000 {
		return v, fmt.Errorf("ok fuera de rango: %v", p.OK)
	}
	if p.Failed < 0 || p.Failed > 1000 {
		return v, fmt.Errorf("failed fuera de rango: %v", p.Failed)
	}
	v.Samples, v.OK, v.Failed = p.Samples, p.OK, p.Failed

	if p.SuccessRatio < 0 || p.SuccessRatio > 1 {
		return v, fmt.Errorf("success ratio fuera de rango: %v", p.SuccessRatio)
	}
	v.SuccessRatio = p.SuccessRatio
	if v.SuccessRatio == 0 && (v.OK+v.Failed) > 0 {
		v.SuccessRatio = SuccessRatio(v.OK, v.Failed)
	}

	if len(p.EffectiveType) > 12 {
		return v, fmt.Errorf("effectiveType inválido")
	}
	v.EffectiveType = strings.ToLower(strings.TrimSpace(p.EffectiveType))

	if p.BrowserRTT < 0 || p.BrowserRTT > 60000 {
		return v, fmt.Errorf("browser rtt fuera de rango: %v", p.BrowserRTT)
	}
	if p.BrowserDownlink < 0 || p.BrowserDownlink > 10000 {
		return v, fmt.Errorf("browser downlink fuera de rango: %v", p.BrowserDownlink)
	}
	v.BrowserRTT, v.BrowserDownLk = p.BrowserRTT, p.BrowserDownlink

	v.SaveData = p.SaveData != 0

	cs := strings.ToLower(strings.TrimSpace(p.CallSignal))
	if cs != "" && !validCallSignal[cs] {
		return v, fmt.Errorf("call signal inválido: %q", p.CallSignal)
	}
	v.CallSignal = cs

	if op := NormalizeOperator(p.OperatorUser); op != "desconocido" {
		v.OperatorUser = op
	}

	if p.Probe1kMs < -1 || p.Probe1kMs > 120000 {
		return v, fmt.Errorf("probe 1k fuera de rango")
	}
	if p.Probe4kMs < -1 || p.Probe4kMs > 120000 {
		return v, fmt.Errorf("probe 4k fuera de rango")
	}
	v.Probe1kMs, v.Probe4kMs = p.Probe1kMs, p.Probe4kMs

	// Timestamp observado: epoch segundos. Se permite antigüedad de hasta
	// maxAge (observaciones offline sincronizadas después), pero nunca en el
	// futuro más allá de un margen pequeño.
	const maxAge = 30 * 24 * time.Hour
	const futureSlack = 5 * time.Minute
	t := time.Unix(p.ObservedAt, 0).UTC()
	if p.ObservedAt == 0 {
		t = now.UTC()
	}
	if t.After(now.Add(futureSlack)) {
		return v, fmt.Errorf("timestamp en el futuro: %d", p.ObservedAt)
	}
	if now.Sub(t) > maxAge {
		return v, fmt.Errorf("timestamp demasiado antiguo: %d", p.ObservedAt)
	}
	v.ObservedAt = t

	v.WantID = p.WantID != 0
	return v, nil
}
