// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"fmt"
	mbits "math/bits"

	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/vlc"
)

// decodeSpectrum decodes spectral_data down to quantized integer
// coefficients, the fixed-point decoder's symbol level. Mirrors the
// USE_FIXED branches of decode_spectrum_and_dequant
// (libavcodec/aac/aacdec_proc_template.c @ d09d5afc3a). Dequantization
// (vector_pow43 + subband_scale) and the PNS noise fill are later phases;
// noise and intensity bands carry no spectral symbols.
//
// Every swb width in the offset tables is a positive multiple of 4, the
// invariant the C's do-while quad/pair loops rely on; the slice-bounded
// loops here consume identical codeword counts under that invariant.
func decodeSpectrum(r *bits.Reader, sce *SCE, pulse *Pulse) error {
	ics := &sce.ICS
	coefs := &sce.QCoefs
	offsets := ics.SWBOffset
	for i := range coefs {
		coefs[i] = 0
	}

	coefBase := 0
	idx := 0
	for g := range ics.NumWindowGroups {
		gLen := ics.GroupLen[g]
		for i := 0; i < ics.MaxSFB; i, idx = i+1, idx+1 {
			cbtM1 := uint32(sce.BandType[idx]) - 1 // ZERO_BT wraps to 2^32-1
			offLen := int(offsets[i+1] - offsets[i])
			if cbtM1 >= NoiseBT-1 {
				// Zero, noise and intensity bands carry no coefficient bits.
				continue
			}
			tab := vlc.Spectral[cbtM1]
			for group := range gLen {
				cf := coefBase + group*128 + int(offsets[i])
				band := coefs[cf : cf+offLen]
				var err error
				switch cbtM1 >> 1 {
				case 0: // codebooks 1-2: signed quads
					err = decodeSQuad(r, tab, band)
				case 1: // codebooks 3-4: unsigned quads with sign bits
					err = decodeUQuad(r, tab, band)
				case 2: // codebooks 5-6: signed pairs
					err = decodeSPair(r, tab, band)
				case 3, 4: // codebooks 7-10: unsigned pairs with sign bits
					err = decodeUPair(r, tab, band)
				default: // codebook 11: escape pairs
					err = decodeEscape(r, tab, band)
				}
				if err != nil {
					return err
				}
			}
		}
		coefBase += gLen << 7
	}

	if pulse != nil {
		applyPulses(sce, pulse)
	}
	return nil
}

// decodeSQuad mirrors the case 0 loop with DEC_SQUAD
// (aacdec_proc_template.c / aacdec_fixed_dequant.h @ d09d5afc3a).
func decodeSQuad(r *bits.Reader, tab *vlc.Table, out []int32) error {
	for cf := 0; cf < len(out); cf += 4 {
		cbIdx, ok := tab.Decode(r)
		if !ok {
			return fmt.Errorf("%w: bad spectral code", ErrInvalidData)
		}
		out[cf+0] = int32(cbIdx&3) - 1
		out[cf+1] = int32(cbIdx>>2&3) - 1
		out[cf+2] = int32(cbIdx>>4&3) - 1
		out[cf+3] = int32(cbIdx>>6&3) - 1
	}
	return nil
}

// decodeUQuad mirrors the case 1 loop with DEC_UQUAD @ d09d5afc3a. The C
// hands DEC_UQUAD the full 32-bit cache; only the top nnz bits are ever
// examined for non-zero values (zero values multiply whatever bit is at
// the top), so reading exactly nnz bits MSB-aligned is value-identical.
func decodeUQuad(r *bits.Reader, tab *vlc.Table, out []int32) error {
	for cf := 0; cf < len(out); cf += 4 {
		cbIdx, ok := tab.Decode(r)
		if !ok {
			return fmt.Errorf("%w: bad spectral code", ErrInvalidData)
		}
		nnz := int(cbIdx >> 8 & 15)
		var sign uint32
		if nnz > 0 {
			sign = r.Read(nnz) << (32 - nnz)
		}
		nz := cbIdx >> 12
		for j := range 4 {
			out[cf+j] = int32(cbIdx>>(2*j)&3) * (1 + (int32(sign)>>31)*2)
			if j < 3 {
				sign <<= nz & 1
				nz >>= 1
			}
		}
	}
	return nil
}

