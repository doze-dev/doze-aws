package cloudformation_test

// The CDK's shape around a function: a LayerVersion the function lists, a
// Version (`fn.currentVersion`), an Alias at it, and a Url. Deploying it
// twice must converge (one version, one layer version), the alias must run
// the layer-using code, the URL must answer, and export must carry all four
// resources back out.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const lambdaVersioningTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Helpers:
    Type: AWS::Lambda::LayerVersion
    Properties:
      LayerName: helpers
      Description: shared helpers
      CompatibleRuntimes: [python3.12]
      Content:
        S3Bucket: _local_
        S3Key: LAYER_DIR
  Fn:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: versioned
      Runtime: python3.12
      Handler: h.handler
      Role: arn:aws:iam::000000000000:role/x
      Code:
        S3Bucket: _local_
        S3Key: CODE_DIR
      Layers:
        - Ref: Helpers
  FnVersion:
    Type: AWS::Lambda::Version
    Properties:
      FunctionName:
        Ref: Fn
  Live:
    Type: AWS::Lambda::Alias
    Properties:
      Name: live
      Description: what production runs
      FunctionName:
        Ref: Fn
      FunctionVersion:
        Fn::GetAtt: [FnVersion, Version]
  Url:
    Type: AWS::Lambda::Url
    Properties:
      TargetFunctionArn:
        Fn::GetAtt: [Fn, Arn]
      AuthType: NONE
Outputs:
  AliasArn:
    Value:
      Ref: Live
  FunctionUrl:
    Value:
      Fn::GetAtt: [Url, FunctionUrl]
  LayerArn:
    Value:
      Ref: Helpers
