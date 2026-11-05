// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package psy

import (
	"github.com/tphakala/go-aac/internal/tables"
	"github.com/tphakala/simd/cpu"
)

// hpFilterKernel runs psyHpFilter with NEON and reports true; under
// SIMD_DISABLE it reports false and the caller runs the scalar loop. The
// kernel uses separate FMUL and FADD, never FMLA, so it matches the scalar
// loop whose float32 conversions block fusion: TestHpFilterKernelMatchesScalar
// and FuzzHpFilterEquiv gate it.
func hpFilterKernel(firbuf []float32, hpfsmpl *[blockSizeLong]float32) bool {
	if !cpu.ARM64.NEON {
		return false
	}
	_ = firbuf[blockSizeLong-1+psyLameFirLen]
	hpFilterNEON(&firbuf[0], &tables.PsyFirCoeffs, hpfsmpl)
	return true
}

// hpFilterNEON is the NEON high-pass filter kernel, the same lane scheme as
// hpFilterAVX2 with four output samples per iteration.
//
//go:noescape
func hpFilterNEON(firbuf *float32, coeffs *[10]float32, out *[blockSizeLong]float32)
