// Package source runs the console's capture pipeline, in three stages:
//
//	capture  -> linear 16-bit mono frames from a device or the tone generator
//	encode   -> audio.Codec turns frames into wire bytes
//	transmit -> raw TCP carries the bytes to the relay
//
// The transmit stage speaks the same wire as esp32/src/main.cpp: one binary
// format header, then timestamped audio/quiet records. That is deliberate, so the two
// sources can be read and compared directly.
package source

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"sonic-bridge/internal/audio"
	"sonic-bridge/internal/queue"
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
	Gain               float64
	DisableSuppression bool
	SensitivityDB      float64
	Settle             time.Duration
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
	if config.SensitivityDB == 0 {
		config.SensitivityDB = audio.DefaultSensitivityDB
	}
	if config.Settle == 0 {
		config.Settle = audio.DefaultSettle
	}
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
	if math.IsNaN(s.config.Gain) || math.IsInf(s.config.Gain, 0) || s.config.Gain < 0 {
		return fmt.Errorf("gain must be finite and nonnegative")
	}
	if math.IsNaN(s.config.SensitivityDB) || math.IsInf(s.config.SensitivityDB, 0) || s.config.SensitivityDB < 0.5 || s.config.SensitivityDB > 24 {
		return fmt.Errorf("sensitivity must be between 0.5 and 24 dB")
	}
	if s.config.Settle < time.Second || s.config.Settle > 10*time.Minute {
		return fmt.Errorf("settle must be between 1s and 10m")
	}
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

	records := s.encode(ctx, frames)
	for ctx.Err() == nil {
		err := s.transmitUntilClosed(ctx, relayAddr, records)
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
func (s *Source) transmitUntilClosed(ctx context.Context, relayAddr string, records <-chan capturedRecord) error {
	relay, err := dialRelay(ctx, relayAddr, s.format)
	if err != nil {
		return err
	}
	defer relay.close()
	stopClose := context.AfterFunc(ctx, relay.close)
	defer stopClose()
	// Discard data captured while dialing. The worker continues updating the gate.
	for len(records) > 0 {
		<-records
	}

	s.log.Info("relay connected", "addr", relayAddr)

	meter := time.NewTicker(meterInterval)
	defer meter.Stop()

	var sent uint64
	var peak float64
	var audioBytes uint64
	controlBytes := uint64(audio.HeaderBytes)
	var suppressed time.Duration
	var quiet bool
	var lastQuiet time.Time

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-meter.C:
			s.log.Info("streaming", "frames", sent, "audio_bytes", audioBytes, "control_bytes", controlBytes, "suppressed_seconds", suppressed.Seconds(), "peak_dbfs", toDecibelsFullScale(peak))
			peak = 0
		case captured, ok := <-records:
			if !ok {
				return nil
			}
			peak = math.Max(peak, captured.peak)
			record := captured.record
			if record.Quiet() {
				suppressed += s.format.FrameDuration()
				if quiet && time.Since(lastQuiet) < audio.QuietHeartbeat {
					continue
				}
				lastQuiet = time.Now()
			}
			quiet = record.Quiet()
			if err := relay.write(record.Bytes()); err != nil {
				return err
			}
			audioBytes += uint64(len(record.Frame))
			controlBytes += audio.RecordHeaderBytes
			if !quiet {
				sent++
			}
		}
	}
}

type capturedRecord struct {
	record audio.Record
	peak   float64
}

func (s *Source) encode(ctx context.Context, frames <-chan []int16) <-chan capturedRecord {
	records := make(chan capturedRecord, captureQueueDepth)
	gate := audio.NewGate(s.format, s.config.SensitivityDB, s.config.Settle,
		!s.config.DisableSuppression && s.config.Input != InputTone)
	go func() {
		defer close(records)
		for {
			select {
			case <-ctx.Done():
				return
			case samples, ok := <-frames:
				if !ok {
					return
				}
				amplified := applyGain(samples, s.config.Gain)
				if record, ready := gate.Push(amplified); ready {
					queue.Offer(records, capturedRecord{record, peakLevel(amplified)})
				}
			}
		}
	}()
	return records
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

// bitrateKbps is the active audio payload rate of this source. mu-law is one byte per sample.
func (s *Source) bitrateKbps() int {
	return s.format.SampleRate * 8 / 1000
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
