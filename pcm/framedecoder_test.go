// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	aac "github.com/tphakala/go-aac"
)

// splitADTSFrames splits a self-framing ADTS stream into complete access units
// (7-byte header plus payload), using each header's 13-bit frame length field.
// It differs from splitADTS, which strips the header: an ADTS FrameDecoder
// consumes the whole self-describing frame, so the test feeds it the header
// too. Like splitADTS it parses the header by hand, so it stays an independent
// oracle rather than routing through the production parser it checks.
func splitADTSFrames(t *testing.T, stream []byte) [][]byte {
	t.Helper()
	var frames [][]byte
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
		frames = append(frames, stream[off:off+frameLen])
		off += frameLen
	}
	return frames
}

// encodeADTS encodes pcm through the streaming Encoder and returns the ADTS
// bytes, the vehicle for the frame-vs-reader equivalence tests.
func encodeADTS(t *testing.T, cfg Config, pcm []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := NewEncoder(&buf, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

// lengthPrefix builds the 2-byte big-endian length-prefixed raw stream that
// WithRawStream consumes, from a sequence of bare access units.
func lengthPrefix(aus [][]byte) []byte {
	var out []byte
	var hdr [2]byte
	for _, au := range aus {
		binary.BigEndian.PutUint16(hdr[:], uint16(len(au)))
		out = append(out, hdr[:]...)
		out = append(out, au...)
	}
	return out
}

// frameDecoderConfigs is the config matrix shared by the equivalence tests: the
// mono/stereo, sample-rate and bit-depth combinations a transport hands off.
var frameDecoderConfigs = []struct {
	name string
	cfg  Config
}{
	{"16bit_mono_48k", Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}},
	{"16bit_stereo_44k1", Config{SampleRate: 44100, BitDepth: 16, Channels: 2, Bitrate: 128000}},
	{"16bit_stereo_48k", Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}},
	{"24bit_stereo_48k", Config{SampleRate: 48000, BitDepth: 24, Channels: 2, Bitrate: 128000}},
}

// TestFrameDecoderADTSMatchesReader is the core acceptance test for the ADTS
// path: decoding an ADTS stream access unit by access unit through
// FrameDecoder produces byte-identical PCM to the io.Reader-based Decoder over
// the same input. Both routes call the same internal AppendS16, so any
// divergence is a bug in the frame wrapper's framing or state handling.
func TestFrameDecoderADTSMatchesReader(t *testing.T) {
	for _, tc := range frameDecoderConfigs {
		t.Run(tc.name, func(t *testing.T) {
			pcm := testPCM(4500, tc.cfg)
			stream := encodeADTS(t, tc.cfg, pcm)

			ref, err := io.ReadAll(mustDecoder(t, bytes.NewReader(stream)))
			if err != nil {
				t.Fatalf("reference decode: %v", err)
			}
			if len(ref) == 0 {
				t.Fatal("reference decode produced no PCM; the equivalence check would be vacuous")
			}

			d := NewADTSDecoder()
			var got []byte
			for i, frame := range splitADTSFrames(t, stream) {
				var n int
				got, n, err = d.DecodeFrame(got, frame)
				if err != nil {
					t.Fatalf("DecodeFrame %d: %v", i, err)
				}
				if n != aac.FrameSize {
					t.Errorf("DecodeFrame %d reported %d samples, want %d", i, n, aac.FrameSize)
				}
			}
			if !bytes.Equal(got, ref) {
				t.Fatalf("frame decode produced %d bytes, reader decode %d; not byte-identical", len(got), len(ref))
			}
		})
	}
}

