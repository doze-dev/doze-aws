package kms

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"sort"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// handlers maps every supported KMS action. Actions absent here (grants
// management, custom key stores, key material import, multi-region replicas)
// answer via stubs registered at the bottom.
var handlers = map[string]handler{
	"CreateKey":                           (*Server).createKey,
	"DescribeKey":                         (*Server).describeKey,
	"ListKeys":                            (*Server).listKeys,
	"EnableKey":                           (*Server).enableKey,
	"DisableKey":                          (*Server).disableKey,
	"ScheduleKeyDeletion":                 (*Server).scheduleKeyDeletion,
	"CancelKeyDeletion":                   (*Server).cancelKeyDeletion,
	"Encrypt":                             (*Server).encrypt,
	"Decrypt":                             (*Server).decrypt,
	"ReEncrypt":                           (*Server).reEncrypt,
	"GenerateDataKey":                     (*Server).generateDataKey,
	"GenerateDataKeyWithoutPlaintext":     (*Server).generateDataKeyWithoutPlaintext,
	"GenerateDataKeyPair":                 (*Server).generateDataKeyPair,
	"GenerateDataKeyPairWithoutPlaintext": (*Server).generateDataKeyPairWithoutPlaintext,
	"GenerateRandom":                      (*Server).generateRandom,
	"Sign":                                (*Server).sign,
	"Verify":                              (*Server).verify,
	"GetPublicKey":                        (*Server).getPublicKey,
	"GenerateMac":                         (*Server).generateMac,
	"VerifyMac":                           (*Server).verifyMac,
	"CreateAlias":                         (*Server).createAlias,
	"UpdateAlias":                         (*Server).updateAlias,
	"DeleteAlias":                         (*Server).deleteAlias,
	"ListAliases":                         (*Server).listAliases,
	"TagResource":                         (*Server).tagResource,
	"UntagResource":                       (*Server).untagResource,
	"ListResourceTags":                    (*Server).listResourceTags,
	"GetKeyPolicy":                        (*Server).getKeyPolicy,
	"PutKeyPolicy":                        (*Server).putKeyPolicy,
	"ListKeyPolicies":                     (*Server).listKeyPolicies,
	"GetKeyRotationStatus":                (*Server).getKeyRotationStatus,
	"EnableKeyRotation":                   (*Server).enableKeyRotation,
	"DisableKeyRotation":                  (*Server).disableKeyRotation,
	"UpdateKeyDescription":                (*Server).updateKeyDescription,
}

func init() {
	// Tier S: infrastructure that cannot exist locally answers honestly.
	for _, name := range []string{
		"CreateCustomKeyStore", "ConnectCustomKeyStore", "DisconnectCustomKeyStore",
		"DeleteCustomKeyStore", "DescribeCustomKeyStores", "UpdateCustomKeyStore",
		"ImportKeyMaterial", "DeleteImportedKeyMaterial", "GetParametersForImport",
		"ReplicateKey", "UpdatePrimaryRegion",
		"CreateGrant", "RetireGrant", "RevokeGrant", "ListGrants", "ListRetirableGrants",
		"RotateKeyOnDemand", "ListKeyRotations", // Phase 8
		"DeriveSharedSecret", "VerifyMacForImport",
	} {
		handlers[name] = stub(name)
	}
}

func stub(name string) handler {
	return func(*Server, map[string]any) (any, *awshttp.APIError) {
		return nil, awshttp.Errf(400, "UnsupportedOperationException",
			"%s is not supported by doze-aws (see docs/api-support/kms.md)", name)
	}
}

// ---- param helpers (JSON protocol: blobs travel base64-encoded) ----

func pstr(p map[string]any, key string) string {
	s, _ := p[key].(string)
	return s
}

func pint(p map[string]any, key string, def int) int {
	if f, ok := p[key].(float64); ok {
		return int(f)
	}
	return def
}

