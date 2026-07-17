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
		policy = defaultKeyPolicy
	}
	return map[string]any{"Policy": policy}, nil
}

func (s *Server) putKeyPolicy(p map[string]any) (any, *awshttp.APIError) {
	policy := awsjson.Str(p, "Policy")
	if policy != "" && !json.Valid([]byte(policy)) {
		return nil, awshttp.Errf(400, "MalformedPolicyDocumentException", "Policy is not valid JSON")
	}
	_, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
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

const defaultKeyPolicy = `{"Version":"2012-10-17","Id":"key-default-1","Statement":[{"Sid":"Enable IAM policies","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"kms:*","Resource":"*"}]}`
