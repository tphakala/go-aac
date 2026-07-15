// SPDX-License-Identifier: LGPL-2.1-or-later

// This file generates the Phase 2 psy fixture signal shared by the
// comparison tests and, via TestWritePsyFixtureInput, by tools/cpsy, which
// consumes the identical samples from a raw file, so both sides analyze the
// same PCM.
//
// Cross-platform caveat: math.Sin has no architecture-specific assembly on
// the targets we build (only s390x), so it is bit-identical across
// amd64/arm64. math.Exp does NOT — it has exp_amd64.s and exp_arm64.s that
// can differ in the last ulp. It is used only for the click decay envelope
// below, and the committed C traces were generated from the arm64 result.
// TestFixtureSignalChecksum pins the generated PCM so that if math.Exp ever
// diverges on some build platform, that fails loudly with a clear message
// instead of surfacing as an unexplained psy-trace mismatch.

package psy

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"testing"
)

// fixtureSignalSHA256 pins the exact bytes of the two fixture channels, planar
// f32le, as generated on the arm64 machine that authored the committed C traces.
// See the package comment: math.Exp is architecture-dependent, so this guards
// against a build platform silently producing different PCM.
const fixtureSignalSHA256 = "980993a8c44c1ec4745583e1374fbbb9c1fdb624e097814f528851361974b295"

// TestFixtureSignalChecksum fails loudly, with a clear message, if the fixture
// PCM this platform generates differs from the canonical bytes the C traces were
// made from -- turning a latent cross-platform math.Exp divergence into an
// obvious, diagnosable failure instead of an unexplained psy-trace mismatch.
func TestFixtureSignalChecksum(t *testing.T) {
	h := sha256.New()
	for _, ch := range fixtureSignal() {
		if err := binary.Write(h, binary.LittleEndian, ch); err != nil {
			t.Fatal(err)
		}
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != fixtureSignalSHA256 {
		t.Fatalf("fixture PCM checksum = %s, want %s\n"+
			"the generated signal differs from the canonical bytes the C traces were made from; "+
			"math.Exp is architecture-dependent (see the package comment)", got, fixtureSignalSHA256)
	}
}

// fixtureRate is the fixture sample rate.
const fixtureRate = 44100

// fixtureFrames is the fixture length in AAC frames per channel.
const fixtureFrames = 48

// fixtureN is the fixture length in samples per channel.
const fixtureN = fixtureFrames * 1024

type siglcg struct{ s uint32 }

func (l *siglcg) next() float64 { // uniform in [-1, 1)
	l.s = l.s*1664525 + 1013904223
	return float64(int32(l.s)) / 2147483648.0
}

// synth renders one channel. The signal walks through every
// window-decision regime: steady tones (ONLY_LONG), a castanet-like click
// (LONG_START -> EIGHT_SHORT -> LONG_STOP), decay, a long quiet stretch
// followed by a gentle isolated onset that only the 2026-06 pre-echo
// relaxation catches (verified in rehearsal against a variant C build with
// PSY_LAME_PE_RED neutered), broadband noise and silence to the flush.
func synth(clickAt int, clickAmp, phase float64) []float32 {
	out := make([]float32, fixtureN)
	noise := &siglcg{s: 0x0badcafe}
	onset := &siglcg{s: 0x5eed5eed}
	floorN := &siglcg{s: 0x00f100f1}
	big := &siglcg{s: 0xb0a7b0a7}
	for i := range fixtureN {
		t := float64(i)/fixtureRate + phase
		f := i / 1024 // frame index
		var v float64
		switch {
		case f < 12: // steady tones -> ONLY_LONG
			v = 0.25*math.Sin(2*math.Pi*220*t) + 0.15*math.Sin(2*math.Pi*997*t)
		case f < 18: // decaying tone after the click
			v = 0.12 * math.Sin(2*math.Pi*220*t)
		case f < 31: // quiet stretch with a tiny HF noise floor
			v = 0.08*math.Sin(2*math.Pi*440*t) + 0.001*floorN.next()
		case f < 33: // gentle isolated onset (pre-echo relaxation target):
			// the tone ramps smoothly (no wideband step); only the small HF
			// noise floor steps up at the onset point, so the attack
			// intensity lands between the relaxed and the full threshold
			onsetAt := 31*1024 + 500
			ramp := 0.0
			nf := 0.001
			if i >= onsetAt {
				ramp = math.Min(1, float64(i-onsetAt)/2000.0)
				nf = 0.0038
			}
			v = (0.08+0.22*ramp)*math.Sin(2*math.Pi*440*t) + nf*onset.next()
		case f < 41: // steady broadband noise
			v = 0.30 * big.next()
		default: // near-silence to the flush
			v = 0.0
		}
		out[i] = float32(v)
	}
	// castanet-like click: sharp noise burst with exponential decay
	for k := range 800 {
		i := clickAt + k
		if i < fixtureN {
			out[i] += float32(clickAmp * math.Exp(-float64(k)/150.0) * noise.next())
		}
	}
	return out
}

// fixtureSignal returns the two fixture channels. Channel 1 shifts the
// click by 700 samples and offsets the tone phase so the channels disagree
// on the window decision for some frames (exercises the common_window=0
// path).
func fixtureSignal() [2][]float32 {
	return [2][]float32{
		synth(12588, 0.85, 0),
		synth(13288, 0.70, 0.001),
	}
}

// fixtureBits is the synthetic produced-bits sequence fed to the bit
// reservoir as last_frame_pb_count, reproduced identically by tools/cpsy.
// rateBitsTotal is bitrate*1024/samplerate. The returned function yields
// the value for each successive frame.
func fixtureBits(rateBitsTotal int) func() int {
	l := &siglcg{s: 0x2545f491}
	return func() int {
		l.s = l.s*1664525 + 1013904223
		return int(int64(rateBitsTotal) * int64(512+(l.s>>22)) / 1024)
	}
}

// TestWritePsyFixtureInput writes the fixture PCM (planar f32le, both
// channels back to back) for tools/cpsy when GOAAC_PSY_WRITE_INPUT names a
// target path. Rehearsal helper, mirrors the tools_encrun_test.go pattern.
func TestWritePsyFixtureInput(t *testing.T) {
	out := os.Getenv("GOAAC_PSY_WRITE_INPUT")
	if out == "" {
		t.Skip("set GOAAC_PSY_WRITE_INPUT")
	}
	var buf bytes.Buffer
	for _, ch := range fixtureSignal() {
		if err := binary.Write(&buf, binary.LittleEndian, ch); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
