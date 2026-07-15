// SPDX-License-Identifier: LGPL-2.1-or-later

package psy

import (
	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/fmath"
)

// calcBitDemand computes the frame bit allocation from the bit reservoir.
// Mirrors aacpsy.c:calc_bit_demand @ d09d5afc3a (3GPP TS26.403 5.6.1.2
// "Calculation of Bit Demand").
func (ctx *Context) calcBitDemand(pe float32, bits, size int, shortWindow bool) int {
	var bitsaveSlope, bitsaveAdd, bitspendSlope, bitspendAdd, clipLow, clipHigh float32
	if shortWindow {
		bitsaveSlope = psy3gppSaveSlopeS
		bitsaveAdd = psy3gppSaveAddS
		bitspendSlope = psy3gppSpendSlopeS
		bitspendAdd = psy3gppSpendAddS
		clipLow = psy3gppClipLoS
		clipHigh = psy3gppClipHiS
	} else {
		bitsaveSlope = psy3gppSaveSlopeL
		bitsaveAdd = psy3gppSaveAddL
		bitspendSlope = psy3gppSpendSlopeL
		bitspendAdd = psy3gppSpendAddL
		clipLow = psy3gppClipLoL
		clipHigh = psy3gppClipHiL
	}

	ctx.fillLevel += ctx.frameBits - bits
	ctx.fillLevel = clipi(ctx.fillLevel, 0, size)
	fillLevel := clipf(float32(ctx.fillLevel)/float32(size), clipLow, clipHigh)
	clippedPe := clipf(pe, ctx.pe.min, ctx.pe.max)
	bitSave := (fillLevel + bitsaveAdd) * bitsaveSlope
	bitSpend := (fillLevel + bitspendAdd) * bitspendSlope
	// The bit factor formula deviates from the (incorrect) spec graph; see
	// the comment block at aacpsy.c:534-539.
	bitFactor := 1.0 - bitSave + ((bitSpend-bitSave)/(ctx.pe.max-ctx.pe.min))*(clippedPe-ctx.pe.min)
	// Slowly forget pe.min when pe stays above the mean (aacpsy.c:541-548).
	ctx.pe.max = max(pe, ctx.pe.max)
	forgetfulMinPe := ((ctx.pe.min * psyPeForgetSlope) +
		max(ctx.pe.min, pe*(pe/ctx.pe.max))) / (psyPeForgetSlope + 1)
	ctx.pe.min = min(pe, forgetfulMinPe)

	// Allocate a minimum of 1/8th average frame bits to avoid reservoir
	// starvation from producing zero-bit frames.
	return int(min(float32(ctx.frameBits)*bitFactor,
		float32(max(ctx.frameBits+size-bits, ctx.frameBits/8))))
}

// calcPE3gpp computes the perceptual entropy of one band.
// Mirrors aacpsy.c:calc_pe_3gpp @ d09d5afc3a.
func calcPE3gpp(band *band) float32 {
	band.pe = 0.0
	band.peConst = 0.0
	band.activeLines = 0.0
	if band.energy > band.thr {
		a := fmath.Log232(band.energy)
		pe := a - fmath.Log232(band.thr)
		band.activeLines = band.nzLines
		if pe < psy3gppC1 {
			pe = pe*psy3gppC3 + psy3gppC2
			a = a*psy3gppC3 + psy3gppC2
			band.activeLines *= psy3gppC3
		}
		band.pe = pe * band.nzLines
		band.peConst = a * band.nzLines
	}
	return band.pe
}

// calcReduction3gpp computes the threshold reduction value.
// Mirrors aacpsy.c:calc_reduction_3gpp @ d09d5afc3a.
func calcReduction3gpp(a, desiredPe, pe, activeLines float32) float32 {
	if activeLines == 0.0 {
		return 0
	}
	thrAvg := fmath.Exp232((a - pe) / (4.0 * activeLines))
	reduction := fmath.Exp232((a-desiredPe)/(4.0*activeLines)) - thrAvg
	return max(reduction, 0.0)
}

