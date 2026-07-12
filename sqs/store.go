package sqs

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Defaults matching AWS SQS.
const (
	defVisibilityTimeout = 30     // seconds
	defRetentionPeriod   = 345600 // 4 days, seconds
	defMaxMessageSize    = 262144 // 256 KiB
	maxWaitTimeSeconds   = 20     // long-poll ceiling
	dedupWindow          = 5 * 60 // seconds, FIFO dedup window
	maxReceiveBatch      = 10
)

// metaBucket holds queue definitions; message buckets are named msgBucket(name).
var metaBucket = []byte("queues")

func msgBucket(queue string) []byte   { return []byte("q:" + queue) }
func dedupBucket(queue string) []byte { return []byte("dedup:" + queue) }

// Queue is a queue's durable definition.
type Queue struct {
	Name              string `json:"name"`
	FIFO              bool   `json:"fifo"`
	ContentBasedDedup bool   `json:"content_based_dedup"`
	VisibilityTimeout int    `json:"visibility_timeout"` // seconds
	DelaySeconds      int    `json:"delay_seconds"`
	RetentionPeriod   int    `json:"retention_period"` // seconds
	MaxMessageSize    int    `json:"max_message_size"`
	WaitTimeSeconds   int    `json:"wait_time_seconds"`  // default receive long-poll
	DeadLetterTarget  string `json:"dead_letter_target"` // target queue name, "" if none
	MaxReceiveCount   int    `json:"max_receive_count"`
	Created           int64  `json:"created"` // unix seconds

	Tags map[string]string `json:"tags,omitempty"`
}

// Attr is a message attribute (String/Number use StringValue; Binary uses BinaryValue).
type Attr struct {
	DataType    string `json:"data_type"`
	StringValue string `json:"string_value,omitempty"`
	BinaryValue []byte `json:"binary_value,omitempty"`
}

// Message is one stored message.
type Message struct {
	ID            string          `json:"id"`
	Body          string          `json:"body"`
	Attrs         map[string]Attr `json:"attrs,omitempty"`
	MD5Body       string          `json:"md5_body"`
	MD5Attrs      string          `json:"md5_attrs,omitempty"`
	Sent          int64           `json:"sent"`       // unixnano
	VisibleAt     int64           `json:"visible_at"` // unixnano; <= now => visible
	ReceiveCount  int             `json:"receive_count"`
	FirstReceived int64           `json:"first_received"` // unixnano, 0 if never
	GroupID       string          `json:"group_id,omitempty"`
	DedupID       string          `json:"dedup_id,omitempty"`
	Seq           uint64          `json:"seq"`
}

// Store is the bbolt-backed SQS state.
type Store struct {
	db     *bolt.DB
	clock  func() time.Time
	notify *notifier
}

func newStore(db *bolt.DB) *Store {
	return &Store{db: db, clock: time.Now, notify: newNotifier()}
}

func (s *Store) now() time.Time { return s.clock() }

// apiError is an SQS-shaped error (code maps to HTTP status + AWS error code).
type apiError struct {
	Code   string // AWS error code, e.g. "QueueDoesNotExist"
	Status int    // HTTP status
	Msg    string
}

func (e *apiError) Error() string { return e.Code + ": " + e.Msg }

func errQueueMissing(name string) *apiError {
	return &apiError{Code: "AWS.SimpleQueueService.NonExistentQueue", Status: 400, Msg: "The specified queue does not exist: " + name}
}
func errInvalid(msg string) *apiError {
	return &apiError{Code: "InvalidParameterValue", Status: 400, Msg: msg}
}

// ---- queue lifecycle ----

func (s *Store) getQueue(tx *bolt.Tx, name string) (*Queue, error) {
	b := tx.Bucket(metaBucket)
	if b == nil {
		return nil, errQueueMissing(name)
	}
	raw := b.Get([]byte(name))
	if raw == nil {
		return nil, errQueueMissing(name)
	}
	var q Queue
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	return &q, nil
}

func (s *Store) putQueue(tx *bolt.Tx, q *Queue) error {
	b, err := tx.CreateBucketIfNotExists(metaBucket)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return b.Put([]byte(q.Name), raw)
}

