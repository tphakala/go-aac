// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/tables"
)

// writeCoefRow emits a tagged int32 row exactly like cdec2's dump_ints.
func writeCoefRow(b *strings.Builder, tag string, v []int32) {
	b.WriteString(tag)
	fmt.Fprintf(b, " %d", len(v))
	for _, x := range v {
		fmt.Fprintf(b, " %d", x)
	}
	b.WriteByte('\n')
}

// goTablesDump regenerates tools/cdec2's "tables" dump: the cbrt fixed table
// and the fixed_sqrt probe, validated once against the oracle (the real-C
// arbiter for the computed cbrt table and the integer sqrt).
func goTablesDump() string {
	var b strings.Builder
	b.WriteString("CBRT 8192")
	for _, v := range tables.CbrtTabFixed {
		fmt.Fprintf(&b, " %d", uint32(v))
	}
	b.WriteByte('\n')
	b.WriteString("FSQ")
	for x := int64(0); x <= 0x7fffffff; {
		fmt.Fprintf(&b, " %d:%d", int32(x), fixedSqrt(int32(x), 31))
		if x == 0 {
			x = 1
		} else {
			x = x + (x >> 3) + 1
		}
	}
	b.WriteString("\nEND\n")
	return b.String()
}

// goD2Dump regenerates tools/cdec2's per-stream D2 reconstruction dump from
// the Go decoder: per frame the dequantized spectrum (DQ), the post-TNS
// spectrum at IMDCT entry (SPEC) and the clipped S32P PCM (PCM). Byte-identical
// to the C dump under zero tolerance.
func goD2Dump(t *testing.T, data []byte, raw bool, asc []byte) string {
	t.Helper()
	var b strings.Builder

	var d *Decoder
	if raw {
		var err error
		if d, err = NewRaw(asc); err != nil {
			t.Fatalf("NewRaw: %v", err)
		}
	} else {
		d = NewADTS()
	}

	pos, fidx := 0, 0
	for pos < len(data) {
		var frame []byte
		if raw {
			if pos+2 > len(data) {
				break
			}
			flen := int(data[pos])<<8 | int(data[pos+1])
			pos += 2
			if pos+flen > len(data) {
				break
			}
			frame = data[pos : pos+flen]
			pos += flen
		} else {
			sync := FindSync(data, pos)
			if sync < 0 {
				break
			}
			pos = sync
			hdr, err := ParseADTS(bits.NewReader(data[pos:]))
			if err != nil {
				t.Fatalf("frame %d: %v", fidx, err)
			}
			if pos+hdr.FrameLength > len(data) {
				break
			}
			frame = data[pos : pos+hdr.FrameLength]
			pos += hdr.FrameLength
		}

		fmt.Fprintf(&b, "FRAME %d\n", fidx)
		f := fidx
		taps := &reconTaps{
			afterDequant: func(ty, id, ch int, coeffs []int32) {
				fmt.Fprintf(&b, "DQ f=%d t=%d id=%d ch=%d\n", f, ty, id, ch)
				writeCoefRow(&b, "C", coeffs)
			},
			preIMDCT: func(ty, id, ch int, coeffs []int32) {
				fmt.Fprintf(&b, "SPEC f=%d t=%d id=%d ch=%d\n", f, ty, id, ch)
				writeCoefRow(&b, "C", coeffs)
			},
			postClip: func(ty, id int, isCPE bool, ch0, ch1 []int32) {
				fmt.Fprintf(&b, "PCM f=%d t=%d id=%d n=%d\n", f, ty, id, 1024)
				writeCoefRow(&b, "L", ch0[:1024])
				if isCPE {
					writeCoefRow(&b, "R", ch1[:1024])
				}
			},
		}
		err := d.DecodeFrame(frame)
		if err == nil {
			d.reconstruct(taps)
			b.WriteString("FRAMEEND ok samples=1024\n")
		} else {
			b.WriteString("FRAMEEND err\n")
		}
		fidx++
	}
	fmt.Fprintf(&b, "STREAMEND frames=%d\n", fidx)
	return b.String()
}

