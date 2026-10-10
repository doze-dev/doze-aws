# Lambda

Stage 10. Size L. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds Lambda's own.

## 1. Summary

Functions run as supervised local processes speaking the Lambda Runtime API
(`internal/lambdaruntime`), with the control plane in `lambda/` over one bbolt
store, on restJson1. Peers: SQS, Kinesis and DynamoDB streams feed it through
event source mappings that Lambda itself polls; S3, SNS, EventBridge, Logs,
API Gateway, Step Functions, CloudWatch alarms and Secrets Manager rotation
invoke it; it writes to CloudWatch Logs and CloudWatch, and delivers async
results to SQS, SNS and other functions. CloudFormation and `provision` create
it.

The function runtime is in good shape. The event source mapping code is not: it
reads four of the request's members and drops the rest, and that is where most
of the P0s are.

Line numbers are as found on 2026-10-10 at `8cd261f` and will drift; the symbol
beside each one will not. Every `run` below was reproduced against a binary
built from that commit, driven over HTTP.

**Estimate:** L, five or more focused days. Section 11 argues it is nearer
eight, and that one fix belongs in stage 1. **Actual:** _fill in when done._

## 2. Contract

The model (`lambda/testdata/ops_lambda.json`) lists **88** operations. 52 are
routed, plus one extension. 36 are not routed at all and answer
`404 ResourceNotFoundException: unknown path "…"` (run), which is neither a
tier nor a row in `not-built.md`. See LAM-12.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateFunction, UpdateFunctionConfiguration, UpdateFunctionCode | F | F | Per-setting tiers below. `Publish` is ignored (LAM-7). Update drops most members (LAM-2). |
| GetFunction, GetFunctionConfiguration, DeleteFunction | F | F | `CodeSize` missing, `LastModified` wrong (LAM-21, LAM-22). Delete leaves mappings (LAM-1). |
| ListFunctions | F | F | Ignores `MaxItems` and `Marker` (LAM-13). |
| Invoke | F | F | RequestResponse, Event, DryRun. Tail and size limit: LAM-11, LAM-18. |
| PublishVersion, ListVersionsByFunction | F | F | List does not page. |
| CreateAlias, GetAlias, ListAliases, UpdateAlias, DeleteAlias | F | F | `RoutingConfig` is C: accepted, not stored. |
| PutFunctionEventInvokeConfig, UpdateFunctionEventInvokeConfig, GetFunctionEventInvokeConfig, DeleteFunctionEventInvokeConfig, ListFunctionEventInvokeConfigs | F | F | `MaximumEventAgeInSeconds` is C. Retries have no delay (LAM-27). |
| CreateFunctionUrlConfig, GetFunctionUrlConfig, UpdateFunctionUrlConfig, DeleteFunctionUrlConfig, ListFunctionUrlConfigs | F | F | `AuthType AWS_IAM` unsigned, `InvokeMode` always `BUFFERED`: both C. A second create is not refused (LAM-20). |
| PutFunctionConcurrency, GetFunctionConcurrency, DeleteFunctionConcurrency | C | F, with a written difference | The ledger says "no throttling locally". It throttles at 0 and sets the process ceiling at N (LAM-25). |
| TagResource, UntagResource, ListTags | F | F | Functions only. No limit (LAM-19). Tags on a mapping or a layer: not established. |
| CreateEventSourceMapping, GetEventSourceMapping, ListEventSourceMappings, UpdateEventSourceMapping, DeleteEventSourceMapping | F | F | Per-setting tiers below. Most findings are here. |
| AddPermission, RemovePermission, GetPolicy | F | F | Allow statements only, as on AWS. |
| PublishLayerVersion, GetLayerVersion, GetLayerVersionByArn, ListLayers, ListLayerVersions, DeleteLayerVersion | F | F | LAM-23, LAM-24, LAM-37. |
| AddLayerVersionPermission, GetLayerVersionPolicy, RemoveLayerVersionPermission | F | C | Stored and returned, never evaluated (`SUPPORT.md`, "Not covered"). The row says F (LAM-26). |
| GetAccountSettings | F | F | `TotalCodeSize` is wrong (LAM-23). Limits are nominal. |
| GetFunctionCodeSigningConfig, PutFunctionCodeSigningConfig, DeleteFunctionCodeSigningConfig | C | C | Round trip only, so Terraform's refresh passes. Not in the ledger table today. |
| InvokeAsync, InvokeWithResponseStream | absent | S, or F for InvokeAsync | InvokeAsync is deprecated on AWS and is one line over the Event path. Decide. Streaming is declined. |
| PutProvisionedConcurrencyConfig, GetProvisionedConcurrencyConfig, ListProvisionedConcurrencyConfigs, DeleteProvisionedConcurrencyConfig | absent | S or C | `not-built.md` says "provisioned concurrency semantics" are cloud-only. Whether a deploy tool reads these on refresh is not established. |
| CreateCodeSigningConfig, GetCodeSigningConfig, UpdateCodeSigningConfig, DeleteCodeSigningConfig, ListCodeSigningConfigs, ListFunctionsByCodeSigningConfig | absent | S | Declined. |
| GetRuntimeManagementConfig, PutRuntimeManagementConfig | absent | S or C | Not established whether Terraform or CDK call them. |
| GetFunctionRecursionConfig, PutFunctionRecursionConfig | absent | S or C | Same question. |
| GetFunctionScalingConfig, PutFunctionScalingConfig | absent | S | |
| GetResourcePolicy, PutResourcePolicy, DeleteResourcePolicy | absent | S | Which resources these address in the current model is not established. |
| CreateCapacityProvider, GetCapacityProvider, UpdateCapacityProvider, DeleteCapacityProvider, ListCapacityProviders, ListFunctionVersionsByCapacityProvider | absent | S | Managed instances. Declined. |
| CheckpointDurableExecution, GetDurableExecution, GetDurableExecutionHistory, GetDurableExecutionState, ListDurableExecutionsByFunction, SendDurableExecutionCallbackFailure, SendDurableExecutionCallbackHeartbeat, SendDurableExecutionCallbackSuccess, StopDurableExecution | absent | S | Durable executions. Declined. |
| DozeRuntime (`GET /functions/{name}/doze-runtime`) | extension | extension | Process state for the console badge. |

Function settings, because "CreateFunction is F" hides which do anything.
"Update" is whether `UpdateFunctionConfiguration` applies the member (run).

