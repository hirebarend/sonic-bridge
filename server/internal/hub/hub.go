// Package hub is a single-producer, multi-consumer fan-out for fixed-size
// audio chunks. Each consumer has a bounded queue; on overflow the oldest
// chunk is discarded so the producer is never blocked.
package hub

import (
	"sync"
	"sync/atomic"
)

type Client struct {
	name  string
	ch    chan []byte
	drops uint64
}

func (c *Client) Recv() <-chan []byte { return c.ch }
func (c *Client) Name() string        { return c.name }
func (c *Client) Drops() uint64       { return atomic.LoadUint64(&c.drops) }

type Hub struct {
	mu      sync.Mutex
	clients map[*Client]struct{}
}

func New() *Hub {
	return &Hub{clients: make(map[*Client]struct{})}
}

func (h *Hub) Register(name string, queueDepth int) *Client {
	c := &Client{name: name, ch: make(chan []byte, queueDepth)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.ch)
	}
	h.mu.Unlock()
}

func (h *Hub) Broadcast(chunk []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		push(c, chunk)
	}
}

func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// push enqueues chunk into c.ch. If c.ch is full, the oldest chunk is
// discarded to make room. Caller must hold h.mu so that the channel cannot
// be closed concurrently by Unregister.
func push(c *Client, chunk []byte) {
	for {
		select {
		case c.ch <- chunk:
			return
		default:
			select {
			case <-c.ch:
				atomic.AddUint64(&c.drops, 1)
			default:
			}
		}
	}
}
