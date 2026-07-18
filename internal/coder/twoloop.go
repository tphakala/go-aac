// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// fastPowf mirrors libavutil/ffmath.h:ff_fast_powf @ d09d5afc3a:
// expf(logf(x) * y).
func fastPowf(x, y float32) float32 {
	return fmath.Exp32(fmath.Log32(x) * y)
}

// sqrf mirrors ff_sqrf: x*x.
func sqrf(x float32) float32 { return x * x }

// pnsBits reflects the cost to change codebooks for a PNS band. Mirrors
// aaccoder_twoloop.h:ff_pns_bits @ d09d5afc3a.
func pnsBits(sce *SingleChannelElement, w, g int) int {
	if g == 0 || !sce.Zeroes[w*16+g-1] || !sce.CanPNS[w*16+g-1] {
		return 9
	}
	return 5
}

// FindFormFactor estimates how noisy or tonal a band is from the
// maxval-energy relation. Mirrors aacenc_utils.h:find_form_factor
// @ d09d5afc3a.
func FindFormFactor(groupLen, swbSize int, thresh float32, scaled []float32,
	nzslope float32) float32 {
	iswbSize := 1.0 / float32(swbSize)
	iswbSizem1 := 1.0 / float32(swbSize-1)
	ethresh := thresh
	form, weight := float32(0.0), float32(0.0)
	for w2 := range groupLen {
		var e, e2, variance, maxval float32
		var nzl float32
		for i := range swbSize {
			s := fmath.Absf(scaled[w2*128+i])
			maxval = max(maxval, s)
			e += s
			s = float32(s * s) // e2 += s *= s, without cross-statement FMA
			e2 += s
			// Fall steeply towards zero below the threshold, but smoothly.
			if s >= ethresh {
				nzl += 1.0
			} else {
				if nzslope == 2.0 {
					t := float32((s / ethresh) * (s / ethresh)) // no FMA
					nzl += t
				} else {
					nzl += fastPowf(s/ethresh, nzslope)
				}
			}
		}
		if e2 > thresh {
			e = float32(e * iswbSize) // rounding barrier: no deferred FMA

			// compute variance
			for i := range swbSize {
				d := fmath.Absf(scaled[w2*128+i]) - e
				t := float32(d * d) // no cross-statement FMA
				variance += t
			}
			variance = fmath.Sqrt32(variance * iswbSizem1)

			e2 = float32(e2 * iswbSize) // rounding barrier: no deferred FMA
			frm := e / min(e+4*variance, maxval)
			form += e2 * fmath.Sqrt32(frm) / max(0.5, nzl)
			weight += e2
		}
	}
	if weight > 0 {
		return form / weight
	}
	return 1.0
}

