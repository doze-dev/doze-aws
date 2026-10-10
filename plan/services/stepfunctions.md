# Step Functions

Stage 13. Size L. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds Step Functions' own.

## 1. Summary

Standard and Express state machines over one bbolt store, on AWS JSON 1.0
(target `AWSStepFunctions`, signing name `states`, lowercase-initial members).
The States Language is parsed, analysed and interpreted in `internal/asl`,
which knows nothing about AWS calls; `stepfunctions/` owns the store, one
driver goroutine that steps every execution, the history, and the Task
integrations. The console adds a layout package, `internal/console/sfngraph`.

Peers it calls: Lambda, SQS, SNS, DynamoDB, EventBridge, S3 (Distributed Map
reader and writer), and through `aws-sdk:*` also KMS, SSM, Secrets Manager,
Kinesis, STS, IAM, CloudFormation and itself. Peers that call it: CloudWatch
Logs receives its vended history, CloudWatch its `AWS/States` metrics,
CloudFormation and `provision` create machines, versions, aliases and
activities.

It is L because the contract is two things: 37 operations, and a language. The
operations are in good shape (229 of 229 model constraints enforced). The
findings are almost all in the second half: what the analyser accepts, what
the interpreter does on retry and catch, and what the history records.

**Estimate:** L, five or more focused days. **Actual:** _fill in when done._

## 2. Contract

The model lists 37 operations. All 37 are dispatched (`handlers` in
`stepfunctions/actions_machines.go`) and all 37 have a constraint table
(`stepfunctions/validate.go`). Nothing is S today.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateStateMachine | F | F | Idempotent on an identical definition. Refuses task resources outside the local set (see the integration table). Accepts definitions AWS is believed to refuse: SFN-3, SFN-6. |
| DescribeStateMachine, ListStateMachines | F | F | Describe also answers a version or alias-qualified ARN. |
| UpdateStateMachine | F | F | Running executions keep their frozen definition. Per-setting tier below. |
| DeleteStateMachine | F | F | Immediate, not DELETING (listed difference). Leaves the machine's executions in the store: SFN-1. |
| ValidateStateMachineDefinition | F | F | Ignores `type`, `severity` and `maxResults`; every diagnostic is `ERROR` / `SCHEMA_VALIDATION_FAILED`: SFN-7. |
| PublishStateMachineVersion, DeleteStateMachineVersion, ListStateMachineVersions | F | F | Publishing an unchanged definition answers the existing version (run). |
| CreateStateMachineAlias, DescribeStateMachineAlias, UpdateStateMachineAlias, DeleteStateMachineAlias, ListStateMachineAliases | F | F | One or two versions, weights summing to 100, a weight of 0 allowed (run). |
| CreateActivity, DescribeActivity, DeleteActivity, ListActivities | F | F | Create is idempotent on the name. |
| GetActivityTask | F | F | 60 s long poll. |
| TagResource, UntagResource, ListTagsForResource | F | F | Machines and activities. The per-resource limit is not established. |
| StartExecution | F | F | Machine, version or alias ARN. Express: fire and forget. |
| StartSyncExecution | F | F | Express only. `billingDetails` is nominal (see settings). |
| TestState | F | F | `stateConfiguration`, `variables`, `context`, `revealSecrets`: not established. |
| DescribeExecution, ListExecutions | F | F | An Express machine refuses ListExecutions with `StateMachineTypeNotSupported`. |
| DescribeStateMachineForExecution | F | F | The frozen snapshot. |
| GetExecutionHistory | F | F | A caught failure records no `TaskStateExited`: SFN-5. |
| StopExecution | F | F | Leaves the parked token redeemable: SFN-2. |
| RedriveExecution | F | F | 14-day window, `clientToken` idempotency. |
| SendTaskSuccess, SendTaskFailure, SendTaskHeartbeat | F | F | Model constraints not replayed by the parity test (19 cases): section 3. |
| DescribeMapRun, ListMapRuns, UpdateMapRun | F | F | UpdateMapRun on a finished run is accepted: SFN-13. |

**Settings that "the operation is F" hides**

| Setting | On | Now | Target | To confirm |
|---|---|---|---|---|
| `type` | Create | F | F | STANDARD and EXPRESS behave differently: listing, describing, redrive, token parking, the 5-minute cap. |
| `roleArn` | Create, Update, TestState | C | C | Shape is checked (`validRoleARN`); nothing evaluates it. The create form says so. |
| `loggingConfiguration.level`, `.destinations`, `.includeExecutionData` | Create, Update | F | F | Every history event is vended to the group, filtered by level. |
| Express with logging off | Create | extension | extension | Writes to `/aws/vendedlogs/states/<name>` at ALL. A listed difference. |
| `tracingConfiguration` | Create, Update | C | C | Stored and returned. No X-Ray. Not shown in the console today. |
| `encryptionConfiguration` | Create, Update, CreateActivity | C | C | Stored and returned. No KMS wrapping. Not shown in the console today. |
| `publish`, `versionDescription` | Create, Update | F | F | |
| `tags` | Create, CreateActivity | F | F | Limit and key rules beyond the model's lengths: not established. |
| `traceHeader` | StartExecution, StartSync | C | C | Returned only when given. |
| `billingDetails` | StartSync answer | C | C | Duration rounded up to 100 ms; memory is the constant 64. The ledger does not say so: SFN-17. |
| `includedData` | Describe*, StartSync | F | F | |
| `inspectionLevel`, `mock` | TestState | F | F | A mock on a `.waitForTaskToken` Task still fails `States.Runtime` (run). What AWS answers is not established. |
| `maxConcurrency`, `toleratedFailureCount`, `toleratedFailurePercentage` | UpdateMapRun | F | F | Only `maxConcurrency` was exercised (run). The other two: not established. |
| `error`, `cause` | StopExecution | F | F | The console sends neither: SFN-18. |

**What of the States Language runs**

