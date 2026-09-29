package audio

import "math"

// Tone generates a continuous sine wave as linear 16-bit samples. It is the
// relay's built-in test signal: a destination can always play something, even
// with no source connected.
type Tone struct {
	sampleRate int
	frequency  float64
	amplitude  float64
	phase      float64
}

// NewTone returns a generator at the given frequency. amplitude is a fraction
// of full scale, so 0.5 is about -6 dBFS.
func NewTone(sampleRate int, frequency, amplitude float64) *Tone {
	return &Tone{sampleRate: sampleRate, frequency: frequency, amplitude: amplitude}
}

// Next returns the next samples of the wave. Phase carries across calls, so
// successive frames join without a click.
func (t *Tone) Next(samples int) []int16 {
	out := make([]int16, samples)
	step := 2 * math.Pi * t.frequency / float64(t.sampleRate)
	scale := t.amplitude * math.MaxInt16

	for i := range out {
		out[i] = int16(scale * math.Sin(t.phase))
		t.phase += step

		if t.phase >= 2*math.Pi {
			t.phase -= 2 * math.Pi
		}
	}

	return out
}
