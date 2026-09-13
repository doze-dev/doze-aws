// Package awshttp holds the HTTP-level plumbing shared by every doze-aws
// service: the API error type that stores return for API-visible failures,
// request-id generation, and the date formats AWS wire protocols use.
package awshttp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// APIError is a failure that maps onto an AWS API error: an error code the SDK
// matches on, an HTTP status, and a human message. Service stores return
// *APIError for anything a client should see; any other error type is treated
// as an internal fault (HTTP 500).
type APIError struct {
	Code    string // AWS error code, e.g. "ValidationError"
	Status  int    // HTTP status to respond with
	Message string
	// SenderFault distinguishes 4xx Sender errors from Receiver faults in the
	// Query/XML error envelope.
	SenderFault bool
	// Item optionally carries a wire-format item inside the error body —
	// DynamoDB's ReturnValuesOnConditionCheckFailure.
	Item json.RawMessage
	// Extra merges additional members into the JSON error body (e.g.
	// DynamoDB's CancellationReasons).
	Extra map[string]json.RawMessage
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// Errf builds a Sender-fault APIError with a formatted message.
func Errf(status int, code, format string, args ...any) *APIError {
	return &APIError{Code: code, Status: status, Message: fmt.Sprintf(format, args...), SenderFault: true}
}

// OnInternalFault receives every error AsAPIError turns into an opaque 500.
//
// It exists because those errors used to be DISCARDED. An unexpected failure
// anywhere in 400-odd call sites produced "InternalFailure: internal error" on
// the wire and absolutely nothing in the log — no message, no operation, no
// clue. A user hitting one had no way to learn anything and nowhere to look,
// and neither did we.
//
// A package-level hook rather than a parameter: threading a logger through
// every one of those call sites would be a large mechanical change for a
// diagnostic, and coercion is exactly the choke point where the error is still
// in hand. Unset means discard, which keeps awshttp usable as a library and
// keeps tests quiet.
//
// ATOMIC, and that is not decoration. Every Stack sets it at construction, and
// stacks are built concurrently — one per region, and the root package's
// TestStackChurn builds them in parallel on purpose. The first version of this
// was a plain `var OnInternalFault func(error)`, and `go test -race` reported
// it immediately: concurrent NewStack calls writing the same word.
//
// The wire response is deliberately unchanged — internal detail still never
// leaves the process.
var onInternalFault atomic.Pointer[func(error)]

// SetInternalFaultHandler installs the handler described on onInternalFault.
// Passing nil clears it. Safe to call from several goroutines; last writer
// wins, and every writer in practice installs the same behaviour.
func SetInternalFaultHandler(fn func(error)) {
	if fn == nil {
		onInternalFault.Store(nil)
		return
	}
	onInternalFault.Store(&fn)
}

// AsAPIError coerces err into an *APIError, wrapping unknown error types as an
// opaque InternalFailure so internal details never leak onto the wire.
func AsAPIError(err error) *APIError {
	if ae, ok := err.(*APIError); ok {
		return ae
	}
	if fn := onInternalFault.Load(); fn != nil && err != nil {
		(*fn)(err)
	}
	return &APIError{Code: "InternalFailure", Status: 500, Message: "internal error"}
}

// AsAPIErrorOrNil is AsAPIError that passes nil through — for the common
// `return nil, awshttp.AsAPIErrorOrNil(err)` handler tail.
func AsAPIErrorOrNil(err error) *APIError {
	if err == nil {
		return nil
	}
	return AsAPIError(err)
}

// RequestID returns a fresh request id in UUID shape. AWS request ids are
// opaque; UUID shape keeps SDK log lines familiar.
func RequestID() string {
	var b [16]byte
	rand.Read(b[:]) // never fails per crypto/rand contract
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}

// The request id has to be ONE value per request, reachable from three places
// that do not share a parameter: the protocol writer (which has only a
// ResponseWriter), the log, and the console's Traffic row.
//
// It used to be minted at RESPONSE-WRITE time, separately at each of ten call
// sites. So the id in `x-amzn-RequestId` was a value that had never existed
// anywhere else — nothing logged it, nothing recorded it, and "my call failed
// with request id 7f3a…" led nowhere.
//
// S3 had a second problem on top: its success path set the header and its
// error path set only the <RequestId> ELEMENT, no header at all. The two never
// contradicted each other because they never appeared together, which is its
// own kind of wrong — a client reading x-amz-request-id off a failure got
// nothing.
//
// Minting happens once, in the gateway, which is the one place every client
// request passes through. It lands in two places because the two consumers
// cannot reach each other: the request CONTEXT, for anything holding an
// *http.Request, and a ResponseWriter wrapper, for the protocol writers, whose
// signatures take only a ResponseWriter. Threading the request through those
// would mean touching 85 call sites to deliver a diagnostic — the same trade
// this package already refused for the fault hook above.

type requestIDKey struct{}

// idWriter carries a request's id on its ResponseWriter.
type idWriter struct {
	http.ResponseWriter
	id string
}

// RequestID reports the id, satisfying the interface ResponseID looks for.
func (w *idWriter) RequestID() string { return w.id }

// Unwrap lets http.ResponseController reach the real writer, so wrapping does
// not silently disable flushing or hijacking for anything underneath.
func (w *idWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// WithRequestID mints an id for r and returns it with the request and a
// ResponseWriter that both carry it. Call once, at the entry point.
func WithRequestID(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, string) {
	id := RequestID()
	return &idWriter{ResponseWriter: w, id: id},
		r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)),
		id
}

