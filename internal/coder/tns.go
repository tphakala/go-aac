// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// TNS constants. Mirror libavcodec/aac.h and aacenc_tns.c @ d09d5afc3a.
// At this pin both long and short coefficient resolutions are 4 bits, so
// the C's c_bits expression (TNS_Q_BITS == 4) is always 1.
const (
	TNSMaxOrder     = 20   // TNS_MAX_ORDER (aac.h)
	tnsPredGainGate = 1.4  // first gate: predicted LPC gain
	tnsPGC1Long     = 1.4  // min measured gain, long blocks
	tnsPGC1Short    = 3.2  // min measured gain, short blocks
	tnsPGClamp      = 6.0  // upper bound: poles near unit circle
	tnsWeightFloor  = 0.01 // per-bin masking floor for the weighted spectrum
)

// tnsMaxNonPNS caps the TNS band range at the first PNS band to avoid
// TNS+PNS conflicts. Mirrors aacenc_tns.c:tns_max_nonpns @ d09d5afc3a.
func tnsMaxNonPNS(sce *SingleChannelElement, mmm int) int {
	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		for g := range mmm {
			if sce.BandType[w*16+g] == NoiseBT {
				mmm = g
				break
			}
		}
	}
	return mmm
}

// quantArrayIdx returns the closest index into arr for val. Mirrors
// aacenc_utils.h:quant_array_idx @ d09d5afc3a.
func quantArrayIdx(val float32, arr []float32) int {
	index := 0
	quantMinErr := fmath.Inf32()
	for i, a := range arr {
		e := float32((val - a) * (val - a))
		if e < quantMinErr {
			quantMinErr = e
			index = i
		}
	}
	return index
}

