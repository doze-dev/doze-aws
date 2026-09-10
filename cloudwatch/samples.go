package cloudwatch

// The sample store: the observations themselves, as opposed to the catalogue
// of which metrics exist (store.go).
//
// # Raw samples, not pre-aggregated buckets
//
// AWS keeps statistics, not points: it rolls observations into periods on
// ingest and cannot answer a question at a finer grain later. Keeping the raw
// samples instead costs disk and buys two things a local emulator wants more
// than it wants that disk. Percentiles become exact rather than estimated —
// p99 over the actual observations, not over a digest — and a caller may ask
// for any period after the fact, so a developer chasing a spike can narrow
// from five minutes to one without republishing.
//
// The trade is bounded by retention: a day of samples, and a hard cap, both
// swept in the background.
//
// # The key
//
// Fixed-width and NUL-delimited, so byte order IS time order within a series
// and a range query is one cursor walk:
//
//	<series key>\x00<13-digit millis>\x00<10-digit sequence>
//
// The sequence disambiguates observations sharing a millisecond, which is
// ordinary when a loop publishes: without it the second write would replace
// the first and a count would silently be wrong.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketSamples = []byte("samples")
	bucketMeta    = []byte("meta")
)

// keySeq is the monotonic counter that separates same-millisecond samples.
var keySeq = []byte("sample_seq")

// Retention defaults. AWS's real schedule is resolution-dependent — 1-second
// data for 3 hours, 1-minute for 15 days, 1-hour for 63 days — which a local
// emulator has no reason to reproduce: nobody is doing capacity planning off
// a laptop. One flat window, documented in the ledger as a divergence.
const (
	defaultRetention  = 24 * time.Hour
	defaultMaxSamples = 1_000_000
)

// sample is one observation, stored.
type sample struct {
	Value float64
	// Count is the number of observations this record stands for. A plain
	// datum is 1; a StatisticValues datum carries the SampleCount its caller
	// summarised, so an average over it weights correctly.
	Count float64
	// Min and Max are the extremes this record stands for, which differ from
	// Value only for a StatisticValues datum.
	Min, Max float64
	Unit     string
}

// encode renders a sample compactly. Five fixed-width fields and a unit,
// rather than JSON: a million of these is the cap, so the per-record cost is
// the one that matters.
func (s sample) encode() []byte {
	buf := make([]byte, 8*4, 8*4+len(s.Unit))
	binary.BigEndian.PutUint64(buf[0:], math.Float64bits(s.Value))
	binary.BigEndian.PutUint64(buf[8:], math.Float64bits(s.Count))
	binary.BigEndian.PutUint64(buf[16:], math.Float64bits(s.Min))
	binary.BigEndian.PutUint64(buf[24:], math.Float64bits(s.Max))
	return append(buf, s.Unit...)
}

func decodeSample(raw []byte) (sample, bool) {
	if len(raw) < 32 {
		return sample{}, false
	}
	return sample{
		Value: math.Float64frombits(binary.BigEndian.Uint64(raw[0:])),
		Count: math.Float64frombits(binary.BigEndian.Uint64(raw[8:])),
		Min:   math.Float64frombits(binary.BigEndian.Uint64(raw[16:])),
		Max:   math.Float64frombits(binary.BigEndian.Uint64(raw[24:])),
		Unit:  string(raw[32:]),
	}, true
}

// sampleKey is <series>\x00<millis>\x00<seq>.
func sampleKey(series []byte, ms int64, seq uint64) []byte {
	var b bytes.Buffer
	b.Write(series)
	b.WriteByte(0)
	fmt.Fprintf(&b, "%013d", ms)
	b.WriteByte(0)
	fmt.Fprintf(&b, "%010d", seq)
	return b.Bytes()
}

// seriesRange is the key range covering one series across a time window. The
// end is exclusive of the next series because \x00 sorts below every digit.
func seriesRange(series []byte, from, to int64) (lo, hi []byte) {
	return sampleKey(series, from, 0), sampleKey(series, to, math.MaxUint32)
}

