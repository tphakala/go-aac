// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

import (
	"encoding/binary"
	"math"
	"os"
	"strconv"
	"testing"
)

// TestWriteNMRTrace encodes raw interleaved float32 PCM with the NMR coder
// and dumps per-frame internals in the cnmrtrace record layout, for the
// full-pipeline diff against the real C encoder. Rehearsal helper, driven
// by env vars: GOAAC_NMR_TRACE_IN/_RATE/_CH/_BR/_OUT.
func TestWriteNMRTrace(t *testing.T) {
	inPath := os.Getenv("GOAAC_NMR_TRACE_IN")
	if inPath == "" {
		t.Skip("set GOAAC_NMR_TRACE_IN")
	}
	rate, _ := strconv.Atoi(os.Getenv("GOAAC_NMR_TRACE_RATE"))
	channels, err := strconv.Atoi(os.Getenv("GOAAC_NMR_TRACE_CH"))
	if err != nil || channels < 1 || channels > 2 {
		t.Fatalf("GOAAC_NMR_TRACE_CH must be 1 or 2, got %q", os.Getenv("GOAAC_NMR_TRACE_CH"))
	}
	bitrate, _ := strconv.Atoi(os.Getenv("GOAAC_NMR_TRACE_BR"))
	outPath := os.Getenv("GOAAC_NMR_TRACE_OUT")

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

	e, err := New(Config{SampleRate: rate, Bitrate: bitrate, Channels: channels})
	if err != nil {
		t.Fatal(err)
	}

	var out []byte
	put32 := func(v uint32) {
		out = binary.LittleEndian.AppendUint32(out, v)
	}
	putI := func(v int) { put32(uint32(int32(v))) }
	putF := func(v float32) { put32(math.Float32bits(v)) }
	putB := func(v bool) {
		if v {
			out = append(out, 1)
		} else {
			out = append(out, 0)
		}
	}

	dump := func(pktsize int) {
		cpe := &e.cpe
		putI(pktsize)
		putI(e.lastFramePBCount)
		putF(e.lambda)
		putF(e.nmr.LamRC)
		putI(e.nmr.RCFill)
		putF(e.nmr.SideEMA)
		putB(e.nmr.SideInited)
		out = append(out, 0, 0, 0) // pad bool to i32
		putI(e.nmr.FramesSinceShort)
		putB(e.nmr.PrevWasShort)
		out = append(out, 0, 0, 0)
		putF(e.nmr.RunBurst)
		for ch := range channels {
			putF(e.nmr.Lam[ch])
			putI(e.nmr.Counted[ch])
		}
		putI(cpe.CommonWindow)
		putI(cpe.MsMode)
		putB(cpe.IsMode)
		for i := range 128 {
			putB(cpe.MsMask[i])
		}
		for i := range 128 {
			putB(cpe.IsMask[i])
		}
		for ch := range channels {
			sce := &cpe.Ch[ch]
			putI(sce.ICS.WindowSequence[0])
			putI(sce.ICS.MaxSfb)
			for i := range 128 {
				putI(sce.SfIdx[i])
			}
			for i := range 128 {
				putI(sce.BandType[i])
			}
			for i := range 128 {
				putB(sce.Zeroes[i])
			}
		}
	}

	frame := make([][]float32, channels)
	var dst []byte
	for off := 0; off < n; off += 1024 {
		end := min(off+1024, n)
		for ch := range channels {
			frame[ch] = pcm[ch][off:end]
		}
		before := len(dst)
		dst, err = e.EncodeFrame(dst, frame)
		if err != nil {
			t.Fatal(err)
		}
		if len(dst) > before {
			dump(len(dst) - before)
		}
	}
	for !e.Drained() {
		before := len(dst)
		dst, err = e.EncodeFrame(dst, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(dst) > before {
			dump(len(dst) - before)
		}
	}

	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("trace written: %s", outPath)
}
