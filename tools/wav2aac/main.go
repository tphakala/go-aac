// SPDX-License-Identifier: LGPL-2.1-or-later

// wav2aac encodes a canonical RIFF/WAVE file to an ADTS AAC stream with
// go-aac. It exists so scripts/bench-encoders.sh can time go-aac against the
// FFmpeg CLI on exactly the same job (file in, file out, one process), rather
// than timing a Go benchmark against a C program and hoping the difference is
// only the codec.
//
// It drives the low-level aac package directly, calling EncodeFrame and
// writing each access unit itself, instead of going through the pcm
// streaming layer. That keeps the wall time, CPU time and peak RSS this
// benchmark reports attributable to the encoder alone, not to any layer
// built on top of it. pcm.Config exposes Coder selection too now, so that
// is no longer a reason to bypass it; the low-level path stays for the
// measurement isolation described above.
//
// Usage: wav2aac [-b bitrate] [-coder nmr|twoloop|fast] in.wav out.aac
package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"

	aac "github.com/tphakala/go-aac"
)

func main() {
	bitrate := flag.Int("b", 128000, "bitrate in bits per second")
	coderName := flag.String("coder", "nmr", "quantizer search: nmr, twoloop or fast")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: wav2aac [-b bitrate] [-coder nmr|twoloop|fast] in.wav out.aac")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), flag.Arg(1), *bitrate, *coderName); err != nil {
		fmt.Fprintln(os.Stderr, "wav2aac:", err)
		os.Exit(1)
	}
}

