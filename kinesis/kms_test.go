package kinesis_test

// A stream encrypted with a customer key depends on that key staying usable.
// AWS refuses the write rather than storing records it could not encrypt, and
// it distinguishes a missing key from a switched-off one because each needs a
// different fix. These tests pin that behaviour against a real local KMS.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	ktypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/kinesis"
	"github.com/doze-dev/doze-aws/kms"
	"github.com/doze-dev/doze-aws/peers"
)

// encryptedPair builds a Kinesis wired to a real KMS, and returns clients for
// both plus the id of an enabled key.
func encryptedPair(t *testing.T) (*awskinesis.Client, *awskms.Client, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	km, err := kms.New(kms.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { km.Close() })

	dir := peers.InProcess(func(service string) http.Handler {
		if service == "kms" {
			return km
		}
		return nil
	})
	ks, err := kinesis.New(kinesis.Options{DataDir: t.TempDir(), Logf: t.Logf, Peers: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ks.Close() })

	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	kts := httptest.NewServer(ks)
	t.Cleanup(kts.Close)
	mts := httptest.NewServer(km)
	t.Cleanup(mts.Close)

	kc := awskinesis.NewFromConfig(cfg, func(o *awskinesis.Options) { o.BaseEndpoint = aws.String(kts.URL) })
	mc := awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = aws.String(mts.URL) })

	key, err := mc.CreateKey(context.Background(), &awskms.CreateKeyInput{
		Description: aws.String("kinesis at-rest"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return kc, mc, aws.ToString(key.KeyMetadata.KeyId)
}

func TestEncryptionRejectsUnknownKey(t *testing.T) {
	ctx := context.Background()
	c, _, _ := encryptedPair(t)
	mustCreate(t, c, "enc-unknown", 1)

	_, err := c.StartStreamEncryption(ctx, &awskinesis.StartStreamEncryptionInput{
		StreamName: aws.String("enc-unknown"), EncryptionType: ktypes.EncryptionTypeKms,
		KeyId: aws.String("no-such-key-anywhere"),
	})
	assertCode(t, err, "KMSNotFoundException")

	// The stream must be left alone: a refused request does not half-apply.
	d, err := c.DescribeStreamSummary(ctx, &awskinesis.DescribeStreamSummaryInput{
		StreamName: aws.String("enc-unknown"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.StreamDescriptionSummary.EncryptionType; got != ktypes.EncryptionTypeNone {
		t.Fatalf("encryption type is %q after a refused request, want NONE", got)
	}
}

func TestDisabledKeyStopsProducersAndConsumers(t *testing.T) {
	ctx := context.Background()
	c, mc, key := encryptedPair(t)
	mustCreate(t, c, "enc-live", 1)

	if _, err := c.StartStreamEncryption(ctx, &awskinesis.StartStreamEncryptionInput{
		StreamName: aws.String("enc-live"), EncryptionType: ktypes.EncryptionTypeKms,
		KeyId: aws.String(key),
	}); err != nil {
		t.Fatalf("StartStreamEncryption with a good key: %v", err)
	}

	put := func() error {
		_, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
			StreamName: aws.String("enc-live"), PartitionKey: aws.String("k"),
			Data: []byte("payload"),
		})
		return err
	}
	if err := put(); err != nil {
		t.Fatalf("put under an enabled key: %v", err)
	}

	// Switch the key off underneath the stream. A producer has to hear about
	// it rather than keep writing records nothing could later read.
	if _, err := mc.DisableKey(ctx, &awskms.DisableKeyInput{KeyId: aws.String(key)}); err != nil {
		t.Fatal(err)
	}
	assertCode(t, put(), "KMSDisabledException")

	// Reads decrypt, so they fail the same way.
	it, err := c.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName: aws.String("enc-live"), ShardId: aws.String("shardId-000000000000"),
		ShardIteratorType: ktypes.ShardIteratorTypeTrimHorizon,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator})
	assertCode(t, err, "KMSDisabledException")

	// Re-enabling restores both.
	if _, err := mc.EnableKey(ctx, &awskms.EnableKeyInput{KeyId: aws.String(key)}); err != nil {
		t.Fatal(err)
	}
	if err := put(); err != nil {
		t.Fatalf("put after the key came back: %v", err)
	}
}

func TestKeyPendingDeletionIsInvalidState(t *testing.T) {
	ctx := context.Background()
	c, mc, key := encryptedPair(t)
	mustCreate(t, c, "enc-doomed", 1)

	if _, err := c.StartStreamEncryption(ctx, &awskinesis.StartStreamEncryptionInput{
		StreamName: aws.String("enc-doomed"), EncryptionType: ktypes.EncryptionTypeKms,
		KeyId: aws.String(key),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := mc.ScheduleKeyDeletion(ctx, &awskms.ScheduleKeyDeletionInput{
		KeyId: aws.String(key), PendingWindowInDays: aws.Int32(7),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName: aws.String("enc-doomed"), PartitionKey: aws.String("k"), Data: []byte("x"),
	})
	assertCode(t, err, "KMSInvalidStateException")
}

// A stream with no customer key is unaffected by any of this, and a stack
// assembled without KMS wired keeps working rather than refusing writes.
func TestUnencryptedStreamNeedsNoKMS(t *testing.T) {
	ctx := context.Background()
	c := client(t) // built with no peers at all
	mustCreate(t, c, "plain", 1)
	if _, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName: aws.String("plain"), PartitionKey: aws.String("k"), Data: []byte("x"),
	}); err != nil {
		t.Fatalf("put on an unencrypted stream with no KMS wired: %v", err)
	}
}
