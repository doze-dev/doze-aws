package peercall

import (
	"context"
	"errors"
	"strings"

	"github.com/doze-dev/doze-aws/peers"
)

// CloudWatch Logs, as Lambda calls it: a function's lines are shipped to the
// logs service through the same wire an SDK would use, so the two services
// can run in one process or two.

const logsTarget = "Logs_20140328."
const logsContentType = "application/x-amz-json-1.1"

// ErrNoLogs says the logs service is not in the directory — the stack was
// started without it — so a caller can fall back rather than retry.
var ErrNoLogs = errors.New("the logs service is not enabled")

// LogEvent is one line with its timestamp in epoch milliseconds. RequestID is
// a doze extension: the invocation the line belongs to, which lets the console
// show one request's lines without a filter pattern.
type LogEvent struct {
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

// LogsEnsure creates the group and the stream, treating "already exists" as
// success — it runs once per stream, before its first batch.
func LogsEnsure(ctx context.Context, dir peers.Directory, group, stream string) error {
	ep, ok := dir.Endpoint("logs")
	if !ok {
		return ErrNoLogs
	}
	err := postJSON(ctx, ep, logsTarget+"CreateLogGroup", logsContentType, map[string]any{"logGroupName": group})
	if err != nil && !strings.Contains(err.Error(), "ResourceAlreadyExistsException") {
		return err
	}
	err = postJSON(ctx, ep, logsTarget+"CreateLogStream", logsContentType, map[string]any{"logGroupName": group, "logStreamName": stream})
	if err != nil && !strings.Contains(err.Error(), "ResourceAlreadyExistsException") {
		return err
	}
	return nil
}

// LogsPut appends a batch to a stream.
func LogsPut(ctx context.Context, dir peers.Directory, group, stream string, events []LogEvent) error {
	ep, ok := dir.Endpoint("logs")
	if !ok {
		return ErrNoLogs
	}
	return postJSON(ctx, ep, logsTarget+"PutLogEvents", logsContentType, map[string]any{
		"logGroupName": group, "logStreamName": stream, "logEvents": events,
	})
}
