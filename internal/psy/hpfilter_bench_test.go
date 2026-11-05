// SPDX-License-Identifier: LGPL-2.1-or-later

package psy

import "testing"

// BenchmarkPsyHpFilter times the canonical scalar filter against the
// dispatched one on a PCM-range block. On the noasm build both run the scalar.
func BenchmarkPsyHpFilter(b *testing.B) {
	firbuf := hpPCMBuffer()
	var out [blockSizeLong]float32
	b.Run("scalar", func(b *testing.B) {
		for b.Loop() {
			psyHpFilterScalar(firbuf, &out)
		}
	})
	b.Run("dispatched", func(b *testing.B) {
		for b.Loop() {
			psyHpFilter(firbuf, &out)
		}
	})
}
