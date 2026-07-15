// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// trellisPath mirrors struct TrellisBandCodingPath (aaccoder_trellis.h
// @ d09d5afc3a).
type trellisPath struct {
	prevIdx int
	cost    float32
	run     int
}

// CodebookTrellisRate performs run-length codebook sectioning for one window
// group, minimizing section run bits plus exact spectral payload bits, then
// writes the section_data and updates the band types and zero flags to the
// chosen sections. Mirrors aaccoder_trellis.h:codebook_trellis_rate
// @ d09d5afc3a. The lambda parameter is kept for signature parity with the
// C coder table; the cost is pure bits.
func (c *Coder) CodebookTrellisRate(pb *bits.Writer, sce *SingleChannelElement,
	win, groupLen int, _ float32) {
	ics := &sce.ICS
	maxSfb := ics.MaxSfb
	runBits := 3
	if ics.NumWindows == 1 {
		runBits = 5
	}
	runEsc := 1<<runBits - 1
	runValueBits := tables.RunValueBitsShort[:]
	if ics.NumWindows == 1 {
		runValueBits = tables.RunValueBitsLong[:]
	}
	nextMinbits := fmath.Inf32()
	nextMincb := 0

	dsp.AbsPow34(c.scoefs[:], sce.Coeffs[:])
	start := win * 128
	for cb := range CBTotAll {
		c.path[0][cb].cost = float32(runBits + 4)
		c.path[0][cb].prevIdx = -1
		c.path[0][cb].run = 0
	}
	for swb := range maxSfb {
		size := int(ics.SwbSizes[swb])
		if sce.Zeroes[win*16+swb] {
			costStayHere := c.path[swb][0].cost
			costGetHere := nextMinbits + float32(runBits+4)
			if runValueBits[c.path[swb][0].run] != runValueBits[c.path[swb][0].run+1] {
				costStayHere += float32(runBits)
			}
			if costGetHere < costStayHere {
				c.path[swb+1][0].prevIdx = nextMincb
				c.path[swb+1][0].cost = costGetHere
				c.path[swb+1][0].run = 1
			} else {
				c.path[swb+1][0].prevIdx = 0
				c.path[swb+1][0].cost = costStayHere
				c.path[swb+1][0].run = c.path[swb][0].run + 1
			}
			nextMinbits = c.path[swb+1][0].cost
			nextMincb = 0
			for cb := 1; cb < CBTotAll; cb++ {
				c.path[swb+1][cb].cost = 61450
				c.path[swb+1][cb].prevIdx = -1
				c.path[swb+1][cb].run = 0
			}
		} else {
			minbits := nextMinbits
			mincb := nextMincb
			startcb := int(tables.CBInMap[sce.BandType[win*16+swb]])
			nextMinbits = fmath.Inf32()
			nextMincb = 0
			for cb := range startcb {
				c.path[swb+1][cb].cost = 61450
				c.path[swb+1][cb].prevIdx = -1
				c.path[swb+1][cb].run = 0
			}
			for cb := startcb; cb < CBTotAll; cb++ {
				if cb >= 12 && sce.BandType[win*16+swb] != int(tables.CBOutMap[cb]) {
					c.path[swb+1][cb].cost = 61450
					c.path[swb+1][cb].prevIdx = -1
					c.path[swb+1][cb].run = 0
					continue
				}
				var bandBits float32
				for w := range groupLen {
					bandBits += float32(c.quantizeBandCostBits(
						sce.Coeffs[start+w*128:start+w*128+size],
						c.scoefs[start+w*128:start+w*128+size],
						sce.SfIdx[win*16+swb],
						int(tables.CBOutMap[cb])))
				}
				costStayHere := c.path[swb][cb].cost + bandBits
				costGetHere := minbits + bandBits + float32(runBits+4)
				if runValueBits[c.path[swb][cb].run] != runValueBits[c.path[swb][cb].run+1] {
					costStayHere += float32(runBits)
				}
				if costGetHere < costStayHere {
					c.path[swb+1][cb].prevIdx = mincb
					c.path[swb+1][cb].cost = costGetHere
					c.path[swb+1][cb].run = 1
				} else {
					c.path[swb+1][cb].prevIdx = cb
					c.path[swb+1][cb].cost = costStayHere
					c.path[swb+1][cb].run = c.path[swb][cb].run + 1
				}
				if c.path[swb+1][cb].cost < nextMinbits {
					nextMinbits = c.path[swb+1][cb].cost
					nextMincb = cb
				}
			}
		}
		start += int(ics.SwbSizes[swb])
	}

	// convert resulting path from backward-linked list
	stackLen := 0
	idx := 0
	for cb := 1; cb < CBTotAll; cb++ {
		if c.path[maxSfb][cb].cost < c.path[maxSfb][idx].cost {
			idx = cb
		}
	}
	ppos := maxSfb
	for ppos > 0 {
		cb := idx
		c.stackRun[stackLen] = c.path[ppos][cb].run
		c.stackCB[stackLen] = cb
		idx = c.path[ppos-c.path[ppos][cb].run+1][cb].prevIdx
		ppos -= c.path[ppos][cb].run
		stackLen++
	}
	// perform actual band info encoding
	start = 0
	for i := stackLen - 1; i >= 0; i-- {
		cb := int(tables.CBOutMap[c.stackCB[i]])
		pb.Put(4, uint32(cb))
		count := c.stackRun[i]
		for range count {
			sce.Zeroes[win*16+start] = cb == 0
			sce.BandType[win*16+start] = cb
			start++
		}
		for count >= runEsc {
			pb.Put(runBits, uint32(runEsc))
			count -= runEsc
		}
		pb.Put(runBits, uint32(count))
	}
}