// TestFrameDecoderRawMatchesReader is the raw-stream counterpart: bare access
// units described by an AudioSpecificConfig decode identically through
// FrameDecoder and through the reader Decoder configured WithRawStream over the
// same units. This is the RTSP/HLS shape, where the ASC arrives out of band.
func TestFrameDecoderRawMatchesReader(t *testing.T) {
	for _, tc := range frameDecoderConfigs {
		t.Run(tc.name, func(t *testing.T) {
			pcm := testPCM(4500, tc.cfg)
			aus, _ := collectAUs(t, tc.cfg, pcm, 0)

			fe, err := NewFrameEncoder(tc.cfg)
			if err != nil {
				t.Fatalf("NewFrameEncoder: %v", err)
			}
			asc := fe.AudioSpecificConfig()

			ref, err := io.ReadAll(mustDecoder(t, bytes.NewReader(lengthPrefix(aus)), WithRawStream(asc)))
			if err != nil {
				t.Fatalf("reference decode: %v", err)
			}
			if len(ref) == 0 {
				t.Fatal("reference decode produced no PCM; the equivalence check would be vacuous")
			}

			d, err := NewRawDecoder(asc)
			if err != nil {
				t.Fatalf("NewRawDecoder: %v", err)
			}
			var got []byte
			for i, au := range aus {
				var n int
				got, n, err = d.DecodeFrame(got, au)
				if err != nil {
					t.Fatalf("DecodeFrame %d: %v", i, err)
				}
				if n != aac.FrameSize {
					t.Errorf("DecodeFrame %d reported %d samples, want %d", i, n, aac.FrameSize)
				}
			}
			if !bytes.Equal(got, ref) {
				t.Fatalf("frame decode produced %d bytes, reader decode %d; not byte-identical", len(got), len(ref))
			}
		})
	}
}

// mustDecoder builds a Decoder or fails the test.
func mustDecoder(t *testing.T, r io.Reader, opts ...Option) *Decoder {
	t.Helper()
	d, err := NewDecoder(r, opts...)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	return d
}

// TestFrameDecoderSampleRateChannels checks the accessors. A raw decoder knows
// its config at construction; an ADTS decoder learns it from the first frame,
// so it reports zero until then.
func TestFrameDecoderSampleRateChannels(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, Bitrate: 128000}

	adts := NewADTSDecoder()
	if got := adts.SampleRate(); got != 0 {
		t.Errorf("ADTS SampleRate before first frame = %d, want 0", got)
	}
	if got := adts.Channels(); got != 0 {
		t.Errorf("ADTS Channels before first frame = %d, want 0", got)
	}
	stream := encodeADTS(t, cfg, testPCM(2500, cfg))
	if _, _, err := adts.DecodeFrame(nil, splitADTSFrames(t, stream)[0]); err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if got := adts.SampleRate(); got != cfg.SampleRate {
		t.Errorf("ADTS SampleRate = %d, want %d", got, cfg.SampleRate)
	}
	if got := adts.Channels(); got != cfg.Channels {
		t.Errorf("ADTS Channels = %d, want %d", got, cfg.Channels)
	}

	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := NewRawDecoder(fe.AudioSpecificConfig())
	if err != nil {
		t.Fatalf("NewRawDecoder: %v", err)
	}
	if got := raw.SampleRate(); got != cfg.SampleRate {
		t.Errorf("raw SampleRate = %d, want %d (known at construction)", got, cfg.SampleRate)
	}
	if got := raw.Channels(); got != cfg.Channels {
		t.Errorf("raw Channels = %d, want %d (known at construction)", got, cfg.Channels)
	}
}

// TestFrameDecoderReset checks that Reset re-arms a decoder for a fresh session
// under the same config, producing byte-identical output to a fresh decoder,
// on both the ADTS and raw paths.
func TestFrameDecoderReset(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	pcm := testPCM(4500, cfg)

	t.Run("adts", func(t *testing.T) {
		frames := splitADTSFrames(t, encodeADTS(t, cfg, pcm))
		decodeAll := func() []byte {
			d := NewADTSDecoder()
			return decodeFrames(t, d, frames)
		}
		want := decodeAll()

		d := NewADTSDecoder()
		first := decodeFrames(t, d, frames)
		if !bytes.Equal(first, want) {
			t.Fatal("first pass differs from a fresh decoder")
		}
		if err := d.Reset(); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		// ADTS config is relearned per stream, so Reset returns the accessors to
		// their pre-first-frame zero state until the next frame is decoded.
		if sr, ch := d.SampleRate(), d.Channels(); sr != 0 || ch != 0 {
			t.Errorf("after Reset SampleRate=%d Channels=%d, want 0/0 before the next frame", sr, ch)
		}
		if second := decodeFrames(t, d, frames); !bytes.Equal(second, want) {
			t.Fatal("post-Reset pass differs from a fresh decoder")
		}
	})

	t.Run("raw", func(t *testing.T) {
		aus, _ := collectAUs(t, cfg, pcm, 0)
		fe, err := NewFrameEncoder(cfg)
		if err != nil {
			t.Fatal(err)
		}
		asc := fe.AudioSpecificConfig()

		fresh, err := NewRawDecoder(asc)
		if err != nil {
			t.Fatal(err)
		}
		want := decodeFrames(t, fresh, aus)

		d, err := NewRawDecoder(asc)
		if err != nil {
			t.Fatal(err)
		}
		if first := decodeFrames(t, d, aus); !bytes.Equal(first, want) {
			t.Fatal("first pass differs from a fresh decoder")
		}
		if err := d.Reset(); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		if second := decodeFrames(t, d, aus); !bytes.Equal(second, want) {
			t.Fatal("post-Reset pass differs from a fresh decoder")
		}
	})
}

