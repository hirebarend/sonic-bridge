// sonic-bridge server. Fans out the ESP32 audio stream and a built-in test
// tone to TCP destination clients. Wire format is documented in the root
// README: 16 kHz / 16-bit signed LE PCM / mono / raw stream.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"sonic-bridge/server/internal/hub"
	"sonic-bridge/server/internal/listener"
	"sonic-bridge/server/internal/tone"
)

const (
	sampleRate    = 16000
	chunkSamples  = 1024
	chunkBytes    = chunkSamples * 2
	chunkInterval = time.Second * time.Duration(chunkSamples) / time.Duration(sampleRate) // 64 ms
)

func main() {
	sourceAddr := flag.String("source-addr", ":9000", "TCP address for the ESP32 source connection")
	liveAddr := flag.String("live-addr", ":9001", "TCP address for live destination clients")
	toneAddr := flag.String("tone-addr", ":9002", "TCP address for test-tone destination clients")
	toneFreq := flag.Float64("tone-freq", 440.0, "test tone frequency in Hz")
	toneAmp := flag.Float64("tone-amp", 0.5, "test tone amplitude (0..1, where 0.5 ~= -6 dBFS)")
	queueDepth := flag.Int("queue-depth", 32, "per-destination chunk queue depth before drop-oldest kicks in")
	writeTimeout := flag.Duration("write-timeout", 2*time.Second, "TCP write timeout per destination chunk")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Live path: ESP32 -> sourceHub -> :9001 destination listener.
	sourceHub := hub.New()
	source := &listener.Source{
		Addr:       *sourceAddr,
		Hub:        sourceHub,
		ChunkBytes: chunkBytes,
		Label:      "source",
	}
	liveDest := &listener.Destination{
		Addr:         *liveAddr,
		Hub:          sourceHub,
		QueueDepth:   *queueDepth,
		WriteTimeout: *writeTimeout,
		Label:        "live",
	}

	// Test-tone path: generator -> toneHub -> :9002 destination listener.
	toneHub := hub.New()
	go runToneGenerator(ctx, toneHub, *toneFreq, *toneAmp)
	toneDest := &listener.Destination{
		Addr:         *toneAddr,
		Hub:          toneHub,
		QueueDepth:   *queueDepth,
		WriteTimeout: *writeTimeout,
		Label:        "tone",
	}

	errCh := make(chan error, 3)
	go func() { errCh <- source.Serve(ctx) }()
	go func() { errCh <- liveDest.Serve(ctx) }()
	go func() { errCh <- toneDest.Serve(ctx) }()

	select {
	case <-ctx.Done():
		log.Printf("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			log.Printf("listener exited with error: %v", err)
		}
	}
}

func runToneGenerator(ctx context.Context, h *hub.Hub, freq, amp float64) {
	g := tone.New(sampleRate, freq, amp)
	ticker := time.NewTicker(chunkInterval)
	defer ticker.Stop()
	log.Printf("tone generator: %.1f Hz, amp=%.2f, %d samples/chunk every %v", freq, amp, chunkSamples, chunkInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.Broadcast(g.Next(chunkSamples))
		}
	}
}
