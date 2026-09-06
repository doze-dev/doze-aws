package asl

import "encoding/json"

// Effects: what the interpreter asks the service to do next.
//
// Exactly one Effect is returned per entry-point call. EffContinue means "the
// frame is still runnable, call Advance again" — so the engine owns the loop
// and can persist (and emit history) between every transition, which is the
// durability contract: a crash between any two effects loses nothing.

// Effect is the closed set below and nothing else.
type Effect interface{ isEffect() }

// EffContinue: an internal transition completed (a Pass exited, a Choice
// chose). The frame is RUNNABLE again.
type EffContinue struct{}

// EffCallTask: the frame evaluated a Task state's input and needs the call
// performed. The frame is CALLING — or already PARKED when Token is set,
// because a .waitForTaskToken task's result arrives via SendTaskSuccess
// rather than from the call itself.
type EffCallTask struct {
	Frame    int
	Resource string          // the Task's Resource, verbatim
	Input    json.RawMessage // the evaluated task input
	Token    string          // non-empty for token-pattern tasks
	Deadline int64           // epoch millis TimeoutSeconds cutoff, 0 = none
}

// EffSleep: the frame is SLEEPING (a Wait) or RETRY_WAIT (backoff); call
// Wake once Until passes.
type EffSleep struct {
	Frame int
	Until int64 // epoch millis
}

// EffSpawn: a Parallel or Map started. The parent is JOIN; the listed child
// frames are RUNNABLE (or PENDING, held by MaxConcurrency).
type EffSpawn struct {
	Parent int
	Frames []int
}

// EffDone: the frame finished. On the root frame this ends the execution;
// on a child it triggers the parent's join check.
type EffDone struct {
	Frame  int
	Output json.RawMessage
}

// EffFail: the frame failed with no Catch to absorb it. On the root frame
// this fails the execution; on a child the failure surfaces at the parent.
type EffFail struct {
	Frame   int
	Failure *Failure
}

func (EffContinue) isEffect() {}
func (EffCallTask) isEffect() {}
func (EffSleep) isEffect()    {}
func (EffSpawn) isEffect()    {}
func (EffDone) isEffect()     {}
func (EffFail) isEffect()     {}

// TaskResult is a task outcome fed back into the interpreter via Deliver: a
// completed call, a SendTaskSuccess/Failure payload, a joined Parallel/Map
// result, or a service-side timeout. Failure nil means success.
type TaskResult struct {
	Output  json.RawMessage
	Failure *Failure
}

// Note is a history-relevant fact a transition produced, in interpreter
// vocabulary. The service's history layer maps Notes to AWS event types and
// ids; Express discards them.
type Note struct {
	Kind  NoteKind
	Frame int
	State string
	Type  StateType
	Data  json.RawMessage // input/output/error payload for the event details
}

// NoteKind names what happened.
type NoteKind string

const (
	// NoteEntered: a state began, Data is its raw input.
	NoteEntered NoteKind = "entered"
	// NoteExited: a state completed, Data is its output.
	NoteExited NoteKind = "exited"
	// NoteFailed: a state's failure was not caught, Data is {error, cause}.
	NoteFailed NoteKind = "failed"
	// NoteRetried: a failure was absorbed by a Retrier; a backoff is running.
	NoteRetried NoteKind = "retried"
	// NoteCaught: a failure was absorbed by a Catcher; Data is the error
	// object it injected.
	NoteCaught NoteKind = "caught"
)
