package source

import (
	"context"
	"fmt"
	"time"

	"sonic-bridge/internal/audio"
	"sonic-bridge/internal/queue"
)

// InputName selects where the console gets its audio.
type InputName string

const (
	// InputDevice captures from a microphone.
	InputDevice InputName = "device"
	// InputTone synthesises a sine wave.
	InputTone InputName = "tone"
)

// Capture is the first stage of the pipeline: it produces frames of linear
// 16-bit mono samples at the format's sample rate.
type Capture interface {
	// Frames starts capturing and returns the channel frames arrive on. The
	// channel closes when ctx is cancelled or the input stops.
	Frames(ctx context.Context) (<-chan []int16, error)

	// Describe names the input for the startup log line.
	Describe() string
}

// toneCapture synthesises audio. It exercises the whole pipeline on a machine
// with no microphone, and it is the same generator the relay uses for its test
// stream, so the console and the relay produce comparable signals.
type toneCapture struct {
	format    audio.Format
	frequency float64
	amplitude float64
}

func (c toneCapture) Describe() string {
	return fmt.Sprintf("tone generator, %.0f Hz at %.2f of full scale", c.frequency, c.amplitude)
}

func (c toneCapture) Frames(ctx context.Context) (<-chan []int16, error) {
	frames := make(chan []int16, captureQueueDepth)
	tone := audio.NewTone(c.format.SampleRate, c.frequency, c.amplitude)

	go func() {
		defer close(frames)

		ticker := time.NewTicker(c.format.FrameDuration())
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				queue.Offer(frames, tone.Next(c.format.FrameSamples))
			}
		}
	}()

	return frames, nil
}
