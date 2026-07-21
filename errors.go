// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import "errors"

// Sentinel errors returned by the aac and pcm packages, testable with
// errors.Is. They live in the root package, mirroring go-flac's layout
// (flac.ErrEncoderClosed is used by go-flac/pcm the same way).
var (
	// ErrInvalidAudio reports input samples containing NaN or Inf. It maps
	// the C encoder's spectral coefficient guard (aacenc.c:1119-1124
	// @ d09d5afc3a) to a testable sentinel.
	ErrInvalidAudio = errors.New("go-aac: input contains NaN or Inf")
	// ErrEncoderClosed is returned by pcm.Encoder.Write after Close and by
	// pcm.FrameEncoder.EncodeInterleaved after Flush; Close and Flush are
	// themselves idempotent and re-report the outcome of the flush they
	// performed, so neither returns this error on a stream that ended
	// cleanly. It is also returned by the aac.Encoder, pcm.Encoder and
	// pcm.FrameEncoder methods when the encoder is uninitialized: a zero
	// value, or one left unusable by a failed Reset.
	ErrEncoderClosed = errors.New("go-aac: encoder is closed")
)
