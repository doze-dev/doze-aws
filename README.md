# doze-aws

Local AWS services, built from scratch in Go. One small static binary that
speaks the real AWS wire protocols — no Docker, no JVM, no cloud.

```sh
curl -fsSL https://raw.githubusercontent.com/doze-dev/doze-aws/main/install.sh | sh

cd ~/code/harbour && doze-aws
# reachable at http://aws.harbour.doze
```

The first run on a machine offers to set up `.doze` for you — one prompt, one
sudo, never again:

```
doze-aws addresses itself by name, and .doze does not resolve on this machine yet.
Setting it up needs sudo once — per machine, not per project.
Set it up now? [Y/n]
```

With no terminal to ask on (CI) it changes nothing and tells you the two ways
forward. In a container, skip the name entirely and use `--listen` — see
[getting-started.md](docs/getting-started.md#in-docker-if-that-is-where-your-team-already-is).

Point any AWS SDK at it and go:

```sh
eval "$(doze-aws env)"
aws sts get-caller-identity
```

It tells you where it is and what to do next:

```
  doze-aws is up.

    endpoint  http://aws.harbour.doze
    console   http://aws.harbour.doze/_console/   what your app is doing, live
    serving   17 services in us-east-1, account 000000000000

    eval "$(doze-aws env)"   point this shell at it
    doze-aws doctor          when something looks wrong
```

**There is a console, and it opens on your own traffic.** Not a resource
browser with a traffic tab — the home page *is* the wire: every call your app
makes, in order, with the work each one caused nested underneath it, a request
id you can quote, and a "copy as curl" on each row. Real AWS cannot offer that
view, because real AWS is not sitting between your code and the answer.

Alongside it: every service has pages that read and write real resources —
browse and upload S3 objects, receive and redrive SQS messages, run PartiQL
against DynamoDB, watch a Step Functions execution graph, invoke a Lambda and
tail its logs, simulate an IAM policy. And a **fidelity ledger** per service
saying which operations are functional, which are cosmetic round-trips, and
which are honest stubs — the one table real AWS never has to show you.

`doze-aws doctor` is the first thing to run when something is off: it reports
what `.doze` needs on this machine, what is registered, and what to do about
it. It always exits 0 — being told what is missing is the command working.

An instance answers on **its own name**, taken from the project directory, and
the AWS-shaped hostnames sit beneath it — so a URL it hands back differs from
the real one by the suffix alone:

```
https://sqs.ap-south-1.amazonaws.com/811690671382/orders     AWS
http://sqs.ap-south-1.aws.harbour.doze/811690671382/orders   doze-aws
```

Two projects each get their own name and never contend. No DNS available — CI,
a container, a sibling service over a compose network? `doze-aws --listen
127.0.0.1:4566` serves an address instead, and that is the only other way in.
→ **[Endpoints — where doze-aws answers](docs/endpoints.md)**

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
| Step Functions | ✅ All 37 operations: Standard and Express, JSONPath and JSONata (all but the `%`/`@`/`#` path operators — [measured](docs/SUPPORT.md#the-jsonata-dialect-measured)), versions and aliases, activities, redrive, Distributed Map with Map Runs, child executions (`.sync`), task tokens; Lambda/SQS/SNS/DynamoDB/EventBridge and `aws-sdk:` integrations for every local service; history vended to CloudWatch Logs per `loggingConfiguration`, Express runs included | **fully audited**: 33 of 37 operations · 229/229 constraint cases · 19 cases consume the state they address · no known gaps |
| CloudWatch Logs | ✅ log groups, streams and events — Lambda output, Step Functions history, API Gateway access and execution logs, EventBridge deliveries and SNS delivery status land where they do on AWS, each line with its request id, and `aws logs tail --follow`, `sam logs` and the console read it; subscription filters forward matching lines to Lambda and Kinesis in AWS's gzip envelope; metric filters turn matching lines into CloudWatch metrics on ingest | **fully audited**: the 25 dispatched operations · 237/237 constraint cases · 93 refused by name · no known gaps |
| CloudWatch | ✅ metrics with dimension-correct identity, statistics and exact percentiles over retained samples, and alarms that evaluate M-of-N over completed periods with `TreatMissingData` and notify SNS topics and Lambda functions with AWS's own alarm JSON — the alarm you would deploy, testable before you deploy it; Lambda, API Gateway and Step Functions publish their `AWS/*` metrics unasked, and EMF lines and log metric filters make custom ones. Served on **all three wires**: RPC v2 CBOR (Go v2, Java, Rust), JSON 1.0 (**the AWS CLI**, boto3, JS v3) and Query | **fully audited**: the 19 dispatched operations · 183/183 constraint cases on each of the three wires · 31 refused by name · no known gaps |

**Why two columns.** A ✅ means every documented operation of that service has a
real handler, verified against both AWS SDK generations. It does **not** mean
doze-aws refuses everything AWS refuses, and those are different promises. An
emulator that is too permissive is the more dangerous kind: your code passes
here and fails on deploy, which is the one place the cost is real.

The right-hand column says where that has actually been checked. "Not yet
audited" means exactly that — no claim either way, not a known failure. The
audit is in progress and its state lives in [`docs/SUPPORT.md`](docs/SUPPORT.md),
one page per service; `cmd/dzaudit` derives the checklist from AWS's own service
models, and each service gets a rejection-parity suite as it lands
(`*/rejection_parity_test.go`).

**Operations first, cases second, and the order is the point.** The operation
count is the claim that matters: every operation the model documents is either
handled or refused by name, so nothing falls through to a confusing
`InvalidAction`. That is enforced per service by a model-derived operation list
(`*/coverage_test.go`, reading the committed `testdata/ops_*.json` that CI
re-derives from AWS's models weekly) — for the fourteen services with an
action-dispatched wire. S3, Lambda and both API Gateways dispatch by method and
path with no action table, so their equivalent is the committed route table and
the rejection-parity suite.

Where the claim does not yet hold, the list says so rather than the prose
rounding up: fifty-nine operations across Kinesis, KMS, IAM and SSM reach no
handler and no named refusal today, each written into the owning service's
coverage test so the gap cannot grow unnoticed. The case count underneath is the mechanical long tail —
lengths, patterns, enums, required members — and it is a weaker number by
nature: nobody writes a 513-character description by accident, and AWS would
have caught it at deploy. It is worth having and it is not the score. A number
that goes up when you generate more padding should never be the headline.

If a gap above bites you, it is a bug worth reporting — the goal is an empty
right-hand column.

All 17 services talk to each other: EventBridge→SQS/SNS/Lambda/HTTP API destinations, S3
notifications→SQS/SNS/Lambda, SNS→SQS/Lambda/webhooks, SQS/DynamoDB
streams/Kinesis→Lambda, API Gateway→Lambda, Step Functions→Lambda/SQS/SNS
and back through task tokens, Lambda/Step Functions/API Gateway/EventBridge/SNS→CloudWatch Logs,
CloudWatch Logs subscription filters→Lambda/Kinesis, CloudWatch Logs metric filters→CloudWatch,
Lambda/API Gateway/Step Functions→CloudWatch metrics, and CloudWatch alarms→SNS/Lambda.

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

Per-service operation coverage lives in [docs/SUPPORT.md](docs/SUPPORT.md),
and what doze-aws **does not** build — with the argument for each, and the
separate list of what is merely deferred — is
**[docs/not-built.md](docs/not-built.md)**. It also states what the binary does
not do at all: no outbound connections of its own, no account, no telemetry,
nothing outside the data directory you name. Those are tests, not promises.

What a 1.x release will and will not change — the Go API, the CLI, the data
directory, the one log line worth parsing, and the `.doze` names — is
**[docs/COMPATIBILITY.md](docs/COMPATIBILITY.md)**.

## Design ground rules

- **Lightweight above all.** Five dependencies we chose: bbolt, a TOML parser,
  a YAML parser, a JSONata evaluator (Step Functions) and doze-names (the
  `.doze` zone). Six modules reach the binary — `golang.org/x/sys` arrives
  through bbolt — and a test fails if a seventh ever does. Data persists across
  restarts under one directory you can delete.
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

Or build from source: `go build ./cmd/doze-aws` (Go 1.27+).

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) covers the build loop (`go tool task check`,
~60s), the eight places a new service has to register itself, and what each
tier of test is for.

## License

Apache 2.0 — see [LICENSE](LICENSE).
