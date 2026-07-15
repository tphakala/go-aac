// SPDX-License-Identifier: LGPL-2.1-or-later
package enc

import (
	"encoding/binary"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/tphakala/go-aac/internal/coder"
)

// Scratch: per-tool activity stats over an encode, mirroring the C's
// uninit Qavg report. Env-driven; skipped by default.
func TestToolStatsScratch(t *testing.T) {
	inPath := os.Getenv("GOAAC_STATS_IN")
	if inPath == "" {
		t.Skip("set GOAAC_STATS_IN")
	}
	rate, _ := strconv.Atoi(os.Getenv("GOAAC_STATS_RATE"))
	br, _ := strconv.Atoi(os.Getenv("GOAAC_STATS_BR"))
	raw, err := os.ReadFile(inPath)
	if err != nil {
		t.Fatal(err)
	}
	n := len(raw) / 4
	pcm := make([]float32, n)
	for i := range n {
		pcm[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	cfg := Config{SampleRate: rate, Bitrate: br, Channels: 1,
		DisableTNS: os.Getenv("GOAAC_STATS_NOTNS") == "1"}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var au []byte
	frames, tnsL, tnsS, shortF := 0, 0, 0, 0
	noiseBands, totBands := 0, 0
	count := func() {
		sce := &e.cpe.Ch[0]
		frames++
		short := sce.ICS.WindowSequence[0] == coder.EightShortSequence
		if short {
			shortF++
		}
		if sce.TNS.Present {
			if short {
				tnsS++
			} else {
				tnsL++
			}
		}
		for w := 0; w < sce.ICS.NumWindows; w += sce.ICS.GroupLen[w] {
			for g := range sce.ICS.MaxSfb {
				totBands++
				if sce.BandType[w*16+g] == coder.NoiseBT {
					noiseBands++
				}
			}
		}
	}
	for off := 0; off < n; off += 1024 {
		end := min(off+1024, n)
		au, err = e.EncodeFrame(au[:0], [][]float32{pcm[off:end]})
		if err != nil {
			t.Fatal(err)
		}
		if len(au) > 0 {
			count()
		}
	}
	for !e.Drained() {
		au, err = e.EncodeFrame(au[:0], nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(au) > 0 {
			count()
		}
	}
	long := frames - shortF
	pct := func(a, b int) float64 {
		if b == 0 {
			return 0
		}
		return 100.0 * float64(a) / float64(b)
	}
	t.Logf("frames %d  TNS(L) %.1f%%  TNS(S) %.1f%%  PNS %.1f%% (%d/%d bands)",
		frames, pct(tnsL, long), pct(tnsS, shortF), pct(noiseBands, totBands),
		noiseBands, totBands)
}
