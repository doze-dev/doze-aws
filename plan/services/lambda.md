# Lambda

Stage 10. Size **XL** (was L; see section 1 and section 11). Follows
[../01-protocol.md](../01-protocol.md); the common acceptance criteria there
apply in full, and this page adds Lambda's own.

**Read the review below first.** This page was corrected on 2026-10-11 against
the model's own documentation text, against twelve AWS documentation pages read
for that purpose, and against a binary built from the working copy; every
change is marked *Corrected 2026-10-11*. Findings LAM-36 to LAM-74 are new.

## Pre-stage review (2026-10-11)

Answers to the twelve questions in [../02-lessons-from-sqs.md](../02-lessons-from-sqs.md).
"Read" says what was read; a finding id is in section 4. Sources are `model`
(`.audit-models/lambda.json`: the documentation text of all 88 operations and
every member of their input and output shapes, the 56 error shapes, the enums),
`doc` (a named AWS page that was fetched and read, listed at the end of this
section), `run` (reproduced against a binary built from this checkout) and
`memory` (not fixed on).

| Q | Answer |
|---|---|
| **L1** Read the model's documentation | **Findings.** Every number, range, default and name in this document was re-read; 58 corrections are marked in place in sections 1 to 10 (a claim that was wrong, a source tag that was wrong, or a fact the document said was "not established" that the model establishes). The model's text alone produced LAM-37, 38, 40, 45, 46, 47, 49, 53, 59, 61, 62, 65, 66. The biggest single correction is a source tag: LAM-5's refusal is `doc`, not `memory` (the Lambda model does not contain it; "Creating and configuring an Amazon SQS event source mapping" does). **Also found:** `lambda/validate.go` was generated from an older model (47 operations, 621 constraints); the model now has 88 and 842. LAM-43, LAM-44. |
| **L2** Optional parameters have meanings | **Findings.** `DryRun` (LAM-37), `Publish` (LAM-7), `CodeSha256` on PublishVersion (LAM-40), `FunctionVersion=ALL` and the compatible-runtime and -architecture filters (LAM-65), `Qualifier` on event invoke config, URL config and policy (LAM-52, 58, 59), `StartingPosition` required for streams and the batching window required above 10 (LAM-53), `BatchSize` default 100 for streams (LAM-53), `Enabled`, `FunctionName` on UpdateEventSourceMapping (LAM-3). |
| **L3** Error codes are in the model | **Findings.** The model names codes the service never emits: `RequestTooLargeException` 413 (LAM-18), `UnsupportedMediaTypeException` 415 (LAM-64), `ResourceInUseException` 400 (LAM-55), `PolicyLengthExceededException`, `PublicPolicyException`, `CodeStorageExceededException`, `AliasLimitExceededException` (limits whose values the model does not give: recording queue), `CodeSigningConfigNotFoundException` (LAM-60), `ResourceNotFoundException` on seven deletes that succeed (LAM-57), `PreconditionFailedException` on eight operations of which one checks (LAM-17). `TooManyRequestsException` carries `Reason` and `Retry-After` (LAM-63). Unrouted operations answer 404 `ResourceNotFoundException` and should answer 501 (LAM-12). |
| **L4** Direction | Named on every finding (column "Direction"). Of the 39 new rows: 16 more forgiving than AWS, 1 stricter (a hard-coded identity), 6 wrong shape, 14 wrong behaviour or value, 1 a deliberate difference, 1 a document. Severity: 1 P0, 20 P1, 15 P2, 3 P3. |
| **L5** Every wire | **Checked, with findings.** The model declares `restJson1` only (`aws.protocols#restJson1`, no `xmlName`, no `jsonName`, no flattened members), so there is one wire and one decoder. What the sweep found is the restJson1 form of SQS-26: the constraint walk reads no **map keys** (LAM-43) and the table is stale (LAM-44); four routed operations with constrained inputs have no table at all. Five routed operations have no SDK-spelled call in any test (grep: `DeleteFunctionCodeSigningConfig`, `ListFunctionUrlConfigs`, `PutFunctionCodeSigningConfig`, `UpdateFunctionEventInvokeConfig`, `UpdateFunctionUrlConfig`): each gets one accepted-as-the-SDK-spells-it test (section 7). The gateway routes unsigned requests by path prefix and knows eight of the seventeen date prefixes Lambda uses (LAM-68). |
| **L6** Tokens are per use | **Findings.** `RevisionId`: the model lists `PreconditionFailedException` on eight operations and four of them were "not established" here; they are now (LAM-17). The *policy's* revision id is separate from the function's and is never changed by AddPermission (LAM-59). `Marker` is "valid for 24 hours" on the durable-execution lists only; Lambda's lists give no lifetime (LAM-13). Function version numbers are never reused, even across a delete and re-create (`doc`; LAM-41); layer numbers are not stated by the model (LAM-24 stays `memory`). An event source mapping's `UUID` is the only handle; a stale one is `ResourceNotFoundException` (LAM-16). |
| **L7** Asynchronous on AWS | **Decided below**, findings LAM-55, 56, 49, 27. The table after this one. |
| **L8** Latency-based rules, opt-in | **Decided below.** Each rule listed with its switch. |
| **L9** State in memory or on disk | **Decided below.** |
| **L10** The board is the spec | **Table 6a**, made from 24 boards rendered beside 24 pages, light and dark, and at 900 wide. Verdicts: 4 matches, 19 differs (each with a build decision), 1 cannot match yet. 7 facts on the design were wrong and are corrected in the working copy. |
| **L11** Five teaching lines | **Findings, none present.** No Lambda form field carries any of the five lines today. The settings that need a record are listed in section 10, and build item 1 of 6a is the work. |
| **L12** Icons | **Checked, none.** The rail and the resource header draw the AWS icon (`awsIcon "lambda"`, seen in every render). The list pane header draws text only ("Lambda (n)"), as it does for every service (`lp_head`): a shell matter, not Lambda's. Where a board draws `λ` the page keeps the icon. |

**Classes of mistake Lambda is most at risk of**

1. *Writing a setting down and not reading it back* (LAM-2, 3, 42, 52, 58, 60): Lambda has the widest surface of any service here, and every deploy tool reads each member back and plans a diff forever when one is missing. The test for every member is "send it, read it, send it again, read the same".
2. *Behaviour the model states in prose, not in a trait* (LAM-37, 38, 45, 46, 53, 62): "can't", "only", "must", "at least" in documentation strings, which the generated tables cannot hold. The SQS lesson exactly.
3. *A tool that was generated once* (LAM-43, 44, 71): the constraint table is a snapshot; the model moved by 41 operations and 221 constraints, and the walk misses map keys. A weekly drift check has to compare the table, not only the route list.
4. *Identity and region as a literal in a helper* (LAM-36): the poller was written when the account in a queue URL was ignored. SQS stopped ignoring it in stage 1; nothing re-ran Lambda.
5. *Time and states that AWS makes observable* (LAM-55, 56): `Creating`, `Pending`, `InProgress`, `Deleting`, the one-minute and two-minute retry waits, the six-hour queue. Tools poll these; the decision below keeps them immediate and says so.
6. *A console written from the design's words* (6a): 19 of 24 boards differ in shape.

**Documentation pages read (named, `doc`).** Lambda quotas (`gettingstarted-limits`); Using Lambda with Amazon SQS (`with-sqs`); Creating and configuring an Amazon SQS event source mapping (`services-sqs-configure`); Handling errors for an SQS event source (`services-sqs-errorhandling`); Control which events Lambda sends to your function (`invocation-eventfiltering`); How Lambda handles errors and retries with asynchronous invocation (`invocation-async-error-handling`); Capturing records of Lambda asynchronous invocations (`invocation-async-retain-records`); Working with Lambda environment variables (`configuration-envvars`); Manage Lambda function versions (`configuration-versions`); Lambda function states (`functions-states`); Using Lambda to process records from Amazon Kinesis Data Streams (`with-kinesis`); Invoking Lambda function URLs (`urls-invocation`); Using tags on Lambda functions (`configuration-tags`). All under `docs.aws.amazon.com/lambda/latest/dg/`.

### L7: what is asynchronous on AWS, and what doze-aws does

