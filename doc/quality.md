# Quality and validation

## Scope

Every layer is validated against the C reference before it lands (see
Validation below).

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
  afconvert output, with no cgo. Mono and stereo across the AAC-LC sample-rate
  range: the parser accepts the full MPEG-4 sample-rate table (7.35 to 96 kHz),
  and decode is byte-verified against the oracle at representative rates from 8
  to 96 kHz. The encoder, separately, is restricted to 44.1 and 48 kHz. HE-AAC
  (SBR/PS) is rejected with a precise `ErrUnsupportedSBR` / `ErrUnsupportedPS`
  so a caller can hand it to an external decoder; 960-sample frames and channel
  configs above stereo are also not covered.

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

## Audio quality

go-aac is competitive with ffmpeg's built-in AAC-LC encoder: it matches or beats
it on waveform fidelity and PEAQ, while ViSQOL can favour either depending on the
material. ffmpeg's native `aac` is a modest reference; a fully tuned encoder such
as libfdk_aac would be a stronger opponent.

Quality is measured objectively, not by ear: real 48 kHz recordings are encoded,
decoded, and scored against the original.

- ViSQOL (MOS, 1-5, higher is better): predicted listening-test score; 4.5+ is
  effectively transparent.
- PEAQ (ITU-R BS.1387 ODG, 0 to -4, closer to 0 is better): audibility of
  impairment; 0 imperceptible, -1 audible but not annoying, -2 slightly
  annoying.
- SNR and spectral distance (dB): raw fidelity. A high SNR alone does not
  guarantee good perceived quality.

Snapshot (2026-09-05), 128 kbps, decoded output scored against the source:

| Clip              | go-aac ViSQOL / PEAQ | ffmpeg aac ViSQOL / PEAQ |
|-------------------|----------------------|--------------------------|
| nature soundscape | 4.64 / -0.01         | 4.63 / -0.37             |
| pygmy owl         | 4.20 / -0.05         | 4.49 / -0.10             |

On the soundscape go-aac is transparent while ffmpeg shows a small penalty; on
the owl clip ffmpeg scores higher on ViSQOL while go-aac keeps better PEAQ and
raw fidelity. Net: comparable and material-dependent. This is a small
directional sample, not a full benchmark; results vary with material and
bitrate.

A corpus-based quality gate guards against regressions. `task quality:gate`
re-measures the encoder on a deterministic synthetic corpus (encode, decode
with go-aac's own decoder, align by cross-correlation, score against the
source) and fails when SNR, band-limited SNR, segmental SNR, log-spectral
distance, or pre-echo drifts past a tolerance from a committed baseline, or the
encoder's alignment delay changes; it needs no external tool and runs in CI on
every platform. `task quality` renders
the full go-aac-versus-ffmpeg (native `aac`, and `libfdk_aac` when the build
has it) comparison report, per program, bitrate, and coder, with optional
ViSQOL and PEAQ columns when those binaries are present.

One case in the full report is expected to fail. Encoding the synthetic
`bird-chirps` program at 44.1 kHz and 128 kbps with the twoloop coder makes the
rate control stop moving: lambda pins at its floor while the frame stays at the
buffer ceiling, so every further pass quantizes identically. FFmpeg's C encoder
has the same loop with no exit and spins forever on that input (the pinned build
ignores SIGTERM while it does), which is why a reference run on it needs a hard
kill. go-aac detects the condition and returns an error (`enc: rate control
cannot converge at 128000 bps, 44100 Hz`) instead of hanging, so `task quality`
reports `1 failed` and exits 1 for that case. It is not a quality regression,
and that configuration is not in the committed gate grid (64 and 192 kbps). The
other cases in a run over both sample rates, 64 to 192 kbps and all three
coders completed. Only this one input and configuration is known to reach the
state; which real signals could reach it has not been characterised.

## Validation

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
