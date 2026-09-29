package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"sonic-bridge/internal/audio"
	"sonic-bridge/internal/stream"
)

// dropReportInterval paces the per-listener drop heartbeat. Drops are the
// canonical signal that a destination cannot keep up, and reporting them while
// the stream is live makes jitter diagnosable instead of post-mortem only.
const dropReportInterval = 5 * time.Second

// handleWebSocketListener delivers the stream to a destination: a JSON format
// message whenever the format changes, then binary frames of that format. This
// is the low-latency path the browser player uses.
//
// websocket.Accept is given nil options deliberately. The library defaults are
// exactly what this needs: compression disabled, and an Origin check that
// requires same-origin, which holds because the relay serves the player itself.
func (r *Relay) handleWebSocketListener(w http.ResponseWriter, req *http.Request) {
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	listener := r.stream.Attach(req.RemoteAddr, queueDepth)
	defer r.stream.Detach(listener)

	r.log.Info("listener attached", "remote", listener.Name(), "transport", "websocket")
	defer func() {
		r.log.Info("listener detached", "remote", listener.Name(), "dropped", listener.Dropped())
	}()

	r.pumpWebSocket(req.Context(), conn, listener)
}

func (r *Relay) pumpWebSocket(ctx context.Context, conn *websocket.Conn, listener *stream.Listener) {
	dropReports := time.NewTicker(dropReportInterval)
	defer dropReports.Stop()

	var announced audio.Format
	var reportedDrops uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-dropReports.C:
			reportedDrops = r.reportDrops(listener, reportedDrops)
		case packet, ok := <-listener.Packets():
			if !ok {
				return
			}

			if packet.Format != announced {
				if err := writeJsonMessage(ctx, conn, packet.Format); err != nil {
					return
				}

				announced = packet.Format
			}

			// A listener that disappears mid-write is an ordinary departure,
			// and the deferred log line already records it with its drop count.
			if err := writeMessage(ctx, conn, websocket.MessageBinary, packet.Frame); err != nil {
				return
			}
		}
	}
}

// handleWavListener delivers the stream as an endless WAV file, so any media
// player can consume it without knowing anything about sonic-bridge:
// `ffplay http://host/stream.wav`, VLC, or curl. This is the only place the
// relay decodes audio, because WAV carries linear PCM only.
//
// It is not the browser path. Safari hands a media element to AVFoundation,
// which rejects a chunked response with no Content-Length, and no browser can
// recover from a stalled media element without JavaScript.
func (r *Relay) handleWavListener(w http.ResponseWriter, req *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)

		return
	}

	listener := r.stream.Attach(req.RemoteAddr, queueDepth)
	defer r.stream.Detach(listener)

	ctx := req.Context()

	first, ok := waitForPacket(ctx, listener, r.sourceTimeout)
	if !ok {
		http.Error(w, "no source is publishing", http.StatusServiceUnavailable)

		return
	}

	r.log.Info("listener attached", "remote", listener.Name(), "transport", "wav")
	defer func() {
		r.log.Info("listener detached", "remote", listener.Name(), "dropped", listener.Dropped())
	}()

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(audio.BuildLiveWavHeader(first.Format.SampleRate, first.Format.Channels)); err != nil {
		return
	}

	r.pumpWav(ctx, w, flusher, listener, first)
}

func (r *Relay) pumpWav(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	listener *stream.Listener,
	first stream.Packet,
) {
	format := first.Format
	packet := first

	for {
		// A RIFF header cannot be amended mid-file, so a source that
		// reconnects with a different format ends this response.
		if packet.Format != format {
			return
		}

		if _, err := w.Write(audio.EncodePcm16(audio.DecodeMulaw(packet.Frame))); err != nil {
			return
		}

		flusher.Flush()

		select {
		case <-ctx.Done():
			return
		case next, ok := <-listener.Packets():
			if !ok {
				return
			}

			packet = next
		}
	}
}

// waitForPacket blocks for the first frame so a WAV header can describe the
// audio that follows. It reports false when no source publishes in time.
func waitForPacket(ctx context.Context, listener *stream.Listener, timeout time.Duration) (stream.Packet, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	select {
	case <-ctx.Done():
		return stream.Packet{}, false
	case <-deadline.C:
		return stream.Packet{}, false
	case packet, ok := <-listener.Packets():
		return packet, ok
	}
}

func (r *Relay) reportDrops(listener *stream.Listener, reported uint64) uint64 {
	current := listener.Dropped()
	if current == reported {
		return reported
	}

	r.log.Warn("listener is falling behind",
		"remote", listener.Name(),
		"dropped", current,
		"since", current-reported,
		"interval", dropReportInterval)

	return current
}

func writeJsonMessage(ctx context.Context, conn *websocket.Conn, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return writeMessage(ctx, conn, websocket.MessageText, encoded)
}

func writeMessage(
	ctx context.Context,
	conn *websocket.Conn,
	messageType websocket.MessageType,
	payload []byte,
) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()

	return conn.Write(ctx, messageType, payload)
}
