package kinesis

// The bbolt-backed store: stream/shard/record schema, stream lifecycle,
// partition-key routing and the append path. Resharding lives in reshard.go;
// iterator cursors in iterator.go.

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Defaults and limits matching AWS Kinesis.
const (
	defRetentionHours = 24
	minRetentionHours = 24
	maxRetentionHours = 8760 // 365 days
	maxRecordSize     = 1 << 20
	maxPutRecords     = 500
	maxGetRecords     = 10000
	// maxGetBytes caps one GetRecords response the way AWS does (10 MiB).
	maxGetBytes = 10 << 20

	modeProvisioned = "PROVISIONED"
	modeOnDemand    = "ON_DEMAND"
)

// metaBucket holds stream definitions; per-shard record buckets are named
// recBucket(stream, shard) and consumers live in consumerBucket.
var (
	metaBucket     = []byte("streams")
	consumerBucket = []byte("consumers")
)

func recBucket(stream, shard string) []byte { return []byte("r:" + stream + ":" + shard) }

// maxHash is 2^128-1, the top of the Kinesis partition hash space. Every
// stream's shards tile [0, maxHash] without gaps.
var maxHash = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))

// Shard is one shard of a stream. Hash bounds are inclusive decimal strings,
// matching the wire format so DescribeStream can echo them unchanged.
//
// A shard is "closed" once it has been split or merged: it keeps its records
// and its EndSeq, consumers drain it to the end and then move to the children.
// That parent/child lineage is what preserves per-partition-key ordering across
// a reshard, so it is modelled properly rather than faked.
type Shard struct {
	ID         string `json:"id"`
	StartHash  string `json:"start_hash"`
	EndHash    string `json:"end_hash"`
	ParentID   string `json:"parent,omitempty"`
	AdjacentID string `json:"adjacent,omitempty"` // second parent, merges only
	StartSeq   uint64 `json:"start_seq"`
	EndSeq     uint64 `json:"end_seq,omitempty"` // 0 while open
	Closed     bool   `json:"closed,omitempty"`
}

// Stream is a stream's durable definition. Shards are held inline: a local
// stream has tens of shards at most, and keeping them with the stream makes
// every reshard a single atomic write.
type Stream struct {
	Name           string            `json:"name"`
	Created        int64             `json:"created"` // unix seconds
	RetentionHours int               `json:"retention_hours"`
	Mode           string            `json:"mode"`
	Shards         []Shard           `json:"shards"`
	NextShardNum   int               `json:"next_shard_num"`
	NextSeq        uint64            `json:"next_seq"`
	Tags           map[string]string `json:"tags,omitempty"`
	// Encryption round-trips StartStreamEncryption; records are stored under
	// the data directory either way, so this is configuration, not crypto.
	EncryptionType string `json:"encryption_type,omitempty"`
	KeyID          string `json:"key_id,omitempty"`
	// EnhancedMetrics round-trips EnableEnhancedMonitoring; there is no
	// CloudWatch locally to publish them to.
	EnhancedMetrics []string `json:"enhanced_metrics,omitempty"`
	ResourcePolicy  string   `json:"resource_policy,omitempty"`
}

// OpenShards returns the shards still accepting writes, in shard order.
func (s *Stream) OpenShards() []Shard {
	var out []Shard
	for _, sh := range s.Shards {
		if !sh.Closed {
			out = append(out, sh)
		}
	}
	return out
}

// shard returns the shard with the given id.
func (s *Stream) shard(id string) (*Shard, bool) {
	for i := range s.Shards {
		if s.Shards[i].ID == id {
			return &s.Shards[i], true
		}
	}
	return nil, false
}

// Record is one stored record.
type Record struct {
	Seq             uint64 `json:"seq"`
	PartitionKey    string `json:"pk"`
	ExplicitHashKey string `json:"ehk,omitempty"`
	Data            []byte `json:"data"`
	ArrivedNs       int64  `json:"t"`
}

// Consumer is an enhanced fan-out consumer registration. The registration is
// real (SDKs list and describe it); only the SubscribeToShard data plane, which
// needs HTTP/2 event-stream framing, is refused.
type Consumer struct {
	Name      string `json:"name"`
	StreamARN string `json:"stream_arn"`
	ARN       string `json:"arn"`
	Created   int64  `json:"created"`
	Status    string `json:"status"`
}

// Store is the bbolt-backed Kinesis state.
type Store struct {
	db     *bolt.DB
	clock  func() time.Time
	notify *notifier
}

