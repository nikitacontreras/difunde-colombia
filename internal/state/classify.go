// Package state clasifica el estado de conectividad de una zona
// con un score simple y EXPLICABLE. Sin machine learning.
package state

import "fmt"

// Estados posibles. NUNCA afirmar automáticamente que una antena está caída.
const (
	Operativo          = "OPERATIVO"
	Degradado          = "DEGRADADO"
	AfectacionProbable = "AFECTACION_PROBABLE"
	SinDatos           = "SIN_DATOS"
)

const (
	ConfAlta  = "ALTA"
	ConfMedia = "MEDIA"
	ConfBaja  = "BAJA"
)

type Result struct {
	State      string
	Confidence string
	Reasons    []string
}

// Input contiene los datos agregados de una celda más el baseline oficial.
type Input struct {
	SampleCount  int
	MedianRTT    float64
	Jitter       float64
	SuccessRatio float64
	// BaselineExpected indica que la cobertura oficial / infraestructura
	// cercana sugiere que DEBERÍA existir servicio en la zona.
	BaselineExpected bool
}

type Thresholds struct {
	MinSamples               int
	HighConfidenceMinSamples int
	OperativeMaxRTT          float64
	JitterElevated           float64
	DegradedMinRTT           float64
	DegradedMaxSuccess       float64
	ProbableAffectMinSamples int
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		MinSamples:               3,
		HighConfidenceMinSamples: 10,
		OperativeMaxRTT:          900,
		JitterElevated:           400,
		DegradedMinRTT:           1500,
		DegradedMaxSuccess:       0.8,
		ProbableAffectMinSamples: 5,
	}
}

// Classify devuelve el estado estimado. Reglas (centralizadas en Thresholds):
//
//	SinDatos:            muestras < MinSamples
//	Operativo:           success alto y RTT/jitter razonables
//	Degradado:           success reducido o RTT/jitter elevados
//	AfectacionProbable:  condiciones degradadas + baseline oficial espera servicio
//	                      + muestras suficientes
func Classify(in Input, t Thresholds) Result {
	if in.SampleCount == 0 {
		return Result{State: SinDatos, Reasons: []string{"sin observaciones"}}
	}
	if in.SampleCount < t.MinSamples {
		return Result{
			State:      SinDatos,
			Confidence: ConfBaja,
			Reasons:    []string{fmt.Sprintf("muestras insuficientes (%d < %d)", in.SampleCount, t.MinSamples)},
		}
	}

	reasons := []string{}
	successBad := in.SuccessRatio < t.DegradedMaxSuccess
	rttBad := in.MedianRTT > t.DegradedMinRTT
	jitterBad := in.Jitter > t.JitterElevated
	degraded := successBad || rttBad || jitterBad

	conf := confFor(in.SampleCount, t)
	if !degraded {
		return Result{State: Operativo, Confidence: conf, Reasons: append(reasons,
			fmt.Sprintf("success %.2f, rtt %.0fms, jitter %.0fms", in.SuccessRatio, in.MedianRTT, in.Jitter))}
	}

	if degraded {
		reasons = append(reasons, fmt.Sprintf("success %.2f (< %.2f), rtt %.0fms (> %.0fms), jitter %.0fms (> %.0fms)",
			in.SuccessRatio, t.DegradedMaxSuccess, in.MedianRTT, t.DegradedMinRTT, in.Jitter, t.JitterElevated))
	}

	if in.BaselineExpected && in.SampleCount >= t.ProbableAffectMinSamples {
		reasons = append(reasons, "baseline oficial indica cobertura esperada")
		return Result{State: AfectacionProbable, Confidence: conf, Reasons: reasons}
	}
	return Result{State: Degradado, Confidence: conf, Reasons: reasons}
}

func confFor(samples int, t Thresholds) string {
	switch {
	case samples >= t.HighConfidenceMinSamples:
		return ConfAlta
	case samples >= t.MinSamples:
		return ConfMedia
	default:
		return ConfBaja
	}
}
