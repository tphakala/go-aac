// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !goaac_simd

package simdbuild

// Enabled is false on the default build: the scalar kernels are compiled in and
// no code from github.com/tphakala/simd is linked into the binary.
const Enabled = false
