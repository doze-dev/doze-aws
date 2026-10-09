# A realistic slice of one application, applied by the real AWS provider.
#
# What this proves is not that these resources can be created — the Go and boto3
# suites do that — but that the provider's whole conversation with the service
# works: create, read back what it just wrote, and agree with what it reads. The
# second `terraform plan` in smoke.sh must find nothing to change; a field
# doze-aws drops or rewrites shows up there as drift, which is how a Terraform
# user would meet it.

terraform {
  required_providers {
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.0"
    }
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

variable "endpoint" {
  type = string
}

provider "aws" {
  region     = "us-east-1"
  access_key = "test"
  secret_key = "test"

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true

  endpoints {
    apigateway     = var.endpoint
    cloudformation = var.endpoint
    cloudwatch     = var.endpoint
    cloudwatchlogs = var.endpoint
    dynamodb       = var.endpoint
    events         = var.endpoint
    iam            = var.endpoint
    kinesis        = var.endpoint
    kms            = var.endpoint
    lambda         = var.endpoint
    s3             = var.endpoint
    secretsmanager = var.endpoint
    sfn            = var.endpoint
    sns            = var.endpoint
    sqs            = var.endpoint
    ssm            = var.endpoint
    sts            = var.endpoint
  }
}

resource "aws_kms_key" "app" {
  description             = "harbour"
  deletion_window_in_days = 7
}

resource "aws_s3_bucket" "receipts" {
  bucket = "harbour-tf-receipts"
}

resource "aws_s3_bucket_versioning" "receipts" {
  bucket = aws_s3_bucket.receipts.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_sqs_queue" "dlq" {
  name = "harbour-tf-orders-dlq"
}

resource "aws_sqs_queue" "orders" {
  name                       = "harbour-tf-orders"
  visibility_timeout_seconds = 45
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = 3
  })
}

resource "aws_sns_topic" "alerts" {
  name = "harbour-tf-alerts"
}

resource "aws_sns_topic_subscription" "alerts_to_orders" {
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "sqs"
  endpoint  = aws_sqs_queue.orders.arn
}

resource "aws_dynamodb_table" "sessions" {
  name         = "harbour-tf-sessions"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"
  range_key    = "ts"

  attribute {
    name = "id"
    type = "S"
  }
  attribute {
    name = "ts"
    type = "N"
  }
  ttl {
    attribute_name = "expires"
    enabled        = true
  }
}

resource "aws_ssm_parameter" "region" {
  name  = "/harbour/tf/region"
  type  = "String"
  value = "eu-west-1"
}

resource "aws_secretsmanager_secret" "db" {
  name                    = "harbour/tf/db"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "db" {
  secret_id     = aws_secretsmanager_secret.db.id
  secret_string = jsonencode({ user = "app", password = "hunter2" })
}

resource "aws_kinesis_stream" "events" {
  name        = "harbour-tf-events"
  shard_count = 1
}

resource "aws_cloudwatch_log_group" "app" {
  name              = "/harbour/tf/app"
  retention_in_days = 7
}

resource "aws_cloudwatch_metric_alarm" "backlog" {
  alarm_name          = "harbour-tf-backlog"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 60
  statistic           = "Maximum"
  threshold           = 100
  dimensions = {
    QueueName = aws_sqs_queue.orders.name
  }
  alarm_actions = [aws_sns_topic.alerts.arn]
}

resource "aws_cloudwatch_event_rule" "nightly" {
  name                = "harbour-tf-nightly"
  schedule_expression = "rate(1 day)"
}

resource "aws_cloudwatch_event_target" "nightly_to_queue" {
  rule = aws_cloudwatch_event_rule.nightly.name
  arn  = aws_sqs_queue.orders.arn
}

data "aws_iam_policy_document" "assume_lambda" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "fn" {
  name               = "harbour-tf-fn"
  assume_role_policy = data.aws_iam_policy_document.assume_lambda.json
}

resource "aws_iam_role_policy" "fn" {
  name = "queue-access"
  role = aws_iam_role.fn.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["sqs:SendMessage", "sqs:ReceiveMessage"]
      Resource = aws_sqs_queue.orders.arn
    }]
  })
}

data "archive_file" "fn" {
  type        = "zip"
  output_path = "${path.module}/fn.zip"
  source {
    filename = "index.py"
    content  = "def handler(event, context):\n    return {'ok': True}\n"
  }
}

resource "aws_lambda_function" "fn" {
  function_name    = "harbour-tf-fn"
  role             = aws_iam_role.fn.arn
  runtime          = "python3.12"
  handler          = "index.handler"
  filename         = data.archive_file.fn.output_path
  source_code_hash = data.archive_file.fn.output_base64sha256
  timeout          = 10
  environment {
    variables = {
      QUEUE = aws_sqs_queue.orders.url
    }
  }
}

resource "aws_iam_role" "sfn" {
  name = "harbour-tf-sfn"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "states.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_sfn_state_machine" "flow" {
  name     = "harbour-tf-flow"
  role_arn = aws_iam_role.sfn.arn
  definition = jsonencode({
    StartAt = "Hello"
    States = {
      Hello = { Type = "Pass", Result = "hi", End = true }
    }
  })
}

output "queue_url" {
  value = aws_sqs_queue.orders.url
}

# The other billing mode, with an index: its own capacity, and the index's.
resource "aws_dynamodb_table" "ledger" {
  name           = "harbour-tf-ledger"
  billing_mode   = "PROVISIONED"
  read_capacity  = 5
  write_capacity = 7
  hash_key       = "account"
  range_key      = "entry"

  attribute {
    name = "account"
    type = "S"
  }
  attribute {
    name = "entry"
    type = "S"
  }
  attribute {
    name = "day"
    type = "S"
  }

  global_secondary_index {
    name            = "by-day"
    hash_key        = "day"
    projection_type = "ALL"
    read_capacity   = 2
    write_capacity  = 3
  }

  tags = { app = "harbour" }
}
