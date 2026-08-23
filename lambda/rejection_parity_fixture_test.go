package lambda_test

// The state the Lambda baselines address, and the preconditions each mutating
// operation needs.
//
// Lambda's fixture costs more than any other service's: a function needs real
// deployable code, and a version, alias, layer, permission or event source
// mapping needs a function first. The code is built once and every throwaway
// function points at the same directory — the audit is about what the service
// refuses, not about what the handler prints.

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fnName  = "audit-fn"
	alias   = "audit-alias"
	layer   = "audit-layer"
	roleARN = "arn:aws:iam::000000000000:role/audit-role"
	stmtID  = "audit-statement"
)

// fx is the state the baselines address.
type fx struct {
	codeDir string
	fnARN   string
	version string
	uuid    string
	layerV  string
}

func mk(t *testing.T, ts *httptest.Server, op string, body map[string]any) string {
	t.Helper()
	code, resp := call(t, ts, bindingFor(t, op), body)
	if code < 200 || code > 299 {
		t.Fatalf("fixture %s = %d: %s", op, code, resp)
	}
	return resp
}

// bindingFor reads an operation's REST binding out of the committed cases,
// rather than restating it here — the harness and the service must agree with
// the model, not with each other.
func bindingFor(t *testing.T, op string) *httpBinding {
	t.Helper()
	for _, c := range loadCases(t) {
		if c.Operation == op {
			return c.HTTP
		}
	}
	t.Fatalf("no binding for %s", op)
	return nil
}

func field(t *testing.T, resp, name string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(resp), &m); err != nil {
		t.Fatalf("response is not JSON: %s", resp)
	}
	switch v := m[name].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%d", int64(v))
	}
	t.Fatalf("response has no %s: %s", name, resp)
	return ""
}

