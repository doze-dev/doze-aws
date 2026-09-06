package console

import (
	"context"
	"encoding/json"
	"strings"
)

// ---- Step Functions: the execution history ----
//
// GetExecutionHistory answers sixty event shapes, each with its own
// *EventDetails key; the table wants one row shape. flattenEvent is the
// lift, and this file is the lift and the row.

// HistoryEvent is one GetExecutionHistory row, flattened for a table: the
// state name and the payload come from whichever *EventDetails the event
// carries, so the template never has to know the sixty detail-key names.
type HistoryEvent struct {
	ID      int64
	PrevID  int64
	When    string
	Type    string
	State   string // the state a StateEntered/Exited event names
	Payload string // input, output, parameters, or error+cause — whichever the event has
	Error   string
	Cause   string
	IsErr   bool
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
	for _, raw := range out.Events {
		evs = append(evs, flattenEvent(raw))
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
	if ts, ok := raw["timestamp"].(float64); ok {
		ev.When = epochToTime(ts)
	}
	for k, v := range raw {
		details, ok := v.(map[string]any)
		if !ok || !strings.HasSuffix(k, "EventDetails") {
			continue
		}
		ev.State = str(details["name"])
		if r := str(details["resource"]); r != "" && ev.State == "" {
			ev.State = arnLeaf(r)
		}
		for _, key := range []string{"input", "output", "parameters"} {
			if p := str(details[key]); p != "" {
				ev.Payload = prettyJSON(p)
				break
			}
		}
		ev.Error, ev.Cause = str(details["error"]), str(details["cause"])
	}
	ev.IsErr = ev.Error != "" || strings.HasSuffix(ev.Type, "Failed") ||
		strings.HasSuffix(ev.Type, "TimedOut") || strings.HasSuffix(ev.Type, "Aborted")
	return ev
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
