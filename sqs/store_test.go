package sqs

import (
	"testing"
	"time"
)

func TestPeekShowsAllFifoMessagesReadOnly(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("orders.fifo", map[string]string{"FifoQueue": "true"}, nil); err != nil {
		t.Fatal(err)
	}
	// Two messages in one group, one in another — a plain Receive would only return
	// the head of each group (2), but Peek must show all 3.
	for _, m := range []struct{ body, group, dedup string }{
		{"a1", "g1", "d1"}, {"a2", "g1", "d2"}, {"b1", "g2", "d3"},
	} {
		if _, err := s.Send("orders.fifo", m.body, nil, -1, m.group, m.dedup); err != nil {
			t.Fatal(err)
		}
	}
	// Peek shows the whole queue in order — all 3, not just the 2 group heads a
	// plain Receive would release.
	peeked, err := s.Peek("orders.fifo", 100)
	if err != nil || len(peeked) != 3 {
		t.Fatalf("Peek: err=%v n=%d, want 3", err, len(peeked))
	}
	if peeked[0].Body != "a1" || peeked[1].Body != "a2" || peeked[2].Body != "b1" {
		t.Fatalf("Peek order = %q,%q,%q", peeked[0].Body, peeked[1].Body, peeked[2].Body)
	}
	// Peek is read-only: repeated peeks never bump the receive count.
	_, _ = s.Peek("orders.fifo", 100)
	again, _ := s.Peek("orders.fifo", 100)
	for _, m := range again {
		if m.ReceiveCount != 0 {
			t.Fatalf("Peek bumped receive count for %q: %d, want 0", m.Body, m.ReceiveCount)
		}
	}
	// Contrast: a real Receive only releases the group heads (2).
	if got, _ := s.Receive("orders.fifo", 10, 0, -1); len(got) != 2 {
		t.Fatalf("Receive returned %d FIFO heads, want 2", len(got))
	}
}

func TestSendReceiveDeleteVisibility(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", map[string]string{"VisibilityTimeout": "1"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q", "hello", nil, -1, "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Receive("q", 1, 0, -1)
	if err != nil || len(got) != 1 || got[0].Body != "hello" {
		t.Fatalf("receive: %v %+v", err, got)
	}
	// Immediately re-receiving returns nothing (in flight).
	if again, _ := s.Receive("q", 1, 0, -1); len(again) != 0 {
		t.Fatalf("expected nothing in flight, got %d", len(again))
	}
	// After visibility timeout it reappears.
	time.Sleep(1100 * time.Millisecond)
	if back, _ := s.Receive("q", 1, 0, -1); len(back) != 1 {
		t.Fatalf("expected message to reappear, got %d", len(back))
	} else if back[0].ReceiveCount != 2 {
		t.Fatalf("ReceiveCount = %d, want 2", back[0].ReceiveCount)
	}
	// Delete removes it for good.
	if err := s.Delete("q", got[0].Handle()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if final, _ := s.Receive("q", 1, 0, -1); len(final) != 0 {
		t.Fatalf("expected deleted, got %d", len(final))
	}
}

func TestFIFOOrderingAndGroupLock(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q.fifo", map[string]string{"FifoQueue": "true"}, nil); err != nil {
		t.Fatal(err)
	}
	// Two groups; messages interleaved on send.
	send := func(group, body, dedup string) {
		if _, err := s.Send("q.fifo", body, nil, -1, group, dedup); err != nil {
			t.Fatalf("send %s: %v", body, err)
		}
	}
	send("A", "a1", "1")
	send("B", "b1", "2")
	send("A", "a2", "3")

	// One receive: should get the head of each available group (a1 and b1), not a2.
	got, _ := s.Receive("q.fifo", 10, 0, -1)
	bodies := map[string]bool{}
	for _, m := range got {
		bodies[m.Body] = true
	}
	if !bodies["a1"] || !bodies["b1"] || bodies["a2"] {
		t.Fatalf("FIFO group head delivery wrong: %v", bodies)
	}
	// Group A is locked (a1 in flight) → a2 not deliverable yet.
	if more, _ := s.Receive("q.fifo", 10, 0, -1); len(more) != 0 {
		t.Fatalf("groups should be locked, got %d", len(more))
	}
}

func TestFIFODedup(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("d.fifo", map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "true"}, nil); err != nil {
		t.Fatal(err)
	}
	// Same body twice within the window → only one enqueued (content-based dedup).
	if _, err := s.Send("d.fifo", "same", nil, -1, "g", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("d.fifo", "same", nil, -1, "g", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Receive("d.fifo", 10, 0, -1)
	if len(got) != 1 {
		t.Fatalf("dedup failed: got %d messages, want 1", len(got))
	}
}

func TestDLQRedrive(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("dlq", nil, nil); err != nil {
		t.Fatal(err)
	}
	rp := `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:dlq","maxReceiveCount":"2"}`
	if _, err := s.CreateQueue("main", map[string]string{"VisibilityTimeout": "0", "RedrivePolicy": rp}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("main", "poison", nil, -1, "", ""); err != nil {
		t.Fatal(err)
	}
	// Receive it maxReceiveCount times (visibility 0 → immediately receivable).
	for i := 0; i < 2; i++ {
		if got, _ := s.Receive("main", 1, 0, -1); len(got) != 1 {
			t.Fatalf("receive %d: expected message, got %d", i, len(got))
		}
	}
	// Next receive on main should move it to the DLQ, returning nothing from main.
	if got, _ := s.Receive("main", 1, 0, -1); len(got) != 0 {
		t.Fatalf("expected redrive (empty main), got %d", len(got))
	}
	if dl, _ := s.Receive("dlq", 1, 0, -1); len(dl) != 1 || dl[0].Body != "poison" {
		t.Fatalf("expected poison in DLQ, got %+v", dl)
	}
}
