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

	r.pumpWebSocket(conn.CloseRead(req.Context()), conn, listener, req.URL.Query().Get("v") == "2")
}

func (r *Relay) pumpWebSocket(ctx context.Context, conn *websocket.Conn, listener *stream.Listener, sparseSupported bool) {
	dropReports := time.NewTicker(dropReportInterval)
	defer dropReports.Stop()

	var announced audio.Format
	var epoch uint64
	var ticks int
	var reportedDrops uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-dropReports.C:
			reportedDrops = r.reportDrops(listener, reportedDrops)
			ticks++
			if ticks%4 == 0 {
				// Waiting for a pong must not stall audio by one network RTT.
				// A failed Ping closes the connection and cancels CloseRead.
				go func() {
					pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
					defer cancel()
					_ = conn.Ping(pingCtx)
				}()
			}
		case packet, ok := <-listener.Packets():
			if !ok {
				return
			}

			if packet.Sparse && !sparseSupported {
				_ = conn.Close(websocket.StatusPolicyViolation, "Reload the player for the updated audio protocol")
				return
			}
			if packet.State == "waiting" {
				if err := r.writeControl(ctx, conn, map[string]any{"state": "waiting"}); err != nil {
					return
				}
				announced = audio.Format{}
				continue
			}
			if packet.Format != announced || packet.Epoch != epoch {
				announcement := struct {
					audio.Format
					Epoch    uint64 `json:"epoch"`
					Sparse   bool   `json:"sparse"`
					State    string `json:"state"`
					Position uint64 `json:"position"`
				}{packet.Format, packet.Epoch, packet.Sparse, packet.State, packet.Position}
				if err := r.writeControl(ctx, conn, announcement); err != nil {
					return
				}
				announced, epoch = packet.Format, packet.Epoch
			}
			if packet.State == "live" && len(packet.Frame) == 0 {
				continue
			}
			payload := packet.Frame
			if packet.Sparse {
				payload = (audio.Record{Position: packet.Position, Frame: packet.Frame}).Bytes()
			}
			if err := writeMessage(ctx, conn, websocket.MessageBinary, payload); err != nil {
				return
			}
			r.egressAudioBytes.Add(uint64(len(packet.Frame)))
			r.egressControlBytes.Add(uint64(len(payload) - len(packet.Frame)))
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

	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeTimeout))
	n, err := w.Write(audio.BuildLiveWavHeader(first.Format.SampleRate, first.Format.Channels))
	r.wavBytes.Add(uint64(n))
	if err != nil {
		return
	}

	r.pumpWav(ctx, w, flusher, listener, first)
}

// waitForPacket blocks for the first frame so a WAV header can describe the
// audio that follows. It reports false when no source publishes in time.
func waitForPacket(ctx context.Context, listener *stream.Listener, timeout time.Duration) (stream.Packet, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return stream.Packet{}, false
		case <-deadline.C:
			return stream.Packet{}, false
		case packet, ok := <-listener.Packets():
			if !ok {
				return stream.Packet{}, false
			}
			if packet.State == "waiting" || (packet.State == "live" && len(packet.Frame) == 0) {
				continue
			}
			return packet, true
		}
	}
}

// WAV uses a local clock only while the source explicitly declares quiet.
// Heartbeats do not append five seconds of zeros or restart the playback clock.
func (r *Relay) pumpWav(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, listener *stream.Listener, first stream.Packet) {
	ticker := time.NewTicker(first.Format.FrameDuration())
	ticker.Stop()
	defer ticker.Stop()
	var silenceTicks <-chan time.Time
	zeros := make([]byte, first.Format.FrameSamples*2)
	packet := first
	quiet := false
	write := func(payload []byte) bool {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeTimeout))
		n, err := w.Write(payload)
		r.wavBytes.Add(uint64(n))
		if err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	for {
		if packet.Epoch != first.Epoch || packet.Format != first.Format || packet.State == "waiting" {
			return
		}
		if packet.State == "quiet" {
			if !quiet {
				ticker.Reset(first.Format.FrameDuration())
				silenceTicks = ticker.C
			}
			quiet = true
		} else if len(packet.Frame) > 0 {
			quiet = false
			ticker.Stop()
			silenceTicks = nil
			if !write(audio.EncodePcm16(audio.DecodeMulaw(packet.Frame))) {
				return
			}
		}
		// Stay here across silence ticks, so an audio frame is never written twice.
		for {
			select {
			case <-ctx.Done():
				return
			case <-silenceTicks:
				if quiet && !write(zeros) {
					return
				}
				continue
			case next, ok := <-listener.Packets():
				if !ok {
					return
				}
				packet = next
			}
			break
		}
	}
}

func (r *Relay) writeControl(ctx context.Context, conn *websocket.Conn, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err = writeMessage(ctx, conn, websocket.MessageText, encoded); err == nil {
		r.egressControlBytes.Add(uint64(len(encoded)))
	}
	return err
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
