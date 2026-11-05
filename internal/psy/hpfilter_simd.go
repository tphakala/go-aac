// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package psy

import (
	"github.com/tphakala/simd/f32"

	"github.com/tphakala/go-aac/internal/tables"
)

// HpFilterIsSIMD is true on the default build, recording that this file
// provides the SIMD dispatch for the high-pass filter. The root package
// asserts it against aac.SIMDEnabled (simd_kernels_test.go), so the public
// accessor cannot claim a kernel this package did not compile.
const HpFilterIsSIMD = true

// psyHpFilter is the default dispatch: psyHpFilterScalar (lame.go) rebuilt from
// ten f32.PairSumScaleAdd passes, one per tap pair, over the whole block. Each
// pass computes acc + float32(c*float32(a+b)) with three roundings and no FMA
// on every path, the scalar's t = c*(x[i+j]+x[i+21-j]); sum += t, so the result
// is bit-identical. Two things keep it so:
//
//   - the taps accumulate in ascending j into two chains, sum1 (even taps,
//     seeded with the centre tap firbuf[i+10]) and sum2 (odd taps), exactly as
//     lame.go; float addition is not associative, so the order is the contract;
//   - sum2 starts at +0 (a zero-valued scratch), not at its first product,
//     because the scalar's 0 + (-0) is +0 and a product-seeded chain would
//     keep -0.
//
// The final (sum1+sum2)*32768 is a plain loop: f32.Add and f32.Scale move
// their slices to the heap on amd64 (tphakala/simd#326), which would allocate
// per frame, and the loop is one pass over data already in cache.
//
// firbuf must hold blockSizeLong + psyLameFirLen + 13 samples (lookaheadLen is
// enforced by the caller); a short slice panics at the slicing below.
func psyHpFilter(firbuf []float32, hpfsmpl *[blockSizeLong]float32) {
	const n = blockSizeLong
	const half = (psyLameFirLen - 1) / 2 // the centre tap
	c := &tables.PsyFirCoeffs
	h := hpfsmpl[:]

	// sum1 = firbuf[i+half] + c[0]*(x[i]+x[i+21]) + c[2]*(...) + ...
	f32.PairSumScaleAdd(h, firbuf[half:half+n], firbuf[0:n], firbuf[psyLameFirLen:psyLameFirLen+n], c[0])
	for j := 2; j < half-1; j += 2 {
		f32.PairSumScaleAdd(h, h, firbuf[j:j+n], firbuf[psyLameFirLen-j:psyLameFirLen-j+n], c[j])
	}

	// sum2 = 0 + c[1]*(x[i+1]+x[i+20]) + c[3]*(...) + ...
	var s2 [n]float32
	for j := 0; j < half-1; j += 2 {
		f32.PairSumScaleAdd(s2[:], s2[:], firbuf[j+1:j+1+n], firbuf[psyLameFirLen-j-1:psyLameFirLen-j-1+n], c[j+1])
	}

	for i := range h {
		h[i] = (h[i] + s2[i]) * 32768.0
	}
}
