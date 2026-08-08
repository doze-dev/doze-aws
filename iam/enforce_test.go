// Enforcement tests: the three modes, exercised through a full stack so the
// middleware, the request→action resolver and the engine are all in play.
package iam_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
)

// stack stands up a full doze-aws stack in the given IAM mode and returns an
// IAM client on the root identity plus the endpoint URL.
func stack(t *testing.T, mode iam.Mode) (*awsiam.Client, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping stack test in -short mode")
	}
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Logf: t.Logf, IAMMode: mode,
		Services: []string{"iam", "sqs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(st.Handler())
	t.Cleanup(ts.Close)

	root := awsiam.NewFromConfig(rootCfg(), func(o *awsiam.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	return root, ts.URL
}

func rootCfg() aws.Config {
	return aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
}

// userSQS creates an IAM user with the given inline policy, mints an access
// key, and returns an SQS client signing as that user.
func userSQS(t *testing.T, root *awsiam.Client, endpoint, user, policy string) *awssqs.Client {
	t.Helper()
	ctx := context.Background()
	if _, err := root.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String(user)}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if policy != "" {
		if _, err := root.PutUserPolicy(ctx, &awsiam.PutUserPolicyInput{
			UserName: aws.String(user), PolicyName: aws.String("inline"),
			PolicyDocument: aws.String(policy),
		}); err != nil {
			t.Fatalf("PutUserPolicy: %v", err)
		}
	}
	key, err := root.CreateAccessKey(ctx, &awsiam.CreateAccessKeyInput{UserName: aws.String(user)})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}
	cfg := aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			aws.ToString(key.AccessKey.AccessKeyId),
			aws.ToString(key.AccessKey.SecretAccessKey), ""),
	}
	return awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// TestModeOffNeverDenies is the guarantee that matters most: switching IAM on
// must not break anyone's existing test suite.
func TestModeOffNeverDenies(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeOff)

	// A user with no policy at all — in AWS this can do nothing.
	sqs := userSQS(t, root, endpoint, "nobody", "")
	if _, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("jobs")}); err != nil {
		t.Fatalf("off mode must never deny, got: %v", err)
	}
}

func TestModeEnforceDeniesAndAllows(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeEnforce)

	// A user allowed only to create queues.
	limited := userSQS(t, root, endpoint, "limited",
		`{"Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*"}]}`)

	if _, err := limited.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("allowed")}); err != nil {
		t.Fatalf("granted action was denied: %v", err)
	}
	_, err := limited.ListQueues(ctx, &awssqs.ListQueuesInput{})
	if err == nil {
		t.Fatal("ungranted action should have been denied")
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("want AccessDenied, got %v", err)
	}
	// The denial should name the principal and the action.
	if !strings.Contains(err.Error(), "user/limited") || !strings.Contains(err.Error(), "sqs:ListQueues") {
		t.Fatalf("denial message should identify principal and action, got %v", err)
	}
}

// TestEnforceExplicitDenyWins checks the evaluation order survives the full
// round trip, not just the unit test.
func TestEnforceExplicitDenyWins(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeEnforce)

	sqs := userSQS(t, root, endpoint, "mixed", `{"Statement":[
		{"Effect":"Allow","Action":"sqs:*","Resource":"*"},
		{"Effect":"Deny","Action":"sqs:DeleteQueue","Resource":"*"}]}`)

	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("keep")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := sqs.DeleteQueue(ctx, &awssqs.DeleteQueueInput{QueueUrl: q.QueueUrl}); err == nil {
		t.Fatal("explicit Deny should override the sqs:* Allow")
	}
}

