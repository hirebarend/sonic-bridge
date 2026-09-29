package source

import (
	"context"
	"math"
	"testing"
	"time"

	"sonic-bridge/internal/audio"
)

func TestResolveRelayAddrDefaultsThePort(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1":              "127.0.0.1:9000",
		"127.0.0.1:9000":         "127.0.0.1:9000",
		"relay.example.com":      "relay.example.com:9000",
		"relay.example.com:9100": "relay.example.com:9100",
		"  127.0.0.1  ":          "127.0.0.1:9000",
		"::1":                    "[::1]:9000",
		"[::1]:9000":             "[::1]:9000",
	}

	for server, want := range cases {
		got, err := resolveRelayAddr(server)
		if err != nil {
			t.Errorf("%q: %v", server, err)

			continue
		}

		if got != want {
			t.Errorf("%q: got %s, want %s", server, got, want)
		}
	}
}

// The console used to take a URL. Coercing one silently would send frames to
// the HTTP port and fail with a confusing read error, so it is rejected with a
// message naming the right port.
func TestResolveRelayAddrRejectsUrls(t *testing.T) {
	for _, server := range []string{"http://127.0.0.1:8080", "https://relay.example.com", "ws://host:9000", ""} {
		if _, err := resolveRelayAddr(server); err == nil {
			t.Errorf("%q: expected an error", server)
		}
	}
}

// The caller owns the frame it passes, so gain must never write through it.
func TestApplyGainDoesNotMutateItsInput(t *testing.T) {
	samples := []int16{100, -200, 300}
	original := append([]int16(nil), samples...)

	applyGain(samples, 4)

	for i := range samples {
		if samples[i] != original[i] {
			t.Fatalf("applyGain modified the caller's frame at %d: %d became %d", i, original[i], samples[i])
		}
	}
}

func TestApplyGainReturnsTheInputAtUnityGain(t *testing.T) {
	samples := []int16{1, 2, 3}

	if got := applyGain(samples, 1); &got[0] != &samples[0] {
		t.Fatal("unity gain must not copy the frame")
	}
}

func TestApplyGainClipsAtFullScale(t *testing.T) {
	amplified := applyGain([]int16{20000, -20000}, 4)

	if amplified[0] != math.MaxInt16 {
		t.Errorf("positive clip = %d, want %d", amplified[0], math.MaxInt16)
	}

	if amplified[1] != math.MinInt16 {
		t.Errorf("negative clip = %d, want %d", amplified[1], math.MinInt16)
	}
}

func TestPeakLevelIsAFractionOfFullScale(t *testing.T) {
	if got := peakLevel([]int16{0, 0}); got != 0 {
		t.Errorf("silence reported %v, want 0", got)
	}

	if got := peakLevel([]int16{math.MaxInt16}); got != 1 {
		t.Errorf("full scale reported %v, want 1", got)
	}

	if got := peakLevel([]int16{-16384, 100}); math.Abs(got-0.5) > 0.001 {
		t.Errorf("half scale reported %v, want about 0.5", got)
	}
}

func TestDecibelsFullScaleReportsAFloorForSilence(t *testing.T) {
	if got := toDecibelsFullScale(0); got != -120 {
		t.Errorf("silence reported %v dBFS, want the -120 floor", got)
	}

	if got := toDecibelsFullScale(1); got != 0 {
		t.Errorf("full scale reported %v dBFS, want 0", got)
	}

	if got := toDecibelsFullScale(0.5); math.Abs(got-(-6.02)) > 0.05 {
		t.Errorf("half scale reported %v dBFS, want about -6.02", got)
	}
}

func TestToneCaptureProducesFramesOfTheDeclaredSize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	format := audio.Format{Codec: audio.CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}
	capture := toneCapture{format: format, frequency: 440, amplitude: 0.5}

	frames, err := capture.Frames(ctx)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("the capture channel closed before producing a frame")
		}

		if len(frame) != format.FrameSamples {
			t.Fatalf("frame has %d samples, want %d", len(frame), format.FrameSamples)
		}
	case <-ctx.Done():
		t.Fatal("no frame was produced")
	}
}

func TestToneCaptureClosesItsChannelOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	format := audio.Format{Codec: audio.CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}
	capture := toneCapture{format: format, frequency: 440, amplitude: 0.5}

	frames, err := capture.Frames(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-frames:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the capture channel did not close after cancellation")
		}
	}
}

func TestBuildCaptureRejectsAnUnknownInput(t *testing.T) {
	s := &Source{config: Config{Input: "microphone"}}

	if _, err := s.buildCapture(); err == nil {
		t.Fatal("expected an error for an unknown input name")
	}
}

// The format is now built entirely from package constants, so this guards the
// constants rather than any caller input.
func TestTheDeclaredFormatIsValid(t *testing.T) {
	if err := New(Config{}, discardLogger()).format.Validate(); err != nil {
		t.Fatalf("the format built from package constants is invalid: %v", err)
	}
}

// An unusable --server value has no valid continuation, so Run must fail
// immediately rather than fall into the reconnect loop and retry forever.
func TestRunRejectsAnUnusableRelayAddressWithoutRetrying(t *testing.T) {
	s := New(Config{ServerAddr: "http://127.0.0.1:8080", Input: InputTone}, discardLogger())

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error for a URL passed as --server")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run retried an unusable address instead of returning")
	}
}
