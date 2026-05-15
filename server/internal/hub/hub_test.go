package hub

import (
	"testing"
	"time"
)

func TestBroadcastDeliversToAllClients(t *testing.T) {
	h := New()
	a := h.Register("a", 4)
	b := h.Register("b", 4)
	defer h.Unregister(a)
	defer h.Unregister(b)

	h.Broadcast([]byte{1, 2, 3})

	for _, c := range []*Client{a, b} {
		select {
		case chunk := <-c.Recv():
			if len(chunk) != 3 {
				t.Fatalf("%s: got len %d", c.Name(), len(chunk))
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("%s: no chunk delivered", c.Name())
		}
	}
}

func TestSlowConsumerDropsOldestNotProducer(t *testing.T) {
	h := New()
	slow := h.Register("slow", 2)
	defer h.Unregister(slow)

	// Push more than the queue depth without reading.
	for i := 0; i < 10; i++ {
		h.Broadcast([]byte{byte(i)})
	}

	if got := slow.Drops(); got < 8 {
		t.Fatalf("expected at least 8 drops, got %d", got)
	}

	// Queue should still hold exactly queueDepth items, and they should be
	// the most recent ones.
	if got := len(slow.Recv()); got != 2 {
		t.Fatalf("expected queue len 2, got %d", got)
	}
	first := <-slow.Recv()
	second := <-slow.Recv()
	if first[0] != 8 || second[0] != 9 {
		t.Fatalf("expected newest chunks 8,9; got %d,%d", first[0], second[0])
	}
}

func TestUnregisterClosesChannel(t *testing.T) {
	h := New()
	c := h.Register("x", 1)
	h.Unregister(c)
	if _, ok := <-c.Recv(); ok {
		t.Fatal("channel should be closed after Unregister")
	}
	if h.Len() != 0 {
		t.Fatalf("expected 0 clients, got %d", h.Len())
	}
}
