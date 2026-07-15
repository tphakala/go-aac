// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"

	aac "github.com/tphakala/go-aac"
)

// genPCM16 makes n samples of deterministic 16-bit interleaved test audio.
func genPCM16(n, channels int) []byte {
	buf := make([]byte, n*channels*2)
	for i := range n {
		for c := range channels {
			ts := float64(i) / 48000
			v := 0.3*math.Sin(2*math.Pi*440*ts) + 0.15*math.Sin(2*math.Pi*(997+float64(c)*100)*ts)
			s := int16(v * 32767)
			binary.LittleEndian.PutUint16(buf[(i*channels+c)*2:], uint16(s))
		}
	}
	return buf
}

// widen16 converts 16-bit interleaved PCM to depth (24 or 32) by shifting
// left; the converted float values are bit-identical to the 16-bit path.
func widen16(pcm16 []byte, depth int) []byte {
	n := len(pcm16) / 2
	out := make([]byte, n*depth/8)
	for i := range n {
		v := int32(int16(binary.LittleEndian.Uint16(pcm16[i*2:])))
		switch depth {
		case 24:
			w := v << 8
			o := i * 3
			out[o], out[o+1], out[o+2] = byte(w), byte(w>>8), byte(w>>16)
		case 32:
			binary.LittleEndian.PutUint32(out[i*4:], uint32(v<<16))
		}
	}
	return out
}

func TestConfigValidate(t *testing.T) {
	good := Config{SampleRate: 48000, BitDepth: 16, Channels: 1}
	if err := good.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []Config{
		{SampleRate: 22050, BitDepth: 16, Channels: 1},
		{SampleRate: 0, BitDepth: 16, Channels: 1},
		{SampleRate: 48000, BitDepth: 8, Channels: 1},
		{SampleRate: 48000, BitDepth: 0, Channels: 1},
		{SampleRate: 48000, BitDepth: 16, Channels: 0},
		{SampleRate: 48000, BitDepth: 16, Channels: 3},
		{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: -1},
		{SampleRate: 48000, BitDepth: 16, Channels: 1, Cutoff: 24001},
		{SampleRate: 48000, BitDepth: 16, Channels: 1, Cutoff: -1},
	}
	for _, cfg := range bad {
		if _, err := NewEncoder(io.Discard, cfg); err == nil {
			t.Errorf("config %+v accepted, want error", cfg)
		}
	}
}

// TestBitDepthEquivalence proves the 24- and 32-bit conversion paths: a
// 16-bit stream widened losslessly to 24 or 32 bits must produce a
// byte-identical ADTS stream, because the scaled float32 values are
// mathematically identical (v/2^15 == (v<<8)/2^23 == (v<<16)/2^31).
func TestBitDepthEquivalence(t *testing.T) {
	for _, channels := range []int{1, 2} {
		pcm16 := genPCM16(48000*2+123, channels)
		var ref bytes.Buffer
		if err := EncodeInterleaved(&ref, Config{SampleRate: 48000, BitDepth: 16, Channels: channels, Bitrate: 128000}, pcm16); err != nil {
			t.Fatal(err)
		}
		for _, depth := range []int{24, 32} {
			var got bytes.Buffer
			err := EncodeInterleaved(&got, Config{SampleRate: 48000, BitDepth: depth, Channels: channels, Bitrate: 128000}, widen16(pcm16, depth))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(ref.Bytes(), got.Bytes()) {
				t.Errorf("ch=%d depth=%d: stream differs from 16-bit reference (%d vs %d bytes)",
					channels, depth, got.Len(), ref.Len())
			}
		}
	}
}

// TestWriteChunkingByteIdentical is the Write chunking contract: the same
// PCM fed in any chunking, including chunks that split samples and frames,
// must produce a byte-identical stream, and the streaming result must
// equal the one-shot result.
func TestWriteChunkingByteIdentical(t *testing.T) {
	for _, tc := range []struct {
		depth, channels int
	}{
		{16, 1}, {24, 2}, {32, 2},
	} {
		pcm16 := genPCM16(48000+777, tc.channels)
		pcm := pcm16
		if tc.depth != 16 {
			pcm = widen16(pcm16, tc.depth)
		}
		cfg := Config{SampleRate: 48000, BitDepth: tc.depth, Channels: tc.channels, Bitrate: 96000}

		var ref bytes.Buffer
		if err := EncodeInterleaved(&ref, cfg, pcm); err != nil {
			t.Fatal(err)
		}

		for _, chunk := range []int{1, 3, 7, 1024, 4096, 7919, 32768} {
			var got bytes.Buffer
			enc, err := NewEncoder(&got, cfg)
			if err != nil {
				t.Fatal(err)
			}
			for off := 0; off < len(pcm); off += chunk {
				end := min(off+chunk, len(pcm))
				n, err := enc.Write(pcm[off:end])
				if err != nil {
					t.Fatal(err)
				}
				if n != end-off {
					t.Fatalf("Write returned %d, want %d", n, end-off)
				}
			}
			if err := enc.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(ref.Bytes(), got.Bytes()) {
				t.Errorf("depth=%d ch=%d chunk=%d: stream differs from one-shot (%d vs %d bytes)",
					tc.depth, tc.channels, chunk, got.Len(), ref.Len())
			}
		}
	}
}

