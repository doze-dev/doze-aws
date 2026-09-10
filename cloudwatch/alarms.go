package cloudwatch

// Alarms: the stored configuration and its state machine.
//
// An alarm watches one metric statistic over a window and moves between three
// states. The evaluator (evaluate.go) drives the transitions; this file is
// what an alarm IS and how it is kept.
//
// # Why alarms fsync and samples do not
//
// The store is opened NoSync because samples are high-volume and disposable —
// losing the last second of metrics on a crash costs nothing. An alarm is
// neither: it is configuration a developer wrote, often through CloudFormation,
// and losing it means a stack that claims to be deployed is not. So every
// alarm write is followed by an explicit sync, which is the narrow version of
// the durability the samples deliberately give up.

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
)

var (
	bucketAlarms  = []byte("alarms")
	bucketHistory = []byte("history")
)

// The three states, spelled as AWS spells them.
const (
	stateOK               = "OK"
	stateAlarm            = "ALARM"
	stateInsufficientData = "INSUFFICIENT_DATA"
)

// comparisonOperators is the model's enum. The three that mention an upper or
// lower threshold belong to anomaly-detection alarms, which need a trained
// band doze-aws does not have; they are refused at PutMetricAlarm rather than
// accepted and never fired.
var comparisonOperators = []string{
	"GreaterThanOrEqualToThreshold", "GreaterThanThreshold",
	"LessThanThreshold", "LessThanOrEqualToThreshold",
	"LessThanLowerOrGreaterThanUpperThreshold", "LessThanLowerThreshold",
	"GreaterThanUpperThreshold",
}

// anomalyOperators need a band from an anomaly detector.
var anomalyOperators = map[string]bool{
	"LessThanLowerOrGreaterThanUpperThreshold": true,
	"LessThanLowerThreshold":                   true,
	"GreaterThanUpperThreshold":                true,
}

// treatMissingValues are the four ways an alarm may read a period with no
// observations. The model types this as a plain string, so the enum is here.
var treatMissingValues = []string{"breaching", "notBreaching", "ignore", "missing"}

// alarm is a stored metric alarm.
type alarm struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Namespace   string            `json:"namespace"`
	MetricName  string            `json:"metric_name"`
	Dimensions  map[string]string `json:"dimensions,omitempty"`
	Statistic   string            `json:"statistic"`
	Unit        string            `json:"unit,omitempty"`
	Period      int               `json:"period"`
	// EvaluationPeriods is how many periods are examined; DatapointsToAlarm
	// is how many of them must breach. AWS calls the pair "M out of N", and
	// DatapointsToAlarm defaults to EvaluationPeriods when omitted.
	EvaluationPeriods int      `json:"evaluation_periods"`
	DatapointsToAlarm int      `json:"datapoints_to_alarm"`
	ComparisonOp      string   `json:"comparison_operator"`
	Threshold         float64  `json:"threshold"`
	TreatMissingData  string   `json:"treat_missing_data,omitempty"`
	ActionsEnabled    bool     `json:"actions_enabled"`
	AlarmActions      []string `json:"alarm_actions,omitempty"`
	OKActions         []string `json:"ok_actions,omitempty"`
	InsufficientData  []string `json:"insufficient_data_actions,omitempty"`

	State       string `json:"state"`
	StateReason string `json:"state_reason,omitempty"`
	// StateReasonData is AWS's JSON blob explaining the transition, which the
	// console renders and a caller may parse.
	StateReasonData string `json:"state_reason_data,omitempty"`
	StateUpdatedMs  int64  `json:"state_updated_ms"`
	UpdatedMs       int64  `json:"updated_ms"`
	// StateSetManually records that SetAlarmState put this state here, so the
	// evaluator does not immediately undo a deliberate flip.
	StateSetManually bool `json:"state_set_manually,omitempty"`
}

// ARN is the alarm's ARN, which is what an IAM policy names and what an
// action's payload identifies it by.
func (a *alarm) ARN() string { return awsident.ARN("cloudwatch", "alarm:"+a.Name) }

// actionsFor returns the actions to fire on entering a state.
func (a *alarm) actionsFor(state string) []string {
	switch state {
	case stateAlarm:
		return a.AlarmActions
	case stateOK:
		return a.OKActions
	case stateInsufficientData:
		return a.InsufficientData
	}
	return nil
}

// historyEntry is one recorded change. AWS keeps configuration updates and
// state transitions in the same log, distinguished by type.
type historyEntry struct {
	AlarmName string `json:"alarm_name"`
	Type      string `json:"type"` // StateUpdate | ConfigurationUpdate | Action
	Summary   string `json:"summary"`
	Data      string `json:"data,omitempty"`
	AtMs      int64  `json:"at_ms"`
}

