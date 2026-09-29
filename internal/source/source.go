// Package source runs the console's capture pipeline, in three stages:
//
//	capture  -> linear 16-bit mono frames from a device or the tone generator
//	encode   -> audio.Codec turns frames into wire bytes
//	transmit -> raw TCP carries the bytes to the relay
//
// The transmit stage speaks the same wire as esp32/src/main.cpp: one binary
// format header, then frames back to back. That is deliberate, so the two
// sources can be read and compared directly.
package source

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"sonic-bridge/internal/audio"
)

const (
	// sampleRate and frameSamples fix the wire format at 16 kHz in 20 ms
	// frames. They are constants rather than flags because the relay accepts
	// whatever a source declares, so nothing downstream needs them to vary per
	// run. Changing them here changes what this source declares.
	sampleRate   = 16000
	frameSamples = 320

	// toneFrequency and toneAmplitude shape the synthetic input.
	toneFrequency = 440.0
	toneAmplitude = 0.3

	// captureQueueDepth is how many frames may wait between capture and
	// transmit. It is deliberately short: a long queue converts a network stall
	// into growing latency, which is worse for live audio than a gap.
	captureQueueDepth = 16

	// meterInterval paces the level and throughput report.
	meterInterval = 5 * time.Second

	// reconnectDelay is the pause before redialling a lost relay connection.
	reconnectDelay = time.Second
)

// Config is the console's complete runtime configuration.
type Config struct {
	// ServerAddr is the relay's TCP source address, as host or host:port.
	ServerAddr string

	// Input selects a microphone or the built-in tone.
	Input InputName

	// DeviceName selects a capture device by case-insensitive substring.
	// Empty selects the system default.
	DeviceName string

	// Gain is applied before encoding and clipped at full scale.
	Gain float64
}

// Source is the console pipeline. It captures once and reconnects to the relay
// as often as needed, discarding audio while disconnected rather than queueing
// it, so a reconnect resumes live instead of replaying a backlog.
type Source struct {
	config Config
	format audio.Format
	log    *slog.Logger
}

// New returns a source. Call Run to start capturing.
func New(config Config, log *slog.Logger) *Source {
	return &Source{
		config: config,
		format: audio.Format{
			Codec:        audio.CodecMulaw,
			SampleRate:   sampleRate,
			Channels:     1,
			FrameSamples: frameSamples,
		},
		log: log,
	}
}

// Run captures and transmits until ctx is cancelled. It returns an error only
// when the pipeline cannot be started at all; a lost connection is retried.
func (s *Source) Run(ctx context.Context) error {
	if err := s.format.Validate(); err != nil {
		return err
	}

	// Resolved once, before anything starts. An unusable address is a
	// configuration error with no valid continuation, so it must not fall into
	// the reconnect loop below and retry forever.
	relayAddr, err := resolveRelayAddr(s.config.ServerAddr)
	if err != nil {
		return err
	}

	capture, err := s.buildCapture()
	if err != nil {
		return err
	}

	frames, err := capture.Frames(ctx)
	if err != nil {
		return err
	}

	s.log.Info("capture started",
		"input", capture.Describe(),
		"format", s.format.String(),
		"bitrate", fmt.Sprintf("%d kbps", s.bitrateKbps()))

	for ctx.Err() == nil {
		err := s.transmitUntilClosed(ctx, relayAddr, frames)
		if err == nil || ctx.Err() != nil {
			break
		}

		s.log.Warn("relay connection lost", "error", err, "retrying_in", reconnectDelay)

		select {
		case <-ctx.Done():
		case <-time.After(reconnectDelay):
		}
	}

	return nil
}

// transmitUntilClosed holds one relay connection for as long as it lasts. It
// returns nil when capture ended, and an error when the connection did.
func (s *Source) transmitUntilClosed(ctx context.Context, relayAddr string, frames <-chan []int16) error {
	relay, err := dialRelay(ctx, relayAddr, s.format)
	if err != nil {
		return err
	}
	defer relay.close()

	s.log.Info("relay connected", "addr", relayAddr)

	meter := time.NewTicker(meterInterval)
	defer meter.Stop()

	var sent uint64
	var peak float64

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-meter.C:
			s.reportLevel(sent, peak)
			peak = 0
		case samples, ok := <-frames:
			if !ok {
				return nil
			}

			amplified := applyGain(samples, s.config.Gain)
			peak = math.Max(peak, peakLevel(amplified))

			if err := relay.write(audio.EncodeMulaw(amplified)); err != nil {
				return err
			}

			sent++
		}
	}
}

func (s *Source) buildCapture() (Capture, error) {
	switch s.config.Input {
	case InputTone:
		return toneCapture{format: s.format, frequency: toneFrequency, amplitude: toneAmplitude}, nil
	case InputDevice:
		return newDeviceCapture(s.format, s.config.DeviceName, s.log)
	default:
		return nil, fmt.Errorf("unknown input %q, want %s or %s", s.config.Input, InputDevice, InputTone)
	}
}

// bitrateKbps is the wire cost of this source. mu-law is one byte per sample.
func (s *Source) bitrateKbps() int {
	return s.format.SampleRate * 8 / 1000
}

func (s *Source) reportLevel(sent uint64, peak float64) {
	s.log.Info("streaming",
		"frames", sent,
		"peak", fmt.Sprintf("%.1f dBFS", toDecibelsFullScale(peak)))
}

// applyGain scales samples and clips at full scale. It returns a new slice
// because the caller owns the frame it passed, and returns the input unchanged
// at unity gain so the common case copies nothing.
func applyGain(samples []int16, gain float64) []int16 {
	if gain == 1 {
		return samples
	}

	amplified := make([]int16, len(samples))
	for i, sample := range samples {
		amplified[i] = clipToInt16(float64(sample) * gain)
	}

	return amplified
}

func clipToInt16(value float64) int16 {
	if value > math.MaxInt16 {
		return math.MaxInt16
	}

	if value < math.MinInt16 {
		return math.MinInt16
	}

	return int16(value)
}

// peakLevel is the loudest sample in a frame as a fraction of full scale.
func peakLevel(samples []int16) float64 {
	var peak float64
	for _, sample := range samples {
		peak = math.Max(peak, math.Abs(float64(sample)))
	}

	return peak / math.MaxInt16
}

// toDecibelsFullScale converts a 0..1 level to dBFS. Silence reports as the
// floor rather than negative infinity so it formats cleanly in a log line.
func toDecibelsFullScale(level float64) float64 {
	const floor = -120.0

	if level <= 0 {
		return floor
	}

	return math.Max(floor, 20*math.Log10(level))
}
