package audio

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Deterministic environmental fixture, shared byte-for-byte with the C++ check.
// Sections: silence, steady hiss/hum, impulses, new steady frequency, repeated
// footsteps over the new background, then a steady background again.
func activityFixture() [][]int16 {
	frames := make([][]int16, 220*50)
	seed := uint32(42)
	for frame := range frames {
		samples := make([]int16, 320)
		seconds := float64(frame) / 50
		for i := range samples {
			n := frame*320 + i
			seed = seed*1664525 + 1013904223
			noise := float64(int32(seed>>16)-32768) / 32768 * 60
			value := 0.0
			if seconds >= 40 {
				frequency := 120.0
				if seconds >= 100 {
					frequency = 2400
				}
				value = noise + 200*math.Sin(2*math.Pi*frequency*float64(n)/16000)
			}
			if frame == 80*50 && i == 40 {
				value += 8000
			}
			if seconds >= 140 && seconds < 180 && frame%100 < 5 {
				value += 500 * math.Sin(2*math.Pi*90*float64(n)/16000)
			}
			samples[i] = int16(value)
		}
		frames[frame] = samples
	}
	return frames
}

func TestActivityEnvironmentalChanges(t *testing.T) {
	frames := activityFixture()
	detector := NewActivity(16000, DefaultSensitivityDB, DefaultSettle)
	states := make([]byte, len(frames))
	var pcm []byte
	for i, samples := range frames {
		if detector.Active(samples) {
			states[i] = 1
		}
		if os.Getenv("SONIC_ACTIVITY_DIR") != "" {
			pcm = append(pcm, EncodePcm16(samples)...)
		}
	}
	for _, second := range []int{35, 75, 135, 215} {
		if states[second*50] != 0 {
			t.Errorf("steady background at %ds must suppress", second)
		}
	}
	for _, second := range []int{0, 41, 80, 101, 141, 160, 179, 200} {
		if states[second*50] != 1 {
			t.Errorf("activity/tail at %ds must stream", second)
		}
	}
	for frame := 140 * 50; frame < 180*50; frame++ {
		if states[frame] != 1 {
			t.Fatalf("recurring activity suppressed at frame %d", frame)
		}
	}
	if dir := os.Getenv("SONIC_ACTIVITY_DIR"); dir != "" {
		for name, data := range map[string][]byte{"activity.pcm": pcm, "go-activity.bin": states} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestGatePreRollAndTenMinuteSilence(t *testing.T) {
	f := Format{Codec: CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}
	gate := NewGate(f, DefaultSensitivityDB, DefaultSettle, true)
	var quietRecords, quietSamples uint64
	var lastSent uint64
	quiet := false
	for i := 0; i < 632*50; i++ {
		samples := make([]int16, 320)
		if i == 631*50 {
			samples[42] = 5000
		}
		r, ready := gate.Push(samples)
		if !ready {
			continue
		}
		if i >= 31*50 && i < 631*50 && !r.Quiet() {
			t.Fatalf("audio transmitted during silence at frame %d", i)
		}
		if r.Quiet() {
			quietSamples += 320
			if !quiet || r.Position-lastSent >= 80000 {
				quietRecords++
				lastSent = r.Position
			}
		}
		quiet = r.Quiet()
		if i == 631*50 {
			if r.Quiet() || r.Position != uint64((i-10)*320) {
				t.Fatal("event must open with 200ms lead-in")
			}
		}
		if i == 631*50+10 {
			if r.Quiet() || DecodeMulaw(r.Frame)[42] < 4500 {
				t.Fatal("event onset lost")
			}
		}
	}
	if quietRecords > 121 || quietRecords < 119 {
		t.Fatalf("got %d quiet records, expected about 120", quietRecords)
	}
	if quietSamples < 600*16000 {
		t.Fatalf("only %d suppressed samples", quietSamples)
	}
}

func TestActivityQuietOnsetsAndSensitivity(t *testing.T) {
	for _, amplitude := range []int16{24, 300, 12000} {
		d := NewActivity(16000, 3, 30*time.Second)
		for range 1600 {
			d.Active(make([]int16, 320))
		}
		samples := make([]int16, 320)
		samples[0] = amplitude
		if !d.Active(samples) {
			t.Errorf("missed impulse at PCM amplitude %d", amplitude)
		}
	}
}

func TestStationaryHissSettles(t *testing.T) {
	d := NewActivity(16000, 3, DefaultSettle)
	seed := uint32(1)
	quiet := 0
	for frame := 0; frame < 100*50; frame++ {
		samples := make([]int16, 320)
		// Sum of uniforms approximates microphone hiss without a fixed waveform.
		for i := range samples {
			value := 0
			for range 6 {
				seed = seed*1664525 + 1013904223
				value += int(seed>>24) - 128
			}
			samples[i] = int16(value)
		}
		if !d.Active(samples) && frame >= 40*50 {
			quiet++
		}
	}
	if quiet < 55*50 {
		t.Fatalf("stationary hiss suppressed for only %.1fs of the final 60s", float64(quiet)/50)
	}
}
