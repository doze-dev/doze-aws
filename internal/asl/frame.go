package asl

import (
	"encoding/json"
	"fmt"
	"time"
)

// The execution snapshot contract.
//
// Everything the interpreter needs to continue an execution lives in exactly
// two JSON-serialisable values: the frozen definition text (parsed on load)
// and the Exec with its flat frame list. No goroutine, channel or closure is
// part of execution state — which is what lets an execution parked on a task
// token survive a restart, and what makes recovery identical to steady state:
// load every running execution and resume.
//
// The frame list is flat rather than a tree. JSON round-trips it trivially,
// sibling scans (a Parallel join, Map concurrency promotion) are one linear
// pass over a handful of entries, and frame IDs stay stable for history
// chaining and token records. Children point up via Parent; a parent finds
// its children with one scan.

// FrameStatus is where a frame is between transitions. Every status other
// than the two terminal ones names the effect that must be re-issued or the
// event that must arrive — that is the whole restart contract:
//
//	RUNNABLE            call Advance
//	CALLING             a task call is (or after a restart, must be re-)
//	                    dispatched; its TaskResult arrives via Deliver
//	SLEEPING            a Wait state; when WakeAt passes, call Wake
//	RETRY_WAIT          retry backoff; when WakeAt passes, call Wake
//	PARKED              awaiting SendTaskSuccess/Failure on Token, or a
//	                    child execution; the result arrives via Deliver
//	JOIN                a Parallel/Map parent; its children carry the rest
//	PENDING             a spawned Map item held back by MaxConcurrency
//	DONE / FAILED       terminal
type FrameStatus string

const (
	FrameRunnable  FrameStatus = "RUNNABLE"
	FrameCalling   FrameStatus = "CALLING"
	FrameSleeping  FrameStatus = "SLEEPING"
	FrameRetryWait FrameStatus = "RETRY_WAIT"
	FrameParked    FrameStatus = "PARKED"
	FrameJoin      FrameStatus = "JOIN"
	FramePending   FrameStatus = "PENDING"
	FrameDone      FrameStatus = "DONE"
	FrameFailed    FrameStatus = "FAILED"
)

// Terminal reports whether the frame has finished, either way.
func (st FrameStatus) Terminal() bool { return st == FrameDone || st == FrameFailed }

// DefHop locates one descent into a sub-definition: a Parallel state and
// which branch, or a Map state (Branch == -1) and its ItemProcessor. A
// Parallel nested in a Map nested in a Parallel is just a longer slice.
type DefHop struct {
	State  string `json:"s"`
	Branch int    `json:"b"`
}

// Sub resolves a hop list to the definition it names. The root frame's nil
// list resolves to the machine itself.
func (d *Definition) Sub(ref []DefHop) (*Definition, error) {
	cur := d
	for _, hop := range ref {
		s := cur.States[hop.State]
		if s == nil {
			return nil, fmt.Errorf("asl: no state %q on the way to a sub-definition", hop.State)
		}
		switch {
		case hop.Branch >= 0:
			if hop.Branch >= len(s.Branches) {
				return nil, fmt.Errorf("asl: state %q has no branch %d", hop.State, hop.Branch)
			}
			cur = s.Branches[hop.Branch]
		default:
			cur = s.Processor()
			if cur == nil {
				return nil, fmt.Errorf("asl: state %q has no ItemProcessor", hop.State)
			}
		}
	}
	return cur, nil
}

// Frame is one branch of one execution, between two transitions.
type Frame struct {
	// ID is 1 for the root frame; allocated from Exec.NextFrame, never reused.
	ID     int `json:"id"`
	Parent int `json:"parent,omitempty"` // 0 for the root
	// Branch is the index in the parent's Branches, or the Map item index.
	Branch int `json:"branch,omitempty"`

	// Def locates which Definition State resolves in; nil is the machine root.
	Def    []DefHop    `json:"def,omitempty"`
	State  string      `json:"state"` // current state name; "" = about to enter StartAt
	Status FrameStatus `json:"status"`

	Input  json.RawMessage `json:"input"`            // raw input to the current state, pre-InputPath
	Output json.RawMessage `json:"output,omitempty"` // set on DONE

	// TaskInput is the evaluated task input, kept for CALLING / RETRY_WAIT /
	// PARKED so a restart re-dispatches without re-evaluating Parameters —
	// which would mint a new token or re-run intrinsics.
	TaskInput json.RawMessage `json:"taskInput,omitempty"`

	// Suspension bookkeeping, all epoch milliseconds.
	WakeAt      int64 `json:"wakeAt,omitempty"`      // SLEEPING / RETRY_WAIT
	Deadline    int64 `json:"deadline,omitempty"`    // CALLING/PARKED: TimeoutSeconds cutoff, 0 = none
	HeartbeatAt int64 `json:"heartbeatAt,omitempty"` // PARKED: last heartbeat (park time to start)
	HeartbeatS  int64 `json:"heartbeatS,omitempty"`  // PARKED: HeartbeatSeconds, 0 = none
	// TimeoutS is the seconds a JSONata Task's TimeoutSeconds expression
	// evaluated to on entry, so a retry re-dispatch does not re-evaluate it.
	TimeoutS float64 `json:"timeoutS,omitempty"`
	// Limit is the MaxConcurrency a JSONata Map evaluated on entry, for the
	// same reason: PromotePending needs it after the expression is gone.
	Limit    int    `json:"limit,omitempty"`
	Token    string `json:"token,omitempty"`    // PARKED on a task token
	WaitExec string `json:"waitExec,omitempty"` // PARKED on a child execution's store key
	MapRun   string `json:"mapRun,omitempty"`   // PARKED on a Distributed Map's Map Run ARN

	// Attempts counts retries per Retrier of the current state, index-parallel
	// to the state's Retry array. Reset on every state transition. RetryIdx
	// names which retrier owns the current RETRY_WAIT, so Wake increments the
	// right slot.
	Attempts []int `json:"attempts,omitempty"`
	RetryIdx int   `json:"retryIdx,omitempty"`

	// MapItem is the item a Map ItemProcessor frame is running, for
	// $$.Map.Item.Value; nil on every other frame.
	MapItem json.RawMessage `json:"mapItem,omitempty"`

	EnteredAt int64    `json:"enteredAt,omitempty"` // for $$.State.EnteredTime
	Failure   *Failure `json:"failure,omitempty"`   // set on FAILED

	// Vars is the frame's variable scope, written by Assign and read as
	// `$name` in JSONata expressions. A frame IS a scope: the root frame holds
	// the execution's variables, and a Parallel branch or Map iteration starts
	// with a copy of its parent's and never writes back — AWS's rule that an
	// inner scope reads outer variables but cannot assign into them. A Catch on
	// the Parallel/Map state itself runs on the parent frame, so its Assign
	// lands in the outer scope, as AWS documents.
	Vars map[string]json.RawMessage `json:"vars,omitempty"`

	// PrevEventID is the id of the last history event in THIS frame's causal
	// chain. Nested Parallel/Map history is why it is per-frame: a history
	// event's previousEventId points along the branch, not at the globally
	// previous event. A spawned child starts from its parent's state-started
	// event id.
	PrevEventID int64 `json:"prevEventId,omitempty"`

	// SpawnGen counts the Parallel/Map states this frame has spawned children
	// for, and Gen is the generation a child was spawned in. Settled children
	// stay in the frame list — they are history, and a restart needs them —
	// so a second Parallel on the same frame must not see the first one's
	// branches when it joins. Before this stamp it did: a Map after a
	// Parallel joined with trailing nulls, and a Map after a caught branch
	// failure failed with that branch's error.
	SpawnGen int `json:"spawnGen,omitempty"`
	Gen      int `json:"gen,omitempty"`
}

