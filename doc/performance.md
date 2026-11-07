# Performance

## Benchmarking

`scripts/bench-encoders.sh` compares go-aac against FFmpeg's native AAC
encoder, the C this library is ported from, on the same input (encode
single-threaded, one process, file in and file out), reporting wall time, CPU
seconds, peak RSS and stream size. `GOAAC_FFMPEG` must point at the pinned
oracle build; a distro FFmpeg is refused, because 7.x and earlier ship a
different coder set whose `anmr` is not the `nmr` trellis this library ports.
PROVENANCE.md carries the required configure recipe, including the
`-ffp-contract=off` that is part of the pin. Each run writes to a new private
`go-aac-bench.*` directory under `GOAAC_BENCH_WORK`, or under `$TMPDIR`
(default `/tmp`) when that is unset, and prints its path. The script removes
nothing.

```sh
GOAAC_FFMPEG=/path/to/pinned/ffmpeg scripts/bench-encoders.sh          # generated reproducible input
GOAAC_FFMPEG=/path/to/pinned/ffmpeg scripts/bench-encoders.sh my.wav   # your own WAV
```

Results for v0.7.2 on a 120 s 48 kHz mono recording at 128 kbps, over a real
broadband recording that keeps the NMR search fully loaded; a sparse synthetic
tone understates it. Each figure is the median of five runs on an otherwise
idle machine, with both encoders pinned to one core. The ratio is CPU seconds,
go-aac over FFmpeg; the FFmpeg CLI spawns helper threads, so CPU time compares
more honestly than wall time.

| Coder | Platform | go-aac | FFmpeg | go/C |
| ----- | -------- | -----: | -----: | ---: |
| NMR (default) | Raspberry Pi 5 (Cortex-A76) | 43x realtime | 35x | 0.83x |
| NMR (default) | x86_64 (i5-7260U) | 58x | 47x | 0.81x |
| twoloop | Raspberry Pi 5 (Cortex-A76) | 37x | 56x | 1.49x |
| twoloop | x86_64 (i5-7260U) | 48x | 67x | 1.42x |
| fast | Raspberry Pi 5 (Cortex-A76) | 112x | 135x | 1.22x |
| fast | x86_64 (i5-7260U) | 129x | 152x | 1.16x |

On the default NMR coder go-aac is **faster than the C**, using about 17% less
CPU time on the Pi 5 (0.83x) and 19% less on the i5-7260U (0.81x), in **about a
third of the memory or less** (about 4 MB peak RSS on both, against 12 MB for
FFmpeg on the Pi and 18 MB on x86_64). The twoloop and fast coders stay behind,
roughly 1.2x to 1.5x. Stream sizes track FFmpeg closely at the same bitrate,
within about 0.001% for NMR and on the order of 0.01% for the other coders.
These numbers moved a long way from the first baseline, where the NMR coder
cost about twice the C's CPU time; the default SIMD kernels and the scalar-path
work since then more than doubled its throughput.

The gap that closed was compiler auto-vectorization, now hand-written. GCC emits
631 packed floating-point arithmetic instructions in `aaccoder.o` from plain C,
concentrated in the NMR quantizer search; Go's compiler emits none anywhere in
the equivalent package. Disabling FFmpeg's hand-written assembly (`-cpuflags 0`)
changes AAC encoding by only about 1%, so the gap was never the asm. The default
SIMD kernels below reproduce that vectorization for the NMR trellis, the
quantizer and the band cost, which is what took the default coder past the C;
twoloop and fast are not targeted as heavily and keep more of the gap. The
scalar port remains the canonical reference.

Steady-state encoding is allocation-free (0 allocs/frame) for every coder, mono
and stereo. Decoding is far cheaper: `pcm.Decoder` turns a 128 kbps 48 kHz
stream into 16-bit PCM at about 540x real time for mono and 310x for stereo on
the Pi 5, and 780x and 450x on the i5-7260U (one core, best of 30 passes over
120 s of audio). It is likewise allocation-free per frame in steady state.

## Profiling

Profile on Linux. On darwin/arm64, pprof fabricates hot spots: it has parked 10%
to 16% of encode on cheap bulk-memory leaves
(`encoding/binary.littleEndian.Uint16`, `runtime.memmove`) that direct
measurement puts at 0.2% and 0.04%, wrong by 47x and 260x, and the phantom moves
between runs while landing on whatever streaming leaf is nearby. The same
benchmarks on linux/amd64 and linux/arm64 are clean, and
cumulative shares for large subsystems stay trustworthy on macOS; it is leaf
attribution that is not. If you must profile on a Mac, reconcile every leaf
number against call counts before acting on it: count the calls, microbenchmark
the leaf at the shapes the real encode drives, multiply, and compare against
encode wall time. A disagreement above about 2x means the profile is wrong, not
the arithmetic. Run benchmark cases isolated and min-of-N when a number will feed
a decision; a back-to-back batch produced a spurious 1.9x on one case here.

## SIMD kernels (default, opt out with `-tags noasm`)

By default the encoder's hottest kernels are SIMD implementations built on
[github.com/tphakala/simd](https://github.com/tphakala/simd): the NMR Viterbi
trellis search and its node cost, the AbsPow34 magnitude transform
(`|x|^(3/4)`), the QuantizeBands quantizer, and the LAME high-pass filter of the
psychoacoustic model. The `simd` library picks the widest path the CPU
supports at runtime and falls back to pure Go on any CPU without it. All use NEON
on arm64. On x86_64 the trellis step needs AVX2 and falls back to portable Go without
it, while the node cost, the high-pass filter, AbsPow34 and QuantizeBands use AVX
(AbsPow34 with an SSE path below that). The cost-only band cost evaluated by every rate-distortion estimate is
Go assembly in this module: AVX2 on x86_64 and NEON on arm64, with the scalar
loop on any other CPU and under `SIMD_DISABLE`. Every backend is bit-identical,
so the default build produces byte-identical output to the scalar path and
passes the same differential oracle gate, not a relaxed PSNR tier.

Building with `-tags noasm` selects the pure-Go scalar kernels instead: no
assembly in the binary and the `simd` dependency linked out entirely, for a
smaller and more easily audited build. The scalar kernels stay canonical and are
the reference the SIMD ones are gated against.

Measured full-encode speedups of the SIMD default over the `-tags noasm` scalar
build for v0.7.2: `BenchmarkEncodeFrames` over a 120 s 48 kHz mono recording at
128 kbps, one pinned core, `GOMAXPROCS=1`, 10 interleaved rounds per build,
`benchstat` medians (every row p < 0.01). Every percentage here is a reduction
in encode time, so 30% faster means the SIMD default spends 30% less time, not
that it does 30% more work per second:

| Coder | Raspberry Pi 5 (Cortex-A76, NEON) | x86_64 i5-7260U (AVX2) |
| ----- | --------------------------------: | ---------------------: |
| NMR (default) | 33% faster (4.18 s to 2.82 s) | 45% faster (3.82 s to 2.08 s) |
| twoloop | 17% faster (3.83 s to 3.20 s) | 18% faster (3.05 s to 2.49 s) |
| fast | 30% faster (1.51 s to 1.06 s) | 31% faster (1.33 s to 0.92 s) |

The seconds are one `BenchmarkEncodeFrames` pass over the recording. They time
the codec alone in a warm loop, so they do not correspond to the realtime
multiples in the `bench-encoders.sh` table above, which are whole-process wall
clock. Every SIMD kernel listed above is byte-identical to the scalar port, so
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
