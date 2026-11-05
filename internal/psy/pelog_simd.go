// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package psy

// PeLogIsSIMD is true on the default build, recording that this package
// compiled the SIMD logarithm dispatch for calcPE3gpp (pelog_arm64.go, and the
// portable stub pelog_other.go on amd64 and other architectures). The root
// package asserts it against aac.SIMDEnabled (simd_kernels_test.go).
const PeLogIsSIMD = true
