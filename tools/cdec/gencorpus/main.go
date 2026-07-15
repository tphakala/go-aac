// Generates D0 rehearsal corpus streams from the go-aac encoder itself:
// an ADTS stream and a raw-AU stream (2-byte BE length prefix per AU).
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	aac "github.com/tphakala/go-aac"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gencorpus <output-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	enc, err := aac.NewEncoder(aac.EncoderConfig{SampleRate: 48000, Channels: 2, Bitrate: 128000})
	if err != nil {
		panic(err)
	}
	asc := enc.AudioSpecificConfig()
	fmt.Printf("ASC: %x\n", asc)

	const frames = 240
	var adts, rawau []byte
	l := make([]float32, 1024)
	r := make([]float32, 1024)
	n := 0
	for f := range frames + 1 {
		for i := range l {
			t := float64(n) / 48000
			v := 0.35*math.Sin(2*math.Pi*440*t) + 0.15*math.Sin(2*math.Pi*3100*t)
			// transient burst every ~0.4s to force short blocks
			if math.Mod(t, 0.4) < 0.003 {
				v += 0.55 * math.Sin(2*math.Pi*6000*t)
			}
			l[i] = float32(v)
			r[i] = float32(0.35 * math.Sin(2*math.Pi*445*t))
			n++
		}
		in := [][]float32{l, r}
		if f == frames { // drain priming delay
			in = nil
		}
		au, err := enc.EncodeFrame(nil, in)
		if err != nil {
			panic(err)
		}
		if len(au) == 0 {
			continue
		}
		hdr, err := aac.AppendADTSHeader(nil, 48000, 2, len(au))
		if err != nil {
			panic(err)
		}
		adts = append(adts, hdr...)
		adts = append(adts, au...)
		rawau = binary.BigEndian.AppendUint16(rawau, uint16(len(au)))
		rawau = append(rawau, au...)
	}
	if err := os.WriteFile(dir+"/goenc_s48_128k.adts", adts, 0o644); err != nil {
		panic(err)
	}
	if err := os.WriteFile(dir+"/goenc_raw.rawau", rawau, 0o644); err != nil {
		panic(err)
	}
	if err := os.WriteFile(dir+"/goenc_raw.asc", []byte(fmt.Sprintf("%x\n", asc)), 0o644); err != nil {
		panic(err)
	}
}
