// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"fmt"
	"io"
	"sync"
)

// encoderPool recycles Encoders for EncodeInterleaved so back-to-back
// one-shot encodes reuse the large internal state (the low-level encoder
// is about 650 KiB) instead of re-allocating it. Every Get is paired with
// a Reset before any encoding, so a recycled encoder never carries state
// from a prior call. Mirrors go-flac pcm.EncodeInterleaved and go-opus
// oggopus.EncodeInterleaved.
var encoderPool = sync.Pool{New: func() any { return new(Encoder) }}

// EncodeInterleaved encodes a complete interleaved little-endian PCM
// buffer to an ADTS stream on w in a single call, centralizing the
// NewEncoder/Write/Close sequence. It draws an Encoder from an internal
// sync.Pool, so repeated calls are allocation-light, and it is safe for
// concurrent use.
//
// Unlike Write, the one-shot buffer must hold a whole number of
// inter-channel samples for cfg; a trailing partial sample is an error
// before any sink write (go-flac semantics).
func EncodeInterleaved(w io.Writer, cfg Config, pcm []byte) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	stride := cfg.BitDepth / 8 * cfg.Channels
	if len(pcm)%stride != 0 {
		return fmt.Errorf("go-aac/pcm: %d bytes is not a whole number of %d-byte samples", len(pcm), stride)
	}
	e, _ := encoderPool.Get().(*Encoder)
	defer func() {
		// Drop the reference to the caller's sink so an idle pooled encoder
		// does not pin it; the next Reset rebinds w before any use.
		e.w = nil
		encoderPool.Put(e)
	}()
	if err := e.Reset(w, cfg); err != nil {
		return err
	}
	if _, err := e.Write(pcm); err != nil {
		return err
	}
	return e.Close()
}
