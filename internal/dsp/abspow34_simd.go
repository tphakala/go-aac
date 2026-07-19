// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build goaac_simd

package dsp

// Vendoring boundary: github.com/tphakala/simd holds only truly generic math
// primitives (here, f32.AbsPow34). The AAC-specific composition and the
// destination-length panic that TestKernelLengthContract pins stay vendored in
// this package; nothing codec-specific migrates upstream.

import "github.com/tphakala/simd/f32"

// AbsPow34 is the goaac_simd dispatch: it maps out[i] = |in[i]|^(3/4) onto
// f32.AbsPow34 (NEON on arm64, AVX on amd64, bit-identical pure-Go fallback
// elsewhere). Its output is byte-identical to absPow34Scalar for every finite
// input: the primitive computes the same exact abs, the same two IEEE
// correctly-rounded square roots, and the same single correctly-rounded float32
// multiply, and there is no add so the no-FMA question never arises.
// abspow34_simd_equiv_test.go gates it bitwise against the scalar.
//
// The one divergence class is NaN payload bits, which never reaches this call
// site: the public encoder rejects non-finite PCM (#18). The panic must live
// here because f32.AbsPow34 silently processes min(len(dst), len(src)) instead
// of panicking, so without it the destination-length contract would quietly
// weaken on the tagged build.
func AbsPow34(out, in []float32) {
	if len(in) < len(out) {
		panic("dsp: AbsPow34: source shorter than out")
	}
	in = in[:len(out)]
	f32.AbsPow34(out, in)
}
