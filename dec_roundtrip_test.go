// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"testing"

	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/dec"
)

// TestADTSRoundTrip parses every header the encoder's ADTS writer can emit
// and requires the fields back exactly (issue #12 round-trip gate).
func TestADTSRoundTrip(t *testing.T) {
	for _, rate := range []int{96000, 88200, 64000, 48000, 44100, 32000,
		24000, 22050, 16000, 12000, 11025, 8000, 7350} {
		for ch := 1; ch <= 2; ch++ {
			for _, payload := range []int{1, 200, 6144/8*2 - 7} {
				hdr, err := AppendADTSHeader(nil, rate, ch, payload)
				if err != nil {
					t.Fatalf("%d/%d: %v", rate, ch, err)
				}
				h, err := dec.ParseADTS(bits.NewReader(hdr))
				if err != nil {
					t.Fatalf("%d/%d: ParseADTS: %v", rate, ch, err)
				}
				srIdx, _ := sampleRateIndex(rate)
				if h.ObjectType != 2 || h.SampleRate != rate ||
					h.SamplingIndex != srIdx || h.ChanConfig != ch ||
					h.CRCAbsent != 1 || h.NumAACFrames != 1 ||
					h.FrameLength != payload+len(hdr) {
					t.Fatalf("%d/%d: fields %+v", rate, ch, h)
				}
			}
		}
	}
}

// TestASCRoundTrip parses every AudioSpecificConfig the encoder emits.
func TestASCRoundTrip(t *testing.T) {
	for _, rate := range []int{96000, 88200, 64000, 48000, 44100, 32000,
		24000, 22050, 16000, 12000, 11025, 8000, 7350} {
		for ch := 1; ch <= 2; ch++ {
			srIdx, ok := sampleRateIndex(rate)
			if !ok {
				t.Fatal("bad rate in test table")
			}
			asc := appendAudioSpecificConfig(nil, srIdx, ch)
			cfg, err := dec.ParseASC(asc)
			if err != nil {
				t.Fatalf("%d/%d: ParseASC: %v", rate, ch, err)
			}
			if cfg.ObjectType != 2 || cfg.SamplingIndex != srIdx ||
				cfg.SampleRate != rate || cfg.ChanConfig != ch ||
				cfg.SBR != -1 || cfg.FrameLenShort {
				t.Fatalf("%d/%d: config %+v", rate, ch, cfg)
			}
		}
	}
}