// putSamples writes one call's observations and their series records in a
// single transaction, so a PutMetricData lands whole or not at all.
func (s *Server) putSamples(data []datum) error {
	nowMs := s.now().UnixMilli()
	return s.db.Update(func(tx *bolt.Tx) error {
		sb, err := tx.CreateBucketIfNotExists(bucketSamples)
		if err != nil {
			return err
		}
		mb, err := tx.CreateBucketIfNotExists(bucketMeta)
		if err != nil {
			return err
		}
		seq := readSeq(mb)
		for _, d := range data {
			if err := s.recordSeries(tx, d, nowMs); err != nil {
				return err
			}
			ts := nowMs
			if !d.Timestamp.IsZero() {
				ts = d.Timestamp.UnixMilli()
			}
			seq++
			key := sampleKey(seriesKey(d.Namespace, d.MetricName, d.Dimensions), ts, seq)
			if err := sb.Put(key, d.sample().encode()); err != nil {
				return err
			}
		}
		return writeSeq(mb, seq)
	})
}

// sample renders a datum as the record stored for it. A StatisticValues datum
// stands for many observations at once, which is what Count/Min/Max carry.
func (d datum) sample() sample {
	if d.Stats != nil {
		return sample{Value: d.Stats.Sum, Count: d.Stats.SampleCount,
			Min: d.Stats.Minimum, Max: d.Stats.Maximum, Unit: d.Unit}
	}
	return sample{Value: d.Value, Count: 1, Min: d.Value, Max: d.Value, Unit: d.Unit}
}

func readSeq(b *bolt.Bucket) uint64 {
	raw := b.Get(keySeq)
	if len(raw) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(raw)
}

func writeSeq(b *bolt.Bucket, seq uint64) error {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], seq)
	return b.Put(keySeq, raw[:])
}

// readSamples walks one series over a half-open window [from, to).
func (s *Server) readSamples(series []byte, from, to time.Time) ([]tsSample, error) {
	lo, hi := seriesRange(series, from.UnixMilli(), to.UnixMilli())
	var out []tsSample
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSamples)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(lo); k != nil && bytes.Compare(k, hi) < 0; k, v = c.Next() {
			sm, ok := decodeSample(v)
			if !ok {
				continue
			}
			ms, ok := millisOfKey(k, len(series))
			if !ok {
				continue
			}
			out = append(out, tsSample{At: ms, sample: sm})
		}
		return nil
	})
	return out, err
}

// tsSample is a stored sample with the time it was observed.
type tsSample struct {
	At int64 // epoch millis
	sample
}

// millisOfKey reads the timestamp back out of a key. The series length is
// known by the caller, so this is a slice rather than a scan for separators —
// a series name may itself contain the separator's neighbours.
func millisOfKey(key []byte, seriesLen int) (int64, bool) {
	start := seriesLen + 1
	if len(key) < start+13 {
		return 0, false
	}
	var ms int64
	for _, c := range key[start : start+13] {
		if c < '0' || c > '9' {
			return 0, false
		}
		ms = ms*10 + int64(c-'0')
	}
	return ms, true
}

// sweep drops samples past the retention window, and then the oldest samples
// beyond the cap. Two limits because they answer different failures: the
// window bounds how far back a question can reach, and the cap bounds what a
// runaway publisher can do to the disk inside that window.
func (s *Server) sweep(retention time.Duration, max int) (int, error) {
	cutoff := s.now().Add(-retention).UnixMilli()
	removed := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSamples)
		if b == nil {
			return nil
		}
		// bbolt forbids deleting under a live cursor, so collect then delete.
		var stale [][]byte
		total := 0
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			total++
			ms, ok := millisAnywhere(k)
			if ok && ms < cutoff {
				stale = append(stale, append([]byte(nil), k...))
			}
		}
		for _, k := range stale {
			if err := b.Delete(k); err != nil {
				return err
			}
			removed++
		}
		// Then the cap. Key order is series-then-time, not global time, so
		// this drops the oldest WITHIN each series rather than globally —
		// which is the behaviour worth having: one noisy metric should not
		// evict a quiet one entirely.
		over := total - len(stale) - max
		if over <= 0 {
			return nil
		}
		var excess [][]byte
		c = b.Cursor()
		for k, _ := c.First(); k != nil && len(excess) < over; k, _ = c.Next() {
			excess = append(excess, append([]byte(nil), k...))
		}
		for _, k := range excess {
			if err := b.Delete(k); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	return removed, err
}

// millisAnywhere reads the timestamp from a key whose series length is not
// known: the last two NUL-delimited fields are the timestamp and sequence.
func millisAnywhere(key []byte) (int64, bool) {
	last := bytes.LastIndexByte(key, 0)
	if last < 0 {
		return 0, false
	}
	prev := bytes.LastIndexByte(key[:last], 0)
	if prev < 0 {
		return 0, false
	}
	return millisOfKey(key, prev)
}
