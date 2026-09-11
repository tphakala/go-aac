package quality

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// WAVE format tags this package understands.
const (
	wavFormatPCM        = 1
	wavFormatIEEEFloat  = 3
	wavFormatExtensible = 0xFFFE
)

// wavHeaderBytes is the size of the canonical RIFF/WAVE header WriteWAV16
// emits: RIFF chunk header (12) + fmt chunk (24) + data chunk header (8).
const wavHeaderBytes = 44

// Quantize16 rounds every sample to the nearest 16-bit PCM code (clamping to
// the int16 rails) and returns the dequantized values in [-1, 32767/32768],
// exactly the signal a reader of WriteWAV16's output sees. The harness feeds
// this quantized signal to both encoders so they start from identical input.
func Quantize16(x []float64) []float64 {
	q := make([]float64, len(x))
	for i, v := range x {
		q[i] = float64(sampleToInt16(v)) / 32768
	}
	return q
}

// sampleToInt16 scales v in [-1, 1] to int16 with round-half-away-from-zero
// and clamping, so 1.0 saturates to 32767 rather than wrapping.
func sampleToInt16(v float64) int16 {
	s := math.Round(v * 32768)
	if s > 32767 {
		return 32767
	}
	if s < -32768 {
		return -32768
	}
	return int16(s)
}

// PackS16LE packs one or two planar float64 channels of equal length into
// interleaved little-endian signed 16-bit PCM, the byte layout pcm.Config with
// BitDepth 16 consumes. It uses the same sampleToInt16 rounding WriteWAV16
// uses, so the go-aac encode path and a WAV written for the perceptual tools
// start from the identical quantized samples.
func PackS16LE(ch [][]float64) []byte {
	if len(ch) == 0 {
		return nil
	}
	n := len(ch[0])
	nch := len(ch)
	buf := make([]byte, n*nch*2)
	for i := range n {
		for c := range nch {
			off := (i*nch + c) * 2
			binary.LittleEndian.PutUint16(buf[off:], uint16(sampleToInt16(ch[c][i])))
		}
	}
	return buf
}

// WriteWAV16 writes ch (one or two planar float64 channels of equal length)
// as a canonical 44-byte-header RIFF/WAVE 16-bit PCM file.
func WriteWAV16(w io.Writer, sampleRate int, ch [][]float64) error {
	if len(ch) == 0 || len(ch) > 2 {
		return fmt.Errorf("quality: WriteWAV16 wants 1 or 2 channels, got %d", len(ch))
	}
	n := len(ch[0])
	for _, c := range ch {
		if len(c) != n {
			return errors.New("quality: WriteWAV16 channels differ in length")
		}
	}
	nch := len(ch)
	// int64 arithmetic so a long file cannot wrap the size on a 32-bit int
	// before the RIFF field check.
	dataBytes64 := int64(n) * int64(nch) * 2
	// Bounded by MaxInt32 as well as by the RIFF field: the narrowing below
	// would otherwise wrap negative on a 32-bit int and panic in make.
	if dataBytes64 > math.MaxInt32 || dataBytes64 > math.MaxUint32-(wavHeaderBytes-8) {
		return errors.New("quality: WriteWAV16 payload exceeds the RIFF 32-bit size field")
	}
	dataBytes := int(dataBytes64)
	var hdr [wavHeaderBytes]byte
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(wavHeaderBytes-8+dataBytes))
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], wavFormatPCM)
	binary.LittleEndian.PutUint16(hdr[22:], uint16(nch))
	binary.LittleEndian.PutUint32(hdr[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(hdr[28:], uint32(sampleRate*nch*2))
	binary.LittleEndian.PutUint16(hdr[32:], uint16(nch*2))
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(dataBytes))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	// The data section is the same interleaved S16LE packing PackS16LE
	// produces; reuse it so the parity PackS16LE documents is guaranteed by
	// one implementation rather than a duplicated loop. ch is already
	// validated as 1 or 2 equal-length channels above, so PackS16LE cannot
	// return nil here, and n*nch*2 is the dataBytes the overflow guard bounded.
	buf := PackS16LE(ch)
	_, err := w.Write(buf)
	return err
}

