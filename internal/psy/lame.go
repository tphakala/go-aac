// SPDX-License-Identifier: LGPL-2.1-or-later

package psy

import (
	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// psyHpFilter applies the 21-tap fs/4 high-pass FIR from LAME to one long
// block. Mirrors aacpsy.c:psy_hp_filter @ d09d5afc3a. The LAME psy model
// expects input in the range -32768..32768, hence the output scaling.
// firbuf must hold blockSizeLong + psyLameFirLen + 13 samples.
func psyHpFilter(firbuf []float32, hpfsmpl *[blockSizeLong]float32) {
	for i := range blockSizeLong {
		var sum1, sum2 float32
		sum1 = firbuf[i+(psyLameFirLen-1)/2]
		sum2 = 0.0
		for j := 0; j < ((psyLameFirLen-1)/2)-1; j += 2 {
			t1 := tables.PsyFirCoeffs[j] * (firbuf[i+j] + firbuf[i+psyLameFirLen-j])
			sum1 += t1
			t2 := tables.PsyFirCoeffs[j+1] * (firbuf[i+j+1] + firbuf[i+psyLameFirLen-j-1])
			sum2 += t2
		}
		hpfsmpl[i] = (sum1 + sum2) * 32768.0
	}
}

// lameApplyBlockType runs the LAME window-sequence state machine.
// Mirrors aacpsy.c:lame_apply_block_type @ d09d5afc3a.
func lameApplyBlockType(pch *channel, wi *WindowInfo, uselongblock bool) {
	blocktype := coder.OnlyLongSequence
	if uselongblock {
		if pch.nextWindowSeq == coder.EightShortSequence {
			blocktype = coder.LongStopSequence
		}
	} else {
		blocktype = coder.EightShortSequence
		if pch.nextWindowSeq == coder.OnlyLongSequence {
			pch.nextWindowSeq = coder.LongStartSequence
		}
		if pch.nextWindowSeq == coder.LongStopSequence {
			pch.nextWindowSeq = coder.EightShortSequence
		}
	}

	wi.WindowType[0] = pch.nextWindowSeq
	pch.nextWindowSeq = blocktype
}

// Window suggests the window sequence, shape and grouping for one channel.
// Mirrors aacpsy.c:psy_lame_window @ d09d5afc3a: the LAME attack detection
// (21-tap FIR high pass, sub-shortblock energy ratios against the
// bitrate-dependent attack threshold, the 2026-06 pre-echo relaxation for
// isolated onsets) followed by the window-sequence state machine. la is
// the lookahead pointer (samples2 + 448 + 64 in the encoder) and is nil on
// the final flush frame, in which case the previous sequence is continued
// (docs/porting-guide.md pitfall 11). The complexity waiver covers a
// faithful port of a single 150-line C function.
//
//nolint:gocognit,gocyclo // faithful port of one C function, see doc comment
func (ctx *Context) Window(la []float32, ch, prevType int) WindowInfo {
	pch := &ctx.pch[ch]
	grouping := 0
	uselongblock := true
	var attacks [numBlocksShort + 1]int
	var wi WindowInfo

	// The lookahead branch reads firbuf up to index blockSizeLong-1+psyLameFirLen
	// past its start at la[blockSizeShort/4-psyLameFirLen], i.e. la[1055], so it
	// needs at least lookaheadLen samples. The encoder always hands over a full
	// overlap tail (1536 samples), so this only guards against a future or
	// mistaken caller; a short slice falls back to continuing the previous
	// sequence exactly as the nil (flush) path does.
	if len(la) >= lookaheadLen {
		var hpfsmpl [blockSizeLong]float32
		var attackIntensity [(numBlocksShort + 1) * psyLameNumSubblocks]float32
		var energySubshort [(numBlocksShort + 1) * psyLameNumSubblocks]float32
		var energyShort [numBlocksShort + 1]float32
		firbuf := la[blockSizeShort/4-psyLameFirLen:]
		attSum := 0

		// LAME comment: apply high pass filter of fs/4
		psyHpFilter(firbuf, &hpfsmpl)

		// Calculate the energies of each sub-shortblock
		for i := range psyLameNumSubblocks {
			energySubshort[i] = pch.prevEnergySubshort[i+((numBlocksShort-1)*psyLameNumSubblocks)]
			attackIntensity[i] = energySubshort[i] /
				pch.prevEnergySubshort[i+((numBlocksShort-1)*psyLameNumSubblocks-2)]
			energyShort[0] += energySubshort[i]
		}

		pf := 0
		for i := range numBlocksShort * psyLameNumSubblocks {
			pfe := pf + blockSizeLong/(numBlocksShort*psyLameNumSubblocks)
			var p float32 = 1.0
			for ; pf < pfe; pf++ {
				p = max(p, fmath.Absf(hpfsmpl[pf]))
			}
			pch.prevEnergySubshort[i] = p
			energySubshort[i+psyLameNumSubblocks] = p
			energyShort[1+i/psyLameNumSubblocks] += p

			// NOTE: the indexes below are [i + 3 - 2] in the LAME source.
			// Compare each sub-block to sub-block - 2. The C is an
			// if/else-if/else chain (aacpsy.c:968-973); a Go switch
			// evaluates the cases in the same order.
			switch {
			case p > energySubshort[i+psyLameNumSubblocks-2]:
				p /= energySubshort[i+psyLameNumSubblocks-2]
			case energySubshort[i+psyLameNumSubblocks-2] > p*10.0:
				p = energySubshort[i+psyLameNumSubblocks-2] / (p * 10.0)
			default:
				p = 0.0
			}

			attackIntensity[i+psyLameNumSubblocks] = p
		}

		{ // pre-echo-aware threshold relaxation, see PSY_LAME_PE_*
			var framePeak float32 = 1.0
			for i := psyLameNumSubblocks; i < (numBlocksShort+1)*psyLameNumSubblocks; i++ {
				framePeak = max(framePeak, energySubshort[i])
			}
			for i := range (numBlocksShort + 1) * psyLameNumSubblocks {
				if attacks[i/psyLameNumSubblocks] == 0 {
					thr := pch.attackThreshold
					if i >= psyLameNumSubblocks &&
						pch.framesSinceShort >= psyLamePreEchoGap &&
						energySubshort[i-psyLameNumSubblocks] < psyLamePreEchoQuiet*framePeak {
						thr *= psyLamePreEchoRed
					}
					if attackIntensity[i] > thr {
						attacks[i/psyLameNumSubblocks] = (i % psyLameNumSubblocks) + 1
					}
				}
			}
		}

		// There should be an energy change between short blocks, in order
		// to avoid periodic signals (aacpsy.c:994-1010 and its LAME tuning
		// history comments).
		for i := 1; i < numBlocksShort+1; i++ {
			u := energyShort[i-1]
			v := energyShort[i]
			m := max(u, v)
			if m < 40000 { // (2)
				if u < 2.3*v && v < 2.3*u { // (1)
					if i == 1 && attacks[0] < attacks[i] {
						attacks[0] = 0
					}
					attacks[i] = 0
				}
			}
			attSum += attacks[i]
		}

		if pch.nextAttack0Zero {
			attacks[0] = 0
		}
		pch.nextAttack0Zero = attacks[numBlocksShort] == 0

		if attacks[0] <= pch.prevAttack {
			attacks[0] = 0
		}

		attSum += attacks[0]

		// If the previous attack happened in the last sub-block of the
		// previous sequence, or if there is a new attack, use short windows.
		if pch.prevAttack == psyLameNumSubblocks || attSum != 0 {
			uselongblock = false

			for i := 1; i < numBlocksShort+1; i++ {
				if attacks[i] != 0 && attacks[i-1] != 0 {
					attacks[i] = 0
				}
			}
		}

		if uselongblock {
			pch.framesSinceShort++
		} else {
			pch.framesSinceShort = 0
		}
	} else {
		// No lookahead: use the same type as the previous sequence.
		uselongblock = prevType != coder.EightShortSequence
	}

	lameApplyBlockType(pch, &wi, uselongblock)

	wi.WindowType[1] = prevType
	if wi.WindowType[0] != coder.EightShortSequence {
		wi.NumWindows = 1
		wi.Grouping[0] = 1
		if wi.WindowType[0] == coder.LongStartSequence {
			wi.WindowShape = 0
		} else {
			wi.WindowShape = 1
		}
	} else {
		lastgrp := 0
		wi.NumWindows = 8
		wi.WindowShape = 0
		for i := range 8 {
			if (pch.nextGrouping>>i)&1 == 0 {
				lastgrp = i
			}
			wi.Grouping[lastgrp]++
		}
	}

	// Determine grouping from the location of the first attack, and save
	// it for the next frame (aacpsy.c:1061-1073).
	for i := range 9 {
		if attacks[i] != 0 {
			grouping = i
			break
		}
	}
	pch.nextGrouping = tables.WindowGrouping[grouping]

	pch.prevAttack = attacks[numBlocksShort-1]

	return wi
}
