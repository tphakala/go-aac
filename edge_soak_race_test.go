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
// run, against two for the full non-race matrix. Halving the input length here
// shortens all three tests; dropping to the two bitrate ENDS shortens the soak
// specifically. Measured together on a 16-thread desktop, the three tests run
// about 33 s in this configuration against about 63 s at full size.
//
// The ends are kept rather than the middle because the pathological floor and
// the clamped ceiling are where rate control is under the most stress and a
// hang or a malformed frame is most likely. The in-range points (32 kb/s and
// 128 kb/s) are dropped here and run at full length in the non-race lane; see
// edge_soak_norace_test.go for exactly which CI job that is, and for the arm64
// and Windows gap it leaves. Keeping the encodes running under the detector at
// all is deliberate: it is what proves the encoder stays race-clean on these
// configurations.
const edgeSoakSeconds = 1

func edgeSoakBitrates() []int {
	return []int{edgeBitrateFloor, edgeBitrateClamped}
}
