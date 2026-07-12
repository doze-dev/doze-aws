// SDK contract tests: a real aws-sdk-go-v2 KMS client driving every key
// family — including verifying a KMS signature OUTSIDE KMS with the public key
// GetPublicKey returns, which proves the crypto is real.
package kms_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/kms"
)

func kmsClient(t *testing.T) *awskms.Client {
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
	return awskms.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awskms.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func TestSDKSymmetricRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{Description: aws.String("app key")})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	enc, err := c.Encrypt(ctx, &awskms.EncryptInput{
		KeyId:             aws.String(keyID),
		Plaintext:         []byte("secret payload"),
		EncryptionContext: map[string]string{"app": "doze", "env": "dev"},
	})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Decrypt needs no KeyId — the blob carries it, like real KMS.
	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{
		CiphertextBlob:    enc.CiphertextBlob,
		EncryptionContext: map[string]string{"app": "doze", "env": "dev"},
	})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(dec.Plaintext) != "secret payload" {
		t.Fatalf("round trip = %q", dec.Plaintext)
	}

	// Wrong encryption context must fail (it is authenticated data).
	if _, err := c.Decrypt(ctx, &awskms.DecryptInput{
		CiphertextBlob:    enc.CiphertextBlob,
		EncryptionContext: map[string]string{"app": "other"},
	}); err == nil {
		t.Fatal("Decrypt with wrong context succeeded")
	}
}

func TestSDKDataKeyEnvelope(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	dk, err := c.GenerateDataKey(ctx, &awskms.GenerateDataKeyInput{
		KeyId: key.KeyMetadata.KeyId, KeySpec: kmstypes.DataKeySpecAes256,
	})
	if err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}
	if len(dk.Plaintext) != 32 {
		t.Fatalf("data key length = %d", len(dk.Plaintext))
	}
	// The ciphertext blob decrypts back to the same data key.
	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: dk.CiphertextBlob})
	if err != nil || string(dec.Plaintext) != string(dk.Plaintext) {
		t.Fatalf("envelope decrypt: %v", err)
	}
}

func TestSDKAsymmetricSignVerify(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecEccNistP256, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		t.Fatalf("CreateKey ECC: %v", err)
	}
	msg := []byte("sign me")
	sig, err := c.Sign(ctx, &awskms.SignInput{
		KeyId: key.KeyMetadata.KeyId, Message: msg,
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecEcdsaSha256,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Verify inside KMS.
	ver, err := c.Verify(ctx, &awskms.VerifyInput{
		KeyId: key.KeyMetadata.KeyId, Message: msg, Signature: sig.Signature,
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecEcdsaSha256,
	})
	if err != nil || !ver.SignatureValid {
		t.Fatalf("Verify: %v valid=%v", err, ver != nil && ver.SignatureValid)
	}

	// Verify OUTSIDE KMS with the exported public key — the crypto is real.
	pub, err := c.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: key.KeyMetadata.KeyId})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	parsed, err := x509.ParsePKIXPublicKey(pub.PublicKey)
	if err != nil {
		t.Fatalf("public key is not valid SPKI DER: %v", err)
	}
	ecPub, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("public key type = %T", parsed)
	}
	digest := sha256.Sum256(msg)
	if !ecdsa.VerifyASN1(ecPub, digest[:], sig.Signature) {
		t.Fatal("signature does not verify outside KMS")
	}

	// A tampered signature is rejected with the KMS error code.
	bad := append([]byte(nil), sig.Signature...)
	bad[len(bad)-1] ^= 0xff
	if _, err := c.Verify(ctx, &awskms.VerifyInput{
		KeyId: key.KeyMetadata.KeyId, Message: msg, Signature: bad,
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecEcdsaSha256,
	}); err == nil {
		t.Fatal("tampered signature verified")
	}
}

func TestSDKRSAEncryptAndPSS(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)

	// RSA encryption key.
	encKey, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecRsa2048, KeyUsage: kmstypes.KeyUsageTypeEncryptDecrypt,
	})
	if err != nil {
		t.Fatalf("CreateKey RSA: %v", err)
	}
	enc, err := c.Encrypt(ctx, &awskms.EncryptInput{
		KeyId: encKey.KeyMetadata.KeyId, Plaintext: []byte("rsa payload"),
		EncryptionAlgorithm: kmstypes.EncryptionAlgorithmSpecRsaesOaepSha256,
	})
	if err != nil {
		t.Fatalf("RSA Encrypt: %v", err)
	}
	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{
		KeyId: encKey.KeyMetadata.KeyId, CiphertextBlob: enc.CiphertextBlob,
		EncryptionAlgorithm: kmstypes.EncryptionAlgorithmSpecRsaesOaepSha256,
	})
	if err != nil || string(dec.Plaintext) != "rsa payload" {
		t.Fatalf("RSA Decrypt: %v %q", err, dec.Plaintext)
	}

	// RSA signing key with PSS.
	signKey, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecRsa2048, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	sig, err := c.Sign(ctx, &awskms.SignInput{
		KeyId: signKey.KeyMetadata.KeyId, Message: []byte("pss me"),
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPssSha256,
	})
	if err != nil {
		t.Fatalf("RSA Sign: %v", err)
	}
	ver, err := c.Verify(ctx, &awskms.VerifyInput{
		KeyId: signKey.KeyMetadata.KeyId, Message: []byte("pss me"), Signature: sig.Signature,
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPssSha256,
	})
	if err != nil || !ver.SignatureValid {
		t.Fatalf("RSA Verify: %v", err)
	}
}

