// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

import "github.com/tphakala/simd/cpu"

// bandCostKernelOK reports whether bandCostAsm runs on this CPU: it needs AVX2
// and POPCNT, and cpu.X86 already reflects SIMD_DISABLE when this package
// initializes. When false, quantizeAndEncodeBandCost runs its scalar loop.
var bandCostKernelOK = cpu.X86.AVX2 && cpu.X86.POPCNT

// bandCostMinSize is the narrowest band bandCostAsm takes.
const bandCostMinSize = 4