`

func TestApplyLambdaVersioningTemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack and runs python")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH")
	}
	ctx := context.Background()

	layerDir := filepath.Join(t.TempDir(), "layer")
	if err := os.MkdirAll(filepath.Join(layerDir, "python"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(layerDir, "python", "helper.py"), []byte("def greet(who):\n    return 'hi ' + who\n"), 0o644)
	codeDir := filepath.Join(t.TempDir(), "code")
	os.MkdirAll(codeDir, 0o755)
	os.WriteFile(filepath.Join(codeDir, "h.py"), []byte("import helper\nimport os\ndef handler(event, context):\n    return {'greeting': helper.greet(event.get('who', 'url')), 'version': os.environ['AWS_LAMBDA_FUNCTION_VERSION']}\n"), 0o644)
	template := strings.NewReplacer("LAYER_DIR", layerDir, "CODE_DIR", codeDir).Replace(lambdaVersioningTemplate)

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	tmpl, err := cloudformation.Parse([]byte(template))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "lam", Endpoint: ts.URL})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	f, ok := sf.Functions["versioned"]
	if !ok {
		t.Fatalf("no function mapped: %+v", rep.Entries)
	}
	if !f.Publish || f.Aliases["live"].Description != "what production runs" || f.Aliases["live"].Version != "" {
		t.Errorf("version and alias did not map: publish=%v aliases=%+v", f.Publish, f.Aliases)
	}
	if f.URL == nil || f.URL.AuthType != "NONE" {
		t.Errorf("URL did not map: %+v", f.URL)
	}
	if len(f.Layers) != 1 || f.Layers[0] != "helpers" {
		t.Errorf("the layer should be referenced by its stack name, got %v", f.Layers)
	}
	if l := sf.Layers["helpers"]; l.Code != layerDir || l.Description != "shared helpers" || len(l.Runtimes) != 1 {
		t.Errorf("layer mapped as %+v", l)
	}
	aliasARN := awsident.ARN("lambda", "function:versioned:live")
	if got := rep.Outputs["AliasArn"]; got != aliasARN {
		t.Errorf("Ref Live = %q, want %q", got, aliasARN)
	}
	wantURL := ts.URL + "/_aws/lambda-url/" + awsident.FunctionURLID("versioned") + "/"
	if got := rep.Outputs["FunctionUrl"]; got != wantURL {
		t.Errorf("GetAtt Url.FunctionUrl = %q, want %q", got, wantURL)
	}

	for i := 0; i < 2; i++ {
		if _, err := provision.Apply(ctx, stack.Handler(), sf, awsident.Default()); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}

	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	lc := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	alias, err := lc.GetAlias(ctx, &awslambda.GetAliasInput{FunctionName: aws.String("versioned"), Name: aws.String("live")})
	if err != nil {
		t.Fatalf("the deployed alias does not exist: %v", err)
	}
	if aws.ToString(alias.FunctionVersion) != "1" || aws.ToString(alias.Description) != "what production runs" {
		t.Errorf("alias = version %s %q, want version 1 with the template's description", aws.ToString(alias.FunctionVersion), aws.ToString(alias.Description))
	}
	versions, err := lc.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{FunctionName: aws.String("versioned")})
	if err != nil || len(versions.Versions) != 2 { // $LATEST and 1
		t.Fatalf("two applies of unchanged code should leave one published version, got %d (%v)", len(versions.Versions), err)
	}
	layers, err := lc.ListLayerVersions(ctx, &awslambda.ListLayerVersionsInput{LayerName: aws.String("helpers")})
	if err != nil || len(layers.LayerVersions) != 1 {
		t.Fatalf("two applies of unchanged layer content should leave one layer version, got %d (%v)", len(layers.LayerVersions), err)
	}
	fn, err := lc.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("versioned")})
	if err != nil || len(fn.Configuration.Layers) != 1 || !strings.HasSuffix(aws.ToString(fn.Configuration.Layers[0].Arn), ":layer:helpers:1") {
		t.Errorf("the function should carry the layer version: %+v (%v)", fn.Configuration.Layers, err)
	}

	// The alias runs the published version, with the layer on the path.
	out, err := lc.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String(aliasARN), Payload: []byte(`{"who":"alias"}`)})
	if err != nil {
		t.Fatalf("Invoke on the alias: %v", err)
	}
	var got struct{ Greeting, Version string }
	json.Unmarshal(out.Payload, &got)
	if got.Greeting != "hi alias" || got.Version != "1" || aws.ToString(out.ExecutedVersion) != "1" {
		t.Errorf("alias invoke = %s (executed %s), want the layer's greeting from version 1", out.Payload, aws.ToString(out.ExecutedVersion))
	}
	// The URL answers.
	resp, err := http.Get(wantURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"hi url"`) {
		t.Errorf("function URL = %d %s", resp.StatusCode, body)
	}

	// Export carries the four resources back out as a template that parses
	// and transpiles to the same stack.
	exported, err := provision.Export(ctx, stack.Handler(), awsident.Default())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	emitted, err := cloudformation.Emit(exported)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	for _, want := range []string{"AWS::Lambda::LayerVersion", "AWS::Lambda::Version", "AWS::Lambda::Alias", "AWS::Lambda::Url"} {
		if !strings.Contains(string(emitted), want) {
			t.Errorf("exported template lacks %s:\n%s", want, emitted)
		}
	}
	again, err := cloudformation.Parse(emitted)
	if err != nil {
		t.Fatalf("exported template does not parse: %v", err)
	}
	round, _, err := cloudformation.Transpile(again, cloudformation.TranspileOptions{StackName: "lam"})
	if err != nil {
		t.Fatalf("exported template does not transpile: %v", err)
	}
	r := round.Functions["versioned"]
	if !r.Publish || r.Aliases["live"].Description != "what production runs" || r.URL == nil || len(r.Layers) != 1 || r.Layers[0] != "helpers" {
		t.Errorf("round trip lost the version, alias, URL or layer: %+v", r)
	}
	if l := round.Layers["helpers"]; l.Code != layerDir {
		t.Errorf("round trip lost the layer's local path: %+v", l)
	}

	// Destroy takes the layer with the function.
	//
	// This assertion used to be three nested guards, each of which skipped
	// the check: the outer list had to error for the body to run, the inner
	// list discarded its own error, and a nil result skipped again. It was
	// the only claim in the repository that Destroy removes a Lambda function
	// and it could not fail. The report is now inspected, and the function —
	// which the comment always promised and never checked — with it.
	drep, err := provision.Destroy(ctx, stack.Handler(), sf, awsident.Default())
	if err != nil {
		t.Fatalf("Destroy: %v\n%+v", err, drep.Actions)
	}
	if len(drep.Failures()) != 0 {
		t.Errorf("Destroy reported failures: %+v", drep.Failures())
	}
	if _, err := lc.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("versioned")}); err == nil {
		t.Error("destroy left the function behind")
	}
	lv, err := lc.ListLayerVersions(ctx, &awslambda.ListLayerVersionsInput{LayerName: aws.String("helpers")})
	if err == nil && len(lv.LayerVersions) != 0 {
		t.Errorf("destroy left %d layer version(s) behind", len(lv.LayerVersions))
	}
}
