package sqs

// The receive state machine: long-poll delivery, visibility timeout, FIFO
// group locking + dedup, DLQ redrive, and receipt-handle operations.

import (
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Receive returns up to max visible messages, applying visibility timeout, FIFO
// group locking, DLQ redrive, and retention. waitSec long-polls when empty.
func (s *Store) Receive(queue string, max, waitSec int, visibilityOverride int) ([]Message, error) {
	if max <= 0 || max > maxReceiveBatch {
		max = 1
	}
	if waitSec < 0 {
		waitSec = s.queueDefaultWait(queue) // not specified: use the queue's default
	}
	if waitSec > maxWaitTimeSeconds {
		waitSec = maxWaitTimeSeconds
	}
	deadline := s.now().Add(time.Duration(waitSec) * time.Second)
	for {
		// Register interest BEFORE checking, so a concurrent Send between the
		// check and the wait can't be missed (it closes this channel).
		wakeCh := s.notify.wait(queue)
		msgs, nextVisible, err := s.receiveOnce(queue, max, visibilityOverride)
		if err != nil || len(msgs) > 0 {
			return msgs, err
		}
		now := s.now()
		if !now.Before(deadline) {
			return nil, nil
		}
		// Sleep until: a Send wakes us, an in-flight/delayed message becomes
		// visible, or the long-poll deadline — whichever comes first.
		wakeAt := deadline
		if !nextVisible.IsZero() && nextVisible.Before(wakeAt) {
			wakeAt = nextVisible
		}
		timer := time.NewTimer(wakeAt.Sub(now))
		select {
		case <-wakeCh:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (s *Store) queueDefaultWait(queue string) int {
	w := 0
	_ = s.db.View(func(tx *bolt.Tx) error {
		if q, err := s.getQueue(tx, queue); err == nil {
			w = q.WaitTimeSeconds
		}
		return nil
	})
	return w
}

// receiveOnce attempts one delivery pass. nextVisible is the earliest time an
// in-flight or delayed message becomes available (zero if none), so the caller
// can sleep precisely instead of polling.
func (s *Store) receiveOnce(queue string, max, visibilityOverride int) (out []Message, nextVisible time.Time, err error) {
	var dlqHit []string
	var minVisible int64 // earliest future VisibleAt seen (nano), 0 if none
	err = s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, queue)
		if err != nil {
			return err
		}
		mb := tx.Bucket(msgBucket(queue))
		if mb == nil {
			return nil
		}
		now := s.now()
		nowN := now.UnixNano()
		vis := q.VisibilityTimeout
		if visibilityOverride >= 0 {
			vis = visibilityOverride
		}
		lockedGroups := map[string]bool{}

		// First pass (FIFO): mark groups that already have an in-flight message.
		if q.FIFO {
			_ = mb.ForEach(func(_, raw []byte) error {
				var m Message
				if json.Unmarshal(raw, &m) == nil && m.VisibleAt > nowN && m.GroupID != "" {
					lockedGroups[m.GroupID] = true
				}
				return nil
			})
		}

		// Decide the pass in a read-only cursor walk, then apply all mutations
		// afterward: bbolt's cursor gives undefined results if the bucket is
		// mutated (Delete/Put) while the cursor is live.
		var expireKeys [][]byte
		var dlqMsgs []Message
		var deliver []Message
		c := mb.Cursor()
		for k, raw := c.First(); k != nil && len(deliver) < max; k, raw = c.Next() {
			var m Message
			if json.Unmarshal(raw, &m) != nil {
				continue
			}
			// Retention: drop expired messages.
			if q.RetentionPeriod > 0 && now.Sub(time.Unix(0, m.Sent)) > time.Duration(q.RetentionPeriod)*time.Second {
				expireKeys = append(expireKeys, append([]byte(nil), k...))
				continue
			}
			if m.VisibleAt > nowN {
				if minVisible == 0 || m.VisibleAt < minVisible {
					minVisible = m.VisibleAt // in flight or delayed; wake when it's due
				}
				continue
			}
			if q.FIFO && lockedGroups[m.GroupID] {
				continue // a message from this group is already in flight
			}
			// DLQ redrive: if it has been received too many times, move it.
			if q.DeadLetterTarget != "" && q.MaxReceiveCount > 0 && m.ReceiveCount >= q.MaxReceiveCount {
				dlqMsgs = append(dlqMsgs, m)
				continue
			}
			// Deliver.
			m.ReceiveCount++
			if m.FirstReceived == 0 {
				m.FirstReceived = nowN
			}
			m.VisibleAt = now.Add(time.Duration(vis) * time.Second).UnixNano()
			deliver = append(deliver, m)
			if q.FIFO {
				lockedGroups[m.GroupID] = true
			}
		}
		// Apply mutations now that the cursor is done.
		for _, k := range expireKeys {
			_ = mb.Delete(k)
		}
		for i := range dlqMsgs {
			moved, err := s.moveToDLQ(tx, q.DeadLetterTarget, &dlqMsgs[i])
			if err != nil {
				return err
			}
			if moved {
				// A moved message no longer exists in the source queue.
				_ = mb.Delete(seqKey(dlqMsgs[i].Seq))
				dlqHit = append(dlqHit, q.DeadLetterTarget)
			}
			// If the DLQ was gone, leave the message in the source queue.
		}
		for i := range deliver {
			if err := putMessage(mb, &deliver[i]); err != nil {
				return err
			}
			out = append(out, deliver[i])
		}
		return nil
	})
	if minVisible != 0 {
		nextVisible = time.Unix(0, minVisible)
	}
	for _, dlq := range dlqHit {
		s.notify.signal(dlq) // a redriven message just landed in the DLQ
	}
	return out, nextVisible, err
}

// Peek returns up to max currently-visible messages in queue (FIFO) order WITHOUT
// consuming them: it never changes visibility, never increments the receive count,
// and ignores FIFO group locking — so it shows the FULL queue contents (every
// message, not just the head of each message group, the way a plain Receive does).
// Purely read-only; the returned handles are still valid for Delete.
func (s *Store) Peek(queue string, max int) ([]Message, error) {
	if max <= 0 {
		max = 10
	}
	var out []Message
	err := s.db.View(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, queue)
		if err != nil {
			return err
		}
		mb := tx.Bucket(msgBucket(queue))
		if mb == nil {
			return nil
		}
		now := s.now()
		nowN := now.UnixNano()
		c := mb.Cursor()
		for k, raw := c.First(); k != nil && len(out) < max; k, raw = c.Next() {
			var m Message
			if json.Unmarshal(raw, &m) != nil {
				continue
			}
			// Match ApproximateNumberOfMessages (visible depth): skip in-flight and
			// retention-expired messages, but show everything else regardless of group.
			if m.VisibleAt > nowN {
				continue
			}
			if q.RetentionPeriod > 0 && now.Sub(time.Unix(0, m.Sent)) > time.Duration(q.RetentionPeriod)*time.Second {
				continue
			}
			out = append(out, m)
		}
		return nil
	})
	return out, err
}

