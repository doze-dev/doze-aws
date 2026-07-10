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

## What it is

doze-aws emulates the AWS services a development stack leans on, implemented
from the wire format up and verified against the real AWS SDKs — both
generations (aws-sdk-go v1 and aws-sdk-go-v2 / boto3-era and modern), both
signature versions (SigV2 and SigV4), and the legacy Query protocols older
clients still speak.

| Service | Status |
|---|---|
| STS | ✅ complete |
| SQS | 🚧 next (porting from doze) |
| SNS | 🚧 next (porting from doze) |
| KMS, SSM, Secrets Manager | 🗺 roadmap |
| S3 | 🗺 roadmap |
| DynamoDB | 🗺 roadmap |
| EventBridge, Lambda | 🗺 roadmap |

Per-service operation coverage lives in [docs/api-support](docs/api-support/).

## Design ground rules

- **Lightweight above all.** Runtime dependencies are bbolt and a TOML parser.
  Data persists across restarts under one directory you can delete.
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
