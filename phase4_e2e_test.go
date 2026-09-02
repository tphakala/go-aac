// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
)

// gateCastanetSecs is the length of the Phase 4 gate's castanet pair.
//
// The gate statistic is whole-signal PSNR, which on this material is
// effectively a sum over the transient events: between clicks both
// encoders are transparent and contribute nothing measurable. One click
// every 0.45 s left just 13 events in the 6 s pair this gate first
// used, and that sample is far too small, because the per-event error
// is heavy tailed. A single knife-edge decision (twoloop zeroing the
// top bands of one short-window group, which search_for_pns then turns
// into NOISE_BT) costs more than the rest of the file put together, so
// whichever encoder loses that one coin toss loses the cell.
//
// Issue #53 measured what that does to the C encoder itself. Nudging
// the input by one float32 ULP, about 100 dB below the coding noise,
// moved the C's own twoloop/castanets/192k reading across a 1.9 dB
// spread, so the C failed both the -0.5 dB mean bound and the -1.0 dB
// worst-channel backstop measured against itself. Repeating that
// measurement per duration, the spread of the mean delta on that cell
// runs 1.20 dB at 6 s, 0.65 at 12 s, 0.27 at 24 s and 0.28 at 48 s, so
// 24 s is where the curve flattens. Over 40 one-ULP perturbations at
// 24 s the C scatters by 0.32 dB on the mean and 0.75 dB on the worst
// channel, and go-aac sits at the centre of that distribution instead
// of being scored against one lucky draw of it. The twoloop 96 kbit/s
// cell tightens from 0.74 dB to 0.26 dB the same way; the three NMR
// castanet cells were already stable, within 0.01 dB at either length.
//
// Lengthening changes no threshold and no statistic, so the gate keeps
// whole-signal PSNR's full sensitivity to a systematic regression; it
// only stops a single frame from deciding the verdict. Measured on a
// deliberate reproduction of the failure above, one extra band zeroed
// per short-window group: every castanet cell fails, and every twoloop
// castanet cell responds more strongly at 24 s than it did at 6 s. The
// tonal pair stays at 8 s, where the same perturbation already moves
// the C by only about 0.1 dB.
const gateCastanetSecs = 24

// The Phase 4 same-settings bounds; phase4Bounds (oracle_harness_test.go)
// carries them into checkGateVsC for the gates below. Not every gate that
// scores a Go stream against the C stream uses them: phase3Bounds sits beside
// phase4Bounds with the older worst-channel rule, which TestPhase3NMRGateVsC
// keeps.
//
// M/S and I/S trade quantization error between the two channels, so
// per-channel PSNR is not stable under a stereo-tool decision flip; the gate
// is the per-case MEAN delta, with a worst-channel backstop catching a
// collapse the mean would average away. See gateCastanetSecs for why the
// reference is reproducible enough to assert against.
const (
	gateSizeBound  = 3.0  // percent, absolute
	gateMeanBound  = -0.5 // dB, mean over channels
	gateWorstBound = -1.0 // dB, worst channel
)

// TestPhase4ToolsGateVsC is the Phase 4 gate (issue #6): with TNS, PNS,
// I/S and M/S active on BOTH sides (all tool defaults), for BOTH the NMR
// and twoloop coders, the Go stream size must land within 3% of the C
// encoder's and the decoded PSNR, meaned over channels, within 0.5 dB of the
// C encoder's own, with the worst channel within 1.0 dB, and the Go streams
// must decode cleanly under the pinned ffmpeg.
func TestPhase4ToolsGateVsC(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	sigs := []gateSignal{
		newGateSignal(t, "stereo tonal", synthStereoNMR(44100*8, 44100)),
		newGateSignal(t, sigStereoCastanets,
			castanetsChannels(archChanStereo, 44100, 44100*gateCastanetSecs)),
	}
	for _, coder := range []struct {
		name string
		kind enc.CoderKind
	}{
		{coderNMR, enc.CoderNMR},
		{coderTwoLoop, enc.CoderTwoLoop},
	} {
		for _, sig := range sigs {
			for _, br := range []int{96000, 128000, 192000} {
				t.Run(fmt.Sprintf("%s %s %dk", coder.name, sig.name, br/1000), func(t *testing.T) {
					gateCellVsC(t, ffmpeg, sig,
						enc.Config{SampleRate: 44100, Bitrate: br, Channels: 2, Coder: coder.kind},
						phase4Bounds)
				})
			}
		}
	}
}

