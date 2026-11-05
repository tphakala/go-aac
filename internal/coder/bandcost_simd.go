// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

// BandCostIsSIMD is true on the default build, recording that this package
// compiled the SIMD band-cost dispatch (bandcost_amd64.go, bandcost_arm64.go,
// and the portable fallback bandcost_other.go on other architectures). The
// root package asserts it against aac.SIMDEnabled (simd_kernels_test.go).
const BandCostIsSIMD = true
