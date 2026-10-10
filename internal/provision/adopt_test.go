package provision_test

// What provision.Apply does to a resource that is already there, held so that
// no change made for the sake of stacks can quietly change `doze-aws apply`.
//
// internal/provision serves two callers with opposite ideas of ownership. A
// stack file converges: a queue that exists under the name is adopted and
// brought in line, a value that is live is left alone unless the file says
// force, and nothing is ever deleted. CloudFormation owns what it creates and
// must be able to overwrite a value on update. The difference is carried by
// the per-resource Force field and by refuseTaken in cloudformation/adopt.go,
// not by a mode on Apply, which is why none was added: the next fix that
// touches this file (the dead-letter ordering, a parameter updated by a
// stack) has to pass through the behaviour pinned here.

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/provision"
)

func TestApplyAdoptsWhatIsThereAndDeletesNothing(t *testing.T) {
	ctx := context.Background()
	h, cfg, url := liveStack(t)
	sqsC := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) })
	ssmC := awsssm.NewFromConfig(cfg, func(o *awsssm.Options) { o.BaseEndpoint = aws.String(url) })
	id := awsident.Default()

	// Something the file does not mention: an adopting apply must leave it.
	stranger, err := sqsC.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("stranger")})
	if err != nil {
		t.Fatal(err)
	}

	first := &provision.Stack{
		Queues:     map[string]provision.Queue{"orders": {Visibility: 30}},
		Parameters: map[string]provision.Parameter{"/app/rate": {Value: "5"}},
	}
	if _, err := provision.Apply(ctx, h, first, id); err != nil {
		t.Fatal(err)
	}
	q, err := sqsC.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}

	// The file now says something different about both.
	second := &provision.Stack{
		Queues:     map[string]provision.Queue{"orders": {Visibility: 60}},
		Parameters: map[string]provision.Parameter{"/app/rate": {Value: "9"}},
	}
	rep, err := provision.Apply(ctx, h, second, id)
	if err != nil {
		t.Fatal(err)
	}

	// The queue is the same queue, converged in place.
	again, err := sqsC.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("orders")})
	if err != nil || aws.ToString(again.QueueUrl) != aws.ToString(q.QueueUrl) {
		t.Fatalf("the queue was replaced or lost: %v", err)
	}
	attrs, err := sqsC.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameVisibilityTimeout}})
	if err != nil || attrs.Attributes["VisibilityTimeout"] != "60" {
		t.Errorf("an existing queue was not converged: %v %v", attrs.Attributes, err)
	}

	// The live parameter value stands without force, and says so.
	got, err := ssmC.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/rate")})
	if err != nil || aws.ToString(got.Parameter.Value) != "5" {
		t.Errorf("without force the live value must stand, got %v (%v)", got, err)
	}
	var skipped bool
	for _, a := range rep.Actions {
		if a.Resource == "parameter//app/rate" && a.Op == "skipped" {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("the untouched parameter must be reported skipped: %+v", rep.Actions)
	}

	// With force the file wins.
	second.Parameters["/app/rate"] = provision.Parameter{Value: "9", Force: true}
	if _, err := provision.Apply(ctx, h, second, id); err != nil {
		t.Fatal(err)
	}
	got, _ = ssmC.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/rate")})
	if aws.ToString(got.Parameter.Value) != "9" {
		t.Errorf("with force the file wins, got %q", aws.ToString(got.Parameter.Value))
	}

	// Nothing the file did not mention was touched.
	if _, err := sqsC.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: stranger.QueueUrl}); err != nil {
		t.Errorf("apply deleted or damaged a queue it was never given: %v", err)
	}
}
