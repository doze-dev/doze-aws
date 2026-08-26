package asl

import "fmt"

// The predefined error names ASL reserves. A Retrier or Catcher matches on
// these by string, and so does a Fail state's Error field.
//
// Getting these names exactly right is what makes Catch work. A state machine
// that catches States.TaskFailed and gets handed "TaskFailed" or
// "States.TaskFailure" does not catch it, falls through, and fails the whole
// execution — with a message that points at the task rather than at the
// mismatch. It is the most commonly wrong thing in a local Step Functions
// implementation, which is why the names live in one place with no second
// spelling anywhere.
const (
	// ErrAll matches every error name. Legal only as the sole entry of an
	// ErrorEquals, and only in the last Retrier or Catcher of its array.
	ErrAll = "States.ALL"

	ErrBranchFailed           = "States.BranchFailed"
	ErrDataLimitExceeded      = "States.DataLimitExceeded"
	ErrHeartbeatTimeout       = "States.HeartbeatTimeout"
	ErrIntrinsicFailure       = "States.IntrinsicFailure"
	ErrItemReaderFailed       = "States.ItemReaderFailed"
	ErrNoChoiceMatched        = "States.NoChoiceMatched"
	ErrParameterPathFailure   = "States.ParameterPathFailure"
	ErrPermissions            = "States.Permissions"
	ErrQueryEvaluationError   = "States.QueryEvaluationError"
	ErrResultPathMatchFailure = "States.ResultPathMatchFailure"
	ErrResultWriterFailed     = "States.ResultWriterFailed"
	ErrRuntime                = "States.Runtime"
	ErrTaskFailed             = "States.TaskFailed"
	ErrTimeout                = "States.Timeout"

	// ErrExceedToleratedFailureThreshold ends a Map that failed more items than
	// its tolerance allows.
	ErrExceedToleratedFailureThreshold = "States.ExceedToleratedFailureThreshold"
)

// reserved is every name above except States.ALL, for the rule that a
// user-defined error may not start with "States.".
var reserved = map[string]bool{
	ErrAll: true, ErrBranchFailed: true, ErrDataLimitExceeded: true,
	ErrHeartbeatTimeout: true, ErrIntrinsicFailure: true, ErrItemReaderFailed: true,
	ErrNoChoiceMatched: true, ErrParameterPathFailure: true, ErrPermissions: true,
	ErrQueryEvaluationError: true, ErrResultPathMatchFailure: true,
	ErrResultWriterFailed: true, ErrRuntime: true, ErrTaskFailed: true,
	ErrTimeout: true, ErrExceedToleratedFailureThreshold: true,
}

// Failure is an error name and its cause, which is the shape every ASL failure
// takes — a Task that threw, a Fail state, a timeout, a Choice with no match.
//
// It implements error so it can travel through ordinary Go plumbing, but the
// Name is what Retry and Catch match on, never the message.
type Failure struct {
	Name  string
	Cause string
}

func (f *Failure) Error() string {
	if f.Cause == "" {
		return f.Name
	}
	return f.Name + ": " + f.Cause
}

// Fail builds a Failure.
func Failf(name, format string, args ...any) *Failure {
	return &Failure{Name: name, Cause: fmt.Sprintf(format, args...)}
}

// IsReserved reports whether a name is one ASL defines. Used by the analyser to
// refuse a definition that invents its own States.* name, which AWS rejects —
// and which would otherwise silently never match anything.
func IsReserved(name string) bool { return reserved[name] }
