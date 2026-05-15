// Package tone generates a 16-bit signed little-endian mono PCM sine wave.
package tone

import (
	"encoding/binary"
	"math"
)

type Generator struct {
	sampleRate int
	freq       float64
	amplitude  float64 // 0..1, fraction of full-scale int16
	phase      float64 // radians, kept in [0, 2*pi)
}

// New returns a sine generator. amplitude is 0..1; 0.5 is ~ -6 dBFS.
func New(sampleRate int, freq, amplitude float64) *Generator {
	return &Generator{sampleRate: sampleRate, freq: freq, amplitude: amplitude}
}

// Next returns 2*samples bytes of S16LE PCM. Phase is preserved across calls
// so successive chunks form a continuous waveform.
func (g *Generator) Next(samples int) []byte {
	out := make([]byte, samples*2)
	step := 2 * math.Pi * g.freq / float64(g.sampleRate)
	scale := g.amplitude * math.MaxInt16
	for i := 0; i < samples; i++ {
		v := int16(scale * math.Sin(g.phase))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
		g.phase += step
		if g.phase >= 2*math.Pi {
			g.phase -= 2 * math.Pi
		}
	}
	return out
}
