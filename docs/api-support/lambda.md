# Lambda — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

Functions run as **real supervised local processes** speaking the AWS Lambda
Runtime API (no Docker). The official runtime interface clients speak the
protocol unmodified: `provided.*`/Go run a `bootstrap`/binary directly,
`python3.x` runs `python -m awslambdaric`, `nodejs*` runs
`npx aws-lambda-ric`. One process per function, serial invocations in this
phase (scale-out pool in Phase 8).

Code: `Code.ZipFile` (unpacked under the data dir) **and** a doze extension —
`Code.S3Bucket == "_local_"` with `Code.S3Key` an absolute path to a directory
or binary, used in place for edit-and-reinvoke with no copy.

| Operation | Tier | Notes |
|---|---|---|
| CreateFunction / UpdateFunctionConfiguration / UpdateFunctionCode | F | zip + _local_ packaging; env vars; DLQ/DestinationConfig; layers stored |
| GetFunction / ListFunctions / DeleteFunction | F | |
| Invoke (RequestResponse) | F | real process, X-Amz-Function-Error on handler error, Tail log result |
| Invoke (Event) | F | async with configurable retries → DLQ / OnFailure destination |
| Put/Get/Update/List/DeleteFunctionEventInvokeConfig | F | async destinations (OnSuccess/OnFailure → SQS/SNS/Lambda) + MaximumRetryAttempts (honored) / MaximumEventAgeInSeconds (stored) |
| Invoke (DryRun) | F | 204 |
| PublishVersion / aliases (Create/Get/List/Delete) | F | local version numbering |
| Function URL config (Create/Get/Delete) | F | URL served on the gateway |
| PutFunctionConcurrency / GetFunctionConcurrency / Delete | C | stored; no throttling locally |
| TagResource / UntagResource / ListTags | F | |
| CreateEventSourceMapping (SQS) | F | polls the queue, delivers batches (batch-size honored), delete-on-success, visibility-timeout retry on failure |
| Get/List/Update/DeleteEventSourceMapping | F | |
| DynamoDB/Kinesis event source mappings | F | both are polled for real — one iterator per shard for Kinesis, refreshed on reshard |
| Container images, SnapStart, provisioned concurrency semantics | S | config accepted where trivial; execution semantics are cloud-only |
| AddPermission / RemovePermission / GetPolicy | F | the API behind `AWS::Lambda::Permission`; service and account principals, SourceArn/SourceAccount synthesized into ArnLike/StringEquals conditions. Nothing locally gates invocation on the policy — it round-trips for templates |
| PublishLayerVersion / GetLayerVersion / GetLayerVersionByArn | F | inline ZipFile written under the data dir, or the `_local_` in-place path convention |
| ListLayers / ListLayerVersions / DeleteLayerVersion | F | newest-first ordering; ListLayers reports each layer's latest version |
| AddLayerVersionPermission / GetLayerVersionPolicy / RemoveLayerVersionPermission | F | |
| GetAccountSettings | F | live function count and code size against nominal limits |

Child processes get `AWS_LAMBDA_RUNTIME_API`, `_HANDLER`, the function's env,
test credentials, and `AWS_ENDPOINT_URL*` pointing back at the doze-aws
endpoint — so handlers reach every sibling service unmodified.

## Layers, honestly

Layer versions are stored, versioned and served for real, and a function's
`Layers` list round-trips. What doze-aws does **not** do is overlay a layer into
`/opt` at invoke time: local functions run as ordinary processes against real
files on disk, so there is no container filesystem to mount into. Code that
reads a layer path at runtime needs those files present locally.

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what Lambda refuses**.

| Input | Status |
|---|---|
| `MemorySize` — 128..32768 | ✅ both bounds, on create and update |
| `Timeout` — 1..5400 | ✅ both bounds, on create and update |
| Everything else | not yet audited |

`MemorySize` is the instructive one. doze-aws does not allocate memory per
function, so the value has no local effect and nothing had ever looked at it —
which is precisely why any number was accepted. A member the emulator ignores
still has to be refused when it is invalid, or a function CloudFormation would
reject deploys clean here and fails in the account.

Bounds come from Lambda's own service model (`dzaudit list --op CreateFunction
lambda`), not from the documentation prose. Covered by
`lambda/rejection_parity_test.go`.

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what Lambda refuses**.

**612/621 model-derived constraints enforced across all 47 routed operations
with constrained input, with `knownGaps` empty.** Removing the constraint table
makes 452 of them slip through — the largest share of any service here, because
Lambda's inputs are the widest: `CreateFunction` alone carries 76 constraints.
The remaining nine cannot be put on this wire; see below.

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