| Feature | Now | Target | Note |
|---|---|---|---|
| State types: Pass, Task, Choice, Wait, Succeed, Fail, Parallel, Map | F | F | All eight. |
| `Retry`: `ErrorEquals`, `IntervalSeconds`, `MaxAttempts`, `BackoffRate`, `MaxDelaySeconds`, `JitterStrategy` | F | F | Defaults 1, 3, 2.0 (`internal/asl/retry.go` `nextRetry`). A retried parked task keeps its old heartbeat clock: SFN-4. Whether a retry reuses the task token: not established (SFN-12). |
| `Catch` | F | F | SFN-5. |
| Machine `TimeoutSeconds` | F | F | Must be positive. Ends the execution TIMED_OUT. Restarts on redrive. |
| Task `TimeoutSeconds` | F | F | Only checked to be a number; 0 or less means no deadline: SFN-6. |
| Task `HeartbeatSeconds` | F | F | Counts only for a parked task (token or activity); ignored on a synchronous call. Not checked against the timeout: SFN-6. |
| `TimeoutSecondsPath`, `HeartbeatSecondsPath` | not established | F | Parsed and path-checked; whether the interpreter reads them is not established. |
| `Credentials` | not established | C | An allowed Task field; nothing assumes a role locally. |
| Inline Map: `ItemsPath`, `ItemSelector`, `MaxConcurrency`, tolerated failures | F | F | |
| Distributed Map: `ItemReader` (S3 JSON, JSON Lines, CSV, `listObjectsV2`), `ItemBatcher`, `ResultWriter`, `Label` | F | F | Default concurrency 40, not 10,000 (listed difference). |
| JSONPath: paths, `InputPath`, `Parameters`, `ResultSelector`, `ResultPath`, `OutputPath`, context object | F | F | Which `$$` fields beyond `Task.Token` exist: not established. |
| JSONPath `Assign` and `$name` variables | not established | F | The ledger says they run; a comment in `stepfunctions/task.go` (`refuseUnrunnable`) says only JSONata evaluates `Assign`. One of the two is stale. |
| Intrinsics | F | F | All 18: `States.Array`, `ArrayPartition`, `ArrayContains`, `ArrayRange`, `ArrayGetItem`, `ArrayLength`, `ArrayUnique`, `Base64Encode`, `Base64Decode`, `Hash`, `JsonMerge`, `StringToJson`, `JsonToString`, `MathRandom`, `MathAdd`, `StringSplit`, `UUID`, `Format` (`internal/asl/intrinsic_fn.go`). |
| JSONata: `Arguments`, `Output`, `Assign`, `Condition`, `Items`, Wait fields, `$states`, the AWS functions | F | F | `%`, `@` and `#` do not work (listed difference, frozen by `jsonata_dialect_test.go`). |
| Choice operators | F | F | A `Variable` that selects nothing fails `States.Runtime` (run). |

**Which Task resources run**

