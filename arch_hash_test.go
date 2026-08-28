// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
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

// hashPlanar returns the SHA-256 of the raw little-endian float32 bytes of a
// planar frame, channel by channel. It hashes the INPUT PCM (not encoder
// output) so a non-arch-stable input signal is caught directly, independent of
// the encoder: if these lines differ across arches, the corpus input itself is
// not reproducible and its output goldens must not be trusted.
func hashPlanar(src [][]float32) string {
	h := sha256.New()
	var b [4]byte
	for _, ch := range src {
		for _, v := range ch {
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
			h.Write(b[:])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestArchHashCorpus is the diagnostic and golden generator for issue #59:
// encode the synthetic corpus and hash each ADTS stream, printing one sorted
// line per case, plus input-PCM hash lines for cross-arch input verification.
// Run it under native arm64 and under amd64 and diff the output; it is the tool
// used to (re)generate and verify the TestEncoderArchDeterminism goldens. It
// drives off archCases so it covers exactly the gated matrix. Env-gated so it
// never runs in a normal suite.
//
// Caveat: the tonal signal (synthStereoNMR) produces float32 input samples that
// differ across architectures, so its tonal ADTS hashes differ for reasons in
// the SIGNAL GENERATOR, not the encoder (float64 FMA contraction on arm64 in the
// generator's math, including math.Sin's internal polynomial and its own
// a*Sin(x)+b*Sin(y) sum). It is printed for reproduction of the issue #59
// investigation and is never gated. The castanets inputs round to identical
// float32 on both arches at 44100 and 48000 (confirm via the "input" lines), so
// TestEncoderArchDeterminism gates only those.
func TestArchHashCorpus(t *testing.T) {
	if os.Getenv("GOAAC_ARCHHASH") == "" {
		t.Skip("set GOAAC_ARCHHASH to run the arch-hash reproduction")
	}

	// 4 input rows + 9 tonal diagnostic rows + one row per gated case.
	lines := make([]string, 0, 13+len(archCases()))

	// Input-PCM arch-stability lines: hash the raw float32 bytes of each
	// distinct castanets input the gate uses. These must match across arches.
	for _, in := range []struct {
		chLabel string
		rate    int
	}{
		{archChanStereo, 44100},
		{archChanMono, 44100},
		{archChanStereo, 48000},
		{archChanMono, 48000},
	} {
		src := archCastanetsInput(in.chLabel, in.rate)
		lines = append(lines, fmt.Sprintf("input    %-9s %-6s %5d      %s",
			archSigCastanets, in.chLabel, in.rate, hashPlanar(src)))
	}

	// Tonal diagnostic rows (stereo/44100 only), never gated.
	tonal := synthStereoNMR(44100*8, 44100)
	for _, c := range archCoders {
		for _, br := range []int{96000, 128000, 192000} {
			stream := encodeADTSPlanar(t,
				enc.Config{SampleRate: 44100, Bitrate: br, Channels: 2, Coder: c.kind},
				tonal)
			sum := sha256.Sum256(stream)
			lines = append(lines, fmt.Sprintf("adts     %-8s %-9s %-6s %5d %6d  %s",
				c.name, archSigTonal, archChanStereo, 44100, br, hex.EncodeToString(sum[:])))
		}
	}

	// Castanets ADTS rows: exactly the gated matrix.
	for _, c := range archCases() {
		stream := encodeADTSPlanar(t,
			enc.Config{SampleRate: c.rate, Bitrate: c.bitrate, Channels: c.channels, Coder: c.kind},
			archCastanetsInput(c.chLabel, c.rate))
		sum := sha256.Sum256(stream)
		lines = append(lines, fmt.Sprintf("adts     %-8s %-9s %-6s %5d %6d  %s",
			c.coder, archSigCastanets, c.chLabel, c.rate, c.bitrate, hex.EncodeToString(sum[:])))
	}

	slices.Sort(lines)
	for _, l := range lines {
		fmt.Println(l)
	}
}
