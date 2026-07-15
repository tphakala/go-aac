// SPDX-License-Identifier: LGPL-2.1-or-later
package aac

import (
	"bytes"
	"testing"
)

func TestAppendAudioSpecificConfig(t *testing.T) {
	// 48 kHz mono: AOT 2 (00010), index 3 (0011), config 1 (0001), GA 000
	if got := appendAudioSpecificConfig(nil, 3, 1); !bytes.Equal(got, []byte{0x11, 0x88}) {
		t.Errorf("asc(3,1) = % x, want 11 88", got)
	}
	// 44.1 kHz stereo
	if got := appendAudioSpecificConfig(nil, 4, 2); !bytes.Equal(got, []byte{0x12, 0x10}) {
		t.Errorf("asc(4,2) = % x, want 12 10", got)
	}
}
