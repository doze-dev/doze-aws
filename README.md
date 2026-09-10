# doze-aws

Local AWS services, built from scratch in Go. One small static binary that
speaks the real AWS wire protocols — no Docker, no JVM, no cloud.

```sh
doze-aws
# listening on 127.0.0.1:4566
```

Point any AWS SDK at it and go:

```sh
export AWS_ENDPOINT_URL=http://127.0.0.1:4566
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

aws sts get-caller-identity
```

`127.0.0.1:4566` is a permanent address — the port is LocalStack's on purpose,
and it will never require the `doze` CLI. Local DNS names like
`aws.<stack>.doze` are additive on top of it, and switching between them does
not strand what you already created. → **[Endpoints — the contract](docs/endpoints.md)**

## What it is

doze-aws emulates the AWS services a development stack leans on, implemented
from the wire format up and verified against the real AWS SDKs — both
generations (aws-sdk-go v1 and aws-sdk-go-v2 / boto3-era and modern), both
signature versions (SigV2 and SigV4), and the legacy Query protocols older
clients still speak.

| Service | Operations | Input validation |
|---|---|---|
| STS | ✅ | **fully audited**: 7 of 8 dispatched operations · 108/108 constraint cases · no known gaps |
| SQS | ✅ both protocols, FIFO, DLQ redrive, long polling, move tasks, tags | **fully audited**: 21 of 22 dispatched operations · 48/48 constraint cases, plus hand-derived attribute and queue-name checks · no known gaps |
| SNS | ✅ fanout to SQS/Lambda/webhooks, filter policies, confirmation handshake, delivery status logs to CloudWatch Logs | **fully audited**: all 19 dispatched operations · 53/53 constraint cases · no known gaps |
| KMS | ✅ symmetric + asymmetric (RSA/ECC) + HMAC, real stdlib crypto | **fully audited**: all 36 dispatched operations · 263/263 constraint cases · no known gaps |
| SSM Parameter Store | ✅ versions, labels, hierarchies, SecureString at-rest encryption | **fully audited**: all 13 dispatched operations · 100/100 constraint cases · no known gaps |
| Secrets Manager | ✅ version stages, recovery-window deletion, encrypted at rest | **fully audited**: 19 of 20 dispatched operations · 132/132 constraint cases · no known gaps |
| S3 | ✅ versioning, multipart, full checksum/chunked matrix, CORS, lifecycle, object lock, website, public access block enforced on bucket policies | **fully audited**: all 74 routed operations · 236/296 constraint cases · 60 not expressible on the wire · no known gaps |
| DynamoDB | ✅ full expression engine, GSI/LSI, transactions, TTL, paging semantics | **fully audited**: all 27 dispatched operations · 333/333 constraint cases · no known gaps |
| EventBridge | ✅ full pattern language, SQS/SNS/Lambda/CloudWatch Logs/API destination targets, cron and rate schedules, connections with real Basic/API key/OAuth delivery, input transformers | **fully audited**: all 40 dispatched operations · 448/448 constraint cases · no known gaps |
| Lambda | ✅ real host processes speaking the Runtime API (no Docker) — Python, Node and Ruby through embedded clients on the host interpreter, Go and `provided.*` as is, Java and .NET with AWS's own packaging; edit-in-place code; every line logged with its request id; versions that freeze, aliases, layers on the search paths, function URLs served, SQS/DynamoDB/Kinesis event source mappings | **fully audited**: all 47 routed operations with constrained input · 612/621 constraint cases · 9 not expressible on the wire · no known gaps |
| Kinesis | ✅ native Go (no JVM), real partition-key routing, resharding with parent/child lineage | **fully audited**: 32 of 35 dispatched operations · 356/356 constraint cases · no known gaps |
| IAM | ✅ real policy evaluation, on by default in `soft` — evaluated and logged, never blocked — with least-privilege generation; under `soft` and `enforce` bucket, queue, topic, key, secret and stream policies and Lambda permissions are evaluated too, so an S3 notification needs its Lambda permission exactly as on AWS | **fully audited**: 89 of 93 dispatched operations · 702/702 constraint cases · no known gaps |
| CloudFormation | ✅ stacks, nested stacks, change sets, deletion — `sam deploy`, `cdk deploy` and Serverless all work | **fully audited**: 22 of 23 dispatched operations · 182/182 constraint cases · no known gaps |
| API Gateway | ✅ REST v1 and HTTP APIs (v2) — deployed APIs actually serve into Lambda over a real HTTP endpoint; an HTTP API answers at its `$default` stage with payload 2.0 events, CORS and REQUEST authorizers; TOKEN and REQUEST Lambda authorizers gate methods with the policy the function answers; API keys and usage plans gate methods that require a key; stage access and execution logs written to CloudWatch Logs; a CDK `RestApi`'s Resource and Method tree deploys as declared | **fully audited**: all 47 routed operations with constrained input · 118/126 constraint cases · 8 not expressible on the wire · no known gaps |
| Step Functions | ✅ All 37 operations: Standard and Express, JSONPath and JSONata, versions and aliases, activities, redrive, Distributed Map with Map Runs, child executions (`.sync`), task tokens; Lambda/SQS/SNS/DynamoDB/EventBridge and `aws-sdk:` integrations for every local service; history vended to CloudWatch Logs per `loggingConfiguration`, Express runs included | **fully audited**: 33 of 37 operations · 229/229 constraint cases · 19 cases consume the state they address · no known gaps |
| CloudWatch Logs | ✅ log groups, streams and events — Lambda output, Step Functions history, API Gateway access and execution logs, EventBridge deliveries and SNS delivery status land where they do on AWS, each line with its request id, and `aws logs tail --follow`, `sam logs` and the console read it; subscription filters forward matching lines to Lambda and Kinesis in AWS's gzip envelope | **fully audited**: the 21 dispatched operations · 197/197 constraint cases · 97 refused by name · no known gaps |

