package dozeaws_test

// Durability for the control-plane records added after the original nine
// services.
//
// TestPersistenceAcrossRestart proves the data plane survives a restart: an
// object, an item, a message, a secret, a key. It was written when those were
// the whole of the emulator, and it has not grown since. Everything three
// batches of work added — a bucket's public-access block, an EventBridge
// connection and the API destination that uses it, a cron schedule, a log
// group's subscription filter, a REST API's authorizer, its API keys and
// usage plans, a whole HTTP API with its routes, integrations, CORS and
// stages — is a NEW bbolt bucket or a NEW field on an existing record, and
// nothing checked that any of it came back.
//
// That is precisely where a marshalling bug hides in silence: a map field
// tagged omitempty reads back nil, a struct gains a field the stored JSON
// does not carry, a bucket is created on write but never opened on boot. One
// of those (an omitempty map read back as nil) actually shipped and was
// caught by hand, not by a test.
//
// So: write one distinctive artifact per surface, close the stack, open a new
// one over the same directory, and read every one of them back through the
// real SDK. The values are deliberately NOT the defaults — a public-access
// block of all-true would pass this test on a stack that lost the record and
// re-seeded it.

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsapi "github.com/aws/aws-sdk-go-v2/service/apigateway"
	awsv2 "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	v2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	awskin "github.com/aws/aws-sdk-go-v2/service/kinesis"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// surfaceClients is the second bundle: the services whose control planes grew.
type surfaceClients struct {
	s3   *awss3.Client
	eb   *awseb.Client
	logs *cwl.Client
	kin  *awskin.Client
	agw  *awsapi.Client
	v2   *awsv2.Client
}

func surfaceClientsFor(url string) surfaceClients {
	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	return surfaceClients{
		s3:   awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(url); o.UsePathStyle = true }),
		eb:   awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(url) }),
		logs: cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(url) }),
		kin:  awskin.NewFromConfig(cfg, func(o *awskin.Options) { o.BaseEndpoint = aws.String(url) }),
		agw:  awsapi.NewFromConfig(cfg, func(o *awsapi.Options) { o.BaseEndpoint = aws.String(url) }),
		v2:   awsv2.NewFromConfig(cfg, func(o *awsv2.Options) { o.BaseEndpoint = aws.String(url) }),
	}
}

// ids carries the server-minted identifiers from the first boot to the second.
type surfaceIDs struct {
	restAPI     string
	authorizer  string
	apiKey      string
	usagePlan   string
	httpAPI     string
	integration string
	route       string
	streamARN   string
}

const authorizerURIForPersistence = "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/" +
	"arn:aws:lambda:us-east-1:000000000000:function:gate/invocations"

func TestPersistenceOfRecentSurfaces(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two Stacks over a shared data dir")
	}
	ctx := context.Background()
	dir := t.TempDir()

	stack1, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ts1 := httptest.NewServer(stack1.Handler())
	ids := writeSurfaces(ctx, t, surfaceClientsFor(ts1.URL))

	ts1.Close()
	if err := stack1.Close(); err != nil {
		t.Fatalf("stack1 close: %v", err)
	}

	stack2, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: t.Logf})
	if err != nil {
		t.Fatalf("reopen stack: %v", err)
	}
	defer stack2.Close()
	ts2 := httptest.NewServer(stack2.Handler())
	defer ts2.Close()
	readSurfaces(ctx, t, surfaceClientsFor(ts2.URL), ids)
}

