package dozeaws_test

// Every service's refusal for an unauthorised caller says that a policy
// decided it.
//
// The console tells a denial from a refusal by a fact the server stamped on the
// answer (X-Doze-Denied), not by the spelling of the status or the code: a 403
// is also what a bad signature and an expired presigned URL answer. So a
// service that rebuilds the guard's error in its own spelling and drops the
// stamp turns a denial into "Refused" on the wire, which is the wrong place to
// look. Three services did exactly that. This is the test that would have said
// so, and it exercises every service the same way: a user with no policy, in
// enforce mode, makes one read call.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryServicesDenialIsStampedAsOne(t *testing.T) {
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t), IAMMode: iam.ModeEnforce})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)

	// A user with no policy attached: every call is an implicit deny.
	root := aws.Config{Region: awsident.Region, Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	ic := awsiam.NewFromConfig(root, func(o *awsiam.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()
	if _, err := ic.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("nobody")}); err != nil {
		t.Fatal(err)
	}
	k, err := ic.CreateAccessKey(ctx, &awsiam.CreateAccessKeyInput{UserName: aws.String("nobody")})
	if err != nil {
		t.Fatal(err)
	}
	keyID := aws.ToString(k.AccessKey.AccessKeyId)

	const json10, json11, form = "application/x-amz-json-1.0", "application/x-amz-json-1.1", "application/x-www-form-urlencoded"
	for _, c := range []struct {
		scope, method, path, ctype, target, body string
	}{
		// Calls that name a resource. These are the ones the middleware does not
		// settle itself: a service with a resource policy finishes the question,
		// so the denial is written by the service's own guard, in its own
		// spelling, and that is where a stamp gets lost.
		{"sqs", "POST", "/", json10, "AmazonSQS.SendMessage", `{"QueueUrl":"http://localhost/000000000000/q","MessageBody":"x"}`},
		{"sqs", "POST", "/", form, "", "Action=SendMessage&Version=2012-11-05&QueueUrl=http%3A%2F%2Flocalhost%2F000000000000%2Fq&MessageBody=x"},
		{"sns", "POST", "/", form, "", "Action=Publish&Version=2010-03-31&TopicArn=arn%3Aaws%3Asns%3Aus-east-1%3A000000000000%3At&Message=x"},
		{"s3", "GET", "/bucket/key", "", "", ""},
		{"s3", "PUT", "/bucket", "", "", ""},
		{"kms", "POST", "/", json11, "TrentService.Encrypt", `{"KeyId":"alias/none","Plaintext":"AA=="}`},
		{"secretsmanager", "POST", "/", json11, "secretsmanager.GetSecretValue", `{"SecretId":"s"}`},
		{"kinesis", "POST", "/", json11, "Kinesis_20131202.PutRecord", `{"StreamName":"s","Data":"AA==","PartitionKey":"p"}`},
		{"lambda", "GET", "/2015-03-31/functions/f", "", "", ""},
		// Calls that name none: the middleware decides these alone.
		{"sqs", "POST", "/", json10, "AmazonSQS.ListQueues", "{}"},
		{"sqs", "POST", "/", form, "", "Action=ListQueues&Version=2012-11-05"},
		{"sns", "POST", "/", form, "", "Action=ListTopics&Version=2010-03-31"},
		{"s3", "GET", "/", "", "", ""},
		{"kms", "POST", "/", json11, "TrentService.ListKeys", "{}"},
		{"kinesis", "POST", "/", json11, "Kinesis_20131202.ListStreams", "{}"},
		{"secretsmanager", "POST", "/", json11, "secretsmanager.ListSecrets", "{}"},
		{"lambda", "GET", "/2015-03-31/functions", "", "", ""},
		{"ssm", "POST", "/", json11, "AmazonSSM.DescribeParameters", "{}"},
		{"dynamodb", "POST", "/", json10, "DynamoDB_20120810.ListTables", "{}"},
		{"events", "POST", "/", json11, "AWSEvents.ListEventBuses", "{}"},
		{"logs", "POST", "/", json11, "Logs_20140328.DescribeLogGroups", "{}"},
		{"states", "POST", "/", json10, "AWSStepFunctions.ListStateMachines", "{}"},
		{"monitoring", "POST", "/", form, "", "Action=ListMetrics&Version=2010-08-01"},
		{"iam", "POST", "/", form, "", "Action=ListUsers&Version=2010-05-08"},
	} {
		name := c.scope
		if c.target != "" {
			name += "/" + c.target
		} else if strings.HasPrefix(c.body, "Action=") {
			name += "/" + strings.SplitN(strings.TrimPrefix(c.body, "Action="), "&", 2)[0]
		}
		t.Run(name, func(t *testing.T) {
			req, _ := http.NewRequest(c.method, ts.URL+c.path, strings.NewReader(c.body))
			if c.ctype != "" {
				req.Header.Set("Content-Type", c.ctype)
			}
			if c.target != "" {
				req.Header.Set("X-Amz-Target", c.target)
			}
			req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+keyID+"/20260101/"+awsident.Region+"/"+c.scope+
				"/aws4_request, SignedHeaders=host, Signature=00")
			req.Header.Set("X-Amz-Date", "20260101T000000Z")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode < 400 {
				t.Fatalf("an unpermitted caller was served (%d): %s", resp.StatusCode, body)
			}
			if got := resp.Header.Get(awshttp.DeniedHeader); got != "iam" {
				t.Errorf("%d answered without %s: iam, so the console will call a policy decision a refusal\n  body: %s",
					resp.StatusCode, awshttp.DeniedHeader, strings.TrimSpace(string(body)))
			}
		})
	}
}
