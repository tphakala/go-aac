// SPDX-License-Identifier: LGPL-2.1-or-later

package tx

import (
	"math"
	"testing"
)

// TestRescale pins the RESCALE edge semantics: the +1.0 endpoint saturates
// through av_clip64 (cos(0) in every table), the double->float narrowing
// happens before rounding, and ties round to even like llrintf under
// FE_TONEAREST.
func TestRescale(t *testing.T) {
	cases := []struct {
		x    float64
		want int32
	}{
		{1.0, math.MaxInt32},  // 2^31 clipped to 2^31-1
		{-1.0, math.MinInt32}, // exactly representable
		{0.0, 0},
		{0.5, 1 << 30},
		{3.0 / (1 << 32), 2},   // 1.5 in scaled units -> even 2
		{5.0 / (1 << 32), 2},   // 2.5 -> even 2
		{7.0 / (1 << 32), 4},   // 3.5 -> even 4
		{-3.0 / (1 << 32), -2}, // ties to even, negative side
	}
	for _, c := range cases {
		if got := rescale(c.x); got != c.want {
			t.Errorf("rescale(%v) = %d, want %d", c.x, got, c.want)
		}
	}
}

// TestSplitRadixMapProperties checks the gather revtab is a permutation for
// the two FFT sizes the decoder uses; the exact values are locked against
// the C's TXMAP dump by TestIMDCTDump in internal/dec.
func TestSplitRadixMapProperties(t *testing.T) {
	for _, n := range []int{64, 512} {
		m := ptwoRevtabGather(n, 1)
		seen := make([]bool, n)
		for i, v := range m {
			if v < 0 || v >= n {
				t.Fatalf("n=%d: map[%d] = %d out of range", n, i, v)
			}
			if seen[v] {
				t.Fatalf("n=%d: duplicate map value %d", n, v)
			}
			seen[v] = true
		}
	}
}

// TestIMDCTDeterminism pins that two runs of Transform over the same input
// produce identical output (no hidden state). Exact value checks against the
// C oracle live in internal/dec's IMDCT dump tests.
func TestIMDCTDeterminism(t *testing.T) {
	m := NewIMDCT(128, 1.0)
	in := make([]int32, 128)
	in[0] = 1 << 28
	in[64] = -3 << 20
	a := make([]int32, 128)
	b := make([]int32, 128)
	m.Transform(a, in)
	m.Transform(b, in)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("run-to-run mismatch at %d: %d vs %d", i, a[i], b[i])
		}
	}
}
