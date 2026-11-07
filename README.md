# go-aac

[![CI](https://github.com/tphakala/go-aac/actions/workflows/ci.yml/badge.svg)](https://github.com/tphakala/go-aac/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tphakala/go-aac.svg)](https://pkg.go.dev/github.com/tphakala/go-aac)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/tphakala/go-aac/badge)](https://scorecard.dev/viewer/?uri=github.com/tphakala/go-aac)
[![License: LGPL-2.1-or-later](https://img.shields.io/badge/License-LGPL--2.1--or--later-blue.svg)](LICENSE)

Pure-Go AAC-LC encoder and decoder, ported from FFmpeg's native AAC encoder and
fixed-point decoder. No cgo and no external libraries. Each subsystem is
validated bit for bit against the C reference, and the decoder's output is
byte-identical to `ffmpeg -c:a aac_fixed`.

It is the AAC member of a family of pure-Go audio libraries with
[go-wav](https://github.com/tphakala/go-wav),
[go-flac](https://github.com/tphakala/go-flac),
[go-opus](https://github.com/tphakala/go-opus) and
[go-m4a](https://github.com/tphakala/go-m4a), and has the same API shape.

## Status

- **Encoder:** complete for AAC-LC, mono and stereo at 44.1 and 48 kHz, with
  FFmpeg's three coders (NMR, the default, twoloop and fast) and all four coding
  tools. Quality is on par with FFmpeg's `aac` encoder, and the default coder is
  faster than it.
- **Decoder:** AAC-LC, mono and stereo, 7.35 to 96 kHz, ADTS or raw access units.
  HE-AAC is rejected with `ErrUnsupportedSBR` or `ErrUnsupportedPS`.
- **Not supported:** HE-AAC (SBR/PS), xHE-AAC, LATM, ER/LD/ELD, more than two
  channels, VBR and MP4 muxing (use [go-m4a](https://github.com/tphakala/go-m4a)).

## Install

```sh
go get github.com/tphakala/go-aac
```

## Example

```go
import aacpcm "github.com/tphakala/go-aac/pcm"

// Encode interleaved 16-bit PCM to an ADTS stream.
cfg := aacpcm.Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
err := aacpcm.EncodeInterleaved(w, cfg, pcmBytes)

// Decode an ADTS stream back to interleaved 16-bit PCM.
d, err := aacpcm.NewDecoder(r)
_, err = io.Copy(out, d)
```

## Documentation

- [Usage](doc/usage.md): streaming and frame-level encoding and decoding, muxing
  raw access units, gapless playback, the low-level `aac` package.
- [Quality and validation](doc/quality.md): quality measurements and how the port
  is checked against the C reference.
- [Performance](doc/performance.md): benchmarks against FFmpeg, the SIMD kernels
  and `-tags noasm`, profiling notes.
- [API reference](https://pkg.go.dev/github.com/tphakala/go-aac) on pkg.go.dev.

## License

LGPL-2.1-or-later. go-aac is a derivative work of FFmpeg's LGPL-licensed AAC
encoder and cannot be relicensed permissively. See [LICENSE](LICENSE) and
[PROVENANCE.md](PROVENANCE.md). If go-aac is useful to you, you can support its
maintenance through [GitHub Sponsors](https://github.com/sponsors/tphakala).
