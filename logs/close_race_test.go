package logs

import (
	"context"
	"sync"
	"testing"
)

// A PutLogEvents in flight when the server closes must not panic.
//
// The fan-out used to `close(f.in)` in close() and guard enqueue with a
// `closed` flag — but enqueue released the mutex before its send, and close()
// released it before closing, so the check and the send were never ordered
// against each other. The file's own comment asserted the ordering that would
// have made it safe; nothing enforced it.
//
// It is worse than a 500. This handler is reached over the in-process peer
// path, and peers.handlerTransport calls ServeHTTP directly with no recover —
// unlike net/http — so the panic unwinds into whichever goroutine made the
// peer call and gets attributed to that instead.
//
// The fix is structural: f.in is never closed. Nothing here can detect "sent
// on a closed channel" by inspection, so the assertion is simply that a few
// thousand interleavings do not panic, under -race.
func TestEnqueueDuringCloseDoesNotPanic(t *testing.T) {
	for range 200 {
		f := newFanout(nil, nil, func(string, ...any) {})

		var wg sync.WaitGroup
		start := make(chan struct{})

		// Writers, standing in for handlers still in flight.
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for range 50 {
					f.enqueue(context.Background(), "g", "s", []Stored{{Msg: "x"}})
				}
			}()
		}
		// The closer, racing them.
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f.close()
		}()

		close(start)
		wg.Wait()

		// And close is idempotent: the service packages are exported for direct
		// embedding, so `defer svc.Close()` plus an error path must not be a
		// second close of the same channel.
		f.close()
	}
}

// The same shape in the metric-filter emitter, which was a copy of the
// fan-out's and carried the same defect.
func TestMetricEmitterEnqueueDuringCloseDoesNotPanic(t *testing.T) {
	for range 200 {
		m := newMetricEmitter(nil, nil, func(string, ...any) {})

		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for range 50 {
					m.enqueue(context.Background(), "g", []Stored{{Msg: "x"}})
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			m.close()
		}()

		close(start)
		wg.Wait()
		m.close()
	}
}
