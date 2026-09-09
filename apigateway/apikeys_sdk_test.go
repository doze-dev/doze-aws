// API keys and usage plans through the real aws-sdk-go-v2 client: the list
// operations must deserialize (the wire member is "item", which the audit
// found the service spelling "items" — every SDK saw an empty account).
package apigateway_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsapi "github.com/aws/aws-sdk-go-v2/service/apigateway"
	apitypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"

	"github.com/doze-dev/doze-aws/apigateway"
	"github.com/doze-dev/doze-aws/awsident"
)

func TestSDKListsAPIKeysAndUsagePlans(t *testing.T) {
	ctx := context.Background()
	s, err := apigateway.New(apigateway.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	cfg := aws.Config{Region: awsident.Region, Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	agw := awsapi.NewFromConfig(cfg, func(o *awsapi.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	api, err := agw.CreateRestApi(ctx, &awsapi.CreateRestApiInput{Name: aws.String("keyed")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agw.CreateDeployment(ctx, &awsapi.CreateDeploymentInput{RestApiId: api.Id, StageName: aws.String("prod")}); err != nil {
		t.Fatal(err)
	}
	key, err := agw.CreateApiKey(ctx, &awsapi.CreateApiKeyInput{Name: aws.String("k1"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// A key created without enabled is disabled, as on AWS.
	off, err := agw.CreateApiKey(ctx, &awsapi.CreateApiKeyInput{Name: aws.String("k2")})
	if err != nil {
		t.Fatal(err)
	}
	if off.Enabled {
		t.Fatal("CreateApiKey without enabled must make a disabled key")
	}
	plan, err := agw.CreateUsagePlan(ctx, &awsapi.CreateUsagePlanInput{Name: aws.String("p1"),
		ApiStages: []apitypes.ApiStage{{ApiId: api.Id, Stage: aws.String("prod")}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agw.CreateUsagePlanKey(ctx, &awsapi.CreateUsagePlanKeyInput{UsagePlanId: plan.Id, KeyId: key.Id, KeyType: aws.String("API_KEY")}); err != nil {
		t.Fatal(err)
	}

	keys, err := agw.GetApiKeys(ctx, &awsapi.GetApiKeysInput{IncludeValues: aws.Bool(true)})
	if err != nil || len(keys.Items) != 2 {
		t.Fatalf("GetApiKeys: %v, %d items", err, len(keys.Items))
	}
	plans, err := agw.GetUsagePlans(ctx, &awsapi.GetUsagePlansInput{})
	if err != nil || len(plans.Items) != 1 || aws.ToString(plans.Items[0].Name) != "p1" {
		t.Fatalf("GetUsagePlans: %v, %+v", err, plans.Items)
	}
	pk, err := agw.GetUsagePlanKeys(ctx, &awsapi.GetUsagePlanKeysInput{UsagePlanId: plan.Id})
	if err != nil || len(pk.Items) != 1 || aws.ToString(pk.Items[0].Id) != aws.ToString(key.Id) {
		t.Fatalf("GetUsagePlanKeys: %v, %+v", err, pk.Items)
	}
	byName, err := agw.GetApiKeys(ctx, &awsapi.GetApiKeysInput{NameQuery: aws.String("k2")})
	if err != nil || len(byName.Items) != 1 {
		t.Fatalf("GetApiKeys by name: %v, %d items", err, len(byName.Items))
	}
}
