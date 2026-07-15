// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

import (
	"fmt"

	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/tables"
)

// encodeIndividualChannel writes one channel's payload: global gain,
// ics_info (unless the CPE shares a common window), section data,
// scalefactors, the pulse/TNS/gain-control presence flags and the spectral
// data. Mirrors aacenc.c:encode_individual_channel (aacenc.c:948-963)
// @ d09d5afc3a.
func (e *Encoder) encodeIndividualChannel(sce *coder.SingleChannelElement, commonWindow bool) {
	e.pb.Put(8, uint32(sce.SfIdx[0])) // global_gain (aacenc.c:952)
	if !commonWindow {
		e.putIcsInfo(&sce.ICS)
	}
	e.encodeBandInfo(sce)
	e.encodeScaleFactors(sce)
	// encode_pulses (aacenc.c:885): the encoder never enables pulses
	// (num_pulse is always 0), so only the presence bit is written.
	e.pb.Put(1, 0)
	if sce.TNS.Present { // tns_data_present (aacenc.c:958)
		e.pb.Put(1, 1)
	} else {
		e.pb.Put(1, 0)
	}
	coder.EncodeTNSInfo(e.pb, sce) // no-op unless present
	e.pb.Put(1, 0)                 // gain_control / ssr (aacenc.c:961), never used
	e.encodeSpectralCoeffs(sce)
}

// putIcsInfo writes the ics_info element: window sequence, shape, max_sfb
// and, for EIGHT_SHORT, the seven scale_factor_grouping bits (1 = window
// continues the previous group). Mirrors aacenc.c:put_ics_info
// (aacenc.c:468-483) @ d09d5afc3a.
func (e *Encoder) putIcsInfo(ics *coder.IndividualChannelStream) {
	e.pb.Put(1, 0) // ics_reserved bit
	e.pb.Put(2, uint32(ics.WindowSequence[0]))
	e.pb.Put(1, uint32(ics.UseKBWindow[0]))
	if ics.WindowSequence[0] != coder.EightShortSequence {
		e.pb.Put(6, uint32(ics.MaxSfb))
		e.pb.Put(1, 0) // no predictor present
	} else {
		e.pb.Put(4, uint32(ics.MaxSfb))
		for w := 1; w < 8; w++ {
			var cont uint32
			if ics.GroupLen[w] == 0 {
				cont = 1
			}
			e.pb.Put(1, cont)
		}
	}
}

// encodeMSInfo writes the M/S coding mode for a common-window pair and,
// at mode 1, the per-band mask. Mirrors aacenc.c:encode_ms_info
// (aacenc.c:489-498) @ d09d5afc3a.
func (e *Encoder) encodeMSInfo(cpe *coder.ChannelElement) {
	e.pb.Put(2, uint32(cpe.MsMode))
	if cpe.MsMode == 1 {
		ics := &cpe.Ch[0].ICS
		for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
			for i := range ics.MaxSfb {
				var bit uint32
				if cpe.MsMask[w*16+i] {
					bit = 1
				}
				e.pb.Put(1, bit)
			}
		}
	}
}

// encodeBandInfo derives the special (NOISE/INTENSITY) band scalefactors
// and runs the sectioning trellis per window group. Mirrors
// aacenc.c:encode_band_info (aacenc.c:831-841) @ d09d5afc3a.
func (e *Encoder) encodeBandInfo(sce *coder.SingleChannelElement) {
	coder.SetSpecialBandScalefactors(sce)
	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		e.cd.CodebookTrellisRate(e.pb, sce, w, sce.ICS.GroupLen[w], e.lambda)
	}
}

// encodeScaleFactors writes the delta-coded scalefactors. The first
// scalefactor is the global gain already written; subsequent coded bands
// send huffman-coded deltas against the previous coded band. NOISE and
// INTENSITY bands run their own delta chains and the first noise band uses
// the 9-bit NOISE_PRE escape (docs/porting-guide.md pitfall 4); those
// branches are unreachable until PNS/IS land but are ported now so the
// chains exist from day one. Mirrors aacenc.c:encode_scale_factors
// (aacenc.c:845-877) @ d09d5afc3a.
func (e *Encoder) encodeScaleFactors(sce *coder.SingleChannelElement) {
	offSf := sce.SfIdx[0]
	offPns := sce.SfIdx[0] - coder.NoiseOffset
	offIs := 0
	noiseFlag := true
	sce.ICS.EachBand(sce.ICS.MaxSfb, func(_, _, idx int) {
		if sce.Zeroes[idx] {
			return
		}
		var diff int
		switch sce.BandType[idx] {
		case coder.NoiseBT:
			diff = sce.SfIdx[idx] - offPns
			offPns = sce.SfIdx[idx]
			if noiseFlag {
				noiseFlag = false
				e.pb.Put(coder.NoisePreBits, uint32(diff+coder.NoisePre))
				return
			}
		case coder.IntensityBT, coder.IntensityBT2:
			diff = sce.SfIdx[idx] - offIs
			offIs = sce.SfIdx[idx]
		default:
			diff = sce.SfIdx[idx] - offSf
			offSf = sce.SfIdx[idx]
		}
		diff += coder.ScaleDiffZero
		if diff < 0 || diff > 120 {
			// aacenc.c:871 guards this with av_assert0, which is compiled in even
			// for release builds and calls abort(). It is an internal invariant:
			// the coders' scalefactor search is what keeps adjacent deltas inside
			// +-SCALE_DIFF_ZERO, so a violation means the encoder is broken and the
			// bitstream would be garbage. A library must not abort or panic in the
			// caller's process for that, so it becomes a sticky error which
			// EncodeFrame returns; the remaining bands are skipped rather than
			// written from a corrupt state.
			e.fail(fmt.Errorf("enc: scalefactor delta %d out of range [0,120] at band %d "+
				"(internal invariant, mirrors av_assert0 at aacenc.c:871)", diff, idx))
			return
		}
		e.pb.Put(int(tables.ScalefactorBits[diff]), tables.ScalefactorCode[diff])
	})
}

// encodeSpectralCoeffs writes the huffman-coded spectrum of every coded
// band. Mirrors aacenc.c:encode_spectral_coeffs (aacenc.c:900-923)
// @ d09d5afc3a.
func (e *Encoder) encodeSpectralCoeffs(sce *coder.SingleChannelElement) {
	ics := &sce.ICS
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		start := 0
		for i := range ics.MaxSfb {
			if sce.Zeroes[w*16+i] {
				start += int(ics.SwbSizes[i])
				continue
			}
			for w2 := w; w2 < w+ics.GroupLen[w]; w2++ {
				size := int(ics.SwbSizes[i])
				e.cd.QuantizeAndEncodeBand(e.pb,
					sce.Coeffs[start+w2*128:start+w2*128+size],
					sce.SfIdx[w*16+i], sce.BandType[w*16+i],
					e.lambda, ics.WindowClipping[w])
			}
			start += int(ics.SwbSizes[i])
		}
	}
}