| Setting | Create | Update | Target | Note |
|---|---|---|---|---|
| Runtime, Handler | F | F | F | A missing interpreter is a log line at create and `Runtime.LaunchError` at invoke. |
| Code (`ZipFile`, `S3Bucket`/`S3Key`, `_local_`) | F | F (UpdateFunctionCode) | F | `ImageUri` refused by name. |
| Timeout | F | F | F | Enforced on the caller's side only (LAM-10). Default 3. |
| MemorySize | C | C | C | Reaches the process as an environment variable and the REPORT line. Default 512, AWS 128 (LAM-9). |
| Environment.Variables | F | F | F | No reserved-key or size check (LAM-14). |
| Layers | F | F | F | Checked to exist; on search paths, not `/opt`. |
| DeadLetterConfig | F | set only | F | Cannot be cleared (LAM-2). |
| LoggingConfig (`LogFormat`, `ApplicationLogLevel`) | F | dropped | F | `LogGroup` and `SystemLogLevel`: not established. |
| Description | stored | dropped | F | LAM-2. |
| Role | C | dropped | C | No execution-role enforcement. |
| TracingConfig, EphemeralStorage, KMSKeyArn, VpcConfig, FileSystemConfigs, SnapStart, Architectures | C | dropped | C | LAM-2. The console must say cosmetic wherever it shows one. |
| Publish | ignored | ignored | F | LAM-7. |
| Tags (on create) | F | n/a | F | |
| `Command` | extension | extension | extension | Stored, never read back. |
| PackageType `Image`, ImageConfig, CodeSigningConfigArn, DurableConfig, CapacityProviderConfig, TenancyConfig | validated by the model table | | S or C | What each answers today is not established beyond `ImageUri`. |

Event source mapping settings. `createMapping` decodes `FunctionName`,
`EventSourceArn`, `BatchSize` and `Enabled`; everything else passes model
validation and is then discarded (run).

| Setting | Now | Target | Note |
|---|---|---|---|
| EventSourceArn: SQS, DynamoDB stream, Kinesis | F | F | Anything else refused by name. |
| FunctionName | F for a bare name | F | A qualified name or alias ARN resolves to the wrong function name (LAM-8). |
| BatchSize | F up to 10 on SQS | F | Above 10 delivers 1 (LAM-6). |
| Enabled | F | F | |
| FunctionResponseTypes (`ReportBatchItemFailures`) | dropped | F | LAM-3. |
| FilterCriteria | dropped | F | LAM-3. |
| MaximumBatchingWindowInSeconds | dropped | F or C | LAM-3. |
| ScalingConfig.MaximumConcurrency | dropped | C | |
| StartingPosition, StartingPositionTimestamp | dropped; always `TRIM_HORIZON` | F | LAM-4. |
| BisectBatchOnFunctionError, MaximumRetryAttempts, MaximumRecordAgeInSeconds, ParallelizationFactor, TumblingWindowInSeconds, DestinationConfig.OnFailure | dropped | F or C, each decided | LAM-4. |
| Kafka, MQ, DocumentDB members | validated, dropped | S | Sources not served. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `lambda/`: 52 tests in 17 files (SDK v2 and v1, real interpreters, logs end to end, versions, URLs and URL IAM, policy and layers, router, tree hash, endpoint injection, declared config). `internal/lambdaruntime/`: 22 tests in 7 files (pool, runtime, shims, output attribution, logs). |
| Parity | `rejection_parity_test.go` (2 tests over `testdata/cases_lambda.json`); the ledger says 612 of 621 model constraints enforced on 47 routed operations, `knownGaps` empty, nine not expressible on the wire. |
| Conformance | `conformance/tests/test_lambda.py`: 5 scenarios (lifecycle and invoke, versions and aliases, permissions, a queue drives the function, refusals). All **unverified**: `conformance/snapshots/` is empty. |
| Console (Go) | `internal/console/console_test.go`: `TestConsoleLambdaPage`, `TestLambdaLifecycle`. |
| Browser | `e2e/tests/lambda.spec.ts`: 13 tests. |
| Mutation | No per-package baseline yet (foundation B3). |

None of the 52 package tests covers an event source mapping setting other than
batch size, a stream mapping across a restart, a deleted function's mapping, or
`UpdateFunctionConfiguration` reading back what it was sent. That is why the
P0s below were all still there.

## 4. Findings

