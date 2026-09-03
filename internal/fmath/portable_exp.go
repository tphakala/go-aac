// SPDX-License-Identifier: BSD-3-Clause
// Copyright 2009 The Go Authors. All rights reserved.
//
// Derived from Go 1.27.0 GOROOT/src/math/exp.go. Vendored so the encoder's
// transcendentals are architecture-deterministic: the standard library
// dispatches Exp/Exp2 to per-arch assembly (arm64 has archExp/archExp2, amd64
// has an AVX+FMA-conditional archExp), and gc contracts a*b+c into a fused
// multiply-add on arm64, so the same input yields different last-ulp results
// per arch (issues #59 and #79). The explicit float64(...) conversions below are
// rounding barriers that forbid contraction, matching the -ffp-contract=off
// reference. See PROVENANCE.md ("Go standard library derivations").
//
// The original C code and constants are from FreeBSD's e_exp.c (Sun
// Microsystems notice preserved in the Go source this is derived from).

package fmath

import "math"

// exp returns e**x. Barriered copy of math.exp. Unlike exp2, math.Exp runs
// per-arch assembly on BOTH arm64 and amd64 (amd64's path is additionally
// AVX+FMA-conditional), so this changes output on both arches to a single
// deterministic value; the differential oracle gate bounds the drift.
//
//go:noinline
func exp(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01
		Ln2Lo = 1.90821492927058770002e-10
		Log2e = 1.44269504088896338700e+00

		Overflow  = 7.09782712893383973096e+02
		Underflow = -7.45133219101941108420e+02
		NearZero  = 1.0 / (1 << 28) // 2**-28
	)

	// special cases
	switch {
	case math.IsNaN(x):
		return x
	case x > Overflow: // handles case where x is +Inf
		return math.Inf(1)
	case x < Underflow: // handles case where x is -Inf
		return 0
	case -NearZero < x && x < NearZero:
		return 1 + x
	}

	// reduce; computed as r = hi - lo for extra precision.
	var k int
	switch {
	case x < 0:
		k = int(float64(Log2e*x) - 0.5) // no cross-statement FMA
	case x > 0:
		k = int(float64(Log2e*x) + 0.5) // no cross-statement FMA
	}
	hi := x - float64(float64(k)*Ln2Hi) // no cross-statement FMA
	lo := float64(float64(k) * Ln2Lo)   // barriered; identity on amd64 (no FMA at v1)

	return expmulti(hi, lo, k)
}

// exp2 returns 2**x. Barriered copy of math.exp2 (the amd64 portable path);
// bit-identical to amd64's stdlib Exp2, deterministic across architectures.
//
//go:noinline
func exp2(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01
		Ln2Lo = 1.90821492927058770002e-10

		Overflow  = 1.0239999999999999e+03
		Underflow = -1.0740e+03
	)

	switch {
	case math.IsNaN(x):
		return x
	case x > Overflow: // handles case where x is +Inf
		return math.Inf(1)
	case x < Underflow: // handles case where x is -Inf
		return 0
	}

	// argument reduction; x = r*lg(e) + k with |r| <= ln(2)/2.
	// computed as r = hi - lo for extra precision.
	var k int
	switch {
	case x > 0:
		k = int(x + 0.5)
	case x < 0:
		k = int(x - 0.5)
	}
	t := x - float64(k)
	hi := float64(t * Ln2Hi)  // barriered; identity on amd64 (no FMA at v1)
	lo := float64(-t * Ln2Lo) // barriered; identity on amd64 (no FMA at v1)

	return expmulti(hi, lo, k)
}

// expmulti returns e**r * 2**k where r = hi - lo and |r| <= ln(2)/2. Barriered
// so the Remez polynomial evaluates with the same per-op rounding as the
// -ffp-contract=off reference (no cross-statement FMA).
//
//go:noinline
func expmulti(hi, lo float64, k int) float64 {
	const (
		P1 = 1.66666666666666657415e-01  /* 0x3FC55555; 0x55555555 */
		P2 = -2.77777777770155933842e-03 /* 0xBF66C16C; 0x16BEBD93 */
		P3 = 6.61375632143793436117e-05  /* 0x3F11566A; 0xAF25DE2C */
		P4 = -1.65339022054652515390e-06 /* 0xBEBBBD41; 0xC5D26BF1 */
		P5 = 4.13813679705723846039e-08  /* 0x3E663769; 0x72BEA4D0 */
	)

	r := hi - lo
	t := r * r
	// t*(P1+t*(P2+t*(P3+t*(P4+t*P5)))), Horner, each product rounded before the
	// add so gc cannot contract it into an FMA. // no cross-statement FMA
	poly := float64(t*P5) + P4
	poly = float64(t*poly) + P3
	poly = float64(t*poly) + P2
	poly = float64(t*poly) + P1
	poly = float64(t * poly)
	c := r - poly
	y := 1 - ((lo - float64(r*c)/(2-c)) - hi)
	return math.Ldexp(y, k)
}
