package listener

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync/atomic"

	"sonic-bridge/server/internal/hub"
)

// Source accepts a single TCP producer connection at a time on Addr, reads
// fixed-size PCM chunks, and broadcasts each chunk through Hub. If a second
// producer connects while one is active, it is logged and closed immediately.
type Source struct {
	Addr      string
	Hub       *hub.Hub
	ChunkBytes int
	Label     string

	active atomic.Bool
}

func (s *Source) Serve(ctx context.Context) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", s.Addr)
	if err != nil {
		return err
	}
	log.Printf("listener[%s] listening on %s", s.Label, s.Addr)

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
			log.Printf("listener[%s] accept error: %v", s.Label, err)
			continue
		}

		if !s.active.CompareAndSwap(false, true) {
			log.Printf("listener[%s] rejecting %s: source already active", s.Label, conn.RemoteAddr())
			_ = conn.Close()
			continue
		}

		go s.serveConn(ctx, conn)
	}
}

func (s *Source) serveConn(ctx context.Context, conn net.Conn) {
	remote := conn.RemoteAddr().String()
	log.Printf("listener[%s] source connected: %s", s.Label, remote)

	defer func() {
		_ = conn.Close()
		s.active.Store(false)
		log.Printf("listener[%s] source disconnected: %s", s.Label, remote)
	}()

	// Close the connection if the context is cancelled to unblock the read.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	for {
		buf := make([]byte, s.ChunkBytes)
		if _, err := io.ReadFull(conn, buf); err != nil {
			if err != io.EOF && !errors.Is(err, net.ErrClosed) {
				log.Printf("listener[%s] read error from %s: %v", s.Label, remote, err)
			}
			return
		}
		s.Hub.Broadcast(buf)
	}
}
