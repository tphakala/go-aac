<!-- SPDX-License-Identifier: LGPL-2.1-or-later -->

# Decoder corpus provenance

Each `*.adts` file here is an AAC-LC stream in ADTS framing. `refs.sha256`
pins, per fixture, the SHA-256 of the **pinned oracle's** interleaved
little-endian S16 decode (`ffmpeg -c:a aac_fixed ... -f s16le`), not the hash
of the `.adts` bytes. `TestDecodeStreamHermetic` checks the public decoder
against those hashes on every runner; `TestDecodeStreamVsOracle` (gated on
`GOAAC_FFMPEG`) re-derives them from a live oracle and asserts byte-identical
output.

## `tone_*` rate-matrix fixtures (issue #85)

`tone_{m,s}<rate>.adts` are short 440 Hz sine tones that complete the AAC-LC
low-rate decode matrix (8/11.025/12/16/22.05/24/32 kHz, mono `m` and stereo
`s`). They exist to prove the decoder handles AAC-LC at the low sample rates
(8 to 32 kHz) that camera and stream sources commonly emit, not only
44.1/48 kHz. `sine_m8_24k` (8 kHz mono) and `is_s22_48k_fast`
(22.05 kHz stereo) predate this set and cover their two cells.

Generated with the native (LC-only, no SBR) FFmpeg `aac` encoder:

```sh
# mono   (48 kbps), stereo (96 kbps); RATE in {8000,11025,12000,16000,22050,24000,32000}
ffmpeg -hide_banner -bitexact \
  -f lavfi -i "sine=frequency=440:duration=0.5:sample_rate=RATE" \
  -ac CHANNELS -c:a aac -b:a BITRATE -bitexact -f adts tone_<m|s>RATE.adts
```

The manifest hash for each is then the oracle decode:

```sh
ffmpeg -hide_banner -bitexact -c:a aac_fixed -i FILE.adts -bitexact -f s16le - | sha256sum
```

The go-aac decoder reproduces every one of these byte-for-byte, so the whole
low-rate LC range is a supported contract, verified against C.
