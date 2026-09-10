// Package emf reads AWS's Embedded Metric Format: a JSON log line that is
// also a metric publication.
//
// A function that writes EMF gets custom metrics without an SDK call and
// without the latency of one — it prints a line, and the log pipeline turns
// it into metrics. That is the whole mechanism, which is why it belongs here
// rather than in an SDK: the line reaching CloudWatch Logs IS the publish.
//
//	{
//	  "_aws": {
//	    "Timestamp": 1700000000000,
//	    "CloudWatchMetrics": [{
//	      "Namespace": "Shop",
//	      "Dimensions": [["Stage"], []],
//	      "Metrics": [{"Name": "Checkouts", "Unit": "Count"}]
//	    }]
//	  },
//	  "Stage": "prod",
//	  "Checkouts": 3
//	}
//
// The values and the dimension values live at the TOP level of the document,
// not inside _aws: the directive names them and the body carries them, so one
// line is both a readable log entry and a metric.
//
// # Dimension sets, plural
//
// `Dimensions` is a list of dimension SETS, and each one produces its own
// copy of every metric. `[["Stage"], []]` means "publish this per stage, and
// again with no dimensions" — the same value reachable two ways, which is how
// an account-wide alarm and a per-stage graph read the same publication. A
// reader that treated it as one flat list would silently publish a single
// wrongly-dimensioned metric.
package emf

import (
	"encoding/json"
)

// Metric is one observation the directive names, resolved against the body.
type Metric struct {
	Namespace  string
	Name       string
	Unit       string
	Value      float64
	Dimensions map[string]string
	// TimestampMs is the directive's Timestamp, or zero when it had none.
	TimestampMs int64
	// StorageResolution is 1 for a high-resolution metric, else 0.
	StorageResolution int
}

// directive is the _aws block.
type directive struct {
	Timestamp int64 `json:"Timestamp"`
	Metrics   []struct {
		Namespace  string     `json:"Namespace"`
		Dimensions [][]string `json:"Dimensions"`
		Metrics    []struct {
			Name              string `json:"Name"`
			Unit              string `json:"Unit"`
			StorageResolution int    `json:"StorageResolution"`
		} `json:"Metrics"`
	} `json:"CloudWatchMetrics"`
}

// Looks reports whether a line could be EMF, cheaply enough to run on every
// log line a function prints. A full parse of every line would make printing
// expensive for the vast majority that are not EMF.
func Looks(line []byte) bool {
	// The cheapest true statement about an EMF line: it is a JSON object
	// mentioning the directive key.
	if len(line) < 2 || line[0] != '{' {
		return false
	}
	return containsKey(line, `"_aws"`)
}

func containsKey(b []byte, key string) bool {
	for i := 0; i+len(key) <= len(b); i++ {
		if string(b[i:i+len(key)]) == key {
			return true
		}
	}
	return false
}

// Parse reads one line. It returns nothing — not an error — for a line that
// is not EMF or is malformed: this runs over arbitrary function output, and a
// developer printing a JSON object that happens to mention _aws should get a
// log line, not a failure.
func Parse(line []byte) []Metric {
	if !Looks(line) {
		return nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(line, &doc) != nil {
		return nil
	}
	raw, ok := doc["_aws"]
	if !ok {
		return nil
	}
	var d directive
	if json.Unmarshal(raw, &d) != nil {
		return nil
	}

	var out []Metric
	for _, set := range d.Metrics {
		if set.Namespace == "" {
			continue
		}
		// No Dimensions member at all means one undimensioned set, which is
		// different from an explicit empty list — though both produce one
		// undimensioned copy, so this is really about not skipping the
		// metrics entirely.
		dimSets := set.Dimensions
		if len(dimSets) == 0 {
			dimSets = [][]string{{}}
		}
		for _, def := range set.Metrics {
			value, ok := numberAt(doc, def.Name)
			if !ok {
				// The directive named a metric the body does not carry. That
				// is the caller's bug, and dropping it is better than
				// publishing a zero they never observed.
				continue
			}
			for _, names := range dimSets {
				dims := map[string]string{}
				complete := true
				for _, n := range names {
					v, ok := stringAt(doc, n)
					if !ok {
						// A dimension the body does not carry makes this set
						// unpublishable; the others may still be fine.
						complete = false
						break
					}
					dims[n] = v
				}
				if !complete {
					continue
				}
				if len(dims) == 0 {
					dims = nil
				}
				out = append(out, Metric{
					Namespace: set.Namespace, Name: def.Name, Unit: def.Unit,
					Value: value, Dimensions: dims, TimestampMs: d.Timestamp,
					StorageResolution: def.StorageResolution,
				})
			}
		}
	}
	return out
}

// numberAt reads a metric value from the body. EMF allows an array of values
// for one metric name; the sum is what a Count means and what a caller
// batching observations into one line intends.
func numberAt(doc map[string]json.RawMessage, name string) (float64, bool) {
	raw, ok := doc[name]
	if !ok {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f, true
	}
	var many []float64
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		total := 0.0
		for _, v := range many {
			total += v
		}
		return total, true
	}
	return 0, false
}

// stringAt reads a dimension value. A dimension is a string on AWS, but a
// caller writing {"StatusCode": 200} and naming it as a dimension means the
// obvious thing, so a number is rendered rather than refused.
func stringAt(doc map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := doc[name]
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return trimFloat(f), true
	}
	return "", false
}

func trimFloat(f float64) string {
	b, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	return string(b)
}
