// Package awshttp holds the HTTP-level plumbing shared by every doze-aws
// service: the API error type that stores return for API-visible failures,
// request-id generation, and the date formats AWS wire protocols use.
package awshttp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// AsAPIError coerces err into an *APIError, wrapping unknown error types as an
// opaque InternalFailure so internal details never leak onto the wire.
func AsAPIError(err error) *APIError {
	if ae, ok := err.(*APIError); ok {
		return ae
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

// ISO8601 renders t the way AWS XML/JSON APIs expect timestamps
// (UTC, second precision, trailing Z).
func ISO8601(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}
