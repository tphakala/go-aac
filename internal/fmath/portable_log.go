// SPDX-License-Identifier: BSD-3-Clause
// Copyright 2009 The Go Authors. All rights reserved.
//
// Derived from Go 1.27.0 GOROOT/src/math/log.go and log10.go. Vendored so the
// encoder's logarithms are architecture-deterministic: the standard library
// runs archLog assembly on amd64 and a gc-contracted (FMADDD) portable path on
// arm64, so the same input yields different last-ulp results per arch (issue
// #59). The explicit float64(...) conversions below are rounding barriers that
// forbid contraction, matching the -ffp-contract=off reference. This changes
// output on both arches (amd64 leaves its asm path); the differential oracle
// gate bounds the drift. See PROVENANCE.md ("Go standard library derivations").
//
// The original C code and constants are from FreeBSD's e_log.c.

package fmath

import "math"

// log returns the natural logarithm of x. Barriered copy of math.log so the
// Remez polynomial and reconstruction evaluate with the same per-op rounding as
// the -ffp-contract=off reference (no cross-statement FMA).
//
//go:noinline
func log(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01 /* 3fe62e42 fee00000 */
		Ln2Lo = 1.90821492927058770002e-10 /* 3dea39ef 35793c76 */
		L1    = 6.666666666666735130e-01   /* 3FE55555 55555593 */
		L2    = 3.999999999940941908e-01   /* 3FD99999 9997FA04 */
		L3    = 2.857142874366239149e-01   /* 3FD24924 94229359 */
		L4    = 2.222219843214978396e-01   /* 3FCC71C5 1D8E78AF */
		L5    = 1.818357216161805012e-01   /* 3FC74664 96CB03DE */
		L6    = 1.531383769920937332e-01   /* 3FC39A09 D078C69F */
		L7    = 1.479819860511658591e-01   /* 3FC2F112 DF3E5244 */
	)

	// special cases
	switch {
	case math.IsNaN(x) || math.IsInf(x, 1):
		return x
	case x < 0:
		return math.NaN()
	case x == 0:
		return math.Inf(-1)
	}

	// reduce
	f1, ki := math.Frexp(x)
	if f1 < math.Sqrt2/2 {
		f1 *= 2
		ki--
	}
	f := f1 - 1
	k := float64(ki)

	// compute. Horner stages are barriered: L_i + s4*inner rounds the product
	// before the add so gc cannot contract it into an FMA. // no cross-statement FMA
	s := f / (2 + f)
	s2 := s * s
	s4 := s2 * s2
	t1 := float64(s4*L7) + L5
	t1 = float64(s4*t1) + L3
	t1 = float64(s4*t1) + L1
	t1 = float64(s2 * t1)
	t2 := float64(s4*L6) + L4
	t2 = float64(s4*t2) + L2
	t2 = float64(s4 * t2)
	R := t1 + t2
	hfsq := float64(0.5 * f * f) // barriered: hfsq feeds hfsq+R and hfsq-p below
	// k*Ln2Hi - ((hfsq - (s*(hfsq+R) + k*Ln2Lo)) - f), each product barriered.
	p := float64(s*(hfsq+R)) + float64(k*Ln2Lo)
	q := hfsq - p
	d := q - f
	return float64(k*Ln2Hi) - d
}

// log2 returns the base-2 logarithm of x. Barriered copy of math.log2 over the
// vendored log; the exact-power-of-two shortcut is retained so Log232 stays
// exact at powers of two (fmath_test pins Log232(8) == 3).
//
//go:noinline
func log2(x float64) float64 {
	frac, exp := math.Frexp(x)
	// Make sure exact powers of two give an exact answer.
	if frac == 0.5 {
		return float64(exp - 1)
	}
	return float64(log(frac)*(1/math.Ln2)) + float64(exp) // no cross-statement FMA
}
