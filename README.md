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
| STS | ✅ | not yet audited |
| SQS | ✅ both protocols, FIFO, DLQ redrive, long polling, move tasks, tags | **audited**: attributes, redrive, queue-name charset · no known gaps |
| SNS | ✅ fanout to SQS/webhooks, filter policies, confirmation handshake | **audited**: `Subscribe` protocol · no known gaps |
| KMS | ✅ symmetric + asymmetric (RSA/ECC) + HMAC, real stdlib crypto | not yet audited |
| SSM Parameter Store | ✅ versions, labels, hierarchies, SecureString at-rest encryption | not yet audited |
| Secrets Manager | ✅ version stages, recovery-window deletion, encrypted at rest | not yet audited |
| S3 | ✅ versioning, multipart, full checksum/chunked matrix, CORS, lifecycle, object lock, website | **audited**: bucket naming (case, charset, length, IP-form) · rest not yet audited |
| DynamoDB | ✅ full expression engine, GSI/LSI, transactions, TTL, paging semantics | **fully audited**: 333/333 across all 27 dispatched operations · no known gaps |
| EventBridge | ✅ full pattern language, SQS/SNS/Lambda targets, input transformers | not yet audited |
| Lambda | ✅ real process runtime (no Docker), versions, layers, function URLs, SQS/DynamoDB/Kinesis event source mappings | **audited**: `MemorySize`, `Timeout` · rest not yet audited |
| Kinesis | ✅ native Go (no JVM), real partition-key routing, resharding with parent/child lineage | **fully audited**: 356/356 across 32 of 35 dispatched operations · no known gaps |
| IAM | ✅ real policy evaluation, off by default, with least-privilege generation | not yet audited |
| CloudFormation | ✅ stacks, change sets, deletion — `sam deploy`, `cdk deploy` and Serverless all work | not yet audited |
| API Gateway | ✅ REST v1 — deployed APIs actually serve into Lambda over a real HTTP endpoint | not yet audited |

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

If a gap above bites you, it is a bug worth reporting — the goal is an empty
right-hand column.

All 14 services talk to each other: EventBridge→SQS/SNS/Lambda, S3
notifications→SQS/SNS/Lambda, SNS→SQS/Lambda/webhooks, SQS/DynamoDB
streams/Kinesis→Lambda, API Gateway→Lambda.

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
