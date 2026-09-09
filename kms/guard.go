package kms

// The key policy, evaluated on key-scoped requests under IAM soft or enforce.
// KMS is the one service where the resource policy gates the identity
// policies: an identity policy grants only when the key policy lets the
// account in ("Enable IAM policies", the default policy's one statement),
// so a key policy that names nobody locks everyone out, as on AWS.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// guardRequest runs the guard for a request that names a key — or, as the
// audit found, one that names it some other way: Decrypt carries the key
// inside the ciphertext, ReEncrypt names a source and a destination, the
// alias operations name a target key or an alias. Each key the request
// touches is checked against its own policy.
func (s *Server) guardRequest(w http.ResponseWriter, r *http.Request, action string, p map[string]any) *awshttp.APIError {
	if s.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	type check struct{ action, ident string }
	var checks []check
	switch action {
	case "CreateKey", "ListKeys", "ListAliases", "GenerateRandom":
		return s.guard.CheckIdentity(w, r, "kms:"+action, "")
	case "Decrypt":
		checks = append(checks, check{action, s.decryptKeyIdent(p)})
	case "ReEncrypt":
		// The source key authorizes ReEncryptFrom, the destination ReEncryptTo.
		src := awsjson.Str(p, "SourceKeyId")
		if src == "" {
			src = s.decryptKeyIdent(p)
		}
		checks = append(checks, check{"ReEncryptFrom", src}, check{"ReEncryptTo", awsjson.Str(p, "DestinationKeyId")})
	case "CreateAlias", "UpdateAlias":
		checks = append(checks, check{action, awsjson.Str(p, "TargetKeyId")})
	case "DeleteAlias":
		checks = append(checks, check{action, awsjson.Str(p, "AliasName")})
	default:
		checks = append(checks, check{action, awsjson.Str(p, "KeyId")})
	}
	for _, c := range checks {
		if c.ident == "" {
			// No key named: the handler reports the missing parameter, and
			// the identity verdict alone decides.
			if aerr := s.guard.CheckIdentity(w, r, "kms:"+c.action, ""); aerr != nil {
				return aerr
			}
			continue
		}
		k, err := s.store.Resolve(c.ident)
		if err != nil {
			// The handler reports the missing key.
			if aerr := s.guard.CheckIdentity(w, r, "kms:"+c.action, ""); aerr != nil {
				return aerr
			}
			continue
		}
		policy := k.Policy
		if policy == "" {
			policy = defaultKeyPolicy
		}
		var docs []*iampolicy.Document
		if doc, err := iampolicy.Parse(policy); err == nil {
			docs = append(docs, doc)
		}
		if aerr := s.guard.Check(w, r, docs, "kms:"+c.action, k.ARN()); aerr != nil {
			return aerr
		}
	}
	return nil
}

// decryptKeyIdent finds the key a Decrypt addresses: KeyId when given (an
// asymmetric decrypt must name it), else the key the ciphertext was sealed
// under. "" when the blob is not one of ours; the handler refuses it.
func (s *Server) decryptKeyIdent(p map[string]any) string {
	if id := awsjson.Str(p, "KeyId"); id != "" {
		return id
	}
	blob, aerr := awsjson.Blob(p, "CiphertextBlob")
	if aerr != nil {
		return ""
	}
	keyID, _, aerr := openBlob(blob)
	if aerr != nil {
		return ""
	}
	return keyID
}
