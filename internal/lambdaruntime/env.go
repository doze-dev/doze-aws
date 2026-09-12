package lambdaruntime

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// The child's environment: everything Lambda's execution environment sets,
// so a handler — or the SDK inside it — that reads any of these finds the
// value it would find in the cloud.
//
// Precedence, lowest to highest: the parent's environment, the Lambda set,
// the sibling-service endpoints, then the function's own variables, which
// win the way they do on AWS (a function may override AWS_REGION).

// FunctionARN is the unqualified ARN of a function by name.
func FunctionARN(id awsident.Identity, name string) string {
	return id.ARN("lambda", "function:"+name)
}

// LogGroupName is the log group Lambda creates for a function.
func LogGroupName(name string) string { return "/aws/lambda/" + name }

// version is the qualifier the child sees, "$LATEST" when none.
func (r *Runner) version() string {
	if r.spec.Version == "" {
		return "$LATEST"
	}
	return r.spec.Version
}

// childEnv builds the environment for one process. runtimeAPI is the
// loopback address the Runner listens on; stream is the log stream it minted.
func (r *Runner) childEnv(runtimeAPI, stream string) []string {
	taskRoot := r.spec.Dir
	if abs, err := filepath.Abs(taskRoot); err == nil && taskRoot != "" {
		taskRoot = abs
	}
	set := map[string]string{
		"AWS_LAMBDA_RUNTIME_API":          runtimeAPI,
		"_HANDLER":                        r.spec.Handler,
		"AWS_LAMBDA_FUNCTION_NAME":        r.spec.Name,
		"AWS_LAMBDA_FUNCTION_VERSION":     r.version(),
		"AWS_LAMBDA_FUNCTION_MEMORY_SIZE": strconv.Itoa(r.memoryMB()),
		"AWS_LAMBDA_LOG_GROUP_NAME":       LogGroupName(r.spec.Name),
		"AWS_LAMBDA_LOG_STREAM_NAME":      stream,
		"AWS_LAMBDA_INITIALIZATION_TYPE":  "on-demand",
		"AWS_EXECUTION_ENV":               "AWS_Lambda_" + executionEnv(r.spec.Runtime),
		"LAMBDA_TASK_ROOT":                taskRoot,
		"LAMBDA_RUNTIME_DIR":              r.spec.ShimDir,
		"AWS_REGION":                      r.spec.Identity.RegionName(),
		"AWS_DEFAULT_REGION":              r.spec.Identity.RegionName(),
		"AWS_ACCESS_KEY_ID":               awsident.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY":           awsident.SecretAccessKey,
		"AWS_SESSION_TOKEN":               "test",
		"TZ":                              "UTC",
		"LANG":                            "en_US.UTF-8",
		// Unbuffered stdout is what makes a print land before the REPORT
		// line that closes its invocation, which is what attribution needs.
		"PYTHONUNBUFFERED": "1",
	}
	if r.spec.ShimDir == "" {
		delete(set, "LAMBDA_RUNTIME_DIR")
	}
	for k, v := range r.spec.Endpoints {
		set[k] = v
	}
	for k, v := range r.spec.Env {
		set[k] = v
	}
	// Layers go on the search paths after the function's own variables, so
	// a PYTHONPATH the function sets keeps precedence over a layer's.
	layerEnv(r.spec.LayerDirs, r.spec.Runtime, set)
	env := os.Environ()
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys) // a stable order makes a child's env diffable in a test
	for _, k := range keys {
		env = append(env, k+"="+set[k])
	}
	return env
}

// executionEnv is the AWS_EXECUTION_ENV suffix: "python3.12", "nodejs20.x",
// "provided.al2023" — the runtime identifier as AWS spells it there.
func executionEnv(runtime string) string {
	if runtime == "" || runtime == "go" {
		return "provided.al2023"
	}
	return strings.TrimSpace(runtime)
}
