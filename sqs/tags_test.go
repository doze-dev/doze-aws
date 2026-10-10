package sqs

// A queue's tags are held to the limits AWS states: a key of 1 to 128
// characters and a value of 0 to 256, no key beginning "aws:" (reserved), and
// at most 50 tags on a queue. TagQueue accepted all of it (SQS-9).
//
// doc: the SQS developer guide, "Configuring cost allocation tags for Amazon
// SQS" (the tag restrictions list).

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestTagLimits(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("tagged")})
	if err != nil {
		t.Fatal(err)
	}
	tag := func(tags map[string]string) error {
		_, err := c.TagQueue(ctx, &awssqs.TagQueueInput{QueueUrl: q.QueueUrl, Tags: tags})
		return err
	}
	count := func() int {
		out, err := c.ListQueueTags(ctx, &awssqs.ListQueueTagsInput{QueueUrl: q.QueueUrl})
		if err != nil {
			t.Fatal(err)
		}
		return len(out.Tags)
	}

	for name, tags := range map[string]map[string]string{
		"a key of 129 characters":           {strings.Repeat("k", 129): "v"},
		"an empty key":                      {"": "v"},
		"a value of 257 characters":         {"k": strings.Repeat("v", 257)},
		"a key with the reserved prefix":    {"aws:team": "v"},
		"a key with the prefix in capitals": {"AWS:team": "v"},
	} {
		if err := tag(tags); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if n := count(); n != 0 {
		t.Errorf("a refused TagQueue left %d tags on the queue", n)
	}

	// The limits themselves are fine, and an empty value is a value.
	if err := tag(map[string]string{strings.Repeat("k", 128): strings.Repeat("v", 256), "empty": ""}); err != nil {
		t.Errorf("a key of 128 and a value of 256 were refused: %v", err)
	}

	// Fifty tags, and not a fifty-first.
	all := map[string]string{}
	for i := 0; i < 48; i++ {
		all["t"+strconv.Itoa(i)] = "v"
	}
	if err := tag(all); err != nil {
		t.Fatalf("filling to fifty: %v", err)
	}
	if count() != 50 {
		t.Fatalf("expected 50 tags, have %d", count())
	}
	if err := tag(map[string]string{"one-more": "v"}); err == nil {
		t.Error("a fifty-first tag was accepted")
	}
	// Overwriting a tag that is there is not a new one.
	if err := tag(map[string]string{"t1": "changed"}); err != nil {
		t.Errorf("overwriting a tag at the limit was refused: %v", err)
	}

	// CreateQueue takes tags, and the same rules.
	if _, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("badtags"),
		Tags: map[string]string{"aws:x": "v"}}); err == nil {
		t.Error("CreateQueue accepted a reserved tag key")
	}
	if _, err := c.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("badtags")}); err == nil {
		t.Error("a CreateQueue refused for its tags still created the queue")
	}
}
