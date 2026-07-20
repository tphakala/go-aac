// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !race

package pcm

// encodeReuseMaxAllocs bounds TestEncodeInterleavedReuseAllocs. In a normal
// build a warmed pool with the reused psy context (issue #41) is exactly
// allocation-free, so the ceiling is 0: any allocation means a dropped pool or
// a reverted psy retention.
const encodeReuseMaxAllocs = 0
