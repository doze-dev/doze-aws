// Resource policies under enforce, continued: KMS's gate, Secrets Manager,
// Kinesis; then soft mode's record and off mode's indifference.
package iam_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
)

func kmsClient(cfg aws.Config, endpoint string) *awskms.Client {
	return awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

func smClient(cfg aws.Config, endpoint string) *awssm.Client {
	return awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

func kinesisClient(cfg aws.Config, endpoint string) *awskinesis.Client {
	return awskinesis.NewFromConfig(cfg, func(o *awskinesis.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// TestKeyPolicyGatesIdentityPolicies: KMS is the service where the resource
// policy gates the identity policies. The default key policy lets the
// account in; a key policy that names nobody in the account locks everyone
// out, root included — which is also the real lockout AWS warns about.
func TestKeyPolicyGatesIdentityPolicies(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeEnforce, "iam", "kms")
	rootKMS := kmsClient(rootCfg(), endpoint)
	crypto := kmsClient(userConfig(t, root, "crypto", allowAll), endpoint)
	guest := kmsClient(userConfig(t, root, "guest", noPolicy), endpoint)

	key, err := rootKMS.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}
	keyID := aws.ToString(key.KeyMetadata.KeyId)
	encrypt := func(c *awskms.Client) error {
		_, err := c.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: []byte("secret")})
		return err
	}
	if err := encrypt(crypto); err != nil {
		t.Fatalf("default key policy plus an identity allow: %v", err)
	}
	wantDenied(t, encrypt(guest), "default key policy admits only granted identities", "kms:Encrypt", "user/guest")

	// The key policy names the guest and nobody else: the guest is in, the
	// identity policy no longer counts, root is locked out too.
	if _, err := rootKMS.PutKeyPolicy(ctx, &awskms.PutKeyPolicyInput{KeyId: aws.String(keyID), PolicyName: aws.String("default"),
		Policy: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + userARN("guest") +
			`"},"Action":"kms:Encrypt","Resource":"*"}]}`)}); err != nil {
		t.Fatalf("PutKeyPolicy: %v", err)
	}
	if err := encrypt(guest); err != nil {
		t.Fatalf("key policy allow must admit the guest: %v", err)
	}
	wantDenied(t, encrypt(crypto), "key policy does not open the gate", "kms:Encrypt", "the resource policy does not allow it")
	wantDenied(t, encrypt(rootKMS), "root is gated too")
	_, err = rootKMS.PutKeyPolicy(ctx, &awskms.PutKeyPolicyInput{KeyId: aws.String(keyID), PolicyName: aws.String("default"), Policy: aws.String(`{"Statement":[]}`)})
	wantDenied(t, err, "the lockout is real: root cannot rewrite the policy either", "kms:PutKeyPolicy")

	// Operations that name no key are the identity policies' alone.
	if _, err := crypto.CreateKey(ctx, &awskms.CreateKeyInput{}); err != nil {
		t.Fatalf("CreateKey has no key policy to gate it: %v", err)
	}
	if _, err := crypto.ListKeys(ctx, &awskms.ListKeysInput{}); err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	_, err = guest.CreateKey(ctx, &awskms.CreateKeyInput{})
	wantDenied(t, err, "CreateKey by an ungranted identity", "kms:CreateKey")
}

// TestSecretPolicyUnderEnforce: a secret's resource policy admits an
// ungranted identity and denies a granted one.
func TestSecretPolicyUnderEnforce(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeEnforce, "iam", "secretsmanager")
	rootSM := smClient(rootCfg(), endpoint)
	sec, err := rootSM.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String("db"), SecretString: aws.String("pw")})
	if err != nil {
		t.Fatal(err)
	}
	read := func(c *awssm.Client) error {
		_, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("db")})
		return err
	}
	admin := smClient(userConfig(t, root, "admin", allowAll), endpoint)
	guest := smClient(userConfig(t, root, "guest", noPolicy), endpoint)
	if err := read(admin); err != nil {
		t.Fatalf("no resource policy: %v", err)
	}
	wantDenied(t, read(guest), "no grant anywhere", "secretsmanager:GetSecretValue", aws.ToString(sec.ARN))

	if _, err := rootSM.PutResourcePolicy(ctx, &awssm.PutResourcePolicyInput{SecretId: aws.String("db"), ResourcePolicy: aws.String(
		`{"Version":"2012-10-17","Statement":[
			{"Effect":"Allow","Principal":{"AWS":"` + userARN("guest") + `"},"Action":"secretsmanager:GetSecretValue","Resource":"*"},
			{"Sid":"NoAdmin","Effect":"Deny","Principal":{"AWS":"` + userARN("admin") + `"},"Action":"secretsmanager:GetSecretValue","Resource":"*"}]}`)}); err != nil {
		t.Fatalf("PutResourcePolicy: %v", err)
	}
	if err := read(guest); err != nil {
		t.Fatalf("resource policy allow must admit the guest: %v", err)
	}
	wantDenied(t, read(admin), "resource policy deny on a granted user", "NoAdmin")
	if _, err := admin.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("db")}); err != nil {
		t.Fatalf("the deny is on GetSecretValue only: %v", err)
	}
	_, err = guest.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("db")})
	wantDenied(t, err, "an action the resource policy does not name")

	if _, err := rootSM.DeleteResourcePolicy(ctx, &awssm.DeleteResourcePolicyInput{SecretId: aws.String("db")}); err != nil {
		t.Fatal(err)
	}
	if err := read(admin); err != nil {
		t.Fatalf("policy deleted: %v", err)
	}
	_, err = guest.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("missing")})
	wantDenied(t, err, "a secret that does not exist, by an ungranted identity", "secretsmanager:GetSecretValue")
}

