package sqs

// The background sweeper: reclaims retention-expired messages and stale FIFO
// dedup entries so write-only queues can't grow unbounded.

import (
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Sweep drops retention-expired messages and prunes stale dedup entries across
// every queue. Receive does this lazily for read queues; a periodic Sweep also
// reclaims write-only queues so nothing grows unbounded.
func (s *Store) Sweep() {
	var queues []string
	_ = s.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(metaBucket); b != nil {
			return b.ForEach(func(k, _ []byte) error {
				queues = append(queues, string(k))
				return nil
			})
		}
		return nil
	})
	now := s.now()
	for _, qn := range queues {
		_ = s.db.Update(func(tx *bolt.Tx) error {
			q, err := s.getQueue(tx, qn)
			if err != nil {
				return nil
			}
			if mb := tx.Bucket(msgBucket(qn)); mb != nil && q.RetentionPeriod > 0 {
				var stale [][]byte
				_ = mb.ForEach(func(k, raw []byte) error {
					var m Message
					if json.Unmarshal(raw, &m) == nil && now.Sub(time.Unix(0, m.Sent)) > time.Duration(q.RetentionPeriod)*time.Second {
						stale = append(stale, append([]byte(nil), k...))
					}
					return nil
				})
				for _, k := range stale {
					_ = mb.Delete(k)
				}
			}
			if db := tx.Bucket(dedupBucket(qn)); db != nil {
				s.gcDedup(db)
			}
			return nil
		})
	}
}
