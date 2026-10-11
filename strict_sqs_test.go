package dozeaws_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// SQSStrict reaches the service from the stack's configuration, and is off
// unless asked for.
func TestSQSStrictReachesTheService(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	ctx := context.Background()
	for _, strict := range []bool{false, true} {
		cfg, url := strictStack(t, strict)
		c := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) })
		q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("again")})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.DeleteQueue(ctx, &awssqs.DeleteQueueInput{QueueUrl: q.QueueUrl}); err != nil {
			t.Fatal(err)
		}
		_, err = c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("again")})
		var recently *sqstypes.QueueDeletedRecently
		switch {
		case strict && !errors.As(err, &recently):
			t.Errorf("strict: re-creating at once answered %v, want QueueDeletedRecently", err)
		case !strict && err != nil:
			t.Errorf("not strict: re-creating at once answered %v", err)
		}
	}
}

func strictStack(t *testing.T, strict bool) (aws.Config, string) {
	t.Helper()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t),
		Services: []string{"sqs"}, SQSStrict: strict})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	return aws.Config{Region: awsident.Region, Credentials: creds}, ts.URL
}
