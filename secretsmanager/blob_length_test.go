package secretsmanager_test

import (
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// SecretBinary may be 65536 BYTES (the model's SecretBinaryType, length 1 to
// 65536). On the wire it is base64, which is 87,384 characters, so a check
// that measured the text would refuse anything over 49,152 bytes. KMS had
// exactly that mistake (kms/blob_length_test.go); this holds the same boundary
// here, on each operation that takes the member, through the SDK so the bytes
// travel the way a caller's do.
func TestSecretBinaryAcceptsTheFullSixtyFourKibibytes(t *testing.T) {
	ctx := context.Background()
	c := smClient(t)
	atBound := bytes.Repeat([]byte{0x5a}, 65536)
	over := make([]byte, 65537)

	// One byte over 49,152 is the first size a text-measuring check refuses.
	for _, n := range []int{1, 49152, 49153, 65535, 65536} {
		name := "blob-" + strconv.Itoa(n)
		value := bytes.Repeat([]byte{0x5a}, n)
		if _, err := c.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String(name), SecretBinary: value}); err != nil {
			t.Fatalf("CreateSecret with %d bytes was refused: %v", n, err)
		}
		got, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String(name)})
		if err != nil || !bytes.Equal(got.SecretBinary, value) {
			t.Fatalf("%d bytes did not round-trip: %d back, %v", n, len(got.SecretBinary), err)
		}
	}

	// One byte more is over, and refused as the model's length, before
	// anything is stored.
	_, err := c.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String("blob-over"), SecretBinary: over})
	assertCode(t, err, "ValidationException")
	if _, err := c.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("blob-over")}); err == nil {
		t.Errorf("a secret refused for its length exists anyway")
	}

	// The same bound on a new version, and on an update.
	if _, err := c.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String("blob-versions"), SecretBinary: []byte("v1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSecretValue(ctx, &awssm.PutSecretValueInput{SecretId: aws.String("blob-versions"), SecretBinary: atBound}); err != nil {
		t.Fatalf("PutSecretValue with 65536 bytes was refused: %v", err)
	}
	_, err = c.PutSecretValue(ctx, &awssm.PutSecretValueInput{SecretId: aws.String("blob-versions"), SecretBinary: over})
	assertCode(t, err, "ValidationException")
	if _, err := c.UpdateSecret(ctx, &awssm.UpdateSecretInput{SecretId: aws.String("blob-versions"), SecretBinary: atBound}); err != nil {
		t.Fatalf("UpdateSecret with 65536 bytes was refused: %v", err)
	}
	_, err = c.UpdateSecret(ctx, &awssm.UpdateSecretInput{SecretId: aws.String("blob-versions"), SecretBinary: over})
	assertCode(t, err, "ValidationException")

	// The refused writes changed nothing: the value is the last one accepted.
	got, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("blob-versions")})
	if err != nil || !bytes.Equal(got.SecretBinary, atBound) {
		t.Fatalf("after two refused writes the secret holds %d bytes, want the 65536 last accepted: %v", len(got.SecretBinary), err)
	}
}

// SecretString's bound is the same number and is of the string itself.
func TestSecretStringAcceptsSixtyFiveThousandFiveHundredAndThirtySix(t *testing.T) {
	ctx := context.Background()
	c := smClient(t)
	atBound := string(bytes.Repeat([]byte{'s'}, 65536))
	if _, err := c.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String("string-bound"), SecretString: aws.String(atBound)}); err != nil {
		t.Fatalf("CreateSecret with a 65536 character string was refused: %v", err)
	}
	got, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("string-bound")})
	if err != nil || aws.ToString(got.SecretString) != atBound {
		t.Fatalf("65536 characters did not round-trip: %v", err)
	}
	_, err = c.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String("string-over"), SecretString: aws.String(atBound + "s")})
	assertCode(t, err, "ValidationException")
}
