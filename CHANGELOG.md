# Changelog

Hand-written, because the generated alternative was 334 commit subjects in
order. A section per version, newest first; `release.yml` pulls the section
matching the tag and makes it the GitHub release notes, and fails the release
if the tag has no section.

Versions are [semantic](https://semver.org). From 1.0.0 on, the Go API is a
promise: what `testdata/api.txt` records cannot be removed or changed without a
2.0.0. `docs/COMPATIBILITY.md` says what else is covered.

## 1.0.0

**Seventeen services, and the first release that promises anything.**

0.x shipped ten services and no compatibility statement. This one covers
seventeen and freezes the Go API, the CLI, the data directory layout and the
`.doze` hostname contract.

### Breaking

- **The Go API is 380 exported symbols, down from 1,867.** If you imported
  doze-aws as a library at 0.x, the packages you called are still there and
  still work — `New(Options) → *Server`, `dozeaws.NewStack`, `peers`,
  `awsident` — but everything that was never part of that contract is gone.
  What left: the thirteen `Store` types and their ~260 methods, 94 domain-model
  types (`sqs.Message`, `lambda.Function`, `iam.Role`, `apigateway.RestAPI` and
  the rest) that no exported function took or returned, the `console` and
  `provision` packages and the CloudFormation transpiler, all now under
  `internal/`. `awsident.ARN` and `awsident.GlobalARN` are deleted — use an
  `Identity`. They were the package-level helpers that always used the default
  account, and under `--account-id` the console displayed that default
  everywhere: computed ARNs, queue URLs and the credential scopes used for
  routing.
- **`internal/trace` is now `trace`.** The one thing that became MORE public:
  five services expose `SetTraceSink(trace.Sink)` and no caller outside the
  module could name the argument, so the method was decoration.
- **No Windows binaries.** The matrix listed `windows` and the build did not
  compile — the `.doze` zone locks its registry with `syscall.Flock`. v0.3.0
  shipped Windows because that dependency did not exist yet. Supporting it
  means porting the resolver setup, which is a project, not a build flag.

### Added since 0.3.0

- **Seven services**: IAM (three enforcement modes, resource policies,
  least-privilege generation), CloudFormation and SAM (transpiled onto the
  existing convergent apply — `sam deploy` and `cdk deploy` work unmodified),
  API Gateway (REST v1 and HTTP v2, deploy and invoke), Step Functions
  (Standard and Express, JSONPath and JSONata, versions, aliases, activities,
  Distributed Map), CloudWatch Logs, CloudWatch Metrics and Alarms, Kinesis.
- **The `.doze` zone.** doze-aws answers on a name derived from the directory —
  `aws.harbour.doze` — set up once per machine with one sudo prompt. In a
  container or CI, `--listen` skips it.
- **The console opens on the wire**: every call your app made, newest first,
  with the work each one caused nested underneath, a request id you can quote,
  and copy-as-curl on each row. Plus per-service pages that read and write real
  resources, and a fidelity ledger saying which operations are real.
- **An honest support ledger.** `docs/SUPPORT.md` records all 385 operation
  rows at one of three tiers — functional, cosmetic round-trip, or a stub that
  refuses cleanly — and `docs/not-built.md` gives an argument for each of the
  81 stubs. Both are parsed at runtime by the console and checked by tests, so
  neither can drift from the code.
- **Model-derived rejection parity.** Every constraint in AWS's own service
  models is replayed as a mutation of a baseline the service is first proved to
  accept, because a request refused for the wrong reason looks exactly like a
  pass.
- **Measured budgets that fail the build**: the binary (22,622,368 bytes for
  linux/amd64), what it links (six modules), each embedded tree, boot time,
  idle footprint, and the exported API surface.

### Changed

- Cold start is ~20 ms and an untouched data directory is 158 bytes across
  three files. Databases are created on first use, so a stack nobody speaks to
  creates none. It was 793 ms and 2.1 MB across seventeen files.
- Data is laid out per region, with IAM and STS under `_global`.

## 0.3.0

The glance API the sibling `doze` dashboard reads, and queue URLs that respect
a path prefix. One commit after 0.2.0.

## 0.2.0

74 commits. The console arrived, topology-agnostic and with reachable function
endpoints; DynamoDB Streams with S3→Lambda proven end to end; the god-files
were split and the protocol codecs de-duplicated.

## 0.1.0

First release: ten services — S3, DynamoDB, SQS, SNS, STS, KMS, SSM Parameter
Store, Secrets Manager, EventBridge, Lambda — speaking the real wire protocols
to both AWS SDK generations, with Lambda running functions as local processes.
These three releases also shipped Windows binaries, which built at the time.
