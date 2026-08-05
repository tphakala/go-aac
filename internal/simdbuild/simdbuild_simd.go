// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package simdbuild

// Enabled is true on the default build: the SIMD kernels are compiled in place
// of their scalar counterparts, and github.com/tphakala/simd is linked. A build
// with -tags noasm compiles the noasm half of this pair instead.
const Enabled = true
