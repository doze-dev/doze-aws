// SDK v1 contract tests: the legacy aws-sdk-go (v1) KMS client.
package kms_test

import (
	"net/http/httptest"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	kmsv1 "github.com/aws/aws-sdk-go/service/kms"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/kms"
)

func kmsV1Client(t *testing.T) *kmsv1.KMS {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	s, err := kms.New(kms.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(ts.URL).
		WithCredentials(credsv1.NewStaticCredentials(awsident.AccessKeyID, awsident.SecretAccessKey, "")))
	if err != nil {
		t.Fatal(err)
	}
	return kmsv1.New(sess)
}

func TestSDKV1EncryptDecrypt(t *testing.T) {
	c := kmsV1Client(t)

	key, err := c.CreateKey(&kmsv1.CreateKeyInput{Description: awsv1.String("v1 key")})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	enc, err := c.Encrypt(&kmsv1.EncryptInput{
		KeyId: key.KeyMetadata.KeyId, Plaintext: []byte("v1 secret"),
	})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	dec, err := c.Decrypt(&kmsv1.DecryptInput{CiphertextBlob: enc.CiphertextBlob})
	if err != nil || string(dec.Plaintext) != "v1 secret" {
		t.Fatalf("Decrypt: %v %q", err, dec.Plaintext)
	}
	if awsv1.StringValue(dec.KeyId) == "" {
		t.Error("Decrypt did not report the key id")
	}
}

func TestSDKV1ErrorCode(t *testing.T) {
	c := kmsV1Client(t)
	_, err := c.DescribeKey(&kmsv1.DescribeKeyInput{KeyId: awsv1.String("no-such-key")})
	if err == nil {
		t.Fatal("want NotFoundException")
	}
	type coder interface{ Code() string }
	if ce, ok := err.(coder); !ok || ce.Code() != "NotFoundException" {
		t.Errorf("error = %v", err)
	}
}
