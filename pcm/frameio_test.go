// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	aac "github.com/tphakala/go-aac"
)

// collectAUs runs pcm through a FrameEncoder in chunks of chunkSize bytes
// (0 meaning one call with the whole buffer), flushes, and returns a copy
// of every emitted access unit together with the reported sample counts.
func collectAUs(t *testing.T, cfg Config, pcm []byte, chunkSize int) (aus [][]byte, samples []int) {
	t.Helper()
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatalf("NewFrameEncoder: %v", err)
	}
	emit := func(au []byte, n int) error {
		aus = append(aus, bytes.Clone(au))
		samples = append(samples, n)
		return nil
	}
	if chunkSize <= 0 {
		if err := fe.EncodeInterleaved(pcm, emit); err != nil {
			t.Fatalf("EncodeInterleaved: %v", err)
		}
	} else {
		for off := 0; off < len(pcm); off += chunkSize {
			end := min(off+chunkSize, len(pcm))
			if err := fe.EncodeInterleaved(pcm[off:end], emit); err != nil {
				t.Fatalf("EncodeInterleaved at %d: %v", off, err)
			}
		}
	}
	if err := fe.Flush(emit); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return aus, samples
}

// splitADTS splits a self-framing ADTS stream into its raw access units,
// using each header's 13-bit frame length field. It deliberately parses the
// header by hand rather than calling the package's own decoder: it is the
// independent oracle the encoder is checked against, so routing it through
// production parsing code would let one bug hide another.
func splitADTS(t *testing.T, stream []byte) [][]byte {
	t.Helper()
	var aus [][]byte
	for off := 0; off < len(stream); {
		if len(stream)-off < adtsHeaderLen {
			t.Fatalf("truncated ADTS header at %d", off)
		}
		h := stream[off : off+adtsHeaderLen]
		if h[0] != 0xff || h[1]&0xf0 != 0xf0 {
			t.Fatalf("no ADTS syncword at %d", off)
		}
		frameLen := int(h[3]&0x03)<<11 | int(h[4])<<3 | int(h[5])>>5
		if frameLen < adtsHeaderLen || off+frameLen > len(stream) {
			t.Fatalf("bad ADTS frame length %d at %d", frameLen, off)
		}
		aus = append(aus, stream[off+adtsHeaderLen:off+frameLen])
		off += frameLen
	}
	return aus
}

// goldenAUs pins the access units the encoder produces for genPCM16(4500)
// under each Config. The digests were generated from the PRE-refactor encoder
// (pcm.Encoder on the commit before FrameEncoder existed, with the ADTS
// headers stripped), so they are an anchor external to the code under test:
// they fail on any change to the integer-to-float conversion, the framing, the
// carry, the priming or the drain, none of which
// TestFrameEncoderMatchesADTSStream can see now that both encoders share one
// implementation. Regenerate only when a bitstream change is intended, and say
// so in the commit message.
var goldenAUs = []struct {
	name   string
	cfg    Config
	units  int
	sha256 string
}{
	{"16bit_mono_48k", Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}, 6,
		"ac38780e64a00040c7f88e205c531730c2d4da8f9e2300775727d158da91f92a"},
	{"16bit_stereo_44k1", Config{SampleRate: 44100, BitDepth: 16, Channels: 2, Bitrate: 128000}, 6,
		"1e7b0be739e4d71b2238051a99d3719c72e536c9abdda1dde291ff2173636016"},
	{"24bit_stereo_48k", Config{SampleRate: 48000, BitDepth: 24, Channels: 2, Bitrate: 128000}, 6,
		"eb9cafa57dd19cdb058337653b3705c428f271c44425e99b4cbed9fdcbb558d3"},
	{"32bit_mono_48k", Config{SampleRate: 48000, BitDepth: 32, Channels: 1, Bitrate: 64000}, 6,
		"757252b2b4ba751ae21f28deb354f160ac1ed459dc707c784314eb937d437d3e"},
	// Exercises Cutoff and a non-default Coder, so the whole Config forwards.
	{"16bit_stereo_48k_cutoff_fast", Config{SampleRate: 48000, BitDepth: 16, Channels: 2,
		Bitrate: 128000, Cutoff: 12000, Coder: aac.CoderFast}, 6,
		"98e075a654f06eacb9c4c1cbb46369bf5e166c14368e8d93e9de61be9397f436"},
}

// testPCM builds the standard test signal at cfg's bit depth.
func testPCM(samples int, cfg Config) []byte {
	pcm := genPCM16(samples, cfg.Channels)
	if cfg.BitDepth != 16 {
		pcm = widen16(pcm, cfg.BitDepth)
	}
	return pcm
}

