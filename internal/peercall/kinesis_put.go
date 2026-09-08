package peercall

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/doze-dev/doze-aws/internal/trace"
	"github.com/doze-dev/doze-aws/peers"
)

// KinesisPutRecord writes one record to a local Kinesis stream. data is the
// raw record; the wire carries it base64-encoded, as the API does.
func KinesisPutRecord(ctx context.Context, dir peers.Directory, stream, partitionKey string, data []byte) error {
	return trace.Step(ctx, trace.Event{Service: "kinesis", Action: "PutRecord", Resource: stream},
		func(ctx context.Context) error {
			ep, ok := dir.Endpoint("kinesis")
			if !ok {
				return fmt.Errorf("no kinesis peer wired")
			}
			return postJSON(ctx, ep, "Kinesis_20131202.PutRecord", "application/x-amz-json-1.1", map[string]any{
				"StreamName": stream, "PartitionKey": partitionKey, "Data": base64.StdEncoding.EncodeToString(data),
			})
		})
}