// TestCbrtSqrtVsC compares the Go cbrt fixed table and fixed_sqrt against the
// oracle's (tools/cdec2 tables). The committed testdata/cbrt_sqrt.tables.gz is
// the real-C arbiter for the computed cbrt table; D2_CORPUS/cbrt_sqrt.tables
// overrides it for a fresh oracle run.
func TestCbrtSqrtVsC(t *testing.T) {
	path := filepath.Join("testdata", "cbrt_sqrt.tables.gz")
	if dir := os.Getenv("D2_CORPUS"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "cbrt_sqrt.tables")); err == nil {
			path = filepath.Join(dir, "cbrt_sqrt.tables")
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("required tables fixture %s: %v", path, err)
	}
	want := readMaybeGzip(t, path)
	got := goTablesDump()
	if got != want {
		firstDiff(t, "cbrt_sqrt", want, got)
	}
}

// TestReconStreamsVsC compares the Go decoder's D2 reconstruction against the
// instrumented C decoder dumps (tools/cdec2) for every stream in the corpus
// named by D2_CORPUS (each <name>.d2 has a sibling <name>.adts, or
// <name>.rawau + <name>.asc). Zero tolerance: the first differing line fails.
func TestReconStreamsVsC(t *testing.T) {
	// Default to the committed gzipped fixtures (always run, no oracle needed);
	// D2_CORPUS points at a fresh uncompressed cdec2 sweep for full coverage.
	dir := os.Getenv("D2_CORPUS")
	glob := "*.d2"
	if dir == "" {
		dir = "testdata"
		glob = "*.d2.gz"
	}
	dumps, err := filepath.Glob(filepath.Join(dir, glob))
	if err != nil || len(dumps) == 0 {
		t.Fatalf("no dumps (%s) in %s", glob, dir)
	}
	for _, dumpPath := range dumps {
		name := strings.TrimSuffix(strings.TrimSuffix(
			filepath.Base(dumpPath), ".gz"), ".d2")
		t.Run(name, func(t *testing.T) {
			want := readMaybeGzip(t, dumpPath)
			var got string
			if raw, err := os.ReadFile(filepath.Join(dir, name+".rawau")); err == nil {
				ascHex, err := os.ReadFile(filepath.Join(dir, name+".asc"))
				if err != nil {
					t.Fatalf("raw stream without ASC sidecar: %v", err)
				}
				asc, err := hex.DecodeString(strings.TrimSpace(string(ascHex)))
				if err != nil {
					t.Fatal(err)
				}
				got = goD2Dump(t, raw, true, asc)
			} else {
				adts, err := os.ReadFile(filepath.Join(dir, name+".adts"))
				if err != nil {
					t.Fatal(err)
				}
				got = goD2Dump(t, adts, false, nil)
			}
			if out := os.Getenv("D2_DUMP_OUT"); out != "" {
				if err := os.WriteFile(filepath.Join(out, name+".d2.go"), []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got != want {
				firstDiff(t, name, want, got)
			}
		})
	}
}

// BenchmarkReconstruct measures the D2 reconstruction throughput on one stream
// (DecodeFrame parse + reconstruct, no dump), reported per frame.
func BenchmarkReconstruct(b *testing.B) {
	dir := os.Getenv("D2_CORPUS")
	if dir == "" {
		b.Skip("D2_CORPUS not set")
	}
	name := os.Getenv("D2_BENCH")
	if name == "" {
		name = "tonal_s48_128k"
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".adts"))
	if err != nil {
		b.Fatal(err)
	}
	// Pre-split frames.
	var frames [][]byte
	for pos := 0; pos < len(data); {
		sync := FindSync(data, pos)
		if sync < 0 {
			break
		}
		pos = sync
		hdr, err := ParseADTS(bits.NewReader(data[pos:]))
		if err != nil || pos+hdr.FrameLength > len(data) {
			break
		}
		frames = append(frames, data[pos:pos+hdr.FrameLength])
		pos += hdr.FrameLength
	}
	if len(frames) == 0 {
		b.Fatal("no valid ADTS frames found")
	}
	b.ReportAllocs()
	b.ResetTimer()
	total := 0
	for b.Loop() {
		d := NewADTS()
		for _, f := range frames {
			if err := d.DecodeFrame(f); err != nil {
				b.Fatalf("DecodeFrame: %v", err)
			}
			d.reconstruct(nil)
		}
		total += len(frames)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(total), "ns/frame")
}
