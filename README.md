# go-aac

[![CI](https://github.com/tphakala/go-aac/actions/workflows/ci.yml/badge.svg)](https://github.com/tphakala/go-aac/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tphakala/go-aac.svg)](https://pkg.go.dev/github.com/tphakala/go-aac)
[![Go Version](https://img.shields.io/github/go-mod/go-version/tphakala/go-aac)](go.mod)
[![License: LGPL-2.1-or-later](https://img.shields.io/badge/License-LGPL--2.1--or--later-blue.svg)](LICENSE)
[![Sponsor](https://img.shields.io/github/sponsors/tphakala?logo=githubsponsors&color=ea4aaa&label=Sponsor)](https://github.com/sponsors/tphakala)

Pure-Go AAC-LC encoder and decoder, ported from FFmpeg's native AAC encoder and
fixed-point decoder. No cgo and no external libraries in the published module.

## Status

Every layer is validated against the C reference before it lands (see
Approach), so the pieces marked done are done in the strong sense.

- **Encoder: complete for AAC-LC.** All three FFmpeg coders (NMR, twoloop,
  fast), all four coding tools (TNS, PNS, M/S, I/S), mono and stereo, 44.1 and
  48 kHz, ADTS output. The NMR coder is the default, as it is upstream.
- **Decoder: usable for AAC-LC.** Pure fixed point, producing output identical
  to `ffmpeg -c:a aac_fixed` at the sample level. The public `pcm.NewDecoder`
  streams an ADTS (or raw plus ASC) AAC-LC stream to interleaved little-endian
  S16 PCM, matching the oracle byte for byte across the test corpus and on Apple
  afconvert output, with no cgo. Mono and stereo, 44.1 and 48 kHz. Not yet
  covered: HE-AAC (SBR/PS), 960-sample frames, and channel configs above stereo.

Quality tracks the C encoder closely. At 96/128/192 kbps stereo with the NMR
coder on both sides, decoded PSNR is within **+-0.04 dB** of FFmpeg's own
output and stream sizes within **0.22%**.

On real field recordings the port slightly exceeds the C encoder at the same
bitrate:

| Recording (48 kHz mono, 128 kbps) | go-aac | FFmpeg (same coder) |
| --------------------------------- | -----: | ------------------: |
| 120 s dawn chorus                 | **85.44 dB** | 85.42 dB |
| 15 s distant owl call             | **63.90 dB** | 63.87 dB |

Not implemented: HE-AAC (SBR/PS), xHE-AAC, LATM, ER/LD/ELD profiles,
multichannel beyond stereo, VBR (`global_quality`), MP4 muxing (the pure-Go
[go-m4a](https://github.com/tphakala/go-m4a) is the container companion).

## Approach

go-aac is a faithful port of FFmpeg's AAC encoder and fixed-point decoder at a
pinned commit (`d09d5afc3a`), kept honest by differential testing against the
real C.

For each subsystem, a C harness links the pinned FFmpeg libraries, runs the
**actual FFmpeg function** on identical input, and dumps its internals; the Go
port must then reproduce them. That is a far sharper instrument than PSNR:

| Harness | What it pins | Result |
| ------- | ------------ | ------ |
| `tools/cdump` | MDCT, KBD windows, LPC | 1.17e-07 relative / bit-exact / 0 |
| `tools/gentables` | 31 codec tables | byte-identical |
| `tools/cquant` | quantizer search, codebook trellis, band encoding | 128/128 band decisions, byte-identical bitstreams |
| `tools/cpsy` | the 3GPP psychoacoustic model | window decisions identical, bit reservoir exact |
| `tools/cnmr` | the NMR Viterbi trellis and rate control | bit-exact, tie-breaking included |
| `tools/ctns`, `tools/ctwoloop` | TNS and the twoloop coder | bit-exact |
| decoder gates | LC symbol decode, int32 IMDCT, full reconstruction, s16 PCM | 1,999,224 symbols + 12,969,984 reconstructed values byte-identical; s16 PCM identical to `ffmpeg -c:a aac_fixed` |

PSNR cannot tell you that a psychoacoustic constant was misported, that a bit
reservoir is drifting, or that a Viterbi path was suboptimal. These harnesses
can, and they caught real bugs that would otherwise have shipped silently.

The internal packages are deliberately written in a C-shaped style, so they
stay diffable against upstream FFmpeg, and every ported function carries a
provenance comment naming its C origin. That constraint is temporary: with the
AAC-LC port complete and the differential gates green, the idiomatic-Go rewrite
happens alongside the optimization work. The public API is idiomatic Go today.

## Install

```sh
go get github.com/tphakala/go-aac
```

## Usage

The library has two layers, mirroring [go-flac](https://github.com/tphakala/go-flac)
(`flac` + `pcm`) and [go-opus](https://github.com/tphakala/go-opus)
(`opus` + `oggopus`).

### pcm: the streaming layer

Interleaved little-endian integer PCM in via `io.Writer`, a self-framing ADTS
stream out. This is the right entry point for almost all callers.

```go
import aacpcm "github.com/tphakala/go-aac/pcm"

cfg := aacpcm.Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}

// One shot (encoder drawn from a pool, safe for concurrent use):
err := aacpcm.EncodeInterleaved(w, cfg, pcmBytes)

// Or streaming, accepting any chunk size:
e, err := aacpcm.NewEncoder(w, cfg)
_, err = io.Copy(e, src)
err = e.Close()
```

`Write` accepts arbitrary chunk sizes and buffers partial samples internally,
so `io.Copy` works with any buffer, including sizes that do not divide the
sample stride.

The package name deliberately collides with `go-flac/pcm`; import it with an
alias (`aacpcm`), which is ordinary Go practice and lets a consumer switch
between the two encoders with the same call shape.

Decoding mirrors go-flac's `pcm.Decoder`: an AAC-LC stream in via `io.Reader`,
interleaved little-endian S16 PCM out.

```go
d, err := aacpcm.NewDecoder(r) // ADTS by default, resynced past leading garbage
if err != nil {                // classify with errors.Is against aacpcm.ErrCorruptStream or aacpcm.ErrUnsupported
    return err
}
info := d.Info()       // SampleRate, Channels, Profile, valid immediately
_, err = io.Copy(w, d) // WriteTo drains the whole decode; Read fills any buffer
```

The decoded PCM is byte-identical to `ffmpeg -c:a aac_fixed -f s16le` on every
LC stream tested, including Apple afconvert output. The decoder never panics on
malformed input (it returns wrapped `ErrCorruptStream` or `ErrUnsupported`
sentinels) and runs at zero allocations per frame in steady state. Raw access
units plus an `AudioSpecificConfig` are opt in via `aacpcm.WithRawStream(asc)`.

### aac: the low-level codec

Planar float32 frames in, raw AAC access units out, append-style and
allocation-free in steady state.

```go
import "github.com/tphakala/go-aac"

e, err := aac.NewEncoder(aac.EncoderConfig{SampleRate: 48000, Channels: 1, Bitrate: 128000})
au, err := e.EncodeFrame(au[:0], [][]float32{frame}) // up to aac.FrameSize (1024) samples
```

Raw access units are not self-framing. Use `aac.AppendADTSHeader` to build a
streamable ADTS stream, or `Encoder.AudioSpecificConfig` to mux them elsewhere.

## Gapless playback

ADTS cannot signal encoder delay. Decoders emit roughly 1024 extra leading
samples, and every AAC-in-ADTS stream behaves this way. Compute clip durations
from the source PCM, not from the decoded AAC length.

For gapless, sample-accurate output, mux into a container that carries an edit
list. [go-m4a](https://github.com/tphakala/go-m4a) is the pure-Go MP4/M4A muxer
and demuxer that pairs with go-aac for exactly this: it writes the encoder
priming (`aac.EncoderDelay`, also `Encoder.Delay`) into an `elst` edit list so
playback is gapless, and reads `.m4a` files back into access units. Its
`aacm4a` subpackage is a one-call bridge over go-aac, PCM to `.m4a` and back.
No cgo and no external binaries.

## Benchmarking

`scripts/bench-encoders.sh` compares go-aac against FFmpeg's native AAC
encoder, the C this library is ported from, on the same input (encode
single-threaded, one process, file in and file out), reporting wall time, CPU
seconds, peak RSS and stream size. `GOAAC_FFMPEG` must point at the pinned
oracle build; a distro FFmpeg is refused, because 7.x and earlier ship a
different coder set whose `anmr` is not the `nmr` trellis this library ports.

```sh
GOAAC_FFMPEG=/path/to/pinned/ffmpeg scripts/bench-encoders.sh          # generated reproducible input
GOAAC_FFMPEG=/path/to/pinned/ffmpeg scripts/bench-encoders.sh my.wav   # your own WAV
```

Results on a 120 s 48 kHz mono recording at 128 kbps, single-threaded. The
ratio is CPU seconds, go-aac over FFmpeg; the FFmpeg CLI spawns helper threads,
so CPU time compares more honestly than wall time.

| Coder | Platform | go-aac | FFmpeg | go/C |
| ----- | -------- | -----: | -----: | ---: |
| NMR (default) | Raspberry Pi 5 | 19x realtime | 40x | 2.09x |
| NMR (default) | x86_64 (i7-1260P) | 43x | 98x | 2.09x |
| NMR (default) | Apple M4 Pro | 68x | 179x | 2.64x |
| twoloop | x86_64 | 78x | 156x | 1.74x |
| fast | x86_64 | 143x | 308x | 1.87x |

go-aac runs at roughly **twice the CPU time of the C** and in **about a third
of the memory** (4.1 MB peak RSS against 10.4 to 13.3 MB). Stream sizes match
FFmpeg to within 0.001% for the NMR and fast coders at the same bitrate.

The gap is **not** FFmpeg's hand-written assembly: disabling it (`-cpuflags 0`)
changes AAC encoding by about 1%. It is compiler auto-vectorization. GCC emits
631 packed floating-point arithmetic instructions in `aaccoder.o` from plain C,
concentrated in the NMR quantizer search; Go's compiler emits none anywhere in
the equivalent package. Closing the gap therefore means hand-writing in Go what
C compilers generate for free. The optional SIMD trellis below takes the first
step; the scalar port remains the canonical reference.

Steady-state encoding is allocation-free (0 allocs/frame) for every coder, mono
and stereo. Decoding is far cheaper, roughly 3000x real time for mono and 1500x
for stereo at 48 kHz, and is likewise allocation-free per frame in steady state.

### Optional SIMD trellis (`-tags goaac_simd`)

Building with `-tags goaac_simd` swaps the encoder's hottest kernel, the NMR
Viterbi trellis search, for a SIMD implementation built on
[github.com/tphakala/simd](https://github.com/tphakala/simd): AVX2 on x86_64,
NEON on arm64, and a portable Go fallback everywhere else. Every backend is
bit-identical, so the tagged build produces byte-identical output to the scalar
default and passes the same differential oracle gate, not a relaxed PSNR tier.

Measured full-encode speedups over the scalar default (128 kbps, single
recording, `benchstat` over interleaved rounds):

| Platform | Full-encode NMR |
| -------- | --------------: |
| Raspberry Pi 5 (Cortex-A76, NEON) | 14% faster |
| x86_64 i7-1260P (AVX2) | 22% faster |

The default build stays pure Go, with no assembly in the binary and the `simd`
dependency linked only under the tag. The tag is opt-in and the scalar kernel
remains canonical.

## License

LGPL-2.1-or-later. go-aac is a derivative work of FFmpeg's LGPL-licensed AAC
encoder and cannot be relicensed permissively. See [LICENSE](LICENSE) and
[PROVENANCE.md](PROVENANCE.md).

## Sponsor

If go-aac is useful to you, consider sponsoring its development.

[![Sponsor on GitHub](https://img.shields.io/github/sponsors/tphakala?logo=githubsponsors&color=ea4aaa&label=Sponsor%20%40tphakala)](https://github.com/sponsors/tphakala)
