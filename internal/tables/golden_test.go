// SPDX-License-Identifier: LGPL-2.1-or-later
package tables

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// readRecords parses testdata/ctables.bin: per record a uint8 name length,
// the name, a uint32 little-endian byte count and the raw data (the format
// tools/gentables bin mode writes).
func readRecords(t *testing.T, path string) map[string][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	recs := map[string][]byte{}
	for off := 0; off < len(raw); {
		nl := int(raw[off])
		off++
		name := string(raw[off : off+nl])
		off += nl
		n := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		recs[name] = raw[off : off+n]
		off += n
	}
	return recs
}

func TestTablesMatchC(t *testing.T) {
	recs := readRecords(t, "testdata/ctables.bin")
	got := serializedTables()
	if len(recs) != len(got) {
		t.Fatalf("fixture has %d records, Go has %d tables", len(recs), len(got))
	}
	for name, want := range recs {
		g, ok := got[name]
		if !ok {
			t.Errorf("%s: present in fixture, missing in Go", name)
			continue
		}
		if bytes.Equal(g, want) {
			continue
		}
		// The two runtime-computed tables are allowed the validation.md
		// table tolerance of 1e-7 relative; everything else is generated
		// literal data and must be byte-identical.
		if name != pow2SFName && name != pow34SFName {
			t.Errorf("%s: Go table differs from C dump", name)
			continue
		}
		if len(g) != len(want) {
			t.Errorf("%s: length %d, want %d", name, len(g), len(want))
			continue
		}
		maxRel := 0.0
		for i := 0; i+4 <= len(g); i += 4 {
			a := float64(math.Float32frombits(binary.LittleEndian.Uint32(g[i:])))
			b := float64(math.Float32frombits(binary.LittleEndian.Uint32(want[i:])))
			if d := math.Abs(a-b) / math.Abs(b); d > maxRel {
				maxRel = d
			}
		}
		if maxRel > 1e-7 {
			t.Errorf("%s: max relative diff %g > 1e-7", name, maxRel)
		} else {
			t.Logf("%s: within tolerance, max relative diff %g", name, maxRel)
		}
	}
}

// TestPowTablesBitExact reports (and pins) that the Go init reproduces the C
// float arithmetic exactly; if a future toolchain breaks bit-exactness the
// tolerance-based TestTablesMatchC above is the contract, and this test's
// message tells us the drift started.
func TestPowTablesBitExact(t *testing.T) {
	recs := readRecords(t, "testdata/ctables.bin")
	for _, name := range []string{pow2SFName, pow34SFName} {
		g := serializedTables()[name]
		if !bytes.Equal(g, recs[name]) {
			t.Errorf("%s: not bit-exact vs C dump (tolerance test governs)", name)
		} else {
			t.Logf("%s: bit-exact vs C dump", name)
		}
	}
}
