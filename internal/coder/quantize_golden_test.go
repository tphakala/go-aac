// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"bytes"
	"encoding/binary"
	"testing"

	gbits "github.com/tphakala/go-aac/internal/bits"
)

func TestQuantizeAndEncodeBandMatchesC(t *testing.T) {
	raw := loadU8(t, "qeb_bytes.bin")
	amps := [12]float32{0, 1.5, 1.5, 2.5, 2.5, 4.5, 4.5, 7.5, 7.5, 12.5, 12.5, 500.0}
	state := uint32(0x1f2e3d4c)
	var c Coder
	off := 0
	for cb := 1; cb <= 11; cb++ {
		in := make([]float32, 32)
		for i := range in {
			state = state*1664525 + 1013904223
			in[i] = amps[cb] * float32(int32(state)) / 2147483648.0
		}
		nbits := int32(binary.LittleEndian.Uint32(raw[off:]))
		nbytes := int32(binary.LittleEndian.Uint32(raw[off+4:]))
		want := raw[off+8 : off+8+int(nbytes)]
		off += 8 + int(nbytes)

		pb := gbits.NewWriter(make([]byte, 0, 512))
		c.QuantizeAndEncodeBand(pb, in, ScaleOnePos, cb, 120.0, false)
		if int32(pb.Count()) != nbits {
			t.Errorf("cb %d: bits %d, want %d", cb, pb.Count(), nbits)
		}
		got := pb.Flush()
		if !bytes.Equal(got, want) {
			t.Errorf("cb %d: bytes differ\n got %x\nwant %x", cb, got, want)
		}
	}
}
