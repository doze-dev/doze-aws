# CLI reference

`doze-aws` is a single static binary. Run it with no arguments to serve every
implemented service on one endpoint; it runs in the foreground until you
interrupt it (Ctrl-C).

```sh
doze-aws
# msg=listening addr=127.0.0.1:4566 services=s3,dynamodb,sqs,sns,sts,kms,ssm,secretsmanager,eventbridge,lambda
```

## Commands

| Command | What it does |
|---|---|
| `doze-aws` | Serve the enabled services on the shared endpoint (the default). If `./stack.yaml` exists (or `--stack` names a file), it is applied at boot. |
| `doze-aws apply [--var k=v ...] [file]` | Converge the stack toward a declarative `stack.yaml` (default `./stack.yaml`): create what's missing, cheaply update what exists, never delete. Targets the running server if one is listening, the data dir otherwise. See [stack-file.md](stack-file.md). |
| `doze-aws export` | Write the running stack (queues, tables, buckets, functions, wiring, …) to stdout as a `stack.yaml`. Secret values are left blank on purpose. |
| `doze-aws version` | Print the build version and the list of implemented services. |
| `doze-aws config print [flags]` | Resolve and print the effective configuration (defaults + config file + flags), then exit. Use it to see exactly what a given invocation would run. |

There is no daemon and nothing to install or clean up: state lives under the
data directory, and deleting it resets everything.

## Flags

Flags apply to serving and to `config print`.

| Flag | Default | Meaning |
|---|---|---|
| `--config <path>` | `./doze-aws.toml` if present | Path to a TOML config file. |
| `--listen <host:port>` | `127.0.0.1:4566` | Address the shared endpoint binds. 4566 matches LocalStack, so existing `AWS_ENDPOINT_URL` setups work unchanged. |
| `--data-dir <dir>` | `./data` | Root directory; each service gets its own subdirectory beneath it. |
| `--services <a,b,…>` | all implemented | Comma-separated subset of services to enable. Unknown names are an error. |
| `--s3-host <host>` | (none) | Base host for virtual-hosted-style S3 addressing (`<bucket>.<host>`). Path-style always works regardless. |
| `--stack <path>` | `./stack.yaml` if present | Declarative stack file to apply at boot. See [stack-file.md](stack-file.md). |

```sh
# Only S3 + SQS, on a custom port, with data under /tmp/aws
doze-aws --services s3,sqs --listen 127.0.0.1:9000 --data-dir /tmp/aws
```

Service names: `s3`, `dynamodb`, `sqs`, `sns`, `sts`, `kms`, `ssm`,
`secretsmanager`, `eventbridge`, `lambda`.

## Config file

Instead of flags, put settings in `doze-aws.toml` (auto-loaded from the working
directory, or point `--config` at one elsewhere):

```toml
listen   = "127.0.0.1:4566"
data-dir = "./data"
services = ["s3", "dynamodb", "sqs"]

[s3]
host = "s3.localhost"   # enables http://<bucket>.s3.localhost addressing
```

Every key is optional; omitted keys fall back to the defaults above.

**Precedence** (lowest to highest): built-in defaults → config file → flags. A
key set in the file survives unless the matching flag is explicitly passed, so
you can keep a committed `doze-aws.toml` and override one thing on the command
line. Run `doze-aws config print` to see the resolved result.

## Talking to it

Point any AWS SDK or the AWS CLI at the endpoint. Credentials are not verified,
so any non-empty values work; the region is `us-east-1`.

```sh
export AWS_ENDPOINT_URL=http://127.0.0.1:4566
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

aws sts get-caller-identity
aws s3 mb s3://my-bucket
aws s3 cp ./file.txt s3://my-bucket/
aws dynamodb create-table --table-name t \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH --billing-mode PAY_PER_REQUEST
aws sqs create-queue --queue-name jobs
```

Both AWS SDK generations work (v1 `aws-sdk-go` / boto-era and v2
`aws-sdk-go-v2`), and both signature versions (SigV2 and SigV4) are accepted.

Per-SDK endpoint variables are honored too, so you can send one service
elsewhere: `AWS_ENDPOINT_URL_S3`, `AWS_ENDPOINT_URL_DYNAMODB`, etc.

## Persistence & resetting

Data is written under `--data-dir` (`./data` by default), one subdirectory per
service, and survives restarts. To reset a service, stop `doze-aws` and delete
its subdirectory; to reset everything, delete the whole data directory. There is
nothing else to tear down.

## See also

- [getting-started.md](getting-started.md) — a first run, end to end.
- [embedding.md](embedding.md) — use doze-aws as a Go library instead of a CLI.
- [api-support/](api-support/) — per-service operation support (Functional /
  Cosmetic / honest Stub).
