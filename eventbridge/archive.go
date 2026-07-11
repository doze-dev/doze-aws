package eventbridge

import (
	"encoding/binary"
	"encoding/json"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/eventpattern"
)

// Archives capture the events delivered to a bus so they can be replayed later.
// Locally this is real: PutEvents appends every matching event to the archive's
// event log, and StartReplay re-injects the windowed events back through the
// destination bus's rules. Retention is stored but not actively expired (the
// stack is ephemeral) — documented in docs/api-support/eventbridge.md.

var (
	archivesBucket      = []byte("archives")
	archiveEventsBucket = []byte("archive_events")
	replaysBucket       = []byte("replays")
)

// Archive is an event archive over one bus.
type Archive struct {
	Name           string `json:"name"`
	EventSourceArn string `json:"source_arn"` // bus ARN
	Pattern        string `json:"pattern,omitempty"`
	RetentionDays  int    `json:"retention_days,omitempty"`
	Desc           string `json:"desc,omitempty"`
	State          string `json:"state"`
	CreationTime   int64  `json:"created"` // unix seconds
	EventCount     int64  `json:"event_count"`
	SizeBytes      int64  `json:"size_bytes"`
}

func (a *Archive) ARN() string { return awsident.ARN("events", "archive/"+a.Name) }

// Replay is a completed (local replays run synchronously) archive replay.
type Replay struct {
	Name           string   `json:"name"`
	EventSourceArn string   `json:"archive_arn"` // archive ARN
	DestinationArn string   `json:"dest_arn"`    // bus ARN
	FilterArns     []string `json:"filter_arns,omitempty"`
	EventStartTime int64    `json:"event_start"`
	EventEndTime   int64    `json:"event_end"`
	State          string   `json:"state"`
	StateReason    string   `json:"state_reason,omitempty"`
	StartTime      int64    `json:"start"`
	EndTime        int64    `json:"end"`
	LastEventTime  int64    `json:"last_event"`
}

func (r *Replay) ARN() string { return awsident.ARN("events", "replay/"+r.Name) }

// storedEvent is one archived event with its ingestion time.
type storedEvent struct {
	Time  int64           `json:"t"`
	Event json.RawMessage `json:"e"`
}

// busFromArn extracts the bus name from an event-bus ARN, or returns the input
// unchanged if it is already a bare name.
func busFromArn(arn string) string {
	if i := strings.Index(arn, ":event-bus/"); i >= 0 {
		return arn[i+len(":event-bus/"):]
	}
	return arn
}

// ---- archive store ----

// CreateArchive registers an archive over an existing bus.
func (s *Store) CreateArchive(a Archive) error {
	bus := busFromArn(a.EventSourceArn)
	if a.Pattern != "" {
		if _, err := eventpattern.Parse([]byte(a.Pattern)); err != nil {
			return awshttp.Errf(400, "InvalidEventPatternException", "%v", err)
		}
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if !s.busExists(tx, bus) {
			return awshttp.Errf(400, "ResourceNotFoundException", "event bus %s does not exist", bus)
		}
		b, err := tx.CreateBucketIfNotExists(archivesBucket)
		if err != nil {
			return err
		}
		if b.Get([]byte(a.Name)) != nil {
			return awshttp.Errf(400, "ResourceAlreadyExistsException", "archive %s already exists", a.Name)
		}
		raw, _ := json.Marshal(a)
		return b.Put([]byte(a.Name), raw)
	})
}

// GetArchive loads one archive.
func (s *Store) GetArchive(name string) (*Archive, error) {
	var out *Archive
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(archivesBucket)
		if b == nil {
			return errArchiveNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errArchiveNotFound(name)
		}
		var a Archive
		if err := json.Unmarshal(raw, &a); err != nil {
			return err
		}
		out = &a
		return nil
	})
	return out, err
}

func errArchiveNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceNotFoundException", "Archive %s does not exist", name)
}