| Operation or state | AWS (source) | Decision | Difference listed |
|---|---|---|---|
| Function create: `State` `Pending` then `Active` | `model` (`State` enum; CreateFunction: "you can't invoke or modify the function … The State fields indicate when the function is ready"); `doc` (functions-states: `Pending`, "Lambda does not invoke a function during pending state") | **Immediate**: `Active` and `Successful` in the response. A zip function with no VPC is seconds on AWS, and tools wait with a poll that an `Active` answer satisfies. | "State is never `Pending`; `Invoke` and the update operations are never refused for it." LAM-56 |
| Function update: `LastUpdateStatus` `InProgress` | `doc` (functions-states: Update*, PublishVersion and TagResource fail while `InProgress`; invokes keep the previous code) | **Immediate**, same note. | LAM-56 |
| Event source mapping create: `Creating` then `Enabled`; `Enabling`, `Disabling`, `Updating` | `model` (`State`: "Creating, Enabling, Enabled, Disabling, Disabled, Updating, or Deleting"); `doc` (a CloudTrail sample shows `"state": "Creating"` on the response) | **Immediate** for create and update. `Enabled` or `Disabled` answers at once. | LAM-55 |
| Event source mapping delete: `Deleting` "for several seconds" | `model` (DeleteEventSourceMapping) | **Decided: model it.** The delete answers 202 with the full mapping in `Deleting`, a Get for the next two seconds answers `Deleting`, then `ResourceNotFoundException`. It is cheap (a timestamp on the record) and a test that deletes and re-creates a mapping to the same source races on AWS. The poller stops at the call. | LAM-54, 55 |
| `ResourceInUseException`: update while `Creating`, delete while `Updating` | `model` (the error shape's text) | With immediate create and update there is no window, so the rule has nothing to guard. Listed. | LAM-55 |
| Async invoke (`Event`): queued, retried | `model` (Invoke: "up to two more times"; PutFunctionEventInvokeConfig: "retains events in a queue for up to six hours"); `doc` (invocation-async-error-handling: one minute, then two; 6 hours and 1 s to 5 min back-off for throttles) | **Modelled with the waits, opt-in**: `[lambda] async-retry-delays = "aws"` (default `"none"`: back to back, as today). A test loop that waits three minutes for a retry is a worse tool; one that wants the real timing sets it. `MaximumEventAgeInSeconds` is honoured in both modes (an event older than the age at its next attempt is discarded). | LAM-27 |
| Async invoke on reserved concurrency 0 | `doc` (invocation-async-retain-records: events go "to the configured dead-letter queue or the on-failure destination, without any retries") | **Modelled**: 202, then the failure path at once. | LAM-49 |
| Throttled async event (concurrency exhausted) | `doc` (retried "for up to 6 hours by default", 1 s growing to 5 min) | Waits for a process (the pool queues): immediate-with-difference. | LAM-25 |
| DeleteLayerVersion: "a copy of the version remains in Lambda until no functions refer to it" | `model` | **Modelled**: the copy stays while any function or version names it; it is gone from Get and List at once. | LAM-11 |
| Function delete | `model` (DeleteFunction returns at once; mappings are not deleted: "To delete Lambda event source mappings that invoke a function, use DeleteEventSourceMapping") | Immediate. The mapping stays, **its poller stops receiving** (nothing is read on behalf of a function that is gone). What AWS does with the messages is `memory`: recording queue. | LAM-1 |
| Provisioned concurrency `IN_PROGRESS` to `READY` | `model` | Declined (S, 501). | LAM-12 |
| Stream mapping: shard polling, checkpoint | `doc` (with-kinesis: "stops processing additional records in a shard if your function returns an error") | Continuous: persisted checkpoint (L9). | LAM-4 |

### L8: rules that exist because of AWS's latency

| Rule | Where | Switch (default off) |
|---|---|---|
| Update, publish and tag are refused while a function is `Pending` or `InProgress` (`ResourceConflictException`) | `doc` (functions-states) | `[lambda] strict = true` (`--lambda-strict`): the function sits `Pending` for 1 s after create and `InProgress` for 1 s after an update, and the refusals apply. Off by default. |
| Update an event source mapping in `Creating`, delete one in `Updating` (`ResourceInUseException`) | `model` | Same switch; the mapping spends 1 s in `Creating` and `Updating`. |
| A replayed async delivery after a restart | `memory` | None: the in-flight queue is the process's (L9). |

Nothing else in Lambda's model is a "wait a minute" rule: there is no
name-reuse window and no purge limit. The switch exists only if the stage
builds the two rows above; if it is cut, the rows move to "Differences from
AWS" in the ledger and the switch is not added.

### L9: state, in memory or on disk

| State | Where | Why |
|---|---|---|
| Functions, versions, aliases, URL, policy, concurrency, event invoke config, layers, mappings | disk (today) | The application's configuration. |
| **Function version high-water mark** (survives deleting the function: `doc`, "never reuses version numbers, even after you delete and recreate a function") | disk, new | Losing it makes a re-created function hand out a number an alias elsewhere still holds. LAM-41. |
| Layer version high-water mark | disk, new | Same, and `DeleteLayerVersion` of the newest must not free its number. LAM-24. |
| **Stream checkpoint** per mapping and shard | disk, new | Losing it replays a stream on every restart (LAM-4); it is the application's data. |
| Mapping state transitions (`Deleting` window) | memory | A 2-second window is the process's. |
| In-flight async events and their retry timers | memory | AWS persists them; locally a restart inside the window drops them, listed as a difference. Persisting them would make every restart replay side effects. |
| `ApproximateInvokeCount`, the retry chain | memory | Part of the above. |
| Warm process pool, idle timer, poller goroutines | memory | The process's. |
| The batching buffer of a mapping (`MaximumBatchingWindowInSeconds`) | memory | Messages stay in the queue until delivered; nothing is lost. |

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

*Corrected 2026-10-11: "the function runtime is in good shape" holds for the
process supervision and not for the control plane around it. The review found
a P0 in the poller that the first pass could not have seen (LAM-36: no SQS
trigger delivers under a non-default account, since stage 1 made a queue URL's
owner count), 20 more P1s, and the generated validation tables are a snapshot
of a model that has since grown by 41 operations (LAM-44).*

Line numbers are as found on 2026-10-10 at `8cd261f` and will drift; the symbol
beside each one will not. Every `run` below was reproduced against a binary
built from that commit, driven over HTTP. Rows marked *run 2026-10-11* were
reproduced again, or for the first time, against a binary built from the
checkout at `7cae441` (the stage-1 code).

**Estimate:** ~~L, five or more focused days. Section 11 argues it is nearer
eight, and that one fix belongs in stage 1.~~ *Corrected 2026-10-11:* **XL,
about 25 focused days (20 to 30), as two stages.** The plan listed 35
findings; the review adds 39 (74). SQS went from 8 listed to 29 closed, 3.6
times, and this review is the same exercise done before the stage instead of
after, so the count is expected to grow again by a fraction rather than a
multiple. The mapping layer is a rewrite (rows 3 to 5 of section 11, 6
days alone), a process is now stopped at its timeout (row 7), the constraint
tables are regenerated and the walk gains map keys (LAM-43, 44; the walker is
shared by every service's table), and 19 of 24 boards are rebuilt in shape (6a).
**Recommended split:** *10a* the API (contract, identity, mappings, functions,
versions, runtime, validation: about 17 days), *10b* the console (about 8 days,
and it can start only when LAM-73, 22, 30, 45 and 68 have landed because the
teaching lines read them). The line between them is the one section 11
already draws. **Actual:** _fill in when done._

## 2. Contract

The model (`lambda/testdata/ops_lambda.json`) lists **88** operations. 52 are
routed, plus one extension. 36 are not routed at all and answer
`404 ResourceNotFoundException: unknown path "…"` (run), which is neither a
tier nor a row in `not-built.md`. See LAM-12.

*Checked 2026-10-11 (`model`, run 2026-10-11): 88 operations in
`.audit-models/lambda.json`; 52 routed (`validate.go` 47 plus
`GetLayerVersionByArn` and the four layer-policy and account operations that
`router.go` adds); 36 unrouted, including seven on routes the **gateway** does
not send to Lambda when the request is unsigned (LAM-68). The model also gives
`InvokeAsync` a `smithy.api#deprecated` trait. `validate.go`'s constraint
tables cover 47 of the 52 and are generated from an older model (LAM-44).*

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateFunction, UpdateFunctionConfiguration, UpdateFunctionCode | F | F | Per-setting tiers below. `Publish` is ignored (LAM-7). Update drops most members (LAM-2). |
| GetFunction, GetFunctionConfiguration, DeleteFunction | F | F | `CodeSize` missing, `LastModified` wrong (LAM-21, LAM-22). Delete leaves mappings (LAM-1). |
| ListFunctions | F | F | Ignores `MaxItems` and `Marker` (LAM-13). |
| Invoke | F | F | RequestResponse, Event, DryRun. Tail and size limit: LAM-11, LAM-18. |
| PublishVersion, ListVersionsByFunction | F | F | List does not page. |
| CreateAlias, GetAlias, ListAliases, UpdateAlias, DeleteAlias | F | F | *Corrected 2026-10-11: was "`RoutingConfig` is C: accepted, not stored". C means accepted **and round-tripped** (protocol step 1); not stored reads back absent, and a deploy tool plans the change forever (run 2026-10-11). It becomes C when it is stored and returned (LAM-42).* A missing alias deletes with 204; the model lists `ResourceNotFoundException` (LAM-57). |
| PutFunctionEventInvokeConfig, UpdateFunctionEventInvokeConfig, GetFunctionEventInvokeConfig, DeleteFunctionEventInvokeConfig, ListFunctionEventInvokeConfigs | F | F | `MaximumEventAgeInSeconds` is C. Retries have no delay (LAM-27). *Added 2026-10-11:* the config is per function, version or alias (`model`: "for a function, version, or alias"); the code keeps one per function and ignores `Qualifier` (LAM-52). Destination types: SQS (standard), SNS (standard), Lambda, EventBridge, S3 on failure only (`model`, `doc`); two of the five are dropped silently (LAM-50). |
| CreateFunctionUrlConfig, GetFunctionUrlConfig, UpdateFunctionUrlConfig, DeleteFunctionUrlConfig, ListFunctionUrlConfigs | F | F | `AuthType AWS_IAM` unsigned: C. *Corrected 2026-10-11: `InvokeMode` is not C: a `RESPONSE_STREAM` request reads back `BUFFERED` (run); C needs the round trip (LAM-58).* A second create is not refused (LAM-20). One URL per function and one per alias (`model`: `Qualifier` "The alias name"); the code keeps one (LAM-58). |
| PutFunctionConcurrency, GetFunctionConcurrency, DeleteFunctionConcurrency | C | F, with a written difference | The ledger says "no throttling locally". It throttles at 0 and sets the process ceiling at N (LAM-25). *Added 2026-10-11:* `GetFunction` does not return it (LAM-47); the rule "leave at least 100 simultaneous executions unreserved" (`model`) is not checked (LAM-62). |
| TagResource, UntagResource, ListTags | F | F | Functions only. No limit (LAM-19). ~~Tags on a mapping or a layer: not established.~~ *Corrected 2026-10-11 (`model`): tags are for "a function, event source mapping, or code signing configuration" (and capacity providers); "Lambda does not support adding tags to function aliases or versions"; a layer ARN is refused by the `TaggableResource` pattern (run, correct). Mappings are therefore a gap: a tag call on a mapping ARN answers 404 (LAM-72).* |
| CreateEventSourceMapping, GetEventSourceMapping, ListEventSourceMappings, UpdateEventSourceMapping, DeleteEventSourceMapping | F | F | Per-setting tiers below. Most findings are here. |
| AddPermission, RemovePermission, GetPolicy | F | F | Allow statements only, as on AWS (`memory`: the model says "adds a statement", not its effect). *Added 2026-10-11:* `Qualifier`, `FunctionUrlAuthType`, `InvokedViaFunctionUrl` and `RevisionId` are not honoured (LAM-17, LAM-59). |
| PublishLayerVersion, GetLayerVersion, GetLayerVersionByArn, ListLayers, ListLayerVersions, DeleteLayerVersion | F | F | LAM-11, LAM-23, LAM-24, LAM-65. *Corrected 2026-10-11: was "LAM-23, LAM-24, LAM-37"; there was no LAM-37 in this document.* |
| AddLayerVersionPermission, GetLayerVersionPolicy, RemoveLayerVersionPermission | F | C | Stored and returned, never evaluated (`SUPPORT.md`, "Not covered"). The row says F (LAM-26). *Added 2026-10-11: these three have no constraint table at all (run: `lambda:Nope`, a statement id with a space and principal `abc` were accepted; LAM-44), `OrganizationId` is dropped and `RevisionId` is the constant `"1"` (LAM-17).* |
| GetAccountSettings | F | F | `TotalCodeSize` is wrong (LAM-23). Limits are nominal. *Added 2026-10-11:* `UnreservedConcurrentExecutions` does not fall when concurrency is reserved, and the usage omits layers (`model`, LAM-61); the console's Account strip, which reads this, never renders (LAM-68). |
| GetFunctionCodeSigningConfig, PutFunctionCodeSigningConfig, DeleteFunctionCodeSigningConfig | C | C | Round trip only, so Terraform's refresh passes. Not in the ledger table today. *Added 2026-10-11:* Put accepts the ARN of a config that cannot exist, because `CreateCodeSigningConfig` is not served and the model lists `CodeSigningConfigNotFoundException` (LAM-60). |
| InvokeAsync, InvokeWithResponseStream | absent | S, or F for InvokeAsync | InvokeAsync is deprecated on AWS (`model`: `smithy.api#deprecated`; "For asynchronous function invocation, use Invoke"; payload limit 256 KB) and is one line over the Event path. **Proposed 2026-10-11: F**, 202 with no body (`Status` is the response code), the 256 KB limit refused as `RequestTooLargeException`; it is a deprecated operation old SDKs still call. Streaming is declined (S). |
| PutProvisionedConcurrencyConfig, GetProvisionedConcurrencyConfig, ListProvisionedConcurrencyConfigs, DeleteProvisionedConcurrencyConfig | absent | S or C | `not-built.md` says "provisioned concurrency semantics" are cloud-only. Whether a deploy tool reads these on refresh is not established. *Checked 2026-10-11: the one real Terraform apply and re-plan on record (`e2e/deploy/terraform`, state serial 23) read none of them and showed no drift; a newer provider may. **Proposed: S** (501), with `Get` as `ProvisionedConcurrencyConfigNotFoundException` if a recording shows a refresh calls it.* |
| CreateCodeSigningConfig, GetCodeSigningConfig, UpdateCodeSigningConfig, DeleteCodeSigningConfig, ListCodeSigningConfigs, ListFunctionsByCodeSigningConfig | absent | S | Declined. |
| GetRuntimeManagementConfig, PutRuntimeManagementConfig | absent | S or C | Not established whether Terraform or CDK call them. *Model, 2026-10-11: `UpdateRuntimeOn` `Auto` is the default, so a C answer is `{"UpdateRuntimeOn":"Auto","FunctionArn":…}` and a Put stores `Auto`, `FunctionUpdate` or `Manual`. **Proposed: C**, because the answer costs nothing and a provider that adds the read breaks every function otherwise.* |
| GetFunctionRecursionConfig, PutFunctionRecursionConfig | absent | S or C | Same question. *Model: `RecursiveLoop` `Allow` or `Terminate`, "By default, Lambda sets your function's configuration to Terminate". **Proposed: C**, same reason.* |
| GetFunctionScalingConfig, PutFunctionScalingConfig | absent | S | |
| GetResourcePolicy, PutResourcePolicy, DeleteResourcePolicy | absent | S | ~~Which resources these address in the current model is not established.~~ *Corrected 2026-10-11 (`model`): `/2026-07-09/resource-policy/{ResourceArn}`, `ResourceArn` a function (qualified or not) or a `runtime/` ARN; `Policy` 1 to 20 480 characters; `RevisionId` a UUID; "This operation replaces any existing policy on the Lambda resource. If you previously added permissions using the AddPermission operation, the new policy overwrites those permissions." It is the same document `AddPermission` edits, so it is **F** when the policy code is touched for LAM-59 and S until then.* |
| CreateCapacityProvider, GetCapacityProvider, UpdateCapacityProvider, DeleteCapacityProvider, ListCapacityProviders, ListFunctionVersionsByCapacityProvider | absent | S | Managed instances. Declined. |
| CheckpointDurableExecution, GetDurableExecution, GetDurableExecutionHistory, GetDurableExecutionState, ListDurableExecutionsByFunction, SendDurableExecutionCallbackFailure, SendDurableExecutionCallbackHeartbeat, SendDurableExecutionCallbackSuccess, StopDurableExecution | absent | S | Durable executions. Declined. |
| DozeRuntime (`GET /functions/{name}/doze-runtime`) | extension | extension | Process state for the console badge. |

Function settings, because "CreateFunction is F" hides which do anything.
"Update" is whether `UpdateFunctionConfiguration` applies the member (run).

| Setting | Create | Update | Target | Note |
|---|---|---|---|---|
| Runtime, Handler | F | F | F | A missing interpreter is a log line at create and `Runtime.LaunchError` at invoke. |
| Code (`ZipFile`, `S3Bucket`/`S3Key`, `_local_`) | F | F (UpdateFunctionCode) | F | `ImageUri` refused by name. |
| Timeout | F | F | F | Enforced on the caller's side only (LAM-10). Default 3 (`model`: "The default is 3 seconds"). *Corrected 2026-10-11: the range trait is 1 to 5400 and the same text says "the maximum allowed value is 900 seconds. For functions using Lambda Managed Instances, asynchronous invocations and event source mapping invocations … 5,400"; doze-aws has no managed instances, so a standard function's limit is 900 (`model` text, `doc` quotas page). The code enforces the trait and accepts 901 to 5400 (run; LAM-30).* |
| MemorySize | C | C | C | Reaches the process as an environment variable and the REPORT line. ~~Default 512, AWS 128 (LAM-9).~~ *Corrected 2026-10-11: LAM-9 is the SQS record, not the memory default, which had no finding and is now LAM-73 (`model`): "The default value is 128 MB. The value can be any multiple of 1 MB"; the code's default is 512 (run). Range: trait 128 to 32 768; the quotas page (`doc`) says "128 MB to 10,240 MB"; the 32 768 is the Managed Instances ceiling (LAM-30).* |
| Environment.Variables | F | F | F | No reserved-key or size check (LAM-14). *Corrected 2026-10-11: the model's own name pattern `^[a-zA-Z]([a-zA-Z0-9_])+$` is not enforced either (`_HANDLER`, `1abc` accepted; run): the walk reads no map keys (LAM-43). `doc` (configuration-envvars): 21 reserved keys that "cannot be set in your function configuration", 4 KB in aggregate, keys "start with a letter and are at least two characters".* |
| Layers | F | F | F | Checked to exist; on search paths, not `/opt`. *Added: at most 5 per function (`doc`, quotas page); not in the model and not enforced (recording queue).* |
| DeadLetterConfig | F | set only | F | Cannot be cleared (LAM-2). Standard SQS queue or standard SNS topic only (`doc`: "FIFO queues and Amazon SNS FIFO topics are not supported"); function-level, and "Function versions use the same dead-letter queue settings as the unpublished version" (`doc`). |
| LoggingConfig (`LogFormat`, `ApplicationLogLevel`) | F | dropped | F | ~~`LogGroup` and `SystemLogLevel`: not established.~~ *Corrected 2026-10-11 (`model`): `LogGroup` "The name of the Amazon CloudWatch log group the function sends logs to … enter an existing log group or enter a new log group name", `SystemLogLevel` DEBUG, INFO, WARN. The record is stored verbatim and round-trips on create; the log group is ignored (logs always go to `/aws/lambda/<name>`; LAM-66) and `SystemLogLevel` has no effect (C).* |
| Description | stored | dropped | F | LAM-2. *Added: not part of the version fingerprint, so a description-only change publishes nothing (run; LAM-39).* |
| Role | C | dropped | C | No execution-role enforcement. |
| TracingConfig, EphemeralStorage, KMSKeyArn, VpcConfig, FileSystemConfigs, SnapStart | C | dropped | C | LAM-2. The console must say cosmetic wherever it shows one. *Corrected 2026-10-11: `Architectures` was in this list; it is not a member of `UpdateFunctionConfiguration` in the model, it is a member of `CreateFunction` and `UpdateFunctionCode`, where it is dropped on update. `EphemeralStorage.Size`: trait 512 to 32 768, text "any whole number between 512 and 10,240 MB"; the table says 10 240 (LAM-44).* |
| Architectures | C | dropped (UpdateFunctionCode) | C | Length 1 to 1, `x86_64` or `arm64` (`model`). |
| Publish | ignored | ignored | F | LAM-7. |
| Tags (on create) | F | n/a | F | |
| `Command` | extension | extension | extension | Stored, never read back. |
| PackageType `Image`, ImageConfig, CodeSigningConfigArn, DurableConfig, CapacityProviderConfig, TenancyConfig | validated by the model table | | S or C | ~~What each answers today is not established beyond `ImageUri`.~~ *Corrected 2026-10-11: `ImageUri` is refused by name; `CodeSigningConfigArn` is dropped on create (LAM-60); the others are accepted and dropped. Target: `PackageType: Image`, `CapacityProviderConfig`, `DurableConfig`, `TenancyConfig` are S (501, "not built"); `ImageConfig` is accepted only with `Image` and so is S; `CodeSigningConfigArn` is C.* |
| `Code.S3ObjectVersion`, `Code.S3ObjectStorageMode`, `SourceKMSKeyArn` | dropped | dropped | C | Model members of `FunctionCode`; `REFERENCE` ("Lambda references the deployment package from the specified Amazon S3 bucket") is accepted and the package is copied (a listed difference). |

Event source mapping settings. `createMapping` decodes `FunctionName`,
`EventSourceArn`, `BatchSize` and `Enabled`; everything else passes model
validation and is then discarded (run).

| Setting | Now | Target | Note |
|---|---|---|---|
| EventSourceArn: SQS, DynamoDB stream, Kinesis | F | F | Anything else refused by name. |
| FunctionName | F for a bare name | F | A qualified name or alias ARN resolves to the wrong function name (LAM-8). |
| BatchSize | F | F | ~~F up to 10 on SQS; above 10 delivers 1 (LAM-6).~~ *Corrected 2026-10-11: LAM-6 is closed (stage 1). The poller gathers a batch of up to `BatchSize` over receives of at most 10 and invokes once (`pollSQS`, read; the batch-20 integration test). What it does not do: default 100 for a stream (it uses 10; run), cap a FIFO source at 10, require `MaximumBatchingWindowInSeconds` of at least 1 above 10, cut a batch at 6 MB (LAM-53, LAM-67).* |
| Enabled | F | F | `Enabled` false creates `Disabled` (run). |
| FunctionResponseTypes (`ReportBatchItemFailures`) | dropped | F | LAM-3. Semantics `doc` (services-sqs-errorhandling): an empty or null `batchItemFailures` is a complete success; invalid JSON, an empty or null `itemIdentifier`, a bad key name or an id not in the batch is a complete failure; a thrown exception is a complete failure. |
| FilterCriteria | dropped | F | LAM-3. `doc` (invocation-eventfiltering): up to 5 filters, ORed; keys `body` (SQS), `dynamodb`, `data` (Kinesis); an unmatched SQS message is deleted from the queue and a stream iterator advances past an unmatched record; an empty `{}` removes the criteria. |
| MaximumBatchingWindowInSeconds | dropped | F or C | LAM-3. **F** (decided 2026-10-11): 0 to 300; above 10 messages it is required to be at least 1 (`model`); the poller gathers until the window, the batch size or 6 MB (`doc`, with-sqs). |
| ScalingConfig.MaximumConcurrency | dropped | C | 2 to 1000 (`model`); SQS only. Round trip. |
| StartingPosition, StartingPositionTimestamp | dropped; always `TRIM_HORIZON` | F | LAM-4. "Required for Amazon Kinesis and Amazon DynamoDB Stream event sources" (`model`; LAM-53); `AT_TIMESTAMP` only for Kinesis; "cannot be in the future". |
| BisectBatchOnFunctionError, MaximumRetryAttempts, MaximumRecordAgeInSeconds, ParallelizationFactor, TumblingWindowInSeconds, DestinationConfig.OnFailure | dropped | F or C, each decided | LAM-4. |
| Kafka, MQ, DocumentDB members | validated, dropped | S | Sources not served. *Corrected 2026-10-11: a Kafka source ARN is refused as `InvalidParameterValueException: doze-aws event source mappings support SQS queues, DynamoDB streams and Kinesis streams` (run). The input is valid; protocol step 1 says a declined operation is 501 `NotImplemented` with `X-Doze-Not-Built`, never a validation error (LAM-53).* |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `lambda/`: 52 tests in ~~17~~ 20 files (SDK v2 and v1, real interpreters, logs end to end, versions, URLs and URL IAM, policy and layers, router, tree hash, endpoint injection, declared config). `internal/lambdaruntime/`: 22 tests in 7 files (pool, runtime, shims, output attribution, logs). *Corrected 2026-10-11: 20 files (counted); five routed operations have no SDK-spelled call in any test (section 7).* |
| Parity | `rejection_parity_test.go` (2 tests over `testdata/cases_lambda.json`, 621 cases); the ledger says 612 of 621 model constraints enforced on 47 routed operations, `knownGaps` empty, nine not expressible on the wire. *Corrected 2026-10-11: those figures are for the model the table was generated from. The model on disk now has 842 constrained inputs on 87 operations (`dzaudit summary lambda`), the table covers 47 of the 52 routed operations, and it misses every map key (LAM-43, LAM-44). "knownGaps empty" is true of a snapshot.* |
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
| LAM-1 | P0 | *2026-10-20: the SQS leftovers package wrote the test (`TestADeletedFunctionsMappingDoesNotReadItsQueue`, skipped: memory) and the recording scenario `test_lambda::test_a_deleted_functions_mapping`; sqs.md SQS-61. Run again: the poller of a deleted function still receives the message and holds it.* Deleting a function leaves its event source mappings stored, `Enabled`, and polling. The poller receives each batch, finds no function, and drops it undeleted; the message's receive count climbs and it lands in the dead-letter queue. Reproduced: one message, `maxReceiveCount` 3, in the DLQ nine seconds after the delete. | `lambda/functions.go` `deleteFunction`; `lambda/store.go` `DeleteFunction`; `lambda/extras.go` `pollSQS` (the `continue` after `GetFunction`, about 496), same in `pollDDBStream` and `pollKinesis` | run; *run 2026-10-11 on the stage-1 binary: one message to a queue with `maxReceiveCount` 3, mapping on the deleted function, in the DLQ within nine seconds; the mapping still `Enabled`* | *Corrected 2026-10-11: was "memory: what AWS does with a deleted function's mappings"; `model` for the mapping surviving: DeleteFunction, "To delete Lambda event source mappings that invoke a function, use DeleteEventSourceMapping". So the mapping staying is as AWS and section 5's "DeleteFunction removes … the mappings' pollers" is wrong about the mapping; the defect is the poller receiving for a function that is gone. What AWS does with the messages is `memory` (recording queue).* Direction: wrong. | SDK test: mapping, delete the function, send, assert the message is still visible with receive count 0 and the poller is gone |
| LAM-2 | P0 | `UpdateFunctionConfiguration` answers 200 and silently drops `Description`, `Role`, `TracingConfig`, `EphemeralStorage`, `KMSKeyArn`, `LoggingConfig`, `VpcConfig`, `FileSystemConfigs`, `SnapStart` and `Architectures`. An empty `DeadLetterConfig.TargetArn` does not clear the target. A deploy tool reads back the old value and plans the same change forever. | `lambda/functions.go` `updateConfiguration` | run | model (the members exist on the input shape; *run 2026-10-11: `Description`, `TracingConfig`, `EphemeralStorage`, `LoggingConfig` and `Role` read back unchanged after an update that sent them*). *Corrected 2026-10-11: `Architectures` is not a member of `UpdateFunctionConfiguration` (it belongs to `CreateFunction` and `UpdateFunctionCode`, where update drops it); `ImageConfig`, `CapacityProviderConfig` and `DurableConfig` are also dropped.* Direction: wrong (silent no-op). | SDK test: update each member, read it back; clear the DLQ |
| LAM-3 | P0 | `CreateEventSourceMapping` and `UpdateEventSourceMapping` drop `FunctionResponseTypes`, `FilterCriteria`, `MaximumBatchingWindowInSeconds` and `ScalingConfig`. Two change what an app sees: a filtered-out message is delivered anyway (run: pattern `{"body":["keep"]}`, body `drop`, invoked and deleted), and a function that returns `batchItemFailures` has its whole batch deleted, so the failed items are lost. | `lambda/extras.go` `createMapping`, `updateMapping`, `mappingView`, `pollSQS`; `lambda/store.go` `eventSourceMapping` | run (settings dropped, filter ignored); read (partial batch) | doc: `invocation-eventfiltering`, `services-sqs-errorhandling` (read); `model`: the members. *Corrected 2026-10-11: `UpdateEventSourceMapping` also takes `FunctionName` ("You can change the function that Lambda invokes") and ignores it.* Direction: wrong. | SDK test per setting round trip; a filter test; a handler returning one `itemIdentifier`, assert that message alone returns |
| LAM-4 | P0 | Stream mappings always open at `TRIM_HORIZON` and keep the iterator in memory. `StartingPosition` is ignored, and every restart, `UpdateEventSourceMapping` or re-enable replays the stream from the start. A failed batch is not retried: the iterator has already advanced. `BisectBatchOnFunctionError`, `MaximumRetryAttempts`, `MaximumRecordAgeInSeconds`, `ParallelizationFactor` and `DestinationConfig` are dropped. | `lambda/extras.go` `pollDDBStream`, `pollKinesis` | read | doc: `with-kinesis` (read): "Lambda processes records in each shard in order. It stops processing additional records in a shard if your function returns an error"; `model`: `StartingPosition` "Required for Amazon Kinesis and Amazon DynamoDB Stream event sources". Direction: wrong. | Restart test: write three records, consume, restart, assert no redelivery; `LATEST` test; a throwing handler sees the same batch again |
| LAM-5 | P0 | **Closed 2026-10-20 by the SQS leftovers package** (`TestATriggerWhoseQueueHidesMessagesForLessThanTheFunctionRunsIsRefused`, red first: 202; `lambda/extras.go` `createMapping` and `updateMapping`, `peercall.SQSVisibilityTimeout`; sqs.md SQS-60; recording scenario `test_lambda::test_a_trigger_whose_queue_hides_for_less_than_the_function_runs`). An SQS trigger is created when the queue's visibility timeout (2 s) is shorter than the function's timeout (30 s). | `lambda/extras.go` `createMapping`, about 246 to 293; `grep -i visibility lambda/` finds one comment | run | *Corrected 2026-10-11: was "doc (the refusal and its wording are memory)", and the SQS plan's note that the Lambda model does not say it. The model indeed does not (CreateEventSourceMapping and UpdateEventSourceMapping read, text and error shapes); `doc` does, in "Creating and configuring an Amazon SQS event source mapping": "Your function's timeout must be less than or equal to the queue's visibility timeout. Lambda validates this requirement when you create or update an event source mapping and returns an error if the function timeout exceeds the queue's visibility timeout." **The refusal is the timeout against the visibility timeout, on create and on update of the mapping; six times is a recommendation** ("at least six times the configuration timeout", plus `MaximumBatchingWindowInSeconds`). The code is `memory` (the model lists `InvalidParameterValueException`, 400, as the one fitting client error) and the message is `memory`: test the refusal and not its text.* Direction: more forgiving. | Rejection test: timeout 30, visibility 2, create **and** update refused; timeout 30, visibility 30 accepted. The message assertion stays skipped pending the recording |
| LAM-6 | P0 | A trigger with `BatchSize` above 10 delivers one message per invocation. Reproduced: 15 messages, batch 20, fifteen invocations of 1; batch 10 gave 10 and 5. The poller asks SQS for `BatchSize` messages and SQS turns any request above 10 into 1. **When SQS-5 is fixed in stage 1, this poller starts failing on every receive instead**, so the cap belongs in stage 1. | `lambda/extras.go` `pollSQS`, about 468; `sqs/receive.go`, about 17 | run | doc: `BatchSize` up to 10 000 for standard queues with a batching window. *Closed in stage 1 (see "Moved between stages"); re-checked 2026-10-11: `pollSQS` gathers over receives of at most 10. The residual rules are LAM-53 and LAM-67.* `model` and `doc` (services-sqs-configure): "For a standard queue, this can be up to 10,000 records. For a FIFO queue, the maximum is 10. For a batch size over 10, you must also set the batch window to at least 1 second." | SDK test: batch 20, 15 waiting messages, assert one invocation of 15 (or of 10 and 5 if the window is declared cosmetic) |
| LAM-7 | P0 | `Publish: true` on `CreateFunction` and `UpdateFunctionCode` is ignored: the answer is `Version: "$LATEST"` and no version exists. An alias or a mapping then pointed at version 1 fails. | `lambda/functions.go` `createFunctionReq` (no `Publish` member), `updateCode` | run | model: CreateFunction ("Use the Publish parameter to create version 1 of your function from its initial configuration"), UpdateFunctionCode `Publish` ("same effect as calling PublishVersion separately"); *run 2026-10-11: confirmed on update, `Version` stays `$LATEST`*. Direction: wrong. | SDK test: create with `Publish`, expect `Version: "1"` and two entries from `ListVersionsByFunction` |
| LAM-8 | P0 | A mapping whose `FunctionName` is qualified resolves the wrong name. `createMapping` takes the text after the last colon, so `fn:live` and `arn:…:function:fn:live` both look up a function called `live`. SAM's `AutoPublishAlias` with an SQS event produces exactly that. What the CloudFormation path sends is not established. | `lambda/extras.go` `createMapping`, about 260; compare `functionNameFromARN` in the same file, which is right | read | model (the `FunctionName` pattern allows a qualifier); *run 2026-10-11: `fn:prod` answers `Function not found: prod`*. Direction: wrong. | SDK test: mapping on `fn:live`, assert it is created and the alias's version runs |
| LAM-9 | P0 | The SQS event a function receives has six keys per record. `attributes` (`ApproximateReceiveCount`, `SentTimestamp`, `MessageGroupId`…), `messageAttributes` and `md5OfBody` are missing, so a handler that reads them throws here and not on AWS. Reproduced with a message attribute, which did not arrive. `awsRegion` is the literal `us-east-1` (foundation A8). | `lambda/extras.go` `pollSQS`, about 485 to 493; `internal/peercall` `SQSReceive` (asks only for `AWSTraceHeader`) | run | doc (`with-sqs`, read): `attributes` {`ApproximateReceiveCount`, `SentTimestamp`, `SenderId`, `ApproximateFirstReceiveTimestamp`; FIFO adds `SequenceNumber`, `MessageGroupId`, `MessageDeduplicationId`}, `messageAttributes` {name: {`stringValue`, `stringListValues`, `binaryListValues`, `dataType`}}, `md5OfBody`. *Corrected 2026-10-11: the poller also has to ask SQS for them: `peercall.SQSReceive` requests only `MessageSystemAttributeNames: [AWSTraceHeader]`, so the fix is in the call, not the template.* Direction: wrong shape. *Sections 2, 5 and 6 cited "LAM-9" for the default `MemorySize`; that is a different defect and is now LAM-73.* | Runtime test asserting the record's key set and one value of each |
| LAM-10 | P0 | A timed-out invocation is answered, and nothing stops the handler. Reproduced: timeout 1 s, handler 4 s; the call returned at 2.0 s and the handler logged its last line two seconds later. Writes made after the timeout happen here and not on AWS, and a queue trigger then redelivers the batch on top of them. Also: the payload is `{"errorMessage":"Task timed out"}` with no request id or duration, and REPORT `Duration` is timeout plus the one-second grace (2001 ms). | `internal/lambdaruntime/runtime.go` `InvokeInput`, about 289 to 296, `withTiming` | run; *run 2026-10-11: timeout 1 s, handler 3 s: answered at 2.0 s, `Duration: 2001.93 ms`, payload `{"errorMessage":"Task timed out"}`, `FunctionError: Unhandled`* | doc: "Lambda terminates the invocation"; the payload wording is `memory` | Runtime test: a handler that writes a file after its timeout, assert the file is absent |
| LAM-11 | P0 | Deleting a layer version removes it from functions that use it. `layerDirs` resolves each layer through the store on every cold start and skips one that is gone, so the function's next process starts without the layer. The console's confirm says "Functions already configured with it keep running". | `lambda/layers.go` `layerDirs`, `deleteLayerVersion` | read | *Corrected 2026-10-11: was "doc"; `model`: DeleteLayerVersion, "Deleted versions can no longer be viewed or added to functions. To avoid breaking functions, a copy of the version remains in Lambda until no functions refer to it."* Direction: wrong. | SDK test: function with a layer, delete the version, force a cold start, invoke and import from the layer |
| LAM-12 | P1 | 36 operations in the model have no route. Each answers `404 ResourceNotFoundException` with `unknown path "/…"`, which an SDK reports as "the function was not found". Checked on seven of them. | `lambda/router.go` `notFound`, `routeSpecs`; `docs/not-built.md` (one line for four families) | run | model; *run 2026-10-11: capacity providers, durable executions, provisioned concurrency, runtime management, recursion config and resource policy all answer 404 `unknown path`; the target is 501 `NotImplemented` + `X-Doze-Not-Built` (protocol D5)*. Direction: wrong code. | Router test: every operation in `ops_lambda.json` is routed or answers the agreed stub code; `not-built.md` reconciles |
| LAM-13 | P1 | No list operation pages. `ListFunctions`, `ListLayerVersions` and `ListEventSourceMappings` with `MaxItems=1` returned everything and no `NextMarker`. `ListVersionsByFunction`, `ListAliases`, `ListLayers`, `ListFunctionEventInvokeConfigs` and `ListFunctionUrlConfigs` read no paging members either. | `lambda/functions.go` `listFunctions`; `lambda/layers.go`; `lambda/extras.go` `listMappings`; `lambda/versions.go`; `lambda/aliases.go` | run (three), read (five) | model. *Corrected 2026-10-11: the model gives the page sizes: ListFunctions, ListVersionsByFunction "up to 50 … per call" whatever `MaxItems` (1 to 10 000) says; ListEventSourceMappings 100; ListFunctionUrlConfigs 50; ListLayers, ListLayerVersions and ListFunctionEventInvokeConfigs `MaxItems` 1 to 50; ListAliases 1 to 10 000. `Marker`/`NextMarker` are opaque; no lifetime is stated for Lambda's (the "24 hours" is on the durable-execution lists).* *run 2026-10-11: `ListFunctions MaxItems=1` over two functions: both, no `NextMarker`.* Direction: wrong. | SDK paginator test per list with `MaxItems: 1` |
| LAM-14 | P1 | Environment variables are not checked. `AWS_REGION` and `_HANDLER` were accepted, and because the function's own variables are applied last they override the runtime's. There is no 4 KB total check. | `lambda/functions.go` `createFunction`, `updateConfiguration`; `internal/lambdaruntime/env.go` | run (keys), read (size) | doc (`configuration-envvars`, read): reserved keys "cannot be set in your function configuration" (`_HANDLER`, `_X_AMZN_TRACE_ID`, `AWS_DEFAULT_REGION`, `AWS_REGION`, `AWS_EXECUTION_ENV`, `AWS_LAMBDA_FUNCTION_NAME`, `AWS_LAMBDA_FUNCTION_MEMORY_SIZE`, `AWS_LAMBDA_FUNCTION_VERSION`, `AWS_LAMBDA_INITIALIZATION_TYPE`, `AWS_LAMBDA_LOG_GROUP_NAME`, `AWS_LAMBDA_LOG_STREAM_NAME`, `AWS_ACCESS_KEY`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_LAMBDA_RUNTIME_API`, `LAMBDA_TASK_ROOT`, `LAMBDA_RUNTIME_DIR`, `AWS_LAMBDA_MAX_CONCURRENCY`, `AWS_LAMBDA_METADATA_API`, `AWS_LAMBDA_METADATA_TOKEN`), "4 KB, for all environment variables … in aggregate"; `model`: name pattern. *Corrected 2026-10-11: `docs/SUPPORT.md` says "a function may override AWS_REGION"; the page says it cannot (LAM-71).* Direction: more forgiving. | Rejection test for a reserved key and for 4097 bytes |
| LAM-15 | P1 | A second mapping for the same queue and function is created, and both poll. | `lambda/extras.go` `createMapping` | run | model lists `ResourceConflictException` on CreateEventSourceMapping ("The resource already exists, or another operation is in progress"); that a duplicate source and function is the case is `memory`. *Run 2026-10-11: a second mapping created.* | Skipped SDK test, pending the recording |
| LAM-16 | P1 | `DeleteEventSourceMapping` for a UUID that does not exist answers 202 with an empty mapping in state `Deleting`. | `lambda/extras.go` `deleteMapping` | run | *Corrected 2026-10-11: was "doc"; `model`: `ResourceNotFoundException` is listed on DeleteEventSourceMapping, and the text says "it enters a Deleting state and might not be completely deleted for several seconds"; `ResourceInUseException` for a delete while `Updating`.* *Run 2026-10-11: any unknown UUID answers 202 and a mapping with `BatchSize` 0, an empty `EventSourceArn` and `FunctionArn` `…:function:`; an existing mapping's delete returns the same empty shape (LAM-54).* Direction: more forgiving, wrong shape. | SDK test expecting the error |
| LAM-17 | P1 | `RevisionId` is honoured by `PublishVersion` only. `UpdateFunctionConfiguration` with a stale one answered 200. `UpdateFunctionCode`, `UpdateAlias`, `AddPermission` and `RemovePermission` are not established. | `lambda/functions.go` `updateConfiguration`, `updateCode`; `lambda/aliases.go`; `lambda/policy.go` | run (one), read | *Corrected 2026-10-11: was "doc: `PreconditionFailedException` on each", with four operations "not established". `model`: `PreconditionFailedException` (412) is on UpdateFunctionConfiguration, UpdateFunctionCode, PublishVersion, UpdateAlias, AddPermission, RemovePermission, AddLayerVersionPermission, RemoveLayerVersionPermission (and the unrouted Put/DeleteResourcePolicy); its text: "For AddPermission and RemovePermission API operations: Call GetPolicy to retrieve the latest RevisionId … For all other API operations: Call GetFunction or GetAlias". So the policy's revision is not the function's: the code returns the function's from GetPolicy and never changes it on AddPermission (run); layer-policy ids are the constant `"1"`; an alias's id is the function's, so it changes when the function does. *Run 2026-10-11: a stale id answered 200 on UpdateFunctionCode and on AddPermission.* Direction: more forgiving. | SDK test per operation with a stale id, expect 412 |
| LAM-18 | P1 | An invoke payload above 6 MB is cut at 6 MB and then refused as `InvalidRequestContentException: … not valid JSON`. | `lambda/invoke.go` `invoke` (the `LimitReader`) | run | *Corrected 2026-10-11: was "doc: `RequestTooLargeException`, 413"; `model`: Invoke, "For synchronous invocations, the maximum payload size is 6 MB. For asynchronous invocations, the maximum payload size is 1 MB", and `RequestTooLargeException` (413) is on Invoke; InvokeAsync "The payload limit is 256KB". *Run 2026-10-11: 7 MB sync answers `InvalidRequestContentException`; a 2 MB `Event` answers 202 (the cap is 6 MB for both).* The async limit is this row, not "a separate row".* Direction: wrong code (sync), more forgiving (async). | Rejection test at 6 MB plus one byte, for sync; the async limit is a separate row |
| LAM-19 | P1 | Tags have no limit and no existence check: 51 tags were accepted, and `UntagResource` on a function that does not exist answered 204. | `lambda/extras.go` `tagResource`, `untagResource` | run | *Corrected 2026-10-11: was "doc: 50 tags per resource; the 404 is memory". The limit of 50 is **not** in the model (`TagKey` 1 to 128, `TagValue` 0 to 256, no count), nor on the quotas page or the tags page (both read): `memory`, and SQS-16 found the same figure was advice there; do not enforce it on `memory`. The 404 is `model`: `ResourceNotFoundException` is listed on TagResource, UntagResource and ListTags.* *Run 2026-10-11: 51 tags accepted; untag of a missing function 204; a 129-character key and a key with `#` accepted (LAM-43).* Direction: more forgiving. | SDK tests for both |
| LAM-20 | P1 | `CreateFunctionUrlConfig` twice answers 201 twice. | `lambda/urls.go` `putFunctionURL` | run | model lists `ResourceConflictException` on CreateFunctionUrlConfig; the case is `memory`. *Run 2026-10-11: second create 201 with the same address.* | SDK test |
| LAM-21 | P1 | `LastModified` is the time of the read, not of the last change: two reads two seconds apart differed. `function.LastMod` is written in three places and read nowhere. The format is `2026-10-10T11:24:49Z`. | `lambda/functions.go` `configView`, about 469 | run | *Corrected 2026-10-11: was "memory: AWS's format is …T11:24:49.000+0000"; `model`: "ISO-8601 format (YYYY-MM-DDThh:mm:ss.sTZD)" and, on a layer version, "For example, 2018-11-27T15:10:45.123+0000". The code answers `2026-10-11T12:04:07Z` (no fraction). The read-time defect stands (run 2026-10-11).* Direction: wrong. | Handler test: two reads agree; the format test waits for the recording |
| LAM-22 | P1 | `CodeSize` is absent from every function configuration. | `lambda/functions.go` `configView` | run | model (`CodeSize` is a member of `FunctionConfiguration`: "The size of the function's deployment package, in bytes"). *Run 2026-10-11: absent; Terraform's `source_code_size` reads 0 in the state on record.* | Handler test: a zip of known size |
| LAM-23 | P1 | Two sizes are a directory entry's, not the content's. `GetAccountSettings` reported `TotalCodeSize: 288` for three functions (96 bytes each on this file system), and a layer published from a `_local_` directory reported `CodeSize: 96`. | `lambda/layers.go` `accountSettings` (about 526), `publishLayerVersion` (`info.Size()`, about 313) | run | *Corrected 2026-10-11: was "n/a"; `model`: AccountUsage.TotalCodeSize "The amount of storage space, in bytes, that's being used by deployment packages and layer archives" (so layers count; LAM-61), `LayerVersionContentOutput.CodeSize` "The size of the layer archive in bytes".* Direction: wrong. | Handler tests with a directory of known content |
| LAM-24 | P1 | A layer's version number is reused: publish 1, 2, 3, delete 3, publish again, get 3. | `lambda/layers.go` `nextLayerVersion` | run | memory: version numbers are not reused. *Corrected 2026-10-11: for **function** versions the rule is `doc` (LAM-41); for layers the model is silent and this stays `memory`.* | Skipped SDK test, pending the recording |
| LAM-25 | P3 | The ledger row for function concurrency says "C, stored; no throttling locally". Zero answers `429 TooManyRequestsException` (run) and N is the process ceiling; a call past the ceiling waits in a queue of 64 where AWS throttles. The same document's "Verified against" says the throttle works. | `docs/SUPPORT.md`; `lambda/invoke.go`, about 34 and 312; `internal/lambdaruntime/pool.go` `acquire` | run, read | *Corrected 2026-10-11: was "doc"; `model`, Invoke: "Lambda returns TooManyRequestsException if running the function would cause you to exceed a concurrency limit at either the account level (ConcurrentInvocationLimitExceeded) or function level (ReservedFunctionConcurrentInvocationLimitExceeded)". The shape of that error is LAM-63.* Direction: more forgiving (a wait where AWS throttles). | Ledger test row; a decision on queue or throttle |
| LAM-26 | P3 | Layer permissions are tiered F and are stored without being evaluated. | `docs/SUPPORT.md` | read | n/a | Ledger row to C |
| LAM-27 | P3 | Async retries run back to back. `MaximumEventAgeInSeconds` is stored and unused, which the ledger says. | `lambda/invoke.go` `invokeAsync` | read | *Corrected 2026-10-11: was "doc: one minute, then two"; now named: `invocation-async-error-handling`: "a one-minute wait between the first two attempts, and two minutes between the second and third"; throttles and 5xx are retried "for up to 6 hours … from 1 second … to a maximum of 5 minutes"; `model`: "retains events in a queue for up to six hours". Decision in the review (L7).* Direction: wrong (timing). | A line in "Differences from AWS" |
| LAM-28 | P2 | The process badge says "Warm · sleeps in …" for a function that has no process: after `Runtime.LaunchError` and after the process exits, `doze-runtime` reports `Runners: 1, Warm: true`. The pool counts runners it created, not processes that started. | `internal/lambdaruntime/pool.go` `acquire` (about 112), `Size`; `lambda/functions.go` `dozeRuntime` | run; *run 2026-10-11: after `Runtime.LaunchError` (`dotnet8`, no `dotnet`), `doze-runtime` answers `Runners: 1, Warm: true`* | n/a | Pool test: a spec that cannot launch reports zero live processes |
| LAM-29 | P2 | The Configuration tab's "Code size" is always `0 B` (LAM-22), and Home's "Code stored" is the figure from LAM-23. | `internal/console/templates/lambda.html` (`lambda_config`, `lambda_home`); `client_lambda.go` | run; *run 2026-10-11 for "Code size 0 B"*; read for the rest | n/a. *Corrected 2026-10-11: "Home's 'Code stored' is the figure from LAM-23" is unreachable: the Account strip never renders at all (LAM-68), so neither fact is on the page.* | Browser test reads a non-zero size |
| LAM-30 | P2 | The forms' number inputs allow timeout 1 to 900 and memory 128 to 10 240; the service's model-derived table allows 1 to 5400 and 128 to 32 768. Which the console should teach is a decision: the model's range, or the limit AWS documents for a standard function. | `lambda.html` (`lambda_create`, `lambda_config`); `lambda/validate.go` (about 222, 234); `lambda/functions.go` constants | read | *Corrected 2026-10-11: was "model (5400, 32 768); doc (900, 10 240)"; `model` states both: the trait 1 to 5400 and the text "the maximum allowed value is 900 seconds. For functions using Lambda Managed Instances, asynchronous invocations and event source mapping invocations … 5,400"; memory has the trait 128 to 32 768 and no text for 10 240, which is `doc` (`gettingstarted-limits`: "128 MB to 10,240 MB"). `EphemeralStorage` is the same pair (text 10 240, trait 32 768; quotas page 512 to 10 240).* **Proposed decision:** teach and enforce a standard function's 900 and 10 240 as hand-derived rules with their `doc` source, beside the model-derived 5400 and 32 768 (which keep their messages); doze-aws has no managed instances. Direction: more forgiving than AWS for a standard function. | Settings-table test once B4 exists |
| LAM-31 | P2 | Home says of layers "attach by ARN in a function's configuration". The Configuration tab has no layers field and `UpdateConfig` never sends `Layers`. | `lambda.html`, about 30; `client_lambda.go` `UpdateConfig` | read | n/a | Browser test: attach a layer from the tab |
| LAM-32 | P2 | The console cannot do what the API can for: versions and aliases, reserved concurrency, the function's resource policy, the dead-letter target and destinations (shown, reset, never set), a mapping's enable switch and settings, Kinesis and DynamoDB sources, layers on a function, the description. Section 6 says which the design adds. | `lambda.html`, `handlers_lambda.go`, `client_lambda.go` | read | n/a | One browser test per item the design takes |
| LAM-33 | P2 | A function URL made from the console answers 403 under `--iam-mode enforce` until a statement grants `lambda:InvokeFunctionUrl` to everyone, and the console has no way to add one. AWS's console adds it. | `handlers_lambda.go` `lambdaCreateURL`; `lambda/urls.go` `serveFunctionURL` | read | doc: function URL auth, the `NONE` policy statement. *Corrected 2026-10-11: `urls-invocation` (read): "To invoke a function URL, you must have `lambda:InvokeFunctionUrl` and `lambda:InvokeFunction` permissions"; the code evaluates the first only (LAM-69).* | Enforce-mode browser test: create a URL, fetch it |
| LAM-34 | P3 | Timeout payloads, and `Runtime.LaunchError` at create time, are only in doze-aws's own log. The create form says nothing when the chosen runtime's interpreter is missing. | `lambda/invoke.go` `warnRuntime`; `handlers_lambda.go` `lambdaCreate` | read | n/a | Handler test: create with a missing interpreter, the redirect carries the warning |
| LAM-35 | P1 | `LogType: Tail` returns the last 4 KB the process printed, not the invocation's lines. Reproduced: a warm call's tail began with the previous call's START, END and REPORT. The console's timing split already works around it by reading the last REPORT. | `internal/lambdaruntime/runtime.go` `withTiming` (the ring snapshot); `lambda/invoke.go` `invoke`; `invocationTail` in `lambda/extras.go` already cuts at START for the wire | run | *Corrected 2026-10-11: was "doc: the API reference"; `model`: LogResult "The last 4 KB of the execution log, which is base64-encoded", LogType "Applies to synchronously invoked functions only". Run 2026-10-11: confirmed, a tail of two invocations' lines.* Direction: wrong. | SDK test: invoke twice, the second tail does not contain the first request id |

### Found by the pre-stage review (2026-10-11)

Every row was found by reading the model's documentation text, the AWS pages
named in the review, the code, or by rendering a page, and the `run` rows were
reproduced against a binary built from `7cae441` driven over HTTP and the
AWS CLI. The direction is named, because "stricter than AWS" breaks a correct
caller exactly as "more forgiving" breaks a deploy. Each is red first
(protocol step 2); the last column is the test.

| Id | Sev | What | Where | Evidence | Direction | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| LAM-36 | P0 | **No SQS trigger delivers under a non-default account.** The poller, the SQS delete, the DLQ and destination sends build the queue URL with `awsident.AccountID`, the *default*. The comment on them says SQS "resolves a queue from the LAST path segment alone … so the account is discarded before it is ever compared". Since SQS-23 (stage 1) SQS compares the owner. With `--account-id 111122223333 --region eu-west-1` a mapping is created, a message sent, and `sqs: ReceiveMessage -> NonExistentQueue` is logged every second; the function is never called, and a failed async event never reaches its DLQ (run: `lambda: deliver to sqs dlq1: … QueueDoesNotExist`). The same helpers serve S3 and EventBridge deliveries to a queue. *Checked 2026-10-12 by the integrator: an EventBridge rule to a queue fails under a non-default account (`QueueDoesNotExist`, reproduced) and works under the default; SNS to SQS delivers under both, so SNS is not in this list. Same cause and same fix as SQS-30, which is where it is closed (2026-10-12: the helpers now deliver under another account; the Lambda poller itself was not run under one, so LAM-36 keeps its own test to write: a mapping, a message, one invocation, under `--account-id 111122223333`).* | `internal/peercall/peercall.go` `SQSReceive`, `SQSSend`, `SQSDelete`, `SQSSendDetailed` and the note above them; `lambda/extras.go` `pollSQS`, `deliverToArn` | run 2026-10-11 | stricter (a hard-coded identity) | model: SQS GetQueueUrl `QueueOwnerAWSAccountId`; `plan/services/sqs.md` SQS-23 | Package test under `--account-id 111122223333`: queue, mapping, message, assert one invocation. Then the same through SNS and S3 (owed, section 9) |
| LAM-37 | P1 | `UpdateFunctionCode` with `DryRun: true` changes the function: the code is replaced and `RevisionId` moves. The model: "Set to true to validate the request parameters and access permissions without modifying the function code." | `lambda/functions.go` `updateCode` (reads the code members only) | run 2026-10-11 | wrong (performs) | model: UpdateFunctionCode.DryRun | SDK test: dry-run update, read `CodeSha256` and `RevisionId` unchanged, an invalid zip still refused |
| LAM-38 | P1 | A published version an alias points at is deleted without complaint. The model: "You can't delete a version that an alias references." Also deleting a version leaves its pool and its directory only when no alias held it, which the code never asks. | `lambda/functions.go` `deleteFunction` (qualifier arm) | run 2026-10-11 | more forgiving | model: DeleteFunction.Qualifier; the code is `ResourceConflictException` by the operation's error list (not stated) | SDK test: alias at version 1, delete version 1 refused, delete the alias, then it succeeds |
| LAM-39 | P1 | The version fingerprint omits settings AWS snapshots. `doc` (`configuration-versions`) lists code, environment, runtime, handler, layers, memory, timeout, VPC, DLQ, role, **description**, architecture, **ephemeral storage**, package type, code storage mode, logging, **file system**, **SnapStart**, **tracing**; `fingerprint` has code, runtime, handler, env, timeout, memory, layers, role, command, logging, DLQ and architecture. A description-only change answers the version that exists. | `lambda/versions.go` `fingerprint` | run 2026-10-11 (description changed, publish answered `1` again) | wrong | doc: `configuration-versions` | SDK test per missing setting: change it, publish, expect a new version number |
| LAM-40 | P1 | `PublishVersion` ignores `CodeSha256`: "Only publish a version if the hash value matches the value that's specified." A wrong hash publishes. | `lambda/versions.go` `publishVersion` (reads `Description` and `RevisionId` only) | run 2026-10-11 | more forgiving | model: PublishVersion.CodeSha256, `PreconditionFailedException` | SDK test: wrong hash answers 412 |
| LAM-41 | P1 | Version numbers are reused. `doc`: "Lambda assigns monotonically increasing sequence numbers for versioning. Lambda never reuses version numbers, even after you delete and recreate a function." The next number is one past the highest *existing* version, so deleting the newest, or the function, hands the number out again. An alias or a mapping elsewhere that held it now runs different code. | `lambda/versions.go` `publishVersion`; `lambda/store_versions.go` | run 2026-10-11 (all versions deleted, next publish answered `1`) | wrong | doc: `configuration-versions` | SDK test: publish 1 and 2, delete 2, publish: 3; delete the function, re-create, publish: 4 |
| LAM-42 | P1 | An alias's `RoutingConfig` is never stored: it is absent from every `AliasConfiguration`. Section 2 called that C; C is accepted **and** round-tripped. A deploy tool that sent it reads back nothing and plans the change forever (LAM-2's class). The weights' keys (`^[0-9]+$`, 1 to 1024) are not validated either (LAM-43). | `lambda/aliases.go` (`createAlias`, `updateAlias`, `aliasView`) | run 2026-10-11 | wrong shape (silent drop) | model: AliasConfiguration.RoutingConfig; AdditionalVersionWeights, `Weight` 0 to 1 | SDK test: create with `{"2": 0.1}`, get and list return it; update clears it |
| LAM-43 | P1 | **Map keys are not validated.** The constraint walk reads no keys: environment variable names (`^[a-zA-Z]([a-zA-Z0-9_])+$`: `1abc` and `_HANDLER` accepted), tag keys on CreateFunction, TagResource and CreateEventSourceMapping (1 to 128, `^([\p{L}\p{Z}\p{N}_.:/=+\-@]*)$`: a 129-character key and `a#b` accepted), `AdditionalVersionWeights` keys. `dzaudit list lambda` shows 21 "map key" constraints; `validate.go` has none, and `cases_lambda.json` has no map-key case (`dzaudit cases` emits them now; the fixture predates that). `internal/modelcheck`'s path grammar has a marker for a list's elements (`[]`) and a map's values (`{}`) and none for a map's keys, so the table cannot hold the rule. The walker is shared: any other service whose model has map-key constraints is exposed in the same way, which is for that service's review. | `lambda/validate.go` (generated); `internal/modelcheck` (path grammar); `cmd/dzaudit` | run 2026-10-11 | more forgiving | model: `EnvironmentVariableName`, `TagKey` | Rejection-parity cases for each map, generated by `dzaudit cases` once the walk reads keys |
| LAM-44 | P1 | **The constraint tables are a snapshot, and four routed operations with constrained inputs have none.** Generated 2026-08/09 from 47 operations and 621 constraints (the ledger's figure); the model is 88 operations and 842 constrained inputs. Drift found by diffing `dzaudit list` against `validate.go`: `EphemeralStorage.Size` 512 to 10 240 in the table, 512 to 32 768 in the model (stricter); `SourceAccessConfigurations[].Type` 8 values, the model 15; `DeadLetterConfig.TargetArn`, `KMSKeyArn`, `Code.S3Bucket` and `FileSystemConfigs[].S3FilesConfig.DirectS3Read` patterns and enums absent. `AddLayerVersionPermission` (15 constraints), `RemoveLayerVersionPermission` (7), `GetLayerVersionPolicy` (4), `GetLayerVersionByArn` (3) are served and have no table (`GetAccountSettings` has no input): `lambda:Nope` as the action, `s 1` as the statement id and `abc` as the principal were accepted. The model-drift workflow compares routes and ops, not the table. | `lambda/validate.go`; `.github/workflows/model-drift.yml` | run 2026-10-11 | more forgiving (and one stricter) | model | Regenerate; the rejection-parity fixture then covers the five; a drift test that fails when `dzaudit list` and the table differ |
| LAM-45 | P1 | The 64-character limit for a bare function name is not enforced; a 65-character name is created. "If you specify only the function name, it is limited to 64 characters in length." (The pattern's 140 is the ARN.) Applies to every operation that takes a name. | `lambda/validate.go` (`FunctionName` length 1 to 140 on the whole reference) | run 2026-10-11 | more forgiving | model: CreateFunction.FunctionName | Rejection case: 65 characters bare, 64 accepted, a 140-character ARN with a 64-character name accepted |
| LAM-46 | P1 | A `.zip` function is created with no `Runtime` and no `Handler` (`Runtime: ""`, `Handler: ""`). "Runtime is required if the deployment package is a .zip file archive", "Handler is required if the deployment package is a .zip file archive." | `lambda/functions.go` `createFunction` | run 2026-10-11 | more forgiving | model: CreateFunction.Runtime, .Handler | Rejection case: no runtime, no handler; with `Command` set neither is required (the extension) |
| LAM-47 | P1 | `GetFunction` has no `Concurrency` (the model: "The function's reserved concurrency"; the Terraform provider reads `reserved_concurrent_executions` from it, so a function with one set reads as unset, the provider's `-1` in the state on record), answers `"Tags": null` for a function with none, and `Code.Location` is `local:///…`, not "A presigned URL … valid for 10 minutes". | `lambda/functions.go` `getFunction` | run 2026-10-11 | wrong shape | model: GetFunctionResponse | SDK test: reserved concurrency set, `GetFunction.Concurrency` carries it; no tags: member absent |
| LAM-48 | P2 | A function's `CodeSha256` is lower-case hex; a layer's is base64, and the code's own comment says base64 "is the CodeSha256 encoding AWS uses for layer and function code". The Terraform state on record shows `code_sha256` in hex beside a base64 `source_code_hash`. | `lambda/functions.go` `materializeCode` (`hex.EncodeToString`) | run 2026-10-11 | wrong | memory (the model: "The SHA256 hash of the function's deployment package"); the code's own comment | Skipped test pending `create_defaults`; the `source_code_hash` round trip with a real provider is the gate |
| LAM-49 | P1 | Reserved concurrency 0 refuses an `Event` invoke with 429 before it queues. `doc` (`invocation-async-retain-records`): "When you set reserved concurrency to zero for an asynchronously invoked function, Lambda begins sending new events to the configured dead-letter queue or the on-failure event destination, without any retries." | `lambda/invoke.go` `invoke` (the throttle check above the invocation-type switch) | read | wrong | doc: `invocation-async-retain-records` | SDK test: reserved 0, `Event` invoke answers 202, the DLQ gets the event once, no retry |
| LAM-50 | P1 | Destinations: `deliverToArn` knows SQS, SNS and Lambda; **S3** (on failure only) and **EventBridge** destinations are accepted and silently dropped, and none is validated at put time: an S3 `OnSuccess`, a FIFO queue or topic ("FIFO queues and Amazon SNS FIFO topics are not supported"), an ARN that does not exist. | `lambda/invoke.go` `deliverToArn`; `lambda/extras.go` `putEventInvokeConfig` | read | more forgiving; missing | model: PutFunctionEventInvokeConfig ("S3 buckets are supported only for on-failure destinations", "The ARN of a standard SQS queue"); doc: `invocation-async-retain-records` (object name `aws/lambda/async/<function>/YYYY/MM/DD/YYYY-MM-DDTHH.MM.SS-<uuid>`; EventBridge `source` `lambda`, `detail-type` "Lambda Function Invocation Result - Success" or "- Failure") | SDK test per type; put-time refusals; delivery to S3 and a bus |
| LAM-51 | P1 | The invocation record is the wrong shape. The code sends `requestContext{functionArn, condition}`, `requestPayload`, `responsePayload`. `doc`: `version` "1.0", `timestamp`, `requestContext{requestId, functionArn` (qualified, `:$LATEST`)`, condition, approximateInvokeCount}`, `requestPayload`, `responseContext{statusCode, executedVersion, functionError}`, `responsePayload`. A DLQ message carries the event and the attributes `RequestID` (String), `ErrorCode` (Number), `ErrorMessage` (String, first 1 KB); `SQSSend` is called with none. | `lambda/invoke.go` `routeDestination`, `invokeAsync` | read | wrong shape | doc: `invocation-async-retain-records` | Handler test: the record's key set and one value of each; the DLQ message's three attributes |
| LAM-52 | P1 | The event invoke config ignores `Qualifier` on every operation: Put, Get, Update, Delete and List read the function and report `…:$LATEST`; `LastModified` is the time of the read (LAM-21). Config on an alias bleeds into `$LATEST`. | `lambda/extras.go` `putEventInvokeConfig`, `eventInvokeView`; `lambda/store.go` | run 2026-10-11 | wrong | model: PutFunctionEventInvokeConfig "on a function, version, or alias" | SDK test: put on version 1 and on `$LATEST`, each reads back its own; list returns both |
| LAM-53 | P1 | Mapping rules the model states and the code does not apply. (a) A stream mapping's default `BatchSize` is 100 (model); the code says 10. (b) `StartingPosition` is required for Kinesis and DynamoDB streams; a Kinesis mapping with none is created, and `AT_TIMESTAMP` and a future timestamp are accepted. (c) A FIFO queue's `BatchSize` is at most 10; 20 is accepted. (d) A standard queue's `BatchSize` above 10 needs `MaximumBatchingWindowInSeconds` of at least 1; 11 with none is accepted. (e) A stream that does not exist is accepted and polled forever. (f) A source of a type doze-aws does not run (Kafka, MQ, DocumentDB) is `InvalidParameterValueException: doze-aws event source mappings support …`, which is a validation error for a valid input; protocol step 1 says 501 `NotImplemented`. | `lambda/extras.go` `createMapping` | run 2026-10-11 | more forgiving; wrong code | model (BatchSize, StartingPosition, MaximumBatchingWindowInSeconds); doc: `services-sqs-configure` | Rejection and SDK cases per sub-row; (f) a router-level test |
| LAM-54 | P1 | The mapping wire shape. A mapping answers 6 of its 33 members (about 15 apply to the three sources doze-aws serves): no `LastModified`, `EventSourceMappingArn`, `StartingPosition`, `MaximumBatchingWindowInSeconds`, `FunctionResponseTypes`, `LastProcessingResult`, `ScalingConfig`, `FilterCriteria`; `FunctionArn` is `us-east-1`/`000000000000` (A8); `StateTransitionReason` is `User action` (`model`: "Indicates whether a user or Lambda made the last change"). **DeleteEventSourceMapping returns an empty mapping** (`BatchSize` 0, `EventSourceArn` `""`, `FunctionArn` `…:function:`) for every UUID, an existing one included; the model returns the mapping. A mapping's UUID is 16 random bytes in the 8-4-4-4-12 layout with no version bits (run: `5df1a23f-a9fb-f2cc-…`). | `lambda/extras.go` `mappingView`, `deleteMapping`, `newUUID` | run 2026-10-11 | wrong shape | model: EventSourceMappingConfiguration | SDK test: every member round-trips; delete returns the full mapping in `Deleting` |
| LAM-55 | P2 | The mapping state model (L7). Create answers `Enabled` at once (AWS `Creating`; `doc` CloudTrail sample "state": "Creating"); update never answers `Updating`; delete is immediate where the model says "might not be completely deleted for several seconds"; `ResourceInUseException` (400) for update while `Creating` and delete while `Updating` has no window to exist in. **Decided in the review (L7): create and update immediate, delete modelled with a 2-second `Deleting`; the in-use refusals behind `--lambda-strict`.** | `lambda/extras.go`; `lambda/store.go` | run 2026-10-11 | more forgiving (immediate) | model: State, DeleteEventSourceMapping, ResourceInUseException | Delete: 202 `Deleting`, Get `Deleting`, then 404; strict: update during `Creating` refused |
| LAM-56 | P2 | The function state model (L7). `State` is always `Active` and `LastUpdateStatus` always `Successful`; `doc` (`functions-states`): `Pending` after create ("Lambda does not invoke a function during pending state. Any invocations or other API actions that operate on the function fail"), Update*, PublishVersion and TagResource fail while `InProgress`. **Decided: immediate, listed; the refusals behind `--lambda-strict`.** | `lambda/functions.go` `configView` | read | more forgiving (immediate) | model: State, LastUpdateStatus; doc: `functions-states` | None by default; strict: update during `InProgress` refused `ResourceConflictException` |
| LAM-57 | P1 | Deletes of something that is not there succeed. DeleteAlias (missing alias), DeleteFunctionConcurrency (missing function), DeleteFunctionUrlConfig (no URL, or no function), DeleteEventSourceMapping (LAM-16), UntagResource (LAM-19) answer 204 or 202; `ResourceNotFoundException` is on each operation's error list. `UpdateFunctionUrlConfig` on a function with no URL **creates** one (201-shaped 200). `DeleteLayerVersion` of a missing version is a 204 too, on the code's say-so ("AWS's DeleteLayerVersion is idempotent", `memory`; the model lists `ResourceNotFoundException`): recording queue. | `lambda/aliases.go`, `lambda/extras.go`, `lambda/urls.go`, `lambda/layers.go` | run 2026-10-11 (alias, URL, tags, mapping); read (the rest) | more forgiving | model: the operations' `errors` | SDK test per operation; the layer one stays skipped pending the recording |
| LAM-58 | P2 | The function URL config does not round-trip. `InvokeMode` `RESPONSE_STREAM` reads back `BUFFERED`; `CreationTime` and `LastModifiedTime` are the time of each read (two reads one second apart differed); `Qualifier` is ignored, so a URL on an alias is the function's. | `lambda/urls.go` `urlConfigView`, `putFunctionURL` | run 2026-10-11 | wrong shape | model: CreateFunctionUrlConfig, FunctionUrlConfig | SDK test: stream mode, timestamps stable, a URL per alias |
| LAM-59 | P2 | AddPermission, beyond LAM-17. `FunctionUrlAuthType` and `InvokedViaFunctionUrl` are dropped (no condition written; run: none); `Qualifier` `$LATEST` is accepted ("Lambda does not support adding policies to version $LATEST"); the policy is the function's whatever the qualifier (GetPolicy and RemovePermission ignore it); `PolicyLengthExceededException` and `PublicPolicyException` have no check (the limit is 20 KB, `doc`). The condition operator is `ArnLike` where the model text says "Lambda configures the comparison using the StringLike operator": **unresolved**, recording queue. | `lambda/policy.go` | run 2026-10-11 | wrong; more forgiving | model: AddPermission; doc: `gettingstarted-limits` | SDK tests per member; `$LATEST` refused |
| LAM-60 | P2 | Code signing. `PutFunctionCodeSigningConfig` accepts the ARN of a configuration that cannot exist (nothing creates one); the model lists `CodeSigningConfigNotFoundException` (404). `GetFunctionCodeSigningConfig` omits `CodeSigningConfigArn` when none, which is `required` in the output. `CodeSigningConfigArn` on CreateFunction is dropped. | `lambda/functions.go` | run 2026-10-11 | more forgiving; wrong shape | model | SDK test: Put of an unknown ARN 404 |
| LAM-61 | P2 | `GetAccountSettings` is nominal where the model gives rules: `UnreservedConcurrentExecutions` is "minus the capacity that's reserved for individual functions" and does not move (run: reserved 2000, still 1000); `TotalCodeSize` usage omits layers (LAM-23); the limit is 75 GB where `doc` says "300 GB (unzipped)". | `lambda/layers.go` `accountSettings` | run 2026-10-11 | wrong | model: AccountLimit, AccountUsage; doc: `gettingstarted-limits` | Handler test: reserve 200, unreserved 800 |
| LAM-62 | P2 | `PutFunctionConcurrency` has no account rule: "as long as you leave at least 100 simultaneous executions unreserved"; 2000 was accepted against a limit of 1000. | `lambda/extras.go` `putConcurrency` | run 2026-10-11 | more forgiving | model: PutFunctionConcurrency; the code `InvalidParameterValueException` is the one fitting 400 (message `memory`) | Rejection test: 901 refused, 900 accepted (limit 1000) |
| LAM-63 | P2 | The throttle has the wrong shape. `TooManyRequestsException` carries `Reason` (`ReservedFunctionConcurrentInvocationLimitExceeded`, `ConcurrentInvocationLimitExceeded`, …) in the body and `Retry-After` as a header (`retryAfterSeconds`, `httpHeader`); the code sends the message only. | `lambda/invoke.go`; `lambda/store.go` `writeError` | read | wrong shape | model: TooManyRequestsException, ThrottleReason | SDK test: `errors.As` reads `Reason` and the retry header |
| LAM-64 | P3 | Invoke errors from the model that are never raised: `UnsupportedMediaTypeException` 415 ("The content type of the Invoke request body is not JSON"; `text/plain` ran); `InvalidRequestContentException` for "the 'x-amzn-RequestId' header is not a valid UUID"; `ClientContext` over 3 583 bytes is not refused (the model: "Up to 3,583 bytes of base64-encoded data"). | `lambda/invoke.go` | run 2026-10-11 | more forgiving | model: Invoke errors, ClientContext | Rejection cases per rule |
| LAM-65 | P2 | List filters are ignored. `ListLayers` and `ListLayerVersions` ignore `CompatibleRuntime` and `CompatibleArchitecture` ("Specify a runtime identifier to list only versions that indicate that they're compatible"); `ListAliases` ignores `FunctionVersion`; `ListFunctions` ignores `FunctionVersion=ALL` (no published versions listed) and `MasterRegion`; `ListLayerVersions` of a layer that does not exist answers an empty list where the model lists `ResourceNotFoundException` (`memory` for which AWS does). | `lambda/layers.go`, `lambda/aliases.go`, `lambda/functions.go` | run 2026-10-11 | wrong | model: the operations' member docs | SDK test per filter |
| LAM-66 | P2 | `LoggingConfig.LogGroup` is ignored: output goes to `/aws/lambda/<name>` and `AWS_LAMBDA_LOG_GROUP_NAME` says so, where the model says the function sends logs to the named group (created when new). `SystemLogLevel` is stored and has no effect. | `internal/lambdaruntime/env.go` `LogGroupName`; `lambda/logsink.go` | read | wrong | model: LoggingConfig | Logs test: a function with `LogGroup` writes there and the Logs tab reads it |
| LAM-67 | P2 | A mapping batch can exceed the 6 MB synchronous payload. SQS accepts 1 MiB messages since SQS-11; the poller gathers up to `BatchSize` of them and invokes with no size check. `doc` (`services-sqs-configure`): "as long as the total size of the events doesn't exceed the invocation payload size quota … (6 MB). Both Lambda and Amazon SQS generate metadata for each record. This additional metadata is counted". | `lambda/extras.go` `pollSQS` | read | more forgiving | model: BatchSize ("up to the payload limit for synchronous invocation (6 MB)"); doc | Poller test: 10 messages of 1 MiB arrive as invocations under 6 MB each |
| LAM-68 | P2 | **The console's Account strip never renders, and an unsigned request on nine of Lambda's date prefixes reaches S3.** The gateway sends an unsigned request to Lambda by a list of eight path prefixes; the model uses seventeen (`/2014-11-13/` InvokeAsync, `/2016-08-19/` account settings, `/2020-04-22/` code signing configs, `/2021-07-20/`, `/2021-11-15/`, `/2024-08-31/`, `/2025-11-30/`, `/2025-12-01/`, `/2026-07-09/` are missing; a signed request routes by its scope and is fine). The console's `LambdaAccount` is an unsigned peer call to `/2016-08-19/account-settings`; it gets S3's `NoSuchBucket`, and `lambdaFns` discards the error (`data["Acct"], _ =`), so the strip, "Code stored" and the concurrency limit are not on the page for anyone. | `internal/gateway/gateway.go` `lambdaPathPrefixes`; `internal/console/handlers_lambda.go` `lambdaFns`; `client_lambda.go` `LambdaAccount` | run 2026-10-11 (the page; `curl` the path) | wrong | model: the operations' `http` traits | Gateway test per prefix; browser test: the strip is there |
| LAM-69 | P2 | A function URL under `--iam-mode enforce` is evaluated for `lambda:InvokeFunctionUrl` alone. `doc` (`urls-invocation`): "To invoke a function URL, you must have `lambda:InvokeFunctionUrl` and `lambda:InvokeFunction` permissions." The model's AddPermission carries `InvokedViaFunctionUrl` for the second. | `lambda/urls.go` `serveFunctionURL` | read | more forgiving | doc: `urls-invocation`; model: AddPermission.InvokedViaFunctionUrl | Enforce test: only the first granted, request refused |
| LAM-70 | P3 | A function URL's address is derived from the function's name, so deleting and re-creating gives the same address. `model` (DeleteFunctionUrlConfig): "Creating a new function URL results in a different URL address"; `doc`: "its URL endpoint never changes" once created. Deliberate (CloudFormation `GetAtt` and a redeploy must address the same URL, `awsident.FunctionURLID`) and a listed difference; board 20 said it as a fact about AWS and is corrected. | `lambda/urls.go` `urlID` | read | differs, deliberately | model; doc | A line in "Differences from AWS" |
| LAM-71 | P3 | Documents that say what is not true: `docs/SUPPORT.md` "a function may override AWS_REGION" (reserved, `doc`); "612/621 … across all 47 routed operations" (LAM-44); the Environment variables section's "three passes" omits that the host's own environment is the lowest layer (`os.Environ()` in `childEnv`: a host `AWS_PROFILE` or token reaches a function); `lambda/router.go` "47 of Lambda's 85 operations" (88). | `docs/SUPPORT.md`, `lambda/router.go`, `internal/lambdaruntime/env.go` | read | wrong (documents) | doc: `configuration-envvars` | Ledger test rows |
| LAM-72 | P2 | Tags on an event source mapping answer 404: `tagTarget` takes the last ARN segment and looks up a function of that name (the UUID). The model: tags are for "a function, event source mapping, or code signing configuration"; CreateEventSourceMapping takes `Tags` and drops them; `ListTags` of a mapping the same. | `lambda/extras.go` `tagTarget`, `tagResource`, `listTags`; `createMapping` | read | wrong | model: TagResource, ListTags, CreateEventSourceMappingRequest.Tags | SDK test: tag a mapping, list, untag; create with tags |
| LAM-73 | P1 | The default `MemorySize` is 512; the model says "The default value is 128 MB". (Sections 2, 5 and 6 cited "LAM-9" for this; LAM-9 is the SQS record.) | `lambda/functions.go` `createFunction` (`orInt(req.MemorySize, 512)`) | run 2026-10-11 (`MemorySize: 512` on a create with none) | wrong | model: CreateFunction.MemorySize | Handler test: create with none reads 128; the REPORT line and `AWS_LAMBDA_FUNCTION_MEMORY_SIZE` follow |
| LAM-74 | P1 | **`CreateFunction`'s own response carries the default identity.** Under `--account-id 111122223333 --region eu-west-1` it answers `FunctionArn` `arn:aws:lambda:us-east-1:000000000000:function:f9`; `GetFunctionConfiguration` (eu-west-1, 111122223333), `PublishVersion`, the alias, the URL and the event invoke config are right. The record handed to `configView` is built in `createFunction` and never stamped with the instance's identity (the store stamps on read). The role default `arn:aws:iam::000000000000:role/lambda-role` is the same literal. | `lambda/functions.go` `createFunction`, `configView`; `lambda/store.go` `stamp` | run 2026-10-11 | wrong | model: FunctionConfiguration.FunctionArn; A8 | Handler test under a second identity: the create response equals the Get |

Owned by the foundation, with what Lambda adds:

- **A1** (the refusal's code): now `run` for Lambda. Every refusal renders
  "Refused" and the message with no code line and no "why" line.
- **A7** (unreachable copy): add Lambda. `copy.go`'s `Unsel` for `lambda` never
  renders, for a different reason from the other ten: `lambda_home` shows the
  account strip and the layers registry instead. *Corrected 2026-10-11: the
  account strip is itself unreachable (LAM-68), so Home shows the layers
  registry and nothing above it.*
- **A8** (literal identity): `run` under `--account-id 111122223333 --region
  eu-west-1`. The console-created role and `mappingView`'s `FunctionArn` both
  came back with `000000000000` and `us-east-1`. A third site is not in A8's
  list: `"awsRegion": "us-east-1"` in `pollSQS` (`lambda/extras.go`, about 492).
  *Added 2026-10-11: a fourth, and the one that matters: the queue URL the
  poller, the delete, the DLQ and the destinations build (LAM-36). The create
  response's `FunctionArn` and the role default in `configView`
  (`arn:aws:iam::000000000000:role/lambda-role`) are a fifth (LAM-74), and
  `GetFunction`'s `Code.Location` carries the data dir.*
- **A13** (confirms): the layer-version confirm is false today (LAM-11) and the
  function delete says nothing about mappings (LAM-1). *Corrected 2026-10-11:
  what the function-delete confirm should say is established: the model says a
  delete leaves the mappings ("To delete Lambda event source mappings that
  invoke a function, use DeleteEventSourceMapping"), so the board's sentence
  is right; it is the poller that must stop (LAM-1).*

Not a defect, recorded so nobody draws the board: no console action on a
function can be Denied. `AddPermission` writes `Effect: "Allow"` only
(`lambda/policy.go`, about 112), the console calls as root, and a root is
refused only by an explicit Deny.

**Moved between stages.** Three notes from reconciling the plan; the plan's
README has the reasons.

- **LAM-6 moves out, to SQS (stage 1).** The poller's cap of 10 per receive
  lands in the same commit as SQS-5, because once SQS refuses more than 10 a
  trigger with a larger batch size would stop receiving. By this stage it is
  closed; ~~what remains here is delivering one invocation with up to
  `BatchSize` messages gathered over several receives~~ *Corrected 2026-10-11:
  that was built in stage 1 (`pollSQS` gathers; `integration_sqs_batch_test.go`).
  What remains is the window, the FIFO cap and the 6 MB cut (LAM-53, LAM-67).*
  **Checked against the SQS stage:** receipt handles are per receive (the
  poller deletes with the handle of the receive it made: right);
  `GetQueueAttributes` with no names answers nothing (the poller never calls
  it; the console's queue check must pass `AttributeNames`); `ReceiveMessage`
  at most 10 (right); move tasks are asynchronous (not used). **What broke:**
  the queue URL's owner (LAM-36).
- **The function part of CFN-4 moves in (P0, run).** `AWS::Lambda::Permission`
  is accepted and dropped, so a template's grant never reaches the function
  policy. Acceptance: a stack with a function and a permission leaves that
  statement readable through `GetPolicy`.
- **Precondition: DDB-1.** `UpdateTable` ignores `StreamSpecification` until
  DynamoDB's stage (7) fixes it. Stream-trigger work here assumes it is fixed.

## 5. What to check

Beyond the common criteria.

**Functions**
- [ ] Create: name pattern and the 64-character limit for a bare name (LAM-45:
      *not enforced, run*); a duplicate is `ResourceConflictException`; `Role`
      required and well formed; `Runtime` and `Handler` required for a zip
      (LAM-46); each of zip, S3 and `_local_`; a zip that is not a zip; a path
      that does not exist; `ImageUri` refused by name.
- [ ] Every member of the function settings table in section 2 either reads
      back after create **and** after update, or is refused.
- [ ] `Publish` on create and on code update; `DryRun` on code update (LAM-37:
      *modifies the function, run*).
- [ ] Defaults: `Timeout` 3, `MemorySize` 128 (LAM-73), `Architectures`,
      `PackageType`, `State`, `LastUpdateStatus`.
- [ ] `GetFunction` on a name, a partial ARN, a full ARN, `name:version`,
      `name:alias` and `?Qualifier=`; the same forms on every operation that
      takes a function name.
- [ ] `DeleteFunction`: removes versions, aliases, the URL, the concurrency
      setting, the event invoke config and the process; ~~the mappings'
      pollers (LAM-1)~~ *Corrected 2026-10-11: **not** the mappings (`model`:
      "To delete Lambda event source mappings that invoke a function, use
      DeleteEventSourceMapping"); the mapping stays and its poller receives
      nothing (LAM-1).* A version-qualified delete removes that version only,
      and is refused while an alias points at it (~~not established~~ `model`:
      "You can't delete a version that an alias references"; LAM-38).
- [ ] `RevisionId` on every operation that takes one (LAM-17): UpdateFunction
      Configuration, UpdateFunctionCode, PublishVersion, UpdateAlias,
      AddPermission, RemovePermission, AddLayerVersionPermission,
      RemoveLayerVersionPermission.
- [ ] Version numbers never reused (LAM-41); a description-only change
      publishes (LAM-39); `CodeSha256` on PublishVersion (LAM-40).

**Invoke**
- [ ] The three invocation types; an unknown type; `Qualifier` naming a
      version, an alias and nothing that exists.
- [ ] The payload limit, sync 6 MB and async 1 MB (LAM-18); a non-JSON payload;
      an empty one; a non-JSON content type (LAM-64); `ClientContext` over
      3 583 bytes (LAM-64).
- [ ] `X-Amz-Function-Error`, `X-Amz-Executed-Version`, `X-Amzn-RequestId`,
      and `X-Amz-Log-Result` holding that invocation's lines only (LAM-35).
- [ ] Timeout: the caller's answer, the process afterwards (LAM-10), the next
      invocation on that process.
- [ ] Init: a handler that will not import, a handler name that does not
      exist, a process that exits, a process that never polls. Each answers a
      function error with a distinct `errorType`, and none waits out the timeout.
- [ ] Concurrency: 0, 1 with two callers, N above 5, and no setting (LAM-25),
      the `Reason` and `Retry-After` of a throttle (LAM-63), reserved
      concurrency against the account's unreserved 100 (LAM-62), 0 on an
      `Event` invoke (LAM-49).
- [ ] The process environment: every variable the ledger lists, the three-pass
      precedence, and the reserved keys (LAM-14: 21 of them, `doc`); the
      host's own environment as the lowest layer (LAM-71).

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
      `RoutingConfig` accepted, **returned**, and visibly cosmetic (LAM-42);
      delete of a missing alias (LAM-57).
- [ ] Deleting a version an alias points at (refused, LAM-38) or a mapping
      points at (a mapping on a version stays; the poller then finds no
      function, LAM-1).

**Async and destinations**
- [ ] Retry count 0, 1, 2 and the default; on-success, on-failure and
      dead-letter delivery to SQS, SNS, a function, EventBridge and (on
      failure) S3, with the record shape (LAM-50, LAM-51) and the one-minute
      and two-minute waits when the setting asks for them (LAM-27).
- [ ] A destination ARN that does not exist, at put time and at delivery time;
      a FIFO queue or topic; S3 on success (LAM-50). Config per qualifier
      (LAM-52).

**Event source mappings**
- [ ] Every setting in the mapping table: round trip, then behaviour.
- [ ] SQS: delete on success; return on failure; partial batch responses
      (the five complete-failure cases and the four complete-success cases in
      `doc`, `services-sqs-errorhandling`); filters, and an unmatched message
      deleted; batch above 10 needs a window (LAM-53); a FIFO queue keeps
      group order and is capped at 10; the record shape (LAM-9); visibility
      against timeout, on create and on update (LAM-5); a duplicate (LAM-15);
      the queue deleted under a running mapping; a batch cut at 6 MB
      (LAM-67); under a non-default account (LAM-36).
- [ ] Streams: starting position; a checkpoint that survives a restart; a
      failed batch; a reshard; the record shape for each source.
- [ ] `FunctionName` as a name, an ARN, and with a qualifier (LAM-8).
- [ ] `ListEventSourceMappings` filters by function and by source; states
      `Enabled`, `Disabled` and ~~whatever AWS reports while changing~~
      *Corrected 2026-10-11 (`model`): `Creating`, `Enabling`, `Enabled`,
      `Disabling`, `Disabled`, `Updating`, `Deleting`; the review's L7 table
      says which are modelled (LAM-55)*.
- [ ] Delete and update of a UUID that does not exist (LAM-16); delete returns
      the mapping (LAM-54); tags on a mapping (LAM-72).

**Function URLs**
- [ ] One per function and one per alias (~~one per function (LAM-20)~~
      *Corrected 2026-10-11, `model`: `Qualifier` "The alias name"; a second
      create is LAM-20; the alias case is LAM-58*); both addresses route;
      `NONE` and `AWS_IAM` under off, soft and enforce, and under enforce both
      `lambda:InvokeFunctionUrl` and `lambda:InvokeFunction` (LAM-69); `Cors`
      round trips and is applied or is declared cosmetic; `InvokeMode`
      round trips (LAM-58); a function error is a 502 (`memory`: the
      `urls-invocation` page does not say); update and delete of a URL that is
      not there (LAM-57).

**Permissions**
- [ ] `AddPermission`: service and account principals, `SourceArn`,
      `SourceAccount`, a duplicate statement id, an action that is not a
      Lambda action (~~not established~~ *`model`: the `Action` pattern
      `^(lambda:[*]|lambda:[a-zA-Z]+|[*])$`, enforced*), the 20 KB policy limit
      (~~not established~~ *`doc`, quotas page: "Function resource-based policy
      20 KB"; `PolicyLengthExceededException` is on the operation*),
      `FunctionUrlAuthType` and `InvokedViaFunctionUrl` (LAM-59), `$LATEST`
      refused (LAM-59), the policy per qualifier (LAM-59).
- [ ] Under soft and enforce each peer needs its statement (owed to integration).

**Layers**
- [ ] Publish from zip, S3 and `_local_`; sizes and hashes (LAM-23); numbering
      (LAM-24); compatible runtimes **and architectures** filter on
      `ListLayers` and `ListLayerVersions` (LAM-65: *not implemented, run*).
- [ ] A function naming a missing version is refused at create and update; at
      most 5 layers (`doc`, recording queue).
- [ ] The layer-permission operations validate their input (LAM-44).
- [ ] Delete a version in use (LAM-11).
- [ ] Layer order on the search paths, and the function's own path winning.

**Tags, account, lifecycle**
- [ ] Tags: limit (`memory`; LAM-19), key and value rules (LAM-43), a resource
      that does not exist (LAM-19), tags on a mapping (LAM-72), none on a
      version or alias (`model`).
- [ ] `GetAccountSettings` usage figures (LAM-23) and the unreserved figure
      (LAM-61).
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
| Lambda-Home | Functions, none open: account and layers | `lambda_home`, `lambda_layers` | exists. The caption is `copy.go`'s unreachable `Unsel` (A7). "Code stored" is left off until LAM-23. *Corrected 2026-10-11: the Account strip does not render at all (LAM-68); the layers half does.* |
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
| Lambda-16-Versions | Versions and aliases | a new block and handler | **new** entirely: no tab, no handler, no client call. Real pages answer the Test tab for `?tab=versions` |
| Lambda-17-Layer-Versions | A layer's versions | `lambda_layer_versions` | exists |
| Lambda-18-Layer-Version | One version and its permissions | `lambda_layer_detail` | exists as an inline panel under the registry forms; the design is its own screen (6a) |
| Lambda-19-Layer-Refused | Publish and look-up refused; no layers | `fail_inline`, `lambda_layers` | exists |
| Lambda-20-Confirms | The six confirms | six `hx-confirm` attributes in `lambda.html` | exist as one-line questions; bodies, verbs and the trigger list on function delete are **new** (6a) |
| Lambda-21-Bad-Launch | `Runtime.LaunchError` and the config fix | `lambda_result` | result exists; the fix card and "Said once already" are **new** |
| Lambda-22-Bad-Threw | The handler threw | `lambda_result` | exists; "What a trigger does with this" is **new** |
| Lambda-23-Bad-Timeout | Timed out; the process exited | `lambda_result` | results exist; the two explanations are **new** |

There is no Denied board, for the reason at the end of section 4.

**New items, and what each is built from**

| Item | Built from | Depends on |
|---|---|---|
| Teaching lines under each field | The settings table (B4): range and default from `lambda/validate.go` and `validSizing`, tier from the ledger | LAM-73, LAM-30, LAM-45 decided first |
| The life of one invocation | The form's timeout, `DefaultPoolMax`, the idle window from `doze-runtime` (10 m before a function exists) | |
| "Fits its queue" and the two warnings | A mapping's source ARN, `GetQueueAttributes` `VisibilityTimeout` (**name it**), the function's `Timeout` | Wording changes when LAM-5 is fixed: "AWS refuses this trigger, and so does doze-aws" |
| The life of one batch | The form's batch size, the function's timeout, the queue's visibility and redrive policy (`GetQueueAttributes` **with** `AttributeNames`: a call with none answers nothing since SQS-25) | LAM-53 (the window and the FIFO cap) |
| What changes | The form against `GetFunctionConfiguration` | |
| Layers on the Configuration tab | `Configuration.Layers`, which the service returns and `lambdaConfigWire` does not read; the newest version from `ListLayers` | LAM-31 for attaching |
| Reserved concurrency | `GetFunctionConcurrency`, read-only | LAM-25 |
| Versions tab | `ListVersionsByFunction`, `ListAliases`, read-only | LAM-13 for long lists |
| "If it fails" on an async invoke | `GetFunctionEventInvokeConfig` and the function's dead-letter target | |
| Triggers still wired, in the delete confirm | `Fn.Mappings`, already loaded | ~~Goes away when LAM-1 is fixed; the sentence then says the triggers are removed too~~ *Corrected 2026-10-11: stays. `model`: a function delete leaves the mappings; LAM-1 stops their poller, it does not remove them, so "1 trigger is still wired" is the true sentence on AWS too.* |
| The config fix on a launch error | The `errorType` and `errorMessage` in the result; the family name is in the message | |
| "Said once already" | Not built from data the console has. See below. | |
| "What a trigger does with this" | A mapping's source and that queue's `VisibilityTimeout` | |
| The error code chip | Foundation A1 | |

**Where each teaching line's facts come from**

| Line | Source |
|---|---|
| Timeout: starting the process is not counted | `runtime.go`, the two-phase wait and `initBudget` |
| Timeout: past it the call returns a function error | `runtime.go` `InvokeInput`; LAM-10 changes "the process is not stopped" to the opposite |
| Memory: also sets the CPU share on AWS | *Corrected 2026-10-11:* `model`, "Increasing the function memory also increases its CPU allocation" (was `doc: developer guide`) |
| Memory: 128 to 10 240 | `doc` (`gettingstarted-limits`: "128 MB to 10,240 MB, in 1-MB increments"); the model's trait is 128 to 32 768 (Managed Instances) |
| Timeout: 1 to 900 | `model` text and `doc` (quotas); the trait is 1 to 5400 (LAM-30) |
| Function name: 1 to 64 | `model`: "If you specify only the function name, it is limited to 64 characters in length" |
| Batch size: 1 to 10 000, default 10 for SQS and 100 for a stream; a FIFO queue 10; above 10 a window of at least 1 s | `model` (BatchSize, MaximumBatchingWindowInSeconds), `doc` (`services-sqs-configure`) |
| Memory: stored, not enforced | ledger, "Differences from AWS" |
| Memory: default 128 (`model`) | `functions.go` `createFunction`; the code's 512 changes with LAM-73. *Corrected 2026-10-11: the board said "default 512" on its line 4 and "the default is 128" on its line 3; both now say 128.* |
| Handler: up to 128 characters, no spaces | model: `Handler` length and pattern in `validate.go` |
| Runtime: a missing interpreter is not refused | ledger, Runtimes; `warnRuntime` |
| Code path: runs in place, a warm process keeps old code | ledger, Code |
| Variables: yours win over the injected ones | ledger, Environment variables |
| Up to 5 processes; a call past that waits | `pool.go` `DefaultPoolMax`, `acquire` |
| Concurrency 0 refuses every call | `invoke.go`; *Corrected 2026-10-11:* `model` (Invoke, `TooManyRequestsException` at "function level (ReservedFunctionConcurrentInvocationLimitExceeded)"); for an `Event` invoke the events go to the DLQ or on-failure destination (`doc`; LAM-49), and the sentence must say so |
| Idle window | `[lambda] idle-timeout`, `doze-runtime` |
| Six times the timeout for a queue | *Corrected 2026-10-11:* `doc` (`services-sqs-configure`, read): "at least six times the configuration timeout", plus `MaximumBatchingWindowInSeconds`; and "Your function's timeout must be less than or equal to the queue's visibility timeout. Lambda validates this requirement when you create or update an event source mapping" |
| Function URL: `AWS_IAM` unsigned; `NONE` needs a statement under enforce | ledger; `urls.go` `serveFunctionURL` |
| Layers: laid out as on AWS, on search paths, not `/opt` | ledger, "Layers, without /opt" |
| Layer permissions: stored, not evaluated | ledger, "Not covered" |
| A version freezes code and configuration; weights not applied | ledger; `aliases.go`; `doc` (`configuration-versions`) for what is frozen |
| A version number is never reused | `doc` (`configuration-versions`); LAM-41 |
| Tags: not on a version or an alias | `model` (ListTags) and `doc` (`configuration-tags`) |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- 21, "Said once already": the console has no access to doze-aws's log, so it
  cannot show the create-time warning. Drop the card, or carry the warning on
  the create response (LAM-34) and store it.
- 21, the badge reads "Warm" on a function that cannot start. True of the code
  (LAM-28) and wrong; *Corrected 2026-10-11: the board now draws Idle (listed
  in 6a)*.
- 12, "Over 10, SQS here still hands out one message per receive": ~~true
  today (run) and removed by LAM-6~~ *Corrected 2026-10-11: false since stage
  1 (LAM-6 closed; the poller gathers); the board now says the poller gathers
  over receives of at most 10 and that AWS needs a window of at least 1 s above
  10, which doze-aws does not check (LAM-53).*
- 12 and 10, "AWS refuses an SQS trigger set up like this": ~~`doc` for the
  rule, `memory` for the refusal~~ *Corrected 2026-10-11: `doc` for both
  (`services-sqs-configure`: "returns an error if the function timeout exceeds
  the queue's visibility timeout"); the board's sentence stands. Only the
  error's code and wording are `memory`, and no board prints them.*
- 20, "Functions already configured with it keep running": false today (LAM-11);
  `model` says it is true on AWS.
- 20, "The address is derived from the function's name, so a new one is the
  same address": true of the code (`urlID`; run). *Corrected 2026-10-11: the
  board now says that AWS gives a new URL a different address (`model`,
  DeleteFunctionUrlConfig; LAM-70).*
- 20, "Its event source mappings are not [removed]: the poller and the mapping
  stay behind": the mapping staying is `model`; the poller staying is the bug
  (LAM-1). *The board now says the mappings stay and are removed on their own.*
- 07, "Each is a full invoke with the same event": true; the retries run back
  to back here and one minute, then two, apart on AWS (`doc`; LAM-27). *The
  board now says so.*
- 02, 09, 10, "1 – 900 · default 3 · enforced": the **target**; the service
  accepts 901 to 5400 today (LAM-30). Not "enforced" until the hand-derived
  rule lands.
- Home, "Letters, digits, hyphens and underscores, up to 64" for a
  layer name: `memory` (the model: 1 to 140 for a name or an ARN, no 64).
  *The number is removed from the board.*
- 23, "plus one second of grace for the function to report its own": the grace
  exists, and no embedded client reports its own timeout. Reword when LAM-10 lands.
- 06, 22, 23: durations, request ids and stack frames are sample data. A real
  Node trace carries four more frames from the embedded client.
- 09 and 10 leave out the wiring strip that sits above the tabs on every tab.

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each board from the working copy
(`console-design/project/Lambda-*.dc.html`, 1440 by 900, light; Home, 02, 09,
12 and 16 also dark) and the real page in the same state beside it, from a
spec that created the resources the boards draw (a Python function through the
console's create form, a queue and its DLQ, three layer versions and a
permission, tags, a version, an async config, a URL, a function that cannot
start, one that throws, one that times out, one whose handler is missing). The
real pages were rendered in light and dark (Create, Test, Configuration,
Triggers, Home) and at 900 wide (Test, Configuration, Create). Both images of
every pair were looked at; the real page was never taken to be the board.
**Shell** differences are the same on every board and are not Lambda's: the
design's rail is marks only and the real one is labelled, the design's list
header reads "Functions / New" and the real "Lambda (n)", and the real page has
a top bar the design draws smaller. They are not re-opened here.

| Board | Verdict | What differs |
|---|---|---|
| Lambda-Home | differs | The Account strip (Functions, Concurrency limit, the sentence "The limit is nominal…") is not on the page, for any state: LAM-68. The "Lambda" heading with its icon and the line "Select a function to invoke it…" are absent; the real page opens on the layers table. The publish form carries labels only where the board has a teaching sentence under each field (layer name, runtimes "Each is checked against AWS's runtime list", the content path layout). *(builds 10, 1)* |
| Lambda-01-None | matches | Copy is `copy.go`'s. The board repeats "No functions yet" as a bold line above the sentence that says it; the page shows the sentence and the button. |
| Lambda-02-Create | differs | The right-hand panel "The life of one invocation" is absent. **No field carries the teaching pattern**: the label is line 1; there is no value in words, no one-sentence "what it does", no range, default and **enforced or stored**, no neighbour check. The name line ("1 – 64 · logs to /aws/lambda/…") is absent; Memory's default is 512 (LAM-73) and its line 4 on the board said so too (corrected). *(builds 1, 4)* |
| Lambda-03-Create-Node | differs | As 02, and the Runtime and Handler hints do not change with the runtime (the page has one hint for all six). *(builds 1, 4)* |
| Lambda-04-Create-Refused | differs | The refusal renders below the buttons, outside the first screen; the board puts it above the form with the **error code chip** (A1 sends none), the sentence "doze-aws refuses this because AWS does…" and "What you typed is kept…". The right-hand column is an annotation of other states, not a page region. *(build 2)* |
| Lambda-05-Test | differs | The overview strip (triggers, function, destinations) sits **below** the tab bar on the page and **above** it on the board; there is no **Versions** tab; the sentence "Runs your handler as a local process. It has 30 s once it has the work; starting the process does not count." is replaced by another. The editor (CodeMirror, "valid JSON / Format") and the "cli" button are the shell's and are right. *(build 3)* |
| Lambda-06-Invoke-OK | differs | The chips (cold start, init, execution, round trip) and the log tail match. Absent: "Init is the process coming up. Execution is your handler…" and, under the tail, "The last 4 KB this process printed. On a warm process it can start with the previous call's lines…" (the second is true: LAM-35). *(build 3)* |
| Lambda-07-Invoke-Async | differs | The receipt matches. The "If it fails" panel (tried up to 3 times; then handed on to the on-failure destination as a record; if it succeeds…) is absent. *(build 3)* |
| Lambda-08-Invoke-Refused | differs | As 04: below the buttons, no code chip, none of "The event is kept. Nothing ran…". The editor's own client-side message is extra. *(build 2)* |
| Lambda-09-Config | differs | No teaching lines on Runtime, Handler, Timeout, Memory; the "Fits its queue" callout, the Layers card and the Reserved concurrency card are absent; "Code size" reads `0 B` (LAM-22). The page keeps the overview strip on this tab and the board drops it for room: **kept, deliberately** (it is the shell's pattern on every tab). *(builds 1, 5)* |
| Lambda-10-Config-Edit | differs | No "What changes" panel, no unit echo ("1 minute 30 seconds", "half a gigabyte"), no "Longer than the queue's visibility timeout" warning. *(builds 1, 5)* |
| Lambda-11-Config-Refused | differs | The save refusal sits under the Save button and the code refusal under Update code; the board puts a summary on top, "Saved: index.handler. Nothing was changed." beside the field and "The function keeps running the code it had…" under the code field. No code chip. *(build 2)* |
| Lambda-12-Triggers | differs | The table (source, batch size, state, remove) matches. "Add a trigger" is a one-line inline row (select, batch, button) where the board has a panel with labelled fields, the teaching lines, the "Fits its queue" and "Shorter than this function's timeout" callouts, and the right-hand "The life of one batch". *(build 6)* |
| Lambda-13-Trigger-Refused | differs | The inline refusal under the form matches. The "Not refused" note (a queue whose visibility is shorter than the timeout) and the callout it points at are absent. *(build 6)* |
| Lambda-14-Logs | differs | Time, request chip and line match, with the filter box and pause. The explanation under the table ("Lines are in time order, newest at the bottom. A request chip narrows…") and the `init` label note are absent. *(build 3)* |
| Lambda-15-Tags | matches | The shared draft editor (foundation A5). A value is up to 256 characters (`model`); a key 1 to 128 (not enforced, LAM-43). |
| Lambda-16-Versions | cannot match yet | There is no tab, handler or client call. Waits on `ListVersionsByFunction` and `ListAliases` in `client_lambda.go`, on LAM-13 for long lists, and on LAM-39, 41 and 42 so that what the tab shows is true (a version per changed setting, never a reused number, an alias's routing). The board's "A version freezes the code and the configuration…" is `doc`. *(build 3)* |
| Lambda-17-Layer-Versions | matches | Registry, publish and look-up panels and the versions list match in structure; the delete is a trash icon where the board writes "delete" (its confirm is row 20). |
| Lambda-18-Layer-Version | differs | The board is its own screen (title "harbour-shared-utils v3", details left, permissions right); the page opens a panel under the registry forms and the permissions are one column below the details. The inline refusal ("The statement id (share-1) provided already exists.") matches. *(build 7)* |
| Lambda-19-Layer-Refused | matches | Both refusals render inline under their forms with the code; "with no layers yet" is the empty state. |
| Lambda-20-Confirms | differs | The dialog is one question with Cancel and the verb. The board's six have a body ("The function, its versions and its aliases are removed. Its event source mappings are not…"), "Keep it" and the verb, an "After it:" line, and for function delete the wired triggers. *(build 8)* |
| Lambda-21-Bad-Launch | differs | The result (code and message) matches. "Fix it in the config" and "Said once already" are absent (the second needs the create warning carried: LAM-34). The page's badge says Warm after a launch error where the board (corrected) says Idle: LAM-28. *(build 9)* |
| Lambda-22-Bad-Threw | differs | Result and tail match. "What a trigger does with this" and "A warm call: no cold start, no init…" are absent. *(build 9)* |
| Lambda-23-Bad-Timeout | differs | Results match in kind (the page shows `Duration: 2000.33 ms` and `Billed Duration: 2001 ms` for a 1 s timeout: LAM-10). "Why 4 s for a 3 s timeout" and "The process is not stopped" are absent, and the second is wrong once LAM-10 lands. *(build 9)* |

**Where the design was wrong** (corrected in the working copy on 2026-10-11, in
`Lambda-02`, `07`, `12`, `20`, `21` and `Home`; the published canvas still
carries the old text until it is republished)
- 02, Memory "default 512": the model says 128, and the board's own line above
  it said so. Now 128, and the placeholder.
- 12, "Over 10, SQS here still hands out one message per receive, so a batch
  over 10 stays at 1": false since stage 1 closed LAM-6. Now says the poller
  gathers over receives of at most 10 and that AWS needs a window of at least
  1 s above 10.
- 21, the badge "Warm · sleeps in 9m 58s" for a function that cannot start:
  now "Idle · wakes on invoke".
- 20, "The address is derived from the function's name, so a new one is the same
  address": drawn as a fact of AWS; the model says the opposite. Now says what
  doze-aws does and what AWS does.
- 20, "the poller and the mapping stay behind": the mapping staying is the
  model's; the poller staying is LAM-1. Now "they stay, and are removed on
  their own".
- Home, layer name "up to 64": not in the model (1 to 140 for a name or an
  ARN). The number is removed.
- 07, retries described without their timing: now "doze-aws retries at once;
  AWS waits one minute, then two" (`doc`).
- **Not changed, listed:** the "1 – 900 · enforced" lines are the target of
  LAM-30 (the service accepts 901 to 5400 today); "128 – 10 240" is `doc`,
  the model trait is 32 768 (LAM-30); 04 and 11 quote the model's own
  5400 and 32768 messages, which are right and stay. The "AWS refuses an SQS
  trigger" sentences on 10, 12 and 13 stand, and are now `doc`.

**Build list, in order** (all of it is console work; behaviour changes it
needs are named, and each is a finding above)
1. **Settings record and the five teaching lines** on create, configuration and
   the add-a-trigger form, from the validation table and the ledger (kit
   `setting_field`): name and unit; the value in words ("1 minute 30 seconds",
   "half a gigabyte"); one sentence; range, default and **enforced or
   stored**; the neighbour check with the real numbers (timeout against the
   queue's visibility, batch size against the window and the queue). Needs
   LAM-73 (default 128), LAM-45 (64), LAM-30 (900 and 10 240) and LAM-53
   decided first, or the line states a number the service does not hold.
2. **Refusals** at the board's place with the code chip and the two sentences
   (A1), on create, invoke, configuration and trigger forms.
3. **The Test tab**: the overview strip above the tab bar; a **Versions** tab
   (new handler, `ListVersionsByFunction` and `ListAliases`, read-only); "If it
   fails" from `GetFunctionEventInvokeConfig` and the dead-letter target; the
   two sentences under the result; the Logs footer.
4. **Create**: two columns, with "The life of one invocation" as a pure
   function of the form's timeout, `DefaultPoolMax` and the idle window from
   `doze-runtime`; runtime-specific handler hints.
5. **Configuration**: "Fits its queue" and the visibility warning
   (`GetQueueAttributes` **with** `AttributeNames`), "What changes", the unit
   echo, the Layers and Reserved concurrency cards (read-only first; attaching
   a layer is LAM-31), a true Code size (LAM-22).
6. **Triggers**: the add-a-trigger panel with its callouts and "The life of one
   batch" (needs LAM-53 so the batch panel is true).
7. **The layer version as its own screen**, two columns.
8. **Confirms**: bodies, "Keep it" and the verb, "After it", the wired
   triggers on function delete (A13; true on AWS, `model`).
9. **Bad-day cards**: the config fix (a family-to-config-key map), "Said once
   already" (LAM-34 must carry the warning), "What a trigger does with this",
   the two timeout explanations (reworded with LAM-10).
10. **Home**: the Account strip (LAM-68), the heading and icon, the teaching
    sentences on the publish form.
11. Render every board beside its page in light, dark and at 900, and write the
    table again.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | `configView` carries `CodeSize` and a stable `LastModified` (LAM-21, 22); account and layer sizes (LAM-23, 61); layer numbering (LAM-24); function version numbers across a delete (LAM-41); mapping delete and update of a missing UUID (LAM-16); the mapping wire shape and delete's answer (LAM-54); `GetFunction.Concurrency` (LAM-47); default memory 128 (LAM-73); the 64-character name (LAM-45); `Runtime` and `Handler` required (LAM-46); event invoke config per qualifier (LAM-52); URL round trip (LAM-58); alias `RoutingConfig` (LAM-42) |
| Runtime (`internal/lambdaruntime`) | The handler is stopped at its timeout (LAM-10); a pool whose process never started reports none (LAM-28); the SQS record's key set (LAM-9); `LoggingConfig.LogGroup` (LAM-66) |
| SDK v2 | Update reads back every member (LAM-2); `Publish` (LAM-7); `DryRun` (LAM-37); a qualified mapping (LAM-8); delete with a mapping (LAM-1); every mapping setting (LAM-3, LAM-4); batch 20 (LAM-6, kept); a paginator per list (LAM-13); `RevisionId` on each of the eight operations (LAM-17); `CodeSha256` on PublishVersion (LAM-40); delete a version an alias holds (LAM-38); a description-only publish (LAM-39); URL twice (LAM-20); tags, including on a mapping (LAM-19, 72); a layer deleted in use (LAM-11); the list filters (LAM-65); destinations of every type and their record (LAM-50, 51); `Event` invoke with reserved 0 (LAM-49); the five operations with no SDK-spelled test today (`DeleteFunctionCodeSigningConfig`, `ListFunctionUrlConfigs`, `PutFunctionCodeSigningConfig`, `UpdateFunctionEventInvokeConfig`, `UpdateFunctionUrlConfig`) |
| Rejection parity | Reserved keys and 4 KB (LAM-14); sync 6 MB and async 1 MB, RequestTooLargeException (LAM-18); map keys (LAM-43); the regenerated tables over all 52 routed operations, including the layer-permission four (LAM-44); a 65-character name (LAM-45); the leave-100 rule (LAM-62); visibility against timeout on create and update (LAM-5; the refusal asserted, the message skipped); a duplicate mapping (LAM-15, skipped); batch above 10 without a window, FIFO above 10, a stream with no starting position (LAM-53) |
| Router | Every operation in `ops_lambda.json` routed or stubbed with the agreed code (LAM-12); a gateway test per date prefix (LAM-68) |
| Restart | A stream mapping resumes where it stopped (LAM-4); an SQS mapping resumes; a version's frozen code; the version high-water mark across a delete and restart (LAM-41) |
| Identity | The package and the browser spec under a second account and region (A8, B2), **an SQS trigger delivering under it, and a failed async event reaching its DLQ** (LAM-36), and the create response's ARN equal to the Get's (LAM-74) |
| Drift | A test that fails when `dzaudit list lambda` and the constraint table differ, so a regenerated model is a red build (LAM-44) |
| Conformance (boto3) | Every row in section 8 |
| Browser | Each `new` item; each confirm's sentence; both states of the queue check; a refusal on every form; the badge after a launch error; the Account strip is on Home (LAM-68); the page at 900 wide, light and dark, for the main create and detail pages |
| Mutation | `lambda/` and `internal/lambdaruntime/` baselines, then survivors in `extras.go`, `functions.go`, `pool.go` |

## 8. Recording queue

Every one of these is `doc`, `model` (for the rule; the AWS answer is to be
recorded) or `memory` today. Scenarios go in `conformance/tests/test_lambda.py`.
*Corrected 2026-10-11: the "Source now" column was re-read; changes are
marked.*

| Claim | Source now | Scenario |
|---|---|---|
| A trigger is refused when function timeout exceeds queue visibility, on create **and** update; the code and message | ~~doc (rule), memory (refusal)~~ `doc` (`services-sqs-configure`) for the rule; `memory` for the code (the model lists `InvalidParameterValueException`) and the message | `trigger_visibility_shorter_than_timeout` |
| A second mapping for the same queue and function is a conflict | memory (`model` lists `ResourceConflictException`) | `trigger_duplicate` |
| What happens to a function's mappings and their messages when it is deleted | mappings stay: `model`; the messages: `memory` | `delete_function_with_trigger` |
| The SQS event record's keys and values (`attributes`, `messageAttributes`, `md5OfBody`, FIFO's three) | doc (`with-sqs`) | `sqs_event_shape` |
| Partial batch response returns only the failed items; the nine success and failure cases | doc (`services-sqs-errorhandling`) | `partial_batch_response` |
| A filter drops a non-matching message and deletes it from the queue | doc (`invocation-eventfiltering`) | `trigger_filter` |
| Batch size above 10 needs a batching window of at least 1 s; the refusal without one | ~~memory~~ rule: `model` and `doc`; the code and message: memory | `trigger_batch_over_ten` |
| A FIFO source with batch size above 10 is refused | `model` (BatchSize text), `doc` | `trigger_fifo_batch` |
| Default `MemorySize` is 128 | ~~doc~~ `model` | `create_defaults` |
| `Publish: true` returns version 1 | ~~doc~~ `model` | `create_publish` |
| Reserved environment keys (21) and the 4 KB limit; codes and messages | doc (`configuration-envvars`); the messages `memory` | `environment_refusals` |
| A timed-out invoke's payload text and REPORT line | memory | `invoke_timeout` |
| `LogType: Tail` holds only that invocation | ~~doc~~ `model` | `tail_is_one_invocation` |
| Payload above 6 MB sync and 1 MB async is `RequestTooLargeException` | ~~doc~~ `model` | `invoke_payload_limit` |
| `LastModified` format | ~~memory~~ `model` text ("YYYY-MM-DDThh:mm:ss.sTZD"); the exact string `memory` | `create_defaults` |
| `RevisionId` mismatch on each of eight operations | ~~doc~~ `model` | `revision_id` |
| `CreateFunctionUrlConfig` twice | ~~doc~~ `model` lists the code; the case `memory` | `url_twice` |
| 51 tags accepted; untag on a missing function | tag limit `memory` (not in the model, the quotas or the tags page); untag `model` | `tag_limits` |
| Function version numbers after a delete and re-create | `doc` (`configuration-versions`) | `version_numbering` |
| Layer version numbers after a delete | memory | `layer_numbering` |
| A function keeps a deleted layer version | ~~doc~~ `model` | `layer_deleted_in_use` |
| `DeleteLayerVersion` of a version that does not exist | memory (the code says idempotent; the model lists `ResourceNotFoundException`) | `layer_delete_missing` |
| Delete of a mapping that does not exist; the mapping in `Deleting` | ~~doc~~ `model` | `delete_missing_trigger` |
| Paging on each list, the page sizes, and `NextMarker` | model | `list_paging` |
| Stream mapping: `StartingPosition` required, a failed batch blocks the shard | `model`, `doc` (`with-kinesis`) | `stream_trigger_failure` |
| Async retry timing (1 minute, 2 minutes), the 6-hour queue, and the destination record's shape | doc (`invocation-async-error-handling`, `invocation-async-retain-records`) | `async_retries` |
| Reserved concurrency 0: 429 on sync with `Reason`; on async, the DLQ without retries | `model` (sync), `doc` (async) | `reserved_concurrency_zero` |
| `AddPermission` `SourceArn` condition operator: `ArnLike` (code) against "StringLike" (model text) | **unresolved** | `permission_source_arn_operator` |
| `AddPermission` of an account id principal: the stored form | memory | `permission_principal_form` |
| At most 5 layers per function; the code and message | `doc` (quotas page); code `memory` | `layers_limit` |
| `CodeSha256` of a function is base64 | memory | `create_defaults` |
| `ListLayerVersions` of a layer that does not exist | memory | `layer_versions_missing_layer` |
| Alias limit per function (`AliasLimitExceededException`), code storage limit (`CodeStorageExceededException`) | the model names the errors and not the values | `alias_limit`, `code_storage_limit` |
| What an unsupported operation answers, per family | ~~not established~~ decided: 501 `NotImplemented` with `X-Doze-Not-Built` (protocol D5; the answer is doze-aws's, not AWS's) | `unsupported_operations` is dropped |

## 9. Owed to integration

| What | With |
|---|---|
| Visibility shorter than timeout is refused at trigger creation; the queue's Configuration tab shows the same check from its side. *Added 2026-10-11: the refusal is `doc`, so the SQS board's reader check (Sqs-18) can say "AWS refuses this trigger, and so does doze-aws" once LAM-5 lands, and its "Statements on the boards not yet backed" and 6a "Where the design was wrong" entries on LAM-5 are out of date* | SQS |
| A deleted function's poller stops; its messages are not received on its behalf | SQS, Kinesis, DynamoDB |
| A trigger batch above 10, gathered in receives of at most 10 (done in stage 1), cut at 6 MB with 1 MiB messages, and a FIFO source capped at 10 (LAM-53, LAM-67) | SQS |
| **Every `peercall` SQS helper under a non-default account** (LAM-36): the same hard-coded owner serves SNS, S3 and EventBridge deliveries to a queue and their DLQs; Lambda's stage fixes the helper, those stages assert it | SNS, S3, EventBridge, SQS |
| An unsigned request on a Lambda date prefix the gateway does not know (LAM-68) | gateway (foundation) |
| The constraint walk reads map keys, and a drift test compares the table (LAM-43, LAM-44): the walker is shared by every service's table; each service's review says whether its model has map-key constraints | foundation (`internal/modelcheck`, `dzaudit`) |
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

- [ ] ~~LAM-1 to LAM-35~~ *Corrected 2026-10-11:* LAM-1 to LAM-74 each closed,
      with the test that failed first named beside it, or moved to section 8 or
      9 with a reason. (LAM-6 is closed in stage 1; LAM-70 is a documented
      difference.)
- [ ] The 36 unrouted operations each have a tier in `SUPPORT.md` and an entry
      in `not-built.md`, and answer the stub code the recording or the decision
      names (501 `NotImplemented`, `X-Doze-Not-Built`). Resolved here:
      InvokeAsync (F), provisioned concurrency (S), runtime management config
      and recursion config (C), the three resource-policy operations (F with
      LAM-59, S until then). *Proposed 2026-10-11; confirm in `SUPPORT.md`.*
- [ ] The function settings table in section 2 has no "dropped", "ignored" or
      "not established" left: each member reads back after create and update,
      or is refused, and each C member reads as cosmetic in the console.
- [ ] The mapping settings table has no "dropped" left. Each of the six stream
      settings in its last-but-one row is decided F or C in `SUPPORT.md`.
- [ ] Decided and written into `SUPPORT.md`: the default `MemorySize` (128,
      `model`); the ranges the console teaches (LAM-30); a call past the
      concurrency ceiling queues or throttles (LAM-25); async retry timing
      (L7: `[lambda] async-retry-delays`); the batching window; `Creating`,
      `Pending` and `InProgress` immediate and `--lambda-strict` (L7, L8);
      `Deleting` modelled for two seconds.
- [ ] ~~Established and written down~~ *Corrected 2026-10-11: established by
      this review (`model`, `doc`) and to be written into `SUPPORT.md`:*
      `RevisionId` on eight operations (LAM-17); tags on a mapping (yes) and a
      layer (no), a version or alias (no); `LoggingConfig.LogGroup` and
      `SystemLogLevel` (LAM-66); `AddPermission` with a non-Lambda action
      (refused by pattern) and at the policy size limit (20 KB); a version
      delete while an alias points at it (refused, LAM-38); the SQS rule is
      timeout against visibility (LAM-5). *Still open:* what the CloudFormation
      path sends for a mapping on an alias; which of the absent families a
      newer Terraform or CDK refresh calls (the one apply on record read none);
      the `SourceArn` condition operator (section 8).
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] A handler is stopped at its timeout, and a test shows a write after the
      timeout does not happen.
- [ ] No stream record is delivered twice across a restart.
- [ ] Deleting a function leaves **no mapping polling and every mapping
      listed** (`model`), and deleting a layer version leaves every function
      that names it working.
- [ ] **An SQS trigger delivers under `--account-id 111122223333 --region
      eu-west-1`** (LAM-36), in the package test and in the browser spec, and
      a DLQ and a destination reach their queue under it.
- [ ] The constraint tables are regenerated from the current model, the walk
      reads map keys, all 52 routed operations have a table, and a drift test
      holds the table to `dzaudit list` (LAM-43, LAM-44).
- [ ] **The fidelity table (6a) exists, was made from rendered boards beside
      rendered pages in light, dark and at 900 wide, and no row says
      "differs" without a build decision beside it.** After the build it is
      written again, and every row reads "matches" or "differs, deliberately"
      with the reason.
- [ ] **The AWS icons stay**: in the rail, the resource header and (shell) the
      list pane header; where a board draws `λ` the page keeps the icon.
- [ ] **The five lines of the teaching pattern on every setting** of the
      create form, the Configuration tab and the add-a-trigger form: name and
      unit; the value in words; one sentence; range, default and enforced or
      stored from the settings record; the neighbour check with real numbers.
      The settings: function name, runtime, handler, code path, timeout,
      memory, environment variables (name rules, 4 KB), layers, function URL
      auth type, async retries, destinations, reserved concurrency, queue,
      batch size, batching window.
- [ ] **Every wire, one operation at a time**: Lambda declares `restJson1`
      only; each of the 52 routed operations has one accepted-as-the-SDK-spells-it
      test (five have none today) and one rejection case from the regenerated
      table; headers (`X-Amz-*`) and query members are read as the model binds
      them.
- [ ] **Each asynchronous operation decided and written** in `SUPPORT.md`
      (the L7 table), with the difference listed for every row that is
      immediate, and the L8 rules behind `--lambda-strict` or listed.
- [ ] All 24 boards implemented from the foundation's shell, or listed here as
      dropped with the reason. Each `new` item reads only the data named for it.
- [ ] No statement on a Lambda screen is in the "not yet backed" list in section 6.
- [ ] The six confirms say only what the code does, each with the body and
      verbs of its board.
- [ ] The queue check appears in both states and names the real queue, and its
      wording matches whether LAM-5 is fixed ("AWS refuses this trigger, and so
      does doze-aws").
- [ ] The process badge is Idle for a function with no process.
- [ ] `e2e/tests/lambda.spec.ts` covers every route under `/lambda` with the
      route gate on, and the accessibility sweep is clean on the six tabs
      (Test, Configuration, Triggers, Logs, Tags, Versions), create and Home.
- [ ] Conformance: a scenario for every row in section 8. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baselines recorded here: `lambda/` ____ killed, ____ survived;
      `internal/lambdaruntime/` ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

*Corrected 2026-10-11: rows 0a and 1a are new; rows 3 to 9 absorb LAM-36 to
LAM-73; the column "Days" is the honest sizing and replaces the "five days,
nearer eight" below.*

| Order | Work | Files | Days |
|---|---|---|---|
| 0 | *Done in stage 1*, with SQS-5: cap the poller's receive at 10 and fill a batch over several receives | `lambda/extras.go` `pollSQS` | 0 |
| 0a | **Identity first**: queue URLs from the instance's identity (LAM-36), with a test under `--account-id 111122223333`; the SNS, S3 and EventBridge assertions are theirs | `internal/peercall/peercall.go`, `lambda/extras.go` | 0.5 |
| 1 | Contract: all 88 operations tiered, the two settings tables, the decisions of sections 2 and the L7 and L8 tables | `docs/SUPPORT.md`, `docs/not-built.md` | 1 |
| 1a | **Tables and walk**: the walk reads map keys, regenerate `validate.go` for 88 operations, the drift test, hand-derived rules each with its `doc` source (900, 10 240, 64, 5 layers, 4 KB, reserved keys, 20 KB policy, leave-100) (LAM-43, 44, 45, 14, 30, 62) | `internal/modelcheck`, `cmd/dzaudit`, `lambda/validate.go` (generated), `lambda/validate_hook.go` | 1.5 |
| 2 | Red tests for every finding, written with the row that fixes it (protocol step 2) | `lambda/*_test.go`, `internal/lambdaruntime/*_test.go` | in the rows |
| 3 | Mappings: store every setting and return the full shape (LAM-3, 54), qualified names (8), delete with the function and the `Deleting` window (1, 16, 55), duplicates, the visibility rule on create and update (5, 15), the rules of LAM-53, tags (72), a 501 for sources not served | `lambda/extras.go`, `lambda/store.go`, `lambda/functions.go` | 2 |
| 4 | Mappings: the SQS record from `All` attributes (9), filters, partial batch responses, the batching window, the 6 MB cut (67) | `lambda/extras.go`, `internal/peercall/peercall.go` | 2 |
| 5 | Mappings: stream starting position, a persisted checkpoint, failure handling, bisect, retries, `OnFailure` (4) | `lambda/extras.go`, `lambda/store.go` | 2 |
| 6 | Functions: update applies every member (2), `Publish` (7), `DryRun` (37), `RevisionId` on eight operations (17), `CodeSha256` (40), `CodeSize` and `LastModified` (21, 22), defaults (73, 45, 46), the fingerprint and version numbers (39, 41), `GetFunction` shape (47), the create response's identity (74), alias `RoutingConfig` (42) and version delete (38), policy (59), code signing (60) | `lambda/functions.go`, `lambda/versions.go`, `lambda/aliases.go`, `lambda/policy.go` | 2 |
| 7 | Runtime: stop the handler at its timeout, the timeout payload, the tail per invocation, the pool's live count, `LogGroup` (10, 35, 28, 66) | `internal/lambdaruntime/runtime.go`, `pool.go`, `logs.go`; `lambda/invoke.go` | 2 |
| 8 | Layers, tags, URLs, account, payload limits, throttle shape, async: destinations of five types and the record (50, 51), reserved 0 (49), retry delays (27), per-qualifier config (52), URL round trip (58) | `lambda/layers.go`, `lambda/urls.go`, `lambda/invoke.go`, `lambda/extras.go` | 1.5 |
| 9 | Paging on every list and the list filters (13, 65); the 36 absent operations as 501 (12) | `lambda/router.go`, `lambda/functions.go`, `lambda/layers.go`, `lambda/versions.go`, `lambda/aliases.go`, `lambda/extras.go` | 1 |
| 10 | Section 5 sweep and parity cases | `lambda/rejection_parity_test.go`, `lambda/sdk_test.go`, `lambda/sdk_coverage_test.go`, `lambda/testdata/cases_lambda.json` | 1 |
| 11 | Conformance scenarios (about 35) and the claims table | `conformance/tests/test_lambda.py`, the claims file from foundation B1 | 1 |
| **10a** | **The API, rows 0a to 11** | | **~17.5** |
| 12 | Console: the build list of 6a | `internal/console/templates/lambda.html`, `handlers_lambda.go`, `client_lambda.go`, `routes_compute.go`, `copy.go`, the gateway prefixes (LAM-68) | 6 |
| 13 | Browser tests, including the second identity, 900 wide, light and dark | `e2e/tests/lambda.spec.ts` | 1.5 |
| 14 | Ledger, changelog, user guide, status | `docs/SUPPORT.md`, `docs/lambda.md`, `CHANGELOG.md`, `plan/README.md` | 0.5 |
| **10b** | **The console, rows 12 to 14** | | **~8** |

**Size.** Rows 3 to 5 are a rewrite of the mapping layer, row 7 changes how a
process is supervised, and row 1a changes a tool every service's table uses.
~~Together with 24 boards that is nearer eight days than five.~~ *Corrected
2026-10-11:* about 25 focused days, 20 to 30. The count behind it: 74
findings, of which 35 were known; 19 of 24 boards differing in shape; 36
operations tiered from "absent". If the stage has to be cut, the line is
between rows 0a to 11 and rows 12 to 14, not inside the mapping work: LAM-1 to
LAM-11 and LAM-36 are all P0. Do not cut row 0a or 1a: the first is a feature
that does not work under a documented flag, the second is what keeps the next
model drift from becoming this review again.

**Prior art.** AWS's developer guide is the source for every hand-derived rule
above, and this review read the pages instead of citing them (named in the
review): "Using Lambda with Amazon SQS" and "Creating and configuring an Amazon
SQS event source mapping" for the visibility rule, the six-times advice, the
record shape and the batch rules; "Handling errors for an SQS event source" for
partial batch responses; "Control which events Lambda sends to your function"
for filters; "How Lambda handles errors and retries with asynchronous
invocation" and "Capturing records of Lambda asynchronous invocations" for the
retry waits, destinations and the record; the Kinesis page for starting
position and failure handling; "Manage Lambda function versions" and "Lambda
function states" for what a version freezes, number reuse and the state
model. Each rule's test names its page. The Runtime API reference is what
`internal/lambdaruntime` already implements, and its
`Lambda-Runtime-Deadline-Ms` header is the hook LAM-10 needs. No other
emulator's source was read for this document, so none is cited.
