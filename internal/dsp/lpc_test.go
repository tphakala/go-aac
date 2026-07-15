// SPDX-License-Identifier: LGPL-2.1-or-later
package dsp

import (
	"math"
	"testing"
)

func TestLPCSinePredictability(t *testing.T) {
	// A strongly periodic input must be highly predictable: large prediction
	// gain and a first reflection coefficient close to -cos(w).
	// Issue #10 expected RefToLPC to recover the exact sine recursion
	// coefficients [-2cos(w), +1]. That expectation is wrong for
	// ff_lpc_calc_ref_coefs_f: its unnormalized truncated autocorrelation
	// (r_j ~ (N-j)/2 cos(w j)) regularizes the second reflection coefficient
	// heavily. The real C @ d09d5afc3a returns ref = [-0.99409, 0.84422] for
	// this input, matching this port, so the theory values were never
	// reachable. Exact behavior is pinned by TestLPCGolden against C
	// fixtures; this test keeps the physics checks that do hold.
	const n, w, amp = 1024, 0.1, 1000
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = amp * float32(math.Sin(w*float64(i)))
	}
	ref := make([]float64, 2)
	scratch := make([]float64, n)
	gain := CalcRefCoefs(samples, 2, ref, false, scratch)
	if !(gain > 100) {
		t.Fatalf("prediction gain = %v, want > 100 for a pure sine", gain)
	}
	if d := math.Abs(ref[0] + math.Cos(w)); d > 2e-3 {
		t.Errorf("ref[0] = %v, want about %v (diff %g)", ref[0], -math.Cos(w), d)
	}
}

func TestRefToLPC(t *testing.T) {
	// Hand-computed from the compute_lpc_coefs recursion (exact in float32):
	// i=0: r = +0.5  -> lpc[0] = 0.5
	// i=1: r = -0.25 -> lpc[1] = -0.25; lpc[0] = 0.5 + (-0.25)*0.5 = 0.375
	ref := []float32{-0.5, 0.25}
	lpc := make([]float32, 2)
	RefToLPC(ref, 2, lpc)
	if lpc[0] != 0.375 || lpc[1] != -0.25 {
		t.Errorf("lpc = %v, want [0.375 -0.25]", lpc)
	}
}

func TestAutocorrMirrorsC(t *testing.T) {
	// FFmpeg's lpc_compute_autocorr_c seeds every lag sum with 1.0, so the
	// expected values carry a +1 bias vs the textbook autocorrelation.
	// Issue #10 expected 14 and 8 (no bias), which does not match the C
	// @ d09d5afc3a.
	x := []float64{1, 2, 3}
	out := make([]float64, 2)
	Autocorr(x, 1, out)
	if out[0] != 15 {
		t.Errorf("autoc[0] = %v, want 15 (1 + 1*1+2*2+3*3)", out[0])
	}
	if out[1] != 9 {
		t.Errorf("autoc[1] = %v, want 9 (1 + 1*2 + 2*3)", out[1])
	}
}
