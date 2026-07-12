package sqs

// Store operations added in the doze-aws port, beyond the original doze
// builtin's surface: queue tags, dead-letter source discovery, and message
// move tasks (DLQ redrive).

import (
	"encoding/json"
	"fmt"
	"sort"

	bolt "go.etcd.io/bbolt"
)

var moveTasksBucket = []byte("movetasks")

// TagQueue merges tags into a queue's tag set.
func (s *Store) TagQueue(name string, tags map[string]string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			return err
		}
		if q.Tags == nil {
			q.Tags = map[string]string{}
		}
		for k, v := range tags {
			q.Tags[k] = v
		}
		return s.putQueue(tx, q)
	})
}

// UntagQueue removes the named tag keys.
func (s *Store) UntagQueue(name string, keys []string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			return err
		}
		for _, k := range keys {
			delete(q.Tags, k)
		}
		return s.putQueue(tx, q)
	})
}

// Tags returns a queue's tag set.
func (s *Store) Tags(name string) (map[string]string, error) {
	var out map[string]string
	err := s.db.View(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			return err
		}
		out = q.Tags
		return nil
	})
	return out, err
}

// DeadLetterSourceQueues lists the queues whose redrive policy targets dlq.
func (s *Store) DeadLetterSourceQueues(dlq string) ([]string, error) {
	var out []string
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, dlq); err != nil {
			return err
		}
		b := tx.Bucket(metaBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var q Queue
			if json.Unmarshal(raw, &q) == nil && q.DeadLetterTarget == dlq {
				out = append(out, q.Name)
			}
			return nil
		})
	})
	sort.Strings(out)
	return out, err
}

// MoveTask records one message move task. Local moves are synchronous, so a
// stored task is always in a terminal state.
type MoveTask struct {
	Handle      string `json:"handle"`
	Status      string `json:"status"` // COMPLETED | FAILED
	Source      string `json:"source"` // queue name
	Destination string `json:"destination"`
	Moved       int    `json:"moved"`
	StartedAt   int64  `json:"started_at"` // unix seconds
	FailureWhy  string `json:"failure_why,omitempty"`
}

// StartMessageMoveTask moves every currently-stored message from source to
// dest, synchronously — the local equivalent of a DLQ redrive. AWS moves
// asynchronously with rate control; locally the volumes are small enough that
// completing inline is simpler and deterministic.
func (s *Store) StartMessageMoveTask(source, dest string) (*MoveTask, error) {
	task := &MoveTask{
		Handle:      newID(),
		Status:      "COMPLETED",
		Source:      source,
		Destination: dest,
		StartedAt:   s.now().Unix(),
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, source); err != nil {
			return err
		}
		if _, err := s.getQueue(tx, dest); err != nil {
			return err
		}
		src := tx.Bucket(msgBucket(source))
		if src == nil {
			return s.recordMoveTask(tx, task)
		}
		dst, err := tx.CreateBucketIfNotExists(msgBucket(dest))
		if err != nil {
			return err
		}
		var keys [][]byte
		_ = src.ForEach(func(k, raw []byte) error {
			var m Message
			if json.Unmarshal(raw, &m) != nil {
				return nil
			}
			seq, _ := dst.NextSequence()
			moved := m
			moved.Seq = seq
			moved.ReceiveCount = 0
			moved.VisibleAt = s.now().UnixNano()
			if err := putMessage(dst, &moved); err != nil {
				return err
			}
			keys = append(keys, append([]byte(nil), k...))
			return nil
		})
		for _, k := range keys {
			_ = src.Delete(k)
		}
		task.Moved = len(keys)
		return s.recordMoveTask(tx, task)
	})
	if err != nil {
		return nil, err
	}
	s.notify.signal(dest)
	return task, nil
}

func (s *Store) recordMoveTask(tx *bolt.Tx, task *MoveTask) error {
	b, err := tx.CreateBucketIfNotExists(moveTasksBucket)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(task)
	return b.Put([]byte(fmt.Sprintf("%020d:%s", task.StartedAt, task.Handle)), raw)
}

// ListMessageMoveTasks returns the recorded tasks for a source queue, newest
// first, up to max.
func (s *Store) ListMessageMoveTasks(source string, max int) ([]MoveTask, error) {
	if max <= 0 {
		max = 1
	}
	var out []MoveTask
	err := s.db.View(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, source); err != nil {
			return err
		}
		b := tx.Bucket(moveTasksBucket)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, raw := c.Last(); k != nil && len(out) < max; k, raw = c.Prev() {
			var t MoveTask
			if json.Unmarshal(raw, &t) == nil && t.Source == source {
				out = append(out, t)
			}
		}
		return nil
	})
	return out, err
}

// CancelMessageMoveTask always fails locally: moves complete synchronously, so
// by the time a cancel arrives the task is already terminal — which is exactly
// what AWS reports for a finished task.
func (s *Store) CancelMessageMoveTask(handle string) error {
	return &apiError{
		Code:   "ResourceNotFoundException",
		Status: 400,
		Msg:    "task is not active: local message move tasks complete synchronously",
	}
}
