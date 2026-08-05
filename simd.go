// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import "github.com/tphakala/go-aac/internal/simdbuild"

// SIMDEnabled reports whether this build has the SIMD kernels (the NMR Viterbi
// trellis, the AbsPow34 magnitude transform, and the QuantizeBands quantizer)
// compiled in instead of their scalar counterparts. They are the default; a
// build with -tags noasm links the scalar kernels and no SIMD code, and
// SIMDEnabled returns false.
//
// It is a performance signal and nothing more. The opt-out exists for a consumer
// who wants a pure-Go binary with no linked assembly; SIMDEnabled lets a build or
// startup check assert which one it got. It says nothing about output: both
// builds produce byte-identical streams and pass the same differential gate
// against the C reference. It also says nothing about the CPU underneath, only
// about what was compiled in. Each kernel then takes the widest path its host
// offers, and they differ: the trellis needs AVX2 on x86_64 and falls back to
// portable Go without it, while AbsPow34 and QuantizeBands use AVX (AbsPow34 with
// an SSE path below it). All use NEON on arm64.
func SIMDEnabled() bool { return simdbuild.Enabled }
