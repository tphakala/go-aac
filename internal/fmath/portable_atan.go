// SPDX-License-Identifier: BSD-3-Clause
// Copyright 2009 The Go Authors. All rights reserved.
//
// Derived from Go 1.27.0 GOROOT/src/math/atan.go. Vendored so the encoder's
// arctangent is architecture-deterministic: math.Atan is portable Go on both
// arm64 and amd64 but gc contracts its Cephes polynomial into FMADDD on arm64,
// so the same input yields different last-ulp results per arch (issues #59 and #79). The
// explicit float64(...) conversions below are rounding barriers forbidding
// contraction; a barriered copy equals amd64's stdlib Atan bit-for-bit, so
// amd64 output is unchanged and arm64 converges to it. See PROVENANCE.md.
//
// The original C code, the long comment, and the constants below are from
// http://netlib.sandia.gov/cephes/cmath/atan.c (Cephes Math Library, Stephen
// L. Moshier), used freely per its readme.

package fmath

// xatan evaluates a series valid in the range [0, 0.66]. Barriered so the
// rational approximation evaluates with per-op rounding (no cross-statement FMA).
func xatan(x float64) float64 {
	const (
		P0 = -8.750608600031904122785e-01
		P1 = -1.615753718733365076637e+01
		P2 = -7.500855792314704667340e+01
		P3 = -1.228866684490136173410e+02
		P4 = -6.485021904942025371773e+01
		Q0 = +2.485846490142306297962e+01
		Q1 = +1.650270098316988542046e+02
		Q2 = +4.328810604912902668951e+02
		Q3 = +4.853903996359136964868e+02
		Q4 = +1.945506571482613964425e+02
	)
	z := float64(x * x) // barriered so z + Q0 below does not contract to FMADDD
	// ((((P0*z+P1)*z+P2)*z+P3)*z+P4) / (((((z+Q0)*z+Q1)*z+Q2)*z+Q3)*z+Q4),
	// each product rounded before the add. // no cross-statement FMA
	p := float64(P0*z) + P1
	p = float64(p*z) + P2
	p = float64(p*z) + P3
	p = float64(p*z) + P4
	q := z + Q0
	q = float64(q*z) + Q1
	q = float64(q*z) + Q2
	q = float64(q*z) + Q3
	q = float64(q*z) + Q4
	z = z * p / q
	z = float64(x*z) + x // no cross-statement FMA
	return z
}

// satan reduces its argument (known to be positive) to the range [0, 0.66] and
// calls xatan.
func satan(x float64) float64 {
	const (
		Morebits = 6.123233995736765886130e-17 // pi/2 = PIO2 + Morebits
		Tan3pio8 = 2.41421356237309504880      // tan(3*pi/8)
	)
	const pi = 3.14159265358979311600 // math.Pi
	if x <= 0.66 {
		return xatan(x)
	}
	if x > Tan3pio8 {
		return pi/2 - xatan(1/x) + Morebits
	}
	return pi/4 + xatan((x-1)/(x+1)) + 0.5*Morebits
}

// atan returns the arctangent, in radians, of x. Special cases:
// atan(+-0) = +-0, atan(+-Inf) = +-Pi/2.
//
//go:noinline
func atan(x float64) float64 {
	if x == 0 {
		return x
	}
	if x > 0 {
		return satan(x)
	}
	return -satan(-x)
}
