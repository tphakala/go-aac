// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"testing"
)

// Bitrate axis for the edge-config soak. The two ends are the interesting ones.
// Note the arch-determinism gate now pins goldens at 32000 and 1000000 too, so
// what is unique here is the 1 bps floor (which no golden covers) and the fact
// that these cells sweep the channel and rate axes against the extremes, which
// the gate deliberately does not:
//
//   - edgeBitrateFloor is the smallest target the public API accepts. Zero
//     selects DefaultBitrate and a negative target is rejected, so 1 bps is
//     the true minimum a caller can ask for. It is far below anything the
//     quantizer can actually reach, which makes it the pathological
//     rate-control input: the encoder must still emit well-formed, decodable
//     frames rather than empty, silent or malformed ones.
//   - edgeBitrateClamped is above the AAC buffer model ceiling
//     (6144 bits per channel per 1024-sample frame) for every shipped config,
//     so it exercises the Reset clamp (internal/enc/encoder.go, mirroring
//     aacenc.c:1560-1566) that the normal targets never reach.
const (
	edgeBitrateFloor   = 1
	edgeBitrateLow     = 32_000
	edgeBitrateMid     = 128_000
	edgeBitrateClamped = 1_000_000
)

// maxFrameBitsPerChannel is the AAC buffer model cap the encoder clamps the
// configured bitrate against, duplicated from the internal encoder so this
// test asserts against the specification value rather than against whatever
// the implementation currently holds.
const maxFrameBitsPerChannel = 6144

// edgeSoakMaxSeconds bounds GOAAC_ENC_SOAK. Sixty seconds per cell is already
// roughly 48 minutes of audio across the matrix, far past any useful soak, and
// it keeps the whole run near 100 s, comfortably inside Go's default 10-minute
// per-binary timeout. A larger cap would defeat the point of having one: the
// bound exists so a mistyped value fails fast instead of blowing that timeout
// and surfacing as an unreadable stack dump. It also stays well under
// 2^31/48000, keeping the frame arithmetic safe on a 32-bit build.
const edgeSoakMaxSeconds = 60

// resetWarmupFrames is how many frames TestEncoderEdgeConfigReset encodes
// through an encoder before re-arming it, purely to dirty the state Reset must
// wipe. Five frames clears the priming frame and leaves four coded ones behind.
const resetWarmupFrames = 5

// edgeSoakFrames renders exactly n whole frames of the castanets corpus for a
// channel layout, through the same generator the arch-determinism gate uses, so
// the soak and the gate cannot drift apart. It takes a frame count because the
// soak sweeps its input length and the gate does not.
func edgeSoakFrames(chLabel string, rate, frames int) [][]float32 {
	return castanetsChannels(chLabel, rate, frames*FrameSize)
}

// edgeSoakInput renders whole frames covering the given number of seconds,
// rounded down to a frame boundary.
func edgeSoakInput(chLabel string, rate, seconds int) [][]float32 {
	return edgeSoakFrames(chLabel, rate, rate*seconds/FrameSize)
}

// otherSampleRate and otherChannelCount return the shipped value that is not
// the one passed, used to build a warm-up configuration that differs from the
// one under test on every axis it can. They switch explicitly rather than doing
// arithmetic on the supported set, so adding a third sample rate is a compile-
// visible decision here instead of silently yielding an invalid rate.
func otherSampleRate(rate int) int {
	if rate == 44100 {
		return 48000
	}
	return 44100
}

func otherChannelCount(channels int) int {
	if channels == 1 {
		return 2
	}
	return 1
}

// edgeInputCache memoizes rendered corpus inputs for one test function. The
// matrix sweeps four axes but only two of them (channel layout and sample rate)
// change the input, so without this the same signal is re-rendered for every
// coder and bitrate. It is created per test function and never shared at
// package scope, and the encoder treats its input as read-only, so handing the
// same backing array to several subtests is safe.
type edgeInputCache map[string][][]float32

func (c edgeInputCache) get(chLabel string, rate, frames int) [][]float32 {
	key := fmt.Sprintf("%s_%d_%d", chLabel, rate, frames)
	if src, ok := c[key]; ok {
		return src
	}
	src := edgeSoakFrames(chLabel, rate, frames)
	c[key] = src
	return src
}