The AWS source column is honest about what was not checked: `memory` rows are
on the recording queue and are not acted on until they are `doc` or `recorded`.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| LAM-1 | P0 | Deleting a function leaves its event source mappings stored, `Enabled`, and polling. The poller receives each batch, finds no function, and drops it undeleted; the message's receive count climbs and it lands in the dead-letter queue. Reproduced: one message, `maxReceiveCount` 3, in the DLQ nine seconds after the delete. | `lambda/functions.go` `deleteFunction`; `lambda/store.go` `DeleteFunction`; `lambda/extras.go` `pollSQS` (the `continue` after `GetFunction`, about 496), same in `pollDDBStream` and `pollKinesis` | run | memory: what AWS does with a deleted function's mappings | SDK test: mapping, delete the function, send, assert the message is still visible with receive count 0 and the poller is gone |
| LAM-2 | P0 | `UpdateFunctionConfiguration` answers 200 and silently drops `Description`, `Role`, `TracingConfig`, `EphemeralStorage`, `KMSKeyArn`, `LoggingConfig`, `VpcConfig`, `FileSystemConfigs`, `SnapStart` and `Architectures`. An empty `DeadLetterConfig.TargetArn` does not clear the target. A deploy tool reads back the old value and plans the same change forever. | `lambda/functions.go` `updateConfiguration` | run | model (the members exist on the input shape) | SDK test: update each member, read it back; clear the DLQ |
| LAM-3 | P0 | `CreateEventSourceMapping` and `UpdateEventSourceMapping` drop `FunctionResponseTypes`, `FilterCriteria`, `MaximumBatchingWindowInSeconds` and `ScalingConfig`. Two change what an app sees: a filtered-out message is delivered anyway (run: pattern `{"body":["keep"]}`, body `drop`, invoked and deleted), and a function that returns `batchItemFailures` has its whole batch deleted, so the failed items are lost. | `lambda/extras.go` `createMapping`, `updateMapping`, `mappingView`, `pollSQS`; `lambda/store.go` `eventSourceMapping` | run (settings dropped, filter ignored); read (partial batch) | doc: Lambda developer guide, event filtering and partial batch responses | SDK test per setting round trip; a filter test; a handler returning one `itemIdentifier`, assert that message alone returns |
| LAM-4 | P0 | Stream mappings always open at `TRIM_HORIZON` and keep the iterator in memory. `StartingPosition` is ignored, and every restart, `UpdateEventSourceMapping` or re-enable replays the stream from the start. A failed batch is not retried: the iterator has already advanced. `BisectBatchOnFunctionError`, `MaximumRetryAttempts`, `MaximumRecordAgeInSeconds`, `ParallelizationFactor` and `DestinationConfig` are dropped. | `lambda/extras.go` `pollDDBStream`, `pollKinesis` | read | doc: developer guide, Kinesis and DynamoDB event source mappings | Restart test: write three records, consume, restart, assert no redelivery; `LATEST` test; a throwing handler sees the same batch again |
| LAM-5 | P0 | An SQS trigger is created when the queue's visibility timeout (2 s) is shorter than the function's timeout (30 s). | `lambda/extras.go` `createMapping`, about 246 to 293; `grep -i visibility lambda/` finds one comment | run | doc: developer guide, "Using Lambda with Amazon SQS" (the refusal and its wording are `memory`) | Rejection test: timeout 30, visibility 2, expect a refusal. Skipped pending the recording of the code and message |
| LAM-6 | P0 | A trigger with `BatchSize` above 10 delivers one message per invocation. Reproduced: 15 messages, batch 20, fifteen invocations of 1; batch 10 gave 10 and 5. The poller asks SQS for `BatchSize` messages and SQS turns any request above 10 into 1. **When SQS-5 is fixed in stage 1, this poller starts failing on every receive instead**, so the cap belongs in stage 1. | `lambda/extras.go` `pollSQS`, about 468; `sqs/receive.go`, about 17 | run | doc: `BatchSize` up to 10 000 for standard queues with a batching window | SDK test: batch 20, 15 waiting messages, assert one invocation of 15 (or of 10 and 5 if the window is declared cosmetic) |
| LAM-7 | P0 | `Publish: true` on `CreateFunction` and `UpdateFunctionCode` is ignored: the answer is `Version: "$LATEST"` and no version exists. An alias or a mapping then pointed at version 1 fails. | `lambda/functions.go` `createFunctionReq` (no `Publish` member), `updateCode` | run | model | SDK test: create with `Publish`, expect `Version: "1"` and two entries from `ListVersionsByFunction` |
| LAM-8 | P0 | A mapping whose `FunctionName` is qualified resolves the wrong name. `createMapping` takes the text after the last colon, so `fn:live` and `arn:…:function:fn:live` both look up a function called `live`. SAM's `AutoPublishAlias` with an SQS event produces exactly that. What the CloudFormation path sends is not established. | `lambda/extras.go` `createMapping`, about 260; compare `functionNameFromARN` in the same file, which is right | read | model (the `FunctionName` pattern allows a qualifier) | SDK test: mapping on `fn:live`, assert it is created and the alias's version runs |
| LAM-9 | P0 | The SQS event a function receives has six keys per record. `attributes` (`ApproximateReceiveCount`, `SentTimestamp`, `MessageGroupId`…), `messageAttributes` and `md5OfBody` are missing, so a handler that reads them throws here and not on AWS. Reproduced with a message attribute, which did not arrive. `awsRegion` is the literal `us-east-1` (foundation A8). | `lambda/extras.go` `pollSQS`, about 485 to 493; `internal/peercall` `SQSReceive` (asks only for `AWSTraceHeader`) | run | doc: developer guide, the example SQS event | Runtime test asserting the record's key set and one value of each |
| LAM-10 | P0 | A timed-out invocation is answered, and nothing stops the handler. Reproduced: timeout 1 s, handler 4 s; the call returned at 2.0 s and the handler logged its last line two seconds later. Writes made after the timeout happen here and not on AWS, and a queue trigger then redelivers the batch on top of them. Also: the payload is `{"errorMessage":"Task timed out"}` with no request id or duration, and REPORT `Duration` is timeout plus the one-second grace (2001 ms). | `internal/lambdaruntime/runtime.go` `InvokeInput`, about 289 to 296, `withTiming` | run | doc: "Lambda terminates the invocation"; the payload wording is `memory` | Runtime test: a handler that writes a file after its timeout, assert the file is absent |
| LAM-11 | P0 | Deleting a layer version removes it from functions that use it. `layerDirs` resolves each layer through the store on every cold start and skips one that is gone, so the function's next process starts without the layer. The console's confirm says "Functions already configured with it keep running". | `lambda/layers.go` `layerDirs`, `deleteLayerVersion` | read | doc: `DeleteLayerVersion`, "a copy of the version remains in Lambda until no functions refer to it" | SDK test: function with a layer, delete the version, force a cold start, invoke and import from the layer |
| LAM-12 | P1 | 36 operations in the model have no route. Each answers `404 ResourceNotFoundException` with `unknown path "/…"`, which an SDK reports as "the function was not found". Checked on seven of them. | `lambda/router.go` `notFound`, `routeSpecs`; `docs/not-built.md` (one line for four families) | run | model | Router test: every operation in `ops_lambda.json` is routed or answers the agreed stub code; `not-built.md` reconciles |
| LAM-13 | P1 | No list operation pages. `ListFunctions`, `ListLayerVersions` and `ListEventSourceMappings` with `MaxItems=1` returned everything and no `NextMarker`. `ListVersionsByFunction`, `ListAliases`, `ListLayers`, `ListFunctionEventInvokeConfigs` and `ListFunctionUrlConfigs` read no paging members either. | `lambda/functions.go` `listFunctions`; `lambda/layers.go`; `lambda/extras.go` `listMappings`; `lambda/versions.go`; `lambda/aliases.go` | run (three), read (five) | model | SDK paginator test per list with `MaxItems: 1` |
| LAM-14 | P1 | Environment variables are not checked. `AWS_REGION` and `_HANDLER` were accepted, and because the function's own variables are applied last they override the runtime's. There is no 4 KB total check. | `lambda/functions.go` `createFunction`, `updateConfiguration`; `internal/lambdaruntime/env.go` | run (keys), read (size) | doc: developer guide, "Reserved environment variables" and the 4 KB quota | Rejection test for a reserved key and for 4097 bytes |
| LAM-15 | P1 | A second mapping for the same queue and function is created, and both poll. | `lambda/extras.go` `createMapping` | run | memory: `ResourceConflictException` | Skipped SDK test, pending the recording |
| LAM-16 | P1 | `DeleteEventSourceMapping` for a UUID that does not exist answers 202 with an empty mapping in state `Deleting`. | `lambda/extras.go` `deleteMapping` | run | doc: the API reference lists `ResourceNotFoundException` | SDK test expecting the error |
| LAM-17 | P1 | `RevisionId` is honoured by `PublishVersion` only. `UpdateFunctionConfiguration` with a stale one answered 200. `UpdateFunctionCode`, `UpdateAlias`, `AddPermission` and `RemovePermission` are not established. | `lambda/functions.go` `updateConfiguration`, `updateCode`; `lambda/aliases.go`; `lambda/policy.go` | run (one), read | doc: `PreconditionFailedException` on each | SDK test per operation with a stale id, expect 412 |
| LAM-18 | P1 | An invoke payload above 6 MB is cut at 6 MB and then refused as `InvalidRequestContentException: … not valid JSON`. | `lambda/invoke.go` `invoke` (the `LimitReader`) | run | doc: `RequestTooLargeException`, 413 | Rejection test at 6 MB plus one byte, for sync; the async limit is a separate row |
| LAM-19 | P1 | Tags have no limit and no existence check: 51 tags were accepted, and `UntagResource` on a function that does not exist answered 204. | `lambda/extras.go` `tagResource`, `untagResource` | run | doc: 50 tags per resource; the 404 is `memory` | SDK tests for both |
| LAM-20 | P1 | `CreateFunctionUrlConfig` twice answers 201 twice. | `lambda/urls.go` `putFunctionURL` | run | doc: `ResourceConflictException` | SDK test |
| LAM-21 | P1 | `LastModified` is the time of the read, not of the last change: two reads two seconds apart differed. `function.LastMod` is written in three places and read nowhere. The format is `2026-10-10T11:24:49Z`. | `lambda/functions.go` `configView`, about 469 | run | memory: AWS's format is `…T11:24:49.000+0000` | Handler test: two reads agree; the format test waits for the recording |
| LAM-22 | P1 | `CodeSize` is absent from every function configuration. | `lambda/functions.go` `configView` | run | model (`CodeSize` is a member of `FunctionConfiguration`) | Handler test: a zip of known size |
| LAM-23 | P1 | Two sizes are a directory entry's, not the content's. `GetAccountSettings` reported `TotalCodeSize: 288` for three functions (96 bytes each on this file system), and a layer published from a `_local_` directory reported `CodeSize: 96`. | `lambda/layers.go` `accountSettings` (about 526), `publishLayerVersion` (`info.Size()`, about 313) | run | n/a | Handler tests with a directory of known content |
| LAM-24 | P1 | A layer's version number is reused: publish 1, 2, 3, delete 3, publish again, get 3. | `lambda/layers.go` `nextLayerVersion` | run | memory: version numbers are not reused | Skipped SDK test, pending the recording |
| LAM-25 | P3 | The ledger row for function concurrency says "C, stored; no throttling locally". Zero answers `429 TooManyRequestsException` (run) and N is the process ceiling; a call past the ceiling waits in a queue of 64 where AWS throttles. The same document's "Verified against" says the throttle works. | `docs/SUPPORT.md`; `lambda/invoke.go`, about 34 and 312; `internal/lambdaruntime/pool.go` `acquire` | run, read | doc | Ledger test row; a decision on queue or throttle |
| LAM-26 | P3 | Layer permissions are tiered F and are stored without being evaluated. | `docs/SUPPORT.md` | read | n/a | Ledger row to C |
| LAM-27 | P3 | Async retries run back to back. `MaximumEventAgeInSeconds` is stored and unused, which the ledger says. | `lambda/invoke.go` `invokeAsync` | read | doc: one minute, then two | A line in "Differences from AWS" |
| LAM-28 | P2 | The process badge says "Warm · sleeps in …" for a function that has no process: after `Runtime.LaunchError` and after the process exits, `doze-runtime` reports `Runners: 1, Warm: true`. The pool counts runners it created, not processes that started. | `internal/lambdaruntime/pool.go` `acquire` (about 112), `Size`; `lambda/functions.go` `dozeRuntime` | run | n/a | Pool test: a spec that cannot launch reports zero live processes |
| LAM-29 | P2 | The Configuration tab's "Code size" is always `0 B` (LAM-22), and Home's "Code stored" is the figure from LAM-23. | `internal/console/templates/lambda.html` (`lambda_config`, `lambda_home`); `client_lambda.go` | run | n/a | Browser test reads a non-zero size |
| LAM-30 | P2 | The forms' number inputs allow timeout 1 to 900 and memory 128 to 10 240; the service's model-derived table allows 1 to 5400 and 128 to 32 768. Which the console should teach is a decision: the model's range, or the limit AWS documents for a standard function. | `lambda.html` (`lambda_create`, `lambda_config`); `lambda/validate.go` (about 222, 234); `lambda/functions.go` constants | read | model (5400, 32 768); doc (900, 10 240) | Settings-table test once B4 exists |
| LAM-31 | P2 | Home says of layers "attach by ARN in a function's configuration". The Configuration tab has no layers field and `UpdateConfig` never sends `Layers`. | `lambda.html`, about 30; `client_lambda.go` `UpdateConfig` | read | n/a | Browser test: attach a layer from the tab |
| LAM-32 | P2 | The console cannot do what the API can for: versions and aliases, reserved concurrency, the function's resource policy, the dead-letter target and destinations (shown, reset, never set), a mapping's enable switch and settings, Kinesis and DynamoDB sources, layers on a function, the description. Section 6 says which the design adds. | `lambda.html`, `handlers_lambda.go`, `client_lambda.go` | read | n/a | One browser test per item the design takes |
| LAM-33 | P2 | A function URL made from the console answers 403 under `--iam-mode enforce` until a statement grants `lambda:InvokeFunctionUrl` to everyone, and the console has no way to add one. AWS's console adds it. | `handlers_lambda.go` `lambdaCreateURL`; `lambda/urls.go` `serveFunctionURL` | read | doc: function URL auth, the `NONE` policy statement | Enforce-mode browser test: create a URL, fetch it |
| LAM-34 | P3 | Timeout payloads, and `Runtime.LaunchError` at create time, are only in doze-aws's own log. The create form says nothing when the chosen runtime's interpreter is missing. | `lambda/invoke.go` `warnRuntime`; `handlers_lambda.go` `lambdaCreate` | read | n/a | Handler test: create with a missing interpreter, the redirect carries the warning |
| LAM-35 | P1 | `LogType: Tail` returns the last 4 KB the process printed, not the invocation's lines. Reproduced: a warm call's tail began with the previous call's START, END and REPORT. The console's timing split already works around it by reading the last REPORT. | `internal/lambdaruntime/runtime.go` `withTiming` (the ring snapshot); `lambda/invoke.go` `invoke`; `invocationTail` in `lambda/extras.go` already cuts at START for the wire | run | doc: the API reference for `LogType` and `LogResult` | SDK test: invoke twice, the second tail does not contain the first request id |