// RetryCount is $$.State.RetryCount: how many retries the current state has
// used so far, across all its retriers.
func (f *Frame) RetryCount() int {
	n := 0
	for _, a := range f.Attempts {
		n += a
	}
	return n
}

// Exec is the pure-interpreter view of one execution: what the context
// object exposes, plus the frames. The service wraps it with the wire and
// status fields it owns.
type Exec struct {
	ID          string          `json:"id"`                    // execution ARN, for $$.Execution.Id
	Name        string          `json:"name"`                  // for $$.Execution.Name
	Machine     string          `json:"machine,omitempty"`     // state machine ARN, $$.StateMachine.Id
	MachineName string          `json:"machineName,omitempty"` // $$.StateMachine.Name
	StartTime   time.Time       `json:"startTime"`
	Input       json.RawMessage `json:"input"`
	RoleARN     string          `json:"roleArn,omitempty"`

	Frames    []*Frame `json:"frames"`
	NextFrame int      `json:"nextFrame"` // next frame ID to allocate
}

// StartExec builds a fresh execution: one root frame, runnable, holding the
// execution input. The interpreter's invariants (root ID 1, NextFrame) live
// here so no caller constructs them by hand.
func StartExec(id, name, machine, machineName, roleARN string, input json.RawMessage, start time.Time) *Exec {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	return &Exec{
		ID: id, Name: name, Machine: machine, MachineName: machineName,
		RoleARN: roleARN, StartTime: start, Input: input,
		Frames:    []*Frame{{ID: 1, Status: FrameRunnable, Input: input}},
		NextFrame: 2,
	}
}

// Frame returns the frame with the given ID, or nil.
func (ex *Exec) Frame(id int) *Frame {
	for _, f := range ex.Frames {
		if f.ID == id {
			return f
		}
	}
	return nil
}

// Root returns the root frame.
func (ex *Exec) Root() *Frame { return ex.Frame(1) }

// Children returns the frames the parent spawned for its CURRENT
// Parallel/Map state, in spawn order. Children of an earlier state on the
// same frame are still in the list but belong to a previous generation.
func (ex *Exec) Children(parent int) []*Frame {
	p := ex.Frame(parent)
	if p == nil {
		return nil
	}
	var out []*Frame
	for _, f := range ex.Frames {
		if f.Parent == parent && f.Gen == p.SpawnGen {
			out = append(out, f)
		}
	}
	return out
}

// BeginSpawn opens a new generation of children on the frame. Parallel and
// Map call it once, before spawning, so the join sees only these.
func (f *Frame) BeginSpawn() { f.SpawnGen++ }

// Spawn allocates a child frame under parent. The caller sets MapItem and
// flips PENDING to RUNNABLE as concurrency allows.
func (ex *Exec) Spawn(parent *Frame, branch int, def []DefHop, input json.RawMessage, status FrameStatus) *Frame {
	f := &Frame{
		ID: ex.NextFrame, Parent: parent.ID, Branch: branch, Gen: parent.SpawnGen,
		Def: def, Status: status, Input: input,
		PrevEventID: parent.PrevEventID,
		Vars:        copyVars(parent.Vars),
	}
	ex.NextFrame++
	ex.Frames = append(ex.Frames, f)
	return f
}

// Env is what the interpreter needs from the world besides the definition —
// injected so tests are deterministic and Express can refuse tokens.
type Env struct {
	Now time.Time
	// Rand yields [0,1) for retry jitter; nil means no jitter.
	Rand func() float64
	// NewToken mints a task token for .waitForTaskToken and activity tasks;
	// nil means tokens are unsupported (Express).
	NewToken func() string
}
