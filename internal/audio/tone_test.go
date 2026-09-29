package audio

import (
	"math"
	"testing"
)

func TestToneReturnsRequestedSampleCount(t *testing.T) {
	if got := len(NewTone(16000, 440, 0.5).Next(1024)); got != 1024 {
		t.Fatalf("got %d samples, want 1024", got)
	}
}

func TestTonePeakMatchesAmplitude(t *testing.T) {
	samples := NewTone(16000, 440, 0.5).Next(16000)

	var peak float64
	for _, sample := range samples {
		peak = math.Max(peak, math.Abs(float64(sample)))
	}

	want := 0.5 * math.MaxInt16
	if math.Abs(peak-want) > 200 {
		t.Fatalf("peak %.0f is not within 200 of %.0f", peak, want)
	}
}

// Frames must join without a discontinuity, otherwise every frame boundary
// is an audible click.
func TestTonePhaseCarriesAcrossCalls(t *testing.T) {
	combined := NewTone(16000, 440, 0.5).Next(2048)

	split := NewTone(16000, 440, 0.5)
	first := split.Next(1024)
	second := split.Next(1024)

	for i := range first {
		if first[i] != combined[i] {
			t.Fatalf("sample %d of the first frame: got %d, want %d", i, first[i], combined[i])
		}
	}

	for i := range second {
		if second[i] != combined[1024+i] {
			t.Fatalf("sample %d of the second frame: got %d, want %d", i, second[i], combined[1024+i])
		}
	}
}