func newStore(db *bolt.DB) *Store {
	return &Store{db: db, clock: time.Now, notify: newNotifier()}
}

func (s *Store) now() time.Time { return s.clock() }

// ---- stream lifecycle ----

func (s *Store) getStream(tx *bolt.Tx, name string) (*Stream, error) {
	b := tx.Bucket(metaBucket)
	if b == nil {
		return nil, errNoStream(name)
	}
	raw := b.Get([]byte(name))
	if raw == nil {
		return nil, errNoStream(name)
	}
	var st Stream
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Store) putStream(tx *bolt.Tx, st *Stream) error {
	b, err := tx.CreateBucketIfNotExists(metaBucket)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return b.Put([]byte(st.Name), raw)
}

// Get returns a stream by name.
func (s *Store) Get(name string) (*Stream, error) {
	var out *Stream
	err := s.db.View(func(tx *bolt.Tx) error {
		st, err := s.getStream(tx, name)
		out = st
		return err
	})
	return out, err
}

// Create makes a new stream with shardCount initial shards tiling the hash
// space. Re-creating an existing stream is a ResourceInUseException, matching
// AWS (unlike SQS, CreateStream is not idempotent).
func (s *Store) Create(name string, shardCount int, mode string) (*Stream, error) {
	if err := validStreamName(name); err != nil {
		return nil, err
	}
	if mode == "" {
		mode = modeProvisioned
	}
	if mode != modeProvisioned && mode != modeOnDemand {
		return nil, errInvalid("StreamModeDetails.StreamMode must be %s or %s", modeProvisioned, modeOnDemand)
	}
	// On-demand streams start at 4 shards in AWS; provisioned honour the count.
	if mode == modeOnDemand {
		shardCount = 4
	} else if shardCount < 1 {
		return nil, errInvalid("ShardCount must be at least 1")
	}

	var out *Stream
	err := s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getStream(tx, name); err == nil {
			return errInUse("stream %s already exists", name)
		}
		st := &Stream{
			Name:           name,
			Created:        s.now().Unix(),
			RetentionHours: defRetentionHours,
			Mode:           mode,
			NextSeq:        1,
		}
		st.Shards = tileShards(st, shardCount)
		out = st
		return s.putStream(tx, st)
	})
	return out, err
}

// tileShards builds shardCount shards splitting [0, maxHash] evenly, assigning
// ids from the stream's shard counter.
func tileShards(st *Stream, shardCount int) []Shard {
	shards := make([]Shard, 0, shardCount)
	n := big.NewInt(int64(shardCount))
	span := new(big.Int).Div(new(big.Int).Add(maxHash, big.NewInt(1)), n)
	lo := big.NewInt(0)
	for i := 0; i < shardCount; i++ {
		hi := new(big.Int).Sub(new(big.Int).Add(lo, span), big.NewInt(1))
		if i == shardCount-1 {
			hi = new(big.Int).Set(maxHash) // last shard absorbs the remainder
		}
		shards = append(shards, Shard{
			ID:        shardID(st.NextShardNum),
			StartHash: lo.String(),
			EndHash:   hi.String(),
			StartSeq:  st.NextSeq,
		})
		st.NextShardNum++
		lo = new(big.Int).Add(hi, big.NewInt(1))
	}
	return shards
}

// Delete removes a stream and every record bucket it owns.
func (s *Store) Delete(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		st, err := s.getStream(tx, name)
		if err != nil {
			return err
		}
		for _, sh := range st.Shards {
			_ = tx.DeleteBucket(recBucket(name, sh.ID))
		}
		if cb := tx.Bucket(consumerBucket); cb != nil {
			arn := streamARN(name)
			c := cb.Cursor()
			var kill [][]byte
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var cs Consumer
				if json.Unmarshal(v, &cs) == nil && cs.StreamARN == arn {
					kill = append(kill, append([]byte(nil), k...))
				}
			}
			for _, k := range kill {
				_ = cb.Delete(k)
			}
		}
		return tx.Bucket(metaBucket).Delete([]byte(name))
	})
}

// List returns stream names in lexical order, starting after exclusiveStart.
func (s *Store) List(exclusiveStart string, limit int) ([]string, bool, error) {
	var names []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(metaBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			if exclusiveStart == "" || string(k) > exclusiveStart {
				names = append(names, string(k))
			}
			return nil
		})
	})
	sort.Strings(names)
	more := false
	if limit > 0 && len(names) > limit {
		names, more = names[:limit], true
	}
	return names, more, err
}

