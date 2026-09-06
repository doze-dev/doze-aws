package stepfunctions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/asl"
)

// Execution persistence. Keyed machineName \x00 execName, so listing a
// machine's executions is a prefix cursor scan and names stay readable in the
// store.

var (
	bucketExecutions = []byte("executions")
	// bucketHistory holds one event per key: execKey \x00 %020d(event id),
	// so a page is a prefix cursor scan in id order.
	bucketHistory = []byte("history")
)

// Execution is the stored record: the frozen snapshot the execution runs
// (UpdateStateMachine must not change what an in-flight execution is doing,
// and DescribeStateMachineForExecution reads it back), the wire-level status
// fields, and the interpreter's Exec embedded whole. One key, one marshal,
// one write per transition — executions are small at local scale, and deltas
// would be complexity with no payoff.
type Execution struct {
	ARN        string `json:"arn"`
	MachineARN string `json:"machine_arn"`
	Name       string `json:"name"`

	Definition string `json:"definition"`
	RoleARN    string `json:"role_arn"`
	RevisionID string `json:"revision_id"`
	Type       string `json:"type"` // STANDARD | EXPRESS

	Status    string `json:"status"` // RUNNING SUCCEEDED FAILED TIMED_OUT ABORTED
	StartedAt int64  `json:"started_at"`
	StoppedAt int64  `json:"stopped_at,omitempty"`
	// Input is kept as the exact text the caller supplied — the same
	// round-trip-fidelity rule as StateMachine.Definition; json.RawMessage
	// would be compacted on every store write.
	Input  string          `json:"input"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
	Cause  string          `json:"cause,omitempty"`

	// Deadline is the machine-level TimeoutSeconds cutoff, epoch millis; 0
	// means none.
	Deadline int64 `json:"deadline,omitempty"`
	// TraceHeader is the causal chain captured at StartExecution, so steps a
	// resumed execution causes still hang off the call that started it. It
	// is doze-aws's own and never answered on the wire.
	TraceHeader string `json:"trace_header,omitempty"`
	// XRayHeader is the traceHeader the caller passed to StartExecution, if
	// any — the X-Ray one, which DescribeExecution echoes the way AWS does.
	XRayHeader string `json:"xray_header,omitempty"`
	// VersionARN and AliasARN record how StartExecution was addressed — a
	// version ARN, or an alias that picked one — for DescribeExecution and
	// ListExecutions to echo. Both empty for an execution of the bare machine.
	VersionARN string `json:"version_arn,omitempty"`
	AliasARN   string `json:"alias_arn,omitempty"`

	// StartedBy is the parent execution's ARN when a states:startExecution
	// Task started this one. Redrive bookkeeping lives beside it.
	StartedBy    string `json:"started_by,omitempty"`
	RedriveCount int    `json:"redrive_count,omitempty"`
	RedriveDate  int64  `json:"redrive_date,omitempty"`
	RedriveToken string `json:"redrive_token,omitempty"` // the last clientToken, for idempotent retries
	// MapRunARN and MapIndex mark a child execution of a Distributed Map:
	// which run it belongs to and which item it is.
	MapRunARN string `json:"map_run_arn,omitempty"`
	MapIndex  int    `json:"map_index,omitempty"`

	// Volatile marks an Express or TestState execution: kept in memory,
	// never written to bbolt, gone when its caller has read it.
	Volatile bool `json:"volatile,omitempty"`
	// Test is set on a TestState run — the state under test and its mock —
	// for the driver to find when it loads the run; TestOutcome is what the
	// run reports back (express.go, actions_teststate.go).
	Test        *TestSpec    `json:"test_spec,omitempty"`
	TestOutcome *TestOutcome `json:"test,omitempty"`

	Exec        *asl.Exec `json:"exec"`
	NextEventID int64     `json:"next_event_id"`
}

// Key is the store key of this execution, also used as the engine's run key.
// An Express execution keys on name plus id, because AWS lets two Express
// executions share a name and only the id tells them apart.
func (e *Execution) Key() string {
	if machine, name, id := parseExpressARN(e.ARN); id != "" {
		return execKey(machine, name+"/"+id)
	}
	return execKey(machineOfExecARN(e.ARN), e.Name)
}

func execKey(machineName, execName string) string {
	return machineName + "\x00" + execName
}

// machineOfExecARN pulls the machine name out of an execution ARN of the
// shape arn:aws:states:<region>:<account>:execution:<machine>:<name>.
func machineOfExecARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 8 || parts[5] != "execution" {
		return ""
	}
	return parts[6]
}

// PutExecution writes the record. Every interpreter transition lands here —
// that cadence is the durability contract.
func (s *Store) PutExecution(e *Execution) error {
	return s.put(bucketExecutions, []byte(e.Key()), e)
}

// SaveTransition writes the execution record, appends its new history
// events, and applies its token writes in ONE bbolt update. A crash between
// any two would otherwise leave history claiming a transition the frames
// don't show, or a PARKED frame whose token can never be redeemed.
func (s *Store) SaveTransition(e *Execution, events []histEvent, tokens []tokenOp) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	key := e.Key()
	if e.Volatile {
		s.vol.save(key, raw, events)
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		eb, err := tx.CreateBucketIfNotExists(bucketExecutions)
		if err != nil {
			return err
		}
		if err := eb.Put([]byte(key), raw); err != nil {
			return err
		}
		if len(events) > 0 {
			hb, err := tx.CreateBucketIfNotExists(bucketHistory)
			if err != nil {
				return err
			}
			for _, ev := range events {
				rawEv, err := json.Marshal(ev)
				if err != nil {
					return err
				}
				if err := hb.Put([]byte(histKey(key, ev.ID)), rawEv); err != nil {
					return err
				}
			}
		}
		if len(tokens) > 0 {
			tb, err := tx.CreateBucketIfNotExists(bucketTokens)
			if err != nil {
				return err
			}
			for _, op := range tokens {
				// An activity token also owns a queue row (store_activities.go);
				// the two are written and dropped together.
				if err := applyTokenOp(tx, tb, op, s.now()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func histKey(execKey string, id int64) string {
	return fmt.Sprintf("%s\x00%020d", execKey, id)
}

// HistoryPage reads events for one execution with id > afterID, at most
// limit, in ascending id order. more says a further page exists.
func (s *Store) HistoryPage(execKey string, afterID int64, limit int) (events []histEvent, more bool, err error) {
	if evs, more, ok := s.vol.history(execKey, afterID, limit); ok {
		return evs, more, nil
	}
	prefix := []byte(execKey + "\x00")
	start := []byte(histKey(execKey, afterID+1))
	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketHistory)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, raw := c.Seek(start); k != nil && bytes.HasPrefix(k, prefix); k, raw = c.Next() {
			if len(events) == limit {
				more = true
				return nil
			}
			var ev histEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				return err
			}
			events = append(events, ev)
		}
		return nil
	})
	return events, more, err
}

// GetExecution reads one execution, nil when absent.
func (s *Store) GetExecution(machineName, execName string) (*Execution, error) {
	if e, ok := s.vol.get(execKey(machineName, execName)); ok {
		return e, nil
	}
	var e Execution
	found, err := s.get(bucketExecutions, []byte(execKey(machineName, execName)), &e)
	if err != nil || !found {
		return nil, err
	}
	return &e, nil
}

// GetExecutionByKey reads by the engine's run key.
func (s *Store) GetExecutionByKey(key string) (*Execution, error) {
	machine, name, ok := strings.Cut(key, "\x00")
	if !ok {
		return nil, nil
	}
	return s.GetExecution(machine, name)
}

// ListExecutionsFor returns every execution of one machine, in key order.
func (s *Store) ListExecutionsFor(machineName string) ([]*Execution, error) {
	prefix := []byte(machineName + "\x00")
	var out []*Execution
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketExecutions)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, raw := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, raw = c.Next() {
			var e Execution
			if err := json.Unmarshal(raw, &e); err != nil {
				return err
			}
			out = append(out, &e)
		}
		return nil
	})
	return out, err
}

// EachRunning visits every RUNNING execution's key — what the engine resumes
// on startup. Keys only: the engine loads on demand, so a thousand finished
// executions cost nothing here.
func (s *Store) EachRunning(fn func(key string)) error {
	return s.each(bucketExecutions, func(k, raw []byte) error {
		var probe struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return err
		}
		if probe.Status == "RUNNING" {
			fn(string(k))
		}
		return nil
	})
}
