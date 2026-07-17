package kms

// Cryptographic actions: encrypt/decrypt/reEncrypt, data keys and data key
// pairs, random generation, sign/verify, MACs, and public key export.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// ---- encrypt / decrypt ----

func (s *Server) encrypt(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if aerr := requireUsage(k, usageEncryptDecrypt); aerr != nil {
		return nil, aerr
	}
	plaintext, aerr := awsjson.Blob(p, "Plaintext")
	if aerr != nil {
		return nil, aerr
	}
	if len(plaintext) == 0 || len(plaintext) > 4096 {
		return nil, awshttp.Errf(400, "ValidationException", "Plaintext must be 1-4096 bytes")
	}
	alg := awsjson.Str(p, "EncryptionAlgorithm")
	if alg == "" {
		alg = "SYMMETRIC_DEFAULT"
	}
	var blob []byte
	switch {
	case alg == "SYMMETRIC_DEFAULT":
		if k.KeySpec != "SYMMETRIC_DEFAULT" {
			return nil, awshttp.Errf(400, "ValidationException", "SYMMETRIC_DEFAULT is not valid for key spec %s", k.KeySpec)
		}
		var err error
		blob, err = seal(k, plaintext, awsjson.StrMap(p, "EncryptionContext"))
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
	default:
		blob, aerr = k.rsaEncrypt(alg, plaintext)
		if aerr != nil {
			return nil, aerr
		}
	}
	return map[string]any{
		"CiphertextBlob":      base64.StdEncoding.EncodeToString(blob),
		"KeyId":               k.ARN(),
		"EncryptionAlgorithm": alg,
	}, nil
}

func (s *Server) decrypt(p map[string]any) (any, *awshttp.APIError) {
	blob, aerr := awsjson.Blob(p, "CiphertextBlob")
	if aerr != nil {
		return nil, aerr
	}
	alg := awsjson.Str(p, "EncryptionAlgorithm")
	if alg == "" {
		alg = "SYMMETRIC_DEFAULT"
	}
	if alg != "SYMMETRIC_DEFAULT" {
		// Asymmetric decryption: the blob carries no key id, so KeyId is required.
		k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		if aerr := usable(k); aerr != nil {
			return nil, aerr
		}
		if aerr := requireUsage(k, usageEncryptDecrypt); aerr != nil {
			return nil, aerr
		}
		pt, aerr := k.rsaDecrypt(alg, blob)
		if aerr != nil {
			return nil, aerr
		}
		return map[string]any{
			"Plaintext":           base64.StdEncoding.EncodeToString(pt),
			"KeyId":               k.ARN(),
			"EncryptionAlgorithm": alg,
		}, nil
	}
	keyID, unseal, aerr := openBlob(blob)
	if aerr != nil {
		return nil, aerr
	}
	k, err := s.store.Resolve(keyID)
	if err != nil {
		return nil, awshttp.Errf(400, "InvalidCiphertextException", "the key this ciphertext was encrypted under no longer exists")
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if aerr := requireUsage(k, usageEncryptDecrypt); aerr != nil {
		return nil, aerr
	}
	pt, uerr := unseal(k, awsjson.StrMap(p, "EncryptionContext"))
	if uerr != nil {
		return nil, awshttp.AsAPIError(uerr)
	}
	return map[string]any{
		"Plaintext":           base64.StdEncoding.EncodeToString(pt),
		"KeyId":               k.ARN(),
		"EncryptionAlgorithm": "SYMMETRIC_DEFAULT",
	}, nil
}

func (s *Server) reEncrypt(p map[string]any) (any, *awshttp.APIError) {
	// Decrypt under the source context, encrypt under the destination.
	dec, aerr := s.decrypt(map[string]any{
		"CiphertextBlob":      p["CiphertextBlob"],
		"EncryptionContext":   p["SourceEncryptionContext"],
		"EncryptionAlgorithm": p["SourceEncryptionAlgorithm"],
		"KeyId":               p["SourceKeyId"],
	})
	if aerr != nil {
		return nil, aerr
	}
	decMap := dec.(map[string]any)
	enc, aerr := s.encrypt(map[string]any{
		"KeyId":               p["DestinationKeyId"],
		"Plaintext":           decMap["Plaintext"],
		"EncryptionContext":   p["DestinationEncryptionContext"],
		"EncryptionAlgorithm": p["DestinationEncryptionAlgorithm"],
	})
	if aerr != nil {
		return nil, aerr
	}
	encMap := enc.(map[string]any)
	return map[string]any{
		"CiphertextBlob":                 encMap["CiphertextBlob"],
		"KeyId":                          encMap["KeyId"],
		"SourceKeyId":                    decMap["KeyId"],
		"SourceEncryptionAlgorithm":      decMap["EncryptionAlgorithm"],
		"DestinationEncryptionAlgorithm": encMap["EncryptionAlgorithm"],
	}, nil
}

// ---- data keys ----

func (s *Server) generateDataKey(p map[string]any) (any, *awshttp.APIError) {
	return s.dataKey(p, true)
}

func (s *Server) generateDataKeyWithoutPlaintext(p map[string]any) (any, *awshttp.APIError) {
	return s.dataKey(p, false)
}

func (s *Server) dataKey(p map[string]any, withPlaintext bool) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if aerr := requireUsage(k, usageEncryptDecrypt); aerr != nil {
		return nil, aerr
	}
	if k.KeySpec != "SYMMETRIC_DEFAULT" {
		return nil, awshttp.Errf(400, "ValidationException", "GenerateDataKey requires a symmetric key")
	}
	n := 0
	switch awsjson.Str(p, "KeySpec") {
	case "AES_256", "":
		n = 32
	case "AES_128":
		n = 16
	}
	if bytes := awsjson.Int(p, "NumberOfBytes", 0); bytes > 0 {
		if bytes > 1024 {
			return nil, awshttp.Errf(400, "ValidationException", "NumberOfBytes must be <= 1024")
		}
		n = bytes
	}
	if n == 0 {
		return nil, awshttp.Errf(400, "ValidationException", "specify KeySpec (AES_128/AES_256) or NumberOfBytes")
	}
	dk := make([]byte, n)
	rand.Read(dk)
	blob, serr := seal(k, dk, awsjson.StrMap(p, "EncryptionContext"))
	if serr != nil {
		return nil, awshttp.AsAPIError(serr)
	}
	out := map[string]any{
		"CiphertextBlob": base64.StdEncoding.EncodeToString(blob),
		"KeyId":          k.ARN(),
	}
	if withPlaintext {
		out["Plaintext"] = base64.StdEncoding.EncodeToString(dk)
	}
	return out, nil
}

