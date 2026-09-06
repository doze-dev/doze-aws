package stepfunctions

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	bolt "go.etcd.io/bbolt"
)

// arn:aws:states:::states:startExecution — a Task that starts another
// execution. Three patterns, as on AWS:
//
//   - plain: start it and move on with {ExecutionArn, StartDate};
//   - .sync / .sync:2: park until the child finishes and take its outcome
//     as the result (.sync carries the child's Output as a JSON string,
//     .sync:2 as the parsed value — the ":2" exists for that one reason);
//   - .waitForTaskToken: start it with a token in its input and park until
//     something redeems the token (the child, usually).
//
// It runs on the driver rather than a worker: starting an execution is a
// store write and a nudge, and parking on the child is a row in the
// childwaits bucket that the child's finalize consults. A restart re-checks
// every parked frame's child, so a crash between the child finishing and the
// parent hearing of it is closed by the resume sweep, not lost.

const childStartResource = "arn:aws:states:::states:startExecution"

// bucketChildWaits maps a child execution's key to the parent frame waiting
// on it: exec key \x00 frame id.
var bucketChildWaits = []byte("childwaits")

// childPattern reports how a startExecution resource waits.
func childPattern(resource string) (sync bool, parsedOutput bool) {
	base := strings.TrimSuffix(resource, ".waitForTaskToken")
	return strings.HasSuffix(base, ".sync") || strings.HasSuffix(base, ".sync:2"), strings.HasSuffix(base, ".sync:2")
}

// startChild is the driver-side handler for the resource. The frame is
// CALLING (or PARKED with a token) when it arrives.
func (g *engine) startChild(r *run, call asl.EffCallTask) {
	f := r.e.Exec.Frame(call.Frame)
	if f == nil {
		return
	}
	g.taskScheduledEvents(r, f, call)
	var p map[string]any
	if err := json.Unmarshal(call.Input, &p); err != nil || p == nil {
		g.deliver(r, f, failResult(asl.ErrTaskFailed, "states:startExecution needs Parameters with StateMachineArn"))
		return
	}
	machineARN := awsjson.Str(p, "StateMachineArn")
	m, versionARN, aliasARN, aerr := g.srv.startTarget(machineARN)
	if aerr != nil {
		g.deliver(r, f, failResult("StepFunctions."+aerr.Code, aerr.Message))
		return
	}
	name := awsjson.Str(p, "Name")
	if name == "" {
		name = randomExecName()
	} else if aerr := checkName(name); aerr != nil {
		g.deliver(r, f, failResult("StepFunctions."+aerr.Code, aerr.Message))
		return
	}
	input := map[string]any{}
	if in, ok := p["Input"].(map[string]any); ok {
		input = in
	} else if s, ok := p["Input"].(string); ok && s != "" {
		if json.Unmarshal([]byte(s), &input) != nil {
			g.deliver(r, f, failResult("StepFunctions.InvalidExecutionInput", "Input is not a JSON object"))
			return
		}
	}
	sync, _ := childPattern(call.Resource)
	if sync || call.Token != "" {
		// AWS adds the parent's id to a child it will wait for, so the child
		// can find its way back.
		input["AWS_STEP_FUNCTIONS_STARTED_BY_EXECUTION_ID"] = r.e.ARN
	}
	rawInput, _ := json.Marshal(input)

	if call.Token != "" {
		g.registerToken(r, call)
	}
	var child *Execution
	if m.Type == "EXPRESS" {
		e, aerr := g.srv.newExpressExecution(g.workerCtx, m, map[string]any{"name": name, "input": string(rawInput)}, versionARN, aliasARN)
		if aerr == nil {
			e.StartedBy = r.e.ARN
			if err := g.srv.store.SaveTransition(e, nil, nil); err != nil {
				aerr = asAPIError(err)
			} else {
				child = e
			}
		}
		if aerr != nil {
			g.deliver(r, f, failResult("StepFunctions."+aerr.Code, aerr.Message))
			return
		}
	} else {
		// Same name, still running, same input answers the existing one, as
		// StartExecution itself does; anything else on a taken name fails.
		if existing, _ := g.srv.store.GetExecution(m.Name, name); existing != nil {
			if existing.Status == "RUNNING" && jsonEqual(json.RawMessage(existing.Input), rawInput) {
				child = existing
			} else {
				g.deliver(r, f, failResult("StepFunctions.ExecutionAlreadyExists", "Execution Already Exists: '"+existing.ARN+"'"))
				return
			}
		}
		if child == nil {
			e, aerr := g.srv.launch(launchSpec{
				Machine: m, VersionARN: versionARN, AliasARN: aliasARN, Name: name, Input: string(rawInput),
				TraceHeader: r.e.TraceHeader, StartedBy: r.e.ARN,
			})
			if aerr != nil {
				g.deliver(r, f, failResult("StepFunctions."+aerr.Code, aerr.Message))
				return
			}
			child = e
		}
	}
	if m.Type == "EXPRESS" {
		// Written before the nudge: an Express child can finish before the
		// driver returns here otherwise. (The launch above nudged Standard
		// children already; their first step runs after this call returns,
		// on the same goroutine.)
		if sync {
			g.parkOnChild(r, f, child)
		}
		g.srv.engine.nudge(child.Key())
	} else if sync {
		g.parkOnChild(r, f, child)
	}
	if !sync && call.Token == "" {
		out, _ := json.Marshal(map[string]any{"ExecutionArn": child.ARN, "StartDate": epoch(child.StartedAt)})
		g.deliver(r, f, asl.TaskResult{Output: out})
		return
	}
	if !sync {
		// Token pattern: the send half is done; the token does the rest.
		g.persist(r)
		return
	}
}

