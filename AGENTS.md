# AGENTS.md

Orientation for coding agents and automated reviewers working on this repository. README.md is the user-facing reference (usage, status, quality and benchmark figures). PROVENANCE.md records the upstream pin and the oracle build recipe. This file is the map for changing and reviewing the code.

## What this is

`github.com/tphakala/go-aac` (root package `aac`, streaming API in `pcm`) is a pure-Go AAC-LC encoder and decoder, ported from FFmpeg's native AAC encoder and fixed-point decoder at a pinned commit (`d09d5afc3aebede25d2d245ee23b75a47ea17c3a`). It is the AAC member of a family with go-wav, go-flac, go-opus and go-m4a, and it presents the same API shape as them. It aims to:

1. **Reproduce the C reference exactly.** The encoder is validated bit-for-bit against C harnesses and a pinned FFmpeg build; the decoder output is byte-identical to `ffmpeg -c:a aac_fixed`. PSNR alone cannot tell a correct port from a plausible one, so the differential gates are the definition of correct.
2. **Build anywhere Go builds.** No cgo and no external libraries in the published module. The default build uses SIMD kernels from `github.com/tphakala/simd`; `-tags noasm` links none of it.
3. **Survive hostile input.** The decoder parses untrusted streams and never panics; failures are wrapped sentinels. Steady-state encode and decode are allocation-free per frame.

Packages:

| Package | Role |
|---|---|
| `aac` (root) | Low-level codec: `Encoder` (planar float32 frames in, raw access units out), ADTS header building, AudioSpecificConfig, `SIMDEnabled`, sentinel errors (`ErrInvalidAudio`, `ErrEncoderClosed`) |
| `pcm` | The API callers use: `Config`, `Encoder` (ADTS to an `io.Writer`), `FrameEncoder` (raw access units to a callback for muxing), `Decoder`, `FrameDecoder` (`NewADTSDecoder`, `NewRawDecoder`), `EncodeInterleaved`, `DecodeInterleaved[Limit]`, `ParseASC`, `ErrDecodeLimit` |
| `internal/enc` | Encoder frame pipeline: buffering, window switching, MDCT, psy, tools, rate control, bitstream |
| `internal/coder` | Quantizer searches (NMR, twoloop, fast), codebook trellis, TNS, PNS, stereo tools |
| `internal/psy` | Psychoacoustic model (LAME block switching, 3GPP threshold analysis, bit reservoir) |
| `internal/dec` | Fixed-point AAC-LC decoder: ADTS and ASC parsing, symbol decode, dequant, IMDCT, reconstruction |
| `internal/dsp`, `internal/mdct`, `internal/tx`, `internal/fdsp`, `internal/window`, `internal/vlc`, `internal/bits`, `internal/tables` | Kernels, transforms, windows, VLC tables, bit I/O, generated codec tables |
| `internal/fmath` | The only per-frame home of float `math` calls; architecture-deterministic transcendentals |
| `internal/simdbuild` | `Enabled` constant, split by the `noasm` build tag |
| `internal/oracletest`, `internal/quality` | Oracle scaffolding for tests; objective quality metrics behind `tools/quality` |
| `tools` | C diff harnesses (`cdump`, `cquant`, `cpsy`, `cnmr`, `ctns`, `ctwoloop`, `cdec`, `cdec2`, ...), `gentables`, `quality`, `wav2aac` |
| `rules` | gocritic ruleguard matchers (build tag `ruleguard`, lint-only) |

Non-goals: HE-AAC (SBR/PS, rejected with `ErrUnsupportedSBR` and `ErrUnsupportedPS`), xHE-AAC, LATM, ER/LD/ELD profiles, 960-sample frames, multichannel beyond stereo, VBR, MP4 muxing (that is go-m4a). The encoder is restricted to 44.1 and 48 kHz, mono and stereo.

### API stability

The exported API of `aac` and `pcm` is the contract. Do not change a signature, remove a symbol or change documented behaviour without calling it out as a breaking change (`feat(pcm)!:` or `fix(pcm)!:`). The `pcm` API is deliberately aligned with go-flac and go-opus (`NewX`, flat `Config`, one-shot helpers, callback-style `FrameEncoder`); a change that breaks that alignment needs the siblings considered. There is no CHANGELOG file; releases are tags.

## Design rules you must not break

A change that violates one of these is wrong even if tests pass.

