// SPDX-License-Identifier: LGPL-2.1-or-later

package tables

import "math"

// CbrtTabFixed is the fixed-point cube-root lookup the decoder's vector_pow43
// reads, ff_cbrt_tab_fixed[LUT_SIZE] (libavcodec/cbrt_data.h:37,55
// @ d09d5afc3a). Entry i is lrint(i^(4/3) * 8192): the dequantized magnitude
// of a quantized coefficient i (x -> x^(4/3), the inverse of the encoder's
// x^(3/4) companding), pre-scaled by 2^13.
//
// The pinned FFmpeg build has CONFIG_HARDCODED_TABLES 0, so the oracle
// computes this table at init through ff_cbrt_tableinit_fixed
// (libavcodec/cbrt_tablegen.h:42-64) over ff_cbrt_dbl_tableinit
// (libavcodec/cbrt_tablegen_common.c:31-60). We mirror that exact double
// arithmetic below. Unlike the D1 integer sine windows (which had to be baked
// because the oracle's sinf is not correctly rounded), Go's math.Cbrt is a
// deterministic pure-Go implementation that reproduces the pinned oracle table
// bit for bit: measured 0 differing entries across all 8192 values. The D2
// gate re-checks the whole table against the oracle CBRT dump on every run, so
// this stays the arbiter.
var CbrtTabFixed = computeCbrtTabFixed()

const (
	cbrtLUTSize = 1 << 13         // LUT_SIZE
	cbrtTmpSize = cbrtLUTSize / 2 // TMP_LUT_SIZE
)

// cbrtDblTableinit mirrors ff_cbrt_dbl_tableinit
// (libavcodec/cbrt_tablegen_common.c:31-60 @ d09d5afc3a): tmp[idx] holds
// (2*idx+1)^(4/3) built up as a product of i*cbrt(i) factors over the
// square-free factorization of the odd integers.
func cbrtDblTableinit(tmp []float64) {
	for idx := range tmp {
		tmp[idx] = 1
	}
	for idx := 1; idx < 45; idx++ {
		if tmp[idx] == 1 {
			i := 2*idx + 1
			cbrtVal := float64(i) * math.Cbrt(float64(i))
			for k := i; k < cbrtLUTSize; k *= i {
				for idx2 := k >> 1; idx2 < cbrtTmpSize; idx2 += k {
					tmp[idx2] *= cbrtVal
				}
			}
		}
	}
	for idx := 45; idx < cbrtTmpSize; idx++ {
		if tmp[idx] == 1 {
			i := 2*idx + 1
			cbrtVal := float64(i) * math.Cbrt(float64(i))
			for idx2 := idx; idx2 < cbrtTmpSize; idx2 += i {
				tmp[idx2] *= cbrtVal
			}
		}
	}
}

// computeCbrtTabFixed mirrors ff_cbrt_tableinit_fixed
// (libavcodec/cbrt_tablegen.h:42-64 @ d09d5afc3a). CBRT(x) for USE_FIXED is
// lrint(x*8192); lrint rounds to nearest, ties to even under the default
// FE_TONEAREST mode, which math.RoundToEven reproduces. Values reach ~1.35e9,
// which fits int32; vector_pow43 casts the uint32 table entry to int.
func computeCbrtTabFixed() []int32 {
	tmp := make([]float64, cbrtTmpSize)
	cbrtDblTableinit(tmp)

	tab := make([]int32, cbrtLUTSize)
	cbrt2 := 2 * math.Cbrt(2)
	for idx := cbrtTmpSize - 1; idx >= 0; idx-- {
		cbrtVal := tmp[idx]
		for i := 2*idx + 1; i < cbrtLUTSize; i *= 2 {
			tab[i] = int32(int64(math.RoundToEven(cbrtVal * 8192)))
			cbrtVal *= cbrt2
		}
	}
	tab[0] = 0
	return tab
}
