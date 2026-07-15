// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import "testing"

// TestConfigureDropsStaleElement verifies that a pooled decoder which
// reconfigures from stereo to mono drops the retained stereo CPE. Otherwise a
// malformed mono stream carrying a CPE element would decode into the leftover
// slot (decodeFrameGA's che != nil guard would pass) and emit two channels of
// stale audio instead of being rejected, diverging from a fresh decoder and
// from the C, which frees elements absent from the new layout in che_configure.
func TestConfigureDropsStaleElement(t *testing.T) {
	stereo := Config{ObjectType: 2, ChanConfig: 2, SamplingIndex: 3, SampleRate: 48000}
	mono := Config{ObjectType: 2, ChanConfig: 1, SamplingIndex: 3, SampleRate: 48000}

	d := NewADTS()
	if err := d.configure(stereo); err != nil {
		t.Fatalf("configure stereo: %v", err)
	}
	if d.che[TypeCPE][0] == nil {
		t.Fatal("stereo configure must allocate the CPE slot")
	}

	// Re-arm for a new stream and reconfigure as mono (the pooled reuse path).
	d.ResetADTS()
	if err := d.configure(mono); err != nil {
		t.Fatalf("configure mono: %v", err)
	}
	if d.che[TypeSCE][0] == nil {
		t.Fatal("mono configure must allocate the SCE slot")
	}
	if d.che[TypeCPE][0] != nil {
		t.Fatal("mono configure must drop the stale stereo CPE slot")
	}

	// Reconfiguring back to stereo must likewise drop the mono SCE slot.
	d.ResetADTS()
	if err := d.configure(stereo); err != nil {
		t.Fatalf("reconfigure stereo: %v", err)
	}
	if d.che[TypeCPE][0] == nil || d.che[TypeSCE][0] != nil {
		t.Fatal("stereo reconfigure must restore the CPE slot and drop the SCE slot")
	}
}
