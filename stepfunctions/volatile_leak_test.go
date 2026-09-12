package stepfunctions

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// held reports how many volatile executions are still resident.
func (v *volatile) held() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.execs)
}

// A StartSyncExecution whose client deadline expires at the moment the driver
// finalises used to leak the execution record and its entire history, for the
// life of the process.
//
// The handover is carried by waiters.release, which reports whether a waiter
// was still registered. finished() drops the record only when that is FALSE —
// "nobody is waiting, so nobody will read this". So when a waiter gives up, it
// owes the drop in exactly the case where the driver already released it.
//
// That case was not handled. Both <-ch and <-ctx.Done() become ready together
// in this race, Go picks a case at random, and picking ctx.Done() called
// release (now false, already gone), returned, and dropped nothing. A
// one-second client timeout against a ~one-second Express workflow hits it
// regularly.
func TestAGivenUpVolatileRunIsNotLeaked(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), &testClock{now: time.Now()})
	vol := srv.store.vol

	// The driver finished and released the waiter, leaving the record for
	// whoever was waiting to collect — then the caller gives up.
	const key = "machine:exec-raced"
	srv.store.vol.save(key, []byte(`{"Status":"SUCCEEDED"}`), nil)
	ch := srv.engine.waiters.add(key)
	srv.engine.waiters.release(key) // the driver's finished(), winning the race
	_ = ch

	if vol.held() != 1 {
		t.Fatalf("setup: want 1 volatile record, got %d", vol.held())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := srv.awaitVolatile(ctx, key, ch); ok {
		t.Error("a cancelled caller should not report success")
	}

	if n := vol.held(); n != 0 {
		t.Errorf("%d volatile record(s) left resident — this is the leak", n)
	}
}

// The other side of the handover: the caller gives up FIRST, so the driver has
// not released yet and must still be the one to drop. Nothing may be dropped
// early, because the run is still going.
func TestGivingUpFirstLeavesTheDropToTheDriver(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), &testClock{now: time.Now()})
	vol := srv.store.vol

	const key = "machine:exec-early"
	srv.store.vol.save(key, []byte(`{"Status":"RUNNING"}`), nil)
	ch := srv.engine.waiters.add(key)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv.awaitVolatile(ctx, key, ch) //nolint:errcheck // giving up is the point

	// Still there: the run has not finished, and dropping it now would delete a
	// live execution out from under the driver.
	if n := vol.held(); n != 1 {
		t.Fatalf("the record was dropped while the run was still going: held=%d", n)
	}

	// Now the driver finishes. No waiter remains, so it drops.
	r := &run{key: key, e: &Execution{Volatile: true}}
	srv.engine.finished(r)
	if n := vol.held(); n != 0 {
		t.Errorf("the driver did not drop a record nobody was waiting for: held=%d", n)
	}
}

// Many raced give-ups must not accumulate, which is the shape the leak
// actually took in use.
func TestRacedGiveUpsDoNotAccumulate(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), &testClock{now: time.Now()})
	vol := srv.store.vol

	for i := range 200 {
		key := fmt.Sprintf("machine:exec-%d", i)
		srv.store.vol.save(key, []byte(`{"Status":"SUCCEEDED"}`), nil)
		ch := srv.engine.waiters.add(key)
		srv.engine.waiters.release(key)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		srv.awaitVolatile(ctx, key, ch) //nolint:errcheck
	}
	if n := vol.held(); n != 0 {
		t.Errorf("%d of 200 raced executions stayed resident", n)
	}
}
