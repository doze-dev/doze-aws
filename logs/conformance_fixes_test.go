package logs

// Refusals the boto3 conformance suite found missing (conformance/tests/test_logs.py).
// None is in the service model, so the model-derived audit could not see them.

import (
	"net/http"
	"strings"
	"testing"
)

// A batch is in time order or it is refused whole, and nothing is written.
func TestPutLogEventsRefusesABatchOutOfOrder(t *testing.T) {
	ts := logsServer(t)
	call(t, ts, "CreateLogGroup", map[string]any{"logGroupName": "/g"})
	call(t, ts, "CreateLogStream", map[string]any{"logGroupName": "/g", "logStreamName": "s"})
	put := func(events ...map[string]any) (int, string) {
		return call(t, ts, "PutLogEvents", map[string]any{
			"logGroupName": "/g", "logStreamName": "s", "logEvents": events})
	}
	at := func(ts int64, msg string) map[string]any { return map[string]any{"timestamp": ts, "message": msg} }

	code, body := put(at(2000, "b"), at(1000, "a"))
	if code != http.StatusBadRequest || !strings.Contains(body, "must be in chronological order") {
		t.Fatalf("out of order = %d: %s", code, body)
	}
	_, body = call(t, ts, "GetLogEvents", map[string]any{"logGroupName": "/g", "logStreamName": "s"})
	if strings.Contains(body, `"message"`) {
		t.Fatalf("the refused batch wrote something: %s", body)
	}
	// In order, and with equal timestamps, is fine.
	if code, body := put(at(1000, "a"), at(1000, "b"), at(2000, "c")); code != http.StatusOK {
		t.Fatalf("in order = %d: %s", code, body)
	}
}

// CreateLogStream comes first. A put used to create the stream it was
// missing, which let a writer that forgot the call work here and lose every
// line on AWS.
func TestPutLogEventsNeedsItsStream(t *testing.T) {
	ts := logsServer(t)
	call(t, ts, "CreateLogGroup", map[string]any{"logGroupName": "/g"})
	put := func() (int, string) {
		return call(t, ts, "PutLogEvents", map[string]any{"logGroupName": "/g", "logStreamName": "s",
			"logEvents": []map[string]any{{"timestamp": 1000, "message": "x"}}})
	}
	code, body := put()
	if code != http.StatusBadRequest || !strings.Contains(body, "ResourceNotFoundException") ||
		!strings.Contains(body, "The specified log stream does not exist.") {
		t.Fatalf("a put to a stream nobody created = %d: %s", code, body)
	}
	_, body = call(t, ts, "DescribeLogStreams", map[string]any{"logGroupName": "/g"})
	if strings.Contains(body, `"logStreamName":"s"`) {
		t.Fatalf("the refused put created the stream anyway: %s", body)
	}
	call(t, ts, "CreateLogStream", map[string]any{"logGroupName": "/g", "logStreamName": "s"})
	if code, body := put(); code != http.StatusOK {
		t.Fatalf("a put once the stream exists = %d: %s", code, body)
	}
}
