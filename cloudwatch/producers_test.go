package cloudwatch_test

// The producers: services publishing AWS's own metrics without being asked.
//
// These are the metrics an alarm written against the cloud already names, so
// producing them is what lets that alarm be tested before it is deployed. The
// test boots a real stack and invokes a real function, because the value here
// is entirely in the wiring — a unit test of the recording call would prove
// nothing about whether it reaches the store.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsapi "github.com/aws/aws-sdk-go-v2/service/apigateway"
	apitypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	awslogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"

	"github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/apigateway"
)

// bashFunction writes a provided.al2 function whose bootstrap is a shell
// script, so the test needs no interpreter beyond the one every machine has.
func bashFunction(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + `
while true; do
  HEADERS="$(mktemp)"
  EVENT=$(curl -sS -LD "$HEADERS" "http://${AWS_LAMBDA_RUNTIME_API}/2018-06-01/runtime/invocation/next")
  REQID=$(grep -Fi Lambda-Runtime-Aws-Request-Id "$HEADERS" | tr -d '[:space:]' | cut -d: -f2)
` + body + `
  curl -sS -X POST "http://${AWS_LAMBDA_RUNTIME_API}/2018-06-01/runtime/invocation/$REQID/response" -d '{"ok":true}' >/dev/null
done
`
	path := filepath.Join(dir, "bootstrap")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// metricStack boots a stack with cloudwatch and whatever else the caller
// names, and hands back the endpoint and a config addressed at it.
func metricStack(t *testing.T, services ...string) (string, aws.Config) {
	t.Helper()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  t.TempDir(),
		Services: append([]string{"cloudwatch"}, services...),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	return ts.URL, aws.Config{Region: "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", "")}
}

func producerStack(t *testing.T) (*awscw.Client, *awslambda.Client, *awslogs.Client, context.Context) {
	t.Helper()
	url, cfg := metricStack(t, "lambda", "logs")
	return awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(url) }),
		awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(url) }),
		awslogs.NewFromConfig(cfg, func(o *awslogs.Options) { o.BaseEndpoint = aws.String(url) }),
		context.Background()
}

// waitForMetric polls until a metric appears, since publishing is off the
// caller's path and deliberately batched.
func waitForMetric(t *testing.T, c *awscw.Client, ctx context.Context,
	namespace, name string, dims []cwtypes.Dimension) float64 {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, err := c.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
			Namespace:  aws.String(namespace),
			MetricName: aws.String(name),
			Dimensions: dims,
			StartTime:  aws.Time(time.Now().Add(-time.Hour)),
			EndTime:    aws.Time(time.Now().Add(time.Hour)),
			Period:     aws.Int32(7200),
			Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
		})
		if err == nil && len(out.Datapoints) > 0 {
			return aws.ToFloat64(out.Datapoints[0].Sum)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s/%s never appeared", namespace, name)
	return 0
}

