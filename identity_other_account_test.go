package dozeaws_test

// What an instance started with another account and region hands back.
//
// TestEveryServiceMintsTheConfiguredIdentity reads one ARN per service. These
// are the values that are not the resource's own ARN, which is where a literal
// survived: a key policy's principal, a subscription's owner, a bucket's
// location, an endpoint address, the context object of a tested state. Each was
// found by reading the source (TestNoIdentityIsWrittenIntoCode); each is read
// back here through the API, because under the defaults every one of them is
// exactly right.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// The other identity. Neither value is a default, and the account has no run
// of zeros a partial match could hide in.
const (
	otherAccount = "111122223333"
	otherRegion  = "eu-west-1"
)

// otherStack boots a stack under the other identity and returns a client
// configuration for it and its address.
func otherStack(t *testing.T, mode iam.Mode, services ...string) (aws.Config, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a stack")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Logf: dozetest.Logf(t), Services: services, IAMMode: mode,
		Identity: awsident.Identity{Region: otherRegion, AccountID: otherAccount},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	return aws.Config{Region: otherRegion, Credentials: creds}, ts.URL
}

// A key with no policy of its own gets the default one, which admits the
// account's root. It named the default account's root, so under any other
// account with IAM enforced the key refused its own creator from the moment it
// existed.
func TestANewKeyAdmitsItsOwnAccountsRoot(t *testing.T) {
	ctx := context.Background()
	cfg, endpoint := otherStack(t, iam.ModeEnforce, "iam", "kms")
	c := awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = aws.String(endpoint) })

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	id := key.KeyMetadata.KeyId
	if got := aws.ToString(key.KeyMetadata.AWSAccountId); got != otherAccount {
		t.Errorf("CreateKey: AWSAccountId = %q, want %q", got, otherAccount)
	}

	enc, err := c.Encrypt(ctx, &awskms.EncryptInput{KeyId: id, Plaintext: []byte("x")})
	if err != nil {
		t.Fatalf("the account's root cannot use a key it has just made: %v", err)
	}
	if _, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc.CiphertextBlob}); err != nil {
		t.Errorf("Decrypt: %v", err)
	}

	desc, err := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: id})
	if err != nil {
		t.Fatalf("DescribeKey: %v", err)
	}
	if got := aws.ToString(desc.KeyMetadata.AWSAccountId); got != otherAccount {
		t.Errorf("DescribeKey: AWSAccountId = %q, want %q", got, otherAccount)
	}
	pol, err := c.GetKeyPolicy(ctx, &awskms.GetKeyPolicyInput{KeyId: id})
	if err != nil {
		t.Fatalf("GetKeyPolicy: %v", err)
	}
	if want := "arn:aws:iam::" + otherAccount + ":root"; !strings.Contains(aws.ToString(pol.Policy), want) {
		t.Errorf("the default key policy does not name %s:\n%s", want, aws.ToString(pol.Policy))
	}
	if strings.Contains(aws.ToString(pol.Policy), awsident.AccountID) {
		t.Errorf("the default key policy names the default account:\n%s", aws.ToString(pol.Policy))
	}
}

// A subscription's Owner is the account that made it.
func TestASubscriptionIsOwnedByTheInstancesAccount(t *testing.T) {
	ctx := context.Background()
	cfg, endpoint := otherStack(t, "", "sns", "sqs")
	snsC := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(endpoint) })
	sqsC := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(endpoint) })

	if _, err := sqsC.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("inbox")}); err != nil {
		t.Fatal(err)
	}
	topic, err := snsC.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("news")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snsC.Subscribe(ctx, &awssns.SubscribeInput{TopicArn: topic.TopicArn, Protocol: aws.String("sqs"),
		Endpoint: aws.String("arn:aws:sqs:" + otherRegion + ":" + otherAccount + ":inbox")}); err != nil {
		t.Fatal(err)
	}

	all, err := snsC.ListSubscriptions(ctx, &awssns.ListSubscriptionsInput{})
	if err != nil {
		t.Fatal(err)
	}
	byTopic, err := snsC.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{TopicArn: topic.TopicArn})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Subscriptions) != 1 || len(byTopic.Subscriptions) != 1 {
		t.Fatalf("want one subscription from each listing, got %d and %d", len(all.Subscriptions), len(byTopic.Subscriptions))
	}
	for what, owner := range map[string]string{
		"ListSubscriptions":        aws.ToString(all.Subscriptions[0].Owner),
		"ListSubscriptionsByTopic": aws.ToString(byTopic.Subscriptions[0].Owner),
	} {
		if owner != otherAccount {
			t.Errorf("%s: Owner = %q, want %q", what, owner, otherAccount)
		}
	}
}

