package main

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	aac "github.com/tphakala/go-aac"
)

func TestParseEncodersForLibFDK(t *testing.T) {
	present := ` A....D aac                  AAC (Advanced Audio Coding)
 A....D libfdk_aac           Fraunhofer FDK AAC (codec aac)
 A....D libmp3lame           libmp3lame MP3`
	if !parseEncodersForLibFDK(present) {
		t.Fatal("libfdk_aac encoder line not detected")
	}
	for _, absent := range []string{
		" A....D aac                  AAC (Advanced Audio Coding)",  // native aac only
		" A....D libfdk_aacx          not the encoder",              // longer name must not match
		"a note mentioning libfdk_aac in prose, not an encoder row", // prose, no capability flags
	} {
		if parseEncodersForLibFDK(absent) {
			t.Fatalf("false positive on %q", absent)
		}
	}
}

func TestCoderName(t *testing.T) {
	cases := map[aac.Coder]string{aac.CoderNMR: "nmr", aac.CoderTwoLoop: "twoloop", aac.CoderFast: "fast"}
	for c, want := range cases {
		if got := coderName(c); got != want {
			t.Fatalf("coderName(%d) = %q, want %q", c, got, want)
		}
	}
}

func TestCoderNamePanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("coderName on an unknown coder must panic")
		}
	}()
	_ = coderName(aac.Coder(0x7f))
}

func TestParseCoders(t *testing.T) {
	got, err := parseCoders("nmr, twoloop ,fast")
	if err != nil || len(got) != 3 || got[0] != aac.CoderNMR || got[2] != aac.CoderFast {
		t.Fatalf("parseCoders default set: %v, %v", got, err)
	}
	// Deduplicated, first-seen order kept.
	dd, err := parseCoders("fast,nmr,fast")
	if err != nil || len(dd) != 2 || dd[0] != aac.CoderFast || dd[1] != aac.CoderNMR {
		t.Fatalf("parseCoders dedup: %v, %v", dd, err)
	}
	if _, err := parseCoders("nmr,bogus"); err == nil {
		t.Fatal("an unknown coder name must error")
	}
	if _, err := parseCoders(""); err == nil {
		t.Fatal("an empty coder list must error")
	}
}

func TestParseLast(t *testing.T) {
	got, err := parseLast(mosRe, "noise\nMOS-LQO: 1.1\nMOS-LQO: 4.25\n", "visqol")
	if err != nil || got != 4.25 {
		t.Fatalf("parseLast last match: %v, %v", got, err)
	}
	nan, err := parseLast(odgRe, "Objective Difference Grade: -nan\n", "peaq")
	if err != nil || !math.IsNaN(nan) {
		t.Fatalf("parseLast nan: %v, %v", nan, err)
	}
	if _, err := parseLast(mosRe, "no result here", "visqol"); err == nil {
		t.Fatal("a missing result line must error")
	}
}

// TestDetectToolsGOAACFFmpegMissing: a GOAAC_FFMPEG pointing at a path that is
// not a usable file leaves ffmpeg unresolved rather than trying to exec it.
func TestDetectToolsGOAACFFmpegMissing(t *testing.T) {
	t.Setenv("GOAAC_FFMPEG", filepath.Join(t.TempDir(), "does-not-exist"))
	tl := detectTools(t.Context(), "", "", "")
	if tl.ffmpeg != "" {
		t.Fatalf("ffmpeg = %q, want empty for an unusable GOAAC_FFMPEG", tl.ffmpeg)
	}
}

// TestDetectToolsGOAACFFmpegUsable: a usable GOAAC_FFMPEG is taken as given
// (the oracle convention), without a PATH lookup. The fake binary is not a real
// ffmpeg, so libfdk detection just comes back false.
func TestDetectToolsGOAACFFmpegUsable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary is POSIX-only")
	}
	bin := filepath.Join(t.TempDir(), "fakeffmpeg")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOAAC_FFMPEG", bin)
	tl := detectTools(t.Context(), "", "", "")
	if tl.ffmpeg != bin {
		t.Fatalf("ffmpeg = %q, want the GOAAC_FFMPEG path %q", tl.ffmpeg, bin)
	}
	if tl.libfdk {
		t.Fatal("a fake ffmpeg that lists no encoders must not report libfdk present")
	}
}
