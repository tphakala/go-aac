// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

// aacMul26 mirrors AAC_MUL26 (libavcodec/aac_defines.h:47 @ d09d5afc3a): a
// Q26 rounding multiply, (int64(x)*y + 0x2000000) >> 26 truncated to int.
func aacMul26(x, y int32) int32 {
	return int32((int64(x)*int64(y) + 0x2000000) >> 26)
}

// computeLPCCoefsFixed converts the Q31 TNS filter coefficients in autoc into
// direct-form LPC coefficients in lpc[0:order]. Mirrors the fixed instantiation
// of compute_lpc_coefs (libavcodec/lpc_functions.h:54-106 @ d09d5afc3a) as the
// decoder calls it from apply_tns: i=0, normalize=0, fail=0, lpc_stride=0, so
// the Levinson-Durbin recursion collapses to the in-place update below.
// LPC_SRA_R(x,5) is the rounding shift (x + 16) >> 5; LPC_MUL26 is AAC_MUL26.
func computeLPCCoefsFixed(autoc []int32, order int, lpc []int32) {
	for i := range order {
		r := (-autoc[i] + (1 << 4)) >> 5
		lpc[i] = r
		for j := range (i + 1) >> 1 {
			f := lpc[j]
			b := lpc[i-1-j]
			lpc[j] = f + aacMul26(r, b)
			lpc[i-1-j] = b + aacMul26(r, f)
		}
	}
}

// applyTNS runs the temporal-noise-shaping all-pole (AR) synthesis filters over
// a channel's dequantized spectrum. Mirrors apply_tns with decode=1 (the LC
// path; LTP uses decode=0, out of scope) at aacdec_dsp_template.c:164-219
// @ d09d5afc3a. The coefficient reads/writes wrap like the C's UINTFLOAT*
// arithmetic (Go int32 wraps identically).
func applyTNS(coef []int32, tns *TNSData, ics *ICSInfo) {
	mmm := ics.TNSMaxBands
	if ics.MaxSFB < mmm {
		mmm = ics.MaxSFB
	}
	if mmm == 0 {
		return
	}
	offsets := ics.SWBOffset
	var lpc [tnsMaxOrder]int32

	for w := range ics.NumWindows {
		bottom := ics.NumSWB
		for filt := range tns.NFilt[w] {
			top := bottom
			bottom = top - tns.Length[w][filt]
			if bottom < 0 {
				bottom = 0
			}
			order := tns.Order[w][filt]
			if order == 0 {
				continue
			}

			computeLPCCoefsFixed(tns.CoefFixed[w][filt][:], order, lpc[:])

			lo := bottom
			if mmm < lo {
				lo = mmm
			}
			hi := top
			if mmm < hi {
				hi = mmm
			}
			start := int(offsets[lo])
			end := int(offsets[hi])
			size := end - start
			if size <= 0 {
				continue
			}

			inc := 1
			if tns.Direction[w][filt] != 0 {
				inc = -1
				start = end - 1
			}
			start += w * 128

			for m := 0; m < size; m, start = m+1, start+inc {
				lim := order
				if m < lim {
					lim = m
				}
				for i := 1; i <= lim; i++ {
					coef[start] -= aacMul26(coef[start-i*inc], lpc[i-1])
				}
			}
		}
	}
}