// parkOnChild records that f waits for child and persists the park.
func (g *engine) parkOnChild(r *run, f *asl.Frame, child *Execution) {
	f.Status = asl.FrameParked
	f.WaitExec = child.Key()
	_, resourceType, resourceAPI := taskFamily(childStartResource + ".sync")
	g.event(r, f, "TaskSubmitted", "taskSubmittedEventDetails", map[string]any{
		"resourceType": resourceType, "resource": resourceAPI,
		"output": fmt.Sprintf(`{"ExecutionArn":%q}`, child.ARN), "outputDetails": notTruncated(),
	})
	g.srv.store.putChildWait(child.Key(), r.key, f.ID)
	g.persist(r)
}

// childFinished runs in the child's finalize: if a parent frame waits on
// it, the child's outcome becomes that frame's task result.
func (g *engine) childFinished(child *Execution) {
	parentKey, frame, ok := g.srv.store.takeChildWait(child.Key())
	if !ok {
		return
	}
	parent := g.ensure(parentKey)
	if parent == nil {
		return
	}
	f := parent.e.Exec.Frame(frame)
	if f == nil || f.Status != asl.FrameParked || f.WaitExec != child.Key() {
		return
	}
	f.WaitExec = ""
	s := parent.stateOf(f, f.State)
	parsed := false
	if s != nil {
		_, parsed = childPattern(s.Resource)
	}
	g.deliver(parent, f, childOutcome(child, parsed))
	if parent.e.Status == "RUNNING" {
		g.drive(parent)
	}
}

// childOutcome is the .sync result: the child described, its Output as a
// string for .sync and as a value for .sync:2. A child that did not succeed
// fails the task with States.TaskFailed and the description as the cause,
// which is what a Catch on the parent gets to read.
func childOutcome(child *Execution, parsedOutput bool) asl.TaskResult {
	desc := map[string]any{
		"ExecutionArn":    child.ARN,
		"Name":            child.Name,
		"StateMachineArn": child.MachineARN,
		"Status":          child.Status,
		"StartDate":       epoch(child.StartedAt),
		"StopDate":        epoch(child.StoppedAt),
		"Input":           child.Input,
		"InputDetails":    map[string]any{"Included": true},
	}
	if child.Output != nil {
		if parsedOutput {
			desc["Output"] = json.RawMessage(child.Output)
		} else {
			desc["Output"] = string(child.Output)
		}
		desc["OutputDetails"] = map[string]any{"Included": true}
	}
	if child.Error != "" {
		desc["Error"] = child.Error
		desc["Cause"] = child.Cause
	}
	raw, _ := json.Marshal(desc)
	if child.Status != "SUCCEEDED" {
		return failResult(asl.ErrTaskFailed, string(raw))
	}
	return asl.TaskResult{Output: raw}
}

// resumeChildWaits runs when a run is loaded: a frame parked on a child
// that already finished (the crash-between window) is delivered now.
func (g *engine) resumeChildWaits(r *run) {
	for _, f := range r.e.Exec.Frames {
		if f.Status != asl.FrameParked || f.WaitExec == "" {
			continue
		}
		child, err := g.srv.store.GetExecutionByKey(f.WaitExec)
		if err != nil || child == nil {
			g.deliver(r, f, failResult(asl.ErrTaskFailed, "the child execution "+f.WaitExec+" no longer exists"))
			continue
		}
		if child.Status != "RUNNING" {
			g.srv.store.takeChildWait(child.Key())
			f.WaitExec = ""
			s := r.stateOf(f, f.State)
			parsed := false
			if s != nil {
				_, parsed = childPattern(s.Resource)
			}
			g.deliver(r, f, childOutcome(child, parsed))
		}
	}
}

// ---- store ----

func (s *Store) putChildWait(childKey, parentKey string, frame int) error {
	return s.put(bucketChildWaits, []byte(childKey), fmt.Sprintf("%s\x00%d", parentKey, frame))
}

// takeChildWait reads and deletes the wait row for a child.
func (s *Store) takeChildWait(childKey string) (parentKey string, frame int, ok bool) {
	var v string
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketChildWaits)
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(childKey))
		if raw == nil {
			return nil
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		return b.Delete([]byte(childKey))
	})
	if err != nil || v == "" {
		return "", 0, false
	}
	// The parent key carries its own \x00 (machine \x00 name); the frame id
	// is after the last one.
	i := strings.LastIndex(v, "\x00")
	if i < 0 {
		return "", 0, false
	}
	fmt.Sscanf(v[i+1:], "%d", &frame)
	return v[:i], frame, true
}

// isChildStart reports whether a Task resource is a states:startExecution
// in any of its three patterns.
func isChildStart(resource string) bool { return strings.HasPrefix(resource, childStartResource) }