| Resource | Now | Target | Note |
|---|---|---|---|
| Lambda by function ARN; `lambda:invoke`, `.waitForTaskToken` | F | F | |
| `sqs:sendMessage`, `sns:publish`, each with `.waitForTaskToken` | F | F | |
| `events:putEvents` | F | F | Whether `.waitForTaskToken` is accepted: not established. |
| `dynamodb:getItem`, `putItem`, `updateItem`, `deleteItem` | F | F | |
| Activity ARN | F | F | |
| `states:startExecution`, `.sync`, `.sync:2`, `.waitForTaskToken` | F | F | |
| `aws-sdk:<service>:<action>` for dynamodb, eventbridge, sqs, kms, ssm, secretsmanager, kinesis, sfn, sns, sts, iam, cloudformation | F | F | Encoded from each service's model (`integration_awssdk.go`). The ledger's list omits kms, sts and cloudformation. |
| `aws-sdk:s3:` getObject, putObject, deleteObject, headObject, listObjectsV2; `aws-sdk:lambda:invoke` | F | F | Other actions refused at create. |
| Every other optimized integration, and `.sync` on anything but Step Functions | S | S | Refused at create and update with `UnsupportedOperationException`. Not in `not-built.md` today. Validate accepts them. |
| A callback or activity Task on an Express machine | accepted, fails at run | S at create | SFN-3. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `stepfunctions/`: 90 tests in 24 files (engine, activities, children, map runs, redrive, logging, express, SDK v1 and v2, driver death, volatile leak). `internal/asl/`: 63 tests and 3 fuzz targets in 10 files. `internal/console/sfngraph/`: 8 tests. |
| Parity | `rejection_parity_test.go`, `coverage_model_test.go`. 229 of 229 model constraints enforced across 33 operations, `knownGaps` empty. The other four (SendTaskSuccess, SendTaskFailure, SendTaskHeartbeat, RedriveExecution; 19 cases) are in `needState` and are **not replayed**. |
| Console tests | `internal/console/sfn_test.go` (1), `sfn_more_test.go` (5) |
| Deploy shapes | `cloudformation/sfn_apply_test.go`, `sfn_alias_apply_test.go`; `internal/provision/svc_sfn.go` |
| Conformance | `conformance/tests/test_stepfunctions.py`: 3 scenarios (lifecycle and history, the States Language, refusals), all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/stepfunctions.spec.ts`: 17 tests. `e2e/tests/stepfunctions-sdk.spec.ts`: 9 tests against `@aws-sdk/client-sfn`. |
| Mutation | No per-package baseline (foundation B3). Two packages need one: `stepfunctions/` and `internal/asl/`. |

The model constrains request shapes and says nothing about the definition
string. Everything the analyser enforces is therefore hand-derived, and every
rule in `internal/asl/analyse*.go` is a `doc` claim against the States Language
specification until recorded. No rule there carries its source beside it today.

## 4. Findings

Evidence `run` means reproduced against a binary built from `main` on
2026-10-10, through the console routes or the AWS CLI.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| SFN-1 | P0 | Deleting a machine leaves its executions and history in the store. `DescribeExecution` and the execution page still answer afterwards, and a new machine created under the same name lists the old machine's executions as its own. The console confirm says they go (foundation A13). | `stepfunctions/store_machines.go` `DeleteMachine`; `actions_machines.go` `deleteStateMachine` | run | memory: what AWS keeps of a deleted machine's executions, and for how long, is not established | Engine test: run, delete, recreate the name, `ListExecutions` is empty |
| SFN-2 | P1 | After `StopExecution`, `SendTaskSuccess` and `SendTaskHeartbeat` with the aborted task's token answer 200 and do nothing. The token row is not removed or tombstoned on abort. The console confirm says the token "stays unredeemable" (A13). | `stepfunctions/engine.go` (the abort path that records `ExecutionAborted`); `store_tokens.go` | run | memory: AWS is believed to answer `TaskTimedOut` or `TaskDoesNotExist` | SDK test: park, stop, send; assert a typed error |
| SFN-3 | P0 | An EXPRESS machine whose definition has a `.waitForTaskToken` or activity Task is created without complaint; every run then fails `States.Runtime: .waitForTaskToken is not available to this execution type`. A deploy that AWS is believed to refuse succeeds here. | `stepfunctions/actions_machines.go` `createStateMachine`, `updateStateMachine` (`refuseUnrunnable` ignores `type`); the run-time refusal is `internal/asl/exec.go` `advanceTask` | run | doc, page not yet named: the Standard-versus-Express comparison. Whether AWS refuses at create or at run is not established | Create test, EXPRESS plus a callback Task, skipped pending `express_callback_at_create` |
| SFN-4 | P0 | A parked task that fails and is retried keeps its old heartbeat clock. `Wake` recomputes `Deadline` on a `FrameRetryWait` frame but not `HeartbeatAt`, so the retried attempt fails `States.HeartbeatTimeout` at the next one-second tick. With `HeartbeatSeconds: 5` the second attempt died 1.0 s after it was scheduled. | `internal/asl/exec.go` `Wake`, the `FrameRetryWait` case (compare `advanceTask`, which sets `HeartbeatAt`) | run | memory: each attempt gets a fresh heartbeat interval | `internal/asl` test: Retry on `States.HeartbeatTimeout`, assert the second timeout is a full interval after the second dispatch |
| SFN-5 | P1 | A failure that a Catch handles records `TaskFailed` (or `TaskTimedOut`) and then the next state's `...StateEntered`. No `TaskStateExited` is written. | `internal/asl/exec.go` `deliverFailure` (emits `NoteCaught` only); `stepfunctions/history.go`, the `NoteFailed, NoteRetried, NoteCaught` case | run | memory: AWS writes `TaskStateExited` after a caught error | History test: Catch to a Pass; assert the event sequence. Skipped pending `catch_history` |
| SFN-6 | P0 | The analyser checks a Task's `TimeoutSeconds` and `HeartbeatSeconds` only for being numbers. A negative or zero value and a heartbeat longer than the timeout are accepted; at run a timeout of 0 or less means no deadline. | `internal/asl/analyse_state.go` `analyseState` (no check); `internal/asl/exec.go` `taskDeadline`, `taskHeartbeat` | run | memory: the States Language requires both positive and the heartbeat smaller than the timeout | Analyser test for -5, 0, and heartbeat 100 with timeout 10. Skipped pending `task_timeout_bounds` |
| SFN-7 | P1 | `ValidateStateMachineDefinition` ignores `type`, `severity` and `maxResults`. Every diagnostic is `ERROR` with code `SCHEMA_VALIDATION_FAILED`; there are no warnings and `truncated` is always false. | `stepfunctions/actions_machines.go` `validateDefinition` | read | doc: the API reference defines the three parameters; the code vocabulary is memory | SDK test: `maxResults: 1` on a definition with two problems |
| SFN-8 | P2 | The Validate button answers "Definition is valid — AWS would accept it too" for a definition Create and Save then refuse with `UnsupportedOperationException`. The create page's subtitle says "what passes here deploys". The refusal's "why" line is foundation A2. | `internal/console/templates/sfn.html` `sfn_diagnostics`; `create.html` `sfn_create` | run | n/a | Handler test: validate, then create, `ecs:runTask.sync`; assert the texts |
| SFN-9 | P2 | The name inputs' browser `pattern` leaves out `&`; the service refuses it. Four inputs: machine name, execution name, alias name (which the model limits to `[a-zA-Z0-9_\-.]`, so that pattern is wrong twice over), activity name. | `create.html` `sfn_create`; `sfn.html` Start form; `sfn_more.html` `sfn_versions_tab`, `sfn_activities`; server rule `actions_machines.go` `nameRule` | run (`a&b`) | model for the alias pattern; doc for the name rule | Browser test types `a&b` and an alias `a b` |
| SFN-10 | P1 | `InvalidName` messages have a doubled quote: `Invalid Name: ''a/b' contains a character that is not allowed'`. `checkName` quotes the name and `errInvalidName` quotes the detail. | `stepfunctions/actions_machines.go` `checkName`; `errors.go` `errInvalidName` | run | memory: AWS's message text | Error test on the message. Record the real text first |
| SFN-11 | P2 | The Map Run "Set concurrency" field shows a Go parse error to the user: `strconv.Atoi: parsing "lots": invalid syntax`, as a refusal with no code. | `internal/console/handlers_sfn_ops.go` `sfnMapRunUpdate` | run | n/a | Handler test posts `max=lots` |
| SFN-12 | P1 | A retried `.waitForTaskToken` task is dispatched again with the **same** token (`Wake` re-sends the persisted `TaskInput`, by design, to avoid re-running intrinsics). Whether AWS mints a new token per attempt is not established; if it does, a worker holding the first token can answer the second attempt here and not on AWS. | `internal/asl/exec.go` `Wake` | run | memory | None until `retry_task_token` is recorded |
| SFN-13 | P1 | `UpdateMapRun` on a run that has finished answers 200. The console only disables the button. | `stepfunctions/actions_mapruns.go` `updateMapRun` | run | memory | SDK test, skipped pending `update_finished_map_run` |
| SFN-14 | P2 | In a machine's Logs tab every line's request chip reads `arn:aws:states:us`: the shared log tail shows the first characters of the "request id", which for Step Functions is the execution ARN. The chip still filters correctly. | `internal/console/templates/logs.html` `log_tail` (`.Short`); the Step Functions caller is `handlers_sfn.go` `sfnLogs` | run | n/a | Browser test: two executions, two different chips |
| SFN-15 | P2 | The console reads history with one call of `maxResults: 1000` and no `nextToken`, so an execution with more than 1,000 events shows the first 1,000 and says nothing. The same single page bounds the machine list, the execution list and Map Run children. | `internal/console/client_sfn_history.go` `ExecutionHistory`; `client_sfn.go` `ListStateMachines`, `listExecutions` | read | n/a | Client test with a 1,200-event history (a Map of Pass states) |
| SFN-16 | P2 | "Test a state" runs the editor's unsaved text, but its State select is filled from the saved definition (`stateNames(sm.Definition)`). A state just added in the editor cannot be chosen. | `handlers_sfn.go` `sfnMachine`; `sfn_more.html` `sfn_test_panel` | read | n/a | Browser test: add a state, do not save, open the select |
| SFN-17 | P3 | `billedMemoryUsedInMB` is the constant 64 and the console prints "100 ms · 64 MB" as a measurement. Neither the ledger nor the panel says the figure is nominal. | `stepfunctions/actions_express.go` `startSyncExecution`; `sfn_more.html` `sfn_sync_result` | read | n/a | Ledger test: the row exists |
| SFN-18 | P2 | The console cannot do what the API can: no logging, tracing, role or tag input on create or on the Definition tab (the save sends `definition` only); activities have no tags; `sfnStop` reads `error` and `cause` from a form that has no such fields. | `handlers_sfn.go` `sfnUpdateDefinition`, `sfnStop`; `sfn.html`; `create.html` | read | n/a | Decide per item in step 5; a browser test for each one built |
| SFN-19 | P3 | `InvalidExecutionInput` and `InvalidOutput` carry Go's JSON decoder text (`invalid character 'o' looking for beginning of object key string`). The code is right; the message is not AWS's. | `actions_executions.go` `startExecution`; `actions_tasks.go` `sendTaskSuccess`; `actions_teststate.go` | run | memory | Record `invalid_input_message`, then decide |
| SFN-20 | P3 | Two documents disagree about JSONPath `Assign`: the ledger says it runs, the comment on `refuseUnrunnable` says only JSONata evaluates it and that create refuses it (it does not; `refuseUnrunnable` only checks resources). | `stepfunctions/task.go` `refuseUnrunnable`; `docs/SUPPORT.md` | read | n/a | Interpreter test for a JSONPath `Assign` read by a later state; then fix whichever text is wrong |
| SFN-21 | P3 | The ledger's `aws-sdk` service list omits kms, sts and cloudformation, which `sdkServices` serves. | `docs/SUPPORT.md`; `stepfunctions/integration_awssdk.go` `sdkServices` | read | n/a | Ledger test over `sdkServices` |

Owned by the foundation, and referenced, not repeated:

- **A2** — the "doze-aws refuses this because AWS does" line under
  `UnsupportedOperationException`, which is the plan's own example of "Refused
  here". Step Functions must supply the fact that tells the two apart: the
  refusal came from `refuseUnrunnable`, not from the analyser or the model.
- **A7** — `copy.go`'s "Select a state machine…" line never renders;
  `sfnMachines` opens the first machine.
- **A12** — "Execution stopped" for an execution that had already finished;
  "Published version 1" and "Alias created" for a repeat that changed nothing;
  "Version 5 deleted" for a version that never existed.
- **A13** — the delete and stop confirms (SFN-1 and SFN-2 are the behaviour
  behind them; the sentence is fixed in whichever direction those are decided).

## 5. What to check

Beyond the common criteria. Step Functions speaks one protocol, so each line is
checked once, through the Go SDK v2 unless it says otherwise.

**Machines**
- [ ] Name: 1 to 80, the forbidden character set, and the message text (SFN-10).
- [ ] Re-create with an identical definition, role and type succeeds; with any
      one of the three changed it answers `StateMachineAlreadyExists`. Whether
      a changed role or changed tags alone conflicts is not established.
- [ ] `roleArn`: the shape rule, on create, update and TestState.
- [ ] `type` cannot be changed by update.
- [ ] Update with nothing but the ARN: AWS is believed to answer
      `MissingRequiredParameter`. Establish what happens here.
- [ ] Delete: twice, with a running execution, and what remains readable (SFN-1).
- [ ] `loggingConfiguration`: each level, `includeExecutionData` false, a
      destination that does not exist, and Express with logging off.
- [ ] `tracingConfiguration` and `encryptionConfiguration` round-trip and say
      cosmetic in the ledger.

**The analyser** (`internal/asl/analyse*.go`). One test per rule, each naming
the page of the States Language specification it comes from.
- [ ] `StartAt` present and naming a state; at least one state; unreachable states.
- [ ] `Next` or `End`, never both, never neither; Succeed and Fail take neither;
      Choice takes neither.
- [ ] Each state type's allowed fields; a misplaced field is named.
- [ ] Choice: non-empty `Choices`, top-level `Next`, one combinator, operand
      type per operator, timestamps, `...Path` operands.
- [ ] Wait: exactly one of the four; `Seconds` not negative.
- [ ] Retry and Catch: only on Task, Parallel, Map; `ErrorEquals` non-empty;
      `States.ALL` alone and last; reserved `States.*` names; `MaxAttempts` not
      negative; `BackoffRate` at least 1; `IntervalSeconds`, `MaxDelaySeconds`
      and `JitterStrategy` bounds (not checked today; establish AWS's).
- [ ] Machine `TimeoutSeconds` positive; Task `TimeoutSeconds` and
      `HeartbeatSeconds` positive and ordered (SFN-6).
- [ ] Paths and reference paths; `ResultPath` may not write to the context object.
- [ ] JSONata: a JSONPath-only field on a JSONata state and the reverse; no
      downgrade to JSONPath inside a JSONata machine.
- [ ] Type-dependent rules: what an EXPRESS machine may not contain (SFN-3).
- [ ] Limits the analyser does not have today and AWS is believed to: state
      name length (80), definition size (the model's 1,048,576 is enforced),
      `MaxConcurrency` bounds. Establish each.
- [ ] Validate and Create agree, or the disagreement is exactly the "Refused
      here" set and both say so (SFN-7, SFN-8).

**The interpreter**
- [ ] Each of the 18 intrinsics against AWS's documented examples, and its
      failure name (`States.IntrinsicFailure`).
- [ ] `InputPath` null and absent; `ResultPath` null; `ResultPath` merges into
      the raw input; `OutputPath`.
- [ ] Context object: every `$$` field a definition can read. List them and
      set each to F or refused.
- [ ] JSONPath `Assign` and variables (SFN-20).
- [ ] Choice: no match and no Default is `States.NoChoiceMatched`; a missing
      variable is `States.Runtime`; `IsPresent`.
- [ ] Wait: seconds, timestamp, both paths, a timestamp in the past.
- [ ] Parallel: one branch fails, the others are cancelled, the error is
      `States.BranchFailed` or the branch's own. Establish which.
- [ ] Map: `MaxConcurrency` 0 and 1, tolerated failures, `ItemSelector`,
      an empty array, a non-array `ItemsPath`.
- [ ] Payload limit: a state output over 256 KiB is `States.DataLimitExceeded`.
      Not established here.

**Retry, Catch and timeouts**
- [ ] Backoff: interval times rate to the power of attempts, `MaxDelaySeconds`,
      `JitterStrategy: FULL`.
- [ ] `MaxAttempts: 0` means never retry; an exhausted retrier does not fall
      through to the next.
- [ ] `States.TaskFailed` matches everything but `States.Timeout`; `States.ALL`
      matches everything. Whether `States.ALL` catches `States.Runtime` and
      `States.DataLimitExceeded` on AWS is not established.
- [ ] Each retry attempt gets a fresh timeout **and** a fresh heartbeat (SFN-4).
- [ ] The task token across a retry (SFN-12).
- [ ] The history of a retry and of a catch, event by event (SFN-5).
- [ ] The machine timeout fires during a Wait, during a parked task, and during
      a retry wait; the execution ends `TIMED_OUT` with `States.Timeout`.

**Executions**
- [ ] Start: name rules, generated name, input not JSON, input over 262,144,
      same name and same input while running, same name after it finished.
- [ ] Start through a version and through an alias; both ARNs on the execution;
      weighted routing with a fixed seed.
- [ ] Stop: running, finished, twice; with `error` and `cause`; its Map Runs;
      a `.sync` child keeps running; the token afterwards (SFN-2).
- [ ] Redrive: FAILED, ABORTED, TIMED_OUT; refused when running, succeeded,
      Express, older than 14 days, nothing left to redrive; `clientToken`;
      the redriven task's token and input.
- [ ] `ListExecutions`: each status filter, `redriveFilter`, `mapRunArn`,
      a version or alias ARN, paging, a stale token.
- [ ] `GetExecutionHistory`: paging forward and reversed, `includeExecutionData`
      false, event ids and `previousEventId` chains in a Parallel.
- [ ] Express: `StartExecution` answers an ARN nothing can describe;
      `StartSyncExecution` on a Standard machine; the 5-minute cap; the host
      prefix under `--listen`.

**Task tokens and activities**
- [ ] `SendTaskSuccess` with output that is not JSON, over 262,144, and empty.
- [ ] `SendTaskFailure` with no error; the error name reaches Retry and Catch.
- [ ] A token used twice; a token after its task timed out (one day of
      `TaskTimedOut`, then `TaskDoesNotExist`); after stop; after the machine
      is deleted.
- [ ] The 19 model cases for the four `needState` operations, replayed with a
      fresh token or execution per case instead of skipped.
- [ ] `GetActivityTask`: FIFO, one worker per task, the empty poll, a cancelled
      request, a deleted activity, `workerName` over 80.
- [ ] `ActivityScheduled`, `ActivityStarted`, `ActivitySucceeded`,
      `ActivityFailed`, `ActivityTimedOut`, `ActivityScheduleFailed` in the history.
- [ ] Heartbeat counted from schedule, not from the worker's claim.

**Versions, aliases, tags**
- [ ] Publish: unchanged definition, `revisionId` mismatch (`ConflictException`),
      description length.
- [ ] Delete a version an alias routes to; delete one that does not exist.
- [ ] Alias: name pattern, an integer name, one route not 100, two routes not
      summing to 100, the same version twice, a third route, a missing version,
      a repeat create with the same and with a different routing.
- [ ] Tags on a machine and an activity: add, overwrite, remove, an ARN nothing
      holds, the per-resource limit (not established), key and value lengths.

**Map Runs**
- [ ] One run per Distributed Map state; counts move; `ListExecutions` hides
      children without `mapRunArn`.
- [ ] `UpdateMapRun`: each of the three settings, on a running and a finished
      run (SFN-13).
- [ ] The `ResultWriter` manifest and files; the reader on a missing object
      (`States.ItemReaderFailed`).

**Lifecycle**
- [ ] A restart resumes a Wait, a Lambda in flight (re-dispatched), a parked
      token, an activity task queued and claimed, a child being waited on, a
      Distributed Map mid-fan-out, a retry wait, and a tombstoned token.
- [ ] An Express run and a TestState run do not survive a restart, and nothing
      leaks (`volatile_leak_test.go` holds this; keep it).

## 6. Console

The design's "Step Functions" page: 26 boards. Templates: `templates/sfn.html`,
`sfn_more.html`, `sfn_graph.html`, the `sfn_create` block in `create.html`,
shared pieces in `panes.html`, `layout.html` and `logs.html`. Handlers:
`handlers_sfn.go`, `handlers_sfn_ops.go`, `handlers_sfn_versions.go`,
`handlers_sfn_activities.go`; routes in `routes_compute.go` `sfnRoutes`.

The older single board `Machine` is dropped. It drew per-state milliseconds on
graph nodes, a state inspector with Send buttons on the graph, and a dock of
"calls this execution made". None of the three exists or is in this plan.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Sfn-Home | Machines, none open | `sfn_home`, `empty_detail` | the line exists and never renders (A7) |
| Sfn-01-None | No state machines yet | `sfn_home`, `empty_detail` | exists |
| Sfn-02-Create | Create, Standard | `sfn_create`, `sfnCreate`, `sfnValidate` | form exists; teaching lines and the rule digest are **new** |
| Sfn-03-Create-Express | Create, Express | `sfn_create` | form exists; the log-group line and "what Express changes" are **new** |
| Sfn-04-Create-Refused | Validate's list, and Create refused | `sfn_diagnostics`, `fail_inline` | exists; the "Refused here" wording is **new** (A2) |
| Sfn-05-Executions | Executions | `sfn_machine`, `sfn_executions` | exists |
| Sfn-06-Executions-Empty | Nothing run; filter empty; Express | `sfn_executions`, the Express branch of `sfn_machine` | exists |
| Sfn-07-Start | Start execution | `sfn_machine` (tab `start`), `sfnStart` | form exists; teaching lines and "where it begins" are **new** |
| Sfn-08-Start-Refused | Start refused | `fail_inline` | exists |
| Sfn-09-Express-Run | Run synchronously, result, failed run | `sfn_sync_result`, `sfnStartSync` | exists |
| Sfn-10-Graph | The definition drawn | `sfn_graph`, `sfn_graph_svg` | exists; the legend is **new** |
| Sfn-11-Definition | Definition | `sfn_machine` (tab `definition`), `sfnUpdateDefinition` | editor exists; the strip and "what is checked" are **new** |
| Sfn-12-Definition-Test | Save refused; Test a state | `sfn_test_panel`, `sfn_test_result`, `sfnTestState` | exists |
| Sfn-13-Versions | Versions and aliases | `sfn_versions_tab`, `sfn_version_detail`, `handlers_sfn_versions.go` | exists; teaching lines are **new** |
| Sfn-14-Logs | Logs, off and Express default | `sfn_machine` (tab `logs`), `log_tail`, `sfnLogs` | exists; the execution-name chip is **new** (SFN-14) |
| Sfn-15-Tags | Tags | `tags_panel`, `tag_editor` | exists (A5) |
| Sfn-16-Execution-Running | History, a parked task, the result form | `sfn_execution`, `sfn_history`, `sfn_task_form` | exists; "waiting on a token" is **new**; the form moves beside the table |
| Sfn-17-Execution-Graph | Graph over the frozen definition | `sfn_graph_live`, `sfnGraph` | exists |
| Sfn-18-Task-Result | Success, failure, heartbeat, refusals | `sfn_task_form`, `sfnTaskResult`, `sfnHeartbeat` | exists; "where it lands" is **new** |
| Sfn-19-Execution-Failed | Failed; redrive | `sfn_execution`, `sfnRedrive` | exists; the redrive reason inline is **new** |
| Sfn-20-Execution-IO | Input and output; definition at start | `sfn_execution` (tabs `io`, `definition`) | exists; "the machine has moved on" is **new** |
| Sfn-21-Activities | Activities | `sfn_activities`, `handlers_sfn_activities.go` | exists |
| Sfn-22-Activity-Task | Take a task, answer it | `sfn_activity_task`, `sfn_activity_done` | exists |
| Sfn-23-Map-Run | A Map Run and its children | `sfn_mapruns`, `sfn_map_children` | exists |
| Sfn-24-Confirms | Six confirms | `hx-confirm` in `sfn.html`, `sfn_more.html` | exist; two sentences are false today (A13) |
| Sfn-25-Bad-Heartbeat | A task nobody answered | `sfn_history` | exists; "why it failed" is **new** |

There is no Denied board. A state machine has no resource policy and the
console acts as root, so no Step Functions console action can meet a 403.

**New items, and what each is built from**

| Item | Board | Built from |
|---|---|---|
| Teaching lines under name, role, type, input, alias weights | 02, 03, 07, 13 | The settings table (foundation B4): lengths from `stepfunctions/validate.go`, tiers from section 2 |
| "What Validate reports" | 02 | A list held next to the analyser, one entry per rule with its message format, generated or tested against `internal/asl/analyse*.go` so it cannot drift. It is typed prose on the board today |
| "Its history will go to /aws/vendedlogs/states/<name>" | 03 | The name field and the type; the group name is `client_sfn.go` `DescribeStateMachine`'s fallback |
| "Refused here" wording for unsupported resources | 04, 12 | The refusal's source, per foundation A2 |
| Where it begins | 07 | `StartAt` and the first transitions of the saved definition |
| The life of one waiting task | 11 | The chosen Task's `TimeoutSeconds`, `HeartbeatSeconds`, `Retry`, `Catch`, and the machine's `TimeoutSeconds`. A pure function of the definition |
| Graph legend | 10 | Static; each sentence is `sfngraph`'s own rule (`Start`, `Kind`, edge kinds, `Container`) |
| The execution name as the log chip | 14 | The execution ARN on each record |
| Waiting on a token | 16 | The last `TaskScheduled` or `ActivityScheduled` event's time, and the frozen definition's heartbeat and timeout |
| Where it lands | 18 | The frozen definition's `Retry` and `Catch` for the parked state, matched with the rules in `internal/asl/retry.go` |
| The redrive reason shown inline | 19 | `DescribeExecution`'s `redriveStatusReason`, which is a hover title today |
| The machine has moved on | 20 | `revisionId` from `DescribeStateMachineForExecution` against `DescribeStateMachine` |
| Counts in the delete confirm | 24 | `ListStateMachineVersions`, `ListStateMachineAliases`, `ListExecutions` |
| Why it failed | 25 | The failing event, the scheduling event before it, and the frozen definition |

**Where each teaching line's facts come from**

| Line | Source |
|---|---|
| Name 1 to 80 and the character set | `nameRule`, `checkName`; model length |
| Role ARN required, shape checked, not evaluated | `validRoleARN`; section 2 tier C |
| Standard and Express differences | `actions_executions.go` (`listExecutions`, `executionOf`), `actions_express.go`, `express.go` `expressMaxDuration`, `actions_redrive.go` `redrivable` |
| Input up to 262,144, JSON, empty means `{}` | model; `startExecution` |
| Same name, same input, still running answers the original | `startExecution` |
| Alias weights, an integer name | model; `actions_aliases.go` |
| Publishing an unchanged definition | run; `actions_versions.go` |
| Task timeout and heartbeat semantics | `exec.go` `taskDeadline`, `taskHeartbeat`; `scheduler.go` `fireDue` |
| Retry defaults and backoff | `retry.go` `nextRetry` |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Sfn-03: "AWS documents both as Standard-only" (SFN-3; `doc`, page not named).
- Sfn-11: the strip says a retry gets "a fresh 300 s". True for the timeout
  (run). It is silent on the heartbeat because of SFN-4; once that is fixed the
  sentence gains "and a fresh 60 s".
- Sfn-17: the `↻1` and `2×` marks are drawn from `sfn_graph_svg` and
  `overlay.go`, not from a rendered run. The legend also keeps "caught", which
  cannot appear until SFN-5 is decided.
- Sfn-19: "Another tab redrove it first" as the cause of
  `ExecutionNotRedrivable: the execution succeeded`.
- Sfn-24: the delete confirm states today's behaviour (executions are kept).
  That is SFN-1, a bug. The sentence is rewritten when SFN-1 is fixed; the
  board is not a reason to keep the behaviour. The stop confirm's "Redrive can
  start it again" is run-verified.
- Sfn-25: the 60-second history is modelled on a 2-second run. The event order
  is real; the timings are not.
- Every board: execution names, counts and ages are illustrative.

## 7. Tests to add

| Layer | Tests |
|---|---|
| `internal/asl` | Heartbeat after retry (SFN-4); catch notes and the exit they should produce (SFN-5); Task timeout bounds (SFN-6); JSONPath `Assign` (SFN-20); one test per analyser rule naming its source; the token across a retry (SFN-12) |
| Engine, store | Delete then recreate (SFN-1); token after stop (SFN-2); Express plus callback at create (SFN-3); UpdateMapRun on a finished run (SFN-13) |
| SDK v2 | Validate with `maxResults`, `severity`, `type` (SFN-7); the `InvalidName` text (SFN-10); every row of section 5's Executions and Versions blocks |
| Rejection parity | The four `needState` operations, with a fresh token or execution per case |
| Restart | Retry wait, tombstoned token, claimed activity task, Map Run mid-flight |
| Conformance (boto3) | Retry and catch history; heartbeat and timeout; callback round trip; activity round trip; Express sync; versions and aliases; redrive; Distributed Map; each intrinsic; validate diagnostics; every row of section 8 |
| Console handlers | Validate then create disagreeing (SFN-8); `max=lots` (SFN-11); history over 1,000 events (SFN-15); each confirm's sentence against the store |
| Browser | `a&b` in each name field (SFN-9); the log chip (SFN-14); a state added and tested unsaved (SFN-16); each `new` item; Express executions tab; take a task and answer it |
| Ledger | `sdkServices` against the documented list (SFN-21); `billingDetails` row (SFN-17); unsupported integrations in `not-built.md` |
| Mutation | Baselines for `stepfunctions/` and `internal/asl/`; then survivors in `retry.go`, `exec.go`, `analyse_state.go`, `scheduler.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today.

