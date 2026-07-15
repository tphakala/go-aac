// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-aac/internal/dec"
)

// decodeAll decodes an in-memory stream fully, returning the PCM and the
// terminal error (nil on clean EOF).
func decodeAll(t *testing.T, data []byte) ([]byte, error) {
	t.Helper()
	d, err := NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	_, werr := d.WriteTo(&out)
	return out.Bytes(), werr
}

// lastFrameStart returns the byte offset of the last complete ADTS frame in
// data, using the exported header parser to walk frame lengths.
func lastFrameStart(t *testing.T, data []byte) int {
	t.Helper()
	last, pos := -1, 0
	for pos+dec.ADTSHeaderSize <= len(data) {
		h, err := dec.ParseADTSHeaderBytes(data[pos:])
		if err != nil || pos+h.FrameLength > len(data) {
			break
		}
		last = pos
		pos += h.FrameLength
	}
	if last < 0 {
		t.Fatal("no complete frame found")
	}
	return last
}

// loadStream reads a committed ADTS corpus stream.
func loadStream(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(decoderTestdata, name+".adts"))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return data
}

// TestGarbageBeforeSyncword prepends non-sync bytes and verifies the decoder
// resyncs to identical output, matching the oracle (which also resyncs).
func TestGarbageBeforeSyncword(t *testing.T) {
	data := loadStream(t, streamMono)
	clean, err := decodeAll(t, data)
	if err != nil {
		t.Fatalf("clean decode: %v", err)
	}
	// 5 bytes that cannot begin an ADTS syncword.
	garbled := append([]byte{0x00, 0x11, 0x22, 0x33, 0x44}, data...)
	got, err := decodeAll(t, garbled)
	if err != nil {
		t.Fatalf("garbled decode: %v", err)
	}
	if !bytes.Equal(got, clean) {
		t.Fatalf("resync output differs: got %d bytes, clean %d bytes", len(got), len(clean))
	}
	t.Logf("garbage-prefix resync: %d bytes, identical to clean decode", len(got))
}

// TestGarbageBetweenFrames injects garbage between two frames and verifies the
// decoder resyncs to the next frame.
func TestGarbageBetweenFrames(t *testing.T) {
	data := loadStream(t, streamMono)
	split := lastFrameStart(t, data)
	// Insert 3 non-sync bytes before the last frame.
	garbled := make([]byte, 0, len(data)+3)
	garbled = append(garbled, data[:split]...)
	garbled = append(garbled, 0x00, 0x01, 0x02)
	garbled = append(garbled, data[split:]...)

	clean, err := decodeAll(t, data)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	got, err := decodeAll(t, garbled)
	if err != nil {
		t.Fatalf("garbled: %v", err)
	}
	if !bytes.Equal(got, clean) {
		t.Fatalf("mid-stream resync differs: got %d, clean %d", len(got), len(clean))
	}
	t.Logf("mid-stream garbage resync: %d bytes, identical to clean decode", len(got))
}

// TestTruncatedFinalFramePayload cuts into the last frame's payload. Expected:
// every complete frame decodes, then the truncated frame yields
// ErrCorruptStream; the emitted bytes equal a clean decode minus one frame,
// matching the oracle's complete-frame output.
func TestTruncatedFinalFramePayload(t *testing.T) {
	for _, name := range []string{streamMono, streamStereo} {
		t.Run(name, func(t *testing.T) {
			data := loadStream(t, name)
			clean, err := decodeAll(t, data)
			if err != nil {
				t.Fatalf("clean: %v", err)
			}
			last := lastFrameStart(t, data)
			h, err := dec.ParseADTSHeaderBytes(data[last:])
			if err != nil {
				t.Fatal(err)
			}
			// Keep the header and part of the payload, drop the tail.
			cut := last + dec.ADTSHeaderSize + (h.FrameLength-dec.ADTSHeaderSize)/2
			trunc := data[:cut]

			got, gerr := decodeAll(t, trunc)
			d, _ := NewDecoder(bytes.NewReader(data))
			frameBytes := 1024 * 2 * d.Info().Channels
			wantBytes := len(clean) - frameBytes
			if !errors.Is(gerr, ErrCorruptStream) {
				t.Fatalf("want ErrCorruptStream, got %v", gerr)
			}
			if len(got) != wantBytes {
				t.Fatalf("emitted %d bytes, want %d (clean minus one frame)", len(got), wantBytes)
			}
			if !bytes.Equal(got, clean[:wantBytes]) {
				t.Fatal("emitted prefix differs from clean decode")
			}
			t.Logf("truncated payload: emitted %d bytes (%d complete frames), then %v",
				len(got), len(got)/frameBytes, gerr)
		})
	}
}

// TestTruncatedFinalHeader cuts the stream to leave fewer than a full header
// of the final frame. It must not panic and must surface truncation while
// preserving all complete frames.
func TestTruncatedFinalHeader(t *testing.T) {
	data := loadStream(t, streamMono)
	last := lastFrameStart(t, data)
	// Keep only 3 bytes of the final frame's header.
	trunc := data[:last+3]
	got, gerr := decodeAll(t, trunc)
	t.Logf("truncated final header: emitted %d bytes, terminal err = %v", len(got), gerr)
	// The truncation begins at a real syncword, so this is deterministically a
	// corrupt stream, never a clean end.
	if !errors.Is(gerr, ErrCorruptStream) {
		t.Fatalf("want ErrCorruptStream for a truncated final header, got %v", gerr)
	}
}

// TestNoDecodableFrame feeds pure garbage: NewDecoder must return a
// ErrCorruptStream, never panic and never a bare io.EOF.
func TestNoDecodableFrame(t *testing.T) {
	_, err := NewDecoder(bytes.NewReader(make([]byte, 4096)))
	if !errors.Is(err, ErrCorruptStream) {
		t.Fatalf("want ErrCorruptStream for garbage-only input, got %v", err)
	}
	t.Logf("garbage-only NewDecoder error: %v", err)
}

// TestMidStreamConfigChange concatenates two streams of different config
// (mono 8 kHz then stereo 48 kHz). The decoder reports the first config in
// Info, delivers every frame of that first config, then returns a clean
// ErrUnsupported at the boundary. This is the documented v1 deviation from the
// C decoder (which re-inits and keeps decoding): a single interleaved s16le
// sink cannot represent a mid-stream channel or rate change.
func TestMidStreamConfigChange(t *testing.T) {
	first := loadStream(t, streamMono)
	second := loadStream(t, streamStereo)
	firstClean, err := decodeAll(t, first)
	if err != nil {
		t.Fatalf("first clean decode: %v", err)
	}

	joined := append(bytes.Clone(first), second...)
	d, err := NewDecoder(bytes.NewReader(joined))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if got := d.Info(); got.Channels != 1 || got.SampleRate != 8000 {
		t.Fatalf("Info = %+v, want mono 8000 (the first config)", got)
	}
	var out bytes.Buffer
	_, werr := d.WriteTo(&out)
	if !errors.Is(werr, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported at the config boundary, got %v", werr)
	}
	if !bytes.Equal(out.Bytes(), firstClean) {
		t.Fatalf("delivered %d bytes before the boundary, want the first config's %d",
			out.Len(), len(firstClean))
	}
	t.Logf("mid-stream config change: delivered %d bytes then %v", out.Len(), werr)
}
