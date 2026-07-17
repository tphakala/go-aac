// SPDX-License-Identifier: LGPL-2.1-or-later

// NMR (noise-to-mask ratio) scalefactor coder: an optimal Viterbi search
// over scalefactors with self rate control. Port of
// libavcodec/aaccoder_nmr.h @ d09d5afc3a in full; see that header's
// comment for the objective and the PNS integration.
package coder

import (
	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// NMR coder constants. Mirror aaccoder_nmr.h @ d09d5afc3a.
const (
	NMRNCand = 96 // per-band scalefactor candidates (aacenc.h NMR_NCAND)

	nmrIters  = 14 // lambda binary-search iters
	nmrIFine  = 9  // fine-pass lambda iters
	nmrCIters = 7  // coarse-pass lambda iters
	nmrCWarm  = 5  // coarse-pass iters when warm-started
	nmrCoarse = 8  // two-pass coarse->fine grid step
	nmrStep   = 1  // fine-pass scalefactor candidate granularity

	nmrPNSBits = 9 // approx cost in bits of signalling PNS

	nmrPNSHoleFrac   = 0.5
	nmrPNSHoleSpread = 0.5

	nmrRCKCBR = 0.5

	nmrRCIters = 8
	nmrRCCorr  = 1.5

	nmrCBRBuf   = 512
	nmrRCCIters = 3

	nmrBurstGap  = 10
	nmrBurstGain = 8.0
	nmrRCFIters  = 4
	nmrRCTrack   = 0.1

	nmrPNSNDGate = 4.0
	nmrPNSMaxET  = 8.0
	nmrPNSLam    = 100.0
)

// NMRState is the NMR coder's persistent per-band candidate cost curves
// (~96 KiB) and rate-control carry-over. Allocated once at encoder init
// and reused across frames (docs/porting-guide.md pitfall 12). Mirrors
// struct AACNMRCurves (libavcodec/aacenc.h @ d09d5afc3a).
type NMRState struct {
	Nd [128][NMRNCand]float32 // dist / threshold per candidate
	Nb [128][NMRNCand]int32   // spectral bits per candidate

	Lam     [16]float32 // per-channel operating lambda of the previous frame
	Counted [16]int     // per-channel bits the trellis accounted for

	SideEMA    float32 // running estimate of real-minus-counted bits per frame
	SideInited bool    // SideEMA holds a measurement

	RCFrameNum       int64   // frame the reservoir was last advanced for
	LamRC            float32 // global-lambda rate control operating lambda
	RCFill           int     // virtual bit reservoir fill, + = bits saved
	FramesSinceShort int     // long frames since the last short run
	PrevWasShort     bool    // previous frame was a short block
	RunBurst         float32 // transient bit-burst factor
}

// NMRInput carries the encoder-context inputs of
// search_for_quantizers_nmr that live outside the coder in C
// (AVCodecContext and AACEncContext fields @ d09d5afc3a).
type NMRInput struct {
	BitRate          int   // avctx->bit_rate
	SampleRate       int   // avctx->sample_rate
	Channels         int   // s->channels == avctx->ch_layout.nb_channels
	FrameNum         int64 // avctx->frame_num
	BitresAlloc      int   // s->psy.bitres.alloc
	Bandwidth        int   // s->bandwidth
	CurChannel       int   // s->cur_channel
	Speed            int   // s->options.nmr_speed
	RateControlOK    bool  // !QSCALE && bit_rate > 0 && bit_rate_tolerance != 0
	QScaleChannels   int   // bch: 2 under QSCALE, else channels
	LastFramePBCount int   // s->last_frame_pb_count
}

// floorDiv and ceilDiv round the quotient toward -inf and +inf respectively,
// for a divisor of either sign. Go's / truncates toward zero, which is the
// wrong rounding for the negative numerators the trellis window produces.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func ceilDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) == (b < 0) {
		q++
	}
	return q
}

