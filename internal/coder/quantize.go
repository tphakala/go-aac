// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	mbits "math/bits"

	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
)

// Coder holds the scratch state shared by the quantizer search, the
// sectioning trellis and the quantize-and-encode core: the pow34-scaled
// coefficients, the integer quantization scratch, the memoization cache and
// the trellis path matrix. Mirrors the coder-owned fields of AACEncContext
// (libavcodec/aacenc.h @ d09d5afc3a: scoefs, qcoefs,
// quantize_band_cost_cache) plus the C stack scratch of
// codebook_trellis_rate, preallocated here so the encode path performs no
// steady-state allocations (docs/go-design.md allocation policy).
type Coder struct {
	scoefs          [1024]float32
	qcoefs          [96]int32
	cacheGeneration uint16
	cache           [256][128]cacheEntry
	path            [121][CBTotAll]trellisPath
	stackRun        [120]int
	stackCB         [120]int
	lpcScratch      [1024]float64 // ff_lpc windowed_samples scratch (TNS)
	// RandomState is the deterministic PNS noise LFSR, seeded with
	// 0x1f2e3d4c at init (aacenc.c:1640 @ d09d5afc3a).
	RandomState int32
}

// cacheEntry mirrors AACQuantizeBandCostCacheEntry (libavcodec/aacenc.h
// @ d09d5afc3a).
type cacheEntry struct {
	rd         float32
	energy     float32
	bits       int32
	cb         int8
	rtz        int8
	generation uint16
}

// CacheInit invalidates the quantize-band-cost cache by bumping the
// generation stamp. Mirrors aacenc.c:ff_quantize_band_cost_cache_init
// @ d09d5afc3a. Must be called per channel per search iteration
// (docs/porting-guide.md pitfall 6).
func (c *Coder) CacheInit() {
	c.cacheGeneration++
	if c.cacheGeneration == 0 {
		c.cache = [256][128]cacheEntry{}
		c.cacheGeneration = 1
	}
}

// cbInfo describes one codebook class for the table-driven quantize core,
// replacing the nine QUANTIZE_AND_ENCODE_BAND_COST_FUNC macro
// specializations of aaccoder.c @ d09d5afc3a (docs/go-design.md idiom map).
type cbInfo struct {
	zeroLike bool // BT_ZERO, BT_NOISE or BT_STEREO: nothing is coded
	unsigned bool // BT_UNSIGNED: magnitude codebook plus sign bits
	pair     bool // BT_PAIR: dimension 2 instead of 4
	escape   bool // BT_ESC: codebook 11 escape sequences
}

// cbDescriptors is indexed by band type and mirrors
// quantize_and_encode_band_cost_arr (aaccoder.c:228 @ d09d5afc3a):
// 0 ZERO, 1-2 SQUAD, 3-4 UQUAD, 5-6 SPAIR, 7-10 UPAIR, 11 ESC,
// 12 unused (RESERVED_BT), 13 NOISE, 14-15 STEREO (intensity).
var cbDescriptors = [16]cbInfo{
	0:  {zeroLike: true},
	1:  {},
	2:  {},
	3:  {unsigned: true},
	4:  {unsigned: true},
	5:  {pair: true},
	6:  {pair: true},
	7:  {unsigned: true, pair: true},
	8:  {unsigned: true, pair: true},
	9:  {unsigned: true, pair: true},
	10: {unsigned: true, pair: true},
	11: {unsigned: true, pair: true, escape: true},
	12: {zeroLike: true}, // never a band type; mirrors _NONE (av_assert0)
	13: {zeroLike: true},
	14: {zeroLike: true},
	15: {zeroLike: true},
}

// quant quantizes one coefficient magnitude, returning the absolute value
// of the quantized coefficient. Mirrors aacenc_utils.h:quant @ d09d5afc3a.
func quant(coef, q, rounding float32) int {
	a := coef * q
	return int(fmath.Sqrt32(a*fmath.Sqrt32(a)) + rounding)
}

// log2i is FFmpeg's av_log2: floor(log2(v)) with log2i(0) == 0.
func log2i(v int) int {
	if v == 0 {
		return 0
	}
	return mbits.Len32(uint32(v)) - 1
}

func clip(v, lo, hi int) int { return min(max(v, lo), hi) }
func absf(x float32) float32 { return max(x, -x) }
func clipf(v, lo, hi float32) float32 {
	return min(max(v, lo), hi)
}

