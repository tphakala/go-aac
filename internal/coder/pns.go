// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// PNS candidacy constants. Mirror libavcodec/aaccoder_twoloop.h and
// aaccoder.c @ d09d5afc3a.
const (
	noiseSpreadThreshold = 0.9
	noiseLowLimit        = 4000
	noiseLambdaReplace   = 1.948
)

// MarkPNS flags the bands whose content is noise-like enough to be
// replaced by perceptual noise substitution. The NMR coder consumes
// can_pns directly. Mirrors aaccoder.c:mark_pns @ d09d5afc3a.
func (c *Coder) MarkPNS(sampleRate, bandwidth int, sce *SingleChannelElement,
	psy *[128]PsyBand, lambda float32) {
	wlen := 1024 / sce.ICS.NumWindows
	freqMult := float32(sampleRate) * 0.5 / float32(wlen)
	spreadThreshold := min(0.75, noiseSpreadThreshold*max(0.5, lambda/100.0))
	pnsTransientEnergyR := min(0.7, lambda/140.0)

	// PNS candidacy must use the coder's actual coding bandwidth
	// (s->bandwidth, fixed at init), not a separate heuristic.
	cutoff := bandwidth * 2 * wlen / sampleRate

	sce.BandAlt = sce.BandType
	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		for g := range sce.ICS.NumSwb {
			var sfbEnergy, threshold float32
			spread := float32(2.0)
			minEnergy := float32(-1.0)
			maxEnergy := float32(0.0)
			start := int(sce.ICS.SwbOffset[g])
			freq := float32(start) * freqMult
			if freq < noiseLowLimit || start >= cutoff {
				sce.CanPNS[w*16+g] = false
				continue
			}
			freqBoost := max(0.88*freq/noiseLowLimit, 1.0)
			for w2 := range sce.ICS.GroupLen[w] {
				band := &psy[(w+w2)*16+g]
				sfbEnergy += band.Energy
				spread = min(spread, band.Spread)
				threshold += band.Threshold
				if w2 == 0 {
					minEnergy = band.Energy
					maxEnergy = band.Energy
				} else {
					minEnergy = min(minEnergy, band.Energy)
					maxEnergy = max(maxEnergy, band.Energy)
				}
			}

			sce.PnsEner[w*16+g] = sfbEnergy
			if sfbEnergy < threshold*fmath.Sqrt32(1.5/freqBoost) ||
				spread < spreadThreshold ||
				minEnergy < pnsTransientEnergyR*maxEnergy {
				sce.CanPNS[w*16+g] = false
			} else {
				sce.CanPNS[w*16+g] = true
			}
		}
	}
}

// lcgRandom mirrors aacenc_utils.h:lcg_random @ d09d5afc3a: one LCG step,
// reinterpreted as a signed int.
func lcgRandom(previousVal uint32) int32 {
	return int32(previousVal*1664525 + 1013904223)
}