// DescribeEndpoints is "the regional endpoint information": where to send the
// requests that follow. It answered AWS's own us-east-1 hostname, which sends a
// client that honours it to the real service. A local instance's endpoint is
// the address the request arrived on.
func TestDescribeEndpointsAnswersThisInstance(t *testing.T) {
	ctx := context.Background()
	cfg, endpoint := otherStack(t, "", "dynamodb")
	c := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(endpoint) })

	out, err := c.DescribeEndpoints(ctx, &awsddb.DescribeEndpointsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Endpoints) != 1 {
		t.Fatalf("want one endpoint, got %d", len(out.Endpoints))
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	got := aws.ToString(out.Endpoints[0].Address)
	if got != u.Host {
		t.Errorf("Address = %q, want the address the request came to, %q", got, u.Host)
	}
	if strings.Contains(got, "amazonaws.com") {
		t.Errorf("Address = %q sends a client to AWS", got)
	}
}

// GetBucketLocation "returns the Region the bucket resides in"; only a bucket
// in us-east-1 has a null LocationConstraint. It was always empty, so every
// bucket of an instance in eu-west-1 claimed to be in us-east-1.
func TestABucketIsLocatedInTheInstancesRegion(t *testing.T) {
	ctx := context.Background()
	cfg, endpoint := otherStack(t, "", "s3")
	c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("photos")}); err != nil {
		t.Fatal(err)
	}
	loc, err := c.GetBucketLocation(ctx, &awss3.GetBucketLocationInput{Bucket: aws.String("photos")})
	if err != nil {
		t.Fatal(err)
	}
	if string(loc.LocationConstraint) != otherRegion {
		t.Errorf("LocationConstraint = %q, want %q", loc.LocationConstraint, otherRegion)
	}
	head, err := c.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String("photos")})
	if err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(head.BucketRegion); got != otherRegion {
		t.Errorf("HeadBucket: BucketRegion = %q, want %q; the two must agree", got, otherRegion)
	}
}

// And the other half of the same sentence: in us-east-1 it is null.
func TestABucketInUSEast1HasANullLocation(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t), Services: []string{"s3"},
		Identity: awsident.Identity{Region: "us-east-1", AccountID: otherAccount}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	c := awss3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: creds},
		func(o *awss3.Options) { o.BaseEndpoint = aws.String(ts.URL); o.UsePathStyle = true })
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("photos")}); err != nil {
		t.Fatal(err)
	}
	loc, err := c.GetBucketLocation(ctx, &awss3.GetBucketLocationInput{Bucket: aws.String("photos")})
	if err != nil {
		t.Fatal(err)
	}
	if loc.LocationConstraint != "" {
		t.Errorf("LocationConstraint = %q, want it empty for us-east-1", loc.LocationConstraint)
	}
}

// TestState's inspection data shows each stage of the state's input pipeline.
// The stages were computed against an execution with the default identity's
// ARNs, so a Parameters block reading the context object showed one execution
// in the output and another in afterParameters.
func TestATestedStatesInspectionNamesTheSameExecution(t *testing.T) {
	_, endpoint := otherStack(t, "", "stepfunctions")

	// Posted by hand: the SDK sends TestState to sync-<host>, and there is no
	// such name for 127.0.0.1.
	definition, _ := json.Marshal(`{"Type":"Pass","Parameters":{"exec.$":"$$.Execution.Id","machine.$":"$$.StateMachine.Id"},"End":true}`)
	body := call(t, endpoint, "AWSStepFunctions.TestState", `{"inspectionLevel":"DEBUG","input":"{}",`+
		`"roleArn":"arn:aws:iam::`+otherAccount+`:role/r","definition":`+string(definition)+`}`)
	var out struct {
		Output         string `json:"output"`
		InspectionData struct {
			AfterParameters string `json:"afterParameters"`
		} `json:"inspectionData"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	after := out.InspectionData.AfterParameters
	if want := "arn:aws:states:" + otherRegion + ":" + otherAccount + ":"; strings.Count(after, want) != 2 {
		t.Errorf("afterParameters should carry two ARNs starting %s:\n%s", want, after)
	}
	if strings.Contains(after, awsident.AccountID) || strings.Contains(after, awsident.Region) {
		t.Errorf("afterParameters names the default identity:\n%s", after)
	}
	// The state's output is the same Parameters, evaluated by the run itself.
	if out.Output != after {
		t.Errorf("the run's output and the inspected afterParameters disagree:\n output: %s\n after:  %s", out.Output, after)
	}
}