// quantizeAndEncodeBandCost quantizes in with scalefactor index scaleIdx and
// codebook cb, returning the rate-distortion cost. When pb is non-nil the
// codewords, sign bits and escapes are written; when out is non-nil the
// dequantized coefficients are stored; when scaled is nil the pow34 spectrum
// is computed into the Coder scratch. resBits/energy receive the exact
// spectral bit count and quantized energy when non-nil.
// Mirrors aaccoder.c:quantize_and_encode_band_cost_template @ d09d5afc3a
// (aaccoder.c:75-200), with the nine QUANTIZE_AND_ENCODE_BAND_COST_FUNC
// macro specializations folded into one table-driven body per
// docs/go-design.md.
// Splitting it would break the line-by-line mapping to the pinned source,
// hence the complexity waivers.
//
//nolint:gocognit,gocyclo // faithful port of a single C function, see above
func (c *Coder) quantizeAndEncodeBandCost(pb *bits.Writer, in, out, scaled []float32,
	scaleIdx, cb int, lambda, uplim float32, resBits *int, energy *float32,
	rounding float32) float32 {
	qIdx := tables.PowSF2Zero - scaleIdx + ScaleOnePos - ScaleDiv512
	q := tables.Pow2SF[qIdx]
	q34 := tables.Pow34SF[qIdx]
	iq := tables.Pow2SF[tables.PowSF2Zero+scaleIdx-ScaleOnePos+ScaleDiv512]
	clippedEscape := 165140.0 * iq
	size := len(in)
	var cost, qenergy float32
	d := cbDescriptors[cb]
	dim := 4
	if d.pair {
		dim = 2
	}
	totBits := 0

	if d.zeroLike {
		for i := range size {
			t := float32(in[i] * in[i]) // no cross-statement FMA
			cost += t
		}
		if resBits != nil {
			*resBits = 0
		}
		if energy != nil {
			*energy = qenergy
		}
		if out != nil {
			for i := range size {
				out[i] = 0
			}
		}
		return cost * lambda
	}
	if scaled == nil {
		dsp.AbsPow34(c.scoefs[:size], in)
		scaled = c.scoefs[:size]
	}
	dsp.QuantizeBands(c.qcoefs[:size], in, scaled, !d.unsigned,
		int(tables.CBMaxval[cb]), q34, rounding)
	off := 0
	if !d.unsigned {
		off = int(tables.CBMaxval[cb])
	}
	for i := 0; i < size; i += dim {
		curidx := 0
		for j := range dim {
			curidx *= int(tables.CBRange[cb])
			curidx += int(c.qcoefs[i+j]) + off
		}
		curbits := int(tables.SpectralBits[cb-1][curidx])
		vec := tables.CodebookVectors[cb-1][curidx*dim:]
		var rd float32
		if d.unsigned {
			for j := range dim {
				t := absf(in[i+j])
				var quantized float32
				if d.escape && vec[j] == 64.0 {
					if t >= clippedEscape {
						quantized = clippedEscape
						curbits += 21
					} else {
						cq := clip(quant(t, q, rounding), 0, (1<<13)-1)
						quantized = float32(tables.Pow43[cq] * iq)
						curbits += log2i(cq)*2 - 4 + 1
					}
				} else {
					quantized = float32(vec[j] * iq) // no FMA into di below
				}
				di := t - quantized
				if out != nil {
					if in[i+j] >= 0 {
						out[i+j] = quantized
					} else {
						out[i+j] = -quantized
					}
				}
				if vec[j] != 0.0 {
					curbits++
				}
				t2 := float32(quantized * quantized) // no cross-statement FMA
				qenergy += t2
				t3 := float32(di * di)
				rd += t3
			}
		} else {
			for j := range dim {
				quantized := float32(vec[j] * iq)    // no FMA into di below
				t2 := float32(quantized * quantized) // no cross-statement FMA
				qenergy += t2
				if out != nil {
					out[i+j] = quantized
				}
				di := in[i+j] - quantized
				t3 := float32(di * di)
				rd += t3
			}
		}
		tc := float32(rd * lambda) // no cross-statement FMA
		cost += tc + float32(curbits)
		totBits += curbits
		if cost >= uplim {
			return uplim
		}
		if pb != nil {
			pb.Put(int(tables.SpectralBits[cb-1][curidx]),
				uint32(tables.SpectralCodes[cb-1][curidx]))
			if d.unsigned {
				for j := range dim {
					if tables.CodebookVectors[cb-1][curidx*dim+j] != 0.0 {
						var sign uint32
						if in[i+j] < 0.0 {
							sign = 1
						}
						pb.Put(1, sign)
					}
				}
			}
			if d.escape {
				for j := range 2 {
					if tables.CodebookVectors[cb-1][curidx*2+j] == 64.0 {
						coef := clip(quant(absf(in[i+j]), q, rounding), 16, (1<<13)-1)
						length := log2i(coef)
						pb.Put(length-4+1, uint32(1)<<(length-4+1)-2)
						pb.Put(length, uint32(coef))
					}
				}
			}
		}
	}

	if resBits != nil {
		*resBits = totBits
	}
	if energy != nil {
		*energy = qenergy
	}
	return cost
}

