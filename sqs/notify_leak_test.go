package sqs

// The long-poll wakeup map grows for every queue that is received from and then
// deleted.
//
// Receive registers interest BEFORE it checks the store, so a Send landing
// between the check and the wait cannot be missed. That registration is removed
// again in exactly two places: signal(), when a Send wakes the waiters, and
// forget(), on the branch where receiveOnce returned an error.
//
// A receive that SUCCEEDS takes neither path. It returns from inside the loop
// with its entry still in the map, which is harmless while the queue lives —
// the next Send signals it away — and permanent once the queue is deleted,
// because nothing can ever signal that name again. One stranded channel, one
// map entry and one string per queue, for the life of the process.
//
// notify.go's own comment already names this case: "a queue deleted out from
// under a waiter stranded one the same way". The fix that comment describes
// went into the receive path's error branch, and queue deletion itself was
// never wired up.
//
// # How it was found
//
// Not by reading. TestResourcesStayBoundedUnderRepeatedUse reported retention
// linear in work — +208 bytes a round on macOS at 150/300 rounds, ~210 in CI at
// 4,000/8,000 — over a workload whose live set is identical at both measurement
// points. Far too small to trip any sane band and far too consistent to be
// noise. Attributing it with runtime.MemProfile named two sites: the channel
// allocated in notifier.wait, and the growth of the map holding them.
//
// Which is the case this whole month of work was for. It is a few hundred bytes
// a round; nothing that lives 300ms could see it, and neither could a person.

import (
	"fmt"
	"testing"
)

// steadyStateRounds is a workload whose live set is the same at the end as at
// the beginning: create a queue, use it, delete it, under a name never used
// before. Anything the store still holds afterwards is something it had no
// reason to keep.
const steadyStateRounds = 50

func TestAReceivedThenDeletedQueueStrandsNoWakeupEntry(t *testing.T) {
	s := testStore(t)
	for i := 0; i < steadyStateRounds; i++ {
		name := fmt.Sprintf("bounded-%d", i)
		if _, err := s.CreateQueue(name, nil, nil); err != nil {
			t.Fatalf("round %d: CreateQueue: %v", i, err)
		}
		if _, err := s.Send(name, "m", nil, -1, "", "", nil); err != nil {
			t.Fatalf("round %d: Send: %v", i, err)
		}
		// The successful receive is the one that matters. waitSec 0 so it
		// returns the message rather than long-polling.
		msgs, err := s.Receive(name, 1, 0, -1)
		if err != nil {
			t.Fatalf("round %d: Receive: %v", i, err)
		}
		if len(msgs) != 1 {
			t.Fatalf("round %d: Receive returned %d messages, want 1", i, len(msgs))
		}
		if err := s.DeleteQueue(name); err != nil {
			t.Fatalf("round %d: DeleteQueue: %v", i, err)
		}
	}

	s.notify.mu.Lock()
	held := len(s.notify.chans)
	names := make([]string, 0, held)
	for q := range s.notify.chans {
		names = append(names, q)
	}
	s.notify.mu.Unlock()

	if held != 0 {
		if len(names) > 5 {
			names = names[:5]
		}
		t.Errorf("after %d create/receive/delete rounds the wakeup map holds %d "+
			"entries (e.g. %v) — every one names a queue that no longer exists, "+
			"so nothing will ever signal it away",
			steadyStateRounds, held, names)
	}
}

// The entry must survive a receive while the queue is still there: dropping it
// eagerly would cost a concurrent waiter its prompt wakeup, which is the whole
// reason wait() registers before checking.
func TestALiveQueueKeepsItsWakeupEntry(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("live", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("live", "m", nil, -1, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive("live", 1, 0, -1); err != nil {
		t.Fatal(err)
	}
	s.notify.mu.Lock()
	_, ok := s.notify.chans["live"]
	s.notify.mu.Unlock()
	if !ok {
		t.Error("the wakeup entry for a live queue was dropped; a Send racing a " +
			"receive can no longer wake it promptly")
	}
}