Owned by the foundation, with what Lambda adds:

- **A1** (the refusal's code): now `run` for Lambda. Every refusal renders
  "Refused" and the message with no code line and no "why" line.
- **A7** (unreachable copy): add Lambda. `copy.go`'s `Unsel` for `lambda` never
  renders, for a different reason from the other ten: `lambda_home` shows the
  account strip and the layers registry instead.
- **A8** (literal identity): `run` under `--account-id 111122223333 --region
  eu-west-1`. The console-created role and `mappingView`'s `FunctionArn` both
  came back with `000000000000` and `us-east-1`. A third site is not in A8's
  list: `"awsRegion": "us-east-1"` in `pollSQS` (`lambda/extras.go`, about 492).
- **A13** (confirms): the layer-version confirm is false today (LAM-11) and the
  function delete says nothing about mappings (LAM-1).

Not a defect, recorded so nobody draws the board: no console action on a
function can be Denied. `AddPermission` writes `Effect: "Allow"` only
(`lambda/policy.go`, about 112), the console calls as root, and a root is
refused only by an explicit Deny.

**Moved between stages.** Three notes from reconciling the plan; the plan's
README has the reasons.

- **LAM-6 moves out, to SQS (stage 1).** The poller's cap of 10 per receive
  lands in the same commit as SQS-5, because once SQS refuses more than 10 a
  trigger with a larger batch size would stop receiving. By this stage it is
  closed; what remains here is delivering one invocation with up to
  `BatchSize` messages gathered over several receives.
- **The function part of CFN-4 moves in (P0, run).** `AWS::Lambda::Permission`
  is accepted and dropped, so a template's grant never reaches the function
  policy. Acceptance: a stack with a function and a permission leaves that
  statement readable through `GetPolicy`.
- **Precondition: DDB-1.** `UpdateTable` ignores `StreamSpecification` until
  DynamoDB's stage (7) fixes it. Stream-trigger work here assumes it is fixed.

## 5. What to check

Beyond the common criteria.

**Functions**
- [ ] Create: name pattern and the 64-character limit for a bare name; a
      duplicate is `ResourceConflictException`; `Role` required and well
      formed; each of zip, S3 and `_local_`; a zip that is not a zip; a path
      that does not exist; `ImageUri` refused by name.
- [ ] Every member of the function settings table in section 2 either reads
      back after create **and** after update, or is refused.
- [ ] `Publish` on create and on code update; `DryRun` on code update.
- [ ] Defaults: `Timeout` 3, `MemorySize` (LAM-9), `Architectures`,
      `PackageType`, `State`, `LastUpdateStatus`.
- [ ] `GetFunction` on a name, a partial ARN, a full ARN, `name:version`,
      `name:alias` and `?Qualifier=`; the same forms on every operation that
      takes a function name.
- [ ] `DeleteFunction`: removes versions, aliases, the URL, the concurrency
      setting, the event invoke config, the mappings' pollers (LAM-1), and the
      process. A version-qualified delete removes that version only, and is
      refused while an alias points at it (not established).
- [ ] `RevisionId` on every operation that takes one (LAM-17).

**Invoke**
- [ ] The three invocation types; an unknown type; `Qualifier` naming a
      version, an alias and nothing that exists.
- [ ] The payload limit, sync and async (LAM-18); a non-JSON payload; an empty one.
- [ ] `X-Amz-Function-Error`, `X-Amz-Executed-Version`, `X-Amzn-RequestId`,
      and `X-Amz-Log-Result` holding that invocation's lines only (LAM-35).
- [ ] Timeout: the caller's answer, the process afterwards (LAM-10), the next
      invocation on that process.
- [ ] Init: a handler that will not import, a handler name that does not
      exist, a process that exits, a process that never polls. Each answers a
      function error with a distinct `errorType`, and none waits out the timeout.
- [ ] Concurrency: 0, 1 with two callers, N above 5, and no setting (LAM-25).
- [ ] The process environment: every variable the ledger lists, the three-pass
      precedence, and the reserved keys (LAM-14).

**Runtimes**
- [ ] Each family in the ledger's table starts, runs, logs with its prefix,
      and reports a thrown error in AWS's shape for that language.
- [ ] `[lambda.runtimes]` wins over `PATH`; a configured path that does not
      exist says so.
- [ ] Code edited in place: a warm process keeps the old code, and a
      configuration update or the idle window brings in the new.

**Versions and aliases**
- [ ] A version freezes code and every setting the fingerprint lists; an
      unchanged publish returns the existing version.
- [ ] Alias create, repoint, delete; a missing target version; a duplicate;
      `RoutingConfig` accepted and visibly cosmetic.
- [ ] Deleting a version an alias or a mapping points at.

**Async and destinations**
- [ ] Retry count 0, 1, 2 and the default; on-success, on-failure and
      dead-letter delivery to SQS, SNS and a function, with the record shape.
- [ ] A destination ARN that does not exist, at put time and at delivery time.

**Event source mappings**
- [ ] Every setting in the mapping table: round trip, then behaviour.
- [ ] SQS: delete on success; return on failure; partial batch responses;
      filters; batch above 10 (LAM-6); a FIFO queue keeps group order; the
      record shape (LAM-9); visibility against timeout (LAM-5); a duplicate
      (LAM-15); the queue deleted under a running mapping.
- [ ] Streams: starting position; a checkpoint that survives a restart; a
      failed batch; a reshard; the record shape for each source.
- [ ] `FunctionName` as a name, an ARN, and with a qualifier (LAM-8).
- [ ] `ListEventSourceMappings` filters by function and by source; states
      `Enabled`, `Disabled` and whatever AWS reports while changing.
- [ ] Delete and update of a UUID that does not exist (LAM-16).

**Function URLs**
- [ ] One per function (LAM-20); both addresses route; `NONE` and `AWS_IAM`
      under off, soft and enforce; `Cors` round trips and is applied or is
      declared cosmetic; a function error is a 502.

**Permissions**
- [ ] `AddPermission`: service and account principals, `SourceArn`,
      `SourceAccount`, a duplicate statement id, an action that is not a
      Lambda action (not established), the 20 KB policy limit (not established).
- [ ] Under soft and enforce each peer needs its statement (owed to integration).

**Layers**
- [ ] Publish from zip, S3 and `_local_`; sizes and hashes (LAM-23); numbering
      (LAM-24); compatible runtimes filter on `ListLayers` and `ListLayerVersions`.
- [ ] A function naming a missing version is refused at create and update.
- [ ] Delete a version in use (LAM-11).
- [ ] Layer order on the search paths, and the function's own path winning.

**Tags, account, lifecycle**
- [ ] Tags: limit, key and value rules, a resource that does not exist (LAM-19).
- [ ] `GetAccountSettings` usage figures (LAM-23).
- [ ] Restart: functions, versions, aliases, concurrency, URL, policy, layers
      and mappings survive, and pollers resume without replay (run today for
      functions, concurrency, layers and an SQS poller).

## 6. Console

The design's "Lambda" page: 24 boards. Templates: `templates/lambda.html`, the
log tail in `logs.html`, shared pieces in `panes.html` and `layout.html`.
Handlers: `handlers_lambda.go`, `lambdaLogs` in `handlers_logs.go`,
`handlers_tags.go`; client `client_lambda.go`; routes `lambdaRoutes` in
`routes_compute.go`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Lambda-Home | Functions, none open: account and layers | `lambda_home`, `lambda_layers` | exists. The caption is `copy.go`'s unreachable `Unsel` (A7). "Code stored" is left off until LAM-23. |
| Lambda-01-None | No functions yet | `lambda_home`, `empty_detail`, `lp_empty` | exists |
| Lambda-02-Create | Create, default | `lambda_create` | form exists; teaching lines and the strip are **new** |
| Lambda-03-Create-Node | Create, Node with variables | `lambda_create`, `env_editor` | form exists |
| Lambda-04-Create-Refused | Create refused | `fail_inline` | exists, without the code (A1) |
| Lambda-05-Test | Test tab before a run | `lambda_fn`, the `cli` block | exists |
| Lambda-06-Invoke-OK | Result: response, timing split, log tail | `lambda_result` | exists |
| Lambda-07-Invoke-Async | Event accepted | `lambda_result` | receipt exists; "If it fails" is **new** |
| Lambda-08-Invoke-Refused | Invoke refused | `fail_inline` | exists, without the code (A1) |
| Lambda-09-Config | Configuration | `lambda_config` | exists; teaching, the queue check, Layers and Reserved concurrency are **new** |
| Lambda-10-Config-Edit | Configuration, edited | `lambda_config` | form exists; "What changes" and the warning are **new** |
| Lambda-11-Config-Refused | Save and Update code refused | `fail_inline` | exists |
| Lambda-12-Triggers | Mappings and the add form | `lambda_triggers` | exists; the two checks and the strip are **new** |
| Lambda-13-Trigger-Refused | Add refused; no triggers | `fail_inline`, `lambda_triggers` | exists |
| Lambda-14-Logs | Logs, one invocation | `lambda_logs`, `log_tail` | exists |
| Lambda-15-Tags | Tags as a draft | `tags_panel`, `tag_editor` | exists (A5) |
| Lambda-16-Versions | Versions and aliases | a new block and handler | **new** entirely |
| Lambda-17-Layer-Versions | A layer's versions | `lambda_layer_versions` | exists |
| Lambda-18-Layer-Version | One version and its permissions | `lambda_layer_detail` | exists |
| Lambda-19-Layer-Refused | Publish and look-up refused; no layers | `fail_inline`, `lambda_layers` | exists |
| Lambda-20-Confirms | The six confirms | six `hx-confirm` attributes in `lambda.html` | exist; the trigger list on function delete is **new** |
| Lambda-21-Bad-Launch | `Runtime.LaunchError` and the config fix | `lambda_result` | result exists; the fix card and "Said once already" are **new** |
| Lambda-22-Bad-Threw | The handler threw | `lambda_result` | exists; "What a trigger does with this" is **new** |
| Lambda-23-Bad-Timeout | Timed out; the process exited | `lambda_result` | results exist; the two explanations are **new** |

There is no Denied board, for the reason at the end of section 4.

**New items, and what each is built from**

| Item | Built from | Depends on |
|---|---|---|
| Teaching lines under each field | The settings table (B4): range and default from `lambda/validate.go` and `validSizing`, tier from the ledger | LAM-9, LAM-30 decided first |
| The life of one invocation | The form's timeout, `DefaultPoolMax`, the idle window from `doze-runtime` (10 m before a function exists) | |
| "Fits its queue" and the two warnings | A mapping's source ARN, `GetQueueAttributes` `VisibilityTimeout`, the function's `Timeout` | Wording changes when LAM-5 is fixed |
| The life of one batch | The form's batch size, the function's timeout, the queue's visibility and redrive policy | LAM-6 |
| What changes | The form against `GetFunctionConfiguration` | |
| Layers on the Configuration tab | `Configuration.Layers`, which the service returns and `lambdaConfigWire` does not read; the newest version from `ListLayers` | LAM-31 for attaching |
| Reserved concurrency | `GetFunctionConcurrency`, read-only | LAM-25 |
| Versions tab | `ListVersionsByFunction`, `ListAliases`, read-only | LAM-13 for long lists |
| "If it fails" on an async invoke | `GetFunctionEventInvokeConfig` and the function's dead-letter target | |
| Triggers still wired, in the delete confirm | `Fn.Mappings`, already loaded | Goes away when LAM-1 is fixed; the sentence then says the triggers are removed too |
| The config fix on a launch error | The `errorType` and `errorMessage` in the result; the family name is in the message | |
| "Said once already" | Not built from data the console has. See below. | |
| "What a trigger does with this" | A mapping's source and that queue's `VisibilityTimeout` | |
| The error code chip | Foundation A1 | |

**Where each teaching line's facts come from**

| Line | Source |
|---|---|
| Timeout: starting the process is not counted | `runtime.go`, the two-phase wait and `initBudget` |
| Timeout: past it the call returns a function error | `runtime.go` `InvokeInput`; LAM-10 changes "the process is not stopped" to the opposite |
| Memory: also sets the CPU share on AWS | doc: developer guide, memory and computing power |
| Memory: stored, not enforced | ledger, "Differences from AWS" |
| Memory: default 512, AWS 128 | `functions.go` `createFunction`; the board changes with LAM-9 |
| Handler: up to 128 characters, no spaces | model: `Handler` length and pattern in `validate.go` |
| Runtime: a missing interpreter is not refused | ledger, Runtimes; `warnRuntime` |
| Code path: runs in place, a warm process keeps old code | ledger, Code |
| Variables: yours win over the injected ones | ledger, Environment variables |
| Up to 5 processes; a call past that waits | `pool.go` `DefaultPoolMax`, `acquire` |
| Concurrency 0 refuses every call | `invoke.go`; doc for AWS |
| Idle window | `[lambda] idle-timeout`, `doze-runtime` |
| Six times the timeout for a queue | doc: "Using Lambda with Amazon SQS" |
| Function URL: `AWS_IAM` unsigned; `NONE` needs a statement under enforce | ledger; `urls.go` `serveFunctionURL` |
| Layers: laid out as on AWS, on search paths, not `/opt` | ledger, "Layers, without /opt" |
| Layer permissions: stored, not evaluated | ledger, "Not covered" |
| A version freezes code and configuration; weights not applied | ledger; `aliases.go` |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- 21, "Said once already": the console has no access to doze-aws's log, so it
  cannot show the create-time warning. Drop the card, or carry the warning on
  the create response (LAM-34) and store it.
- 21, the badge reads "Warm" on a function that cannot start. True of the code
  (LAM-28) and wrong; the board changes to Idle with the fix.
- 12, "Over 10, SQS here still hands out one message per receive": true today
  (run) and removed by LAM-6.
- 12 and 10, "AWS refuses an SQS trigger set up like this": `doc` for the rule,
  `memory` for the refusal. Recording queue.
- 20, "Functions already configured with it keep running": false today (LAM-11).
- 20, "The address is derived from the function's name, so a new one is the
  same address": read from `urlID`; not run.
- 23, "plus one second of grace for the function to report its own": the grace
  exists, and no embedded client reports its own timeout. Reword when LAM-10 lands.
- 06, 22, 23: durations, request ids and stack frames are sample data. A real
  Node trace carries four more frames from the embedded client.
- 09 and 10 leave out the wiring strip that sits above the tabs on every tab.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | `configView` carries `CodeSize` and a stable `LastModified` (LAM-21, 22); account and layer sizes (LAM-23); layer numbering (LAM-24); mapping delete and update of a missing UUID (LAM-16) |
| Runtime (`internal/lambdaruntime`) | The handler is stopped at its timeout (LAM-10); a pool whose process never started reports none (LAM-28); the SQS record's key set (LAM-9) |
| SDK v2 | Update reads back every member (LAM-2); `Publish` (LAM-7); a qualified mapping (LAM-8); delete with a mapping (LAM-1); every mapping setting (LAM-3, LAM-4); batch 20 (LAM-6); a paginator per list (LAM-13); `RevisionId` (LAM-17); URL twice (LAM-20); tags (LAM-19); a layer deleted in use (LAM-11) |
| Rejection parity | Reserved keys and 4 KB (LAM-14); 6 MB (LAM-18); visibility against timeout (LAM-5, skipped until recorded); a duplicate mapping (LAM-15, skipped) |
| Router | Every operation in `ops_lambda.json` routed or stubbed with the agreed code (LAM-12) |
| Restart | A stream mapping resumes where it stopped (LAM-4); an SQS mapping resumes; a version's frozen code |
| Identity | The package and the browser spec under a second account and region (A8, B2) |
| Conformance (boto3) | Every row in section 8 |
| Browser | Each `new` item; each confirm's sentence; both states of the queue check; a refusal on every form; the badge after a launch error |
| Mutation | `lambda/` and `internal/lambdaruntime/` baselines, then survivors in `extras.go`, `functions.go`, `pool.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today. Scenarios go in
`conformance/tests/test_lambda.py`.

| Claim | Source now | Scenario |
|---|---|---|
| A trigger is refused when queue visibility is shorter than the function timeout; the code and message | doc (rule), memory (refusal) | `trigger_visibility_shorter_than_timeout` |
| A second mapping for the same queue and function is a conflict | memory | `trigger_duplicate` |
| What happens to a function's mappings when it is deleted | memory | `delete_function_with_trigger` |
| The SQS event record's keys | doc | `sqs_event_shape` |
| Partial batch response returns only the failed items | doc | `partial_batch_response` |
| A filter drops a non-matching message and deletes it from the queue | doc | `trigger_filter` |
| Batch size above 10 needs a batching window; the refusal without one | memory | `trigger_batch_over_ten` |
| Default `MemorySize` is 128 | doc | `create_defaults` |
| `Publish: true` returns version 1 | doc | `create_publish` |
| Reserved environment keys and the 4 KB limit; codes and messages | doc | `environment_refusals` |
| A timed-out invoke's payload text and REPORT line | memory | `invoke_timeout` |
| `LogType: Tail` holds only that invocation | doc | `tail_is_one_invocation` |
| Payload above 6 MB is `RequestTooLargeException` | doc | `invoke_payload_limit` |
| `LastModified` format | memory | `create_defaults` |
| `RevisionId` mismatch on each operation | doc | `revision_id` |
| `CreateFunctionUrlConfig` twice | doc | `url_twice` |
| 51 tags; untag on a missing function | doc, memory | `tag_limits` |
| Layer version numbers after a delete | memory | `layer_numbering` |
| A function keeps a deleted layer version | doc | `layer_deleted_in_use` |
| Delete of a mapping that does not exist | doc | `delete_missing_trigger` |
| Paging on each list, and `NextMarker` | model | `list_paging` |
| Stream mapping: `StartingPosition` required, a failed batch blocks the shard | doc | `stream_trigger_failure` |
| Async retry timing and the destination record's shape | doc | `async_retries` |
| Reserved concurrency 0, and the throttle's code on sync and async | doc | `reserved_concurrency_zero` |
| What an unsupported operation answers, per family | not established | `unsupported_operations` (decides LAM-12's stub code) |

## 9. Owed to integration

| What | With |
|---|---|
| Visibility shorter than timeout is refused at trigger creation; the queue's Configuration tab shows the same check from its side | SQS |
| A deleted function's poller stops; its messages are not received on its behalf | SQS, Kinesis, DynamoDB |
| A trigger batch above 10 | SQS |
| A FIFO queue as a source keeps group order across a failure | SQS |
| A stream mapping across a reshard and a restart | Kinesis, DynamoDB |
| A function's output reaches its log group; the Logs tab and the Logs service show the same lines | CloudWatch Logs |
| Invocations, Errors, Throttles and Duration metrics, including a throttled call | CloudWatch |
| Each invoker needs its `AddPermission` statement under soft and enforce | S3, SNS, EventBridge, API Gateway, Logs, Secrets Manager |
| Async destinations and the dead-letter target deliver, and a missing one is visible | SQS, SNS |
| A function, version, alias, URL, layer and mapping from one template, including `AutoPublishAlias` with an SQS event (LAM-8) | CloudFormation |
| A function error from a rotation or an authorizer reads as the function's failure, not the emulator's | Secrets Manager, API Gateway |
| The wire shows an event-source invoke under the call that caused it | the console's trace |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] LAM-1 to LAM-35 each closed, with the test that failed first named
      beside it, or moved to section 8 or 9 with a reason.
- [ ] The 36 unrouted operations each have a tier in `SUPPORT.md` and an entry
      in `not-built.md`, and answer the stub code the recording or the decision
      names. Resolved here: InvokeAsync, provisioned concurrency, runtime
      management config, recursion config, the three resource-policy operations.
- [ ] The function settings table in section 2 has no "dropped", "ignored" or
      "not established" left: each member reads back after create and update,
      or is refused, and each C member reads as cosmetic in the console.
- [ ] The mapping settings table has no "dropped" left. Each of the six stream
      settings in its last-but-one row is decided F or C in `SUPPORT.md`.
- [ ] Decided and written into `SUPPORT.md`: the default `MemorySize`; the
      ranges the console teaches (LAM-30); a call past the concurrency ceiling
      queues or throttles; async retry timing; the batching window.
- [ ] Established and written down: `RevisionId` on `UpdateFunctionCode`,
      `UpdateAlias`, `AddPermission`, `RemovePermission`; tags on a mapping or
      a layer; `LoggingConfig.LogGroup` and `SystemLogLevel`; `AddPermission`
      with a non-Lambda action and at the policy size limit; a version delete
      while an alias points at it; what the CloudFormation path sends for a
      mapping on an alias; which of the absent families a Terraform or CDK
      refresh calls.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] A handler is stopped at its timeout, and a test shows a write after the
      timeout does not happen.
- [ ] No stream record is delivered twice across a restart.
- [ ] Deleting a function leaves no mapping polling, and deleting a layer
      version leaves every function that names it working.
- [ ] All 24 boards implemented from the foundation's shell, or listed here as
      dropped with the reason. Each `new` item reads only the data named for it.
- [ ] No statement on a Lambda screen is in the "not yet backed" list in section 6.
- [ ] The six confirms say only what the code does.
- [ ] The queue check appears in both states and names the real queue, and its
      wording matches whether LAM-5 is fixed.
- [ ] The process badge is Idle for a function with no process.
- [ ] `e2e/tests/lambda.spec.ts` covers every route under `/lambda` with the
      route gate on, and the accessibility sweep is clean on the six tabs,
      create and Home.
- [ ] Conformance: a scenario for every row in section 8. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baselines recorded here: `lambda/` ____ killed, ____ survived;
      `internal/lambdaruntime/` ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 0 | In stage 1, with SQS-5: cap the poller's receive at 10 and fill a batch over several receives | `lambda/extras.go` `pollSQS` |
| 1 | Contract: all 88 operations tiered, the two settings tables, the decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for LAM-1 to LAM-24 and LAM-35 | `lambda/*_test.go`, `internal/lambdaruntime/*_test.go` |
| 3 | Mappings: store every setting, qualified names, delete with the function, missing UUIDs, duplicates, visibility | `lambda/extras.go`, `lambda/store.go`, `lambda/functions.go` |
| 4 | Mappings: the SQS record, filters, partial batch responses, batching | `lambda/extras.go`, `internal/peercall/peercall.go` |
| 5 | Mappings: stream starting position, a persisted checkpoint, failure handling | `lambda/extras.go`, `lambda/store.go` |
| 6 | Functions: update applies every member, `Publish`, `RevisionId`, `CodeSize`, `LastModified`, defaults, environment checks | `lambda/functions.go`, `lambda/versions.go`, `lambda/aliases.go`, `lambda/policy.go` |
| 7 | Runtime: stop the handler at its timeout, the timeout payload, the tail per invocation, the pool's live count | `internal/lambdaruntime/runtime.go`, `pool.go`, `logs.go`; `lambda/invoke.go` |
| 8 | Layers, tags, URLs, account, payload limit | `lambda/layers.go`, `lambda/urls.go`, `lambda/invoke.go` |
| 9 | Paging on every list; the 36 absent operations | `lambda/router.go`, `lambda/validate.go` (generated), `lambda/functions.go`, `lambda/layers.go`, `lambda/versions.go`, `lambda/aliases.go`, `lambda/extras.go` |
| 10 | Section 5 sweep and parity cases | `lambda/rejection_parity_test.go`, `lambda/sdk_test.go`, `lambda/sdk_coverage_test.go`, `lambda/testdata/cases_lambda.json` |
| 11 | Conformance scenarios and the claims table | `conformance/tests/test_lambda.py`, the claims file from foundation B1 |
| 12 | Console | `internal/console/templates/lambda.html`, `handlers_lambda.go`, `client_lambda.go`, `routes_compute.go`, `copy.go` |
| 13 | Browser tests | `e2e/tests/lambda.spec.ts` |
| 14 | Ledger, changelog, user guide, status | `docs/SUPPORT.md`, `docs/lambda.md`, `CHANGELOG.md`, `plan/README.md` |

**Size.** Rows 3 to 5 are a rewrite of the mapping layer, and row 7 changes how
a process is supervised. Together with 24 boards that is nearer eight days than
five. If the stage has to be cut, the line is between rows 1 to 11 and rows 12
to 13, not inside the mapping work: LAM-1 to LAM-11 are all P0.

**Prior art.** AWS's developer guide is the source for every hand-derived rule
above: "Using Lambda with Amazon SQS" for the visibility rule, the six-times
advice, the record shape and partial batch responses; "Control which events
Lambda sends to your function" for filters; the Kinesis and DynamoDB pages for
starting position and failure handling. Each rule's test names its page. The
Runtime API reference is what `internal/lambdaruntime` already implements, and
its `Lambda-Runtime-Deadline-Ms` header is the hook LAM-10 needs. No other
emulator's source was read for this document, so none is cited.
