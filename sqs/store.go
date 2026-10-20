package sqs

// The bbolt-backed store: queue/message schema, queue lifecycle, and enqueue.
// Receive/visibility logic lives in receive.go; attributes in attrs.go.

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/lazybolt"
	bolt "go.etcd.io/bbolt"
)

// Defaults matching AWS SQS.
const (
	defVisibilityTimeout = 30      // seconds
	defRetentionPeriod   = 345600  // 4 days, seconds
	defMaxMessageSize    = 1048576 // 1 MiB (doc: the model's MaximumMessageSize and SendMessage)
	maxWaitTimeSeconds   = 20      // long-poll ceiling
	dedupWindow          = 5 * 60  // seconds, FIFO dedup window
	maxReceiveBatch      = 10
	defMaxReceiveCount   = 10    // a RedrivePolicy that names none (model: "Default: 10")
	maxListQueues        = 1000  // MaxResults on ListQueues, and a page's size
	maxDelaySeconds      = 900   // DelaySeconds on a send, and on a queue
	maxVisibilityTimeout = 43200 // 12 hours, in seconds
)

// metaBucket holds queue definitions; message buckets are named msgBucket(name).
var metaBucket = []byte("queues")

func msgBucket(queue string) []byte   { return []byte("q:" + queue) }
func dedupBucket(queue string) []byte { return []byte("dedup:" + queue) }

// queue is a queue's durable definition.
type queue struct {
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
	Created           int64  `json:"created"`  // unix seconds
	Modified          int64  `json:"modified"` // unix seconds

	// Attrs holds every other attribute the queue was given. SQS has a long
	// tail of settings with no local behaviour — the queue policy, the SSE
	// pair, RedriveAllowPolicy, the FIFO throughput controls — and dropping
	// one silently is worse than storing it: Terraform writes an attribute,
	// reads it back, sees nothing, and never converges. Anything not handled
	// above round-trips here so an unrecognised attribute cannot go missing.
	Attrs map[string]string `json:"attrs,omitempty"`

	Tags map[string]string `json:"tags,omitempty"`
}

// attr is a message attribute (String/Number use StringValue; Binary uses BinaryValue).
type attr struct {
	DataType    string `json:"data_type"`
	StringValue string `json:"string_value,omitempty"`
	BinaryValue []byte `json:"binary_value,omitempty"`
}

// message is one stored message.
type message struct {
	ID            string          `json:"id"`
	Body          string          `json:"body"`
	Attrs         map[string]attr `json:"attrs,omitempty"`
	MD5Body       string          `json:"md5_body"`
	MD5Attrs      string          `json:"md5_attrs,omitempty"`
	Sent          int64           `json:"sent"`       // unixnano
	VisibleAt     int64           `json:"visible_at"` // unixnano; <= now => visible
	ReceiveCount  int             `json:"receive_count"`
	FirstReceived int64           `json:"first_received"` // unixnano, 0 if never
	// LastReceived is when the current receipt began. ChangeMessageVisibility
	// counts the twelve hours from it, and a new receive starts the count again.
	LastReceived int64 `json:"last_received,omitempty"`
	// SysAttrs are SQS's own message system attributes — currently just
	// AWSTraceHeader. They are deliberately NOT part of Attrs: system
	// attributes are excluded from MD5OfMessageAttributes and are returned
	// alongside SentTimestamp rather than with the user's attributes, so
	// storing them together would corrupt both.
	SysAttrs map[string]string `json:"sys_attrs,omitempty"`
	// SourceArn is the queue this message was dead-lettered from, which AWS
	// reports as DeadLetterQueueSourceArn and which a move task without a
	// destination returns it to. Empty for a message that was sent here.
	SourceArn string `json:"source_arn,omitempty"`
	GroupID   string `json:"group_id,omitempty"`
	DedupID   string `json:"dedup_id,omitempty"`
	Seq       uint64 `json:"seq"`
}

// store is the bbolt-backed SQS state.
type store struct {
	db    *lazybolt.DB
	clock func() time.Time
	// id is the region and account ARNs are minted for. Stamped by New after
	// construction, like clock: a queue decoded out of bbolt carries no pointer
	// back to the Server that owns it.
	id awsident.Identity
	// strict turns on the 60-second rules (strict.go). Off by default.
	strict bool
	minute minuteRules
	// moves is the move tasks that are running (move_task.go).
	moves  moveTasks
	notify *notifier
	// attempts is what ReceiveRequestAttemptId matches a retry against. Empty
	// until a FIFO receive carries one.
	attempts receiveAttempts
}

