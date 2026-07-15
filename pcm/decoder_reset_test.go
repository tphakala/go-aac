// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// TestResetFailureLatched checks that a failed Reset latches the error, so a
// pooled decoder whose caller ignored the Reset return value cannot silently
// decode against the previous stream's state. Read must return the latched
// error, not proceed.
func TestResetFailureLatched(t *testing.T) {
	data := loadStream(t, streamMono)
	d, err := NewDecoder(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	// Re-arm on garbage: Reset must fail and latch.
	rerr := d.Reset(bytes.NewReader(make([]byte, 64)))
	if !errors.Is(rerr, ErrCorruptStream) {
		t.Fatalf("Reset on garbage: want ErrCorruptStream, got %v", rerr)
	}
	n, readErr := d.Read(make([]byte, 64))
	if n != 0 || !errors.Is(readErr, ErrCorruptStream) {
		t.Fatalf("Read after failed Reset: got n=%d err=%v, want 0 and ErrCorruptStream", n, readErr)
	}
}

// errBoom is a non-EOF transport error used to prove the decoder surfaces a
// genuine underlying reader failure instead of masking it as corruption or EOF.
var errBoom = errors.New("boom: transport failure")

// errReader serves data until fail bytes have been read, then returns errBoom.
type errReader struct {
	data []byte
	pos  int
	fail int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= r.fail {
		return 0, errBoom
	}
	end := min(r.fail, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n
	return n, nil
}

// TestReaderErrorSurfaced feeds a stream that fails midway with a non-EOF
// transport error and asserts the decoder surfaces that exact error (via
// errors.Is), rather than relabeling it as ErrCorruptStream or a clean end.
func TestReaderErrorSurfaced(t *testing.T) {
	data := loadStream(t, streamMono)
	d, err := NewDecoder(&errReader{data: data, fail: len(data) / 2})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, werr := d.WriteTo(io.Discard); !errors.Is(werr, errBoom) {
		t.Fatalf("want the underlying transport error surfaced, got %v", werr)
	}
}
