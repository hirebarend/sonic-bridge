// Package listener implements the server's TCP listeners.
package listener

import (
	"context"
	"errors"
	"log"
	"net"
	"time"

	"sonic-bridge/server/internal/hub"
)

// Destination accepts TCP connections and pumps every chunk broadcast on the
// given hub to each connected client. Each accepted connection gets its own
// bounded hub queue with drop-oldest semantics.
type Destination struct {
	Addr        string
	Hub         *hub.Hub
	QueueDepth  int
	WriteTimeout time.Duration
	Label       string // used in log messages, e.g. "live" or "tone"
}

func (d *Destination) Serve(ctx context.Context) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", d.Addr)
	if err != nil {
		return err
	}
	log.Printf("listener[%s] listening on %s", d.Label, d.Addr)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			log.Printf("listener[%s] accept error: %v", d.Label, err)
			continue
		}
		go d.serveConn(ctx, conn)
	}
}

func (d *Destination) serveConn(ctx context.Context, conn net.Conn) {
	remote := conn.RemoteAddr().String()
	log.Printf("listener[%s] client connected: %s", d.Label, remote)

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	client := d.Hub.Register(remote, d.QueueDepth)
	defer func() {
		d.Hub.Unregister(client)
		_ = conn.Close()
		log.Printf("listener[%s] client disconnected: %s (drops=%d)", d.Label, remote, client.Drops())
	}()

	// Periodic drop-counter heartbeat. Drops are the canonical signal that a
	// destination can't keep up; logging them while the stream is live makes
	// jitter diagnosable instead of only being visible post-mortem.
	statTicker := time.NewTicker(5 * time.Second)
	defer statTicker.Stop()
	var lastDrops uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-statTicker.C:
			cur := client.Drops()
			if cur != lastDrops {
				log.Printf("listener[%s] %s drops=%d (+%d in last 5s)", d.Label, remote, cur, cur-lastDrops)
				lastDrops = cur
			}
		case chunk, ok := <-client.Recv():
			if !ok {
				return
			}
			if d.WriteTimeout > 0 {
				_ = conn.SetWriteDeadline(time.Now().Add(d.WriteTimeout))
			}
			if _, err := conn.Write(chunk); err != nil {
				log.Printf("listener[%s] write error to %s: %v", d.Label, remote, err)
				return
			}
		}
	}
}
