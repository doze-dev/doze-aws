package sqs

// ReceiveRequestAttemptId, a FIFO queue's way of making a ReceiveMessage safe
// to retry.
//
// doc: the model's ReceiveRequestAttemptId — "it is possible to retry the same
// action with an identical ReceiveRequestAttemptId to retrieve the same set of
// messages, even if their visibility timeout has not yet expired. You can use
// ReceiveRequestAttemptId only for 5 minutes after a ReceiveMessage action ...
// if none of the messages have been modified (deleted or had their visibility
// changes). During a visibility timeout, subsequent calls with the same
// ReceiveRequestAttemptId return the same messages and receipt handles. If a
// retry occurs within the deduplication interval, it resets the visibility
// timeout."
//
// The record is in memory: a restart inside the five minutes forgets it, and
// the retry then behaves as a new receive, which is what a caller sees when
// the service has nothing to match.

import (
	"encoding/json"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

const receiveAttemptWindow = 5 * time.Minute

type receiveAttempt struct {
	handles []string
	// visibleAt is each message's VisibleAt as that receive left it. A message
	// whose VisibleAt is anything else has been deleted, moved, or had its
	// visibility changed, which is when a retry stops matching.
	visibleAt []int64
	at        time.Time
}

type receiveAttempts struct {
	mu sync.Mutex
	by map[string]receiveAttempt // queue + "\x00" + attempt id
}

func (a *receiveAttempts) get(key string, now time.Time) (receiveAttempt, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.by[key]
	if ok && now.Sub(rec.at) > receiveAttemptWindow {
		delete(a.by, key)
		return receiveAttempt{}, false
	}
	return rec, ok
}

func (a *receiveAttempts) put(key string, rec receiveAttempt, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.by == nil {
		a.by = map[string]receiveAttempt{}
	}
	// A record older than the window can never match again; sweep as we go so
	// the map holds at most five minutes of attempts.
	for k, r := range a.by {
		if now.Sub(r.at) > receiveAttemptWindow {
			delete(a.by, k)
		}
	}
	a.by[key] = rec
}

func (a *receiveAttempts) drop(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.by, key)
}

// ReceiveAttempt is Receive with a ReceiveRequestAttemptId.
func (s *store) ReceiveAttempt(queue, attemptID string, max, waitSec, visibilityOverride int) ([]message, error) {
	key := queue + "\x00" + attemptID
	if rec, ok := s.attempts.get(key, s.now()); ok {
		msgs, err := s.replayReceive(queue, rec, visibilityOverride)
		if err != nil {
			return nil, err
		}
		if msgs != nil {
			rec.visibleAt = rec.visibleAt[:0]
			for i := range msgs {
				rec.visibleAt = append(rec.visibleAt, msgs[i].VisibleAt)
			}
			s.attempts.put(key, rec, s.now())
			return msgs, nil
		}
		s.attempts.drop(key) // something was modified: this is a new receive
	}
	msgs, err := s.Receive(queue, max, waitSec, visibilityOverride)
	if err != nil {
		return nil, err
	}
	if len(msgs) > 0 {
		rec := receiveAttempt{at: s.now()}
		for i := range msgs {
			rec.handles = append(rec.handles, msgs[i].Handle())
			rec.visibleAt = append(rec.visibleAt, msgs[i].VisibleAt)
		}
		s.attempts.put(key, rec, s.now())
	}
	return msgs, nil
}

// replayReceive returns the messages a recorded receive handed out, with their
// visibility timeout started again, or nil when any of them has been deleted,
// moved, or had its visibility changed since (it is no longer in flight from
// that receive).
func (s *store) replayReceive(queue string, rec receiveAttempt, visibilityOverride int) ([]message, error) {
	var out []message
	err := s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, queue)
		if err != nil {
			return err
		}
		mb := tx.Bucket(msgBucket(queue))
		if mb == nil {
			return nil
		}
		vis := q.VisibilityTimeout
		if visibilityOverride >= 0 {
			vis = visibilityOverride
		}
		now := s.now()
		nowN := now.UnixNano()
		var msgs []message
		for i, h := range rec.handles {
			seq, id, err := decodeHandle(h)
			if err != nil {
				return nil
			}
			raw := mb.Get(seq)
			if raw == nil {
				return nil
			}
			var m message
			if json.Unmarshal(raw, &m) != nil || m.ID != id {
				return nil
			}
			// Still in flight from the recorded receive, and untouched since.
			if m.VisibleAt <= nowN || m.VisibleAt != rec.visibleAt[i] {
				return nil
			}
			msgs = append(msgs, m)
		}
		for i := range msgs {
			msgs[i].VisibleAt = now.Add(time.Duration(vis) * time.Second).UnixNano()
			msgs[i].LastReceived = nowN
			if err := putMessage(mb, &msgs[i]); err != nil {
				return err
			}
		}
		out = msgs
		return nil
	})
	return out, err
}