1. **The pinned FFmpeg commit is the specification.** Behaviour comes from `d09d5afc3a`, not from newer FFmpeg or from what sounds better. Ported functions carry a provenance comment naming the C origin (file and line at the pin). Keep it when editing, and keep the C-shaped style of the internal packages so they stay diffable against upstream.
2. **Differential oracle gates are the proof.** Anything that changes encoded bytes, decoded samples, or a table must pass the C harness fixtures under `internal/*/testdata/` and the oracle tests against the pinned FFmpeg (`GOAAC_FFMPEG`, `GOAAC_REQUIRE_ORACLE=1`). Do not regenerate a fixture to make a test pass.
3. **The oracle recipe includes `-ffp-contract=off`.** The pin is the commit plus the configure flags (see PROVENANCE.md and `FFMPEG_CONFIGURE_FLAGS` in `ci.yml`). All committed C fixtures were generated contraction-off, so the port must not fuse multiply-adds. `internal/coder/stereo_tools.go` splits a product into a temporary on purpose to stop the Go compiler fusing it; `fma_guard_test.go` guards this. Do not "simplify" such code.
4. **Bit-identical across architectures and builds.** The same input gives the same bytes on amd64 and arm64, with SIMD, with `-tags noasm`, and with `SIMD_DISABLE=all`. `TestEncoderArchDeterminism` pins SHA-256 goldens. Float `math` is fenced by the `depguard` rule: per-frame code imports `internal/fmath`, not `math`. The exemptions (`internal/fmath`, `window`, `mdct`, `tx`, `dsp/lpc.go`, `tables/cbrt_fixed.go`, `oracletest`, `quality`, `tools/`, tests) are listed in `.golangci.yaml`; do not widen them casually. The `internal/fmath/portable_*.go` files are vendored from the Go standard library and listed in PROVENANCE.md; keep them diffable.
5. **Scalar is canonical, SIMD must match it.** The scalar kernels are the reference; the SIMD kernels (NMR trellis, AbsPow34, QuantizeBands) are gated bit-identical against them by equivalence tests and fuzz targets. Every change to a kernel is made in both halves of the `*_simd.go` / `*_noasm.go` pair. No cgo, ever.
6. **License constraint.** The code derives from LGPL FFmpeg and is `LGPL-2.1-or-later`; `LICENSE.golang` covers the vendored Go standard library derivations. Every source file carries the SPDX header. Do not add code from a source with an incompatible license, and do not suggest relicensing. FFmpeg's `aac` and `libfdk_aac` are used only as black-box binaries by the quality harness.
7. **The decoder is total.** Malformed input returns a typed error (`ErrCorruptStream`, `ErrUnsupported`, `ErrUnsupportedSBR`, `ErrUnsupportedPS`), never a panic or index error. A failed `FrameDecoder` unit leaves the decoder as it was before the call; restoring state on failure is tested (`decoder_reset_test.go`, `framedecoder_recovery_test.go`).
8. **Bounded one-shot decodes.** `DecodeInterleaved` stops at `DefaultMaxDecodedBytes` (1 GiB) with a wrapped `ErrDecodeLimit`; `DecodeInterleavedLimit` takes the ceiling. `ErrDecodeLimit` is a resource limit, not a verdict on the stream; keep it distinct from `ErrCorruptStream`.
9. **Encoder input is validated, rate control cannot spin.** NaN or Inf samples return `ErrInvalidAudio` (the port of the C spectral-coefficient guard). Where the C rate loop would never exit, the Go port returns an error instead of hanging. Do not remove that exit.
10. **Steady state does not allocate.** Encode, `EncodeFrame` and decode run at zero allocations per frame. Allocation tests (`encode_alloc_test.go`, `decoder_alloc_test.go`, `decode_s16_alloc_test.go`) enforce it, with the bound relaxed only under `-race`. Pooled objects (`EncodeInterleaved` draws from a pool) must be fully reset on reuse.
11. **Concurrency.** `Encoder`, `FrameEncoder`, `Decoder` and `FrameDecoder` are not safe for concurrent use; separate instances are independent. The one-shot helpers are safe for concurrent use.
12. **Typed errors with `%w`.** Sentinels live in `errors.go` (root) and `pcm/decoder.go`, `pcm/decode_oneshot.go`. `ErrUnsupportedSBR` wraps `ErrUnsupported`, and `ErrUnsupportedPS` wraps `ErrUnsupportedSBR`, so `errors.Is` works at every level.
13. **Gapless is the muxer's job.** ADTS cannot signal encoder delay. `aac.EncoderDelay` and `FrameEncoder.Delay` are the contract with go-m4a's edit list; do not change the priming without the sibling in view.

