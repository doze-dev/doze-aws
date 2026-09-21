package logs

// Metric filters: a log group turns matching lines into CloudWatch metrics.
//
// This is how a stack alarms on something only its logs know — an ERROR line,
// a latency value printed by a handler — without the code publishing a metric
// itself. AWS evaluates the filter on ingest, which is what makes it cheap,
// and so does this: emission hangs off PutLogEvents beside the subscription
// fan-out, on the same worker-and-channel shape, so a slow CloudWatch costs
// the writer nothing.
//
// # metricValue is two different things
//
// A literal — "1", counting occurrences — or a `$.field` reference, extracting
// a number from the matched line. The second is what makes a metric filter
// more than a counter: a handler printing {"latency": 42} gets a latency
// metric without knowing CloudWatch exists. A reference that does not resolve
// contributes the defaultValue when one is set and nothing otherwise, which is
// AWS's rule and the reason defaultValue exists at all.

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var bucketMetricFilters = []byte("metricfilters") // group \x00 name → MetricFilter

// maxMetricFiltersPerGroup is AWS's limit.
const maxMetricFiltersPerGroup = 100

// metricFilter is one filter on a group.
type metricFilter struct {
	Group           string                 `json:"group"`
	Name            string                 `json:"name"`
	Pattern         string                 `json:"pattern"`
	Transformations []metricTransformation `json:"transformations"`
	CreatedMs       int64                  `json:"created"`
}

// metricTransformation says what metric a match produces.
type metricTransformation struct {
	Namespace  string            `json:"namespace"`
	MetricName string            `json:"metric_name"`
	Value      string            `json:"value"`
	Default    *float64          `json:"default,omitempty"`
	Unit       string            `json:"unit,omitempty"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
}

func metricFilterKey(group, name string) []byte { return []byte(group + "\x00" + name) }

// ---- store ----

func (s *store) PutMetricFilter(f metricFilter) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketGroups).Get([]byte(f.Group)) == nil {
			return ErrNoGroup
		}
		b, err := tx.CreateBucketIfNotExists(bucketMetricFilters)
		if err != nil {
			return err
		}
		if b.Get(metricFilterKey(f.Group, f.Name)) == nil {
			n := 0
			c := b.Cursor()
			prefix := metricFilterKey(f.Group, "")
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				n++
			}
			if n >= maxMetricFiltersPerGroup {
				return awshttp.Errf(400, "LimitExceededException", "Resource limit exceeded.")
			}
		}
		raw, _ := json.Marshal(f)
		return b.Put(metricFilterKey(f.Group, f.Name), raw)
	})
}

func (s *store) DeleteMetricFilter(group, name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMetricFilters)
		if b == nil {
			return ErrNoFilter
		}
		key := metricFilterKey(group, name)
		if b.Get(key) == nil {
			return ErrNoFilter
		}
		return b.Delete(key)
	})
}

// MetricFilters lists a group's filters, or every group's when group is
// empty — which is what DescribeMetricFilters does with no group given.
func (s *store) MetricFilters(group, prefix string) ([]metricFilter, error) {
	var out []metricFilter
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMetricFilters)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var f metricFilter
			if json.Unmarshal(v, &f) != nil {
				return nil
			}
			if group != "" && f.Group != group {
				return nil
			}
			if prefix != "" && !strings.HasPrefix(f.Name, prefix) {
				return nil
			}
			out = append(out, f)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out, err
}

// ---- evaluation ----

// metricValueOf resolves one transformation against a matched line.
//
// The bool reports whether anything should be published at all: a `$.field`
// reference that does not resolve, with no defaultValue, produces nothing —
// which is different from producing zero, and is why defaultValue is a
// pointer rather than a float with a zero that means "unset".
func metricValueOf(t metricTransformation, message string) (float64, bool) {
	if !strings.HasPrefix(t.Value, "$") {
		// A literal. AWS accepts any number here; "1" is the counting case.
		f, err := strconv.ParseFloat(t.Value, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	if v, ok := jsonNumberAt(message, strings.TrimPrefix(t.Value, "$.")); ok {
		return v, true
	}
	if t.Default != nil {
		return *t.Default, true
	}
	return 0, false
}

// jsonNumberAt reads a dotted path out of a JSON line. Only the shape a
// metric filter can reference: nested objects and numbers at the leaf.
func jsonNumberAt(message, path string) (float64, bool) {
	var doc any
	if json.Unmarshal([]byte(message), &doc) != nil {
		return 0, false
	}
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur, ok = m[seg]
		if !ok {
			return 0, false
		}
	}
	switch v := cur.(type) {
	case float64:
		return v, true
	case string:
		// A number printed as a string is what a caller means; refusing it
		// would make the filter depend on how the handler formatted its log.
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}
