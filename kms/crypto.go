package kms

// Key-family crypto: spec/usage validation, material generation, and the
// asymmetric + HMAC primitives. Everything is Go standard library — real
// RSA/ECDSA/HMAC, no shims.

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"hash"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Key usages.
const (
	usageEncryptDecrypt    = "ENCRYPT_DECRYPT"
	usageSignVerify        = "SIGN_VERIFY"
	usageGenerateVerifyMac = "GENERATE_VERIFY_MAC"
)

// specInfo describes one supported KeySpec.
type specInfo struct {
	usages     []string // allowed usages; first is the default
	rsaBits    int
	curve      elliptic.Curve
	hmacBytes  int
	signingAlg map[string][]string // usage -> algorithm list advertised by DescribeKey/GetPublicKey
}

var specs = map[string]specInfo{
	"SYMMETRIC_DEFAULT": {usages: []string{usageEncryptDecrypt}},
	"RSA_2048":          {usages: []string{usageEncryptDecrypt, usageSignVerify}, rsaBits: 2048},
	"RSA_3072":          {usages: []string{usageEncryptDecrypt, usageSignVerify}, rsaBits: 3072},
	"RSA_4096":          {usages: []string{usageEncryptDecrypt, usageSignVerify}, rsaBits: 4096},
	"ECC_NIST_P256":     {usages: []string{usageSignVerify}, curve: elliptic.P256()},
	"ECC_NIST_P384":     {usages: []string{usageSignVerify}, curve: elliptic.P384()},
	"ECC_NIST_P521":     {usages: []string{usageSignVerify}, curve: elliptic.P521()},
	"HMAC_224":          {usages: []string{usageGenerateVerifyMac}, hmacBytes: 28},
	"HMAC_256":          {usages: []string{usageGenerateVerifyMac}, hmacBytes: 32},
	"HMAC_384":          {usages: []string{usageGenerateVerifyMac}, hmacBytes: 48},
	"HMAC_512":          {usages: []string{usageGenerateVerifyMac}, hmacBytes: 64},
}

func defaultUsageFor(spec string) string {
	if si, ok := specs[spec]; ok {
		return si.usages[0]
	}
	return usageEncryptDecrypt
}

func validateSpecUsage(spec, usage string) *awshttp.APIError {
	if spec == "ECC_SECG_P256K1" {
		return awshttp.Errf(400, "UnsupportedOperationException",
			"ECC_SECG_P256K1 (secp256k1) is not supported by doze-aws: Go's standard library has no secp256k1 implementation")
	}
	si, ok := specs[spec]
	if !ok {
		return awshttp.Errf(400, "ValidationException", "unsupported KeySpec %q", spec)
	}
	for _, u := range si.usages {
		if u == usage {
			return nil
		}
	}
	return awshttp.Errf(400, "ValidationException", "KeyUsage %q is not valid for KeySpec %q", usage, spec)
}

// generateMaterial mints the key material for a spec: raw bytes for
// symmetric/HMAC, PKCS#8 DER for RSA/ECC.
func generateMaterial(spec string) ([]byte, error) {
	si := specs[spec]
	switch {
	case si.rsaBits > 0:
		priv, err := rsa.GenerateKey(rand.Reader, si.rsaBits)
		if err != nil {
			return nil, err
		}
		return x509.MarshalPKCS8PrivateKey(priv)
	case si.curve != nil:
		priv, err := ecdsa.GenerateKey(si.curve, rand.Reader)
		if err != nil {
			return nil, err
		}
		return x509.MarshalPKCS8PrivateKey(priv)
	case si.hmacBytes > 0:
		b := make([]byte, si.hmacBytes)
		rand.Read(b)
		return b, nil
	default: // SYMMETRIC_DEFAULT
		b := make([]byte, 32)
		rand.Read(b)
		return b, nil
	}
}

// privateKey parses an asymmetric key's PKCS#8 material.
func (k *Key) privateKey() (any, *awshttp.APIError) {
	priv, err := x509.ParsePKCS8PrivateKey(k.Material)
	if err != nil {
		return nil, awshttp.Errf(500, "KMSInternalException", "stored key material is corrupt")
	}
	return priv, nil
}

