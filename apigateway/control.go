package apigateway

// What every control-plane handler shares: the error constructors, the JSON
// writers, and the patch-document decoder. The handlers themselves live by
// resource (restapis.go, resources.go, methods.go, deployments.go, stages.go,
// authorizers.go, apikeys.go, usageplans.go, tags.go, account.go); router.go
// says which one serves each operation.

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// ---- helpers ----

func errNotFound(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(404, "NotFoundException", format, args...)
}

func errBadRequest(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "BadRequestException", format, args...)
}

func errConflict(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(409, "ConflictException", format, args...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, awshttp.AsAPIError(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-amzn-RequestId", awshttp.ResponseID(w))
	w.WriteHeader(status)
	w.Write(body)
}

func writeError(w http.ResponseWriter, e *awshttp.APIError) {
	body, _ := json.Marshal(map[string]string{"message": e.Message})
	w.Header().Set("Content-Type", "application/json")
	id := awshttp.ResponseID(w)
	awshttp.NoteFault(w, id, e)
	w.Header().Set("x-amzn-ErrorType", e.Code)
	w.Header().Set("x-amzn-RequestId", id)
	w.WriteHeader(e.Status)
	w.Write(body)
}

func decode(r *http.Request, dst any) *awshttp.APIError {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		return errBadRequest("read request body: %v", err)
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return errBadRequest("malformed JSON body: %v", err)
	}
	return nil
}

// patchOp is one entry of the JSON-Patch-ish document API Gateway uses for
// updates. Only replace/add/remove on simple paths are honoured.
type patchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
	From  string `json:"from"`
}

func decodePatch(r *http.Request) ([]patchOp, *awshttp.APIError) {
	var req struct {
		PatchOperations []patchOp `json:"patchOperations"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return nil, aerr
	}
	return req.PatchOperations, nil
}