// waitForMetricSum polls until a metric's sum reaches want, for the cases
// where the metric already exists and the question is whether a LATER
// publication landed — waitForMetric would answer with the earlier value.
func waitForMetricSum(t *testing.T, c *awscw.Client, ctx context.Context,
	namespace, name string, dims []cwtypes.Dimension, want float64) {
	t.Helper()
	var last float64
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, err := c.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
			Namespace:  aws.String(namespace),
			MetricName: aws.String(name),
			Dimensions: dims,
			StartTime:  aws.Time(time.Now().Add(-time.Hour)),
			EndTime:    aws.Time(time.Now().Add(time.Hour)),
			Period:     aws.Int32(7200),
			Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
		})
		if err == nil && len(out.Datapoints) > 0 {
			if last = aws.ToFloat64(out.Datapoints[0].Sum); last == want {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("%s/%s sum = %v, want %v", namespace, name, last, want)
}

// A Lambda invocation raises AWS/Lambda Invocations, dimensioned by
// FunctionName — the metric almost every stack's first alarm watches.
func TestLambdaPublishesInvocationMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a full stack and runs a function")
	}
	cwC, lamC, _, ctx := producerStack(t)
	code := bashFunction(t, `  echo "handled $REQID"`)

	if _, err := lamC.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("worker"),
		Runtime:      lambdatypes.RuntimeProvidedal2,
		Handler:      aws.String("bootstrap"),
		Role:         aws.String("arn:aws:iam::000000000000:role/x"),
		Code:         &lambdatypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(code)},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	for range 2 {
		if _, err := lamC.Invoke(ctx, &awslambda.InvokeInput{
			FunctionName: aws.String("worker"), Payload: []byte(`{}`)}); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
	}

	dims := []cwtypes.Dimension{{Name: aws.String("FunctionName"), Value: aws.String("worker")}}
	if got := waitForMetric(t, cwC, ctx, "AWS/Lambda", "Invocations", dims); got != 2 {
		t.Errorf("Invocations = %v, want 2", got)
	}
	// Duration is published too, and is a real measurement rather than zero.
	if got := waitForMetric(t, cwC, ctx, "AWS/Lambda", "Duration", dims); got <= 0 {
		t.Errorf("Duration = %v, want a positive measurement", got)
	}
	// The undimensioned copy is what an account-wide alarm watches.
	if got := waitForMetric(t, cwC, ctx, "AWS/Lambda", "Invocations", nil); got != 2 {
		t.Errorf("undimensioned Invocations = %v, want 2", got)
	}
}

// An EMF line a function prints becomes a custom metric, with no SDK call.
// This is the whole mechanism: the log line IS the publication.
func TestLambdaEMFBecomesCustomMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a full stack and runs a function")
	}
	cwC, lamC, _, ctx := producerStack(t)
	// The function prints one EMF line naming a metric in its own namespace,
	// dimensioned two ways — per stage and undimensioned.
	emfLine := `{"_aws":{"CloudWatchMetrics":[{"Namespace":"Shop",` +
		`"Dimensions":[["Stage"],[]],"Metrics":[{"Name":"Checkouts","Unit":"Count"}]}]},` +
		`"Stage":"prod","Checkouts":7}`
	code := bashFunction(t, `  echo '`+emfLine+`'`)

	if _, err := lamC.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("emitter"),
		Runtime:      lambdatypes.RuntimeProvidedal2,
		Handler:      aws.String("bootstrap"),
		Role:         aws.String("arn:aws:iam::000000000000:role/x"),
		Code:         &lambdatypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(code)},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	if _, err := lamC.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("emitter"), Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	dims := []cwtypes.Dimension{{Name: aws.String("Stage"), Value: aws.String("prod")}}
	if got := waitForMetric(t, cwC, ctx, "Shop", "Checkouts", dims); got != 7 {
		t.Errorf("Shop/Checkouts{Stage=prod} = %v, want 7", got)
	}
	// The second dimension set in the same directive.
	if got := waitForMetric(t, cwC, ctx, "Shop", "Checkouts", nil); got != 7 {
		t.Errorf("undimensioned Shop/Checkouts = %v, want 7", got)
	}

	// And it is still a log line: EMF is a publication IN ADDITION to being
	// output, never instead of it.
	metrics, err := cwC.ListMetrics(ctx, &awscw.ListMetricsInput{Namespace: aws.String("Shop")})
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics.Metrics) != 2 {
		t.Errorf("want the two dimension sets as two metrics, got %d", len(metrics.Metrics))
	}
}

