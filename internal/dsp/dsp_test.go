// SPDX-License-Identifier: LGPL-2.1-or-later
package dsp

import (
	"math"
	"testing"
)

func TestAbsPow34(t *testing.T) {
	in := []float32{0, 1, -1, 0.5, -2.25, 100}
	out := make([]float32, len(in))
	AbsPow34(out, in)
	for i, v := range in {
		want := math.Pow(math.Abs(float64(v)), 0.75)
		if math.Abs(float64(out[i])-want) > 1e-5*(want+1e-10) {
			t.Errorf("AbsPow34(%v) = %v, want %v", v, out[i], want)
		}
	}
}

func TestQuantizeBands(t *testing.T) {
	in := []float32{1, -1, 1, 1}
	scaled := []float32{0.9, 0.9, 5.0, 0.2}
	out := make([]int32, 4)
	// Q34 = 1, rounding = 0.4054, maxval = 3:
	// 0.9+0.4054 -> 1; signed negative -> -1; min(5.4054, 3) -> 3; 0.6054 -> 0
	QuantizeBands(out, in, scaled, true, 3, 1, 0.4054)
	want := []int32{1, -1, 3, 0}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("out[%d] = %d, want %d", i, out[i], want[i])
		}
	}
}

func TestLCGSequence(t *testing.T) {
	// First five values from the encoder seed 0x1f2e3d4c, computed during
	// planning from lcg_random (aacenc_utils.h @ d09d5afc3a).
	want := []int32{983586875, -1216483234, 885496869, -1301962944, 1447791007}
	l := LCGSeed
	for i, w := range want {
		if got := l.Next(); got != w {
			t.Fatalf("Next() #%d = %d, want %d", i, got, w)
		}
	}
}

func TestVectorFMulReverse(t *testing.T) {
	dst := make([]float32, 3)
	VectorFMulReverse(dst, []float32{1, 2, 3}, []float32{10, 20, 30})
	for i, w := range []float32{30, 40, 30} {
		if dst[i] != w {
			t.Errorf("dst[%d] = %v, want %v", i, dst[i], w)
		}
	}
}
