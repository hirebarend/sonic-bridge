package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"sonic-bridge/internal/audio"
)

// serveTcpSource accepts sources. This is the relay's only ingest transport:
// the ESP32 firmware has no TLS or WebSocket stack, and the console speaks the
// same wire so the two can be compared directly. A connection sends one binary
// format header, then legacy frames or SB02 audio/quiet records.
func (r *Relay) serveTcpSource(ctx context.Context) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", r.config.TcpSourceAddr)
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	r.log.Info("tcp source listening", "addr", r.config.TcpSourceAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			if errors.Is(err, net.ErrClosed) {
				return nil
			}

			r.log.Warn("tcp accept failed", "error", err)

			continue
		}

		go r.readTcpSource(ctx, conn)
	}
}

func (r *Relay) readTcpSource(ctx context.Context, conn net.Conn) {
	remote := conn.RemoteAddr().String()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	header := make([]byte, audio.HeaderBytes)
	_ = conn.SetReadDeadline(time.Now().Add(r.sourceTimeout))

	if _, err := io.ReadFull(conn, header); err != nil {
		r.log.Warn("tcp source sent no usable header", "remote", remote, "error", err)

		return
	}

	format, err := audio.ParseHeader(header)
	if err != nil {
		r.log.Warn("tcp source rejected", "remote", remote, "error", err)

		return
	}

	sparse := string(header[:4]) == "SB02"
	acquire := r.stream.AcquireSource
	if sparse {
		acquire = r.stream.AcquireSparseSource
	}
	if !acquire(format) {
		r.log.Warn("tcp source rejected, the stream already has a source", "remote", remote)

		return
	}
	defer r.stream.ReleaseSource()

	r.log.Info("tcp source connected", "remote", remote, "format", format.String())

	r.ingressControlBytes.Add(audio.HeaderBytes)
	frames := r.readTcpFrames(conn, format, remote, sparse)

	r.log.Info("tcp source disconnected", "remote", remote, "frames", frames)
}

func (r *Relay) readTcpFrames(conn net.Conn, format audio.Format, remote string, sparse bool) uint64 {
	frameBytes := format.FrameBytes()

	var frames, nextPosition uint64
	for {
		_ = conn.SetReadDeadline(time.Now().Add(r.sourceTimeout))
		var record audio.Record
		var err error
		if sparse {
			record, err = audio.ReadRecord(conn, format)
			if err == nil && record.Position < nextPosition {
				err = fmt.Errorf("sample position moved backwards")
			}
		} else {
			record.Frame = make([]byte, frameBytes)
			_, err = io.ReadFull(conn, record.Frame)
		}
		if err != nil {
			if !isExpectedDisconnect(err) {
				r.log.Warn("tcp source read failed", "remote", remote, "error", err)
			}
			return frames
		}
		if sparse {
			r.ingressControlBytes.Add(audio.RecordHeaderBytes)
			nextPosition = record.Position + uint64(format.FrameSamples)
			r.stream.PublishRecord(record)
		} else {
			r.stream.Publish(record.Frame)
		}
		r.ingressAudioBytes.Add(uint64(len(record.Frame)))
		if !record.Quiet() {
			frames++
		}
	}

}

// isExpectedDisconnect reports whether an error is an ordinary end of stream
// rather than a fault worth logging. io.ErrUnexpectedEOF belongs here because
// a source that drops mid-frame is normal: an ESP32 losing WiFi does exactly
// that, and logging it as a read failure buries the real errors.
func isExpectedDisconnect(err error) bool {
	if err == nil {
		return false
	}

	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.Canceled)
}
