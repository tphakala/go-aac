// SPDX-License-Identifier: LGPL-2.1-or-later
package dsp

// LCG is the encoder's PNS pseudo-random generator. Mirrors
// libavcodec/aacenc_utils.h:lcg_random @ d09d5afc3a. The update order is
// load-bearing for validation diffs; never substitute math/rand.
type LCG uint32

// LCGSeed is the initial state set in aacenc.c (@ d09d5afc3a).
const LCGSeed LCG = 0x1f2e3d4c

// Next advances the state and returns it reinterpreted as int32.
func (l *LCG) Next() int32 {
	*l = *l*1664525 + 1013904223
	return int32(*l)
}