func writeSurfaces(ctx context.Context, t *testing.T, c surfaceClients) surfaceIDs {
	t.Helper()
	var ids surfaceIDs

	// S3: a public-access block that is NOT the all-true default, and
	// ownership controls, which are unset until asked for.
	if _, err := c.s3.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("durable-access")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s3.PutPublicAccessBlock(ctx, &awss3.PutPublicAccessBlockInput{
		Bucket: aws.String("durable-access"),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls: aws.Bool(true), IgnorePublicAcls: aws.Bool(false),
			BlockPublicPolicy: aws.Bool(true), RestrictPublicBuckets: aws.Bool(false),
		}}); err != nil {
		t.Fatalf("PutPublicAccessBlock: %v", err)
	}
	if _, err := c.s3.PutBucketOwnershipControls(ctx, &awss3.PutBucketOwnershipControlsInput{
		Bucket:            aws.String("durable-access"),
		OwnershipControls: &s3types.OwnershipControls{Rules: []s3types.OwnershipControlsRule{{ObjectOwnership: s3types.ObjectOwnershipBucketOwnerPreferred}}},
	}); err != nil {
		t.Fatalf("PutBucketOwnershipControls: %v", err)
	}

	// EventBridge: a connection, the API destination that uses it, and a cron
	// rule. The connection's secret half must not come back, but the rest must.
	conn, err := c.eb.CreateConnection(ctx, &awseb.CreateConnectionInput{
		Name: aws.String("durable-conn"), AuthorizationType: ebtypes.ConnectionAuthorizationTypeBasic,
		AuthParameters: &ebtypes.CreateConnectionAuthRequestParameters{
			BasicAuthParameters: &ebtypes.CreateConnectionBasicAuthRequestParameters{
				Username: aws.String("alice"), Password: aws.String("s3cret")},
		}})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	if _, err := c.eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("durable-dest"), ConnectionArn: conn.ConnectionArn,
		InvocationEndpoint: aws.String("http://127.0.0.1:1/orders"),
		HttpMethod:         ebtypes.ApiDestinationHttpMethodPost,
	}); err != nil {
		t.Fatalf("CreateApiDestination: %v", err)
	}
	if _, err := c.eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("durable-cron"), ScheduleExpression: aws.String("cron(15 10 ? * MON *)"),
	}); err != nil {
		t.Fatalf("PutRule cron: %v", err)
	}

	// Logs: a subscription filter onto a Kinesis stream, which needs no code.
	if _, err := c.kin.CreateStream(ctx, &awskin.CreateStreamInput{
		StreamName: aws.String("durable-sink"), ShardCount: aws.Int32(1)}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}
	desc, err := c.kin.DescribeStream(ctx, &awskin.DescribeStreamInput{StreamName: aws.String("durable-sink")})
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}
	ids.streamARN = aws.ToString(desc.StreamDescription.StreamARN)
	if _, err := c.logs.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/durable/group")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.logs.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{
		LogGroupName: aws.String("/durable/group"), FilterName: aws.String("errors"),
		FilterPattern: aws.String("ERROR"), DestinationArn: aws.String(ids.streamARN),
	}); err != nil {
		t.Fatalf("PutSubscriptionFilter: %v", err)
	}

	// API Gateway REST: an authorizer, an API key, a usage plan holding it.
	rest, err := c.agw.CreateRestApi(ctx, &awsapi.CreateRestApiInput{Name: aws.String("durable-rest")})
	if err != nil {
		t.Fatalf("CreateRestApi: %v", err)
	}
	ids.restAPI = aws.ToString(rest.Id)
	auth, err := c.agw.CreateAuthorizer(ctx, &awsapi.CreateAuthorizerInput{
		RestApiId: rest.Id, Name: aws.String("gate"), Type: "TOKEN",
		AuthorizerUri:                aws.String(authorizerURIForPersistence),
		IdentitySource:               aws.String("method.request.header.Auth"),
		AuthorizerResultTtlInSeconds: aws.Int32(42),
	})
	if err != nil {
		t.Fatalf("CreateAuthorizer: %v", err)
	}
	ids.authorizer = aws.ToString(auth.Id)
	key, err := c.agw.CreateApiKey(ctx, &awsapi.CreateApiKeyInput{Name: aws.String("durable-key"), Enabled: true})
	if err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	ids.apiKey = aws.ToString(key.Id)
	plan, err := c.agw.CreateUsagePlan(ctx, &awsapi.CreateUsagePlanInput{Name: aws.String("durable-plan")})
	if err != nil {
		t.Fatalf("CreateUsagePlan: %v", err)
	}
	ids.usagePlan = aws.ToString(plan.Id)
	if _, err := c.agw.CreateUsagePlanKey(ctx, &awsapi.CreateUsagePlanKeyInput{
		UsagePlanId: plan.Id, KeyId: key.Id, KeyType: aws.String("API_KEY")}); err != nil {
		t.Fatalf("CreateUsagePlanKey: %v", err)
	}

	// API Gateway v2: a whole HTTP API — CORS on the api, an integration, a
	// route pointing at it, an auto-deploying $default stage.
	api, err := c.v2.CreateApi(ctx, &awsv2.CreateApiInput{
		Name: aws.String("durable-http"), ProtocolType: v2types.ProtocolTypeHttp,
		CorsConfiguration: &v2types.Cors{AllowOrigins: []string{"https://shop.example"}, AllowMethods: []string{"GET"}},
	})
	if err != nil {
		t.Fatalf("CreateApi: %v", err)
	}
	ids.httpAPI = aws.ToString(api.ApiId)
	integ, err := c.v2.CreateIntegration(ctx, &awsv2.CreateIntegrationInput{
		ApiId: api.ApiId, IntegrationType: v2types.IntegrationTypeHttpProxy,
		IntegrationUri: aws.String("http://127.0.0.1:1/upstream"), IntegrationMethod: aws.String("GET"),
		PayloadFormatVersion: aws.String("1.0"),
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	ids.integration = aws.ToString(integ.IntegrationId)
	route, err := c.v2.CreateRoute(ctx, &awsv2.CreateRouteInput{
		ApiId: api.ApiId, RouteKey: aws.String("GET /items"),
		Target: aws.String("integrations/" + ids.integration),
	})
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	ids.route = aws.ToString(route.RouteId)
	if _, err := c.v2.CreateStage(ctx, &awsv2.CreateStageInput{
		ApiId: api.ApiId, StageName: aws.String("$default"), AutoDeploy: aws.Bool(true)}); err != nil {
		t.Fatalf("CreateStage: %v", err)
	}
	return ids
}

func readSurfaces(ctx context.Context, t *testing.T, c surfaceClients, ids surfaceIDs) {
	t.Helper()

	pab, err := c.s3.GetPublicAccessBlock(ctx, &awss3.GetPublicAccessBlockInput{Bucket: aws.String("durable-access")})
	if err != nil {
		t.Fatalf("GetPublicAccessBlock after restart: %v", err)
	}
	got := pab.PublicAccessBlockConfiguration
	if !aws.ToBool(got.BlockPublicAcls) || aws.ToBool(got.IgnorePublicAcls) ||
		!aws.ToBool(got.BlockPublicPolicy) || aws.ToBool(got.RestrictPublicBuckets) {
		t.Errorf("public access block after restart = %+v, want true/false/true/false — all-true means the record was lost and re-seeded", got)
	}
	oc, err := c.s3.GetBucketOwnershipControls(ctx, &awss3.GetBucketOwnershipControlsInput{Bucket: aws.String("durable-access")})
	if err != nil || len(oc.OwnershipControls.Rules) != 1 ||
		oc.OwnershipControls.Rules[0].ObjectOwnership != s3types.ObjectOwnershipBucketOwnerPreferred {
		t.Errorf("ownership controls after restart = %+v err=%v", oc, err)
	}

	conn, err := c.eb.DescribeConnection(ctx, &awseb.DescribeConnectionInput{Name: aws.String("durable-conn")})
	if err != nil {
		t.Fatalf("DescribeConnection after restart: %v", err)
	}
	if conn.AuthorizationType != ebtypes.ConnectionAuthorizationTypeBasic {
		t.Errorf("connection auth type after restart = %q", conn.AuthorizationType)
	}
	if conn.AuthParameters == nil || conn.AuthParameters.BasicAuthParameters == nil ||
		aws.ToString(conn.AuthParameters.BasicAuthParameters.Username) != "alice" {
		t.Errorf("connection username did not survive: %+v", conn.AuthParameters)
	}
	dest, err := c.eb.DescribeApiDestination(ctx, &awseb.DescribeApiDestinationInput{Name: aws.String("durable-dest")})
	if err != nil {
		t.Fatalf("DescribeApiDestination after restart: %v", err)
	}
	if aws.ToString(dest.InvocationEndpoint) != "http://127.0.0.1:1/orders" ||
		dest.HttpMethod != ebtypes.ApiDestinationHttpMethodPost ||
		aws.ToString(dest.ConnectionArn) != aws.ToString(conn.ConnectionArn) {
		t.Errorf("api destination after restart = endpoint %q method %q conn %q",
			aws.ToString(dest.InvocationEndpoint), dest.HttpMethod, aws.ToString(dest.ConnectionArn))
	}
	rule, err := c.eb.DescribeRule(ctx, &awseb.DescribeRuleInput{Name: aws.String("durable-cron")})
	if err != nil || aws.ToString(rule.ScheduleExpression) != "cron(15 10 ? * MON *)" {
		t.Errorf("cron rule after restart = %q err=%v", aws.ToString(rule.ScheduleExpression), err)
	}

	subs, err := c.logs.DescribeSubscriptionFilters(ctx, &cwl.DescribeSubscriptionFiltersInput{
		LogGroupName: aws.String("/durable/group")})
	if err != nil || len(subs.SubscriptionFilters) != 1 {
		t.Fatalf("subscription filters after restart = %+v err=%v", subs, err)
	}
	sf := subs.SubscriptionFilters[0]
	if aws.ToString(sf.FilterName) != "errors" || aws.ToString(sf.FilterPattern) != "ERROR" ||
		aws.ToString(sf.DestinationArn) != ids.streamARN {
		t.Errorf("subscription filter after restart = %+v", sf)
	}

	auths, err := c.agw.GetAuthorizers(ctx, &awsapi.GetAuthorizersInput{RestApiId: aws.String(ids.restAPI)})
	if err != nil || len(auths.Items) != 1 {
		t.Fatalf("authorizers after restart = %+v err=%v", auths, err)
	}
	if a := auths.Items[0]; aws.ToString(a.Name) != "gate" || string(a.Type) != "TOKEN" ||
		aws.ToString(a.IdentitySource) != "method.request.header.Auth" || aws.ToInt32(a.AuthorizerResultTtlInSeconds) != 42 {
		t.Errorf("authorizer after restart = %+v", auths.Items[0])
	}
	keys, err := c.agw.GetApiKeys(ctx, &awsapi.GetApiKeysInput{})
	if err != nil || len(keys.Items) != 1 || aws.ToString(keys.Items[0].Name) != "durable-key" ||
		!keys.Items[0].Enabled {
		t.Errorf("api keys after restart = %+v err=%v", keys, err)
	}
	plans, err := c.agw.GetUsagePlans(ctx, &awsapi.GetUsagePlansInput{})
	if err != nil || len(plans.Items) != 1 || aws.ToString(plans.Items[0].Name) != "durable-plan" {
		t.Errorf("usage plans after restart = %+v err=%v", plans, err)
	}
	planKeys, err := c.agw.GetUsagePlanKeys(ctx, &awsapi.GetUsagePlanKeysInput{UsagePlanId: aws.String(ids.usagePlan)})
	if err != nil || len(planKeys.Items) != 1 || aws.ToString(planKeys.Items[0].Id) != ids.apiKey {
		t.Errorf("usage plan keys after restart = %+v err=%v", planKeys, err)
	}

	api, err := c.v2.GetApi(ctx, &awsv2.GetApiInput{ApiId: aws.String(ids.httpAPI)})
	if err != nil {
		t.Fatalf("GetApi after restart: %v", err)
	}
	if api.ProtocolType != v2types.ProtocolTypeHttp || api.CorsConfiguration == nil ||
		len(api.CorsConfiguration.AllowOrigins) != 1 || api.CorsConfiguration.AllowOrigins[0] != "https://shop.example" {
		t.Errorf("http api after restart = protocol %q cors %+v", api.ProtocolType, api.CorsConfiguration)
	}
	routes, err := c.v2.GetRoutes(ctx, &awsv2.GetRoutesInput{ApiId: aws.String(ids.httpAPI)})
	if err != nil || len(routes.Items) != 1 || aws.ToString(routes.Items[0].RouteKey) != "GET /items" ||
		aws.ToString(routes.Items[0].Target) != "integrations/"+ids.integration {
		t.Errorf("routes after restart = %+v err=%v", routes, err)
	}
	integs, err := c.v2.GetIntegrations(ctx, &awsv2.GetIntegrationsInput{ApiId: aws.String(ids.httpAPI)})
	if err != nil || len(integs.Items) != 1 ||
		aws.ToString(integs.Items[0].IntegrationUri) != "http://127.0.0.1:1/upstream" {
		t.Errorf("integrations after restart = %+v err=%v", integs, err)
	}
	stages, err := c.v2.GetStages(ctx, &awsv2.GetStagesInput{ApiId: aws.String(ids.httpAPI)})
	if err != nil || len(stages.Items) != 1 || aws.ToString(stages.Items[0].StageName) != "$default" ||
		!aws.ToBool(stages.Items[0].AutoDeploy) {
		t.Errorf("stages after restart = %+v err=%v", stages, err)
	}
}