// chLabelFor maps a channel count onto the corpus channel-layout label.
func chLabelFor(channels int) string {
	if channels == 1 {
		return archChanMono
	}
	return archChanStereo
}

// edgeCase is one cell of the edge-config matrix.
type edgeCase struct {
	coderName string
	coder     Coder
	chLabel   string
	channels  int
	rate      int
	bitrate   int
}

func (c edgeCase) name() string {
	return fmt.Sprintf("%s_%s_%d_%d", c.coderName, c.chLabel, c.rate, c.bitrate)
}

// edgeCases is the full edge-config cross product: every coder, both channel
// layouts, both sample rates, and the four bitrate points above. Unlike the
// arch-determinism corpus this sweeps every axis simultaneously, because the
// failures it looks for (rate control that cannot converge, a frame that will
// not decode, a drain that loses or invents a frame) are interactions between
// axes rather than per-axis divergence.
func edgeCases() []edgeCase {
	bitrates := edgeSoakBitrates()
	cases := make([]edgeCase, 0, len(testCoders)*2*2*len(bitrates))
	for _, c := range testCoders {
		for _, rate := range []int{44100, 48000} {
			for _, ch := range []struct {
				label string
				n     int
			}{{archChanMono, 1}, {archChanStereo, 2}} {
				for _, br := range bitrates {
					cases = append(cases, edgeCase{c.name, c.coder, ch.label, ch.n, rate, br})
				}
			}
		}
	}
	return cases
}

