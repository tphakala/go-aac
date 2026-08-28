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
// Where CI runs this lane, precisely: the differential-oracle job, which is
// ubuntu-latest only, and which runs `go test -count=1 ./...` and then the same
// under `-tags noasm`. So the full matrix is covered on both the SIMD and the
// scalar kernels, but on amd64 alone. macos-latest (arm64) and windows-latest
// run only the race lane, so the two in-range bitrate points never execute
// there. That is an accepted gap rather than an oversight: these tests assert
// contracts, not bytes, and the arch-sensitive cells are pinned separately by
// TestEncoderArchDeterminism, which does run on all three platforms.
const edgeSoakSeconds = 2

func edgeSoakBitrates() []int {
	return []int{edgeBitrateFloor, edgeBitrateLow, edgeBitrateMid, edgeBitrateClamped}
}