// SearchForTNS runs the per-window TNS decision: an LPC fit on the
// perceptually weighted spectrum of each filter region, gated on the
// predicted gain, then on the measured post-quantization gain of the
// filtered spectrum. The filter direction follows the temporal energy
// split of the windowed time signal retained in RetBuf. Mirrors
// aacenc_tns.c:ff_aac_search_for_tns @ d09d5afc3a. The negation of the
// reflection coefficients before quantization is load-bearing: the
// ff_lpc_calc_ref_coefs_f sign convention is opposite to what the
// decoder's MA filter needs; fed unnegated, TNS anti-whitens
// (docs/porting-guide.md pitfall 3, aacenc_tns.c:272). Splitting this
// faithfully ported C function would break the line-by-line mapping to
// the pinned source, hence the complexity waiver.
//
//nolint:gocognit,gocyclo // faithful port of one C function, see doc comment
func (c *Coder) SearchForTNS(samplerateIndex int, sce *SingleChannelElement,
	psy *[128]PsyBand) {
	tns := &sce.TNS
	count := 0
	mmm := tnsMaxNonPNS(sce, min(sce.ICS.TnsMaxBands, sce.ICS.MaxSfb))
	is8 := sce.ICS.WindowSequence[0] == EightShortSequence
	minSfbTab := tables.TNSMinSfbLong[:]
	if is8 {
		minSfbTab = tables.TNSMinSfbShort[:]
	}
	sfbStart := clip(int(minSfbTab[samplerateIndex]), 0, mmm)
	sfbEnd := clip(sce.ICS.NumSwb, 0, mmm)
	order := 12
	if is8 {
		order = 7
	}
	var slant int
	switch sce.ICS.WindowSequence[0] {
	case LongStopSequence:
		slant = 1
	case LongStartSequence:
		slant = 0
	default:
		slant = 2
	}
	sfbLen := sfbEnd - sfbStart
	coefLen := int(sce.ICS.SwbOffset[sfbEnd]) - int(sce.ICS.SwbOffset[sfbStart])
	nFilt := 2 // order (12) != TNS_MAX_ORDER (20); short blocks use 1
	if is8 {
		nFilt = 1
	}
	ordG := order / nFilt

	// Apple's accept bar (minimum measured prediction gain): higher on
	// short blocks, where a weak filter's shaped-noise tail spreads
	// across the 50% overlap.
	c1 := float32(tnsPGC1Long)
	if is8 {
		c1 = tnsPGC1Short
	}

	if coefLen <= 0 || sfbLen <= 0 {
		sce.TNS.Present = false
		return
	}

	// Time-domain window length backing one coding window: a long MDCT
	// block is fed 2048 windowed samples (current 1024 + overlap), each
	// short block 256.
	tlen := 2048
	if is8 {
		tlen = 256
	}

	for w := range sce.ICS.NumWindows {
		anyFilt := false

		// The filter gets ran in the direction of the signal's temporal
		// energy, so the quantization noise stays in the loud masked part
		// rather than spilling into the quiet part.
		tw := sce.RetBuf[w*tlen : w*tlen+tlen]
		var eEarly, eLate float32
		for ti := range tlen / 2 {
			t := float32(tw[ti] * tw[ti]) // no cross-statement FMA
			eEarly += t
		}
		for ti := tlen / 2; ti < tlen; ti++ {
			t := float32(tw[ti] * tw[ti]) // no cross-statement FMA
			eLate += t
		}
		tdir := 0
		if eEarly > eLate {
			tdir = 1
		}

		// Walk the frequency regions exactly as the decoder does: filter 0
		// is the topmost band region, each subsequent filter covers the
		// next region down, clamped to mmm.
		topSfb := sce.ICS.NumSwb
		for filt := range nFilt {
			var coefs [dsp.MaxLPCOrder]float64
			var wspec, tmp [1024]float32
			var lpcQ [TNSMaxOrder]float32
			lenSfb := sfbLen / nFilt
			if filt == nFilt-1 {
				lenSfb = sfbLen - filt*(sfbLen/nFilt)
			}
			botSfb := max(0, topSfb-lenSfb)
			gLo, gHi := min(botSfb, mmm), min(topSfb, mmm)
			cLo := int(sce.ICS.SwbOffset[gLo])
			cHi := int(sce.ICS.SwbOffset[gHi])
			clen := cHi - cLo
			dir := slant
			if slant == 2 {
				dir = tdir
			}

			tns.Length[w][filt] = lenSfb
			tns.Order[w][filt] = 0 // default: region carries no filter
			topSfb = botSfb

			if clen <= 2*ordG { // too short for a stable order-ordG LPC
				continue
			}

			// Fit LPC on the perceptually weighted spectrum X/sqrt(thr),
			// floored to avoid a near-zero threshold blowing up a single
			// bin.
			var maxrms float32
			for g := gLo; g < gHi; g++ {
				s0, s1 := int(sce.ICS.SwbOffset[g]), int(sce.ICS.SwbOffset[g+1])
				rms := fmath.Sqrt32(max(psy[w*16+g].Threshold, 0) /
					float32(max(s1-s0, 1)))
				maxrms = max(maxrms, rms)
			}
			floorrms := max(maxrms*tnsWeightFloor, 1e-9)
			for g := gLo; g < gHi; g++ {
				s0, s1 := int(sce.ICS.SwbOffset[g]), int(sce.ICS.SwbOffset[g+1])
				rms := fmath.Sqrt32(max(psy[w*16+g].Threshold, 0) /
					float32(max(s1-s0, 1)))
				wgt := 1.0 / max(rms, floorrms)
				for k := s0; k < s1; k++ {
					wspec[k-cLo] = sce.Coeffs[w*128+k] * wgt
				}
			}
			// Short blocks: unwindowed fit; a Hann window zeros the edges
			// of the tiny region, wrecking the LPC. Long blocks keep the
			// window.
			gain := float32(dsp.CalcRefCoefs(wspec[:clen], ordG, coefs[:],
				!is8, c.lpcScratch[:]))
			// Accept iff finite and inside [gate, clamp]; NaN fails both
			// comparisons, exactly like the C's
			// !isfinite || gain < GATE || gain > CLAMP rejection.
			if !(gain >= tnsPredGainGate && gain <= tnsPGClamp) {
				continue
			}
			// Negate: ff_lpc_calc_ref_coefs_f sign convention is opposite
			// to what ff_aac_apply_tns's MA filter needs; fed unnegated,
			// it anti-whitens (aacenc_tns.c:272).
			for i := range ordG {
				coefs[i] = -coefs[i]
			}

			// Quantize, then build the decoder's direct-form LPC.
			quantArr := tables.TNSTmp2Map[1] // c_bits == 1: 16 entries
			for i := range ordG {
				idx := quantArrayIdx(float32(coefs[i]), quantArr[:16])
				tns.CoefIdx[w][filt][i] = idx
				tns.Coef[w][filt][i] = quantArr[idx]
			}
			dsp.RefToLPC(tns.Coef[w][filt][:ordG], ordG, lpcQ[:])

			// Apply the quantized filter to the weighted spectrum and
			// measure the gain.
			inc, st := 1, 0
			if dir != 0 {
				inc, st = -1, clen-1
			}
			for m := range clen {
				idx := st + m*inc
				acc := wspec[idx]
				for i := 1; i <= min(m, ordG); i++ {
					t := float32(lpcQ[i-1] * wspec[idx-i*inc]) // no FMA
					acc += t
				}
				tmp[idx] = acc
			}
			var origE, filtE float32
			for m := range clen {
				t := float32(wspec[m] * wspec[m]) // no cross-statement FMA
				origE += t
				t2 := float32(tmp[m] * tmp[m]) // no cross-statement FMA
				filtE += t2
			}
			filtE = max(filtE, 1e-9)

			// Keep only if the measured post-quantization gain clears C1.
			if origE < c1*filtE {
				continue
			}

			tns.Order[w][filt] = ordG
			tns.Direction[w][filt] = dir
			anyFilt = true
		}
		if anyFilt {
			tns.NFilt[w] = nFilt
			count++
		} else {
			tns.NFilt[w] = 0
		}
	}
	sce.TNS.Present = count != 0
}

