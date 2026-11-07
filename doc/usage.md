# Usage

The library has two layers, mirroring [go-flac](https://github.com/tphakala/go-flac)
(`flac` + `pcm`) and [go-opus](https://github.com/tphakala/go-opus)
(`opus` + `oggopus`).

## pcm: the streaming layer

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
if err != nil {                // errors.Is: aacpcm.ErrCorruptStream, aacpcm.ErrUnsupported, or the precise ErrUnsupportedSBR / ErrUnsupportedPS
    return err
}
info := d.Info()       // SampleRate, Channels, Profile, valid immediately
_, err = io.Copy(w, d) // WriteTo drains the whole decode; Read fills any buffer
```

The decoded PCM is byte-identical to `ffmpeg -c:a aac_fixed -f s16le` on every
LC stream tested, including Apple afconvert output, from 8 kHz up to 96 kHz.
The decoder never panics on malformed input (it returns wrapped
`ErrCorruptStream` or `ErrUnsupported` sentinels, with `ErrUnsupportedSBR` /
`ErrUnsupportedPS` naming HE-AAC and HE-AACv2 specifically so a caller can hand
those off to an external decoder) and runs at zero allocations per frame in
steady state. Raw access units plus an `AudioSpecificConfig` are opt in via
`aacpcm.WithRawStream(asc)`. When the whole stream is in memory,
`aacpcm.DecodeInterleaved(r)` returns all the PCM in one call, and
`aacpcm.DecodeInterleavedLimit(r, maxBytes)` bounds the allocation for untrusted
input (`aacpcm.ErrDecodeLimit` when the limit is hit).

Live ingest (RTSP, HLS, HTTP or UDP) delivers access units one at a time, with
loss and corruption, so there is a frame-level decoder that needs no reader in
between: `aacpcm.NewADTSDecoder()` for self-describing ADTS frames, or
`aacpcm.NewRawDecoder(asc)` for bare access units described by an
AudioSpecificConfig that arrives out of band.

```go
fd, err := aacpcm.NewRawDecoder(asc) // or aacpcm.NewADTSDecoder()
if err != nil {                      // ErrUnsupported, ErrUnsupportedSBR or ErrUnsupportedPS, from the ASC
    return err
}
var pcm []byte
for au := range units {
    pcm, _, err = fd.DecodeFrame(pcm[:0], au) // samples per channel is always 1024
    if err != nil {
        continue // skip a corrupt unit; the next valid one still decodes correctly
    }
    play(pcm)
}
```

`DecodeFrame` appends interleaved S16 PCM to a caller-owned buffer, decodes
byte-identically to the reader `Decoder`, and allocates nothing in steady state.
A failed unit leaves the decoder as it was before the call, so the next valid
unit decodes exactly as if the bad one had never arrived (use `Reset` to start a
clean session). A corrupt unit that still parses is decoded as audio and does
advance the state, as the codec requires. An ADTS `au` must hold exactly one
frame; the length field is not checked against the buffer. A `FrameDecoder` is
not safe for concurrent use, but separate instances are independent.
`aacpcm.ParseASC(asc)` reads an AudioSpecificConfig without building a decoder.

## aac: the low-level codec

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
