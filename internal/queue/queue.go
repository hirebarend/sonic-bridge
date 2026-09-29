// Package queue implements the project's backpressure policy: a bounded queue
// that discards its oldest item rather than blocking its producer.
//
// Every stage of sonic-bridge has a real-time producer that must not stall. An
// audio device callback that blocks overruns the driver's buffer and corrupts
// the capture; a relay that blocks on one slow destination stalls the source
// and every other destination. Losing the oldest frame is always cheaper than
// either, because the newest audio is the only audio anyone wants.
package queue

// Offer enqueues item, discarding the oldest item to make room when the queue
// is full. It never blocks. It reports false when an item was dropped, which
// is the caller's signal to count it.
func Offer[T any](queue chan T, item T) bool {
	select {
	case queue <- item:
		return true
	default:
	}

	select {
	case <-queue:
	default:
	}

	select {
	case queue <- item:
	default:
	}

	return false
}