// CreateQueue creates (or, idempotently, updates the attributes of) a queue.
func (s *Store) CreateQueue(name string, attrs map[string]string, tags map[string]string) (*Queue, error) {
	if name == "" {
		return nil, errInvalid("queue name is required")
	}
	fifoAttr := attrs["FifoQueue"] == "true"
	if strings.HasSuffix(name, ".fifo") != fifoAttr && (fifoAttr || strings.HasSuffix(name, ".fifo")) {
		return nil, errInvalid("FIFO queue names must end in .fifo and only FIFO queues may")
	}
	var out *Queue
	err := s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			q = &Queue{
				Name:              name,
				FIFO:              fifoAttr,
				VisibilityTimeout: defVisibilityTimeout,
				RetentionPeriod:   defRetentionPeriod,
				MaxMessageSize:    defMaxMessageSize,
				Created:           s.now().Unix(),
			}
		}
		if err := applyAttrs(q, attrs); err != nil {
			return err
		}
		for k, v := range tags {
			if q.Tags == nil {
				q.Tags = map[string]string{}
			}
			q.Tags[k] = v
		}
		out = q
		return s.putQueue(tx, q)
	})
	return out, err
}

// applyAttrs folds an SQS attribute map into a queue definition.
func applyAttrs(q *Queue, attrs map[string]string) error {
	for k, v := range attrs {
		switch k {
		case "FifoQueue":
			q.FIFO = v == "true"
		case "ContentBasedDeduplication":
			q.ContentBasedDedup = v == "true"
		case "VisibilityTimeout":
			q.VisibilityTimeout = atoiDefault(v, q.VisibilityTimeout)
		case "DelaySeconds":
			q.DelaySeconds = atoiDefault(v, q.DelaySeconds)
		case "MessageRetentionPeriod":
			q.RetentionPeriod = atoiDefault(v, q.RetentionPeriod)
		case "MaximumMessageSize":
			q.MaxMessageSize = atoiDefault(v, q.MaxMessageSize)
		case "ReceiveMessageWaitTimeSeconds":
			q.WaitTimeSeconds = atoiDefault(v, q.WaitTimeSeconds)
		case "RedrivePolicy":
			if v == "" {
				q.DeadLetterTarget, q.MaxReceiveCount = "", 0
				continue
			}
			var rp struct {
				DeadLetterTargetArn string      `json:"deadLetterTargetArn"`
				MaxReceiveCount     json.Number `json:"maxReceiveCount"`
			}
			if err := json.Unmarshal([]byte(v), &rp); err != nil {
				return errInvalid("invalid RedrivePolicy: " + err.Error())
			}
			q.DeadLetterTarget = arnQueueName(rp.DeadLetterTargetArn)
			n, _ := rp.MaxReceiveCount.Int64()
			q.MaxReceiveCount = int(n)
		}
	}
	return nil
}

func (s *Store) DeleteQueue(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, name); err != nil {
			return err
		}
		_ = tx.Bucket(metaBucket).Delete([]byte(name))
		_ = tx.DeleteBucket(msgBucket(name))
		_ = tx.DeleteBucket(dedupBucket(name))
		return nil
	})
}

func (s *Store) ListQueues(prefix string) ([]string, error) {
	var names []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(metaBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			if prefix == "" || strings.HasPrefix(string(k), prefix) {
				names = append(names, string(k))
			}
			return nil
		})
	})
	sort.Strings(names)
	return names, err
}

// Attributes returns the GetQueueAttributes view of a queue.
func (s *Store) Attributes(name string) (map[string]string, error) {
	out := map[string]string{}
	err := s.db.View(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			return err
		}
		visible, inflight := 0, 0
		now := s.now().UnixNano()
		if mb := tx.Bucket(msgBucket(name)); mb != nil {
			_ = mb.ForEach(func(_, raw []byte) error {
				var m Message
				if json.Unmarshal(raw, &m) == nil {
					if m.VisibleAt <= now {
						visible++
					} else {
						inflight++
					}
				}
				return nil
			})
		}
		out["VisibilityTimeout"] = strconv.Itoa(q.VisibilityTimeout)
		out["DelaySeconds"] = strconv.Itoa(q.DelaySeconds)
		out["MessageRetentionPeriod"] = strconv.Itoa(q.RetentionPeriod)
		out["MaximumMessageSize"] = strconv.Itoa(q.MaxMessageSize)
		out["ReceiveMessageWaitTimeSeconds"] = strconv.Itoa(q.WaitTimeSeconds)
		out["CreatedTimestamp"] = strconv.FormatInt(q.Created, 10)
		out["ApproximateNumberOfMessages"] = strconv.Itoa(visible)
		out["ApproximateNumberOfMessagesNotVisible"] = strconv.Itoa(inflight)
		out["QueueArn"] = queueARN(name)
		if q.FIFO {
			out["FifoQueue"] = "true"
			out["ContentBasedDeduplication"] = strconv.FormatBool(q.ContentBasedDedup)
		}
		if q.DeadLetterTarget != "" {
			rp, _ := json.Marshal(map[string]string{
				"deadLetterTargetArn": queueARN(q.DeadLetterTarget),
				"maxReceiveCount":     strconv.Itoa(q.MaxReceiveCount),
			})
			out["RedrivePolicy"] = string(rp)
		}
		return nil
	})
	return out, err
}

