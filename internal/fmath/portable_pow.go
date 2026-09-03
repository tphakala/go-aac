// SPDX-License-Identifier: BSD-3-Clause
// Copyright 2009 The Go Authors. All rights reserved.
//
// Derived from Go 1.27.0 GOROOT/src/math/pow.go. Vendored so the encoder's
// power function is architecture-deterministic: math.Pow's own body has no
// contractible multiply-add, but it computes x**yf as Exp(yf*Log(x)), and the
// stdlib Exp/Log are per-arch (issue #59). Routing through the vendored exp/log
// makes Pow deterministic. The integer-exponent squaring loop and the y = +-0.5
// -> Sqrt shortcuts stay bit-identical across arches. See PROVENANCE.md.
//
// Special cases are from FreeBSD's e_pow.c.

package fmath

import "math"

func isOddInt(x float64) bool {
	if math.Abs(x) >= (1 << 53) {
		// 1 << 53 is the largest exact integer in the float64 format. Any number
		// outside this range will be truncated before the decimal point and is
		// therefore always an even integer. Without this check, if x overflows
		// int64 the int64(xi) conversion below may produce incorrect results on
		// some architectures (and does so on arm64). See Go issue #57465.
		return false
	}
	xi, xf := math.Modf(x)
	return xf == 0 && int64(xi)&1 == 1
}

// pow returns x**y. Copy of math.pow routed through the vendored exp/log so it
// is architecture-deterministic. The fractional part flows through the barriered
// exp/log; the integer squaring loop's x1 *= x1 is barriered below so the
// x1 += x1 that follows cannot contract to an FMADDD on arm64.
//
//go:noinline
func pow(x, y float64) float64 {
	switch {
	case y == 0 || x == 1:
		return 1
	case y == 1:
		return x
	case math.IsNaN(x) || math.IsNaN(y):
		return math.NaN()
	case x == 0:
		switch {
		case y < 0:
			if math.Signbit(x) && isOddInt(y) {
				return math.Inf(-1)
			}
			return math.Inf(1)
		case y > 0:
			if math.Signbit(x) && isOddInt(y) {
				return x
			}
			return 0
		}
	case math.IsInf(y, 0):
		switch {
		case x == -1:
			return 1
		case (math.Abs(x) < 1) == math.IsInf(y, 1):
			return 0
		default:
			return math.Inf(1)
		}
	case math.IsInf(x, 0):
		if math.IsInf(x, -1) {
			return pow(1/x, -y) // pow(-0, -y)
		}
		switch {
		case y < 0:
			return 0
		case y > 0:
			return math.Inf(1)
		}
	case y == 0.5:
		return math.Sqrt(x)
	case y == -0.5:
		return 1 / math.Sqrt(x)
	}

	yi, yf := math.Modf(math.Abs(y))
	if yf != 0 && x < 0 {
		return math.NaN()
	}
	if yi >= 1<<63 {
		// yi is a large even int that will lead to overflow (or underflow to 0)
		// for all x except -1 (x == 1 was handled earlier)
		switch {
		case x == -1:
			return 1
		case (math.Abs(x) < 1) == (y > 0):
			return 0
		default:
			return math.Inf(1)
		}
	}

	// ans = a1 * 2**ae (= 1 for now).
	a1 := 1.0
	ae := 0

	// ans *= x**yf
	if yf != 0 {
		if yf > 0.5 {
			yf--
			yi++
		}
		a1 = exp(yf * log(x))
	}

	// ans *= x**yi
	// by multiplying in successive squarings of x according to bits of yi.
	x1, xe := math.Frexp(x)
	for i := int64(yi); i != 0; i >>= 1 {
		if xe < -1<<12 || 1<<12 < xe {
			// catch xe before it overflows the left shift below
			ae += xe
			break
		}
		if i&1 == 1 {
			a1 *= x1
			ae += xe
		}
		x1 = float64(x1 * x1) // barriered so x1 += x1 below does not contract to FMADDD
		xe <<= 1
		if x1 < .5 {
			x1 += x1
			xe--
		}
	}

	// ans = a1*2**ae; if y < 0 { ans = 1 / ans }, in the opposite order.
	if y < 0 {
		a1 = 1 / a1
		ae = -ae
	}
	return math.Ldexp(a1, ae)
}
