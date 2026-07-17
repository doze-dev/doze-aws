package secretsmanager

import (
	"encoding/json"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

// rotateSecret runs the standard four-step Secrets Manager rotation by invoking
// the configured rotation Lambda synchronously for createSecret, setSecret,
// testSecret, and finishSecret — the function does the real work and moves the
// version stages back through Secrets Manager (PutSecretValue /
// UpdateSecretVersionStage). Requires a wired lambda peer.
func (s *Server) rotateSecret(p map[string]any) (any, *awshttp.APIError) {
	sec, err := s.store.Get(awsjson.Str(p, "SecretId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	lambdaARN := awsjson.Str(p, "RotationLambdaARN")
	if lambdaARN == "" {
		if rr, ok := p["RotationRules"].(map[string]any); ok {
			lambdaARN = pstrIn(rr, "RotationLambdaARN")
		}
	}
	if lambdaARN == "" {
		lambdaARN = sec.RotationLambdaARN
	}
	if lambdaARN == "" {
		return nil, awshttp.Errf(400, "InvalidParameterException", "a RotationLambdaARN is required to rotate the secret")
	}
	fn := lambdaFunctionName(lambdaARN)

	token := newUUID()
	for _, step := range []string{"createSecret", "setSecret", "testSecret", "finishSecret"} {
		payload, _ := json.Marshal(map[string]any{
			"Step":               step,
			"SecretId":           sec.ARN,
			"ClientRequestToken": token,
		})
		if _, err := peercall.LambdaInvoke(s.peers, fn, payload); err != nil {
			return nil, awshttp.Errf(500, "InternalServiceError", "rotation step %s failed: %v", step, err)
		}
	}

	if _, err := s.store.Mutate(sec.Name, func(x *Secret) error {
		x.RotationEnabled = true
		x.RotationLambdaARN = lambdaARN
		x.LastRotatedDate = s.store.now().Unix()
		return nil
	}); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"ARN": sec.ARN, "Name": sec.Name, "VersionId": token}, nil
}

// cancelRotateSecret disables rotation (leaving any in-flight AWSPENDING version
// as-is, matching AWS).
func (s *Server) cancelRotateSecret(p map[string]any) (any, *awshttp.APIError) {
	sec, err := s.store.Mutate(awsjson.Str(p, "SecretId"), func(x *Secret) error {
		x.RotationEnabled = false
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"ARN": sec.ARN, "Name": sec.Name}, nil
}

// lambdaFunctionName extracts the function name from a Lambda ARN
// (arn:...:function:NAME[:qualifier]); a bare name passes through.
func lambdaFunctionName(arn string) string {
	if i := strings.Index(arn, ":function:"); i >= 0 {
		name := arn[i+len(":function:"):]
		if c := strings.IndexByte(name, ':'); c >= 0 {
			name = name[:c]
		}
		return name
	}
	return arn
}

// pstrIn reads a string from a nested map.
func pstrIn(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