// Update applies fn to a stream inside a write transaction.
func (s *Store) Update(name string, fn func(*Stream) error) (*Stream, error) {
	var out *Stream
	err := s.db.Update(func(tx *bolt.Tx) error {
		st, err := s.getStream(tx, name)
		if err != nil {
			return err
		}
		if err := fn(st); err != nil {
			return err
		}
		out = st
		return s.putStream(tx, st)
	})
	return out, err
}

// ---- routing and append ----

// hashOf maps a partition key onto the Kinesis hash space: the MD5 digest read
// as a 128-bit big-endian unsigned integer. This is AWS's documented rule, and
// getting it exactly right is what makes a partition key land on the same shard
// locally as it would in the cloud.
func hashOf(partitionKey string) *big.Int {
	sum := md5.Sum([]byte(partitionKey))
	return new(big.Int).SetBytes(sum[:])
}

// shardFor picks the open shard whose hash range contains key. An explicit hash
// key overrides the partition key, as in AWS.
func shardFor(st *Stream, partitionKey, explicitHashKey string) (*Shard, error) {
	h := hashOf(partitionKey)
	if explicitHashKey != "" {
		v, ok := new(big.Int).SetString(explicitHashKey, 10)
		if !ok || v.Sign() < 0 || v.Cmp(maxHash) > 0 {
			return nil, errInvalid("ExplicitHashKey %q is not in [0, 2^128-1]", explicitHashKey)
		}
		h = v
	}
	for i := range st.Shards {
		sh := &st.Shards[i]
		if sh.Closed {
			continue
		}
		lo, _ := new(big.Int).SetString(sh.StartHash, 10)
		hi, _ := new(big.Int).SetString(sh.EndHash, 10)
		if h.Cmp(lo) >= 0 && h.Cmp(hi) <= 0 {
			return sh, nil
		}
	}
	return nil, errInvalid("no open shard covers hash %s", h)
}

// PutEntry is one record to append.
type PutEntry struct {
	PartitionKey    string
	ExplicitHashKey string
	Data            []byte
}

// PutResult is where an appended record landed.
type PutResult struct {
	ShardID string
	Seq     uint64
}

// Put appends entries to the stream, routing each by partition key. Every
// entry is written in one transaction so a PutRecords batch is atomic — AWS
// allows partial failure, but locally there is no throttling to cause one, and
// atomicity is the more useful guarantee.
func (s *Store) Put(stream string, entries []PutEntry) ([]PutResult, error) {
	if len(entries) == 0 {
		return nil, errInvalid("at least one record is required")
	}
	if len(entries) > maxPutRecords {
		return nil, errInvalid("a PutRecords request supports at most %d records", maxPutRecords)
	}
	results := make([]PutResult, len(entries))
	err := s.db.Update(func(tx *bolt.Tx) error {
		st, err := s.getStream(tx, stream)
		if err != nil {
			return err
		}
		now := s.now().UnixNano()
		for i, e := range entries {
			if e.PartitionKey == "" {
				return errInvalid("record %d: PartitionKey is required", i)
			}
			if len(e.PartitionKey) > 256 {
				return errInvalid("record %d: PartitionKey may be at most 256 characters", i)
			}
			if len(e.Data) > maxRecordSize {
				return errInvalid("record %d: data of %d bytes exceeds the %d byte limit", i, len(e.Data), maxRecordSize)
			}
			sh, err := shardFor(st, e.PartitionKey, e.ExplicitHashKey)
			if err != nil {
				return err
			}
			b, err := tx.CreateBucketIfNotExists(recBucket(stream, sh.ID))
			if err != nil {
				return err
			}
			rec := Record{
				Seq:             st.NextSeq,
				PartitionKey:    e.PartitionKey,
				ExplicitHashKey: e.ExplicitHashKey,
				Data:            e.Data,
				ArrivedNs:       now,
			}
			st.NextSeq++
			raw, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			if err := b.Put(seqKey(rec.Seq), raw); err != nil {
				return err
			}
			results[i] = PutResult{ShardID: sh.ID, Seq: rec.Seq}
		}
		return s.putStream(tx, st)
	})
	if err == nil {
		s.notify.signal(stream)
	}
	return results, err
}

// ---- reads ----