func (s *Server) generateDataKeyPair(p map[string]any) (any, *awshttp.APIError) {
	return s.dataKeyPair(p, true)
}

func (s *Server) generateDataKeyPairWithoutPlaintext(p map[string]any) (any, *awshttp.APIError) {
	return s.dataKeyPair(p, false)
}

func (s *Server) dataKeyPair(p map[string]any, withPlaintext bool) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if k.KeySpec != "SYMMETRIC_DEFAULT" {
		return nil, awshttp.Errf(400, "ValidationException", "GenerateDataKeyPair requires a symmetric wrapping key")
	}
	spec := awsjson.Str(p, "KeyPairSpec")
	if aerr := validateSpecUsage(spec, defaultUsageFor(spec)); aerr != nil {
		return nil, aerr
	}
	if specs[spec].hmacBytes > 0 || spec == "SYMMETRIC_DEFAULT" {
		return nil, awshttp.Errf(400, "ValidationException", "KeyPairSpec must be an RSA or ECC spec, got %q", spec)
	}
	privDER, gerr := generateMaterial(spec)
	if gerr != nil {
		return nil, awshttp.AsAPIError(gerr)
	}
	pubDER, aerr := (&Key{Material: privDER}).publicKeyDER()
	if aerr != nil {
		return nil, aerr
	}
	blob, serr := seal(k, privDER, awsjson.StrMap(p, "EncryptionContext"))
	if serr != nil {
		return nil, awshttp.AsAPIError(serr)
	}
	out := map[string]any{
		"KeyId":                    k.ARN(),
		"KeyPairSpec":              spec,
		"PrivateKeyCiphertextBlob": base64.StdEncoding.EncodeToString(blob),
		"PublicKey":                base64.StdEncoding.EncodeToString(pubDER),
	}
	if withPlaintext {
		out["PrivateKeyPlaintext"] = base64.StdEncoding.EncodeToString(privDER)
	}
	return out, nil
}