// TestEnforceResourceScoping proves the resolver really extracts the resource
// ARN from the wire, not just the action.
func TestEnforceResourceScoping(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeEnforce)

	// Root creates both queues so the scoped user does not need CreateQueue.
	rootSQS := awssqs.NewFromConfig(rootCfg(), func(o *awssqs.Options) { o.BaseEndpoint = aws.String(endpoint) })
	mine, err := rootSQS.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("mine")})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := rootSQS.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("theirs")})
	if err != nil {
		t.Fatal(err)
	}

	scoped := userSQS(t, root, endpoint, "scoped",
		`{"Statement":[{"Effect":"Allow","Action":"sqs:SendMessage","Resource":"arn:aws:sqs:us-east-1:000000000000:mine"}]}`)

	if _, err := scoped.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: mine.QueueUrl, MessageBody: aws.String("ok"),
	}); err != nil {
		t.Fatalf("in-scope queue was denied: %v", err)
	}
	if _, err := scoped.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: theirs.QueueUrl, MessageBody: aws.String("nope"),
	}); err == nil {
		t.Fatal("out-of-scope queue should have been denied — resource ARN was not resolved")
	}
}

// TestSoftModeRecordsWithoutBlocking is the differentiating behaviour: nothing
// is denied, but everything is observed and a least-privilege policy falls out.
func TestSoftModeRecordsWithoutBlocking(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeSoft)

	// No policy at all: in enforce mode every call would fail.
	sqs := userSQS(t, root, endpoint, "observed", "")
	if _, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("watched")}); err != nil {
		t.Fatalf("soft mode must not block: %v", err)
	}
	if _, err := sqs.ListQueues(ctx, &awssqs.ListQueuesInput{}); err != nil {
		t.Fatalf("soft mode must not block: %v", err)
	}

	// The recorder is reachable through the doze extension actions.
	log := dozeCall(t, endpoint, "DozeAccessLog", nil)
	for _, want := range []string{"sqs:CreateQueue", "sqs:ListQueues", "user/observed", "implicitDeny"} {
		if !strings.Contains(log, want) {
			t.Fatalf("access log missing %q:\n%s", want, log)
		}
	}

	// And the generated policy covers exactly what was exercised.
	policy := dozeCall(t, endpoint, "DozeGeneratePolicy", map[string]string{
		"Principal": awsident.GlobalARN("iam", "user/observed"),
	})
	for _, want := range []string{"sqs:CreateQueue", "sqs:ListQueues"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("generated policy missing %q:\n%s", want, policy)
		}
	}
	// It must not grant anything that was never used.
	if strings.Contains(policy, "sqs:DeleteQueue") {
		t.Fatalf("generated policy over-grants:\n%s", policy)
	}
}

// TestGeneratedPolicyIsAcceptedBack closes the loop: the policy soft mode
// produces must itself be a valid policy the service accepts.
func TestGeneratedPolicyIsAcceptedBack(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeSoft)

	sqs := userSQS(t, root, endpoint, "looped", "")
	sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("q")})

	raw := dozeCall(t, endpoint, "DozeGeneratePolicy", map[string]string{
		"Principal": awsident.GlobalARN("iam", "user/looped"),
	})
	doc := between(raw, "<PolicyDocument>", "</PolicyDocument>")
	if doc == "" {
		t.Fatalf("no PolicyDocument in response:\n%s", raw)
	}
	doc = strings.ReplaceAll(doc, "&#34;", `"`)
	doc = strings.ReplaceAll(doc, "&quot;", `"`)

	if _, err := root.CreatePolicy(ctx, &awsiam.CreatePolicyInput{
		PolicyName: aws.String("generated"), PolicyDocument: aws.String(doc),
	}); err != nil {
		t.Fatalf("generated policy was rejected by CreatePolicy: %v\ndocument: %s", err, doc)
	}
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// dozeCall invokes a doze extension action directly over the Query protocol,
// since the AWS SDK has no model for them.
func dozeCall(t *testing.T, endpoint, action string, extra map[string]string) string {
	t.Helper()
	form := url.Values{"Action": {action}, "Version": {"2010-05-08"}}
	for k, v := range extra {
		form.Set(k, v)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint+"/",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Sign the scope so the gateway routes to IAM.
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20240101/us-east-1/iam/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s -> %d: %s", action, resp.StatusCode, body)
	}
	return string(body)
}
