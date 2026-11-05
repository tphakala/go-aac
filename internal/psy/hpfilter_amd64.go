// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package psy

import (
	"github.com/tphakala/go-aac/internal/tables"
	"github.com/tphakala/simd/cpu"
)

// hpFilterKernel runs psyHpFilter with AVX2 and reports true; without AVX2 (or
// under SIMD_DISABLE) it reports false and the caller runs the scalar loop.
// Each lane computes one output sample in psyHpFilterScalar's operation order
// with separately rounded products and sums, so the result is bit-identical:
// TestHpFilterKernelMatchesScalar and FuzzHpFilterEquiv gate it.
func hpFilterKernel(firbuf []float32, hpfsmpl *[blockSizeLong]float32) bool {
	if !cpu.X86.AVX2 {
		return false
	}
	_ = firbuf[blockSizeLong-1+psyLameFirLen]
	hpFilterAVX2(&firbuf[0], &tables.PsyFirCoeffs, hpfsmpl)
	return true
}

// hpFilterAVX2 is the AVX2 high-pass filter kernel, eight output samples per
// iteration.
//
//go:noescape
func hpFilterAVX2(firbuf *float32, coeffs *[10]float32, out *[blockSizeLong]float32)