## Layout

```
aac.go, encoder.go   Low-level Encoder, EncoderConfig, FrameSize
adts.go, asc.go      ADTS header building, AudioSpecificConfig
errors.go, simd.go   Sentinels; SIMDEnabled
stats.go             Encoder statistics

pcm/pcm.go           Config and validation
pcm/encoder.go       Encoder (io.WriteCloser, ADTS)
pcm/frameio.go       FrameEncoder (raw access units via callback)
pcm/decoder.go       Decoder, Info, options, decode sentinels
pcm/framedecoder.go  FrameDecoder (NewADTSDecoder, NewRawDecoder)
pcm/*_oneshot.go     EncodeInterleaved, DecodeInterleaved[Limit]

internal/            enc, coder, psy, dec, dsp, mdct, tx, fdsp, window, vlc, bits,
                     tables, fmath, simdbuild, oracletest, quality
tools/               C harnesses with READMEs, gentables, quality, wav2aac
scripts/             fuzz-smoke.sh, bench-encoders.sh
```

Kernels with SIMD variants come in `*_simd.go` and `*_noasm.go` pairs (`internal/dsp`, `internal/coder/nmr_trellis*.go`, `internal/simdbuild`). `internal/tables/tables_gen.go` is emitted by `tools/gentables`; regenerate it, do not hand-edit.

## Testing approach

Tests run without audio hardware. Oracle tests skip when `GOAAC_FFMPEG` is unset, unless `GOAAC_REQUIRE_ORACLE=1` turns a missing oracle into a failure (CI sets it).

- **Regression tests.** New behaviour gets a test. A bug fix gets a regression test that fails before the fix. A test must be able to fail: break the line it pins and confirm it fails.
- **Golden fixtures.** C-harness fixtures live in `internal/*/testdata/`, one set per subsystem. They are generated by the `tools/c*` harnesses against the contraction-off oracle; each tool directory documents regeneration.
- **Fuzzing.** CI runs a smoke job through `scripts/fuzz-smoke.sh` for `FuzzParseADTS`, `FuzzParseASC`, `FuzzDecodeFrame` (`internal/dec`), `FuzzDecodeStream` (`pcm`), `FuzzNMRTrellisStep` (`internal/coder`), `FuzzAbsPow34Equiv` and `FuzzQuantizeBandsEquiv` (`internal/dsp`). Run a new target in the package that defines it, since a non-matching `-fuzz` pattern passes without running. Crashers are committed under `testdata/fuzz` as regression seeds.
- **Quality gate.** `TestQualityBaseline` (`tools/quality`) re-measures the encoder on a deterministic synthetic corpus against `tools/quality/testdata/baseline.json`. Refreshing the baseline is a deliberate, reviewed action.
- **Build states.** The suite must pass in three states: default, `-tags noasm`, and SIMD compiled in but masked with `SIMD_DISABLE=all`. `TestSIMDDisableEnvIsHonoured` proves the masking took effect.
- **Race and non-race.** Some tests shrink or skip under `-race` (`edge_soak_race_test.go`, `pcm/raceflag_race_test.go`), so both lanes matter.
- **Known failing quality case.** The full `task quality` report is expected to fail one case (twoloop, `bird-chirps`, 44.1 kHz, 128 kbps: rate control cannot converge, by design). It is not in the committed gate grid.

## Commands

Go version is read from `go.mod` (currently 1.26). The golangci-lint version is pinned in `.github/workflows/ci.yml` (`GOLANGCI_LINT_VERSION`) so hooks, `task lint` and CI run the same linter.

```
task build         # go build ./...
task test          # go test -v ./...
task test:short    # go test ./...
task test:race     # go test -race ./...
task test:noasm    # race tests with -tags noasm
task test:norace   # suite without the race detector, -count=1
task bench         # all benchmarks with allocation counts
task vet           # go vet for amd64 and arm64, default, noasm and race tags
task lint          # golangci-lint, default, noasm and race tags
task tidy          # go mod tidy and verify
task quality       # go-aac versus ffmpeg report (needs ffmpeg)
task quality:gate  # regression gate against the committed baseline
task check         # build, vet, lint, race, noasm, non-race tests, quality gate
task hooks:install # git config core.hooksPath .githooks
```

