//go:build !cgo

package source

import (
	"errors"
	"log/slog"

	"sonic-bridge/internal/audio"
)

var errCaptureUnavailable = errors.New(
	"this binary was built without cgo, so it cannot open an audio device; use --input tone")

// newDeviceCapture is unavailable without cgo, because the miniaudio backend
// is C. The relay binary is built this way on purpose so it links statically;
// the console is built with cgo enabled.
func newDeviceCapture(_ audio.Format, _ string, _ *slog.Logger) (Capture, error) {
	return nil, errCaptureUnavailable
}

// FindCaptureDeviceNames cannot enumerate devices without cgo.
func FindCaptureDeviceNames() ([]string, error) {
	return nil, errCaptureUnavailable
}
