package sqs

// store operations added in the doze-aws port, beyond the original doze
// builtin's surface: queue tags, dead-letter source discovery, and message
// move tasks (DLQ redrive).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
		if err := validateTags(q.Tags, tags); err != nil {
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

// Tag limits (doc: the SQS developer guide, "Configuring cost allocation tags
// for Amazon SQS"): a key of 1 to 128 characters, a value of 0 to 256, and the
// reserved aws: prefix in any case. The model's TagQueue says only that
// "adding more than 50 tags to a queue isn't recommended", which is advice, so
// the count is not refused.
const (
	maxTagKey   = 128
	maxTagValue = 256
)

// validateTags checks what is about to be added to what a queue already has.
// A key already there is overwritten, not added, so it does not count again.
func validateTags(have, add map[string]string) error {
	for k, v := range add {
		switch {
		case k == "" || len([]rune(k)) > maxTagKey:
			return errInvalid("Value for parameter Tag key is invalid. Reason: a tag key is 1 to 128 characters.")
		case len([]rune(v)) > maxTagValue:
			return errInvalid("Value for parameter Tag value is invalid. Reason: a tag value is at most 256 characters.")
		case strings.HasPrefix(strings.ToLower(k), "aws:"):
			return errInvalid("Value for parameter Tag key is invalid. Reason: the aws: prefix is reserved.")
		}
	}
	return nil
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

// moveTask records one message move task (move_task.go runs them).
type moveTask struct {
	Handle        string `json:"handle"`
	Status        string `json:"status"` // RUNNING in memory; COMPLETED, FAILED or CANCELLED once recorded
	Source        string `json:"source"` // queue name
	Destination   string `json:"destination,omitempty"`
	Moved         int    `json:"moved"`
	ToMove        int    `json:"to_move"`                  // what the source held when the task started
	MaxPerSecond  int    `json:"max_per_second,omitempty"` // the rate asked for
	FailureReason string `json:"failure_reason,omitempty"`
	StartedAt     int64  `json:"started_at"` // unix seconds
}

// isDeadLetterQueue reports whether some queue's redrive policy targets name.
func (s *store) isDeadLetterQueue(tx *bolt.Tx, name string) bool {
	b := tx.Bucket(metaBucket)
	if b == nil {
		return false
	}
	found := false
	_ = b.ForEach(func(_, raw []byte) error {
		var q queue
		if json.Unmarshal(raw, &q) == nil && q.DeadLetterTarget == name {
			found = true
		}
		return nil
	})
	return found
}

func (s *store) recordMoveTask(tx *bolt.Tx, task *moveTask) error {
	b, err := tx.CreateBucketIfNotExists(moveTasksBucket)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(task)
	// Keyed by the moment it is recorded, in nanoseconds, so "newest first" holds
	// for two tasks started inside one second. A record from before this keyed
	// by seconds sorts before any of these.
	return b.Put([]byte(fmt.Sprintf("%020d:%s", s.now().UnixNano(), task.Handle)), raw)
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
		// What is running now is the newest there is.
		var running string
		if rt, ok := s.moves.runningFor(source); ok {
			out = append(out, rt)
			running = rt.Handle // its record may already be written
		}
		b := tx.Bucket(moveTasksBucket)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, raw := c.Last(); k != nil && len(out) < max; k, raw = c.Prev() {
			var t moveTask
			if json.Unmarshal(raw, &t) == nil && t.Source == source && t.Handle != running {
				out = append(out, t)
			}
		}
		return nil
	})
	return out, err
}
