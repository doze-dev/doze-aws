// Resource policies under enforce, through a full stack: the middleware
// stamps the identity verdict, the service combines it with the queue, topic
// or bucket policy, and the client sees AWS's answer.
package iam_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
)

const (
	allowAll = `{"Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`
	// noPolicy is a user with no identity policy at all: only a resource
	// policy can let it in.
	noPolicy = ""
)

func userARN(name string) string { return awsident.GlobalARN("iam", "user/"+name) }

func wantDenied(t *testing.T, err error, what string, mentions ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: should have been denied", what)
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("%s: want AccessDenied, got %v", what, err)
	}
	for _, m := range mentions {
		if !strings.Contains(err.Error(), m) {
			t.Fatalf("%s: denial should mention %q, got %v", what, m, err)
		}
	}
}

func sqsClient(cfg aws.Config, endpoint string) *awssqs.Client {
	return awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

func snsClient(cfg aws.Config, endpoint string) *awssns.Client {
	return awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

func s3Client(cfg aws.Config, endpoint string) *awss3.Client {
	return awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
}

// TestQueuePolicyUnderEnforce: a queue policy denies a granted user, admits
// an ungranted one, and AddPermission/RemovePermission write it for real.
func TestQueuePolicyUnderEnforce(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeEnforce)
	rootSQS := sqsClient(rootCfg(), endpoint)
	q, err := rootSQS.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}
	queueARN := awsident.ARN("sqs", "orders")
	send := func(c *awssqs.Client) error {
		_, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("hi")})
		return err
	}
	setPolicy := func(policy string) {
		t.Helper()
		if _, err := rootSQS.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
			QueueUrl: q.QueueUrl, Attributes: map[string]string{"Policy": policy},
		}); err != nil {
			t.Fatalf("SetQueueAttributes: %v", err)
		}
	}

	// A user the identity policies allow, denied by the queue policy.
	writer := sqsClient(userConfig(t, root, "writer", allowAll), endpoint)
	if err := send(writer); err != nil {
		t.Fatalf("no queue policy: identity allow must suffice: %v", err)
	}
	setPolicy(`{"Version":"2012-10-17","Statement":[{"Sid":"NoWriter","Effect":"Deny","Principal":{"AWS":"` + userARN("writer") +
		`"},"Action":"sqs:SendMessage","Resource":"` + queueARN + `"}]}`)
	wantDenied(t, send(writer), "explicit deny in the queue policy", "user/writer", "sqs:SendMessage", "NoWriter")
	if _, err := writer.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{"All"}}); err != nil {
		t.Fatalf("the deny is on SendMessage only: %v", err)
	}

	// A user with no identity policy, admitted by the queue policy alone.
	guest := sqsClient(userConfig(t, root, "guest", noPolicy), endpoint)
	wantDenied(t, send(guest), "no grant anywhere", "user/guest", "neither the identity policies nor the resource policy")
	setPolicy(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + userARN("guest") +
		`"},"Action":"sqs:SendMessage","Resource":"` + queueARN + `"}]}`)
	if err := send(guest); err != nil {
		t.Fatalf("queue policy allow must admit an ungranted identity: %v", err)
	}
	_, err = guest.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl})
	wantDenied(t, err, "an action the queue policy does not name")

	// AddPermission writes the statement AWS writes: the account root as
	// principal. That names the account, which delegates to identity
	// policies — so, as on AWS, it changes nothing for a same-account user:
	// the guest stays out and the writer (whose deny is gone) gets in.
	setPolicy("")
	wantDenied(t, send(guest), "policy cleared")
	if _, err := rootSQS.AddPermission(ctx, &awssqs.AddPermissionInput{
		QueueUrl: q.QueueUrl, Label: aws.String("account"), AWSAccountIds: []string{awsident.AccountID}, Actions: []string{"SendMessage"},
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	attrs, _ := rootSQS.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{"Policy"}})
	pol := attrs.Attributes["Policy"]
	for _, want := range []string{`"account"`, "SQS:SendMessage", awsident.GlobalARN("iam", "root"), queueARN} {
		if !strings.Contains(pol, want) {
			t.Fatalf("AddPermission policy lacks %q: %s", want, pol)
		}
	}
	wantDenied(t, send(guest), "the account principal delegates to identity policies the guest lacks")
	if err := send(writer); err != nil {
		t.Fatalf("writer's identity allow: %v", err)
	}
	if _, err := rootSQS.AddPermission(ctx, &awssqs.AddPermissionInput{
		QueueUrl: q.QueueUrl, Label: aws.String("account"), AWSAccountIds: []string{awsident.AccountID}, Actions: []string{"SendMessage"},
	}); err == nil {
		t.Fatal("AddPermission with a label in use must fail")
	}
	if _, err := rootSQS.RemovePermission(ctx, &awssqs.RemovePermissionInput{QueueUrl: q.QueueUrl, Label: aws.String("account")}); err != nil {
		t.Fatalf("RemovePermission: %v", err)
	}
	attrs, _ = rootSQS.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{"Policy"}})
	if strings.Contains(attrs.Attributes["Policy"], `"account"`) {
		t.Fatalf("RemovePermission left the statement: %s", attrs.Attributes["Policy"])
	}
	if _, err := rootSQS.RemovePermission(ctx, &awssqs.RemovePermissionInput{QueueUrl: q.QueueUrl, Label: aws.String("account")}); err == nil {
		t.Fatal("RemovePermission of an unknown label must fail")
	}
}

