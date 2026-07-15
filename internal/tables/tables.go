// SPDX-License-Identifier: LGPL-2.1-or-later

// Package tables holds the AAC codec tables consumed by the encoder (and,
// by design, the future decoder's VLC construction). The static tables in
// tables_gen.go are emitted mechanically by tools/gentables from the pinned
// FFmpeg tree; the tables below are computed at init time, mirroring
// ff_aac_float_common_init. Regenerate with go generate (requires the pinned
// FFmpeg checkout, see tools/gentables/generate.sh).
package tables

//go:generate ../../tools/gentables/generate.sh

// PsyLamePreset pairs a quality target with the LAME short threshold.
// Mirrors struct PsyLamePreset (libavcodec/aacpsy.c @ d09d5afc3a): Quality is
// kbps per channel in ABR mode and the requested quality in constant quality
// mode; StLrm is the short threshold for the L, R and M channels.
type PsyLamePreset struct {
	Quality int32
	StLrm   float32
}

// PowSF2Zero is the Pow2SF index corresponding to pow(2, 0).
// Mirrors POW_SF2_ZERO (libavcodec/aac.h @ d09d5afc3a).
const PowSF2Zero = 200

// Pow2SF and Pow34SF are the scalefactor power tables filled at init:
// Pow2SF[i] = 2^((i - PowSF2Zero) / 4), Pow34SF[i] = Pow2SF[i]^(3/4).
// Mirror ff_aac_pow2sf_tab and ff_aac_pow34sf_tab (libavcodec/aactab.c
// @ d09d5afc3a), filled by aac_tableinit.
var (
	Pow2SF  [428]float32
	Pow34SF [428]float32
)

func init() {
	aacTableinit()
}

// aacTableinit fills Pow2SF and Pow34SF with pure float32 arithmetic
// (doublings and one multiply per entry, no libm, hence no math import).
// Mirrors libavcodec/aactab.c:aac_tableinit @ d09d5afc3a line by line; the
// lut values are 2^(i/16) rounded to float64 then float32, exactly like the
// C double literals assigned to a float array.
func aacTableinit() {
	lut64 := [16]float64{
		1.00000000000000000000,
		1.04427378242741384032,
		1.09050773266525765921,
		1.13878863475669165370,
		1.18920711500272106672,
		1.24185781207348404859,
		1.29683955465100966593,
		1.35425554693689272830,
		1.41421356237309504880,
		1.47682614593949931139,
		1.54221082540794082361,
		1.61049033194925430818,
		1.68179283050742908606,
		1.75625216037329948311,
		1.83400808640934246349,
		1.91520656139714729387,
	}
	var exp2Lut [16]float32
	for i, v := range lut64 {
		exp2Lut[i] = float32(v)
	}
	t1 := float32(8.8817841970012523233890533447265625e-16) // 2^(-50)
	t2 := float32(3.63797880709171295166015625e-12)         // 2^(-38)
	t1IncPrev := 0
	t2IncPrev := 8
	for i := range 428 {
		t1IncCur := 4 * (i % 4)
		t2IncCur := (8 + 3*i) % 16
		if t1IncCur < t1IncPrev {
			t1 *= 2
		}
		if t2IncCur < t2IncPrev {
			t2 *= 2
		}
		Pow2SF[i] = t1 * exp2Lut[t1IncCur]
		Pow34SF[i] = t2 * exp2Lut[t2IncCur]
		t1IncPrev = t1IncCur
		t2IncPrev = t2IncCur
	}
}
