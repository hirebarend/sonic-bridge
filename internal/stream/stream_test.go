package stream

import (
	"sync"
	"testing"
	"time"

	"sonic-bridge/internal/audio"
)

func testFormat() audio.Format {
	return audio.Format{Codec: audio.CodecMulaw, SampleRate: 16000, Channels: 1, FrameSamples: 320}
}

func TestPublishReachesEveryListener(t *testing.T) {
	s := New()
	if !s.AcquireSource(testFormat()) {
		t.Fatal("AcquireSource on an idle stream must succeed")
	}

	first := s.Attach("first", 4)
	second := s.Attach("second", 4)
	defer s.Detach(first)
	defer s.Detach(second)

	s.Publish([]byte{1, 2, 3})

	for _, listener := range []*Listener{first, second} {
		select {
		case packet := <-listener.Packets():
			if len(packet.Frame) != 3 {
				t.Errorf("%s: frame is %d bytes, want 3", listener.Name(), len(packet.Frame))
			}

			if packet.Format != testFormat() {
				t.Errorf("%s: got format %+v, want %+v", listener.Name(), packet.Format, testFormat())
			}
		case <-time.After(time.Second):
			t.Errorf("%s: no packet delivered", listener.Name())
		}
	}
}

func TestPublishWithoutASourceIsIgnored(t *testing.T) {
	s := New()
	listener := s.Attach("listener", 4)
	defer s.Detach(listener)

	s.Publish([]byte{1})

	if got := len(listener.Packets()); got != 0 {
		t.Fatalf("queued %d packets with no source, want 0", got)
	}

	if got := s.Published(); got != 0 {
		t.Fatalf("Published = %d with no source, want 0", got)
	}
}

func TestSecondSourceIsTurnedAway(t *testing.T) {
	s := New()

	if !s.AcquireSource(testFormat()) {
		t.Fatal("first AcquireSource must succeed")
	}

	if s.AcquireSource(testFormat()) {
		t.Fatal("second AcquireSource must fail while the first holds the stream")
	}

	s.ReleaseSource()

	if !s.AcquireSource(testFormat()) {
		t.Fatal("AcquireSource must succeed again after ReleaseSource")
	}
}

// A destination that stops reading must lose its own oldest frames and must
// not hold up the source or any other destination.
func TestSlowListenerDropsOldestAndKeepsNewest(t *testing.T) {
	s := New()
	s.AcquireSource(testFormat())

	slow := s.Attach("slow", 2)
	defer s.Detach(slow)

	for i := range 10 {
		s.Publish([]byte{byte(i)})
	}

	if got := slow.Dropped(); got != 8 {
		t.Fatalf("Dropped = %d, want 8", got)
	}

	if got := len(slow.Packets()); got != 2 {
		t.Fatalf("queue holds %d packets, want 2", got)
	}

	first := <-slow.Packets()
	second := <-slow.Packets()

	if first.Frame[0] != 8 || second.Frame[0] != 9 {
		t.Fatalf("queue holds frames %d,%d, want the newest 8,9", first.Frame[0], second.Frame[0])
	}
}

func TestSlowListenerDoesNotStarveFastListener(t *testing.T) {
	s := New()
	s.AcquireSource(testFormat())

	slow := s.Attach("slow", 1)
	fast := s.Attach("fast", 64)
	defer s.Detach(slow)
	defer s.Detach(fast)

	for i := range 32 {
		s.Publish([]byte{byte(i)})
	}

	if got := fast.Dropped(); got != 0 {
		t.Fatalf("fast listener dropped %d frames, want 0", got)
	}

	if got := len(fast.Packets()); got != 32 {
		t.Fatalf("fast listener holds %d packets, want 32", got)
	}
}

func TestDetachClosesTheQueue(t *testing.T) {
	s := New()
	listener := s.Attach("listener", 1)

	s.Detach(listener)

	if _, ok := <-listener.Packets(); ok {
		t.Fatal("queue must be closed after Detach")
	}

	if got := s.ListenerCount(); got != 0 {
		t.Fatalf("ListenerCount = %d after Detach, want 0", got)
	}
}

func TestDetachTwiceIsSafe(t *testing.T) {
	s := New()
	listener := s.Attach("listener", 1)

	s.Detach(listener)
	s.Detach(listener)
}

func TestFindFormatReportsSourceLifecycle(t *testing.T) {
	s := New()

	if s.FindFormat() != nil {
		t.Fatal("FindFormat must be nil on an idle stream")
	}

	s.AcquireSource(testFormat())

	format := s.FindFormat()
	if format == nil || *format != testFormat() {
		t.Fatalf("FindFormat = %v, want %+v", format, testFormat())
	}

	s.ReleaseSource()

	if s.FindFormat() != nil {
		t.Fatal("FindFormat must be nil after ReleaseSource")
	}
}

// Publishing while listeners attach and detach must not race or panic on a
// closed channel. Run with -race.
func TestConcurrentPublishAttachDetach(t *testing.T) {
	s := New()
	s.AcquireSource(testFormat())

	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				s.Publish([]byte{1})
			}
		}
	}()

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				listener := s.Attach("churn", 2)
				<-time.After(time.Microsecond)
				s.Detach(listener)
			}
		}()
	}

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			listener := s.Attach("reader", 4)
			defer s.Detach(listener)
			for range 50 {
				select {
				case <-listener.Packets():
				case <-time.After(10 * time.Millisecond):
				}
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()
}

func TestSparseStateSurvivesDropsAndSourceReplacement(t *testing.T) {
	s := New()
	s.AcquireSparseSource(testFormat())
	l := s.Attach("slow", 1)
	defer s.Detach(l)
	first := <-l.Packets()
	s.PublishRecord(audio.Record{Position: 0, Frame: []byte{1}})
	s.PublishRecord(audio.Record{Position: 320})
	s.PublishRecord(audio.Record{Position: 80320})
	got := <-l.Packets()
	if got.State != "quiet" || got.Position != 80320 {
		t.Fatal("latest state lost", got)
	}
	s.ReleaseSource()
	s.AcquireSparseSource(testFormat())
	got = <-l.Packets()
	if got.Epoch == first.Epoch || got.State != "live" {
		t.Fatal("same-format replacement needs new identity")
	}
}
