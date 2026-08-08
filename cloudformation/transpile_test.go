package cloudformation

import (
	"strings"
	"testing"
)

// TestTranspileRealisticStack is the end-to-end case: a template of the shape
// people actually write, with cross-resource references in both directions.
func TestTranspileRealisticStack(t *testing.T) {
	tmpl := `
AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Stage:
    Type: String
    Default: dev
Resources:
  DLQ:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "${Stage}-dead"
  Orders:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "${Stage}-orders"
      VisibilityTimeout: 60
      MessageRetentionPeriod: 86400
      RedrivePolicy:
        deadLetterTargetArn: !GetAtt DLQ.Arn
        maxReceiveCount: 4
      Tags:
        - Key: team
          Value: shop
  Events:
    Type: AWS::SNS::Topic
    Properties:
      TopicName: order-events
      Subscription:
        - Protocol: sqs
          Endpoint: !GetAtt Orders.Arn
  Uploads:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Sub "${Stage}-uploads"
      VersioningConfiguration:
        Status: Enabled
      NotificationConfiguration:
        QueueConfigurations:
          - Event: "s3:ObjectCreated:*"
            Queue: !GetAtt Orders.Arn
            Filter:
              S3Key:
                Rules:
                  - Name: prefix
                    Value: incoming/
  Sessions:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: sessions
      AttributeDefinitions:
        - AttributeName: sessionId
          AttributeType: S
        - AttributeName: userId
          AttributeType: S
      KeySchema:
        - AttributeName: sessionId
          KeyType: HASH
      GlobalSecondaryIndexes:
        - IndexName: by-user
          KeySchema:
            - AttributeName: userId
              KeyType: HASH
          Projection:
            ProjectionType: ALL
      TimeToLiveSpecification:
        AttributeName: expiresAt
        Enabled: true
  Role:
    Type: AWS::IAM::Role
    Properties:
      AssumeRolePolicyDocument: {}
Outputs:
  QueueUrl:
    Value: !Ref Orders
  TopicArn:
    Value: !GetAtt Events.TopicArn
`
	parsed, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	stack, rep, err := Transpile(parsed, TranspileOptions{StackName: "shop"})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}

	// Queues, with the DLQ resolved from a GetAtt ARN back to a name.
	orders, ok := stack.Queues["dev-orders"]
	if !ok {
		t.Fatalf("queues = %v", stack.Queues)
	}
	if orders.Visibility != 60 || orders.Retention != 86400 {
		t.Errorf("queue attrs = %+v", orders)
	}
	if orders.DLQ != "dev-dead" {
		t.Errorf("DLQ = %q, want dev-dead (resolved from GetAtt ARN)", orders.DLQ)
	}
	if orders.MaxReceives != 4 {
		t.Errorf("MaxReceives = %d", orders.MaxReceives)
	}
	if orders.Tags["team"] != "shop" {
		t.Errorf("tags = %v", orders.Tags)
	}

	// Topic with an inline subscription pointing at the queue.
	topic, ok := stack.Topics["order-events"]
	if !ok || len(topic.Subscriptions) != 1 {
		t.Fatalf("topics = %+v", stack.Topics)
	}
	if topic.Subscriptions[0].Queue != "dev-orders" {
		t.Errorf("subscription queue = %q", topic.Subscriptions[0].Queue)
	}

	// Bucket with versioning and a filtered notification.
	bucket, ok := stack.Buckets["dev-uploads"]
	if !ok || !bucket.Versioning {
		t.Fatalf("buckets = %+v", stack.Buckets)
	}
	if len(bucket.Notify) != 1 {
		t.Fatalf("notifications = %+v", bucket.Notify)
	}
	n := bucket.Notify[0]
	if n.Queue != "dev-orders" || n.Prefix != "incoming/" || len(n.Events) != 1 {
		t.Errorf("notification = %+v", n)
	}

	// Table with the key shorthand, a GSI and TTL.
	table, ok := stack.Tables["sessions"]
	if !ok {
		t.Fatalf("tables = %+v", stack.Tables)
	}
	if table.Key != "sessionId:S" {
		t.Errorf("key = %q", table.Key)
	}
	if table.TTL != "expiresAt" {
		t.Errorf("ttl = %q", table.TTL)
	}
	if gsi, ok := table.GSIs["by-user"]; !ok || gsi.Key != "userId:S" {
		t.Errorf("gsi = %+v", table.GSIs)
	}

	// The IAM role is accepted and reported, never silently dropped.
	mapped, ignored, rejected := rep.Counts()
	if rejected != 0 {
		t.Errorf("rejected = %d, want 0", rejected)
	}
	if ignored != 1 {
		t.Errorf("ignored = %d, want 1 (the IAM role)", ignored)
	}
	if mapped != 5 {
		t.Errorf("mapped = %d, want 5", mapped)
	}
	if ig := rep.Ignored(); len(ig) != 1 || ig[0].Type != "AWS::IAM::Role" {
		t.Errorf("Ignored() = %+v", ig)
	}

	// Outputs follow AWS's per-type Ref rules.
	if !strings.HasSuffix(rep.Outputs["QueueUrl"], "/dev-orders") {
		t.Errorf("Ref on a queue should be its URL, got %q", rep.Outputs["QueueUrl"])
	}
	if rep.Outputs["TopicArn"] != "arn:aws:sns:us-east-1:000000000000:order-events" {
		t.Errorf("TopicArn = %q", rep.Outputs["TopicArn"])
	}
}

