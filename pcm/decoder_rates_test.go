// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// rateMatrixCase pins one decoder-corpus fixture to the sample rate and channel
// count its ADTS header declares. The byte-exact decode of each is already
// covered by TestDecodeStreamHermetic / TestDecodeStreamVsOracle; this table
// asserts the public contract separately: NewDecoder reports the parsed rate
// and channel count for the full AAC-LC low-rate range, not just 44.1/48 kHz.
type rateMatrixCase struct {
	file     string
	rate     int
	channels int
}

// The 8/11.025/12/16/22.05/24/32 kHz LC matrix, mono and stereo. sine_m8_24k
// (8 kHz mono) and is_s22_48k_fast (22.05 kHz stereo) predate this table; the
// tone_* fixtures fill the remaining cells (see testdata/decoder/PROVENANCE.md).
var rateMatrix = []rateMatrixCase{
	{"sine_m8_24k.adts", 8000, 1},
	{"tone_s8000.adts", 8000, 2},
	{"tone_m11025.adts", 11025, 1},
	{"tone_s11025.adts", 11025, 2},
	{"tone_m12000.adts", 12000, 1},
	{"tone_s12000.adts", 12000, 2},
	{"tone_m16000.adts", 16000, 1},
	{"tone_s16000.adts", 16000, 2},
	{"tone_m22050.adts", 22050, 1},
	{"is_s22_48k_fast.adts", 22050, 2},
	{"tone_m24000.adts", 24000, 1},
	{"tone_s24000.adts", 24000, 2},
	{"tone_m32000.adts", 32000, 1},
	{"tone_s32000.adts", 32000, 2},
}

// TestDecodeRateMatrix asserts the decoder accepts and correctly reports every
// AAC-LC rate in the MPEG-4 low-rate range (issue #85). The README documents
// 44.1/48 kHz only, but the ADTS/ASC parser and the rate-indexed SWB tables
// cover the whole 13-entry sample-rate table; this proves the decode contract
// for the rates camera and stream sources actually emit (16 kHz, 32 kHz, ...).
func TestDecodeRateMatrix(t *testing.T) {
	for _, tc := range rateMatrix {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join(decoderTestdata, tc.file)
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()

			d, err := NewDecoder(f)
			if err != nil {
				t.Fatalf("NewDecoder(%s): %v", tc.file, err)
			}
			info := d.Info()
			if info.SampleRate != tc.rate {
				t.Errorf("SampleRate = %d, want %d", info.SampleRate, tc.rate)
			}
			if info.Channels != tc.channels {
				t.Errorf("Channels = %d, want %d", info.Channels, tc.channels)
			}
			if info.Profile != profileAACLC {
				t.Errorf("Profile = %q, want %q", info.Profile, profileAACLC)
			}

			// Decode must run clean and yield whole interleaved S16 frames.
			var buf bytes.Buffer
			if _, err := d.WriteTo(&buf); err != nil {
				t.Fatalf("WriteTo(%s): %v", tc.file, err)
			}
			frame := 2 * tc.channels // bytes per interleaved S16 sample
			if buf.Len() == 0 || buf.Len()%frame != 0 {
				t.Fatalf("decoded %d bytes, not a whole multiple of %d", buf.Len(), frame)
			}
		})
	}
}
