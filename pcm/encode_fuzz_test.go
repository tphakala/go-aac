// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"bytes"
	"testing"
)

// FuzzWriteChunking feeds arbitrary PCM bytes through Write under a
// fuzzer-chosen chunking and asserts the stream is byte-identical to the
// one-shot encode of the same bytes: the chunking contract, plus a
// no-panic guarantee over arbitrary input (every byte pattern is valid
// integer PCM).
func FuzzWriteChunking(f *testing.F) {
	f.Add([]byte{0, 0, 1, 0, 2, 0, 3, 0}, uint8(0), uint16(1))
	f.Add(bytes.Repeat([]byte{0xAA, 0xBB, 0xCC}, 700), uint8(4), uint16(7))
	f.Add(make([]byte, 3000), uint8(2), uint16(4096))
	f.Fuzz(func(t *testing.T, raw []byte, sel uint8, chunkSel uint16) {
		depth := []int{16, 24, 32}[int(sel)%3]
		channels := int(sel/3)%2 + 1
		cfg := Config{SampleRate: 48000, BitDepth: depth, Channels: channels, Bitrate: 96000}
		stride := depth / 8 * channels
		raw = raw[:len(raw)-len(raw)%stride] // one-shot demands whole samples
		chunk := int(chunkSel)%8192 + 1

		var ref bytes.Buffer
		if err := EncodeInterleaved(&ref, cfg, raw); err != nil {
			t.Fatalf("one-shot: %v", err)
		}
		var got bytes.Buffer
		enc, err := NewEncoder(&got, cfg)
		if err != nil {
			t.Fatal(err)
		}
		for off := 0; off < len(raw); off += chunk {
			end := min(off+chunk, len(raw))
			if _, err := enc.Write(raw[off:end]); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
		if err := enc.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if !bytes.Equal(ref.Bytes(), got.Bytes()) {
			t.Fatalf("chunk=%d depth=%d ch=%d: chunked stream (%d B) differs from one-shot (%d B)",
				chunk, depth, channels, got.Len(), ref.Len())
		}
	})
}
