// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"math"
	"testing"

	"github.com/tphakala/go-aac/internal/dec"
)

// delayTestSignal builds a deterministic, spectrally rich signal of n samples
// per channel. Each channel carries a distinct pair of tones so a stereo test
// cannot pass by accident on channel symmetry.
func delayTestSignal(n, channels int) [][]float32 {
	sig := make([][]float32, channels)
	for ch := range sig {
		s := make([]float32, n)
		f0 := 300.0 + 130.0*float64(ch)
		f1 := 1700.0 + 370.0*float64(ch)
		for i := range s {
			t := float64(i) / 48000.0
			s[i] = float32(0.30*math.Sin(2*math.Pi*f0*t) +
				0.18*math.Sin(2*math.Pi*f1*t))
		}
		sig[ch] = s
	}
	return sig
}

// encodeDrain feeds the whole per-channel signal through e in FrameSize frames
// and then drains, returning every emitted raw access unit. Each EncodeFrame
// call is passed a nil dst, so every returned slice is an independent access
// unit safe to retain.
//
// It takes an already-constructed encoder rather than a config so that callers
// needing a particular encoder lifecycle (a fresh one, or one dirtied and then
// Reset onto the config under test) can arrange that themselves and still share
// one definition of the access-unit collection and drain contract. That
// contract lives here for the collection helpers: if the encoder API moves,
// this is the feed loop they share (encodeADTSPlanar carries its own, since it
// also wraps ADTS framing).
func encodeDrain(t *testing.T, e *Encoder, sig [][]float32) [][]byte {
	t.Helper()
	n := len(sig[0])
	// Whole frames only. Without this the reslice below panics on a bounds
	// error instead of failing with a diagnostic.
	if n%FrameSize != 0 {
		t.Fatalf("signal length %d is not a whole number of frames", n)
	}
	aus := make([][]byte, 0, n/FrameSize+1)
	collect := func(au []byte, encErr error) {
		if encErr != nil {
			t.Fatal(encErr)
		}
		if len(au) > 0 {
			aus = append(aus, au)
		}
	}
	frame := make([][]float32, len(sig))
	for off := 0; off < n; off += FrameSize {
		for ch := range sig {
			frame[ch] = sig[ch][off : off+FrameSize]
		}
		collect(e.EncodeFrame(nil, frame))
	}
	for !e.Drained() {
		collect(e.EncodeFrame(nil, nil))
	}
	return aus
}

// encodeCollectStats encodes the whole per-channel signal through a fresh
// encoder for cfg, returning every emitted raw access unit, the stream's ASC
// and the tool-usage counters accumulated over the run.
func encodeCollectStats(t *testing.T, cfg EncoderConfig, sig [][]float32) (aus [][]byte, asc []byte, st Stats) {
	t.Helper()
	e, err := NewEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	aus = encodeDrain(t, e, sig)
	return aus, e.AudioSpecificConfig(), e.Stats()
}

// encodeCollect is encodeCollectStats for the callers that do not look at the
// counters. It exists so those call sites stay readable, not to add behaviour.
func encodeCollect(t *testing.T, cfg EncoderConfig, sig [][]float32) (aus [][]byte, asc []byte) {
	t.Helper()
	aus, asc, _ = encodeCollectStats(t, cfg, sig)
	return aus, asc
}

// assertDecodable is the decodability contract the encoder gates that decode
// their streams share (the edge-config soak, the mid/side gate and the
// tool-wiring gate): the access units decode through the in-repo decoder to the
// expected channel count and to exactly the input length plus the priming
// delay, and the result is not digital silence. A gate whose variant stream
// merely differed, or merely decoded, would otherwise pass on a broken
// stream. label prefixes each
// failure so a caller checking several variants can tell them apart.
func assertDecodable(t *testing.T, label string, asc []byte, aus [][]byte, src [][]float32) {
	t.Helper()
	perChannel, pcmS16, channels := decodeAll(t, asc, aus)
	if channels != len(src) {
		t.Errorf("%s: decoded %d channels, want %d", label, channels, len(src))
	}
	if want := len(src[0]) + EncoderDelay; perChannel != want {
		t.Errorf("%s: decoded %d samples per channel, want %d", label, perChannel, want)
	}
	if isDigitalSilence(pcmS16) {
		t.Errorf("%s: decoded output is digital silence", label)
	}
}

