// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm

package coder

// BandCostIsSIMD is false on the noasm build: quantizeAndEncodeBandCost runs
// only its scalar loop.
const BandCostIsSIMD = false