// calcReducedThr3gpp applies a reduction to one band's threshold.
// Mirrors aacpsy.c:calc_reduced_thr_3gpp @ d09d5afc3a, including the
// deviation from the 3GPP spec described in its comment: min() against
// energy/min_snr applies only to bands with hole avoidance on.
func calcReducedThr3gpp(band *band, minSnr, reduction float32) float32 {
	thr := band.thr
	if band.energy > thr {
		thr = fmath.Sqrt32(thr)
		thr = fmath.Sqrt32(thr) + reduction
		thr *= thr
		thr *= thr
		if thr > band.energy*minSnr && band.avoidHoles != ahNone {
			thr = max(band.thr, band.energy*minSnr)
			band.avoidHoles = ahActive
		}
	}
	return thr
}

// calcThr3gpp computes per-band energies, initial thresholds and non-zero
// line estimates. Mirrors aacpsy.c:calc_thr_3gpp @ d09d5afc3a (3GPP
// TS26.403 5.4.2 "Threshold Calculation").
func calcThr3gpp(wi *WindowInfo, numBands int, pch *channel, bandSizes []uint8,
	coefs []float32, cutoff int) {
	start := 0
	for w := 0; w < wi.NumWindows*16; w += 16 {
		wstart := 0
		for g := range numBands {
			band := &pch.band[w+g]

			var formFactor float32
			var temp float32
			band.energy = 0.0
			if wstart < cutoff {
				for i := range int(bandSizes[g]) {
					t := coefs[start+i] * coefs[start+i]
					band.energy += t
					formFactor += fmath.Sqrt32(absf(coefs[start+i]))
				}
			}
			if band.energy > 0 {
				temp = fmath.Sqrt32(float32(bandSizes[g]) / band.energy)
			} else {
				temp = 0
			}
			band.thr = band.energy * 0.001258925
			band.nzLines = formFactor * fmath.Sqrt32(temp)

			start += int(bandSizes[g])
			wstart += int(bandSizes[g])
		}
	}
}