// TestEncoderEdgeConfigSoak is the edge-config soak for the reliability pass
// (issue #81): it drives every shipped encoder configuration over a multi-frame
// input and asserts the stream-level invariants that must hold regardless of
// coder, channel layout, sample rate or bitrate.
//
// It is deliberately not a golden test. TestEncoderArchDeterminism already pins
// exact bytes for the representative cells; what is unpinned is whether the
// *edges* of the config space stay well behaved at all. So this asserts
// contracts rather than hashes:
//
//   - encoding never fails and never panics, including at a 1 bps target that
//     rate control cannot possibly meet and at a target above the buffer model
//     ceiling;
//   - the access-unit count is exactly the input frame count plus one, so the
//     priming frame and the drain neither lose nor invent a frame;
//   - every access unit is small enough for an ADTS header to frame it;
//   - the average coded rate stays under the AAC buffer model ceiling for every
//     shipped configuration (a sanity rail, not a gate on any one mechanism;
//     see the comment at the assertion itself);
//   - every stream decodes through the in-repo decoder to exactly the expected
//     sample count on the expected channel count, and is not digital silence.
//
// A per-frame ceiling is deliberately NOT asserted. The ABR rate-control loop
// returns as soon as its lambda ratio converges even when the frame is still at
// or above maxFrameBits*channels-3, exactly as the C breaks out of the same loop
// (internal/enc/encoder.go, aacenc.c:1309-1350), so individual frames may exceed
// the per-channel cap while the average stays under it. Asserting the per-frame
// bound would gate a deviation from upstream, not a defect.
//
// Set GOAAC_ENC_SOAK=<seconds> to run the same matrix over a longer input; that
// is the actual soak, and it is opt in so the normal suite stays fast. The
// matrix itself always runs, because its breadth is the coverage that was
// missing. The name is deliberately NOT the pcm package's GOAAC_SOAK: that one
// is an iteration count gating an opt-in decode test, so a shared name would
// make one `GOAAC_SOAK=n go test ./...` mean n decode passes there (seconds)
// and n seconds of audio across every cell here, which at n=300 runs the root
// binary past Go's default 10-minute test timeout and looks like a hang.
//
// The size of one run comes from edgeSoakSeconds and edgeSoakBitrates, which
// the race and non-race builds define differently; see edge_soak_race_test.go
// for why the race lane runs a reduced sweep.
func TestEncoderEdgeConfigSoak(t *testing.T) {
	seconds := edgeSoakSeconds
	if v := os.Getenv("GOAAC_ENC_SOAK"); v != "" {
		n, err := strconv.Atoi(v)
		// Bounded at both ends. The upper bound keeps rate*seconds inside an
		// int32 (it would wrap on a 32-bit build) and keeps a mistyped value
		// from running the binary past Go's default 10-minute test timeout,
		// which surfaces as an unreadable stack dump rather than as misuse.
		if err != nil || n <= 0 || n > edgeSoakMaxSeconds {
			t.Fatalf("GOAAC_ENC_SOAK must be an integer number of seconds in 1..%d, got %q",
				edgeSoakMaxSeconds, v)
		}
		seconds = n
	}

	inputs := edgeInputCache{}
	for _, c := range edgeCases() {
		t.Run(c.name(), func(t *testing.T) {
			src := inputs.get(c.chLabel, c.rate, c.rate*seconds/FrameSize)
			samples := len(src[0])
			frames := samples / FrameSize

			aus, asc := encodeCollect(t, EncoderConfig{
				SampleRate: c.rate,
				Channels:   c.channels,
				Bitrate:    c.bitrate,
				Coder:      c.coder,
			}, src)

			// The priming frame emits nothing and the drain emits one extra
			// packet to cover it, so a run of N input frames yields N+1 access
			// units. A drain that stopped early or ran long shows up here.
			if want := frames + 1; len(aus) != want {
				t.Fatalf("emitted %d access units over %d input frames, want %d", len(aus), frames, want)
			}

			// An empty access unit cannot appear here: encodeCollect appends
			// only when len(au) > 0, so a dropped frame surfaces as the count
			// mismatch above rather than as a zero-length element.
			total := 0
			for i, au := range aus {
				if len(au) > maxADTSPayload {
					t.Fatalf("access unit %d is %d bytes, too large for an ADTS header to frame (max %d)",
						i, len(au), maxADTSPayload)
				}
				total += len(au)
			}

			// Whatever was configured, the average coded rate must land under the
			// buffer model ceiling. Two independent mechanisms hold it there:
			// the Reset bitrate clamp, and the rate-control loop's own
			// min(rateBits, maxFrameBits*chans-3). Because the second alone is
			// sufficient, this assertion does NOT gate the clamp: deleting the
			// clamp leaves every cell here passing and instead turns the three
			// _1000000 arch-determinism goldens red, which are its real guard.
			// What this does assert is the end-to-end contract that no shipped
			// configuration can be made to exceed the buffer model on average.
			// The coded stream spans frames+1 access units, so its duration is
			// the input plus the priming delay; dividing by the input alone
			// would inflate the measured rate by (frames+1)/frames.
			// int64 throughout: the numerator is bytes * 8 * sample rate, which
			// runs past 2^31 on a long input and would wrap on a 32-bit build.
			ceiling := int64(maxFrameBitsPerChannel) * int64(c.channels) * int64(c.rate) / FrameSize
			coded := int64(samples) + EncoderDelay
			if got := int64(total) * 8 * int64(c.rate) / coded; got > ceiling {
				t.Errorf("average coded rate %d bps exceeds the buffer model ceiling %d bps", got, ceiling)
			}

			perChannel, pcmS16, channels := decodeAll(t, asc, aus)
			if channels != c.channels {
				t.Errorf("decoded %d channels, want %d", channels, c.channels)
			}
			// Decoded length is the input plus the encoder's priming delay.
			if want := samples + EncoderDelay; perChannel != want {
				t.Errorf("decoded %d samples per channel, want %d", perChannel, want)
			}
			if isDigitalSilence(pcmS16) {
				t.Errorf("decoded output is digital silence over %d frames", frames)
			}
		})
	}
}

