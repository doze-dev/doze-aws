//go:build soak

// Soak/chaos harness (build-tagged, run via `go tool task soak`). It drives a
// mixed cross-service workload against a stack under sustained load, and can
// restart that stack mid-load to prove the data survives.
//
//	SOAK_DURATION     how long to run (default 2m)
//	SOAK_CHAOS=1      restart the stack mid-load and assert data survives
//	SOAK_CHAOS_EVERY  ops between restarts (default 2000)
//
// Two things this file used to get wrong, both worth naming because they are
// the failure modes a soak test is most prone to.
//
// IT DROPPED EVERY RETURN VALUE. The S3, SQS and DynamoDB calls in the loop
// discarded their error, as did the four resource-creation calls. The only
// live assertion was the engine-keeping-up check, so a weekly 5-minute job
// could watch three services fail every iteration and still report success.
// Every call is checked now.
//
// IT ADVERTISED A MODE IT DID NOT HAVE. The doc comment promised SOAK_CHAOS=1
// and the string appeared nowhere else in the tree. It is implemented here,
// scoped honestly: this harness runs an in-process Stack, not a subprocess, so
// chaos closes the stack and reopens it over the SAME data directory. That
// exercises the property the claim was about — every store closed and reopened,
// nothing lost — without pretending to kill a binary it never started.
package main_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envInt(name string, def int64) int64 {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func TestSoak(t *testing.T) {
	dur := envDuration("SOAK_DURATION", 2*time.Minute)
	chaos := os.Getenv("SOAK_CHAOS") == "1"
	chaosEvery := envInt("SOAK_CHAOS_EVERY", 2000)

	// One directory for the life of the test: chaos reopens over it.
	dataDir := t.TempDir()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stack.Close() }()
	srv := serve(t, stack)

	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	ep := aws.String("http://" + srv.addr)
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = ep; o.UsePathStyle = true })
	sqsc := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = ep })
	ddbc := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = ep })
	sfnc := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = ep })

	ctx := context.Background()
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("soak")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	q, err := sqsc.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("soak")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	// One execution per iteration, fire-and-forget: hours of these is where
	// the engine's execution and history buckets, and its one driver
	// goroutine, would show growth or a stall that a short test cannot.
	machine, err := sfnc.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("soak"), RoleArn: aws.String("arn:aws:iam::000000000000:role/soak"),
		Definition: aws.String(`{"StartAt":"Send","States":{"Send":{"Type":"Task","Resource":"arn:aws:states:::sqs:sendMessage",
		  "Parameters":{"QueueUrl":"` + aws.ToString(q.QueueUrl) + `","MessageBody.$":"$.key"},"Next":"Done"},"Done":{"Type":"Succeed"}}}`),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}
	if _, err := ddbc.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName:            aws.String("soak"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// restart closes every store and reopens over the same directory, then
	// proves the given key is still readable everywhere it was written.
	restart := func(n int64, witness string) {
		if err := stack.Close(); err != nil {
			t.Fatalf("soak: closing the stack at op %d: %v", n, err)
		}
		next, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dataDir})
		if err != nil {
			t.Fatalf("soak: reopening the stack at op %d: %v", n, err)
		}
		stack = next
		srv.install(stack)

		obj, err := s3c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("soak"), Key: aws.String(witness)})
		if err != nil {
			t.Fatalf("soak: after restart at op %d, S3 lost %s: %v", n, witness, err)
		}
		obj.Body.Close()
		item, err := ddbc.GetItem(ctx, &awsddb.GetItemInput{TableName: aws.String("soak"),
			Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: witness}}})
		if err != nil {
			t.Fatalf("soak: after restart at op %d, DynamoDB GetItem failed: %v", n, err)
		}
		if item.Item == nil {
			t.Fatalf("soak: after restart at op %d, DynamoDB lost item %s", n, witness)
		}
		// Nothing ever receives from this queue, so every message sent so far
		// must still be on it.
		attrs, err := sqsc.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
			QueueUrl:       q.QueueUrl,
			AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameApproximateNumberOfMessages},
		})
		if err != nil {
			t.Fatalf("soak: after restart at op %d, SQS GetQueueAttributes failed: %v", n, err)
		}
		depth := attrs.Attributes[string(sqstypes.QueueAttributeNameApproximateNumberOfMessages)]
		if depth == "" || depth == "0" {
			t.Fatalf("soak: after restart at op %d, the queue is empty (%q) — %d messages were sent", n, depth, n)
		}
		t.Logf("soak: restarted at %d ops; %s survived, queue holds %s", n, witness, depth)
	}

	var ops int64
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		n := atomic.AddInt64(&ops, 1)
		key := fmt.Sprintf("k%d", n)
		if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String("soak"), Key: aws.String(key), Body: strings.NewReader(key)}); err != nil {
			t.Fatalf("soak: op %d: s3 put: %v", n, err)
		}
		if _, err := sqsc.SendMessage(ctx, &awssqs.SendMessageInput{
			QueueUrl: q.QueueUrl, MessageBody: aws.String(key)}); err != nil {
			t.Fatalf("soak: op %d: sqs send: %v", n, err)
		}
		if _, err := ddbc.PutItem(ctx, &awsddb.PutItemInput{TableName: aws.String("soak"),
			Item: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: key}}}); err != nil {
			t.Fatalf("soak: op %d: ddb put: %v", n, err)
		}
		started, err := sfnc.StartExecution(ctx, &awssfn.StartExecutionInput{
			StateMachineArn: machine.StateMachineArn, Name: aws.String(key), Input: aws.String(`{"key":"` + key + `"}`),
		})
		if err != nil {
			t.Fatalf("soak: op %d: sfn start: %v", n, err)
		}
		if n%500 == 0 {
			// The engine has to be keeping up: the execution from 500 ops ago
			// must have finished, or the driver is stalling under the load.
			prev := fmt.Sprintf("k%d", n-499)
			d, err := sfnc.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{
				ExecutionArn: aws.String(strings.Replace(aws.ToString(started.ExecutionArn), key, prev, 1)),
			})
			if err != nil || d.Status == "RUNNING" {
				t.Fatalf("soak: execution %s from 500 ops ago is %v (%v) — the engine is not keeping up", prev, d.Status, err)
			}
			t.Logf("soak: %d ops", n)
		}
		if chaos && n%chaosEvery == 0 {
			restart(n, key)
		}
	}
	if ops == 0 {
		t.Fatal("soak: the workload never ran an iteration")
	}
	t.Logf("soak complete: %d ops over %s (chaos=%v)", atomic.LoadInt64(&ops), dur, chaos)
}
