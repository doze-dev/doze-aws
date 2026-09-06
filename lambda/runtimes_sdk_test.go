package lambda_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// A Python and a Node function deployed the way a deploy tool deploys them —
// a zip through CreateFunction — run on the host interpreter through the
// embedded runtime clients, with nothing installed. Skipped where the
// interpreter is absent.

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(body))
	}
	w.Close()
	return buf.Bytes()
}

func TestInterpretedRuntimesRunFromAZip(t *testing.T) {
	if testing.Short() {
		t.Skip("runs interpreters")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := newTestServer(t, stack.Handler())
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	lc := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts) })
	logs := cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts) })

	cases := []struct {
		name, interp, handler string
		runtime               lambdatypes.Runtime
		files                 map[string]string
	}{
		{"py", "python3", "handler.main", lambdatypes.RuntimePython312, map[string]string{
			"handler.py": "import os\ndef main(event, context):\n    print('py saw', event['who'])\n    return {'hello': event['who'], 'env': os.environ['GREETING'], 'root': os.environ['LAMBDA_TASK_ROOT'] != ''}\n",
		}},
		{"node", "node", "index.handler", lambdatypes.RuntimeNodejs20x, map[string]string{
			"index.mjs": "export const handler = async (event) => { console.log('node saw', event.who); return { hello: event.who, env: process.env.GREETING, root: !!process.env.LAMBDA_TASK_ROOT }; };\n",
		}},
		{"rb", "ruby", "func.handler", lambdatypes.RuntimeRuby33, map[string]string{
			"func.rb": "def handler(event:, context:)\n  puts \"ruby saw #{event['who']}\"\n  { 'hello' => event['who'], 'env' => ENV['GREETING'], 'root' => !ENV['LAMBDA_TASK_ROOT'].to_s.empty? }\nend\n",
		}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, err := exec.LookPath(c.interp); err != nil {
				t.Skipf("no %s on PATH", c.interp)
			}
			if _, err := lc.CreateFunction(ctx, &awslambda.CreateFunctionInput{
				FunctionName: aws.String("fn-" + c.name), Runtime: c.runtime, Handler: aws.String(c.handler),
				Role:        aws.String("arn:aws:iam::000000000000:role/x"),
				Code:        &lambdatypes.FunctionCode{ZipFile: zipOf(t, c.files)},
				Environment: &lambdatypes.Environment{Variables: map[string]string{"GREETING": "hi", "PROTO_NODE_VERSION": "26.8.1"}},
				Timeout:     aws.Int32(10),
			}); err != nil {
				t.Fatal(err)
			}
			out, err := lc.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("fn-" + c.name), Payload: []byte(`{"who":"ada"}`)})
			if err != nil {
				t.Fatal(err)
			}
			if out.FunctionError != nil {
				t.Fatalf("function error: %s", out.Payload)
			}
			if got := string(out.Payload); !strings.Contains(got, `"hello":"ada"`) && !strings.Contains(got, `"hello": "ada"`) || !strings.Contains(got, `"env":"hi"`) && !strings.Contains(got, `"env": "hi"`) || !strings.Contains(got, `"root":true`) && !strings.Contains(got, `"root": true`) {
				t.Errorf("payload = %s", got)
			}
			// The print reached the logs service under the function's group.
			deadline := time.Now().Add(5 * time.Second)
			for {
				res, err := logs.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String("/aws/lambda/fn-" + c.name), FilterPattern: aws.String("saw ada")})
				if err == nil && len(res.Events) == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the function's print never reached the logs service (%v)", err)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}

// TestMissingInterpreterIsSaidAtCreate: a runtime whose interpreter is not
// here is warned about when the function is created, and the first invoke
// fails with the same message rather than a timeout.
func TestMissingInterpreterIsSaidAtCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	ctx := context.Background()
	var lines []string
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Services: []string{"lambda"},
		LambdaRuntimes: map[string]string{"ruby": "/nonexistent/ruby"},
		Logf:           func(format string, args ...any) { lines = append(lines, sprintf(format, args...)) }})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := newTestServer(t, stack.Handler())
	lc := awslambda.NewFromConfig(aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", "")},
		func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts) })
	if _, err := lc.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("rb"), Runtime: lambdatypes.RuntimeRuby33, Handler: aws.String("f.handler"),
		Role: aws.String("arn:aws:iam::000000000000:role/x"),
		Code: &lambdatypes.FunctionCode{ZipFile: zipOf(t, map[string]string{"f.rb": "def handler(event:, context:)\n 1\nend\n"})},
	}); err != nil {
		t.Fatalf("AWS accepts the create; so should this: %v", err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "[lambda.runtimes] ruby") {
		t.Errorf("the create should warn about the interpreter:\n%s", strings.Join(lines, "\n"))
	}
	start := time.Now()
	// A launch failure is a function error, as a broken init is on AWS —
	// not a 500 the SDK would retry three times.
	out, err := lc.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("rb"), Payload: []byte(`{}`)})
	if err != nil || out.FunctionError == nil || !strings.Contains(string(out.Payload), "[lambda.runtimes] ruby") || !strings.Contains(string(out.Payload), "Runtime.LaunchError") {
		t.Errorf("the invoke should answer a function error naming the config key, got %v / %s", err, out.Payload)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("the failure took %v; a missing interpreter is known before the process starts", time.Since(start))
	}
}
