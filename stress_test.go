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
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
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
				if err := stressOnce(ctx, s3c, ddbc, sqsc, kmsc, keyID, q.QueueUrl, id); err != nil {
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

func stressOnce(ctx context.Context, s3c *awss3.Client, ddbc *awsddb.Client, sqsc *awssqs.Client, kmsc *awskms.Client, keyID string, queueURL *string, id string) error {
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
	return nil
}
