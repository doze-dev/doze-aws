package sqs

// store operations added in the doze-aws port, beyond the original doze
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
func (s *store) TagQueue(name string, tags map[string]string) error {
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
func (s *store) UntagQueue(name string, keys []string) error {
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
func (s *store) Tags(name string) (map[string]string, error) {
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
func (s *store) DeadLetterSourceQueues(dlq string) ([]string, error) {
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
			var q queue
			if json.Unmarshal(raw, &q) == nil && q.DeadLetterTarget == dlq {
				out = append(out, q.Name)
			}
			return nil
		})
	})
	sort.Strings(out)
	return out, err
}

// MoveTask records one message move task.
//
// Status is always COMPLETED, and that is a consequence of the local design
// rather than an omission. AWS moves asynchronously, so a task can be RUNNING
// and can later FAIL; here the whole move happens inside one bbolt transaction,
// so a failure rolls the transaction back and comes out of StartMessageMoveTask
// as an API error. There is no moment at which a failed task could be observed,
// which means there is nothing for a FailureReason to say.
//
// The field for it used to be here — declared, read into the wire response, and
// never assigned anywhere in the tree, so ListMessageMoveTasks reported an empty
// FailureReason forever. The wire view keeps the field (AWS has it, and it is
// omitempty) but nothing pretends to fill it.
type moveTask struct {
	Handle      string `json:"handle"`
	Status      string `json:"status"` // always COMPLETED — see above
	Source      string `json:"source"` // queue name
	Destination string `json:"destination"`
	Moved       int    `json:"moved"`
	StartedAt   int64  `json:"started_at"` // unix seconds
}

// StartMessageMoveTask moves every currently-stored message from source to
// dest, synchronously — the local equivalent of a DLQ redrive. AWS moves
// asynchronously with rate control; locally the volumes are small enough that
// completing inline is simpler and deterministic.
func (s *store) StartMessageMoveTask(source, dest string) (*moveTask, error) {
	task := &moveTask{
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
			var m message
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

func (s *store) recordMoveTask(tx *bolt.Tx, task *moveTask) error {
	b, err := tx.CreateBucketIfNotExists(moveTasksBucket)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(task)
	return b.Put([]byte(fmt.Sprintf("%020d:%s", task.StartedAt, task.Handle)), raw)
}

// ListMessageMoveTasks returns the recorded tasks for a source queue, newest
// first, up to max.
func (s *store) ListMessageMoveTasks(source string, max int) ([]moveTask, error) {
	if max <= 0 {
		max = 1
	}
	var out []moveTask
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
			var t moveTask
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
func (s *store) CancelMessageMoveTask(handle string) error {
	return &apiError{
		Code:        "ResourceNotFoundException",
		Status:      400,
		Message:     "task is not active: local message move tasks complete synchronously",
		SenderFault: true,
	}
}
