// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build race

package pcm

// encodeReuseMaxAllocs bounds TestEncodeInterleavedReuseAllocs. Under -race the
// detector instruments sync.Pool and inflates AllocsPerRun to a single-digit
// count (measured 7-9), so the ceiling is relaxed. The exact zero-allocation
// gate lives in the pool-free internal/psy TestResetNoAllocSameChannels, which
// stays 0 even under -race.
const encodeReuseMaxAllocs = 12
