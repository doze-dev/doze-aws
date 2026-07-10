// Package awsjson implements the AWS JSON 1.0/1.1 protocols: POST / with an
// X-Amz-Target header naming the action ("Prefix.Action"), a JSON body, and
// JSON responses. DynamoDB and modern SQS speak 1.0; KMS, SSM, Secrets Manager
// and EventBridge speak 1.1. The two versions differ only in the Content-Type
// they echo and minor error-shape history — one codec covers both.
//
// Errors travel as HTTP status + {"__type":"Code","message":"..."} plus the
// x-amzn-ErrorType header, which is what both SDK generations match on.
package awsjson

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// maxBodyBytes bounds request bodies. The largest legitimate JSON-protocol
// payloads (DynamoDB batch writes) are low single-digit MiB.
const maxBodyBytes = 32 << 20

// API decodes requests and renders responses for one JSON-protocol service.
type API struct {
	// TargetPrefix is the service's X-Amz-Target prefix, e.g. "AmazonSQS",
	// "DynamoDB_20120810", "TrentService".
	TargetPrefix string
	// JSONVersion is "1.0" or "1.1" (sets the response Content-Type).
	JSONVersion string
}

// ContentType returns the protocol's content type, e.g. "application/x-amz-json-1.0".
func (a API) ContentType() string { return "application/x-amz-json-" + a.JSONVersion }

// Action extracts the action name from the X-Amz-Target header, validating the
// service prefix.
func (a API) Action(r *http.Request) (string, *awshttp.APIError) {
	target := r.Header.Get("X-Amz-Target")
	if target == "" {
		return "", awshttp.Errf(400, "MissingAction", "request has no X-Amz-Target header")
	}
	prefix, action, ok := strings.Cut(target, ".")
	if !ok || prefix != a.TargetPrefix || action == "" {
		return "", awshttp.Errf(400, "InvalidAction", "unexpected X-Amz-Target %q", target)
	}
	return action, nil
}

// DecodeBody reads and unmarshals the JSON request body into dst. An empty
// body decodes as an empty object (several actions take no parameters and some
// SDKs send nothing at all).
func DecodeBody(r *http.Request, dst any) *awshttp.APIError {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		return awshttp.Errf(400, "InvalidParameterValue", "read request body: %v", err)
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return awshttp.Errf(400, "SerializationException", "malformed JSON body: %v", err)
	}
	return nil
}

// Write renders a success response. A nil result writes an empty JSON object
// (the SDKs expect a body even for result-less actions).
func (a API) Write(w http.ResponseWriter, result any) {
	if result == nil {
		result = struct{}{}
	}
	body, err := json.Marshal(result)
	if err != nil {
		a.WriteError(w, awshttp.AsAPIError(err))
		return
	}
	w.Header().Set("Content-Type", a.ContentType())
	w.Header().Set("x-amzn-RequestId", awshttp.RequestID())
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// WriteError renders the JSON-protocol error shape with e's HTTP status.
func (a API) WriteError(w http.ResponseWriter, e *awshttp.APIError) {
	payload := map[string]any{
		"__type":  e.Code,
		"message": e.Message,
	}
	if len(e.Item) > 0 {
		payload["Item"] = json.RawMessage(e.Item)
	}
	for k, v := range e.Extra {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", a.ContentType())
	w.Header().Set("x-amzn-RequestId", awshttp.RequestID())
	w.Header().Set("x-amzn-ErrorType", e.Code)
	w.WriteHeader(e.Status)
	w.Write(body)
}
