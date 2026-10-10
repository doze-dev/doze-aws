package sqs

// A queue's access policy is validated when it is stored. It used to be stored
// as written: the guard parsed it on every request, failed, and carried on with
// no policy, so a mistyped Deny was silently inert and "not json" read back as
// applied (SQS-8). doc: the IAM JSON policy elements reference, and the SQS
// developer guide's access policy language page.

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	awsv1 "github.com/aws/aws-sdk-go/aws"
	sqsv1 "github.com/aws/aws-sdk-go/service/sqs"
)

const goodPolicy = `{"Version":"2012-10-17","Statement":[{"Sid":"s","Effect":"Allow","Principal":{"AWS":"*"},"Action":"sqs:SendMessage","Resource":"*"}]}`

var badPolicies = map[string]string{
	"not JSON":                   `not json`,
	"a JSON array":               `[]`,
	"no Statement":               `{"Version":"2012-10-17"}`,
	"a statement with no Effect": `{"Version":"2012-10-17","Statement":[{"Principal":"*","Action":"sqs:*","Resource":"*"}]}`,
	"an Effect that is neither":  `{"Version":"2012-10-17","Statement":[{"Effect":"Maybe","Principal":"*","Action":"sqs:*","Resource":"*"}]}`,
	"no Principal":               `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"}]}`,
	"no Action":                  `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Resource":"*"}]}`,
	"no Resource":                `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"sqs:*"}]}`,
	"an unknown Version":         `{"Version":"2020-01-01","Statement":[{"Effect":"Allow","Principal":"*","Action":"sqs:*","Resource":"*"}]}`,
}

func TestAnInvalidPolicyIsRefusedWhenStoredOverJSON(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("guarded")})
	if err != nil {
		t.Fatal(err)
	}
	for name, doc := range badPolicies {
		_, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: q.QueueUrl,
			Attributes: map[string]string{"Policy": doc}})
		if err == nil {
			t.Errorf("SetQueueAttributes accepted %s", name)
			continue
		}
		if !strings.Contains(err.Error(), "InvalidAttributeValue") {
			t.Errorf("%s: refused with %v, want InvalidAttributeValue", name, err)
		}
		if _, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("born-bad"),
			Attributes: map[string]string{"Policy": doc}}); err == nil {
			t.Errorf("CreateQueue accepted %s", name)
		}
	}
	if _, err := c.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("born-bad")}); err == nil {
		t.Error("a CreateQueue refused for its policy still created the queue")
	}
	got, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNamePolicy}})
	if err != nil {
		t.Fatal(err)
	}
	if p := got.Attributes["Policy"]; p != "" {
		t.Errorf("a refused policy was stored: %q", p)
	}

	// A good one is stored, read back, and cleared by an empty value.
	if _, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: q.QueueUrl,
		Attributes: map[string]string{"Policy": goodPolicy}}); err != nil {
		t.Fatalf("a valid policy was refused: %v", err)
	}
	got, _ = c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNamePolicy}})
	if !strings.Contains(got.Attributes["Policy"], "sqs:SendMessage") {
		t.Errorf("the policy did not read back: %q", got.Attributes["Policy"])
	}
	if _, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: q.QueueUrl,
		Attributes: map[string]string{"Policy": ""}}); err != nil {
		t.Errorf("clearing the policy was refused: %v", err)
	}
}

func TestAnInvalidPolicyIsRefusedWhenStoredOverQuery(t *testing.T) {
	c := sdkV1Client(t)
	q, err := c.CreateQueue(&sqsv1.CreateQueueInput{QueueName: awsv1.String("guarded")})
	if err != nil {
		t.Fatal(err)
	}
	for name, doc := range badPolicies {
		_, err := c.SetQueueAttributes(&sqsv1.SetQueueAttributesInput{QueueUrl: q.QueueUrl,
			Attributes: map[string]*string{"Policy": awsv1.String(doc)}})
		if err == nil {
			t.Errorf("SetQueueAttributes accepted %s", name)
		}
	}
	if _, err := c.SetQueueAttributes(&sqsv1.SetQueueAttributesInput{QueueUrl: q.QueueUrl,
		Attributes: map[string]*string{"Policy": awsv1.String(goodPolicy)}}); err != nil {
		t.Errorf("a valid policy was refused: %v", err)
	}
}

// AddPermission writes a statement into the policy, and what it writes is a
// policy this check accepts.
func TestAddPermissionWritesAPolicyThatValidates(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	q, _ := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("granted")})
	if _, err := c.AddPermission(ctx, &awssqs.AddPermissionInput{QueueUrl: q.QueueUrl, Label: aws.String("share"),
		AWSAccountIds: []string{"123456789012"}, Actions: []string{"SendMessage"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: q.QueueUrl,
		Attributes: map[string]string{"VisibilityTimeout": "40"}}); err != nil {
		t.Errorf("a queue holding an AddPermission statement could not be updated: %v", err)
	}
}
