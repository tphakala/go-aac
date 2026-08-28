// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build race

package pcm

// encodeReuseMaxAllocs bounds TestEncodeInterleavedReuseAllocs. Under -race the
// detector instruments sync.Pool, so AllocsPerRun stops reflecting real
// allocation and becomes scheduler and GC noise: single digits in isolation,
// up to ~14 under full-suite race load. A numeric ceiling here would only be a
// magic number a loaded runner could still exceed, and it buys no coverage the
// non-race lane does not already give: that lane (this same test at bound 0,
// run by the `test` job's non-race step on all three OSes and by the CI oracle
// job) plus the pool-free internal/psy TestResetNoAllocSameChannels (0 even
// under -race) catch a dropped-pool or reverted-psy regression at zero
// tolerance, and such a regression costs
// hundreds of allocs anyway. So the count is not asserted in the race build:
// the sentinel below disables the bound while the encodes still run, keeping
// the reuse path under the race detector.
const encodeReuseMaxAllocs = -1
