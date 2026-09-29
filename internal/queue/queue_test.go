package queue

import "testing"

func TestOfferReportsSuccessWhileThereIsRoom(t *testing.T) {
	q := make(chan int, 2)

	if !Offer(q, 1) || !Offer(q, 2) {
		t.Fatal("Offer must report success while the queue has room")
	}
}

func TestOfferDiscardsOldestWhenFull(t *testing.T) {
	q := make(chan int, 2)
	Offer(q, 1)
	Offer(q, 2)

	if Offer(q, 3) {
		t.Fatal("Offer must report a drop when the queue is full")
	}

	if got := len(q); got != 2 {
		t.Fatalf("queue holds %d items, want 2", got)
	}

	if first, second := <-q, <-q; first != 2 || second != 3 {
		t.Fatalf("queue holds %d,%d, want the newest 2,3", first, second)
	}
}

func TestOfferNeverBlocksOnAZeroCapacityQueue(t *testing.T) {
	if Offer(make(chan int), 1) {
		t.Fatal("an unbuffered queue with no receiver cannot accept an item")
	}
}
