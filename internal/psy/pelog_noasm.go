// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm

package psy

import "github.com/tphakala/go-aac/internal/fmath"

// PeLogIsSIMD is false on the noasm build: calcPE3gpp takes its two
// logarithms with fmath.Log232.
const PeLogIsSIMD = false

// peLog2 returns fmath.Log232(e) and fmath.Log232(t) on the noasm build.
func peLog2(e, t float32) (logE, logT float32) {
	return fmath.Log232(e), fmath.Log232(t)
}
