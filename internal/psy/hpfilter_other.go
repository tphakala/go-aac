// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm && !amd64 && !arm64

package psy

// hpFilterKernel reports false on architectures without a high-pass filter
// kernel, so the caller runs the scalar loop.
func hpFilterKernel(firbuf []float32, hpfsmpl *[blockSizeLong]float32) bool {
	return false
}
