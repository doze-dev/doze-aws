# Getting started

doze-aws is one small static binary that emulates the AWS services a
development stack leans on — built from the wire protocol up, verified against
both AWS SDK generations. No Docker, no JVM, no cloud.

## Run it

```sh
doze-aws
# msg=listening addr=127.0.0.1:4566 services=s3,dynamodb,sqs,sns,sts,kms,ssm,secretsmanager,eventbridge,lambda
```

Point any AWS SDK or the CLI at it:

```sh
export AWS_ENDPOINT_URL=http://127.0.0.1:4566
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

aws s3 mb s3://my-bucket
aws s3 cp ./file.txt s3://my-bucket/
aws dynamodb create-table --table-name t \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH --billing-mode PAY_PER_REQUEST
aws sqs create-queue --queue-name jobs
```

The default port (4566) matches LocalStack's, so existing `AWS_ENDPOINT_URL`
setups work unchanged.

## Configure

Everything is a flag or a TOML key (flags win). Print the effective config:

```sh
doze-aws config print
```

Copy [`doze-aws.example.toml`](../doze-aws.example.toml) to `./doze-aws.toml`
(auto-loaded) to enable a subset of services, change the port, or set the data
directory:

```toml
listen   = "127.0.0.1:4566"
data-dir = "./data"
services = ["s3", "dynamodb", "sqs"]
```

## Persistence

Data lives under `data/<service>/` and survives restarts. Delete the directory
to reset. There is nothing else to clean up.

## Where things run

Everything is behind one endpoint. The gateway routes each request to the right
service by its wire signals (the `X-Amz-Target` header, the SigV4 scope, the
request path, or the S3 fallback) — exactly how AWS SDKs address a custom
endpoint. Cross-service features work out of the box: an S3 event notification
lands in SQS, an SNS publish fans out to SQS and Lambda, an EventBridge rule
routes to its targets, a Lambda handler reaches every sibling service.

## Lambda

Functions run as **real local processes** speaking the AWS Lambda Runtime API.
For quick local iteration, point a function at code in place instead of zipping
it (the `_local_` extension — see [api-support/lambda.md](api-support/lambda.md)).

## Coverage

Every implemented service documents its operation support in
[api-support/](api-support/): **F**unctional (real local semantics),
**C**osmetic (config round-trips, no local effect), or an honest **S**tub for
what's physically meaningless locally.
