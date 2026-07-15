// SPDX-License-Identifier: LGPL-2.1-or-later
package aac

import (
	"encoding/binary"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
)

// TestGateEncodeADTS encodes raw interleaved float32 PCM to an ADTS stream
// for the Phase 3 gate checks. Rehearsal helper, driven by env vars:
// GOAAC_GATE_IN/_RATE/_CH/_BR/_CODER/_OUT.
func TestGateEncodeADTS(t *testing.T) {
	inPath := os.Getenv("GOAAC_GATE_IN")
	if inPath == "" {
		t.Skip("set GOAAC_GATE_IN")
	}
	rate, _ := strconv.Atoi(os.Getenv("GOAAC_GATE_RATE"))
	channels, err := strconv.Atoi(os.Getenv("GOAAC_GATE_CH"))
	if err != nil || channels < 1 || channels > 2 {
		t.Fatalf("GOAAC_GATE_CH must be 1 or 2, got %q", os.Getenv("GOAAC_GATE_CH"))
	}
	bitrate, _ := strconv.Atoi(os.Getenv("GOAAC_GATE_BR"))
	kind := enc.CoderNMR
	switch os.Getenv("GOAAC_GATE_CODER") {
	case "", "nmr":
		kind = enc.CoderNMR
	case "fast":
		kind = enc.CoderFast
	case "twoloop":
		kind = enc.CoderTwoLoop
	default:
		t.Fatalf("GOAAC_GATE_CODER must be nmr, fast, or twoloop, got %q",
			os.Getenv("GOAAC_GATE_CODER"))
	}
	outPath := os.Getenv("GOAAC_GATE_OUT")

	raw, err := os.ReadFile(inPath)
	if err != nil {
		t.Fatal(err)
	}
	n := len(raw) / 4 / channels
	pcm := make([][]float32, channels)
	for ch := range channels {
		pcm[ch] = make([]float32, n)
	}
	for i := range n {
		for ch := range channels {
			bits := binary.LittleEndian.Uint32(raw[(i*channels+ch)*4:])
			pcm[ch][i] = math.Float32frombits(bits)
		}
	}

	e, err := enc.New(enc.Config{
		SampleRate: rate, Bitrate: bitrate, Channels: channels, Coder: kind,
		DisableTNS: os.Getenv("GOAAC_GATE_NOTNS") == "1",
		DisablePNS: os.Getenv("GOAAC_GATE_NOPNS") == "1",
		DisableMS:  os.Getenv("GOAAC_GATE_NOMS") == "1",
		DisableIS:  os.Getenv("GOAAC_GATE_NOIS") == "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	srIdx, ok := sampleRateIndex(rate)
	if !ok {
		t.Fatalf("no samplerate index for %d", rate)
	}

	var stream []byte
	au := make([]byte, 0, 4096)
	frame := make([][]float32, channels)
	wrap := func(payload []byte) {
		if len(payload) == 0 {
			return
		}
		stream = appendADTSHeader(stream, srIdx, channels, len(payload))
		stream = append(stream, payload...)
	}
	for off := 0; off < n; off += 1024 {
		end := min(off+1024, n)
		for ch := range channels {
			frame[ch] = pcm[ch][off:end]
		}
		au, err = e.EncodeFrame(au[:0], frame)
		if err != nil {
			t.Fatal(err)
		}
		wrap(au)
	}
	for !e.Drained() {
		au, err = e.EncodeFrame(au[:0], nil)
		if err != nil {
			t.Fatal(err)
		}
		wrap(au)
	}
	if err := os.WriteFile(outPath, stream, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("gate stream: %s (%d bytes)", outPath, len(stream))
}