func TestWriteAfterClose(t *testing.T) {
	enc, err := NewEncoder(io.Discard, Config{SampleRate: 48000, BitDepth: 16, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil { // idempotent
		t.Fatalf("second Close: %v", err)
	}
	if _, err := enc.Write([]byte{0, 0}); !errors.Is(err, aac.ErrEncoderClosed) {
		t.Fatalf("Write after Close: %v, want ErrEncoderClosed", err)
	}
}

func TestCloseTrailingPartialSample(t *testing.T) {
	enc, err := NewEncoder(io.Discard, Config{SampleRate: 48000, BitDepth: 24, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(make([]byte, 6*10+1)); err != nil { // 10 samples + 1 stray byte
		t.Fatal(err)
	}
	if err := enc.Close(); err == nil {
		t.Fatal("Close accepted a trailing partial sample")
	}
}

// TestEmptyStreamClose: Close with no input must still produce a valid,
// non-empty stream (one all-silence frame), so downstream tools can open
// the file (mirrors the go-opus empty-clip finding).
func TestEmptyStreamClose(t *testing.T) {
	var buf bytes.Buffer
	enc, err := NewEncoder(&buf, Config{SampleRate: 48000, BitDepth: 16, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty-input Close produced no output")
	}
	if buf.Bytes()[0] != 0xFF || buf.Bytes()[1]&0xF0 != 0xF0 {
		t.Fatalf("output does not start with an ADTS sync word: % x", buf.Bytes()[:2])
	}
}

// TestResetByteIdentity: an encoder reused via Reset must produce exactly
// the bytes a fresh encoder produces, for every config axis that changes
// between clips.
func TestResetByteIdentity(t *testing.T) {
	cfgs := []Config{
		{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000},
		{SampleRate: 44100, BitDepth: 24, Channels: 2, Bitrate: 128000},
		{SampleRate: 48000, BitDepth: 32, Channels: 2, Bitrate: 192000, Cutoff: 14000},
	}
	var reused *Encoder
	for i, cfg := range cfgs {
		pcm16 := genPCM16(48000+i*333, cfg.Channels)
		pcm := pcm16
		if cfg.BitDepth != 16 {
			pcm = widen16(pcm16, cfg.BitDepth)
		}
		var fresh bytes.Buffer
		fe, err := NewEncoder(&fresh, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fe.Write(pcm); err != nil {
			t.Fatal(err)
		}
		if err := fe.Close(); err != nil {
			t.Fatal(err)
		}

		var pooled bytes.Buffer
		if reused == nil {
			reused, err = NewEncoder(&pooled, cfg)
		} else {
			err = reused.Reset(&pooled, cfg)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reused.Write(pcm); err != nil {
			t.Fatal(err)
		}
		if err := reused.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fresh.Bytes(), pooled.Bytes()) {
			t.Errorf("cfg %d: reused encoder differs from fresh (%d vs %d bytes)", i, pooled.Len(), fresh.Len())
		}
	}
}

// TestEncodeInterleavedPartialSample: a buffer with a trailing partial
// sample errors before any sink write.
func TestEncodeInterleavedPartialSample(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeInterleaved(&buf, Config{SampleRate: 48000, BitDepth: 24, Channels: 2}, make([]byte, 6001))
	if err == nil {
		t.Fatal("partial sample accepted")
	}
	if buf.Len() != 0 {
		t.Fatalf("sink saw %d bytes before the error", buf.Len())
	}
}

// TestAudioSpecificConfigFreshCopy: mutating a returned ASC must not
// affect later calls.
func TestAudioSpecificConfigFreshCopy(t *testing.T) {
	enc, err := NewEncoder(io.Discard, Config{SampleRate: 48000, BitDepth: 16, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	a := enc.AudioSpecificConfig()
	want := bytes.Clone(a)
	a[0] = 0xEE
	if b := enc.AudioSpecificConfig(); !bytes.Equal(b, want) {
		t.Fatalf("ASC not a fresh copy: % x after mutation, want % x", b, want)
	}
	// AAC-LC, 48 kHz (index 3), mono: 5-bit AOT=2, 4-bit idx=3, 4-bit ch=1.
	if !bytes.Equal(want, []byte{0x11, 0x88}) {
		t.Fatalf("ASC = % x, want 11 88", want)
	}
}

// TestStats: after a real encode the counters must be coherent.
func TestStats(t *testing.T) {
	var buf bytes.Buffer
	enc, err := NewEncoder(&buf, Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(genPCM16(48000, 2)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	st := enc.Stats()
	if st.Frames == 0 || st.ChannelFrames != 2*st.Frames || st.Bands == 0 || st.MeanLambda <= 0 {
		t.Fatalf("incoherent stats: %+v", st)
	}
	t.Logf("stats: %v", st)
}

// failAfter is a sink that fails after accepting n writes.
type failAfter struct {
	n   int
	err error
}

func (f *failAfter) Write(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, f.err
	}
	f.n--
	return len(p), nil
}

// TestWriteSinkErrorByteCount: on a mid-write sink error, Write reports
// the bytes of p durably consumed, per the io.Writer contract.
func TestWriteSinkErrorByteCount(t *testing.T) {
	sinkErr := errors.New("sink full")
	enc, err := NewEncoder(&failAfter{n: 2, err: sinkErr}, Config{SampleRate: 48000, BitDepth: 16, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 8 frames. The priming frame emits no access unit, so the sink (failAfter
	// n=2) errors a few frames in; Write must report exactly the whole-frame
	// byte count durably consumed before that error.
	pcm := genPCM16(1024*8, 1)
	n, err := enc.Write(pcm)
	if !errors.Is(err, sinkErr) {
		t.Fatalf("err = %v, want sink error", err)
	}
	if n <= 0 || n >= len(pcm) || n%2048 != 0 {
		t.Fatalf("consumed %d bytes of %d, want a positive whole-frame count", n, len(pcm))
	}
	t.Logf("consumed %d of %d bytes before sink error", n, len(pcm))
}

// TestZeroValueEncoderErrorsNoPanic covers the misuse where a caller builds an
// Encoder directly (enc is nil) instead of via NewEncoder. The API must error
// rather than nil-deref.
func TestZeroValueEncoderErrorsNoPanic(t *testing.T) {
	var e Encoder
	if n, err := e.Write([]byte{0, 0, 0, 0}); n != 0 || !errors.Is(err, aac.ErrEncoderClosed) {
		t.Fatalf("zero-value Write = (%d, %v), want (0, ErrEncoderClosed)", n, err)
	}
	if err := e.Close(); !errors.Is(err, aac.ErrEncoderClosed) {
		t.Fatalf("zero-value Close = %v, want ErrEncoderClosed", err)
	}
	if asc := e.AudioSpecificConfig(); asc != nil {
		t.Fatalf("zero-value AudioSpecificConfig = % x, want nil", asc)
	}
	_ = e.Stats() // must not panic on a zero-value encoder
}

// TestCloseErrorIsSticky checks that a failure during the final flush is
// reported on every Close call, not masked by a nil on retry.
func TestCloseErrorIsSticky(t *testing.T) {
	sinkErr := errors.New("sink full")
	// n=0: the sink fails on the first access unit the drain writes.
	enc, err := NewEncoder(&failAfter{err: sinkErr}, Config{SampleRate: 48000, BitDepth: 16, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first := enc.Close(); !errors.Is(first, sinkErr) {
		t.Fatalf("first Close = %v, want sink error", first)
	}
	if second := enc.Close(); !errors.Is(second, sinkErr) {
		t.Fatalf("second Close = %v, want the same sink error, not a masked nil", second)
	}
}

// TestWriteFailureIsTerminal covers a mid-stream encode failure: encodeFrameBytes
// advances the codec state before writeAU can fail, so the failure cannot be
// recovered by re-feeding PCM. Write must latch the error and every later Write
// and Close must return it without re-encoding against the advanced state.
func TestWriteFailureIsTerminal(t *testing.T) {
	sinkErr := errors.New("sink full")
	// n=1: the priming frame emits no access unit, so sink write 1 is the
	// first real frame (succeeds) and sink write 2 is the carry top-off (fails).
	enc, err := NewEncoder(&failAfter{n: 1, err: sinkErr}, Config{SampleRate: 48000, BitDepth: 16, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	frame := genPCM16(1024, 1) // one full frame, 2048 bytes
	half := len(frame) / 2

	if _, err := enc.Write(frame); err != nil { // frame 1: priming, no sink write
		t.Fatalf("write frame 1: %v", err)
	}
	if _, err := enc.Write(frame); err != nil { // frame 2: sink write 1 succeeds
		t.Fatalf("write frame 2: %v", err)
	}
	if n, err := enc.Write(frame[:half]); err != nil || n != half { // stash partial
		t.Fatalf("partial write = (%d, %v), want (%d, nil)", n, err, half)
	}
	// Top off the carry: encodes frame 3, sink write 2 fails.
	if n, err := enc.Write(frame[:half]); n != 0 || !errors.Is(err, sinkErr) {
		t.Fatalf("top-off write = (%d, %v), want (0, sink error)", n, err)
	}
	// A retry must return the latched error and consume nothing, never
	// re-encode against the advanced codec state.
	if n, err := enc.Write(frame); n != 0 || !errors.Is(err, sinkErr) {
		t.Fatalf("retry write = (%d, %v), want (0, latched sink error)", n, err)
	}
	// Close reports the same terminal error.
	if err := enc.Close(); !errors.Is(err, sinkErr) {
		t.Fatalf("Close = %v, want the latched sink error", err)
	}
}