func newStore(db *lazybolt.DB) *store {
	return &store{db: db, clock: time.Now, notify: newNotifier()}
}

func (s *store) now() time.Time { return s.clock() }

// ---- queue lifecycle ----

// lookupIn binds a transaction into a queueLookup, so attribute validation can
// ask whether a queue it has been pointed at actually exists.
func (s *store) lookupIn(tx *bolt.Tx) queueLookup {
	return queueLookup{
		byARN: func(arn string) (*queue, error) {
			name, ok := s.ownQueue(arn)
			if !ok {
				return nil, errQueueMissing(arn)
			}
			return s.getQueue(tx, name)
		},
		arnOf: s.queueARN,
	}
}

func (s *store) getQueue(tx *bolt.Tx, name string) (*queue, error) {
	b := tx.Bucket(metaBucket)
	if b == nil {
		return nil, errQueueMissing(name)
	}
	raw := b.Get([]byte(name))
	if raw == nil {
		return nil, errQueueMissing(name)
	}
	var q queue
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	return &q, nil
}

func (s *store) putQueue(tx *bolt.Tx, q *queue) error {
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

// CreateQueue creates a queue, or answers with the one that is already there.
//
// It used to update an existing queue with whatever attributes came with the
// call. That is not idempotence: CreateQueue with a different VisibilityTimeout
// quietly rewrote a queue that was already in use, and returned success. AWS
// refuses it — a second CreateQueue is only allowed to agree with the first.
// The attributes it is given are held against the queue's, and the first one
// that differs is named in the refusal. Changing a queue is SetQueueAttributes.
func (s *store) CreateQueue(name string, attrs map[string]string, tags map[string]string) (*queue, error) {
	if name == "" {
		return nil, errInvalid("queue name is required")
	}
	if err := validQueueName(name); err != nil {
		return nil, err
	}
	fifoAttr := attrs["FifoQueue"] == "true"
	if strings.HasSuffix(name, ".fifo") != fifoAttr && (fifoAttr || strings.HasSuffix(name, ".fifo")) {
		return nil, errInvalid("FIFO queue names must end in .fifo, and only a FIFO queue may use that ending")
	}
	var out *queue
	err := s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, name); err != nil && s.strict && s.minute.deletedRecently(name, s.now()) {
			return errQueueDeletedRecently()
		}
		if q, err := s.getQueue(tx, name); err == nil {
			have := s.configuredAttrs(q)
			// What the request would read back as, not what was typed: a value
			// the model gives a default for is the same ask with or without it
			// (a RedrivePolicy with no maxReceiveCount reads back as 10).
			cand := &queue{Name: name, FIFO: fifoAttr, VisibilityTimeout: defVisibilityTimeout,
				RetentionPeriod: defRetentionPeriod, MaxMessageSize: defMaxMessageSize}
			if err := applyAttrs(cand, attrs, queueLookup{}); err != nil {
				return err
			}
			want := s.configuredAttrs(cand)
			keys := make([]string, 0, len(attrs))
			for k := range attrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				w, h := attrs[k], have[k]
				if v, ok := want[k]; ok {
					w = v
				}
				if h == "" {
					h = attrDefaults[k]
				}
				if !sameAttrValue(h, w) {
					return errQueueExists(k)
				}
			}
			out = q
			return nil
		}
		q := &queue{
			Name:              name,
			FIFO:              fifoAttr,
			VisibilityTimeout: defVisibilityTimeout,
			RetentionPeriod:   defRetentionPeriod,
			MaxMessageSize:    defMaxMessageSize,
			Created:           s.now().Unix(),
		}
		if err := applyAttrs(q, attrs, s.lookupIn(tx)); err != nil {
			return err
		}
		if err := validateTags(nil, tags); err != nil {
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

func (s *store) DeleteQueue(name string) error {
	if err := s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, name); err != nil {
			return err
		}
		_ = tx.Bucket(metaBucket).Delete([]byte(name))
		_ = tx.DeleteBucket(msgBucket(name))
		_ = tx.DeleteBucket(dedupBucket(name))
		return nil
	}); err != nil {
		return err
	}
	if s.strict {
		s.minute.noteDeleted(name, s.now())
	}
	// A receive registers interest before it checks the store, and a receive
	// that SUCCEEDS returns without removing it — harmless while the queue
	// lives, because the next Send signals it away, and permanent the moment
	// the queue does not, because nothing can ever signal that name again. One
	// stranded channel, one map entry and one string per deleted queue, for the
	// life of the process.
	//
	// forget rather than signal: anyone still holding that channel is waiting
	// on a queue that cannot deliver, and they have a long-poll deadline of
	// their own. Closing it would be a wakeup with nothing behind it.
	s.notify.forget(name)
	return nil
}

