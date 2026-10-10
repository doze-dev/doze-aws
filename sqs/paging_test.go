package sqs

// ListQueues pages. It ignored MaxResults and never returned a NextToken, so
// an SDK paginator was satisfied by one page of everything and a caller with
// more queues than it asked for got them all (SQS-6). Held on both wire
// protocols through the shared check, which walks limits of one and two.

import (
	"context"
	"sort"
	"testing"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go/aws"
	sqsv1 "github.com/aws/aws-sdk-go/service/sqs"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

var pagedQueues = []string{"alpha", "bravo", "charlie", "delta", "echo"}

func TestListQueuesPagesOverJSON(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	for _, n := range pagedQueues {
		if _, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: awsv2.String(n)}); err != nil {
			t.Fatal(err)
		}
	}
	dozetest.AssertPaginates(t, func(limit int, token string) ([]string, string, error) {
		in := &awssqs.ListQueuesInput{MaxResults: awsv2.Int32(int32(limit))}
		if token != "" {
			in.NextToken = awsv2.String(token)
		}
		out, err := c.ListQueues(ctx, in)
		if err != nil {
			return nil, "", err
		}
		ids := make([]string, len(out.QueueUrls))
		for i, u := range out.QueueUrls {
			ids[i] = u[len(u)-len(pagedQueues[0]):]
			for _, n := range pagedQueues {
				if len(u) >= len(n) && u[len(u)-len(n):] == n {
					ids[i] = n
				}
			}
		}
		return ids, awsv2.ToString(out.NextToken), nil
	}, pagedQueues, dozetest.PagingOptions{Ordered: true, BadToken: "not-a-token"})
}

func TestListQueuesPagesOverQuery(t *testing.T) {
	c := sdkV1Client(t)
	for _, n := range pagedQueues {
		if _, err := c.CreateQueue(&sqsv1.CreateQueueInput{QueueName: aws.String(n)}); err != nil {
			t.Fatal(err)
		}
	}
	dozetest.AssertPaginates(t, func(limit int, token string) ([]string, string, error) {
		in := &sqsv1.ListQueuesInput{MaxResults: aws.Int64(int64(limit))}
		if token != "" {
			in.NextToken = aws.String(token)
		}
		out, err := c.ListQueues(in)
		if err != nil {
			return nil, "", err
		}
		var ids []string
		for _, u := range out.QueueUrls {
			s := aws.StringValue(u)
			for _, n := range pagedQueues {
				if len(s) >= len(n) && s[len(s)-len(n):] == n {
					ids = append(ids, n)
				}
			}
		}
		sort.SliceStable(ids, func(i, j int) bool { return false })
		return ids, aws.StringValue(out.NextToken), nil
	}, pagedQueues, dozetest.PagingOptions{Ordered: true, BadToken: "not-a-token"})
}

// A prefix and paging together: the token continues the filtered listing.
func TestListQueuesPagesWithinAPrefix(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	for _, n := range []string{"app-a", "app-b", "app-c", "other-a", "other-b"} {
		if _, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: awsv2.String(n)}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := c.ListQueues(ctx, &awssqs.ListQueuesInput{QueueNamePrefix: awsv2.String("app-"), MaxResults: awsv2.Int32(2)})
	if err != nil || len(first.QueueUrls) != 2 || first.NextToken == nil {
		t.Fatalf("first page of app-: %v %v", first, err)
	}
	second, err := c.ListQueues(ctx, &awssqs.ListQueuesInput{QueueNamePrefix: awsv2.String("app-"), MaxResults: awsv2.Int32(2), NextToken: first.NextToken})
	if err != nil || len(second.QueueUrls) != 1 || second.NextToken != nil {
		t.Fatalf("second page of app-: %v %v", second, err)
	}
	// MaxResults is 1 to 1000.
	if _, err := c.ListQueues(ctx, &awssqs.ListQueuesInput{MaxResults: awsv2.Int32(1001)}); err == nil {
		t.Error("MaxResults 1001 was accepted")
	}
	if _, err := c.ListQueues(ctx, &awssqs.ListQueuesInput{MaxResults: awsv2.Int32(0)}); err == nil {
		t.Error("MaxResults 0 was accepted")
	}
}

func TestListDeadLetterSourceQueuesPages(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	dlq, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: awsv2.String("sink")})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range pagedQueues {
		if _, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: awsv2.String(n), Attributes: map[string]string{
			"RedrivePolicy": redrive("sink", `"3"`)}}); err != nil {
			t.Fatal(err)
		}
	}
	dozetest.AssertPaginates(t, func(limit int, token string) ([]string, string, error) {
		in := &awssqs.ListDeadLetterSourceQueuesInput{QueueUrl: dlq.QueueUrl, MaxResults: awsv2.Int32(int32(limit))}
		if token != "" {
			in.NextToken = awsv2.String(token)
		}
		out, err := c.ListDeadLetterSourceQueues(ctx, in)
		if err != nil {
			return nil, "", err
		}
		var ids []string
		for _, u := range out.QueueUrls {
			for _, n := range pagedQueues {
				if len(u) >= len(n) && u[len(u)-len(n):] == n {
					ids = append(ids, n)
				}
			}
		}
		return ids, awsv2.ToString(out.NextToken), nil
	}, pagedQueues, dozetest.PagingOptions{Ordered: true, BadToken: "not-a-token"})
}