// TestEncoderEdgeConfigReset asserts the Reset contract across the edges of the
// config space: an encoder re-armed with Reset must produce byte-identical
// output to a fresh encoder on the same input.
//
// What is new here is BREADTH, not the contract and not the NMR angle.
// TestEncoderResetByteIdentity already covers one configuration, and because
// Coder's zero value is CoderNMR its warm-up already dirties NMRState. This
// extends the same contract to both bitrate extremes, both sample rates and all
// three coders, which is where rate control carries the most state (lambda, the
// bit reservoir and the NMR servo) and a missed field in the Reset wipe is most
// likely to show. Stereo only, since the stereo path carries the most
// cross-frame state of the two layouts.
func TestEncoderEdgeConfigReset(t *testing.T) {
	inputs := edgeInputCache{}
	for _, c := range testCoders {
		for _, rate := range []int{44100, 48000} {
			for _, br := range []int{edgeBitrateFloor, edgeBitrateClamped} {
				cfg := EncoderConfig{SampleRate: rate, Channels: 2, Bitrate: br, Coder: c.coder}
				t.Run(fmt.Sprintf("%s_%d_%d", c.name, rate, br), func(t *testing.T) {
					src := inputs.get(archChanStereo, rate, rate*edgeSoakSeconds/FrameSize)
					fresh, _ := encodeCollect(t, cfg, src)
					reused := encodeCollectReset(t, cfg, src)
					// Assert both sides carry a stream before comparing them.
					// Without this the comparison passes trivially if both
					// encoders ever produced nothing: two empty slices are
					// equal, and the loop below would not execute at all.
					if want := len(src[0])/FrameSize + 1; len(fresh) != want {
						t.Fatalf("fresh encoder emitted %d access units, want %d", len(fresh), want)
					}
					if len(fresh) != len(reused) {
						t.Fatalf("reset encoder emitted %d access units, fresh emitted %d", len(reused), len(fresh))
					}
					for i := range fresh {
						if !bytes.Equal(fresh[i], reused[i]) {
							t.Fatalf("access unit %d differs between a fresh encoder and a Reset one", i)
						}
					}
				})
			}
		}
	}
}

// TestEncoderMidSideWiring gates the mid/side contract that the encoder
// documents but nothing exercised. DisableMS reaches every coder, through two
// different mechanisms: twoloop and fast decide mid/side after quantization, so
// the switch skips SearchForMS and applyMidSideStereo (internal/enc/encoder.go,
// mirroring aacenc.c:1249-1255), while NMR decides it inside nmrDecideStereo
// before quantization, so the switch is threaded in as midSide == 0
// (aacenc.c:1214-1220 and the options term at aacenc.c:1216-1217). Either way
// the bitstream must change, and both variants must still decode.
//
// The NMR arm asserted the opposite until issue #92: nmrDecideStereo was built
// with midSide hardcoded to -1, so DisableMS was a strict no-op there and this
// test pinned that. If the NMR case starts comparing equal again, the wiring
// has regressed rather than the expectation being wrong.
//
// Only mid/side is gated here, hence the name. TestEncoderToolWiring
// (tool_wiring_test.go) is the companion gate for DisableTNS, DisablePNS and
// DisableIS; it asserts against the public Stats counters, which name WHICH
// tool fired where byte inequality only says that something did. Byte equality
// is the right instrument for this one because the correlated input below makes
// mid/side unambiguous, and because MSBands cannot serve: SearchForIS also
// writes MsMask, using it to signal intensity phase (internal/coder/
// stereo_tools.go), so the counter is not attributable to this switch alone.
func TestEncoderMidSideWiring(t *testing.T) {
	const rate = 44100
	// Both channels carry the SAME signal. That is deliberate: it drives the
	// side channel (L-R) to zero, so mid/side is unambiguously the cheaper
	// coding for every band and the search cannot decline it for
	// signal-dependent reasons. With two uncorrelated channels the decision is
	// marginal (measured: at 32 kb/s the M/S stream is occasionally the LARGER
	// one), so "DisableMS changes the bitstream" would hold only incidentally
	// and a psychoacoustic retune could flip it into a spurious failure.
	mono := edgeSoakInput(archChanMono, rate, edgeSoakSeconds)[0]
	src := [][]float32{mono, mono}

	for _, tc := range testCoders {
		// Every coder must change the bytes: there is no longer a coder for
		// which DisableMS is a no-op, so a coder added to testCoders inherits
		// the same expectation with nothing to opt out of.
		for _, br := range []int{edgeBitrateLow, edgeBitrateMid} {
			t.Run(fmt.Sprintf("%s_%d", tc.name, br), func(t *testing.T) {
				base := EncoderConfig{SampleRate: rate, Channels: 2, Bitrate: br, Coder: tc.coder}
				noMS := base
				noMS.DisableMS = true

				on, onASC := encodeCollect(t, base, src)
				off, offASC := encodeCollect(t, noMS, src)

				// Both streams must exist before they are compared: two empty
				// slices compare equal, which would make the wantEqual case
				// pass without either encode having produced anything.
				want := len(src[0])/FrameSize + 1
				if len(on) != want || len(off) != want {
					t.Fatalf("emitted %d access units with M/S and %d without, want %d each",
						len(on), len(off), want)
				}

				// Lengths already match (asserted above), so only the bytes differ.
				equal := true
				for i := range on {
					if !bytes.Equal(on[i], off[i]) {
						equal = false
						break
					}
				}
				if equal {
					t.Errorf("DisableMS left the %s bitstream unchanged; mid/side should have been "+
						"chosen with the switch clear on this fully correlated input, so the flag "+
						"is not reaching the decision (issue #92)", tc.name)
				}

				// Both variants must remain decodable, not merely different.
				for _, v := range []struct {
					label string
					aus   [][]byte
					asc   []byte
				}{{"ms", on, onASC}, {"noms", off, offASC}} {
					perChannel, pcmS16, channels := decodeAll(t, v.asc, v.aus)
					if channels != 2 {
						t.Errorf("%s: decoded %d channels, want 2", v.label, channels)
					}
					if want := len(src[0]) + EncoderDelay; perChannel != want {
						t.Errorf("%s: decoded %d samples per channel, want %d", v.label, perChannel, want)
					}
					if isDigitalSilence(pcmS16) {
						t.Errorf("%s: decoded output is digital silence", v.label)
					}
				}
			})
		}
	}
}

