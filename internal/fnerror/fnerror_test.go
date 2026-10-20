package fnerror

import (
	"errors"
	"fmt"
	"testing"
)

// The error says what the header and the function said, and hands both to
// whoever asks for them through a wrapping (trace.Step does).
func TestTheErrorCarriesTheHeaderAndTheFunctionsDocument(t *testing.T) {
	e := &Error{Kind: "Unhandled", Payload: []byte(`{"errorType":"KeyError"}`)}
	if got := e.Error(); got != `function error (Unhandled): {"errorType":"KeyError"}` {
		t.Errorf("Error() = %q", got)
	}
	var asked interface {
		FunctionError() (string, []byte)
	}
	if !errors.As(fmt.Errorf("invoking: %w", e), &asked) {
		t.Fatal("a wrapped function error is not found")
	}
	if kind, doc := asked.FunctionError(); kind != "Unhandled" || string(doc) != `{"errorType":"KeyError"}` {
		t.Errorf("FunctionError() = %q, %q", kind, doc)
	}
}
