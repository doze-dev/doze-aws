# CLI reference

`doze-aws` is a single static binary. Run it with no arguments to serve every
implemented service; it runs in the foreground until you interrupt it (Ctrl-C).

```sh
cd ~/code/harbour && doze-aws
# msg=listening addr=127.0.0.17:4566 services=s3,dynamodb,sqs,… instance=harbour
# msg="reachable at" url=http://aws.harbour.doze as=name
```

The instance name comes from the directory. The first run on a machine offers
to make `.doze` resolve (one prompt, one sudo); `dns-setup` below is the same
thing run deliberately, for CI or a scripted install. See
[endpoints.md](endpoints.md) for the addressing model.

## Commands

| Command | What it does |
|---|---|
| `doze-aws` | Serve the enabled services (the default). If `./template.yaml` exists (or `--template` names a file), it is applied at boot. |
| `doze-aws dns-setup [--print]` | Set up `.doze` **deliberately**, rather than accepting the offer the first run makes. For CI, a scripted install, or a machine where you want it done before anything needs it. Idempotent. `--print` writes the script instead of running it, for anyone who will not hand a tool sudo. |
| `doze-aws dns-setup --check` | Report whether `.doze` resolves; exits non-zero if not. |
| `doze-aws doctor` | What this instance is, whether `.doze` resolves, and who holds which name. The first thing to run when a name stops working. |
| `doze-aws env` | Print the shell block that points an AWS SDK at this instance — endpoint, region, credentials, and the per-service `AWS_ENDPOINT_URL_*` hostnames. Use it as `eval "$(doze-aws env)"`. |
| `doze-aws apply [--var k=v ...] [file]` | Deploy a CloudFormation or SAM template (default `./template.yaml`): create what's missing, cheaply update what exists, never delete. `--var` supplies template parameters. Targets the running server if one is listening, the data dir otherwise. See [cloudformation.md](cloudformation.md). |
| `doze-aws export` | Write the running stack (queues, tables, buckets, functions, wiring, …) to stdout as a CloudFormation template. Secret values are left blank on purpose. |
| `doze-aws version` | Print the build version and the list of implemented services. |
| `doze-aws config print [flags]` | Resolve and print the effective configuration (defaults + config file + flags), then exit. Use it to see exactly what a given invocation would run. |

There is no daemon and nothing to install or clean up: state lives under the
data directory, and deleting it resets everything.

## Flags

Flags apply to serving and to `config print`.

