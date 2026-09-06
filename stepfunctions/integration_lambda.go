package stepfunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

// The Lambda integration, in both spellings. A bare function ARN unwraps the
// payload — the function's return value IS the task result. The optimized
// arn:aws:states:::lambda:invoke answers with the API's response shape
// (Payload, StatusCode, ExecutedVersion) instead.
//
// Error-name fidelity is the entire point of this file: a handler that threw
// MyError must surface as MyError for Catch to route it, a throttle must be
// Lambda.TooManyRequestsException for the CDK's default Retry to match, and
// only a transport-level failure is States.TaskFailed.

// lambdaFunctionOfARN extracts what the invoke path wants from a function
// ARN: the name, with a qualifier kept if one is present.
func lambdaFunctionOfARN(arn string) string {
	_, after, ok := strings.Cut(arn, ":function:")
	if !ok {
		return arn
	}
	return after
}

func (s *Server) invokeLambda(ctx context.Context, function string, payload []byte, bare bool) asl.TaskResult {
	res, err := peercall.LambdaInvokeDetailed(ctx, s.peers, function, payload)
	if err != nil {
		return failResult(asl.ErrTaskFailed, err.Error())
	}
	if res.StatusCode/100 != 2 {
		code := res.ErrorCode
		if code == "" {
			return failResult(asl.ErrTaskFailed,
				fmt.Sprintf("lambda answered %d: %s", res.StatusCode, res.Payload))
		}
		// AWS prefixes the service's own error code: Lambda.ResourceNotFound-
		// Exception, Lambda.TooManyRequestsException — the names Retry
		// templates are written against.
		return failResult("Lambda."+code, string(res.Payload))
	}
	if res.FunctionError != "" {
		name := "Lambda.Unknown"
		var e struct {
			ErrorType string `json:"errorType"`
		}
		if json.Unmarshal(res.Payload, &e) == nil && e.ErrorType != "" {
			name = e.ErrorType
		}
		return failResult(name, string(res.Payload))
	}
	if bare {
		return asl.TaskResult{Output: res.Payload}
	}
	var payloadValue any
	if err := json.Unmarshal(res.Payload, &payloadValue); err != nil {
		payloadValue = string(res.Payload)
	}
	out, _ := json.Marshal(map[string]any{
		"Payload":         payloadValue,
		"StatusCode":      res.StatusCode,
		"ExecutedVersion": "$LATEST",
	})
	return asl.TaskResult{Output: out}
}

// invokeLambdaAPI is arn:aws:states:::lambda:invoke — parameters are the
// Invoke API's own, and the result is API-shaped.
func (s *Server) invokeLambdaAPI(ctx context.Context, input []byte) asl.TaskResult {
	var in struct {
		FunctionName   string          `json:"FunctionName"`
		Payload        json.RawMessage `json:"Payload"`
		InvocationType string          `json:"InvocationType"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.FunctionName == "" {
		return failResult(asl.ErrTaskFailed, "lambda:invoke needs a FunctionName parameter")
	}
	if in.InvocationType != "" && in.InvocationType != "RequestResponse" {
		return failResult(asl.ErrTaskFailed,
			fmt.Sprintf("InvocationType %q is not supported here; omit it or use RequestResponse", in.InvocationType))
	}
	payload := in.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	fn := in.FunctionName
	if strings.Contains(fn, ":function:") {
		fn = lambdaFunctionOfARN(fn)
	}
	return s.invokeLambda(ctx, fn, payload, false)
}
