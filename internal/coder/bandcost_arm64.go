// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

import "github.com/tphakala/simd/cpu"

// bandCostKernelOK reports whether bandCostAsm runs on this CPU: it needs
// NEON, and cpu.ARM64 already reflects SIMD_DISABLE when this package
// initializes. When false, quantizeAndEncodeBandCost runs its scalar loop.
var bandCostKernelOK = cpu.ARM64.NEON

// bandCostMinSize is the narrowest band bandCostAsm takes: NEON loses to the
// scalar loop on four-coefficient bands of the quad codebooks.
const bandCostMinSize = 8
