package cloudformation_test

// Round-trip coverage for the resource kinds Emit could previously have
// broken silently.
//
// Six of Emit's blocks were exercised by an apply test somewhere — state
// machines, Lambda functions and versions, buckets, log groups, alarms,
// EventBridge connections. Five were not: DynamoDB tables, EventBridge rules,
// KMS keys and their aliases, Secrets Manager secrets, and SSM parameters.
// Those five could have been emitted wrongly, or not at all, and every test in
// the package would still have passed.
//
// This is one template through the whole loop — Parse, Transpile, Apply,
// Export, Emit, Parse, Transpile — asserting on both the raw text (that the
// type appears at all) and the re-transpiled IR (that its properties
// survived). Written BEFORE Emit was split into per-resource files, so the
// split had something to fail against.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const roundTripTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Orders:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: orders
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions:
        - AttributeName: pk
          AttributeType: S
        - AttributeName: gsi1pk
          AttributeType: S
      KeySchema:
        - AttributeName: pk
          KeyType: HASH
      GlobalSecondaryIndexes:
        - IndexName: by-customer
          KeySchema:
            - AttributeName: gsi1pk
              KeyType: HASH
          Projection:
            ProjectionType: ALL
      TimeToLiveSpecification:
        AttributeName: expires
        Enabled: true
  Sink:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: rule-sink
  Nightly:
    Type: AWS::Events::Rule
    Properties:
      Name: nightly
      Description: the overnight sweep
      ScheduleExpression: "rate(1 day)"
      State: ENABLED
      Targets:
        - Id: to-queue
          Arn: !GetAtt Sink.Arn
  Signing:
    Type: AWS::KMS::Key
    Properties:
      Description: signs receipts
      KeyUsage: ENCRYPT_DECRYPT
      KeySpec: SYMMETRIC_DEFAULT
      EnableKeyRotation: true
  SigningAlias:
    Type: AWS::KMS::Alias
    Properties:
      AliasName: alias/receipts
      TargetKeyId: !Ref Signing
  ApiToken:
    Type: AWS::SecretsManager::Secret
    Properties:
      Name: api-token
      Description: the upstream token
      SecretString: '{"token":"s3cr3t"}'
  Stage:
    Type: AWS::SSM::Parameter
    Properties:
      Name: /shop/stage
      Type: String
      Value: production
      Description: which stage this stack is
`

func TestEmitRoundTripsEveryResourceKind(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	tmpl, err := cloudformation.Parse([]byte(roundTripTemplate))
	if err != nil {
		t.Fatal(err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rejected := rep.Counts(); rejected > 0 {
		t.Fatalf("rejected: %+v", rep.Entries)
	}
	if _, err := provision.Apply(ctx, stack.Handler(), sf); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	exported, err := provision.Export(ctx, stack.Handler())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	out, err := cloudformation.Emit(exported)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// The raw text first: a block that emits nothing at all would still
	// produce a template that parses and transpiles to an empty stack, so the
	// type names are checked before the structure.
	for _, want := range []string{
		"AWS::DynamoDB::Table",
		"AWS::Events::Rule",
		"AWS::KMS::Key",
		"AWS::KMS::Alias",
		"AWS::SecretsManager::Secret",
		"AWS::SSM::Parameter",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the exported template has no %s", want)
		}
	}

	again, err := cloudformation.Parse(out)
	if err != nil {
		t.Fatalf("the exported template does not parse: %v\n%s", err, out)
	}
	round, _, err := cloudformation.Transpile(again, cloudformation.TranspileOptions{StackName: "shop"})
	if err != nil {
		t.Fatalf("the exported template does not transpile: %v\n%s", err, out)
	}

	// And now the properties, per kind. Each of these is a field some block
	// of Emit has to write for the round trip to be lossless.
	t.Run("table", func(t *testing.T) {
		got, ok := round.Tables["orders"]
		if !ok {
			t.Fatalf("the table did not survive: %+v", round.Tables)
		}
		if got.Key != "pk:S" {
			t.Errorf("key = %q, want pk:S", got.Key)
		}
		if gsi, ok := got.GSIs["by-customer"]; !ok || gsi.Key != "gsi1pk:S" {
			t.Errorf("GSI did not survive: %+v", got.GSIs)
		}
		if got.TTL != "expires" {
			t.Errorf("TTL attribute = %q, want expires", got.TTL)
		}
	})

	t.Run("rule", func(t *testing.T) {
		got, ok := round.Rules["nightly"]
		if !ok {
			t.Fatalf("the rule did not survive: %+v", round.Rules)
		}
		if got.Schedule != "rate(1 day)" {
			t.Errorf("schedule = %q", got.Schedule)
		}
		if len(got.Targets) != 1 || got.Targets[0].Queue != "rule-sink" {
			t.Errorf("target did not survive: %+v", got.Targets)
		}
	})

	t.Run("key and alias", func(t *testing.T) {
		// Keys are addressed by alias, which is how nameProperty declares them.
		got, ok := round.Keys["receipts"]
		if !ok {
			t.Fatalf("the key did not survive under its alias: %+v", round.Keys)
		}
		if got.Description != "signs receipts" {
			t.Errorf("description = %q", got.Description)
		}
		if !got.Rotation {
			t.Error("EnableKeyRotation did not survive")
		}
	})

	t.Run("secret", func(t *testing.T) {
		got, ok := round.Secrets["api-token"]
		if !ok {
			t.Fatalf("the secret did not survive: %+v", round.Secrets)
		}
		if got.Description != "the upstream token" {
			t.Errorf("description = %q", got.Description)
		}
	})

	t.Run("parameter", func(t *testing.T) {
		got, ok := round.Parameters["/shop/stage"]
		if !ok {
			t.Fatalf("the parameter did not survive: %+v", round.Parameters)
		}
		if got.Value != "production" || got.Type != "String" {
			t.Errorf("parameter = %+v", got)
		}
	})
}