// nmrTrellisStep runs one Viterbi step: for each current-band candidate,
// find the previous-band candidate minimising dpp[op] + lamsf[d], then set
// dp[o] = node[o] + that cost and record the back-pointer bp[o].
// Mirrors aacencdsp.c:nmr_trellis_step_c @ d09d5afc3a, the SCALAR kernel.
// Both accumulations are plain adds with no multiply, so no FMA can arise
// here and every add stays separately rounded. Separately: walking op in
// ascending order under a strict c < bestc is what makes a tie pick the
// LOWEST op.
//
// The C tests |d| <= mdiff once per iteration. d is affine in op and falls by
// step as op rises, so the surviving op values form one contiguous range whose
// offsets from o are computable once per call. Iterating exactly that range
// visits the same pairs in the same increasing order, so every float op and
// the tie-break are unchanged; TestNMRTrellisStepMatchesReference pins the
// equivalence against the pre-optimisation form.
//
// Callers must pass cap(dp), cap(bp), cap(node) >= nCur, cap(dpp) >= nPrev and
// len(lamsf) >= 2*mdiff+1. The entry re-slices turn the first four into a panic
// at the call boundary instead of at whichever access first ran off the end,
// but they check cap, not len: a short slice with spare capacity is silently
// extended rather than rejected. lamsf is not re-sliced, so like the C it
// faults only on an access that actually reaches past it.
func nmrTrellisStep(dp []float32, bp []uint8, dpp, node, lamsf []float32,
	nCur, nPrev, base, step, mdiff int) {
	// Re-slice to the loop bound so the indexing needs no in-loop check.
	// This is the BCE recipe measured to work in this repo; the lamsf index
	// is computed and keeps its check, the prover cannot connect it to len.
	dp = dp[:nCur]
	bp = bp[:nCur]
	node = node[:nCur]
	dpp = dpp[:nPrev]

	// Window as offsets from o: opLo = max(0, o-hiOff), opHi = min(nPrev-1,
	// o-loOff). With k = o-op, the filter -mdiff <= base+k*step <= mdiff is
	// lo <= k*step <= hi, so dividing through by step bounds k. Dividing
	// flips the two bounds when step is negative.
	lo, hi := -mdiff-base, mdiff-base
	loOff, hiOff := 0, -1 // empty window
	switch {
	case step > 0:
		loOff, hiOff = ceilDiv(lo, step), floorDiv(hi, step)
	case step < 0:
		loOff, hiOff = ceilDiv(hi, step), floorDiv(lo, step)
	case lo <= 0 && 0 <= hi:
		// step == 0 makes d constant at base, so the filter either admits
		// every op or none; it cannot be divided through. o < nCur makes
		// opLo clamp to 0, and o >= 0 makes opHi clamp to nPrev-1, so these
		// offsets admit the full [0, nPrev-1].
		loOff, hiOff = -nPrev, nCur
	}

	baseIdx := base + mdiff
	for o := range nCur {
		best := -1
		bestc := float32(fmath.MaxFloat32)
		opLo := max(0, o-hiOff)
		opHi := min(nPrev-1, o-loOff)
		if opLo <= opHi {
			// Range over the window rather than indexing dpp by op: the
			// prover cannot carry op < nPrev through the min(), but it
			// clears every check on a slice walked by range.
			w := dpp[opLo : opHi+1]
			// lamsf index at op is baseIdx + (o-op)*step, so it is first
			// evaluated at the window's lowest op and moves by -step as op
			// rises
			idx := baseIdx + (o-opLo)*step
			besti := -1
			for i := range w {
				c := w[i] + lamsf[idx]
				if c < bestc {
					bestc = c
					besti = i
				}
				idx -= step
			}
			if besti >= 0 {
				best = opLo + besti
			}
		}
		if best < 0 {
			bp[o] = 0
			dp[o] = fmath.MaxFloat32
		} else {
			bp[o] = uint8(best)
			dp[o] = node[o] + bestc
		}
	}
}

