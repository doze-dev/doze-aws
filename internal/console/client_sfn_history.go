package console

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---- Step Functions: the execution history ----
//
// GetExecutionHistory answers sixty event shapes, each with its own
// *EventDetails key; the table wants one row shape. flattenEvent is the
// lift, attribute gives every row the state it belongs to, and this file is
// the lift and the row.

// HistoryEvent is one GetExecutionHistory row, flattened for a table.
type HistoryEvent struct {
	ID     int64
	PrevID int64
	When   string // full timestamp, for the title
	Clock  string // HH:MM:SS.mmm — what a person reads down a column
	// Elapsed is the time since the execution started; Delta the time since
	// the previous row. Debugging a slow execution is reading these two.
	Elapsed string
	Delta   string
	Type    string
	// Kind classes the event for colour: enter, exit, sched, start, ok, err,
	// or info for the execution-level rows.
	Kind     string
	State    string // the state this event belongs to, attributed through previousEventId
	Resource string // a task event's resource, "lambda:invoke", or an activity's name
	Input    string // pretty JSON, when the event carries one
	Output   string
	Params   string
	Error    string
	Cause    string
	IsErr    bool
	Token    string // a TaskScheduled's task token, for the send-result form
}

// HasDetail says whether the row can expand.
func (e HistoryEvent) HasDetail() bool {
	return e.Input != "" || e.Output != "" || e.Params != "" || e.Error != ""
}

func (b *backend) ExecutionHistory(ctx context.Context, arn string) ([]HistoryEvent, error) {
	body, err := b.sfnCall(ctx, "GetExecutionHistory", map[string]any{
		"executionArn": arn, "maxResults": 1000, "includeExecutionData": true,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Events []map[string]any `json:"events"`
	}
	json.Unmarshal(body, &out)
	evs := make([]HistoryEvent, 0, len(out.Events))
	stamps := make([]float64, 0, len(out.Events))
	for _, raw := range out.Events {
		evs = append(evs, flattenEvent(raw))
		ts, _ := raw["timestamp"].(float64)
		stamps = append(stamps, ts)
	}
	attribute(evs)
	for i := range evs {
		if len(stamps) == 0 || stamps[i] == 0 {
			continue
		}
		evs[i].Elapsed = "+" + spanText(stamps[i]-stamps[0])
		if i > 0 {
			evs[i].Delta = spanText(stamps[i] - stamps[i-1])
		}
	}
	return evs, nil
}

// flattenEvent lifts the one *EventDetails object an event carries into the
// flat row the table renders.
func flattenEvent(raw map[string]any) HistoryEvent {
	ev := HistoryEvent{Type: str(raw["type"])}
	if id, ok := raw["id"].(float64); ok {
		ev.ID = int64(id)
	}
	if id, ok := raw["previousEventId"].(float64); ok {
		ev.PrevID = int64(id)
	}
	if ts, ok := raw["timestamp"].(float64); ok && ts > 0 {
		t := time.UnixMilli(int64(ts * 1000)).Local()
		ev.When = t.Format("2006-01-02 15:04:05.000 MST")
		ev.Clock = t.Format("15:04:05.000")
	}
	for k, v := range raw {
		details, ok := v.(map[string]any)
		if !ok || !strings.HasSuffix(k, "EventDetails") {
			continue
		}
		ev.State = str(details["name"])
		if rt, r := str(details["resourceType"]), str(details["resource"]); r != "" {
			ev.Resource = r
			if rt != "" {
				ev.Resource = rt + ":" + r
			}
			if strings.HasPrefix(r, "arn:") {
				ev.Resource = arnLeaf(r)
			}
		}
		if p := str(details["input"]); p != "" {
			ev.Input = prettyJSON(p)
		}
		if p := str(details["output"]); p != "" {
			ev.Output = prettyJSON(p)
		}
		if p := str(details["parameters"]); p != "" {
			ev.Params = prettyJSON(p)
			// The token a .waitForTaskToken Task sent is the one thing a
			// person reading this row wants to copy out of it.
			var params map[string]any
			if json.Unmarshal([]byte(p), &params) == nil {
				ev.Token = findToken(params)
			}
		}
		ev.Error, ev.Cause = str(details["error"]), str(details["cause"])
	}
	ev.IsErr = ev.Error != "" || strings.HasSuffix(ev.Type, "Failed") ||
		strings.HasSuffix(ev.Type, "TimedOut") || strings.HasSuffix(ev.Type, "Aborted")
	ev.Kind = eventKind(ev)
	return ev
}

func eventKind(ev HistoryEvent) string {
	switch {
	case ev.IsErr:
		return "err"
	case strings.HasSuffix(ev.Type, "Entered"):
		return "enter"
	case strings.HasSuffix(ev.Type, "Exited"):
		return "exit"
	case strings.HasSuffix(ev.Type, "Scheduled"), strings.HasSuffix(ev.Type, "Submitted"):
		return "sched"
	case strings.HasSuffix(ev.Type, "Started"):
		return "start"
	case strings.HasSuffix(ev.Type, "Succeeded"):
		return "ok"
	}
	return "info"
}

// attribute gives every event the state it belongs to: a task event names
// its resource, not its Task, so it is walked back through previousEventId
// to the StateEntered that opened its frame — the same rule the graph
// overlay applies, and what keeps two branches of a Parallel apart.
func attribute(evs []HistoryEvent) {
	byID := make(map[int64]int, len(evs))
	for i, ev := range evs {
		byID[ev.ID] = i
	}
	for i := range evs {
		if evs[i].State != "" {
			continue
		}
		prev, ok := byID[evs[i].PrevID]
		for ok {
			p := evs[prev]
			if strings.HasSuffix(p.Type, "StateEntered") {
				evs[i].State = p.State
				break
			}
			if strings.HasSuffix(p.Type, "StateExited") || p.PrevID >= p.ID {
				break
			}
			prev, ok = byID[p.PrevID]
		}
	}
}

// findToken locates a task token in a Parameters object — any string under
// a key named TaskToken or taskToken, at any depth.
func findToken(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok && strings.EqualFold(k, "taskToken") && s != "" {
				return s
			}
			if found := findToken(val); found != "" {
				return found
			}
		}
	case []any:
		for _, val := range t {
			if found := findToken(val); found != "" {
				return found
			}
		}
	}
	return ""
}

// spanText renders seconds the way a person reads a duration column: ms
// under a second, seconds with a decimal under a minute, then m and s.
func spanText(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 1:
		return fmt.Sprintf("%dms", int(seconds*1000+0.5))
	case seconds < 60:
		return fmt.Sprintf("%.1fs", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm %ds", int(seconds)/60, int(seconds)%60)
	}
	return fmt.Sprintf("%dh %dm", int(seconds)/3600, int(seconds)%3600/60)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
