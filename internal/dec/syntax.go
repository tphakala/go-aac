// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"fmt"

	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/tables"
	"github.com/tphakala/go-aac/internal/vlc"
)

// decodeICSInfo parses ics_info. Mirrors the AAC-LC path of
// libavcodec/aac/aacdec.c:decode_ics_info @ d09d5afc3a; the ER/LD/ELD and
// AOT_AAC_MAIN prediction branches are not ported (those object types are
// rejected at configuration).
func (d *Decoder) decodeICSInfo(r *bits.Reader, ics *ICSInfo) error {
	sri := d.cfg.SamplingIndex
	if r.ReadBit() != 0 {
		// Reserved bit set: the C logs and continues unless strict error
		// recognition is enabled; mirror the permissive default.
		_ = 0
	}
	ics.WindowSequence[1] = ics.WindowSequence[0]
	ics.WindowSequence[0] = int(r.Read(2))
	ics.UseKBWindow[1] = ics.UseKBWindow[0]
	ics.UseKBWindow[0] = int(r.ReadBit())
	ics.NumWindowGroups = 1
	ics.GroupLen[0] = 1
	if ics.WindowSequence[0] == EightShortSequence {
		ics.MaxSFB = int(r.Read(4))
		for range 7 {
			if r.ReadBit() != 0 {
				ics.GroupLen[ics.NumWindowGroups-1]++
			} else {
				ics.NumWindowGroups++
				ics.GroupLen[ics.NumWindowGroups-1] = 1
			}
		}
		ics.NumWindows = 8
		ics.SWBOffset = tables.SwbOffset128[sri]
		ics.NumSWB = int(tables.NumSwb128[sri])
		ics.TNSMaxBands = int(tables.TNSMaxBands128[sri])
		ics.PredictorPresent = false
	} else {
		ics.MaxSFB = int(r.Read(6))
		ics.NumWindows = 1
		ics.SWBOffset = tables.SwbOffset1024[sri]
		ics.NumSWB = int(tables.NumSwb1024[sri])
		ics.TNSMaxBands = int(tables.TNSMaxBands1024[sri])
		ics.PredictorPresent = r.ReadBit() != 0
		if ics.PredictorPresent {
			// Prediction is AAC-Main; forbidden in LC streams.
			ics.MaxSFB = 0
			return fmt.Errorf("%w: prediction is not allowed in AAC-LC", ErrInvalidData)
		}
	}
	if ics.MaxSFB > ics.NumSWB {
		maxSFB := ics.MaxSFB
		ics.MaxSFB = 0
		return fmt.Errorf("%w: max_sfb %d exceeds num_swb %d", ErrInvalidData,
			maxSFB, ics.NumSWB)
	}
	return nil
}

// decodeBandTypes parses section_data. Mirrors
// libavcodec/aac/aacdec.c:decode_band_types @ d09d5afc3a: 4-bit codebook,
// run lengths in 5-bit (long) or 3-bit (short) increments with all-ones
// escape continuation, band type 12 rejected.
func decodeBandTypes(r *bits.Reader, sce *SCE) error {
	ics := &sce.ICS
	runBits := 5
	if ics.WindowSequence[0] == EightShortSequence {
		runBits = 3
	}
	for g := range ics.NumWindowGroups {
		k := 0
		for k < ics.MaxSFB {
			sectEnd := k
			sectBandType := int(r.Read(4))
			if sectBandType == ReservedBT {
				return fmt.Errorf("%w: invalid band type", ErrInvalidData)
			}
			for {
				sectLenIncr := int(r.Read(runBits))
				sectEnd += sectLenIncr
				if r.Err() != nil {
					return fmt.Errorf("%w: overread in section data", ErrInvalidData)
				}
				if sectEnd > ics.MaxSFB {
					return fmt.Errorf("%w: number of bands %d exceeds limit %d",
						ErrInvalidData, sectEnd, ics.MaxSFB)
				}
				if sectLenIncr != 1<<runBits-1 {
					break
				}
			}
			for ; k < sectEnd; k++ {
				sce.BandType[g*ics.MaxSFB+k] = uint8(sectBandType)
			}
		}
	}
	return nil
}

