// Command quality compares this project's AAC-LC encoder against ffmpeg's aac
// and libfdk_aac encoders (used strictly as black-box binaries, see
// PROVENANCE.md) on a deterministic synthetic corpus and optional user WAV
// files. Every stream is decoded through this project's own pcm decoder,
// aligned by cross-correlation, and scored against the source with the
// internal/quality metrics, plus ViSQOL MOS-LQO and PEAQ ODG when those tools
// are on PATH. Each decode scores against the source, never against another
// encoder, so decoder differences cannot flatter one encoder. Output is a
// Markdown report and a JSON twin.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	aac "github.com/tphakala/go-aac"
	"github.com/tphakala/go-aac/internal/quality"
	"github.com/tphakala/go-aac/pcm"
)

// cmdTimeout bounds every external binary invocation.
const cmdTimeout = 5 * time.Minute

// goaacDelay is this project's encoder priming delay, the lag a correctly
// aligned tagless stream measures at. ffmpeg's native aac encoder primes the
// same 1024 samples, so both columns align at this value; libfdk_aac primes
// about twice that. Named here so the report legend can state it.
const goaacDelay = aac.EncoderDelay

// alignment search window handed to quality.AlignLag: a small negative
// allowance for an over-trimmed stream, and well past any AAC codec delay on
// the positive side (go-aac and ffmpeg aac at 1024, libfdk at about 2048 plus
// its filter latency).
const (
	alignMinLag = -128
	alignMaxLag = 4096
)

// crosscheckMinSNR is the floor, in dB, below which the -crosscheck diagnostic
// treats this project's pcm decode and ffmpeg's aac_fixed decode of the same
// stream as diverging and logs a warning. The pcm decoder is proven
// byte-identical to ffmpeg's fixed-point AAC decoder (pcm/decoder_test.go), so
// a faithful decode agrees to the SNR cap and this wide margin fires only on a
// real disagreement in decode, the class of bug a go-aac-versus-ffmpeg
// comparison cannot see because it routes both encoders through the same pcm
// decode.
const crosscheckMinSNR = 40.0

// caseSpec names one (program, sample rate, bitrate, coder) comparison.
type caseSpec struct {
	Program    quality.Program
	SampleRate int
	Kbps       int
	Coder      aac.Coder
	Seconds    int
}

// encoderResult is one encoder's score on one case. MOS and ODG are NaN when
// the external tool was not run or produced no number.
type encoderResult struct {
	Name    string
	Lag     int
	Bytes   int
	Metrics quality.Metrics
	MOS     float64
	ODG     float64
}

// caseResult pairs the encoders' results on one case. FFmpegAAC is ffmpeg's
// native aac encoder at the same coder; LibFDK is ffmpeg's libfdk_aac and is
// nil when that encoder is absent from the ffmpeg build.
type caseResult struct {
	Program    string
	Channels   int
	SampleRate int
	Kbps       int
	Coder      string
	GoAAC      encoderResult
	FFmpegAAC  encoderResult
	LibFDK     *encoderResult
}

