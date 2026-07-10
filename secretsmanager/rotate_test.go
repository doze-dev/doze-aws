package secretsmanager_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// rotationBootstrap records each rotation step it's invoked with to MARKER_FILE,
// proving Secrets Manager drove the four-step protocol against it.
const rotationBootstrap = `package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
)

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	marker := os.Getenv("MARKER_FILE")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil { os.Exit(1) }
		reqID := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var ev map[string]any
		json.Unmarshal(body, &ev)
		if step, _ := ev["Step"].(string); step != "" {
			f, _ := os.OpenFile(marker, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			f.WriteString(step + "\n")
			f.Close()
		}
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+reqID+"/response",
			"application/json", nil)
	}
}
`

func TestRotateSecretViaLambda(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a rotation lambda through a full stack")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	smc := awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	lamc := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	// Compile the rotation lambda.
	codeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(codeDir, "main.go"), []byte(rotationBootstrap), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(codeDir, "go.mod"), []byte("module rot\n\ngo 1.26\n"), 0o644)
	build := exec.Command("go", "build", "-o", filepath.Join(codeDir, "bootstrap"), ".")
	build.Dir = codeDir
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build rotation lambda: %v\n%s", err, out)
	}

	marker := filepath.Join(t.TempDir(), "steps.txt")
	if _, err := lamc.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("rotator"),
		Runtime:      lamtypes.RuntimeProvidedal2,
		Handler:      aws.String("bootstrap"),
		Role:         aws.String("arn:aws:iam::000000000000:role/rot"),
		Code:         &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(codeDir)},
		Environment:  &lamtypes.Environment{Variables: map[string]string{"MARKER_FILE": marker}},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// Create the secret, then rotate it via the lambda.
	if _, err := smc.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("db-password"), SecretString: aws.String("original"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if _, err := smc.RotateSecret(ctx, &awssm.RotateSecretInput{
		SecretId:          aws.String("db-password"),
		RotationLambdaARN: aws.String("rotator"),
	}); err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}

	// The lambda must have been driven through all four rotation steps in order.
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading step marker: %v", err)
	}
	steps := strings.Fields(string(got))
	want := []string{"createSecret", "setSecret", "testSecret", "finishSecret"}
	if len(steps) != len(want) {
		t.Fatalf("rotation steps = %v, want %v", steps, want)
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Fatalf("step %d = %q, want %q", i, steps[i], want[i])
		}
	}

	// Rotation metadata is recorded.
	d, err := smc.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("db-password")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}
	if d.RotationEnabled == nil || !*d.RotationEnabled {
		t.Fatalf("RotationEnabled not set after rotation")
	}
}
