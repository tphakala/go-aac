// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"fmt"
	"math"
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

// TestPhase3NMRGateVsC is the Phase 3 gate (issue #5): with the NMR coder
// on BOTH sides at 96/128/192 kbps stereo, TNS off on both (the Phase 3
// feature set; PNS, I/S and M/S on), the Go stream size must land within 3%
// of the C encoder's, the decoded PSNR within 0.5 dB of the C encoder's own
// PSNR per case, and the Go streams must decode cleanly under the pinned
// ffmpeg. It scores through the shared gate under phase3Bounds, which keep
// this gate's worst-channel rule rather than Phase 4's mean rule.
func TestPhase3NMRGateVsC(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	sigs := []gateSignal{
		newGateSignal(t, "stereo tonal", synthStereoNMR(44100*8, 44100)),
		newGateSignal(t, sigStereoCastanets, castanetsChannels(archChanStereo, 44100, 44100*6)),
	}
	for _, sig := range sigs {
		for _, br := range []int{96000, 128000, 192000} {
			t.Run(fmt.Sprintf("%s %dk", sig.name, br/1000), func(t *testing.T) {
				gateCellVsC(t, ffmpeg, sig,
					enc.Config{SampleRate: 44100, Bitrate: br, Channels: 2, DisableTNS: true},
					phase3Bounds)
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
