// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
)

// Intensity stereo constants. Mirror libavcodec/aacenc_is.c @ d09d5afc3a.
const intStereoLowLimit = 6100 // Hz, lower limit of intensity stereo

// fltMinNormal32 mirrors C FLT_MIN (the smallest normal float32).
const fltMinNormal32 = 1.1754943508222875e-38

// bval2bmax approximates exp10f(-3*(0.5+0.5*cos(min(b,15.5)/15.5))).
// Mirrors aacenc_utils.h:bval2bmax @ d09d5afc3a.
func bval2bmax(b float32) float32 {
	t := 0.0035 * (b * b * b) / (15.5 * 15.5 * 15.5)
	return 0.001 + t
}

// SfdeltaCanReplace reports whether a band's scalefactor can be replaced
// without violating the SF delta constraints. Mirrors
// aacenc_utils.h:ff_sfdelta_can_replace @ d09d5afc3a.
func SfdeltaCanReplace(sce *SingleChannelElement, nextband *[128]uint8,
	prevSF, newSF, band int) bool {
	return newSF >= prevSF-ScaleMaxDiff &&
		newSF <= prevSF+ScaleMaxDiff &&
		sce.SfIdx[nextband[band]] >= newSF-ScaleMaxDiff &&
		sce.SfIdx[nextband[band]] <= newSF+ScaleMaxDiff
}

// posPow34 mirrors aacenc_utils.h:pos_pow34 @ d09d5afc3a.
func posPow34(a float32) float32 {
	return fmath.Sqrt32(a * fmath.Sqrt32(a))
}

// isError mirrors struct AACISError (libavcodec/aacenc_is.c @ d09d5afc3a).
type isError struct {
	pass   bool
	phase  int
	err    float32
	dist1  float32
	dist2  float32
	ener01 float32
}

// isEncodingErr computes the rate-distortion error of coding one band as
// intensity stereo with the given phase. Mirrors
// aacenc_is.c:aac_is_encoding_err @ d09d5afc3a.
//
//nolint:gocognit // faithful port of one C function
func (c *Coder) isEncodingErr(cpe *ChannelElement, psy0, psy1 *[128]PsyBand,
	lambda float32, start, w, g int, ener0, ener1, ener01 float32,
	phase int) isError {
	sce0, sce1 := &cpe.Ch[0], &cpe.Ch[1]
	l34 := c.scoefs[256*0 : 256*0+256]
	r34 := c.scoefs[256*1 : 256*1+256]
	is := c.scoefs[256*2 : 256*2+256]
	i34 := c.scoefs[256*3 : 256*3+256]
	var dist1, dist2 float32
	var e isError

	if ener01 <= 0 || ener0 <= 0 {
		return e
	}

	for w2 := range sce0.ICS.GroupLen[w] {
		band0 := &psy0[(w+w2)*16+g]
		band1 := &psy1[(w+w2)*16+g]
		isSfIdx := max(1, sce0.SfIdx[w*16+g]-4)
		e0134 := float32(phase) * posPow34(ener1/ener0)
		var distSpecErr float32
		minthr := min(band0.Threshold, band1.Threshold)
		size := int(sce0.ICS.SwbSizes[g])
		base := start + (w+w2)*128
		// the IS downmix scale is computed in double precision (C sqrt)
		isScale := fmath.Sqrt64(float64(ener0 / ener01))
		for i := range size {
			p := float32(phase) * sce1.Coeffs[base+i]
			is[i] = float32(float64(sce0.Coeffs[base+i]+p) * isScale)
		}
		dsp.AbsPow34(l34[:size], sce0.Coeffs[base:base+size])
		dsp.AbsPow34(r34[:size], sce1.Coeffs[base:base+size])
		dsp.AbsPow34(i34[:size], is[:size])
		maxval := FindMaxVal(1, size, i34)
		isBandType := FindMinBook(maxval, isSfIdx)
		dist1 += c.quantizeBandCost(sce0.Coeffs[base:base+size], l34[:size],
			sce0.SfIdx[w*16+g], sce0.BandType[w*16+g],
			lambda/band0.Threshold, fmath.Inf32(), nil, nil)
		dist1 += c.quantizeBandCost(sce1.Coeffs[base:base+size], r34[:size],
			sce1.SfIdx[w*16+g], sce1.BandType[w*16+g],
			lambda/band1.Threshold, fmath.Inf32(), nil, nil)
		dist2 += c.quantizeBandCost(is[:size], i34[:size], isSfIdx, isBandType,
			lambda/minthr, fmath.Inf32(), nil, nil)
		for i := range size {
			d1 := l34[i] - i34[i]
			t1 := float32(d1 * d1) // no cross-statement FMA
			distSpecErr += t1
			p := float32(i34[i] * e0134) // no FMA into the subtract
			d2 := r34[i] - p
			t2 := float32(d2 * d2)
			distSpecErr += t2
		}
		distSpecErr *= lambda / minthr
		dist2 += distSpecErr
	}

	e.pass = dist2 <= dist1
	e.phase = phase
	e.err = dist2 - dist1
	e.dist1 = dist1
	e.dist2 = dist2
	e.ener01 = ener01
	return e
}