func pblob(p map[string]any, key string) ([]byte, *awshttp.APIError) {
	s, ok := p[key].(string)
	if !ok || s == "" {
		return nil, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, awshttp.Errf(400, "ValidationException", "%s is not valid base64", key)
	}
	return b, nil
}

func pstrmap(p map[string]any, key string) map[string]string {
	m, ok := p[key].(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// ptags reads the KMS tag list shape [{TagKey, TagValue}].
func ptags(p map[string]any, key string) map[string]string {
	list, ok := p[key].([]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			k, _ := m["TagKey"].(string)
			v, _ := m["TagValue"].(string)
			if k != "" {
				out[k] = v
			}
		}
	}
	return out
}

// keyMetadata is the DescribeKey/CreateKey metadata shape.
type keyMetadata struct {
	AWSAccountId          string   `json:"AWSAccountId"`
	KeyId                 string   `json:"KeyId"`
	Arn                   string   `json:"Arn"`
	CreationDate          float64  `json:"CreationDate"`
	Enabled               bool     `json:"Enabled"`
	Description           string   `json:"Description"`
	KeyUsage              string   `json:"KeyUsage"`
	KeyState              string   `json:"KeyState"`
	DeletionDate          *float64 `json:"DeletionDate,omitempty"`
	Origin                string   `json:"Origin"`
	KeyManager            string   `json:"KeyManager"`
	KeySpec               string   `json:"KeySpec"`
	CustomerMasterKeySpec string   `json:"CustomerMasterKeySpec"` // legacy field, same value
	EncryptionAlgorithms  []string `json:"EncryptionAlgorithms,omitempty"`
	SigningAlgorithms     []string `json:"SigningAlgorithms,omitempty"`
	MacAlgorithms         []string `json:"MacAlgorithms,omitempty"`
	MultiRegion           bool     `json:"MultiRegion"`
}

func metadataFor(k *Key) keyMetadata {
	md := keyMetadata{
		AWSAccountId:          "000000000000",
		KeyId:                 k.ID,
		Arn:                   k.ARN(),
		CreationDate:          float64(k.Created),
		Enabled:               k.State == stateEnabled,
		Description:           k.Description,
		KeyUsage:              k.KeyUsage,
		KeyState:              k.State,
		Origin:                "AWS_KMS",
		KeyManager:            "CUSTOMER",
		KeySpec:               k.KeySpec,
		CustomerMasterKeySpec: k.KeySpec,
		EncryptionAlgorithms:  k.encryptionAlgorithms(),
		MultiRegion:           k.MultiRegion,
	}
	if k.KeyUsage == usageSignVerify {
		md.SigningAlgorithms = k.signingAlgorithms()
		md.EncryptionAlgorithms = nil
	}
	if alg, _ := k.macAlgorithm(); alg != "" {
		md.MacAlgorithms = []string{alg}
		md.EncryptionAlgorithms = nil
	}
	if k.DeletionAt > 0 {
		d := float64(k.DeletionAt)
		md.DeletionDate = &d
	}
	return md
}

// ---- key lifecycle ----

func (s *Server) createKey(p map[string]any) (any, *awshttp.APIError) {
	spec := pstr(p, "KeySpec")
	if spec == "" {
		spec = pstr(p, "CustomerMasterKeySpec") // legacy parameter name
	}
	k, err := s.store.CreateKey(spec, pstr(p, "KeyUsage"), pstr(p, "Description"), pstr(p, "Policy"), ptags(p, "Tags"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"KeyMetadata": metadataFor(k)}, nil
}

func (s *Server) describeKey(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"KeyMetadata": metadataFor(k)}, nil
}

func (s *Server) listKeys(map[string]any) (any, *awshttp.APIError) {
	keys, err := s.store.List()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type entry struct {
		KeyId  string `json:"KeyId"`
		KeyArn string `json:"KeyArn"`
	}
	out := []entry{}
	for _, k := range keys {
		out = append(out, entry{KeyId: k.ID, KeyArn: k.ARN()})
	}
	return map[string]any{"Keys": out, "Truncated": false}, nil
}

func (s *Server) enableKey(p map[string]any) (any, *awshttp.APIError) {
	_, err := s.store.Update(pstr(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.State == statePendingDeletion {
			return awshttp.Errf(400, "KMSInvalidStateException", "key %s is pending deletion", k.ID)
		}
		k.State = stateEnabled
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) disableKey(p map[string]any) (any, *awshttp.APIError) {
	_, err := s.store.Update(pstr(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.State == statePendingDeletion {
			return awshttp.Errf(400, "KMSInvalidStateException", "key %s is pending deletion", k.ID)
		}
		k.State = stateDisabled
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) scheduleKeyDeletion(p map[string]any) (any, *awshttp.APIError) {
	days := pint(p, "PendingWindowInDays", 30)
	if days < 7 || days > 30 {
		return nil, awshttp.Errf(400, "ValidationException", "PendingWindowInDays must be between 7 and 30, got %d", days)
	}
	var when time.Time
	k, err := s.store.Update(pstr(p, "KeyId"), func(k *Key) *awshttp.APIError {
		when = s.store.now().Add(time.Duration(days) * 24 * time.Hour)
		k.State = statePendingDeletion
		k.DeletionAt = when.Unix()
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"KeyId":               k.ARN(),
		"DeletionDate":        float64(when.Unix()),
		"KeyState":            statePendingDeletion,
		"PendingWindowInDays": days,
	}, nil
}

func (s *Server) cancelKeyDeletion(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Update(pstr(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.State != statePendingDeletion {
			return awshttp.Errf(400, "KMSInvalidStateException", "key %s is not pending deletion", k.ID)
		}
		k.State = stateDisabled // AWS leaves a cancelled key disabled
		k.DeletionAt = 0
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"KeyId": k.ARN()}, nil
}

func (s *Server) updateKeyDescription(p map[string]any) (any, *awshttp.APIError) {
	_, err := s.store.Update(pstr(p, "KeyId"), func(k *Key) *awshttp.APIError {
		k.Description = pstr(p, "Description")
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

// ---- encrypt / decrypt ----

func (s *Server) encrypt(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	plaintext, aerr := pblob(p, "Plaintext")
	if aerr != nil {
		return nil, aerr
	}
	if len(plaintext) == 0 || len(plaintext) > 4096 {
		return nil, awshttp.Errf(400, "ValidationException", "Plaintext must be 1-4096 bytes")
	}
	alg := pstr(p, "EncryptionAlgorithm")
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
		blob, err = seal(k, plaintext, pstrmap(p, "EncryptionContext"))
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
	blob, aerr := pblob(p, "CiphertextBlob")
	if aerr != nil {
		return nil, aerr
	}
	alg := pstr(p, "EncryptionAlgorithm")
	if alg == "" {
		alg = "SYMMETRIC_DEFAULT"
	}
	if alg != "SYMMETRIC_DEFAULT" {
		// Asymmetric decryption: the blob carries no key id, so KeyId is required.
		k, err := s.store.Resolve(pstr(p, "KeyId"))
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		if aerr := usable(k); aerr != nil {
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
	pt, uerr := unseal(k, pstrmap(p, "EncryptionContext"))
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
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if k.KeySpec != "SYMMETRIC_DEFAULT" {
		return nil, awshttp.Errf(400, "ValidationException", "GenerateDataKey requires a symmetric key")
	}
	n := 0
	switch pstr(p, "KeySpec") {
	case "AES_256", "":
		n = 32
	case "AES_128":
		n = 16
	}
	if bytes := pint(p, "NumberOfBytes", 0); bytes > 0 {
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
	blob, serr := seal(k, dk, pstrmap(p, "EncryptionContext"))
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
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	if k.KeySpec != "SYMMETRIC_DEFAULT" {
		return nil, awshttp.Errf(400, "ValidationException", "GenerateDataKeyPair requires a symmetric wrapping key")
	}
	spec := pstr(p, "KeyPairSpec")
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
	blob, serr := seal(k, privDER, pstrmap(p, "EncryptionContext"))
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
	n := pint(p, "NumberOfBytes", 0)
	if n < 1 || n > 1024 {
		return nil, awshttp.Errf(400, "ValidationException", "NumberOfBytes must be 1-1024")
	}
	b := make([]byte, n)
	rand.Read(b)
	return map[string]any{"Plaintext": base64.StdEncoding.EncodeToString(b)}, nil
}

// ---- sign / verify / MAC / public key ----

func (s *Server) sign(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	message, aerr := pblob(p, "Message")
	if aerr != nil {
		return nil, aerr
	}
	sig, aerr := k.sign(pstr(p, "SigningAlgorithm"), message, pstr(p, "MessageType"))
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{
		"KeyId":            k.ARN(),
		"Signature":        base64.StdEncoding.EncodeToString(sig),
		"SigningAlgorithm": pstr(p, "SigningAlgorithm"),
	}, nil
}

func (s *Server) verify(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	message, aerr := pblob(p, "Message")
	if aerr != nil {
		return nil, aerr
	}
	sig, aerr := pblob(p, "Signature")
	if aerr != nil {
		return nil, aerr
	}
	ok, aerr := k.verify(pstr(p, "SigningAlgorithm"), message, pstr(p, "MessageType"), sig)
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, awshttp.Errf(400, "KMSInvalidSignatureException", "signature verification failed")
	}
	return map[string]any{
		"KeyId":            k.ARN(),
		"SignatureValid":   true,
		"SigningAlgorithm": pstr(p, "SigningAlgorithm"),
	}, nil
}

func (s *Server) getPublicKey(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(pstr(p, "KeyId"))
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
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	message, aerr := pblob(p, "Message")
	if aerr != nil {
		return nil, aerr
	}
	mac, aerr := k.mac(pstr(p, "MacAlgorithm"), message)
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{
		"KeyId":        k.ARN(),
		"Mac":          base64.StdEncoding.EncodeToString(mac),
		"MacAlgorithm": pstr(p, "MacAlgorithm"),
	}, nil
}

func (s *Server) verifyMac(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if aerr := usable(k); aerr != nil {
		return nil, aerr
	}
	message, aerr := pblob(p, "Message")
	if aerr != nil {
		return nil, aerr
	}
	want, aerr := pblob(p, "Mac")
	if aerr != nil {
		return nil, aerr
	}
	got, aerr := k.mac(pstr(p, "MacAlgorithm"), message)
	if aerr != nil {
		return nil, aerr
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return nil, awshttp.Errf(400, "KMSInvalidMacException", "MAC verification failed")
	}
	return map[string]any{
		"KeyId":        k.ARN(),
		"MacValid":     true,
		"MacAlgorithm": pstr(p, "MacAlgorithm"),
	}, nil
}

// ---- aliases, tags, policies, rotation flags ----

func (s *Server) createAlias(p map[string]any) (any, *awshttp.APIError) {
	name, aerr := aliasName(pstr(p, "AliasName"))
	if aerr != nil {
		return nil, aerr
	}
	return nil, awshttp.AsAPIErrorOrNil(s.store.SetAlias(name, pstr(p, "TargetKeyId"), false, true))
}

func (s *Server) updateAlias(p map[string]any) (any, *awshttp.APIError) {
	name, aerr := aliasName(pstr(p, "AliasName"))
	if aerr != nil {
		return nil, aerr
	}
	return nil, awshttp.AsAPIErrorOrNil(s.store.SetAlias(name, pstr(p, "TargetKeyId"), true, false))
}

func (s *Server) deleteAlias(p map[string]any) (any, *awshttp.APIError) {
	name, aerr := aliasName(pstr(p, "AliasName"))
	if aerr != nil {
		return nil, aerr
	}
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteAlias(name))
}

func aliasName(full string) (string, *awshttp.APIError) {
	if len(full) < 7 || full[:6] != "alias/" {
		return "", awshttp.Errf(400, "ValidationException", "AliasName must start with alias/")
	}
	return full[6:], nil
}

func (s *Server) listAliases(p map[string]any) (any, *awshttp.APIError) {
	aliases, err := s.store.Aliases()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	filterKey := ""
	if ident := pstr(p, "KeyId"); ident != "" {
		k, err := s.store.Resolve(ident)
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		filterKey = k.ID
	}
	type entry struct {
		AliasName   string `json:"AliasName"`
		AliasArn    string `json:"AliasArn"`
		TargetKeyId string `json:"TargetKeyId"`
	}
	out := []entry{}
	for _, a := range aliases {
		if filterKey != "" && a[1] != filterKey {
			continue
		}
		out = append(out, entry{
			AliasName:   "alias/" + a[0],
			AliasArn:    aliasARN(a[0]),
			TargetKeyId: a[1],
		})
	}
	return map[string]any{"Aliases": out, "Truncated": false}, nil
}

func (s *Server) tagResource(p map[string]any) (any, *awshttp.APIError) {
	tags := ptags(p, "Tags")
	_, err := s.store.Update(pstr(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.Tags == nil {
			k.Tags = map[string]string{}
		}
		for key, v := range tags {
			k.Tags[key] = v
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) untagResource(p map[string]any) (any, *awshttp.APIError) {
	keys, _ := p["TagKeys"].([]any)
	_, err := s.store.Update(pstr(p, "KeyId"), func(k *Key) *awshttp.APIError {
		for _, tk := range keys {
			if name, ok := tk.(string); ok {
				delete(k.Tags, name)
			}
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) listResourceTags(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type tag struct {
		TagKey   string `json:"TagKey"`
		TagValue string `json:"TagValue"`
	}
	out := []tag{}
	for _, key := range sortedKeys(k.Tags) {
		out = append(out, tag{TagKey: key, TagValue: k.Tags[key]})
	}
	return map[string]any{"Tags": out, "Truncated": false}, nil
}

func (s *Server) getKeyPolicy(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	policy := k.Policy
	if policy == "" {
		policy = defaultKeyPolicy
	}
	return map[string]any{"Policy": policy}, nil
}

func (s *Server) putKeyPolicy(p map[string]any) (any, *awshttp.APIError) {
	policy := pstr(p, "Policy")
	if policy != "" && !json.Valid([]byte(policy)) {
		return nil, awshttp.Errf(400, "MalformedPolicyDocumentException", "Policy is not valid JSON")
	}
	_, err := s.store.Update(pstr(p, "KeyId"), func(k *Key) *awshttp.APIError {
		k.Policy = policy
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) listKeyPolicies(p map[string]any) (any, *awshttp.APIError) {
	if _, err := s.store.Resolve(pstr(p, "KeyId")); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	// Real KMS supports exactly one policy, named "default".
	return map[string]any{"PolicyNames": []string{"default"}, "Truncated": false}, nil
}

func (s *Server) getKeyRotationStatus(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(pstr(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"KeyRotationEnabled": k.RotationOn}, nil
}

func (s *Server) enableKeyRotation(p map[string]any) (any, *awshttp.APIError) {
	return s.setRotation(p, true)
}

func (s *Server) disableKeyRotation(p map[string]any) (any, *awshttp.APIError) {
	return s.setRotation(p, false)
}

func (s *Server) setRotation(p map[string]any, on bool) (any, *awshttp.APIError) {
	_, err := s.store.Update(pstr(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.KeySpec != "SYMMETRIC_DEFAULT" {
			return awshttp.Errf(400, "UnsupportedOperationException", "rotation applies to symmetric keys only")
		}
		k.RotationOn = on
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

const defaultKeyPolicy = `{"Version":"2012-10-17","Id":"key-default-1","Statement":[{"Sid":"Enable IAM policies","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"kms:*","Resource":"*"}]}`

func aliasARN(name string) string {
	return awsident.ARN("kms", "alias/"+name)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
