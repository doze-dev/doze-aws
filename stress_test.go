package dozeaws_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// TestConcurrencyStress hammers several bbolt-backed services with many
// concurrent workers doing mixed reads and writes against shared resources. Its
// value is under `go test -race`: it surfaces data races and concurrent-access
// bugs in the stores that single-threaded contract tests can't. It also asserts
// every operation succeeds under contention.
func TestConcurrencyStress(t *testing.T) {
	if testing.Short() {
		t.Skip("concurrency stress; run in the full -race suite")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(ts.URL); o.UsePathStyle = true })
	ddbc := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqsc := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	kmsc := awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	snsc := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ebc := awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	// Shared resources every worker contends on.
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("stress")}); err != nil {
		t.Fatal(err)
	}
	if _, err := ddbc.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName:            aws.String("stress"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	}); err != nil {
		t.Fatal(err)
	}
	q, err := sqsc.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("stress")})
	if err != nil {
		t.Fatal(err)
	}
	key, err := kmsc.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	// SNS topic subscribed to the queue, and an EventBridge rule targeting it —
	// so Publish/PutEvents drive the delivery goroutines under concurrency.
	topic, err := snsc.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("stress")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snsc.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: topic.TopicArn, Protocol: aws.String("sqs"),
		Endpoint: aws.String(awsident.ARN("sqs", "stress")), ReturnSubscriptionArn: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ebc.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("stress"), EventPattern: aws.String(`{"source":["stress"]}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ebc.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("stress"),
		Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(awsident.ARN("sqs", "stress"))}},
	}); err != nil {
		t.Fatal(err)
	}

	const (
		workers = 24
		iters   = 20
	)
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := fmt.Sprintf("w%d-i%d", w, i)
				if err := stressOnce(ctx, &stressClients{s3c, ddbc, sqsc, kmsc, snsc, ebc}, keyID, q.QueueUrl, aws.ToString(topic.TopicArn), id); err != nil {
					select {
					case errCh <- fmt.Errorf("worker %d iter %d: %w", w, i, err):
					default:
					}
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

type stressClients struct {
	s3c  *awss3.Client
	ddbc *awsddb.Client
	sqsc *awssqs.Client
	kmsc *awskms.Client
	snsc *awssns.Client
	ebc  *awseb.Client
}

func stressOnce(ctx context.Context, c *stressClients, keyID string, queueURL *string, topicARN, id string) error {
	s3c, ddbc, sqsc, kmsc := c.s3c, c.ddbc, c.sqsc, c.kmsc
	// S3: put then read back a unique object.
	if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("stress"), Key: aws.String(id), Body: bytes.NewReader([]byte(id)),
	}); err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}
	got, err := s3c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("stress"), Key: aws.String(id)})
	if err != nil {
		return fmt.Errorf("s3 get: %w", err)
	}
	b, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if string(b) != id {
		return fmt.Errorf("s3 body = %q, want %q", b, id)
	}

	// DynamoDB: put then get a unique item.
	if _, err := ddbc.PutItem(ctx, &awsddb.PutItemInput{
		TableName: aws.String("stress"),
		Item:      map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: id}},
	}); err != nil {
		return fmt.Errorf("ddb put: %w", err)
	}
	di, err := ddbc.GetItem(ctx, &awsddb.GetItemInput{
		TableName: aws.String("stress"),
		Key:       map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: id}},
	})
	if err != nil {
		return fmt.Errorf("ddb get: %w", err)
	}
	if di.Item == nil {
		return fmt.Errorf("ddb item %q missing", id)
	}

	// SQS: send + receive against the shared queue.
	if _, err := sqsc.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: queueURL, MessageBody: aws.String(id)}); err != nil {
		return fmt.Errorf("sqs send: %w", err)
	}
	if _, err := sqsc.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: queueURL, MaxNumberOfMessages: 1}); err != nil {
		return fmt.Errorf("sqs receive: %w", err)
	}

	// KMS: encrypt + decrypt against the shared key (exercises the crypto path).
	enc, err := kmsc.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: []byte(id)})
	if err != nil {
		return fmt.Errorf("kms encrypt: %w", err)
	}
	dec, err := kmsc.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc.CiphertextBlob})
	if err != nil {
		return fmt.Errorf("kms decrypt: %w", err)
	}
	if string(dec.Plaintext) != id {
		return fmt.Errorf("kms decrypt = %q, want %q", dec.Plaintext, id)
	}

	// SNS publish (fans out to the SQS-subscribed topic) and EventBridge
	// PutEvents (matches the rule, delivers to SQS) — exercise the delivery
	// paths under concurrency.
	if _, err := c.snsc.Publish(ctx, &awssns.PublishInput{TopicArn: aws.String(topicARN), Message: aws.String(id)}); err != nil {
		return fmt.Errorf("sns publish: %w", err)
	}
	if _, err := c.ebc.PutEvents(ctx, &awseb.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{{
			Source: aws.String("stress"), DetailType: aws.String("t"), Detail: aws.String(`{}`),
		}},
	}); err != nil {
		return fmt.Errorf("eb putevents: %w", err)
	}
	return nil
}

// TestStackChurn starts and stops many Stacks concurrently, exercising the
// service New/Close lifecycles (background goroutines, DB open/close) under
// contention — the shutdown-race class the EventBridge scheduler bug was.
func TestStackChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("lifecycle churn; run in the full -race suite")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir()})
				if err != nil {
					t.Errorf("NewStack: %v", err)
					return
				}
				stack.Close()
				stack.Close() // idempotent Close must be safe
			}
		}()
	}
	wg.Wait()
}
