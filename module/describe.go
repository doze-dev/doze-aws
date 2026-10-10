package aws

import "github.com/doze-dev/doze-sdk/engine"

// Describe implements engine.Describer: the catalog metadata for the aws
// engine. Versionless — the whole doze-aws stack ships inside the plugin.
func (Driver) Describe() engine.Description {
	return engine.Description{
		Title:        "AWS",
		Tagline:      "The whole local AWS as ONE service — with its web console.",
		Category:     "cloud",
		Description:  "Every doze-aws service — S3, SQS, SNS, DynamoDB, Lambda, EventBridge, Step Functions, Kinesis, API Gateway, CloudFormation, IAM, KMS, SSM, Secrets Manager, STS — behind one gateway on one endpoint, as one doze instance. Declare buckets, queues, topics, tables, functions, rules, keys, secrets, parameters and state machines as nested blocks (the same resource graph doze-aws compiles a CloudFormation template down to); doze converges them on boot. A stock SDK or the aws CLI points AWS_ENDPOINT_URL at the one endpoint; the web console at /_console manages everything, with the live Flows graph and a Traffic tail capturing every call. One process, no Docker, no JVM, no LocalStack.",
		Port:         4566,
		Source:       "doze/aws",
		Homepage:     "https://github.com/doze-dev/doze-aws/tree/main/module",
		ExampleLabel: "local",
		Example: `aws "local" {
  port = 4566

  bucket "uploads" { versioning = true }
  queue  "emails"  { dlq = "auto"  max_receives = 5 }
  queue  "orders"  { fifo = true  content_dedup = true }

  topic "signups" {
    subscribe { queue = "emails"  raw = true }
  }

  table "sessions" {
    key = "session_id:S"
    ttl = "expires_at"
  }

  function "resize" {
    code    = "./functions/resize"   # dir with a provided.al2 bootstrap
    timeout = 10
  }

  rule "order_placed" {
    pattern = "{\"source\":[\"shop.checkout\"]}"
    targets = ["lambda:resize", "queue:emails"]
  }

  key "app_key" {}
  secret "db_password" { value = "s3cr3t" }
  parameter "/app/feature/banner" { value = "hello" }

  state_machine "order_flow" {
    definition = <<-ASL
      {
        "StartAt": "Resize",
        "States": {
          "Resize": {
            "Type": "Task",
            "Resource": "arn:aws:lambda:us-east-1:000000000000:function:resize",
            "Retry": [{"ErrorEquals": ["States.TaskFailed"], "MaxAttempts": 2}],
            "Next": "Notify"
          },
          "Notify": {
            "Type": "Task",
            "Resource": "arn:aws:states:::sqs:sendMessage",
            "Parameters": {"QueueUrl": "http://localhost:4566/000000000000/emails", "MessageBody.$": "$"},
            "End": true
          }
        }
      }
    ASL
  }
}`,
		Blocks: []engine.ConfigBlock{
			{Name: "bucket", Desc: "One S3 bucket (the label is the bucket name).", Args: []engine.ConfigArg{
				{Name: "versioning", Type: "bool", Default: "false", Desc: "Enable object versioning."},
				{Name: "object_lock", Type: "bool", Default: "false", Desc: "Enable object lock (implies versioning)."},
			}},
			{Name: "queue", Desc: "One SQS queue; dlq = \"auto\" creates and wires a dead-letter companion.", Args: []engine.ConfigArg{
				{Name: "fifo", Type: "bool", Default: "false", Desc: "FIFO queue (name gets .fifo)."},
				{Name: "dlq", Type: "string", Desc: "\"auto\" or an existing queue name."},
				{Name: "max_receives", Type: "number", Desc: "Receives before dead-lettering."},
			}},
			{Name: "topic", Desc: "One SNS topic with subscribe blocks (queue/lambda/http, filter, raw)."},
			{Name: "table", Desc: "One DynamoDB table; key uses the \"pk:S sk:N\" shorthand, plus gsi/lsi blocks."},
			{Name: "function", Desc: "One Lambda function running as a real local process (code = local dir)."},
			{Name: "rule", Desc: "One EventBridge rule; targets use \"queue:name\"/\"topic:name\"/\"lambda:name\"."},
			{Name: "key", Desc: "One KMS key with real local crypto."},
			{Name: "secret", Desc: "One Secrets Manager secret (never stomped without force)."},
			{Name: "parameter", Desc: "One SSM parameter (the label is the full /path)."},
			{Name: "state_machine", Desc: "One Step Functions state machine, run locally against the stack's functions, queues and topics.", Args: []engine.ConfigArg{
				{Name: "definition", Type: "string", Required: true, Desc: "The States Language document — a heredoc, or file(\"./flow.asl.json\")."},
				{Name: "role", Type: "string", Desc: "Role ARN; the API requires one and nothing local evaluates it, so it defaults."},
				{Name: "type", Type: "string", Default: "STANDARD", Desc: "STANDARD or EXPRESS (Express executions arrive after Standard ones)."},
			}},
		},
	}
}
