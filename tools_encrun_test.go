// SPDX-License-Identifier: LGPL-2.1-or-later
package aac

import (
	"os"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
)

// TestWriteADTSArtifact writes out.adts for manual gate checks when
// GOAAC_WRITE_ADTS names a target path. Rehearsal helper.
func TestWriteADTSArtifact(t *testing.T) {
	out := os.Getenv("GOAAC_WRITE_ADTS")
	if out == "" {
		t.Skip("set GOAAC_WRITE_ADTS")
	}
	src := synthTonal(44100*5, 44100)
	stream := encodeADTS(t, enc.Config{SampleRate: 44100, Bitrate: 128000, Channels: 1, Coder: enc.CoderFast}, src)
	if err := os.WriteFile(out, stream, 0o644); err != nil {
		t.Fatal(err)
	}
}