// A log line matching a metric filter increments a metric. This is the third
// way a metric is born — not an SDK call, not an EMF directive the writer
// chose, but a rule the operator attached to the group afterwards, over logs
// the application had no idea would be measured.
func TestMetricFilterTurnsLogLinesIntoMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a full stack")
	}
	cwC, _, logC, ctx := producerStack(t)
	const group = "/audit/app"

	if _, err := logC.CreateLogGroup(ctx, &awslogs.CreateLogGroupInput{
		LogGroupName: aws.String(group)}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}
	if _, err := logC.CreateLogStream(ctx, &awslogs.CreateLogStreamInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String("main")}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}
	// Two filters over the same group: one counting occurrences with a
	// literal, one extracting a number out of the line with $.latency.
	for _, f := range []struct {
		name, pattern, metric, value string
	}{
		{"errors", "ERROR", "ErrorCount", "1"},
		{"latency", "{$.latency = *}", "Latency", "$.latency"},
	} {
		if _, err := logC.PutMetricFilter(ctx, &awslogs.PutMetricFilterInput{
			LogGroupName:  aws.String(group),
			FilterName:    aws.String(f.name),
			FilterPattern: aws.String(f.pattern),
			MetricTransformations: []logstypes.MetricTransformation{{
				MetricNamespace: aws.String("App"),
				MetricName:      aws.String(f.metric),
				MetricValue:     aws.String(f.value),
			}},
		}); err != nil {
			t.Fatalf("PutMetricFilter %s: %v", f.name, err)
		}
	}

	now := time.Now().UnixMilli()
	if _, err := logC.PutLogEvents(ctx, &awslogs.PutLogEventsInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String("main"),
		LogEvents: []logstypes.InputLogEvent{
			{Timestamp: aws.Int64(now), Message: aws.String("ERROR could not reach the database")},
			{Timestamp: aws.Int64(now + 1), Message: aws.String("INFO all is well")},
			{Timestamp: aws.Int64(now + 2), Message: aws.String("ERROR still cannot reach it")},
			{Timestamp: aws.Int64(now + 3), Message: aws.String(`{"latency": 40}`)},
			{Timestamp: aws.Int64(now + 4), Message: aws.String(`{"latency": 2}`)},
		},
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	// Two ERROR lines out of five, so the literal counter is 2 — not 5, which
	// is what a filter that matched everything would give.
	if got := waitForMetric(t, cwC, ctx, "App", "ErrorCount", nil); got != 2 {
		t.Errorf("App/ErrorCount = %v, want 2", got)
	}
	// And the extracted values are the numbers the lines carried, not counts.
	if got := waitForMetric(t, cwC, ctx, "App", "Latency", nil); got != 42 {
		t.Errorf("App/Latency sum = %v, want 42", got)
	}

	// A deleted filter stops producing, and the cache does not outlive it.
	if _, err := logC.DeleteMetricFilter(ctx, &awslogs.DeleteMetricFilterInput{
		LogGroupName: aws.String(group), FilterName: aws.String("errors")}); err != nil {
		t.Fatalf("DeleteMetricFilter: %v", err)
	}
	if _, err := logC.PutLogEvents(ctx, &awslogs.PutLogEventsInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String("main"),
		LogEvents: []logstypes.InputLogEvent{
			{Timestamp: aws.Int64(now + 5), Message: aws.String("ERROR one more")},
		},
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}
	// The surviving filter is the observable proof the second batch was
	// evaluated at all, so a still-2 ErrorCount means deleted, not unread.
	if _, err := logC.PutLogEvents(ctx, &awslogs.PutLogEventsInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String("main"),
		LogEvents: []logstypes.InputLogEvent{
			{Timestamp: aws.Int64(now + 6), Message: aws.String(`{"latency": 5}`)},
		},
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}
	waitForMetricSum(t, cwC, ctx, "App", "Latency", nil, 47)
	if got := waitForMetric(t, cwC, ctx, "App", "ErrorCount", nil); got != 2 {
		t.Errorf("App/ErrorCount after deleting the filter = %v, want it still 2", got)
	}
}

