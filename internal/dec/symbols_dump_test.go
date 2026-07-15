// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tphakala/go-aac/internal/bits"
)

// dumpSCE formats one decoded channel element exactly like the tools/cdec
// hook, so the two dumps can be compared byte for byte.
func dumpSCE(b *strings.Builder, elemType, elemID, ch int, cpe *CPE) {
	sce := &cpe.Ch[ch]
	ics := &sce.ICS
	fmt.Fprintf(b, "ELEM t=%d id=%d ch=%d\n", elemType, elemID, ch)
	fmt.Fprintf(b, "ICS ws=%d kb=%d msfb=%d nw=%d nwg=%d gl=",
		ics.WindowSequence[0], ics.UseKBWindow[0], ics.MaxSFB,
		ics.NumWindows, ics.NumWindowGroups)
	for g := range ics.NumWindowGroups {
		if g > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(b, "%d", ics.GroupLen[g])
	}
	pred := 0
	if ics.PredictorPresent {
		pred = 1
	}
	fmt.Fprintf(b, " swb=%d pred=%d\n", ics.NumSWB, pred)

	if elemType == TypeCPE && ch == 0 {
		b.WriteString("MS")
		for i := range ics.NumWindowGroups * ics.MaxSFB {
			fmt.Fprintf(b, " %d", cpe.MSMask[i])
		}
		b.WriteByte('\n')
	}

	tns := &sce.TNS
	present := 0
	if tns.Present {
		present = 1
	}
	fmt.Fprintf(b, "TNS %d\n", present)
	if tns.Present {
		for w := range ics.NumWindows {
			fmt.Fprintf(b, "TNSW w=%d nf=%d\n", w, tns.NFilt[w])
			for f := range tns.NFilt[w] {
				fmt.Fprintf(b, "TNSF l=%d o=%d d=%d c=",
					tns.Length[w][f], tns.Order[w][f], tns.Direction[w][f])
				for o := range tns.Order[w][f] {
					if o > 0 {
						b.WriteByte(',')
					}
					fmt.Fprintf(b, "%d", tns.CoefFixed[w][f][o])
				}
				b.WriteByte('\n')
			}
		}
	}

	if sce.PulsePresent {
		fmt.Fprintf(b, "PUL %d", sce.Pulse.NumPulse)
		for i := range sce.Pulse.NumPulse {
			fmt.Fprintf(b, " %d:%d", sce.Pulse.Pos[i], sce.Pulse.Amp[i])
		}
		b.WriteByte('\n')
	}

	n := ics.NumWindowGroups * ics.MaxSFB
	b.WriteString("BT")
	for i := range n {
		fmt.Fprintf(b, " %d", sce.BandType[i])
	}
	b.WriteString("\nSFO")
	for i := range n {
		fmt.Fprintf(b, " %d", sce.SFO[i])
	}
	b.WriteByte('\n')

	// Spectral symbol dump in the exact order the fixed decoder's final
	// dequant loop visits coded bands (the cdec vector_pow43 hook).
	coefBase := 0
	idx := 0
	for g := range ics.NumWindowGroups {
		gLen := ics.GroupLen[g]
		for i := 0; i < ics.MaxSFB; i, idx = i+1, idx+1 {
			cbtM1 := uint32(sce.BandType[idx]) - 1
			if cbtM1 >= NoiseBT-1 {
				continue
			}
			offLen := int(ics.SWBOffset[i+1] - ics.SWBOffset[i])
			for group := range gLen {
				cf := coefBase + group*128 + int(ics.SWBOffset[i])
				fmt.Fprintf(b, "SD %d", offLen)
				for k := range offLen {
					fmt.Fprintf(b, " %d", sce.QCoefs[cf+k])
				}
				b.WriteByte('\n')
			}
		}
		coefBase += gLen << 7
	}
}