// SearchForQuantizersTwoLoop is the two-loop quantizer search taken from
// ISO 13818-7 Appendix C: derive zero bands and distortion limits from the
// psy output, apply noisy-band depriorization and tonal prioritization to
// the limits, then iterate an inner bit-fitting loop and an outer
// quality-improvement loop, optionally trading overdistorted bands for
// PNS-able zeroes under bit starvation. Mirrors
// aaccoder_twoloop.h:search_for_quantizers_twoloop @ d09d5afc3a. The
// QSCALE (constant quality) branches are not ported: the pipeline never
// sets AV_CODEC_FLAG_QSCALE (no VBR mode in the API); this also makes the
// C's pre-branch sfoffs computation dead, so it is omitted. Splitting this
// faithfully ported C function would break the line-by-line mapping to the
// pinned source, hence the complexity waiver.
//
//nolint:gocognit,gocyclo,maintidx // faithful port of one C function, see doc comment
func (c *Coder) SearchForQuantizersTwoLoop(bitRate, sampleRate, channels,
	bitresAlloc, bandwidth int, pns bool, sce *SingleChannelElement,
	psy *[128]PsyBand, lambda float32) {
	ics := &sce.ICS
	destbits := int(float64(bitRate) * 1024.0 / float64(sampleRate) /
		float64(channels) * float64(lambda/120.0))
	var nzs [128]int8
	var nextband [128]uint8
	var maxsf, minsf [128]int
	var dists, qenergies [128]float32
	var uplims, euplims, energies [128]float32
	var maxvals, spreadThrR [128]float32
	var minSpreadThrR, maxSpreadThrR float32

	// rdlambda controls the maximum tolerated distortion; rdmin/rdmax the
	// relative deviation allowed for tonality compensation.
	rdlambda := fmath.Clipf(2.0*120.0/lambda, 0.0625, 16.0)
	const nzslope = 1.5
	rdmin := float32(0.03125)
	rdmax := float32(1.0)

	its := 0
	maxits := 30
	// allz keeps the C variable name (aaccoder_twoloop.h:106). Despite the
	// name it is set true when ANY band is non-zero, so the later !allz test
	// means the whole frame quantized to zero.
	allz := false
	var tbits int
	var recomprd, fflag bool
	var prev int

	// zeroscale: force a zero when band energy is below this multiple of
	// the threshold.
	zeroscale := float32(1.0)
	if lambda > 120.0 {
		zeroscale = fmath.Clipf(fmath.Pow32(120.0/lambda, 0.25), 0.0625, 1.0)
	}

	if bitresAlloc >= 0 {
		// psy granted us extra bits from the reservoir; adjust for lambda
		// except what psy already did.
		destbits = int(float32(bitresAlloc) * (lambda / 120))
	}

	// ABR: strict but with leeway so RC can track the rate smoothly.
	toomanybits := destbits + destbits/8
	toofewbits := destbits - destbits/8
	sfoffs := float32(0)
	rdlambda = sqrtf32(rdlambda)

	// zero out above the cutoff frequency (fixed at init, shared with psy)
	wlen := 1024 / ics.NumWindows
	cutoff := bandwidth * 2 * wlen / sampleRate
	pnsStartPos := noiseLowLimit * 2 * wlen / sampleRate

	// above this the decoder might loop endlessly
	destbits = min(destbits, 5800)
	toomanybits = min(toomanybits, 5800)
	toofewbits = min(toofewbits, 5800)

	// determine zero bands and upper distortion limits
	minSpreadThrR = -1
	maxSpreadThrR = -1
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		start := 0
		for g := range ics.NumSwb {
			nz := 0
			var uplim, energy, spread float32
			for w2 := range ics.GroupLen[w] {
				band := &psy[(w+w2)*16+g]
				if start >= cutoff || band.Energy <= band.Threshold*zeroscale ||
					band.Threshold == 0.0 {
					sce.Zeroes[(w+w2)*16+g] = true
					continue
				}
				nz = 1
			}
			if nz == 0 {
				uplim = 0.0
			} else {
				nz = 0
				for w2 := range ics.GroupLen[w] {
					band := &psy[(w+w2)*16+g]
					if band.Energy <= band.Threshold*zeroscale || band.Threshold == 0.0 {
						continue
					}
					uplim += band.Threshold
					energy += band.Energy
					spread += band.Spread
					nz++
				}
			}
			uplims[w*16+g] = uplim
			energies[w*16+g] = energy
			nzs[w*16+g] = int8(nz)
			sce.Zeroes[w*16+g] = nz == 0
			allz = allz || nz != 0
			if nz != 0 && sce.CanPNS[w*16+g] {
				spreadThrR[w*16+g] = energy * float32(nz) / (uplim * spread)
				if minSpreadThrR < 0 {
					minSpreadThrR = spreadThrR[w*16+g]
					maxSpreadThrR = spreadThrR[w*16+g]
				} else {
					minSpreadThrR = min(minSpreadThrR, spreadThrR[w*16+g])
					maxSpreadThrR = max(maxSpreadThrR, spreadThrR[w*16+g])
				}
			}
			start += int(ics.SwbSizes[g])
		}
	}

	// compute initial scalers
	minscaler := 65535
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		for g := range ics.NumSwb {
			if sce.Zeroes[w*16+g] {
				sce.SfIdx[w*16+g] = ScaleOnePos
				continue
			}
			// a lower log2f-to-distortion ratio (1.75) makes scaling more
			// conservative and robust for low-frequency signals
			t := float64(1.75 * float64(fmath.Log232(
				max(0.00125, uplims[w*16+g])/float32(ics.SwbSizes[g])))) // no FMA
			sce.SfIdx[w*16+g] = clip(int(ScaleOnePos+t+float64(sfoffs)),
				60, ScaleMaxPos)
			minscaler = min(minscaler, sce.SfIdx[w*16+g])
		}
	}

	// clip
	minscaler = clip(minscaler, ScaleOnePos-ScaleDiv512, ScaleMaxPos-ScaleDiv512)
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		for g := range ics.NumSwb {
			if !sce.Zeroes[w*16+g] {
				sce.SfIdx[w*16+g] = clip(sce.SfIdx[w*16+g], minscaler,
					minscaler+ScaleMaxDiff-1)
			}
		}
	}

	if !allz {
		return
	}
	dsp.AbsPow34(c.scoefs[:], sce.Coeffs[:])
	c.CacheInit()

	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		start := w * 128
		for g := range ics.NumSwb {
			maxvals[w*16+g] = FindMaxVal(ics.GroupLen[w], int(ics.SwbSizes[g]),
				c.scoefs[start:])
			if maxvals[w*16+g] > 0 {
				minsfidx := int(Coef2MinSF(maxvals[w*16+g]))
				for w2 := range ics.GroupLen[w] {
					minsf[(w+w2)*16+g] = minsfidx
				}
			}
			start += int(ics.SwbSizes[g])
		}
	}

	// Scale uplims to match rate distortion to quality: noisy band
	// depriorization and tonal band prioritization from the maxval-energy
	// ratio.
	euplims = uplims
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		// psy already prioritizes transients to some extent
		dePsyFactor := float32(1.0)
		if ics.NumWindows > 1 {
			dePsyFactor = 8.0 / float32(ics.GroupLen[w])
		}
		start := w * 128
		for g := range ics.NumSwb {
			if nzs[g] > 0 { // upstream reads nzs[g], not nzs[w*16+g]
				cleanupFactor := sqrf(fmath.Clipf(float32(start)/(float32(cutoff)*0.75), 1.0, 2.0))
				// upstream divides by swb_sizes[w], not swb_sizes[g]
				energy2uplim := FindFormFactor(ics.GroupLen[w], int(ics.SwbSizes[g]),
					uplims[w*16+g]/float32(int(nzs[g])*int(ics.SwbSizes[w])),
					sce.Coeffs[start:], nzslope*cleanupFactor)
				energy2uplim *= dePsyFactor
				// ABR: prioritize less, let rate control do its thing
				energy2uplim = sqrtf32(energy2uplim)
				energy2uplim = max(0.015625, min(1.0, energy2uplim))
				uplims[w*16+g] *= fmath.Clipf(rdlambda*energy2uplim, rdmin, rdmax) *
					float32(ics.GroupLen[w])

				energy2uplim = FindFormFactor(ics.GroupLen[w], int(ics.SwbSizes[g]),
					uplims[w*16+g]/float32(int(nzs[g])*int(ics.SwbSizes[w])),
					sce.Coeffs[start:], 2.0)
				energy2uplim *= dePsyFactor
				energy2uplim = sqrtf32(energy2uplim)
				energy2uplim = max(0.015625, min(1.0, energy2uplim))
				euplims[w*16+g] *= fmath.Clipf(rdlambda*energy2uplim*float32(ics.GroupLen[w]),
					0.5, 1.0)
			}
			start += int(ics.SwbSizes[g])
		}
	}

	for i := range maxsf {
		maxsf[i] = ScaleMaxPos
	}

	// two-loop search: outer improves quality, inner fits the bit budget
	for {
		var overdist int
		qstep := 1
		if its == 0 {
			qstep = 32
		}
		for {
			changed := false
			prev = -1
			recomprd = false
			tbits = 0
			//nolint:dupl // the C duplicates this distortion loop verbatim
			for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
				start := w * 128
				for g := range ics.NumSwb {
					bits := 0
					var dist, qenergy float32

					if sce.Zeroes[w*16+g] || sce.SfIdx[w*16+g] >= 218 {
						start += int(ics.SwbSizes[g])
						if sce.CanPNS[w*16+g] {
							tbits += pnsBits(sce, w, g) // PNS isn't free
						}
						continue
					}
					cb := FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
					for w2 := range ics.GroupLen[w] {
						b := 0
						var sqenergy float32
						size := int(ics.SwbSizes[g])
						dist += c.quantizeBandCostCached(w+w2, g,
							sce.Coeffs[start+w2*128:start+w2*128+size],
							c.scoefs[start+w2*128:start+w2*128+size],
							sce.SfIdx[w*16+g], cb, 1.0, fmath.Inf32(),
							&b, &sqenergy, 0)
						bits += b
						qenergy += sqenergy
					}
					dists[w*16+g] = dist - float32(bits)
					qenergies[w*16+g] = qenergy
					if prev != -1 {
						sfdiff := clip(sce.SfIdx[w*16+g]-prev+ScaleDiffZero,
							0, 2*ScaleMaxDiff)
						bits += int(tables.ScalefactorBits[sfdiff])
					}
					tbits += bits
					start += int(ics.SwbSizes[g])
					prev = sce.SfIdx[w*16+g]
				}
			}
			if tbits > toomanybits {
				recomprd = true
				for i := range 128 {
					if sce.SfIdx[i] < ScaleMaxPos-ScaleDiv512 {
						maxsfI := maxsf[i]
						if tbits > 5800 {
							maxsfI = ScaleMaxPos
						}
						newSf := min(maxsfI, sce.SfIdx[i]+qstep)
						if newSf != sce.SfIdx[i] {
							sce.SfIdx[i] = newSf
							changed = true
						}
					}
				}
			} else if tbits < toofewbits {
				recomprd = true
				for i := range 128 {
					if sce.SfIdx[i] > ScaleOnePos {
						newSf := max(minsf[i], ScaleOnePos, sce.SfIdx[i]-qstep)
						if newSf != sce.SfIdx[i] {
							sce.SfIdx[i] = newSf
							changed = true
						}
					}
				}
			}
			qstep >>= 1
			if qstep == 0 && tbits > toomanybits && sce.SfIdx[0] < 217 && changed {
				qstep = 1
			}
			if qstep == 0 {
				break
			}
		}

		overdist = 1
		fflag = tbits < toofewbits
		for i := 0; i < 2 && (overdist != 0 || recomprd); i++ {
			if recomprd {
				// must recompute distortion
				prev = -1
				tbits = 0
				//nolint:dupl // the C duplicates this distortion loop verbatim
				for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
					start := w * 128
					for g := range ics.NumSwb {
						bits := 0
						var dist, qenergy float32

						if sce.Zeroes[w*16+g] || sce.SfIdx[w*16+g] >= 218 {
							start += int(ics.SwbSizes[g])
							if sce.CanPNS[w*16+g] {
								tbits += pnsBits(sce, w, g)
							}
							continue
						}
						cb := FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
						for w2 := range ics.GroupLen[w] {
							b := 0
							var sqenergy float32
							size := int(ics.SwbSizes[g])
							dist += c.quantizeBandCostCached(w+w2, g,
								sce.Coeffs[start+w2*128:start+w2*128+size],
								c.scoefs[start+w2*128:start+w2*128+size],
								sce.SfIdx[w*16+g], cb, 1.0, fmath.Inf32(),
								&b, &sqenergy, 0)
							bits += b
							qenergy += sqenergy
						}
						dists[w*16+g] = dist - float32(bits)
						qenergies[w*16+g] = qenergy
						if prev != -1 {
							sfdiff := clip(sce.SfIdx[w*16+g]-prev+ScaleDiffZero,
								0, 2*ScaleMaxDiff)
							bits += int(tables.ScalefactorBits[sfdiff])
						}
						tbits += bits
						start += int(ics.SwbSizes[g])
						prev = sce.SfIdx[w*16+g]
					}
				}
			}
			if i == 0 && pns && its > maxits/2 && tbits > toofewbits {
				var maxoverdist float32
				ovrfactor := 1.0 + float32(maxits-its)*16.0/float32(maxits)
				overdist = 0
				recomprd = false
				for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
					for g := range ics.NumSwb {
						if !sce.Zeroes[w*16+g] && sce.SfIdx[w*16+g] > ScaleOnePos &&
							dists[w*16+g] > uplims[w*16+g]*ovrfactor {
							ovrdist := dists[w*16+g] / max(uplims[w*16+g], euplims[w*16+g])
							maxoverdist = max(maxoverdist, ovrdist)
							overdist++
						}
					}
				}
				if overdist != 0 {
					// trade overdistorted bands for PNS-able zeroes, in the
					// lowest 1.25% spread-energy-threshold ranking
					minspread := maxSpreadThrR
					maxspread := minSpreadThrR
					var zspread float32
					zeroable := 0
					zeroed := 0
					for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
						start := 0
						for g := range ics.NumSwb {
							if start >= pnsStartPos && !sce.Zeroes[w*16+g] &&
								sce.CanPNS[w*16+g] {
								minspread = min(minspread, spreadThrR[w*16+g])
								maxspread = max(maxspread, spreadThrR[w*16+g])
								zeroable++
							}
							start += int(ics.SwbSizes[g])
						}
					}
					t := float32((maxspread - minspread) * 0.0125) // no FMA
					zspread = t + minspread
					// PNS only a fraction of the range depending on how
					// starved for bits we are
					t1 := float32(float32(toomanybits-tbits) * minSpreadThrR)
					t2 := float32(float32(tbits-toofewbits) * maxSpreadThrR)
					zspread = min(minSpreadThrR*8.0, zspread,
						(t1+t2)/float32(toomanybits-toofewbits+1))
					maxzeroed := min(zeroable, max(1, (zeroable*its+maxits-1)/(2*maxits)))
					for zloop := range 2 {
						// first pass: distorted stuff; second: anything viable
						loopovrfactor := ovrfactor
						loopminsf := ScaleOnePos
						if zloop != 0 {
							loopovrfactor = 1.0
							loopminsf = ScaleOnePos - ScaleDiv512
						}
						var mcb int
						for g := ics.NumSwb - 1; g > 0 && zeroed < maxzeroed; g-- {
							if int(ics.SwbOffset[g]) < pnsStartPos {
								continue
							}
							for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
								if !sce.Zeroes[w*16+g] && sce.CanPNS[w*16+g] &&
									spreadThrR[w*16+g] <= zspread &&
									sce.SfIdx[w*16+g] > loopminsf &&
									(dists[w*16+g] > loopovrfactor*uplims[w*16+g] ||
										func() bool {
											mcb = FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
											return mcb == 0
										}() ||
										(mcb <= 1 && dists[w*16+g] > min(uplims[w*16+g], euplims[w*16+g]))) {
									sce.Zeroes[w*16+g] = true
									sce.BandType[w*16+g] = 0
									zeroed++
								}
							}
						}
					}
					if zeroed != 0 {
						recomprd = true
						fflag = true
					}
				} else {
					overdist = 0
				}
			}
		}

		minscaler = ScaleMaxPos
		for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
			for g := range ics.NumSwb {
				if !sce.Zeroes[w*16+g] {
					minscaler = min(minscaler, sce.SfIdx[w*16+g])
				}
			}
		}

		minscaler = clip(minscaler, ScaleOnePos-ScaleDiv512, ScaleMaxPos-ScaleDiv512)
		nminscaler := minscaler
		prev = -1
		for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
			// start with big steps, end up fine-tuning
			depth := 10
			if its > maxits/2 {
				if its > maxits*2/3 {
					depth = 1
				} else {
					depth = 3
				}
			}
			edepth := depth + 2
			uplmax := float32(its)/(float32(maxits)*0.25) + 1.0
			if tbits > destbits {
				uplmax *= min(2.0, float32(tbits)/float32(max(1, destbits)))
			}
			start := w * 128
			for g := range ics.NumSwb {
				prevsc := sce.SfIdx[w*16+g]
				if prev < 0 && !sce.Zeroes[w*16+g] {
					prev = sce.SfIdx[0]
				}
				if !sce.Zeroes[w*16+g] {
					cmb := FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
					mindeltasf := max(0, prev-ScaleMaxDiff)
					maxdeltasf := min(ScaleMaxPos-ScaleDiv512, prev+ScaleMaxDiff)
					if (cmb == 0 || dists[w*16+g] > uplims[w*16+g]) &&
						sce.SfIdx[w*16+g] > max(mindeltasf, minsf[w*16+g]) {
						// make sure there is some energy in every nonzero
						// band; forcibly imbalanced or there is no net gain
						for i := 0; i < edepth && sce.SfIdx[w*16+g] > mindeltasf; i++ {
							bits := 0
							var dist, qenergy float32
							mb := FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g]-1)
							cb := FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
							if cb == 0 {
								maxsf[w*16+g] = min(sce.SfIdx[w*16+g]-1, maxsf[w*16+g])
							} else if i >= depth && dists[w*16+g] < euplims[w*16+g] {
								break
							}
							// the DC band is important: quantization error
							// there is intermodulation distortion
							if g == 0 && ics.NumWindows > 1 && dists[w*16+g] >= euplims[w*16+g] {
								maxsf[w*16+g] = min(sce.SfIdx[w*16+g], maxsf[w*16+g])
							}
							for w2 := range ics.GroupLen[w] {
								b := 0
								var sqenergy float32
								size := int(ics.SwbSizes[g])
								dist += c.quantizeBandCostCached(w+w2, g,
									sce.Coeffs[start+w2*128:start+w2*128+size],
									c.scoefs[start+w2*128:start+w2*128+size],
									sce.SfIdx[w*16+g]-1, cb, 1.0, fmath.Inf32(),
									&b, &sqenergy, 0)
								bits += b
								qenergy += sqenergy
							}
							sce.SfIdx[w*16+g]--
							dists[w*16+g] = dist - float32(bits)
							qenergies[w*16+g] = qenergy
							if mb != 0 && (sce.SfIdx[w*16+g] < mindeltasf ||
								(dists[w*16+g] < min(uplmax*uplims[w*16+g], euplims[w*16+g]) &&
									fmath.Absf(qenergies[w*16+g]-energies[w*16+g]) < euplims[w*16+g])) {
								break
							}
						}
					} else if tbits > toofewbits &&
						sce.SfIdx[w*16+g] < min(maxdeltasf, maxsf[w*16+g]) &&
						dists[w*16+g] < min(euplims[w*16+g], uplims[w*16+g]) &&
						fmath.Absf(qenergies[w*16+g]-energies[w*16+g]) < euplims[w*16+g] {
						// over target: save bits for more important stuff
						for i := 0; i < depth && sce.SfIdx[w*16+g] < maxdeltasf; i++ {
							cb := FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g]+1)
							if cb > 0 {
								bits := 0
								var dist, qenergy float32
								for w2 := range ics.GroupLen[w] {
									b := 0
									var sqenergy float32
									size := int(ics.SwbSizes[g])
									dist += c.quantizeBandCostCached(w+w2, g,
										sce.Coeffs[start+w2*128:start+w2*128+size],
										c.scoefs[start+w2*128:start+w2*128+size],
										sce.SfIdx[w*16+g]+1, cb, 1.0, fmath.Inf32(),
										&b, &sqenergy, 0)
									bits += b
									qenergy += sqenergy
								}
								dist -= float32(bits)
								if dist < min(euplims[w*16+g], uplims[w*16+g]) {
									sce.SfIdx[w*16+g]++
									dists[w*16+g] = dist
									qenergies[w*16+g] = qenergy
								} else {
									break
								}
							} else {
								maxsf[w*16+g] = min(sce.SfIdx[w*16+g], maxsf[w*16+g])
								break
							}
						}
					}
					sce.SfIdx[w*16+g] = clip(sce.SfIdx[w*16+g], mindeltasf, maxdeltasf)
					prev = sce.SfIdx[w*16+g]
					if sce.SfIdx[w*16+g] != prevsc {
						fflag = true
					}
					nminscaler = min(nminscaler, sce.SfIdx[w*16+g])
					sce.BandType[w*16+g] = FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
				}
				start += int(ics.SwbSizes[g])
			}
		}

		// SF difference limit violation risk: re-clamp
		prev = -1
		for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
			for g := range ics.NumSwb {
				if sce.Zeroes[w*16+g] {
					continue
				}
				prevsf := sce.SfIdx[w*16+g]
				if prev < 0 {
					prev = prevsf
				}
				sce.SfIdx[w*16+g] = clip(sce.SfIdx[w*16+g],
					prev-ScaleMaxDiff, prev+ScaleMaxDiff)
				sce.BandType[w*16+g] = FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
				prev = sce.SfIdx[w*16+g]
				if !fflag && prevsf != sce.SfIdx[w*16+g] {
					fflag = true
				}
			}
		}

		its++
		if !fflag || its >= maxits {
			break
		}
	}

	// scout out the next nonzero bands
	InitNextbandMap(sce, &nextband)

	prev = -1
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		// make sure proper codebooks are set
		for g := range ics.NumSwb {
			if !sce.Zeroes[w*16+g] {
				sce.BandType[w*16+g] = FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
				if sce.BandType[w*16+g] <= 0 {
					if !SfdeltaCanRemoveBand(sce, &nextband, prev, w*16+g) {
						// cannot zero out; make sure it is not attempted
						sce.BandType[w*16+g] = 1
					} else {
						sce.Zeroes[w*16+g] = true
						sce.BandType[w*16+g] = 0
					}
				}
			} else {
				sce.BandType[w*16+g] = 0
			}
			// no SF delta range violations (av_assert1 in the C)
			if !sce.Zeroes[w*16+g] {
				if prev == -1 && sce.Zeroes[0] {
					// set the global gain to something useful
					sce.SfIdx[0] = sce.SfIdx[w*16+g]
				}
				prev = sce.SfIdx[w*16+g]
			}
		}
	}
}

// sqrtf32 keeps the search reading like the C source it mirrors.
func sqrtf32(x float32) float32 { return fmath.Sqrt32(x) }
