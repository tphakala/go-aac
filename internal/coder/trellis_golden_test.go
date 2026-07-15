// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"bytes"
	"testing"

	gbits "github.com/tphakala/go-aac/internal/bits"
)

func TestCodebookTrellisRateMatchesC(t *testing.T) {
	sce, psy := newTonalSCE(t)
	var c Coder
	c.SearchForQuantizersFast(128000, 44100, 1, sce, psy, 120.0)

	// adjust_frame_information, mono long-window subset.
	maxsfb := sce.ICS.NumSwb
	for maxsfb > 0 && sce.Zeroes[maxsfb-1] {
		maxsfb--
	}
	sce.ICS.MaxSfb = maxsfb

	misc := loadI32(t, "trellis_misc.i32")
	if int32(maxsfb) != misc[0] {
		t.Fatalf("max_sfb %d, C had %d", maxsfb, misc[0])
	}

	pb := gbits.NewWriter(make([]byte, 0, 1536))
	c.CodebookTrellisRate(pb, sce, 0, 1, 120.0)
	if int32(pb.Count()) != misc[1] {
		t.Errorf("section bits %d, want %d", pb.Count(), misc[1])
	}
	got := pb.Flush()
	want := loadU8(t, "trellis_bits.bin")
	if !bytes.Equal(got, want) {
		t.Errorf("section bytes differ:\n got %x\nwant %x", got, want)
	}

	wantBt := loadI32(t, "trellis_bt.i32")
	wantZero := loadU8(t, "trellis_zero.u8")
	for i := range 128 {
		if int32(sce.BandType[i]) != wantBt[i] || sce.Zeroes[i] != (wantZero[i] != 0) {
			t.Errorf("band %d: got bt=%d z=%v, want bt=%d z=%d",
				i, sce.BandType[i], sce.Zeroes[i], wantBt[i], wantZero[i])
		}
	}
}
