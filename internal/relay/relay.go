// Package relay is the sonic-bridge server. It accepts encoded audio from one
// source at a time and delivers it to every attached destination. The relay
// never decodes or re-encodes audio on the live path: it forwards frames with
// the format the source declared, and destinations decode.
//
// Sources arrive over raw TCP, which is all the ESP32 firmware and the console
// need. Destinations arrive over a WebSocket, for the browser player, or over
// HTTP as an endless WAV file, for any ordinary media player.
package relay

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"sonic-bridge/internal/stream"
)

const (
	// queueDepth is how many frames a destination may fall behind before its
	// oldest is dropped. At 20 ms frames that is a little under one second, so
	// it bounds recovery latency after a stall as much as it bounds memory.
	queueDepth = 48

	// writeTimeout bounds one write to one destination. A destination slower
	// than this is disconnected rather than left holding a slot.
	writeTimeout = 5 * time.Second

	// sourceTimeout bounds the waits that depend on a source being alive: the
	// gap between audio/quiet records, and how long a WAV request waits for
	// the first audio or quiet state before reporting no source.
	sourceTimeout = 10 * time.Second
)

// Config is the relay's complete runtime configuration.
type Config struct {
	// HttpAddr carries the player, the listener WebSocket and the WAV stream.
	HttpAddr string

	// TcpSourceAddr accepts sources. It is unauthenticated and unencrypted,
	// because the firmware that uses it has no TLS stack.
	TcpSourceAddr string
}

// Relay owns the stream and the listeners that feed and drain it.
type Relay struct {
	config              Config
	log                 *slog.Logger
	stream              *stream.Stream
	ingressAudioBytes   atomic.Uint64
	ingressControlBytes atomic.Uint64
	egressAudioBytes    atomic.Uint64
	egressControlBytes  atomic.Uint64
	wavBytes            atomic.Uint64

	// sourceTimeout is a field rather than a bare constant only so tests can
	// shorten it. Nothing in production changes it.
	sourceTimeout time.Duration
}

// New returns a relay with an idle stream.
func New(config Config, log *slog.Logger) *Relay {
	return &Relay{
		config:        config,
		log:           log,
		stream:        stream.New(),
		sourceTimeout: sourceTimeout,
	}
}

// Run serves the HTTP surface and the TCP source port until ctx is cancelled.
// It returns the first error that stopped a listener.
func (r *Relay) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	tasks := []func(context.Context) error{r.serveHttp, r.serveTcpSource}
	exits := make(chan error, len(tasks))

	for _, task := range tasks {
		go func() { exits <- task(ctx) }()
	}

	var first error
	for range tasks {
		err := <-exits
		cancel()

		if first == nil && err != nil && !errors.Is(err, context.Canceled) {
			first = err
		}
	}

	return first
}

// Handler is the relay's HTTP surface. It is exported so tests can exercise
// the routes without binding a port.
func (r *Relay) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", r.handleHealth)
	mux.HandleFunc("GET /stats", r.handleStats)
	mux.HandleFunc("GET /listen", r.handleWebSocketListener)
	mux.HandleFunc("GET /stream.wav", r.handleWavListener)
	mux.Handle("/", newWebHandler())

	return mux
}

func (r *Relay) serveHttp(ctx context.Context) error {
	server := &http.Server{
		Addr:              r.config.HttpAddr,
		Handler:           r.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	r.log.Info("http listening", "addr", r.config.HttpAddr)

	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