// goDump decodes a whole stream and produces the cdec-format text dump.
func goDump(t *testing.T, data []byte, raw bool, asc []byte) string {
	t.Helper()
	var b strings.Builder
	var d *Decoder
	if raw {
		var err error
		d, err = NewRaw(asc)
		if err != nil {
			t.Fatalf("NewRaw: %v", err)
		}
		c := d.Config()
		fls := 0
		if c.FrameLenShort {
			fls = 1
		}
		fmt.Fprintf(&b, "ASC obj=%d sri=%d sr=%d cfg=%d sbr=%d ps=%d fls=%d\n",
			c.ObjectType, c.SamplingIndex, c.SampleRate, c.ChanConfig,
			c.SBR, c.PS, fls)
	} else {
		d = NewADTS()
	}
	dumpedM4A := false
	pos := 0
	fidx := 0
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
			fmt.Fprintf(&b, "HDR obj=%d cfg=%d crcabs=%d rdb=%d sri=%d sr=%d len=%d\n",
				hdr.ObjectType, hdr.ChanConfig, hdr.CRCAbsent, hdr.NumAACFrames,
				hdr.SamplingIndex, hdr.SampleRate, hdr.FrameLength)
			if pos+hdr.FrameLength > len(data) {
				break
			}
			frame = data[pos : pos+hdr.FrameLength]
			pos += hdr.FrameLength
		}
		fmt.Fprintf(&b, "FRAME %d\n", fidx)
		err := d.DecodeFrame(frame)
		if !dumpedM4A {
			c := d.Config()
			fls := 0
			if c.FrameLenShort {
				fls = 1
			}
			fmt.Fprintf(&b, "M4A obj=%d sri=%d sr=%d cfg=%d sbr=%d ps=%d fls=%d\n",
				c.ObjectType, c.SamplingIndex, c.SampleRate, c.ChanConfig,
				c.SBR, c.PS, fls)
			dumpedM4A = true
		}
		for _, e := range d.Elems {
			dumpSCE(&b, e.Type, e.ID, 0, e.CPE)
			if e.Type == TypeCPE {
				dumpSCE(&b, e.Type, e.ID, 1, e.CPE)
			}
		}
		if err != nil {
			fmt.Fprintf(&b, "FRAMEEND err\n")
		} else {
			fmt.Fprintf(&b, "FRAMEEND ok samples=1024\n")
		}
		fidx++
	}
	fmt.Fprintf(&b, "STREAMEND frames=%d\n", fidx)
	return b.String()
}

// firstDiff reports the first differing line with context.
func firstDiff(t *testing.T, name, want, got string) {
	t.Helper()
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	for i := range min(len(wl), len(gl)) {
		if wl[i] != gl[i] {
			lo := max(0, i-3)
			t.Fatalf("%s: first divergence at line %d:\nC : %s\nGo: %s\ncontext:\n%s",
				name, i+1, wl[i], gl[i], strings.Join(wl[lo:i+1], "\n"))
		}
	}
	if len(wl) != len(gl) {
		t.Fatalf("%s: line count differs: C %d vs Go %d", name, len(wl), len(gl))
	}
}

// TestSymbolStreamsVsC compares the Go decoder's symbol streams against
// the instrumented C decoder dumps (tools/cdec) for every stream in the
// corpus. The committed corpus lives in testdata/: for each <name>.dump.gz
// there is either <name>.adts (ADTS stream) or <name>.rawau (raw access
// units, 2-byte big-endian length prefix each) plus <name>.asc (hex
// AudioSpecificConfig). DEC_CORPUS overrides the corpus dir when
// regenerating against a fresh cdec run (then plain *.dump also works).
func TestSymbolStreamsVsC(t *testing.T) {
	dir := os.Getenv("DEC_CORPUS")
	if dir == "" {
		dir = "testdata"
	}
	dumps, err := filepath.Glob(filepath.Join(dir, "*.dump*"))
	if err != nil || len(dumps) == 0 {
		t.Fatalf("no dumps in %s", dir)
	}
	for _, dumpPath := range dumps {
		name := strings.TrimSuffix(strings.TrimSuffix(
			filepath.Base(dumpPath), ".gz"), ".dump")
		t.Run(name, func(t *testing.T) {
			want := readMaybeGzip(t, dumpPath)
			var got string
			if data, err := os.ReadFile(filepath.Join(dir, name+".rawau")); err == nil {
				ascHex, err := os.ReadFile(filepath.Join(dir, name+".asc"))
				if err != nil {
					t.Fatalf("raw stream without ASC sidecar: %v", err)
				}
				asc, err := hex.DecodeString(strings.TrimSpace(string(ascHex)))
				if err != nil {
					t.Fatal(err)
				}
				got = goDump(t, data, true, asc)
			} else {
				data, err := os.ReadFile(filepath.Join(dir, name+".adts"))
				if err != nil {
					t.Fatal(err)
				}
				got = goDump(t, data, false, nil)
			}
			if got != want {
				firstDiff(t, name, want, got)
			}
		})
	}
}

// readMaybeGzip returns the dump text, transparently decompressing
// .gz-suffixed fixtures.
func readMaybeGzip(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if b, err = io.ReadAll(gz); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return string(b)
}
