package stepfunctions

import (
	"encoding/json"
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
)

// A Map Run is the record a Distributed Map keeps: which execution and
// frame it belongs to, the items, how far launching has got, and the
// per-item outcomes as children finish. It lives in bbolt beside the
// executions because a restart has to pick up where it left off — items
// not yet launched get launched, children still running finish on their
// own and report in.

var (
	bucketMapRuns = []byte("mapruns")
	// bucketMapItems holds one row per item: mapRunARN \x00 %08d.
	bucketMapItems = []byte("mapitems")
)

// MapRun is the stored record.
type MapRun struct {
	ARN        string `json:"arn"`
	ExecKey    string `json:"exec_key"`
	ExecARN    string `json:"exec_arn"`
	MachineARN string `json:"machine_arn"`
	Machine    string `json:"machine"`
	Frame      int    `json:"frame"`
	ID         string `json:"id"`
	Label      string `json:"label,omitempty"`

	Status    string `json:"status"` // RUNNING SUCCEEDED FAILED ABORTED
	StartedAt int64  `json:"started_at"`
	StoppedAt int64  `json:"stopped_at,omitempty"`

	MaxConcurrency             int             `json:"max_concurrency"`
	ToleratedFailureCount      *int            `json:"tolerated_failure_count,omitempty"`
	ToleratedFailurePercentage *float64        `json:"tolerated_failure_percentage,omitempty"`
	ExecutionType              string          `json:"execution_type"`
	Processor                  string          `json:"processor"` // the ItemProcessor's definition text
	RoleARN                    string          `json:"role_arn,omitempty"`
	ResultWriter               json.RawMessage `json:"result_writer,omitempty"`

	// Inputs are the child inputs after ItemSelector and ItemBatcher, in
	// order. Next is the index of the next one to launch.
	Inputs []json.RawMessage `json:"inputs"`
	Next   int               `json:"next"`

	Running        int   `json:"running"`
	Succeeded      int   `json:"succeeded"`
	Failed         int   `json:"failed"`
	TimedOut       int   `json:"timed_out"`
	Aborted        int   `json:"aborted"`
	ResultsWritten int   `json:"results_written,omitempty"`
	RedriveCount   int   `json:"redrive_count,omitempty"`
	RedriveDate    int64 `json:"redrive_date,omitempty"`
}

// MapItem is one item's outcome.
type MapItem struct {
	Index  int             `json:"index"`
	Status string          `json:"status"` // SUCCEEDED FAILED TIMED_OUT ABORTED
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
	Cause  string          `json:"cause,omitempty"`
}

func (m *MapRun) Total() int   { return len(m.Inputs) }
func (m *MapRun) Pending() int { return m.Total() - m.Next }
func (m *MapRun) done() bool   { return m.Next == m.Total() && m.Running == 0 }

// failures is Failed plus TimedOut plus Aborted — what the tolerance counts.
func (m *MapRun) failures() int { return m.Failed + m.TimedOut + m.Aborted }

// tolerated reports how many item failures the state allows.
func (m *MapRun) tolerated() int {
	switch {
	case m.ToleratedFailureCount != nil:
		return *m.ToleratedFailureCount
	case m.ToleratedFailurePercentage != nil:
		return int(*m.ToleratedFailurePercentage * float64(m.Total()) / 100)
	}
	return 0
}

// mapRunARN is arn:aws:states:<r>:<a>:mapRun:<machine>/<execution>[/<label>]:<id>,
// the shape AWS gives one.
func mapRunARN(machine, execName, label, id string) string {
	res := "mapRun:" + machine + "/" + execName
	if label != "" {
		res += "/" + label
	}
	return awsident.ARN("states", res+":"+id)
}

func (s *Store) PutMapRun(m *MapRun) error { return s.put(bucketMapRuns, []byte(m.ARN), m) }

func (s *Store) GetMapRun(arn string) (*MapRun, error) {
	var m MapRun
	found, err := s.get(bucketMapRuns, []byte(arn), &m)
	if err != nil || !found {
		return nil, err
	}
	return &m, nil
}

// MapRunsFor lists the Map Runs of one execution, in start order.
func (s *Store) MapRunsFor(execKey string) ([]*MapRun, error) {
	var out []*MapRun
	err := s.each(bucketMapRuns, func(k, raw []byte) error {
		var m MapRun
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if m.ExecKey == execKey {
			out = append(out, &m)
		}
		return nil
	})
	return out, err
}

func mapItemKey(arn string, index int) []byte {
	return []byte(fmt.Sprintf("%s\x00%08d", arn, index))
}

func (s *Store) PutMapItem(arn string, it *MapItem) error {
	return s.put(bucketMapItems, mapItemKey(arn, it.Index), it)
}

// MapItems reads every item outcome of a run, in index order.
func (s *Store) MapItems(arn string) ([]*MapItem, error) {
	prefix := []byte(arn + "\x00")
	var out []*MapItem
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMapItems)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, raw := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, raw = c.Next() {
			var it MapItem
			if err := json.Unmarshal(raw, &it); err != nil {
				return err
			}
			out = append(out, &it)
		}
		return nil
	})
	return out, err
}