// layerZip is a tiny zip, enough for PublishLayerVersion to store something.
func layerZip(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("lib/note.txt")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("audit"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func code(dir string) map[string]any {
	return map[string]any{"S3Bucket": "_local_", "S3Key": dir}
}

func setUpFixture(t *testing.T, ts *httptest.Server) fx {
	t.Helper()
	f := fx{codeDir: buildBootstrap(t)}
	if _, err := os.Stat(filepath.Join(f.codeDir, "bootstrap")); err != nil {
		t.Fatal(err)
	}

	resp := mk(t, ts, "CreateFunction", map[string]any{
		"FunctionName": fnName, "Runtime": "provided.al2", "Handler": "bootstrap",
		"Role": roleARN, "Code": code(f.codeDir), "Timeout": 10,
	})
	f.fnARN = field(t, resp, "FunctionArn")

	resp = mk(t, ts, "PublishVersion", map[string]any{"FunctionName": fnName})
	f.version = field(t, resp, "Version")
	mk(t, ts, "CreateAlias", map[string]any{
		"FunctionName": fnName, "Name": alias, "FunctionVersion": f.version,
	})
	mk(t, ts, "AddPermission", map[string]any{
		"FunctionName": fnName, "StatementId": stmtID,
		"Action": "lambda:InvokeFunction", "Principal": "s3.amazonaws.com",
	})
	mk(t, ts, "PutFunctionConcurrency", map[string]any{
		"FunctionName": fnName, "ReservedConcurrentExecutions": 1,
	})
	mk(t, ts, "PutFunctionEventInvokeConfig", map[string]any{
		"FunctionName": fnName, "MaximumRetryAttempts": 1,
	})
	mk(t, ts, "CreateFunctionUrlConfig", map[string]any{
		"FunctionName": fnName, "AuthType": "NONE",
	})
	mk(t, ts, "TagResource", map[string]any{
		"Resource": f.fnARN, "Tags": map[string]any{"env": "dev"},
	})

	resp = mk(t, ts, "PublishLayerVersion", map[string]any{
		"LayerName": layer, "Content": map[string]any{"ZipFile": layerZip(t)},
	})
	f.layerV = field(t, resp, "Version")

	resp = mk(t, ts, "CreateEventSourceMapping", map[string]any{
		"FunctionName":   fnName,
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:audit-q",
	})
	f.uuid = field(t, resp, "UUID")
	return f
}

func baselines(f fx) map[string]map[string]any {
	fn := map[string]any{"FunctionName": fnName}
	return map[string]map[string]any{
		"CreateFunction": {"FunctionName": "made-by-baseline", "Runtime": "provided.al2",
			"Handler": "bootstrap", "Role": roleARN, "Code": code(f.codeDir)},
		"ListFunctions":               {},
		"GetFunction":                 fn,
		"GetFunctionConfiguration":    fn,
		"UpdateFunctionConfiguration": {"FunctionName": fnName, "Description": "audited"},
		"UpdateFunctionCode":          {"FunctionName": fnName, "S3Bucket": "_local_", "S3Key": f.codeDir},
		"DeleteFunction":              {"FunctionName": "made-by-baseline"},
		"Invoke":                      {"FunctionName": fnName, "Payload": `{"ping":true}`},

		"PublishVersion":         fn,
		"ListVersionsByFunction": fn,

		"CreateAlias": {"FunctionName": fnName, "Name": "made-by-baseline", "FunctionVersion": f.version},
		"GetAlias":    {"FunctionName": fnName, "Name": alias},
		"ListAliases": fn,
		"DeleteAlias": {"FunctionName": fnName, "Name": "made-by-baseline"},

		"AddPermission": {"FunctionName": fnName, "StatementId": "made-by-baseline",
			"Action": "lambda:InvokeFunction", "Principal": "s3.amazonaws.com"},
		"GetPolicy":        fn,
		"RemovePermission": {"FunctionName": fnName, "StatementId": "made-by-baseline"},

		"PutFunctionConcurrency":    {"FunctionName": fnName, "ReservedConcurrentExecutions": 1},
		"GetFunctionConcurrency":    fn,
		"DeleteFunctionConcurrency": {"FunctionName": "made-by-baseline"},

		"PutFunctionCodeSigningConfig": {"FunctionName": fnName,
			"CodeSigningConfigArn": "arn:aws:lambda:us-east-1:000000000000:code-signing-config:csc-0123456789abcdef0"},
		"GetFunctionCodeSigningConfig":    fn,
		"DeleteFunctionCodeSigningConfig": {"FunctionName": fnName},

		"CreateFunctionUrlConfig": {"FunctionName": "made-by-baseline", "AuthType": "NONE"},
		"GetFunctionUrlConfig":    fn,
		"UpdateFunctionUrlConfig": {"FunctionName": fnName, "AuthType": "NONE"},
		"DeleteFunctionUrlConfig": {"FunctionName": "made-by-baseline"},
		"ListFunctionUrlConfigs":  fn,

		"PutFunctionEventInvokeConfig":    {"FunctionName": fnName, "MaximumRetryAttempts": 1},
		"GetFunctionEventInvokeConfig":    fn,
		"UpdateFunctionEventInvokeConfig": {"FunctionName": fnName, "MaximumRetryAttempts": 2},
		"DeleteFunctionEventInvokeConfig": {"FunctionName": "made-by-baseline"},
		"ListFunctionEventInvokeConfigs":  fn,

		"CreateEventSourceMapping": {"FunctionName": fnName,
			"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:audit-q"},
		"ListEventSourceMappings":  {},
		"GetEventSourceMapping":    {"UUID": f.uuid},
		"UpdateEventSourceMapping": {"UUID": f.uuid, "Enabled": true},
		"DeleteEventSourceMapping": {"UUID": "made-by-baseline"},

		"TagResource":   {"Resource": f.fnARN, "Tags": map[string]any{"team": "audit"}},
		"UntagResource": {"Resource": f.fnARN, "TagKeys": []any{"team"}},
		"ListTags":      {"Resource": f.fnARN},

		"PublishLayerVersion": {"LayerName": "made-by-baseline",
			"Content": map[string]any{"ZipFile": ""}},
		"ListLayers":         {},
		"ListLayerVersions":  {"LayerName": layer},
		"GetLayerVersion":    {"LayerName": layer, "VersionNumber": f.layerV},
		"DeleteLayerVersion": {"LayerName": "made-by-baseline", "VersionNumber": "1"},
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"Architectures[]":              []any{"x86_64"},
		"Layers[]":                     []any{"arn:aws:lambda:us-east-1:000000000000:layer:audit-layer:1"},
		"CompatibleRuntimes[]":         []any{"provided.al2"},
		"CompatibleArchitectures[]":    []any{"x86_64"},
		"TagKeys[]":                    []any{"env"},
		"Tags{}":                       "dev",
		"Environment":                  map[string]any{"Variables": map[string]any{"K": "V"}},
		"Environment.Variables{}":      "V",
		"VpcConfig":                    map[string]any{"SubnetIds": []any{"subnet-0123456789abcdef0"}},
		"VpcConfig.SubnetIds[]":        []any{"subnet-0123456789abcdef0"},
		"VpcConfig.SecurityGroupIds[]": []any{"sg-0123456789abcdef0"},
		"DeadLetterConfig":             map[string]any{"TargetArn": "arn:aws:sqs:us-east-1:000000000000:dlq"},
		"TracingConfig":                map[string]any{"Mode": "PassThrough"},
		"FileSystemConfigs[]":          []any{map[string]any{"Arn": "arn:aws:elasticfilesystem:us-east-1:000000000000:access-point/fsap-0123456789abcdef0", "LocalMountPath": "/mnt/audit"}},
		"ImageConfig":                  map[string]any{"Command": []any{"app.handler"}},
		"ImageConfig.Command[]":        []any{"app.handler"},
		"ImageConfig.EntryPoint[]":     []any{"/usr/bin/app"},
		"SnapStart":                    map[string]any{"ApplyOn": "None"},
		"LoggingConfig":                map[string]any{"LogFormat": "Text"},
		"EphemeralStorage":             map[string]any{"Size": 512},
		"RoutingConfig":                map[string]any{"AdditionalVersionWeights": map[string]any{"1": 0.5}},
		"RoutingConfig.AdditionalVersionWeights{}": 0.5,
		"Cors":                                 map[string]any{"AllowOrigins": []any{"*"}},
		"Cors.AllowOrigins[]":                  []any{"*"},
		"Cors.AllowMethods[]":                  []any{"GET"},
		"Cors.AllowHeaders[]":                  []any{"content-type"},
		"Cors.ExposeHeaders[]":                 []any{"date"},
		"Content":                              map[string]any{"S3Bucket": "audit", "S3Key": "layer.zip"},
		"DestinationConfig":                    map[string]any{"OnSuccess": map[string]any{"Destination": "arn:aws:sqs:us-east-1:000000000000:ok"}},
		"FilterCriteria":                       map[string]any{"Filters": []any{map[string]any{"Pattern": `{"a":[1]}`}}},
		"FilterCriteria.Filters[]":             []any{map[string]any{"Pattern": `{"a":[1]}`}},
		"Queues[]":                             []any{"audit-queue"},
		"Topics[]":                             []any{"audit-topic"},
		"SourceAccessConfigurations[]":         []any{map[string]any{"Type": "BASIC_AUTH", "URI": "arn:aws:secretsmanager:us-east-1:000000000000:secret:audit"}},
		"FunctionResponseTypes[]":              []any{"ReportBatchItemFailures"},
		"ScalingConfig":                        map[string]any{"MaximumConcurrency": 2},
		"DocumentDBEventSourceConfig":          map[string]any{"DatabaseName": "audit"},
		"AmazonManagedKafkaEventSourceConfig":  map[string]any{"ConsumerGroupId": "audit-group"},
		"SelfManagedKafkaEventSourceConfig":    map[string]any{"ConsumerGroupId": "audit-group"},
		"SelfManagedEventSource":               map[string]any{"Endpoints": map[string]any{"KAFKA_BOOTSTRAP_SERVERS": []any{"broker:9092"}}},
		"SelfManagedEventSource.Endpoints{}[]": []any{"broker:9092"},
		"ProvisionedPollerConfig":              map[string]any{"MinimumPollers": 1},
		"MetricsConfig":                        map[string]any{"Metrics": []any{"EventCount"}},
		"MetricsConfig.Metrics[]":              []any{"EventCount"},
		"DestinationConfig.OnFailure":          map[string]any{"Destination": "arn:aws:sqs:us-east-1:000000000000:dlq"},
		"AmazonManagedKafkaEventSourceConfig.SchemaRegistryConfig": map[string]any{"SchemaRegistryURI": "arn:aws:glue:us-east-1:000000000000:registry/audit"},
		"SelfManagedKafkaEventSourceConfig.SchemaRegistryConfig":   map[string]any{"SchemaRegistryURI": "https://registry.invalid/audit"},
		"DurableConfig": map[string]any{"DurableExecutionRetentionPeriodInDays": 1},
		"TenancyConfig": map[string]any{"TenantIsolationMode": "PER_TENANT"},
		"CapacityProviderConfig": map[string]any{
			"LambdaManagedInstancesCapacityProviderConfig": map[string]any{
				"CapacityProviderArn": "arn:aws:lambda:us-east-1:000000000000:capacity-provider:audit",
			},
		},
		"CapacityProviderConfig.LambdaManagedInstancesCapacityProviderConfig": map[string]any{
			"CapacityProviderArn": "arn:aws:lambda:us-east-1:000000000000:capacity-provider:audit",
		},
	}
}

// prepare gives each mutating case its own resource. A version, alias, layer
// version or permission statement lives inside a function, so a case that
// consumes one must not consume the fixture's.
func prepare(t *testing.T, ts *httptest.Server, f fx, op, mutating string, body map[string]any, n int) {
	t.Helper()
	// set fills a precondition, unless the case is ABOUT that field or about
	// something inside it. Overwriting Content because the case is on
	// Content.S3Bucket would replace the violating value with a valid one, and
	// the case would silently test nothing.
	set := func(k string, v any) {
		if mutating == k || strings.HasPrefix(mutating, k+".") ||
			strings.HasPrefix(mutating, k+"[") || strings.HasPrefix(mutating, k+"{") {
			return
		}
		body[k] = v
	}
	newFunction := func() string {
		name := fmt.Sprintf("fn-%d", n)
		mk(t, ts, "CreateFunction", map[string]any{
			"FunctionName": name, "Runtime": "provided.al2", "Handler": "bootstrap",
			"Role": roleARN, "Code": code(f.codeDir),
		})
		return name
	}

	switch op {
	case "CreateFunction":
		set("FunctionName", fmt.Sprintf("created-fn-%d", n))
	case "DeleteFunction":
		set("FunctionName", newFunction())

	case "CreateAlias":
		set("Name", fmt.Sprintf("created-alias-%d", n))
	case "DeleteAlias":
		name := fmt.Sprintf("doomed-alias-%d", n)
		mk(t, ts, "CreateAlias", map[string]any{
			"FunctionName": fnName, "Name": name, "FunctionVersion": f.version,
		})
		set("Name", name)

	case "AddPermission":
		set("StatementId", fmt.Sprintf("created-stmt-%d", n))
	case "RemovePermission":
		id := fmt.Sprintf("doomed-stmt-%d", n)
		mk(t, ts, "AddPermission", map[string]any{
			"FunctionName": fnName, "StatementId": id,
			"Action": "lambda:InvokeFunction", "Principal": "s3.amazonaws.com",
		})
		set("StatementId", id)

	case "DeleteFunctionConcurrency":
		name := newFunction()
		mk(t, ts, "PutFunctionConcurrency", map[string]any{
			"FunctionName": name, "ReservedConcurrentExecutions": 1,
		})
		set("FunctionName", name)

	case "CreateFunctionUrlConfig":
		set("FunctionName", newFunction())
	case "DeleteFunctionUrlConfig":
		name := newFunction()
		mk(t, ts, "CreateFunctionUrlConfig", map[string]any{
			"FunctionName": name, "AuthType": "NONE",
		})
		set("FunctionName", name)

	case "DeleteFunctionEventInvokeConfig":
		name := newFunction()
		mk(t, ts, "PutFunctionEventInvokeConfig", map[string]any{
			"FunctionName": name, "MaximumRetryAttempts": 1,
		})
		set("FunctionName", name)

	case "DeleteEventSourceMapping":
		resp := mk(t, ts, "CreateEventSourceMapping", map[string]any{
			"FunctionName":   fnName,
			"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:audit-q",
		})
		set("UUID", field(t, resp, "UUID"))

	case "PublishLayerVersion":
		set("LayerName", fmt.Sprintf("created-layer-%d", n))
		set("Content", map[string]any{"ZipFile": layerZip(t)})
	case "DeleteLayerVersion":
		name := fmt.Sprintf("doomed-layer-%d", n)
		resp := mk(t, ts, "PublishLayerVersion", map[string]any{
			"LayerName": name, "Content": map[string]any{"ZipFile": layerZip(t)},
		})
		set("LayerName", name)
		set("VersionNumber", field(t, resp, "Version"))
	}
}
