package provision_test

// A queue and its dead-letter queue, declared together, apply in either name
// order. `orders` and `orders-dlq` is the usual naming and sorts the queue
// first, so applying in name order created `orders` with a redrive policy
// naming a queue that did not exist yet, and the whole stack rolled back.
// (SQS-1.)

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/provision"
)

func TestAQueueAppliesAfterTheDeadLetterQueueItNames(t *testing.T) {
	for _, c := range []struct {
		name, queue, dlq string
	}{
		{"the queue sorts first", "orders", "orders-dlq"},
		{"the dead-letter queue sorts first", "zeta", "alpha-dlq"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h, cfg, url := liveStack(t)
			sqsC := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) })

			spec := &provision.Stack{Queues: map[string]provision.Queue{
				c.queue: {DLQ: c.dlq, MaxReceives: 5},
				c.dlq:   {},
			}}
			if _, err := provision.Apply(ctx, h, spec, awsident.Default()); err != nil {
				t.Fatalf("a queue and its dead-letter queue in one spec: %v", err)
			}
			q, err := sqsC.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String(c.queue)})
			if err != nil {
				t.Fatal(err)
			}
			got, err := sqsC.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
				QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameRedrivePolicy}})
			if err != nil {
				t.Fatal(err)
			}
			var pol struct {
				Target string `json:"deadLetterTargetArn"`
			}
			if err := json.Unmarshal([]byte(got.Attributes["RedrivePolicy"]), &pol); err != nil || pol.Target != awsident.Default().ARN("sqs", c.dlq) {
				t.Errorf("redrive policy = %q (%v), want a target of %s", got.Attributes["RedrivePolicy"], err, c.dlq)
			}
		})
	}
}

// A chain applies from the end: a points at b, b at c.
func TestAChainOfDeadLetterQueuesAppliesFromTheEnd(t *testing.T) {
	ctx := context.Background()
	h, _, _ := liveStack(t)
	spec := &provision.Stack{Queues: map[string]provision.Queue{
		"a": {DLQ: "b"}, "b": {DLQ: "c"}, "c": {},
	}}
	if _, err := provision.Apply(ctx, h, spec, awsident.Default()); err != nil {
		t.Fatalf("a chain of dead-letter queues: %v", err)
	}
}
