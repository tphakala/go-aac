// SPDX-License-Identifier: LGPL-2.1-or-later
package aac

import (
	"bytes"
	"testing"
)

func TestAppendADTSHeader(t *testing.T) {
	cases := []struct {
		srIdx, ch, payload int
		want               []byte
	}{
		// 48 kHz (index 3), mono, 100-byte payload -> frame length 107
		{3, 1, 100, []byte{0xff, 0xf1, 0x4c, 0x40, 0x0d, 0x7f, 0xfc}},
		// 44.1 kHz (index 4), stereo, 2048-byte payload -> frame length 2055
		{4, 2, 2048, []byte{0xff, 0xf1, 0x50, 0x81, 0x00, 0xff, 0xfc}},
	}
	for _, c := range cases {
		got := appendADTSHeader(nil, c.srIdx, c.ch, c.payload)
		if !bytes.Equal(got, c.want) {
			t.Errorf("adts(%d,%d,%d) = % x, want % x", c.srIdx, c.ch, c.payload, got, c.want)
		}
	}
}

func TestSampleRateIndex(t *testing.T) {
	if i, ok := sampleRateIndex(48000); !ok || i != 3 {
		t.Errorf("sampleRateIndex(48000) = %d,%v, want 3,true", i, ok)
	}
	if i, ok := sampleRateIndex(44100); !ok || i != 4 {
		t.Errorf("sampleRateIndex(44100) = %d,%v, want 4,true", i, ok)
	}
	if _, ok := sampleRateIndex(22222); ok {
		t.Error("sampleRateIndex(22222) ok = true, want false")
	}
}