// analyzeChannel computes band thresholds for one channel as suggested in
// 3GPP TS26.403. Mirrors aacpsy.c:psy_3gpp_analyze_channel @ d09d5afc3a.
// The QSCALE branch is not ported (ABR only in Phase 2). The complexity
// waiver covers a faithful port of a single 200-line C function; splitting
// the threshold-reduction stages would break the line-by-line mapping to
// the pinned source (docs/porting-guide.md ground rule 1).
//
//nolint:gocognit,gocyclo // faithful port of one C function, see doc comment
func (ctx *Context) analyzeChannel(ch int, coefs []float32, wi *WindowInfo) {
	pch := &ctx.pch[ch]
	var desiredBits, desiredPe, deltaPe float32
	reduction := fmath.NaN32()
	var spreadEn [128]float32
	var a, activeLines, normFac float32
	var pe float32
	if ctx.chanBitrate > 32000 {
		pe = 0.0
	} else {
		pe = max(50.0, 100.0-float32(ctx.chanBitrate)*100.0/32000.0)
	}
	short := 0
	if wi.NumWindows == 8 {
		short = 1
	}
	numBands := ctx.NumBands[short]
	bandSizes := ctx.Bands[short]
	coeffs := &ctx.psyCoef[short]
	var avoidHoleThr float32 = psy3gppAhThrLong
	if wi.NumWindows == 8 {
		avoidHoleThr = psy3gppAhThrShort
	}
	bandwidth := ctx.Cutoff
	cutoff := bandwidth * 2048 / wi.NumWindows / ctx.sampleRate

	// energies, initial thresholds - 5.4.2 "Threshold Calculation"
	calcThr3gpp(wi, numBands, pch, bandSizes, coefs, cutoff)

	// spread, threshold in quiet, pre-echo control
	for w := 0; w < wi.NumWindows*16; w += 16 {
		bands := pch.band[w:]

		// 5.4.2.3 "Spreading" & 5.4.3 "Spread Energy Calculation".
		// NOTE: the C seeds spread_en[0] (not spread_en[w]) for every
		// window (aacpsy.c:693); ported as written.
		spreadEn[0] = bands[0].energy
		for g := 1; g < numBands; g++ {
			bands[g].thr = max(bands[g].thr, bands[g-1].thr*coeffs[g].spreadHi[0])
			spreadEn[w+g] = max(bands[g].energy, spreadEn[w+g-1]*coeffs[g].spreadHi[1])
		}
		for g := numBands - 2; g >= 0; g-- {
			bands[g].thr = max(bands[g].thr, bands[g+1].thr*coeffs[g].spreadLow[0])
			spreadEn[w+g] = max(spreadEn[w+g], spreadEn[w+g+1]*coeffs[g].spreadLow[1])
		}
		// 5.4.2.4 "Threshold in quiet"
		for g := range numBands {
			band := &bands[g]

			band.thr = max(band.thr, coeffs[g].ath)
			band.thrQuiet = band.thr
			// 5.4.2.5 "Pre-echo control". The C condition is
			// !(type[0] == LONG_STOP || (!w && type[1] == LONG_START))
			// (aacpsy.c:708); De Morgan applied for staticcheck QF1001.
			if wi.WindowType[0] != coder.LongStopSequence &&
				(w != 0 || wi.WindowType[1] != coder.LongStartSequence) {
				band.thr = max(psy3gppRPEMin*band.thr,
					min(band.thr, psy3gppRPELev*pch.prevBand[w+g].thrQuiet))
			}

			// 5.6.1.3.1 "Preparatory steps of the perceptual entropy calculation"
			pe += calcPE3gpp(band)
			a += band.peConst
			activeLines += band.activeLines

			// 5.6.1.3.3 "Selection of the bands for avoidance of holes"
			if spreadEn[w+g]*avoidHoleThr > band.energy || coeffs[g].minSnr > 1.0 {
				band.avoidHoles = ahNone
			} else {
				band.avoidHoles = ahInactive
			}
		}
	}

	// 5.6.1.3.2 "Calculation of the desired perceptual entropy"
	ctx.Ch[ch].Entropy = pe
	desiredBits = float32(ctx.calcBitDemand(pe, ctx.Bitres.Bits, ctx.Bitres.Size,
		wi.NumWindows == 8))
	desiredPe = bitsToPE(desiredBits)
	// NOTE: PE correction is kept simple (aacpsy.c:747-750).
	if ctx.Bitres.Bits > 0 {
		desiredPe *= clipf(ctx.pe.previous/bitsToPE(float32(ctx.Bitres.Bits)),
			0.85, 1.15)
	}
	ctx.pe.previous = bitsToPE(desiredBits)
	ctx.Bitres.Alloc = int(desiredBits)

	if desiredPe < pe {
		// 5.6.1.3.4 "First Estimation of the reduction value"
		for w := 0; w < wi.NumWindows*16; w += 16 {
			reduction = calcReduction3gpp(a, desiredPe, pe, activeLines)
			pe = 0.0
			a = 0.0
			activeLines = 0.0
			for g := range numBands {
				band := &pch.band[w+g]

				band.thr = calcReducedThr3gpp(band, coeffs[g].minSnr, reduction)
				pe += calcPE3gpp(band)
				a += band.peConst
				activeLines += band.activeLines
			}
		}

		// 5.6.1.3.5 "Second Estimation of the reduction value"
		for range 2 {
			var peNoAh, desiredPeNoAh float32
			activeLines = 0.0
			a = 0.0
			for w := 0; w < wi.NumWindows*16; w += 16 {
				for g := range numBands {
					band := &pch.band[w+g]

					if band.avoidHoles != ahActive {
						peNoAh += band.pe
						a += band.peConst
						activeLines += band.activeLines
					}
				}
			}
			desiredPeNoAh = max(desiredPe-(pe-peNoAh), 0.0)
			if activeLines > 0.0 {
				reduction = calcReduction3gpp(a, desiredPeNoAh, peNoAh, activeLines)
			}

			pe = 0.0
			for w := 0; w < wi.NumWindows*16; w += 16 {
				for g := range numBands {
					band := &pch.band[w+g]

					if activeLines > 0.0 {
						band.thr = calcReducedThr3gpp(band, coeffs[g].minSnr, reduction)
					}
					pe += calcPE3gpp(band)
					if band.thr > 0.0 {
						band.normFac = band.activeLines / band.thr
					} else {
						band.normFac = 0.0
					}
					normFac += band.normFac
				}
			}
			deltaPe = desiredPe - pe
			if absf(deltaPe) > 0.05*desiredPe {
				break
			}
		}

		if pe < 1.15*desiredPe {
			// 6.6.1.3.6 "Final threshold modification by linearization"
			if normFac != 0 {
				normFac = 1.0 / normFac
			} else {
				normFac = 0
			}
			for w := 0; w < wi.NumWindows*16; w += 16 {
				for g := range numBands {
					band := &pch.band[w+g]

					if band.activeLines > 0.5 {
						deltaSfbPe := band.normFac * normFac * deltaPe
						thr := band.thr

						thr *= fmath.Exp232(deltaSfbPe / band.activeLines)
						if thr > coeffs[g].minSnr*band.energy && band.avoidHoles == ahInactive {
							thr = max(band.thr, coeffs[g].minSnr*band.energy)
						}
						band.thr = thr
					}
				}
			}
		} else {
			// 5.6.1.3.7 "Further perceptual entropy reduction"
			g := numBands
			for pe > desiredPe && g > 0 {
				g--
				for w := 0; w < wi.NumWindows*16; w += 16 {
					band := &pch.band[w+g]
					if band.avoidHoles != ahNone && coeffs[g].minSnr < psySnr1dB {
						coeffs[g].minSnr = psySnr1dB
						band.thr = band.energy * psySnr1dB
						pe += band.activeLines*1.5 - band.pe
					}
				}
			}
		}
	}

	for w := 0; w < wi.NumWindows*16; w += 16 {
		for g := range numBands {
			band := &pch.band[w+g]
			psyBand := &ctx.Ch[ch].PsyBands[w+g]

			psyBand.Threshold = band.thr
			psyBand.Energy = band.energy
			psyBand.Spread = band.activeLines * 2.0 / float32(bandSizes[g])
			psyBand.Bits = int32(peToBits(band.pe))
		}
	}

	pch.prevBand = pch.band
}

