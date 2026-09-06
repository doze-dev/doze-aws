package logs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// The log store: groups, streams, and events keyed by time.
//
// Events live in one bucket per group, keyed timestamp first, then stream,
// then a store-wide sequence. FilterLogEvents interleaved across a group's
// streams — what `aws logs tail` and `sam logs` call, every few seconds — is
// then one cursor range; GetLogEvents on a single stream scans the same
// range and keeps its stream, which at local volumes is a non-cost. The
// sequence makes every key unique and every eventId stable.

var (
	bucketGroups  = []byte("groups")
	bucketStreams = []byte("streams") // group \x00 stream → Stream
	bucketEvents  = []byte("events")  // nested: one bucket per group
	bucketMeta    = []byte("meta")    // "seq" → last sequence
)

// Group is a log group.
type Group struct {
	Name          string            `json:"name"`
	CreatedMs     int64             `json:"created"`
	RetentionDays int               `json:"retention,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
}

// Stream is a log stream inside a group.
type Stream struct {
	Group        string `json:"group"`
	Name         string `json:"name"`
	CreatedMs    int64  `json:"created"`
	FirstMs      int64  `json:"first,omitempty"`
	LastMs       int64  `json:"last,omitempty"`
	LastIngestMs int64  `json:"ingest,omitempty"`
}

// Event is one log line. RequestID is the doze extension carried by Lambda's
// PutLogEvents so a console can show one invocation without a pattern.
type Event struct {
	TS        int64  `json:"t"`
	Ingest    int64  `json:"i"`
	Msg       string `json:"m"`
	RequestID string `json:"r,omitempty"`
}

// Stored is an event with the identity the read APIs answer.
type Stored struct {
	Event
	Stream string
	Seq    int64
}

// ID is the eventId: timestamp and sequence, digits only, unique.
func (s Stored) ID() string { return fmt.Sprintf("%d%010d", s.TS, s.Seq) }

// Key is the bucket key: the cursor a page continues from.
func (s Stored) Key() []byte { return eventKey(s.TS, s.Stream, s.Seq) }

func eventKey(ts int64, stream string, seq int64) []byte {
	return []byte(fmt.Sprintf("%013d\x00%s\x00%010d", ts, stream, seq))
}

func streamKey(group, stream string) []byte { return []byte(group + "\x00" + stream) }

// Store is the bbolt-backed log store.
type Store struct {
	db    *bolt.DB
	clock func() time.Time
}

func newStore(db *bolt.DB) (*Store, error) {
	err := db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketGroups, bucketStreams, bucketEvents, bucketMeta} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Store{db: db, clock: time.Now}, nil
}

func (s *Store) now() int64 { return s.clock().UnixMilli() }

// ---- groups ----

func (s *Store) PutGroup(g Group) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		raw, _ := json.Marshal(g)
		return tx.Bucket(bucketGroups).Put([]byte(g.Name), raw)
	})
}

func (s *Store) GetGroup(name string) (*Group, error) {
	var g *Group
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketGroups).Get([]byte(name))
		if raw == nil {
			return nil
		}
		g = &Group{}
		return json.Unmarshal(raw, g)
	})
	return g, err
}

// DeleteGroup removes the group, its streams and its events.
func (s *Store) DeleteGroup(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketGroups).Delete([]byte(name)); err != nil {
			return err
		}
		sb := tx.Bucket(bucketStreams)
		c := sb.Cursor()
		prefix := streamKey(name, "")
		var keys [][]byte
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			keys = append(keys, append([]byte(nil), k...))
		}
		for _, k := range keys {
			if err := sb.Delete(k); err != nil {
				return err
			}
		}
		if tx.Bucket(bucketEvents).Bucket([]byte(name)) != nil {
			return tx.Bucket(bucketEvents).DeleteBucket([]byte(name))
		}
		return nil
	})
}

// ListGroups answers groups by name prefix, sorted, as DescribeLogGroups
// does.
func (s *Store) ListGroups(prefix string) ([]Group, error) {
	var out []Group
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketGroups).Cursor()
		for k, v := c.Seek([]byte(prefix)); k != nil && bytes.HasPrefix(k, []byte(prefix)); k, v = c.Next() {
			var g Group
			if err := json.Unmarshal(v, &g); err != nil {
				return err
			}
			out = append(out, g)
		}
		return nil
	})
	return out, err
}

// ---- streams ----

func (s *Store) PutStream(st Stream) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		raw, _ := json.Marshal(st)
		return tx.Bucket(bucketStreams).Put(streamKey(st.Group, st.Name), raw)
	})
}

func (s *Store) GetStream(group, name string) (*Stream, error) {
	var st *Stream
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketStreams).Get(streamKey(group, name))
		if raw == nil {
			return nil
		}
		st = &Stream{}
		return json.Unmarshal(raw, st)
	})
	return st, err
}

// DeleteStream removes a stream and its events.
func (s *Store) DeleteStream(group, name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketStreams).Delete(streamKey(group, name)); err != nil {
			return err
		}
		eb := tx.Bucket(bucketEvents).Bucket([]byte(group))
		if eb == nil {
			return nil
		}
		var keys [][]byte
		c := eb.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if streamOfKey(k) == name {
				keys = append(keys, append([]byte(nil), k...))
			}
		}
		for _, k := range keys {
			if err := eb.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListStreams answers a group's streams by name prefix.
func (s *Store) ListStreams(group, prefix string) ([]Stream, error) {
	var out []Stream
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketStreams).Cursor()
		p := streamKey(group, prefix)
		for k, v := c.Seek(p); k != nil && bytes.HasPrefix(k, p); k, v = c.Next() {
			var st Stream
			if err := json.Unmarshal(v, &st); err != nil {
				return err
			}
			out = append(out, st)
		}
		return nil
	})
	return out, err
}

// ---- events ----

// PutEvents appends a batch to a stream, creating the stream row if the
// group exists and the stream does not — which is what a Lambda's first
// line does. It returns ErrNoGroup when the group is unknown.
func (s *Store) PutEvents(group, stream string, events []Event) error {
	now := s.now()
	return s.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketGroups).Get([]byte(group)) == nil {
			return ErrNoGroup
		}
		sb := tx.Bucket(bucketStreams)
		var st Stream
		if raw := sb.Get(streamKey(group, stream)); raw != nil {
			_ = json.Unmarshal(raw, &st)
		} else {
			st = Stream{Group: group, Name: stream, CreatedMs: now}
		}
		eb, err := tx.Bucket(bucketEvents).CreateBucketIfNotExists([]byte(group))
		if err != nil {
			return err
		}
		mb := tx.Bucket(bucketMeta)
		seq := int64(0)
		if raw := mb.Get([]byte("seq")); raw != nil {
			fmt.Sscan(string(raw), &seq)
		}
		sorted := append([]Event(nil), events...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].TS < sorted[j].TS })
		for _, ev := range sorted {
			seq++
			ev.Ingest = now
			raw, _ := json.Marshal(ev)
			if err := eb.Put(eventKey(ev.TS, stream, seq), raw); err != nil {
				return err
			}
			if st.FirstMs == 0 || ev.TS < st.FirstMs {
				st.FirstMs = ev.TS
			}
			if ev.TS > st.LastMs {
				st.LastMs = ev.TS
			}
		}
		st.LastIngestMs = now
		if err := mb.Put([]byte("seq"), []byte(fmt.Sprint(seq))); err != nil {
			return err
		}
		raw, _ := json.Marshal(st)
		return sb.Put(streamKey(group, stream), raw)
	})
}

// Scan walks a group's events in time order between from and to (inclusive,
// 0 = open), after the cursor key (exclusive), keeping those whose stream
// and message the predicates accept, up to limit. It returns the page and
// whether more remain past it. backward walks newest-first.
func (s *Store) Scan(group string, from, to int64, after []byte, limit int, backward bool,
	keep func(stream string, ev Event) bool) (page []Stored, more bool, err error) {
	if limit <= 0 {
		limit = 10000
	}
	err = s.db.View(func(tx *bolt.Tx) error {
		eb := tx.Bucket(bucketEvents).Bucket([]byte(group))
		if eb == nil {
			return nil
		}
		c := eb.Cursor()
		lo := []byte(fmt.Sprintf("%013d", from))
		hi := []byte(fmt.Sprintf("%013d\xff", to))
		if to == 0 {
			hi = []byte("\xff")
		}
		var k, v []byte
		if backward {
			k, v = c.Seek(hi)
			if k == nil {
				k, v = c.Last()
			} else {
				k, v = c.Prev()
			}
			if after != nil {
				k, v = c.Seek(after)
				k, v = c.Prev()
			}
		} else {
			k, v = c.Seek(lo)
			if after != nil {
				k, v = c.Seek(after)
				if bytes.Equal(k, after) {
					k, v = c.Next()
				}
			}
		}
		for k != nil {
			if backward {
				if bytes.Compare(k, lo) < 0 {
					break
				}
			} else if bytes.Compare(k, hi) > 0 {
				break
			}
			stream := streamOfKey(k)
			var ev Event
			if err := json.Unmarshal(v, &ev); err == nil && keep(stream, ev) {
				if len(page) == limit {
					more = true
					break
				}
				page = append(page, Stored{Event: ev, Stream: stream, Seq: seqOfKey(k)})
			}
			if backward {
				k, v = c.Prev()
			} else {
				k, v = c.Next()
			}
		}
		return nil
	})
	return page, more, err
}

func streamOfKey(k []byte) string {
	parts := strings.SplitN(string(k), "\x00", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}

func seqOfKey(k []byte) int64 {
	i := bytes.LastIndexByte(k, 0)
	if i < 0 {
		return 0
	}
	var seq int64
	fmt.Sscan(string(k[i+1:]), &seq)
	return seq
}

// Sweep drops events past their group's retention (or the default), then
// trims each group to maxEvents oldest-first. Returns the number dropped.
func (s *Store) Sweep(defaultRetention time.Duration, maxEvents int) (int, error) {
	now := s.now()
	dropped := 0
	groups, err := s.ListGroups("")
	if err != nil {
		return 0, err
	}
	for _, g := range groups {
		cutoff := now - defaultRetention.Milliseconds()
		if g.RetentionDays > 0 {
			cutoff = now - int64(g.RetentionDays)*24*3600*1000
		}
		err := s.db.Update(func(tx *bolt.Tx) error {
			eb := tx.Bucket(bucketEvents).Bucket([]byte(g.Name))
			if eb == nil {
				return nil
			}
			var keys [][]byte
			c := eb.Cursor()
			limit := []byte(fmt.Sprintf("%013d", cutoff))
			for k, _ := c.First(); k != nil && bytes.Compare(k, limit) < 0; k, _ = c.Next() {
				keys = append(keys, append([]byte(nil), k...))
			}
			if excess := eb.Stats().KeyN - len(keys) - maxEvents; maxEvents > 0 && excess > 0 {
				k, _ := c.Seek(limit)
				for ; k != nil && excess > 0; k, _ = c.Next() {
					keys = append(keys, append([]byte(nil), k...))
					excess--
				}
			}
			for _, k := range keys {
				if err := eb.Delete(k); err != nil {
					return err
				}
			}
			dropped += len(keys)
			return nil
		})
		if err != nil {
			return dropped, err
		}
	}
	return dropped, nil
}
