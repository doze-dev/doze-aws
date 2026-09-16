package dozeaws_test

// Do the responses match the shape AWS says they have?
//
// .audit-models holds eighteen Smithy models. Everything in this tree that
// reads them reads op.Input — what a client may send, and what doze-aws must
// refuse. The other half, op.Output, describes what each operation ANSWERS
// with: which members are required, what type each is, which are enums. It sat
// unread until `dzaudit shapes` was written, and this is what reads it.
//
// The gap it fills is specific. A rejection-parity case proves a bad request is
// refused. An SDK contract test proves one operation somebody thought to write
// a test for round-trips. Neither says anything about the shape of the other
// responses, and those failures hide well: a missing required member reaches an
// SDK as a zero value that looks like an answer, and a number rendered as a
// string is a decode error in a strict client and silence in a loose one.
//
// # How the responses are obtained
//
// Through the real SDKs, over the real wire, with a transport in the middle
// that keeps a copy. Driving the operations by hand would mean hand-building a
// valid request for each — which is the expensive part of the existing parity
// suites, and the reason they are per-service. Letting the SDK build the
// request and reading the operation name back off it (X-Amz-Target for JSON,
// Action for Query) costs nothing and works for every service that speaks
// either.
//
// # What it adds over the SDK, which is less than it first appears
//
// Driving through the real SDK means the SDK's own deserialiser is already a
// type oracle, and a strict one. Rendering DynamoDB's Scan Count as a string
// does not reach shapecheck at all — the SDK refuses the response first:
//
//	deserialization failed, expected Integer to be json.Number, got string
//
// So the type half of this check is largely redundant for JSON services. What
// is NOT redundant is everything the SDK deliberately tolerates:
//
//	required   the SDK leaves an absent required member as a nil pointer and
//	           says nothing. A caller dereferences it and gets a zero that
//	           looks like an answer.
//	enum       aws-sdk-go-v2 models enums as open strings, so an undeclared
//	           value round-trips silently. Returning "Active" where the model
//	           says ACTIVE passes the SDK and fails here, naming both.
//
// That is the honest scope, and it is why this is worth having next to the
// contract tests rather than instead of them.
//
// # What it cannot do
//
// It checks SHAPE, never VALUES: that a count is an integer, not that it is the
// integer AWS would have said. Only a real AWS response could tell you that,
// and there is none here. See internal/shapecheck.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/internal/gateway"
	"github.com/doze-dev/doze-aws/internal/shapecheck"
)

// captured is one response, with the service and operation that produced it.
//
// Both, because the operation alone is ambiguous: logs and Step Functions each
// declare a ListTagsForResource, and checking one service's response against
// the other's shape would be a confident, meaningless result. The collision
// guard below found that on the first run.
type captured struct {
	service string
	op      string
	body    []byte
}

// recorder keeps a copy of every successful response, keyed by operation.
type recorder struct {
	inner http.RoundTripper
	mu    sync.Mutex
	got   []captured
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	op := operationOf(req)
	resp, err := r.inner.RoundTrip(req)
	if err != nil || resp == nil || op == "" {
		return resp, err
	}
	// Only successes. A refusal's body is an error shape, which the model
	// describes elsewhere and which the parity suites already cover.
	if resp.StatusCode/100 != 2 || resp.Body == nil {
		return resp, err
	}
	body, rerr := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if rerr == nil {
		r.mu.Lock()
		r.got = append(r.got, captured{service: serviceOf(req), op: op, body: body})
		r.mu.Unlock()
	}
	return resp, err
}

