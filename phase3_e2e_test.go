// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
)

// synthStereoNMR builds a stereo pair exercising the NMR stereo decisions:
// near-mono lows (M/S), correlated-but-scaled highs (I/S candidates) and
// decorrelated noise (PNS reservation), over a tonal bed.
func synthStereoNMR(n, rate int) [][]float32 {
	l := make([]float32, n)
	r := make([]float32, n)
	state := uint32(0x1f2e3d4c)
	lcg := func() float32 {
		state = state*1664525 + 1013904223
		return float32(state>>8)/8388608.0 - 1.0
	}
	for i := range n {
		ts := float64(i) / float64(rate)
		base := 0.32*math.Sin(2*math.Pi*220*ts) + 0.22*math.Sin(2*math.Pi*997*ts)
		hf := 0.08 * math.Sin(2*math.Pi*3800*ts)
		hfr := 0.08 * math.Sin(2*math.Pi*4100*ts+0.7)
		nz := 0.015 * float64(lcg())
		l[i] = float32(base + hf + nz)
		r[i] = float32(base + hfr - nz)
	}
	return [][]float32{l, r}
}

// cEncodeNMR runs the pinned C encoder with the NMR coder at the Phase 3
// feature set (TNS off; PNS/IS/MS on, as the Go pipeline has them).
func cEncodeNMR(t *testing.T, ffmpeg, rawPath string, rate, ch, bitrate int,
	outPath string) []byte {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "f32le",
		"-ar", fmt.Sprint(rate), "-ac", fmt.Sprint(ch), "-i", rawPath,
		"-c:a", "aac", "-aac_coder", "nmr", "-aac_tns", "0",
		"-b:a", fmt.Sprint(bitrate), "-flags", "+bitexact",
		"-f", "adts", outPath)
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("C encode: %v %q", err, out)
	}
	stream, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func writeRawF32(t *testing.T, path string, src [][]float32) {
	t.Helper()
	ch := len(src)
	raw := make([]byte, 4*ch*len(src[0]))
	for i := range src[0] {
		for c := range ch {
			binary.LittleEndian.PutUint32(raw[4*(i*ch+c):], math.Float32bits(src[c][i]))
		}
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPhase3NMRGateVsC is the Phase 3 gate (issue #5): with the NMR coder
// on BOTH sides at 96/128/192 kbps stereo, the Go stream size must land
// within 3% of the C encoder's, the decoded PSNR within 0.5 dB of the C
// encoder's own PSNR per case, and the Go streams must decode cleanly
// under the pinned ffmpeg.
func TestPhase3NMRGateVsC(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	tonal := synthStereoNMR(44100*8, 44100)
	casta := synthCastanets(44100*6, 44100, 0x0badcafe, 0)
	castaR := synthCastanets(44100*6, 44100, 0x5eed1234, 137)
	for _, sig := range []struct {
		name string
		src  [][]float32
	}{
		{"stereo tonal", tonal},
		{"stereo castanets", [][]float32{casta, castaR}},
	} {
		for _, br := range []int{96000, 128000, 192000} {
			t.Run(fmt.Sprintf("%s %dk", sig.name, br/1000), func(t *testing.T) {
				dir := t.TempDir()
				rawPath := filepath.Join(dir, "src.f32")
				writeRawF32(t, rawPath, sig.src)
				cStream := cEncodeNMR(t, ffmpeg, rawPath, 44100, 2, br,
					filepath.Join(dir, "c.adts"))
				goStream := encodeADTSPlanar(t,
					enc.Config{SampleRate: 44100, Bitrate: br, Channels: 2, DisableTNS: true}, sig.src)

				sizeDelta := 100 * (float64(len(goStream)) - float64(len(cStream))) /
					float64(len(cStream))
				if math.Abs(sizeDelta) > 3.0 {
					t.Errorf("stream size %+.2f%% vs C, gate demands within 3%%", sizeDelta)
				}

				goPath := filepath.Join(dir, "go.adts")
				if err := os.WriteFile(goPath, goStream, 0o644); err != nil {
					t.Fatal(err)
				}
				const delay = 1024
				worstDelta := math.Inf(1)
				decG := ffmpegDecode(t, ffmpeg, goPath)
				decC := ffmpegDecode(t, ffmpeg, filepath.Join(dir, "c.adts"))
				for c := range 2 {
					dg := make([]float32, len(decG)/2)
					dc := make([]float32, len(decC)/2)
					for i := range dg {
						dg[i] = decG[i*2+c]
					}
					for i := range dc {
						dc[i] = decC[i*2+c]
					}
					pg := psnr(sig.src[c], dg, delay)
					pc := psnr(sig.src[c], dc, delay)
					t.Logf("ch %d: Go %.2f dB, C %.2f dB (%+.2f), size %+.2f%%",
						c, pg, pc, pg-pc, sizeDelta)
					worstDelta = math.Min(worstDelta, pg-pc)
				}
				if worstDelta < -0.5 {
					t.Errorf("PSNR %.2f dB below the C encoder's, gate allows -0.5 dB",
						worstDelta)
				}
			})
		}
	}
}

// TestPhase3ReservoirSoak holds the NMR reservoir to the nominal rate over
// a one-minute encode: the mean payload bitrate must stay within 1% of the
// target (issue #5 gate: long-run soak holds mean bitrate on target).
func TestPhase3ReservoirSoak(t *testing.T) {
	const rate, br, secs = 44100, 128000, 60
	src := synthStereoNMR(rate*secs, rate)
	stream := encodeADTSPlanar(t,
		enc.Config{SampleRate: rate, Bitrate: br, Channels: 2, DisableTNS: true}, src)
	frames := adtsFrames(t, stream)
	payload := 0
	for _, f := range frames {
		payload += len(f)
	}
	dur := float64(len(frames)) * 1024 / rate
	mean := float64(payload) * 8 / dur
	t.Logf("mean payload rate %.0f b/s over %.1f s (target %d)", mean, dur, br)
	if math.Abs(mean-br)/br > 0.01 {
		t.Errorf("mean rate %.0f b/s drifts more than 1%% from %d", mean, br)
	}
}