// TestPhase4TNSAB is the TNS A/B gate: with the default NMR coder,
// enabling TNS must not regress PSNR on transient material (castanets)
// beyond noise, and must leave tonal material untouched (TNS never fires
// on it, so the streams are identical).
func TestPhase4TNSAB(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	castaSrc := castanetsChannels(archChanStereo, 44100, 44100*6)
	tonal := synthStereoNMR(44100*8, 44100)

	run := func(src [][]float32, disableTNS bool) ([]byte, float64) {
		stream := encodeADTSPlanar(t, enc.Config{SampleRate: 44100,
			Bitrate: 128000, Channels: 2, DisableTNS: disableTNS}, src)
		dir := t.TempDir()
		p := filepath.Join(dir, "ab.adts")
		if err := os.WriteFile(p, stream, 0o644); err != nil {
			t.Fatal(err)
		}
		dec := ffmpegDecode(t, ffmpeg, p, 2)
		worst := math.Inf(1)
		for c := range 2 {
			worst = math.Min(worst, psnr(src[c], dec[c], 1024))
		}
		return stream, worst
	}

	_, pOn := run(castaSrc, false)
	_, pOff := run(castaSrc, true)
	t.Logf("castanets: TNS on %.2f dB, off %.2f dB (%+.2f)", pOn, pOff, pOn-pOff)
	if pOn < pOff-0.10 {
		t.Errorf("TNS regresses castanets by %.2f dB (allow 0.10)", pOff-pOn)
	}

	sOn, tOn := run(tonal, false)
	sOff, tOff := run(tonal, true)
	t.Logf("tonal: TNS on %.2f dB (%dB), off %.2f dB (%dB)", tOn, len(sOn), tOff, len(sOff))
	if tOn < tOff-0.05 {
		t.Errorf("TNS regresses tonal material by %.2f dB", tOff-tOn)
	}
	// The doc comment's claim, made testable: TNS never fires on this tonal
	// material, so the enabled and disabled streams must be byte-identical.
	// PSNR/size parity alone would let a changed tool decision slip through.
	if !bytes.Equal(sOn, sOff) {
		t.Errorf("TNS changed the tonal stream: on=%d bytes, off=%d bytes",
			len(sOn), len(sOff))
	}
}

// TestPhase4FATEAnalogues mirrors the FATE aac-{tns,pns,is,ms}-encode
// methodology (tests/fate/aac.mak @ d09d5afc3a): fast coder, exactly one
// tool enabled, decode and compare against the source with a stddev
// target in s16 units. The floors are this corpus's measured values plus
// a FATE-style fuzz; the luckynight reference of upstream FATE is not
// redistributable, so the methodology is mirrored on the castanet pair.
func TestPhase4FATEAnalogues(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	src := castanetsChannels(archChanStereo, 44100, 44100*6)

	cases := []struct {
		tool   string
		cfg    enc.Config
		target float64 // measured stddev in s16 units
		fuzz   float64
	}{
		{"tns", enc.Config{DisablePNS: true, DisableMS: true, DisableIS: true}, 438, 7},
		{"pns", enc.Config{DisableTNS: true, DisableMS: true, DisableIS: true}, 438, 7},
		{"is", enc.Config{DisableTNS: true, DisablePNS: true, DisableMS: true}, 438, 7},
		{"ms", enc.Config{DisableTNS: true, DisablePNS: true, DisableIS: true}, 438, 7},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			cfg := tc.cfg
			cfg.SampleRate = 44100
			cfg.Bitrate = 128000
			cfg.Channels = 2
			cfg.Coder = enc.CoderFast
			stream := encodeADTSPlanar(t, cfg, src)
			dir := t.TempDir()
			p := filepath.Join(dir, "fate.adts")
			if err := os.WriteFile(p, stream, 0o644); err != nil {
				t.Fatal(err)
			}
			dec := ffmpegDecode(t, ffmpeg, p, 2)
			var err2 float64
			n := 0
			for c := range 2 {
				for i := range len(dec[c]) - 1024 {
					if i >= len(src[c]) {
						break
					}
					d := float64(src[c][i]) - float64(dec[c][i+1024])
					err2 += d * d
					n++
				}
			}
			sd := 32768.0 * math.Sqrt(err2/float64(n))
			t.Logf("%s-encode stddev %.2f (target %.0f fuzz %.0f, %d bytes)",
				tc.tool, sd, tc.target, tc.fuzz, len(stream))
			if sd > tc.target+tc.fuzz {
				t.Errorf("stddev %.2f above target %.0f + fuzz %.0f", sd, tc.target, tc.fuzz)
			}
		})
	}
}

