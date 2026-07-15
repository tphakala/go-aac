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
	// ErrEncoderClosed is returned by pcm.Encoder.Write and Close after Close
	// has been called, and by the aac.Encoder and pcm.Encoder methods when the
	// encoder is uninitialized: a zero value, or one left unusable by a failed
	// Reset.
	ErrEncoderClosed = errors.New("go-aac: encoder is closed")
)
