package provision_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/internal/provision"
)

// A stack file names no role for a function, so apply gives it one. It gave it
// a role in the default account, whatever account the instance ran as.
func TestAnAppliedFunctionsRoleIsInTheInstancesAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	ctx := context.Background()
	id := awsident.Identity{Region: "eu-west-1", AccountID: "111122223333"}
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t), Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(st.Handler())
	t.Cleanup(ts.Close)

	code := t.TempDir()
	if err := os.WriteFile(filepath.Join(code, "bootstrap"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stack := &provision.Stack{Functions: map[string]provision.Function{
		"resize": {Runtime: "provided.al2", Handler: "bootstrap", Code: code},
	}}
	if _, err := provision.Apply(ctx, st.Handler(), stack, id); err != nil {
		t.Fatal(err)
	}

	lam := awslambda.NewFromConfig(aws.Config{Region: id.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")},
		func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	got, err := lam.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{FunctionName: aws.String("resize")})
	if err != nil {
		t.Fatal(err)
	}
	if want := "arn:aws:iam::111122223333:role/stackfile"; aws.ToString(got.Role) != want {
		t.Errorf("Role = %q, want %q", aws.ToString(got.Role), want)
	}
}
