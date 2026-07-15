// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCutoffParityVsC proves the Config.Cutoff plumbing end to end: the
// pinned C encoder and the Go encoder, both at 128k mono NMR with -cutoff
// 12000, must land within the Phase 4 gate bounds of each other (3% size,
// 0.5 dB PSNR). A wrong cutoff wiring (ignored, clamped, off by a band)
// shifts the coded bandwidth and blows the size delta immediately.
func TestCutoffParityVsC(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	data, rate, channels, bits := wavPCM(t, corpusWAV(t, "tawnyowl.wav"))
	src := srcFloats(data, channels, bits)
	dir := t.TempDir()

	// Feed the C encoder the identical float samples the Go encoder sees.
	rawPath := filepath.Join(dir, "src.f32")
	rawF32 := make([]byte, len(src[0])*4)
	for i, v := range src[0] {
		binary.LittleEndian.PutUint32(rawF32[i*4:], math.Float32bits(v))
	}
	if err := os.WriteFile(rawPath, rawF32, 0o644); err != nil {
		t.Fatal(err)
	}

	const cutoff = 12000
	cPath := filepath.Join(dir, "c.adts")
	cmd := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "f32le",
		"-ar", fmt.Sprint(rate), "-ac", "1", "-i", rawPath,
		"-c:a", "aac", "-cutoff", fmt.Sprint(cutoff), "-b:a", "128000",
		"-flags", "+bitexact", "-f", "adts", cPath)
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("C encode: %v %q", err, out)
	}
	cStream, err := os.ReadFile(cPath)
	if err != nil {
		t.Fatal(err)
	}

	var goBuf bytes.Buffer
	if err := EncodeInterleaved(&goBuf,
		Config{SampleRate: rate, BitDepth: bits, Channels: 1, Bitrate: 128000, Cutoff: cutoff}, data); err != nil {
		t.Fatal(err)
	}
	goPath := filepath.Join(dir, "go.adts")
	if err := os.WriteFile(goPath, goBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	sizeDelta := 100 * (float64(goBuf.Len()) - float64(len(cStream))) / float64(len(cStream))
	pGo := psnr(src[0], ffmpegDecode(t, ffmpeg, goPath, 1)[0], 1024)
	pC := psnr(src[0], ffmpegDecode(t, ffmpeg, cPath, 1)[0], 1024)
	t.Logf("cutoff %d: Go %d B %.2f dB, C %d B %.2f dB (size %+.2f%%, PSNR %+.2f dB)",
		cutoff, goBuf.Len(), pGo, len(cStream), pC, sizeDelta, pGo-pC)
	if math.Abs(sizeDelta) > 3.0 {
		t.Errorf("size delta %+.2f%% vs C, gate demands within 3%%", sizeDelta)
	}
	if pGo < pC-0.5 {
		t.Errorf("PSNR %.2f dB is %.2f dB below the C encoder's, gate allows 0.5", pGo, pC-pGo)
	}
}
