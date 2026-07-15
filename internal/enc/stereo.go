// SPDX-License-Identifier: LGPL-2.1-or-later

// NMR pre-quantization stereo decisions: per-band L/R vs M/S vs intensity
// from the psy model alone, so the trellis allocates natively on the
// spectra actually coded. Mirrors the nmr_* stereo helpers of
// libavcodec/aacenc.c @ d09d5afc3a.
package enc

import (
	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/fmath"
)

// NMR stereo decision constants. Mirror libavcodec/aacenc.c @ d09d5afc3a.
const (
	nmrISImgGate  = 0.5  // intensity allowed when the image error is masked
	nmrISLowLimit = 6100 // Hz lower limit for intensity stereo
	nmrISMaxBPS   = 1.52 // bits/sample/channel ceiling for intensity
	nmrISFillGain = 0.27 // rate-ceiling lift per unit reservoir deficit
	nmrISFillMax  = 0.40 // cap of the deficit lift

	nmrMSEquiv = 0.01 // side/mid energy ratio below which M/S is ~free
	nmrMSMask  = 0.0  // masked-side gate (disabled at the pin)

	nmrPNSStereoDecorr = 0.6 // min side/mid ratio to keep a stereo PNS band
)

// stereoInput carries the encoder-context inputs of nmr_decide_stereo.
type stereoInput struct {
	sampleRate      int
	bitRate         int
	channels        int
	midSide         int  // s->options.mid_side: -1 auto, 0 off, 1 all
	intensityStereo bool // s->options.intensity_stereo != 0
	rcFill          int  // s->nmr->rc_fill (0 when nmr is nil)
	haveNMR         bool // s->nmr != NULL
}

// nmrApplyMSBand recodes one band's window group as mid+side in place,
// updating the psy band energies/thresholds to the M/S spectra. Mirrors
// aacenc.c:nmr_apply_ms_band @ d09d5afc3a.
func nmrApplyMSBand(cpe *coder.ChannelElement, psy0, psy1 *[128]coder.PsyBand,
	w, g, start, length, gl int) {
	sce0 := &cpe.Ch[0]
	sce1 := &cpe.Ch[1]
	cpe.MsMask[w*16+g] = true
	for w2 := range gl {
		b0 := &psy0[(w+w2)*16+g]
		b1 := &psy1[(w+w2)*16+g]
		l := sce0.Coeffs[start+(w+w2)*128:]
		r := sce1.Coeffs[start+(w+w2)*128:]
		var em, es float32
		for i := range length {
			m := (l[i] + r[i]) * 0.5
			r[i] = m - r[i]
			l[i] = m
			t1 := float32(l[i] * l[i]) // no cross-statement FMA
			em += t1
			t2 := float32(r[i] * r[i])
			es += t2
		}
		thr := min(b0.Threshold, b1.Threshold) * 0.5
		b0.Threshold = thr
		b1.Threshold = thr
		b0.Energy = em
		b1.Energy = es
	}
}

// nmrISImageMasked is the intensity-stereo perceptual test for one band's
// window group: collapse the pair to a single carrier and check that the
// irreducible image error is masked in both channels. Mirrors
// aacenc.c:nmr_is_image_masked @ d09d5afc3a.
func nmrISImageMasked(cpe *coder.ChannelElement, w, g, start, length, gl int,
	ener0, ener1, dot, minthr0, minthr1 float32,
	scaleOut, srOut *float32, pOut *int) bool {
	p := 1
	if dot < 0.0 {
		p = -1
	}
	t := float32(2*p) * dot
	ener01 := ener0 + ener1 + t // energy of L + p*R
	if ener01 <= smallestNormal {
		return false
	}
	scale := fmath.Sqrt32(ener0 / ener01) // carrier = (L + p*R)*scale
	sr := fmath.Sqrt32(ener1 / ener0)     // decoder: R = p*sr*carrier
	var img0, img1 float32
	ps := float32(p) * sr
	for w2 := range gl {
		l := cpe.Ch[0].Coeffs[start+(w+w2)*128:]
		r := cpe.Ch[1].Coeffs[start+(w+w2)*128:]
		for i := range length {
			pr := float32(p) * r[i]
			c := float32((l[i] + pr) * scale)
			dl := l[i] - c
			tc := float32(ps * c)
			dr := r[i] - tc
			t0 := float32(dl * dl) // no cross-statement FMA
			img0 += t0
			t1 := float32(dr * dr)
			img1 += t1
		}
	}
	if img0 >= nmrISImgGate*minthr0*float32(gl) ||
		img1 >= nmrISImgGate*minthr1*float32(gl) {
		return false
	}
	*scaleOut = scale
	*srOut = sr
	*pOut = p
	return true
}

