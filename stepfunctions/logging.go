package stepfunctions

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/internal/logship"
)

// History to CloudWatch Logs, the way Step Functions vends it.
//
// A machine's loggingConfiguration names a level (OFF, FATAL, ERROR, ALL),
// whether execution data travels, and a log group. Every history event an
// execution records is written to that group as the JSON record AWS writes:
// id, type, details, previous_event_id, event_timestamp and execution_arn.
// For a STANDARD machine that is an extra way to read what
// GetExecutionHistory already answers; for an EXPRESS machine it is the only
// record there is, on AWS and here — an Express execution leaves nothing
// behind once its caller has read it. So an Express machine whose logging is
// OFF still writes, at ALL, to /aws/vendedlogs/states/<machine> (a doze
// extension, said in the ledger), because a fire-and-forget Express run
// nobody can inspect afterwards is the one thing a local emulator should not
// reproduce faithfully.

// defaultExpressGroup is where an Express machine with logging OFF writes.
func defaultExpressGroup(machine string) string { return "/aws/vendedlogs/states/" + machine }

// logPolicy is a machine's resolved logging configuration.
type logPolicy struct {
	group       string // "" means nothing is written
	level       string // FATAL | ERROR | ALL
	includeData bool
}

// policyOf reads a machine's configuration. Express machines fall back to
// the default group at ALL.
func policyOf(m *StateMachine) logPolicy {
	var cfg struct {
		Level                string `json:"level"`
		IncludeExecutionData bool   `json:"includeExecutionData"`
		Destinations         []struct {
			CloudWatchLogsLogGroup struct {
				LogGroupArn string `json:"logGroupArn"`
			} `json:"cloudWatchLogsLogGroup"`
		} `json:"destinations"`
	}
	if len(m.LoggingConfiguration) > 0 {
		json.Unmarshal(m.LoggingConfiguration, &cfg)
	}
	p := logPolicy{level: strings.ToUpper(cfg.Level), includeData: cfg.IncludeExecutionData}
	for _, d := range cfg.Destinations {
		if g := logGroupFromARN(d.CloudWatchLogsLogGroup.LogGroupArn); g != "" {
			p.group = g
			break
		}
	}
	if p.level == "" || p.level == "OFF" || p.group == "" {
		if m.Type == "EXPRESS" {
			return logPolicy{group: defaultExpressGroup(m.Name), level: "ALL", includeData: true}
		}
		return logPolicy{}
	}
	return p
}

// logGroupFromARN reads the group name out of
// arn:aws:logs:region:account:log-group:NAME[:*].
func logGroupFromARN(arn string) string {
	_, rest, ok := strings.Cut(arn, ":log-group:")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(rest, ":*")
}

// levelAdmits says whether an event type is written at a level. ERROR is
// every event that reports a failure; FATAL only the execution's own end.
func levelAdmits(level, typ string) bool {
	switch level {
	case "ALL":
		return true
	case "ERROR":
		return strings.HasSuffix(typ, "Failed") || strings.HasSuffix(typ, "TimedOut") || strings.HasSuffix(typ, "Aborted")
	case "FATAL":
		return typ == "ExecutionFailed" || typ == "ExecutionTimedOut" || typ == "ExecutionAborted"
	}
	return false
}

// machineLogs resolves policies, mints streams and ships events.
type machineLogs struct {
	srv  *Server
	ship *logship.Shipper

	mu       sync.Mutex
	policies map[string]logPolicy // machine name → policy
	streams  map[string]string    // machine name → this process's stream
}

func newMachineLogs(s *Server) *machineLogs {
	return &machineLogs{srv: s, ship: logship.New("stepfunctions", s.peers, s.logf),
		policies: map[string]logPolicy{}, streams: map[string]string{}}
}

// forget drops a cached policy after the machine changed or went away.
func (l *machineLogs) forget(machine string) {
	l.mu.Lock()
	delete(l.policies, machine)
	l.mu.Unlock()
}

func (l *machineLogs) policy(machine string) logPolicy {
	l.mu.Lock()
	p, ok := l.policies[machine]
	l.mu.Unlock()
	if ok {
		return p
	}
	m, _ := l.srv.store.GetMachine(machine)
	if m == nil {
		return logPolicy{}
	}
	p = policyOf(m)
	l.mu.Lock()
	l.policies[machine] = p
	l.mu.Unlock()
	return p
}

// stream is this process's stream for a machine, minted the way AWS names
// them: states/<machine>/<date>/<hex>.
func (l *machineLogs) stream(machine string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s, ok := l.streams[machine]; ok {
		return s
	}
	var b [8]byte
	rand.Read(b[:])
	s := fmt.Sprintf("states/%s/%s/%s", machine, time.Now().UTC().Format("2006-01-02"), hex.EncodeToString(b[:]))
	l.streams[machine] = s
	return s
}

// record ships an execution's newly persisted events. TestState runs are
// not executions and write nothing.
func (l *machineLogs) record(e *Execution, events []histEvent) {
	if len(events) == 0 || e.Test != nil {
		return
	}
	machine, _, ok := splitMachineARN(e.MachineARN)
	if !ok {
		return
	}
	p := l.policy(machine)
	if p.group == "" {
		return
	}
	var out []logship.Event
	for _, ev := range events {
		if !levelAdmits(p.level, ev.Type) {
			continue
		}
		out = append(out, logship.Event{Timestamp: ev.TS, Message: vendedRecord(e, ev, p.includeData), RequestID: e.ARN})
	}
	if len(out) > 0 {
		l.ship.Put(p.group, l.stream(machine), out...)
	}
}

// vendedRecord is the JSON line AWS writes for one history event.
func vendedRecord(e *Execution, ev histEvent, includeData bool) string {
	rec := map[string]any{
		"id":                strconv.FormatInt(ev.ID, 10),
		"type":              ev.Type,
		"previous_event_id": strconv.FormatInt(ev.PrevID, 10),
		"event_timestamp":   strconv.FormatInt(ev.TS, 10),
		"execution_arn":     e.ARN,
	}
	if ev.DetailKey != "" {
		rec["details"] = ev.wire(includeData)[ev.DetailKey]
	}
	raw, _ := json.Marshal(rec)
	return string(raw)
}

// close flushes what is pending.
func (l *machineLogs) close() { l.ship.Close() }
