package state

import "testing"

func TestClassifyOperative(t *testing.T) {
	r := Classify(Input{
		SampleCount: 12, MedianRTT: 250, Jitter: 50, SuccessRatio: 0.97,
		BaselineExpected: true,
	}, DefaultThresholds())
	if r.State != Operativo {
		t.Errorf("State = %s, want OPERATIVO (%v)", r.State, r.Reasons)
	}
	if r.Confidence != ConfAlta {
		t.Errorf("Confidence = %s, want ALTA", r.Confidence)
	}
}

func TestClassifyDegraded(t *testing.T) {
	// RTT alto + success bajo, pero sin baseline -> DEGRADADO.
	r := Classify(Input{
		SampleCount: 20, MedianRTT: 2000, Jitter: 700, SuccessRatio: 0.5,
		BaselineExpected: false,
	}, DefaultThresholds())
	if r.State != Degradado {
		t.Errorf("State = %s, want DEGRADADO (%v)", r.State, r.Reasons)
	}
}

func TestClassifyAfectacionProbable(t *testing.T) {
	// Degradación severa + baseline espera servicio -> AFECTACION_PROBABLE.
	r := Classify(Input{
		SampleCount: 31, MedianRTT: 2200, Jitter: 900, SuccessRatio: 0.3,
		BaselineExpected: true,
	}, DefaultThresholds())
	if r.State != AfectacionProbable {
		t.Errorf("State = %s, want AFECTACION_PROBABLE (%v)", r.State, r.Reasons)
	}
	if r.Confidence != ConfAlta {
		t.Errorf("Confidence = %s, want ALTA", r.Confidence)
	}
}

func TestClassifySinDatos(t *testing.T) {
	r := Classify(Input{SampleCount: 0}, DefaultThresholds())
	if r.State != SinDatos {
		t.Errorf("State = %s, want SIN_DATOS", r.State)
	}
	r = Classify(Input{SampleCount: 1, MedianRTT: 100, SuccessRatio: 1}, DefaultThresholds())
	if r.State != SinDatos {
		t.Errorf("State(1 muestra) = %s, want SIN_DATOS", r.State)
	}
	if r.Confidence != ConfBaja {
		t.Errorf("Confidence(1 muestra) = %s, want BAJA", r.Confidence)
	}
}

func TestClassifyAfectacionRequiereMinSamples(t *testing.T) {
	// Degradación severa + baseline pero muy pocas muestras -> DEGRADADO,
	// no AFECTACION_PROBABLE (evita alarmas con 1 observación).
	r := Classify(Input{
		SampleCount: 3, MedianRTT: 2000, Jitter: 900, SuccessRatio: 0.3,
		BaselineExpected: true,
	}, DefaultThresholds())
	if r.State != Degradado {
		t.Errorf("State = %s, want DEGRADADO (muestras insuficientes para probable)", r.State)
	}
}

func TestClassifyBaselineRequiredForProbable(t *testing.T) {
	// Mismas métricas pero SIN baseline -> DEGRADADO, nunca PROBABLE.
	th := DefaultThresholds()
	r := Classify(Input{
		SampleCount: 20, MedianRTT: 2000, Jitter: 900, SuccessRatio: 0.3,
		BaselineExpected: false,
	}, th)
	if r.State != Degradado {
		t.Errorf("State = %s, want DEGRADADO sin baseline", r.State)
	}
}