// A served request raises AWS/ApiGateway Count and, when it fails, the error
// metric for its class — the pair almost every API's first alarm watches.
func TestAPIGatewayPublishesRequestMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a full stack")
	}
	url, cfg := metricStack(t, "apigateway")
	cwC := awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(url) })
	apiC := awsapi.NewFromConfig(cfg, func(o *awsapi.Options) { o.BaseEndpoint = aws.String(url) })
	ctx := context.Background()

	api, err := apiC.CreateRestApi(ctx, &awsapi.CreateRestApiInput{Name: aws.String("shop")})
	if err != nil {
		t.Fatalf("CreateRestApi: %v", err)
	}
	res, err := apiC.CreateResource(ctx, &awsapi.CreateResourceInput{
		RestApiId: api.Id, ParentId: api.RootResourceId, PathPart: aws.String("things")})
	if err != nil {
		t.Fatalf("CreateResource: %v", err)
	}
	// A MOCK integration, so the metric being tested is the gateway's own and
	// not something a Lambda behind it happened to do.
	if _, err := apiC.PutMethod(ctx, &awsapi.PutMethodInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
		AuthorizationType: aws.String("NONE")}); err != nil {
		t.Fatalf("PutMethod: %v", err)
	}
	if _, err := apiC.PutIntegration(ctx, &awsapi.PutIntegrationInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
		Type:             apitypes.IntegrationTypeMock,
		RequestTemplates: map[string]string{"application/json": `{"statusCode":200}`}}); err != nil {
		t.Fatalf("PutIntegration: %v", err)
	}
	if _, err := apiC.PutMethodResponse(ctx, &awsapi.PutMethodResponseInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
		StatusCode: aws.String("200")}); err != nil {
		t.Fatalf("PutMethodResponse: %v", err)
	}
	if _, err := apiC.PutIntegrationResponse(ctx, &awsapi.PutIntegrationResponseInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
		StatusCode:        aws.String("200"),
		ResponseTemplates: map[string]string{"application/json": `{"ok":true}`}}); err != nil {
		t.Fatalf("PutIntegrationResponse: %v", err)
	}
	if _, err := apiC.CreateDeployment(ctx, &awsapi.CreateDeploymentInput{
		RestApiId: api.Id, StageName: aws.String("dev")}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	base := url + apigateway.ExecutePrefix + aws.ToString(api.Id) + "/dev"
	for range 3 {
		resp, err := http.Get(base + "/things")
		if err != nil {
			t.Fatalf("GET /things: %v", err)
		}
		resp.Body.Close()
	}
	// A path nothing is wired to: the gateway answers 403, which is a 4XX.
	resp, err := http.Get(base + "/nothing-here")
	if err != nil {
		t.Fatalf("GET /nothing-here: %v", err)
	}
	resp.Body.Close()

	// AWS dimensions a REST API's metrics by ApiName and Stage — the API's
	// name, not its generated id, which is why an alarm survives a redeploy.
	dims := []cwtypes.Dimension{
		{Name: aws.String("ApiName"), Value: aws.String("shop")},
		{Name: aws.String("Stage"), Value: aws.String("dev")},
	}
	if got := waitForMetric(t, cwC, ctx, "AWS/ApiGateway", "Count", dims); got != 4 {
		t.Errorf("Count = %v, want 4 (three served and one refused)", got)
	}
	if got := waitForMetric(t, cwC, ctx, "AWS/ApiGateway", "4XXError", dims); got != 1 {
		t.Errorf("4XXError = %v, want 1", got)
	}
	// Latency is published for every request, and is a real measurement.
	if got := waitForMetric(t, cwC, ctx, "AWS/ApiGateway", "Latency", dims); got < 0 {
		t.Errorf("Latency = %v, want a measurement", got)
	}
	// Nothing served a 5XX, so that metric must not exist — a producer that
	// published zeroes would make every error-rate alarm read as healthy
	// rather than as having no data.
	out, err := cwC.ListMetrics(ctx, &awscw.ListMetricsInput{
		Namespace: aws.String("AWS/ApiGateway"), MetricName: aws.String("5XXError")})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Metrics) != 0 {
		t.Errorf("5XXError exists with %d series, want none", len(out.Metrics))
	}
}

