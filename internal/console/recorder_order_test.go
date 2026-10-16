package console

import (
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
)

// A call is numbered when it starts and recorded when it finishes, so a slow
// call (a Lambda invoke that takes 100 ms) is entered into the ring AFTER calls
// that started later and finished first. The inspector looks an entry up by its
// number; scanning newest-first and stopping at the first lower number lost the
// slow call ("Entry expired: this call has scrolled out of the 500-entry
// ring") a second after it was made.
func TestAnEntryRecordedOutOfOrderIsStillFound(t *testing.T) {
	rec := NewRecorder(nil, awsident.Default())
	now := time.Now()
	add := func(seq int64, action string) {
		rec.addAt(seq, TrafficEntry{At: now, Service: "lambda", Action: action, Status: 200})
	}
	add(1, "Early")
	add(3, "Fast") // started after the slow call, finished first
	add(4, "Faster")
	add(2, "Slow") // started second, recorded last
	for _, seq := range []int64{1, 2, 3, 4} {
		e, ok := rec.Get(seq)
		if !ok || e.Seq != seq {
			t.Errorf("Get(%d) = %v, %v: an entry that is in the ring was not found", seq, e.Action, ok)
		}
	}
	if _, ok := rec.Get(99); ok {
		t.Error("Get found an entry that was never recorded")
	}
}

// The same ordering and the polling read: asking for what is newer than a
// number must still return a slow call that finished after it was passed.
func TestEntriesSinceReturnsASlowCallRecordedLate(t *testing.T) {
	rec := NewRecorder(nil, awsident.Default())
	now := time.Now()
	rec.addAt(1, TrafficEntry{At: now, Service: "sqs", Action: "A", Status: 200})
	rec.addAt(3, TrafficEntry{At: now, Service: "sqs", Action: "C", Status: 200})
	rec.addAt(2, TrafficEntry{At: now, Service: "lambda", Action: "Slow", Status: 200}) // recorded last
	got := rec.Entries(1)
	seen := map[int64]bool{}
	for _, e := range got {
		seen[e.Seq] = true
	}
	if !seen[2] || !seen[3] || seen[1] {
		t.Errorf("Entries(1) = seqs %v, want 2 and 3 and not 1", seen)
	}
}

// The live wire asks "did the ring change?" before it renders. A call takes its
// number when it STARTS, so a hash taken from the number alone moves at the
// start of a slow call and never again when the call lands: a lone 3 s
// ReceiveMessage stayed invisible until some other call came along.
func TestTheRingsChangeMarkMovesWhenASlowCallLands(t *testing.T) {
	rec := NewRecorder(nil, awsident.Default())
	before := rec.Changes()
	seq := rec.reserve() // the slow call starts
	started := rec.Changes()
	rec.addAt(seq, TrafficEntry{At: time.Now(), Service: "sqs", Action: "ReceiveMessage", Status: 200})
	landed := rec.Changes()
	if landed == started {
		t.Fatalf("the change mark did not move when the call landed (before %d, started %d, landed %d)", before, started, landed)
	}
	rec.Clear()
	if rec.Changes() == landed {
		t.Error("the change mark did not move on Clear")
	}
}
