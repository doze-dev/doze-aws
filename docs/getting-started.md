# Getting started

doze-aws is one small static binary that emulates the AWS services a
development stack leans on — built from the wire protocol up, verified against
both AWS SDK generations. No Docker, no JVM, no cloud.

## Run it

```sh
doze-aws dns-setup            # one sudo, once per machine
cd ~/code/harbour && doze-aws
# msg="reachable at" url=http://aws.harbour.doze as=name
```

The instance is named after the directory, so each project gets its own. Point
any AWS SDK or the CLI at it:

```sh
eval "$(doze-aws env)"

aws s3 mb s3://my-bucket
aws s3 cp ./file.txt s3://my-bucket/
aws dynamodb create-table --table-name t \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH --billing-mode PAY_PER_REQUEST
aws sqs create-queue --queue-name jobs
```

Queue URLs, invoke URLs and function URLs come back AWS-shaped under the
instance name — `http://sqs.us-east-1.aws.harbour.doze/000000000000/jobs` — so
what you copy out of a response is the shape you would get from AWS with the
suffix swapped.

No DNS available (CI, a container)? `doze-aws --listen 127.0.0.1:4566` serves an
address instead. See [endpoints.md](endpoints.md).

## Configure

Everything is a flag or a TOML key (flags win). Print the effective config:

```sh
doze-aws config print
```

Copy [`doze-aws.example.toml`](../doze-aws.example.toml) to `./doze-aws.toml`
(auto-loaded) to name the instance, enable a subset of services, or set the
data directory:

```toml
name     = "harbour"
region   = "ap-south-1"
data-dir = "data"       # relative to THIS FILE, not to your shell
services = ["s3", "dynamodb", "sqs"]
```

## Persistence

Data lives under `data/<region>/<service>/` — with IAM and STS under
`data/_global/` — and survives restarts. Delete the directory to reset. There is
nothing else to clean up.

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

## Standing up a stack

doze-aws has no file format of its own. Deploy with whatever you already use —
the AWS CLI, SAM, CDK or Serverless Framework all work against the endpoint:

```sh
aws cloudformation deploy --template-file template.yaml --stack-name shop
```

`doze-aws` also applies `./template.yaml` at boot if one is present, so "clone
the repo, run doze-aws" is the whole onboarding story. See
[cloudformation.md](cloudformation.md).