// Analyze performs the psychoacoustic analysis for the channel group
// starting at startCh. Mirrors aacpsy.c:psy_3gpp_analyze @ d09d5afc3a,
// including the rate-loop rewind: the encoder's rate-control loop may
// re-run the analysis for the same frame, and carried state (bit
// reservoir, PE history, previous-frame thresholds) must advance exactly
// once per frame, so it is saved on the frame's first run and rewound on
// re-runs. The frameNum parameter replaces avctx->frame_num. The channel
// group is the context's only group (mono SCE or one CPE), so
// ff_psy_find_group collapses to ctx.channels.
func (ctx *Context) Analyze(frameNum int64, startCh int, coeffs [][]float32, wi []WindowInfo) {
	if frameNum != ctx.rcFrameNum {
		ctx.rcFrameNum = frameNum
		ctx.rcFirstCh = startCh
		ctx.rcFillLevel = ctx.fillLevel
		ctx.rcPeMin = ctx.pe.min
		ctx.rcPeMax = ctx.pe.max
		ctx.rcPePrevious = ctx.pe.previous
	} else if startCh == ctx.rcFirstCh {
		ctx.fillLevel = ctx.rcFillLevel
		ctx.pe.min = ctx.rcPeMin
		ctx.pe.max = ctx.rcPeMax
		ctx.pe.previous = ctx.rcPePrevious
	}

	for ch := range ctx.channels {
		pch := &ctx.pch[startCh+ch]
		if frameNum != pch.rcFrameNum {
			pch.rcFrameNum = frameNum
			pch.rcPrevBand = pch.prevBand
		} else {
			pch.prevBand = pch.rcPrevBand
		}
		ctx.analyzeChannel(startCh+ch, coeffs[ch], &wi[ch])
	}
}
