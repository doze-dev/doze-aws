package dozeaws_test

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// stackClients bundles a client per persisted service, all pointed at one
// endpoint.
type stackClients struct {
	s3  *awss3.Client
	ddb *awsddb.Client
	sqs *awssqs.Client
	sns *awssns.Client
	kms *awskms.Client
	sm  *awssm.Client
	ssm *awsssm.Client
	eb  *awseb.Client
	lam *awslambda.Client
}

func clientsFor(url string) stackClients {
	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	return stackClients{
		s3:  awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(url); o.UsePathStyle = true }),
		ddb: awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(url) }),
		sqs: awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) }),
		sns: awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(url) }),
		kms: awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = aws.String(url) }),
		sm:  awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) }),
		ssm: awsssm.NewFromConfig(cfg, func(o *awsssm.Options) { o.BaseEndpoint = aws.String(url) }),
		eb:  awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(url) }),
		lam: awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(url) }),
	}
}

// TestPersistenceAcrossRestart is the core durability guarantee: everything
// written to a Stack survives closing it and reopening a fresh Stack over the
// SAME data dir. This is the "I restarted doze and my data is still here"
// invariant — proven black-box through the real SDKs for every bbolt-backed
// service, including a KMS decrypt (key material must round-trip) and a bit of
// stored ciphertext that must still open under the reloaded key.
func TestPersistenceAcrossRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two Stacks over a shared data dir")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// ---- first boot: write one durable artifact per service ----
	stack1, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ts1 := httptest.NewServer(stack1.Handler())
	c := clientsFor(ts1.URL)

	c.s3.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("durable")})
	if _, err := c.s3.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("durable"), Key: aws.String("k"), Body: strings.NewReader("s3-body")}); err != nil {
		t.Fatalf("s3 put: %v", err)
	}

	c.ddb.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName:            aws.String("durable"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	})
	if _, err := c.ddb.PutItem(ctx, &awsddb.PutItemInput{
		TableName: aws.String("durable"),
		Item:      map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: "row1"}, "v": &ddbtypes.AttributeValueMemberS{Value: "ddb-val"}},
	}); err != nil {
		t.Fatalf("ddb put: %v", err)
	}

	q, _ := c.sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("durable")})
	c.sqs.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("sqs-msg")})

	topic, _ := c.sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("durable")})

	key, err := c.kms.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("kms createkey: %v", err)
	}
	keyID := aws.ToString(key.KeyMetadata.KeyId)
	enc, err := c.kms.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: []byte("kms-secret")})
	if err != nil {
		t.Fatalf("kms encrypt: %v", err)
	}
	blob := enc.CiphertextBlob // must still decrypt after restart

	c.sm.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String("durable"), SecretString: aws.String("sm-secret")})

	c.ssm.PutParameter(ctx, &awsssm.PutParameterInput{Name: aws.String("/durable/p"), Value: aws.String("ssm-val"), Type: "String"})

	c.eb.PutRule(ctx, &awseb.PutRuleInput{Name: aws.String("durable"), EventPattern: aws.String(`{"source":["x"]}`)})

	// Lambda: the function DEFINITION must survive (the running child does not —
	// it is re-spawned on demand). No invoke here, so no process is started.
	if _, err := c.lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("durable"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role:        aws.String("arn:aws:iam::000000000000:role/r"),
		Code:        &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(t.TempDir())},
		Environment: &lamtypes.Environment{Variables: map[string]string{"K": "lambda-env"}},
	}); err != nil {
		t.Fatalf("lambda create: %v", err)
	}

	// ---- restart: close, reopen a NEW Stack over the same dir ----
	ts1.Close()
	if err := stack1.Close(); err != nil {
		t.Fatalf("stack1 close: %v", err)
	}

	stack2, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: t.Logf})
	if err != nil {
		t.Fatalf("reopen stack: %v", err)
	}
	defer stack2.Close()
	ts2 := httptest.NewServer(stack2.Handler())
	defer ts2.Close()
	d := clientsFor(ts2.URL)

	// ---- second boot: every artifact must still be there ----
	obj, err := d.s3.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("durable"), Key: aws.String("k")})
	if err != nil {
		t.Fatalf("s3 get after restart: %v", err)
	}
	if b, _ := io.ReadAll(obj.Body); string(b) != "s3-body" {
		t.Fatalf("s3 body after restart = %q", b)
	}

	gi, err := d.ddb.GetItem(ctx, &awsddb.GetItemInput{
		TableName: aws.String("durable"),
		Key:       map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: "row1"}},
	})
	if err != nil || gi.Item == nil || gi.Item["v"].(*ddbtypes.AttributeValueMemberS).Value != "ddb-val" {
		t.Fatalf("ddb item after restart = %+v err=%v", gi.Item, err)
	}

	q2, _ := d.sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("durable")})
	rc, err := d.sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q2.QueueUrl, MaxNumberOfMessages: 1, WaitTimeSeconds: 1})
	if err != nil || len(rc.Messages) != 1 || aws.ToString(rc.Messages[0].Body) != "sqs-msg" {
		t.Fatalf("sqs msg after restart = %+v err=%v", rc.Messages, err)
	}

	lt, err := d.sns.ListTopics(ctx, &awssns.ListTopicsInput{})
	if err != nil || !containsTopic(lt.Topics, aws.ToString(topic.TopicArn)) {
		t.Fatalf("sns topic missing after restart: %+v err=%v", lt.Topics, err)
	}

	// The strongest check: decrypt ciphertext produced before the restart —
	// proves the key's backing material was persisted, not regenerated.
	dec, err := d.kms.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: blob})
	if err != nil || string(dec.Plaintext) != "kms-secret" {
		t.Fatalf("kms decrypt after restart = %q err=%v", dec.Plaintext, err)
	}

	sv, err := d.sm.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("durable")})
	if err != nil || aws.ToString(sv.SecretString) != "sm-secret" {
		t.Fatalf("secretsmanager after restart = %v err=%v", sv, err)
	}

	pv, err := d.ssm.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/durable/p")})
	if err != nil || aws.ToString(pv.Parameter.Value) != "ssm-val" {
		t.Fatalf("ssm param after restart = %v err=%v", pv, err)
	}

	rules, err := d.eb.ListRules(ctx, &awseb.ListRulesInput{NamePrefix: aws.String("durable")})
	if err != nil || len(rules.Rules) != 1 {
		t.Fatalf("eventbridge rule after restart = %+v err=%v", rules.Rules, err)
	}

	fn, err := d.lam.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("durable")})
	if err != nil || fn.Configuration == nil || fn.Configuration.Environment.Variables["K"] != "lambda-env" {
		t.Fatalf("lambda function after restart = %+v err=%v", fn.Configuration, err)
	}
}

func containsTopic(topics []snstypes.Topic, arn string) bool {
	for _, t := range topics {
		if aws.ToString(t.TopicArn) == arn {
			return true
		}
	}
	return false
}
