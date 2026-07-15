// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"github.com/tphakala/go-aac/internal/fdsp"
	"github.com/tphakala/go-aac/internal/tables"
)

// initRandomState is the seed the fixed decoder plants once at init and never
// resets; the PNS RNG state then evolves across every noise band of every
// frame. Mirrors ac->random_state = 0x1f2e3d4c (libavcodec/aac/aacdec.c:1353
// @ d09d5afc3a).
const initRandomState = 0x1f2e3d4c

// exp2tab holds 2^(k/4)/2 for k in 0..3, the fractional-exponent factors of
// subband_scale and noise_scale. Mirrors the exp2tab[] of
// aacdec_fixed_dequant.h:51-55 @ d09d5afc3a, built with
// Q31(x) = (int)(x*2^31 + 0.5). Computed at init in float64 so the rounding
// matches the C's double constant folding exactly (the D2 gate confirms it
// through every tonal band's dequantized value).
var exp2tab = [4]int32{
	q31(1.0000000000 / 2),
	q31(1.1892071150 / 2),
	q31(1.4142135624 / 2),
	q31(1.6817928305 / 2),
}

func q31(x float64) int32 { return int32(x*2147483648.0 + 0.5) }

// lcgRandom mirrors lcg_random (libavcodec/aac/aacdec_proc_template.c:39-43
// @ d09d5afc3a): the Numerical Recipes multiplier/increment in unsigned 32-bit
// arithmetic, the result reinterpreted as a signed int. This is the decoder's
// PNS generator, distinct from the encoder's dsp.LCG.
func lcgRandom(prev int32) int32 {
	return int32(uint32(prev)*1664525 + 1013904223)
}

// vectorPow43 dequantizes the magnitude of each quantized coefficient through
// the cube-root table. Mirrors vector_pow43 (aacdec_fixed_dequant.h:37-49
// @ d09d5afc3a): coef = sign(x) * ff_cbrt_tab_fixed[|x| & 8191]. The C runs in
// place; the port reads src (D0's raw quantized ints, with pulses already
// applied) and writes dst (the dequantized spectrum).
func vectorPow43(dst, src []int32) {
	cbrt := tables.CbrtTabFixed
	dst = dst[:len(src)]
	for i, x := range src {
		if x < 0 {
			dst[i] = -cbrt[(-x)&8191]
		} else {
			dst[i] = cbrt[x&8191]
		}
	}
}

// subbandScale applies the integer scalefactor exponent to a dequantized band.
// Mirrors subband_scale (aacdec_fixed_dequant.h:57-87 @ d09d5afc3a). offset is
// 34 for the main dequant pass and 23 for intensity stereo. src and dst may be
// the same slice (the dequant pass calls it in place).
func subbandScale(dst, src []int32, scale, offset, length int) {
	var ssign int32 = 1
	if scale < 0 {
		ssign = -1
	}
	s := scale
	if s < 0 {
		s = -s
	}
	c := exp2tab[s&3]
	s = offset - (s >> 2)

	dst = dst[:length]
	src = src[:length]
	switch {
	case s > 31:
		for i := range dst {
			dst[i] = 0
		}
	case s > 0:
		round := uint32(1) << (s - 1)
		for i := range dst {
			out := int32((int64(src[i]) * int64(c)) >> 32)
			dst[i] = (int32(uint32(out)+round) >> s) * ssign
		}
	case s > -32:
		s += 32
		round := uint32(1) << (s - 1)
		for i := range dst {
			out := int32((int64(src[i])*int64(c) + int64(round)) >> s)
			dst[i] = out * ssign
		}
	default:
		// The C logs "Overflow in subband_scale()" and leaves dst untouched;
		// unreachable for valid LC streams (offset - (|scale|>>2) <= -32 needs
		// an absurd scalefactor). Preserve the no-op.
	}
}

// noiseScale scales a PNS noise band by its energy and scalefactor. Mirrors
// noise_scale (aacdec_fixed_dequant.h:89-128 @ d09d5afc3a). bandEnergy is the
// fixed_sqrt of the band's scalarproduct.
func noiseScale(coefs []int32, scale, bandEnergy, length int) {
	s := -scale
	c := exp2tab[s&3]
	nlz := 0
	for bandEnergy > 0x7fff {
		bandEnergy >>= 1
		nlz++
	}
	c /= int32(bandEnergy)
	s = 21 + nlz - (s >> 2)

	coefs = coefs[:length]
	switch {
	case s > 31:
		for i := range coefs {
			coefs[i] = 0
		}
	case s >= 0:
		var round uint32
		if s != 0 {
			round = uint32(1) << (s - 1)
		}
		for i := range coefs {
			out := int32((int64(coefs[i]) * int64(c)) >> 32)
			coefs[i] = -(int32(uint32(out)+round) >> s)
		}
	default:
		s += 32
		if s > 0 {
			round := uint32(1) << (s - 1)
			for i := range coefs {
				out := int32((int64(coefs[i])*int64(c) + int64(round)) >> s)
				coefs[i] = -out
			}
		} else {
			for i := range coefs {
				coefs[i] = int32(-int64(coefs[i]) * int64(c) * (int64(1) << uint(-s)))
			}
		}
	}
}