// decodeFrames decodes every access unit and returns the concatenated PCM.
func decodeFrames(t *testing.T, d *FrameDecoder, aus [][]byte) []byte {
	t.Helper()
	var out []byte
	for i, au := range aus {
		var err error
		out, _, err = d.DecodeFrame(out, au)
		if err != nil {
			t.Fatalf("DecodeFrame %d: %v", i, err)
		}
	}
	return out
}

// TestParseASC checks the probe helper: AAC-LC returns object type, sample rate
// and channel count with no error, while HE-AAC, HE-AACv2, non-LC object types
// and channel configs above two return the typed sentinels a consumer branches
// on to name the unsupported codec.
func TestParseASC(t *testing.T) {
	t.Run("aac_lc", func(t *testing.T) {
		cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, Bitrate: 128000}
		fe, err := NewFrameEncoder(cfg)
		if err != nil {
			t.Fatal(err)
		}
		info, err := ParseASC(fe.AudioSpecificConfig())
		if err != nil {
			t.Fatalf("ParseASC(AAC-LC) = %v, want nil", err)
		}
		if info.ObjectType != 2 {
			t.Errorf("ObjectType = %d, want 2 (AAC-LC)", info.ObjectType)
		}
		if info.SampleRate != cfg.SampleRate {
			t.Errorf("SampleRate = %d, want %d", info.SampleRate, cfg.SampleRate)
		}
		if info.Channels != cfg.Channels {
			t.Errorf("Channels = %d, want %d", info.Channels, cfg.Channels)
		}
		if info.SBR || info.PS {
			t.Errorf("SBR=%v PS=%v, want both false for AAC-LC", info.SBR, info.PS)
		}
	})

	// Fixtures below are lifted from internal/dec's ASC tests (explicit SBR/PS)
	// and hand-packed for the object-type and channel-config cases; the bit
	// layout is objectType(5) samplingIndex(4) chanConfig(4).
	cases := []struct {
		name      string
		asc       []byte
		want      error
		alsoMatch []error
		notMatch  []error
	}{
		{"non_lc_aot1", []byte{0x0a, 0x10}, ErrUnsupported, nil, []error{ErrUnsupportedSBR}},
		{"chan_config_3", []byte{0x12, 0x18}, ErrUnsupported, nil, []error{ErrUnsupportedSBR}},
		{"explicit_sbr", []byte{0x2b, 0x92, 0x08, 0x00}, ErrUnsupportedSBR, []error{ErrUnsupported}, []error{ErrUnsupportedPS}},
		{"explicit_ps", []byte{0xea, 0x12, 0x08}, ErrUnsupportedPS, []error{ErrUnsupportedSBR, ErrUnsupported}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseASC(tc.asc)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseASC = %v, want errors.Is %v", err, tc.want)
			}
			for _, e := range tc.alsoMatch {
				if !errors.Is(err, e) {
					t.Errorf("error %v should also match %v", err, e)
				}
			}
			for _, e := range tc.notMatch {
				if errors.Is(err, e) {
					t.Errorf("error %v wrongly matches %v", err, e)
				}
			}
		})
	}
}

