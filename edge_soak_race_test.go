// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build race

package aac

// edgeSoakSeconds and edgeSoakBitrates size the edge-config tests. The race
// build runs them smaller, because the race detector cannot find anything here:
// all three tests are single-goroutine start to finish, so there is no
// interleaving to detect, and what they assert (rate control converges, the
// drain frame-counts, every stream decodes) is arithmetic and bitstream
// structure, identical in both builds.
//
// The cost is worth stating exactly, because this is the lane CI runs most. The
// `test` job runs `go test -race ./...` AND `go test -race -tags noasm ./...`
// on each of ubuntu, macos and windows, so this lane executes six times per CI
// run. The non-race matrix is also six (the `test` job's non-race step on the
// same three OSes, plus the oracle job's three passes), but four of those are
// ubuntu-only, so this is still the lane that runs on the most machines.
// Halving the input length here shortens all three tests; dropping to the two
// bitrate ENDS shortens the soak specifically. Measured together on a 16-thread
// desktop, the three tests run about 33 s in this configuration against about
// 63 s at full size.
//
// The ends are kept rather than the middle because the pathological floor and
// the clamped ceiling are where rate control is under the most stress and a
// hang or a malformed frame is most likely. The in-range points (32 kb/s and
// 128 kb/s) are dropped here and run at full length in the non-race lane; see
// edge_soak_norace_test.go for exactly which CI jobs that is now, on every
// supported platform rather than amd64 alone. Keeping the encodes running under
// the detector at all is deliberate: it is what proves the encoder stays
// race-clean on these configurations.
const edgeSoakSeconds = 1

func edgeSoakBitrates() []int {
	return []int{edgeBitrateFloor, edgeBitrateClamped}
}

// toolWiringSkipRace skips TestEncoderToolWiring under the race detector.
//
// Unlike the three tests sized above, this one is not shortened but dropped,
// because shortening it does not work: TestEncoderToolWiring needs a fixed
// two-second input to keep TNS firing often enough for its non-vacuity check to
// mean anything (at one second NMR is down to a single active channel-frame;
// see toolWiringSeconds), so the lever the others use is unavailable. Left in,
// it costs about 25 s per lane against 2 s in the non-race build, and this lane
// runs six times per CI run (three OSes, each with and without -tags noasm), so
// it would add roughly 2.5 minutes to a suite whose race lane currently runs
// about 2 minutes.
//
// That buys nothing. The test is single-goroutine start to finish, so the
// detector has no interleaving to find, and what it asserts (that DisableTNS,
// DisablePNS and DisableIS reach the encoder) is a wiring property, identical in
// every build. The encoder's race-cleanliness on these configurations is
// separately covered: TestEncoderEdgeConfigSoak, TestEncoderEdgeConfigReset and
// TestEncoderMidSideWiring all keep encoding under the detector.
//
// It does not rot from being skipped here. The `test` job runs a non-race pass
// on ubuntu, macos and windows, so the gate still executes on every platform
// this project supports, and the differential-oracle job runs it again on all
// three SIMD build states (default, -tags noasm, and SIMD_DISABLE=all).
const toolWiringSkipRace = true