// isDigitalSilence reports whether b is a non-empty run of zero bytes, i.e.
// decoded digital silence. A stream that encodes and decodes cleanly but
// carries no signal is still an encoder failure, and at the bitrate floor it is
// the plausible degenerate outcome. Empty input is not silence: a decode that
// produced nothing at all is caught by the sample-count assertion instead, and
// reporting it here too would only mislabel it. assertDecodable is its only
// caller.
func isDigitalSilence(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// decodeAll decodes every access unit through the raw decoder and returns the
// total inter-channel (per-channel) sample count, the concatenated interleaved
// S16 PCM, and the decoded channel count.
func decodeAll(t *testing.T, asc []byte, aus [][]byte) (perChannel int, pcmS16 []byte, channels int) {
	t.Helper()
	d, err := dec.NewRaw(asc)
	if err != nil {
		t.Fatal(err)
	}
	for _, au := range aus {
		var n int
		pcmS16, n, err = d.AppendS16(pcmS16, au)
		if err != nil {
			t.Fatal(err)
		}
		perChannel += n
	}
	return perChannel, pcmS16, d.Channels()
}

// TestEncoderDelayAPI pins the exported delay contract: EncoderDelay is 1024
// and equals FrameSize, Delay reports EncoderDelay, and the priming frame
// (the source of the delay) emits no access unit.
func TestEncoderDelayAPI(t *testing.T) {
	if EncoderDelay != 1024 {
		t.Fatalf("EncoderDelay = %d, want 1024", EncoderDelay)
	}
	if EncoderDelay != FrameSize {
		t.Fatalf("EncoderDelay = %d, want FrameSize (%d)", EncoderDelay, FrameSize)
	}
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Bitrate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Delay(); got != EncoderDelay {
		t.Fatalf("Delay() = %d, want EncoderDelay (%d)", got, EncoderDelay)
	}
	au, err := e.EncodeFrame(nil, delayTestSignal(FrameSize, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(au) != 0 {
		t.Fatalf("priming call emitted %d bytes, want 0", len(au))
	}
}

// TestEncoderDelayRoundTrip is the behavioral proof of the priming delay and
// reproduces the issue #27 author's observation: encode a whole number of full
// frames, drain, decode every access unit, and the decoder emits exactly
// EncoderDelay more samples per channel than went in. That surplus is the
// priming a muxer trims with an edit list.
func TestEncoderDelayRoundTrip(t *testing.T) {
	const frames = 16
	for _, tc := range []struct {
		name string
		ch   int
	}{{"mono", 1}, {"stereo", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := EncoderConfig{SampleRate: 48000, Channels: tc.ch, Bitrate: 128000}
			sig := delayTestSignal(frames*FrameSize, tc.ch)
			aus, asc := encodeCollect(t, cfg, sig)
			decodedPerCh, _, decCh := decodeAll(t, asc, aus)
			if decCh != tc.ch {
				t.Fatalf("decoded %d channels, want %d", decCh, tc.ch)
			}
			inputPerCh := frames * FrameSize
			delta := decodedPerCh - inputPerCh
			t.Logf("%s: input=%d decoded=%d delta=%d (EncoderDelay=%d)",
				tc.name, inputPerCh, decodedPerCh, delta, EncoderDelay)
			if delta != EncoderDelay {
				t.Fatalf("decoded-minus-input delay = %d samples/channel, want %d",
					delta, EncoderDelay)
			}
		})
	}
}

// s16leToFloat decodes interleaved little-endian S16 to float64 samples.
func s16leToFloat(b []byte) []float64 {
	out := make([]float64, len(b)/2)
	for i := range out {
		out[i] = float64(int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8))
	}
	return out
}

// TestEncoderDelayCorrelationLag confirms the delay is not just a sample count
// but a true alignment: the decoded mono output correlates most strongly with
// the source at a lag of exactly EncoderDelay. This is the normalized
// cross-correlation lag search the issue author described.
func TestEncoderDelayCorrelationLag(t *testing.T) {
	const frames = 24
	cfg := EncoderConfig{SampleRate: 48000, Channels: 1, Bitrate: 160000}
	sig := delayTestSignal(frames*FrameSize, 1)
	aus, asc := encodeCollect(t, cfg, sig)
	_, pcmS16, _ := decodeAll(t, asc, aus)
	decoded := s16leToFloat(pcmS16)
	in := sig[0]

	const (
		margin = 48
		start  = 3 * FrameSize
		width  = 6 * FrameSize
	)
	bestLag, bestR := -1, math.Inf(-1)
	for lag := EncoderDelay - margin; lag <= EncoderDelay+margin; lag++ {
		var cross, energyD, energyIn float64
		for i := start; i < start+width; i++ {
			d := decoded[lag+i]
			s := float64(in[i])
			cross += d * s
			energyD += d * d
			energyIn += s * s
		}
		r := cross / math.Sqrt(energyD*energyIn)
		if r > bestR {
			bestR, bestLag = r, lag
		}
	}
	t.Logf("cross-correlation peak lag=%d r=%.5f", bestLag, bestR)
	if bestLag != EncoderDelay {
		t.Fatalf("cross-correlation peak at lag %d, want EncoderDelay (%d)", bestLag, EncoderDelay)
	}
	if bestR < 0.99 {
		t.Fatalf("cross-correlation peak r=%.5f too low; alignment unconvincing", bestR)
	}
}
