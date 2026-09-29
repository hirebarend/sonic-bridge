//go:build cgo

package source

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/gen2brain/malgo"

	"sonic-bridge/internal/audio"
	"sonic-bridge/internal/queue"
)

// deviceCapture reads from a microphone through miniaudio, which uses
// CoreAudio, WASAPI, ALSA or PulseAudio depending on the host. The backend is
// vendored, so there is no system audio library to install.
type deviceCapture struct {
	format     audio.Format
	deviceName string
	log        *slog.Logger
}

func newDeviceCapture(format audio.Format, deviceName string, log *slog.Logger) (Capture, error) {
	return &deviceCapture{format: format, deviceName: deviceName, log: log}, nil
}

func (c *deviceCapture) Describe() string {
	if c.deviceName == "" {
		return "default capture device"
	}

	return fmt.Sprintf("capture device matching %q", c.deviceName)
}

func (c *deviceCapture) Frames(ctx context.Context) (<-chan []int16, error) {
	backend, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("starting the audio backend: %w", err)
	}

	config, err := c.buildDeviceConfig(backend)
	if err != nil {
		releaseBackend(backend)

		return nil, err
	}

	frames := make(chan []int16, captureQueueDepth)
	collector := &frameCollector{frameSamples: c.format.FrameSamples, frames: frames}

	device, err := malgo.InitDevice(backend.Context, config, malgo.DeviceCallbacks{Data: collector.collect})
	if err != nil {
		releaseBackend(backend)

		return nil, fmt.Errorf("opening the capture device: %w", err)
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		releaseBackend(backend)

		return nil, fmt.Errorf("starting the capture device: %w", err)
	}

	go func() {
		<-ctx.Done()

		_ = device.Stop()
		device.Uninit()
		releaseBackend(backend)

		if dropped := collector.dropped.Load(); dropped > 0 {
			c.log.Warn("frames dropped between capture and transmit", "frames", dropped)
		}

		close(frames)
	}()

	return frames, nil
}

func (c *deviceCapture) buildDeviceConfig(backend *malgo.AllocatedContext) (malgo.DeviceConfig, error) {
	config := malgo.DefaultDeviceConfig(malgo.Capture)
	config.Capture.Format = malgo.FormatS16
	config.Capture.Channels = 1
	config.SampleRate = uint32(c.format.SampleRate)
	config.PeriodSizeInFrames = uint32(c.format.FrameSamples)
	config.Alsa.NoMMap = 1

	if c.deviceName == "" {
		return config, nil
	}

	devices, err := backend.Devices(malgo.Capture)
	if err != nil {
		return config, fmt.Errorf("listing capture devices: %w", err)
	}

	selected := findCaptureDevice(devices, c.deviceName)
	if selected == nil {
		return config, fmt.Errorf("no capture device matching %q, available: %s",
			c.deviceName, strings.Join(captureDeviceNames(devices), ", "))
	}

	config.Capture.DeviceID = selected.ID.Pointer()

	return config, nil
}

// frameCollector reassembles the driver's callback buffers into fixed-size
// frames. miniaudio does not promise a callback carries exactly
// PeriodSizeInFrames samples, so a partial frame carries over to the next
// callback.
type frameCollector struct {
	frameSamples int
	frames       chan []int16
	pending      []int16
	dropped      atomic.Uint64
}

// collect runs on the audio thread. It must never block or allocate
// unboundedly, which is why it hands frames to a drop-oldest queue.
func (c *frameCollector) collect(_, input []byte, frameCount uint32) {
	available := min(len(input), int(frameCount)*2)
	if available <= 0 {
		return
	}

	c.pending = append(c.pending, audio.DecodePcm16(input[:available])...)

	for len(c.pending) >= c.frameSamples {
		frame := make([]int16, c.frameSamples)
		copy(frame, c.pending)
		c.pending = append(c.pending[:0], c.pending[c.frameSamples:]...)

		if !queue.Offer(c.frames, frame) {
			c.dropped.Add(1)
		}
	}
}

// FindCaptureDeviceNames lists the microphones this host exposes, for the
// console's --list-devices flag.
func FindCaptureDeviceNames() ([]string, error) {
	backend, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("starting the audio backend: %w", err)
	}
	defer releaseBackend(backend)

	devices, err := backend.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("listing capture devices: %w", err)
	}

	return captureDeviceNames(devices), nil
}

// findCaptureDevice returns the first device whose name contains name, case
// insensitively, or nil when none does.
func findCaptureDevice(devices []malgo.DeviceInfo, name string) *malgo.DeviceInfo {
	wanted := strings.ToLower(name)

	for i := range devices {
		if strings.Contains(strings.ToLower(devices[i].Name()), wanted) {
			return &devices[i]
		}
	}

	return nil
}

func captureDeviceNames(devices []malgo.DeviceInfo) []string {
	names := make([]string, 0, len(devices))
	for i := range devices {
		names = append(names, devices[i].Name())
	}

	return names
}

func releaseBackend(backend *malgo.AllocatedContext) {
	_ = backend.Uninit()
	backend.Free()
}