// TestFrameEncoderGoldenAccessUnits is the fidelity anchor for the muxing
// path. Unlike the ADTS comparison, it does not re-run the implementation to
// produce its own expectation; it compares against bytes captured from the
// encoder as it behaved before FrameEncoder was extracted.
func TestFrameEncoderGoldenAccessUnits(t *testing.T) {
	for _, tc := range goldenAUs {
		t.Run(tc.name, func(t *testing.T) {
			aus, _ := collectAUs(t, tc.cfg, testPCM(4500, tc.cfg), 0)
			if len(aus) != tc.units {
				t.Fatalf("emitted %d access units, want %d", len(aus), tc.units)
			}
			h := sha256.New()
			for _, au := range aus {
				h.Write(au)
			}
			if got := hex.EncodeToString(h.Sum(nil)); got != tc.sha256 {
				t.Errorf("access unit digest %s, want %s (the encoded bitstream changed)", got, tc.sha256)
			}
		})
	}
}

// TestFrameEncoderMatchesADTSStream pins the ADTS wrapper: the access units a
// FrameEncoder emits must equal the payloads of the stream Encoder writes for
// the same input.
//
// Since the refactor made Encoder a wrapper over FrameEncoder, both sides run
// one implementation, so this canNOT catch a wrong conversion, carry, priming
// or drain (all of which it would once have caught, and which
// TestFrameEncoderGoldenAccessUnits now covers). What it does prove is that
// Encoder adds exactly a 7-byte header and nothing else, that the header's
// frame-length field is right, and that the two entry points stay in step if
// Encoder ever grows its own pipeline again.
func TestFrameEncoderMatchesADTSStream(t *testing.T) {
	for _, tc := range goldenAUs {
		t.Run(tc.name, func(t *testing.T) {
			pcm := testPCM(4500, tc.cfg)

			var buf bytes.Buffer
			enc, err := NewEncoder(&buf, tc.cfg)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if _, err := enc.Write(pcm); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if err := enc.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			want := splitADTS(t, buf.Bytes())

			got, _ := collectAUs(t, tc.cfg, pcm, 0)
			if len(got) != len(want) {
				t.Fatalf("emitted %d access units, ADTS stream carries %d", len(got), len(want))
			}
			for i := range want {
				if !bytes.Equal(got[i], want[i]) {
					t.Fatalf("access unit %d differs: %d raw bytes vs %d ADTS payload bytes", i, len(got[i]), len(want[i]))
				}
			}
			if len(got) == 0 {
				t.Fatal("no access units emitted")
			}
		})
	}
}

// TestFrameEncoderChunkingInvariance pins the carry-buffer contract: the
// emitted sequence depends only on the byte sequence, never on how it was
// split across EncodeInterleaved calls, including chunks that do not land
// on an inter-channel sample boundary.
func TestFrameEncoderChunkingInvariance(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 24, Channels: 2, Bitrate: 128000}
	pcm := testPCM(4500, cfg)

	want, wantN := collectAUs(t, cfg, pcm, 0)
	for _, chunk := range []int{1, 7, 7919, 32768} {
		got, gotN := collectAUs(t, cfg, pcm, chunk)
		if len(got) != len(want) {
			t.Fatalf("chunk %d: %d access units, want %d", chunk, len(got), len(want))
		}
		for i := range want {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("chunk %d: access unit %d differs", chunk, i)
			}
			if gotN[i] != wantN[i] {
				t.Fatalf("chunk %d: access unit %d reports %d samples, want %d", chunk, i, gotN[i], wantN[i])
			}
		}
	}
}

// TestFrameEncoderShortStreams covers the frame-boundary cases a muxer hits on
// very short clips: less than one frame, and exactly one frame. Both must
// match the same input through Encoder.
func TestFrameEncoderShortStreams(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	for _, samples := range []int{1, 500, aac.FrameSize, aac.FrameSize + 1} {
		pcm := testPCM(samples, cfg)

		var buf bytes.Buffer
		enc, err := NewEncoder(&buf, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enc.Write(pcm); err != nil {
			t.Fatal(err)
		}
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		want := splitADTS(t, buf.Bytes())

		got, _ := collectAUs(t, cfg, pcm, 0)
		if len(got) != len(want) {
			t.Fatalf("%d samples: %d access units, ADTS stream carries %d", samples, len(got), len(want))
		}
		if len(got) == 0 {
			t.Fatalf("%d samples: no access units emitted; a short clip must still produce a track", samples)
		}
		for i := range want {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("%d samples: access unit %d differs", samples, i)
			}
		}
	}
}

