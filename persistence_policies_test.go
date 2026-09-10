package dozeaws_test

// Resource policies must survive a restart AND still be enforced.
//
// A resource policy is enforced by the service's guard reading the record on
// every request, so a policy that is written but not reloaded fails OPEN: the
// operation the developer denied starts working again after a restart, and
// every existing test still passes because they all run inside one boot. That
// is the worst shape a bug can have here, and nothing covered it — the enforce
// tests in iam/ and the two cross-service tests in
// resource_policy_integration_test.go all live and die inside a single stack.
//
// So this test denies one user across six services, restarts, and demands the
// SAME denials from the SAME signing identity. The IAM user, its inline
// policy and its access key have to survive too, or nothing can be attempted.
//
// The positive control matters as much as the denials: an unguarded queue the
// same user must STILL be able to write to after the restart. Without it, a
// restart that lost the user's identity policy would deny everything and this
// test would pass for entirely the wrong reason.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awskin "github.com/aws/aws-sdk-go-v2/service/kinesis"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
)

// guardedNames are fixed so the second boot can find everything by name.
const (
	guardedQueue  = "guarded-queue"
	openQueue     = "open-queue"
	guardedTopic  = "guarded-topic"
	guardedBucket = "guarded-bucket"
	guardedStream = "guarded-stream"
	guardedSecret = "guarded-secret"
)

// policyClients signs every call as one particular identity.
type policyClients struct {
	sqs *awssqs.Client
	sns *awssns.Client
	s3  *awss3.Client
	kms *awskms.Client
	sm  *awssm.Client
	kin *awskin.Client
}

func policyClientsFor(cfg aws.Config, url string) policyClients {
	return policyClients{
		sqs: awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) }),
		sns: awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(url) }),
		s3:  awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(url); o.UsePathStyle = true }),
		kms: awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = aws.String(url) }),
		sm:  awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) }),
		kin: awskin.NewFromConfig(cfg, func(o *awskin.Options) { o.BaseEndpoint = aws.String(url) }),
	}
}

func rootConfig() aws.Config {
	return aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
}

// guardedIDs are the server-minted identifiers the second boot needs.
type guardedIDs struct {
	queueURL    string
	openURL     string
	topicARN    string
	keyID       string
	secretARN   string
	writerCreds aws.Config
}

func TestResourcePoliciesSurviveRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two enforcing Stacks over a shared data dir")
	}
	ctx := context.Background()
	dir := t.TempDir()

	stack1, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: t.Logf, IAMMode: iam.ModeEnforce})
	if err != nil {
		t.Fatal(err)
	}
	ts1 := httptest.NewServer(stack1.Handler())
	ids := writeGuardedResources(ctx, t, ts1.URL)

	// Before the restart the denials must already bite; otherwise the second
	// half proves nothing.
	assertDenials(ctx, t, policyClientsFor(ids.writerCreds, ts1.URL), ids, "before restart")

	ts1.Close()
	if err := stack1.Close(); err != nil {
		t.Fatalf("stack1 close: %v", err)
	}

	stack2, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: t.Logf, IAMMode: iam.ModeEnforce})
	if err != nil {
		t.Fatalf("reopen stack: %v", err)
	}
	defer stack2.Close()
	ts2 := httptest.NewServer(stack2.Handler())
	defer ts2.Close()

	assertDenials(ctx, t, policyClientsFor(ids.writerCreds, ts2.URL), ids, "after restart")
	assertPoliciesReadBack(ctx, t, policyClientsFor(rootConfig(), ts2.URL), ids)
}

