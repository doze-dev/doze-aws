package metricship

// The shutdown race is the whole of this file.
//
// Flush runs on a timer goroutine, so it can be anywhere in its body when a
// service closes — including between releasing the lock and sending the batch
// on. An earlier version closed the work channel in Close, and that window
// was a panic ("send on closed channel") that only showed under -race on a
// loaded machine, from a goroutine with no connection to the test that
// triggered it.

import (
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/peers"
)

func TestFlushRacingCloseDoesNotPanic(t *testing.T) {
	// peers.None() has no cloudwatch, so publishing fails with
	// ErrNoCloudWatch — which is the shape a stack without CloudWatch has,
	// and it exercises the shipper's own machinery rather than a service's.
	for range 50 {
		s := New("test", peers.None(), func(string, ...any) {})
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 20 {
					s.Count("Test", "Metric", map[string]string{"k": "v"}, 1)
					s.Flush()
				}
			}()
		}
		go s.Close()
		wg.Wait()
		s.Close() // idempotent, and the second call must not block
	}
}

func TestCloseIsIdempotentAndPutAfterCloseIsInert(t *testing.T) {
	s := New("test", peers.None(), func(string, ...any) {})
	s.Close()
	s.Close()
	// A producer that records a metric on a shutdown path — a Lambda whose
	// last invocation lands as the stack closes — must not panic or block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Count("Test", "Metric", nil, 1)
		s.Flush()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Put or Flush blocked after Close")
	}
}
