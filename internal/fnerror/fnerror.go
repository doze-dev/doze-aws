// Package fnerror is the error a step's work returns when Lambda served an
// invoke and the function raised (the X-Amz-Function-Error header).
//
// trace.Step and trace.StepDetail recognise it, through any wrapping, and
// record it on the event as the target's answer (Event.FnError and FnBody)
// instead of as a failed delivery. The caller still receives it as an error,
// because a poller must not delete a message the function did not take.
//
// It is internal, and the interface trace looks for is unexported, so the public
// API of trace grows by the two fields of the event and nothing else.
package fnerror

import "fmt"

// Error is a function that raised.
type Error struct {
	Kind    string // the header's value: "Unhandled" or "Handled"
	Payload []byte // the error document the function returned
}

func (e *Error) Error() string {
	return fmt.Sprintf("function error (%s): %s", e.Kind, e.Payload)
}

// FunctionError is what trace reads: the header's value and the document.
func (e *Error) FunctionError() (kind string, payload []byte) { return e.Kind, e.Payload }