func TestTranspileParameterOverride(t *testing.T) {
	tmpl := `
Parameters:
  Stage: {Type: String, Default: dev}
Resources:
  Q:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "${Stage}-q"
`
	parsed, _ := Parse([]byte(tmpl))
	stack, _, err := Transpile(parsed, TranspileOptions{Parameters: map[string]string{"Stage": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stack.Queues["prod-q"]; !ok {
		t.Fatalf("parameter override ignored: %v", stack.Queues)
	}
}

func TestTranspileRequiresParameterValue(t *testing.T) {
	tmpl := `
Parameters:
  Stage: {Type: String}
Resources:
  Q: {Type: AWS::SQS::Queue}
`
	parsed, _ := Parse([]byte(tmpl))
	_, _, err := Transpile(parsed, TranspileOptions{})
	if err == nil || !strings.Contains(err.Error(), "no value and no default") {
		t.Fatalf("a parameter with no value must fail loudly, got %v", err)
	}
}

func TestTranspileRejectsUndeclaredParameter(t *testing.T) {
	parsed, _ := Parse([]byte(`Resources: {Q: {Type: AWS::SQS::Queue}}`))
	_, _, err := Transpile(parsed, TranspileOptions{Parameters: map[string]string{"Ghost": "x"}})
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("supplying an undeclared parameter should fail, got %v", err)
	}
}

func TestTranspileConditionsGateResources(t *testing.T) {
	tmpl := `
Parameters:
  Stage: {Type: String, Default: dev}
Conditions:
  IsProd: !Equals [!Ref Stage, prod]
Resources:
  Always:
    Type: AWS::SQS::Queue
  OnlyProd:
    Type: AWS::SQS::Queue
    Condition: IsProd
`
	parsed, _ := Parse([]byte(tmpl))
	stack, rep, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stack.Queues["Always"]; !ok {
		t.Error("unconditional resource missing")
	}
	if _, ok := stack.Queues["OnlyProd"]; ok {
		t.Error("a resource gated by a false condition must not be created")
	}
	// It is reported rather than vanishing.
	found := false
	for _, e := range rep.Ignored() {
		if e.LogicalID == "OnlyProd" && strings.Contains(e.Reason, "condition") {
			found = true
		}
	}
	if !found {
		t.Errorf("skipped conditional resource should be reported: %+v", rep.Entries)
	}

	// With the condition true it appears.
	parsed2, _ := Parse([]byte(tmpl))
	stack2, _, _ := Transpile(parsed2, TranspileOptions{Parameters: map[string]string{"Stage": "prod"}})
	if _, ok := stack2.Queues["OnlyProd"]; !ok {
		t.Error("condition true should create the resource")
	}
}

// TestTranspileRejectsUnsupportedType is the guarantee that a template never
// half-deploys: an unmodellable service fails the whole transpile.
func TestTranspileRejectsUnsupportedType(t *testing.T) {
	tmpl := `
Resources:
  Q: {Type: AWS::SQS::Queue}
  Cluster: {Type: AWS::ECS::Cluster}
`
	parsed, _ := Parse([]byte(tmpl))
	_, rep, err := Transpile(parsed, TranspileOptions{})
	if err == nil {
		t.Fatal("an unsupported resource type should fail the transpile")
	}
	if !strings.Contains(err.Error(), "ECS") {
		t.Errorf("error should name the service, got %v", err)
	}
	// The report still explains what happened.
	if _, _, rejected := rep.Counts(); rejected != 1 {
		t.Errorf("report should record the rejection: %+v", rep.Entries)
	}

	// AllowUnsupported downgrades it to a warning.
	parsed2, _ := Parse([]byte(tmpl))
	stack, rep2, err := Transpile(parsed2, TranspileOptions{AllowUnsupported: true})
	if err != nil {
		t.Fatalf("AllowUnsupported should not fail: %v", err)
	}
	if _, ok := stack.Queues["Q"]; !ok {
		t.Error("the supported resource should still be mapped")
	}
	if _, _, rejected := rep2.Counts(); rejected != 1 {
		t.Error("the rejection should still be reported")
	}
}

func TestTranspileStandaloneSubscription(t *testing.T) {
	// The subscription is declared BEFORE the topic, so it can only work if
	// attachment is deferred.
	tmpl := `
Resources:
  Sub:
    Type: AWS::SNS::Subscription
    Properties:
      TopicArn: !Ref Events
      Protocol: sqs
      Endpoint: !GetAtt Q.Arn
      RawMessageDelivery: true
  Events:
    Type: AWS::SNS::Topic
  Q:
    Type: AWS::SQS::Queue
`
	parsed, _ := Parse([]byte(tmpl))
	stack, _, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	topic := stack.Topics["Events"]
	if len(topic.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %+v", topic.Subscriptions)
	}
	if topic.Subscriptions[0].Queue != "Q" || !topic.Subscriptions[0].Raw {
		t.Errorf("subscription = %+v", topic.Subscriptions[0])
	}
}

func TestTranspileEventSourceMapping(t *testing.T) {
	tmpl := `
Resources:
  Q:
    Type: AWS::SQS::Queue
  Fn:
    Type: AWS::Lambda::Function
    Properties:
      Runtime: provided.al2
      Handler: bootstrap
      Code:
        S3Bucket: _local_
        S3Key: ./build
  ESM:
    Type: AWS::Lambda::EventSourceMapping
    Properties:
      FunctionName: !Ref Fn
      EventSourceArn: !GetAtt Q.Arn
      BatchSize: 5
`
	parsed, _ := Parse([]byte(tmpl))
	stack, _, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	fn := stack.Functions["Fn"]
	if fn.Code != "./build" || fn.Runtime != "provided.al2" {
		t.Errorf("function = %+v", fn)
	}
	if len(fn.Triggers) != 1 || fn.Triggers[0].Queue != "Q" || fn.Triggers[0].Batch != 5 {
		t.Errorf("triggers = %+v", fn.Triggers)
	}
}

func TestTranspileEventBridgeRule(t *testing.T) {
	tmpl := `
Resources:
  Q:
    Type: AWS::SQS::Queue
  Rule:
    Type: AWS::Events::Rule
    Properties:
      Name: on-order
      EventPattern:
        source: [shop.orders]
      State: ENABLED
      Targets:
        - Arn: !GetAtt Q.Arn
          Id: "1"
          InputPath: $.detail
`
	parsed, _ := Parse([]byte(tmpl))
	stack, _, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	rule := stack.Rules["on-order"]
	if !strings.Contains(rule.Pattern.JSON, "shop.orders") {
		t.Errorf("pattern = %q", rule.Pattern.JSON)
	}
	if len(rule.Targets) != 1 || rule.Targets[0].Queue != "Q" || rule.Targets[0].InputPath != "$.detail" {
		t.Errorf("targets = %+v", rule.Targets)
	}
	if rule.Enabled == nil || !*rule.Enabled {
		t.Errorf("enabled = %v", rule.Enabled)
	}
}

func TestTranspileSAMFunction(t *testing.T) {
	tmpl := `
Transform: AWS::Serverless-2016-10-31
Globals:
  Function:
    Runtime: provided.al2
    Timeout: 30
Resources:
  Orders:
    Type: AWS::SQS::Queue
  Worker:
    Type: AWS::Serverless::Function
    Properties:
      Handler: bootstrap
      CodeUri: ./worker
      Environment:
        Variables:
          LOG_LEVEL: debug
      Events:
        FromQueue:
          Type: SQS
          Properties:
            Queue: !GetAtt Orders.Arn
            BatchSize: 3
        Nightly:
          Type: Schedule
          Properties:
            Schedule: rate(1 day)
`
	parsed, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	stack, rep, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if !rep.SAM {
		t.Error("SAM transform should be reported")
	}
	fn := stack.Functions["Worker"]
	// Globals supply the runtime and timeout.
	if fn.Runtime != "provided.al2" || fn.Timeout != 30 {
		t.Errorf("Globals not applied: %+v", fn)
	}
	if fn.Code != "./worker" || fn.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("function = %+v", fn)
	}
	if len(fn.Triggers) != 1 || fn.Triggers[0].Queue != "Orders" || fn.Triggers[0].Batch != 3 {
		t.Errorf("SQS event did not become a trigger: %+v", fn.Triggers)
	}
	if len(stack.Rules) != 1 {
		t.Errorf("Schedule event did not become a rule: %+v", stack.Rules)
	}
	for _, r := range stack.Rules {
		if r.Schedule != "rate(1 day)" || r.Targets[0].Lambda != "Worker" {
			t.Errorf("schedule rule = %+v", r)
		}
	}
}

// TestTranspileSAMApiEvent covers what phase 4 unblocked: an Api event used to
// be refused because a function behind it would never have been reachable.
// Now it becomes a route on an API.
func TestTranspileSAMApiEvent(t *testing.T) {
	tmpl := `
Transform: AWS::Serverless-2016-10-31
Resources:
  Api:
    Type: AWS::Serverless::Function
    Properties:
      Handler: bootstrap
      CodeUri: ./api
      Events:
        GetOne:
          Type: Api
          Properties:
            Path: /orders/{id}
            Method: get
        CreateOne:
          Type: Api
          Properties:
            Path: /orders
            Method: post
`
	parsed, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	stack, _, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("an Api event should now transpile: %v", err)
	}
	api, ok := stack.APIs["ServerlessRestApi"]
	if !ok {
		t.Fatalf("apis = %v", stack.APIs)
	}
	if len(api.Routes) != 2 {
		t.Fatalf("routes = %+v", api.Routes)
	}
	byPath := map[string]string{}
	for _, r := range api.Routes {
		byPath[r.Path] = r.Method
		if r.Lambda != "Api" {
			t.Errorf("route %s targets %q, want the function", r.Path, r.Lambda)
		}
	}
	if byPath["/orders/{id}"] != "GET" || byPath["/orders"] != "POST" {
		t.Fatalf("routes = %v", byPath)
	}
	if api.Stage != "Prod" {
		t.Errorf("stage = %q, want SAM's default Prod", api.Stage)
	}
}