// runCase executes one comparison inside dir, which must exist and is where
// every intermediate file (the raw f32le reference, each ADTS stream, aligned
// WAVs for the external tools) is written.
func runCase(ctx context.Context, tl tools, dir string, spec caseSpec, ref [][]float64, crosscheck bool, errw io.Writer) (caseResult, error) {
	if len(ref) == 0 || len(ref[0]) == 0 {
		return caseResult{}, errors.New("empty program at this sample rate")
	}
	// ref is the quantized 16-bit signal, shared read-only across this
	// program's bitrate and coder cases. ffmpeg reads it as raw f32le (the
	// exact quantized samples, int16/32768 as float32); go-aac gets the
	// identical samples packed as S16LE. Nothing below mutates ref.
	const rawName = "ref.f32"
	if err := writeRawF32(filepath.Join(dir, rawName), ref); err != nil {
		return caseResult{}, err
	}
	// A ref WAV is written only when a perceptual tool needs one; the objective
	// metrics work from the in-memory signal.
	if tl.visqol != "" || tl.peaq != "" {
		if err := writeWAVFile(filepath.Join(dir, "ref.wav"), spec.SampleRate, ref); err != nil {
			return caseResult{}, err
		}
	}

	goStream, err := encodeGoAAC(ref, spec.SampleRate, spec.Kbps, spec.Coder)
	if err != nil {
		return caseResult{}, fmt.Errorf("go-aac encode: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goaac.aac"), goStream, 0o644); err != nil {
		return caseResult{}, err
	}
	ffStream, err := refEncoder{kind: refFFmpegAAC, bin: tl.ffmpeg}.
		encode(ctx, dir, rawName, "ffaac.aac", spec.SampleRate, len(ref), spec.Kbps, spec.Coder)
	if err != nil {
		return caseResult{}, fmt.Errorf("ffmpeg aac encode: %w", err)
	}

	res := caseResult{
		Program: spec.Program.Name, Channels: len(ref), SampleRate: spec.SampleRate,
		Kbps: spec.Kbps, Coder: coderName(spec.Coder),
	}
	res.GoAAC, err = measure(ctx, tl, dir, "go-aac", "goaac", ref, goStream, spec.SampleRate, crosscheck, errw)
	if err != nil {
		return caseResult{}, err
	}
	res.FFmpegAAC, err = measure(ctx, tl, dir, "ffmpeg-aac", "ffaac", ref, ffStream, spec.SampleRate, crosscheck, errw)
	if err != nil {
		return caseResult{}, err
	}
	// libfdk_aac is measured only when the ffmpeg build carries it (never in the
	// pinned-oracle CI, always absent there); it is reported, never gated.
	if tl.libfdk {
		fdkStream, err := refEncoder{kind: refLibFDK, bin: tl.ffmpeg}.
			encode(ctx, dir, rawName, "libfdk.aac", spec.SampleRate, len(ref), spec.Kbps, spec.Coder)
		if err != nil {
			return caseResult{}, fmt.Errorf("libfdk_aac encode: %w", err)
		}
		fdk, err := measure(ctx, tl, dir, "libfdk", "libfdk", ref, fdkStream, spec.SampleRate, crosscheck, errw)
		if err != nil {
			return caseResult{}, err
		}
		res.LibFDK = &fdk
	}
	return res, nil
}

// measure decodes, aligns, and scores one encoder's stream against ref. The
// external perceptual tools run only when configured; their failures are
// reported through errw and leave the value NaN rather than failing the case.
func measure(ctx context.Context, tl tools, dir, name, base string, ref [][]float64, stream []byte, sampleRate int, crosscheck bool, errw io.Writer) (encoderResult, error) {
	deg, err := decodeStream(stream)
	if err != nil {
		return encoderResult{}, fmt.Errorf("%s decode: %w", name, err)
	}
	if len(deg) != len(ref) {
		return encoderResult{}, fmt.Errorf("%s: decoded %d channels, want %d", name, len(deg), len(ref))
	}
	// The cross-check compares this pcm decode against ffmpeg's decode of the
	// same stream, so it uses the raw deg, before the ref alignment and trim.
	if crosscheck {
		crossCheck(ctx, tl, dir, name, base+".aac", deg, sampleRate, errw)
	}
	refA, degA, lag := alignTrim(ref, deg, alignMinLag, alignMaxLag)
	if len(refA) == 0 || len(refA[0]) < quality.SegSNRSegment {
		// Nothing meaningful overlaps. Scoring this would report the metrics'
		// degenerate-input values as a measurement, so fail the case instead.
		return encoderResult{}, fmt.Errorf("%s: alignment at lag %d left %d overlapping samples", name, lag, len(refA[0]))
	}
	r := encoderResult{Name: name, Lag: lag, Bytes: len(stream), MOS: math.NaN(), ODG: math.NaN()}
	r.Metrics = quality.Compare(refA, degA, sampleRate)
	if tl.visqol == "" && tl.peaq == "" {
		return r, nil
	}
	// The reference is aligned per encoder (the lags differ), so its file name
	// is namespaced too: a shared name would leave -keep holding only the last
	// encoder's copy.
	refWav, degWav := base+"-ref-aligned.wav", base+"-aligned.wav"
	if err := writeWAVFile(filepath.Join(dir, refWav), sampleRate, refA); err != nil {
		return encoderResult{}, err
	}
	if err := writeWAVFile(filepath.Join(dir, degWav), sampleRate, degA); err != nil {
		return encoderResult{}, err
	}
	if tl.visqol != "" {
		if v, err := runVisqol(ctx, tl, dir, refWav, degWav, sampleRate); err == nil {
			r.MOS = v
		} else {
			logf(errw, "warning: visqol %s/%s: %v\n", filepath.Base(dir), name, err)
		}
	}
	if tl.peaq != "" {
		if v, err := runPEAQ(ctx, tl, dir, refWav, degWav, sampleRate); err == nil {
			r.ODG = v
		} else {
			logf(errw, "warning: peaq %s/%s: %v\n", filepath.Base(dir), name, err)
		}
	}
	return r, nil
}

