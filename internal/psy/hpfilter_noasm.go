// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm

package psy

// HpFilterIsSIMD is false on the noasm build: psyHpFilter runs only its
// scalar loop.
const HpFilterIsSIMD = false

// hpFilterKernel reports false on the noasm build, so the caller runs the
// scalar loop.
func hpFilterKernel(firbuf []float32, hpfsmpl *[blockSizeLong]float32) bool {
	return false
}
