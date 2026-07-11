package kms_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

func TestSDKKMSAdmin(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)
	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{Description: aws.String("d")})
	id := aws.ToString(key.KeyMetadata.KeyId)

	if _, err := c.ListKeys(ctx, &awskms.ListKeysInput{}); err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if _, err := c.UpdateKeyDescription(ctx, &awskms.UpdateKeyDescriptionInput{KeyId: aws.String(id), Description: aws.String("d2")}); err != nil {
		t.Fatalf("UpdateKeyDescription: %v", err)
	}
	if _, err := c.GenerateRandom(ctx, &awskms.GenerateRandomInput{NumberOfBytes: aws.Int32(16)}); err != nil {
		t.Fatalf("GenerateRandom: %v", err)
	}

	// Data keys.
	dk, err := c.GenerateDataKey(ctx, &awskms.GenerateDataKeyInput{KeyId: aws.String(id), KeySpec: kmstypes.DataKeySpecAes256})
	if err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}
	if _, err := c.GenerateDataKeyWithoutPlaintext(ctx, &awskms.GenerateDataKeyWithoutPlaintextInput{KeyId: aws.String(id), KeySpec: kmstypes.DataKeySpecAes256}); err != nil {
		t.Fatalf("GenerateDataKeyWithoutPlaintext: %v", err)
	}

	// ReEncrypt the data-key ciphertext under the same key.
	if _, err := c.ReEncrypt(ctx, &awskms.ReEncryptInput{CiphertextBlob: dk.CiphertextBlob, DestinationKeyId: aws.String(id)}); err != nil {
		t.Fatalf("ReEncrypt: %v", err)
	}

	// Aliases.
	if _, err := c.CreateAlias(ctx, &awskms.CreateAliasInput{AliasName: aws.String("alias/app"), TargetKeyId: aws.String(id)}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}
	key2, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if _, err := c.UpdateAlias(ctx, &awskms.UpdateAliasInput{AliasName: aws.String("alias/app"), TargetKeyId: key2.KeyMetadata.KeyId}); err != nil {
		t.Fatalf("UpdateAlias: %v", err)
	}
	if _, err := c.ListAliases(ctx, &awskms.ListAliasesInput{}); err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if _, err := c.DeleteAlias(ctx, &awskms.DeleteAliasInput{AliasName: aws.String("alias/app")}); err != nil {
		t.Fatalf("DeleteAlias: %v", err)
	}

	// Tags.
	if _, err := c.TagResource(ctx, &awskms.TagResourceInput{KeyId: aws.String(id), Tags: []kmstypes.Tag{{TagKey: aws.String("t"), TagValue: aws.String("v")}}}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}
}
