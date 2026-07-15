// SPDX-License-Identifier: LGPL-2.1-or-later

package window

import (
	"math"

	"github.com/tphakala/go-aac/internal/fmath"
)

// Fixed-point (int32) decoder windows, mirroring init_tables_fixed_fn
// (libavcodec/aac/aacdec_fixed.c:53-66 @ d09d5afc3a). The KBD windows are
// computed at init (their C initializer is all-double arithmetic, which
// ports exactly); the sine windows are baked oracle dumps, because their C
// initializer rounds sinf() output and the oracle libm's sinf is not
// correctly rounded (see sinefixed_tables.go).
var (
	KBDLong1024Fixed = KBDFixed(4.0, 1024)
	KBDShort128Fixed = KBDFixed(6.0, 128)
	Sine1024Fixed    = sine1024FixedTab[:]
	Sine128Fixed     = sine128FixedTab[:]
)

// KBDFixed computes the integer Kaiser-Bessel derived window as a length-n
// slice: all n entries are filled and used (they are the rising half of the
// 2n-point symmetric window, which vector_fmul_window reads in full). Mirrors
// the int_window branch of kbd_window_init
// (libavcodec/kbdwin.c:25-52 @ d09d5afc3a):
// lrint(2147483647 * sqrt(sum*scale)), all intermediates double, lrint
// rounding ties to even (FE_TONEAREST).
func KBDFixed(alpha float64, n int) []int32 {
	a := alpha * math.Pi / float64(n)
	alpha2 := 4 * a * a
	temp := make([]float64, n/2+1)
	scale := 0.0
	for i := 0; i <= n/2; i++ {
		temp[i] = fmath.BesselI0(math.Sqrt(float64(i) * float64(n-i) * alpha2))
		m := 1.0
		if i > 0 && i < n/2 {
			m = 2.0
		}
		scale += temp[i] * m
	}
	scale = 1.0 / (scale + 1)
	w := make([]int32, n)
	sum := 0.0
	for i := 0; i <= n/2; i++ {
		sum += temp[i]
		w[i] = int32(math.RoundToEven(2147483647 * math.Sqrt(sum*scale)))
	}
	for i := n/2 + 1; i < n; i++ {
		sum += temp[n-i]
		w[i] = int32(math.RoundToEven(2147483647 * math.Sqrt(sum*scale)))
	}
	return w
}

// SineFixed computes the integer sine window as a length-n slice (all n
// entries filled) the way the FORMULA computes it: sine_window_init_fixed
// (libavcodec/sinewin_fixed_tablegen.h:56-63 @ d09d5afc3a),
// SIN_FIX(a) = (int)floor(a*0x80000000 + 0.5) where a is sinf() of a float
// argument, here approximated by the correctly rounded
// float32(sin(float64(x))).
//
// This is NOT what the decoder uses: the oracle libm's sinf deviates from
// correct rounding on a handful of inputs (14/1024, 3/128), so the decoder
// windows are the baked oracle values above. This function exists so
// TestSineFixedBakedDelta can pin exactly how far the baked tables sit from
// the portable formula; if a future pin moves, rerun tools/cimdct and
// re-bake.
func SineFixed(n int) []int32 {
	w := make([]int32, n)
	for i := range n {
		x := float32((float64(i) + 0.5) * (math.Pi / (2.0 * float64(n))))
		s := float32(math.Sin(float64(x))) // sinf, if sinf rounded correctly
		w[i] = int32(math.Floor(float64(s)*2147483648.0 + 0.5))
	}
	return w
}
