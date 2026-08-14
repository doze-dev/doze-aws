package sns_test

// Rejection parity: the Subscribe protocols real SNS refuses.
//
// This closes the gap docs/api-support/sns.md carried as "❌ `Subscribe`
// accepts any protocol". SNS's own service model types Protocol as a plain
// string with no enum trait — its constraint lives only in the API reference —
// so the accepted set is hand-derived, and asserted here by the error code an
// SDK branches on.
//
// The distinction the cases below encode: membership means "AWS accepts it",
// not "doze-aws delivers to it". Refusing `email` because nothing local sends
// mail would break a subscription that works in AWS, which is the opposite of
// the failure this file exists to prevent.

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/smithy-go"
)

func TestSubscribeRejectsProtocolsAWSRejects(t *testing.T) {
	ctx := context.Background()
	snsC, _ := startStack(t)
	top, err := snsC.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("protos")})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		proto string
		ok    bool
	}{
		// Delivered locally.
		{"sqs", true},
		{"https", true},
		{"http", true},
		{"lambda", true},
		// Valid in AWS but not delivered locally — still must be accepted,
		// or a subscription that works in AWS fails here.
		{"email", true},
		{"email-json", true},
		{"sms", true},
		{"application", true},
		{"firehose", true},
		// Not protocols at all.
		{"ftp", false},
		{"HTTP", false}, // AWS is case-sensitive here
		{"webhook", false},
		{"", false}, // caught earlier, as a missing required parameter
	}
	for _, tc := range cases {
		name := tc.proto
		if name == "" {
			name = "(empty)"
		}
		t.Run(name, func(t *testing.T) {
			_, err := snsC.Subscribe(ctx, &awssns.SubscribeInput{
				TopicArn:              top.TopicArn,
				Protocol:              aws.String(tc.proto),
				Endpoint:              aws.String("arn:aws:sqs:us-east-1:000000000000:q"),
				ReturnSubscriptionArn: true,
			})
			if tc.ok {
				if err != nil {
					t.Fatalf("Subscribe(protocol=%q) = %v, want accepted", tc.proto, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Subscribe(protocol=%q) was accepted; AWS refuses it", tc.proto)
			}
			var ae smithy.APIError
			if !errors.As(err, &ae) || ae.ErrorCode() != "InvalidParameter" {
				t.Errorf("code = %v, want InvalidParameter", err)
			}
		})
	}
}