// nmrSolve runs the Viterbi over the coding sequence act[0..nact-1] with
// lambda binary-searched so the coded size approaches destbits. Fills
// chosen[band] for every band referenced by act and returns the operating
// lambda. Mirrors aaccoder_nmr.h:nmr_solve @ d09d5afc3a.
func (c *Coder) nmrSolve(st *NMRState, blo, bnc []int, step int,
	act []int, nact, destbits int, chosen []int, loL, hiL float32,
	iters int) float32 {
	var dp, dpp, node [NMRNCand]float32
	var lamsf [2*ScaleMaxDiff + 1]float32
	var bp [128][NMRNCand]uint8
	lam := float32(1.0)

	if nact <= 0 {
		return lam
	}

	for it := range iters {
		lam = fmath.Sqrt32(loL * hiL)
		for i := 0; i <= 2*ScaleMaxDiff; i++ {
			lamsf[i] = lam * float32(tables.ScalefactorBits[i])
		}

		b0 := act[0]
		for o := range bnc[b0] {
			// explicit float32 conversion: keeps the multiply and add
			// separately rounded (Go may fuse into FMA even across
			// statements; only a conversion forces the rounding),
			// matching the scalar C reference
			t := float32(lam * float32(st.Nb[b0][o]))
			dp[o] = st.Nd[b0][o] + t
		}

		for k := 1; k < nact; k++ {
			b, pb := act[k], act[k-1]
			dpp = dp
			for o := range bnc[b] {
				t := float32(lam * float32(st.Nb[b][o])) // no FMA, see anchor loop
				node[o] = st.Nd[b][o] + t
			}
			nmrTrellisStep(dp[:], bp[k][:], dpp[:], node[:], lamsf[:],
				bnc[b], bnc[pb], blo[b]-blo[pb], step, ScaleMaxDiff)
		}

		// backtrack
		beo, b := 0, act[nact-1]
		bec := float32(fmath.MaxFloat32)
		for o := range bnc[b] {
			if dp[o] < bec {
				bec = dp[o]
				beo = o
			}
		}
		chosen[b] = beo
		for k := nact - 1; k > 0; k-- {
			chosen[act[k-1]] = int(bp[k][chosen[act[k]]])
		}

		// calc cost
		total := 0
		for k := range nact {
			total += int(st.Nb[act[k]][chosen[act[k]]])
		}
		for k := 1; k < nact; k++ {
			total += scalefactorBits((blo[act[k]] + chosen[act[k]]*step) -
				(blo[act[k-1]] + chosen[act[k-1]]*step))
		}

		if it == iters-1 {
			break
		}

		// check if we went over budget, go coarser if we did
		if total > destbits {
			loL = lam
		} else {
			hiL = lam
		}
	}
	return lam
}

// nmrBandCurve builds one coded band's (dist/threshold, bits) cost curve
// for candidates sf = lo + o*step, stopping when the band would drop
// (cb <= 0). Returns the candidate count. Mirrors
// aaccoder_nmr.h:nmr_band_curve @ d09d5afc3a.
func (c *Coder) nmrBandCurve(sce *SingleChannelElement, w, g, start, lo,
	step, maxn int, invthr, maxval float32, ndRow *[NMRNCand]float32,
	nbRow *[NMRNCand]int32) int {
	ncand := 0
	size := int(sce.ICS.SwbSizes[g])
	for o := 0; o < maxn && lo+o*step <= ScaleMaxPos; o++ {
		sf := lo + o*step
		btot := 0
		cb := FindMinBook(maxval, sf)
		var dist float32
		if cb <= 0 {
			break
		}
		for w2 := range sce.ICS.GroupLen[w] {
			bb := 0
			dist += c.quantizeBandCostCached(w+w2, g,
				sce.Coeffs[start+w2*128:start+w2*128+size],
				c.scoefs[start+w2*128:start+w2*128+size],
				sf, cb, 1.0, fmath.Inf32(), &bb, nil, 0)
			btot += bb
		}
		ndRow[ncand] = (dist - float32(btot)) * invthr
		nbRow[ncand] = int32(btot)
		ncand++
	}
	return ncand
}

// nmrBail leaves a fully consistent all-zero state when nothing in the
// channel is codeable, keeping pre-decided intensity signalling. Mirrors
// the bail label of search_for_quantizers_nmr @ d09d5afc3a.
func nmrBail(sce *SingleChannelElement) {
	for i := range 128 {
		if sce.BandType[i] == IntensityBT || sce.BandType[i] == IntensityBT2 {
			continue
		}
		sce.Zeroes[i] = true
		sce.BandType[i] = 0
	}
}

