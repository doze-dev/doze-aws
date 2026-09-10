package logship

// The shutdown race, which this package had and metricship — its near-twin —
// did not, because metricship got a test and this did not.
//
// Flush runs on a timer goroutine, so it can be anywhere in its body when a
// service closes, including between releasing the lock and sending the batch
// on. Closing the work channel in Close leaves that window a panic: "send on
// closed channel", from a goroutine with no connection to whatever triggered
// the shutdown.

import (
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/peers"
)

func TestFlushRacingCloseDoesNotPanic(t *testing.T) {
	// peers.None() has no logs service, so shipping fails with ErrNoLogs —
	// the shape a stack without the logs service has, and it exercises the
	// shipper's own machinery rather than a service's.
	for range 50 {
		s := New("test", peers.None(), func(string, ...any) {})
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 20 {
					s.Put("/g", "s", Event{Message: "line"})
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
	// A producer writing a line on a shutdown path — a Step Functions
	// execution finishing as the stack closes — must not panic or block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Put("/g", "s", Event{Message: "late"})
		s.Flush()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Put or Flush blocked after Close")
	}
}
