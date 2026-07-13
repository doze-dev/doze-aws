package store

// DynamoDB Streams: a per-table ordered change log captured at the single write
// choke point (writeItem). Records are stored in a bbolt bucket keyed by a
// monotonic sequence number, bounded to a recent window. The dynamodb service
// reads them for the Streams API (ListStreams/DescribeStream/GetShardIterator/
// GetRecords), and the Lambda event-source-mapping poller consumes them.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// streamMaxRecords bounds a table's retained change log (real DynamoDB retains
// 24h; a count cap keeps local memory/disk bounded and is enough for the
// consume-soon local use case).
const streamMaxRecords = 1000

func streamBucket(table string) []byte { return []byte("stream:" + table) }

// streamSpec is the decoded StreamSpecification stored on a table.
type streamSpec struct {
	StreamEnabled  bool   `json:"StreamEnabled"`
	StreamViewType string `json:"StreamViewType"`
}

// streamState reports a table's stream configuration.
func (t *Table) streamState() (viewType string, enabled bool) {
	if t.StreamSpec == "" {
		return "", false
	}
	var sp streamSpec
	if json.Unmarshal([]byte(t.StreamSpec), &sp) != nil {
		return "", false
	}
	if !sp.StreamEnabled {
		return "", false
	}
	vt := sp.StreamViewType
	if vt == "" {
		vt = "NEW_AND_OLD_IMAGES"
	}
	return vt, true
}

// StreamViewType (on Table) returns the stream view type and whether streaming
// is enabled.
func (t *Table) StreamViewType() (string, bool) { return t.streamState() }

// StreamViewType (on Store) resolves a table by name first.
func (s *Store) StreamViewType(table string) (string, bool) {
	t, err := s.GetTable(table)
	if err != nil {
		return "", false
	}
	return t.streamState()
}

// StreamLabel is the stable per-table stream label (derived from creation time),
// and StreamARN the corresponding ARN.
func (t *Table) StreamLabel() string {
	return time.Unix(t.Created, 0).UTC().Format("2006-01-02T15:04:05.000")
}
func (t *Table) StreamARN() string {
	return awsident.ARN("dynamodb", "table/"+t.Name+"/stream/"+t.StreamLabel())
}

// storedStreamRecord is the on-disk change record (both images kept; the view
// type is applied when the record is read).
type storedStreamRecord struct {
	Seq       uint64          `json:"seq"`
	EventName string          `json:"event"` // INSERT | MODIFY | REMOVE
	Keys      json.RawMessage `json:"keys"`
	Old       json.RawMessage `json:"old,omitempty"`
	New       json.RawMessage `json:"new,omitempty"`
	CreatedNs int64           `json:"created"`
	SizeBytes int             `json:"size"`
}

// StreamRecord is one change record handed to the Streams API / poller.
type StreamRecord struct {
	Seq       uint64
	EventName string
	Keys      json.RawMessage
	Old       json.RawMessage
	New       json.RawMessage
	CreatedNs int64
	SizeBytes int
}

// captureStream appends a change record when the table has streaming enabled.
// Called inside writeItem's transaction, so the record commits atomically with
// the mutation it describes.
func (s *Store) captureStream(tx *bolt.Tx, t *Table, old, new item.Item) error {
	if _, enabled := t.streamState(); !enabled {
		return nil
	}
	eventName := "MODIFY"
	switch {
	case old == nil && new != nil:
		eventName = "INSERT"
	case new == nil:
		eventName = "REMOVE"
	}
	img := new
	if img == nil {
		img = old
	}
	if img == nil {
		return nil // nothing to key on
	}

	b, err := tx.CreateBucketIfNotExists(streamBucket(t.Name))
	if err != nil {
		return err
	}
	seq, _ := b.NextSequence()
	rec := storedStreamRecord{
		Seq:       seq,
		EventName: eventName,
		Keys:      keysWire(t, img),
		CreatedNs: s.now().UnixNano(),
	}
	if old != nil {
		rec.Old = item.ItemJSON(old)
		rec.SizeBytes = item.Size(old)
	}
	if new != nil {
		rec.New = item.ItemJSON(new)
		rec.SizeBytes = item.Size(new)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("stream record marshal: %w", err)
	}
	if err := b.Put(streamKey(seq), raw); err != nil {
		return err
	}
	pruneStream(b)
	return nil
}

// pruneStream drops the oldest records beyond streamMaxRecords.
func pruneStream(b *bolt.Bucket) {
	over := b.Stats().KeyN - streamMaxRecords
	if over <= 0 {
		return
	}
	c := b.Cursor()
	for k, _ := c.First(); k != nil && over > 0; k, _ = c.Next() {
		_ = b.Delete(k)
		over--
	}
}

// StreamRecords returns records with sequence > afterSeq (afterSeq 0 = from the
// trim horizon), up to limit, plus the highest sequence currently stored.
func (s *Store) StreamRecords(table string, afterSeq uint64, limit int) ([]StreamRecord, uint64, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []StreamRecord
	var latest uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(streamBucket(table))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var rec storedStreamRecord
			if json.Unmarshal(v, &rec) != nil {
				continue
			}
			latest = rec.Seq
			if rec.Seq <= afterSeq || len(out) >= limit {
				continue
			}
			out = append(out, StreamRecord{
				Seq: rec.Seq, EventName: rec.EventName, Keys: rec.Keys,
				Old: rec.Old, New: rec.New, CreatedNs: rec.CreatedNs, SizeBytes: rec.SizeBytes,
			})
		}
		return nil
	})
	return out, latest, err
}

// LatestStreamSeq returns the highest stored sequence for a table's stream (0 if
// none) — the position a LATEST shard iterator starts after.
func (s *Store) LatestStreamSeq(table string) uint64 {
	var latest uint64
	_ = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(streamBucket(table))
		if b == nil {
			return nil
		}
		if k, _ := b.Cursor().Last(); k != nil {
			latest = binary.BigEndian.Uint64(k)
		}
		return nil
	})
	return latest
}

func streamKey(seq uint64) []byte {
	var k [8]byte
	binary.BigEndian.PutUint64(k[:], seq)
	return k[:]
}

// keysWire extracts the primary-key attributes of an image as a wire map.
func keysWire(t *Table, img item.Item) json.RawMessage {
	keys := item.Item{}
	if v, ok := img[t.Hash.Name]; ok {
		keys[t.Hash.Name] = v
	}
	if t.Range != nil {
		if v, ok := img[t.Range.Name]; ok {
			keys[t.Range.Name] = v
		}
	}
	return item.ItemJSON(keys)
}
