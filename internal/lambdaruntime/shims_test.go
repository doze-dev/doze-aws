package lambdaruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The embedded runtime clients, driven for real: each interpreter on PATH
// runs a handler through its shim and the Runner, and the test reads what
// AWS's own client would have given the handler — the context fields, the
// error shape, the log attribution. A missing interpreter skips its case.

type shimCase struct {
	family, runtime, handler string
	files                    map[string]string
	envForNode               bool
}

// The handlers: echo the event plus the context, raise on {"fail":true},
// print at import time and per invocation.
var shimCases = []shimCase{
	{family: "python", runtime: "python3.12", handler: "src/app.handler", files: map[string]string{
		"src/__init__.py": "",
		"src/app.py": `import logging, sys
print("python init")
log = logging.getLogger()
class Boom(Exception):
    pass
def handler(event, context):
    print("handling", event)
    log.warning("a warning")
    if event.get("fail"):
        raise Boom("asked to fail")
    return {"event": event, "rid": context.aws_request_id, "fn": context.function_name,
            "version": context.function_version, "arn": context.invoked_function_arn,
            "mem": context.memory_limit_in_mb, "group": context.log_group_name,
            "stream": context.log_stream_name, "left": context.get_remaining_time_in_millis(),
            "cc": context.client_context.custom if context.client_context else None}
`,
	}},
	{family: "nodejs", runtime: "nodejs20.x", handler: "src/index.handler", envForNode: true, files: map[string]string{
		"src/index.mjs": `console.log("node init");
export async function handler(event, context) {
  console.log("handling", JSON.stringify(event));
  console.warn("a warning");
  if (event.fail) { const e = new Error("asked to fail"); e.name = "Boom"; throw e; }
  return { event, rid: context.awsRequestId, fn: context.functionName, version: context.functionVersion,
    arn: context.invokedFunctionArn, mem: Number(context.memoryLimitInMB), group: context.logGroupName,
    stream: context.logStreamName, left: context.getRemainingTimeInMillis(), cc: context.clientContext?.custom ?? null };
}
`,
	}},
	{family: "nodejs", runtime: "nodejs18.x", handler: "legacy.handler", envForNode: true, files: map[string]string{
		// CommonJS with a callback: the older shape the RIC still accepts.
		"legacy.js": `console.log("node init");
exports.handler = function (event, context, callback) {
  console.log("handling", JSON.stringify(event));
  console.warn("a warning");
  if (event.fail) { const e = new Error("asked to fail"); e.name = "Boom"; return callback(e); }
  callback(null, { event, rid: context.awsRequestId, fn: context.functionName, version: context.functionVersion,
    arn: context.invokedFunctionArn, mem: Number(context.memoryLimitInMB), group: context.logGroupName,
    stream: context.logStreamName, left: context.getRemainingTimeInMillis(), cc: (context.clientContext || {}).custom || null });
};
`,
	}},
	{family: "ruby", runtime: "ruby3.3", handler: "app.Handler.process", files: map[string]string{
		"app.rb": `puts "ruby init"
class Boom < StandardError; end
module Handler
  def self.process(event:, context:)
    puts "handling #{event.to_json}"
    warn "a warning"
    raise Boom, "asked to fail" if event["fail"]
    { "event" => event, "rid" => context.aws_request_id, "fn" => context.function_name,
      "version" => context.function_version, "arn" => context.invoked_function_arn,
      "mem" => context.memory_limit_in_mb, "group" => context.log_group_name,
      "stream" => context.log_stream_name, "left" => context.get_remaining_time_in_millis,
      "cc" => context.client_context && context.client_context["custom"] }
  end
end
`,
	}},
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestShimsRunRealHandlers(t *testing.T) {
	if testing.Short() {
		t.Skip("runs interpreters")
	}
	shims, err := Materialize(filepath.Join(t.TempDir(), "shims"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range shimCases {
		c := c
		t.Run(c.runtime, func(t *testing.T) {
			if _, err := (Interpreters{}).Resolve(c.family); err != nil {
				t.Skipf("no %s interpreter: %v", c.family, err)
			}
			if c.envForNode && os.Getenv("PROTO_NODE_VERSION") == "" && os.Getenv("CI") == "" {
				// The proto shim needs a pinned version to run node at all.
				os.Setenv("PROTO_NODE_VERSION", "26.8.1")
			}
			dir := writeFiles(t, c.files)
			sink := &recordingSink{}
			r := NewRunner(Spec{Name: "shimmed", Runtime: c.runtime, Handler: c.handler, Dir: dir, ShimDir: shims,
				Timeout: 10 * time.Second, MemorySize: 256, Version: "3", LogSink: sink}, t.Logf)
			defer r.Stop()

			res, err := r.InvokeInput(context.Background(), Input{Payload: []byte(`{"n":1}`),
				InvokedARN: FunctionARN("shimmed") + ":3", ClientContext: `{"custom":{"k":"v"}}`})
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			if res.FunctionErr != "" {
				t.Fatalf("handler failed: %s\nlogs:\n%s", res.Payload, res.Logs)
			}
			var out struct {
				Event   map[string]any `json:"event"`
				RID     string         `json:"rid"`
				Fn      string         `json:"fn"`
				Version string         `json:"version"`
				ARN     string         `json:"arn"`
				Mem     int            `json:"mem"`
				Group   string         `json:"group"`
				Stream  string         `json:"stream"`
				Left    int            `json:"left"`
				CC      map[string]any `json:"cc"`
			}
			if err := json.Unmarshal(res.Payload, &out); err != nil {
				t.Fatalf("payload %s: %v", res.Payload, err)
			}
			if out.Event["n"] != float64(1) || out.RID != res.RequestID || out.Fn != "shimmed" || out.Version != "3" ||
				out.ARN != FunctionARN("shimmed")+":3" || out.Mem != 256 || out.Group != "/aws/lambda/shimmed" ||
				out.Stream != r.Stream() || out.Left <= 0 || out.Left > 10000 || out.CC["k"] != "v" {
				t.Errorf("context as the handler saw it: %+v (payload %s)", out, res.Payload)
			}

			// A raise is a function error with the client's error shape.
			fail, err := r.Invoke(context.Background(), []byte(`{"fail":true}`))
			if err != nil {
				t.Fatal(err)
			}
			if fail.FunctionErr != "Unhandled" {
				t.Fatalf("a raising handler should be Unhandled: %s", fail.Payload)
			}
			var ferr map[string]any
			json.Unmarshal(fail.Payload, &ferr)
			if ferr["errorType"] != "Boom" || !strings.Contains(ferr["errorMessage"].(string), "asked to fail") {
				t.Errorf("error shape = %s", fail.Payload)
			}
			if _, hasTrace := ferr["stackTrace"]; !hasTrace {
				if _, hasNodeTrace := ferr["trace"]; !hasNodeTrace {
					t.Errorf("error should carry a stack: %s", fail.Payload)
				}
			}

			// Attribution: init output before any request, each invocation's
			// prints and warnings under its own id, and the error logged.
			lines, _ := sink.snapshot()
			byRID := map[string][]string{}
			for _, l := range lines {
				byRID[l.rid] = append(byRID[l.rid], l.text)
			}
			if init := strings.Join(byRID[""], "|"); !strings.Contains(init, c.family+" init") && !strings.Contains(init, "node init") {
				t.Errorf("init output should carry no request id: %q", init)
			}
			first := strings.Join(byRID[res.RequestID], "\n")
			if !strings.Contains(first, "handling") || !strings.Contains(first, "a warning") || !strings.Contains(first, "REPORT RequestId: "+res.RequestID) {
				t.Errorf("first invocation's lines:\n%s", first)
			}
			second := strings.Join(byRID[fail.RequestID], "\n")
			if !strings.Contains(second, "Boom") || !strings.Contains(second, "asked to fail") {
				t.Errorf("the failure should be logged under its request:\n%s", second)
			}
		})
	}
}

// TestShimImportErrorIsAnInitError: a handler that will not import fails the
// queued invocation with the import error, at once, instead of timing out.
func TestShimImportErrorIsAnInitError(t *testing.T) {
	if testing.Short() {
		t.Skip("runs interpreters")
	}
	if _, err := (Interpreters{}).Resolve("python"); err != nil {
		t.Skip(err)
	}
	shims, _ := Materialize(filepath.Join(t.TempDir(), "shims"))
	dir := writeFiles(t, map[string]string{"broken.py": "import does_not_exist\n"})
	sink := &recordingSink{}
	r := NewRunner(Spec{Name: "broken", Runtime: "python3.12", Handler: "broken.handler", Dir: dir, ShimDir: shims, LogSink: sink}, t.Logf)
	defer r.Stop()
	start := time.Now()
	res, err := r.Invoke(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.FunctionErr == "" || !strings.Contains(string(res.Payload), "exited") {
		t.Errorf("an import failure should fail the invocation with the exit: %s", res.Payload)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("the failure took %v; it should not wait out the init budget", time.Since(start))
	}
	lines, _ := sink.snapshot()
	all := ""
	for _, l := range lines {
		all += l.text + "\n"
	}
	if !regexp.MustCompile(`Unable to import module 'broken'`).MatchString(all) {
		t.Errorf("the import error should be in the logs:\n%s", all)
	}
}

// TestShimJSONLogFormat: AWS_LAMBDA_LOG_FORMAT=JSON turns Python's logging
// into one JSON record per line, with the request id.
func TestShimJSONLogFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("runs interpreters")
	}
	if _, err := (Interpreters{}).Resolve("python"); err != nil {
		t.Skip(err)
	}
	shims, _ := Materialize(filepath.Join(t.TempDir(), "shims"))
	dir := writeFiles(t, map[string]string{"app.py": "import logging\ndef handler(event, context):\n    logging.getLogger().info('structured %s', 'x')\n    return 1\n"})
	sink := &recordingSink{}
	r := NewRunner(Spec{Name: "json", Runtime: "python3.12", Handler: "app.handler", Dir: dir, ShimDir: shims, LogSink: sink,
		Env: map[string]string{"AWS_LAMBDA_LOG_FORMAT": "JSON"}}, t.Logf)
	defer r.Stop()
	res, err := r.Invoke(context.Background(), []byte(`{}`))
	if err != nil || res.FunctionErr != "" {
		t.Fatalf("%v %s", err, res.Payload)
	}
	lines, _ := sink.snapshot()
	found := false
	for _, l := range lines {
		var rec map[string]any
		if json.Unmarshal([]byte(l.text), &rec) == nil && rec["message"] == "structured x" {
			found = true
			if rec["level"] != "INFO" || rec["requestId"] != res.RequestID {
				t.Errorf("JSON record = %s", l.text)
			}
		}
	}
	if !found {
		t.Errorf("no JSON record found in:\n%+v", lines)
	}
}