| Flag | Default | Meaning |
|---|---|---|
| `--config <path>` | `./doze-aws.toml` if present | Path to a TOML config file. Relative paths **inside** it resolve against the file, not against your working directory. |
| `--name <name>` | the directory's name | This instance's name in `.doze`. It answers on `aws.<name>.doze`. |
| `--listen <host:port>` | (none — the name is the address) | Serve on an address **instead of** a `.doze` name; no name is claimed. For containers, CI, anywhere without DNS. See [endpoints.md](endpoints.md). |
| `--data-dir <dir>` | `./data`, or beside `doze-aws.toml` | Root directory: each region gets a subdirectory, with IAM and STS under `_global`. The **flag** resolves against your working directory; the config-file key resolves against the file. |
| `--region <region>` | `us-east-1` | Default region for unqualified requests. Others are created on first use. |
| `--account-id <12 digits>` | `000000000000` | The account every ARN carries. Set at creation and effectively frozen — the data records it and a mismatch is refused. |
| `--suffix <host>` | the instance's own name | What stands in for `amazonaws.com` in minted hostnames. Set this when a proxy owns the name and forwards to doze-aws on an address. |
| `--services <a,b,…>` | all implemented | Comma-separated subset of services to enable. Unknown names are an error. |
| `--template <path>` | `./template.yaml` if present | CloudFormation/SAM template to apply at boot. See [cloudformation.md](cloudformation.md). |
| `--iam-mode <mode>` | `soft` | IAM enforcement: `soft` (evaluate and record, never block), `off` (no evaluation at all), `enforce` (real denials). See [SUPPORT.md](SUPPORT.md#iam--api-support). |
| `--console` | on | Serve the web management console at `/_console`. |
| `--lambda-idle <duration>` | `10m` | How long a warm Lambda keeps its process before scaling to zero. |
| `--yes` | off | Answer yes to the confirmation described below. For scripts, and for anyone who does this daily. |

### When a flag overrules the config file

`--data-dir`, `--services`, `--name` and `--region` **ask before proceeding**
when `doze-aws.toml` set the same key, because each one produces a running
instance that looks like something went wrong:

```
These flags overrule doze-aws.toml:

  --data-dir   the resources you had are in the directory the file names, not here
  --name       URLs minted under the old name stop resolving — nothing claims it once you rename

Continue? [y/N]
```

Enter alone declines — the safe answer is the one you get by not deciding. Off
a terminal (CI, `doze-aws &`) it explains and proceeds rather than asking a
question nobody can answer; `--yes` skips the question and keeps the
explanation. A flag the file did **not** set overrules no decision and is never
questioned.

`--account-id` is not in that list: a mismatch is **refused** outright by the
data's own record of the account it was created under, because stored ARNs
embed it.

```sh
# Only S3 + SQS, with data under /tmp/aws
doze-aws --services s3,sqs --data-dir /tmp/aws

# Watch what IAM would deny, without denying it
doze-aws --iam-mode soft
```

Service names: `s3`, `dynamodb`, `sqs`, `sns`, `sts`, `kms`, `ssm`,
`secretsmanager`, `eventbridge`, `lambda`, `kinesis`, `iam`, `cloudformation`,
`apigateway`, `stepfunctions`.

## Config file

Instead of flags, put settings in `doze-aws.toml` (auto-loaded from the working
directory, or point `--config` at one elsewhere):

```toml
name     = "harbour"           # answers on aws.harbour.doze
region   = "ap-south-1"
data-dir = "data"              # relative to THIS FILE, not to your shell
services = ["s3", "dynamodb", "sqs"]
```

Every key is optional; omitted keys fall back to the defaults above.

**Paths in this file resolve against the file**, the way `Cargo.toml` and
`package.json` work — so `doze-aws --config /srv/harbour/doze-aws.toml` run from
anywhere puts the data in `/srv/harbour/data`. An absolute `data-dir` is used as
written, which is how you point at a mounted volume. The `--data-dir` *flag* is
the exception: typed in a shell, resolved from where you are standing.

Removed keys fail loudly rather than being ignored, and the error names the
replacement — `[s3] host` became `--suffix`, for instance.

**Precedence** (lowest to highest): built-in defaults → config file → flags. A
key set in the file survives unless the matching flag is explicitly passed, so
you can keep a committed `doze-aws.toml` and override one thing on the command
line. Run `doze-aws config print` to see the resolved result.

## Talking to it

Point any AWS SDK or the AWS CLI at the instance. Credentials are not verified,
so any non-empty values work. `doze-aws env` prints the right block for the
configuration in front of you, including the per-service hostnames:

```sh
eval "$(doze-aws env)"

# or by hand:
export AWS_ENDPOINT_URL=http://aws.harbour.doze
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

Data is written under `--data-dir` and survives restarts. The layout is one
directory per region, with the region-less services under `_global`:

```
data/
  instance.json        what this data was created with — account, region
  ap-south-1/          sqs/ s3/ dynamodb/ lambda/ …
  eu-west-1/           the same, created on first use
  _global/             iam/ sts/
```

To reset one service in one region, stop `doze-aws` and delete its directory;
to reset everything, delete the whole data directory. There is nothing else to
tear down.

`instance.json` is written by doze-aws, not by you. It records the account and
region the data was created under, so starting the same data with a different
`--account-id` is **refused** rather than silently orphaning every stored ARN —
they are embedded in other resources as plain strings, and would break at fire
time rather than at startup. A changed default region is reported, not refused:
regions are separate directories and the old one's resources are still there.

## See also

- [getting-started.md](getting-started.md) — a first run, end to end.
- [cloudformation.md](cloudformation.md) — deploying with the AWS CLI, SAM, CDK
  or Serverless.
- [embedding.md](embedding.md) — use doze-aws as a Go library instead of a CLI.
- [SUPPORT.md](SUPPORT.md) — per-service operation support (Functional /
  Cosmetic / honest Stub).