const (
	historyStateUpdate  = "StateUpdate"
	historyConfigUpdate = "ConfigurationUpdate"
	historyAction       = "Action"
)

// putAlarm stores an alarm and syncs, because an alarm is configuration.
func (s *Server) putAlarm(a *alarm, h historyEntry) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketAlarms)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(a.Name), raw); err != nil {
			return err
		}
		return appendHistory(tx, h)
	})
	if err != nil {
		return err
	}
	return s.db.Sync()
}

// historyKey orders by time then name, so a listing for one alarm is a filter
// over a time range rather than a separate index.
func historyKey(atMs int64, name string, seq uint64) []byte {
	var b strings.Builder
	// Fixed width, so byte order is time order.
	b.WriteString(pad13(atMs))
	b.WriteByte(0)
	b.WriteString(name)
	b.WriteByte(0)
	b.WriteString(pad10(seq))
	return []byte(b.String())
}

func pad13(v int64) string {
	s := make([]byte, 13)
	for i := 12; i >= 0; i-- {
		s[i] = byte('0' + v%10)
		v /= 10
	}
	return string(s)
}

func pad10(v uint64) string {
	s := make([]byte, 10)
	for i := 9; i >= 0; i-- {
		s[i] = byte('0' + v%10)
		v /= 10
	}
	return string(s)
}

func appendHistory(tx *bolt.Tx, h historyEntry) error {
	if h.AlarmName == "" {
		return nil
	}
	b, err := tx.CreateBucketIfNotExists(bucketHistory)
	if err != nil {
		return err
	}
	seq, err := b.NextSequence()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return b.Put(historyKey(h.AtMs, h.AlarmName, seq), raw)
}

// getAlarm reads one alarm, or nil when it does not exist.
func (s *Server) getAlarm(name string) (*alarm, error) {
	var a *alarm
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAlarms)
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return nil
		}
		var parsed alarm
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return err
		}
		a = &parsed
		return nil
	})
	return a, err
}

// listAlarms reads every alarm, sorted by name so a listing is stable.
func (s *Server) listAlarms() ([]*alarm, error) {
	var out []*alarm
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAlarms)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var a alarm
			if json.Unmarshal(v, &a) != nil {
				return nil
			}
			out = append(out, &a)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// deleteAlarms removes alarms and their history. History goes with the alarm:
// keeping transitions for something that no longer exists is a listing nobody
// can act on, and a recreated alarm inheriting the old one's history would
// misreport when it started.
func (s *Server) deleteAlarms(names []string) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAlarms)
		if b == nil {
			return nil
		}
		gone := map[string]bool{}
		for _, n := range names {
			if b.Get([]byte(n)) != nil {
				gone[n] = true
			}
			if err := b.Delete([]byte(n)); err != nil {
				return err
			}
		}
		hb := tx.Bucket(bucketHistory)
		if hb == nil || len(gone) == 0 {
			return nil
		}
		// bbolt forbids deleting under a live cursor: collect, then delete.
		var stale [][]byte
		if err := hb.ForEach(func(k, v []byte) error {
			var h historyEntry
			if json.Unmarshal(v, &h) == nil && gone[h.AlarmName] {
				stale = append(stale, append([]byte(nil), k...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range stale {
			if err := hb.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.db.Sync()
}

// readHistory returns entries for one alarm (or all) within a window,
// newest first, which is the order a caller reading "what just happened"
// wants.
func (s *Server) readHistory(name string, from, to time.Time, typ string, limit int) ([]historyEntry, error) {
	var out []historyEntry
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketHistory)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var h historyEntry
			if json.Unmarshal(v, &h) != nil {
				return nil
			}
			if name != "" && h.AlarmName != name {
				return nil
			}
			if typ != "" && h.Type != typ {
				return nil
			}
			at := time.UnixMilli(h.AtMs)
			if !from.IsZero() && at.Before(from) {
				return nil
			}
			if !to.IsZero() && !at.Before(to) {
				return nil
			}
			out = append(out, h)
			return nil
		})
	})
	// Newest first, and the reversal rather than a sort is deliberate: the
	// key is <millis>\0<name>\0<sequence>, so ForEach already walks in
	// (time, name, insertion) order and reversing it is a TOTAL order.
	// Sorting on the timestamp alone leaves ties — which is every entry when
	// the clock is frozen, as it is under test, or two changes inside one
	// millisecond, which a create-then-set does easily.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, err
}
