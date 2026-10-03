// Command console is a sonic-bridge source. It captures audio from a
// microphone, or synthesises a test tone, encodes it, and streams it to the
// relay over raw TCP.
//
// It speaks the same wire as the ESP32 firmware: one binary format header,
// then timestamped audio/quiet records. See internal/source/source.go for the pipeline and
// esp32/src/main.cpp for the firmware that mirrors it.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"sonic-bridge/internal/audio"
	"sonic-bridge/internal/source"
)

func main() {
	config, listDevices := parseFlags()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if listDevices {
		if err := printCaptureDevices(); err != nil {
			log.Error("cannot list capture devices", "error", err)
			os.Exit(1)
		}

		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := source.New(config, log).Run(ctx); err != nil {
		log.Error("source stopped", "error", err)
		os.Exit(1)
	}

	log.Info("source stopped cleanly")
}

func parseFlags() (source.Config, bool) {
	server := flag.String("server", "127.0.0.1:9000",
		"relay TCP source address, as host or host:port")
	input := flag.String("input", string(source.InputDevice),
		"where to get audio: device or tone")
	deviceName := flag.String("device", "",
		"capture device to open, matched as a case-insensitive substring of its name; empty selects the default")
	listDevices := flag.Bool("list-devices", false,
		"print the capture devices this host exposes and exit")
	gain := flag.Float64("gain", 1.0,
		"linear gain applied before encoding, clipped at full scale")
	suppression := flag.Bool("suppression", true, "suppress steady background sound (tone input always streams)")
	sensitivity := flag.Float64("sensitivity-db", audio.DefaultSensitivityDB, "background change threshold in dB; lower is more sensitive (0.5..24)")
	settle := flag.Duration("settle", audio.DefaultSettle, "stable period before suppression (1s..10m)")
	flag.Parse()

	config := source.Config{
		ServerAddr:         *server,
		Input:              source.InputName(*input),
		DeviceName:         *deviceName,
		Gain:               *gain,
		DisableSuppression: !*suppression,
		SensitivityDB:      *sensitivity,
		Settle:             *settle,
	}

	return config, *listDevices
}

func printCaptureDevices() error {
	names, err := source.FindCaptureDeviceNames()
	if err != nil {
		return err
	}

	if len(names) == 0 {
		fmt.Println("no capture devices found")

		return nil
	}

	fmt.Println("capture devices:")
	for _, name := range names {
		fmt.Printf("  %s\n", name)
	}

	return nil
}