// writeGuardedResources creates one resource per guarded service, denies the
// user "writer" on each, and returns the identifiers plus that user's config.
func writeGuardedResources(ctx context.Context, t *testing.T, url string) guardedIDs {
	t.Helper()
	root := policyClientsFor(rootConfig(), url)
	rootIAM := awsiam.NewFromConfig(rootConfig(), func(o *awsiam.Options) { o.BaseEndpoint = aws.String(url) })
	var ids guardedIDs

	// The user whose access key must itself survive the restart.
	if _, err := rootIAM.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("writer")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := rootIAM.PutUserPolicy(ctx, &awsiam.PutUserPolicyInput{
		UserName: aws.String("writer"), PolicyName: aws.String("inline"),
		PolicyDocument: aws.String(`{"Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`),
	}); err != nil {
		t.Fatalf("PutUserPolicy: %v", err)
	}
	key, err := rootIAM.CreateAccessKey(ctx, &awsiam.CreateAccessKeyInput{UserName: aws.String("writer")})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}
	ids.writerCreds = aws.Config{Region: awsident.Region, Credentials: credentials.NewStaticCredentialsProvider(
		aws.ToString(key.AccessKey.AccessKeyId), aws.ToString(key.AccessKey.SecretAccessKey), "")}
	writer := awsident.GlobalARN("iam", "user/writer")
	deny := func(sid, action, resource string) string {
		return `{"Version":"2012-10-17","Statement":[{"Sid":"` + sid + `","Effect":"Deny","Principal":{"AWS":"` +
			writer + `"},"Action":"` + action + `","Resource":"` + resource + `"}]}`
	}

	// SQS: one queue denied, one left open as the positive control.
	q, err := root.sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(guardedQueue)})
	if err != nil {
		t.Fatal(err)
	}
	ids.queueURL = aws.ToString(q.QueueUrl)
	open, err := root.sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(openQueue)})
	if err != nil {
		t.Fatal(err)
	}
	ids.openURL = aws.ToString(open.QueueUrl)
	if _, err := root.sqs.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl:   q.QueueUrl,
		Attributes: map[string]string{"Policy": deny("NoWriterQueue", "sqs:SendMessage", awsident.ARN("sqs", guardedQueue))},
	}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	// SNS.
	topic, err := root.sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String(guardedTopic)})
	if err != nil {
		t.Fatal(err)
	}
	ids.topicARN = aws.ToString(topic.TopicArn)
	if _, err := root.sns.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
		TopicArn: topic.TopicArn, AttributeName: aws.String("Policy"),
		AttributeValue: aws.String(deny("NoWriterTopic", "sns:Publish", ids.topicARN)),
	}); err != nil {
		t.Fatalf("SetTopicAttributes: %v", err)
	}

	// S3.
	if _, err := root.s3.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(guardedBucket)}); err != nil {
		t.Fatal(err)
	}
	if _, err := root.s3.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(guardedBucket), Key: aws.String("k"), Body: strings.NewReader("body")}); err != nil {
		t.Fatal(err)
	}
	if _, err := root.s3.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{
		Bucket: aws.String(guardedBucket),
		Policy: aws.String(deny("NoWriterObject", "s3:GetObject", "arn:aws:s3:::"+guardedBucket+"/*")),
	}); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}

	// KMS: the key policy must keep admitting the account root, or the gate
	// closes on everyone and the denial below would prove nothing.
	created, err := root.kms.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}
	ids.keyID = aws.ToString(created.KeyMetadata.KeyId)
	keyPolicy := `{"Version":"2012-10-17","Statement":[` +
		`{"Sid":"Root","Effect":"Allow","Principal":{"AWS":"` + awsident.GlobalARN("iam", "root") + `"},"Action":"kms:*","Resource":"*"},` +
		`{"Sid":"NoWriterKey","Effect":"Deny","Principal":{"AWS":"` + writer + `"},"Action":"kms:Encrypt","Resource":"*"}]}`
	if _, err := root.kms.PutKeyPolicy(ctx, &awskms.PutKeyPolicyInput{
		KeyId: aws.String(ids.keyID), PolicyName: aws.String("default"), Policy: aws.String(keyPolicy)}); err != nil {
		t.Fatalf("PutKeyPolicy: %v", err)
	}

	// Secrets Manager.
	sec, err := root.sm.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String(guardedSecret), SecretString: aws.String("shh")})
	if err != nil {
		t.Fatal(err)
	}
	ids.secretARN = aws.ToString(sec.ARN)
	if _, err := root.sm.PutResourcePolicy(ctx, &awssm.PutResourcePolicyInput{
		SecretId:       aws.String(guardedSecret),
		ResourcePolicy: aws.String(deny("NoWriterSecret", "secretsmanager:GetSecretValue", "*")),
	}); err != nil {
		t.Fatalf("PutResourcePolicy secret: %v", err)
	}

	// Kinesis.
	if _, err := root.kin.CreateStream(ctx, &awskin.CreateStreamInput{
		StreamName: aws.String(guardedStream), ShardCount: aws.Int32(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := root.kin.PutResourcePolicy(ctx, &awskin.PutResourcePolicyInput{
		ResourceARN: aws.String(awsident.ARN("kinesis", "stream/"+guardedStream)),
		Policy:      aws.String(deny("NoWriterStream", "kinesis:PutRecord", "*")),
	}); err != nil {
		t.Fatalf("PutResourcePolicy stream: %v", err)
	}
	return ids
}

// assertDenials runs the six denied operations plus the positive control.
func assertDenials(ctx context.Context, t *testing.T, c policyClients, ids guardedIDs, when string) {
	t.Helper()
	// The Sid is the point. A bare "AccessDenied" could come from anywhere —
	// a lost access key, a service that failed open into a generic refusal.
	// Naming the statement proves THIS policy, reloaded from disk, is what
	// stopped the call.
	denied := func(what, sid string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: %s should have been denied by its resource policy", when, what)
			return
		}
		if !strings.Contains(err.Error(), "AccessDenied") && !strings.Contains(err.Error(), "AuthorizationError") {
			t.Errorf("%s: %s: want AccessDenied or AuthorizationError, got %v", when, what, err)
			return
		}
		if !strings.Contains(err.Error(), sid) {
			t.Errorf("%s: %s: the denial should name the statement %q that caused it, got %v", when, what, sid, err)
		}
	}

	_, err := c.sqs.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: aws.String(ids.queueURL), MessageBody: aws.String("hi")})
	denied("sqs:SendMessage", "NoWriterQueue", err)

	_, err = c.sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: aws.String(ids.topicARN), Message: aws.String("hi")})
	denied("sns:Publish", "NoWriterTopic", err)

	_, err = c.s3.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(guardedBucket), Key: aws.String("k")})
	denied("s3:GetObject", "NoWriterObject", err)

	_, err = c.kms.Encrypt(ctx, &awskms.EncryptInput{
		KeyId: aws.String(ids.keyID), Plaintext: []byte("x")})
	denied("kms:Encrypt", "NoWriterKey", err)

	_, err = c.sm.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String(guardedSecret)})
	denied("secretsmanager:GetSecretValue", "NoWriterSecret", err)

	_, err = c.kin.PutRecord(ctx, &awskin.PutRecordInput{
		StreamName: aws.String(guardedStream), Data: []byte("x"), PartitionKey: aws.String("p")})
	denied("kinesis:PutRecord", "NoWriterStream", err)

	// The positive control: the same signing identity, an unguarded queue.
	// If this fails, the identity policy or the access key was lost and every
	// denial above is meaningless.
	if _, err := c.sqs.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: aws.String(ids.openURL), MessageBody: aws.String("hi")}); err != nil {
		t.Fatalf("%s: the unguarded queue must still accept the same user — the identity half did not survive: %v", when, err)
	}
}

