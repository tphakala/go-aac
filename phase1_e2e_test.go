// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
)

// ffmpegBin returns the pinned FFmpeg CLI named by GOAAC_FFMPEG, skipping the
// test when it is unset. The gate compares against FFmpeg pinned at d09d5afc3a;
// a distro ffmpeg is not a valid oracle (it predates the NMR coder), so there is
// deliberately no default path to fall back on.
func ffmpegBin(t *testing.T) string {
	t.Helper()
	p := os.Getenv("GOAAC_FFMPEG")
	if p == "" {
		skipOrFatalOracle(t, "GOAAC_FFMPEG is not set; skipping the C differential gate")
	}
	if _, err := os.Stat(p); err != nil {
		skipOrFatalOracle(t, fmt.Sprintf("GOAAC_FFMPEG=%q is not usable: %v", p, err))
	}
	return p
}

// skipOrFatalOracle skips the calling test, or fails it when
// GOAAC_REQUIRE_ORACLE is set to a non-empty value.
//
// Skipping is right for a contributor without the pinned FFmpeg. It is wrong
// for a runner whose whole job is to run the gate: a mistyped path or a broken
// build would skip every differential test and still print ok, which is exactly
// how a rate-control regression once passed CI green. The CI oracle job sets
// GOAAC_REQUIRE_ORACLE so that an absent oracle reports red.
func skipOrFatalOracle(t *testing.T, msg string) {
	t.Helper()
	if os.Getenv("GOAAC_REQUIRE_ORACLE") != "" {
		t.Fatalf("GOAAC_REQUIRE_ORACLE is set, so a missing oracle is a failure: %s", msg)
	}
	t.Skip(msg)
}

// synthTonal generates n samples of a deterministic three-tone mix, the
// tonal Phase 1 gate signal (issue #3: tonal corpus).
func synthTonal(n, rate int) []float32 {
	src := make([]float32, n)
	for i := range n {
		ts := float64(i) / float64(rate)
		v := 0.35*math.Sin(2*math.Pi*220*ts) +
			0.25*math.Sin(2*math.Pi*997*ts) +
			0.10*math.Sin(2*math.Pi*3800*ts)
		src[i] = float32(v)
	}
	return src
}

// encodeADTS runs the Phase 1 encoder over src and returns an ADTS stream.
func encodeADTS(t *testing.T, cfg enc.Config, src []float32) []byte {
	t.Helper()
	e, err := enc.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srIdx, ok := sampleRateIndex(cfg.SampleRate)
	if !ok {
		t.Fatalf("no samplerate index for %d", cfg.SampleRate)
	}
	var stream []byte
	au := make([]byte, 0, 1536)
	wrap := func(payload []byte) {
		if len(payload) == 0 {
			return
		}
		stream = appendADTSHeader(stream, srIdx, 1, len(payload))
		stream = append(stream, payload...)
	}
	for off := 0; off < len(src); off += 1024 {
		frame := src[off:min(off+1024, len(src))]
		au, err = e.EncodeFrame(au[:0], [][]float32{frame})
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
	return stream
}

// ffmpegDecode decodes an ADTS file to raw mono f32le via the pinned ffmpeg
// and fails the test on any decoder diagnostic (the issue #3 gate demands
// zero errors at -v error).
func ffmpegDecode(t *testing.T, ffmpeg, path string) []float32 {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", path,
		"-f", "f32le", "-c:a", "pcm_f32le", "-")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg decode failed: %v\n%s", err, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("ffmpeg -v error reported diagnostics:\n%s", stderr.String())
	}
	raw := stdout.Bytes()
	pcm := make([]float32, len(raw)/4)
	for i := range pcm {
		pcm[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return pcm
}

// psnr computes 10*log10(1/MSE) against full-scale 1.0 between src and
// dec[delay:], the documented Phase 1 PSNR definition.
func psnr(src, dec []float32, delay int) float64 {
	var mse float64
	n := 0
	for i := range src {
		if delay+i >= len(dec) {
			break
		}
		d := float64(src[i]) - float64(dec[delay+i])
		mse += d * d
		n++
	}
	mse /= float64(n)
	if mse == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(1/mse)
}

func TestPhase1Gate(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	for _, rate := range []int{44100, 48000} {
		t.Run(fmt.Sprintf("%dHz", rate), func(t *testing.T) {
			n := rate * 5
			src := synthTonal(n, rate)
			stream := encodeADTS(t, enc.Config{SampleRate: rate, Bitrate: 128000, Channels: 1, Coder: enc.CoderFast, DisableTNS: true, DisablePNS: true}, src)

			dir := t.TempDir()
			adts := filepath.Join(dir, "out.adts")
			if err := os.WriteFile(adts, stream, 0o644); err != nil {
				t.Fatal(err)
			}
			dec := ffmpegDecode(t, ffmpeg, adts)
			if len(dec) < n {
				t.Fatalf("decoded %d samples, want >= %d", len(dec), n)
			}
			got := psnr(src, dec, 1024)
			t.Logf("rate %d: %d bytes ADTS, decoded %d samples, PSNR %.2f dB",
				rate, len(stream), len(dec), got)
			if got < 30 {
				t.Errorf("PSNR %.2f dB below the 30 dB Phase 1 gate", got)
			}
		})
	}
}