// SearchForIS decides intensity stereo per band for the twoloop and fast
// coder paths, after quantization. Mirrors
// aacenc_is.c:ff_aac_search_for_is @ d09d5afc3a.
//
//nolint:gocognit,gocyclo // faithful port of one C function
func (c *Coder) SearchForIS(sampleRate int, cpe *ChannelElement,
	psy0, psy1 *[128]PsyBand, lambda float32) {
	sce0, sce1 := &cpe.Ch[0], &cpe.Ch[1]
	prevSf1, prevBt := -1, -1
	prevIs := false
	count := 0
	freqMult := float32(sampleRate) / (1024.0 / float32(sce0.ICS.NumWindows)) / 2.0
	var nextband1 [128]uint8

	if cpe.CommonWindow == 0 {
		return
	}

	// scout out the next nonzero bands
	InitNextbandMap(sce1, &nextband1)

	for w := 0; w < sce0.ICS.NumWindows; w += sce0.ICS.GroupLen[w] {
		start := 0
		for g := range sce0.ICS.NumSwb {
			if float32(start)*freqMult > intStereoLowLimit*(lambda/170.0) &&
				cpe.Ch[0].BandType[w*16+g] != NoiseBT && !cpe.Ch[0].Zeroes[w*16+g] &&
				cpe.Ch[1].BandType[w*16+g] != NoiseBT && !cpe.Ch[1].Zeroes[w*16+g] &&
				SfdeltaCanRemoveBand(sce1, &nextband1, prevSf1, w*16+g) {
				var ener0, ener1, ener01, ener01p float32
				for w2 := range sce0.ICS.GroupLen[w] {
					for i := range int(sce0.ICS.SwbSizes[g]) {
						coef0 := sce0.Coeffs[start+(w+w2)*128+i]
						coef1 := sce1.Coeffs[start+(w+w2)*128+i]
						t0 := float32(coef0 * coef0) // no cross-statement FMA
						ener0 += t0
						t1 := float32(coef1 * coef1)
						ener1 += t1
						sum := coef0 + coef1
						t01 := float32(sum * sum)
						ener01 += t01
						dif := coef0 - coef1
						t01p := float32(dif * dif)
						ener01p += t01p
					}
				}
				phErr1 := c.isEncodingErr(cpe, psy0, psy1, lambda, start, w, g,
					ener0, ener1, ener01p, -1)
				phErr2 := c.isEncodingErr(cpe, psy0, psy1, lambda, start, w, g,
					ener0, ener1, ener01, +1)
				best := &phErr2
				if phErr1.pass && phErr1.err < phErr2.err {
					best = &phErr1
				}
				if best.pass {
					cpe.IsMask[w*16+g] = true
					cpe.MsMask[w*16+g] = false
					cpe.Ch[0].IsEner[w*16+g] = float32(fmath.Sqrt64(float64(ener0 / best.ener01)))
					cpe.Ch[1].IsEner[w*16+g] = ener0 / ener1
					if best.phase > 0 {
						cpe.Ch[1].BandType[w*16+g] = IntensityBT
					} else {
						cpe.Ch[1].BandType[w*16+g] = IntensityBT2
					}
					if prevIs && prevBt != cpe.Ch[1].BandType[w*16+g] {
						// flip the M/S mask and pick the other codebook: it
						// encodes more efficiently
						cpe.MsMask[w*16+g] = true
						if best.phase > 0 {
							cpe.Ch[1].BandType[w*16+g] = IntensityBT2
						} else {
							cpe.Ch[1].BandType[w*16+g] = IntensityBT
						}
					}
					prevBt = cpe.Ch[1].BandType[w*16+g]
					count++
				}
			}
			if !sce1.Zeroes[w*16+g] && sce1.BandType[w*16+g] < ReservedBT {
				prevSf1 = sce1.SfIdx[w*16+g]
			}
			prevIs = cpe.IsMask[w*16+g]
			start += int(sce0.ICS.SwbSizes[g])
		}
	}
	cpe.IsMode = count != 0
}

