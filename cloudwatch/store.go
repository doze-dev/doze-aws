package cloudwatch

// The metric catalogue.
//
// A "series" is the identity of a metric: namespace, name and its exact set
// of dimensions. It is what ListMetrics answers with, and it is deliberately
// separate from the samples themselves — one series has many observations,
// and listing what exists should not read any of them.
//
// Only the catalogue lands here. Samples, aggregation and retention arrive
// with the metric store in the next sub-batch; PutMetricData records the
// series a datum belongs to so ListMetrics has something true to answer.
//
// # Dimensions are part of the identity
//
// Two metrics with the same name and different dimensions are two metrics,
// not one — that is how a per-function Invocations count works at all. Moto
// gets this wrong and sums by name alone, so the key below carries a
// canonical rendering of the dimension set, sorted, so the same set always
// produces the same key regardless of the order the client sent it in.

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"
)

var bucketSeries = []byte("series")

// series is one metric identity, as stored.
type series struct {
	Namespace  string            `json:"namespace"`
	MetricName string            `json:"metric_name"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
	FirstSeen  int64             `json:"first_seen_ms"`
	LastSeen   int64             `json:"last_seen_ms"`
}

// seriesKey is the identity, rendered so byte order is a stable, groupable
// order: namespace, then metric name, then the dimension set. The separator
// is NUL so a name containing the separator cannot forge a different key.
func seriesKey(namespace, metric string, dims map[string]string) []byte {
	var b bytes.Buffer
	b.WriteString(namespace)
	b.WriteByte(0)
	b.WriteString(metric)
	b.WriteByte(0)
	b.WriteString(dimensionSignature(dims))
	return b.Bytes()
}

// dimensionSignature renders a dimension set canonically. Sorted by name, so
// {a,b} and {b,a} are one series rather than two — a client is free to send
// them in either order and AWS treats them as the same metric.
func dimensionSignature(dims map[string]string) string {
	if len(dims) == 0 {
		return ""
	}
	names := make([]string, 0, len(dims))
	for n := range dims {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteByte(0x1f) // unit separator, between pairs
		}
		b.WriteString(n)
		b.WriteByte(0x1e) // record separator, between a name and its value
		b.WriteString(dims[n])
	}
	return b.String()
}

// recordSeries notes that a metric exists, creating it on first sight and
// moving its last-seen stamp after that.
func (s *Server) recordSeries(tx *bolt.Tx, d datum, nowMs int64) error {
	b, err := tx.CreateBucketIfNotExists(bucketSeries)
	if err != nil {
		return err
	}
	key := seriesKey(d.Namespace, d.MetricName, d.Dimensions)
	rec := series{Namespace: d.Namespace, MetricName: d.MetricName,
		Dimensions: d.Dimensions, FirstSeen: nowMs, LastSeen: nowMs}
	if raw := b.Get(key); raw != nil {
		var prev series
		if json.Unmarshal(raw, &prev) == nil {
			rec.FirstSeen = prev.FirstSeen
		}
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return b.Put(key, raw)
}

// seriesFilter narrows a listing. A zero filter matches everything.
type seriesFilter struct {
	Namespace  string
	MetricName string
	// Dimensions are DimensionFilters: a name alone matches any value, a
	// name with a value matches that value. A series must match every
	// filter given, which is AWS's rule — the filters are ANDed.
	Dimensions []dimensionFilter
	// RecentlyActive, when set, keeps only series seen within the window.
	SinceMs int64
}

type dimensionFilter struct {
	Name  string
	Value string
	// HasValue distinguishes "any value for this name" from "the empty
	// value", which are different questions.
	HasValue bool
}

func (f seriesFilter) matches(rec series) bool {
	if f.Namespace != "" && rec.Namespace != f.Namespace {
		return false
	}
	if f.MetricName != "" && rec.MetricName != f.MetricName {
		return false
	}
	if f.SinceMs > 0 && rec.LastSeen < f.SinceMs {
		return false
	}
	for _, df := range f.Dimensions {
		v, ok := rec.Dimensions[df.Name]
		if !ok {
			return false
		}
		if df.HasValue && v != df.Value {
			return false
		}
	}
	return true
}

// listSeries walks the catalogue in key order, returning at most limit
// matches from after the given key, plus the key to continue from when there
// are more. Key order groups a namespace's metrics together, so a listing
// filtered by namespace is a range scan rather than a full walk.
func (s *Server) listSeries(f seriesFilter, after []byte, limit int) ([]series, []byte, error) {
	var out []series
	var next []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSeries)
		if b == nil {
			return nil // nothing published yet
		}
		c := b.Cursor()
		var k, v []byte
		if len(after) > 0 {
			// Seek lands on the continuation key itself; step past it so a
			// page boundary does not repeat an entry.
			k, v = c.Seek(after)
			if k != nil && bytes.Equal(k, after) {
				k, v = c.Next()
			}
		} else {
			k, v = c.First()
		}
		for ; k != nil; k, v = c.Next() {
			var rec series
			if json.Unmarshal(v, &rec) != nil {
				continue // a record this build cannot read is not a match
			}
			if !f.matches(rec) {
				continue
			}
			if len(out) == limit {
				next = append([]byte(nil), k...)
				return nil
			}
			out = append(out, rec)
		}
		return nil
	})
	return out, next, err
}
