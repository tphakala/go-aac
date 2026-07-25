// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
)

// cEncodeArgs runs the pinned C encoder with extra codec options.
func cEncodeArgs(t *testing.T, ffmpeg, rawPath string, rate, ch, bitrate int,
	outPath string, extra ...string) []byte {
	t.Helper()
	args := make([]string, 0, 20+len(extra))
	args = append(args, "-v", "error", "-y", "-f", "f32le",
		"-ar", fmt.Sprint(rate), "-ac", fmt.Sprint(ch), "-i", rawPath,
		"-c:a", "aac")
	args = append(args, extra...)
	args = append(args, "-b:a", fmt.Sprint(bitrate), "-flags", "+bitexact",
		"-f", "adts", outPath)
	cmd := exec.Command(ffmpeg, args...)
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("C encode: %v %q", err, out)
	}
	stream, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

// cEncodeTools runs the pinned C encoder with a chosen coder at its tool
// DEFAULTS (tns/pns/is/ms all on), the Phase 4 same-settings reference.
func cEncodeTools(t *testing.T, ffmpeg, rawPath string, rate, ch, bitrate int,
	coder, outPath string) []byte {
	t.Helper()
	return cEncodeArgs(t, ffmpeg, rawPath, rate, ch, bitrate, outPath,
		"-aac_coder", coder)
}

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

// TestPhase4ToolsGateVsC is the Phase 4 gate (issue #6): with TNS, PNS,
// I/S and M/S active on BOTH sides (all tool defaults), for BOTH the NMR
// and twoloop coders, the Go stream size must land within 3% of the C
// encoder's and the decoded PSNR within 0.5 dB of the C encoder's own
// PSNR per case, and the Go streams must decode cleanly under the pinned
// ffmpeg.
func TestPhase4ToolsGateVsC(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	tonal := synthStereoNMR(44100*8, 44100)
	casta := synthCastanets(44100*gateCastanetSecs, 44100, 0x0badcafe, 0)
	castaR := synthCastanets(44100*gateCastanetSecs, 44100, 0x5eed1234, 137)
	for _, coder := range []struct {
		name string
		kind enc.CoderKind
	}{
		{coderNMR, enc.CoderNMR},
		{coderTwoLoop, enc.CoderTwoLoop},
	} {
		for _, sig := range []struct {
			name string
			src  [][]float32
		}{
			{"stereo tonal", tonal},
			{sigStereoCastanets, [][]float32{casta, castaR}},
		} {
			for _, br := range []int{96000, 128000, 192000} {
				t.Run(fmt.Sprintf("%s %s %dk", coder.name, sig.name, br/1000), func(t *testing.T) {
					dir := t.TempDir()
					rawPath := filepath.Join(dir, "src.f32")
					writeRawF32(t, rawPath, sig.src)
					cStream := cEncodeTools(t, ffmpeg, rawPath, 44100, 2, br,
						coder.name, filepath.Join(dir, "c.adts"))
					goStream := encodeADTSPlanar(t,
						enc.Config{SampleRate: 44100, Bitrate: br, Channels: 2,
							Coder: coder.kind}, sig.src)

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
					meanDelta := 0.0
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
						meanDelta += (pg - pc) / 2
					}
					// M/S and I/S trade quantization error between the two
					// channels, so per-channel PSNR is not stable under a
					// stereo-tool decision flip; the gate is the per-case
					// MEAN delta, with a worst-channel backstop. One rule
					// for all twelve cases; see gateCastanetSecs for why the
					// reference is reproducible enough to assert against.
					const meanBound, worstBound = -0.5, -1.0
					if meanDelta < meanBound {
						t.Errorf("mean PSNR %.2f dB below the C encoder's, gate allows %.1f dB",
							meanDelta, meanBound)
					}
					if worstDelta < worstBound {
						t.Errorf("worst-channel PSNR %.2f dB below the C encoder's, backstop is %.1f dB",
							worstDelta, worstBound)
					}
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
	casta := synthCastanets(44100*6, 44100, 0x0badcafe, 0)
	castaR := synthCastanets(44100*6, 44100, 0x5eed1234, 137)
	tonal := synthStereoNMR(44100*8, 44100)

	run := func(src [][]float32, disableTNS bool) ([]byte, float64) {
		stream := encodeADTSPlanar(t, enc.Config{SampleRate: 44100,
			Bitrate: 128000, Channels: 2, DisableTNS: disableTNS}, src)
		dir := t.TempDir()
		p := filepath.Join(dir, "ab.adts")
		if err := os.WriteFile(p, stream, 0o644); err != nil {
			t.Fatal(err)
		}
		dec := ffmpegDecode(t, ffmpeg, p)
		worst := math.Inf(1)
		for c := range 2 {
			d := make([]float32, len(dec)/2)
			for i := range d {
				d[i] = dec[i*2+c]
			}
			worst = math.Min(worst, psnr(src[c], d, 1024))
		}
		return stream, worst
	}

	castaSrc := [][]float32{casta, castaR}
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
	casta := synthCastanets(44100*6, 44100, 0x0badcafe, 0)
	castaR := synthCastanets(44100*6, 44100, 0x5eed1234, 137)
	src := [][]float32{casta, castaR}

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
			dec := ffmpegDecode(t, ffmpeg, p)
			var err2 float64
			n := 0
			for c := range 2 {
				for i := range len(dec)/2 - 1024 {
					if i >= len(src[c]) {
						break
					}
					d := float64(src[c][i]) - float64(dec[(i+1024)*2+c])
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
