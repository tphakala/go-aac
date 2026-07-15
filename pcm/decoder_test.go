// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// decoderTestdata is the package-owned decoder corpus: the ADTS streams plus a
// refs.sha256 manifest of the pinned oracle's interleaved s16le decode for each.
const decoderTestdata = "testdata/decoder"

const (
	streamMono   = "sine_m8_24k"
	streamStereo = "tonal_s48_128k"
)

// decoderRefs loads the sha256 manifest (one "hex  name.adts" line per stream).
func decoderRefs(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join(decoderTestdata, "refs.sha256"))
	if err != nil {
		t.Fatalf("open refs manifest: %v", err)
	}
	defer func() { _ = f.Close() }()
	refs := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		refs[fields[1]] = fields[0]
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan refs manifest: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("empty refs manifest")
	}
	return refs
}

// decodeStream decodes a whole ADTS file through the public pcm.Decoder into
// interleaved s16le bytes.
func decodeStream(t *testing.T, adts string) []byte {
	t.Helper()
	f, err := os.Open(adts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	d, err := NewDecoder(f)
	if err != nil {
		t.Fatalf("NewDecoder(%s): %v", adts, err)
	}
	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo(%s): %v", adts, err)
	}
	return buf.Bytes()
}

// TestDecodeStreamHermetic verifies the public decoder is byte-stable against
// the committed oracle-derived sha256 manifest. It runs everywhere with no
// oracle, so CI catches any cross-platform regression in framing, the S32P>>16
// conversion, or interleaving. The full byte diff against a live oracle is in
// TestDecodeStreamVsOracle.
func TestDecodeStreamHermetic(t *testing.T) {
	refs := decoderRefs(t)
	for name, want := range refs {
		t.Run(name, func(t *testing.T) {
			got := decodeStream(t, filepath.Join(decoderTestdata, name))
			sum := sha256.Sum256(got)
			if h := hex.EncodeToString(sum[:]); h != want {
				t.Fatalf("sha256 mismatch: got %s want %s (%d bytes)", h, want, len(got))
			}
		})
	}
}

// TestDecodeStreamVsOracle proves the public decoder output is byte-identical
// to the pinned ffmpeg aac_fixed s16le decode, and re-confirms the committed
// manifest still matches the oracle (so a stale manifest cannot hide a
// regression). Gated on GOAAC_FFMPEG so CI without the oracle skips it.
func TestDecodeStreamVsOracle(t *testing.T) {
	ff := ffmpegBin(t)
	refs := decoderRefs(t)
	for name, wantHash := range refs {
		t.Run(name, func(t *testing.T) {
			adts := filepath.Join(decoderTestdata, name)
			cmd := exec.Command(ff, "-loglevel", "error", "-bitexact", "-c:a",
				"aac_fixed", "-i", adts, "-bitexact", "-f", "s16le", "-")
			want, err := cmd.Output()
			if err != nil {
				t.Fatalf("oracle decode %s: %v", adts, err)
			}
			// Manifest honesty: the committed hash must match the live oracle.
			sum := sha256.Sum256(want)
			if h := hex.EncodeToString(sum[:]); h != wantHash {
				t.Fatalf("manifest stale for %s: oracle %s != committed %s", name, h, wantHash)
			}
			got := decodeStream(t, adts)
			if len(got) != len(want) {
				t.Fatalf("length mismatch: got %d want %d", len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("byte %d: got 0x%02x want 0x%02x", i, got[i], want[i])
				}
			}
			t.Logf("BYTE-IDENTICAL: %d bytes", len(got))
		})
	}
}
