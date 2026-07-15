// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import "github.com/tphakala/go-aac/internal/fdsp"

// applyMidSideStereo sum/difference-decodes the dequantized spectra of a
// channel pair. Mirrors apply_mid_side_stereo (aacdec_dsp_template.c:84-111,
// USE_FIXED branch @ d09d5afc3a): for every band whose ms_mask bit is set and
// whose band type in BOTH channels is below NOISE_BT, butterflies_fixed
// rewrites (ch0, ch1) as (ch0+ch1, ch0-ch1). The C only calls this when
// common_window and ms_present, so cpe.MaxSFBSte (== ch0.max_sfb) is set.
func applyMidSideStereo(cpe *CPE) {
	ics := &cpe.Ch[0].ICS
	ch0 := cpe.Ch[0].Coeffs[:]
	ch1 := cpe.Ch[1].Coeffs[:]
	offsets := ics.SWBOffset
	base := 0
	for g := range ics.NumWindowGroups {
		for sfb := range cpe.MaxSFBSte {
			idx := g*cpe.MaxSFBSte + sfb
			if cpe.MSMask[idx] != 0 &&
				cpe.Ch[0].BandType[idx] < NoiseBT &&
				cpe.Ch[1].BandType[idx] < NoiseBT {
				off := int(offsets[sfb])
				n := int(offsets[sfb+1]) - off
				for group := range ics.GroupLen[g] {
					p := base + group*128 + off
					fdsp.ButterfliesFixed(ch0[p:p+n], ch1[p:p+n], n)
				}
			}
		}
		base += ics.GroupLen[g] * 128
	}
}

// applyIntensityStereo reconstructs the second channel's intensity bands from
// the first channel's dequantized spectrum. Mirrors apply_intensity_stereo
// (aacdec_dsp_template.c:120-156, USE_FIXED branch @ d09d5afc3a). It runs for
// every CPE (even without a common window); msPresent selects whether the
// ms_mask flips the intensity sign. The scalefactor is kept in the C's
// unreduced integer-exponent form (already stored in sce.SF), and the fixed
// subband_scale uses offset 23 here (versus 34 in the main dequant pass).
func applyIntensityStereo(cpe *CPE, msPresent int) {
	ics := &cpe.Ch[1].ICS
	sce1 := &cpe.Ch[1]
	coef0 := cpe.Ch[0].Coeffs[:]
	coef1 := cpe.Ch[1].Coeffs[:]
	offsets := ics.SWBOffset
	base := 0
	for g := range ics.NumWindowGroups {
		for sfb := range ics.MaxSFB {
			idx := g*ics.MaxSFB + sfb
			bt := sce1.BandType[idx]
			if bt == IntensityBT || bt == IntensityBT-1 {
				c := -1 + 2*(int(bt)-14)
				if msPresent != 0 {
					c *= 1 - 2*int(cpe.MSMask[idx])
				}
				scale := c * int(sce1.SF[idx])
				off := int(offsets[sfb])
				n := int(offsets[sfb+1]) - off
				for group := range ics.GroupLen[g] {
					p := base + group*128 + off
					subbandScale(coef1[p:p+n], coef0[p:p+n], scale, 23, n)
				}
			}
		}
		base += ics.GroupLen[g] * 128
	}
}