func run(inPath, outPath string, bitrate int, coderName string) error {
	var coder aac.Coder
	switch coderName {
	case "nmr":
		coder = aac.CoderNMR
	case "twoloop":
		coder = aac.CoderTwoLoop
	case "fast":
		coder = aac.CoderFast
	default:
		return fmt.Errorf("unknown coder %q", coderName)
	}

	in, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	rate, channels, bits, dataLen, err := readWAVHeader(in)
	if err != nil {
		return err
	}
	// convertFrame handles only these depths, and stride would be zero below
	// for anything under 8 bits, so reject it here with a clean error.
	if bits != 16 && bits != 32 {
		return fmt.Errorf("unsupported bit depth %d (must be 16 or 32)", bits)
	}
	if channels < 1 || channels > 2 {
		return fmt.Errorf("unsupported channel count %d (must be 1 or 2)", channels)
	}

	e, err := aac.NewEncoder(aac.EncoderConfig{
		SampleRate: rate,
		Channels:   channels,
		Bitrate:    bitrate,
		Coder:      coder,
	})
	if err != nil {
		return err
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // f.Close is called explicitly below; this is the error path
	w := bufio.NewWriter(f)

	// Stream frame by frame rather than buffering the whole file as planar
	// float32. Peak RSS is part of what the benchmark reports, and holding the
	// entire decoded input would measure this tool's buffering rather than the
	// encoder's footprint.
	// Limit to the declared data chunk: trailing LIST/id3 chunks after data
	// would otherwise be read as audio samples.
	r := bufio.NewReaderSize(io.LimitReader(in, dataLen), 1<<16)
	stride := channels * bits / 8
	chunk := make([]byte, aac.FrameSize*stride)
	planar := make([][]float32, channels)
	for c := range planar {
		planar[c] = make([]float32, aac.FrameSize)
	}
	frame := make([][]float32, channels)
	var au, hdr []byte
	for {
		n, err := io.ReadFull(r, chunk)
		if n == 0 {
			break
		}
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		samples := n / stride
		if samples == 0 {
			break
		}
		if err := convertFrame(planar, chunk[:samples*stride], samples, channels, bits); err != nil {
			return err
		}
		for c := range channels {
			frame[c] = planar[c][:samples]
		}
		au, err = e.EncodeFrame(au[:0], frame)
		if err != nil {
			return err
		}
		if err := writeAU(w, &hdr, au, rate, channels); err != nil {
			return err
		}
		if samples < aac.FrameSize {
			break
		}
	}
	// Drain the encoder delay, or the tail frames are silently dropped and the
	// output is short of what the pcm layer (and FFmpeg) produce.
	for !e.Drained() {
		au, err = e.EncodeFrame(au[:0], nil)
		if err != nil {
			return err
		}
		if err := writeAU(w, &hdr, au, rate, channels); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

// writeAU frames one access unit in ADTS and writes it. An empty unit (the
// priming frame, or an exhausted drain) writes nothing.
func writeAU(w io.Writer, hdr *[]byte, au []byte, rate, channels int) error {
	if len(au) == 0 {
		return nil
	}
	var err error
	*hdr, err = aac.AppendADTSHeader((*hdr)[:0], rate, channels, len(au))
	if err != nil {
		return err
	}
	if _, err := w.Write(*hdr); err != nil {
		return err
	}
	_, err = w.Write(au)
	return err
}

// convertFrame converts one frame of interleaved integer PCM to planar
// float32 using the same scale factors as the pcm layer, so the encoder sees
// identical input.
func convertFrame(planar [][]float32, data []byte, samples, channels, bits int) error {
	for c := range channels {
		dst := planar[c][:samples]
		switch bits {
		case 16:
			for i := range samples {
				v := int16(binary.LittleEndian.Uint16(data[(i*channels+c)*2:]))
				dst[i] = float32(v) / (1 << 15)
			}
		case 32:
			for i := range samples {
				v := int32(binary.LittleEndian.Uint32(data[(i*channels+c)*4:]))
				dst[i] = float32(v) / (1 << 31)
			}
		default:
			return fmt.Errorf("unsupported bit depth %d", bits)
		}
	}
	return nil
}

// readWAVHeader parses chunk headers until it reaches the data chunk, leaving
// r positioned at the first sample and returning the declared data length.
// Accepts plain PCM and WAVE_FORMAT_EXTENSIBLE with the PCM subformat.
func readWAVHeader(r io.Reader) (rate, channels, bits int, dataLen int64, err error) {
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return 0, 0, 0, 0, err
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return 0, 0, 0, 0, fmt.Errorf("not a RIFF/WAVE file")
	}
	for {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			return 0, 0, 0, 0, fmt.Errorf("no data chunk: %w", err)
		}
		id := string(ch[0:4])
		// A chunk size with the high bit set would become negative when cast to
		// int on a 32-bit build (GOARCH=386/arm), turning the make and the
		// ReadFull below into a panic rather than an error.
		szU := binary.LittleEndian.Uint32(ch[4:8])
		if szU > math.MaxInt32 {
			return 0, 0, 0, 0, fmt.Errorf("corrupt chunk %q size %d", id, szU)
		}
		sz := int(szU)
		if id == "data" {
			if rate == 0 || channels == 0 || bits == 0 {
				return 0, 0, 0, 0, fmt.Errorf("data chunk precedes fmt chunk")
			}
			return rate, channels, bits, int64(sz), nil
		}
		padded := int64(sz + sz&1)
		if id != "fmt " {
			// Discard rather than buffer: a large LIST/JUNK chunk would
			// otherwise inflate the peak RSS this tool exists to report.
			if _, err := io.CopyN(io.Discard, r, padded); err != nil {
				return 0, 0, 0, 0, err
			}
			continue
		}
		body := make([]byte, padded)
		if _, err := io.ReadFull(r, body); err != nil {
			return 0, 0, 0, 0, err
		}
		if sz < 16 {
			return 0, 0, 0, 0, fmt.Errorf("short fmt chunk: %d bytes", sz)
		}
		switch f := binary.LittleEndian.Uint16(body[0:2]); f {
		case 1:
		case 0xFFFE:
			if sz < 40 || binary.LittleEndian.Uint16(body[24:26]) != 1 {
				return 0, 0, 0, 0, fmt.Errorf("extensible wav is not PCM subformat")
			}
		default:
			return 0, 0, 0, 0, fmt.Errorf("unsupported wav format %d", f)
		}
		channels = int(binary.LittleEndian.Uint16(body[2:4]))
		rate = int(binary.LittleEndian.Uint32(body[4:8]))
		bits = int(binary.LittleEndian.Uint16(body[14:16]))
	}
}
