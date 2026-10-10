package kms_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/smithy-go"
)

// Plaintext may be 4096 BYTES. On the wire it is base64, so that is 5,464
// characters; the check used to measure the text and refused anything over
// 3,072 bytes that AWS accepts. The SDK sends the bytes it is given, so this
// goes through the same path a caller's does.
func TestEncryptAcceptsTheFullFourKilobytes(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)
	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}
	id := aws.ToString(key.KeyMetadata.KeyId)

	for _, n := range []int{3072, 3073, 4096} {
		plain := bytes.Repeat([]byte{0x5a}, n)
		enc, err := c.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(id), Plaintext: plain})
		if err != nil {
			t.Fatalf("Encrypt of %d bytes was refused: %v", n, err)
		}
		dec, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc.CiphertextBlob})
		if err != nil || !bytes.Equal(dec.Plaintext, plain) {
			t.Fatalf("%d bytes did not round-trip: %v", n, err)
		}
	}

	// One byte more is over the limit, and refused as such.
	_, err = c.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(id), Plaintext: make([]byte, 4097)})
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != "ValidationException" {
		t.Fatalf("4097 bytes: err = %v, want ValidationException", err)
	}
}
