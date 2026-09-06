package stepfunctions

import (
	"bytes"
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// The activity queue: one unclaimed task per row, keyed activityName \x00
// %020d(sequence), so an activity's backlog is a prefix cursor scan in the
// order the engine scheduled it — that is the FIFO GetActivityTask promises.
//
// Rows are written by SaveTransition, in the transaction that parks the
// frame and writes its token, and are removed either by a worker's claim
// (ClaimActivityTask) or by the token's own deletion (a timeout, a stop)
// through TokenRef.Queue. A restart therefore finds exactly the tasks no
// worker has taken, which is what makes a queued task survive one.

var bucketActivityQueue = []byte("activityqueue")

// ActivityTask is one queued task: what GetActivityTask hands a worker, plus
// where it came from for the console.
type ActivityTask struct {
	Activity    string `json:"activity"`
	Token       string `json:"token"`
	Input       string `json:"input"`
	ExecKey     string `json:"exec_key"`
	Frame       int    `json:"frame"`
	ScheduledAt int64  `json:"scheduled_at"`
}

func activityQueueKey(name string, seq uint64) []byte {
	return []byte(fmt.Sprintf("%s\x00%020d", name, seq))
}

// applyTokenOp performs one buffered token write inside SaveTransition's
// transaction: put or delete the token row, and keep the activity queue in
// step with it.
func applyTokenOp(tx *bolt.Tx, tb *bolt.Bucket, op tokenOp) error {
	if op.Ref == nil {
		return deleteToken(tx, tb, op.Token)
	}
	if op.Task != nil {
		// A retry re-dispatches with the same token. The earlier entry, if
		// still unclaimed, must go first or the worker would see it twice.
		if err := deleteToken(tx, tb, op.Token); err != nil {
			return err
		}
		qb, err := tx.CreateBucketIfNotExists(bucketActivityQueue)
		if err != nil {
			return err
		}
		seq, err := qb.NextSequence()
		if err != nil {
			return err
		}
		key := activityQueueKey(op.Task.Activity, seq)
		raw, err := json.Marshal(op.Task)
		if err != nil {
			return err
		}
		if err := qb.Put(key, raw); err != nil {
			return err
		}
		op.Ref.Queue = string(key)
	}
	rawRef, err := json.Marshal(op.Ref)
	if err != nil {
		return err
	}
	return tb.Put([]byte(op.Token), rawRef)
}

// deleteToken removes a token row and the queue entry it still points at.
func deleteToken(tx *bolt.Tx, tb *bolt.Bucket, token string) error {
	raw := tb.Get([]byte(token))
	if raw == nil {
		return nil
	}
	var ref TokenRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return err
	}
	if ref.Queue != "" {
		if qb := tx.Bucket(bucketActivityQueue); qb != nil {
			if err := qb.Delete([]byte(ref.Queue)); err != nil {
				return err
			}
		}
	}
	return tb.Delete([]byte(token))
}

// ClaimActivityTask pops the oldest queued task of one activity, nil when
// the queue is empty. The claim and the token's detachment from the queue
// are one transaction, so two workers polling the same activity can never
// both receive a task — bbolt serialises writers.
func (s *Store) ClaimActivityTask(name string) (*ActivityTask, error) {
	var out *ActivityTask
	prefix := []byte(name + "\x00")
	err := s.db.Update(func(tx *bolt.Tx) error {
		qb := tx.Bucket(bucketActivityQueue)
		if qb == nil {
			return nil
		}
		c := qb.Cursor()
		k, raw := c.Seek(prefix)
		if k == nil || !bytes.HasPrefix(k, prefix) {
			return nil
		}
		var task ActivityTask
		if err := json.Unmarshal(raw, &task); err != nil {
			return err
		}
		if err := qb.Delete(k); err != nil {
			return err
		}
		// Detach the token from the queue so a later token delete does not
		// reach for a row that is gone — harmless, but honest.
		if tb := tx.Bucket(bucketTokens); tb != nil {
			if rawRef := tb.Get([]byte(task.Token)); rawRef != nil {
				var ref TokenRef
				if err := json.Unmarshal(rawRef, &ref); err != nil {
					return err
				}
				ref.Queue = ""
				rawRef, err := json.Marshal(&ref)
				if err != nil {
					return err
				}
				if err := tb.Put([]byte(task.Token), rawRef); err != nil {
					return err
				}
			}
		}
		out = &task
		return nil
	})
	return out, err
}

// ListActivityTasks returns one activity's unclaimed tasks, oldest first —
// its queue depth is the length. This is what a console page reads.
func (s *Store) ListActivityTasks(name string) ([]ActivityTask, error) {
	prefix := []byte(name + "\x00")
	var out []ActivityTask
	err := s.db.View(func(tx *bolt.Tx) error {
		qb := tx.Bucket(bucketActivityQueue)
		if qb == nil {
			return nil
		}
		c := qb.Cursor()
		for k, raw := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, raw = c.Next() {
			var task ActivityTask
			if err := json.Unmarshal(raw, &task); err != nil {
				return err
			}
			out = append(out, task)
		}
		return nil
	})
	return out, err
}