// operationOf reads the operation name off the REQUEST.
//
// The SDK has just built it, so the name is on the wire in one of two places
// depending on protocol, and reading it there avoids threading a Smithy
// middleware through every client for the same answer.
func operationOf(req *http.Request) string {
	if t := req.Header.Get("X-Amz-Target"); t != "" {
		if _, after, ok := strings.Cut(t, "."); ok {
			return after
		}
		return t
	}
	// Query protocol: the body is a form with Action=.
	if req.Body != nil && strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		raw, err := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(raw))
		if err == nil {
			for _, kv := range strings.Split(string(raw), "&") {
				if v, ok := strings.CutPrefix(kv, "Action="); ok {
					return v
				}
			}
		}
	}
	return ""
}

// serviceOf names the doze-aws service a request is for, using the gateway's
// own routing rules rather than a second table that could disagree with them.
func serviceOf(req *http.Request) string {
	return gateway.Route(awsident.Default(), "", req)
}

// shapesFor loads one service's committed output shapes.
func shapesFor(t *testing.T, dir, model string) map[string]shapecheck.Shape {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", dir, "testdata", "shapes_"+model+".json"))
	if err != nil {
		// The test binary runs in the package directory; the root package's own
		// path is simply the service dir.
		raw, err = os.ReadFile(filepath.Join(dir, "testdata", "shapes_"+model+".json"))
	}
	if err != nil {
		t.Fatalf("reading shapes for %s: %v", model, err)
	}
	var list []shapecheck.Shape
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("parsing shapes for %s: %v", model, err)
	}
	out := make(map[string]shapecheck.Shape, len(list))
	for _, s := range list {
		out[s.Operation] = s
	}
	return out
}

func TestResponsesMatchTheModelsOutputShapes(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Logf: dozetest.Quiet(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()
	t.Cleanup(func() { dozetest.NoFaults(t, stack) })

	rec := &recorder{inner: http.DefaultTransport}
	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
		HTTPClient:  &http.Client{Transport: rec},
	}
	ep := aws.String(ts.URL)

	// Keyed by SERVICE then operation. Flattening to the operation alone was
	// the first thing tried and it refused to run: logs and Step Functions both
	// declare ListTagsForResource, so a flat lookup would have checked one
	// service's response against the other's shape.
	//
	// The service comes from gateway.Route — the router doze-aws actually
	// dispatches with — rather than a second prefix table beside it.
	// JSON-protocol services only.
	//
	// SNS and STS answer Query/XML, and shapecheck reads JSON — pointing it at
	// an XML body produces "the response is not JSON" for every operation,
	// which says nothing about the response and everything about the harness.
	// Covering them means teaching the checker the XML shape rules (a
	// one-element list is indistinguishable from a scalar without the model's
	// own xmlFlattened/xmlName traits, which dzaudit already extracts for the
	// rejection suites) and is worth doing separately rather than badly here.
	//
	// S3, Lambda and API Gateway are absent for a different reason: they are
	// REST-routed, so the operation is not named on the wire and operationOf
	// cannot read it back. Their route tables are already emitted by
	// `dzaudit routes`, which is the way in when someone extends this.
	shapes := map[string]map[string]shapecheck.Shape{}
	for _, p := range []struct{ service, dir, model string }{
		{"sqs", "sqs", "sqs"},
		{"dynamodb", "dynamodb", "dynamodb"},
		{"kms", "kms", "kms"},
		{"logs", "logs", "cloudwatch-logs"},
		{"stepfunctions", "stepfunctions", "sfn"},
	} {
		shapes[p.service] = shapesFor(t, p.dir, p.model)
	}

	drive(ctx, t, cfg, ep)

	rec.mu.Lock()
	got := append([]captured(nil), rec.got...)
	rec.mu.Unlock()
	if len(got) < 15 {
		t.Fatalf("only %d responses captured — the workload did not run, and a "+
			"conformance suite that checks nothing passes vacuously", len(got))
	}

	checked, seen := 0, map[string]bool{}
	for _, c := range got {
		ops, ok := shapes[c.service]
		if !ok {
			continue // a service outside the six loaded
		}
		s, ok := ops[c.op]
		if !ok {
			continue // an operation the model does not describe (a doze extension)
		}
		seen[c.service+":"+c.op] = true
		checked++
		for _, p := range shapecheck.Check(c.body, s) {
			t.Errorf("%s %s response does not match the model: %s\n  body: %s",
				c.service, c.op, p, truncateBody(c.body))
		}
	}
	if checked == 0 {
		t.Fatal("no captured response matched a loaded shape — the operation names do not line up")
	}
	t.Logf("checked %d responses across %d distinct operations", checked, len(seen))
}

