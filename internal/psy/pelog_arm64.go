// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package psy

import (
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/simd/cpu"
)

// peLog2 returns fmath.Log232(e) and fmath.Log232(t), the two logarithms
// calcPE3gpp takes for a band, from one NEON call (unless SIMD_DISABLE masks
// it). The kernel uses separate FMUL and FADD, never FMLA, matching the
// vendored log whose float64 conversions block fusion, so both results are
// bit-identical: TestPeLog2MatchesScalar and FuzzPeLog2Equiv gate it.
func peLog2(e, t float32) (logE, logT float32) {
	if !cpu.ARM64.NEON {
		return fmath.Log232(e), fmath.Log232(t)
	}
	x, r := [2]float32{e, t}, [2]float32{}
	peLog2NEON(&x, &r)
	return r[0], r[1]
}

// peLog2NEON stores fmath.Log232 of both elements of x into r, from one
// 128-bit vector.
//
//go:noescape
func peLog2NEON(x, r *[2]float32)
