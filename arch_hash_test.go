// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
)

// Signal names for the arch-hash corpus, hoisted to constants so the labels
// are not repeated string literals (goconst).
const (
	archSigTonal     = "tonal"
	archSigCastanets = "castanets"
)

// TestArchHashCorpus is a diagnostic for issue #59: encode the synthetic stereo
// corpus across every coder and bitrate, hash each ADTS stream, and print one
// line per case. Run it under native arm64 and under amd64 and diff the output.
// Env-gated so it never runs in a normal suite.
//
// Caveat: the tonal signal (synthStereoNMR) produces float32 input samples that
// differ across architectures, so its tonal ADTS hashes differ for reasons in
// the SIGNAL GENERATOR, not the encoder. The cause is float64 FMA contraction on
// arm64 (gc fuses a float64 a*b+c that default amd64 does not) in the
// generator's math, including math.Sin's internal polynomial and its own
// a*Sin(x)+b*Sin(y) sum. The castanets input happens to round to identical
// float32 on both arches (verified), so TestEncoderArchDeterminism uses it as
// the real cross-arch encoder-determinism guard.
func TestArchHashCorpus(t *testing.T) {
	if os.Getenv("GOAAC_ARCHHASH") == "" {
		t.Skip("set GOAAC_ARCHHASH to run the arch-hash reproduction")
	}
	tonal := synthStereoNMR(44100*8, 44100)
	casta := synthCastanets(44100*6, 44100, 0x0badcafe, 0)
	castaR := synthCastanets(44100*6, 44100, 0x5eed1234, 137)

	signals := []struct {
		name string
		src  [][]float32
	}{
		{archSigTonal, tonal},
		{archSigCastanets, [][]float32{casta, castaR}},
	}
	coders := []struct {
		name string
		kind enc.CoderKind
	}{
		{coderNMR, enc.CoderNMR},
		{coderTwoLoop, enc.CoderTwoLoop},
		{coderFast, enc.CoderFast},
	}
	bitrates := []int{96000, 128000, 192000}

	lines := make([]string, 0, len(signals)*len(coders)*len(bitrates))
	for _, sig := range signals {
		for _, c := range coders {
			for _, br := range bitrates {
				stream := encodeADTSPlanar(t,
					enc.Config{SampleRate: 44100, Bitrate: br, Channels: 2, Coder: c.kind},
					sig.src)
				sum := sha256.Sum256(stream)
				lines = append(lines, fmt.Sprintf("%-8s %-10s %6d  %s  %d",
					c.name, sig.name, br, hex.EncodeToString(sum[:]), len(stream)))
			}
		}
	}
	slices.Sort(lines)
	for _, l := range lines {
		fmt.Println(l)
	}
}