// encodeCollectReset encodes sig through an encoder that was built for a
// throwaway configuration and then Reset onto cfg, so the returned access units
// come from a reused encoder rather than a fresh one. The throwaway config
// deliberately differs from cfg in coder, channel count, sample rate and
// bitrate, so every field Reset is responsible for wiping has actually been
// dirtied first.
func encodeCollectReset(t *testing.T, cfg EncoderConfig, sig [][]float32) [][]byte {
	t.Helper()
	// The warm-up always runs CoderNMR, whatever cfg asks for, so it does not
	// necessarily differ from cfg in coder. Only the NMR path allocates and
	// populates the ~99 KiB NMRState, so a warm-up on any other coder would
	// leave it untouched and Reset would then allocate a pristine one, skipping
	// that part of the wipe entirely. Running NMR exercises the wipe when cfg is
	// NMR and, when cfg is not, proves a leftover NMRState does not leak into
	// another coder's output. The sample rate, channel count and bitrate axes do
	// all differ from cfg, so the rest of the retained state is dirtied too.
	warmup := EncoderConfig{
		SampleRate: otherSampleRate(cfg.SampleRate),
		Channels:   otherChannelCount(cfg.Channels),
		Bitrate:    edgeBitrateMid,
		Coder:      CoderNMR,
	}
	e, err := NewEncoder(warmup)
	if err != nil {
		t.Fatal(err)
	}
	// Dirty the retained state before the Reset so the wipe has something to
	// do. A handful of frames is enough: the priming frame plus a few coded
	// ones already populate the MDCT overlap, the psy history, the NMR servo
	// and the bit reservoir, which is everything Reset is responsible for
	// wiping. Encoding more would only cost time.
	warmupSig := edgeSoakFrames(chLabelFor(warmup.Channels), warmup.SampleRate, resetWarmupFrames)
	frame := make([][]float32, len(warmupSig))
	for off := 0; off+FrameSize <= len(warmupSig[0]); off += FrameSize {
		for ch := range warmupSig {
			frame[ch] = warmupSig[ch][off : off+FrameSize]
		}
		if _, err := e.EncodeFrame(nil, frame); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Reset(cfg); err != nil {
		t.Fatal(err)
	}

	// The frame-feed, drain and whole-frame precondition are shared with
	// encodeCollect (encoder_delay_test.go); only the encoder lifecycle above
	// is what this helper adds.
	return encodeDrain(t, e, sig)
}

// isDigitalSilence reports whether b is a non-empty run of zero bytes, i.e.
// decoded digital silence. A stream that encodes and decodes cleanly but
// carries no signal is still an encoder failure, and at the bitrate floor it is
// the plausible degenerate outcome. Empty input is not silence: a decode that
// produced nothing at all is caught by the sample-count assertion instead, and
// reporting it here too would only mislabel it.
func isDigitalSilence(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
