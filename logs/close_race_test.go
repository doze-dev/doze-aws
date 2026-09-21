package logs

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
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
					f.enqueue(context.Background(), "g", "s", []storedEvent{{Msg: "x"}})
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
					m.enqueue(context.Background(), "g", []storedEvent{{Msg: "x"}})
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

// A panicking emitter must still close its shipper.
//
// die() sets `closed` so enqueue starts reporting drops — and close() used to
// read that same flag to decide whether it had already run. So after a
// contained panic the flag was true, close() returned at its first line, and
// the metricship worker behind it was never told to stop. It sat on its select
// for the life of the process, holding the peers directory with it.
//
// The flag answers "should enqueue accept more?". Whether close has run is a
// different question, and conflating them cost a goroutine per panicking
// emitter. The fan-out next door has always used a sync.Once; this is the copy
// that drifted.
func TestAPanickingEmitterStillClosesItsShipper(t *testing.T) {
	m := newMetricEmitter(nil, nil, func(string, ...any) {})

	// A nil store makes evaluate panic, which bg.Recover contains and which
	// runs die() — the state the bug needed.
	m.enqueue(context.Background(), "g", []storedEvent{{Msg: "x"}})

	// Wait for the worker to have died, so the close below is the one that has
	// to cope with it rather than a race against it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		dead := m.dead
		m.mu.Unlock()
		if dead {
			break
		}
		time.Sleep(time.Millisecond)
	}
	m.mu.Lock()
	dead := m.dead
	m.mu.Unlock()
	if !dead {
		t.Skip("the emitter did not panic, so there is nothing to check here")
	}

	before := runtime.NumGoroutine()
	m.close()
	// close() must have reached m.ship.Close(), which stops the shipper's
	// worker. Give it a moment to unwind, then require the count to have gone
	// DOWN rather than stayed level.
	settled := false
	for range 200 {
		if runtime.NumGoroutine() < before {
			settled = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !settled {
		t.Errorf("closing a dead emitter freed no goroutine (%d before, %d after) — "+
			"its shipper was never closed", before, runtime.NumGoroutine())
	}
}