// nmrApplyISBand recodes one band's window group as intensity stereo in
// place: replace L with the carrier, zero R, signal the phase via the side
// channel's band type, and fold the pair's masking into the carrier
// channel. Mirrors aacenc.c:nmr_apply_is_band @ d09d5afc3a.
func nmrApplyISBand(cpe *coder.ChannelElement, psy0, psy1 *[128]coder.PsyBand,
	w, g, start, length, gl int, scale, sr float32, p int, ener0, ener1 float32) {
	cpe.IsMask[w*16+g] = true
	cpe.Ch[0].IsEner[w*16+g] = scale
	cpe.Ch[1].IsEner[w*16+g] = ener0 / ener1
	if p > 0 {
		cpe.Ch[1].BandType[w*16+g] = coder.IntensityBT
	} else {
		cpe.Ch[1].BandType[w*16+g] = coder.IntensityBT2
	}
	for w2 := range gl {
		b0 := &psy0[(w+w2)*16+g]
		b1 := &psy1[(w+w2)*16+g]
		l := cpe.Ch[0].Coeffs[start+(w+w2)*128:]
		r := cpe.Ch[1].Coeffs[start+(w+w2)*128:]
		var ec float32
		for i := range length {
			pr := float32(p) * r[i]
			l[i] = float32((l[i] + pr) * scale)
			r[i] = 0.0
			t := float32(l[i] * l[i]) // no cross-statement FMA
			ec += t
		}
		srsq := float32(sr * sr)
		b0.Threshold = min(b0.Threshold, b1.Threshold/max(srsq, 1e-9))
		b0.Energy = ec
		b1.Energy = 0.0
	}
}

// smallestNormal mirrors C FLT_MIN, the smallest normal float32.
const smallestNormal = 1.1754943508222875e-38

// nmrDecideStereo makes the per-band stereo-mode decision (L/R vs M/S vs
// intensity) for the NMR coder before quantization, from the
// psychoacoustic model alone. Mirrors aacenc.c:nmr_decide_stereo
// @ d09d5afc3a. The complexity waiver covers a faithful port of a single
// C function (docs/porting-guide.md ground rule 1).
//
//nolint:gocognit,gocyclo // faithful port of one C function, see doc comment
func nmrDecideStereo(in stereoInput, cpe *coder.ChannelElement,
	psy0, psy1 *[128]coder.PsyBand) {
	sce0 := &cpe.Ch[0]
	sce1 := &cpe.Ch[1]
	ics := &sce0.ICS
	freqMult := float32(in.sampleRate) / (1024.0 / float32(ics.NumWindows)) / 2.0
	var bps float32
	if in.bitRate > 0 {
		bps = float32(in.bitRate) / float32(in.sampleRate) / float32(in.channels)
	}
	isCount := 0

	// I/S rate gate: eligible at/below ~128 kbps, with the ceiling lifted
	// on hard frames (bit reservoir in deficit).
	rateFrame := float32(in.bitRate) * 1024.0 / float32(max(in.sampleRate, 1))
	var deficit float32
	if in.haveNMR && rateFrame > 0.0 {
		deficit = max(0.0, -float32(in.rcFill)/rateFrame)
	}
	isBonus := min(nmrISFillMax, nmrISFillGain*deficit)
	allowIS := in.intensityStereo && bps < nmrISMaxBPS+isBonus

	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		start := 0
		for g := range ics.NumSwb {
			length := int(ics.SwbSizes[g])
			gl := ics.GroupLen[w]
			var ener0, ener1, dot, esTot, emTot float32
			minthr0 := float32(fmath.MaxFloat32)
			minthr1 := float32(fmath.MaxFloat32)

			cpe.IsMask[w*16+g] = false
			cpe.MsMask[w*16+g] = false

			for w2 := range gl {
				b0 := &psy0[(w+w2)*16+g]
				b1 := &psy1[(w+w2)*16+g]
				l := sce0.Coeffs[start+(w+w2)*128:]
				r := sce1.Coeffs[start+(w+w2)*128:]
				var el, er, em, es, d float32
				for i := range length {
					m := (l[i] + r[i]) * 0.5
					sv := m - r[i]
					t0 := float32(l[i] * l[i]) // no cross-statement FMA
					el += t0
					t1 := float32(r[i] * r[i])
					er += t1
					t2 := float32(m * m)
					em += t2
					t3 := float32(sv * sv)
					es += t3
					t4 := float32(l[i] * r[i])
					d += t4
				}
				ener0 += el
				ener1 += er
				dot += d
				esTot += es
				emTot += em
				minthr0 = min(minthr0, b0.Threshold)
				minthr1 = min(minthr1, b1.Threshold)
			}
			thrG := min(minthr0, minthr1) * float32(gl) // group masking budget

			// PNS-stereo reservation: keep a band for noise substitution
			// only if noise-like in both channels and clearly decorrelated.
			if sce0.CanPNS[w*16+g] && sce1.CanPNS[w*16+g] &&
				esTot > nmrPNSStereoDecorr*emTot {
				start += length
				continue
			}
			sce0.CanPNS[w*16+g] = false
			sce1.CanPNS[w*16+g] = false

			msOK := in.midSide != 0 &&
				(in.midSide == 1 ||
					esTot < nmrMSEquiv*emTot ||
					esTot < nmrMSMask*thrG)
			var scale, sr float32
			var p int
			isOK := !msOK &&
				float32(start)*freqMult > nmrISLowLimit &&
				ener0 > smallestNormal && ener1 > smallestNormal &&
				nmrISImageMasked(cpe, w, g, start, length, gl,
					ener0, ener1, dot, minthr0, minthr1,
					&scale, &sr, &p)

			switch {
			case msOK:
				nmrApplyMSBand(cpe, psy0, psy1, w, g, start, length, gl)
			case isOK && allowIS:
				nmrApplyISBand(cpe, psy0, psy1, w, g, start, length, gl,
					scale, sr, p, ener0, ener1)
				isCount++
			case isOK && in.midSide != 0:
				nmrApplyMSBand(cpe, psy0, psy1, w, g, start, length, gl)
			}
			// else: keep full L/R stereo
			start += length
		}
	}
	cpe.IsMode = isCount != 0
}