// ffSqrt returns floor(sqrt(a)). Mirrors ff_sqrt (libavcodec/mathops.h:
// 219-239 @ d09d5afc3a): the pinned ff_sqrt is a table-and-FASTDIV
// approximation with a -1 correction that yields exactly the integer square
// root. Verified against the pinned ff_sqrt: 0 mismatches exhaustively over
// [0, 2^24) and sampled to 2^32, so this table-free bit-by-bit floor sqrt is
// a drop-in equivalent (no math import, exact by construction).
func ffSqrt(a uint32) uint32 {
	var res uint32
	bit := uint32(1) << 30
	for bit > a {
		bit >>= 2
	}
	n := a
	for bit != 0 {
		if n >= res+bit {
			n -= res + bit
			res = (res >> 1) + bit
		} else {
			res >>= 1
		}
		bit >>= 2
	}
	return res
}

// fixedSqrt mirrors fixed_sqrt (libavutil/fixed_dsp.h:176-203 @ d09d5afc3a):
// a normalized fixed-point square root seeded by ff_sqrt and refined bit by
// bit. The decoder calls it with bits = 31 on PNS band energy. The input shift
// happens on the SIGNED value (arithmetic) before the reinterpret to unsigned
// that ff_sqrt's argument does, matching the C exactly.
func fixedSqrt(x int32, bits int) int32 {
	shift1 := 30 - bits
	shift2 := bits - 15

	var retval int32
	if shift1 > 0 {
		retval = int32(ffSqrt(uint32(x << shift1)))
	} else {
		retval = int32(ffSqrt(uint32(x >> -shift1)))
	}

	if shift2 > 0 {
		retval <<= shift2
		bitMask := int32(1) << (shift2 - 1)
		for range shift2 {
			guess := retval + bitMask
			accu := int64(guess) * int64(guess)
			square := int32((accu + int64(bitMask)) >> bits)
			if x >= square {
				retval += bitMask
			}
			bitMask >>= 1
		}
	} else {
		retval >>= -shift2
	}
	return retval
}

// dequant finishes decode_spectrum_and_dequant's fixed path for one channel:
// the PNS noise fill of NOISE_BT bands and the pow43 + subband_scale of tonal
// bands, writing sce.Coeffs from sce.QCoefs (which already carries D0's VLC
// symbols and pulses). Zero and intensity bands are left at zero (intensity is
// filled later by apply_intensity_stereo). Mirrors the USE_FIXED pass-1 noise
// branch and pass-2 loop of decode_spectrum_and_dequant
// (aacdec_proc_template.c:88-99,327-348 @ d09d5afc3a), including the shared
// ac->random_state that threads through every noise band in bitstream order.
func (d *Decoder) dequant(sce *SCE) {
	ics := &sce.ICS
	offsets := ics.SWBOffset
	coeffs := sce.Coeffs[:]
	qcoefs := sce.QCoefs[:]
	for i := range coeffs {
		coeffs[i] = 0
	}

	coefBase := 0
	idx := 0
	for g := range ics.NumWindowGroups {
		gLen := ics.GroupLen[g]
		for i := 0; i < ics.MaxSFB; i, idx = i+1, idx+1 {
			cbtM1 := uint32(sce.BandType[idx]) - 1 // ZERO_BT wraps high
			offLen := int(offsets[i+1] - offsets[i])
			switch {
			case cbtM1 == NoiseBT-1:
				for group := range gLen {
					base := coefBase + group*128 + int(offsets[i])
					band := coeffs[base : base+offLen]
					for k := range band {
						d.randomState = lcgRandom(d.randomState)
						band[k] = d.randomState >> 3
					}
					energy := fdsp.ScalarproductFixed(band, band)
					energy = fixedSqrt(energy, 31)
					noiseScale(band, int(sce.SF[idx]), int(energy), offLen)
				}
			case cbtM1 < NoiseBT-1:
				for group := range gLen {
					base := coefBase + group*128 + int(offsets[i])
					dst := coeffs[base : base+offLen]
					src := qcoefs[base : base+offLen]
					vectorPow43(dst, src)
					subbandScale(dst, dst, int(sce.SF[idx]), 34, offLen)
				}
			}
		}
		coefBase += gLen << 7
	}
}
