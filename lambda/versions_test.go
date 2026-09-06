package lambda_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// A version freezes the function. This is the alias-and-rollback flow a
// deploy tool drives: publish, change $LATEST, and the alias keeps running
// what it pointed at.

func TestVersionsFreezeCodeAndConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("runs interpreters")
	}
	ctx := context.Background()
	c, _ := lambdaClient(t)
	if _, err := c.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("vers"), Runtime: lambdatypes.RuntimePython312, Handler: aws.String("h.handler"),
		Role:        aws.String("arn:aws:iam::000000000000:role/x"),
		Code:        &lambdatypes.FunctionCode{ZipFile: zipOf(t, map[string]string{"h.py": "import os\ndef handler(e, c):\n    return {'gen': 1, 'v': c.function_version, 'arn': c.invoked_function_arn, 'env': os.environ.get('X')}\n"})},
		Environment: &lambdatypes.Environment{Variables: map[string]string{"X": "one"}},
	}); err != nil {
		t.Fatal(err)
	}
	skipWithoutPython(t)

	v1, err := c.PublishVersion(ctx, &awslambda.PublishVersionInput{FunctionName: aws.String("vers"), Description: aws.String("first")})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(v1.Version) != "1" || !strings.HasSuffix(aws.ToString(v1.FunctionArn), ":function:vers:1") {
		t.Errorf("version 1 = %s %s", aws.ToString(v1.Version), aws.ToString(v1.FunctionArn))
	}
	// Publishing again with nothing changed answers the same version.
	again, _ := c.PublishVersion(ctx, &awslambda.PublishVersionInput{FunctionName: aws.String("vers")})
	if aws.ToString(again.Version) != "1" {
		t.Errorf("an unchanged publish should answer version 1, got %s", aws.ToString(again.Version))
	}

	// Change the code and an environment variable: $LATEST is generation 2.
	if _, err := c.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{FunctionName: aws.String("vers"),
		ZipFile: zipOf(t, map[string]string{"h.py": "import os\ndef handler(e, c):\n    return {'gen': 2, 'v': c.function_version, 'arn': c.invoked_function_arn, 'env': os.environ.get('X')}\n"})}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{FunctionName: aws.String("vers"),
		Environment: &lambdatypes.Environment{Variables: map[string]string{"X": "two"}}}); err != nil {
		t.Fatal(err)
	}
	v2, _ := c.PublishVersion(ctx, &awslambda.PublishVersionInput{FunctionName: aws.String("vers")})
	if aws.ToString(v2.Version) != "2" {
		t.Fatalf("a changed publish should be version 2, got %s", aws.ToString(v2.Version))
	}
	if _, err := c.CreateAlias(ctx, &awslambda.CreateAliasInput{FunctionName: aws.String("vers"), Name: aws.String("live"), FunctionVersion: aws.String("1")}); err != nil {
		t.Fatal(err)
	}

	invoke := func(qualifier string) (string, string) {
		in := &awslambda.InvokeInput{FunctionName: aws.String("vers"), Payload: []byte(`{}`)}
		if qualifier != "" {
			in.Qualifier = aws.String(qualifier)
		}
		out, err := c.Invoke(ctx, in)
		if err != nil {
			t.Fatalf("invoke %q: %v", qualifier, err)
		}
		if out.FunctionError != nil {
			t.Fatalf("invoke %q: %s", qualifier, out.Payload)
		}
		return string(out.Payload), aws.ToString(out.ExecutedVersion)
	}
	// $LATEST is generation 2 with X=two; the alias runs the frozen version 1
	// with the environment it had; a version number addresses it directly.
	if got, ev := invoke(""); !strings.Contains(got, `"gen": 2`) || !strings.Contains(got, `"env": "two"`) || ev != "$LATEST" {
		t.Errorf("$LATEST = %s (executed %s)", got, ev)
	}
	if got, ev := invoke("live"); !strings.Contains(got, `"gen": 1`) || !strings.Contains(got, `"env": "one"`) || !strings.Contains(got, `"v": "1"`) || !strings.Contains(got, `:function:vers:live"`) || ev != "1" {
		t.Errorf("alias live = %s (executed %s)", got, ev)
	}
	if got, ev := invoke("2"); !strings.Contains(got, `"gen": 2`) || ev != "2" {
		t.Errorf("version 2 = %s (executed %s)", got, ev)
	}
	// Repoint the alias: what a deploy does to go live.
	if _, err := c.UpdateAlias(ctx, &awslambda.UpdateAliasInput{FunctionName: aws.String("vers"), Name: aws.String("live"), FunctionVersion: aws.String("2")}); err != nil {
		t.Fatal(err)
	}
	if got, ev := invoke("live"); !strings.Contains(got, `"gen": 2`) || ev != "2" {
		t.Errorf("after repointing, alias live = %s (executed %s)", got, ev)
	}

	// The list and the qualified reads describe the frozen records.
	list, err := c.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{FunctionName: aws.String("vers")})
	if err != nil || len(list.Versions) != 3 {
		t.Fatalf("versions = %d, %v", len(list.Versions), err)
	}
	if aws.ToString(list.Versions[0].Version) != "$LATEST" || aws.ToString(list.Versions[1].Version) != "1" || aws.ToString(list.Versions[2].Version) != "2" {
		t.Errorf("version order: %s %s %s", aws.ToString(list.Versions[0].Version), aws.ToString(list.Versions[1].Version), aws.ToString(list.Versions[2].Version))
	}
	one, err := c.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("vers"), Qualifier: aws.String("1")})
	if err != nil || one.Configuration.Environment.Variables["X"] != "one" || aws.ToString(one.Configuration.Description) != "first" {
		t.Errorf("GetFunction(1) = %+v, %v", one.Configuration, err)
	}
	byARN, err := c.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{FunctionName: aws.String("arn:aws:lambda:us-east-1:000000000000:function:vers:2")})
	if err != nil || aws.ToString(byARN.Version) != "2" {
		t.Errorf("a qualified ARN addresses the version: %+v %v", byARN, err)
	}

	// A version an alias no longer needs can be deleted; the function keeps
	// the rest. An unknown qualifier is not found.
	if _, err := c.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{FunctionName: aws.String("vers"), Qualifier: aws.String("1")}); err != nil {
		t.Fatal(err)
	}
	var nf *lambdatypes.ResourceNotFoundException
	if _, err := c.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("vers"), Qualifier: aws.String("1"), Payload: []byte(`{}`)}); !errors.As(err, &nf) {
		t.Errorf("a deleted version should be ResourceNotFoundException: %v", err)
	}
	if _, err := c.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("vers"), Qualifier: aws.String("nope"), Payload: []byte(`{}`)}); !errors.As(err, &nf) {
		t.Errorf("an unknown alias should be ResourceNotFoundException: %v", err)
	}
	if got, _ := invoke(""); !strings.Contains(got, `"gen": 2`) {
		t.Errorf("$LATEST survives a version delete: %s", got)
	}
}