// moveToDLQ moves m into the dead-letter queue. It reports whether the move
// happened: if the DLQ no longer exists it returns (false, nil) so the caller
// leaves the message in the source queue instead of destroying it (real SQS
// does not lose the message when redrive can't complete).
func (s *Store) moveToDLQ(tx *bolt.Tx, dlq string, m *Message) (bool, error) {
	if _, err := s.getQueue(tx, dlq); err != nil {
		return false, nil // DLQ gone; do not drop the message
	}
	db, err := tx.CreateBucketIfNotExists(msgBucket(dlq))
	if err != nil {
		return false, err
	}
	seq, _ := db.NextSequence()
	moved := *m
	moved.Seq = seq
	moved.ReceiveCount = 0
	moved.VisibleAt = s.now().UnixNano()
	if err := putMessage(db, &moved); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) Delete(queue, handle string) error {
	seqKey, id, err := decodeHandle(handle)
	if err != nil {
		return errInvalid("invalid receipt handle")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, queue); err != nil {
			return err
		}
		mb := tx.Bucket(msgBucket(queue))
		if mb == nil {
			return nil
		}
		raw := mb.Get(seqKey)
		if raw == nil {
			return nil // already deleted — idempotent, like real SQS
		}
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if m.ID != id {
			return nil // stale handle for a message that has since been replaced
		}
		return mb.Delete(seqKey)
	})
}

func (s *Store) ChangeVisibility(queue, handle string, timeout int) error {
	seqKey, id, err := decodeHandle(handle)
	if err != nil {
		return errInvalid("invalid receipt handle")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, queue); err != nil {
			return err
		}
		mb := tx.Bucket(msgBucket(queue))
		if mb == nil {
			return nil
		}
		raw := mb.Get(seqKey)
		if raw == nil {
			return nil
		}
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if m.ID != id {
			return nil // stale handle; don't disturb the current occupant of this seq
		}
		m.VisibleAt = s.now().Add(time.Duration(timeout) * time.Second).UnixNano()
		return putMessage(mb, &m)
	})
}

// Handle encodes the message's position AND identity; Delete/ChangeVisibility
// decode both and verify the id so a stale handle can't act on a different
// message that later reused the same sequence number.
func (m *Message) Handle() string { return encodeHandle(seqKey(m.Seq), m.ID) }

// ---- dedup tracking (FIFO) ----

// dedupRec records just enough to report a duplicate Send as a success: the
// original message's id and body MD5 (not the whole message).
type dedupRec struct {
	At      int64  `json:"at"` // unix seconds
	ID      string `json:"id"`
	MD5Body string `json:"md5"`
}

func (s *Store) lookupDedup(tx *bolt.Tx, queue, dedupID string) (bool, *Message) {
	b := tx.Bucket(dedupBucket(queue))
	if b == nil {
		return false, nil
	}
	raw := b.Get([]byte(dedupID))
	if raw == nil {
		return false, nil
	}
	var r dedupRec
	if json.Unmarshal(raw, &r) != nil {
		return false, nil
	}
	if s.now().Unix()-r.At > dedupWindow {
		return false, nil
	}
	return true, &Message{ID: r.ID, MD5Body: r.MD5Body}
}

func (s *Store) recordDedup(tx *bolt.Tx, queue, dedupID string, m *Message) error {
	b, err := tx.CreateBucketIfNotExists(dedupBucket(queue))
	if err != nil {
		return err
	}
	s.gcDedup(b) // prune entries past the window so the bucket can't grow unbounded
	raw, _ := json.Marshal(dedupRec{At: s.now().Unix(), ID: m.ID, MD5Body: m.MD5Body})
	return b.Put([]byte(dedupID), raw)
}

// gcDedup deletes dedup entries older than the dedup window.
func (s *Store) gcDedup(b *bolt.Bucket) {
	cutoff := s.now().Unix() - dedupWindow
	var stale [][]byte
	_ = b.ForEach(func(k, raw []byte) error {
		var r dedupRec
		if json.Unmarshal(raw, &r) == nil && r.At < cutoff {
			stale = append(stale, append([]byte(nil), k...))
		}
		return nil
	})
	for _, k := range stale {
		_ = b.Delete(k)
	}
}
