// Command server runs the sonic-bridge relay: it accepts encoded audio from
// one source at a time and delivers it to every connected destination.
//
// Sources reach it over raw TCP, which is all the ESP32 firmware and the
// console need. Destinations reach it over a WebSocket for low latency, or
// over HTTP as an endless WAV file for any ordinary media player. The wire
// format is declared by each source, not agreed by convention: see
// internal/audio for the header and the codecs.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sonic-bridge/internal/relay"
)

func main() {
	config, healthCheck := parseFlags()

	if healthCheck {
		if err := checkHealth(config.HttpAddr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := relay.New(config, log).Run(ctx); err != nil {
		log.Error("relay stopped", "error", err)
		os.Exit(1)
	}

	log.Info("relay stopped cleanly")
}

func parseFlags() (relay.Config, bool) {
	httpAddr := flag.String("http-addr", ":8080",
		"address for the player, the listener WebSocket and the WAV stream")
	tcpSourceAddr := flag.String("tcp-source-addr", ":9000",
		"address for TCP sources: the ESP32 firmware and the console")
	healthCheck := flag.Bool("health-check", false,
		"probe the health endpoint of a relay already listening on --http-addr, then exit; the deployed image has no shell, so this is its container health check")
	flag.Parse()

	config := relay.Config{
		HttpAddr:      *httpAddr,
		TcpSourceAddr: *tcpSourceAddr,
	}

	return config, *healthCheck
}

// checkHealth probes a relay that is already listening. It is the container
// health check, because the deployed image contains only the binary and has no
// shell or curl to run one.
func checkHealth(httpAddr string) error {
	client := &http.Client{Timeout: 3 * time.Second}

	response, err := client.Get("http://" + buildLoopbackAddr(httpAddr) + "/healthz")
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}

	return nil
}

// buildLoopbackAddr turns a listen address into one a probe can dial. A listen
// address often omits the host, and ":8080" is not dialable.
func buildLoopbackAddr(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return listenAddr
	}

	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	return net.JoinHostPort(host, port)
}
