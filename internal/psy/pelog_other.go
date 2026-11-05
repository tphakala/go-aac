// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm && !arm64

package psy

import "github.com/tphakala/go-aac/internal/fmath"

// peLog2 returns fmath.Log232(e) and fmath.Log232(t) on architectures without
// a logarithm kernel.
func peLog2(e, t float32) (logE, logT float32) {
	return fmath.Log232(e), fmath.Log232(t)
}
