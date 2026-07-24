// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import "github.com/tphakala/go-aac/internal/simdbuild"

// SIMDEnabled reports whether this build was compiled with -tags goaac_simd, so
// that the optional SIMD kernels (the NMR Viterbi trellis and the AbsPow34
// magnitude transform) are compiled in instead of their scalar counterparts.
//
// It is a performance signal and nothing more. The tag is opt-in, so a consumer
// can ship the slower scalar path without noticing; SIMDEnabled lets a build or
// startup check assert which one it got. It says nothing about output: both
// builds produce byte-identical streams and pass the same differential gate
// against the C reference. It also says nothing about the CPU underneath, only
// about what was compiled in. Each kernel then takes the widest path its host
// offers, and the two differ: the trellis needs AVX2 on x86_64 and falls back to
// portable Go without it, while AbsPow34 needs only AVX and has an SSE path
// below that. Both use NEON on arm64.
func SIMDEnabled() bool { return simdbuild.Enabled }