// ListArchives returns archives filtered by name prefix and/or source ARN, sorted.
func (s *Store) ListArchives(namePrefix, sourceArn string) ([]Archive, error) {
	var out []Archive
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(archivesBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var a Archive
			if json.Unmarshal(raw, &a) != nil {
				return nil
			}
			if namePrefix != "" && !strings.HasPrefix(a.Name, namePrefix) {
				return nil
			}
			if sourceArn != "" && a.EventSourceArn != sourceArn {
				return nil
			}
			out = append(out, a)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// UpdateArchive applies fn to an archive.
func (s *Store) UpdateArchive(name string, fn func(*Archive) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(archivesBucket)
		if b == nil {
			return errArchiveNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errArchiveNotFound(name)
		}
		var a Archive
		if err := json.Unmarshal(raw, &a); err != nil {
			return err
		}
		if err := fn(&a); err != nil {
			return err
		}
		nraw, _ := json.Marshal(a)
		return b.Put([]byte(name), nraw)
	})
}

// DeleteArchive removes an archive and its event log.
func (s *Store) DeleteArchive(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket(archivesBucket); b != nil {
			_ = b.Delete([]byte(name))
		}
		if eb := tx.Bucket(archiveEventsBucket); eb != nil {
			prefix := []byte(name + "\x00")
			c := eb.Cursor()
			var stale [][]byte
			for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
				stale = append(stale, append([]byte(nil), k...))
			}
			for _, k := range stale {
				_ = eb.Delete(k)
			}
		}
		return nil
	})
}

// AppendArchiveEvent stores one event under an archive and bumps its counters.
func (s *Store) AppendArchiveEvent(name string, t int64, eventJSON []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		ab := tx.Bucket(archivesBucket)
		if ab == nil || ab.Get([]byte(name)) == nil {
			return nil // archive gone between list and append — drop silently
		}
		eb, err := tx.CreateBucketIfNotExists(archiveEventsBucket)
		if err != nil {
			return err
		}
		seq, _ := eb.NextSequence()
		key := make([]byte, len(name)+1+8)
		copy(key, name)
		key[len(name)] = 0
		binary.BigEndian.PutUint64(key[len(name)+1:], seq)
		raw, _ := json.Marshal(storedEvent{Time: t, Event: append(json.RawMessage(nil), eventJSON...)})
		if err := eb.Put(key, raw); err != nil {
			return err
		}
		// Bump counters on the archive record.
		var a Archive
		if json.Unmarshal(ab.Get([]byte(name)), &a) != nil {
			return nil
		}
		a.EventCount++
		a.SizeBytes += int64(len(eventJSON))
		nraw, _ := json.Marshal(a)
		return ab.Put([]byte(name), nraw)
	})
}

// ReplayEvents calls fn for each archived event whose ingestion time is within
// [start, end] (inclusive; a zero bound is open). Returns the count and the
// latest event time seen.
func (s *Store) ReplayEvents(name string, start, end int64, fn func(eventJSON []byte)) (int64, int64, error) {
	var count, last int64
	err := s.db.View(func(tx *bolt.Tx) error {
		eb := tx.Bucket(archiveEventsBucket)
		if eb == nil {
			return nil
		}
		prefix := []byte(name + "\x00")
		c := eb.Cursor()
		for k, raw := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, raw = c.Next() {
			var ev storedEvent
			if json.Unmarshal(raw, &ev) != nil {
				continue
			}
			if start != 0 && ev.Time < start {
				continue
			}
			if end != 0 && ev.Time > end {
				continue
			}
			fn(ev.Event)
			count++
			if ev.Time > last {
				last = ev.Time
			}
		}
		return nil
	})
	return count, last, err
}

// ---- replay store ----

// PutReplay stores a replay record.
func (s *Store) PutReplay(r Replay) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(replaysBucket)
		if err != nil {
			return err
		}
		if b.Get([]byte(r.Name)) != nil {
			return awshttp.Errf(400, "ResourceAlreadyExistsException", "replay %s already exists", r.Name)
		}
		raw, _ := json.Marshal(r)
		return b.Put([]byte(r.Name), raw)
	})
}

// GetReplay loads one replay.
func (s *Store) GetReplay(name string) (*Replay, error) {
	var out *Replay
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(replaysBucket)
		if b == nil {
			return errReplayNotFound(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errReplayNotFound(name)
		}
		var r Replay
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		out = &r
		return nil
	})
	return out, err
}

func errReplayNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceNotFoundException", "Replay %s does not exist", name)
}

// ListReplays returns replays filtered by name prefix, sorted.
func (s *Store) ListReplays(namePrefix string) ([]Replay, error) {
	var out []Replay
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(replaysBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var r Replay
			if json.Unmarshal(raw, &r) == nil {
				if namePrefix == "" || strings.HasPrefix(r.Name, namePrefix) {
					out = append(out, r)
				}
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}