// Fetch returns up to limit records from a shard with sequence > after,
// stopping early at the response byte ceiling. next is the sequence to resume
// from; behind reports the age of the last record returned.
func (s *Store) Fetch(stream, shard string, after uint64, limit int) (recs []Record, next uint64, behind time.Duration, err error) {
	if limit <= 0 || limit > maxGetRecords {
		limit = maxGetRecords
	}
	next = after
	err = s.db.View(func(tx *bolt.Tx) error {
		st, err := s.getStream(tx, stream)
		if err != nil {
			return err
		}
		sh, ok := st.shard(shard)
		if !ok {
			return errNoShard(shard, stream)
		}
		b := tx.Bucket(recBucket(stream, shard))
		if b == nil {
			return nil
		}
		bytes := 0
		c := b.Cursor()
		for k, v := c.Seek(seqKey(after + 1)); k != nil; k, v = c.Next() {
			if len(recs) >= limit || bytes+len(v) > maxGetBytes {
				break
			}
			var rec Record
			if err := json.Unmarshal(v, &rec); err != nil {
				return err
			}
			// A closed shard stops at its ending sequence; its children carry
			// on from there.
			if sh.Closed && sh.EndSeq != 0 && rec.Seq > sh.EndSeq {
				break
			}
			recs = append(recs, rec)
			bytes += len(v)
			next = rec.Seq
		}
		if len(recs) > 0 {
			behind = s.now().Sub(time.Unix(0, recs[len(recs)-1].ArrivedNs))
		}
		return nil
	})
	return recs, next, behind, err
}

// SeqAtOrAfter returns the sequence to start from for an AT_TIMESTAMP
// iterator: the record before the first one at or after ts.
func (s *Store) SeqAtOrAfter(stream, shard string, ts time.Time) (uint64, error) {
	var after uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(recBucket(stream, shard))
		if b == nil {
			return nil
		}
		target := ts.UnixNano()
		return b.ForEach(func(_, v []byte) error {
			var rec Record
			if err := json.Unmarshal(v, &rec); err != nil {
				return err
			}
			if rec.ArrivedNs < target {
				after = rec.Seq
			}
			return nil
		})
	})
	return after, err
}

// LatestSeq returns the highest sequence written to a shard, the starting
// point for a LATEST iterator.
func (s *Store) LatestSeq(stream, shard string) (uint64, error) {
	var last uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(recBucket(stream, shard))
		if b == nil {
			return nil
		}
		if k, _ := b.Cursor().Last(); k != nil {
			last = binary.BigEndian.Uint64(k)
		}
		return nil
	})
	return last, err
}

// TrimHorizonSeq returns the sequence just below the oldest surviving record,
// so a TRIM_HORIZON iterator skips whatever retention has already reclaimed.
func (s *Store) TrimHorizonSeq(stream, shard string) (uint64, error) {
	var first uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(recBucket(stream, shard))
		if b == nil {
			return nil
		}
		if k, _ := b.Cursor().First(); k != nil {
			first = binary.BigEndian.Uint64(k)
		}
		return nil
	})
	if first > 0 {
		first-- // exclusive cursor: the first record must still be delivered
	}
	return first, err
}

// ---- retention ----

// Sweep drops records past their stream's retention window and removes closed
// shards whose records are all gone. Returns the number of records reclaimed.
func (s *Store) Sweep() int {
	n := 0
	_ = s.db.Update(func(tx *bolt.Tx) error {
		mb := tx.Bucket(metaBucket)
		if mb == nil {
			return nil
		}
		var streams []*Stream
		if err := mb.ForEach(func(_, v []byte) error {
			var st Stream
			if err := json.Unmarshal(v, &st); err != nil {
				return nil // skip unreadable entries rather than stall the sweep
			}
			streams = append(streams, &st)
			return nil
		}); err != nil {
			return err
		}
		for _, st := range streams {
			cutoff := s.now().Add(-time.Duration(st.RetentionHours) * time.Hour).UnixNano()
			changed := false
			var live []Shard
			for _, sh := range st.Shards {
				b := tx.Bucket(recBucket(st.Name, sh.ID))
				if b != nil {
					var kill [][]byte
					_ = b.ForEach(func(k, v []byte) error {
						var rec Record
						if json.Unmarshal(v, &rec) == nil && rec.ArrivedNs < cutoff {
							kill = append(kill, append([]byte(nil), k...))
						}
						return nil
					})
					for _, k := range kill {
						_ = b.Delete(k)
						n++
					}
				}
				// A closed shard with nothing left to read is finished: drop it
				// so ListShards stops advertising a dead branch.
				if sh.Closed {
					if b == nil || b.Stats().KeyN == 0 {
						_ = tx.DeleteBucket(recBucket(st.Name, sh.ID))
						changed = true
						continue
					}
				}
				live = append(live, sh)
			}
			if changed {
				st.Shards = live
				_ = s.putStream(tx, st)
			}
		}
		return nil
	})
	return n
}

