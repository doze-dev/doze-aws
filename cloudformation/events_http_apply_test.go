package cloudformation_test

// A template with a connection, an API destination on it and a rule that
// targets the destination deploys: the destination resolves the connection's
// minted ARN, the rule's target carries HttpParameters, an event reaches the
// endpoint, and the export round-trips with the secret blanked.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const eventsHTTPTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Hook:
    Type: AWS::Events::Connection
    Properties:
      Name: hook-conn
      AuthorizationType: API_KEY
      AuthParameters:
        ApiKeyAuthParameters:
          ApiKeyName: X-Api-Key
          ApiKeyValue: k-42
        InvocationHttpParameters:
          HeaderParameters:
            - Key: X-Source
              Value: doze
  HookDest:
    Type: AWS::Events::ApiDestination
    Properties:
      Name: hook-dest
      ConnectionArn: !GetAtt Hook.Arn
      InvocationEndpoint: ENDPOINT/orders/*
      HttpMethod: POST
      InvocationRateLimitPerSecond: 5
  Orders:
    Type: AWS::Events::Rule
    Properties:
      Name: orders-to-hook
      EventPattern:
        source: [app.orders]
      Targets:
        - Id: hook
          Arn: !GetAtt HookDest.Arn
          RoleArn: arn:aws:iam::000000000000:role/events
          HttpParameters:
            PathParameterValues: ["o-9"]
            QueryStringParameters:
              v: "2"
`

func TestApplyEventsHTTP(t *testing.T) {
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

	var hits atomic.Int32
	var seen atomic.Value
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") == "k-42" && r.Header.Get("X-Source") == "doze" {
			seen.Store(r.URL.String())
			hits.Add(1)
		}
	}))
	defer hook.Close()

	tmpl, err := cloudformation.Parse([]byte(strings.ReplaceAll(eventsHTTPTemplate, "ENDPOINT", hook.URL)))
	if err != nil {
		t.Fatal(err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "hooks"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rejected := rep.Counts(); rejected > 0 {
		t.Fatalf("rejected: %+v", rep.Entries)
	}
	if c := sf.Connections["hook-conn"]; c.APIKey == nil || c.APIKey.Value != "k-42" || len(c.Invocation.Headers) != 1 {
		t.Fatalf("connection did not map: %+v", c)
	}
	if d := sf.APIDestinations["hook-dest"]; d.Connection != "hook-conn" || d.RateLimit != 5 || !strings.HasSuffix(d.Endpoint, "/orders/*") {
		t.Fatalf("destination did not map: %+v", d)
	}
	if tg := sf.Rules["orders-to-hook"].Targets[0]; tg.APIDestination != "hook-dest" || len(tg.PathParams) != 1 || tg.Query["v"] != "2" {
		t.Fatalf("rule target did not map: %+v", tg)
	}

	// Apply twice: the second pass updates rather than failing on exists.
	for i := 0; i < 2; i++ {
		if _, err := provision.Apply(ctx, stack.Handler(), sf); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	eb := awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	targets, err := eb.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{Rule: aws.String("orders-to-hook")})
	if err != nil || len(targets.Targets) != 1 || !strings.Contains(aws.ToString(targets.Targets[0].Arn), ":api-destination/hook-dest/") {
		t.Fatalf("the rule target should be the minted destination ARN: %+v %v", targets, err)
	}
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app.orders"), DetailType: aws.String("OrderCreated"), Detail: aws.String(`{}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if url, _ := seen.Load().(string); url != "/orders/o-9?v=2" {
		t.Errorf("the endpoint saw %q (hits %d)", url, hits.Load())
	}

	// Export: the connection comes back with its key value blank and the
	// destination still names it; the rule target still names the destination.
	exported, err := provision.Export(ctx, stack.Handler())
	if err != nil {
		t.Fatal(err)
	}
	if c := exported.Connections["hook-conn"]; c.APIKey == nil || c.APIKey.Name != "X-Api-Key" || c.APIKey.Value != "" {
		t.Errorf("export should carry the key name and blank the value: %+v", c)
	}
	out, err := cloudformation.Emit(exported)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "k-42") {
		t.Errorf("the exported template leaks the key value:\n%s", out)
	}
	again, _ := cloudformation.Parse(out)
	round, _, err := cloudformation.Transpile(again, cloudformation.TranspileOptions{StackName: "hooks"})
	if err != nil {
		t.Fatalf("exported template does not transpile: %v\n%s", err, out)
	}
	if round.APIDestinations["hook-dest"].Connection != "hook-conn" || round.Rules["orders-to-hook"].Targets[0].APIDestination != "hook-dest" {
		t.Errorf("round trip lost the wiring:\n%s", out)
	}

	// Destroy removes the destination before the connection, and the rule.
	drep, err := provision.Destroy(ctx, stack.Handler(), sf)
	if err != nil {
		t.Fatalf("Destroy: %v\n%+v", err, drep)
	}
	if _, err := eb.DescribeConnection(ctx, &awseb.DescribeConnectionInput{Name: aws.String("hook-conn")}); err == nil {
		t.Error("connection survived destroy")
	}
}