// decodeScalefactors parses scale_factor_data. Mirrors
// libavcodec/aac/aacdec.c:decode_scalefactors @ d09d5afc3a, including the
// exact offset chains, clipping and the unsigned 255 range check.
func decodeScalefactors(r *bits.Reader, sce *SCE, globalGain int) error {
	ics := &sce.ICS
	offset := [3]int{globalGain, globalGain - noiseOffset, 0}
	noiseFlag := 1
	for g := range ics.NumWindowGroups {
		for sfb := range ics.MaxSFB {
			idx := g*ics.MaxSFB + sfb
			switch sce.BandType[idx] {
			case ZeroBT:
				sce.SFO[idx] = 0
			case IntensityBT, IntensityBT - 1:
				sym, ok := vlc.Scalefactor.Decode(r)
				if !ok {
					return fmt.Errorf("%w: bad scalefactor code", ErrInvalidData)
				}
				offset[2] += int(sym) - scaleDiffZero
				clipped := clip(offset[2], -155, 100)
				sce.SFO[idx] = int32(clipped - 100)
			case NoiseBT:
				if noiseFlag > 0 {
					noiseFlag--
					offset[1] += int(r.Read(noisePreBits)) - noisePre
				} else {
					sym, ok := vlc.Scalefactor.Decode(r)
					if !ok {
						return fmt.Errorf("%w: bad scalefactor code", ErrInvalidData)
					}
					offset[1] += int(sym) - scaleDiffZero
				}
				clipped := clip(offset[1], -100, 155)
				sce.SFO[idx] = int32(clipped)
			default:
				sym, ok := vlc.Scalefactor.Decode(r)
				if !ok {
					return fmt.Errorf("%w: bad scalefactor code", ErrInvalidData)
				}
				offset[0] += int(sym) - scaleDiffZero
				if offset[0] < 0 || offset[0] > 255 {
					return fmt.Errorf("%w: scalefactor %d out of range",
						ErrInvalidData, offset[0])
				}
				sce.SFO[idx] = int32(offset[0] - 100)
			}
		}
	}
	return nil
}

func clip(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// decodePulses parses pulse_data. Mirrors
// libavcodec/aac/aacdec.c:decode_pulses @ d09d5afc3a.
func decodePulses(r *bits.Reader, p *Pulse, swbOffset []uint16, numSWB int) error {
	p.NumPulse = int(r.Read(2)) + 1
	pulseSWB := int(r.Read(6))
	if pulseSWB >= numSWB {
		return ErrInvalidData
	}
	p.Pos[0] = int(swbOffset[pulseSWB]) + int(r.Read(5))
	if p.Pos[0] >= int(swbOffset[numSWB]) {
		return ErrInvalidData
	}
	p.Amp[0] = int(r.Read(4))
	for i := 1; i < p.NumPulse; i++ {
		p.Pos[i] = int(r.Read(5)) + p.Pos[i-1]
		if p.Pos[i] >= int(swbOffset[numSWB]) {
			return ErrInvalidData
		}
		p.Amp[i] = int(r.Read(4))
	}
	return nil
}

// decodeTNS parses tns_data and derives the Q31 fixed-point coefficients.
// Mirrors the AAC-LC path of libavcodec/aac/aacdec.c:ff_aac_decode_tns
// @ d09d5afc3a with the USE_FIXED coefficient conversion
// (Q31(ff_tns_tmp2_map[tmp2_idx][idx]), aac_defines.h @ d09d5afc3a).
func decodeTNS(r *bits.Reader, tns *TNSData, ics *ICSInfo) error {
	is8 := 0
	if ics.WindowSequence[0] == EightShortSequence {
		is8 = 1
	}
	tnsMaxOrderLC := 12
	if is8 == 1 {
		tnsMaxOrderLC = 7
	}
	for w := range ics.NumWindows {
		tns.NFilt[w] = int(r.Read(2 - is8))
		if tns.NFilt[w] == 0 {
			continue
		}
		coefRes := int(r.ReadBit())
		for filt := range tns.NFilt[w] {
			tns.Length[w][filt] = int(r.Read(6 - 2*is8))
			tns.Order[w][filt] = int(r.Read(5 - 2*is8))
			if tns.Order[w][filt] > tnsMaxOrderLC {
				order := tns.Order[w][filt]
				tns.Order[w][filt] = 0
				return fmt.Errorf("%w: TNS filter order %d greater than maximum %d",
					ErrInvalidData, order, tnsMaxOrderLC)
			}
			if tns.Order[w][filt] == 0 {
				continue
			}
			tns.Direction[w][filt] = int(r.ReadBit())
			coefCompress := int(r.ReadBit())
			coefLen := coefRes + 3 - coefCompress
			tmp2Idx := 2*coefCompress + coefRes
			for i := range tns.Order[w][filt] {
				v := tables.TNSTmp2Map[tmp2Idx][r.Read(coefLen)]
				tns.CoefFixed[w][filt][i] = int32(float64(v)*2147483648.0 + 0.5)
			}
		}
	}
	return nil
}

// decodeGainControl parses (and discards) SSR gain_control_data so the bit
// position stays correct on streams that carry it. Mirrors
// libavcodec/aac/aacdec.c:decode_gain_control @ d09d5afc3a, which also
// only skips the fields.
func decodeGainControl(r *bits.Reader, ics *ICSInfo) {
	// wd_num, wd_test, aloc_size per window sequence.
	gainMode := [4][3]int{{1, 0, 5}, {2, 1, 2}, {8, 0, 2}, {2, 1, 5}}
	mode := ics.WindowSequence[0]
	maxBand := int(r.Read(2))
	for range maxBand {
		for wd := range gainMode[mode][0] {
			adjustNum := int(r.Read(3))
			for range adjustNum {
				n := 4 + gainMode[mode][2]
				if wd == 0 && gainMode[mode][1] != 0 {
					n = 4 + 4
				}
				r.Skip(n)
			}
		}
	}
}
