# go-aac

[![CI](https://github.com/tphakala/go-aac/actions/workflows/ci.yml/badge.svg)](https://github.com/tphakala/go-aac/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tphakala/go-aac.svg)](https://pkg.go.dev/github.com/tphakala/go-aac)
[![Go Version](https://img.shields.io/github/go-mod/go-version/tphakala/go-aac)](go.mod)
[![License: LGPL-2.1-or-later](https://img.shields.io/badge/License-LGPL--2.1--or--later-blue.svg)](LICENSE)
[![Sponsor](https://img.shields.io/github/sponsors/tphakala?logo=githubsponsors&color=ea4aaa&label=Sponsor)](https://github.com/sponsors/tphakala)

Pure-Go AAC-LC encoder, ported from FFmpeg's native AAC encoder. No cgo and no
external libraries in the published module.

## Status

Every layer is validated against the C reference before it lands (see
Approach), so the pieces marked done are done in the strong sense.

- **Encoder: complete for AAC-LC.** All three FFmpeg coders (NMR, twoloop,
  fast), all four coding tools (TNS, PNS, M/S, I/S), mono and stereo, 44.1 and
  48 kHz, ADTS output. The NMR coder is the default, as it is upstream.
- **Decoder: in progress, not yet usable end to end.** The fixed-point
  flavor, validated bit-exactly against `ffmpeg -c:a aac_fixed`. Landed so
  far: bitstream and framing parse (ADTS, AudioSpecificConfig) plus the
  quantized spectral symbol decode (1,999,224 symbols byte-identical to the C
  across the test corpus), and the int32 inverse MDCT with the integer windows
  and overlap-add (bit-exact on every dumped value). Still to come:
  dequantization and tool application (TNS/PNS/M/S/I/S), full PCM
  reconstruction, and the public decoder API.

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
multichannel beyond stereo, VBR (`global_quality`), MP4 muxing.

## Approach

go-aac is a faithful port of FFmpeg's AAC encoder at a pinned commit
(`d09d5afc3a`), kept honest by differential testing against the real C.

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

PSNR cannot tell you that a psychoacoustic constant was misported, that a bit
reservoir is drifting, or that a Viterbi path was suboptimal. These harnesses
can, and they caught real bugs that would otherwise have shipped silently.

While the port is in progress the internal packages are deliberately written in
a C-shaped style, so they stay diffable against upstream FFmpeg and every
ported function carries a provenance comment naming its C origin. That
constraint is temporary: once porting is complete and the differential gates
are green, the internals get rewritten in idiomatic Go alongside the
optimization work. The public API is idiomatic Go today.

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

## Benchmarking

Single-threaded, Apple M-series, 48 kHz, 128 kbps:

| Coder | Channels | x realtime |
| ----- | -------- | ---------: |
| NMR (default) | mono | 65x |
| NMR (default) | stereo | 38x |
| twoloop | stereo | 48x |
| fast | mono | 206x |

Steady-state encoding is allocation-free (0 allocs/frame) for every coder, mono
and stereo. SIMD kernels are future work; the scalar port is the canonical
reference.

## License

LGPL-2.1-or-later. go-aac is a derivative work of FFmpeg's LGPL-licensed AAC
encoder and cannot be relicensed permissively. See [LICENSE](LICENSE) and
[PROVENANCE.md](PROVENANCE.md).

## Sponsor

If go-aac is useful to you, consider sponsoring its development.

[![Sponsor on GitHub](https://img.shields.io/github/sponsors/tphakala?logo=githubsponsors&color=ea4aaa&label=Sponsor%20%40tphakala)](https://github.com/sponsors/tphakala)
