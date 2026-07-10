package sqs

import (
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func bucketCount(s *Store, bucket []byte) int {
	n := 0
	_ = s.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(bucket); b != nil {
			_ = b.ForEach(func(_, _ []byte) error { n++; return nil })
		}
		return nil
	})
	return n
}

// TestDedupGCAndExpiry verifies the FIFO dedup bucket doesn't grow unbounded and
// that dedup stops applying past the window.
func TestDedupGCAndExpiry(t *testing.T) {
	s := testStore(t)
	base := time.Now()
	now := base
	s.clock = func() time.Time { return now }

	if _, err := s.CreateQueue("d.fifo", map[string]string{"FifoQueue": "true"}, nil); err != nil {
		t.Fatal(err)
	}
	// Distinct dedup ids; the first is well inside the window.
	if _, err := s.Send("d.fifo", "a", nil, -1, "g1", "d1"); err != nil {
		t.Fatal(err)
	}
	// Past the dedup window: a new send GCs the stale d1 entry.
	now = base.Add(time.Duration(dedupWindow+10) * time.Second)
	if _, err := s.Send("d.fifo", "b", nil, -1, "g2", "d2"); err != nil {
		t.Fatal(err)
	}
	if c := bucketCount(s, dedupBucket("d.fifo")); c != 1 {
		t.Fatalf("dedup bucket = %d entries, want 1 (d1 should be GC'd)", c)
	}
	// Re-sending d1's id now (window elapsed) must enqueue, not dedup.
	if _, err := s.Send("d.fifo", "a", nil, -1, "g1", "d1"); err != nil {
		t.Fatal(err)
	}
	if c := bucketCount(s, msgBucket("d.fifo")); c != 3 {
		t.Fatalf("messages = %d, want 3 (dedup should have expired)", c)
	}
}

// TestRetentionSweep verifies the janitor's Sweep reclaims expired messages from
// a write-only queue (one that's never received from).
func TestRetentionSweep(t *testing.T) {
	s := testStore(t)
	base := time.Now()
	now := base
	s.clock = func() time.Time { return now }

	if _, err := s.CreateQueue("q", map[string]string{"MessageRetentionPeriod": "1"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q", "old", nil, -1, "", ""); err != nil {
		t.Fatal(err)
	}
	now = base.Add(2 * time.Second) // past retention
	s.Sweep()
	if c := bucketCount(s, msgBucket("q")); c != 0 {
		t.Fatalf("expected expired message swept, %d remain", c)
	}
}

// TestLongPollWakesPromptly verifies a long-poll receive returns ~immediately
// when a message is sent, rather than after the full wait — i.e. the notifier
// fires instead of a fixed poll interval.
func TestLongPollWakesPromptly(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", nil, nil); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = s.Send("q", "late", nil, -1, "", "")
	}()
	start := time.Now()
	got, err := s.Receive("q", 1, 10, -1) // 10s long-poll
	elapsed := time.Since(start)
	if err != nil || len(got) != 1 {
		t.Fatalf("receive: %v, %d msgs", err, len(got))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("long-poll did not wake promptly: %v (notifier not firing?)", elapsed)
	}
}