// ReadWAV parses a RIFF/WAVE file with PCM 16/24/32-bit or IEEE float32
// samples (plain or WAVE_FORMAT_EXTENSIBLE) into planar float64 channels in
// [-1, 1]. Unknown chunks are skipped. It reads the whole input into memory.
func ReadWAV(r io.Reader) (sampleRate int, ch [][]float64, err error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, nil, err
	}
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, nil, errors.New("quality: not a RIFF/WAVE file")
	}
	var format, nch, bits int
	var data []byte
	haveFmt := false
	for pos := 12; pos+8 <= len(b); {
		id := string(b[pos : pos+4])
		// int64 throughout: a declared size at or above 2^31 is negative in a
		// 32-bit int, which slips past the truncation clamp below and panics
		// on body[:size]. ReadWAV parses user-supplied files (the harness's
		// -corpus flag), so a hostile size must clamp, not crash.
		size64 := int64(binary.LittleEndian.Uint32(b[pos+4:]))
		body := b[pos+8:]
		if size64 > int64(len(body)) {
			size64 = int64(len(body)) // tolerate a truncated final chunk
		}
		size := int(size64)
		body = body[:size]
		switch id {
		case "fmt ":
			format, nch, sampleRate, bits, err = parseFmtChunk(body)
			if err != nil {
				return 0, nil, err
			}
			haveFmt = true
		case "data":
			data = body
		}
		pos += 8 + size + size&1 // chunks are word-aligned
	}
	if !haveFmt || data == nil {
		return 0, nil, errors.New("quality: missing fmt or data chunk")
	}
	if nch < 1 || nch > 2 {
		return 0, nil, fmt.Errorf("quality: unsupported channel count %d", nch)
	}
	conv, bytesPer, err := wavSampleReader(format, bits)
	if err != nil {
		return 0, nil, err
	}
	frames := len(data) / (bytesPer * nch)
	ch = make([][]float64, nch)
	for c := range ch {
		ch[c] = make([]float64, frames)
	}
	for i := range frames {
		for c := range nch {
			off := (i*nch + c) * bytesPer
			ch[c][i] = conv(data[off : off+bytesPer])
		}
	}
	return sampleRate, ch, nil
}

// parseFmtChunk decodes the fields ReadWAV needs from a fmt chunk body,
// resolving WAVE_FORMAT_EXTENSIBLE to the sub-format's own tag (the first
// two bytes of its GUID hold the classic format code).
func parseFmtChunk(body []byte) (format, nch, sampleRate, bits int, err error) {
	if len(body) < 16 {
		return 0, 0, 0, 0, errors.New("quality: short fmt chunk")
	}
	format = int(binary.LittleEndian.Uint16(body[0:]))
	nch = int(binary.LittleEndian.Uint16(body[2:]))
	sampleRate = int(binary.LittleEndian.Uint32(body[4:]))
	bits = int(binary.LittleEndian.Uint16(body[14:]))
	if format == wavFormatExtensible {
		if len(body) < 26 {
			return 0, 0, 0, 0, errors.New("quality: short WAVE_FORMAT_EXTENSIBLE fmt chunk")
		}
		format = int(binary.LittleEndian.Uint16(body[24:]))
	}
	return format, nch, sampleRate, bits, nil
}

// wavSampleReader returns the per-sample decoder and byte width for a
// (format, bits) pair, or an error for unsupported layouts.
func wavSampleReader(format, bits int) (conv func([]byte) float64, bytesPer int, err error) {
	switch {
	case format == wavFormatPCM && bits == 16:
		return func(p []byte) float64 { return float64(int16(binary.LittleEndian.Uint16(p))) / 32768 }, 2, nil
	case format == wavFormatPCM && bits == 24:
		return func(p []byte) float64 {
			v := int32(uint32(p[0])<<8|uint32(p[1])<<16|uint32(p[2])<<24) >> 8
			return float64(v) / 8388608
		}, 3, nil
	case format == wavFormatPCM && bits == 32:
		return func(p []byte) float64 { return float64(int32(binary.LittleEndian.Uint32(p))) / 2147483648 }, 4, nil
	case format == wavFormatIEEEFloat && bits == 32:
		// Float WAVs are not bound to [-1, 1] by their format the way the PCM
		// variants are, and a NaN would convert to an implementation-defined
		// int16 downstream, so clamp here to honour ReadWAV's documented range.
		return func(p []byte) float64 {
			v := float64(math.Float32frombits(binary.LittleEndian.Uint32(p)))
			if math.IsNaN(v) {
				return 0
			}
			return max(-1, min(1, v))
		}, 4, nil
	default:
		return nil, 0, fmt.Errorf("quality: unsupported WAV format tag %d with %d bits", format, bits)
	}
}
