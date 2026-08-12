package observe

import "testing"

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{5}, 5},
		{[]float64{3, 1, 2}, 2},
		{[]float64{1, 2, 3, 4}, 2.5},
		{[]float64{10, 20, 30, 40, 50}, 30},
	}
	for _, c := range cases {
		if got := Median(c.in); got != c.want {
			t.Errorf("Median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestJitter(t *testing.T) {
	if got := Jitter(nil); got != 0 {
		t.Errorf("Jitter(nil) = %v", got)
	}
	if got := Jitter([]float64{10}); got != 0 {
		t.Errorf("Jitter(1 sample) = %v", got)
	}
	// 100, 120, 130 -> |20| + |10| = 30 / 2 = 15
	if got := Jitter([]float64{100, 120, 130}); got != 15 {
		t.Errorf("Jitter = %v, want 15", got)
	}
	// 200, 180 -> |20| / 1 = 20
	if got := Jitter([]float64{200, 180}); got != 20 {
		t.Errorf("Jitter = %v, want 20", got)
	}
}

func TestSuccessRatio(t *testing.T) {
	if got := SuccessRatio(0, 0); got != 0 {
		t.Errorf("SuccessRatio(0,0) = %v", got)
	}
	if got := SuccessRatio(3, 1); got != 0.75 {
		t.Errorf("SuccessRatio(3,1) = %v", got)
	}
	if got := SuccessRatio(0, 4); got != 0 {
		t.Errorf("SuccessRatio(0,4) = %v", got)
	}
}