// TestTopicPolicyUnderEnforce: the topic's default policy admits the
// account's identities and nobody else; a written policy replaces it.
func TestTopicPolicyUnderEnforce(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeEnforce, "iam", "sns")
	rootSNS := snsClient(rootCfg(), endpoint)
	top, err := rootSNS.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("alerts")})
	if err != nil {
		t.Fatal(err)
	}
	publish := func(c *awssns.Client) error {
		_, err := c.Publish(ctx, &awssns.PublishInput{TopicArn: top.TopicArn, Message: aws.String("hi")})
		return err
	}

	writer := snsClient(userConfig(t, root, "writer", allowAll), endpoint)
	if err := publish(writer); err != nil {
		t.Fatalf("default topic policy plus an identity allow: %v", err)
	}
	guest := snsClient(userConfig(t, root, "guest", noPolicy), endpoint)
	wantDenied(t, publish(guest), "default topic policy admits only granted identities", "user/guest", "sns:Publish")

	if _, err := rootSNS.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
		TopicArn: top.TopicArn, AttributeName: aws.String("Policy"),
		AttributeValue: aws.String(`{"Version":"2012-10-17","Statement":[
			{"Effect":"Allow","Principal":{"AWS":"` + userARN("guest") + `"},"Action":"sns:Publish","Resource":"` + aws.ToString(top.TopicArn) + `"},
			{"Sid":"NoWriter","Effect":"Deny","Principal":{"AWS":"` + userARN("writer") + `"},"Action":"sns:Publish","Resource":"*"}]}`),
	}); err != nil {
		t.Fatalf("SetTopicAttributes: %v", err)
	}
	if err := publish(guest); err != nil {
		t.Fatalf("topic policy allow must admit the guest: %v", err)
	}
	wantDenied(t, publish(writer), "topic policy deny on a granted user", "NoWriter")

	// The written policy is what GetTopicAttributes reports, once.
	got, _ := rootSNS.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: top.TopicArn})
	if !strings.Contains(got.Attributes["Policy"], "NoWriter") {
		t.Fatalf("Policy attribute should be the written policy: %s", got.Attributes["Policy"])
	}
	// AddPermission names the account, which delegates to identity policies:
	// the statement is written, the guest stays out. RemovePermission drops it.
	if _, err := rootSNS.AddPermission(ctx, &awssns.AddPermissionInput{
		TopicArn: top.TopicArn, Label: aws.String("readers"), AWSAccountId: []string{awsident.AccountID}, ActionName: []string{"GetTopicAttributes"},
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	got, _ = rootSNS.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: top.TopicArn})
	for _, want := range []string{`"readers"`, "SNS:GetTopicAttributes", awsident.GlobalARN("iam", "root"), "NoWriter"} {
		if !strings.Contains(got.Attributes["Policy"], want) {
			t.Fatalf("AddPermission policy lacks %q: %s", want, got.Attributes["Policy"])
		}
	}
	_, err = guest.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: top.TopicArn})
	wantDenied(t, err, "the account principal delegates to identity policies the guest lacks")
	if _, err := rootSNS.RemovePermission(ctx, &awssns.RemovePermissionInput{TopicArn: top.TopicArn, Label: aws.String("readers")}); err != nil {
		t.Fatalf("RemovePermission: %v", err)
	}
	got, _ = rootSNS.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: top.TopicArn})
	if strings.Contains(got.Attributes["Policy"], `"readers"`) {
		t.Fatalf("RemovePermission left the statement: %s", got.Attributes["Policy"])
	}
	if _, err := rootSNS.RemovePermission(ctx, &awssns.RemovePermissionInput{TopicArn: top.TopicArn, Label: aws.String("readers")}); err == nil {
		t.Fatal("RemovePermission of an unknown label must fail")
	}
}

