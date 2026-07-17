// SPDX-License-Identifier: LGPL-2.1-or-later
package tables

import (
	"testing"

	"github.com/tphakala/go-aac/internal/fmath"
)

func TestPow43Exact(t *testing.T) {
	for cq := range 8192 {
		want := float32(cq) * fmath.Cbrt32(float32(cq))
		if Pow43[cq] != want {
			t.Fatalf("cq=%d: table=%v want=%v", cq, Pow43[cq], want)
		}
	}
}
