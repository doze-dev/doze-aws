package stepfunctions

import (
	"encoding/json"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/asl"
)

// History event construction — the shape GetExecutionHistory must answer
// with, and the two rules emulators most often get wrong:
//
// Ids are global (allocated from Execution.NextEventID) but previousEventId
// chains are PER FRAME: nested Parallel/Map branches interleave globally
// while each chains causally along its own branch, which is why Frame carries
// PrevEventID. And every input/output/parameters detail is a JSON-encoded
// STRING, not an object — the SDK's typed HistoryEvent says *string, and an
// object there breaks deserialisation.

// histEvent is one stored event.
type histEvent struct {
	ID        int64          `json:"id"`
	PrevID    int64          `json:"previousEventId"`
	TS        int64          `json:"ts"` // epoch millis
	Type      string         `json:"type"`
	DetailKey string         `json:"detailKey,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// wire renders the event the way the API answers it.
func (ev histEvent) wire(includeData bool) map[string]any {
	out := map[string]any{
		"id":              ev.ID,
		"previousEventId": ev.PrevID,
		"timestamp":       epoch(ev.TS),
		"type":            ev.Type,
	}
	if ev.DetailKey != "" {
		details := ev.Details
		if !includeData {
			details = map[string]any{}
			for k, v := range ev.Details {
				switch k {
				case "input", "output", "parameters":
				default:
					details[k] = v
				}
			}
		}
		out[ev.DetailKey] = details
	}
	return out
}

// event allocates the next id, chains it on the frame, and buffers it for
// the next SaveTransition. f may be nil for execution-level events, which
// chain on the root frame.
func (g *engine) event(r *run, f *asl.Frame, typ, detailKey string, details map[string]any) {
	if f == nil {
		f = r.e.Exec.Root()
	}
	ev := histEvent{
		ID:        r.e.NextEventID,
		TS:        g.srv.store.now(),
		Type:      typ,
		DetailKey: detailKey,
		Details:   details,
	}
	if f != nil {
		ev.PrevID = f.PrevEventID
		f.PrevEventID = ev.ID
	}
	r.e.NextEventID++
	r.pending = append(r.pending, ev)
}

// notTruncated is the detail AWS attaches to every payload it did not trim.
func notTruncated() map[string]any { return map[string]any{"truncated": false} }

// recordNotes maps interpreter Notes onto history events.
func (g *engine) recordNotes(r *run, notes []asl.Note) {
	for _, n := range notes {
		f := r.e.Exec.Frame(n.Frame)
		switch n.Kind {
		case asl.NoteEntered:
			g.event(r, f, string(n.Type)+"StateEntered", "stateEnteredEventDetails", map[string]any{
				"name":         n.State,
				"input":        string(n.Data),
				"inputDetails": notTruncated(),
			})
		case asl.NoteExited:
			g.event(r, f, string(n.Type)+"StateExited", "stateExitedEventDetails", map[string]any{
				"name":          n.State,
				"output":        string(n.Data),
				"outputDetails": notTruncated(),
			})
		case asl.NoteFailed, asl.NoteRetried, asl.NoteCaught:
			// Only Task failures have their own event family; a Fail state or
			// a broken path surfaces as the execution-level event instead.
			if n.Type != asl.Task {
				continue
			}
			s := r.stateOf(f, n.State)
			if s == nil {
				continue
			}
			var errCause struct {
				Error string `json:"error"`
				Cause string `json:"cause"`
			}
			json.Unmarshal(n.Data, &errCause)
			g.taskFailureEvent(r, f, s.Resource, errCause.Error, errCause.Cause)
		}
	}
}

// stateOf resolves the state a frame's note names.
func (r *run) stateOf(f *asl.Frame, name string) *asl.State {
	if f == nil {
		return nil
	}
	sub, err := r.def.Sub(f.Def)
	if err != nil {
		return nil
	}
	return sub.States[name]
}

// taskFamily says which event family a Task resource reports under: a bare
// function ARN is the LambdaFunction* family; the optimized integrations are
// Task* with a resourceType/resource pair.
func taskFamily(resource string) (prefix, resourceType, resourceAPI string) {
	base := strings.TrimSuffix(resource, ".waitForTaskToken")
	if strings.HasPrefix(base, "arn:aws:lambda:") {
		return "LambdaFunction", "", ""
	}
	if asl.IsActivityARN(base) {
		return "Activity", "", ""
	}
	rest := strings.TrimPrefix(base, "arn:aws:states:::")
	svc, api, _ := strings.Cut(rest, ":")
	return "Task", svc, api
}

// taskScheduledEvents emits the Scheduled and Started pair for a dispatch.
func (g *engine) taskScheduledEvents(r *run, f *asl.Frame, call asl.EffCallTask) {
	prefix, resourceType, resourceAPI := taskFamily(call.Resource)
	if prefix == "LambdaFunction" {
		details := map[string]any{
			"resource":     call.Resource,
			"input":        string(call.Input),
			"inputDetails": notTruncated(),
		}
		g.event(r, f, "LambdaFunctionScheduled", "lambdaFunctionScheduledEventDetails", details)
		g.event(r, f, "LambdaFunctionStarted", "", nil)
		return
	}
	details := map[string]any{
		"resourceType": resourceType,
		"resource":     resourceAPI,
		"region":       awsident.Region,
		"parameters":   string(call.Input),
	}
	if call.Token != "" {
		details["taskToken"] = call.Token
	}
	g.event(r, f, "TaskScheduled", "taskScheduledEventDetails", details)
	g.event(r, f, "TaskStarted", "taskStartedEventDetails", map[string]any{
		"resourceType": resourceType, "resource": resourceAPI,
	})
}

// taskSuccessEvent emits the family's Succeeded event.
func (g *engine) taskSuccessEvent(r *run, f *asl.Frame, resource string, output []byte) {
	prefix, resourceType, resourceAPI := taskFamily(resource)
	if prefix == "Activity" {
		g.activitySucceededEvent(r, f, output)
		return
	}
	if prefix == "LambdaFunction" {
		g.event(r, f, "LambdaFunctionSucceeded", "lambdaFunctionSucceededEventDetails", map[string]any{
			"output": string(output), "outputDetails": notTruncated(),
		})
		return
	}
	g.event(r, f, "TaskSucceeded", "taskSucceededEventDetails", map[string]any{
		"resourceType": resourceType, "resource": resourceAPI,
		"output": string(output), "outputDetails": notTruncated(),
	})
}

// taskFailureEvent emits the family's Failed or TimedOut event.
func (g *engine) taskFailureEvent(r *run, f *asl.Frame, resource, errName, cause string) {
	prefix, resourceType, resourceAPI := taskFamily(resource)
	if prefix == "Activity" {
		g.activityFailureEvent(r, f, errName, cause)
		return
	}
	timedOut := errName == asl.ErrTimeout || errName == asl.ErrHeartbeatTimeout
	if prefix == "LambdaFunction" {
		typ, key := "LambdaFunctionFailed", "lambdaFunctionFailedEventDetails"
		if timedOut {
			typ, key = "LambdaFunctionTimedOut", "lambdaFunctionTimedOutEventDetails"
		}
		g.event(r, f, typ, key, map[string]any{"error": errName, "cause": cause})
		return
	}
	typ, key := "TaskFailed", "taskFailedEventDetails"
	if timedOut {
		typ, key = "TaskTimedOut", "taskTimedOutEventDetails"
	}
	g.event(r, f, typ, key, map[string]any{
		"resourceType": resourceType, "resource": resourceAPI,
		"error": errName, "cause": cause,
	})
}
