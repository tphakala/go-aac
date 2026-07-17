// SPDX-License-Identifier: LGPL-2.1-or-later
package tables

import "github.com/tphakala/go-aac/internal/fmath"

// Pow43 holds cq^(4/3) for every escape-codebook quantized value cq, which
// the caller clips to [0, 8191]. Built from the exact expression the scalar
// path used (float32(cq) * fmath.Cbrt32(float32(cq))), so a lookup is an
// identity transform of that arithmetic rather than an approximation of it:
// the differential gate must stay byte-identical. Do not regenerate this from
// cbrtf or a closed form; the Go lineage is what the gate has blessed.
var Pow43 [8192]float32

func init() {
	for cq := range Pow43 {
		Pow43[cq] = float32(cq) * fmath.Cbrt32(float32(cq))
	}
}