// publicKeyDER returns the DER-encoded SPKI public key for RSA/ECC keys.
func (k *Key) publicKeyDER() ([]byte, *awshttp.APIError) {
	priv, aerr := k.privateKey()
	if aerr != nil {
		return nil, aerr
	}
	type puber interface{ Public() crypto.PublicKey }
	p, ok := priv.(puber)
	if !ok {
		return nil, awshttp.Errf(400, "ValidationException", "key %s has no public key", k.ID)
	}
	der, err := x509.MarshalPKIXPublicKey(p.Public())
	if err != nil {
		return nil, awshttp.Errf(500, "KMSInternalException", "encode public key: %v", err)
	}
	return der, nil
}

// signingAlgorithms lists the algorithms a key advertises and accepts.
func (k *Key) signingAlgorithms() []string {
	si := specs[k.KeySpec]
	switch {
	case si.rsaBits > 0:
		return []string{
			"RSASSA_PSS_SHA_256", "RSASSA_PSS_SHA_384", "RSASSA_PSS_SHA_512",
			"RSASSA_PKCS1_V1_5_SHA_256", "RSASSA_PKCS1_V1_5_SHA_384", "RSASSA_PKCS1_V1_5_SHA_512",
		}
	case si.curve == elliptic.P256():
		return []string{"ECDSA_SHA_256"}
	case si.curve == elliptic.P384():
		return []string{"ECDSA_SHA_384"}
	case si.curve == elliptic.P521():
		return []string{"ECDSA_SHA_512"}
	}
	return nil
}

// encryptionAlgorithms lists the encryption algorithms a key supports.
func (k *Key) encryptionAlgorithms() []string {
	if specs[k.KeySpec].rsaBits > 0 {
		return []string{"RSAES_OAEP_SHA_1", "RSAES_OAEP_SHA_256"}
	}
	if k.KeySpec == "SYMMETRIC_DEFAULT" {
		return []string{"SYMMETRIC_DEFAULT"}
	}
	return nil
}

// macAlgorithm returns the key's MAC algorithm name and hash constructor.
func (k *Key) macAlgorithm() (string, func() hash.Hash) {
	switch k.KeySpec {
	case "HMAC_224":
		return "HMAC_SHA_224", sha256.New224
	case "HMAC_256":
		return "HMAC_SHA_256", sha256.New
	case "HMAC_384":
		return "HMAC_SHA_384", sha512.New384
	case "HMAC_512":
		return "HMAC_SHA_512", sha512.New
	}
	return "", nil
}

// hashFor maps a signing/MAC algorithm suffix to its hash.
func hashFor(alg string) (crypto.Hash, *awshttp.APIError) {
	switch {
	case len(alg) >= 3 && alg[len(alg)-3:] == "256":
		return crypto.SHA256, nil
	case len(alg) >= 3 && alg[len(alg)-3:] == "384":
		return crypto.SHA384, nil
	case len(alg) >= 3 && alg[len(alg)-3:] == "512":
		return crypto.SHA512, nil
	}
	return 0, awshttp.Errf(400, "ValidationException", "unsupported algorithm %q", alg)
}

// digest hashes a RAW message, or validates a caller-provided DIGEST's length.
func digest(message []byte, messageType string, h crypto.Hash) ([]byte, *awshttp.APIError) {
	switch messageType {
	case "", "RAW":
		hh := h.New()
		hh.Write(message)
		return hh.Sum(nil), nil
	case "DIGEST":
		if len(message) != h.Size() {
			return nil, awshttp.Errf(400, "ValidationException",
				"DIGEST message must be %d bytes for this algorithm, got %d", h.Size(), len(message))
		}
		return message, nil
	}
	return nil, awshttp.Errf(400, "ValidationException", "unsupported MessageType %q", messageType)
}