func TestTranspileKMSAliasRenamesKey(t *testing.T) {
	tmpl := `
Resources:
  DataKey:
    Type: AWS::KMS::Key
    Properties:
      Description: app data
      EnableKeyRotation: true
  DataAlias:
    Type: AWS::KMS::Alias
    Properties:
      AliasName: alias/app-key
      TargetKeyId: !Ref DataKey
`
	parsed, _ := Parse([]byte(tmpl))
	stack, _, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	// The stack file addresses keys by alias, so the alias renames the key.
	k, ok := stack.Keys["app-key"]
	if !ok {
		t.Fatalf("keys = %+v", stack.Keys)
	}
	if !k.Rotation || k.Description != "app data" {
		t.Errorf("key = %+v", k)
	}
	if _, stillThere := stack.Keys["DataKey"]; stillThere {
		t.Error("the key should have been renamed, not duplicated")
	}
}

func TestTranspileSecretsAndParameters(t *testing.T) {
	tmpl := `
Resources:
  Conf:
    Type: AWS::SecretsManager::Secret
    Properties:
      Name: app/config
      SecretString: '{"apiKey":"local"}'
  Generated:
    Type: AWS::SecretsManager::Secret
    Properties:
      Name: app/generated
      GenerateSecretString:
        SecretStringTemplate: '{"user":"app"}'
  DbHost:
    Type: AWS::SSM::Parameter
    Properties:
      Name: /app/db/host
      Type: String
      Value: localhost
`
	parsed, _ := Parse([]byte(tmpl))
	stack, _, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if s := stack.Secrets["app/config"]; !strings.Contains(s.Value, "apiKey") {
		t.Errorf("secret = %+v", s)
	}
	// A generated secret gets the template as a placeholder rather than being absent.
	if s := stack.Secrets["app/generated"]; !strings.Contains(s.Value, "user") {
		t.Errorf("generated secret = %+v", s)
	}
	if p := stack.Parameters["/app/db/host"]; p.Value != "localhost" || p.Type != "String" {
		t.Errorf("parameter = %+v", p)
	}
}

func TestTranspileNameDefaultsToLogicalID(t *testing.T) {
	parsed, _ := Parse([]byte(`Resources: {MyQueue: {Type: AWS::SQS::Queue}}`))
	stack, rep, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// No random suffix — the logical ID is the name, which is what makes a
	// local stack addressable by hand.
	if _, ok := stack.Queues["MyQueue"]; !ok {
		t.Fatalf("queues = %v", stack.Queues)
	}
	if rep.Entries[0].Name != "MyQueue" {
		t.Errorf("report name = %q", rep.Entries[0].Name)
	}
}