func (s *Store) SetAttributes(name string, attrs map[string]string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			return err
		}
		if err := applyAttrs(q, attrs); err != nil {
			return err
		}
		return s.putQueue(tx, q)
	})
}

// ---- messages ----

// Send enqueues a message. delay<0 means "use the queue default".
func (s *Store) Send(queue, body string, attrs map[string]Attr, delay int, groupID, dedupID string) (*Message, error) {
	var out *Message
	enqueued := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, queue)
		if err != nil {
			return err
		}
		if q.MaxMessageSize > 0 && len(body) > q.MaxMessageSize {
			return errInvalid(fmt.Sprintf("message length %d exceeds MaximumMessageSize %d", len(body), q.MaxMessageSize))
		}
		if q.FIFO && groupID == "" {
			return errInvalid("MessageGroupId is required for FIFO queues")
		}
		if delay < 0 {
			delay = q.DelaySeconds
		}

		// FIFO dedup.
		if q.FIFO {
			if dedupID == "" && q.ContentBasedDedup {
				sum := md5.Sum([]byte(body))
				dedupID = fmt.Sprintf("%x", sum)
			}
			if dedupID == "" {
				return errInvalid("MessageDeduplicationId is required (or enable ContentBasedDeduplication)")
			}
			if dup, dm := s.lookupDedup(tx, queue, dedupID); dup {
				out = dm // duplicate within the window: report success, don't enqueue again
				return nil
			}
		}

		mb, err := tx.CreateBucketIfNotExists(msgBucket(queue))
		if err != nil {
			return err
		}
		seq, _ := mb.NextSequence()
		now := s.now()
		m := &Message{
			ID:        newID(),
			Body:      body,
			Attrs:     attrs,
			MD5Body:   md5hex(body),
			MD5Attrs:  md5Attributes(attrs),
			Sent:      now.UnixNano(),
			VisibleAt: now.Add(time.Duration(delay) * time.Second).UnixNano(),
			GroupID:   groupID,
			DedupID:   dedupID,
			Seq:       seq,
		}
		if err := putMessage(mb, m); err != nil {
			return err
		}
		if q.FIFO {
			if err := s.recordDedup(tx, queue, dedupID, m); err != nil {
				return err
			}
		}
		out, enqueued = m, true
		return nil
	})
	if err == nil && enqueued {
		s.notify.signal(queue) // wake any long-poll receiver immediately
	}
	return out, err
}

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

		c := mb.Cursor()
		for k, raw := c.First(); k != nil && len(out) < max; k, raw = c.Next() {
			var m Message
			if json.Unmarshal(raw, &m) != nil {
				continue
			}
			// Retention: drop expired messages.
			if q.RetentionPeriod > 0 && now.Sub(time.Unix(0, m.Sent)) > time.Duration(q.RetentionPeriod)*time.Second {
				_ = mb.Delete(k)
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
				moved, err := s.moveToDLQ(tx, q.DeadLetterTarget, &m)
				if err != nil {
					return err
				}
				if moved {
					_ = mb.Delete(k)
					dlqHit = append(dlqHit, q.DeadLetterTarget)
				}
				// If the DLQ was gone, leave the message in place rather than
				// deleting it — skip delivery this pass and try again next time.
				continue
			}
			// Deliver.
			m.ReceiveCount++
			if m.FirstReceived == 0 {
				m.FirstReceived = nowN
			}
			m.VisibleAt = now.Add(time.Duration(vis) * time.Second).UnixNano()
			if err := putMessage(mb, &m); err != nil {
				return err
			}
			if q.FIFO {
				lockedGroups[m.GroupID] = true
			}
			out = append(out, m)
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

func (s *Store) Purge(queue string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, queue); err != nil {
			return err
		}
		if tx.Bucket(msgBucket(queue)) != nil {
			if err := tx.DeleteBucket(msgBucket(queue)); err != nil {
				return err
			}
		}
		return nil
	})
}

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

// ---- helpers ----

func putMessage(b *bolt.Bucket, m *Message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return b.Put(seqKey(m.Seq), raw)
}

func seqKey(seq uint64) []byte {
	var k [8]byte
	binary.BigEndian.PutUint64(k[:], seq)
	return k[:]
}

// Handle encodes the message's position AND identity; Delete/ChangeVisibility
// decode both and verify the id so a stale handle can't act on a different
// message that later reused the same sequence number.
func (m *Message) Handle() string { return encodeHandle(seqKey(m.Seq), m.ID) }

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", sum)
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// arnQueueName extracts the queue name (last colon segment) from an ARN.
func arnQueueName(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}
