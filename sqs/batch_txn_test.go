package sqs

import (
	"strings"
	"testing"
)

// The batch operations run in ONE transaction now, for one fsync instead of
// ten. That introduces a failure mode the per-message loop could not have:
// if a bad entry aborted the transaction, the good entries would be rolled
// back with it — and SendMessageBatch's contract is per entry, so nine valid
// messages must survive one oversized one.
//
// This is the test that would catch that, and it asserts on the QUEUE rather
// than on the returned result: a result saying "successful" while the
// transaction rolled back is exactly the bug.
func TestABadEntryDoesNotSinkTheBatch(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("orders", map[string]string{"MaximumMessageSize": "1024"}, nil); err != nil {
		t.Fatal(err)
	}

	items := make([]SendItem, 0, 10)
	for i := range 9 {
		items = append(items, SendItem{Body: "ok-" + string(rune('a'+i)), Delay: -1})
	}
	// One entry over the queue's MaximumMessageSize.
	items = append(items, SendItem{Body: strings.Repeat("x", 2048), Delay: -1})

	res, err := s.SendBatch("orders", items)
	if err != nil {
		t.Fatalf("the batch failed as a whole: %v", err)
	}
	if len(res) != 10 {
		t.Fatalf("got %d results, want 10", len(res))
	}
	for i := range 9 {
		if res[i].Err != nil {
			t.Errorf("entry %d failed: %v", i, res[i].Err)
		}
	}
	if res[9].Err == nil {
		t.Error("the oversized entry was accepted")
	}

	// The nine are really there: the transaction committed rather than rolling
	// back around the failure.
	msgs, err := s.Peek("orders", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 9 {
		t.Errorf("queue holds %d messages, want the 9 that succeeded", len(msgs))
	}
}

// A queue-level failure is different from an entry-level one: nothing is
// written and the request fails, which is what AWS does with a bad QueueUrl.
func TestAMissingQueueFailsTheWholeBatch(t *testing.T) {
	s := testStore(t)
	_, err := s.SendBatch("nope", []SendItem{{Body: "a", Delay: -1}})
	if err == nil {
		t.Fatal("sending to a missing queue succeeded")
	}
}

// Delete is idempotent on AWS, so a handle naming nothing is a success — but a
// malformed one is still an error, and neither may disturb the others.
func TestDeleteBatchReportsPerEntry(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("orders", nil, nil); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := s.Send("orders", "body", nil, -1, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Receive("orders", 3, 0, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("received %d, want 3", len(got))
	}

	errs, err := s.DeleteBatch("orders", []string{got[0].Handle(), "not-a-handle", got[1].Handle()})
	if err != nil {
		t.Fatal(err)
	}
	if errs[0] != nil || errs[2] != nil {
		t.Errorf("valid handles failed: %v, %v", errs[0], errs[2])
	}
	if errs[1] == nil {
		t.Error("a malformed handle was accepted")
	}

	// Two gone, one still in flight. Peek would report zero here whatever
	// happened — it returns only VISIBLE messages, and all three are inflight
	// after the Receive above — so the count comes from the attributes, which
	// include the invisible ones.
	attrs, err := s.Attributes("orders")
	if err != nil {
		t.Fatal(err)
	}
	if got := attrs["ApproximateNumberOfMessagesNotVisible"]; got != "1" {
		t.Errorf("inflight = %s, want 1 after deleting 2 of 3", got)
	}
}