func truncateBody(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

// drive runs a realistic spread of operations. Every call's RESPONSE is what is
// under test, so an error here is a harness failure, not a finding.
func drive(ctx context.Context, t *testing.T, cfg aws.Config, ep *string) {
	t.Helper()
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = ep })
	ddb := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = ep })
	kms := awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = ep })
	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = ep })
	logs := awslogs.NewFromConfig(cfg, func(o *awslogs.Options) { o.BaseEndpoint = ep })
	sfn := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = ep })

	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	// SQS
	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("conf")})
	must("CreateQueue", err)
	_, err = sqs.ListQueues(ctx, &awssqs.ListQueuesInput{})
	must("ListQueues", err)
	_, err = sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("conf")})
	must("GetQueueUrl", err)
	_, err = sqs.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("x")})
	must("SendMessage", err)
	_, err = sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl})
	must("ReceiveMessage", err)

	// DynamoDB
	_, err = ddb.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName:            aws.String("conf"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
	})
	must("CreateTable", err)
	_, err = ddb.ListTables(ctx, &awsddb.ListTablesInput{})
	must("ListTables", err)
	_, err = ddb.DescribeTable(ctx, &awsddb.DescribeTableInput{TableName: aws.String("conf")})
	must("DescribeTable", err)
	_, err = ddb.PutItem(ctx, &awsddb.PutItemInput{TableName: aws.String("conf"),
		Item: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: "a"}}})
	must("PutItem", err)
	_, err = ddb.Scan(ctx, &awsddb.ScanInput{TableName: aws.String("conf")})
	must("Scan", err)

	// KMS
	key, err := kms.CreateKey(ctx, &awskms.CreateKeyInput{})
	must("CreateKey", err)
	_, err = kms.ListKeys(ctx, &awskms.ListKeysInput{})
	must("ListKeys", err)
	_, err = kms.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: key.KeyMetadata.KeyId})
	must("DescribeKey", err)
	enc, err := kms.Encrypt(ctx, &awskms.EncryptInput{KeyId: key.KeyMetadata.KeyId, Plaintext: []byte("hi")})
	must("Encrypt", err)
	_, err = kms.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc.CiphertextBlob})
	must("Decrypt", err)

	// SNS (Query protocol)
	top, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("conf")})
	must("CreateTopic", err)
	_, err = sns.ListTopics(ctx, &awssns.ListTopicsInput{})
	must("ListTopics", err)
	_, err = sns.Publish(ctx, &awssns.PublishInput{TopicArn: top.TopicArn, Message: aws.String("x")})
	must("Publish", err)

	// CloudWatch Logs
	_, err = logs.CreateLogGroup(ctx, &awslogs.CreateLogGroupInput{LogGroupName: aws.String("/conf")})
	must("CreateLogGroup", err)
	_, err = logs.DescribeLogGroups(ctx, &awslogs.DescribeLogGroupsInput{})
	must("DescribeLogGroups", err)

	// Step Functions
	m, err := sfn.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("conf"), RoleArn: aws.String("arn:aws:iam::000000000000:role/r"),
		Definition: aws.String(`{"StartAt":"Done","States":{"Done":{"Type":"Succeed"}}}`),
	})
	must("CreateStateMachine", err)
	_, err = sfn.ListStateMachines(ctx, &awssfn.ListStateMachinesInput{})
	must("ListStateMachines", err)
	_, err = sfn.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: m.StateMachineArn})
	must("DescribeStateMachine", err)
}