**Why two columns.** A ✅ means every documented operation of that service has a
real handler, verified against both AWS SDK generations. It does **not** mean
doze-aws refuses everything AWS refuses, and those are different promises. An
emulator that is too permissive is the more dangerous kind: your code passes
here and fails on deploy, which is the one place the cost is real.

The right-hand column says where that has actually been checked. "Not yet
audited" means exactly that — no claim either way, not a known failure. The
audit is in progress and its state lives in [`docs/api-support/`](docs/api-support/),
one page per service; `cmd/dzaudit` derives the checklist from AWS's own service
models, and each service gets a rejection-parity suite as it lands
(`*/rejection_parity_test.go`).

**Operations first, cases second, and the order is the point.** The operation
count is the claim that matters: every operation the model documents is either
handled or refused by name, so nothing falls through to a confusing
`InvalidAction`. That is enforced per service by a frozen model list
(`*/coverage_test.go`). The case count underneath is the mechanical long tail —
lengths, patterns, enums, required members — and it is a weaker number by
nature: nobody writes a 513-character description by accident, and AWS would
have caught it at deploy. It is worth having and it is not the score. A number
that goes up when you generate more padding should never be the headline.

If a gap above bites you, it is a bug worth reporting — the goal is an empty
right-hand column.

All 16 services talk to each other: EventBridge→SQS/SNS/Lambda/HTTP API destinations, S3
notifications→SQS/SNS/Lambda, SNS→SQS/Lambda/webhooks, SQS/DynamoDB
streams/Kinesis→Lambda, API Gateway→Lambda, Step Functions→Lambda/SQS/SNS
and back through task tokens, Lambda/Step Functions/API Gateway/EventBridge/SNS→CloudWatch Logs,
CloudWatch Logs subscription filters→Lambda/Kinesis.

## Deploy with the tooling you already have

There is no doze-specific file format. Point your existing deployment tool at
the endpoint and it works:

```sh
aws cloudformation deploy --template-file template.yaml --stack-name shop
sam deploy --stack-name shop --s3-bucket artifacts
cdk bootstrap && cdk deploy
serverless package && aws cloudformation deploy \
  --template-file .serverless/cloudformation-template-update-stack.json --stack-name sls-dev
```

Stacks are real: they own their resources, and `delete-stack` (or `cdk destroy`)
takes them back. See [docs/cloudformation.md](docs/cloudformation.md).

Per-service operation coverage lives in [docs/api-support](docs/api-support/).

## Design ground rules

- **Lightweight above all.** Three runtime dependencies: bbolt, a TOML parser
  and a YAML parser. Data persists across restarts under one directory you can
  delete.
- **Real protocols, honest boundaries.** Every documented operation of an
  implemented service gets a handler: functional where locally meaningful,
  faithful config round-trips where the effect is cloud-infrastructure-only,
  and a clean error where emulation would be a lie.
- **Embeddable.** Each service is a plain Go package exporting an
  `http.Handler` (`sts.New`, `sqs.New`, ...), and `dozeaws.NewStack` assembles
  any subset behind one gateway — the binary is a thin wrapper around exactly
  that API.

```go
stack, _ := dozeaws.NewStack(dozeaws.StackConfig{DataDir: "./data"})
defer stack.Close()
http.ListenAndServe("127.0.0.1:4566", stack.Handler())
```

## Part of doze

doze-aws is a sibling of [doze](https://github.com/doze-dev/doze) — the
resource-friendly local dev environment — and powers its AWS modules. It works
just as happily standalone.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/doze-dev/doze-aws/main/install.sh | sh
```

Or build from source: `go build ./cmd/doze-aws` (Go 1.26+).

## License

Apache 2.0 — see [LICENSE](LICENSE).
