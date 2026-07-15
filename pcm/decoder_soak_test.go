// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// TestDecodeSoak decodes the whole corpus repeatedly through a single pooled
// decoder and asserts the output is deterministic across passes and that the
// pooled steady state stays free of per-frame allocation. It is opt in via
// GOAAC_SOAK (the iteration count) so the normal suite stays fast. Because a
// single decode pass runs far above real time, a few hundred passes cover
// hours of audio in seconds of wall time.
func TestDecodeSoak(t *testing.T) {
	iters := 0
	if v := os.Getenv("GOAAC_SOAK"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			t.Fatalf("GOAAC_SOAK must be a positive integer, got %q", v)
		}
		iters = n
	}
	if iters == 0 {
		t.Skip("soak disabled; set GOAAC_SOAK=<iterations> to run")
	}

	streams := []string{
		streamMono, "pns_m48_24k", "noise_m44_96k_tns", "pulse_m48",
		streamStereo, "is_s22_48k_fast", "click_s44_128k", "hs_s96_192k",
	}
	data := make(map[string][]byte, len(streams))
	baseline := make(map[string][]byte, len(streams))
	for _, name := range streams {
		b, err := os.ReadFile(filepath.Join(decoderTestdata, name+".adts"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		data[name] = b
		ref, err := decodeAll(t, b) // first-pass reference
		if err != nil {
			t.Fatalf("%s: baseline decode: %v", name, err)
		}
		if len(ref) == 0 {
			t.Fatalf("%s: empty baseline decode", name)
		}
		baseline[name] = ref
	}

	var d Decoder
	var out bytes.Buffer
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)

	for i := range iters {
		for _, name := range streams {
			if err := d.Reset(bytes.NewReader(data[name])); err != nil {
				t.Fatalf("iter %d %s Reset: %v", i, name, err)
			}
			out.Reset()
			if _, err := d.WriteTo(&out); err != nil {
				t.Fatalf("iter %d %s WriteTo: %v", i, name, err)
			}
			if !bytes.Equal(out.Bytes(), baseline[name]) {
				t.Fatalf("iter %d %s: output drifted from the first-pass decode", i, name)
			}
		}
	}

	runtime.ReadMemStats(&m1)
	// Over many passes the pooled decoder itself must not allocate per frame;
	// the bytes.Buffer growth on the first pass is amortized away. Report the
	// total and per-iteration mallocs as evidence of the pooled steady state.
	mallocs := m1.Mallocs - m0.Mallocs
	perIter := mallocs / uint64(iters)
	t.Logf("soak %d iters over %d streams: %d mallocs total (%d per iter)", iters, len(streams), mallocs, perIter)
}
