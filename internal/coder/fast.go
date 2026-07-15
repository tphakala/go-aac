// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// SearchForQuantizersFast selects scalefactor indices and band codebooks
// with the fast two-loop heuristic: derive zero bands and distortion limits
// from the psy thresholds, estimate initial scalefactors, then run an inner
// bit-fitting loop against the frame bit budget and an outer
// quality-improvement loop. Mirrors aaccoder.c:search_for_quantizers_fast
// @ d09d5afc3a (lines 343-488). The complexity waiver covers a faithful
// port of a single C function: splitting the nested search loops would
// break the line-by-line mapping to the pinned source
// (docs/porting-guide.md ground rule 1).
//
//nolint:gocognit,gocyclo // faithful port of one C function, see doc comment
func (c *Coder) SearchForQuantizersFast(bitRate, sampleRate, channels int,
	sce *SingleChannelElement, psy *[128]PsyBand, lambda float32) {
	ics := &sce.ICS
	destbits := int(float64(bitRate) * 1024.0 / float64(sampleRate) /
		float64(channels) * float64(lambda/120.0))
	var dists, uplims, maxvals [128]float32
	its := 0
	allz := false
	minthr := fmath.Inf32()

	// for values above this the decoder might end up in an endless loop
	// due to always having more bits than what can be encoded.
	destbits = min(destbits, 5800)
	// some heuristic to determine initial quantizers will reduce search time
	// determine zero bands and upper limits
	ics.EachBand(ics.NumSwb, func(w, g, idx int) {
		nz := false
		var uplim float32
		for w2 := range ics.GroupLen[w] {
			band := &psy[(w+w2)*16+g]
			uplim += band.Threshold
			if band.Energy <= band.Threshold || band.Threshold == 0.0 {
				sce.Zeroes[(w+w2)*16+g] = true
				continue
			}
			nz = true
		}
		uplims[idx] = uplim * 512
		sce.BandType[idx] = 0
		sce.Zeroes[idx] = !nz
		if nz {
			minthr = min(minthr, uplim)
		}
		allz = allz || nz
	})
	ics.EachBand(ics.NumSwb, func(_, _, idx int) {
		if sce.Zeroes[idx] {
			sce.SfIdx[idx] = ScaleOnePos
			return
		}
		sce.SfIdx[idx] = ScaleOnePos + int(min(fmath.Log232(uplims[idx]/minthr)*4, 59))
	})

	if !allz {
		return
	}
	dsp.AbsPow34(c.scoefs[:], sce.Coeffs[:])
	c.CacheInit()

	ics.EachBand(ics.NumSwb, func(w, g, idx int) {
		start := w*128 + int(ics.SwbOffset[g])
		maxvals[idx] = FindMaxVal(ics.GroupLen[w], int(ics.SwbSizes[g]), c.scoefs[start:])
	})

	// perform two-loop search
	// outer loop - improve quality
	for {
		var tbits, qstep int
		minscaler := sce.SfIdx[0]
		// inner loop - quantize spectrum to fit into given number of bits
		if its == 0 {
			qstep = 32
		} else {
			qstep = 1
		}
		for {
			prev := -1
			tbits = 0
			for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
				start := w * 128
				for g := range ics.NumSwb {
					coefs := sce.Coeffs[start:]
					scaled := c.scoefs[start:]
					bandBits := 0
					var dist float32

					if sce.Zeroes[w*16+g] || sce.SfIdx[w*16+g] >= 218 {
						start += int(ics.SwbSizes[g])
						continue
					}
					minscaler = min(minscaler, sce.SfIdx[w*16+g])
					cb := FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
					for w2 := range ics.GroupLen[w] {
						b := 0
						size := int(ics.SwbSizes[g])
						dist += c.quantizeBandCostCached(w+w2, g,
							coefs[w2*128:w2*128+size],
							scaled[w2*128:w2*128+size],
							sce.SfIdx[w*16+g],
							cb, 1.0, fmath.Inf32(), &b, nil, 0)
						bandBits += b
					}
					dists[w*16+g] = dist - float32(bandBits)
					if prev != -1 {
						bandBits += int(tables.ScalefactorBits[sce.SfIdx[w*16+g]-prev+ScaleDiffZero])
					}
					tbits += bandBits
					start += int(ics.SwbSizes[g])
					prev = sce.SfIdx[w*16+g]
				}
			}
			if tbits > destbits {
				for i := range 128 {
					if sce.SfIdx[i] < 218-qstep {
						sce.SfIdx[i] += qstep
					}
				}
			} else {
				for i := range 128 {
					if sce.SfIdx[i] > 60-qstep {
						sce.SfIdx[i] -= qstep
					}
				}
			}
			qstep >>= 1
			if qstep == 0 && float64(tbits) > float64(destbits)*1.02 && sce.SfIdx[0] < 217 {
				qstep = 1
			}
			if qstep == 0 {
				break
			}
		}

		fflag := false
		minscaler = clip(minscaler, 60, 255-ScaleMaxDiff)

		ics.EachBand(ics.NumSwb, func(_, _, idx int) {
			prevsc := sce.SfIdx[idx]
			if dists[idx] > uplims[idx] && sce.SfIdx[idx] > 60 {
				if FindMinBook(maxvals[idx], sce.SfIdx[idx]-1) != 0 {
					sce.SfIdx[idx]--
				} else { // Try to make sure there is some energy in every band
					sce.SfIdx[idx] -= 2
				}
			}
			sce.SfIdx[idx] = clip(sce.SfIdx[idx], minscaler, minscaler+ScaleMaxDiff)
			sce.SfIdx[idx] = min(sce.SfIdx[idx], 219)
			if sce.SfIdx[idx] != prevsc {
				fflag = true
			}
			sce.BandType[idx] = FindMinBook(maxvals[idx], sce.SfIdx[idx])
		})
		its++
		if !fflag || its >= 10 {
			break
		}
	}
}
