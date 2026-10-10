package sqs

// RedriveAllowPolicy, DeduplicationScope and FifoThroughputLimit: three
// attributes that were stored as written and did nothing. Each is held to its
// values and to when it applies, and the two that change behaviour change it.
//
// doc: the SQS API reference, CreateQueue/SetQueueAttributes attribute
// descriptions (RedriveAllowPolicy, DeduplicationScope, FifoThroughputLimit),
// and the developer guide's "high throughput for FIFO queues" page.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestRedriveAllowPolicyIsValidatedAndEnforced(t *testing.T) {
	f := newRedriveFixture(t)
	f.create("dlq", nil)
	f.create("friend", nil)
	f.create("stranger", nil)

	set := func(dlq string, policy string) error {
		_, err := f.c.SetQueueAttributes(f.ctx, &awssqs.SetQueueAttributesInput{QueueUrl: aws.String(f.urls[dlq]),
			Attributes: map[string]string{"RedriveAllowPolicy": policy}})
		return err
	}
	for name, doc := range map[string]string{
		"not JSON":                      `nope`,
		"a permission that is none":     `{"redrivePermission":"sometimes"}`,
		"no permission":                 `{}`,
		"byQueue with no source queues": `{"redrivePermission":"byQueue"}`,
		"byQueue with an empty list":    `{"redrivePermission":"byQueue","sourceQueueArns":[]}`,
		"eleven source queues":          `{"redrivePermission":"byQueue","sourceQueueArns":["` + strings.Join(repeat(f.arns["friend"], 11), `","`) + `"]}`,
		"source queues with allowAll":   `{"redrivePermission":"allowAll","sourceQueueArns":["` + f.arns["friend"] + `"]}`,
	} {
		if err := set("dlq", doc); err == nil {
			t.Errorf("RedriveAllowPolicy accepted %s", name)
		}
	}

	pointAt := func(src, dlq string) error {
		_, err := f.c.SetQueueAttributes(f.ctx, &awssqs.SetQueueAttributesInput{QueueUrl: aws.String(f.urls[src]),
			Attributes: map[string]string{"RedrivePolicy": `{"deadLetterTargetArn":"` + f.arns[dlq] + `","maxReceiveCount":3}`}})
		return err
	}
	if err := set("dlq", `{"redrivePermission":"denyAll"}`); err != nil {
		t.Fatal(err)
	}
	if err := pointAt("friend", "dlq"); err == nil {
		t.Error("denyAll let a queue name this one as its dead-letter queue")
	}
	byQueue, _ := json.Marshal(map[string]any{"redrivePermission": "byQueue", "sourceQueueArns": []string{f.arns["friend"]}})
	if err := set("dlq", string(byQueue)); err != nil {
		t.Fatal(err)
	}
	if err := pointAt("stranger", "dlq"); err == nil {
		t.Error("byQueue let a queue that is not listed use this one as its dead-letter queue")
	}
	if err := pointAt("friend", "dlq"); err != nil {
		t.Errorf("byQueue refused the queue it lists: %v", err)
	}
	if err := set("dlq", `{"redrivePermission":"allowAll"}`); err != nil {
		t.Fatal(err)
	}
	if err := pointAt("stranger", "dlq"); err != nil {
		t.Errorf("allowAll refused a queue: %v", err)
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestDeduplicationScopeAndThroughputLimitAreHeldToTheirValues(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	fifo, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("f.fifo"),
		Attributes: map[string]string{"FifoQueue": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	std, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("std")})
	if err != nil {
		t.Fatal(err)
	}
	set := func(url *string, attrs map[string]string) error {
		_, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: url, Attributes: attrs})
		return err
	}

	for name, attrs := range map[string]map[string]string{
		"a scope that is neither":             {"DeduplicationScope": "global"},
		"a limit that is neither":             {"FifoThroughputLimit": "perThing"},
		"per-group throughput at queue scope": {"DeduplicationScope": "queue", "FifoThroughputLimit": "perMessageGroupId"},
	} {
		if err := set(fifo.QueueUrl, attrs); err == nil {
			t.Errorf("a FIFO queue accepted %s", name)
		}
	}
	for name, attrs := range map[string]map[string]string{
		"a deduplication scope": {"DeduplicationScope": "messageGroup"},
		"a throughput limit":    {"FifoThroughputLimit": "perQueue"},
	} {
		if err := set(std.QueueUrl, attrs); err == nil {
			t.Errorf("a standard queue accepted %s: both are FIFO-only", name)
		}
	}
	if err := set(fifo.QueueUrl, map[string]string{"DeduplicationScope": "messageGroup", "FifoThroughputLimit": "perMessageGroupId"}); err != nil {
		t.Errorf("the high-throughput pair was refused: %v", err)
	}
}

func TestMessageGroupScopeDeduplicatesWithinAGroup(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	mk := func(name, scope string) *string {
		attrs := map[string]string{"FifoQueue": "true"}
		if scope != "" {
			attrs["DeduplicationScope"] = scope
			attrs["FifoThroughputLimit"] = map[string]string{"messageGroup": "perMessageGroupId", "queue": "perQueue"}[scope]
		}
		q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name), Attributes: attrs})
		if err != nil {
			t.Fatal(err)
		}
		return q.QueueUrl
	}
	send := func(url *string, group string) string {
		out, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: url, MessageBody: aws.String("b"),
			MessageGroupId: aws.String(group), MessageDeduplicationId: aws.String("same")})
		if err != nil {
			t.Fatal(err)
		}
		return aws.ToString(out.MessageId)
	}

	// Queue scope (the default): the id is unique across groups, so the second
	// send is a duplicate of the first, whatever group it names.
	q := mk("q-scope.fifo", "")
	if a, b := send(q, "g1"), send(q, "g2"); a != b {
		t.Errorf("queue scope: a deduplication id in another group was not a duplicate (%s, %s)", a, b)
	}
	// Message group scope: the id is only unique within its group.
	g := mk("g-scope.fifo", "messageGroup")
	a, b, again := send(g, "g1"), send(g, "g2"), send(g, "g1")
	if a == b {
		t.Errorf("group scope: the same id in another group was treated as a duplicate")
	}
	if a != again {
		t.Errorf("group scope: the same id in the same group was not a duplicate")
	}
}
