// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm

package simdbuild

// Enabled is false on the noasm build (-tags noasm): the scalar kernels are
// compiled in and no code from github.com/tphakala/simd is linked into the
// binary. The default build compiles the other half of this pair instead.
const Enabled = false
