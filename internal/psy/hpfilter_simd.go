// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package psy

// HpFilterIsSIMD is true on the default build, recording that this package
// compiled the SIMD high-pass filter dispatch (hpfilter_amd64.go,
// hpfilter_arm64.go, and the portable stub hpfilter_other.go on other
// architectures). The root package asserts it against aac.SIMDEnabled
// (simd_kernels_test.go).
const HpFilterIsSIMD = true