// TestBucketPolicyUnderEnforce: the bucket policy is evaluated on object
// requests, with S3's own error shape.
func TestBucketPolicyUnderEnforce(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeEnforce, "iam", "s3")
	rootS3 := s3Client(rootCfg(), endpoint)
	if _, err := rootS3.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("docs")}); err != nil {
		t.Fatal(err)
	}
	if _, err := rootS3.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("docs"), Key: aws.String("a.txt"), Body: strings.NewReader("a")}); err != nil {
		t.Fatal(err)
	}
	get := func(c *awss3.Client) error {
		out, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("docs"), Key: aws.String("a.txt")})
		if err == nil {
			out.Body.Close()
		}
		return err
	}

	reader := s3Client(userConfig(t, root, "reader", allowAll), endpoint)
	if err := get(reader); err != nil {
		t.Fatalf("no bucket policy: %v", err)
	}
	if _, err := rootS3.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{Bucket: aws.String("docs"), Policy: aws.String(
		`{"Version":"2012-10-17","Statement":[
			{"Sid":"NoReader","Effect":"Deny","Principal":{"AWS":"` + userARN("reader") + `"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::docs/*"},
			{"Effect":"Allow","Principal":{"AWS":"` + userARN("guest") + `"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::docs/*"}]}`)}); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}
	wantDenied(t, get(reader), "bucket policy deny", "s3:GetObject", "arn:aws:s3:::docs/a.txt", "NoReader")
	if _, err := reader.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String("docs")}); err != nil {
		t.Fatalf("the deny is on GetObject only: %v", err)
	}

	guest := s3Client(userConfig(t, root, "guest", noPolicy), endpoint)
	if err := get(guest); err != nil {
		t.Fatalf("bucket policy allow must admit the guest: %v", err)
	}
	_, err := guest.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("docs"), Key: aws.String("b.txt"), Body: strings.NewReader("b")})
	wantDenied(t, err, "an action the bucket policy does not name", "s3:PutObject")
	_, err = guest.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String("docs")})
	wantDenied(t, err, "bucket-level action", "s3:ListBucket")

	// Deleting the policy restores identity-only evaluation.
	if _, err := rootS3.DeleteBucketPolicy(ctx, &awss3.DeleteBucketPolicyInput{Bucket: aws.String("docs")}); err != nil {
		t.Fatal(err)
	}
	if err := get(reader); err != nil {
		t.Fatalf("policy deleted: %v", err)
	}
	wantDenied(t, get(guest), "policy deleted, guest has nothing")
}

// TestRootAndUnscopedActionsUnderEnforce: the root identity is never gated
// by a resource policy that does not deny it, and an ungranted action that
// names no resource is refused by the middleware, not deferred.
func TestRootAndUnscopedActionsUnderEnforce(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeEnforce)
	rootSQS := sqsClient(rootCfg(), endpoint)
	q, _ := rootSQS.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("q")})
	rootSQS.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: q.QueueUrl, Attributes: map[string]string{
		"Policy": `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Principal":{"AWS":"` + userARN("x") + `"},"Action":"sqs:*","Resource":"*"}]}`,
	}})
	if _, err := rootSQS.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("hi")}); err != nil {
		t.Fatalf("root is not the denied principal: %v", err)
	}
	guest := sqsClient(userConfig(t, root, "guest", noPolicy), endpoint)
	_, err := guest.ListQueues(ctx, &awssqs.ListQueuesInput{})
	wantDenied(t, err, "ListQueues names no queue", "sqs:ListQueues")
	_, err = guest.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("new")})
	wantDenied(t, err, "CreateQueue has no policy yet", "sqs:CreateQueue")
	_, err = guest.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: aws.String(strings.Replace(aws.ToString(q.QueueUrl), "/q", "/missing", 1))})
	wantDenied(t, err, "a queue that does not exist", "sqs:GetQueueAttributes")

}