// TestFrameEncoderSamplesAlwaysFrameSize checks the reported per-unit count.
// It reads back the value the encoder passes to emit; the claim that the unit
// really decodes to that many samples is covered by
// TestFrameEncoderRoundTrip.
func TestFrameEncoderSamplesAlwaysFrameSize(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	_, samples := collectAUs(t, cfg, genPCM16(2500, 1), 0)
	if len(samples) == 0 {
		t.Fatal("no access units emitted")
	}
	for i, n := range samples {
		if n != aac.FrameSize {
			t.Errorf("access unit %d reports %d samples, want %d", i, n, aac.FrameSize)
		}
	}
}

// TestFrameEncoderEmitBorrowsSlice enforces the documented borrow rule from
// both sides: the encoder hands back the same backing array on every emit
// after the first, and the bytes a caller retains really are overwritten by
// the next one. A muxer must copy or append.
func TestFrameEncoderEmitBorrowsSlice(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var retained, copied []byte
	var emits, reuse, clobbered int
	emit := func(au []byte, _ int) error {
		emits++
		if retained != nil {
			if &au[0] == &retained[0] {
				reuse++
			}
			// The previous emit's slice header still points into the scratch,
			// so its contents must no longer match the copy taken back then.
			if !bytes.Equal(retained, copied) {
				clobbered++
			}
		}
		retained, copied = au, bytes.Clone(au)
		return nil
	}
	if err := fe.EncodeInterleaved(genPCM16(8192, 1), emit); err != nil {
		t.Fatal(err)
	}
	if err := fe.Flush(emit); err != nil {
		t.Fatal(err)
	}
	if emits < 3 {
		t.Fatalf("only %d emits; the test needs several to observe reuse", emits)
	}
	// Every emit after the first must alias the same buffer. A weaker
	// "at least one" bound would pass an implementation that reallocates
	// sometimes, which is exactly the regression this guards.
	if reuse != emits-1 {
		t.Errorf("%d of %d follow-up emits reused the scratch buffer, want all of them", reuse, emits-1)
	}
	if clobbered == 0 {
		t.Error("a retained access unit was never overwritten; the borrow contract is not actually being exercised")
	}
}

// TestFrameEncoderAudioSpecificConfig checks the esds DecoderSpecificInfo
// payload: two bytes for AAC-LC, identical to what the Encoder reports, and a
// fresh copy on every call so a muxer may retain or mutate it.
func TestFrameEncoderAudioSpecificConfig(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, Bitrate: 128000}
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	asc := fe.AudioSpecificConfig()
	if len(asc) != 2 {
		t.Fatalf("ASC is %d bytes, want 2", len(asc))
	}
	original := bytes.Clone(asc)

	enc, err := NewEncoder(io.Discard, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := enc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	if want := enc.AudioSpecificConfig(); !bytes.Equal(asc, want) {
		t.Errorf("ASC %x differs from Encoder's %x", asc, want)
	}

	// Mutating a returned copy must not affect the next call, and the next
	// call must still return the real config (not nil or a truncation).
	asc[0] ^= 0xff
	if again := fe.AudioSpecificConfig(); !bytes.Equal(again, original) {
		t.Errorf("AudioSpecificConfig = %x after a caller mutated an earlier copy, want %x", again, original)
	}

	// A zero-value FrameEncoder has no stream to describe.
	var zero FrameEncoder
	if got := zero.AudioSpecificConfig(); got != nil {
		t.Errorf("zero-value AudioSpecificConfig = %x, want nil", got)
	}
}