func (s *Server) generateRandom(p map[string]any) (any, *awshttp.APIError) {
	n := awsjson.Int(p, "NumberOfBytes", 0)
	if n < 1 || n > 1024 {
		return nil, awshttp.Errf(400, "ValidationException", "NumberOfBytes must be 1-1024")
	}
	b := make([]byte, n)
	rand.Read(b)
	return map[string]any{"Plaintext": base64.StdEncoding.EncodeToString(b)}, nil
}

// ---- sign / verify / MAC / public key ----

func (s *Server) sign(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if aerr := requireUsage(k, usageSignVerify); aerr != nil {
		return nil, aerr
	}
	message, aerr := awsjson.Blob(p, "Message")
	if aerr != nil {
		return nil, aerr
	}
	sig, aerr := k.sign(awsjson.Str(p, "SigningAlgorithm"), message, awsjson.Str(p, "MessageType"))
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{
		"KeyId":            k.ARN(),
		"Signature":        base64.StdEncoding.EncodeToString(sig),
		"SigningAlgorithm": awsjson.Str(p, "SigningAlgorithm"),
	}, nil
}

func (s *Server) verify(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if aerr := requireUsage(k, usageSignVerify); aerr != nil {
		return nil, aerr
	}
	message, aerr := awsjson.Blob(p, "Message")
	if aerr != nil {
		return nil, aerr
	}
	sig, aerr := awsjson.Blob(p, "Signature")
	if aerr != nil {
		return nil, aerr
	}
	ok, aerr := k.verify(awsjson.Str(p, "SigningAlgorithm"), message, awsjson.Str(p, "MessageType"), sig)
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, awshttp.Errf(400, "KMSInvalidSignatureException", "signature verification failed")
	}
	return map[string]any{
		"KeyId":            k.ARN(),
		"SignatureValid":   true,
		"SigningAlgorithm": awsjson.Str(p, "SigningAlgorithm"),
	}, nil
}

func (s *Server) getPublicKey(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	der, aerr := k.publicKeyDER()
	if aerr != nil {
		return nil, aerr
	}
	out := map[string]any{
		"KeyId":                 k.ARN(),
		"PublicKey":             base64.StdEncoding.EncodeToString(der),
		"KeySpec":               k.KeySpec,
		"CustomerMasterKeySpec": k.KeySpec,
		"KeyUsage":              k.KeyUsage,
	}
	if k.KeyUsage == usageSignVerify {
		out["SigningAlgorithms"] = k.signingAlgorithms()
	} else {
		out["EncryptionAlgorithms"] = k.encryptionAlgorithms()
	}
	return out, nil
}

func (s *Server) generateMac(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if aerr := requireUsage(k, usageGenerateVerifyMac); aerr != nil {
		return nil, aerr
	}
	message, aerr := awsjson.Blob(p, "Message")
	if aerr != nil {
		return nil, aerr
	}
	mac, aerr := k.mac(awsjson.Str(p, "MacAlgorithm"), message)
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{
		"KeyId":        k.ARN(),
		"Mac":          base64.StdEncoding.EncodeToString(mac),
		"MacAlgorithm": awsjson.Str(p, "MacAlgorithm"),
	}, nil
}

func (s *Server) verifyMac(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if aerr := requireUsage(k, usageGenerateVerifyMac); aerr != nil {
		return nil, aerr
	}
	message, aerr := awsjson.Blob(p, "Message")
	if aerr != nil {
		return nil, aerr
	}
	want, aerr := awsjson.Blob(p, "Mac")
	if aerr != nil {
		return nil, aerr
	}
	got, aerr := k.mac(awsjson.Str(p, "MacAlgorithm"), message)
	if aerr != nil {
		return nil, aerr
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return nil, awshttp.Errf(400, "KMSInvalidMacException", "MAC verification failed")
	}
	return map[string]any{
		"KeyId":        k.ARN(),
		"MacValid":     true,
		"MacAlgorithm": awsjson.Str(p, "MacAlgorithm"),
	}, nil
}