// SearchForPNS decides perceptual noise substitution for the twoloop and
// fast coder paths, after quantization: noise-like near-threshold bands
// are recoded as NOISE_BT when the estimated noise rate-distortion beats
// the coded one. Uses the deterministic LFSR noise in RandomState.
// Mirrors aaccoder.c:search_for_pns @ d09d5afc3a. Splitting this
// faithfully ported C function would break the line-by-line mapping,
// hence the complexity waiver.
//
//nolint:gocognit,gocyclo // faithful port of one C function, see doc comment
func (c *Coder) SearchForPNS(sampleRate, bandwidth int,
	sce *SingleChannelElement, psy *[128]PsyBand, lambda float32) {
	ics := &sce.ICS
	wlen := 1024 / ics.NumWindows
	pns := c.scoefs[0*128 : 0*128+128]
	pns34 := c.scoefs[1*128 : 1*128+128]
	nor34 := c.scoefs[3*128 : 3*128+128]
	var nextband [128]uint8
	freqMult := float32(sampleRate) * 0.5 / float32(wlen)
	thrMult := noiseLambdaReplace * (100.0 / lambda)
	spreadThreshold := min(0.75, noiseSpreadThreshold*max(0.5, lambda/100.0))
	distBias := clipf(4.0*120/lambda, 0.25, 4.0)
	pnsTransientEnergyR := min(0.7, lambda/140.0)

	prev := -1000
	prevSf := -1

	// PNS candidacy must use the coder's actual coding bandwidth, fixed at
	// init, or it evaluates a different band range than the coder codes.
	cutoff := bandwidth * 2 * wlen / sampleRate

	sce.BandAlt = sce.BandType
	InitNextbandMap(sce, &nextband)
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		wstart := w * 128
		for g := range ics.NumSwb {
			var noiseSfi int
			var dist1, dist2, noiseAmp float32
			var pnsEnergy, pnsTgtEnergy, energyRatio, distThresh float32
			var sfbEnergy, threshold float32
			spread := float32(2.0)
			minEnergy := float32(-1.0)
			maxEnergy := float32(0.0)
			start := wstart + int(ics.SwbOffset[g])
			freq := float32(start-wstart) * freqMult
			freqBoost := max(0.88*freq/noiseLowLimit, 1.0)
			if freq < noiseLowLimit || (start-wstart) >= cutoff {
				if !sce.Zeroes[w*16+g] {
					prevSf = sce.SfIdx[w*16+g]
				}
				continue
			}
			for w2 := range ics.GroupLen[w] {
				band := &psy[(w+w2)*16+g]
				sfbEnergy += band.Energy
				spread = min(spread, band.Spread)
				threshold += band.Threshold
				if w2 == 0 {
					minEnergy = band.Energy
					maxEnergy = band.Energy
				} else {
					minEnergy = min(minEnergy, band.Energy)
					maxEnergy = max(maxEnergy, band.Energy)
				}
			}

			// ramps down at ~8000Hz and loosens the dist threshold
			distThresh = clipf(2.5*noiseLowLimit/freq, 0.5, 2.5) * distBias

			// PNS is acceptable when the band is noise-like, near
			// threshold, and (for short groups) of stable energy; point 2
			// is relaxed for zeroed bands near the noise threshold.
			if (!sce.Zeroes[w*16+g] && !SfdeltaCanRemoveBand(sce, &nextband, prevSf, w*16+g)) ||
				((sce.Zeroes[w*16+g] || sce.BandAlt[w*16+g] == 0) &&
					sfbEnergy < threshold*fmath.Sqrt32(1.0/freqBoost)) ||
				spread < spreadThreshold ||
				(!sce.Zeroes[w*16+g] && sce.BandAlt[w*16+g] != 0 &&
					sfbEnergy > threshold*thrMult*freqBoost) ||
				minEnergy < pnsTransientEnergyR*maxEnergy {
				sce.PnsEner[w*16+g] = sfbEnergy
				if !sce.Zeroes[w*16+g] {
					prevSf = sce.SfIdx[w*16+g]
				}
				continue
			}

			pnsTgtEnergy = sfbEnergy * min(1.0, spread*spread)
			noiseSfi = clip(int(fmath.Round32(fmath.Log232(pnsTgtEnergy)*2)), -100, 155)
			noiseAmp = -tables.Pow2SF[noiseSfi+tables.PowSF2Zero] // dequantize
			if prev != -1000 {
				noiseSfdiff := noiseSfi - prev + ScaleDiffZero
				if noiseSfdiff < 0 || noiseSfdiff > 2*ScaleMaxDiff {
					if !sce.Zeroes[w*16+g] {
						prevSf = sce.SfIdx[w*16+g]
					}
					continue
				}
			}
			for w2 := range ics.GroupLen[w] {
				size := int(ics.SwbSizes[g])
				startC := (w+w2)*128 + int(ics.SwbOffset[g])
				band := &psy[(w+w2)*16+g]
				for i := range size {
					c.RandomState = lcgRandom(uint32(c.RandomState))
					pns[i] = float32(c.RandomState)
				}
				bandEnergy := scalarProduct(pns[:size], pns[:size])
				scale := noiseAmp / fmath.Sqrt32(bandEnergy)
				for i := range size {
					pns[i] *= scale
				}
				pnsSenergy := scalarProduct(pns[:size], pns[:size])
				pnsEnergy += pnsSenergy
				dsp.AbsPow34(nor34[:size], sce.Coeffs[startC:startC+size])
				dsp.AbsPow34(pns34[:size], pns[:size])
				dist1 += c.quantizeBandCost(sce.Coeffs[startC:startC+size],
					nor34[:size], sce.SfIdx[(w+w2)*16+g],
					sce.BandAlt[(w+w2)*16+g],
					lambda/band.Threshold, fmath.Inf32(), nil, nil)
				// rd on average: 5 bits for SF, 4 for the CB, plus spread
				// energy * lambda/thr
				dist2 += band.Energy / (band.Spread * band.Spread) * lambda *
					distThresh / band.Threshold
			}
			if g != 0 && sce.BandType[w*16+g-1] == NoiseBT {
				dist2 += 5
			} else {
				dist2 += 9
			}
			energyRatio = pnsTgtEnergy / pnsEnergy // compensates quantization error
			sce.PnsEner[w*16+g] = energyRatio * pnsTgtEnergy
			if sce.Zeroes[w*16+g] || sce.BandAlt[w*16+g] == 0 ||
				(energyRatio > 0.85 && energyRatio < 1.25 && dist2 < dist1) {
				sce.BandType[w*16+g] = NoiseBT
				sce.Zeroes[w*16+g] = false
				prev = noiseSfi
			} else if !sce.Zeroes[w*16+g] {
				prevSf = sce.SfIdx[w*16+g]
			}
		}
	}
}

// scalarProduct mirrors the archive's ff_scalarproduct_float_c
// @ d09d5afc3a as clang compiles it on this platform: the multiply-add is
// FMA-contracted, so the naive Go form (which gc also fuses on arm64) is
// the matching translation. Do NOT add a rounding guard here.
func scalarProduct(v1, v2 []float32) float32 {
	p := float32(0.0)
	for i := range v1 {
		p += v1[i] * v2[i]
	}
	return p
}