// TestPhase4NMRStereoSwitchesVsC is the differential gate for issue #92: with
// the default NMR coder, DisableMS and DisableIS must reach the
// pre-quantization stereo decision the way they reach the C's, not merely
// change the output.
//
// Nothing else scores go-aac against the C with a stereo tool switch SET under
// the default coder. TestPhase4ToolsGateVsC runs both sides at their tool
// defaults, and TestPhase4FATEAnalogues sweeps the switches on CoderFast and
// scores against the SOURCE rather than against the C, so it cannot tell a
// faithful port from a plausible one. The arm the issue was about had no
// oracle cell at all, which is how it stayed wrong: nmrDecideStereo was built
// with the two options hardcoded to their defaults, so the switches were
// ignored under the coder that is both the zero value and the recommendation.
//
// The C reads them at three sites inside nmr_decide_stereo (aacenc.c:731,
// 769-770 and 787) and a fourth guarding the call (aacenc.c:1216-1217). That
// fourth site is where the C decides whether to skip nmr_decide_stereo
// entirely, and with it the PNS-stereo reservation that keeps a stereo PNS band
// only where it is clearly decorrelated and clears CanPNS on the rest (the
// noise-like-in-both intersection having already run before the call). A port
// that ran the function anyway would agree on the stereo decision, which is
// empty either way, and diverge on which bands survive as PNS. This oracle cell
// does NOT pin that fourth site, though: the neither case PASSES under a
// deleted-term mutation of the go-aac guard at -0.06% size, well inside the 3%
// bound. TestNMRStereoGuardWiring (tool_wiring_test.go) is what pins that
// case. The other mutation of the same guard, `||` to `&&`, DOES fail here,
// on the nois cell, at +65% stream size.
//
// The cells do not carry equal weight, and it is worth saying which. Measured
// against the unfixed encoder, noms and neither both miss the C's stream size
// by about 39.5%, so they are what gates the fix. nois misses by only 0.16%
// and PASSES unfixed, because I/S reaches just 333 of the roughly thirty
// thousand coded pair-bands on this corpus at this rate (about 1%): too small
// an effect for a 3% size bound to see. It is kept as corroboration that Go
// and C agree with the tool off, not as the gate. TestEncoderToolWiring
// (tool_wiring_test.go) is the gate for DisableIS, because a counter that must
// reach exactly zero does not care how many bands the tool would have claimed.
//
// One bitrate, because what is under test is which options reach which
// decision rather than how the decision varies with rate; TestPhase4ToolsGateVsC
// carries the rate sweep at the tool defaults. The C side is derived from each
// cell's enc.Config by cToolArgs, so the switch under test and its C mirror
// are one literal rather than two.
func TestPhase4NMRStereoSwitchesVsC(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	// castanetsChannels is the single generator for this corpus (see its doc
	// in arch_determinism_test.go); use it rather than open-coding the seeds so
	// this gate cannot drift from the corpus every sibling gate scores against.
	sig := newGateSignal(t, sigStereoCastanets,
		castanetsChannels(archChanStereo, 44100, 44100*gateCastanetSecs))
	const bitrate = 128000

	for _, tc := range []struct {
		name string
		cfg  enc.Config // the switches under test
	}{
		{"noms", enc.Config{DisableMS: true}},
		{"nois", enc.Config{DisableIS: true}},
		{"neither", enc.Config{DisableMS: true, DisableIS: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.SampleRate, cfg.Bitrate, cfg.Channels = 44100, bitrate, 2
			cfg.Coder = enc.CoderNMR
			gateCellVsC(t, ffmpeg, sig, cfg, phase4Bounds)
		})
	}
}
