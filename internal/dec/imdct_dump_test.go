// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tphakala/go-aac/internal/tx"
	"github.com/tphakala/go-aac/internal/window"
)

// TestIMDCTDump regenerates tools/cimdct's dump from the same LCG seed and
// diffs it byte for byte against the committed C output: integer windows,
// MDCT twiddle/permutation tables, IMDCT input/output pairs for 1024 and
// 128, and chained imdct_and_windowing frames over all four window
// sequences. Zero tolerance: any integer differing anywhere fails.
func TestIMDCTDump(t *testing.T) {
	for _, name := range []string{"imdct_seed1", "imdct_seed2"} {
		t.Run(name, func(t *testing.T) {
			// .imdct.gz, not .dump.gz: TestSymbolStreamsVsC claims
			// testdata/*.dump* as its stream corpus.
			want := readGzLines(t, filepath.Join("testdata", name+".imdct.gz"))
			compareIMDCTDump(t, name, want)
		})
	}
}

// TestIMDCTDumpCorpus checks every fresh tools/cimdct dump in the directory
// named by IMDCT_CORPUS (uncompressed .dump files); the committed fixtures
// already run unconditionally above. Regeneration sweep:
//
//	for s in $(seq 1 16); do ./cimdct $DIR/seed$s.dump $s; done
func TestIMDCTDumpCorpus(t *testing.T) {
	dir := os.Getenv("IMDCT_CORPUS")
	if dir == "" {
		t.Skip("IMDCT_CORPUS not set")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.dump"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no dumps in %s (err=%v)", dir, err)
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			compareIMDCTDump(t, filepath.Base(p), want)
		})
	}
}

func compareIMDCTDump(t *testing.T, name string, want []string) {
	t.Helper()
	if len(want) == 0 || !strings.HasPrefix(want[0], "SEED ") {
		t.Fatalf("fixture %s: missing SEED line", name)
	}
	var seed uint32
	if _, err := fmt.Sscanf(want[0], "SEED %d", &seed); err != nil {
		t.Fatalf("fixture %s: bad SEED line %q", name, want[0])
	}

	got := strings.Split(strings.TrimSuffix(generateIMDCTDump(seed), "\n"), "\n")

	n := len(got)
	if len(want) != n {
		t.Errorf("line count: got %d, want %d", n, len(want))
		if len(want) < n {
			n = len(want)
		}
	}
	values := 0
	for i := range n {
		if got[i] != want[i] {
			t.Fatalf("line %d differs:\ngot:  %.200s\nwant: %.200s",
				i+1, got[i], want[i])
		}
		values += strings.Count(got[i], " ")
	}
	t.Logf("%s: %d lines byte-identical, %d space-separated fields", name, n, values)
}

func readGzLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// lcg mirrors the harness generator: the Numerical Recipes constants, value
// taken as the signed state after an arithmetic shift.
type lcg struct{ state uint32 }

func (l *lcg) next(shift int) int32 {
	l.state = l.state*1664525 + 1013904223
	return int32(l.state) >> shift
}

func writeInts(b *strings.Builder, tag string, v []int32) {
	b.WriteString(tag)
	fmt.Fprintf(b, " %d", len(v))
	for _, x := range v {
		fmt.Fprintf(b, " %d", x)
	}
	b.WriteByte('\n')
}

// generateIMDCTDump reproduces tools/cimdct's whole output from the Go port.
// The TXCFG lines carry the C codelet names as pinned expectations: the port
// mirrors mdct_inv_int32_c over fft512/fft64_ns_int32_c, and if the oracle
// ever selects something else the byte diff fails here.
func generateIMDCTDump(seed uint32) string {
	var b strings.Builder
	rng := lcg{state: seed}
	fmt.Fprintf(&b, "SEED %d\n", seed)

	writeInts(&b, "WIN kbd_long_1024", window.KBDLong1024Fixed)
	writeInts(&b, "WIN kbd_short_128", window.KBDShort128Fixed)
	writeInts(&b, "WIN sine_1024", window.Sine1024Fixed)
	writeInts(&b, "WIN sine_128", window.Sine128Fixed)

	var dsp dspState
	dsp.init()
	dumpTxCfg(&b, "mdct1024", dsp.mdct1024, 1024, 0.125, "fft512_ns_int32_c", 512)
	dumpTxCfg(&b, "mdct128", dsp.mdct128, 128, 1, "fft64_ns_int32_c", 64)

	for _, size := range []int{1024, 128} {
		mdct := dsp.mdct1024
		if size == 128 {
			mdct = dsp.mdct128
		}
		in := make([]int32, size)
		out := make([]int32, size)
		for f := range 8 {
			shift := 0
			if f&1 != 0 {
				shift = 8
			}
			for i := range in {
				in[i] = rng.next(shift)
			}
			clear(out)
			mdct.Transform(out, in)
			fmt.Fprintf(&b, "IMDCT n=%d frame=%d shift=%d\n", size, f, shift)
			writeInts(&b, "IN", in)
			writeInts(&b, "OUT", out)
		}
	}

	// The harness's synthetic frame chain (ws/kb schedules must match
	// run_iaw in tools/cimdct exactly).
	wsChain := []int{0, 1, 2, 2, 3, 0, 0, 1, 3, 2, 3, 0}
	kbChain := []int{0, 1, 1, 0, 1, 1, 0, 0, 1, 1, 0, 1}
	var sce SCE
	prevWS, prevKB := 0, 0
	for f := range 24 {
		ws := wsChain[f%len(wsChain)]
		kb := kbChain[f%len(kbChain)]
		shift := 0
		if f%3 != 0 {
			shift = 8
		}
		sce.ICS.WindowSequence[0] = ws
		sce.ICS.WindowSequence[1] = prevWS
		sce.ICS.UseKBWindow[0] = kb
		sce.ICS.UseKBWindow[1] = prevKB
		for i := range sce.Coeffs {
			sce.Coeffs[i] = rng.next(shift)
		}
		fmt.Fprintf(&b, "IAW frame=%d ws=%d pws=%d kb=%d pkb=%d shift=%d\n",
			f, ws, prevWS, kb, prevKB, shift)
		writeInts(&b, "COEF", sce.Coeffs[:])
		dsp.imdctAndWindowing(&sce)
		writeInts(&b, "OUT", sce.Output[:])
		writeInts(&b, "SAVED", sce.Saved[:512])
		prevWS, prevKB = ws, kb
	}

	b.WriteString("END\n")
	return b.String()
}

func dumpTxCfg(b *strings.Builder, name string, m *tx.IMDCT, n int, scaleD float64, sub string, sublen int) {
	fmt.Fprintf(b, "TXCFG %s len=%d inv=1 scale_d=%.17g cd=mdct_inv_int32_c sub=%s sublen=%d\n",
		name, n, scaleD, sub, sublen)
	mp, expRe, expIm := m.Tables()
	b.WriteString("TXMAP")
	fmt.Fprintf(b, " %d", len(mp))
	for _, x := range mp {
		fmt.Fprintf(b, " %d", x)
	}
	b.WriteByte('\n')
	b.WriteString("TXEXP")
	fmt.Fprintf(b, " %d", len(expRe))
	for i := range expRe {
		fmt.Fprintf(b, " %d %d", expRe[i], expIm[i])
	}
	b.WriteByte('\n')
}