// TestStreamPolicyUnderEnforce: Kinesis's resource policy, the same shape.
func TestStreamPolicyUnderEnforce(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeEnforce, "iam", "kinesis")
	rootK := kinesisClient(rootCfg(), endpoint)
	if _, err := rootK.CreateStream(ctx, &awskinesis.CreateStreamInput{StreamName: aws.String("clicks"), ShardCount: aws.Int32(1)}); err != nil {
		t.Fatal(err)
	}
	streamARN := awsident.ARN("kinesis", "stream/clicks")
	put := func(c *awskinesis.Client) error {
		_, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{StreamName: aws.String("clicks"), PartitionKey: aws.String("k"), Data: []byte("x")})
		return err
	}
	producer := kinesisClient(userConfig(t, root, "producer", allowAll), endpoint)
	guest := kinesisClient(userConfig(t, root, "guest", noPolicy), endpoint)
	if err := put(producer); err != nil {
		t.Fatalf("no resource policy: %v", err)
	}
	wantDenied(t, put(guest), "no grant anywhere", "kinesis:PutRecord", streamARN)

	if _, err := rootK.PutResourcePolicy(ctx, &awskinesis.PutResourcePolicyInput{ResourceARN: aws.String(streamARN), Policy: aws.String(
		`{"Version":"2012-10-17","Statement":[
			{"Effect":"Allow","Principal":{"AWS":"` + userARN("guest") + `"},"Action":"kinesis:PutRecord","Resource":"` + streamARN + `"},
			{"Sid":"NoProducer","Effect":"Deny","Principal":{"AWS":"` + userARN("producer") + `"},"Action":"kinesis:PutRecord","Resource":"*"}]}`)}); err != nil {
		t.Fatalf("PutResourcePolicy: %v", err)
	}
	if err := put(guest); err != nil {
		t.Fatalf("resource policy allow must admit the guest: %v", err)
	}
	wantDenied(t, put(producer), "resource policy deny on a granted user", "NoProducer")
	if _, err := producer.DescribeStreamSummary(ctx, &awskinesis.DescribeStreamSummaryInput{StreamName: aws.String("clicks")}); err != nil {
		t.Fatalf("the deny is on PutRecord only: %v", err)
	}
	if _, err := rootK.DeleteResourcePolicy(ctx, &awskinesis.DeleteResourcePolicyInput{ResourceARN: aws.String(streamARN)}); err != nil {
		t.Fatal(err)
	}
	if err := put(producer); err != nil {
		t.Fatalf("policy deleted: %v", err)
	}
	_, err := guest.CreateStream(ctx, &awskinesis.CreateStreamInput{StreamName: aws.String("more"), ShardCount: aws.Int32(1)})
	wantDenied(t, err, "CreateStream by an ungranted identity", "kinesis:CreateStream")
}

// TestSoftModeRecordsResourceVerdicts: under soft nothing is blocked, and
// the access log carries the resource policy's verdict with its source.
func TestSoftModeRecordsResourceVerdicts(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeSoft, "iam", "s3")
	rootS3 := s3Client(rootCfg(), endpoint)
	rootS3.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("docs")})
	rootS3.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("docs"), Key: aws.String("a.txt"), Body: strings.NewReader("a")})
	if _, err := rootS3.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{Bucket: aws.String("docs"), Policy: aws.String(
		`{"Version":"2012-10-17","Statement":[{"Sid":"NoReader","Effect":"Deny","Principal":{"AWS":"` + userARN("reader") +
			`"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::docs/*"}]}`)}); err != nil {
		t.Fatal(err)
	}
	reader := s3Client(userConfig(t, root, "reader", allowAll), endpoint)
	out, err := reader.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("docs"), Key: aws.String("a.txt")})
	if err != nil {
		t.Fatalf("soft mode must not block: %v", err)
	}
	out.Body.Close()

	log := dozeCall(t, endpoint, "DozeAccessLog", nil)
	entry := between(log, "<Principal>"+userARN("reader")+"</Principal>", "</member>")
	if entry == "" {
		t.Fatalf("no entry for the reader:\n%s", log)
	}
	for _, want := range []string{"s3:GetObject", "arn:aws:s3:::docs/a.txt", "<Decision>explicitDeny</Decision>", "<MatchedBy>NoReader</MatchedBy>", "<Source>resource</Source>"} {
		if !strings.Contains(entry, want) {
			t.Fatalf("access log entry missing %q:\n%s", want, entry)
		}
	}
}

// TestOffModeIgnoresResourcePolicies: with IAM off a bucket policy is
// stored and reported but never evaluated, as before.
func TestOffModeIgnoresResourcePolicies(t *testing.T) {
	ctx := context.Background()
	_, endpoint := stackWith(t, iam.ModeOff, "iam", "s3")
	c := s3Client(rootCfg(), endpoint)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("docs")})
	if _, err := c.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{Bucket: aws.String("docs"), Policy: aws.String(
		`{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Principal":{"AWS":"` + awsident.GlobalARN("iam", "root") +
			`"},"Action":"s3:*","Resource":["arn:aws:s3:::docs","arn:aws:s3:::docs/*"]}]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("docs"), Key: aws.String("a.txt"), Body: strings.NewReader("a")}); err != nil {
		t.Fatalf("off mode must not evaluate the policy: %v", err)
	}
	pol, err := c.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{Bucket: aws.String("docs")})
	if err != nil || !strings.Contains(aws.ToString(pol.Policy), "Deny") {
		t.Fatalf("the policy is still stored: %v %v", err, pol)
	}
}