| Claim | Source now | Scenario |
|---|---|---|
| An EXPRESS machine with a `.waitForTaskToken` or activity Task is refused, and where (create or run) | doc, page not named | `express_callback_at_create` |
| Task `TimeoutSeconds` and `HeartbeatSeconds` must be positive, and heartbeat below timeout | memory | `task_timeout_bounds` |
| A retried task gets a fresh heartbeat interval | memory | `retry_heartbeat_clock` |
| A retried callback task gets a new token | memory | `retry_task_token` |
| A caught failure's history includes `TaskStateExited` | memory | `catch_history` |
| A retry's history: which events between the two `TaskScheduled` | memory | `retry_history` |
| A token after `StopExecution` | memory | `token_after_stop` |
| `TaskTimedOut` for a timed-out token, and for how long | doc | `late_token` |
| A deleted machine's executions: describable, listable, and under a recreated name | memory | `delete_then_recreate` |
| `DeleteStateMachine` leaves the machine `DELETING` while executions run | doc | `delete_with_running` |
| `InvalidName` message text | memory | `invalid_name_message` |
| `InvalidExecutionInput` and `InvalidOutput` message text | memory | `invalid_input_message` |
| Validate: diagnostic codes, warnings, `severity`, `maxResults`, `truncated`, and whether `type` changes the answer | doc | `validate_diagnostics` |
| Every analyser rule's message and location as AWS words it | doc (States Language specification) | `definition_refusals` (extend `test_refusals`) |
| `StateMachineAlreadyExists` on a changed role or changed tags alone | memory | `recreate_changed_role` |
| `UpdateStateMachine` with no changes | memory | `update_nothing` |
| Publishing an unchanged definition answers the existing version | memory | `publish_unchanged` |
| A repeat `CreateStateMachineAlias`, same and different routing | memory | `alias_repeat` |
| `UpdateMapRun` on a finished run | memory | `update_finished_map_run` |
| TestState on a `.waitForTaskToken` Task, with and without a mock | doc, page not named | `test_state_callback` |
| `States.ALL` and `States.Runtime`, `States.DataLimitExceeded` | memory | `catch_all_scope` |
| A Parallel branch failure: the error name the parent sees | memory | `parallel_branch_failure` |
| A Choice whose variable selects nothing fails `States.Runtime` | doc (the ledger cites it as AWS's) | `choice_missing_variable` |
| The per-resource tag limit and its error | memory | `tag_limit` |
| `StopExecution` on a finished execution answers its stop date | memory | `stop_finished` |
| Same name, same input, still running answers the original execution | doc | `start_idempotent` |
| The 18 intrinsics' results and failure names | doc | `intrinsics` (extend `test_the_states_language`) |

## 9. Owed to integration

| What | With |
|---|---|
| A Lambda Task: the handler's own error name reaches Retry and Catch; `Lambda.TooManyRequestsException`; a function that does not exist | Lambda |
| A Lambda in flight is invoked again after a restart, and the function sees it twice | Lambda |
| Task token to a queue, a worker reads it and calls `SendTaskSuccess` (already listed in 90-integration.md) | SQS, Lambda |
| `sqs:sendMessage` to a FIFO queue needs `MessageGroupId`; the refusal's name in the history | SQS |
| `sns:publish` and `events:putEvents` Tasks deliver, and the delivery shows on the wire as caused by the execution | SNS, EventBridge |
| The four optimized DynamoDB actions, and a conditional check failure as `DynamoDB.ConditionalCheckFailedException` | DynamoDB |
| Distributed Map over an S3 object and over a listing; the `ResultWriter` files | S3 |
| `aws-sdk:*` to each of the fourteen services, one action each | all |
| History vended to a log group; `aws logs tail` reads it; a subscription filter on that group | CloudWatch Logs |
| `AWS/States` metrics, and an alarm on `ExecutionsFailed` | CloudWatch |
| An EventBridge rule or an API Gateway integration starting an execution | EventBridge, API Gateway |
| Under enforce: the execution role is not evaluated; say so or evaluate it. A decision for stage 15 | IAM |
| Machine, version, alias and activity through CloudFormation, SAM, CDK and Terraform; a definition change is an update, not a replace | CloudFormation |
| The neighbours strip: the wiring graph has no Step Functions node, so a machine shows no neighbours. Whether to add one is foundation D's call | console |
| Many parked executions across a restart | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] SFN-1 to SFN-21 each closed, with the test that failed first named beside
      it, or moved to section 8 with its test written and skipped.