Run `task check` before pushing. CI additionally runs the test matrix (Linux, macOS, Windows), the cross-build matrix (linux, darwin, windows on amd64 and arm64, with and without `noasm`), the fuzz smoke job, the differential oracle job (builds the pinned FFmpeg, runs the suite in all three build states), govulncheck and OpenSSF Scorecard. The test and vet jobs do not include 32-bit targets.

## Conventions

- **Lint.** Config is `.golangci.yaml` (adapted from go-flac's), with `depguard` fencing `math` and ruleguard matchers in `rules/`. `modernize` is disabled on purpose (it panicked on Go 1.26 with atomic types); revisit only on a linter upgrade.
- **No `//nolint` without a linter name and a reason.** If a linter is wrong often enough to silence globally, change `.golangci.yaml` with a comment saying why.
- **Errors.** Strings carry the `go-aac:` prefix (`go-aac/pcm:` in `pcm`). Wrap with `%w`.
- **Comments.** Explain why: the C origin and line at the pin, the failure being avoided, the measurement behind a constant. Match the existing density.
- **Docs follow code.** A behaviour change updates the package doc, the README and the sentinel's comment in the same change. README claims about quality and speed are measured figures; do not edit them without a measurement.
- **Commits** use an imperative subject with a type prefix (`feat:`, `fix:`, `docs:`, `build:`, `ci:`, `deps:`, `test:`, `refactor:`), optionally scoped (`fix(dec): ...`), with `!` for a breaking change.
- **Git hooks.** `.githooks/pre-commit` runs gofmt and golangci-lint on staged Go files; `pre-push` guards the commit identity (only the GitHub noreply address is allowed) and runs build and vet. Do not bypass either.
- **Dependencies.** Dependabot keeps Go modules and GitHub Actions current. Runtime dependencies are limited to `tphakala/simd`; do not add another.

## Review guidance

For CodeRabbit, Copilot and human reviewers. Rate findings on what a caller of the library would see.

Flag these, they are real defects here:

- Any break of the design rules above: cgo, a new runtime dependency, a `math` import on a per-frame path, a fused multiply-add where the C reference is contraction-off, a SIMD kernel that diverges from the scalar one.
- A change in encoded bytes or decoded samples without updated oracle and golden evidence, or a regenerated fixture with no explanation.
- A panic, index error or unbounded allocation reachable from stream bytes in `internal/dec` or the `pcm` decoders; a failed decode that leaves a `FrameDecoder` in a changed state.
- A resource limit reported as `ErrCorruptStream`, or a corrupt stream reported as a limit or as `ErrUnsupported`.
- A SIMD change made in only one half of a `simd` / `noasm` pair, or a new kernel without an equivalence test.
- An allocation added to the steady-state encode or decode path, or a pooled object returned dirty.
- A ported function that lost its provenance comment or whose behaviour no longer matches the C at the pin.
- A test that cannot fail on the behaviour it names, or a doc sentence that is false.

Intentional, do not flag:

- C-shaped code in `internal/` (long functions, index loops, C-style naming, goto-like control flow). It stays diffable against upstream; the idiomatic rewrite is a planned later step.
- Temporaries that split an expression (`a*b + c`) and vendored float64 transcendentals in `internal/fmath/portable_*.go`; they exist to stop FMA fusion and keep architectures identical.
- `gocognit` threshold of 50 and the gocyclo and gocritic exclusions for `internal/fmath/portable_*.go`.
- The encoder accepting only 44.1 and 48 kHz while the decoder accepts the full MPEG-4 rate table.
- ADTS output carrying roughly 1024 samples of priming; callers wanting gapless use `FrameEncoder` with a muxer.
- The `pcm` package name colliding with go-flac's; callers alias the import.
- The rate-control error on the one documented non-converging configuration.
- Large generated tables in `internal/tables/tables_gen.go` and the `.c` harnesses under `tools/`.

## Things that do not belong in the repo

- Design docs, specs, plans, research notes and handoff prompts. `/docs/` is gitignored on purpose.
- Build outputs (`*.test`, `*.out`, `*.exe`, harness binaries, `/wav2aac`, `/quality`), coverage and profile files, `.serena/`, `.claude/` and `.codegraph/`.
- Raw captures or scratch media. Committed media is limited to small fixtures under `testdata/`.
- Credentials, tokens or real email addresses in any file or commit.
