package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"sonic-bridge/internal/audio"
)

// defaultRelayPort is the relay's raw TCP source port, so a bare hostname is
// enough for --server.
const defaultRelayPort = "9000"

// relayTimeout bounds one dial and one frame write. A source that cannot place
// a frame within this is not going to catch up, so failing and reconnecting
// beats queueing.
const relayTimeout = 5 * time.Second

// relayConnection is the last stage of the pipeline. It carries encoded frames
// to the relay over raw TCP: a format header, then timestamped audio or quiet
// records, matching the ESP32 firmware.
type relayConnection struct {
	conn net.Conn
}

// dialRelay opens a source connection to an already-resolved address and
// declares the wire format. The relay either accepts the stream or closes it,
// so a rejection surfaces as a failed write on a later frame rather than as a
// reply.
func dialRelay(ctx context.Context, addr string, format audio.Format) (*relayConnection, error) {
	dialer := net.Dialer{Timeout: relayTimeout}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dialling %s: %w", addr, err)
	}

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	relay := &relayConnection{conn: conn}

	if err := relay.write(format.BuildSparseHeader()); err != nil {
		relay.close()

		return nil, fmt.Errorf("declaring the format: %w", err)
	}

	return relay, nil
}

func (r *relayConnection) write(payload []byte) error {
	if err := r.conn.SetWriteDeadline(time.Now().Add(relayTimeout)); err != nil {
		return err
	}

	for len(payload) > 0 {
		n, err := r.conn.Write(payload)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

func (r *relayConnection) close() {
	_ = r.conn.Close()
}

// resolveRelayAddr turns a --server value into a dialable TCP address. A bare
// host gains the relay's default source port. A URL is rejected rather than
// coerced, because the console used to take one and a silent reinterpretation
// would be worse than an error naming the port.
func resolveRelayAddr(server string) (string, error) {
	trimmed := strings.TrimSpace(server)

	if trimmed == "" {
		return "", errors.New("--server needs a relay address, for example 127.0.0.1:9000")
	}

	if strings.Contains(trimmed, "://") {
		return "", fmt.Errorf(
			"--server takes the relay's TCP source address, not a URL: try host:%s instead of %q",
			defaultRelayPort, server)
	}

	if _, _, err := net.SplitHostPort(trimmed); err == nil {
		return trimmed, nil
	}

	return net.JoinHostPort(trimmed, defaultRelayPort), nil
}