// TestKeyUsageEnforced proves a key can't be used for the wrong operation:
// signing with an ENCRYPT_DECRYPT key and encrypting with a SIGN_VERIFY key both
// fail, as they do against real KMS (InvalidKeyUsageException).
func TestKeyUsageEnforced(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)

	encKey, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecRsa2048, KeyUsage: kmstypes.KeyUsageTypeEncryptDecrypt,
	})
	if _, err := c.Sign(ctx, &awskms.SignInput{
		KeyId: encKey.KeyMetadata.KeyId, Message: []byte("m"),
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPssSha256,
	}); err == nil {
		t.Fatal("Sign with an ENCRYPT_DECRYPT key should fail")
	}

	signKey, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecRsa2048, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if _, err := c.Encrypt(ctx, &awskms.EncryptInput{
		KeyId: signKey.KeyMetadata.KeyId, Plaintext: []byte("m"),
		EncryptionAlgorithm: kmstypes.EncryptionAlgorithmSpecRsaesOaepSha256,
	}); err == nil {
		t.Fatal("Encrypt with a SIGN_VERIFY key should fail")
	}
}

func TestSDKHMAC(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecHmac256, KeyUsage: kmstypes.KeyUsageTypeGenerateVerifyMac,
	})
	if err != nil {
		t.Fatalf("CreateKey HMAC: %v", err)
	}
	mac, err := c.GenerateMac(ctx, &awskms.GenerateMacInput{
		KeyId: key.KeyMetadata.KeyId, Message: []byte("mac me"),
		MacAlgorithm: kmstypes.MacAlgorithmSpecHmacSha256,
	})
	if err != nil {
		t.Fatalf("GenerateMac: %v", err)
	}
	ver, err := c.VerifyMac(ctx, &awskms.VerifyMacInput{
		KeyId: key.KeyMetadata.KeyId, Message: []byte("mac me"), Mac: mac.Mac,
		MacAlgorithm: kmstypes.MacAlgorithmSpecHmacSha256,
	})
	if err != nil || !ver.MacValid {
		t.Fatalf("VerifyMac: %v", err)
	}
	if _, err := c.VerifyMac(ctx, &awskms.VerifyMacInput{
		KeyId: key.KeyMetadata.KeyId, Message: []byte("tampered"), Mac: mac.Mac,
		MacAlgorithm: kmstypes.MacAlgorithmSpecHmacSha256,
	}); err == nil {
		t.Fatal("tampered MAC verified")
	}
}

func TestSDKAliasesAndLifecycle(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	if _, err := c.CreateAlias(ctx, &awskms.CreateAliasInput{
		AliasName: aws.String("alias/app-key"), TargetKeyId: aws.String(keyID),
	}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}
	// Encrypt by alias resolves to the key.
	if _, err := c.Encrypt(ctx, &awskms.EncryptInput{
		KeyId: aws.String("alias/app-key"), Plaintext: []byte("x"),
	}); err != nil {
		t.Fatalf("Encrypt by alias: %v", err)
	}

	// Disable blocks use with DisabledException.
	if _, err := c.DisableKey(ctx, &awskms.DisableKeyInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Encrypt(ctx, &awskms.EncryptInput{
		KeyId: aws.String(keyID), Plaintext: []byte("x"),
	}); err == nil {
		t.Fatal("Encrypt with disabled key succeeded")
	}
	if _, err := c.EnableKey(ctx, &awskms.EnableKeyInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatal(err)
	}

	// Schedule deletion; state flips and use is blocked; cancel restores Disabled.
	del, err := c.ScheduleKeyDeletion(ctx, &awskms.ScheduleKeyDeletionInput{
		KeyId: aws.String(keyID), PendingWindowInDays: aws.Int32(7),
	})
	if err != nil || del.KeyState != kmstypes.KeyStatePendingDeletion {
		t.Fatalf("ScheduleKeyDeletion: %v %v", err, del.KeyState)
	}
	if _, err := c.CancelKeyDeletion(ctx, &awskms.CancelKeyDeletionInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("CancelKeyDeletion: %v", err)
	}
	desc, _ := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if desc.KeyMetadata.KeyState != kmstypes.KeyStateDisabled {
		t.Errorf("state after cancel = %v, want Disabled", desc.KeyMetadata.KeyState)
	}

	// secp256k1 answers honestly.
	if _, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecEccSecgP256k1, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	}); err == nil {
		t.Fatal("secp256k1 key creation should fail honestly")
	}
}

func TestSDKRotateKeyOnDemand(t *testing.T) {
	ctx := context.Background()
	c := kmsClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{Description: aws.String("rot")})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	// Encrypt with the original material.
	enc, err := c.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: []byte("before rotation")})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Rotate on demand.
	if _, err := c.RotateKeyOnDemand(ctx, &awskms.RotateKeyOnDemandInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("RotateKeyOnDemand: %v", err)
	}

	// The pre-rotation ciphertext must still decrypt (old material is retained).
	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc.CiphertextBlob})
	if err != nil {
		t.Fatalf("Decrypt after rotation: %v", err)
	}
	if string(dec.Plaintext) != "before rotation" {
		t.Fatalf("decrypted %q, want 'before rotation'", dec.Plaintext)
	}

	// A fresh encrypt (new material) also round-trips.
	enc2, err := c.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: []byte("after rotation")})
	if err != nil {
		t.Fatalf("Encrypt after rotation: %v", err)
	}
	dec2, _ := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc2.CiphertextBlob})
	if string(dec2.Plaintext) != "after rotation" {
		t.Fatalf("post-rotation round-trip failed: %q", dec2.Plaintext)
	}

	// ListKeyRotations reports the rotation.
	rots, err := c.ListKeyRotations(ctx, &awskms.ListKeyRotationsInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("ListKeyRotations: %v", err)
	}
	if len(rots.Rotations) != 1 {
		t.Fatalf("Rotations = %d, want 1", len(rots.Rotations))
	}
}
