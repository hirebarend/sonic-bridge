package tone

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestNextReturnsCorrectByteCount(t *testing.T) {
	g := New(16000, 440, 0.5)
	got := g.Next(1024)
	if len(got) != 2048 {
		t.Fatalf("expected 2048 bytes, got %d", len(got))
	}
}

func TestPeakAmplitudeWithinExpectedRange(t *testing.T) {
	g := New(16000, 440, 0.5)
	buf := g.Next(16000) // one second
	var peak int16
	for i := 0; i < len(buf); i += 2 {
		v := int16(binary.LittleEndian.Uint16(buf[i:]))
		if a := int16Abs(v); a > peak {
			peak = a
		}
	}
	// Expected peak: 0.5 * 32767 ~ 16383. Allow generous tolerance.
	expected := int16(math.Round(0.5 * math.MaxInt16))
	low, high := expected-200, expected+200
	if peak < low || peak > high {
		t.Fatalf("peak %d outside [%d,%d]", peak, low, high)
	}
}

func TestPhaseContinuityAcrossCalls(t *testing.T) {
	// Two back-to-back Next() calls should produce the same bytes as one
	// Next() of the combined length.
	a := New(16000, 440, 0.5)
	combined := a.Next(2048)

	b := New(16000, 440, 0.5)
	first := b.Next(1024)
	second := b.Next(1024)

	if !bytesEqual(combined[:2048], first) {
		t.Fatal("first half mismatch")
	}
	if !bytesEqual(combined[2048:], second) {
		t.Fatal("second half mismatch (phase not preserved)")
	}
}

func int16Abs(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
