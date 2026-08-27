# go-aac

[![CI](https://github.com/tphakala/go-aac/actions/workflows/ci.yml/badge.svg)](https://github.com/tphakala/go-aac/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tphakala/go-aac.svg)](https://pkg.go.dev/github.com/tphakala/go-aac)
[![codecov](https://codecov.io/gh/tphakala/go-aac/branch/main/graph/badge.svg)](https://codecov.io/gh/tphakala/go-aac)
[![Go Version](https://img.shields.io/github/go-mod/go-version/tphakala/go-aac)](go.mod)
[![Latest tag](https://img.shields.io/github/v/tag/tphakala/go-aac?sort=semver&label=release)](https://github.com/tphakala/go-aac/tags)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/tphakala/go-aac/badge)](https://scorecard.dev/viewer/?uri=github.com/tphakala/go-aac)
[![License: LGPL-2.1-or-later](https://img.shields.io/badge/License-LGPL--2.1--or--later-blue.svg)](LICENSE)
[![Sponsor](https://img.shields.io/github/sponsors/tphakala?logo=githubsponsors&color=ea4aaa&label=Sponsor)](https://github.com/sponsors/tphakala)

Pure-Go AAC-LC encoder and decoder, ported from FFmpeg's native AAC encoder and
fixed-point decoder. No cgo and no external libraries in the published module.

It is the AAC member of a family of pure-Go audio libraries that also covers
[WAV](https://github.com/tphakala/go-wav),
[FLAC](https://github.com/tphakala/go-flac),
[Opus](https://github.com/tphakala/go-opus) and
[M4A](https://github.com/tphakala/go-m4a), and it presents the same API shape as
its siblings, so a program that already speaks one of them speaks this one too.

## Status

Every layer is validated against the C reference before it lands (see
Approach), so the pieces marked done are done in the strong sense.

- **Encoder: complete for AAC-LC.** All three FFmpeg coders (NMR, twoloop,
  fast), all four coding tools (TNS, PNS, M/S, I/S), mono and stereo, 44.1 and
  48 kHz, ADTS output. The NMR coder is the default, as it is upstream at the
  pin (`aac_coder` is `AAC_CODER_NMR`, and the commit that made it so says the
  old coders will soon be removed). FFmpeg's own released docs still describe
  the older set, in which `twoloop` is the default and `anmr` is a different,
  experimental coder; that text is stale, not a contradiction.
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

Interleaved little-endian integer PCM in, a self-framing ADTS stream out via
`io.Writer`, or raw access units out through a callback for muxing. This is the
right entry point for almost all callers.

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

Muxing into MP4 or fragmented MP4 (CMAF) needs the opposite of ADTS: raw access
units, boundaries reported out of band. `FrameEncoder` is that path, and it is
the same pipeline, so the units are byte-identical to the ADTS stream's
payloads.

```go
fe, err := aacpcm.NewFrameEncoder(cfg)
asc := fe.AudioSpecificConfig() // esds DecoderSpecificInfo, valid before any audio
_ = asc                         // goes in the init segment's esds box
emit := func(au []byte, samples int) error {
    segment = append(segment, au...) // au is borrowed; copy or append
    return nil
}
err = fe.EncodeInterleaved(pcm, emit)
err = fe.Flush(emit) // drains the priming frame; fe.Delay() gives the elst media_time
```

`Delay()` and the per-unit `samples` count are both PCM samples per channel, so
a muxer whose track timescale is not the sample rate scales them into
media-timescale ticks first.

The emit callback has the same shape as go-flac's `pcm.FrameEncoder`, so a
muxer's per-unit path is shared between the two codecs; the lifecycle differs,
since AAC-LC needs a `Flush` to drain the priming frame where the FLAC frame
encoder is one-shot.

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
Most muxing callers want `pcm.FrameEncoder` instead, which does the conversion,
framing and priming for them.

## Gapless playback

ADTS cannot signal encoder delay. Decoders emit roughly 1024 extra leading
samples, and every AAC-in-ADTS stream behaves this way. Compute clip durations
from the source PCM, not from the decoded AAC length.

For gapless, sample-accurate output, mux into a container that carries an edit
list, feeding the muxer from `pcm.FrameEncoder`.
[go-m4a](https://github.com/tphakala/go-m4a) is the pure-Go MP4/M4A muxer
and demuxer that pairs with go-aac for exactly this: it writes the encoder
priming (`aac.EncoderDelay`, also `pcm.FrameEncoder.Delay`) into an `elst` edit list so
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
PROVENANCE.md carries the required configure recipe, including the
`-ffp-contract=off` that is part of the pin.

```sh
GOAAC_FFMPEG=/path/to/pinned/ffmpeg scripts/bench-encoders.sh          # generated reproducible input
GOAAC_FFMPEG=/path/to/pinned/ffmpeg scripts/bench-encoders.sh my.wav   # your own WAV
```

Results on a 120 s 48 kHz mono recording at 128 kbps, single-threaded, over a
real broadband recording that keeps the NMR search fully loaded; a sparse
synthetic tone understates it. The ratio is CPU seconds, go-aac over FFmpeg;
the FFmpeg CLI spawns helper threads, so CPU time compares more honestly than
wall time. The x86_64 figures are pinned to the performance cores for stability
on that hybrid part; the Pi 5 is a single core cluster and needs no pinning.

| Coder | Platform | go-aac | FFmpeg | go/C |
| ----- | -------- | -----: | -----: | ---: |
| NMR (default) | Raspberry Pi 5 | 38x realtime | 38x | 1.02x |
| NMR (default) | x86_64 (i7-1260P) | 90x | 99x | 1.12x |
| twoloop | Raspberry Pi 5 | 42x | 69x | 1.65x |
| twoloop | x86_64 (i7-1260P) | 105x | 164x | 1.57x |
| fast | Raspberry Pi 5 | 90x | 138x | 1.53x |
| fast | x86_64 (i7-1260P) | 203x | 308x | 1.55x |

On the default NMR coder go-aac is **at parity** with the C in CPU time on the
Pi 5 (1.02x) and within about 12% of it on the i7-1260P (1.12x), in **a third
to a half of the memory** (roughly 4 MB peak RSS on the Pi and 6 to 8 MB on
x86_64, against about 12 to 15 MB). The twoloop and fast coders stay modestly
behind, roughly 1.5x to 1.65x on both platforms. Stream sizes track FFmpeg
closely at the same bitrate, within about 0.001%
for NMR and on the order of 0.01% for the other coders. These numbers moved a
long way from the first baseline, where the NMR coder cost about twice the C's
CPU time; the default SIMD kernels and the scalar-path work since then roughly
doubled its throughput.

That closing was compiler auto-vectorization, now hand-written in Go. GCC emits
631 packed floating-point arithmetic instructions in `aaccoder.o` from plain C,
concentrated in the NMR quantizer search; Go's compiler emits none anywhere in
the equivalent package. Disabling FFmpeg's hand-written assembly (`-cpuflags 0`)
changes AAC encoding by only about 1%, so the gap was never the asm. The default
SIMD kernels below reproduce that vectorization for the NMR trellis and
quantizer, which is what brought the default coder level with the C; twoloop and
fast are not targeted as heavily and keep more of the gap. The scalar port
remains the canonical reference.

Steady-state encoding is allocation-free (0 allocs/frame) for every coder, mono
and stereo. Decoding is far cheaper, roughly 3000x real time for mono and 1500x
for stereo at 48 kHz, and is likewise allocation-free per frame in steady state.

### SIMD kernels (default, opt out with `-tags noasm`)

By default the encoder's hottest kernels are SIMD implementations built on
[github.com/tphakala/simd](https://github.com/tphakala/simd): the NMR Viterbi
trellis search, the AbsPow34 magnitude transform (`|x|^(3/4)`), and the
QuantizeBands quantizer. The `simd` library picks the widest path the CPU
supports at runtime and falls back to pure Go on any CPU without it. All use NEON
on arm64. On x86_64 the trellis needs AVX2 and falls back to portable Go without
it, while AbsPow34 and QuantizeBands use AVX (AbsPow34 with an SSE path below
that). Every backend is bit-identical, so the default build produces
byte-identical output to the scalar path and passes the same differential oracle
gate, not a relaxed PSNR tier.

Building with `-tags noasm` selects the pure-Go scalar kernels instead: no
assembly in the binary and the `simd` dependency linked out entirely, for a
smaller and more easily audited build. The scalar kernels stay canonical and are
the reference the SIMD ones are gated against.

Measured full-encode NMR speedups of the SIMD default over the `-tags noasm`
scalar build (128 kbps, single recording, `benchstat` over interleaved rounds).
Every percentage here is a reduction in encode time, so 15% faster means the SIMD
default spends 15% less time, not that it does 15% more work per second:

| Platform | SIMD trellis | Both kernels |
| -------- | -----------: | -----------: |
| Raspberry Pi 5 (Cortex-A76, NEON) | about 14% faster | about 15% faster |
| x86_64 i7-1260P (AVX2) | 22% faster | about 24% faster |

The trellis search is the larger lever. On top of it the AbsPow34 kernel adds
roughly a further 2.7% on the i7-1260P (4.6% with the psychoacoustic tools
disabled) and about 1.2% on the Pi 5 (1.3% with the tools disabled). Those
increments are measured against the trellis-only build, not the scalar one, so
they compound rather than add. The QuantizeBands quantizer is SIMD by default as
well; its separate speedup is not broken out in the table above.

The Pi 5 row comes from a three-way interleaved run in one session on an
otherwise idle machine: the `-tags noasm` scalar build, a trellis-only build
(this tree with AbsPow34 held at its scalar kernel) and the full SIMD build,
`benchstat` over 10 rounds each (n=10 per build, p=0.000). That gives 3.716 s,
3.210 s and 3.173 s per `BenchmarkEncodeFrames` pass over a 120 s recording, so
13.6% and 14.6% against the scalar. Those seconds time the codec alone in a warm
loop, so they do not correspond to the realtime multiples in the
`bench-encoders.sh` table above, which are whole-process wall clock over a
different harness. All three kernels are byte-identical to the scalar port, so
the choice is a pure speed knob with no effect on output.

The two builds differ only in speed, and because `-tags noasm` silently drops to
the scalar path a downstream release can end up there unnoticed.
`aac.SIMDEnabled()` reports which kernel set was compiled in, so a build or
startup check can assert it:

```go
if !aac.SIMDEnabled() {
    log.Println("go-aac: scalar kernels (built with -tags noasm); the default build has the SIMD ones")
}
```

The answer describes what was compiled in, not what the CPU running the binary
supports.

## Sponsor

go-aac is maintained in my own time. If it is useful to you or your project, you
can support continued maintenance through GitHub Sponsors; sponsorship is
entirely optional and never gates any feature.

[![Sponsor on GitHub](https://img.shields.io/github/sponsors/tphakala?logo=githubsponsors&color=ea4aaa&label=Sponsor%20%40tphakala)](https://github.com/sponsors/tphakala)

## License

LGPL-2.1-or-later. go-aac is a derivative work of FFmpeg's LGPL-licensed AAC
encoder and cannot be relicensed permissively. See [LICENSE](LICENSE) and
[PROVENANCE.md](PROVENANCE.md).