// writeWAVFile writes planar float64 channels as a 16-bit WAV at path.
func writeWAVFile(path string, sampleRate int, ch [][]float64) error {
	return writeFile(path, func(f *os.File) error {
		return quality.WriteWAV16(f, sampleRate, ch)
	})
}

// encodeGoAAC encodes ref (planar float64, already 16-bit quantized) through
// the public one-shot pcm.EncodeInterleaved at the given coder, packing the
// samples to the S16LE bytes that path consumes, and returns the ADTS stream.
func encodeGoAAC(ref [][]float64, sampleRate, kbps int, coder aac.Coder) ([]byte, error) {
	var buf bytes.Buffer
	cfg := pcm.Config{
		SampleRate: sampleRate,
		BitDepth:   16,
		Channels:   len(ref),
		Bitrate:    kbps * 1000,
		Coder:      coder,
	}
	if err := pcm.EncodeInterleaved(&buf, cfg, quality.PackS16LE(ref)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeRawF32 writes planar float64 channels as interleaved little-endian
// float32 at path, the raw input ffmpeg reads with -f f32le. ref holds the
// 16-bit quantized samples, each exactly int16/32768, so the float32
// conversion is exact and ffmpeg sees the identical values go-aac encoded.
func writeRawF32(path string, ref [][]float64) error {
	ch := len(ref)
	if ch == 0 || len(ref[0]) == 0 {
		return errors.New("writeRawF32: empty reference")
	}
	n := len(ref[0])
	raw := make([]byte, 4*ch*n)
	for i := range n {
		for c := range ch {
			binary.LittleEndian.PutUint32(raw[4*(i*ch+c):], math.Float32bits(float32(ref[c][i])))
		}
	}
	return os.WriteFile(path, raw, 0o644)
}

// decodeStream decodes an AAC-LC ADTS stream through this project's own pcm
// decoder (interleaved S16LE output) to planar float64 channels. It decodes
// go-aac's own streams and the ffmpeg reference streams alike, so the ffmpeg
// streams double as third-party ADTS decode coverage.
func decodeStream(stream []byte) ([][]float64, error) {
	raw, info, err := pcm.DecodeInterleaved(bytes.NewReader(stream))
	if err != nil {
		return nil, err
	}
	// A zero channel count is a pcm decoder bug, not bad input, and silent
	// without the guard: it would divide by zero in the unpack below.
	if info.Channels <= 0 {
		return nil, fmt.Errorf("decoded stream reports %d channels", info.Channels)
	}
	return deinterleaveS16LE(raw, info.Channels)
}

// deinterleaveS16LE unpacks interleaved little-endian signed 16-bit samples
// into channels planar float64 channels in [-1, 1), dividing by 1<<15, the
// same scale pcm's own S16-to-float conversion uses so a decoded stream scores
// against the reference on one scale. It errors when the byte count is not a
// whole number of channel-sized frames. channels must be positive; callers
// guarantee it.
func deinterleaveS16LE(b []byte, channels int) ([][]float64, error) {
	frameBytes := 2 * channels
	if len(b)%frameBytes != 0 {
		return nil, fmt.Errorf("decoded %d bytes, not a whole number of %d-byte frames (%d channels of S16LE)", len(b), frameBytes, channels)
	}
	frames := len(b) / frameBytes
	out := make([][]float64, channels)
	for c := range out {
		out[c] = make([]float64, frames)
	}
	const scale = 1.0 / (1 << 15)
	for i := range frames {
		for c := range channels {
			off := (i*channels + c) * 2
			out[c][i] = float64(int16(binary.LittleEndian.Uint16(b[off:]))) * scale
		}
	}
	return out, nil
}

// alignTrim measures the lag of deg against ref on channel 0 within
// [minLag, maxLag], applies it to every channel, and trims both to the common
// length.
func alignTrim(ref, deg [][]float64, minLag, maxLag int) (refOut, degOut [][]float64, lag int) {
	lag = quality.AlignLag(ref[0], deg[0], minLag, maxLag)
	refOut = make([][]float64, len(ref))
	degOut = make([][]float64, len(ref))
	for c := range ref {
		r, d := ref[c], deg[c]
		if lag >= 0 {
			d = d[min(lag, len(d)):]
		} else {
			r = r[min(-lag, len(r)):]
		}
		n := min(len(r), len(d))
		refOut[c], degOut[c] = r[:n], d[:n]
	}
	return refOut, degOut, lag
}

// crossCheck decodes aacName a second time through ffmpeg's fixed-point AAC
// decoder and compares it to deg, this project's pcm decode of the same
// stream, logging a warning when the two fall below crosscheckMinSNR. It is
// best-effort and diagnostic only: any error and any result leave the case's
// measured metrics untouched, so it can only ever add a log line, never change
// a number or fail a case.
func crossCheck(ctx context.Context, tl tools, dir, name, aacName string, deg [][]float64, sampleRate int, errw io.Writer) {
	// crossCheckDecode forces ffmpeg to len(deg) channels, so ff always has the
	// same channel count as deg; no channel-count guard is needed.
	ff, err := crossCheckDecode(ctx, tl.ffmpeg, dir, aacName, sampleRate, len(deg))
	if err != nil {
		logf(errw, "warning: crosscheck %s/%s: %v\n", filepath.Base(dir), name, err)
		return
	}
	// Both sides decode the SAME stream, so the lag is near zero, but either
	// side can be the trimmed one, so align over a SYMMETRIC window rather than
	// the asymmetric one measure uses for a coded stream against its source.
	degA, ffA, lag := alignTrim(deg, ff, -alignMaxLag, alignMaxLag)
	if len(degA) == 0 || len(degA[0]) == 0 {
		logf(errw, "warning: crosscheck %s/%s: pcm and ffmpeg decodes do not overlap (lag %d)\n", filepath.Base(dir), name, lag)
		return
	}
	// Score the WORST channel, not a mean: a mean would let one agreeing or one
	// silent channel hide a real divergence on another. A channel whose pcm side
	// is digital silence has an undefined SNR (NaN) and is skipped. If every
	// channel is silent, worst stays +Inf and nothing fires, which is correct.
	worst := math.Inf(1)
	for c := range degA {
		s := quality.SNR(degA[c], ffA[c])
		if math.IsNaN(s) {
			continue
		}
		worst = min(worst, s)
	}
	if worst < crosscheckMinSNR {
		logf(errw, "warning: crosscheck %s/%s: pcm and ffmpeg decodes diverge at %.1f dB (lag %d), below %.0f dB\n",
			filepath.Base(dir), name, worst, lag, crosscheckMinSNR)
	}
}

// crossCheckDecode decodes aacName inside dir through ffmpeg's fixed-point AAC
// decoder (aac_fixed) to planar float64 channels, the reference second decoder
// for the -crosscheck diagnostic. aac_fixed is the integer decoder this
// project's pcm decode is proven byte-identical to, so its S16LE output on the
// same 1<<15 scale is the right comparison; a floating aac decoder would add a
// harmless but real difference and blunt the check. It forces the output
// layout to channels at sampleRate (the stream's own, so this asserts the
// layout rather than resampling).
func crossCheckDecode(ctx context.Context, ffmpeg, dir, aacName string, sampleRate, channels int) ([][]float64, error) {
	rawName := aacName + ".s16"
	if out, err := runTool(ctx, dir, ffmpeg, "-v", "error", "-y", "-c:a", "aac_fixed", "-i", aacName,
		"-f", "s16le", "-ac", fmt.Sprint(channels), "-ar", fmt.Sprint(sampleRate), rawName); err != nil {
		return nil, fmt.Errorf("ffmpeg decode: %w: %s", err, strings.TrimSpace(out))
	}
	b, err := os.ReadFile(filepath.Join(dir, rawName))
	if err != nil {
		return nil, err
	}
	return deinterleaveS16LE(b, channels)
}
