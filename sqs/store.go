package sqs

// The bbolt-backed store: queue/message schema, queue lifecycle, and enqueue.
// Receive/visibility logic lives in receive.go; attributes in attrs.go.

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
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