// applyIntensityStereo collapses the IS-masked bands into channel 0 and
// zeroes channel 1, using the position energies from the IS search.
// Mirrors aacenc.c:apply_intensity_stereo @ d09d5afc3a. Only the non-NMR
// coders use it; the NMR path applies I/S inside nmrDecideStereo.
func applyIntensityStereo(cpe *coder.ChannelElement) {
	ics := &cpe.Ch[0].ICS
	if cpe.CommonWindow == 0 {
		return
	}
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		for w2 := range ics.GroupLen[w] {
			start := (w + w2) * 128
			for g := range ics.NumSwb {
				// IntensityBT2 (14) is out of phase (-1), IntensityBT (15) in phase (+1).
				p := -1 + 2*(cpe.Ch[1].BandType[w*16+g]-coder.IntensityBT2)
				scale := cpe.Ch[0].IsEner[w*16+g]
				if !cpe.IsMask[w*16+g] {
					start += int(ics.SwbSizes[g])
					continue
				}
				if cpe.MsMask[w*16+g] {
					p *= -1
				}
				for i := range int(ics.SwbSizes[g]) {
					t := float32(float32(p) * cpe.Ch[1].Coeffs[start+i]) // no FMA
					sum := (cpe.Ch[0].Coeffs[start+i] + t) * scale
					cpe.Ch[0].Coeffs[start+i] = sum
					cpe.Ch[1].Coeffs[start+i] = 0.0
				}
				start += int(ics.SwbSizes[g])
			}
		}
	}
}

// applyMidSideStereo recodes the M/S-masked bands in place. The mask can
// be used for other purposes in PNS and I/S, so M/S is skipped when a band
// uses either. Mirrors aacenc.c:apply_mid_side_stereo @ d09d5afc3a.
func applyMidSideStereo(cpe *coder.ChannelElement) {
	ics := &cpe.Ch[0].ICS
	if cpe.CommonWindow == 0 {
		return
	}
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		for w2 := range ics.GroupLen[w] {
			start := (w + w2) * 128
			for g := range ics.NumSwb {
				if !cpe.MsMask[w*16+g] || cpe.IsMask[w*16+g] ||
					cpe.Ch[0].BandType[w*16+g] >= coder.NoiseBT ||
					cpe.Ch[1].BandType[w*16+g] >= coder.NoiseBT {
					start += int(ics.SwbSizes[g])
					continue
				}
				for i := range int(ics.SwbSizes[g]) {
					l := (cpe.Ch[0].Coeffs[start+i] + cpe.Ch[1].Coeffs[start+i]) * 0.5
					r := l - cpe.Ch[1].Coeffs[start+i]
					cpe.Ch[0].Coeffs[start+i] = l
					cpe.Ch[1].Coeffs[start+i] = r
				}
				start += int(ics.SwbSizes[g])
			}
		}
	}
}
