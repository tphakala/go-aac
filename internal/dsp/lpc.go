// SPDX-License-Identifier: LGPL-2.1-or-later
package dsp

import "math"

// MaxLPCOrder mirrors MAX_LPC_ORDER (libavcodec/lpc.h @ d09d5afc3a).
const MaxLPCOrder = 32

// Autocorr computes autocorrelation lags 0..maxLag of x into out.
// Mirrors libavcodec/lpc.c:lpc_compute_autocorr_c @ d09d5afc3a, including
// its bias: every lag sum is seeded with 1.0, not 0 (the C reads one zero
// sample of left padding for odd lags, which contributes nothing, so the
// per-lag accumulation order below is identical to the C's paired loop).
// Ranging over the lag-shifted sub-slice x[j:] pairs each element with x[i],
// which is the same product in the same order as the C's x[i]*x[i-j], but lets
// the compiler prove both indices: len(rem) <= len(x), so the loop body carries
// no bounds check at all (verified with -d=ssa/check_bce/debug=1).
func Autocorr(x []float64, maxLag int, out []float64) {
	out = out[:maxLag+1]
	for j := range maxLag + 1 {
		sum := 1.0
		if j <= len(x) {
			rem := x[j:]
			for i, xi := range rem {
				sum += xi * x[i]
			}
		}
		out[j] = sum
	}
}

// ComputeRefCoefs converts autocorrelation values to reflection coefficients
// with per-order prediction error. Mirrors libavcodec/lpc.c:compute_ref_coefs
// @ d09d5afc3a (float path: zero error denominators fall back to 1).
//
// errOut may be nil, as it is in the C (compute_ref_coefs is called with a
// NULL error pointer from ff_lpc_calc_ref_coefs).
func ComputeRefCoefs(autoc []float64, maxOrder int, ref, errOut []float64) {
	if maxOrder > MaxLPCOrder {
		panic("dsp: maxOrder exceeds MaxLPCOrder")
	}
	var gen0, gen1 [MaxLPCOrder]float64
	for i := range maxOrder {
		gen0[i] = autoc[i+1]
		gen1[i] = autoc[i+1]
	}
	err := autoc[0]
	den := err
	if den == 0 {
		den = 1
	}
	ref[0] = -gen1[0] / den
	err += gen1[0] * ref[0]
	if errOut != nil {
		errOut[0] = err
	}
	for i := 1; i < maxOrder; i++ {
		for j := range maxOrder - i {
			gen1[j] = gen1[j+1] + ref[i-1]*gen0[j]
			gen0[j] = gen1[j+1]*ref[i-1] + gen0[j]
		}
		den = err
		if den == 0 {
			den = 1
		}
		ref[i] = -gen1[0] / den
		err += gen1[0] * ref[i]
		if errOut != nil {
			errOut[i] = err
		}
	}
}

// RefToLPC converts reflection coefficients to direct-form LPC coefficients.
// Mirrors libavcodec/lpc_functions.h:compute_lpc_coefs @ d09d5afc3a with
// i=0, stride=0, fail=0, normalize=0, exactly as the TNS search calls it.
// The products r*b and r*f are rounded to float32 before the add, mirroring
// the C's explicit (LPC_TYPE_U) cast; without the conversions Go may fuse the
// mul+add into one FMA on arm64 and drift from the C oracle.
func RefToLPC(ref []float32, order int, lpc []float32) {
	ref = ref[:order]
	lpc = lpc[:order]
	for i := range order {
		r := -ref[i]
		lpc[i] = r
		for j := range (i + 1) >> 1 {
			f := lpc[j]
			b := lpc[i-1-j]
			lpc[j] = f + float32(r*b)
			lpc[i-1-j] = b + float32(r*f)
		}
	}
}

// CalcRefCoefs mirrors libavcodec/lpc.c:ff_lpc_calc_ref_coefs_f
// @ d09d5afc3a: optional Hann window (rectangular when applyWindow is
// false; TNS uses rectangular for short blocks), double-precision
// autocorrelation, reflection coefficients. Returns signal/avg_err
// prediction gain (NaN when avg_err is zero). scratch must hold
// len(samples) float64.
func CalcRefCoefs(samples []float32, order int, ref []float64, applyWindow bool, scratch []float64) float64 {
	n := len(samples)
	if order > MaxLPCOrder {
		panic("dsp: order exceeds MaxLPCOrder")
	}
	scratch = scratch[:n]
	for i := 0; i <= n/2; i++ {
		w := 1.0
		if applyWindow {
			w = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		}
		scratch[i] = w * float64(samples[i])
		scratch[n-1-i] = w * float64(samples[n-1-i])
	}
	var autoc [MaxLPCOrder + 1]float64
	Autocorr(scratch[:n], order, autoc[:order+1])
	signal := autoc[0]
	var errArr [MaxLPCOrder]float64
	ComputeRefCoefs(autoc[:], order, ref, errArr[:])
	avgErr := 0.0
	for i := range order {
		avgErr = (avgErr + errArr[i]) / 2
	}
	if avgErr == 0 {
		return math.NaN()
	}
	return signal / avgErr
}
