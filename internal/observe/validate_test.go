package observe

import (
	"strings"
	"testing"
	"time"
)

func validPayload() *Payload {
	return &Payload{
		Lat: 3.4516, Lon: -76.532, Accuracy: 18,
		RTTMedian: 284, Jitter: 61, Samples: 4, OK: 3, Failed: 1,
		SuccessRatio: 0.75, ObservedAt: time.Now().Unix(),
	}
}

func TestValidatePayloadOK(t *testing.T) {
	v, err := ValidatePayload(validPayload(), time.Now())
	if err != nil {
		t.Fatalf("payload válido rechazado: %v", err)
	}
	if v.Lat != 3.4516 || v.Lon != -76.532 {
		t.Errorf("coordenadas: %v %v", v.Lat, v.Lon)
	}
	if v.ObservedAt.IsZero() {
		t.Error("observed_at vacío")
	}
}

func TestValidatePayloadBadLatLon(t *testing.T) {
	for _, p := range []*Payload{
		{Lat: 91, Lon: 0, ObservedAt: time.Now().Unix()},
		{Lat: -91, Lon: 0, ObservedAt: time.Now().Unix()},
		{Lat: 0, Lon: 181, ObservedAt: time.Now().Unix()},
		{Lat: 0, Lon: -181, ObservedAt: time.Now().Unix()},
	} {
		if _, err := ValidatePayload(p, time.Now()); err == nil {
			t.Errorf("coordenada inválida aceptada: %+v", p)
		}
	}
}

func TestValidatePayloadRanges(t *testing.T) {
	cases := []func(*Payload){
		func(p *Payload) { p.Accuracy = 999999 },
		func(p *Payload) { p.RTTMedian = -1 },
		func(p *Payload) { p.Jitter = 999999 },
		func(p *Payload) { p.Samples = -1 },
		func(p *Payload) { p.SuccessRatio = 1.5 },
		func(p *Payload) { p.BrowserRTT = -5 },
		func(p *Payload) { p.BrowserDownlink = 99999 },
		func(p *Payload) { p.CallSignal = "talvez" },
		func(p *Payload) { p.EffectiveType = strings.Repeat("x", 20) },
		func(p *Payload) { p.Probe1kMs = -5 },
		func(p *Payload) { p.Probe4kMs = 9999999 },
	}
	for i, mut := range cases {
		p := validPayload()
		mut(p)
		if _, err := ValidatePayload(p, time.Now()); err == nil {
			t.Errorf("caso %d: valor inválido aceptado", i)
		}
	}
}

func TestValidatePayloadFutureTimestamp(t *testing.T) {
	p := validPayload()
	p.ObservedAt = time.Now().Add(10 * time.Minute).Unix()
	if _, err := ValidatePayload(p, time.Now()); err == nil {
		t.Error("timestamp futuro aceptado")
	}
}

func TestValidatePayloadOfflineTimestamp(t *testing.T) {
	// Una observación offline 2h antigua es válida y conserva su timestamp.
	now := time.Now()
	p := validPayload()
	p.ObservedAt = now.Add(-2 * time.Hour).Unix()
	v, err := ValidatePayload(p, now)
	if err != nil {
		t.Fatalf("timestamp offline rechazado: %v", err)
	}
	if want := p.ObservedAt; v.ObservedAt.Unix() != want {
		t.Errorf("observed_at perdido: %d != %d", v.ObservedAt.Unix(), want)
	}
}

func TestValidatePayloadTooOld(t *testing.T) {
	now := time.Now()
	p := validPayload()
	p.ObservedAt = now.Add(-31 * 24 * time.Hour).Unix()
	if _, err := ValidatePayload(p, now); err == nil {
		t.Error("timestamp demasiado antiguo aceptado")
	}
}

func TestValidatePayloadZeroTimestampUsesNow(t *testing.T) {
	now := time.Now()
	p := validPayload()
	p.ObservedAt = 0
	v, err := ValidatePayload(p, now)
	if err != nil {
		t.Fatalf("timestamp 0: %v", err)
	}
	if v.ObservedAt.Unix() != now.UTC().Unix() {
		t.Errorf("no usó now: %v", v.ObservedAt)
	}
}

func TestValidateSuccessRatioComputed(t *testing.T) {
	now := time.Now()
	p := validPayload()
	p.SuccessRatio = 0
	p.OK, p.Failed = 3, 1
	v, err := ValidatePayload(p, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v.SuccessRatio != 0.75 {
		t.Errorf("success ratio no computado: %v", v.SuccessRatio)
	}
}

func TestValidateOperatorUserNormalized(t *testing.T) {
	now := time.Now()
	p := validPayload()
	p.OperatorUser = "TIGO"
	v, err := ValidatePayload(p, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v.OperatorUser != "tigo" {
		t.Errorf("operador no normalizado: %q", v.OperatorUser)
	}
	// Valores basura sin marca (que caen en OpDesconocido) no deben pasar.
	p.OperatorUser = "AT&T"
	v, err = ValidatePayload(p, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v.OperatorUser != "" {
		t.Errorf("operador basura aceptado: %q", v.OperatorUser)
	}
}
