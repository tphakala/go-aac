package pcm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
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

// TestDecodeInterleavedWithRawStreamMatchesStreaming covers the advertised
// option-bearing one-shot path. Raw AAC access units carry their configuration
// out of band and use a two-byte big-endian length prefix, so dropping either
// WithRawStream or its ASC while forwarding options would make this decode fail.
func TestDecodeInterleavedWithRawStreamMatchesStreaming(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, Bitrate: 96000}
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatalf("NewFrameEncoder: %v", err)
	}

	var raw bytes.Buffer
	emit := func(au []byte, _ int) error {
		if len(au) > int(^uint16(0)) {
			return errors.New("access unit exceeds raw-stream length prefix")
		}
		var prefix [2]byte
		binary.BigEndian.PutUint16(prefix[:], uint16(len(au)))
		raw.Write(prefix[:])
		raw.Write(au)
		return nil
	}
	if err := fe.EncodeInterleaved(genPCM16(4500, cfg.Channels), emit); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}
	if err := fe.Flush(emit); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if raw.Len() == 0 {
		t.Fatal("FrameEncoder emitted an empty raw stream")
	}
	asc := fe.AudioSpecificConfig()

	streaming, err := NewDecoder(bytes.NewReader(raw.Bytes()), WithRawStream(asc))
	if err != nil {
		t.Fatalf("NewDecoder(raw): %v", err)
	}
	var want bytes.Buffer
	if _, err := io.Copy(&want, streaming); err != nil {
		t.Fatalf("streaming raw decode: %v", err)
	}
	if want.Len() == 0 {
		t.Fatal("streaming raw decode returned no PCM")
	}

	got, info, err := DecodeInterleaved(bytes.NewReader(raw.Bytes()), WithRawStream(asc))
	if err != nil {
		t.Fatalf("DecodeInterleaved(raw): %v", err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("raw one-shot decode differs from streaming: got %d bytes, want %d", len(got), want.Len())
	}
	if info != streaming.Info() {
		t.Errorf("info = %+v, streaming info = %+v", info, streaming.Info())
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
