package kms_test

// The rotation period is declared and reported. Terraform tracks
// rotation_period_in_days on aws_kms_key, so a key that accepts a period and
// reports none reads as a change on every plan.

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
)

func TestRotationPeriodReadsBack(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)
	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{Description: aws.String("rot")})
	if err != nil {
		t.Fatal(err)
	}
	id := key.KeyMetadata.KeyId

	if _, err := c.EnableKeyRotation(ctx, &awskms.EnableKeyRotationInput{
		KeyId: id, RotationPeriodInDays: aws.Int32(180),
	}); err != nil {
		t.Fatalf("EnableKeyRotation: %v", err)
	}
	got, err := c.GetKeyRotationStatus(ctx, &awskms.GetKeyRotationStatusInput{KeyId: id})
	if err != nil {
		t.Fatal(err)
	}
	if !got.KeyRotationEnabled {
		t.Fatal("rotation not enabled")
	}
	if aws.ToInt32(got.RotationPeriodInDays) != 180 {
		t.Errorf("period = %v, want 180", got.RotationPeriodInDays)
	}

	// Disabling clears it rather than leaving the old period behind.
	if _, err := c.DisableKeyRotation(ctx, &awskms.DisableKeyRotationInput{KeyId: id}); err != nil {
		t.Fatal(err)
	}
	off, err := c.GetKeyRotationStatus(ctx, &awskms.GetKeyRotationStatusInput{KeyId: id})
	if err != nil {
		t.Fatal(err)
	}
	if off.KeyRotationEnabled || off.RotationPeriodInDays != nil {
		t.Errorf("after disabling: enabled=%v period=%v", off.KeyRotationEnabled, off.RotationPeriodInDays)
	}
}
