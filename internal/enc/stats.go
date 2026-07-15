// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

// Stats holds the encoder's tool-usage counters, accumulated per encoded
// frame over the final per-band decisions. Mirrors the AACEncContext
// stat_* counters and lambda accounting (libavcodec/aacenc.h:232-239
// @ d09d5afc3a); the C prints them once at uninit, Go exposes them as a
// snapshot instead (docs/go-design.md: no logging in the library, ever).
type Stats struct {
	LambdaSum   float64 // sum of the per-frame operating lambda (aacenc.c:1412)
	LambdaCount int64   // frames folded into LambdaSum
	Chans       int64   // coded channel-frames
	Short       int64   // of which short-block (transient)
	TNSLong     int64   // TNS-active channel-frames among long blocks
	TNSShort    int64   // TNS-active channel-frames among short blocks
	ChBands     int64   // coded channel-bands
	PNS         int64   // of which PNS (noise substitution)
	CPEBands    int64   // coded CPE pair-bands
	MS          int64   // of which mid/side coded
	IS          int64   // of which intensity coded
}
