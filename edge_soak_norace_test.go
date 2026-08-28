// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !race

package aac

// edgeSoakSeconds and edgeSoakBitrates size the edge-config tests. Note the
// asymmetry: edgeSoakBitrates is read only by TestEncoderEdgeConfigSoak, but
// edgeSoakSeconds sets the input length for TestEncoderEdgeConfigReset and
// TestEncoderMidSideWiring as well, so all three shorten in the race build.
// GOAAC_ENC_SOAK, conversely, rescales only the soak.
//
// In a normal build the soak runs its full cross product over a two-second
// input: 86 frames at 44100 Hz, four castanet transients, so every cell reaches
// rate-control steady state rather than only encoder start-up.
//
// Where CI runs this lane, precisely: the `test` job's non-race step, on all
// three of ubuntu-latest, macos-latest and windows-latest, and the
// differential-oracle job, which is ubuntu-latest only and runs
// `go test -count=1 ./...` three times, once on the default build, once under
// `-tags noasm` and once under `SIMD_DISABLE=all`. So the full matrix is covered
// on every supported platform, and on amd64 additionally against all three SIMD
// build states.
//
// It was not always so. Until the non-race step existed, the oracle job was the
// only non-race lane, so the two in-range bitrate points ran on amd64 alone;
// macos and windows saw only the reduced race sweep. That was an accepted gap
// rather than an oversight, on the reasoning that these tests assert contracts
// rather than bytes and the arch-sensitive cells are pinned separately by
// TestEncoderArchDeterminism. Measuring the gap's cost at about 20 s per runner
// made closing it cheaper than continuing to justify it.
const edgeSoakSeconds = 2

func edgeSoakBitrates() []int {
	return []int{edgeBitrateFloor, edgeBitrateLow, edgeBitrateMid, edgeBitrateClamped}
}

// toolWiringSkipRace keeps TestEncoderToolWiring in the non-race lane. See the
// race build's copy of this constant for why it is skipped there.
const toolWiringSkipRace = false
