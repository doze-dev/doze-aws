# Getting started

doze-aws is one small static binary that emulates the AWS services a
development stack leans on — built from the wire protocol up, verified against
both AWS SDK generations. No Docker, no JVM, no cloud.

## Run it

```sh
curl -fsSL https://raw.githubusercontent.com/doze-dev/doze-aws/main/install.sh | sh

cd ~/code/harbour && doze-aws
```

The installer verifies a SHA-256 against the release checksums and puts one
binary on your `PATH`. There is nothing else to install — no runtime, no
daemon, no container. (`--uninstall` takes it away again and leaves your data
alone.)

It prints where it is and what to do next:

```
  doze-aws is up.

    endpoint  http://aws.harbour.doze
    console   http://aws.harbour.doze/_console/   what your app is doing, live
    serving   17 services in us-east-1, account 000000000000

    eval "$(doze-aws env)"   point this shell at it
    doze-aws doctor          when something looks wrong
```

That block goes to stdout; the structured log lines go to stderr, so
`doze-aws 2>/dev/null` leaves just the summary and anything parsing the logs
is unaffected.

That is the whole setup. doze-aws addresses itself by name, so the first run
on a machine offers to make `.doze` resolve:

```
doze-aws addresses itself by name, and .doze does not resolve on this machine yet.
Setting it up needs sudo once — per machine, not per project.
Set it up now? [Y/n]
```

Say yes and it is done for good. With no terminal at all — CI, or `doze-aws &`
— it changes nothing and prints what to run, because a server that hangs
waiting for a password nobody can type is worse than one that stops.

Running as root it installs without asking, since there is nobody to ask.
**Inside a container that install usually fails**, and doze-aws stops rather
than starting half-configured: the setup applies a sysctl through
`sysctl --system`, which reapplies the host's whole sysctl configuration, and
most of `/proc/sys` is read-only in a container — so it fails on keys that have
nothing to do with doze. Verified on `alpine`, `debian:stable-slim`, and
Debian with `sudo` and `procps` installed. **In a container, use `--listen`**,
which skips the name path entirely and is what the compose setup below does.

The instance is named after the directory, so each project gets its own, and
its state lands in `./data` — which doze-aws marks ignored as it creates it,
so git never offers to commit two megabytes of local databases.

Point any AWS SDK at it, or the AWS CLI if you have one (doze-aws does not
install or need it — the examples below are just the shortest way to show the
thing working):

```sh
eval "$(doze-aws env)"

aws s3 mb s3://my-bucket
aws s3 cp ./file.txt s3://my-bucket/
aws dynamodb create-table --table-name t \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH --billing-mode PAY_PER_REQUEST
aws sqs create-queue --queue-name jobs
```

## Open the console

`http://aws.harbour.doze/_console/` — on by default, `--console=false` to turn
it off.

It opens on **the wire**: every call your app has made, newest first, with the
work each one caused nested under it. An S3 upload that fired a notification
that invoked a Lambda is drawn as one thing, not three unrelated rows. Each
row carries the status, the duration, the request id the SDK printed, and a
*copy as curl* button whose body is the redacted copy, so a repro you paste
into a ticket keeps secrets masked.

The calls the console makes on its own behalf never appear there — it talks to
the services directly rather than through the recorder — so the tail is your
app's traffic and nothing else.

From there, every service has pages that do real work: browse and upload S3
objects, actually receive (not peek at) SQS messages and redrive from a DLQ,
run PartiQL against DynamoDB, watch a Step Functions execution graph, invoke a
Lambda and tail its logs, simulate an IAM policy before you trust it.

Two things worth knowing about:

- **Connect** — the same endpoint written four ways: an AWS CLI profile, the
  environment block, a Terraform provider, and the deploy commands. Built from
  the address you reached the console on, with a button that runs a real
  `GetCallerIdentity` to prove it works.
- **The fidelity ledger**, on each service page — which operations are
  functional, which are cosmetic round-trips that accept and return your
  config without acting on it, and which are honest stubs. Real AWS never has
  to answer "is this call real here"; an emulator does, and this is where it
  does.

Queue URLs, invoke URLs and function URLs come back AWS-shaped under the
instance name — `http://sqs.us-east-1.aws.harbour.doze/000000000000/jobs` — so
what you copy out of a response is the shape you would get from AWS with the
suffix swapped.

No DNS available (CI, a container)? `doze-aws --listen 127.0.0.1:4566` serves an
address instead. See [endpoints.md](endpoints.md).

### In Docker, if that is where your team already is

doze-aws ships no image and no Dockerfile, deliberately — the whole argument
against a 1.88 GB emulator container is weakened by shipping one, and a static
binary with no runtime dependencies is a two-line `Dockerfile` anyone can write
for themselves. If your team's stack is already `docker compose up`, this is
enough:

```dockerfile
# Dockerfile
FROM alpine:3
COPY doze-aws /usr/local/bin/doze-aws
ENTRYPOINT ["doze-aws", "--listen", "0.0.0.0:4566", "--data-dir", "/data"]
```

```yaml
# compose.yaml
services:
  aws:
    build: .
    ports: ["4566:4566"]
    volumes: ["aws-data:/data"] # drop this line to start clean every run
volumes:
  aws-data:
```

Then point the SDKs at `http://aws:4566` from sibling services, or
`http://localhost:4566` from the host. Two things to know: `--listen` is
required, because the `.doze` name resolution the default depends on is not
there inside a container; and the data directory is worth a volume only if you
want state to survive `down` — a test run usually does not, and starting clean
is one line shorter.

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
