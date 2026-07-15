// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// Coef2MinSF returns the minimum scalefactor where the quantized coef does
// not clip. Mirrors aacenc_utils.h:coef2minsf @ d09d5afc3a. The float
// expression is truncated once, after all four terms, exactly like the C.
func Coef2MinSF(coef float32) uint8 {
	t := float32(fmath.Log232(coef) * 4) // no FMA with the additive terms
	return uint8(clip(int(t-69+ScaleOnePos-ScaleDiv512), 0, 255))
}

// InitNextbandMap computes the nonzero-band successor map used by the
// scalefactor delta constraint utilities. Mirrors
// aacenc_utils.h:ff_init_nextband_map @ d09d5afc3a.
func InitNextbandMap(sce *SingleChannelElement, nextband *[128]uint8) {
	prevband := uint8(0)
	for g := range 128 {
		nextband[g] = uint8(g)
	}
	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		for g := range sce.ICS.NumSwb {
			if !sce.Zeroes[w*16+g] && sce.BandType[w*16+g] < ReservedBT {
				nextband[prevband] = uint8(w*16 + g)
				prevband = uint8(w*16 + g)
			}
		}
	}
	nextband[prevband] = prevband
}

// SfdeltaCanRemoveBand reports whether removing band keeps the scalefactor
// delta chain legal. Mirrors aacenc_utils.h:ff_sfdelta_can_remove_band
// @ d09d5afc3a.
func SfdeltaCanRemoveBand(sce *SingleChannelElement, nextband *[128]uint8,
	prevSF, band int) bool {
	return prevSF >= 0 &&
		sce.SfIdx[nextband[band]] >= prevSF-ScaleMaxDiff &&
		sce.SfIdx[nextband[band]] <= prevSF+ScaleMaxDiff
}

// SetSpecialBandScalefactors derives scalefactor indices for NOISE and
// INTENSITY bands from their energy values and clips both special delta
// chains. Mirrors aaccoder.c:set_special_band_scalefactors @ d09d5afc3a.
func SetSpecialBandScalefactors(sce *SingleChannelElement) {
	prevscalerN := -255
	prevscalerI := 0
	bands := 0

	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		for g := range sce.ICS.NumSwb {
			if sce.Zeroes[w*16+g] {
				continue
			}
			switch sce.BandType[w*16+g] {
			case IntensityBT, IntensityBT2:
				sce.SfIdx[w*16+g] = clip(int(fmath.Round32(fmath.Log232(sce.IsEner[w*16+g])*2)), -155, 100)
				bands++
			case NoiseBT:
				sce.SfIdx[w*16+g] = clip(3+int(fmath.Ceil32(fmath.Log232(sce.PnsEner[w*16+g])*2)), -100, 155)
				if prevscalerN == -255 {
					prevscalerN = sce.SfIdx[w*16+g]
				}
				bands++
			}
		}
	}

	if bands == 0 {
		return
	}

	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		for g := range sce.ICS.NumSwb {
			if sce.Zeroes[w*16+g] {
				continue
			}
			switch sce.BandType[w*16+g] {
			case IntensityBT, IntensityBT2:
				sce.SfIdx[w*16+g] = clip(sce.SfIdx[w*16+g], prevscalerI-ScaleMaxDiff, prevscalerI+ScaleMaxDiff)
				prevscalerI = sce.SfIdx[w*16+g]
			case NoiseBT:
				sce.SfIdx[w*16+g] = clip(sce.SfIdx[w*16+g], prevscalerN-ScaleMaxDiff, prevscalerN+ScaleMaxDiff)
				prevscalerN = sce.SfIdx[w*16+g]
			}
		}
	}
}

// scalefactorBits returns the differential scalefactor coding cost, the
// operand of the NMR_SFBITS macro (aaccoder_nmr.h:58 @ d09d5afc3a).
func scalefactorBits(d int) int {
	return int(tables.ScalefactorBits[clip(d+ScaleDiffZero, 0, 2*ScaleMaxDiff)])
}