- [ ] Decided and written into `SUPPORT.md`: what delete does to executions
      (SFN-1); the token after stop (SFN-2); Express plus callback (SFN-3);
      whether a retry reuses the token (SFN-12); whether a catch writes
      `TaskStateExited` (SFN-5).
- [ ] The settings table in section 2 has no "not established" left:
      `stateConfiguration`, `variables`, `context` and `revealSecrets` on
      TestState; the two tolerated-failure settings on UpdateMapRun; the tag
      limit; `events:putEvents.waitForTaskToken`.
- [ ] The language table in section 2 has no "not established" left:
      `TimeoutSecondsPath` and `HeartbeatSecondsPath`; `Credentials`; the
      context object's fields; JSONPath `Assign` (SFN-20).
- [ ] Every analyser rule has a test naming its source, and no rule in
      `internal/asl/analyse*.go` is without one.
- [ ] Validate and Create disagree only on the "Refused here" set, the ledger
      lists that set, and the console says "AWS would accept this" for it and
      nothing else (SFN-7, SFN-8, A2).
- [ ] Every unsupported integration family is a row in `not-built.md`.
- [ ] The four `needState` operations' 19 model cases are replayed; the ledger's
      "33 of 37" becomes "37 of 37" or says why not.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] All 26 boards implemented from the foundation's shell, and each `new`
      item reads only the data named for it. The rule digest on Sfn-02 is
      generated or held by a test, not typed.
