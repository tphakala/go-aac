// SPDX-License-Identifier: LGPL-2.1-or-later

// Package simdbuild records whether the SIMD kernels are compiled into this
// build. They are enabled by default; a build with -tags noasm selects the
// scalar fallback instead. It holds nothing but the constant Enabled, split
// across a default / noasm file pair, the same way the kernel dispatch points
// are (internal/coder/nmr_trellis_{simd,noasm}.go,
// internal/dsp/abspow34_{simd,noasm}.go,
// internal/dsp/quantizebands_{simd,noasm}.go).
//
// Its reason to exist is that code with no kernel of its own, in particular the
// public aac.SIMDEnabled accessor, would otherwise have to carry the build tag
// itself to report which kernel set was selected. Keeping the pair here rather
// than in one of the kernel packages avoids making either of them the authority
// on a tag that gates both, and means the next consumer needing the answer does
// not add another copy. Those files decide which implementation compiles; this
// package turns the same tag into a value. Nothing here observes the kernels,
// so the root package asserts that the two agree (simd_kernels_test.go).
package simdbuild
