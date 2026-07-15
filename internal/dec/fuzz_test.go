// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"testing"

	"github.com/tphakala/go-aac/internal/bits"
)

// FuzzParseADTS asserts the ADTS header parser never panics and never
// accepts a buffer without the 12-bit syncword.
func FuzzParseADTS(f *testing.F) {
	f.Add([]byte{0xff, 0xf1, 0x4c, 0x80, 0x0d, 0x3f, 0xfc})
	f.Add([]byte{0xff, 0xf9, 0x50, 0x40, 0x01, 0x7f, 0xfc, 0xde, 0xad})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := ParseADTS(bits.NewReader(data))
		if err == nil {
			if len(data) < 2 || data[0] != 0xff || data[1]&0xf0 != 0xf0 {
				t.Fatal("accepted input without the 12-bit ADTS syncword")
			}
			if h.FrameLength < adtsHeaderSize {
				t.Fatalf("accepted frame length %d", h.FrameLength)
			}
			if h.SampleRate == 0 {
				t.Fatal("accepted zero sample rate")
			}
		}
	})
}

// FuzzParseASC asserts the AudioSpecificConfig parser never panics and
// only configures AAC-LC with 1..2 channels.
func FuzzParseASC(f *testing.F) {
	f.Add([]byte{0x11, 0x90})
	f.Add([]byte{0x12, 0x10})
	f.Add([]byte{0x2b, 0x92, 0x08, 0x00}) // explicit SBR wrapper
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := ParseASC(data)
		if err == nil {
			if c.ObjectType != 2 || c.ChanConfig < 1 || c.ChanConfig > 2 {
				t.Fatalf("accepted config %+v", c)
			}
		}
	})
}

// FuzzDecodeFrame asserts the ADTS frame decoder converts every malformed
// input into an error instead of panicking or reading out of bounds.
func FuzzDecodeFrame(f *testing.F) {
	f.Add([]byte{0xff, 0xf1, 0x4c, 0x80, 0x0d, 0x3f, 0xfc, 0x01, 0x18, 0x20, 0x07})
	f.Fuzz(func(t *testing.T, data []byte) {
		d := NewADTS()
		_ = d.DecodeFrame(data)
		// A second frame exercises the configured/mid-stream paths.
		_ = d.DecodeFrame(data)
	})
}