// TestFrameEncoderDelay pins the priming count a muxer puts in the edit list
// media_time. It asserts the literal rather than restating the constant the
// implementation returns; TestFrameEncoderRoundTrip ties the number to the
// encoder's actual behaviour.
func TestFrameEncoderDelay(t *testing.T) {
	fe, err := NewFrameEncoder(Config{SampleRate: 48000, BitDepth: 16, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := fe.Delay(); got != 1024 {
		t.Errorf("Delay = %d, want 1024", got)
	}
	enc, err := NewEncoder(io.Discard, Config{SampleRate: 48000, BitDepth: 16, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := enc.Delay(); got != fe.Delay() {
		t.Errorf("Encoder.Delay = %d, FrameEncoder.Delay = %d; the two must agree", got, fe.Delay())
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestFrameEncoderStats checks that tool-usage counters accumulate over a
// stream and that Reset clears them, as the method documents.
func TestFrameEncoderStats(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := fe.Stats().Frames; got != 0 {
		t.Errorf("Stats().Frames = %d before encoding, want 0", got)
	}
	emit := func([]byte, int) error { return nil }
	if err := fe.EncodeInterleaved(genPCM16(4096, 1), emit); err != nil {
		t.Fatal(err)
	}
	if err := fe.Flush(emit); err != nil {
		t.Fatal(err)
	}
	if got := fe.Stats().Frames; got == 0 {
		t.Error("Stats().Frames = 0 after encoding a stream, want a positive count")
	}
	if err := fe.Reset(cfg); err != nil {
		t.Fatal(err)
	}
	if got := fe.Stats().Frames; got != 0 {
		t.Errorf("Stats().Frames = %d after Reset, want 0", got)
	}
	var zero FrameEncoder
	if got := zero.Stats(); got != (aac.Stats{}) {
		t.Errorf("zero-value Stats = %+v, want the zero value", got)
	}
}

// TestFrameEncoderEmitError checks that an emit failure aborts the encode,
// surfaces unchanged and latches: every later call returns it until Reset,
// and no further access units reach the callback.
func TestFrameEncoderEmitError(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	sentinel := errors.New("muxer full")
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	failing := func([]byte, int) error {
		calls++
		return sentinel
	}
	err = fe.EncodeInterleaved(genPCM16(8192, 1), failing)
	if !errors.Is(err, sentinel) {
		t.Fatalf("EncodeInterleaved = %v, want the emit error", err)
	}
	if calls != 1 {
		t.Fatalf("emit called %d times after failing, want 1", calls)
	}
	if err := fe.EncodeInterleaved(genPCM16(4096, 1), failing); !errors.Is(err, sentinel) {
		t.Errorf("later EncodeInterleaved = %v, want the latched error", err)
	}
	if err := fe.Flush(failing); !errors.Is(err, sentinel) {
		t.Errorf("Flush = %v, want the latched error", err)
	}
	if calls != 1 {
		t.Errorf("emit called %d times in total, want 1", calls)
	}
	// Reset re-arms the encoder after a latched error.
	if err := fe.Reset(cfg); err != nil {
		t.Fatalf("Reset after error: %v", err)
	}
	if err := fe.EncodeInterleaved(genPCM16(4096, 1), func([]byte, int) error { return nil }); err != nil {
		t.Errorf("EncodeInterleaved after Reset: %v", err)
	}
}

// TestFrameEncoderFlushErrorIsLatched checks the failure half of the
// idempotency contract on both flush paths: the trailing partial frame and
// the drain. When emit fails, a second Flush reports the same error rather
// than a success, and does not emit again.
func TestFrameEncoderFlushErrorIsLatched(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	cases := []struct {
		name    string
		samples int
	}{
		// 2500 leaves a partial frame buffered, so the failing emit lands on
		// the trailing-frame encode inside finish.
		{"partial_frame", 2500},
		// 2048 is a whole number of frames, so the carry is empty and the
		// failing emit lands on the drain instead.
		{"drain", 2048},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sentinel := errors.New("segment buffer full")
			fe, err := NewFrameEncoder(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := fe.EncodeInterleaved(genPCM16(tc.samples, 1), func([]byte, int) error { return nil }); err != nil {
				t.Fatal(err)
			}
			calls := 0
			failing := func([]byte, int) error {
				calls++
				return sentinel
			}
			if err := fe.Flush(failing); !errors.Is(err, sentinel) {
				t.Fatalf("Flush = %v, want the emit error", err)
			}
			if err := fe.Flush(failing); !errors.Is(err, sentinel) {
				t.Errorf("second Flush = %v, want the same latched error", err)
			}
			if calls != 1 {
				t.Errorf("emit called %d times, want 1", calls)
			}
		})
	}
}

// TestFrameEncoderEmptyInput checks that a zero-length call is a no-op:
// no access unit, no state change, no panic. io.Copy and ring-buffer
// drains hand out empty slices routinely.
func TestFrameEncoderEmptyInput(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	count := func([]byte, int) error { n++; return nil }
	for _, empty := range [][]byte{nil, {}} {
		if err := fe.EncodeInterleaved(empty, count); err != nil {
			t.Fatalf("EncodeInterleaved(empty) = %v, want nil", err)
		}
	}
	if n != 0 {
		t.Errorf("empty input emitted %d access units, want 0", n)
	}
	// State is untouched: a following full encode still matches a fresh
	// encoder's output.
	pcm := genPCM16(3000, cfg.Channels)
	var got [][]byte
	emit := func(au []byte, _ int) error { got = append(got, bytes.Clone(au)); return nil }
	if err := fe.EncodeInterleaved(pcm, emit); err != nil {
		t.Fatal(err)
	}
	if err := fe.Flush(emit); err != nil {
		t.Fatal(err)
	}
	want, _ := collectAUs(t, cfg, pcm, 0)
	if len(got) != len(want) {
		t.Fatalf("%d access units after empty calls, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("access unit %d differs after an empty call", i)
		}
	}
}

// TestFrameEncoderFlush covers the terminal-and-idempotent contract, and
// that a FrameEncoder which saw no input at all still flushes one
// all-silence access unit, exactly as Encoder.Close leaves a decodable
// stream.
func TestFrameEncoderFlush(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	count := func([]byte, int) error { n++; return nil }
	if err := fe.Flush(count); err != nil {
		t.Fatalf("Flush on an empty encoder: %v", err)
	}
	if n != 1 {
		t.Errorf("empty-input Flush emitted %d access units, want 1", n)
	}
	if err := fe.Flush(count); err != nil {
		t.Errorf("second Flush = %v, want nil (idempotent)", err)
	}
	if n != 1 {
		t.Errorf("second Flush emitted %d more access units, want 0", n-1)
	}
	if err := fe.EncodeInterleaved(genPCM16(1024, 1), count); !errors.Is(err, aac.ErrEncoderClosed) {
		t.Errorf("EncodeInterleaved after Flush = %v, want ErrEncoderClosed", err)
	}
}

// TestFrameEncoderNilEmitIsRecoverable checks that passing a nil callback is
// reported without destroying the stream. Both entry points treat the misuse
// the same way, so a caller whose callback is nil on one path does not lose
// the buffered tail (up to two access units) with no way back.
func TestFrameEncoderNilEmitIsRecoverable(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := fe.EncodeInterleaved(genPCM16(2048, 1), nil); err == nil {
		t.Error("EncodeInterleaved accepted a nil emit callback")
	}
	n := 0
	count := func([]byte, int) error { n++; return nil }
	// The encoder survived the nil callback and still accepts audio.
	if err := fe.EncodeInterleaved(genPCM16(2048, 1), count); err != nil {
		t.Fatalf("EncodeInterleaved after a nil callback: %v", err)
	}
	if err := fe.Flush(nil); err == nil {
		t.Error("Flush accepted a nil emit callback")
	}
	// The tail is still there: a real Flush must deliver it, not report the
	// stream as already ended.
	if err := fe.Flush(count); err != nil {
		t.Errorf("Flush after a nil callback = %v, want nil", err)
	}
	if n == 0 {
		t.Error("no access units emitted; a nil callback destroyed the buffered tail")
	}
}

// TestFrameEncoderTrailingPartialSample rejects a flush with buffered bytes
// that are not a whole number of inter-channel samples, matching
// Encoder.Close. It runs at several strides, since the modulus check is
// exactly where an odd sample width would show an off-by-one.
func TestFrameEncoderTrailingPartialSample(t *testing.T) {
	for _, cfg := range []Config{
		{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}, // stride 4
		{SampleRate: 48000, BitDepth: 24, Channels: 2, Bitrate: 128000}, // stride 6
		{SampleRate: 48000, BitDepth: 24, Channels: 1, Bitrate: 96000},  // stride 3
		{SampleRate: 48000, BitDepth: 32, Channels: 1, Bitrate: 96000},  // stride 4
	} {
		fe, err := NewFrameEncoder(cfg)
		if err != nil {
			t.Fatal(err)
		}
		pcm := testPCM(100, cfg)
		if err := fe.EncodeInterleaved(pcm[:len(pcm)-1], func([]byte, int) error { return nil }); err != nil {
			t.Fatal(err)
		}
		err = fe.Flush(func([]byte, int) error { return nil })
		if err == nil {
			t.Errorf("%d-bit %dch: Flush with a partial trailing sample succeeded, want an error",
				cfg.BitDepth, cfg.Channels)
		} else if !strings.Contains(err.Error(), "not a whole number") {
			t.Errorf("%d-bit %dch: Flush = %v, want the trailing-partial-sample error",
				cfg.BitDepth, cfg.Channels, err)
		}
	}
}

// TestFrameEncoderReset checks the pooling path: a reset encoder produces
// byte-identical output to a freshly constructed one, including across a
// change of Config.
func TestFrameEncoderReset(t *testing.T) {
	first := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	second := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, Bitrate: 128000}
	pcm1 := genPCM16(3000, first.Channels)
	pcm2 := genPCM16(3000, second.Channels)

	want1, _ := collectAUs(t, first, pcm1, 0)
	want2, _ := collectAUs(t, second, pcm2, 0)

	fe, err := NewFrameEncoder(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cfg  Config
		pcm  []byte
		want [][]byte
	}{{first, pcm1, want1}, {second, pcm2, want2}, {first, pcm1, want1}} {
		if err := fe.Reset(tc.cfg); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		var got [][]byte
		emit := func(au []byte, _ int) error { got = append(got, bytes.Clone(au)); return nil }
		if err := fe.EncodeInterleaved(tc.pcm, emit); err != nil {
			t.Fatal(err)
		}
		if err := fe.Flush(emit); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("after Reset: %d access units, want %d", len(got), len(tc.want))
		}
		for i := range tc.want {
			if !bytes.Equal(got[i], tc.want[i]) {
				t.Fatalf("after Reset: access unit %d differs from a fresh encoder's", i)
			}
		}
	}
}

// TestFrameEncoderResetAfterFlush covers the pooling loop directly:
// Reset must clear the terminal flushed state, not just the codec state,
// so the encoder accepts audio again.
func TestFrameEncoderResetAfterFlush(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	emit := func([]byte, int) error { return nil }
	if err := fe.EncodeInterleaved(genPCM16(2048, 1), emit); err != nil {
		t.Fatal(err)
	}
	if err := fe.Flush(emit); err != nil {
		t.Fatal(err)
	}
	if err := fe.Reset(cfg); err != nil {
		t.Fatalf("Reset after Flush: %v", err)
	}
	if err := fe.EncodeInterleaved(genPCM16(2048, 1), emit); err != nil {
		t.Errorf("EncodeInterleaved after Reset = %v, want nil", err)
	}
	if err := fe.Flush(emit); err != nil {
		t.Errorf("Flush after Reset = %v, want nil", err)
	}
}

// TestFrameEncoderFailedResetIsUnusable pins the state a rejected Reset
// leaves behind. The previous stream's codec state is still allocated (the
// next Reset reuses it), so the encoder must not go on reporting that
// stream: an esds built from a stale AudioSpecificConfig would describe the
// wrong sample rate and channel count for every sample in the track, with no
// error anywhere.
func TestFrameEncoderFailedResetIsUnusable(t *testing.T) {
	good := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	bad := Config{SampleRate: 22050, BitDepth: 16, Channels: 1}
	emit := func([]byte, int) error { return nil }

	for _, armed := range []bool{false, true} {
		fe, err := NewFrameEncoder(good)
		if err != nil {
			t.Fatal(err)
		}
		if armed {
			// Encode a real stream first, so enc is non-nil and holds the
			// previous configuration when the next Reset is rejected.
			if err := fe.EncodeInterleaved(genPCM16(2048, 1), emit); err != nil {
				t.Fatal(err)
			}
		}
		if err := fe.Reset(bad); err == nil {
			t.Fatal("Reset accepted an unsupported sample rate")
		}
		if err := fe.EncodeInterleaved(genPCM16(2048, 1), emit); !errors.Is(err, aac.ErrEncoderClosed) {
			t.Errorf("armed=%v: EncodeInterleaved after a failed Reset = %v, want ErrEncoderClosed", armed, err)
		}
		if err := fe.Flush(emit); !errors.Is(err, aac.ErrEncoderClosed) {
			t.Errorf("armed=%v: Flush after a failed Reset = %v, want ErrEncoderClosed", armed, err)
		}
		if got := fe.AudioSpecificConfig(); got != nil {
			t.Errorf("armed=%v: AudioSpecificConfig after a failed Reset = %x, want nil", armed, got)
		}
		if got := fe.Stats(); got != (aac.Stats{}) {
			t.Errorf("armed=%v: Stats after a failed Reset = %+v, want the zero value", armed, got)
		}
		// A later successful Reset re-arms it.
		if err := fe.Reset(good); err != nil {
			t.Fatalf("armed=%v: Reset after a failed Reset: %v", armed, err)
		}
		if err := fe.EncodeInterleaved(genPCM16(2048, 1), emit); err != nil {
			t.Errorf("armed=%v: EncodeInterleaved after re-arming: %v", armed, err)
		}
	}
}

// TestEncoderFailedResetIsUnusable is the Encoder half of the same contract.
// Close must not report success on an encoder that was never configured: the
// stream it claims to have finalized was never written.
func TestEncoderFailedResetIsUnusable(t *testing.T) {
	good := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	bad := Config{SampleRate: 22050, BitDepth: 16, Channels: 1}

	cases := []struct {
		name string
		fail func(e *Encoder) error
	}{
		{"nil_writer", func(e *Encoder) error { return e.Reset(nil, good) }},
		{"bad_config", func(e *Encoder) error { return e.Reset(io.Discard, bad) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e Encoder
			// Arm it against a real sink first, so the failed Reset below has a
			// previous stream to detach from.
			var prior bytes.Buffer
			if err := e.Reset(&prior, good); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Write(genPCM16(2048, good.Channels)); err != nil {
				t.Fatal(err)
			}
			if err := tc.fail(&e); err == nil {
				t.Fatal("Reset accepted an invalid argument")
			}
			if _, err := e.Write(genPCM16(2048, 1)); !errors.Is(err, aac.ErrEncoderClosed) {
				t.Errorf("Write after a failed Reset = %v, want ErrEncoderClosed", err)
			}
			if err := e.Close(); !errors.Is(err, aac.ErrEncoderClosed) {
				t.Errorf("Close after a failed Reset = %v, want ErrEncoderClosed", err)
			}
			if got := e.AudioSpecificConfig(); got != nil {
				t.Errorf("AudioSpecificConfig after a failed Reset = %x, want nil", got)
			}
			if e.w != nil {
				t.Error("a failed Reset left the previous sink attached; it stays pinned until the next success")
			}
			// The encoder is still reusable once a Reset succeeds.
			var buf bytes.Buffer
			if err := e.Reset(&buf, good); err != nil {
				t.Fatalf("Reset after a failed Reset: %v", err)
			}
			if _, err := e.Write(genPCM16(2048, 1)); err != nil {
				t.Fatal(err)
			}
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			if buf.Len() == 0 {
				t.Error("no ADTS bytes written after re-arming")
			}
		})
	}
}

// TestFrameEncoderRejects covers the remaining guard rails: an invalid Config
// at construction and a zero-value encoder.
func TestFrameEncoderRejects(t *testing.T) {
	if _, err := NewFrameEncoder(Config{SampleRate: 22050, BitDepth: 16, Channels: 1}); err == nil {
		t.Error("NewFrameEncoder accepted an unsupported sample rate")
	}
	var zero FrameEncoder
	emit := func([]byte, int) error { return nil }
	if err := zero.EncodeInterleaved(genPCM16(2048, 1), emit); !errors.Is(err, aac.ErrEncoderClosed) {
		t.Errorf("zero-value EncodeInterleaved = %v, want ErrEncoderClosed", err)
	}
	if err := zero.Flush(emit); !errors.Is(err, aac.ErrEncoderClosed) {
		t.Errorf("zero-value Flush = %v, want ErrEncoderClosed", err)
	}
	if got := zero.Delay(); got != aac.EncoderDelay {
		t.Errorf("zero-value Delay = %d, want %d", got, aac.EncoderDelay)
	}
}

// TestFrameEncoderRoundTrip decodes the emitted access units and compares the
// result to the input, which is what makes the muxing path an audio test
// rather than a plumbing test. It checks three independent properties: the
// stream length a muxer derives its edit list from, waveform fidelity
// (correlation, which catches a misaligned drain or a wrong priming count),
// and amplitude (which catches a mis-scaled conversion; correlation cannot,
// being invariant under any linear gain).
func TestFrameEncoderRoundTrip(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	const samples = 4500
	in := genPCM16(samples, cfg.Channels)

	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var stream []byte
	emit := func(au []byte, _ int) error {
		stream, err = aac.AppendADTSHeader(stream, cfg.SampleRate, cfg.Channels, len(au))
		if err != nil {
			return err
		}
		stream = append(stream, au...)
		return nil
	}
	if err := fe.EncodeInterleaved(in, emit); err != nil {
		t.Fatal(err)
	}
	if err := fe.Flush(emit); err != nil {
		t.Fatal(err)
	}

	d, err := NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Length: the input rounded up to a whole frame, plus the one frame of
	// priming the encoder delays output by.
	gotSamples := len(out) / 2 / cfg.Channels
	want := ((samples+aac.FrameSize-1)/aac.FrameSize)*aac.FrameSize + aac.FrameSize
	if gotSamples != want {
		t.Errorf("decoded %d samples per channel, want %d", gotSamples, want)
	}

	// Fidelity: drop the priming samples the encoder added and check the
	// decoded audio tracks the input. A correlation this high cannot survive a
	// misaligned drain or a wrong priming count.
	r, gain := compare(t, in, out, fe.Delay(), cfg.Channels)
	if r < 0.99 {
		t.Errorf("decoded audio correlates %.4f with the input, want >= 0.99", r)
	}
	// Amplitude: the RMS ratio pins the integer-to-float conversion scale,
	// which the correlation above is blind to. A codec at this bitrate tracks
	// the input level closely; a scale error is a factor of two or more.
	if gain < 0.8 || gain > 1.25 {
		t.Errorf("decoded audio is %.3fx the input level, want ~1.0 (the conversion scale is wrong)", gain)
	}
}

// compare returns the Pearson correlation between the interleaved s16 input
// and the decoded s16 output, and the ratio of their RMS levels, after
// skipping delay priming samples per channel. The two are deliberately
// separate: correlation measures waveform shape and is invariant under gain,
// so it cannot see a conversion-scale error, which is exactly what the RMS
// ratio catches.
func compare(t *testing.T, in, out []byte, delay, channels int) (r, gain float64) {
	t.Helper()
	skip := delay * channels * 2
	if len(out) < skip {
		t.Fatalf("decoded %d bytes, fewer than the %d priming bytes", len(out), skip)
	}
	out = out[skip:]
	n := min(len(in), len(out)) / 2
	if n == 0 {
		t.Fatal("no overlapping samples to compare")
	}
	var sx, sy, sxx, syy, sxy float64
	for i := range n {
		x := float64(int16(binary.LittleEndian.Uint16(in[i*2:])))
		y := float64(int16(binary.LittleEndian.Uint16(out[i*2:])))
		sx, sy = sx+x, sy+y
		sxx, syy, sxy = sxx+x*x, syy+y*y, sxy+x*y
	}
	fn := float64(n)
	den := math.Sqrt((fn*sxx - sx*sx) * (fn*syy - sy*sy))
	if den == 0 || sxx == 0 {
		t.Fatal("degenerate signal; correlation undefined")
	}
	return (fn*sxy - sx*sy) / den, math.Sqrt(syy / sxx)
}

// TestEncoderReleaseDropsCallerState pins the teardown the pooled
// EncodeInterleaved relies on. An encoder goes back into the pool carrying
// whatever the caller's stream left behind, so release must drop the sink and
// the latched error (which wraps the caller's io.Writer error and can retain
// arbitrary caller state through it), and must leave the encoder unable to
// write to the released sink.
func TestEncoderReleaseDropsCallerState(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	sink := &failingWriter{after: 1}
	e, err := NewEncoder(sink, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Drive it until the sink fails, so an error is latched.
	if _, err := e.Write(genPCM16(8192, cfg.Channels)); err == nil {
		t.Fatal("Write succeeded against a failing sink, want an error")
	}
	if e.fe.err == nil {
		t.Fatal("no error latched; the test cannot observe release clearing one")
	}

	e.release()

	if e.w != nil {
		t.Error("release left the caller's sink attached")
	}
	if e.fe.err != nil {
		t.Errorf("release left a latched error attached: %v", e.fe.err)
	}
	if _, err := e.Write(genPCM16(2048, cfg.Channels)); !errors.Is(err, aac.ErrEncoderClosed) {
		t.Errorf("Write after release = %v, want ErrEncoderClosed", err)
	}
	// A Reset re-arms it for the next caller, which is what the pool relies on.
	var buf bytes.Buffer
	if err := e.Reset(&buf, cfg); err != nil {
		t.Fatalf("Reset after release: %v", err)
	}
	if _, err := e.Write(genPCM16(4096, cfg.Channels)); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Error("no ADTS bytes written after Reset; the released encoder did not recover")
	}
}

// failingWriter fails every Write after the first `after` succeed.
type failingWriter struct {
	after int
	n     int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.n++
	if w.n > w.after {
		return 0, errors.New("sink failed")
	}
	return len(p), nil
}

// TestFrameEncoderSteadyStateAllocs is the allocation gate for the muxing
// path: a warmed FrameEncoder must emit access units without allocating,
// since the live use is one segment every couple of seconds per stream on
// an ARM SBC. It runs with a callback that copies the borrowed unit into a
// pre-sized segment buffer, the way a real muxer does, so the gate covers the
// borrow contract rather than only an empty callback.
func TestFrameEncoderSteadyStateAllocs(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pcm := genPCM16(8192, cfg.Channels)
	chunk := pcm[:7919]
	segment := make([]byte, 0, 1<<20) // pre-sized, as a segment buffer would be
	emit := func(au []byte, _ int) error {
		segment = append(segment, au...)
		if len(segment) > 1<<19 { // recycle, so the buffer never has to grow
			segment = segment[:0]
		}
		return nil
	}
	for range 8 { // warm up all growth paths
		if err := fe.EncodeInterleaved(chunk, emit); err != nil {
			t.Fatal(err)
		}
	}
	allocs := testing.AllocsPerRun(50, func() {
		if err := fe.EncodeInterleaved(chunk, emit); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("%.2f allocs/EncodeInterleaved", allocs)
	if allocs > 0 {
		t.Errorf("steady-state EncodeInterleaved allocates %.2f/op, want 0", allocs)
	}
}