// QuantizeAndEncodeBand quantizes one band and writes its codewords.
// rtz selects round-to-zero quantization of ESC escapes for near-clipping
// windows. Mirrors aaccoder.c:quantize_and_encode_band @ d09d5afc3a (the
// rtz table differs from the standard one only in the ESC entry).
func (c *Coder) QuantizeAndEncodeBand(pb *bits.Writer, in []float32,
	scaleIdx, cb int, lambda float32, rtz bool) {
	rounding := float32(RoundStandard)
	if rtz && cb == EscBT {
		rounding = RoundToZero
	}
	c.quantizeAndEncodeBandCost(pb, in, nil, nil, scaleIdx, cb, lambda,
		fmath.Inf32(), nil, nil, rounding)
}

// quantizeBandCost mirrors aacenc_quantization.h:quantize_band_cost
// @ d09d5afc3a.
func (c *Coder) quantizeBandCost(in, scaled []float32, scaleIdx, cb int,
	lambda, uplim float32, resBits *int, energy *float32) float32 {
	return c.quantizeAndEncodeBandCost(nil, in, nil, scaled, scaleIdx, cb,
		lambda, uplim, resBits, energy, RoundStandard)
}

// quantizeBandCostBits mirrors aacenc_quantization.h:quantize_band_cost_bits
// @ d09d5afc3a: the exact spectral bit count at lambda 0.
func (c *Coder) quantizeBandCostBits(in, scaled []float32, scaleIdx, cb int) int {
	auxBits := 0
	c.quantizeAndEncodeBandCost(nil, in, nil, scaled, scaleIdx, cb,
		0.0, fmath.Inf32(), &auxBits, nil, RoundStandard)
	return auxBits
}

// quantizeBandCostCached memoizes quantizeBandCost per (scalefactor, band)
// slot. Mirrors aacenc_quantization_misc.h:quantize_band_cost_cached
// @ d09d5afc3a.
func (c *Coder) quantizeBandCostCached(w, g int, in, scaled []float32,
	scaleIdx, cb int, lambda, uplim float32, resBits *int, energy *float32,
	rtz int8) float32 {
	entry := &c.cache[scaleIdx][w*16+g]
	if entry.generation != c.cacheGeneration || entry.cb != int8(cb) || entry.rtz != rtz {
		b := 0
		entry.rd = c.quantizeBandCost(in, scaled, scaleIdx, cb, lambda, uplim,
			&b, &entry.energy)
		entry.bits = int32(b)
		entry.cb = int8(cb)
		entry.rtz = rtz
		entry.generation = c.cacheGeneration
	}
	if resBits != nil {
		*resBits = int(entry.bits)
	}
	if energy != nil {
		*energy = entry.energy
	}
	return entry.rd
}

// FindMaxVal returns the maximum pow34-scaled magnitude of a band across a
// window group. Mirrors aacenc_utils.h:find_max_val @ d09d5afc3a.
func FindMaxVal(groupLen, swbSize int, scaled []float32) float32 {
	var maxval float32
	for w2 := range groupLen {
		for i := range swbSize {
			maxval = max(maxval, scaled[w2*128+i])
		}
	}
	return maxval
}

// FindMinBook returns the smallest codebook able to represent a band whose
// pow34 maximum is maxval at scalefactor index sf. Mirrors
// aacenc_utils.h:find_min_book @ d09d5afc3a.
func FindMinBook(maxval float32, sf int) int {
	q34 := tables.Pow34SF[tables.PowSF2Zero-sf+ScaleOnePos-ScaleDiv512]
	t := float32(maxval * q34) // no FMA: round the product before the add
	qmaxval := int(t + cQuant)
	if qmaxval >= len(tables.MaxvalCB) {
		return 11
	}
	return int(tables.MaxvalCB[qmaxval])
}