// TestFrameDecoderErrorDoesNotCorruptState checks the no-latch contract: a
// malformed access unit returns an error without consuming decoder state, so a
// following valid unit still decodes. A transport that hits a corrupt packet
// can skip it and keep going.
func TestFrameDecoderErrorDoesNotCorruptState(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	frames := splitADTSFrames(t, encodeADTS(t, cfg, testPCM(4500, cfg)))
	if len(frames) < 3 {
		t.Fatalf("only %d frames; need at least 3", len(frames))
	}

	bad := frames[1]
	payload := len(bad) - adtsHeaderLen
	// Malformed access units from a trivial header-only failure to two deep
	// partial parses that reach well into the spectral data before overreading,
	// so the test exercises a half-populated SCE/CPE (stale MSMask, unfinished
	// element), not only the configuration latch.
	malformed := [][]byte{
		bad[:adtsHeaderLen],           // header only: fails before any element
		bad[:adtsHeaderLen+payload/2], // half payload: fails mid raw_data_block
		bad[:len(bad)-1],              // one byte short: fails at the very end
	}

	d := NewADTSDecoder()
	if _, _, err := d.DecodeFrame(nil, frames[0]); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	sawError := false
	for i, m := range malformed {
		if _, _, err := d.DecodeFrame(nil, m); err != nil {
			sawError = true
		}
		// Whether the malformed unit errored or decoded to garbage, the decoder
		// must stay usable: a following valid frame decodes to a full frame.
		out, n, err := d.DecodeFrame(nil, frames[2])
		if err != nil {
			t.Fatalf("valid frame after malformed input %d: %v", i, err)
		}
		if n != aac.FrameSize {
			t.Errorf("malformed %d: recovered frame reported %d samples, want %d", i, n, aac.FrameSize)
		}
		if len(out) != aac.FrameSize*cfg.Channels*2 {
			t.Errorf("malformed %d: recovered frame produced %d bytes, want %d", i, len(out), aac.FrameSize*cfg.Channels*2)
		}
	}
	if !sawError {
		t.Fatal("no malformed access unit produced an error; the error path was not exercised")
	}
}

// TestFrameDecoderRejectsBadASC checks that NewRawDecoder and ParseASC reject an
// unsupported or malformed config with the same typed error, so a consumer can
// probe with either before committing to the stream.
func TestFrameDecoderRejectsBadASC(t *testing.T) {
	cases := []struct {
		name string
		asc  []byte
		want error
	}{
		{"non_lc_aot1", []byte{0x0a, 0x10}, ErrUnsupported},
		{"chan_config_3", []byte{0x12, 0x18}, ErrUnsupported},
		{"explicit_sbr", []byte{0x2b, 0x92, 0x08, 0x00}, ErrUnsupportedSBR},
		{"explicit_ps", []byte{0xea, 0x12, 0x08}, ErrUnsupportedPS},
		{"truncated", []byte{0xff}, ErrCorruptStream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewRawDecoder(tc.asc); !errors.Is(err, tc.want) {
				t.Errorf("NewRawDecoder = %v, want errors.Is %v", err, tc.want)
			}
			if _, err := ParseASC(tc.asc); !errors.Is(err, tc.want) {
				t.Errorf("ParseASC = %v, want errors.Is %v", err, tc.want)
			}
		})
	}

	// A malformed config returns a zeroed ASCInfo, not overread garbage: an
	// empty buffer must not report the 96 kHz that sample-rate index 0 decodes to.
	if info, err := ParseASC([]byte{}); !errors.Is(err, ErrCorruptStream) {
		t.Errorf("ParseASC(empty) = %v, want ErrCorruptStream", err)
	} else if info != (ASCInfo{}) {
		t.Errorf("ParseASC(empty) info = %+v, want the zero value", info)
	}
}

// TestFrameDecoderRejectsEmptyFrame guards against a phantom-frame report: an
// access unit that carries no audio channel element (a bare TypeEnd) decodes to
// no audio, so DecodeFrame must return an error and append nothing rather than
// claim a full frame of samples with zero bytes written.
func TestFrameDecoderRejectsEmptyFrame(t *testing.T) {
	d, err := NewRawDecoder([]byte{0x12, 0x10}) // AAC-LC, 44.1 kHz, stereo
	if err != nil {
		t.Fatal(err)
	}
	dst := []byte{0xaa, 0xbb}                       // sentinel prefix that must survive an error unchanged
	out, n, err := d.DecodeFrame(dst, []byte{0xe0}) // 0xe0: first 3 bits are TypeEnd
	if !errors.Is(err, ErrCorruptStream) {
		t.Errorf("DecodeFrame(no-element AU) err = %v, want ErrCorruptStream", err)
	}
	if n != 0 {
		t.Errorf("samples = %d, want 0 on error", n)
	}
	if !bytes.Equal(out, dst) {
		t.Errorf("dst mutated on error: got %x, want %x", out, dst)
	}
}