// decodeSPair mirrors the case 2 loop with DEC_SPAIR @ d09d5afc3a.
func decodeSPair(r *bits.Reader, tab *vlc.Table, out []int32) error {
	for cf := 0; cf < len(out); cf += 2 {
		cbIdx, ok := tab.Decode(r)
		if !ok {
			return fmt.Errorf("%w: bad spectral code", ErrInvalidData)
		}
		out[cf+0] = int32(cbIdx&15) - 4
		out[cf+1] = int32(cbIdx>>4&15) - 4
	}
	return nil
}

// decodeUPair mirrors the case 3/4 loop with DEC_UPAIR @ d09d5afc3a,
// including the packed shift (cb_idx >> 12) that aligns a lone
// first-value sign bit to bit 1.
func decodeUPair(r *bits.Reader, tab *vlc.Table, out []int32) error {
	for cf := 0; cf < len(out); cf += 2 {
		cbIdx, ok := tab.Decode(r)
		if !ok {
			return fmt.Errorf("%w: bad spectral code", ErrInvalidData)
		}
		nnz := int(cbIdx >> 8 & 15)
		var sign uint32
		if nnz > 0 {
			sign = r.Read(nnz) << (cbIdx >> 12)
		}
		out[cf+0] = int32(cbIdx&15) * (1 - int32(sign&0xFFFFFFFE))
		out[cf+1] = int32(cbIdx>>4&15) * (1 - int32(sign&1)*2)
	}
	return nil
}

// decodeEscape mirrors the default (ESC) loop @ d09d5afc3a: N leading
// ones, a zero, then N+4 explicit bits; total escape length must stay
// under 22 bits, so N > 8 is invalid data.
func decodeEscape(r *bits.Reader, tab *vlc.Table, out []int32) error {
	for cf := 0; cf < len(out); cf += 2 {
		cbIdx, ok := tab.Decode(r)
		if !ok {
			return fmt.Errorf("%w: bad spectral code", ErrInvalidData)
		}
		if cbIdx == 0 {
			out[cf+0] = 0
			out[cf+1] = 0
			continue
		}
		nnz := int(cbIdx >> 12)
		nzt := cbIdx >> 8
		signBits := r.Read(nnz) << (32 - nnz)
		for j := range 2 {
			if nzt&(1<<j) != 0 {
				b := mbits.LeadingZeros32(^r.Peek(32))
				if b > 8 {
					return fmt.Errorf("%w: spectral data ESC overflow",
						ErrInvalidData)
				}
				r.Skip(b + 1)
				b += 4
				n := int32(1)<<b + int32(r.Read(b))
				if signBits&(1<<31) != 0 {
					n = -n
				}
				out[cf+j] = n
				signBits <<= 1
			} else {
				v := int32(cbIdx & 15)
				if signBits&(1<<31) != 0 {
					v = -v
				}
				out[cf+j] = v
				if v != 0 {
					signBits <<= 1
				}
			}
			cbIdx >>= 4
		}
	}
	return nil
}

// applyPulses adds pulse amplitudes to the quantized coefficients.
// Mirrors the USE_FIXED pulse block of decode_spectrum_and_dequant
// @ d09d5afc3a, including its exact sign convention (a zero coefficient
// becomes -amp).
func applyPulses(sce *SCE, pulse *Pulse) {
	offsets := sce.ICS.SWBOffset
	idx := 0
	for i := range pulse.NumPulse {
		co := sce.QCoefs[pulse.Pos[i]]
		for int(offsets[idx+1]) <= pulse.Pos[i] {
			idx++
		}
		if sce.BandType[idx] != NoiseBT && sce.SF[idx] != 0 {
			ico := int32(-pulse.Amp[i])
			if co != 0 {
				if co > 0 {
					ico = co - ico
				} else {
					ico = co + ico
				}
			}
			sce.QCoefs[pulse.Pos[i]] = ico
		}
	}
}