// ---- consumers ----

func consumerKey(streamARN, name string) []byte { return []byte(streamARN + "|" + name) }

// RegisterConsumer registers an enhanced fan-out consumer.
func (s *Store) RegisterConsumer(streamARN, name string) (*Consumer, error) {
	if name == "" {
		return nil, errInvalid("ConsumerName is required")
	}
	stream, err := streamFromARN(streamARN)
	if err != nil {
		return nil, err
	}
	var out *Consumer
	err = s.db.Update(func(tx *bolt.Tx) error {
		if _, err := s.getStream(tx, stream); err != nil {
			return err
		}
		b, err := tx.CreateBucketIfNotExists(consumerBucket)
		if err != nil {
			return err
		}
		if b.Get(consumerKey(streamARN, name)) != nil {
			return errInUse("consumer %s is already registered on %s", name, stream)
		}
		now := s.now()
		c := &Consumer{
			Name:      name,
			StreamARN: streamARN,
			ARN:       streamARN + "/consumer/" + name + ":" + itoa64(now.Unix()),
			Created:   now.Unix(),
			Status:    "ACTIVE",
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		out = c
		return b.Put(consumerKey(streamARN, name), raw)
	})
	return out, err
}

// FindConsumer resolves a consumer by (streamARN, name) or by consumer ARN.
func (s *Store) FindConsumer(streamARN, name, consumerARN string) (*Consumer, error) {
	var out *Consumer
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(consumerBucket)
		if b == nil {
			return errNoConsumer(name)
		}
		if consumerARN != "" {
			return b.ForEach(func(_, v []byte) error {
				var c Consumer
				if json.Unmarshal(v, &c) == nil && c.ARN == consumerARN {
					out = &c
				}
				return nil
			})
		}
		raw := b.Get(consumerKey(streamARN, name))
		if raw == nil {
			return errNoConsumer(name)
		}
		var c Consumer
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		out = &c
		return nil
	})
	if err == nil && out == nil {
		return nil, errNoConsumer(name)
	}
	return out, err
}

// DeleteConsumer deregisters a consumer.
func (s *Store) DeleteConsumer(c *Consumer) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(consumerBucket)
		if b == nil {
			return errNoConsumer(c.Name)
		}
		return b.Delete(consumerKey(c.StreamARN, c.Name))
	})
}

// ListConsumers returns every consumer registered on a stream.
func (s *Store) ListConsumers(streamARN string) ([]Consumer, error) {
	var out []Consumer
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(consumerBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var c Consumer
			if json.Unmarshal(v, &c) == nil && c.StreamARN == streamARN {
				out = append(out, c)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// ---- helpers ----

func seqKey(seq uint64) []byte {
	var k [8]byte
	binary.BigEndian.PutUint64(k[:], seq)
	return k[:]
}

// validStreamName enforces the AWS naming rule so a name that works locally
// works in the cloud.
func validStreamName(name string) error {
	if name == "" {
		return errInvalid("StreamName is required")
	}
	if len(name) > 128 {
		return errInvalid("StreamName may be at most 128 characters")
	}
	for _, r := range name {
		ok := r == '_' || r == '.' || r == '-' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return errInvalid("StreamName may contain only letters, digits, '_', '.' and '-'")
		}
	}
	return nil
}

// streamFromARN extracts the stream name from arn:aws:kinesis:...:stream/<name>.
func streamFromARN(arn string) (string, error) {
	i := strings.Index(arn, ":stream/")
	if i < 0 {
		return "", errInvalid("%q is not a Kinesis stream ARN", arn)
	}
	name := arn[i+len(":stream/"):]
	if j := strings.Index(name, "/"); j >= 0 {
		name = name[:j] // a consumer ARN carries /consumer/<name>:<ts>
	}
	if name == "" {
		return "", errInvalid("%q is not a Kinesis stream ARN", arn)
	}
	return name, nil
}