// SearchForQuantizersNMR selects scalefactor indices, band codebooks and
// PNS decisions with the NMR trellis and runs the coder's self rate
// control. Mirrors aaccoder_nmr.h:search_for_quantizers_nmr @ d09d5afc3a.
// The complexity waiver covers a faithful port of a single 490-line C
// function; splitting it would break the line-by-line mapping to the
// pinned source (docs/porting-guide.md ground rule 1).
//
//nolint:gocognit,gocyclo // faithful port of one C function, see doc comment
func (c *Coder) SearchForQuantizersNMR(in *NMRInput, st *NMRState,
	sce *SingleChannelElement, psy *[128]PsyBand, lambda float32) {
	bch := in.QScaleChannels
	destbits := int(float64(in.BitRate) * 1024.0 / float64(in.SampleRate) /
		float64(bch) * float64(lambda/120.0))
	allz := false
	nbnd := 0

	var thr [128]float32     // allocation-law effective threshold
	var thrReal [128]float32 // real masking threshold (PNS gates)
	var pener [128]float32   // band energy (for the PNS noise target)
	var pspread [128]float32 // band tonality spread (1 = noise)
	var minsf [128]int
	var maxvals [128]float32

	// coded-band trellis state (indexed 0..nbnd-1)
	var bidx [128]int        // sce band index (w*16+g)
	var bw, bg, bst [128]int // window group, swb, coef start per coded band
	var blo [128]int         // finest candidate scalefactor
	var bnc [128]int         // number of candidates
	var chosen, act [128]int // chosen candidate, active coding order
	var isPNS [128]bool      // trellis band coded as noise

	// two-pass coarse->fine grid step; the lambda search runs on the cheap
	// coarse grid, PASS 2 refines the winner at nmrStep granularity
	const cstep = nmrCoarse

	st.Counted[in.CurChannel] = 0

	// Global-lambda RC: bypassed for VBR and the bootstrap frame.
	rcEligible := in.RateControlOK
	// Leaky-bucket reservoir bounds.
	rcRateFrame := int(float64(in.BitRate) * 1024.0 / float64(in.SampleRate))
	rcBmax := min(max(6144*in.Channels-rcRateFrame, 256), nmrCBRBuf*in.Channels)
	if rcEligible && in.FrameNum != st.RCFrameNum {
		if st.RCFrameNum > 0 && st.LamRC > 0.0 {
			st.RCFill = clip(st.RCFill+rcRateFrame-in.LastFramePBCount,
				-rcBmax, rcBmax)
		}
		st.RCFrameNum = in.FrameNum

		// Transient burst run state, held across the short run.
		isShort := sce.ICS.WindowSequence[0] == EightShortSequence
		if isShort {
			if !st.PrevWasShort { // run start
				if st.FramesSinceShort >= nmrBurstGap {
					st.RunBurst = nmrBurstGain
				} else {
					st.RunBurst = 1.0
				}
			}
			st.FramesSinceShort = 0
		} else {
			st.RunBurst = 1.0
			st.FramesSinceShort++
		}
		st.PrevWasShort = isShort
	}
	rcGlobal := rcEligible && st.LamRC > 0.0

	if in.BitresAlloc >= 0 {
		destbits = int(float32(in.BitresAlloc) * (lambda / 120))
	}
	if rcGlobal && in.BitresAlloc >= 0 {
		// uniform CBR target: nominal rate plus fast reservoir repayment
		tr := float64(float64(in.BitRate) * 1024.0 / float64(in.SampleRate))
		tf := float64(float64(st.RCFill) / 2.0)
		destbits = int((tr + tf) / float64(in.Channels))
	}
	destbits = min(destbits, 5800)
	// honest budget: subtract the measured non-trellis overhead
	if st.SideInited {
		destbits = clip(destbits-int(st.SideEMA/float32(in.Channels)), 64, 5800)
	}

	// Apply the held transient burst factor.
	if sce.ICS.WindowSequence[0] == EightShortSequence && st.RunBurst > 1.0 {
		destbits = clip(int(float32(destbits)*st.RunBurst), 64, 6800)
	}

	// band cutoff index for this frame's window size
	cutoff := in.Bandwidth * 2 * (1024 / sce.ICS.NumWindows) / in.SampleRate

	// Short-block transient noise shaping: temporal premasking clamps each
	// window's threshold toward the preceding windows', and flat-residual
	// flattens each window's thresholds to their per-window mean.
	if sce.ICS.WindowSequence[0] == EightShortSequence {
		const pmP1, pmP2, pmP3 = 0.1, 2.0, 4.0
		for g := range sce.ICS.NumSwb {
			t1 := float32(fmath.MaxFloat32)
			t2 := float32(fmath.MaxFloat32)
			for w := range sce.ICS.NumWindows {
				b := &psy[w*16+g]
				t := b.Threshold
				cl := min(t, min(t1*pmP2, t2*pmP3))
				b.Threshold = max(cl, t*pmP1)
				t2 = t1
				t1 = t
			}
		}
		for w := range sce.ICS.NumWindows {
			var sum float32
			n := 0
			for g := range sce.ICS.NumSwb {
				b := &psy[w*16+g]
				if b.Energy > b.Threshold && b.Threshold > 0.0 {
					sum += b.Threshold
					n++
				}
			}
			if n > 0 {
				mean := sum / float32(n)
				for g := range sce.ICS.NumSwb {
					b := &psy[w*16+g]
					if b.Energy > b.Threshold && b.Threshold > 0.0 {
						b.Threshold = mean
					}
				}
			}
		}
	}

	// Allocation curve to favour high frequencies
	const aAE, aAT = 0.443, 0.111
	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		start := 0
		for g := range sce.ICS.NumSwb {
			var uplim, ener float32
			spread := float32(2.0)
			nz := 0
			if sce.BandType[w*16+g] == IntensityBT ||
				sce.BandType[w*16+g] == IntensityBT2 {
				// pre-decided intensity band: keep its signalling
				for w2 := range sce.ICS.GroupLen[w] {
					sce.Zeroes[(w+w2)*16+g] = false
				}
				start += int(sce.ICS.SwbSizes[g])
				continue
			}
			for w2 := range sce.ICS.GroupLen[w] {
				band := &psy[(w+w2)*16+g]
				ener += band.Energy
				spread = min(spread, band.Spread)
				if start >= cutoff || band.Energy <= band.Threshold || band.Threshold == 0.0 {
					sce.Zeroes[(w+w2)*16+g] = true
					continue
				}
				uplim += band.Threshold
				nz = 1
			}
			sce.Zeroes[w*16+g] = nz == 0
			thrReal[w*16+g] = uplim // real mask, before the allocation law
			if nz != 0 && ener > 0.0 && uplim > 0.0 {
				te := float32(aAE * fmath.Log32(ener)) // no FMA in the sum below
				tt := float32(aAT * fmath.Log32(uplim))
				uplim = fmath.Exp32(te + tt)
			}
			thr[w*16+g] = uplim
			pener[w*16+g] = ener
			pspread[w*16+g] = spread
			allz = allz || nz != 0
			start += int(sce.ICS.SwbSizes[g])
		}
	}
	if !allz {
		nmrBail(sce)
		return
	}

	dsp.AbsPow34(c.scoefs[:], sce.Coeffs[:])
	c.CacheInit()

	// finest codeable scalefactor and max value per band
	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		start := w * 128
		for g := range sce.ICS.NumSwb {
			maxvals[w*16+g] = FindMaxVal(sce.ICS.GroupLen[w],
				int(sce.ICS.SwbSizes[g]), c.scoefs[start:])
			if maxvals[w*16+g] > 0 {
				minsf[w*16+g] = int(Coef2MinSF(maxvals[w*16+g]))
			} else {
				minsf[w*16+g] = 0
			}
			start += int(sce.ICS.SwbSizes[g])
		}
	}

	// PASS 1: precompute each coded band's cost curve at the coarse step
	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		start := w * 128
		for g := range sce.ICS.NumSwb {
			if !sce.Zeroes[w*16+g] && maxvals[w*16+g] > 0 && nbnd < 128 {
				lo := clip(minsf[w*16+g], 0, ScaleMaxPos)
				invthr := 1.0 / max(thr[w*16+g], 1e-9)
				ncand := c.nmrBandCurve(sce, w, g, start, lo, cstep, NMRNCand,
					invthr, maxvals[w*16+g], &st.Nd[nbnd], &st.Nb[nbnd])
				if ncand == 0 {
					// nothing codeable: drop the whole group band including
					// the subwindow flags (the encoder later re-derives the
					// group flag by ANDing them)
					for w2 := range sce.ICS.GroupLen[w] {
						sce.Zeroes[(w+w2)*16+g] = true
					}
				} else {
					bidx[nbnd] = w*16 + g
					bw[nbnd] = w
					bg[nbnd] = g
					bst[nbnd] = start
					blo[nbnd] = lo
					bnc[nbnd] = ncand
					nbnd++
				}
			}
			start += int(sce.ICS.SwbSizes[g])
		}
	}
	if nbnd == 0 {
		nmrBail(sce)
		return
	}

	// solve the trellis, then offer PNS at the operating lambda and
	// re-solve over the survivors with the freed budget
	nact := nbnd
	pnsCount := 0
	lam0 := st.Lam[in.CurChannel]
	var lam float32

	for b := range nbnd {
		act[b] = b
		isPNS[b] = false
	}
	if rcGlobal {
		// bisect to this frame's bit demand within the corridor around
		// the servoed lambda
		lo := st.LamRC / nmrRCCorr
		// Transient burst: widen the lower bound so the bisection can pour
		// the boosted destbits into an onset frame.
		if sce.ICS.WindowSequence[0] == EightShortSequence && st.RunBurst > 1.0 {
			lo /= st.RunBurst
		}
		lam = c.nmrSolve(st, blo[:], bnc[:], cstep, act[:], nact, destbits,
			chosen[:], lo, st.LamRC*nmrRCCorr, nmrRCCIters)

		tot := 0
		for k := range nact {
			tot += int(st.Nb[act[k]][chosen[act[k]]])
		}
		for k := 1; k < nact; k++ {
			tot += scalefactorBits((blo[act[k]] + chosen[act[k]]*cstep) -
				(blo[act[k-1]] + chosen[act[k-1]]*cstep))
		}
		hardcap := clip(int(5800.0*min(1.0, lambda/120.0)), 256, 5800)
		// leaky-bucket window
		rcCap := min(hardcap, (st.RCFill+rcRateFrame+rcBmax)/in.Channels)
		rcFloor := max(0, (st.RCFill+rcRateFrame-rcBmax)/in.Channels)
		if tot > rcCap {
			lam = c.nmrSolve(st, blo[:], bnc[:], cstep, act[:], nact, rcCap,
				chosen[:], lam, 1e4, nmrCIters)
		} else if tot < rcFloor {
			lam = c.nmrSolve(st, blo[:], bnc[:], cstep, act[:], nact, rcFloor,
				chosen[:], 1e-9, lam, nmrCIters)
		}
	} else if lam0 > 0.0 { // nmrCoarse > 0 always
		// warm start: bisect a narrow bracket around the previous frame's
		// operating lambda; a result near the bracket edge means a hard
		// content transition, so redo the full search
		lam = c.nmrSolve(st, blo[:], bnc[:], cstep, act[:], nact, destbits,
			chosen[:], lam0/32.0, lam0*32.0, nmrCWarm)
		if lam < lam0/16.0 || lam > lam0*16.0 {
			lam0 = 0.0
		}
	}
	if !rcGlobal && lam0 <= 0.0 {
		lam = c.nmrSolve(st, blo[:], bnc[:], cstep, act[:], nact, destbits,
			chosen[:], 1e-9, 1e4, nmrCIters)
	}

	// PASS 2: refine each band at full granularity in a +/-cstep window
	// around the coarse pick, then re-solve.
	{
		// nmr_speed narrows the fine refine window below nmrCoarse
		win := nmrCoarse - clip(in.Speed, 0, 4)
		for b := range nbnd {
			center := blo[b] + chosen[b]*cstep
			flo := clip(center-win, clip(minsf[bidx[b]], 0, ScaleMaxPos), ScaleMaxPos)
			maxn := min(NMRNCand, 2*win/nmrStep+1)
			invthr := 1.0 / max(thr[bidx[b]], 1e-9)
			ncand := c.nmrBandCurve(sce, bw[b], bg[b], bst[b], flo, nmrStep,
				maxn, invthr, maxvals[bidx[b]], &st.Nd[b], &st.Nb[b])
			blo[b] = flo
			bnc[b] = max(1, ncand)
		}
		// fine pass: narrow corridor around the coarse solve
		if rcGlobal {
			lam = c.nmrSolve(st, blo[:], bnc[:], nmrStep, act[:], nact,
				destbits, chosen[:], lam/2.0, lam*2.0, nmrRCFIters)
		} else {
			lam = c.nmrSolve(st, blo[:], bnc[:], nmrStep, act[:], nact,
				destbits, chosen[:], lam/16.0, lam*16.0, nmrIFine)
		}
	}

	if rcGlobal {
		// leaky-bucket clamp: keep the frame within [rcFloor, rcCap]
		hardcap := clip(int(5800.0*min(1.0, lambda/120.0)), 256, 5800)
		tot := 0
		for k := range nact {
			tot += int(st.Nb[act[k]][chosen[act[k]]])
		}
		for k := 1; k < nact; k++ {
			tot += scalefactorBits((blo[act[k]] + chosen[act[k]]*nmrStep) -
				(blo[act[k-1]] + chosen[act[k-1]]*nmrStep))
		}
		rcCap := min(hardcap, (st.RCFill+rcRateFrame+rcBmax)/in.Channels)
		rcFloor := max(0, (st.RCFill+rcRateFrame-rcBmax)/in.Channels)
		if tot > rcCap {
			lam = c.nmrSolve(st, blo[:], bnc[:], nmrStep, act[:], nact, rcCap,
				chosen[:], lam, 1e4, nmrRCIters)
		} else if tot < rcFloor {
			lam = c.nmrSolve(st, blo[:], bnc[:], nmrStep, act[:], nact, rcFloor,
				chosen[:], 1e-9, lam, nmrRCIters)
		}
	}

	st.Lam[in.CurChannel] = lam // warm start for the next frame
	if rcGlobal {
		// drag the corridor centre toward the realized lambda, then servo
		// it off the reservoir error so the long-run rate returns to
		// nominal
		cc := st.LamRC * fmath.Pow32(lam/st.LamRC, nmrRCTrack)
		r := float32(float64(in.BitRate) * 1024.0 / float64(in.SampleRate))
		cc *= fmath.Exp232(-nmrRCKCBR * float32(st.RCFill) / r)
		st.LamRC = clipf(cc, 1e-6, 1e4)
	} else if rcEligible && nbnd >= 8 {
		// bootstrap the servo off the first substantive frame
		st.LamRC = clipf(lam, 1e-4, 10.0)
	}

	{ // PNS
		const pnsLam = nmrPNSLam
		// band 0 (lowest freq) is kept as the global-gain / sf-chain anchor
		for b := 1; b < nbnd; b++ {
			bi := bidx[b]
			spread := pspread[bi]
			if !sce.CanPNS[bi] {
				continue
			}

			// Loud-band guard: PNS is for near-masked noise only.
			if pener[bi] > nmrPNSMaxET*thrReal[bi] {
				continue
			}

			// Struggle gate: no PNS unless under genuine bit pressure.
			if lam <= pnsLam {
				continue
			}

			// Spectral-hole fill: a noise-like band left mostly empty
			frac := st.Nd[b][chosen[b]] * thr[bi] / max(pener[bi], 1e-9)
			if spread > nmrPNSHoleSpread && frac > nmrPNSHoleFrac {
				isPNS[b] = true
				pnsCount++
				continue
			}

			// Only replace a band that is being coded audibly badly
			if st.Nd[b][chosen[b]]*thr[bi] <= nmrPNSNDGate*thrReal[bi] {
				continue
			}

			// perceptual cost of replacing the band with matched noise
			sp2 := float32(spread * spread) // no FMA in the 1-x*x and cost sums
			nmrPNS := max(0.0, pener[bi]*(1.0-sp2)) /
				max(thr[bi], 1e-9)
			tk := float32(lam * float32(st.Nb[b][chosen[b]]))
			costKeep := st.Nd[b][chosen[b]] + tk
			tp := float32(lam * float32(nmrPNSBits))
			costPNS := nmrPNS + tp
			if costPNS < costKeep {
				isPNS[b] = true
				pnsCount++
			}
		}
		if pnsCount != 0 {
			budget2 := destbits - pnsCount*nmrPNSBits
			nact = 0
			for b := range nbnd {
				if !isPNS[b] {
					act[nact] = b
					nact++
				}
			}
			// re-solve over the survivors, re-spending the freed budget
			if rcGlobal {
				c.nmrSolve(st, blo[:], bnc[:], nmrStep, act[:], nact, budget2,
					chosen[:], lam, lam, 1)
			} else {
				c.nmrSolve(st, blo[:], bnc[:], nmrStep, act[:], nact, budget2,
					chosen[:], 1e-9, 1e4, nmrIters)
			}
		}
	}
	for b := range nbnd {
		bi := bidx[b]
		if isPNS[b] {
			sce.BandType[bi] = NoiseBT
			sce.Zeroes[bi] = false
			sce.PnsEner[bi] = pener[bi] * min(1.0, pspread[bi]*pspread[bi])
		} else {
			sce.SfIdx[bi] = clip(blo[b]+chosen[b]*nmrStep, 0, ScaleMaxPos)
		}
	}

	{ // record the bits this solve accounted for
		tot := 0
		prevb := -1
		for b := range nbnd {
			if isPNS[b] {
				continue
			}
			tot += int(st.Nb[b][chosen[b]])
			if prevb >= 0 {
				tot += scalefactorBits((blo[b] + chosen[b]*nmrStep) -
					(blo[prevb] + chosen[prevb]*nmrStep))
			}
			prevb = b
		}
		st.Counted[in.CurChannel] = tot
	}

	// SCALE_MAX_DIFF condition: re-clamp, codebook fixup, drop uncodeable,
	// set global gain. NOISE_BT bands keep their own scalefactor chain via
	// SetSpecialBandScalefactors.
	var nextband [128]uint8
	prev := -1
	InitNextbandMap(sce, &nextband)
	for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
		for g := range sce.ICS.NumSwb {
			if sce.BandType[w*16+g] == NoiseBT ||
				sce.BandType[w*16+g] == IntensityBT ||
				sce.BandType[w*16+g] == IntensityBT2 {
				continue
			}
			if sce.Zeroes[w*16+g] {
				sce.BandType[w*16+g] = 0
				continue
			}

			if prev != -1 {
				sce.SfIdx[w*16+g] = clip(sce.SfIdx[w*16+g],
					prev-ScaleMaxDiff, prev+ScaleMaxDiff)
			}
			sce.BandType[w*16+g] = FindMinBook(maxvals[w*16+g], sce.SfIdx[w*16+g])
			if sce.BandType[w*16+g] <= 0 {
				if !SfdeltaCanRemoveBand(sce, &nextband, prev, w*16+g) {
					sce.BandType[w*16+g] = 1
				} else {
					// drop subwindow flags too, see the PASS 1 drop above
					for w2 := range sce.ICS.GroupLen[w] {
						sce.Zeroes[(w+w2)*16+g] = true
					}
					sce.BandType[w*16+g] = 0
					continue
				}
			}
			if prev == -1 {
				sce.SfIdx[0] = sce.SfIdx[w*16+g] // global gain
			}
			prev = sce.SfIdx[w*16+g]
		}
	}

	// Every band, coded or not, must carry a chain-legal scalefactor:
	// forward-fill with the previous coded sf; leading bands get the
	// global gain.
	if prev != -1 {
		last := sce.SfIdx[0]
		for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
			for g := range sce.ICS.NumSwb {
				if !sce.Zeroes[w*16+g] && sce.BandType[w*16+g] != NoiseBT &&
					sce.BandType[w*16+g] < ReservedBT {
					last = sce.SfIdx[w*16+g]
				} else if sce.BandType[w*16+g] < ReservedBT && w*16+g > 0 {
					sce.SfIdx[w*16+g] = last
				}
			}
		}
	}
}
