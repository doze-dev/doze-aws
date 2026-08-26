// Package asl implements the Amazon States Language: the definition a Step
// Functions state machine is written in.
//
// The package is deliberately pure. It knows nothing about AWS service calls,
// bbolt, HTTP or goroutines — it parses a definition, tells you whether it is
// valid, and (from exec.go) advances one execution frame by one step. The
// service package drives it and performs the effects it asks for.
//
// That split is what makes two things possible. Express executions and Standard
// executions are the same interpreter with two callers, one persisting and one
// not; and an execution parked on a task token is a serialisable value rather
// than a goroutine, so it survives a restart.
//
// The vocabulary here is AWS's, not ours: a definition has States, a state has a
// Type, and the field names match the wire exactly. Renaming them to read better
// in Go would mean a translation layer in every direction and an extra place for
// a typo to hide.
package asl

import "encoding/json"

// StateType is the discriminator every state carries.
type StateType string

const (
	Pass     StateType = "Pass"
	Task     StateType = "Task"
	Choice   StateType = "Choice"
	Wait     StateType = "Wait"
	Succeed  StateType = "Succeed"
	Fail     StateType = "Fail"
	Parallel StateType = "Parallel"
	Map      StateType = "Map"
)

// QueryLanguage selects how paths and payloads are evaluated. It may be set on
// the machine and overridden per state, which is why it is a pointer in State:
// "unset" and "explicitly JSONPath" have to be told apart when the machine says
// JSONata.
type QueryLanguage string

const (
	JSONPath QueryLanguage = "JSONPath"
	JSONata  QueryLanguage = "JSONata"
)

// Definition is a whole state machine.
type Definition struct {
	Comment        string
	StartAt        string
	States         map[string]*State
	TimeoutSeconds *float64
	Version        string
	QueryLanguage  QueryLanguage

	// Order is the states in the order the document listed them. Go map
	// iteration is random, and a validation report that lists errors in a
	// different order on every run is a report nobody can diff.
	Order []string
}

// State is every state type in one struct.
//
// A sum type per state would be tidier in isolation and worse here: the parser
// would need a two-pass decode to learn the type before choosing a target, and
// every walk over the machine would become a type switch. ASL's own schema is
// one object with conditional fields, so this mirrors it. `analyse.go` is what
// enforces that the fields present match the Type — the parser deliberately
// does not, so that a definition with a misplaced field produces a *validation*
// error naming it rather than a parse error that swallows it.
type State struct {
	Type    StateType
	Comment string

	// Transition. Exactly one of Next or End is legal, except on Succeed and
	// Fail, which are terminal and may carry neither.
	Next string
	End  bool

	QueryLanguage QueryLanguage

	// Data flow, JSONPath dialect. InputPath and OutputPath are pointers
	// because null is meaningful and distinct from absent: `"InputPath": null`
	// means "the input to this state is {}", where absent means "$".
	InputPath      *string
	OutputPath     *string
	Parameters     json.RawMessage
	ResultSelector json.RawMessage
	ResultPath     *string

	// Data flow, JSONata dialect. Arguments replaces Parameters, Output
	// replaces ResultSelector plus ResultPath, and Assign writes variables.
	Arguments json.RawMessage
	Output    json.RawMessage
	Assign    json.RawMessage

	// Pass
	Result json.RawMessage

	// Task
	Resource             string
	Credentials          json.RawMessage
	TimeoutSecondsState  *float64
	TimeoutSecondsPath   string
	HeartbeatSeconds     *float64
	HeartbeatSecondsPath string

	// Choice
	Choices []*ChoiceRule
	Default string

	// Wait — exactly one of these four.
	Seconds       *float64
	SecondsPath   string
	Timestamp     string
	TimestampPath string

	// Fail
	Error     string
	ErrorPath string
	Cause     string
	CausePath string

	// Parallel
	Branches []*Definition

	// Map
	ItemProcessor              *Definition
	Iterator                   *Definition // the retired spelling of ItemProcessor
	ItemsPath                  string
	ItemSelector               json.RawMessage
	MaxConcurrency             *float64
	MaxConcurrencyPath         string
	ItemReader                 json.RawMessage
	ItemBatcher                json.RawMessage
	ResultWriter               json.RawMessage
	ToleratedFailureCount      *float64
	ToleratedFailurePercentage *float64

	// Error handling, on Task, Parallel and Map.
	Retry []*Retrier
	Catch []*Catcher

	// Name is the key this state was listed under. Carried on the state so a
	// validation error or a history event can name itself without the caller
	// threading the key through every call.
	Name string
}

// Retrier is one entry of a Retry array.
type Retrier struct {
	ErrorEquals     []string
	IntervalSeconds *float64
	MaxAttempts     *int
	BackoffRate     *float64
	MaxDelaySeconds *float64
	JitterStrategy  string
}

// Catcher is one entry of a Catch array.
type Catcher struct {
	ErrorEquals []string
	Next        string
	ResultPath  *string
	Output      json.RawMessage
	Assign      json.RawMessage
}

// ChoiceRule is one entry of a Choices array, or one operand of an And/Or/Not.
//
// A rule is either a comparison (Variable plus exactly one operator) or a
// boolean combinator, never both. Nested rules carry no Next; only a top-level
// rule does.
type ChoiceRule struct {
	Variable string
	Next     string

	And []*ChoiceRule
	Or  []*ChoiceRule
	Not *ChoiceRule

	// Condition is the JSONata dialect's replacement for the whole comparison
	// vocabulary below.
	Condition json.RawMessage

	// Comparison is the operator name and its operand, e.g. "StringEquals" and
	// "hello". Held generically rather than as sixty typed fields, because the
	// operator set is a closed list `choice.go` owns and every one of them
	// behaves the same way structurally: read a value, compare, yield a bool.
	Comparison *Comparison
}

// Comparison is one operator applied to one operand.
type Comparison struct {
	Op string
	// Operand is the literal the variable is compared against. For the *Path
	// spellings (StringEqualsPath and friends) it is a reference path to read
	// the operand from instead.
	Operand json.RawMessage
	// IsPath records that Op ended in "Path", so the operand names a location
	// rather than a value. Stored rather than re-derived, because the operator
	// with the suffix stripped is what the comparison table is keyed on.
	IsPath bool
}

// Terminal reports whether the state ends its branch.
func (s *State) Terminal() bool {
	return s.End || s.Type == Succeed || s.Type == Fail
}

// Processor returns the Map state's inner machine under either spelling.
// ItemProcessor is current; Iterator is the retired name AWS still accepts, and
// state machines written years ago are exactly the ones being run locally.
func (s *State) Processor() *Definition {
	if s.ItemProcessor != nil {
		return s.ItemProcessor
	}
	return s.Iterator
}

// Lang returns the query language in force for this state: its own if it set
// one, otherwise the machine's, otherwise JSONPath.
func (d *Definition) Lang(s *State) QueryLanguage {
	if s != nil && s.QueryLanguage != "" {
		return s.QueryLanguage
	}
	if d.QueryLanguage != "" {
		return d.QueryLanguage
	}
	return JSONPath
}