// assertPoliciesReadBack proves the documents themselves came back, not just
// that something denied the request.
func assertPoliciesReadBack(ctx context.Context, t *testing.T, root policyClients, ids guardedIDs) {
	t.Helper()
	has := func(what, doc, sid string) {
		t.Helper()
		if !strings.Contains(doc, sid) {
			t.Errorf("%s policy after restart does not carry %q: %s", what, sid, doc)
		}
	}

	qa, err := root.sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: aws.String(ids.queueURL), AttributeNames: []sqstypes.QueueAttributeName{"Policy"}})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}
	has("queue", qa.Attributes["Policy"], "NoWriterQueue")

	ta, err := root.sns.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: aws.String(ids.topicARN)})
	if err != nil {
		t.Fatalf("GetTopicAttributes: %v", err)
	}
	has("topic", ta.Attributes["Policy"], "NoWriterTopic")

	bp, err := root.s3.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{Bucket: aws.String(guardedBucket)})
	if err != nil {
		t.Fatalf("GetBucketPolicy: %v", err)
	}
	has("bucket", aws.ToString(bp.Policy), "NoWriterObject")

	kp, err := root.kms.GetKeyPolicy(ctx, &awskms.GetKeyPolicyInput{
		KeyId: aws.String(ids.keyID), PolicyName: aws.String("default")})
	if err != nil {
		t.Fatalf("GetKeyPolicy: %v", err)
	}
	has("key", aws.ToString(kp.Policy), "NoWriterKey")

	sp, err := root.sm.GetResourcePolicy(ctx, &awssm.GetResourcePolicyInput{SecretId: aws.String(guardedSecret)})
	if err != nil {
		t.Fatalf("GetResourcePolicy secret: %v", err)
	}
	has("secret", aws.ToString(sp.ResourcePolicy), "NoWriterSecret")

	rp, err := root.kin.GetResourcePolicy(ctx, &awskin.GetResourcePolicyInput{
		ResourceARN: aws.String(awsident.ARN("kinesis", "stream/"+guardedStream))})
	if err != nil {
		t.Fatalf("GetResourcePolicy stream: %v", err)
	}
	has("stream", aws.ToString(rp.Policy), "NoWriterStream")
}