// RequestIDFrom returns the id carried by ctx, or "" when there is none.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// ResponseID returns the request id for this response.
//
// Minting on a miss rather than returning "" is deliberate: a service package
// used directly — which dozeaws.go advertises — or reached over the in-process
// peer path never passed through the gateway, and a response with no id at all
// would be a worse wire than one whose id is merely unlogged.
func ResponseID(w http.ResponseWriter) string {
	for {
		if v, ok := w.(interface{ RequestID() string }); ok {
			return v.RequestID()
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return RequestID()
		}
		w = u.Unwrap()
	}
}

// onFaultResponse receives the request id of every 5xx a protocol writer
// sends, so the id a client is holding appears in the log at least once.
//
// Deliberately SEPARATE from onInternalFault, and deliberately carrying no
// error detail. The two lines divide the job: the fault hook fires at
// coercion, where the cause is in hand but the request is not; this one fires
// at write, where the request id is in hand but the cause is long gone.
// Between them a 500 produces the cause once and the id once, adjacent.
//
// Joining them into a single line would mean giving AsAPIError the request,
// which is 506 call sites for a diagnostic — the trade this package already
// refused once, above. The honest limitation is that under concurrent load the
// two lines can interleave with another request's pair; the id is still the
// thing you can search for, which is what was missing entirely before.
var onFaultResponse atomic.Pointer[func(id string, e *APIError)]

// SetFaultResponseHandler installs the process-wide fallback described on
// onFaultResponse. Passing nil clears it.
//
// Prefer WithFaultRecorder: this one is global, so when two stacks run at once
// — which the test suite does on purpose — the last one constructed owns the
// hook and the other stack's faults are reported to it.
func SetFaultResponseHandler(fn func(id string, e *APIError)) {
	if fn == nil {
		onFaultResponse.Store(nil)
		return
	}
	onFaultResponse.Store(&fn)
}

// faultWriter carries a per-response fault recorder, so a fault can be reported
// to the stack that produced it rather than to whichever stack was built last.
type faultWriter struct {
	http.ResponseWriter
	rec func(id string, e *APIError)
}

func (w *faultWriter) recordFault(id string, e *APIError) { w.rec(id, e) }
func (w *faultWriter) Unwrap() http.ResponseWriter        { return w.ResponseWriter }

// WithFaultRecorder returns w wrapped so that NoteFault reports to rec instead
// of the process-wide handler. Install it at the stack's edge, outside the
// gateway: NoteFault walks the same Unwrap chain ResponseID does, so the order
// of the two wrappers does not matter.
func WithFaultRecorder(w http.ResponseWriter, rec func(id string, e *APIError)) http.ResponseWriter {
	if rec == nil {
		return w
	}
	return &faultWriter{ResponseWriter: w, rec: rec}
}

// NoteFault reports that this response is a server fault. Protocol writers call
// it; anything below 500 is ignored, so callers need no condition of their own.
//
// A recorder found on the writer wins over the global handler, and there is no
// fallthrough: a stack that installed one has said where its faults go.
func NoteFault(w http.ResponseWriter, id string, e *APIError) {
	if e == nil || e.Status < 500 {
		return
	}
	for cur := w; cur != nil; {
		if fw, ok := cur.(interface {
			recordFault(string, *APIError)
		}); ok {
			fw.recordFault(id, e)
			return
		}
		u, ok := cur.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		cur = u.Unwrap()
	}
	if fn := onFaultResponse.Load(); fn != nil {
		(*fn)(id, e)
	}
}

// ISO8601 renders t the way AWS XML/JSON APIs expect timestamps
// (UTC, second precision, trailing Z).
func ISO8601(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}
