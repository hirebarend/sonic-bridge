// Package stream fans one encoded audio stream out to many destinations. A
// stream has at most one live source at a time and any number of listeners,
// each with a bounded queue that drops its oldest frame rather than stalling
// the source.
package stream

import (
	"sync"
	"sync/atomic"

	"sonic-bridge/internal/audio"
	"sonic-bridge/internal/queue"
)

// Packet is one unit of delivery. Every packet carries the format its frame
// was encoded with, so a listener can never decode a frame with a stale
// format after a source reconnects with different settings.
type Packet struct {
	Format audio.Format
	Frame  []byte
}

// Listener is one attached destination. Frames arrive on Packets until the
// listener is detached, which closes the channel.
type Listener struct {
	name    string
	packets chan Packet
	dropped atomic.Uint64
}

// Name identifies the listener in logs and stats, normally its remote address.
func (l *Listener) Name() string { return l.name }

// Packets is the listener's delivery queue.
func (l *Listener) Packets() <-chan Packet { return l.packets }

// Dropped counts frames discarded because this listener could not keep up. It
// is the canonical signal that a destination is too slow for the stream.
func (l *Listener) Dropped() uint64 { return l.dropped.Load() }

// Stream is the relay's single audio stream. It is safe for concurrent use.
type Stream struct {
	mu        sync.RWMutex
	source    *audio.Format
	listeners map[*Listener]struct{}

	published atomic.Uint64
	dropped   atomic.Uint64
}

// New returns an idle stream with no source and no listeners.
func New() *Stream {
	return &Stream{listeners: make(map[*Listener]struct{})}
}

// AcquireSource claims the stream for one producer. It returns false when
// another producer already holds it, which is how a second source is turned
// away instead of interleaving its audio with the first.
func (s *Stream) AcquireSource(format audio.Format) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.source != nil {
		return false
	}

	s.source = &format

	return true
}

// ReleaseSource gives up the stream so another producer can claim it.
func (s *Stream) ReleaseSource() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.source = nil
}

// FindFormat returns the format the live source declared, or nil when no
// source holds the stream.
func (s *Stream) FindFormat() *audio.Format {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.source == nil {
		return nil
	}

	format := *s.source

	return &format
}

// Publish fans one encoded frame out to every attached listener. It does
// nothing when no source holds the stream. The frame is shared by reference
// with every listener, so the caller must not reuse the slice.
func (s *Stream) Publish(frame []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.source == nil {
		return
	}

	s.published.Add(1)
	packet := Packet{Format: *s.source, Frame: frame}

	for listener := range s.listeners {
		s.deliver(listener, packet)
	}
}

// Attach registers a destination and returns its listener. The caller must
// Detach when it stops reading.
func (s *Stream) Attach(name string, queueDepth int) *Listener {
	listener := &Listener{name: name, packets: make(chan Packet, queueDepth)}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.listeners[listener] = struct{}{}

	return listener
}

// Detach removes a destination and closes its queue. Calling it twice for the
// same listener is safe.
func (s *Stream) Detach(listener *Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.listeners[listener]; !ok {
		return
	}

	delete(s.listeners, listener)
	close(listener.packets)
}

// ListenerCount is the number of attached destinations.
func (s *Stream) ListenerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.listeners)
}

// Published counts frames accepted from the source.
func (s *Stream) Published() uint64 { return s.published.Load() }

// Dropped counts frames discarded across all listeners.
func (s *Stream) Dropped() uint64 { return s.dropped.Load() }

// deliver enqueues one packet without blocking, so one stalled destination can
// never delay the source or the other destinations. The caller must hold at
// least a read lock, which is what keeps Detach from closing the channel
// underneath the send.
func (s *Stream) deliver(listener *Listener, packet Packet) {
	if queue.Offer(listener.packets, packet) {
		return
	}

	listener.dropped.Add(1)
	s.dropped.Add(1)
}