func (s *store) ListQueues(prefix string) ([]string, error) {
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
func (s *store) Send(queue, body string, attrs map[string]attr, delay int, groupID, dedupID string, sysAttrs map[string]string) (*message, error) {
	return s.sendNoted(nil, queue, body, attrs, delay, groupID, dedupID, sysAttrs)
}

// sendNoted is Send for a caller that wants to be told the id of the message
// before it can be received: note runs inside the transaction, ahead of the
// commit that makes the message visible, so a reader that wakes on it can
// already find whatever note recorded (internal/msgcause).
func (s *store) sendNoted(note func(id string), queue, body string, attrs map[string]attr, delay int, groupID, dedupID string, sysAttrs map[string]string) (*message, error) {
	var out *message
	enqueued := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, queue)
		if err != nil {
			return err
		}
		m, sent, err := s.sendIn(tx, q, body, attrs, delay, groupID, dedupID, sysAttrs, note)
		out, enqueued = m, sent
		return err
	})
	if err == nil && enqueued {
		s.notify.signal(queue) // wake any long-poll receiver immediately
	}
	return out, err
}

// sendItem is one message in a SendBatch.
type sendItem struct {
	Body     string
	Attrs    map[string]attr
	Delay    int // <0 means the queue default
	GroupID  string
	DedupID  string
	SysAttrs map[string]string
	// Invalid is a refusal decided before the item reached the store (a number
	// out of its range). The entry fails with it, alone.
	Invalid *apiError
	// Note, when set, is told the message's id before it is committed (sendNoted).
	Note func(id string)
}

// SendResult is one item's outcome. Err is that ITEM's failure, not the
// batch's: AWS reports SendMessageBatch per entry, so one oversized body does
// not sink the other nine.
type sendOutcome struct {
	Msg *message
	Err error
}

// SendBatch enqueues several messages in ONE transaction, and therefore one
// fsync.
//
// This is the whole point of it. The handler used to loop over Send, which
// opened a transaction per message: ten messages cost ten fsyncs — measured at
// 76.1 ms against 7.55 ms for the same ten in one transaction, a 10x
// difference that is entirely durability and not work.
//
// A per-item failure is recorded and the loop continues, because aborting the
// transaction would roll back the items that succeeded and AWS's contract is
// per entry. A QUEUE-level failure (no such queue) returns an error and
// nothing is written, which is also what AWS does — a bad QueueUrl fails the
// request rather than every entry in it.
func (s *store) SendBatch(queue string, items []sendItem) ([]sendOutcome, error) {
	results := make([]sendOutcome, len(items))
	enqueued := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, queue)
		if err != nil {
			return err
		}
		for i, it := range items {
			if it.Invalid != nil {
				results[i] = sendOutcome{Err: it.Invalid}
				continue
			}
			m, sent, err := s.sendIn(tx, q, it.Body, it.Attrs, it.Delay, it.GroupID, it.DedupID, it.SysAttrs, it.Note)
			// Deliberately not returning err: a per-entry failure is reported
			// per entry, and aborting would roll back the entries that worked.
			results[i] = sendOutcome{Msg: m, Err: err}
			enqueued = enqueued || sent
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if enqueued {
		s.notify.signal(queue)
	}
	return results, nil
}

