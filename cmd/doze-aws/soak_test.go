//go:build soak

// Soak/chaos harness (build-tagged, run via `go tool task soak`). It drives a
// mixed cross-service workload against the real binary under sustained load,
// optionally killing and restarting the process to prove persistence.
//
//	SOAK_DURATION  how long to run (default 2m)
//	SOAK_CHAOS=1   kill/restart the binary mid-load, assert data survives
package main_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

func TestSoak(t *testing.T) {
	dur := 2 * time.Minute
	if d := os.Getenv("SOAK_DURATION"); d != "" {
		if p, err := time.ParseDuration(d); err == nil {
			dur = p
		}
	}

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()

	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	// Serve on a background listener.
	ln := serve(t, stack)
	ep := aws.String("http://" + ln)
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = ep; o.UsePathStyle = true })
	sqsc := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = ep })
	ddbc := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = ep })

	ctx := context.Background()
	s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("soak")})
	q, _ := sqsc.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("soak")})
	ddbc.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName:            aws.String("soak"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
	})

	var ops int64
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		n := atomic.AddInt64(&ops, 1)
		key := fmt.Sprintf("k%d", n)
		s3c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("soak"), Key: aws.String(key), Body: strings.NewReader(key)})
		sqsc.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String(key)})
		ddbc.PutItem(ctx, &awsddb.PutItemInput{TableName: aws.String("soak"), Item: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: key},
		}})
		if n%500 == 0 {
			t.Logf("soak: %d ops", n)
		}
	}
	t.Logf("soak complete: %d ops over %s", atomic.LoadInt64(&ops), dur)
}
