// Package observe contiene estadísticas, validación y clasificación
// de operadores para las observaciones de conectividad.
package observe

import "sort"

// Median devuelve la mediana de las muestras (orden ascendente).
// Una muestra vacía devuelve 0.
func Median(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	cp := make([]float64, len(samples))
	copy(cp, samples)
	sort.Float64s(cp)
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

// Jitter devuelve la media de las diferencias absolutas entre muestras
// consecutivas exitosas (en ms). Es una métrica sencilla y documentada;
// NO equivale a ICMP ping jitter. Con menos de 2 muestras devuelve 0.
func Jitter(samples []float64) float64 {
	if len(samples) < 2 {
		return 0
	}
	var sum float64
	for i := 1; i < len(samples); i++ {
		d := samples[i] - samples[i-1]
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return sum / float64(len(samples)-1)
}

// SuccessRatio devuelve ok/(ok+fail); 0 si no hay muestras.
func SuccessRatio(ok, fail int) float64 {
	total := ok + fail
	if total == 0 {
		return 0
	}
	return float64(ok) / float64(total)
}
