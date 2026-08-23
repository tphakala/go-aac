package pcm

import (
	"bytes"
	"errors"
	"testing"
)

// AAC is lossy, so a decode does not reproduce the source PCM. The invariant the
// one-shot must hold is that it returns exactly what the streaming decoder yields
// for the same stream.
func TestDecodeInterleavedMatchesStreaming(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	pcm := genPCM16(48000, cfg.Channels)
	stream := encodeOnce(t, cfg, pcm)

	want, err := decodeAll(t, stream)
	if err != nil {
		t.Fatalf("streaming decode: %v", err)
	}

	got, info, err := DecodeInterleaved(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("DecodeInterleaved: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("one-shot decode differs from streaming: got %d bytes, want %d", len(got), len(want))
	}
	if info.SampleRate != cfg.SampleRate || info.Channels != cfg.Channels {
		t.Errorf("info = %+v, want rate/ch %d/%d", info, cfg.SampleRate, cfg.Channels)
	}
}

// TestDecodeInterleavedLimit covers the ceiling: a limit below the output size
// fails with ErrDecodeLimit and returns no partial buffer, a limit at exactly the
// output size succeeds, and a non-positive limit is unbounded.
func TestDecodeInterleavedLimit(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	pcm := genPCM16(48000, cfg.Channels)
	stream := encodeOnce(t, cfg, pcm)

	want, err := decodeAll(t, stream)
	if err != nil {
		t.Fatalf("streaming decode: %v", err)
	}
	full := len(want)

	t.Run("below output fails", func(t *testing.T) {
		got, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), full-1)
		if !errors.Is(err, ErrDecodeLimit) {
			t.Fatalf("err = %v, want ErrDecodeLimit", err)
		}
		if got != nil {
			t.Errorf("got %d bytes back on limit error, want nil", len(got))
		}
	})

	t.Run("exact output succeeds", func(t *testing.T) {
		got, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), full)
		if err != nil {
			t.Fatalf("DecodeInterleavedLimit(exact): %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("exact-limit decode mismatch: got %d bytes, want %d", len(got), full)
		}
	})

	t.Run("non-positive is unbounded", func(t *testing.T) {
		got, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), 0)
		if err != nil {
			t.Fatalf("DecodeInterleavedLimit(0): %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("unbounded decode mismatch: got %d bytes, want %d", len(got), full)
		}
	})
}

// TestDecodeInterleavedBadStream checks non-AAC input is reported (not as a limit
// error) and never panics.
func TestDecodeInterleavedBadStream(t *testing.T) {
	got, _, err := DecodeInterleaved(bytes.NewReader([]byte("not an aac stream at all")))
	if err == nil {
		t.Fatal("DecodeInterleaved on garbage: want error, got nil")
	}
	if errors.Is(err, ErrDecodeLimit) {
		t.Errorf("garbage input reported as ErrDecodeLimit: %v", err)
	}
	if got != nil {
		t.Errorf("got %d bytes back on error, want nil", len(got))
	}
}