// sign produces a signature with the named KMS algorithm.
func (k *Key) sign(alg string, message []byte, messageType string) ([]byte, *awshttp.APIError) {
	if !contains(k.signingAlgorithms(), alg) {
		return nil, awshttp.Errf(400, "ValidationException", "algorithm %q is not valid for key spec %s", alg, k.KeySpec)
	}
	h, aerr := hashFor(alg)
	if aerr != nil {
		return nil, aerr
	}
	dig, aerr := digest(message, messageType, h)
	if aerr != nil {
		return nil, aerr
	}
	priv, aerr := k.privateKey()
	if aerr != nil {
		return nil, aerr
	}
	switch p := priv.(type) {
	case *rsa.PrivateKey:
		var sig []byte
		var err error
		if len(alg) > 10 && alg[:10] == "RSASSA_PSS" {
			sig, err = rsa.SignPSS(rand.Reader, p, h, dig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		} else {
			sig, err = rsa.SignPKCS1v15(rand.Reader, p, h, dig)
		}
		if err != nil {
			return nil, awshttp.Errf(500, "KMSInternalException", "sign: %v", err)
		}
		return sig, nil
	case *ecdsa.PrivateKey:
		sig, err := ecdsa.SignASN1(rand.Reader, p, dig)
		if err != nil {
			return nil, awshttp.Errf(500, "KMSInternalException", "sign: %v", err)
		}
		return sig, nil
	}
	return nil, awshttp.Errf(400, "ValidationException", "key %s cannot sign", k.ID)
}

// verify checks a signature; a mismatch is (false, nil) like real KMS's
// KMSInvalidSignatureException path handled by the caller.
func (k *Key) verify(alg string, message []byte, messageType string, sig []byte) (bool, *awshttp.APIError) {
	if !contains(k.signingAlgorithms(), alg) {
		return false, awshttp.Errf(400, "ValidationException", "algorithm %q is not valid for key spec %s", alg, k.KeySpec)
	}
	h, aerr := hashFor(alg)
	if aerr != nil {
		return false, aerr
	}
	dig, aerr := digest(message, messageType, h)
	if aerr != nil {
		return false, aerr
	}
	priv, aerr := k.privateKey()
	if aerr != nil {
		return false, aerr
	}
	switch p := priv.(type) {
	case *rsa.PrivateKey:
		var err error
		if len(alg) > 10 && alg[:10] == "RSASSA_PSS" {
			err = rsa.VerifyPSS(&p.PublicKey, h, dig, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		} else {
			err = rsa.VerifyPKCS1v15(&p.PublicKey, h, dig, sig)
		}
		return err == nil, nil
	case *ecdsa.PrivateKey:
		return ecdsa.VerifyASN1(&p.PublicKey, dig, sig), nil
	}
	return false, awshttp.Errf(400, "ValidationException", "key %s cannot verify", k.ID)
}

// rsaEncrypt encrypts with RSAES_OAEP_SHA_1 or _SHA_256.
func (k *Key) rsaEncrypt(alg string, plaintext []byte) ([]byte, *awshttp.APIError) {
	priv, aerr := k.privateKey()
	if aerr != nil {
		return nil, aerr
	}
	p, ok := priv.(*rsa.PrivateKey)
	if !ok {
		return nil, awshttp.Errf(400, "ValidationException", "key %s is not an RSA key", k.ID)
	}
	h, aerr := oaepHash(alg)
	if aerr != nil {
		return nil, aerr
	}
	ct, err := rsa.EncryptOAEP(h, rand.Reader, &p.PublicKey, plaintext, nil)
	if err != nil {
		return nil, awshttp.Errf(400, "ValidationException", "RSA encrypt: %v", err)
	}
	return ct, nil
}

// rsaDecrypt decrypts an RSAES_OAEP ciphertext.
func (k *Key) rsaDecrypt(alg string, ciphertext []byte) ([]byte, *awshttp.APIError) {
	priv, aerr := k.privateKey()
	if aerr != nil {
		return nil, aerr
	}
	p, ok := priv.(*rsa.PrivateKey)
	if !ok {
		return nil, awshttp.Errf(400, "ValidationException", "key %s is not an RSA key", k.ID)
	}
	h, aerr := oaepHash(alg)
	if aerr != nil {
		return nil, aerr
	}
	pt, err := rsa.DecryptOAEP(h, rand.Reader, p, ciphertext, nil)
	if err != nil {
		return nil, awshttp.Errf(400, "InvalidCiphertextException", "decryption failed (wrong key or algorithm)")
	}
	return pt, nil
}

func oaepHash(alg string) (hash.Hash, *awshttp.APIError) {
	switch alg {
	case "RSAES_OAEP_SHA_1":
		return sha1.New(), nil
	case "RSAES_OAEP_SHA_256":
		return sha256.New(), nil
	}
	return nil, awshttp.Errf(400, "ValidationException", "unsupported EncryptionAlgorithm %q for an RSA key", alg)
}

// mac computes the key's HMAC over message with the named algorithm.
func (k *Key) mac(alg string, message []byte) ([]byte, *awshttp.APIError) {
	wantAlg, newHash := k.macAlgorithm()
	if newHash == nil {
		return nil, awshttp.Errf(400, "ValidationException", "key %s is not an HMAC key", k.ID)
	}
	if alg != wantAlg {
		return nil, awshttp.Errf(400, "ValidationException", "MacAlgorithm %q is not valid for key spec %s", alg, k.KeySpec)
	}
	m := hmac.New(newHash, k.Material)
	m.Write(message)
	return m.Sum(nil), nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