// SearchForMS decides mid/side coding per band for the twoloop and fast
// coder paths, after quantization and the intensity stereo search. Mirrors
// aaccoder.c:search_for_ms @ d09d5afc3a.
//
//nolint:gocognit,gocyclo // faithful port of one C function
func (c *Coder) SearchForMS(cpe *ChannelElement, psy0, psy1 *[128]PsyBand,
	lambda float32) {
	var nextband0, nextband1 [128]uint8
	m := c.scoefs[128*0 : 128*0+128]
	s := c.scoefs[128*1 : 128*1+128]
	l34 := c.scoefs[128*2 : 128*2+128]
	r34 := c.scoefs[128*3 : 128*3+128]
	m34 := c.scoefs[128*4 : 128*4+128]
	s34 := c.scoefs[128*5 : 128*5+128]
	mslambda := min(1.0, lambda/120.0)
	sce0, sce1 := &cpe.Ch[0], &cpe.Ch[1]
	if cpe.CommonWindow == 0 {
		return
	}

	// scout out the next nonzero bands
	InitNextbandMap(sce0, &nextband0)
	InitNextbandMap(sce1, &nextband1)

	prevMid := sce0.SfIdx[0]
	prevSide := sce1.SfIdx[0]
	for w := 0; w < sce0.ICS.NumWindows; w += sce0.ICS.GroupLen[w] {
		start := 0
		for g := range sce0.ICS.NumSwb {
			bmax := bval2bmax(float32(g)*17.0/float32(sce0.ICS.NumSwb)) / 0.0045
			if !cpe.IsMask[w*16+g] {
				cpe.MsMask[w*16+g] = false
			}
			if !sce0.Zeroes[w*16+g] && !sce1.Zeroes[w*16+g] && !cpe.IsMask[w*16+g] {
				var mmax, smax float32
				size := int(sce0.ICS.SwbSizes[g])

				// mid/side SF and book for the whole window group
				for w2 := range sce0.ICS.GroupLen[w] {
					for i := range size {
						m[i] = (sce0.Coeffs[start+(w+w2)*128+i] +
							sce1.Coeffs[start+(w+w2)*128+i]) * 0.5
						s[i] = m[i] - sce1.Coeffs[start+(w+w2)*128+i]
					}
					dsp.AbsPow34(m34[:size], m[:size])
					dsp.AbsPow34(s34[:size], s[:size])
					for i := range size {
						mmax = max(mmax, m34[i])
						smax = max(smax, s34[i])
					}
				}

				for sidSfBoost := range 4 {
					var dist1, dist2 float32
					b0, b1sum := 0, 0

					minidx := min(sce0.SfIdx[w*16+g], sce1.SfIdx[w*16+g])
					mididx := clip(minidx, 0, ScaleMaxPos-ScaleDiv512)
					sididx := clip(minidx-sidSfBoost*3, 0, ScaleMaxPos-ScaleDiv512)
					if sce0.BandType[w*16+g] != NoiseBT && sce1.BandType[w*16+g] != NoiseBT &&
						(!SfdeltaCanReplace(sce0, &nextband0, prevMid, mididx, w*16+g) ||
							!SfdeltaCanReplace(sce1, &nextband1, prevSide, sididx, w*16+g)) {
						// scalefactor range violation: would decrease
						// quality unacceptably
						continue
					}

					midcb := max(1, FindMinBook(mmax, mididx))
					sidcb := max(1, FindMinBook(smax, sididx))

					for w2 := range sce0.ICS.GroupLen[w] {
						band0 := &psy0[(w+w2)*16+g]
						band1 := &psy1[(w+w2)*16+g]
						minthr := min(band0.Threshold, band1.Threshold)
						var b1, b2, b3, b4 int
						for i := range size {
							m[i] = (sce0.Coeffs[start+(w+w2)*128+i] +
								sce1.Coeffs[start+(w+w2)*128+i]) * 0.5
							s[i] = m[i] - sce1.Coeffs[start+(w+w2)*128+i]
						}

						dsp.AbsPow34(l34[:size], sce0.Coeffs[start+(w+w2)*128:start+(w+w2)*128+size])
						dsp.AbsPow34(r34[:size], sce1.Coeffs[start+(w+w2)*128:start+(w+w2)*128+size])
						dsp.AbsPow34(m34[:size], m[:size])
						dsp.AbsPow34(s34[:size], s[:size])
						dist1 += c.quantizeBandCost(
							sce0.Coeffs[start+(w+w2)*128:start+(w+w2)*128+size],
							l34[:size], sce0.SfIdx[w*16+g], sce0.BandType[w*16+g],
							lambda/(band0.Threshold+fltMinNormal32), fmath.Inf32(), &b1, nil)
						dist1 += c.quantizeBandCost(
							sce1.Coeffs[start+(w+w2)*128:start+(w+w2)*128+size],
							r34[:size], sce1.SfIdx[w*16+g], sce1.BandType[w*16+g],
							lambda/(band1.Threshold+fltMinNormal32), fmath.Inf32(), &b2, nil)
						dist2 += c.quantizeBandCost(m[:size], m34[:size], mididx, midcb,
							lambda/(minthr+fltMinNormal32), fmath.Inf32(), &b3, nil)
						dist2 += c.quantizeBandCost(s[:size], s34[:size], sididx, sidcb,
							mslambda/(minthr*bmax+fltMinNormal32), fmath.Inf32(), &b4, nil)
						b0 += b1 + b2
						b1sum += b3 + b4
						dist1 -= float32(b1 + b2)
						dist2 -= float32(b3 + b4)
					}
					cpe.MsMask[w*16+g] = dist2 <= dist1 && b1sum < b0
					if cpe.MsMask[w*16+g] {
						if sce0.BandType[w*16+g] != NoiseBT && sce1.BandType[w*16+g] != NoiseBT {
							sce0.SfIdx[w*16+g] = mididx
							sce1.SfIdx[w*16+g] = sididx
							sce0.BandType[w*16+g] = midcb
							sce1.BandType[w*16+g] = sidcb
						} else if (sce0.BandType[w*16+g] != NoiseBT) != (sce1.BandType[w*16+g] != NoiseBT) {
							// ms_mask unneeded, and it confuses some decoders
							cpe.MsMask[w*16+g] = false
						}
						break
					} else if b1sum > b0 {
						// more boost won't fix this
						break
					}
				}
			}
			if !sce0.Zeroes[w*16+g] && sce0.BandType[w*16+g] < ReservedBT {
				prevMid = sce0.SfIdx[w*16+g]
			}
			if !sce1.Zeroes[w*16+g] && !cpe.IsMask[w*16+g] && sce1.BandType[w*16+g] < ReservedBT {
				prevSide = sce1.SfIdx[w*16+g]
			}
			start += int(sce0.ICS.SwbSizes[g])
		}
	}
}