// ApplyTNS filters the working spectrum with the quantized TNS filters,
// predicting from the pre-filter coefficients. Mirrors
// aacenc_tns.c:ff_aac_apply_tns @ d09d5afc3a.
func ApplyTNS(sce *SingleChannelElement) {
	tns := &sce.TNS
	ics := &sce.ICS
	mmm := tnsMaxNonPNS(sce, min(ics.TnsMaxBands, ics.MaxSfb))
	var lpc [TNSMaxOrder]float32

	// TNS predicts from the post-M/S and post-I/S coefficients.
	hist := sce.Coeffs // full array copy, mirrors the C memcpy

	for w := range ics.NumWindows {
		bottom := ics.NumSwb
		for filt := range tns.NFilt[w] {
			top := bottom
			bottom = max(0, top-tns.Length[w][filt])
			order := tns.Order[w][filt]
			if order == 0 {
				continue
			}

			// tns_decode_coef
			dsp.RefToLPC(tns.Coef[w][filt][:order], order, lpc[:])

			start := int(ics.SwbOffset[min(bottom, mmm)])
			end := int(ics.SwbOffset[min(top, mmm)])
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

			// AR filter
			for m := 0; m < size; m, start = m+1, start+inc {
				for i := 1; i <= min(m, order); i++ {
					t := float32(lpc[i-1] * hist[start-i*inc]) // no FMA
					sce.Coeffs[start] += t
				}
			}
		}
	}
}

// compressCoeffs shifts the coefficient indices into the short form when
// no index needs the full range, saving one bit each. Mirrors
// aacenc_tns.c:compress_coeffs @ d09d5afc3a with c_bits == 1
// (TNS_ENABLE_COEF_COMPRESSION is defined at the pin).
func compressCoeffs(coef []int) int {
	const lowIdx, shiftVal, highIdx = 4, 8, 11
	for _, v := range coef {
		if v >= lowIdx && v <= highIdx {
			return 0
		}
	}
	for i, v := range coef {
		if v > highIdx {
			coef[i] = v - shiftVal
		}
	}
	return 1
}

// EncodeTNSInfo writes the tns_data() payload for one channel. Mirrors
// aacenc_tns.c:ff_aac_encode_tns_info @ d09d5afc3a.
func EncodeTNSInfo(pb *bits.Writer, sce *SingleChannelElement) {
	tns := &sce.TNS
	is8 := 0
	if sce.ICS.WindowSequence[0] == EightShortSequence {
		is8 = 1
	}
	const cBits = 1 // TNS_Q_BITS == TNS_Q_BITS_IS8 == 4 at this pin

	if !sce.TNS.Present {
		return
	}

	for i := range sce.ICS.NumWindows {
		pb.Put(2-is8, uint32(tns.NFilt[i]))
		if tns.NFilt[i] == 0 {
			continue
		}
		pb.Put(1, cBits)
		for filt := range tns.NFilt[i] {
			pb.Put(6-2*is8, uint32(tns.Length[i][filt]))
			pb.Put(5-2*is8, uint32(tns.Order[i][filt]))
			if tns.Order[i][filt] == 0 {
				continue
			}
			pb.Put(1, uint32(tns.Direction[i][filt]))
			coefCompress := compressCoeffs(tns.CoefIdx[i][filt][:tns.Order[i][filt]])
			pb.Put(1, uint32(coefCompress))
			coefLen := cBits + 3 - coefCompress
			for w := range tns.Order[i][filt] {
				pb.Put(coefLen, uint32(tns.CoefIdx[i][filt][w]))
			}
		}
	}
}