// sendIn is one message's work inside a caller's transaction. sent reports
// whether anything was actually enqueued — a FIFO duplicate returns a message
// and false, since it reports success without writing.
func (s *store) sendIn(tx *bolt.Tx, q *queue, body string, attrs map[string]attr, delay int, groupID, dedupID string, sysAttrs map[string]string, note func(id string)) (out *message, sent bool, err error) {
	queue := q.Name
	if aerr := validateBody(body); aerr != nil {
		return nil, false, aerr
	}
	if aerr := validateMessageAttrs(attrs); aerr != nil {
		return nil, false, aerr
	}
	if size := messageSize(body, attrs); q.MaxMessageSize > 0 && size > q.MaxMessageSize {
		return nil, false, errInvalid(fmt.Sprintf("One or more parameters are invalid. Reason: Message must be shorter than %d bytes (the body and its attributes were %d).", q.MaxMessageSize, size))
	}
	// A group id is held to its format on either kind of queue: on a standard
	// queue it names a tenant for fair queues (doc: the model's MessageGroupId).
	if groupID != "" && !validFIFOID(groupID) {
		return nil, false, errInvalid("Value " + groupID + " for parameter MessageGroupId is invalid. Reason: MessageGroupId can contain up to 128 alphanumeric characters and punctuation (!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~).")
	}
	if q.FIFO {
		if groupID == "" {
			return nil, false, errInvalid("The request must contain the parameter MessageGroupId.")
		}
		if dedupID != "" && !validFIFOID(dedupID) {
			return nil, false, errInvalid("Value " + dedupID + " for parameter MessageDeduplicationId is invalid. Reason: MessageDeduplicationId can contain up to 128 alphanumeric characters and punctuation (!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~).")
		}
		// A FIFO queue has its queue's delay and no other (doc: SendMessage).
		if delay >= 0 {
			return nil, false, errInvalid("The request include parameter that is not valid for this queue type: a FIFO queue does not take DelaySeconds on a message.")
		}
	} else if dedupID != "" {
		// "This parameter applies only to FIFO queues." The code and wording
		// are memory; the recording queue holds the check.
		return nil, false, errInvalid("The request include parameter that is not valid for this queue type: MessageDeduplicationId belongs to FIFO queues.")
	}
	if delay < 0 {
		delay = q.DelaySeconds
	}

	// FIFO dedup.
	var dedupKey string
	if q.FIFO {
		if dedupID == "" && q.ContentBasedDedup {
			// model: "Amazon SQS uses a SHA-256 hash to generate the
			// MessageDeduplicationId using the body of the message (but not
			// the attributes of the message)". It was an MD5, so the id a
			// consumer read back, and one a producer computed itself to match,
			// were not the ones AWS gives.
			sum := sha256.Sum256([]byte(body))
			dedupID = hex.EncodeToString(sum[:])
		}
		if dedupID == "" {
			return nil, false, errInvalid("MessageDeduplicationId is required (or enable ContentBasedDeduplication)")
		}
		// At message group scope an id is only unique within its group.
		dedupKey = dedupID
		if q.Attrs["DeduplicationScope"] == "messageGroup" {
			dedupKey = groupID + "\x00" + dedupID
		}
		if dup, dm := s.lookupDedup(tx, queue, dedupKey); dup {
			// A duplicate within the window reports success without enqueuing.
			return dm, false, nil
		}
	}

	mb, err := tx.CreateBucketIfNotExists(msgBucket(queue))
	if err != nil {
		return nil, false, err
	}
	seq, _ := mb.NextSequence()
	now := s.now()
	m := &message{
		ID:        newID(),
		Body:      body,
		Attrs:     attrs,
		MD5Body:   md5hex(body),
		MD5Attrs:  md5Attributes(attrs),
		Sent:      now.UnixNano(),
		VisibleAt: now.Add(time.Duration(delay) * time.Second).UnixNano(),
		GroupID:   groupID,
		DedupID:   dedupID,
		SysAttrs:  sysAttrs,
		Seq:       seq,
	}
	if note != nil {
		note(m.ID)
	}
	if err := putMessage(mb, m); err != nil {
		return nil, false, err
	}
	if q.FIFO {
		if err := s.recordDedup(tx, queue, dedupKey, m); err != nil {
			return nil, false, err
		}
	}
	return m, true, nil
}

func (s *store) Purge(queue string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getQueue(tx, queue); err != nil {
			return err
		}
		if s.strict && !s.minute.purgeAllowed(queue, s.now()) {
			return errPurgeInProgress()
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

func putMessage(b *bolt.Bucket, m *message) error {
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

// queueNameMax is SQS's limit, and it counts the ".fifo" suffix — a FIFO queue
// therefore gets 75 characters of name, not 80.
const queueNameMax = 80

// validQueueName enforces the charset and length SQS documents:
// "A queue name can have up to 80 characters. Valid values: alphanumeric
// characters, hyphens (-), and underscores (_)."
//
// Hand-derived rather than generated, because SQS's own service model carries
// no @length or @pattern trait for QueueName — its constraints live only in
// prose, which is exactly why nothing had ever cross-checked them. The period
// is legal in one place only: the ".fifo" suffix, which CreateQueue checks
// against the FifoQueue attribute separately.
func validQueueName(name string) *apiError {
	body := strings.TrimSuffix(name, ".fifo")
	if len(name) > queueNameMax {
		return errInvalid("queue name is longer than " + strconv.Itoa(queueNameMax) +
			" characters (the .fifo suffix counts toward the limit)")
	}
	if body == "" {
		return errInvalid("queue name is required")
	}
	for _, r := range body {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return errInvalid("queue name may contain only alphanumeric characters, " +
				"hyphens and underscores, and got " + strconv.QuoteRune(r))
		}
	}
	return nil
}
