// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"testing"

	"github.com/tphakala/go-aac/internal/tables"
)

// newTonalSCE loads the C MDCT coefficients into a 44.1 kHz long-window SCE
// so the search comparison is isolated from MDCT rounding differences.
func newTonalSCE(t *testing.T) (*SingleChannelElement, *[128]PsyBand) {
	t.Helper()
	sce := &SingleChannelElement{}
	sce.ICS.NumWindows = 1
	sce.ICS.GroupLen[0] = 1
	sce.ICS.SwbSizes = tables.SwbSize1024[4]
	sce.ICS.SwbOffset = tables.SwbOffset1024[4]
	sce.ICS.NumSwb = int(tables.NumSwb1024[4])
	copy(sce.Coeffs[:], loadF32(t, "fast_in_coeffs.f32"))

	// Placeholder psy exactly as internal/enc computes it (cutoff 928).
	const cutoff = 928
	psy := &[128]PsyBand{}
	start := 0
	for g := range sce.ICS.NumSwb {
		size := int(sce.ICS.SwbSizes[g])
		var energy float32
		if start < cutoff {
			for _, v := range sce.Coeffs[start : start+size] {
				energy += v * v
			}
		}
		psy[g] = PsyBand{Energy: energy, Threshold: energy * 0.001258925}
		start += size
	}
	return sce, psy
}

func TestSearchForQuantizersFastMatchesC(t *testing.T) {
	sce, psy := newTonalSCE(t)
	var c Coder
	c.SearchForQuantizersFast(128000, 44100, 1, sce, psy, 120.0)

	wantSf := loadI32(t, "fast_sf.i32")
	wantBt := loadI32(t, "fast_bt.i32")
	wantZero := loadU8(t, "fast_zero.u8")
	mism := 0
	for i := range 128 {
		if int32(sce.SfIdx[i]) != wantSf[i] || int32(sce.BandType[i]) != wantBt[i] ||
			sce.Zeroes[i] != (wantZero[i] != 0) {
			mism++
			if mism <= 10 {
				t.Errorf("band %d: got sf=%d bt=%d z=%v, want sf=%d bt=%d z=%d",
					i, sce.SfIdx[i], sce.BandType[i], sce.Zeroes[i],
					wantSf[i], wantBt[i], wantZero[i])
			}
		}
	}
	if mism > 0 {
		t.Errorf("%d/128 band decisions differ from C", mism)
	}
}
