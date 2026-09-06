# Lambda — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

Functions run as **real supervised local processes** speaking the AWS Lambda
Runtime API — no Docker, no image pulls. Each process is a child of doze-aws
with `AWS_LAMBDA_RUNTIME_API` pointing at a loopback server that hands it
invocations and takes back responses, which is exactly what the official
runtime interface clients and every `provided.*` bootstrap already speak. Up
to five processes per function serve concurrent invocations; an idle process
is reaped after `[lambda].idle-timeout` (10 minutes by default).

The user guide is [docs/lambda.md](../lambda.md). This page is the ledger.

## Runtimes

| Runtime | What runs | What the machine needs |
|---|---|---|
| `provided`, `provided.al2`, `provided.al2023`, `go1.x` | `./bootstrap` (or the handler's name) from the code directory, made executable if the zip dropped the bit | nothing |
| `python3.x` | `python3 <embedded client> <handler>` — a 200-line Runtime API client shipped inside doze-aws, materialised under `<data-dir>/shims`; `a/b/mod.fn` handlers, `LAMBDA_TASK_ROOT` on `sys.path`, the `logging` module wired the way `awslambdaric` wires it (request id on every record, JSON records under `AWS_LAMBDA_LOG_FORMAT=JSON`), a context object with every field AWS exposes | a `python3` on `PATH`, or `[lambda.runtimes] python = "/path"` |
| `nodejs*` | `node <embedded client> <handler>` — CommonJS and ESM (`.mjs`, `.cjs`, `package.json` `type`), nested handler paths, async and callback handlers, `uncaughtException` posted as an invocation error, console output prefixed the way Lambda prefixes it | a `node` on `PATH`, or `[lambda.runtimes] nodejs` |
| `ruby*` | `ruby <embedded client>` — `file.method` handlers taking `event:` and `context:` | a `ruby` on `PATH`, or `[lambda.runtimes] ruby` |
| `java*` | `java -cp <package jars> com.amazonaws.services.lambda.runtime.api.client.AWSLambda <handler>` — the AWS Java runtime interface client, which the package has to carry (`com.amazonaws:aws-lambda-java-runtime-interface-client`); CreateFunction refuses a package without it, naming the coordinate. The client's HTTP layer is a native library shipped for Linux, so on macOS the function needs a `Command` override | `java` |
| `dotnet*` | `dotnet <assembly>.dll` for a function published as a self-hosting executable (the project references `Amazon.Lambda.RuntimeSupport` and calls `LambdaBootstrap` from `Main`); a class-library handler is refused, naming the package and the change | `dotnet` |
| anything, any language | `Command: ["..."]` on CreateFunction or UpdateFunctionConfiguration (a doze extension) replaces the launch line entirely; the process still has to speak the Runtime API | whatever the command needs |

A missing interpreter is a warning at CreateFunction (AWS would accept the
function too) and a `Runtime.LaunchError` function error on the first invoke,
not a timeout. The interpreter's version is the host's: a `python3.12`
function runs on whatever `python3` is, which is the trade every host-process
emulator makes. Memory is not limited and `/tmp` is the host's.

The child's environment is Lambda's: `AWS_LAMBDA_RUNTIME_API`, `_HANDLER`,
`AWS_LAMBDA_FUNCTION_NAME`, `AWS_LAMBDA_FUNCTION_VERSION`,
`AWS_LAMBDA_FUNCTION_MEMORY_SIZE`, `AWS_LAMBDA_LOG_GROUP_NAME`,
`AWS_LAMBDA_LOG_STREAM_NAME`, `AWS_LAMBDA_INITIALIZATION_TYPE`,
`AWS_EXECUTION_ENV`, `LAMBDA_TASK_ROOT`, `LAMBDA_RUNTIME_DIR`, the region,
test credentials with a session token, `TZ=UTC`, the function's own
variables, and `AWS_ENDPOINT_URL*` pointing back at doze-aws so handlers reach
every sibling service unmodified. Each invocation carries
`Lambda-Runtime-Aws-Request-Id`, `-Deadline-Ms`, `-Invoked-Function-Arn`
(qualified when the invoke was), `-Trace-Id` and `-Client-Context`.

## Code

`Code.ZipFile` and `Code.S3Bucket/S3Key` (what `sam deploy` and `cdk deploy`
stage) are fetched and unpacked under the data dir, as on AWS. The doze
extension `Code.S3Bucket == "_local_"` with `S3Key` an absolute path to a
directory or binary runs the code **in place**: edit, invoke, no upload. A
warm process keeps the old code until its idle timeout or a
configuration update restarts it.

## Logs

Everything a function prints — stdout, stderr, init output, and Lambda's own
`START`/`END`/`REPORT` lines — is attributed to the invocation that printed
it and kept by the [CloudWatch Logs](logs.md) service under
`/aws/lambda/<name>`, in a stream per process named the way Lambda names
them. `aws logs tail /aws/lambda/<name> --follow`, `sam logs`, the SDKs'
FilterLogEvents and GetLogEvents, and the console's Logs tab all read it.
Every line is also echoed to doze-aws's own log as `lambda[<name>] <line>`
unless `[lambda].quiet = true` (`-lambda-quiet`). Invoke's `LogType: Tail`
still answers the last 4 KB in `X-Amz-Log-Result`, and every invocation
answers `X-Amzn-RequestId`.

Attribution follows Lambda's rule: output is credited to the current
invocation until the next one is polled, so a line printed after the
response belongs to the request that returned.

| Operation | Tier | Notes |
|---|---|---|
| CreateFunction / UpdateFunctionConfiguration / UpdateFunctionCode | F | zip, S3-staged and `_local_` packaging; env vars; DLQ/DestinationConfig; layers checked to exist; runtime checked for an interpreter (warns); `Command` extension |
| GetFunction / GetFunctionConfiguration / ListFunctions / DeleteFunction | F | a qualifier (`name:2`, `name:live`, `?Qualifier=`) answers the version or alias; `DeleteFunction?Qualifier=N` removes one version, deleting the function removes them all |
| Invoke (RequestResponse) | F | real process, `X-Amz-Function-Error` on a handler error, `X-Amz-Log-Result` tail, `X-Amzn-RequestId`, `X-Amz-Executed-Version`; `Qualifier` runs the version an alias or number names; `ClientContext` reaches the function |
| Invoke (Event) | F | async with configurable retries → DLQ / OnFailure destination |
| Invoke (DryRun) | F | 204 |
| Put/Get/Update/List/DeleteFunctionEventInvokeConfig | F | async destinations (OnSuccess/OnFailure → SQS/SNS/Lambda) + MaximumRetryAttempts (honored) / MaximumEventAgeInSeconds (stored) |
| PublishVersion / ListVersionsByFunction | F | a version freezes the code (copied under the data dir — a `_local_` directory too, so an in-place edit reaches `$LATEST` and not the version) and the configuration AWS snapshots; publishing an unchanged function answers the version it already has; `RevisionId` mismatch is a 412 |
| CreateAlias / GetAlias / ListAliases / UpdateAlias / DeleteAlias | F | name, version, description; a second create is a `ResourceConflictException`; an alias at a version that does not exist is refused. `RoutingConfig` weights are accepted and not stored: an alias runs one version |
| CreateFunctionUrlConfig / GetFunctionUrlConfig / UpdateFunctionUrlConfig / DeleteFunctionUrlConfig | F | **served**: a plain HTTP request to the URL becomes the payload-format-2.0 event and the answer is decoded by AWS's rule (`statusCode` object as a response, anything else as a 200 JSON body). Two addresses route: `<endpoint>/_aws/lambda-url/<id>/…` and `https://<id>.lambda-url.us-east-1.on.aws/` by Host. `AuthType` `NONE` and `AWS_IAM` are both served without a signature check; `InvokeMode` is `BUFFERED`; a config on a qualified ARN addresses the function |
| PutFunctionConcurrency / GetFunctionConcurrency / Delete | C | stored; no throttling locally |
| TagResource / UntagResource / ListTags | F | |
| CreateEventSourceMapping (SQS) | F | polls the queue, delivers batches (batch-size honored), delete-on-success, visibility-timeout retry on failure |
| Get/List/Update/DeleteEventSourceMapping | F | |
| DynamoDB/Kinesis event source mappings | F | both are polled for real — one iterator per shard for Kinesis, refreshed on reshard |
| AddPermission / RemovePermission / GetPolicy | F | the API behind `AWS::Lambda::Permission`; service and account principals, SourceArn/SourceAccount synthesized into ArnLike/StringEquals conditions. Nothing locally gates invocation on the policy — it round-trips for templates |
| PublishLayerVersion / GetLayerVersion / GetLayerVersionByArn | F | inline `ZipFile`, S3-staged content, or `_local_` naming a zip or a directory laid out like an unpacked layer; **unpacked and put on the function's search paths** (below); `CodeSha256` is the zip's hash, or a content fingerprint for a `_local_` directory |
| ListLayers / ListLayerVersions / DeleteLayerVersion | F | newest-first ordering; ListLayers reports each layer's latest version |
| AddLayerVersionPermission / GetLayerVersionPolicy / RemoveLayerVersionPermission | F | |
| GetAccountSettings | F | live function count and code size against nominal limits |
| Container images, SnapStart, provisioned concurrency semantics, code signing | S | config accepted where trivial; execution semantics are cloud-only |

## Layers, without /opt

On AWS a layer is unpacked into `/opt` and the runtimes find it there. A
local process cannot be given a `/opt` without root, so each layer's
directories go onto the search paths the runtimes read — `python/` and
`python/lib/python3.x/site-packages` on `PYTHONPATH`, `nodejs/node_modules`
on `NODE_PATH`, `ruby/lib` on `RUBYLIB` and `ruby/gems/*` on `GEM_PATH`,
`bin/` on `PATH`, `lib/` on `LD_LIBRARY_PATH` and `DYLD_LIBRARY_PATH` — in
layer order, later layers first, which is the precedence AWS gives them.
`LAMBDA_LAYERS_DIRS` names the extracted directories for code that wants to
look. Code that opens `/opt/...` by literal path does not find it; that is
the one thing this cannot fake. A function whose `Layers` names a version
that does not exist is refused at create and update, as on AWS.

## Differences from AWS, in one place

- The interpreter is the host's; `python3.12` means the host's `python3`.
- No memory limit, no ephemeral-storage limit, no execution-role
  enforcement; `AWS_IAM` function URLs are served unsigned.
- Layers are on the search paths, not at `/opt`.
- Java needs the runtime interface client in the package and a Linux host
  (or a `Command`); .NET needs a self-hosting executable.
- Alias routing weights are accepted and not applied.
- Function URLs answer at the endpoint's `/_aws/lambda-url/<id>/` path as
  well as the on.aws host, because a local client cannot always set `Host`.

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what Lambda refuses**.

**612/621 model-derived constraints enforced across all 47 routed operations
with constrained input, with `knownGaps` empty.** Removing the constraint table
makes 452 of them slip through — the largest share of any service here, because
Lambda's inputs are the widest: `CreateFunction` alone carries 76 constraints.
The remaining nine cannot be put on this wire; see below.

### A member the emulator ignores still has to be refused

`MemorySize` is the instructive one, and it was the first gap this service's
audit found. doze-aws does not allocate memory per function, so the value had no
local effect and nothing had ever looked at it — which is precisely why any
number was accepted. A function CloudFormation would reject deployed clean here
and failed in the account.

Generated with `dzaudit cases lambda`, committed to `testdata/cases_lambda.json`,
and replayed case by case in `rejection_parity_test.go` from a baseline the test
first proves the service accepts. Lambda speaks restJson1, so `validate.go`
carries a route table beside the constraint tables — the operation is the method
and the path, and has to be resolved before anything can be looked up.

### The fixture is expensive, and each case gets its own

A function needs real deployable code, and a version, alias, layer, permission
or event source mapping needs a function first. The bootstrap is compiled once
and every throwaway function points at the same directory — the audit is about
what the service refuses, not what the handler prints. `Invoke`'s baseline is
the exception that needs it to genuinely run.

### Nine cases cannot be expressed

Seven omit the **last** label of a URI, which does not produce an invalid
request — it produces a shorter path, which is a different valid operation. `GET
/2015-03-31/functions` is `ListFunctions`, not a `GetFunction` missing its name.
The same is true for `GetAlias`, `GetEventSourceMapping` and `GetLayerVersion`.

Two are `@httpHeader` members — `Invoke`'s `TenantId` and
`DurableExecutionName` — whose pattern violation is a control character. HTTP
forbids that in a header value, and Go's transport refuses to send the request
at all, so the service never sees it. AWS's own SDK is bound by the same rule.

Both are derived from the bindings rather than listed by hand, so an operation
added later cannot quietly acquire a case that tests nothing.

### Not audited

Everything absent from the audit is absent from doze-aws: capacity providers,
durable executions, code signing configs as first-class resources, and the rest
of the cloud-only surface.

`UpdateAlias` used to be on this list — `/aliases/{Name}` answered GET and
DELETE, and the PUT the operation uses fell through to a 405. Repointing an
alias at a new version is the ordinary way a Lambda deploy goes live, so the gap
was on the main path. It is implemented now.
