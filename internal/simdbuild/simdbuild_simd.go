// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build goaac_simd

package simdbuild

// Enabled is true on the goaac_simd build: the SIMD kernels are compiled in
// place of their scalar counterparts.
const Enabled = true