// TestFrameDecoderADTSConfigChange pins the documented contract that an ADTS
// FrameDecoder rejects a mid-stream configuration change: once it has learned a
// config from the first frame, a later frame describing a different sample rate
// or channel count returns ErrUnsupported rather than silently reinterpreting
// the stream, and the rejected frame does not corrupt state.
func TestFrameDecoderADTSConfigChange(t *testing.T) {
	a := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, Bitrate: 128000}
	b := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	framesA := splitADTSFrames(t, encodeADTS(t, a, testPCM(2500, a)))
	if len(framesA) < 2 {
		t.Fatalf("only %d frames in config A; need at least 2", len(framesA))
	}
	frameB := splitADTSFrames(t, encodeADTS(t, b, testPCM(2500, b)))[0]

	d := NewADTSDecoder()
	if _, _, err := d.DecodeFrame(nil, framesA[0]); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if _, _, err := d.DecodeFrame(nil, frameB); !errors.Is(err, ErrUnsupported) {
		t.Errorf("mid-stream config change err = %v, want ErrUnsupported", err)
	}
	// The rejected frame leaves the decoder usable: a frame in the original
	// configuration still decodes to a full frame.
	if _, n, err := d.DecodeFrame(nil, framesA[1]); err != nil || n != aac.FrameSize {
		t.Errorf("frame after rejected config change: n=%d err=%v, want %d and nil", n, err, aac.FrameSize)
	}
}

// TestFrameDecoderZeroValue checks that a zero-value FrameDecoder is inert
// rather than a nil-pointer panic: every method reports the uninitialised state,
// so a caller that skipped the constructor gets an error, not a crash.
func TestFrameDecoderZeroValue(t *testing.T) {
	var d FrameDecoder
	if out, n, err := d.DecodeFrame(nil, []byte{0x21, 0x00}); err == nil {
		t.Errorf("zero-value DecodeFrame = (%d bytes, %d, nil), want an error", len(out), n)
	}
	if got := d.SampleRate(); got != 0 {
		t.Errorf("zero-value SampleRate = %d, want 0", got)
	}
	if got := d.Channels(); got != 0 {
		t.Errorf("zero-value Channels = %d, want 0", got)
	}
	if err := d.Reset(); err == nil {
		t.Error("zero-value Reset = nil, want an error")
	}
}

// TestFrameDecoderSteadyStateAllocs is the allocation gate for the decode path:
// a warmed FrameDecoder must decode an access unit into a reused dst without
// allocating, since the live use decodes one unit every ~21 ms per stream on an
// ARM SBC. It recycles a pre-sized dst the way a resample stage consuming s16le
// bytes would.
func TestFrameDecoderSteadyStateAllocs(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	aus, _ := collectAUs(t, cfg, testPCM(4500, cfg), 0)
	if len(aus) == 0 {
		t.Fatal("no access units to decode")
	}
	au := aus[len(aus)-1] // a steady-state unit, not the priming frame

	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewRawDecoder(fe.AudioSpecificConfig())
	if err != nil {
		t.Fatal(err)
	}

	dst := make([]byte, 0, aac.FrameSize*cfg.Channels*2)
	for range 8 { // warm up any growth paths
		if dst, _, err = d.DecodeFrame(dst[:0], au); err != nil {
			t.Fatal(err)
		}
	}
	allocs := testing.AllocsPerRun(50, func() {
		if dst, _, err = d.DecodeFrame(dst[:0], au); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("%.2f allocs/DecodeFrame", allocs)
	if allocs > 0 {
		t.Errorf("steady-state DecodeFrame allocates %.2f/op, want 0", allocs)
	}
}
