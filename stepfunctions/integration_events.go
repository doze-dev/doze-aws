package stepfunctions

import (
	"context"
	"encoding/json"

	"github.com/doze-dev/doze-aws/internal/asl"
)

// The optimized EventBridge integration, arn:aws:states:::events:putEvents.
// Parameters are the PutEvents API's own — Entries with Detail, DetailType,
// Source, EventBusName — and Detail may be given as an object, which is
// what a machine naturally writes ("Detail": {"orderId.$": "$.id"}); AWS
// serialises it to the JSON string the API wants, and so does this. The
// result is the API response: {"Entries":[{"EventId":...}],"FailedEntryCount":0}.
//
// .waitForTaskToken is valid here on AWS (the token travels in the Detail
// and a consumer calls SendTaskSuccess); the token is minted generically, so
// nothing more is needed than sending.
//
// Errors are "EventBridge.<Code>", the optimized integration's prefix.

func (s *Server) putEvents(ctx context.Context, input []byte) asl.TaskResult {
	var in struct {
		Entries []map[string]any `json:"Entries"`
	}
	if err := json.Unmarshal(input, &in); err != nil || len(in.Entries) == 0 {
		return failResult(asl.ErrTaskFailed, "events:putEvents needs an Entries parameter")
	}
	for _, e := range in.Entries {
		if d, ok := e["Detail"]; ok {
			if _, isString := d.(string); !isString {
				text, _ := json.Marshal(d)
				e["Detail"] = string(text)
			}
		}
	}
	body, _ := json.Marshal(map[string]any{"Entries": in.Entries})
	bus := "default"
	if b, ok := in.Entries[0]["EventBusName"].(string); ok && b != "" {
		bus = b
	}
	return s.callJSON(ctx, sdkServices["eventbridge"], "putEvents", body, "EventBridge", false, bus)
}
