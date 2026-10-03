package audio

import (
	"math"
	"time"
)

const (
	DefaultSensitivityDB = 3.0
	DefaultSettle        = 30 * time.Second
	PreRoll              = 200 * time.Millisecond
	QuietHeartbeat       = 5 * time.Second
)

// Activity detects environmental changes, not speech. A candidate background is
// held fixed until a deviation replaces it; recurring sounds cannot slowly train
// an averaging baseline to ignore themselves. Mirrored in esp32/src/activity.h.
// ponytail: four broad bands cannot distinguish all equal-energy sounds; use a
// finer spectrum only if real microphone recordings demonstrate missed events.
type Activity struct {
	alpha                         [3]float64
	low                           [3]float64
	dc, dcAlpha, smoothing, ratio float64
	energy, reference             [6]float64
	initialized                   bool
	stable, settle                uint64
}

func NewActivity(rate int, sensitivityDB float64, settle time.Duration) *Activity {
	a := &Activity{ratio: math.Pow(10, sensitivityDB/10), settle: uint64(settle.Seconds() * float64(rate))}
	// Rate-relative edges: 250, 1000, 3000 Hz at the default 16 kHz.
	for i, fraction := range []float64{1.0 / 64, 1.0 / 16, 3.0 / 16} {
		a.alpha[i] = 1 - math.Exp(-2*math.Pi*fraction)
	}
	a.dcAlpha = 1 - math.Exp(-2*math.Pi*20/float64(rate))
	a.smoothing = float64(rate) * 0.1
	return a
}

func (a *Activity) Active(samples []int16) bool {
	if len(samples) == 0 {
		return true
	}
	var energy [6]float64
	for _, sample := range samples {
		x := float64(sample)
		a.dc += a.dcAlpha * (x - a.dc)
		x -= a.dc
		for i := range a.low {
			a.low[i] += a.alpha[i] * (x - a.low[i])
		}
		bands := [5]float64{x, a.low[0], a.low[1] - a.low[0], a.low[2] - a.low[1], x - a.low[2]}
		for i, value := range bands {
			energy[i] += value * value
		}
		energy[5] = math.Max(energy[5], x*x)
	}
	weight := 1 - math.Exp(-float64(len(samples))/a.smoothing)
	changed := !a.initialized
	for i := range energy {
		if i < 5 {
			energy[i] /= float64(len(samples))
		}
		// Ignore fluctuations below a small fixed floor (8 PCM units RMS).
		energy[i] = math.Max(64, energy[i])
		a.energy[i] += weight * (energy[i] - a.energy[i])
		value := math.Max(64, a.energy[i])
		if value > a.reference[i]*a.ratio || value*a.ratio < a.reference[i] {
			changed = true
		}
		// A brief impact must open immediately, before the 100 ms smoothing.
		if i == 5 && energy[i] > math.Max(64, a.reference[i])*a.ratio*a.ratio {
			changed = true
		}
	}
	if changed {
		for i, value := range a.energy {
			a.reference[i] = math.Max(64, value)
		}
		a.stable = 0
		a.initialized = true
	} else if a.stable < a.settle {
		a.stable += uint64(len(samples))
	}
	return a.stable < a.settle
}

// Gate keeps a fixed lead-in delay. It returns a record every frame, even when
// quiet, so bounded queues can always recover the latest state after a drop.
// Transmitters coalesce quiet records to one heartbeat every five seconds.
type Gate struct {
	detector      *Activity
	frames        [][]int16
	active        []bool
	index, filled int
	position      uint64
}

func NewGate(f Format, sensitivityDB float64, settle time.Duration, enabled bool) *Gate {
	g := &Gate{}
	if enabled {
		g.detector = NewActivity(f.SampleRate, sensitivityDB, settle)
		depth := max(1, int((PreRoll+f.FrameDuration()-1)/f.FrameDuration()))
		g.frames = make([][]int16, depth)
		g.active = make([]bool, depth)
	}
	return g
}

// Push takes ownership of samples until they leave the delay buffer.
func (g *Gate) Push(samples []int16) (Record, bool) {
	active := true
	if g.detector != nil {
		active = g.detector.Active(samples)
		old, wasActive := g.frames[g.index], g.active[g.index]
		g.frames[g.index], g.active[g.index] = samples, active
		g.index = (g.index + 1) % len(g.frames)
		if g.filled < len(g.frames) {
			g.filled++
			return Record{}, false
		}
		samples = old
		active = active || wasActive
	}
	r := Record{Position: g.position}
	g.position += uint64(len(samples))
	if active {
		r.Frame = EncodeMulaw(samples)
	}
	return r, true
}
