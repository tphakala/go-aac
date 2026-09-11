package quality

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func putLE32w(w *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.Write(b[:])
}

func putLE16w(w *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.Write(b[:])
}

// TestWAVRoundTrip16 writes stereo 16-bit and reads it back exactly, including
// the clamped out-of-range samples.
func TestWAVRoundTrip16(t *testing.T) {
	left := []float64{0, 0.5, -0.5, 0.999, -1, 1.5, -1.5}
	right := []float64{0.25, -0.25, 0.125, 0, 0, 0, 0}
	var buf bytes.Buffer
	if err := WriteWAV16(&buf, 48000, [][]float64{left, right}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != wavHeaderBytes+2*2*len(left) {
		t.Fatalf("wav size = %d, want %d", buf.Len(), wavHeaderBytes+2*2*len(left))
	}
	sr, ch, err := ReadWAV(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if sr != 48000 || len(ch) != 2 || len(ch[0]) != len(left) {
		t.Fatalf("sr=%d ch=%d n=%d", sr, len(ch), len(ch[0]))
	}
	wantL, wantR := Quantize16(left), Quantize16(right)
	for i := range left {
		if ch[0][i] != wantL[i] || ch[1][i] != wantR[i] {
			t.Fatalf("sample %d = (%v, %v), want (%v, %v)", i, ch[0][i], ch[1][i], wantL[i], wantR[i])
		}
	}
	if ch[0][5] != 32767.0/32768 || ch[0][6] != -1 {
		t.Fatalf("clamp: got %v, %v, want the int16 rails", ch[0][5], ch[0][6])
	}
}

func TestWriteWAV16RejectsBadShapes(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWAV16(&buf, 44100, nil); err == nil {
		t.Fatal("zero channels must error")
	}
	if err := WriteWAV16(&buf, 44100, [][]float64{{0}, {0, 0}}); err == nil {
		t.Fatal("ragged channels must error")
	}
	if err := WriteWAV16(&buf, 44100, [][]float64{{0}, {0}, {0}}); err == nil {
		t.Fatal("three channels must error")
	}
}

// TestPackS16LE pins the interleaved little-endian S16 layout the go-aac encode
// path consumes: channels interleaved per frame, each sample the same
// round-half-away-from-zero code WriteWAV16 uses, low byte first.
func TestPackS16LE(t *testing.T) {
	ch := [][]float64{{0, 0.5, -1}, {0.25, -0.25, 1.5}}
	b := PackS16LE(ch)
	if len(b) != 3*2*2 {
		t.Fatalf("len = %d, want 12", len(b))
	}
	// Interleaved frames: L0 R0 L1 R1 L2 R2.
	want := []int16{
		sampleToInt16(0), sampleToInt16(0.25),
		sampleToInt16(0.5), sampleToInt16(-0.25),
		sampleToInt16(-1), sampleToInt16(1.5), // 1.5 saturates to 32767
	}
	for i, w := range want {
		if got := int16(binary.LittleEndian.Uint16(b[i*2:])); got != w {
			t.Fatalf("sample %d = %d, want %d", i, got, w)
		}
	}
	// The clamp: 1.5 must have saturated to 32767, not wrapped.
	if want[5] != 32767 {
		t.Fatalf("1.5 packed as %d, want 32767", want[5])
	}
	// Little-endian: 0.5 -> 16384 -> bytes 0x00 0x40.
	if b[4] != 0x00 || b[5] != 0x40 {
		t.Fatalf("L1 bytes = %#x %#x, want little-endian 0x00 0x40", b[4], b[5])
	}
}

// buildWAV assembles a RIFF/WAVE file from a fmt body and raw sample data, with
// an extra LIST chunk of odd length between them so the reader's
// word-alignment skip and unknown-chunk handling are both exercised.
func buildWAV(fmtBody, data []byte) []byte {
	var buf bytes.Buffer
	list := []byte("INFOx") // 5 bytes: odd length forces a pad byte
	riffSize := 4 + (8 + len(fmtBody)) + (8 + len(list) + 1) + (8 + len(data))
	buf.WriteString("RIFF")
	putLE32w(&buf, uint32(riffSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	putLE32w(&buf, uint32(len(fmtBody)))
	buf.Write(fmtBody)
	buf.WriteString("LIST")
	putLE32w(&buf, uint32(len(list)))
	buf.Write(list)
	buf.WriteByte(0) // pad
	buf.WriteString("data")
	putLE32w(&buf, uint32(len(data)))
	buf.Write(data)
	return buf.Bytes()
}

func fmtBody(format, nch, rate, bits int) []byte {
	var b bytes.Buffer
	putLE16w(&b, uint16(format))
	putLE16w(&b, uint16(nch))
	putLE32w(&b, uint32(rate))
	putLE32w(&b, uint32(rate*nch*bits/8))
	putLE16w(&b, uint16(nch*bits/8))
	putLE16w(&b, uint16(bits))
	return b.Bytes()
}

// TestReadWAVFloat32 hand-builds a WAVE_FORMAT_IEEE_FLOAT mono file with an
// odd-length LIST chunk before data, which ReadWAV must skip.
func TestReadWAVFloat32(t *testing.T) {
	samples := []float32{0.5, -0.25, 1, -1}
	var data bytes.Buffer
	for _, s := range samples {
		putLE32w(&data, math.Float32bits(s))
	}
	sr, ch, err := ReadWAV(bytes.NewReader(buildWAV(fmtBody(wavFormatIEEEFloat, 1, 44100, 32), data.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if sr != 44100 || len(ch) != 1 || len(ch[0]) != len(samples) {
		t.Fatalf("sr=%d ch=%d n=%d", sr, len(ch), len(ch[0]))
	}
	for i, s := range samples {
		if ch[0][i] != float64(s) {
			t.Fatalf("sample %d = %v, want %v", i, ch[0][i], s)
		}
	}
}

// TestReadWAV24Extensible covers 24-bit PCM under WAVE_FORMAT_EXTENSIBLE: the
// sub-format GUID's leading code must be resolved to PCM, and the
// sign-extension of the 24-bit samples must be right.
func TestReadWAV24Extensible(t *testing.T) {
	fb := fmtBody(wavFormatExtensible, 2, 48000, 24)
	var ext bytes.Buffer
	putLE16w(&ext, 22) // cbSize
	putLE16w(&ext, 24) // valid bits
	putLE32w(&ext, 3)  // channel mask
	putLE16w(&ext, wavFormatPCM)
	ext.Write(make([]byte, 14)) // rest of the GUID
	fb = append(fb, ext.Bytes()...)

	var data bytes.Buffer
	for _, v := range []int32{0, 4194304, -4194304, 8388607, -8388608, 1, -1} {
		u := uint32(v)
		data.Write([]byte{byte(u), byte(u >> 8), byte(u >> 16)})
		data.Write([]byte{0, 0, 0}) // right channel silent
	}
	sr, ch, err := ReadWAV(bytes.NewReader(buildWAV(fb, data.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if sr != 48000 || len(ch) != 2 || len(ch[0]) != 7 {
		t.Fatalf("sr=%d ch=%d n=%d", sr, len(ch), len(ch[0]))
	}
	want := []float64{0, 0.5, -0.5, 8388607.0 / 8388608, -1, 1.0 / 8388608, -1.0 / 8388608}
	for i, w := range want {
		if ch[0][i] != w || ch[1][i] != 0 {
			t.Fatalf("sample %d = (%v, %v), want (%v, 0)", i, ch[0][i], ch[1][i], w)
		}
	}
}

func TestReadWAVRejects(t *testing.T) {
	if _, _, err := ReadWAV(bytes.NewReader([]byte("not a wav file at all"))); err == nil {
		t.Fatal("want error on non-WAV input")
	}
	if _, _, err := ReadWAV(bytes.NewReader(buildWAV(fmtBody(wavFormatPCM, 1, 44100, 8), []byte{1, 2, 3}))); err == nil {
		t.Fatal("want error on 8-bit PCM")
	}
	if _, _, err := ReadWAV(bytes.NewReader(buildWAV(fmtBody(wavFormatPCM, 3, 44100, 16), make([]byte, 6)))); err == nil {
		t.Fatal("want error on 3 channels")
	}
}

func TestQuantize16(t *testing.T) {
	got := Quantize16([]float64{0, 1, -1, 0.5, 1 / 32768.0 * 0.4})
	want := []float64{0, 32767.0 / 32768, -1, 16384.0 / 32768, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("q[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestReadWAVHostileChunkSize: a chunk declaring a size at or above 2^31 must
// clamp to the bytes actually present rather than panic. The declared size is
// negative in a 32-bit int, which is what made a naive slice body[:-1].
func TestReadWAVHostileChunkSize(t *testing.T) {
	var full bytes.Buffer
	full.WriteString("RIFF")
	putLE32w(&full, 0xFFFFFFFF)
	full.WriteString("WAVE")
	full.WriteString("fmt ")
	putLE32w(&full, 0xFFFFFFFF)
	full.Write(fmtBody(wavFormatPCM, 1, 44100, 16))
	if _, _, err := ReadWAV(bytes.NewReader(full.Bytes())); err == nil {
		t.Fatal("a chunk swallowing the rest of the file leaves no data chunk, want an error")
	}
}

// TestReadWAVTruncatedDataChunk exercises the documented truncated-final-chunk
// clamp: a data chunk declaring more bytes than the file holds decodes the
// bytes that are there.
func TestReadWAVTruncatedDataChunk(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	putLE32w(&buf, 100)
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	fb := fmtBody(wavFormatPCM, 1, 8000, 16)
	putLE32w(&buf, uint32(len(fb)))
	buf.Write(fb)
	buf.WriteString("data")
	putLE32w(&buf, 4096) // declares far more than follows
	pos, neg := int16(1000), int16(-1000)
	putLE16w(&buf, uint16(pos))
	putLE16w(&buf, uint16(neg))
	sr, ch, err := ReadWAV(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if sr != 8000 || len(ch) != 1 || len(ch[0]) != 2 {
		t.Fatalf("sr=%d ch=%d n=%d, want the two present samples", sr, len(ch), len(ch[0]))
	}
	if ch[0][0] != 1000.0/32768 || ch[0][1] != -1000.0/32768 {
		t.Fatalf("samples = %v, %v", ch[0][0], ch[0][1])
	}
}

// TestReadWAVMissingChunks: a file with a fmt but no data, or data but no fmt,
// is rejected rather than silently decoded as empty.
func TestReadWAVMissingChunks(t *testing.T) {
	var noData bytes.Buffer
	noData.WriteString("RIFF")
	putLE32w(&noData, 28)
	noData.WriteString("WAVE")
	noData.WriteString("fmt ")
	fb := fmtBody(wavFormatPCM, 1, 44100, 16)
	putLE32w(&noData, uint32(len(fb)))
	noData.Write(fb)
	if _, _, err := ReadWAV(bytes.NewReader(noData.Bytes())); err == nil {
		t.Fatal("fmt without data must error")
	}

	var noFmt bytes.Buffer
	noFmt.WriteString("RIFF")
	putLE32w(&noFmt, 12)
	noFmt.WriteString("WAVE")
	noFmt.WriteString("data")
	putLE32w(&noFmt, 2)
	putLE16w(&noFmt, 7)
	if _, _, err := ReadWAV(bytes.NewReader(noFmt.Bytes())); err == nil {
		t.Fatal("data without fmt must error")
	}
}

// TestReadWAVShortFmtChunks covers both short-fmt rejections: a plain body
// under 16 bytes, and an EXTENSIBLE body that stops before its sub-format.
func TestReadWAVShortFmtChunks(t *testing.T) {
	short := fmtBody(wavFormatPCM, 1, 44100, 16)[:14]
	if _, _, err := ReadWAV(bytes.NewReader(buildWAV(short, make([]byte, 4)))); err == nil {
		t.Fatal("a fmt chunk under 16 bytes must error")
	}
	ext := append(fmtBody(wavFormatExtensible, 1, 44100, 16), 22, 0, 16, 0)
	if _, _, err := ReadWAV(bytes.NewReader(buildWAV(ext, make([]byte, 4)))); err == nil {
		t.Fatal("an EXTENSIBLE fmt chunk without its sub-format GUID must error")
	}
}

// TestReadWAV32BitPCM covers the 32-bit integer PCM branch, reachable from any
// user-supplied corpus file.
func TestReadWAV32BitPCM(t *testing.T) {
	var data bytes.Buffer
	for _, v := range []int32{0, 1 << 30, -(1 << 30), math.MaxInt32, math.MinInt32} {
		putLE32w(&data, uint32(v))
	}
	sr, ch, err := ReadWAV(bytes.NewReader(buildWAV(fmtBody(wavFormatPCM, 1, 48000, 32), data.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0, 0.5, -0.5, float64(math.MaxInt32) / 2147483648, -1}
	if sr != 48000 || len(ch) != 1 || len(ch[0]) != len(want) {
		t.Fatalf("sr=%d ch=%d n=%d", sr, len(ch), len(ch[0]))
	}
	for i, w := range want {
		if ch[0][i] != w {
			t.Fatalf("sample %d = %v, want %v", i, ch[0][i], w)
		}
	}
}

// TestReadWAVFloat32Clamps: float WAVs carry no format-imposed range, so
// out-of-range and NaN samples must be brought into ReadWAV's documented
// [-1, 1] rather than propagating into the metrics.
func TestReadWAVFloat32Clamps(t *testing.T) {
	var data bytes.Buffer
	for _, s := range []float32{2.5, -3.0, float32(math.NaN()), 0.5} {
		putLE32w(&data, math.Float32bits(s))
	}
	_, ch, err := ReadWAV(bytes.NewReader(buildWAV(fmtBody(wavFormatIEEEFloat, 1, 44100, 32), data.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{1, -1, 0, 0.5}
	for i, w := range want {
		if ch[0][i] != w {
			t.Fatalf("sample %d = %v, want %v", i, ch[0][i], w)
		}
	}
}

// TestQuantize16RoundsHalfAwayFromZero pins the documented rounding mode: a
// truncating or round-to-even regression shifts every half-code sample.
func TestQuantize16RoundsHalfAwayFromZero(t *testing.T) {
	got := Quantize16([]float64{0.5 / 32768, 1.5 / 32768, -0.5 / 32768, -1.5 / 32768})
	want := []float64{1.0 / 32768, 2.0 / 32768, -1.0 / 32768, -2.0 / 32768}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("q[%d] = %v, want %v", i, got[i]*32768, want[i]*32768)
		}
	}
}
