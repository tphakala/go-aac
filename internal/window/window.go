// SPDX-License-Identifier: LGPL-2.1-or-later

// Package window generates the AAC analysis half-windows at init time.
// Init-time code; may use package math directly (docs/go-design.md).
package window

import (
	"math"

	"github.com/tphakala/go-aac/internal/fmath"
)

// Computed once at init, like ff_aac_float_common_init
// (libavcodec/aactab.c:97-100 @ d09d5afc3a).
var (
	KBDLong1024 = KBD(4.0, 1024)
	KBDShort128 = KBD(6.0, 128)
	Sine1024    = Sine(1024)
	Sine128     = Sine(128)
)

// KBD returns the rising half of a Kaiser-Bessel derived window of length n.
// Mirrors libavcodec/kbdwin.c:kbd_window_init @ d09d5afc3a.
func KBD(alpha float64, n int) []float32 {
	a := alpha * math.Pi / float64(n)
	alpha2 := 4 * a * a
	temp := make([]float64, n/2+1)
	scale := 0.0
	for i := 0; i <= n/2; i++ {
		temp[i] = fmath.BesselI0(math.Sqrt(float64(i) * float64(n-i) * alpha2))
		m := 1.0
		if i > 0 && i < n/2 {
			m = 2.0
		}
		scale += temp[i] * m
	}
	scale = 1.0 / (scale + 1)
	w := make([]float32, n)
	sum := 0.0
	for i := 0; i <= n/2; i++ {
		sum += temp[i]
		w[i] = float32(math.Sqrt(sum * scale))
	}
	for i := n/2 + 1; i < n; i++ {
		sum += temp[n-i]
		w[i] = float32(math.Sqrt(sum * scale))
	}
	return w
}

// Sine returns the rising half of a sine window of length n.
// Mirrors libavcodec/sinewin_tablegen.h @ d09d5afc3a:
// w[i] = sin((i + 0.5) * pi / (2n)).
func Sine(n int) []float32 {
	w := make([]float32, n)
	for i := range n {
		w[i] = float32(math.Sin((float64(i) + 0.5) * math.Pi / (2 * float64(n))))
	}
	return w
}
