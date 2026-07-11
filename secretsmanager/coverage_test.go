package secretsmanager_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

func TestSDKSecretAdmin(t *testing.T) {
	ctx := context.Background()
	c := smClient(t)

	c.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String("s1"), SecretString: aws.String("v1")})
	c.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String("s2"), SecretString: aws.String("v2")})

	// UpdateSecret.
	if _, err := c.UpdateSecret(ctx, &awssm.UpdateSecretInput{SecretId: aws.String("s1"), Description: aws.String("d")}); err != nil {
		t.Fatalf("UpdateSecret: %v", err)
	}
	// BatchGetSecretValue.
	if bg, err := c.BatchGetSecretValue(ctx, &awssm.BatchGetSecretValueInput{SecretIdList: []string{"s1", "s2"}}); err != nil || len(bg.SecretValues) != 2 {
		t.Fatalf("BatchGetSecretValue = %d err=%v", len(bg.SecretValues), err)
	}

	// Tags.
	if _, err := c.TagResource(ctx, &awssm.TagResourceInput{SecretId: aws.String("s1"), Tags: []smtypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}}}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}
	if _, err := c.UntagResource(ctx, &awssm.UntagResourceInput{SecretId: aws.String("s1"), TagKeys: []string{"k"}}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	// Resource policy round-trip.
	pol := `{"Version":"2012-10-17","Statement":[]}`
	if _, err := c.PutResourcePolicy(ctx, &awssm.PutResourcePolicyInput{SecretId: aws.String("s1"), ResourcePolicy: aws.String(pol)}); err != nil {
		t.Fatalf("PutResourcePolicy: %v", err)
	}
	if _, err := c.GetResourcePolicy(ctx, &awssm.GetResourcePolicyInput{SecretId: aws.String("s1")}); err != nil {
		t.Fatalf("GetResourcePolicy: %v", err)
	}
	if _, err := c.ValidateResourcePolicy(ctx, &awssm.ValidateResourcePolicyInput{ResourcePolicy: aws.String(pol)}); err != nil {
		t.Fatalf("ValidateResourcePolicy: %v", err)
	}
	if _, err := c.DeleteResourcePolicy(ctx, &awssm.DeleteResourcePolicyInput{SecretId: aws.String("s1")}); err != nil {
		t.Fatalf("DeleteResourcePolicy: %v", err)
	}

	// CancelRotateSecret (rotation not configured — just flips the flag off).
	if _, err := c.CancelRotateSecret(ctx, &awssm.CancelRotateSecretInput{SecretId: aws.String("s1")}); err != nil {
		t.Fatalf("CancelRotateSecret: %v", err)
	}
}