// An execution raises AWS/States ExecutionsStarted and the terminal metric for
// how it ended, dimensioned by StateMachineArn.
func TestStepFunctionsPublishesExecutionMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a full stack")
	}
	url, cfg := metricStack(t, "stepfunctions")
	cwC := awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(url) })
	sfnC := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(url) })
	ctx := context.Background()

	// Two machines: one that succeeds and one that fails, so the terminal
	// metrics are told apart rather than merely being non-zero.
	arns := map[string]string{}
	for name, def := range map[string]string{
		"good": `{"StartAt":"Done","States":{"Done":{"Type":"Pass","End":true}}}`,
		"bad": `{"StartAt":"Boom","States":{"Boom":{"Type":"Fail",` +
			`"Error":"Nope","Cause":"on purpose"}}}`,
	} {
		out, err := sfnC.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
			Name: aws.String(name), Definition: aws.String(def),
			RoleArn: aws.String("arn:aws:iam::000000000000:role/x")})
		if err != nil {
			t.Fatalf("CreateStateMachine %s: %v", name, err)
		}
		arns[name] = aws.ToString(out.StateMachineArn)
	}

	for range 2 {
		if _, err := sfnC.StartExecution(ctx, &awssfn.StartExecutionInput{
			StateMachineArn: aws.String(arns["good"]), Input: aws.String(`{}`)}); err != nil {
			t.Fatalf("StartExecution good: %v", err)
		}
	}
	if _, err := sfnC.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: aws.String(arns["bad"]), Input: aws.String(`{}`)}); err != nil {
		t.Fatalf("StartExecution bad: %v", err)
	}

	good := []cwtypes.Dimension{{Name: aws.String("StateMachineArn"), Value: aws.String(arns["good"])}}
	bad := []cwtypes.Dimension{{Name: aws.String("StateMachineArn"), Value: aws.String(arns["bad"])}}
	if got := waitForMetric(t, cwC, ctx, "AWS/States", "ExecutionsStarted", good); got != 2 {
		t.Errorf("good ExecutionsStarted = %v, want 2", got)
	}
	if got := waitForMetric(t, cwC, ctx, "AWS/States", "ExecutionsSucceeded", good); got != 2 {
		t.Errorf("ExecutionsSucceeded = %v, want 2", got)
	}
	if got := waitForMetric(t, cwC, ctx, "AWS/States", "ExecutionsFailed", bad); got != 1 {
		t.Errorf("ExecutionsFailed = %v, want 1", got)
	}
	// The failing machine's executions must not appear as the other's — this
	// is the dimension bug Moto has, and the one alarms cannot tolerate.
	out, err := cwC.ListMetrics(ctx, &awscw.ListMetricsInput{
		Namespace: aws.String("AWS/States"), MetricName: aws.String("ExecutionsFailed")})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Metrics) != 1 {
		t.Errorf("ExecutionsFailed has %d series, want only the failing machine's", len(out.Metrics))
	}
}

// A stack without CloudWatch enabled must not pay for metrics it is not
// collecting, and must not fail the work that would have produced them.
func TestProducersTolerateCloudWatchBeingOff(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack and runs a function")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(),
		// Every producer, and no cloudwatch to receive what they produce.
		Services: []string{"lambda", "logs", "apigateway", "stepfunctions"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()
	cfg := aws.Config{Region: "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", "")}
	lamC := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	code := bashFunction(t, `  echo "fine"`)
	if _, err := lamC.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("worker"),
		Runtime:      lambdatypes.RuntimeProvidedal2,
		Handler:      aws.String("bootstrap"),
		Role:         aws.String("arn:aws:iam::000000000000:role/x"),
		Code:         &lambdatypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(code)},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := lamC.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("worker"), Payload: []byte(`{}`)})
	if err != nil {
		t.Fatalf("an invocation failed with CloudWatch disabled: %v", err)
	}
	if out.StatusCode != 200 {
		t.Errorf("status = %d", out.StatusCode)
	}

	// And a workflow runs to completion, which is the producer that fires on
	// a goroutine of its own rather than on the caller's path — the one where
	// a blocking publish would hang rather than merely slow something down.
	sfnC := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	machine, err := sfnC.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("quiet"),
		Definition: aws.String(`{"StartAt":"Done","States":{"Done":{"Type":"Pass","End":true}}}`),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/x")})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}
	exec, err := sfnC.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: machine.StateMachineArn, Input: aws.String(`{}`)})
	if err != nil {
		t.Fatalf("an execution failed to start with CloudWatch disabled: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := sfnC.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{
			ExecutionArn: exec.ExecutionArn})
		if err != nil {
			t.Fatalf("DescribeExecution: %v", err)
		}
		if got.Status == "SUCCEEDED" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the execution never finished with CloudWatch disabled")
}