- [ ] No statement on a Step Functions screen is in the "not yet backed" list
      in section 6.
- [ ] The six confirms say only what the code does after SFN-1 and SFN-2.
- [ ] The history, the machine list and the execution list page past 1,000 or
      say they are truncated (SFN-15).
- [ ] The four name inputs' patterns equal the service's rules (SFN-9).
- [ ] `e2e/tests/stepfunctions.spec.ts` covers every route under `/sfn` (33
      routes) with the route gate on, and the accessibility sweep is clean on
      the seven machine tabs, the four execution tabs, create and activities.
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baselines recorded here: `stepfunctions/` ____ killed, ____
      survived; `internal/asl/` ____ killed, ____ survived, with reasons.
- [ ] Idle heap unchanged: the analyser's rule list for the console is built
      on first use.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: setting, language and integration tiers; the five decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for SFN-1 to SFN-21 | `internal/asl/*_test.go`, `stepfunctions/*_test.go`, `internal/console/sfn_test.go`, `sfn_more_test.go` |
| 3 | Interpreter: heartbeat on retry, catch history, timeout bounds | `internal/asl/exec.go`, `analyse_state.go`, `analyse_jsonata.go`, `retry.go`; `stepfunctions/history.go`, `scheduler.go` |
| 4 | Store and engine: delete, tokens on abort, Express at create, finished Map Runs | `stepfunctions/store_machines.go`, `store_executions.go`, `store_tokens.go`, `engine.go`, `actions_machines.go`, `task.go`, `actions_mapruns.go` |
| 5 | Validate's parameters; message texts | `stepfunctions/actions_machines.go`, `errors.go`, `actions_executions.go`, `actions_tasks.go` |
| 6 | Section 5 sweep; the `needState` cases | `stepfunctions/rejection_parity_test.go`, `sdk_test.go`, `sdk_errors_test.go`, `sdk_versions_test.go`, `sdk_activity_test.go`, `sdk_express_test.go`, `redrive_test.go`, `maprun_test.go`, `internal/asl/analyse_test.go`, `exec_test.go` |
| 7 | Conformance scenarios and claims table | `conformance/tests/test_stepfunctions.py`, the claims file from foundation B1 |
| 8 | Console | `internal/console/templates/sfn.html`, `sfn_more.html`, `sfn_graph.html`, `create.html` (`sfn_create`), `handlers_sfn.go`, `handlers_sfn_ops.go`, `handlers_sfn_versions.go`, `handlers_sfn_activities.go`, `client_sfn.go`, `client_sfn_history.go`, `client_sfn_ops.go`, `client_sfn_versions.go`, `sfngraph/overlay.go`, `static/graph.js` |
| 9 | Browser tests | `e2e/tests/stepfunctions.spec.ts` |
| 10 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Step 3 comes before step 4 because SFN-4, SFN-5 and SFN-6 are in the
interpreter, which has no store and is the cheapest place to make them fail.

**Prior art.** The Amazon States Language specification (states-language.net)
is the source for every rule in step 3 and the page each analyser test should
name. AWS's own `ValidateStateMachineDefinition` is the oracle for the
analyser once an account exists: it costs nothing to call and returns the
diagnostics directly, so `definition_refusals` can record every rule in one
scenario. Nothing else is cited here; no other emulator's handling of these
cases was read for this document.
