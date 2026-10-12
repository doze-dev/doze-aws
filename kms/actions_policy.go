package kms

// Key policy actions: get/put/list (real KMS has exactly one policy, "default").

import (
	"encoding/json"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

func (s *Server) getKeyPolicy(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	policy := k.Policy
	if policy == "" {
		policy = s.defaultKeyPolicy()
	}
	return map[string]any{"Policy": policy}, nil
}

func (s *Server) putKeyPolicy(p map[string]any) (any, *awshttp.APIError) {
	policy := awsjson.Str(p, "Policy")
	if policy != "" && !json.Valid([]byte(policy)) {
		return nil, awshttp.Errf(400, "MalformedPolicyDocumentException", "Policy is not valid JSON")
	}
	_, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *key) *awshttp.APIError {
		k.Policy = policy
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) listKeyPolicies(p map[string]any) (any, *awshttp.APIError) {
	if _, err := s.store.Resolve(awsjson.Str(p, "KeyId")); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	// Real KMS supports exactly one policy, named "default".
	return map[string]any{"PolicyNames": []string{"default"}, "Truncated": false}, nil
}

// defaultKeyPolicy is the policy of a key that was given none: the root of the
// account that owns the key may do everything, which hands the decision to that
// account's IAM policies. It is built for the instance's account. Written with
// the default account, a key made under any other was closed to its own root
// from the moment it existed.
func (s *Server) defaultKeyPolicy() string {
	return `{"Version":"2012-10-17","Id":"key-default-1","Statement":[{"Sid":"Enable IAM policies","Effect":"Allow","Principal":{"AWS":"` +
		s.id.GlobalARN("iam", "root") + `"},"Action":"kms:*","Resource":"*"}]}`
}
