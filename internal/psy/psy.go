// SPDX-License-Identifier: LGPL-2.1-or-later

// Package psy implements the AAC encoder psychoacoustic model: the LAME
// block-switching window decision and the 3GPP TS26.403-inspired threshold
// analysis with its bit reservoir. Mirrors libavcodec/aacpsy.c and the
// consumed subset of libavcodec/psymodel.c/h @ d09d5afc3a. The single
// channel group of ff_psy_init (mono SCE or one CPE) is collapsed into the
// context; coupling's virtual channels are not ported.
package psy

import (
	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// Constants for the 3GPP AAC psychoacoustic model.
// Mirror libavcodec/aacpsy.c:45-108 @ d09d5afc3a.
const (
	psy3gppThrSpreadHi  = 1.5 // spreading factor for low-to-hi threshold spreading (15 dB/Bark)
	psy3gppThrSpreadLow = 3.0 // spreading factor for hi-to-low threshold spreading (30 dB/Bark)
	// spreading factor for low-to-hi energy spreading, long block,
	// > 22kbps/channel (20dB/Bark)
	psy3gppEnSpreadHiL1 = 2.0
	// spreading factor for low-to-hi energy spreading, short block (15 dB/Bark)
	psy3gppEnSpreadHiS = 1.5
	// spreading factor for hi-to-low energy spreading, long block (30dB/Bark)
	psy3gppEnSpreadLowL = 3.0
	// spreading factor for hi-to-low energy spreading, short block (20dB/Bark)
	psy3gppEnSpreadLowS = 2.0

	psy3gppRPEMin = 0.01
	psy3gppRPELev = 2.0

	psy3gppC1 = 3.0        // log2(8)
	psy3gppC2 = 1.3219281  // log2(2.5)
	psy3gppC3 = 0.55935729 // 1 - C2 / C1

	psySnr1dB  = 7.9432821e-1 // -1dB
	psySnr25dB = 3.1622776e-3 // -25dB

	psy3gppSaveSlopeL  = -0.46666667
	psy3gppSaveSlopeS  = -0.36363637
	psy3gppSaveAddL    = -0.84285712
	psy3gppSaveAddS    = -0.75
	psy3gppSpendSlopeL = 0.66666669
	psy3gppSpendSlopeS = 0.81818181
	psy3gppSpendAddL   = -0.35
	psy3gppSpendAddS   = -0.26111111
	psy3gppClipLoL     = 0.2
	psy3gppClipLoS     = 0.2
	psy3gppClipHiL     = 0.95
	psy3gppClipHiS     = 0.75

	psy3gppAhThrLong  = 0.5
	psy3gppAhThrShort = 0.63

	psyPeForgetSlope = 511
)

// Hole avoidance states. Mirror the anonymous enum at aacpsy.c:86-90.
const (
	ahNone = iota
	ahInactive
	ahActive
)

// bitsToPE mirrors PSY_3GPP_BITS_TO_PE (aacpsy.c:92 @ d09d5afc3a).
func bitsToPE(bits float32) float32 { return bits * 1.18 }

// peToBits mirrors PSY_3GPP_PE_TO_BITS (aacpsy.c:93 @ d09d5afc3a).
func peToBits(pe float32) float32 { return pe / 1.18 }

// LAME psy model constants. Mirror aacpsy.c:95-108 @ d09d5afc3a.
const (
	psyLameFirLen  = 21   // LAME psy model FIR order
	blockSizeLong  = 1024 // long block size
	blockSizeShort = 128  // short block size
	// lookaheadLen is the shortest la slice Window can read in its lookahead
	// branch: firbuf starts at la[blockSizeShort/4-psyLameFirLen] and psyHpFilter
	// reaches firbuf[blockSizeLong-1+psyLameFirLen], i.e. la[1055].
	lookaheadLen        = (blockSizeShort/4 - psyLameFirLen) + (blockSizeLong - 1 + psyLameFirLen) + 1
	numBlocksShort      = 8    // number of blocks in a short sequence
	psyLameNumSubblocks = 2    // number of sub-blocks in each short block
	psyLamePreEchoGap   = 12   // min consecutive long frames before the relaxation applies
	psyLamePreEchoQuiet = 0.4  // pre-onset must be below this fraction of the frame peak
	psyLamePreEchoRed   = 0.45 // attack-threshold multiplier for a qualifying isolated onset
)

// band is the per-band state of the 3GPP model.
// Mirrors struct AacPsyBand (aacpsy.c:117-127 @ d09d5afc3a).
type band struct {
	energy      float32 // band energy
	thr         float32 // energy threshold
	thrQuiet    float32 // threshold in quiet
	nzLines     float32 // number of non-zero spectral lines
	activeLines float32 // number of active spectral lines
	pe          float32 // perceptual entropy
	peConst     float32 // constant part of the PE calculation
	normFac     float32 // normalization factor for linearization
	avoidHoles  int     // hole avoidance flag
}

// channel is the per-channel psy state.
// Mirrors struct AacPsyChannel (aacpsy.c:132-150 @ d09d5afc3a).
type channel struct {
	band     [128]band // bands information
	prevBand [128]band // bands information from the previous frame

	winEnergy     float32    // sliding average of channel energy (psy_3gpp_window only)
	iirState      [2]float32 // hi-pass IIR filter state (psy_3gpp_window only)
	nextGrouping  uint8      // stored grouping scheme for the next frame
	nextWindowSeq int        // window sequence to be used in the next frame
	// LAME psy model specific members
	attackThreshold    float32
	prevEnergySubshort [numBlocksShort * psyLameNumSubblocks]float32
	prevAttack         int  // attack value for the last short block in the previous sequence
	nextAttack0Zero    bool // whether attack[0] of the next frame is zero
	framesSinceShort   int  // consecutive long frames (pre-echo-aware isolated-onset gate)

	// rate-loop re-analysis rewind state, see Analyze
	rcFrameNum int64
	rcPrevBand [128]band
}

// bandCoeffs holds the per-band frame-type-dependent coefficients.
// Mirrors struct AacPsyCoeffs (aacpsy.c:155-161 @ d09d5afc3a).
type bandCoeffs struct {
	ath       float32    // absolute threshold of hearing per band
	barks     float32    // Bark value for each spectral band in long frame
	spreadLow [2]float32 // low-to-high spreading factors [threshold, energy]
	spreadHi  [2]float32 // high-to-low spreading factors [threshold, energy]
	minSnr    float32    // minimal SNR
}

// FFChannel is the per-channel analysis output consumed by the coders.
// Mirrors struct FFPsyChannel (psymodel.h:60-63 @ d09d5afc3a).
type FFChannel struct {
	PsyBands [128]coder.PsyBand // channel band information
	Entropy  float32            // total PE for this channel
}

// WindowInfo is the window decision for one channel.
// Mirrors struct FFPsyWindowInfo (psymodel.h:77-84 @ d09d5afc3a); the
// clipping evaluation stays in the encoder (internal/enc), which owns the
// window buffers.
type WindowInfo struct {
	WindowType  [3]int // current, previous and next window type
	WindowShape int    // 1 = KBD, 0 = sine
	NumWindows  int
	Grouping    [8]int // windows per group, at group-start positions
}

// Context is the psychoacoustic model state: the FFPsyContext fields the
// AAC encoder consumes plus the private AacPsyContext, merged because Go
// needs no model vtable (one model exists).
// Mirrors FFPsyContext (psymodel.h:89-109) and AacPsyContext
// (aacpsy.c:166-185) @ d09d5afc3a.
type Context struct {
	sampleRate int
	channels   int

	Bands    [2][]uint8 // scalefactor band sizes: [0] long, [1] short
	NumBands [2]int     // number of scalefactor bands: [0] long, [1] short
	Cutoff   int        // analysis bandwidth in Hz

	Bitres struct {
		Size  int // size of the bit reservoir in bits
		Bits  int // number of bits used in the bit reservoir
		Alloc int // bits allocated by the psy, or -1 if no allocation was done
	}

	Ch []FFChannel // per-channel analysis output

	chanBitrate int // bitrate per channel
	frameBits   int // average bits per frame
	fillLevel   int // bit reservoir fill level
	pe          struct {
		min        float32 // minimum allowed PE for bit factor calculation
		max        float32 // maximum allowed PE for bit factor calculation
		previous   float32 // allowed PE of the previous frame
		correction float32 // PE correction factor (unused, kept for parity)
	}
	psyCoef [2][64]bandCoeffs
	pch     []channel

	// rate-loop re-analysis rewind state, see Analyze
	rcFrameNum   int64
	rcFirstCh    int
	rcFillLevel  int
	rcPeMin      float32
	rcPeMax      float32
	rcPePrevious float32
}

// calcBark returns the Bark value for a given frequency.
// Mirrors aacpsy.c:calc_bark @ d09d5afc3a.
func calcBark(f float32) float32 {
	t1 := 13.3 * fmath.Atan32(0.00076*f)
	t2 := 3.5 * fmath.Atan32((f/7500.0)*(f/7500.0))
	return t1 + t2
}

const athAdd = 4

// ath returns the absolute threshold of hearing for a given frequency,
// borrowed from LAME. Mirrors aacpsy.c:ath @ d09d5afc3a: the frequency
// scaling is float32, the curve itself is computed in float64 like the C
// (double constants promote the expression).
func ath(f, add float32) float32 {
	f /= 1000.0
	fd := float64(f)
	v := 3.64*fmath.Pow(fd, -0.8) -
		6.8*fmath.Exp(-0.6*(fd-3.4)*(fd-3.4)) +
		6.0*fmath.Exp(-0.15*(fd-8.7)*(fd-8.7)) +
		(0.6+0.04*float64(add))*0.001*fd*fd*fd*fd
	return float32(v)
}

// New creates the psy model state for one mono SCE or one stereo CPE.
// Mirrors psy_3gpp_init (aacpsy.c:318-403) plus the consumed subset of
// ff_psy_init (psymodel.c:28-65) @ d09d5afc3a. bands/numBands carry the
// long ([0]) and short ([1]) scalefactor band tables; cutoff is the coding
// bandwidth in Hz (always non-zero: the encoder computes it at init).
// The QSCALE/global_quality branches are not ported (ABR only in Phase 2).
func New(sampleRate, bitRate, channels, cutoff int, bands [2][]uint8, numBands [2]int) *Context {
	ctx := &Context{}
	ctx.Reset(sampleRate, bitRate, channels, cutoff, bands, numBands)
	return ctx
}

// Reset re-arms an existing Context for a new, independent stream with the
// given parameters, reusing the Context and the two per-channel slices
// (channel and FFChannel are pure value types) so a pooled encoder rebuilds
// the psychoacoustic model without allocating. The result is byte-identical to
// a fresh New(...) with the same arguments: every field is recomputed here, and
// the reused slices are cleared to the same zeroed state make would have
// produced. This backs the allocation-free pcm.EncodeInterleaved reuse path
// (issue #41). Growing the channel count reallocates the per-channel slices;
// shrinking or matching it reslices and clears them in place.
func (ctx *Context) Reset(sampleRate, bitRate, channels, cutoff int, bands [2][]uint8, numBands [2]int) {
	// Reuse the per-channel slices when they already hold enough room; both
	// element types are allocation-free value structs, so re-slicing and
	// clearing reproduces a fresh make exactly.
	ch := ctx.Ch
	if cap(ch) >= channels {
		ch = ch[:channels]
		clear(ch)
	} else {
		ch = make([]FFChannel, channels)
	}
	pch := ctx.pch
	if cap(pch) >= channels {
		pch = pch[:channels]
		clear(pch)
	} else {
		pch = make([]channel, channels)
	}
	*ctx = Context{
		sampleRate: sampleRate,
		channels:   channels,
		Bands:      bands,
		NumBands:   numBands,
		Cutoff:     cutoff,
		Ch:         ch,
		pch:        pch,
	}

	chanBitrate := bitRate / channels
	bandwidth := cutoff
	numBark := calcBark(float32(bandwidth))

	ctx.chanBitrate = chanBitrate
	ctx.frameBits = min(2560, chanBitrate*blockSizeLong/sampleRate)
	ctx.pe.min = 8.0 * blockSizeLong * float32(bandwidth) / (float32(sampleRate) * 2.0)
	ctx.pe.max = 12.0 * blockSizeLong * float32(bandwidth) / (float32(sampleRate) * 2.0)
	ctx.Bitres.Size = 6144 - ctx.frameBits
	ctx.Bitres.Size -= ctx.Bitres.Size % 8
	ctx.fillLevel = ctx.Bitres.Size
	minath := ath(3410-0.733*athAdd, athAdd)
	for j := range 2 {
		coeffs := &ctx.psyCoef[j]
		bandSizes := bands[j]
		var lineToFrequency float32
		if j != 0 {
			lineToFrequency = float32(sampleRate) / 256.0
		} else {
			lineToFrequency = float32(sampleRate) / 2048.0
		}
		var frameLen float32 = 1024.0
		if j != 0 {
			frameLen = 128.0
		}
		avgChanBits := float32(chanBitrate) * frameLen / float32(sampleRate)
		// reference encoder uses 2.4% here instead of 60% like the spec says
		barkPe := 0.024 * bitsToPE(avgChanBits) / numBark
		var enSpreadLow float32 = psy3gppEnSpreadLowL
		if j != 0 {
			enSpreadLow = psy3gppEnSpreadLowS
		}
		// High energy spreading for long blocks <= 22kbps/channel and short
		// blocks are the same. NOTE: the C compares chan_bitrate (bits/s)
		// against 22.0f, so the L2 branch is effectively dead; ported as
		// written (aacpsy.c:359).
		var enSpreadHi float32 = psy3gppEnSpreadHiL1
		if j != 0 || float32(chanBitrate) <= 22.0 {
			enSpreadHi = psy3gppEnSpreadHiS
		}

		i := 0
		var prev float32
		for g := range numBands[j] {
			i += int(bandSizes[g])
			bark := calcBark(float32(i-1) * lineToFrequency)
			coeffs[g].barks = float32(float64(bark+prev) / 2.0)
			prev = bark
		}
		for g := range numBands[j] - 1 {
			coeff := &coeffs[g]
			// NOTE: the C reads coeffs->barks, i.e. coeffs[0].barks, not
			// coeffs[g].barks (aacpsy.c:371); ported as written.
			barkWidth := coeffs[g+1].barks - coeffs[0].barks
			coeff.spreadLow[0] = float32(fmath.Exp10(float64(-barkWidth * psy3gppThrSpreadLow)))
			coeff.spreadHi[0] = float32(fmath.Exp10(float64(-barkWidth * psy3gppThrSpreadHi)))
			coeff.spreadLow[1] = float32(fmath.Exp10(float64(-barkWidth * enSpreadLow)))
			coeff.spreadHi[1] = float32(fmath.Exp10(float64(-barkWidth * enSpreadHi)))
			peMin := barkPe * barkWidth
			minsnr := float32(fmath.Exp2(float64(peMin/float32(bandSizes[g]))) - 1.5)
			coeff.minSnr = fmath.Clipf(1.0/minsnr, psySnr25dB, psySnr1dB)
		}
		start := 0
		for g := range numBands[j] {
			minscale := ath(float32(start)*lineToFrequency, athAdd)
			for i := 1; i < int(bandSizes[g]); i++ {
				minscale = min(minscale, ath(float32(start+i)*lineToFrequency, athAdd))
			}
			coeffs[g].ath = minscale - minath
			start += int(bandSizes[g])
		}
	}

	ctx.rcFrameNum = -1
	for i := range ctx.pch {
		ctx.pch[i].rcFrameNum = -1
	}

	// lame_window_init (aacpsy.c:279-294): ABR attack threshold per channel.
	for i := range ctx.pch {
		pch := &ctx.pch[i]
		pch.attackThreshold = lameCalcAttackThreshold(bitRate / channels / 1000)
		for j := range pch.prevEnergySubshort {
			pch.prevEnergySubshort[j] = 10.0
		}
	}
}

// lameCalcAttackThreshold maps an ABR bitrate (kbps per channel) to the
// LAME attack threshold. Mirrors aacpsy.c:lame_calc_attack_threshold
// @ d09d5afc3a.
func lameCalcAttackThreshold(bitrate int) float32 {
	// Assume max bitrate to start with.
	lowerRange, upperRange := 12, 12
	lowerRangeKbps := int(tables.PsyABRMap[12].Quality)
	upperRangeKbps := int(tables.PsyABRMap[12].Quality)

	// Determine which bitrates the value specified falls between. The C
	// body is an if+break (aacpsy.c:260-268); inverted here to satisfy
	// gocritic nestingReduce, integer logic identical, pinned by the
	// attack_threshold golden comparison.
	for i := 1; i < 13; i++ {
		if max(bitrate, int(tables.PsyABRMap[i].Quality)) == bitrate {
			continue
		}
		upperRange = i
		upperRangeKbps = int(tables.PsyABRMap[i].Quality)
		lowerRange = i - 1
		lowerRangeKbps = int(tables.PsyABRMap[i-1].Quality)
		break // upper range found
	}

	// Determine which range the value specified is closer to.
	if (upperRangeKbps - bitrate) > (bitrate - lowerRangeKbps) {
		return tables.PsyABRMap[lowerRange].StLrm
	}
	return tables.PsyABRMap[upperRange].StLrm
}
