# Step Functions

Stage 13. Size **XL** (was L; see section 1). Follows
[../01-protocol.md](../01-protocol.md); the common acceptance criteria there
apply in full, and this page adds Step Functions' own.

Read "Pre-stage review" first: it says what was read, what in this document was
wrong, and which decisions need a person before the stage can start.

## Pre-stage review (2026-10-11)

Done before the stage, so that it starts from facts. The lessons it applies are
[../02-lessons-from-sqs.md](../02-lessons-from-sqs.md) (L1 to L12).

**What was read and run.**

- The model, `.audit-models/sfn.json`: the `documentation` string of all 37
  operations and of every member of every input, output and error shape, each
  operation's `errors`, every error shape's `httpError`, the enums, the
  `length`/`range`/`pattern` traits, and the service traits (`awsJson1_0`,
  signing name `states`, `endpointPrefix` `states`, `hostPrefix` `sync-` on
  `StartSyncExecution` and `TestState`). Tags in the documentation were stripped
  with a whitelist, because a naive strip deletes `< > { } [ ]` (the model's
  name rule lists them) and the `<partition>` placeholders in its ARN examples.
- Three AWS pages, actually fetched: the States Language specification
  (`states-language.net/spec.html`), the Step Functions developer guide's
  *Handling errors* page (`concepts-error-handling.html`) and *Task workflow
  state* page (`state-task.html`). These are the `doc` sources below. Nothing
  else is `doc`.
- The code: `stepfunctions/` (every `actions_*.go`, `store_*.go`, `engine.go`,
  `scheduler.go`, `activity.go`, `task.go`, `history.go`, `validate.go`,
  `errors.go`), `internal/asl/` (`exec.go`, `retry.go`, `analyse*.go`,
  `payload.go`, `redrive.go`, `errors.go`), the console (`handlers_sfn*.go`,
  `client_sfn*.go`, `templates/sfn*.html`, `routes_compute.go`), `docs/SUPPORT.md`
  and `testdata/mutation.json`.
- A binary built from the working tree (2026-10-11), driven over the wire on a
  scratch data dir: about 120 calls, every one named in the findings as `run`.
- All 26 boards (`Sfn-Home` and `Sfn-01` to `Sfn-25`) rendered at 1440 by 900 and
  looked at, and the real page in the same state for all but two (Sfn-06's
  Express half and Sfn-22's third state, which need a state the seed could not
  hold; named in 6a). Light and dark for create, the executions list and a
  running execution; 900 wide for the machine page and an execution.

**What this review changed.**

- **39 claims in this document were wrong or unsourced and are corrected in
  place** (24 marked *Corrected 2026-10-11*, 6 findings re-sourced from `memory`
  or `doc` to `model` or `doc`, 1 finding resolved, 8 recording-queue rows
  re-tagged). The ones that mattered:
  `CreateStateMachine` idempotency (the model says a different role and different
  tags are ignored; the document and the code made a changed role a conflict);
  "14-day redrive window" (the model counts from when the execution finished, the
  code from when it started); "no per-package mutation baseline" (both exist,
  recorded 2026-10-10); `encryptionConfiguration` on an activity "stored and
  returned" (neither); `includedData` "F on Describe" (only `StartSyncExecution`
  honours it); "TestState: not established" (the model establishes all four
  parameters, and the code ignores all four); "heartbeat counted from schedule"
  (the developer guide says from the worker's claim).
- **32 findings added: SFN-22 to SFN-48 from the model's prose, and SFN-49 to
  SFN-53 from the three guide pages**: 3 P0, 17 P1, 7 P2, 5 P3 (SFN-1 to SFN-21
  are unchanged in number, and eight of them were re-sourced or sharpened; see
  section 4). The three new P0s are a failure AWS does not produce (a changed
  role on a repeat create, SFN-23), a silent no-op that hangs an execution
  (`TimeoutSecondsPath`, SFN-36) and a catch-all that swallows a runtime error
  AWS fails on (SFN-49).
- **11 boards were corrected in the working copy** for stale or unsourced facts
  (list in 6a).
- **Size changed from L to XL.** Section 1.

### L1 to L12, answered

| | Answer |
|---|---|
| **L1** model documentation first | **Findings** SFN-22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 37, 38, 40, 42, 43, 44, 46, 48; and re-sourced SFN-1, 2, 7, 13. The document cited 262,144 for inputs and outputs; the model confirms it **in bytes, UTF-8** for `input`, `output`, `mock.result`, `context` and `variables` (`SensitiveData`), 32,768 for `cause`, 256 for `error`, **1,048,576 for the activity task's input as the worker receives it** (`SensitiveDataJobInput`) and for `definition`. Every number in sections 2, 5 and 6 was checked; corrections are marked. |
| **L2** optional parameters have meanings | **Findings** SFN-24 (`UpdateStateMachine`: at least `definition` or `roleArn`), 27 (`maxResults` default 100), 28 (`ListExecutions` filters that require or forbid each other), 30 (`includedData`), 33 (`routingConfiguration`, aliases), 34 (`revisionId: INITIAL`), 35 (`TestState`: `context` only with `mock`; `stateName` means a whole machine), 26 (`CreateActivity` repeat), 23 (`CreateStateMachine` repeat), 36 (the `...Path` fields). Read: every member of all 37 inputs for "if you don't", "only", "optional", "default", "must", "can't", "at most", "required". |
| **L3** error codes | **Findings** SFN-22 (`TaskTimedOut` vs `TaskDoesNotExist`; `InvalidToken` never answered), 24 (`MissingRequiredParameter`), 25 (`InvalidLoggingConfiguration`, `InvalidTracingConfiguration`, `InvalidEncryptionConfiguration`), 31 (`ValidationException` with reason `API_DOES_NOT_SUPPORT_LABELED_ARNS`), 32 (the `reason` member), 33 (`ServiceQuotaExceededException`), 44 (`ResourceNotFound` is 404, `ServiceQuotaExceededException` 402), 45 (`UnsupportedOperationException` is not in the model). Errors the code never produces and the model lists: `StateMachineDeleting`, `ExecutionLimitExceeded`, `StateMachineLimitExceeded`, `ActivityLimitExceeded`, `ActivityWorkerLimitExceeded`, `ActivityAlreadyExists`, `TooManyTags`, `InvalidToken` (task tokens), `Conflict` on a concurrent publish. The quota values are not in the model (the documentation says "see the Limits topic"), so those five are listed differences, not findings, except the two the operation documentation states (100 aliases, 1,000 versions: SFN-33). |
| **L4** direction | Named on every row of section 4. Stricter than AWS: SFN-23 (a changed role), 34, 38, 50, 52. More forgiving: SFN-24, 25, 27, 28, 31, 33, 40, 43, 49, 51. Both: SFN-23. |
| **L5** every wire | **Checked, none.** The model declares one protocol, `awsJson1_0`; there are no `xmlName` or `xmlFlattened` members. The request is validated whole (`modelcheck.ValidateMap` over the decoded body). What was read: `ServeHTTP`, `awsjson.API{TargetPrefix: "AWSStepFunctions", JSONVersion: "1.0"}`; the `sync-` host prefix on `StartSyncExecution` and `TestState` is served under `sync-aws.<instance>.doze` (`cmd/doze-aws/names_test.go`) and **not** under `--listen`, where a client needs `disableHostPrefix` (ledger). Timestamps are epoch seconds with a fraction (model: Timestamp in awsJson). One wire fact is wrong: the error status (SFN-44). |
| **L6** tokens are per use | **Findings** SFN-22 (task token: a closed task's token, a garbage token), 2 (token after stop; re-sourced), 27 (pagination token: an offset, accepted across lists; 24-hour expiry unmodelled), 34 (`revisionId`), 38 (`clientToken`: model keeps the last 10 for 15 minutes; code keeps one, forever). `executionArn` and activity/alias ARNs: SFN-40 (account and region ignored). |
| **L7** asynchronous means observable | The service's asynchronous operations, decided: **`DeleteStateMachine`** is asynchronous on AWS (`DELETING`, "deleted only when all its executions are completed", "on the next state transition, the state machine's executions are terminated"); here it is immediate and leaves the executions: **decision D1**, SFN-1. **Executions** and **Map Runs** are already asynchronous (the driver goroutine) and visible: RUNNING, then terminal. **`RedriveExecution`** answers RUNNING at once: matches. **`GetActivityTask`** holds 60 seconds: matches (the ledger's "no long-poll ceiling" is stale, SFN-41). **Eventual consistency** ("within a few seconds" on `UpdateStateMachine`, `UpdateStateMachineAlias`, every Create and Describe): immediate here, a listed difference, more forgiving by design. **`StateMachineDeleting`** cannot be seen. A board that draws DELETING does not exist; none of the 26 does. |
| **L8** latency rules are opt-in strictness | The time- and consistency-based rules in the model: a name is reusable 90 days after its execution closed (Standard) and at once (Express); a pagination token expires after 24 hours; a redrive `clientToken` is kept for 15 minutes, last 10; the redrive period is 14 days from completion; a state machine is `DELETING` until its executions end. Decision **D2**: none of them is on by default except what already is (the 14-day period). A `--sfn-strict` switch, off by default and in memory, takes the 24-hour token expiry, the 90-day name reuse (a refusal for the whole 90 days is what happens now, forever) and the DELETING window if D1 chooses to model it. Nothing here is worth a default. |
| **L9** state in memory unless losing it changes what the app sees | Persisted and correct: machines, versions, aliases, executions, history, tokens (and the `TimedOut` tombstone), activity queue, Map Runs, tags. In memory and correct: Express and TestState runs, activity pollers, the doorbell. **New state this review adds**: the DELETING marker (if D1 models it) persists; the redrive `clientToken` list is memory (15 minutes); the 24-hour pagination-token expiry would be memory under `--sfn-strict`. A restart inside a DELETING window must not resurrect the machine, which is why that marker persists. |
| **L10** the board is the spec | **26 boards compared, rendered beside the page.** 3 match (one with a one-line gap), 23 differ in shape. 11 boards were wrong about a fact and were corrected. Section 6a. |
| **L11** five teaching lines | Not built on any setting. Today a field carries one hint in its label ("optional — one is generated"). The settings record does not exist for this service: build it (`stepfunctions/settings.go`), then the kit's `setting_field`. Section 6, "The five lines, per setting". |
| **L12** icons | **Checked, none.** `awsIcon "sfn"` is on the resource header, the activities header, the execution header and the empty state; the rail carries the service icon. The board's list-pane header and the page's are both text only. Kept as is. A 2-letter `SF` on a board is the placeholder. |

### Classes of mistake this service is most at risk of

1. **Language rules written from memory.** The model is silent on the States
   Language: every analyser rule is `doc` (the specification) or `memory`, and 21
   of the 53 findings are in `internal/asl`. The three pages read in this review
   promoted several rules to `doc` and found that the interpreter matches a rule
   the guide contradicts (SFN-49: `States.ALL` catches `States.Runtime` here).
   The next pass should read the rest of the guide's state-type pages before
   writing a test, not after.
2. **The code accepts what the model has a name for.** Seven `Invalid...`
   configuration errors, `MissingRequiredParameter`, `StateMachineDeleting`,
   `ServiceQuotaExceededException` and the `ValidationException` reasons are
   absent: the service says yes to everything it was never asked to check.
3. **A value the user wrote is quietly ignored** (`TimeoutSecondsPath`,
   `HeartbeatSecondsPath`, `Credentials`, `includedData`, `variables`,
   `context`, `stateConfiguration`, `revealSecrets`). The protocol calls this a
   bug unless it is a listed C row.
4. **Token and clock semantics.** Which error a closed task's token gets; where a
   task's clock starts (schedule vs claim); which name a missed heartbeat carries.
   Three pages of AWS documentation disagree with each other or with the code on
   these, and none is recorded.
5. **Boards that document a bug.** Three boards drew the current behaviour of a
   defect as the design (`.waitForTaskToken` under TestState, "executions are
   kept", "a day, then TaskDoesNotExist"). Corrected; the next board pass must ask
   of each statement "is this what AWS does, or what we do".

### Decisions that need a person before the stage starts

| | Decision | Options | Recommended |
|---|---|---|---|
| D1 | `DeleteStateMachine` and the executions it owns (SFN-1) | (a) immediate, running executions ended at once as ABORTED, finished ones kept and readable, versions and aliases removed; (b) model `DELETING` until executions drain, `StateMachineDeleting` on start/publish/alias | (a), plus the difference listed. The final status of a terminated execution is not in the model; ABORTED is a guess, so it goes to the queue. |
| D2 | Opt-in strictness (L8) | none / `--sfn-strict` for the 24-hour token, the 90-day name, DELETING | A switch, off by default, in the same commit as SFN-27 |
| D3 | Unsupported integration at create (SFN-45): 400 `UnsupportedOperationException` today | 501 `NotImplemented` + `X-Doze-Not-Built` (D5), shown as "Not built" | D5. The code is not in the model. |
| D4 | `UpdateStateMachine` with no `definition` and no `roleArn` but a `loggingConfiguration` (SFN-24) | refuse as the operation text says / accept as the member list implies | Refuse an update with **no** modifiable member; accept logging-only until recorded. The model's text contradicts itself here. |
| D5 | `CreateStateMachine` repeat with a different role (SFN-23) | follow the operation text (ignore role and tags) / follow the error shape's text ("different definition or role ARN") | The operation text: it is the specific and the current one. Both stay in the queue. |
| D6 | Default page size becomes 100 (SFN-27) and the console reads one page (SFN-15) | page the console / raise nothing | Page the console in the same commit, or the list pane silently loses machine 101. |

---

## Foundation hand-offs (stage 0, 2026-10-20)

What the foundation (stage 0, package P6) left for this stage, so it is not lost, and what it did already. The shared items are in [../00-foundation.md](../00-foundation.md); each line here says what is done, what is owed, and the test that proves it.

| Item | Step Functions: done, and what this stage does |
|---|---|
| B6 the model sweep | Nothing: 37 of 37 operations are handled. |
| B8 generated cases | 248 committed, 248 regenerated, but five values differ (5 only in the committed file, 5 only in the new one): not a no-op. Regenerate in this stage and see which five. |
| B9 stored-policy validator | Not on `resource_policy_wiring_test.go`'s list: this service stores no resource policy document: where the model has the operation (`PutResourcePolicy`, `PutPermission`) the sweep answers it `legacystub` or `unknown`, and where it does not there is nothing to store. If this stage builds one, it calls `iampolicy.ValidateResourcePolicy` with the service's own code and wording and is added to that test. |
| B4 settings records | 0 hand-written bounded number inputs. |
| A15 fragments | Walked and equal to their page: `sfn_executions`, `sfn_history`, `sfn_graph_live`. |
| A2 the why line | Set `Local: true` (`awshttp.APIError`) on each refusal that is doze-aws's own limit and not AWS's, which this document's findings mark; nothing sets the stamp today except the console's own 503. Test: one such limit per service answers with `X-Doze-Local-Limit` and the page says "Refused here" (`internal/console/why_line_test.go` is the worked example). |
| B3 mutation | Baseline recorded in `testdata/mutation.json` (`stepfunctions`); this stage runs `task mutate:check -- stepfunctions` after its changes, and any survivor it adds has a test or a reason. |


## 1. Summary

Standard and Express state machines over one bbolt store, on AWS JSON 1.0
(target `AWSStepFunctions`, signing name `states`, lowercase-initial members)
*(model: `awsJson1_0`, `aws.auth#sigv4` name `states`, `arnNamespace` `states`)*.
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

It is XL because the contract is three things: 37 operations, a language, and a
console of 26 boards of which 22 differ in shape from the design. The
operations pass the constraint table (229 of 229 model constraints enforced
over 33 operations; 248 generated cases, 19 not replayed) and fail the model's
*prose*: 27 findings came from reading it. The language findings are what the
analyser accepts, what the interpreter does on retry, catch and timeout, and
what the history records.

**Estimate:** **XL, about 15 focused days (12 to 18).** *Corrected 2026-10-11:
was "L, five or more focused days"*, which was written against 21 findings and
before the model's prose and the boards had been read. What moved it:

| Work | Days | Why |
|---|---|---|
| API: the 27 model findings (SFN-22 to 48) and SFN-49 to 53 | 4 | Most are small handlers; `TestState` completeness, the DELETING decision, ARN qualifiers, the `reason` member and `ListExecutions` rules are not. |
| Interpreter and analyser (SFN-3 to 6, 36, 49 to 52) | 2.5 | The analyser needs a rule list with its source beside each rule and the model's diagnostic codes and warnings. |
| Console: 22 boards differ in shape | 5 | Two-column create, start, definition, versions, execution; side panels on graph, redrive, map run; five-line teaching on about 20 settings; confirm bodies; refused columns. SQS needed six boards rebuilt for 21; this is 22 for 26. |
| Tests, conformance (35 or so scenarios), mutation | 2.5 | Both baselines exist and will move. |
| Gates and the ledger | 1 | |

SQS was several times its estimate. This number assumes SFN-49 to 53 are not
the last ones: the guide has a page per state type, and only three pages were
read.

## 2. Contract

The model lists 37 operations *(model: counted)*. All 37 are dispatched
(`handlers` in `stepfunctions/actions_machines.go`) and all 37 have a constraint
table (`stepfunctions/validate.go`). Nothing is S today.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateStateMachine | F | F | Idempotent. *Corrected 2026-10-11: was "idempotent on an identical definition"; model: "idempotency check is based on the state machine name, definition, type, LoggingConfiguration, TracingConfiguration, and EncryptionConfiguration ... also ... publish and versionDescription ... If a following request has a different roleArn or tags, Step Functions will ignore these differences".* The code compares definition, role and type only: SFN-23. Refuses task resources outside the local set (see the integration table). Accepts definitions AWS refuses: SFN-3, 6, 37, 51. A configuration block is never validated: SFN-25. |
| DescribeStateMachine, ListStateMachines | F | F | *Corrected 2026-10-11: was "Describe also answers a version or alias-qualified ARN"; the code answers a version ARN only and an alias ARN is `StateMachineDoesNotExist`, and the model documents a version ARN and a Distributed Map label ARN (`machine/label`, which answers the Map state with `label`): SFN-31.* `includedData: METADATA_ONLY` is ignored (model: "the returned definition will be {}"): SFN-30. List answers every machine when `maxResults` is omitted (model: default 100): SFN-27. |
| UpdateStateMachine | F | F | Running executions keep their frozen definition *(model: "Running executions will continue to use the previous definition and roleArn")*. With neither `definition` nor `roleArn` it answers 200 (model: `MissingRequiredParameter`): SFN-24. Per-setting tier below. |
| DeleteStateMachine | F | F | *Corrected 2026-10-11: was "Immediate, not DELETING (listed difference)" only; model: "This is an asynchronous operation. It sets the state machine's status to DELETING ... A state machine is deleted only when all its executions are completed. On the next state transition, the state machine's executions are terminated ... also deletes all versions and aliases".* Leaves the machine's executions running and listed: SFN-1. A missing machine succeeds (the model lists `InvalidArn` and `ValidationException` only: right). A labelled ARN succeeds and deletes nothing: SFN-31. |
| ValidateStateMachineDefinition | F | F | Ignores `type`, `severity` and `maxResults`; every diagnostic is `ERROR` / `SCHEMA_VALIDATION_FAILED`, no warnings, and a location spelled `States.A.Next` where the model spells `/States/A/Next`: SFN-7, 37. |
| PublishStateMachineVersion, DeleteStateMachineVersion, ListStateMachineVersions | F | F | Publishing an unchanged definition answers the existing version *(model: "doesn't create a duplicate state machine version if it already exists for the current revision"; re-tagged from `run`)*. `revisionId: INITIAL` is refused: SFN-34. 1,000 versions per machine is not enforced: SFN-33. Deleting a referenced version: ConflictException (the model says it cannot be done and names no code for it; `ConflictException`'s own text is about concurrent calls: recording queue). A missing version succeeds *(model: the operation lists no not-found error)*. |
| CreateStateMachineAlias, DescribeStateMachineAlias, UpdateStateMachineAlias, DeleteStateMachineAlias, ListStateMachineAliases | F | F | One or two versions *(model: `RoutingConfigurationList` length 1 to 2)*, weights 0 to 100 summing to 100 *(model: "must be equal to 100")*. 100 aliases per machine and the alias name's lookahead are not enforced; the list is in name order where the model says most recently created first: SFN-33. A `ValidationException` here carries no `reason` (`INVALID_ROUTING_CONFIGURATION`): SFN-32. |
| CreateActivity, DescribeActivity, DeleteActivity, ListActivities | F | F | Create is idempotent on the name *(model)*, but a repeat rewrites the tags (model: "tags will not be updated") and an `encryptionConfiguration` is neither stored nor answered by Describe: SFN-26. |
| GetActivityTask | F | F | 60 s long poll *(model: "The maximum time the service holds on to the request before responding is 60 seconds ... returns a taskToken with a null string")*. The empty answer is `{}`. |
| TagResource, UntagResource, ListTagsForResource | F | F | Machines and activities. *Corrected 2026-10-11: was "per-resource limit is not established"; model: `TooManyTags` exists, httpError 400, "See the Limits Topic"; the value is not in the model. The model also states the character rule: "Tags may only contain Unicode letters, digits, white space, or these symbols: _ . : / = + - @".* Neither is enforced; the list order is random: SFN-43. |
| StartExecution | F | F | Machine, version or alias ARN. Express: fire and forget. Standard: idempotent for a running execution with the same name and input *(model; re-tagged from `doc`)*. A labelled ARN answers `StateMachineDoesNotExist` (model: `ValidationException`, reason `API_DOES_NOT_SUPPORT_LABELED_ARNS`): SFN-31. The HTTP `X-Amzn-Trace-Id` header, which the model says wins over `traceHeader`, is not read: SFN-46. |
| StartSyncExecution | F | F | Express only *(model: `StateMachineTypeNotSupported` on a Standard machine)*. `billingDetails` is nominal (see settings). `inputDetails` and `outputDetails` carry `truncated` where the model's type has `included`: SFN-29. |
| TestState | F | F | *Corrected 2026-10-11: was "stateConfiguration, variables, context, revealSecrets: not established"; the model establishes each (section 4, SFN-35) and the code ignores all four. A mock on a `.waitForTaskToken` or activity Task fails `States.Runtime`; model: "TestState only supports ... Activity tasks, .sync or .waitForTaskToken service integration patterns, Parallel, or Map states [when a mock is specified]".* |
| DescribeExecution, ListExecutions | F | F | An Express machine refuses ListExecutions with `StateMachineTypeNotSupported` *(model: listed; "you may list EXPRESS children started by a map run using the mapRunArn parameter")*. The filters' rules, the sort and the item members: SFN-28. |
| DescribeStateMachineForExecution | F | F | The frozen snapshot. Answers 7 of 12 members: SFN-30. |
| GetExecutionHistory | F | F | Default and maximum page match the model (100, 1,000). A caught failure records no `TaskStateExited`: SFN-5 (memory). A stop records no `...StateAborted` event: SFN-42. |
| StopExecution | F | F | Leaves the parked token redeemable: SFN-2. |
| RedriveExecution | F | F | *Corrected 2026-10-11: was "14-day window"; model: "This period starts from the day a state machine completes its execution", and the execution event count must be less than 24,999 and the open time under a year.* `clientToken`: the model keeps the last 10 for 15 minutes; the code one, forever: SFN-38. |
| SendTaskSuccess, SendTaskFailure, SendTaskHeartbeat | F | F | Model constraints not replayed by the parity test (19 cases): section 3. A closed task's token answers `TaskDoesNotExist` where the model says `TaskTimedOut`: SFN-22. |
| DescribeMapRun, ListMapRuns, UpdateMapRun | F | F | UpdateMapRun on a finished run is accepted *(model: `ValidationException`, reason `CANNOT_UPDATE_COMPLETED_MAP_RUN`; "Updates an in-progress Map Run's configuration")*: SFN-13. |

**Settings that "the operation is F" hides**

| Setting | On | Now | Target | To confirm |
|---|---|---|---|---|
| `type` | Create | F | F | STANDARD and EXPRESS differ in: `ListExecutions`, `DescribeExecution`, `DescribeStateMachineForExecution`, `GetExecutionHistory`, `StopExecution`, `RedriveExecution` (all "not supported by EXPRESS" in the model), name reuse, idempotency. *Corrected 2026-10-11: the "5-minute cap" and "token parking" are not in the model; the five minutes is the model's only for TestState. Source: memory (Express quotas), doc page not read.* |
| `roleArn` | Create, Update | C | C | Shape is checked (`validRoleARN`); nothing evaluates it; the create form says so. *Corrected 2026-10-11: was "Create, Update, TestState"; `testState` never calls `validRoleArn` (run: `roleArn: "nope"` answers SUCCEEDED).* The model gives `roleArn` length 1 to 256 and nothing else, so the shape rule is doze's own (`InvalidArn`, source: memory). |
| `loggingConfiguration.level`, `.destinations`, `.includeExecutionData` | Create, Update | F | F | Every history event is vended to the group, filtered by level. Validation is absent: SFN-25 (model: "Limited to size 1. Required, if your log level is not set to OFF"; "The ARN must end with :*"). |
| Express with logging off | Create | extension | extension | Writes to `/aws/vendedlogs/states/<name>` at ALL. A listed difference; on AWS level OFF logs nothing *(model: "By default, the level is set to OFF")*. |
| `tracingConfiguration` | Create, Update | C | C | Stored and returned. No X-Ray. Not shown in the console today. `{"enabled": "yes"}` is accepted (model: `InvalidTracingConfiguration`): SFN-25. |
| `encryptionConfiguration` | Create, Update | C | C | Stored and returned on a state machine. *Corrected 2026-10-11: was "Create, Update, CreateActivity"; `CreateActivity` drops it and `DescribeActivity` never returns it (run).* No KMS wrapping. `kmsDataKeyReusePeriodSeconds` is held to 60 to 900 *(model)*. A `CUSTOMER_MANAGED_KMS_KEY` with no `kmsKeyId` is accepted: SFN-25. |
| `publish`, `versionDescription` | Create, Update | F | F | `versionDescription` without `publish` is refused `ValidationException` *(model: "this API action throws ValidationException"; run)*. |
| `tags` | Create, CreateActivity | F | F | Length 1 to 128 and 0 to 256 enforced *(model)*. The character rule and `TooManyTags`: SFN-43. A repeat create must not rewrite them: SFN-23, 26. |
| `traceHeader` | StartExecution, StartSync | C | C | Returned only when given *(model: `traceHeader`, "ASCII" pattern)*. The HTTP header wins over the body member: SFN-46. |
| `billingDetails` | StartSync answer | C | C | Duration rounded up to 100 ms; memory is the constant 64. The ledger does not say so: SFN-17. |
| `includedData` | DescribeStateMachine, DescribeExecution, DescribeStateMachineForExecution, StartSync | F on StartSync only | F | *Corrected 2026-10-11: was "Describe*, StartSync: F"; only `StartSyncExecution` reads it (`actions_express.go`).* Model for the state machine: "the returned definition will be {}". SFN-30. |
| `inspectionLevel`, `mock`, `context`, `variables`, `stateConfiguration`, `revealSecrets` | TestState | F for the first two | F | SFN-35. The model: `context` "may only be specified if a mock is specified in the same request"; `variables` "sets variables used in the state under test"; `revealSecrets` needs `states:RevealSecrets` and applies to HTTP Tasks (an HTTP Task is not built, so this one is C, stated). |
| `maxConcurrency`, `toleratedFailureCount`, `toleratedFailurePercentage` | UpdateMapRun | F | F | *Corrected 2026-10-11: was "only maxConcurrency exercised; the other two not established"; read: both are stored and used by `mapRun.tolerated()`. When both are set the count wins and the percentage is ignored; the model says neither and calls them "failed items" (SFN-48).* |
| `error`, `cause` | StopExecution | F | F | The console sends neither: SFN-18. Length 256 and 32,768 enforced *(model)*. |

**What of the States Language runs**

The model does not describe the language. Every rule below is `doc` where one of
the three pages read says so, and `memory` otherwise.

| Feature | Now | Target | Note |
|---|---|---|---|
| State types: Pass, Task, Choice, Wait, Succeed, Fail, Parallel, Map | F | F | All eight. |
| `Retry`: `ErrorEquals`, `IntervalSeconds`, `MaxAttempts`, `BackoffRate`, `MaxDelaySeconds`, `JitterStrategy` | F | F | Defaults 1, 3, 2.0 *(doc: error-handling page, "1 by default", "3 by default", "2.0"; `retry.go` `nextRetry`)*. Bounds not checked: SFN-51 (doc: max 99,999,999; `MaxDelaySeconds` greater than 0 and less than 31,622,401; `JitterStrategy` FULL or NONE). A retried parked task keeps its old heartbeat clock: SFN-4. Whether a retry reuses the task token: not established (SFN-12). A redrive resets the count *(doc)*: held. |
| `Catch` | F | F | SFN-5, SFN-49. |
| Machine `TimeoutSeconds` | F | F | Must be positive. Ends the execution TIMED_OUT. Restarts on redrive. |
| Task `TimeoutSeconds` | F | F | *Corrected 2026-10-11: "only checked to be a number" stays true (run: -5 accepted); source is now doc: "positive, non-zero integer. The default value is 99,999,999."* SFN-6. |
| Task `HeartbeatSeconds` | F | F | *doc: "positive, non-zero integer value less than the TimeoutSeconds field value. The default value is 99,999,999".* Counts only for a parked task (token or activity); ignored on a synchronous call. Not checked against the timeout: SFN-6. |
| `TimeoutSecondsPath`, `HeartbeatSecondsPath` | **accepted and ignored** | F | *Corrected 2026-10-11: was "not established"; read and run: a Task with `TimeoutSecondsPath: "$.t"` and `t = 1` was still RUNNING after 3.5 s.* Doc: JSONPath only, "must select fields whose values are positive integers", not both with the static field. SFN-36. |
| `Credentials` | accepted, unread | C | *Corrected 2026-10-11: was "not established"; doc: "Specifies a target role the state machine's execution role must assume before invoking the specified Resource", with `RoleArn` or `RoleArn.$`; model: history carries `taskCredentials.roleArn`.* Nothing assumes a role locally and the event does not echo it: SFN-48. |
| Inline Map: `ItemsPath`, `ItemSelector`, `MaxConcurrency`, tolerated failures | F | F | |
| Distributed Map: `ItemReader` (S3 JSON, JSON Lines, CSV, `listObjectsV2`), `ItemBatcher`, `ResultWriter`, `Label` | F | F | Default concurrency 40, not 10,000 (listed difference; AWS figure: memory). |
| JSONPath: paths, `InputPath`, `Parameters`, `ResultSelector`, `ResultPath`, `OutputPath`, context object | F | F | The `$$` fields the code builds (`exec.go` `buildContext`): `Execution.{Id,Name,Input,StartTime,RoleArn}`, `State.{Name,EnteredTime,RetryCount}`, `StateMachine.{Id,Name}`, `Task.Token`, `Map.Item.{Index,Value}`. AWS's list is not in the model: memory. An `InputPath` or `OutputPath` that selects nothing yields `null` silently: SFN-53 (memory). |
| JSONPath `Assign` and `$name` variables | F | F | *Corrected 2026-10-11: was "not established"; run: `Assign: {"x.$": "$.a"}` then `Parameters: {"v.$": "$x"}` answered `{"v": 7}`. The comment on `refuseUnrunnable` is the stale one (SFN-20).* |
| Intrinsics | F | F | All 18: `States.Array`, `ArrayPartition`, `ArrayContains`, `ArrayRange`, `ArrayGetItem`, `ArrayLength`, `ArrayUnique`, `Base64Encode`, `Base64Decode`, `Hash`, `JsonMerge`, `StringToJson`, `JsonToString`, `MathRandom`, `MathAdd`, `StringSplit`, `UUID`, `Format` (`internal/asl/intrinsic_fn.go`; counted). |
| JSONata: `Arguments`, `Output`, `Assign`, `Condition`, `Items`, Wait fields, `$states`, the AWS functions | F | F | `%`, `@` and `#` do not work (listed difference, frozen by `jsonata_dialect_test.go`). |
| Choice operators | F | F | A `Variable` that selects nothing fails `States.Runtime` (run). |

**Which Task resources run**

| Resource | Now | Target | Note |
|---|---|---|---|
| Lambda by function ARN; `lambda:invoke`, `.waitForTaskToken` | F | F | |
| `sqs:sendMessage`, `sns:publish`, each with `.waitForTaskToken` | F | F | |
| `events:putEvents` | F | F | Whether `.waitForTaskToken` is accepted: not established. |
| `dynamodb:getItem`, `putItem`, `updateItem`, `deleteItem` | F | F | |
| Activity ARN | F | F | *doc (state-task page): "supported only by Standard Workflows, not Express Workflows".* |
| `states:startExecution`, `.sync`, `.sync:2`, `.waitForTaskToken` | F | F | |
| `aws-sdk:<service>:<action>` for dynamodb, eventbridge, sqs, kms, ssm, secretsmanager, kinesis, sfn, sns, sts, iam, cloudformation | F | F | Encoded from each service's model (`integration_awssdk.go`; 14 services with S3 and Lambda: counted). The ledger's list omits kms, sts and cloudformation: SFN-21. |
| `aws-sdk:s3:` getObject, putObject, deleteObject, headObject, listObjectsV2; `aws-sdk:lambda:invoke` | F | F | Other actions refused at create. |
| Every other optimized integration, and `.sync` on anything but Step Functions | S | S | *Corrected 2026-10-11: was "refused with UnsupportedOperationException"; that code is not in the model and the protocol's refusal is 501 `NotImplemented` with `X-Doze-Not-Built` (D5): SFN-45.* Not in `not-built.md` today (checked). Validate accepts them, which is right for a valid resource and wrong for a malformed one (SFN-37). |
| A callback or activity Task on an Express machine | accepted, fails at run | S at create | SFN-3. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `stepfunctions/`: 90 tests in 24 files (engine, activities, children, map runs, redrive, logging, express, SDK v1 and v2, driver death, volatile leak). `internal/asl/`: 63 tests and 3 fuzz targets in 10 files. `internal/console/sfngraph/`: 8 tests. *(counted 2026-10-11)* |
| Parity | `rejection_parity_test.go`, `coverage_model_test.go`. 229 of 229 model constraints enforced across 33 operations, `knownGaps` empty; `testdata/cases_sfn.json` has 248 cases over all 37 operations. The other four (SendTaskSuccess, SendTaskFailure, SendTaskHeartbeat, RedriveExecution; 19 cases) are in `needState` and are **not replayed**. The table holds length, range, enum, required and pattern; it does not hold the model's *prose*, which is what SFN-22 to 48 are. One pattern loses its lookahead (the alias name): SFN-33. |
| Console tests | `internal/console/sfn_test.go` (1), `sfn_more_test.go` (5) |
| Deploy shapes | `cloudformation/sfn_apply_test.go`, `sfn_alias_apply_test.go`; `internal/provision/svc_sfn.go` |
| Conformance | `conformance/tests/test_stepfunctions.py`: 3 scenarios (lifecycle and history, the States Language, refusals), all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/stepfunctions.spec.ts`: 17 tests. `e2e/tests/stepfunctions-sdk.spec.ts`: 9 tests against `@aws-sdk/client-sfn`. |
| Mutation | *Corrected 2026-10-11: was "No per-package baseline (foundation B3)". Both are in `testdata/mutation.json`, recorded 2026-10-10: `internal/asl` 742 killed, 117 survived, 11 timed out, 228 not covered (86.4%); `stepfunctions/` 790 killed, 168 survived, 7 timed out, 112 not covered (82.5%).* They will move: re-record and say why in section 10. |

The model constrains request shapes and says nothing about the definition
string. Everything the analyser enforces is therefore hand-derived, and every
rule in `internal/asl/analyse*.go` is a `doc` claim against the States Language
specification until recorded. No rule there carries its source beside it today.

## 4. Findings

Evidence `run` means reproduced against a binary built from the working tree on
2026-10-11 (SFN-1 to 21 against the build of 2026-10-10 as first written, and
re-run where this review touched them), through the console routes or the wire.
`read` is from the code. `model` carries a short quote.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| SFN-1 | P0 | Deleting a machine leaves its executions and history in the store, and **leaves a RUNNING execution running**. `DescribeExecution` and the execution page still answer afterwards (run: the execution stayed RUNNING), and a new machine created under the same name lists the old machine's executions as its own (run). The console confirm says they go (foundation A13). | `stepfunctions/store_machines.go` `DeleteMachine`; `actions_machines.go` `deleteStateMachine` | run | **model** (*re-sourced 2026-10-11*): "sets the state machine's status to DELETING ... deleted only when all its executions are completed. On the next state transition, the state machine's executions are terminated." What stays readable afterwards, and the final status of a terminated execution: memory | Engine test: run, delete, recreate the name, `ListExecutions` is empty and the running one ended. Decision D1 first |
| SFN-2 | P1 | After `StopExecution`, `SendTaskSuccess` and `SendTaskHeartbeat` with the aborted task's token answer 200 and do nothing (run). The token row is not removed or tombstoned on abort, **nor on a machine-level TIMED_OUT** (read: `engine.finalize` and `scheduler.fireDue` do not release tokens). The console confirm says the token "stays unredeemable" (A13). | `stepfunctions/engine.go` `finalize`, `applyDelivery` `dlvStop`; `scheduler.go` `fireDue`; `store_tokens.go` | run | **model** (*re-sourced*): `TaskTimedOut`: "The task token has either expired or the task associated with the token has already been closed." | SDK test: park, stop, send; assert `TaskTimedOut`. Park, machine timeout, send; the same |
| SFN-3 | P0 | An EXPRESS machine whose definition has a `.waitForTaskToken` or activity Task is created without complaint; every run then fails `States.Runtime: .waitForTaskToken is not available to this execution type` (run). A deploy that AWS is believed to refuse succeeds here. | `stepfunctions/actions_machines.go` `createStateMachine`, `updateStateMachine` (`refuseUnrunnable` ignores `type`); the run-time refusal is `internal/asl/exec.go` `advanceTask` | run | **doc** (*re-sourced*, state-task page): activities "are supported only by Standard Workflows, not Express Workflows". `ValidateStateMachineDefinition.type` is "The target type of state machine for this definition", so type-dependent diagnostics exist (model). Whether AWS refuses at create or at run, and for callbacks as well as activities: not established | Create test, EXPRESS plus a callback Task, skipped pending `express_callback_at_create` |
| SFN-4 | P0 | A parked task that fails and is retried keeps its old heartbeat clock. `Wake` recomputes `Deadline` on a `FrameRetryWait` frame but not `HeartbeatAt`, so the retried attempt fails `States.HeartbeatTimeout` at the next one-second tick. With `HeartbeatSeconds: 5` the second attempt died 1.0 s after it was scheduled. | `internal/asl/exec.go` `Wake`, the `FrameRetryWait` case (compare `advanceTask`, which sets `HeartbeatAt`); read again 2026-10-11 | run, read | memory: each attempt gets a fresh heartbeat interval (the guide: "If more time than the specified seconds elapses between heartbeats from the task, the Task state fails": per attempt by any reading) | `internal/asl` test: Retry on `States.HeartbeatTimeout`, assert the second timeout is a full interval after the second dispatch |
| SFN-5 | P1 | A failure that a Catch handles records `TaskFailed` (or `TaskTimedOut`) and then the next state's `...StateEntered`. No `TaskStateExited` is written (run: `5:TaskFailed 6:PassStateEntered`). | `internal/asl/exec.go` `deliverFailure` (emits `NoteCaught` only); `stepfunctions/history.go`, the `NoteFailed, NoteRetried, NoteCaught` case | run | memory: AWS writes `TaskStateExited` after a caught error (the model's `HistoryEventType` has `TaskStateExited`; it does not say when) | History test: Catch to a Pass; assert the event sequence. Skipped pending `catch_history` |
| SFN-6 | P0 | The analyser checks a Task's `TimeoutSeconds` and `HeartbeatSeconds` only for being numbers. A negative or zero value and a heartbeat longer than the timeout are accepted (run: `-5` and `100 > 10`); at run a timeout of 0 or less means no deadline. | `internal/asl/analyse_state.go` `analyseState` (no check); `internal/asl/exec.go` `taskDeadline`, `taskHeartbeat` | run | **doc** (*re-sourced*): spec: "MUST be positive integers"; "the HeartbeatSeconds interval MUST be smaller than the TimeoutSeconds value"; state-task page: "positive, non-zero integer" | Analyser test for -5, 0, and heartbeat 100 with timeout 10 |
| SFN-7 | P1 | `ValidateStateMachineDefinition` ignores `type`, `severity` and `maxResults`. Every diagnostic is `ERROR` with code `SCHEMA_VALIDATION_FAILED`; there are no warnings and `truncated` is always false (run). See SFN-37 for the codes and the location format. | `stepfunctions/actions_machines.go` `validateDefinition` | run | **model** (*re-sourced*): `severity` "ERROR returns only ERROR diagnostics, whereas WARNING returns both ... The default is ERROR"; `maxResults` 0 to 100, "The default and maximum value is 100 ... will be set to true"; `type` default STANDARD; `result` OK when only warnings | SDK test: `maxResults: 1` on a definition with two problems; `severity: WARNING` on a `NO_DOLLAR` definition |
| SFN-8 | P2 | The Validate button answers "Definition is valid — AWS would accept it too" for a definition Create and Save then refuse (not built). The sentence is true; the create page's subtitle says "what passes here deploys", which is not. After foundation A2 the refusal reads "Not built ... Nothing is wrong with the request" with the sentence for a missing *operation* (run). | `internal/console/templates/sfn.html` `sfn_diagnostics`; `create.html` `sfn_create` | run | n/a | Handler test: validate, then create, `ecs:runTask.sync`; assert the texts |
| SFN-9 | P2 | The name inputs' browser `pattern` leaves out `&`; the service refuses it. Four inputs: machine name, execution name, alias name, activity name. The alias pattern is wrong twice: the model is `^(?=.*[a-zA-Z_\-\.])[a-zA-Z0-9_\-\.]+$` and the console uses the machine-name rule. | `create.html` `sfn_create`; `sfn.html` Start form; `sfn_more.html` `sfn_versions_tab`, `sfn_activities`; server rule `actions_machines.go` `nameRule` | run (`a&b`) | **model** for both: the name's character list, in which `< >` are present (*the earlier strip of the model's HTML had hidden them; the code's rule is right*); the alias pattern | Browser test types `a&b` and an alias `a b` |
| SFN-10 | P1 | `InvalidName` messages have a doubled quote: `Invalid Name: ''a/b' contains a character that is not allowed'` (run). `checkName` quotes the name and `errInvalidName` quotes the detail. | `stepfunctions/actions_machines.go` `checkName`; `errors.go` `errInvalidName` | run | memory: AWS's message text | Error test on the message. Record the real text first |
| SFN-11 | P2 | The Map Run "Set concurrency" field shows a Go parse error to the user: `strconv.Atoi: parsing "lots": invalid syntax`, as a refusal with no code. | `internal/console/handlers_sfn_ops.go` `sfnMapRunUpdate` | run | n/a | Handler test posts `max=lots` |
| SFN-12 | P1 | A retried `.waitForTaskToken` task is dispatched again with the **same** token (`Wake` re-sends the persisted `TaskInput`, by design, to avoid re-running intrinsics). Whether AWS mints a new token per attempt is not established; if it does, a worker holding the first token can answer the second attempt here and not on AWS. | `internal/asl/exec.go` `Wake` | run | memory | None until `retry_task_token` is recorded |
| SFN-13 | P1 | `UpdateMapRun` on a run that has finished answers 200 (run). The console only disables the button. | `stepfunctions/actions_mapruns.go` `updateMapRun` | run | **model** (*re-sourced*): `ValidationExceptionReason` has `CANNOT_UPDATE_COMPLETED_MAP_RUN`; "Updates an in-progress Map Run's configuration" | SDK test: finished run, `maxConcurrency: 5`; assert `ValidationException` with that reason |
| SFN-14 | P2 | In a machine's Logs tab every line's request chip reads `arn:aws:states:us`: the shared log tail shows the first characters of the "request id", which for Step Functions is the execution ARN. The chip still filters correctly. **Rendered: the chip text overlaps the message column**, so the first characters of each JSON record are unreadable (screenshot, Express logs). | `internal/console/templates/logs.html` `log_tail` (`.Short`); the Step Functions caller is `handlers_sfn.go` `sfnLogs` | run | n/a | Browser test: two executions, two different chips; and the chip does not overlap |
| SFN-15 | P2 | The console reads history with one call of `maxResults: 1000` and no `nextToken`, so an execution with more than 1,000 events shows the first 1,000 and says nothing. The same single page bounds the machine list, the execution list and Map Run children. **SFN-27 makes the lists worse**: once the default page is 100, the list pane shows 100 machines. | `internal/console/client_sfn_history.go` `ExecutionHistory`; `client_sfn.go` `ListStateMachines`, `listExecutions` | read | n/a | Client test with a 1,200-event history (a Map of Pass states) and 105 machines |
| SFN-16 | P2 | "Test a state" runs the editor's unsaved text, but its State select is filled from the saved definition (`stateNames(sm.Definition)`). A state just added in the editor cannot be chosen. | `handlers_sfn.go` `sfnMachine`; `sfn_more.html` `sfn_test_panel` | read | n/a | Browser test: add a state, do not save, open the select |
| SFN-17 | P3 | `billedMemoryUsedInMB` is the constant 64 and the console prints "100 ms · 64 MB" as a measurement. Neither the ledger nor the panel says the figure is nominal. | `stepfunctions/actions_express.go` `startSyncExecution`; `sfn_more.html` `sfn_sync_result` | read | n/a | Ledger test: the row exists |
| SFN-18 | P2 | The console cannot do what the API can: no logging, tracing, role or tag input on create or on the Definition tab (the save sends `definition` only); activities have no tags; `sfnStop` reads `error` and `cause` from a form that has no such fields. | `handlers_sfn.go` `sfnUpdateDefinition`, `sfnStop`; `sfn.html`; `create.html` | read | n/a | Decide per item in step 5; a browser test for each one built |
| SFN-19 | P3 | `InvalidExecutionInput` and `InvalidOutput` carry Go's JSON decoder text (`invalid character 'o' looking for beginning of object key string`). The code is right (model: both listed); the message is not AWS's. | `actions_executions.go` `startExecution`; `actions_tasks.go` `sendTaskSuccess`; `actions_teststate.go` | run | memory | Record `invalid_input_message`, then decide |
| SFN-20 | P3 | *Resolved by reading and running, 2026-10-11:* JSONPath `Assign` **runs** (run: `{"v": 7}`); the ledger is right, and only the comment on `refuseUnrunnable` ("Assign is evaluated only by the JSONata dialect ... create refuses it") is stale: `refuseUnrunnable` only calls `refuseResources`. The fix is the comment. | `stepfunctions/task.go` `refuseUnrunnable` | run, read | n/a | A test that a JSONPath `Assign` is read by a later state, so the comment cannot be re-believed |
| SFN-21 | P3 | The ledger's `aws-sdk` service list omits kms, sts and cloudformation, which `sdkServices` serves (read: 14 entries). | `docs/SUPPORT.md`; `stepfunctions/integration_awssdk.go` `sdkServices` | read | n/a | Ledger test over `sdkServices` |

**Found by reading the model's own documentation (`.audit-models/sfn.json`) and
three AWS pages, 2026-10-11.** None of these was in the plan. Each has a
direction, because "stricter" breaks a correct caller exactly as "more
forgiving" breaks a deploy.

| Id | Sev | What | Where | Evidence | Direction | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| SFN-22 | P1 | A task token whose task is **closed** (already answered, stopped, its execution finished) answers `TaskDoesNotExist`; a garbage token the same. `InvalidToken` is never answered. | `stepfunctions/actions_tasks.go` `tokenOf`; `store_tokens.go` (a redeemed token's row is deleted) | run (answered twice; `"garbage"`) | wrong code | **model**: `TaskTimedOut`: "The task token has either expired or the task associated with the token has already been closed."; `InvalidToken`: "The provided token is not valid." on all three `SendTask*`. Which of `InvalidToken` and `TaskDoesNotExist` a well-formed token that never existed gets: not established | SDK test: answer twice; assert `TaskTimedOut` the second time. Garbage: record `unknown_task_token` |
| SFN-23 | P0 | `CreateStateMachine` repeat: a **changed `roleArn` answers `StateMachineAlreadyExists`** (the model: ignored; a CDK role change fails here and succeeds on AWS), a changed `loggingConfiguration`, `tracingConfiguration`, `encryptionConfiguration`, `publish` or `versionDescription` is **accepted** (the model: part of the check), and `tags` on the repeat **overwrite** the stored ones (the model: ignored). | `stepfunctions/store_machines.go` `PutMachine`; `actions_machines.go` `createStateMachine` (calls `setTags` after `PutMachine`) | run | both | **model**: "idempotency check is based on the state machine name, definition, type, LoggingConfiguration, TracingConfiguration, and EncryptionConfiguration ... also ... publish and versionDescription ... a different roleArn or tags ... ignore these differences ... roleArn and tags will not be updated". **Conflict inside the model**: `StateMachineAlreadyExists`: "A state machine with the same name but a different definition or role ARN already exists." D5 | SDK test: repeat with another role (200, role unchanged), with tracing on (`StateMachineAlreadyExists`), with new tags (tags unchanged) |
| SFN-24 | P1 | `UpdateStateMachine` with neither `definition` nor `roleArn` answers 200 (and a logging-only update too). | `stepfunctions/actions_machines.go` `updateStateMachine` | run | more forgiving | **model**: "You must include at least one of definition or roleArn or you will receive a MissingRequiredParameter error"; the shape: "occurs if both definition and roleArn are not specified". The same text lists `loggingConfiguration` and `EncryptionConfiguration` among what it modifies: a logging-only update is not established. D4 | SDK test: ARN only; assert `MissingRequiredParameter`. Logging-only: skipped pending `update_logging_only` |
| SFN-25 | P1 | A configuration block is never validated: `level: ALL` with no `destinations`, a `logGroupArn` not ending `:*`, two destinations, `tracingConfiguration: {"enabled": "yes"}`, `CUSTOMER_MANAGED_KMS_KEY` with no `kmsKeyId` all answer 200. `InvalidLoggingConfiguration`, `InvalidTracingConfiguration` and `InvalidEncryptionConfiguration` are never answered. | `actions_machines.go` `createStateMachine`, `updateStateMachine`; `validate.go` (enum and range only) | run | more forgiving | **model**: `destinations` "Limited to size 1. Required, if your log level is not set to OFF"; `logGroupArn` "The ARN must end with :*"; `InvalidTracingConfiguration`: "enabled has not been set to true or false"; `InvalidEncryptionConfiguration`: "type is set to CUSTOMER_MANAGED_KMS_KEY, but kmsKeyId is null, or kmsDataKeyReusePeriodSeconds is not between 60 and 900, or the KMS key is not symmetric or inactive" | Five handler tests, one per refusal |
| SFN-26 | P1 | `CreateActivity`: a repeat **rewrites the tags** (run: `a=2` replaced `a=1`); the `encryptionConfiguration` is neither stored nor returned by `DescribeActivity` (run), and a repeat with a different one never answers `ActivityAlreadyExists`. | `actions_machines.go` `createActivity`, `describeActivity`; `store_machines.go` `PutActivity` | run | wrong | **model**: "If a following request has different tags values, Step Functions will ignore these differences ... tags will not be updated"; `ActivityAlreadyExists`: "EncryptionConfiguration may not be updated."; `DescribeActivityOutput.encryptionConfiguration` | SDK test: repeat with other tags; with an encryption block; describe |
| SFN-27 | P1 | Every list answers **everything** when `maxResults` is omitted (run: 106 of 106; `ListStateMachines`, `ListActivities`, `ListExecutions`, `ListStateMachineVersions`, `ListStateMachineAliases`, `ListMapRuns`). The page token is the offset: a token from another list is accepted (run), and the 24-hour expiry is not modelled. `GetExecutionHistory` is right (100). | `stepfunctions/actions_machines.go` `page` | run | more forgiving | **model**: "The default is 100 and the maximum allowed page size is 1000. A value of 0 uses the default"; "Each pagination token expires after 24 hours ... HTTP 400 InvalidToken" | SDK test: 105 machines, no `maxResults`, assert 100 and a token. Needs D6 (the console) in the same commit |
| SFN-28 | P1 | `ListExecutions`: (a) with both `stateMachineArn` and `mapRunArn` it lists (model: refused); (b) `statusFilter: PENDING_REDRIVE` with a state machine ARN answers an empty list (model: validation exception); (c) the order is by start date only (model: running by `startDate` or `redriveDate`, finished by `stopDate`); (d) an item never carries `mapRunArn`, `itemCount` when listed through a run, `redriveCount` or `redriveDate`. `redriveFilter` with a state machine ARN: see source. | `actions_executions.go` `listExecutions` | run, read | both | **model**: "You can specify either a mapRunArn or a stateMachineArn, but not both"; "If you provide a stateMachineArn and a PENDING_REDRIVE statusFilter, the API returns a validation exception"; "Running executions are sorted by their startDate or redriveDate, and other executions are sorted by their stopDate"; `mapRunArn`/`itemCount`: "returned only if mapRunArn was specified". **Conflict**: `redriveFilter`: "If you provide a state machine ARN in redriveFilter, the API returns a validation exception", while the operation text says redriven executions can be listed: recording queue | Four handler tests, one per rule |
| SFN-29 | P2 | `StartSyncExecution`'s `inputDetails` and `outputDetails` are `{"truncated": false}`; the model's type for these two members has `included`. A Go or JS caller reads `Included` as false. (History events' `inputDetails` are `truncated`, correctly.) | `stepfunctions/actions_express.go` `startSyncExecution` (`notTruncated()`) | run | wrong shape | **model**: `StartSyncExecutionOutput.inputDetails`: `CloudWatchEventsExecutionDataDetails` (`included`: "Indicates whether input or output was included in the response. Always true for API calls.") | SDK test: `InputDetails.Included` is true |
| SFN-30 | P2 | `DescribeStateMachineForExecution` answers 7 members: no `loggingConfiguration`, `tracingConfiguration`, `encryptionConfiguration`, `mapRunArn`, `label`; `updateDate` is the execution's start. `includedData` is ignored by `DescribeStateMachine` (the definition still comes back), `DescribeExecution` and this one. | `actions_executions.go` `describeStateMachineForExecution`, `describeExecution`; `actions_machines.go` `describeStateMachine` | run | wrong shape, missing | **model**: `DescribeStateMachineForExecutionOutput`; "If called with includedData = METADATA_ONLY, the returned definition will be {}"; `updateDate`: "The date and time the state machine associated with an execution was updated" | SDK test per member; `METADATA_ONLY` answers `{}` |
| SFN-31 | P1 | A **labelled ARN** (`machine/label`, a Distributed Map state): `DescribeStateMachine` and `StartExecution` answer `StateMachineDoesNotExist`; `UpdateStateMachine` the same; **`DeleteStateMachine` answers 200 and deletes nothing**. An alias ARN to Describe is `StateMachineDoesNotExist` as well. | `actions_machines.go` `splitMachineARN`, `unqualifiedMachine`; `actions_versions.go` `describeQualified` | run | wrong code, silent no-op | **model**: "If you provide a qualified state machine ARN that refers to a Distributed Map state, the request fails with ValidationException" (Delete, Update, Start); `ValidationExceptionReason`: `API_DOES_NOT_SUPPORT_LABELED_ARNS`; Describe answers the Map state with `label`: "This parameter is present only if the stateMachineArn specified in input is a qualified state machine ARN" | Four SDK tests |
| SFN-32 | P2 | `ValidationException` never carries `reason`. The model has four: `API_DOES_NOT_SUPPORT_LABELED_ARNS`, `MISSING_REQUIRED_PARAMETER`, `CANNOT_UPDATE_COMPLETED_MAP_RUN`, `INVALID_ROUTING_CONFIGURATION`. | `stepfunctions/errors.go` `errValidation` | read | missing | **model**: `ValidationException.reason` | Typed-error test reads `Reason` |
| SFN-33 | P1 | Quotas and the alias rules: 101 aliases on one machine are created (run); versions are unbounded; an alias named `0` or `007` is accepted (run: the generated table drops the pattern's lookahead, and `versionNumber` only recognises a canonical positive integer); `ListStateMachineAliases` is in name order. | `actions_aliases.go`; `store_versions.go`; `validate.go` | run (aliases, names, order), read (versions) | more forgiving | **model**: "You can create up to 100 aliases for each state machine"; "You can publish up to 1000 versions for each state machine"; `ServiceQuotaExceededException`, httpError 402; the alias pattern `^(?=.*[a-zA-Z_\-\.])...$` (a name needs a letter, `_`, `-` or `.`); "sorted by time, with the most recently created aliases listed first" | SDK tests: 101st alias, 1,001st version, `007`, list order |
| SFN-34 | P1 | `PublishStateMachineVersion` with `revisionId: "INITIAL"` on a machine created here answers `ConflictException`: a created machine carries a hash revision from the start. A never-updated machine's `revisionId` is a value; the model calls it null. | `actions_versions.go` `publishStateMachineVersion`; `store_machines.go` `revisionOf` | run | stricter, wrong shape | **model**: "To specify an initial revision ID for a state machine with no revision ID assigned, specify the string INITIAL ... you can specify a revisionID of INITIAL when you create a state machine"; `DescribeStateMachineForExecution.revisionId`: "The first revision ID when you create the state machine is null" | SDK test: create, publish with `INITIAL`; update, publish with the returned id |
| SFN-35 | P1 | `TestState` ignores `variables`, `context`, `stateConfiguration` (`retrierRetryCount` should decide RETRIABLE against FAILED or CAUGHT_ERROR; run: 0 and 2 both answered RETRIABLE) and `revealSecrets`; a mock on a `.waitForTaskToken` or activity Task fails `States.Runtime` (run); `roleArn` is not shape-checked (run); `inspectionData` has none of `variables`, `errorDetails`, `afterArguments`, `afterItemsPath`...; TRACE returns a request and response body for a non-HTTP Task; a lone state with `stateName` is accepted. | `actions_teststate.go` `testState`, `testStateOutput`, `testDefinition` | run | missing, wrong | **model**: "TestState only supports ... Activity tasks, .sync or .waitForTaskToken service integration patterns, Parallel, or Map states [when a mock is specified]"; `variables`: "sets variables used in the state under test"; `context`: "may only be specified if a mock is specified in the same request"; `TestStateConfiguration.retrierRetryCount`; `TestExecutionStatus` SUCCEEDED, FAILED, RETRIABLE, CAUGHT_ERROR; `stateName`: "If this field is specified, the definition must contain a fully-formed state machine definition"; TRACE: "Shows the HTTP request and response for an HTTP Task" | One SDK test per parameter |
| SFN-36 | P0 | `TimeoutSecondsPath` and `HeartbeatSecondsPath` on a JSONPath Task are parsed, path-checked and **never read**: no deadline, no heartbeat (run: still RUNNING after 3.5 s with a value of 1). A silent no-op that hangs an execution. | `internal/asl/exec.go` `taskDeadline`, `taskHeartbeat` (read `TimeoutSecondsState` and `HeartbeatSeconds` only); `analyse_state.go` | run | missing | **doc** (state-task page): "If you want to provide a timeout value dynamically from the state input using a reference path, use TimeoutSecondsPath. When resolved, the reference path must select fields whose values are positive integers." | `internal/asl` test: a path timeout of 1 ends TIMED_OUT; a non-positive value fails `States.Runtime`; both static and path is refused at analysis |
| SFN-37 | P1 | Validate and Create accept what the model's diagnostics refuse: no terminal state (`A -> A`; Create answered 200: run), a state name over 80 (Create 200: run), a Task `Resource` of `garbage` (Create refuses it only through the not-built path). The warnings `NO_DOLLAR`, `NO_PATH`, `PASS_RESULT_IS_STATIC` are never produced and `severity` is ignored (SFN-7). Every code is `SCHEMA_VALIDATION_FAILED`; the location is `States.A.Next`. | `internal/asl/analyse.go`, `analyse_state.go`; `actions_machines.go` `validateDefinition` | run | more forgiving, wrong shape | **model** (diagnostic codes in `ValidateStateMachineDefinitionDiagnostic`): `MISSING_END_STATE` "The workflow does not have a terminal state"; `STATE_MACHINE_NAME_TOO_LONG`; `INVALID_RESOURCE`; `INVALID_JSON_DESCRIPTION`; `MISSING_TRANSITION_TARGET`; `DUPLICATE_STATE_NAME`; the location `/States/<StateName>/<FieldName>`; "Do not rely on the exact order, count, or wording": the codes are what a tool keys on | One analyser test per code; a Validate test for each warning |
| SFN-38 | P1 | `RedriveExecution`: the 14 days are counted **from the execution's start**, not from when it finished (an execution that ran a day is non-redrivable a day early); the 24,999-event and one-year limits are absent; `redriveStatusReason` is doze's own wording, not the model's seven; `clientToken` idempotency keeps one token, with no expiry; `REDRIVABLE_BY_MAP_RUN` is never answered. | `actions_redrive.go` `redrivable`, `putRedrive`; `store_executions.go` `RedriveToken` | read, run (reason text) | stricter | **model**: "This period starts from the day a state machine completes its execution"; "The execution event history count is less than 24,999"; "maximum open time of one year"; `redriveStatusReason`: "Execution is RUNNING and cannot be redriven ... Execution redrivable period exceeded"; "idempotent responses for the last 10 client tokens ... valid for up to 15 minutes" | Unit tests on `redrivable` with a stop date 13 and 15 days old |
| SFN-39 | P2 | The console's Definition tab (and every Input and Output panel) shows a document **re-marshalled through a Go map**: keys sorted (run: `Accepted?` before `Validate`, `lines` before `orderId`), whitespace reset. **Save writes that text back**: `revisionOf` is a digest of the text, so an unedited save is a new revision and a deploy tool reads drift. | `internal/console/client_eb.go` `prettyJSON`, used by `client_sfn.go` (`DescribeStateMachine`) and `client_sfn_ops.go` | run | wrong | n/a | Browser test: save an unedited definition, the revision does not change |
| SFN-40 | P1 | The account and the region in an ARN are ignored: `arn:aws:states:eu-west-1:111111111111:stateMachine:tm` describes the local machine (run). A hard-coded account id works here and fails on deploy (SQS-23's class). | `stepfunctions/actions_machines.go` `nameFromARN` | run | more forgiving | **model**: the ARN form in the documentation, `arn:partition:states:region:account-id:stateMachine:name`; the error to answer (`StateMachineDoesNotExist`): memory | One test per ARN kind (machine, execution, activity, alias, version, map run) under a non-default `--account-id` |
| SFN-41 | P3 | The ledger contradicts the code in three places: "Express executions are durable ... you can call DescribeExecution on it afterwards" (the code answers `ExecutionDoesNotExist`, and the ledger's first section says they are memory-only); "Activity polling has no long-poll ceiling to speak of ... waits on a local condition rather than a 60-second server-side hold" (the code holds 60 s); "AWS keeps the distinction longer" (a day, then `TaskDoesNotExist`: neither half is established). | `docs/SUPPORT.md` "Differences from AWS" | read | n/a | n/a | Ledger test: each difference row names a test |
| SFN-42 | P2 | A stop records only `ExecutionAborted`: no `TaskStateAborted`, `WaitStateAborted`, `MapStateAborted`, `ParallelStateAborted`, `MapIterationAborted`, `MapRunAborted` (run: `1:ExecutionStarted 2:TaskStateEntered 3:ActivityScheduled 4:ExecutionAborted`). | `stepfunctions/engine.go` `finalize` | run | missing | **model**: `HistoryEventType` has all six; when AWS writes them is memory | History test: stop a Wait; skipped pending `abort_history` |
| SFN-43 | P2 | Tags: the character rule is not enforced (run: `a$b`, `aws:x`); 62 tags are accepted (the limit value is not in the model); `ListTagsForResource` returns them in Go map order, so two calls differ (run). | `actions_tags.go`; `actions_machines.go` `tagsOf`; `store.go` | run | more forgiving | **model**: "Tags may only contain Unicode letters, digits, white space, or these symbols: _ . : / = + - @"; `TooManyTags`. The limit value, the `aws:` prefix: memory | Rejection test for `a$b`; order test |
| SFN-44 | P3 | HTTP status: `ResourceNotFound` answers 400 (model: 404); `ServiceQuotaExceededException` (402) is never answered. `ConflictException` 409 and `TooManyTags` 400 are right. | `stepfunctions/errors.go` `errResourceNotFound` | run | wrong status | **model**: `httpError` 404, 402, 409, 400 | Status test per error |
| SFN-45 | P1 | An unsupported integration is refused at create with 400 `UnsupportedOperationException`, a code the model does not have; the protocol's refusal for a thing not built is 501 `NotImplemented` with `X-Doze-Not-Built` (D5). The console classifies it by sniffing the message (`handlers_surfaces.go`), and shows "does not build this *operation*". The families are not in `not-built.md`. | `stepfunctions/task.go` `refuseResources`; `errors.go` `errNotYet`; `stepfunctions.go` | run | wrong code | **model**: no `UnsupportedOperationException` among the 33 error shapes; protocol step 1 | Create test for `ecs:runTask.sync`: 501, the header, a `not-built.md` row. D3 |
| SFN-46 | P3 | The HTTP header `X-Amzn-Trace-Id` is not read by `StartExecution` or `StartSyncExecution`. | `actions_executions.go`, `actions_express.go` | read (no reference in the package) | missing | **model**: "If both sources are provided, Step Functions will use the header value (preferred) over the value in the request body" | SDK test with both |
| SFN-47 | P3 | `modelcheck` measures string length in **bytes** for every string. The payload members are bytes by their documentation; a `Name` (1 to 80), a tag key (128), a description (256) are Smithy `length` and count code points: a 41-character name of two-byte letters is refused (run). | `internal/modelcheck/modelcheck.go` `lengthOf` | run | stricter | memory (the trait's meaning; the model's docs say "expressed as bytes in UTF-8" only for payloads, and the spec says a state name is "80 Unicode characters"). Recording queue, then the foundation's call | Name of 41 `é` |
| SFN-48 | P3 | Smaller wrong shapes: `DescribeMapRun`'s `itemCounts` and `executionCounts` are the same map (they differ with an `ItemBatcher`); `failuresNotRedrivable` and `pendingRedrive` are constant 0; when both tolerated-failure settings are set the percentage is ignored; `Credentials` is not echoed as `taskCredentials` in `TaskScheduled` and the Lambda events. | `actions_mapruns.go`; `store_mapruns.go` `tolerated`; `task.go` | read | wrong shape | **model**: `MapRunItemCounts` and `MapRunExecutionCounts`; `taskCredentials` | Per-member tests |

**From the guide pages (`doc`, fetched 2026-10-11).**

| Id | Sev | What | Where | Evidence | Direction | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| SFN-49 | P0 | `States.ALL` matches **every** error name here, including `States.Runtime` and `States.DataLimitExceeded`. A Catch-all swallows a runtime error locally that fails the execution on AWS. Board Sfn-12 drew it as the design ("CAUGHT_ERROR ... States.Runtime. Its Catch ran"). | `internal/asl/retry.go` `matches` (`ErrAll` returns true) | read; run (the Express callback run is caught by a `States.ALL` Catch) | more forgiving | **doc** (error-handling page): "`States.ALL` ... cannot catch the `States.DataLimitExceeded` terminal error or `Runtime` error types"; "A `States.Runtime` error isn't retriable, and will always cause the execution to fail. A retry or catch on `States.ALL` won't catch `States.Runtime` errors."; `DataLimitExceeded` can be caught or retried "by explicitly specifying it in the ErrorEquals" | `internal/asl` test: `States.ALL` does not match `States.Runtime` or `States.DataLimitExceeded`; an explicit `States.DataLimitExceeded` does |
| SFN-50 | P1 | A `Retry` or `Catch` naming `States.Timeout` does not match a missed heartbeat: the failure is named `States.HeartbeatTimeout` and `matches` is exact (run: a `States.Timeout` retrier did not retry). The two pages disagree on the name the event carries: the Task page says a missed heartbeat "fails with a States.Timeout error"; the error page lists `States.HeartbeatTimeout` as its own name and also defines `States.Timeout` as "failed to send a heartbeat for a period longer than the HeartbeatSeconds value". Under the second, `States.TaskFailed` must not match it either. | `internal/asl/retry.go` `matches`; `scheduler.go` `fireDue` | run | stricter | **doc**: both pages, quoted. The history's error name: not established (`heartbeat_error_name`) | `internal/asl` test: a retrier on `States.Timeout` matches a heartbeat failure (the rule is established); the name in the event waits for the recording |
| SFN-51 | P2 | The analyser has no bounds on a retrier or on the names it can check: `IntervalSeconds` must be a positive integer (max 99,999,999); `MaxAttempts` max 99,999,999; `MaxDelaySeconds` greater than 0 and less than 31,622,401; `JitterStrategy` FULL or NONE; both a static and a `...Path` timeout; a state name over 80. Only `MaxAttempts < 0` and `BackoffRate < 1` are checked. | `internal/asl/analyse_state.go` lines 118 to 122 | read | more forgiving | **doc**: error-handling page ("has a maximum value of 99,999,999"; "You must specify a value greater than 0 and less than 31622401"; "FULL or NONE"); state-task page ("cannot include both"); spec ("its length MUST BE less than or equal to 80 Unicode characters") | One analyser test per bound |
| SFN-52 | P1 | An activity task's `TimeoutSeconds` and `HeartbeatSeconds` start at **schedule** here (a task nobody polls times out); the guide says they start at the claim. The code comment says "AWS counts HeartbeatSeconds from ActivityScheduled" (memory); section 5 of this document said the same. | `stepfunctions/activity.go` `scheduleActivity`, `activityStarted`; `internal/asl/exec.go` `advanceTask` | read | stricter | **doc** (state-task page): "For Activities, the count begins when `GetActivityTask` receives a token and `ActivityStarted` is logged in the execution event history." Memory disagrees (an unpolled task times out); queue first | Engine test: a queued task with `TimeoutSeconds: 1` and no worker. Recording first: `activity_timeout_clock` |
| SFN-53 | P1 | An `InputPath` or `OutputPath` that selects nothing yields `null` and the state runs on (run: a Task with `InputPath: "$.nope"` on `{}` called its resource). | `internal/asl/payload.go` `selectPath` (`!ok` returns `nil, nil`) | run | unverified | memory: the guide's `States.Runtime` text is about "applying InputPath or OutputPath on a null JSON payload", which is not a missing field. Unverified until `input_path_missing` is recorded | `internal/asl` test, skipped pending the recording |

Owned by the foundation, and referenced, not repeated:

- **A2** — the "why" line under a refusal. *Landed.* Step Functions must still
  supply the fact that tells "Refused" from "Not built" apart; SFN-45 is that
  fact.
- **A7** — `copy.go`'s "Select a state machine…" line never renders;
  `sfnMachines` opens the first machine.
- **A12** — "Execution stopped" for an execution that had already finished;
  "Published version 1" and "Alias created" for a repeat that changed nothing;
  "Version 5 deleted" for a version that never existed.
- **A13** — the delete and stop confirms (SFN-1 and SFN-2 are the behaviour
  behind them; the sentence is fixed in whichever direction those are decided).

## 5. What to check

Beyond the common criteria. Step Functions speaks one protocol, so each line is
checked once, through the Go SDK v2 unless it says otherwise. Boxes are
unticked; the stage ticks them.

**Machines**
- [ ] Name: 1 to 80, the forbidden character set (the model's: white space,
      `< > { } [ ]`, `? *`, `" # % \ ^ | ~ ` $ & , ; : /`, control characters
      `U+0000-001F` `U+007F-009F` `U+FFFE-FFFF`, surrogates, `U+10FFFF`), and the
      message text (SFN-10).
- [ ] Re-create with the same name, definition, type, logging, tracing,
      encryption, `publish` and `versionDescription` succeeds; with any of those
      changed it answers `StateMachineAlreadyExists`; with only a different role
      or tags it succeeds and changes neither *(corrected 2026-10-11: was "any
      one of definition, role, type changed conflicts"; SFN-23)*.
- [ ] `roleArn`: the shape rule (memory), on create and update; TestState
      accepts none today (SFN-35).
- [ ] `type` cannot be changed by update (model: "You cannot update the type of
      a state machine once it has been created").
- [ ] Update with nothing: `MissingRequiredParameter` *(corrected 2026-10-11:
      was "AWS is believed to"; model; SFN-24)*. Logging-only: recorded first.
- [ ] Delete: twice (succeeds), with a running execution (SFN-1), a labelled ARN
      (SFN-31), and what remains readable.
- [ ] `loggingConfiguration`: each level, `includeExecutionData` false, a
      destination that does not exist, two destinations, a group ARN without
      `:*`, `level` other than OFF with none, and Express with logging off
      (SFN-25).
- [ ] `tracingConfiguration` and `encryptionConfiguration` round-trip and say
      cosmetic in the ledger; the three `Invalid...Configuration` refusals.
- [ ] `includedData: METADATA_ONLY` answers a definition of `{}` (SFN-30).

**The analyser** (`internal/asl/analyse*.go`). One test per rule, each naming
the page of the States Language specification it comes from.
- [ ] `StartAt` present and naming a state; at least one state; unreachable
      states; **a terminal state** (`MISSING_END_STATE`, SFN-37).
- [ ] `Next` or `End`, never both, never neither; Succeed and Fail take neither;
      Choice takes neither.
- [ ] Each state type's allowed fields; a misplaced field is named.
- [ ] Choice: non-empty `Choices`, top-level `Next`, one combinator, operand
      type per operator, timestamps, `...Path` operands.
- [ ] Wait: exactly one of the four; `Seconds` not negative.
- [ ] Retry and Catch: only on Task, Parallel, Map; `ErrorEquals` non-empty;
      `States.ALL` alone and last; reserved `States.*` names; `MaxAttempts` 0 to
      99,999,999; `BackoffRate` at least 1; `IntervalSeconds` positive to
      99,999,999; `MaxDelaySeconds` over 0 and under 31,622,401; `JitterStrategy`
      FULL or NONE *(corrected 2026-10-11: was "establish AWS's"; doc; SFN-51)*.
- [ ] Machine `TimeoutSeconds` positive; Task `TimeoutSeconds` and
      `HeartbeatSeconds` positive integers, heartbeat smaller (SFN-6); not both a
      static and a `...Path` field (SFN-36).
- [ ] Paths and reference paths; `ResultPath` may not write to the context object.
- [ ] JSONata: a JSONPath-only field on a JSONata state and the reverse; no
      downgrade to JSONPath inside a JSONata machine.
- [ ] Type-dependent rules: what an EXPRESS machine may not contain (SFN-3), and
      what `ValidateStateMachineDefinition`'s `type` changes.
- [ ] Limits: state name 80 *(model + spec; SFN-51)*, definition size 1,048,576
      (enforced). `MaxConcurrency` bounds: not established.
- [ ] Validate and Create agree, or the disagreement is exactly the "Not built"
      set and both say so (SFN-7, 8, 37, 45).
- [ ] The diagnostic codes and warnings of the model, `severity`, `maxResults`,
      `truncated` (SFN-7, 37).

**The interpreter**
- [ ] Each of the 18 intrinsics against AWS's documented examples, and its
      failure name (`States.IntrinsicFailure`).
- [ ] `InputPath` null and absent; `ResultPath` null; `ResultPath` merges into
      the raw input; `OutputPath`; a path that selects nothing (SFN-53).
- [ ] Context object: every `$$` field a definition can read. The list the code
      builds is in section 2; AWS's is memory.
- [ ] JSONPath `Assign` and variables (held: SFN-20 resolved).
- [ ] Choice: no match and no Default is `States.NoChoiceMatched`; a missing
      variable is `States.Runtime`; `IsPresent`.
- [ ] Wait: seconds, timestamp, both paths, a timestamp in the past.
- [ ] Parallel: one branch fails, the others are cancelled, the error is
      `States.BranchFailed` or the branch's own. Establish which.
- [ ] Map: `MaxConcurrency` 0 and 1, tolerated failures, `ItemSelector`,
      an empty array, a non-array `ItemsPath`.
- [ ] Payload limit: a state output over 256 KiB is `States.DataLimitExceeded`
      (doc: the error is defined; the quota value is on a page not read), and it
      is not caught by `States.ALL` (SFN-49).

**Retry, Catch and timeouts**
- [ ] Backoff: interval times rate to the power of attempts, `MaxDelaySeconds`,
      `JitterStrategy: FULL` (0 to the computed delay: doc).
- [ ] `MaxAttempts: 0` means never retry; an exhausted retrier does not fall
      through to the next *(doc: the guide's example 4)*.
- [ ] `States.TaskFailed` matches everything but `States.Timeout`; `States.ALL`
      matches everything **except `States.Runtime` and `States.DataLimitExceeded`**
      *(corrected 2026-10-11: was "whether States.ALL catches States.Runtime ...
      is not established"; doc; SFN-49)*.
- [ ] A missed heartbeat matches a retrier on `States.Timeout` (SFN-50).
- [ ] Each retry attempt gets a fresh timeout **and** a fresh heartbeat (SFN-4).
- [ ] The task token across a retry (SFN-12).
- [ ] The history of a retry and of a catch, event by event (SFN-5).
- [ ] The machine timeout fires during a Wait, during a parked task, and during
      a retry wait; the execution ends `TIMED_OUT` with `States.Timeout`, and a
      parked token answers `TaskTimedOut` afterwards (SFN-2).

**Executions**
- [ ] Start: name rules, generated name, input not JSON, input over 262,144
      **bytes**, same name and same input while running (the original), same
      name and a different input while running (`ExecutionAlreadyExists`), same
      name after it finished (`ExecutionAlreadyExists`; reusable after 90 days:
      listed difference), an Express name reused at once.
- [ ] Start through a version and through an alias; both ARNs on the execution
      (a version ARN does not record an alias: model); weighted routing with a
      fixed seed (`pickRoute` uses `math/rand/v2` unseeded: a seam is owed).
- [ ] Stop: running, finished, twice; with `error` and `cause`; its Map Runs;
      a `.sync` child keeps running (memory); the token afterwards (SFN-2).
- [ ] Redrive: FAILED, ABORTED, TIMED_OUT; refused when running, succeeded,
      Express, 14 days after it **finished**, nothing left to redrive;
      `clientToken` (10 tokens, 15 minutes); the redriven task's token and input
      (SFN-38).
- [ ] `ListExecutions`: each status filter, `redriveFilter`, `mapRunArn` and
      `stateMachineArn` together (refused), `PENDING_REDRIVE` with a machine ARN
      (refused), a version or alias ARN, the sort, paging at 100, a stale token
      (SFN-27, 28).
- [ ] `GetExecutionHistory`: paging forward and reversed, `includeExecutionData`
      false, event ids and `previousEventId` chains in a Parallel.
- [ ] Express: `StartExecution` answers an ARN nothing can describe;
      `StartSyncExecution` on a Standard machine; the 5-minute cap; the host
      prefix under `--listen`; `inputDetails.included` (SFN-29).

**Task tokens and activities**
- [ ] `SendTaskSuccess` with output that is not JSON, over 262,144 bytes, and empty.
- [ ] `SendTaskFailure` with no error; the error name reaches Retry and Catch.
- [ ] A token used twice, after its task timed out, after stop, after the
      machine is deleted: `TaskTimedOut` *(corrected 2026-10-11: was "one day of
      TaskTimedOut, then TaskDoesNotExist"; the model gives the first half and
      names no second; SFN-22)*. A garbage token: recorded.
- [ ] The 19 model cases for the four `needState` operations, replayed with a
      fresh token or execution per case instead of skipped.
- [ ] `GetActivityTask`: FIFO, one worker per task, the empty poll, a cancelled
      request, a deleted activity, `workerName` over 80.
- [ ] `ActivityScheduled`, `ActivityStarted`, `ActivitySucceeded`,
      `ActivityFailed`, `ActivityTimedOut`, `ActivityScheduleFailed` in the history.
- [ ] Timeout and heartbeat counted from the worker's claim *(corrected
      2026-10-11: was "Heartbeat counted from schedule, not from the worker's
      claim"; doc; SFN-52, recorded first)*.

**Versions, aliases, tags**
- [ ] Publish: unchanged definition, `revisionId` mismatch (`ConflictException`),
      `revisionId: INITIAL`, 1,001st version, description length 256.
- [ ] Delete a version an alias routes to; delete one that does not exist.
- [ ] Alias: name pattern (a name with no letter, `_`, `-` or `.` refused), one
      route not 100, two routes not summing to 100, the same version twice, a
      third route, a missing version, a repeat create with the same and with a
      different routing, the 101st alias, list order.
- [ ] Tags on a machine and an activity: add, overwrite, remove, an ARN nothing
      holds, the character rule, `TooManyTags` (value: memory), key and value
      lengths, a repeat create leaves them alone.

**Map Runs**
- [ ] One run per Distributed Map state; counts move; `ListExecutions` hides
      children without `mapRunArn`, and answers `mapRunArn` and `itemCount` with it.
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
- [ ] A restart inside a DELETING window, if D1 models one.

## 6. Console

The design's "Step Functions" page: 26 boards. Templates: `templates/sfn.html`,
`sfn_more.html`, `sfn_graph.html`, the `sfn_create` block in `create.html`,
shared pieces in `panes.html`, `layout.html` and `logs.html`. Handlers:
`handlers_sfn.go`, `handlers_sfn_ops.go`, `handlers_sfn_versions.go`,
`handlers_sfn_activities.go`; routes in `routes_compute.go` `sfnRoutes`
(33 routes, counted).

The older single board `Machine` is dropped. It drew per-state milliseconds on
graph nodes, a state inspector with Send buttons on the graph, and a dock of
"calls this execution made". None of the three exists or is in this plan.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Sfn-Home | Machines, none open | `sfn_home`, `empty_detail` | the line exists and never renders (A7) |
| Sfn-01-None | No state machines yet | `sfn_home`, `empty_detail` | exists |
| Sfn-02-Create | Create, Standard | `sfn_create`, `sfnCreate`, `sfnValidate` | form exists, one column; teaching lines, the type cards and the rule digest are **new** |
| Sfn-03-Create-Express | Create, Express | `sfn_create` | the log-group line and "what Express changes" are **new** |
| Sfn-04-Create-Refused | Validate's list, and Create refused | `sfn_diagnostics`, `fail_inline` | exists; the "Not built" wording is A2's |
| Sfn-05-Executions | Executions | `sfn_machine`, `sfn_executions` | exists |
| Sfn-06-Executions-Empty | Nothing run; filter empty; Express | `sfn_executions`, the Express branch of `sfn_machine` | exists |
| Sfn-07-Start | Start execution | `sfn_machine` (tab `start`), `sfnStart` | form exists, one column; teaching lines and "where it begins" are **new** |
| Sfn-08-Start-Refused | Start refused | `fail_inline` | exists |
| Sfn-09-Express-Run | Run synchronously, result, failed run | `sfn_sync_result`, `sfnStartSync` | exists |
| Sfn-10-Graph | The definition drawn | `sfn_graph`, `sfn_graph_svg` | exists; the legend panel is **new** |
| Sfn-11-Definition | Definition | `sfn_machine` (tab `definition`), `sfnUpdateDefinition` | editor exists; the strip and "what is checked" are **new** |
| Sfn-12-Definition-Test | Save refused; Test a state | `sfn_test_panel`, `sfn_test_result`, `sfnTestState` | exists |
| Sfn-13-Versions | Versions and aliases | `sfn_versions_tab`, `sfn_version_detail`, `handlers_sfn_versions.go` | exists, one column; teaching lines are **new** |
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
| Teaching lines under name, role, type, input, alias weights | 02, 03, 07, 13 | The settings record (new, `stepfunctions/settings.go`): lengths and ranges from `stepfunctions/validate.go`, tiers from section 2 |
| "What Validate reports" | 02 | A list held next to the analyser, one entry per rule with its message format and its **source** (spec page, or "doze-aws's own"), tested against `internal/asl/analyse*.go` so it cannot drift. It is typed prose on the board today |
| "Its history will go to /aws/vendedlogs/states/<name>" | 03 | The name field and the type; the group name is `client_sfn.go` `DescribeStateMachine`'s fallback. A doze-aws behaviour, said as one |
| "Not built" wording for unsupported resources | 04, 12 | The refusal's source, per foundation A2 and SFN-45 |
| Where it begins | 07 | `StartAt` and the first transitions of the saved definition |
| The life of one waiting task | 11 | The chosen Task's `TimeoutSeconds`, `HeartbeatSeconds`, `Retry`, `Catch`, and the machine's `TimeoutSeconds`. A pure function of the definition. After SFN-52 the clock starts at the claim for an activity |
| Graph legend | 10 | Static; each sentence is `sfngraph`'s own rule (`Start`, `Kind`, edge kinds, `Container`) |
| The execution name as the log chip | 14 | The execution ARN on each record |
| Waiting on a token | 16 | The last `TaskScheduled` or `ActivityScheduled` event's time, and the frozen definition's heartbeat and timeout |
| Where it lands | 18 | The frozen definition's `Retry` and `Catch` for the parked state, matched with the rules in `internal/asl/retry.go` (after SFN-49: `States.ALL` does not catch `States.Runtime`) |
| The redrive reason shown inline | 19 | `DescribeExecution`'s `redriveStatusReason`, which is a hover title today; the model's seven reasons after SFN-38 |
| The machine has moved on | 20 | `revisionId` from `DescribeStateMachineForExecution` against `DescribeStateMachine` |
| Counts in the delete confirm | 24 | `ListStateMachineVersions`, `ListStateMachineAliases`, `ListExecutions` (RUNNING only) |
| Why it failed | 25 | The failing event, the scheduling event before it, and the frozen definition |

**Where each teaching line's facts come from**

| Line | Source |
|---|---|
| Name 1 to 80 and the character set | `nameRule`, `checkName`; model length and the model's list (including `< >`) |
| Role ARN required, shape checked, not evaluated | `validRoleARN`; section 2 tier C |
| Standard and Express differences | `actions_executions.go` (`listExecutions`, `executionOf`), `actions_express.go`, `express.go` `expressMaxDuration`, `actions_redrive.go` `redrivable`; the six operations the model says Express does not support |
| Input up to 262,144 **bytes**, JSON, empty means `{}` | model (bytes, UTF-8); `startExecution`. The model says "you still must include the two braces"; empty-means-`{}` is doze-aws's behaviour, stated as that |
| Same name, same input, still running answers the original | model (idempotent for STANDARD); `startExecution` |
| Alias weights, a name that is not a number | model (0 to 100, sum 100, the pattern); `actions_aliases.go` |
| Publishing an unchanged definition | model; `actions_versions.go` |
| Task timeout and heartbeat semantics | `exec.go` `taskDeadline`, `taskHeartbeat`; `scheduler.go` `fireDue`; the state-task page |
| Retry defaults and backoff | `retry.go` `nextRetry`; the error-handling page |

**The five lines, per setting** (L11: name and unit; the value in words; one
sentence of what it does; range, default and enforced or stored; the neighbour
check with real numbers, live in the form). Today a field has one hint.

| Setting | On | Lines 1 to 3 | Line 4 | Line 5 |
|---|---|---|---|---|
| Name | create machine, activity, start, alias | the name, its character class | 1 to 80, the list, enforced (model) | none; quiet |
| Role ARN | create | what it is for, "nothing evaluates it" | required, shape enforced (memory), tier C | the account and region of the stack against the ARN (build) |
| Type | create | what changes (6 operations, name reuse) | STANDARD or EXPRESS, default STANDARD, enforced | the definition's callback Tasks against Express (SFN-3) |
| Input | start, test, send | the first state's input | 262,144 bytes, JSON, enforced | none |
| Run as | start | version or alias | default the current definition | the alias's weights and versions, live |
| Alias routing | versions | one version, or two with weights | 1 to 2 entries, 0 to 100, sum 100, enforced | the live sum "60 of 100" |
| Publish description | versions | kept on the version | 0 to 256 | none |
| TimeoutSeconds, HeartbeatSeconds | definition (life strip) | what a timeout and a heartbeat do | positive integers, heartbeat smaller (after SFN-6), default 99,999,999 (doc) | the strip: attempts times timeout, plus waits, against the machine's `TimeoutSeconds` |
| Max concurrency | map run | children at once | 0 means unlimited here (40 launched at a time, a listed difference) | the item count |
| Mock, inspection | test | what each level returns | INFO, DEBUG, TRACE; enforced | none |
| Task token | send | which task it answers | 1 to 2,048, enforced | the parked state's timeout and heartbeat remaining |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships. *Updated 2026-10-11; the earlier items for Sfn-03 and Sfn-24 are
resolved by the board corrections in 6a.*

- Sfn-02, Sfn-03: "capped at 5 minutes" (memory: Express quota; the guide page
  was not read).
- Sfn-04: "AWS refuses this too" for each analyser rule: the rules are States
  Language rules; the specification is `doc` for `States.ALL` alone and last,
  and positive timeouts; "MaxAttempts may not be negative" and "an error name
  ASL defines" are memory.
- Sfn-11, Sfn-25: the missed-heartbeat error name (`States.HeartbeatTimeout`
  against `States.Timeout`: SFN-50), and "a fresh 60 s" after a retry (SFN-4).
- Sfn-16: "fails with States.HeartbeatTimeout in 42 s" (the same, and SFN-52:
  from the claim for an activity).
- Sfn-17: the `↻1` and `2×` marks are drawn from `sfn_graph_svg` and
  `overlay.go`, not from a rendered run. The legend also keeps "caught", which
  cannot appear until SFN-5 is decided.
- Sfn-19: "Another tab redrove it first" as the cause of
  `ExecutionNotRedrivable: the execution succeeded`.
- Sfn-24: "A child it started with .sync keeps running" (the board now says it
  is doze-aws's; AWS: not established).
- Sfn-25: the 60-second history is modelled on a 2-second run. The event order
  is real; the timings are not.
- Every board: execution names, counts and ages are illustrative.

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each board from the working copy
(`console-design/project/Sfn-*.dc.html`, 1440 by 900, opened under `file://`)
and the real page, in the same state, against a binary built that day, with
seeded data of the board's own names (`harbour-order-fulfilment` with a running,
a succeeded, a failed and an aborted execution; two versions and two aliases;
an Express machine; a Distributed Map; an activity). Screenshots:
`scratchpad/dcheck/sfn/board-*.png` and `real-*.png`, light, dark for three
pairs, and 900 wide for two. The six console tests and the 26 browser tests
(not re-run for this review) assert content and routes, never shape, which is
the SQS lesson.

**What every real page differs from every board in, once** (a shell decision,
not re-opened here, the same as SQS 6a's last row): the board draws a
marks-only rail, a top bar with the stack's name, and a list pane headed
"State machines N [New]"; the page draws the full rail with labels, a theme
switch and a region chip, and a list pane headed "Step Functions (N)" with a
filter and a `+`. The page's header block (title, ARN chip, fact strip, tabs)
matches the board's shape. The per-service icons are kept (L12).

| Board | Verdict | What differs |
|---|---|---|
| Sfn-Home | differs, known (A7) | The board shows nothing selected with one sentence; the page opens the first machine. |
| Sfn-01 None | matches | The board's heading line "No state machines yet" is missing from the empty detail (the sentence is there once, in the list pane and below the icon). The service icon stands in for the `SF` tile. |
| Sfn-02 Create | **differs** | Two columns on the board (General and Definition left, "What Validate reports" right); one column on the page. Type is two cards with a sentence each on the board and a `<select>` on the page. Name and Role ARN are side by side with five-line teaching on the board and stacked with a one-line hint on the page. The page has no rule digest, no "functions in this stack a Task can call" line, no live "Definition is valid" line until Validate is pressed, and has a Format button the board does not draw. |
| Sfn-03 Create Express | **differs** | None of the three Express additions exist: the log-group line under Name, the right-hand "What choosing Express changes", the callback callout. The page's `<select>` option text carries two of the same sentences. |
| Sfn-04 Create Refused | **differs** | The right-hand column of other refusals is absent. The diagnostics list and the `InvalidDefinition` card are there, below the editor, and match in content. The unsupported-resource refusal reads "Not built ... does not build this operation", which is wrong for a task resource (the board's sentence is the right one). The "why" line under `InvalidDefinition` is the foundation's ("doze-aws refused the request: it breaks a rule of the service"), which says less than the corrected board. |
| Sfn-05 Executions | **differs** | The two footnotes under the table are absent ("Most recent first ...", "An execution started through a version or an alias ..."). The table, filter and fact strip match. |
| Sfn-06 Executions Empty | **differs** | The Express "Where an Express run is written down" callout and its Logs link are absent; the empty states and their buttons match. |
| Sfn-07 Start | **differs** | Two columns on the board (form, "Where it begins"); one on the page, with Name and Run as stacked, an editor full width, no teaching lines 3 to 5 and no "Where it begins". The cli row has its explanatory sentence on the board only. |
| Sfn-08 Start Refused | **differs** | The right-hand column of other refusals is absent; the board's "Open HRB-2026-04811 →" link and its two sentences about why are absent. The refusal card is under the Start button as drawn. |
| Sfn-09 Express Run | **differs** | Name and Input are one row on the board and stacked with a large editor on the page. The Result panel matches in content and shape. The right-hand "bad days" column (a failed run, an input refusal) is absent, and the note that the memory figure is a fixed number is cut to "as on AWS, where only a log group remembers" (an AWS claim that is memory). |
| Sfn-10 Graph | **differs** | The right-hand "Reading the picture" legend is absent. The StartAt node's heavy border is not drawn. At this size the catch edge's label sits on the default edge (`States.ALL` over the `default` line). |
| Sfn-11 Definition | **differs** | Two columns on the board with "The life of one waiting task" and "What is checked here"; one column on the page with the editor and, below it, Test a state. The editor shows the definition with its keys sorted (SFN-39). The board says "revision ... · role" in the header: matches. |
| Sfn-12 Definition Test | **differs**, and the design was wrong in one card | The test panel's four fields exist (State, Inspection, Mock, Input) in one row on the page as on the board, but the result is single column and there is no "Other things a test answers" column. The Save-refused card is the foundation's "Not built". The design's "Reserve stock ... CAUGHT_ERROR ... Mocked or not" was the bug drawn as the spec (SFN-35, SFN-49): corrected. |
| Sfn-13 Versions | **differs** | Two columns on the board (versions, version detail and aliases left; "Create alias" and the refusals right); one column on the page, Create alias last and full width. The "Publishing a definition that has not changed ..." sentence, the inline "Version 2" snapshot (the page loads it on a click) and the refusals column are absent. The alias routing edit is inline on the board and a pencil on the page. |
| Sfn-14 Logs | **differs** | The Standard-off panel lacks its second paragraph ("There is no field for it here ..."). The Express log tail's request chip overlaps the message (SFN-14); the board's execution-name chip does not exist. |
| Sfn-15 Tags | **matches**, minus notes | The draft editor is the shared one (A5). The page has no teaching notes and no "what Save can come back with" column. |
| Sfn-16 Execution Running | **differs** | History left with a right-hand column on the board ("Waiting on a token", then "Send task result"); history over the form on the page, stacked. The page's rows are two lines tall (a Δ under the elapsed time), so nine rows fill the screen; the board's are one line. The "waiting on a token" callout is absent. |
| Sfn-17 Execution Graph | **differs** | The graph and the history are side by side on the board; stacked on the page. The page has the legend (the board's, matches); the "An execution that retried" panel is absent. |
| Sfn-18 Task Result | **differs** | The board is three forms and their refusals; the page has one form with Success and Failure as a segmented control and the refusal under Send. "Where it lands" and the "Then the history reads" previews are absent. The refusal texts were corrected on the board (SFN-22). |
| Sfn-19 Execution Failed | **differs** | The board has a right-hand "Redrive" card and "When Redrive is not there" with the reasons; the page has a header button only, and the reason is a hover title. The page's error row is inline in the table (matches the board's opened row). |
| Sfn-20 Execution IO | **differs**, one gap | Input and Output are two cards on both, and match. The Definition tab lacks "The machine has moved on". Both show their JSON re-sorted (SFN-39). |
| Sfn-21 Activities | **differs**, minor | The Create field's teaching line, the cli sentence and the "no count of queued tasks" footnote are absent; the right-hand "when there are none" and refusals columns are absent. |
| Sfn-22 Activity Task | **differs** | The board is one page of three states side by side with a "Back to Activities" link; the page shows each state inline under the activities table. States 1 and 2 were rendered and match in content (the claimed task's token chip, input, the token filled in, Success and Failure, Heartbeat, Send); "Take a task again" and "the poll is cut at two seconds" are absent. State 3 (the receipt) was not rendered. The input is shown with its keys sorted (SFN-39). |
| Sfn-23 Map Run | **differs** | History left and the Map Run card under it, with Child executions in a right-hand card, on the board; the page puts the Map Run under the history and loads the children on a button. The refusal column is absent. The counts and "tolerates N failures / P%" match. |
| Sfn-24 Confirms | **differs** | The dialog has Cancel and Delete where the board has "Keep it" and Delete; the body is one false sentence for the machine delete ("Its executions and their history go with it") and "a parked task token stays unredeemable" for stop (A13, SFN-1, SFN-2). Version, alias and activity confirm text matches the board word for word; their button labels do not. |
| Sfn-25 Bad Heartbeat | **differs** | The "Why it failed" and "What you can do" panels are absent. The history row matches. |

**Where the design was wrong** (corrected in the working copy on 2026-10-11;
the published canvas still carries the old text until it is republished).
Eleven boards, in eleven files (Sfn-03, 04, 05, 07, 12, 14, 18, 19, 22, 24, 25):

- **Sfn-04**: "the rule comes from AWS's own service model" for the analyser's
  rules. The model carries none of them. Now says they are States Language rules
  with doze-aws's wording. The unsupported-resource card is labelled "Not built"
  (A2's vocabulary), not "Refused". The `StateMachineAlreadyExists` card now
  names what the model's idempotency check includes and that a different role or
  tags are ignored.
- **Sfn-03**: "AWS documents both as Standard-only" (a page not named). Now says
  whether AWS refuses at Create is not established. (The activity half is `doc`:
  the state-task page. The callback half is not.)
- **Sfn-05**: "Newest first". Now the model's order: running by start, finished
  by stop.
- **Sfn-07**: "up to 262 144 characters". The model says bytes, UTF-8.
- **Sfn-12**: the `.waitForTaskToken` card drew `States.Runtime` "mocked or not"
  as correct, and `States.ALL` catching a runtime error as "caught". Now the
  model's behaviour with the bug named (SFN-35); the Save-refused card is "Not
  built".
- **Sfn-14**: "An Express machine writes to /aws/vendedlogs ... even with logging
  off" read as an AWS fact. Now says doze-aws does it and that level OFF logs
  nothing.
- **Sfn-18**: "Answered for a day after the timeout, then it is TaskDoesNotExist
  like any spent token", and "A mistyped token, or one already answered" for
  `TaskDoesNotExist`. Now the model's wording for `TaskTimedOut` (expired, or the
  task is closed, which includes answered twice), and says which code a made-up
  token gets is not established.
- **Sfn-19**: "the execution started more than 14 days ago". The model counts
  from when it finished.
- **Sfn-22**: the same `TaskDoesNotExist` note as Sfn-18.
- **Sfn-24**: "Its 4 execution records are kept, and still open by their
  address" (the bug as the design; and a running execution is terminated) and
  "keeps running, as on AWS" (memory). Now what the model says about versions,
  aliases and running executions, and "doze-aws; not established for AWS".
- **Sfn-25**: "for a day, and then TaskDoesNotExist". Now "TaskTimedOut: the task
  it belongs to is closed".

**Build list, in order**

1. The settings record (`stepfunctions/settings.go`) and the five lines on
   every setting in the table above, through the kit's `setting_field`; the
   name rule's list generated from `nameRule`.
2. Create, two columns: the type cards, "What Validate reports" generated from
   the analyser's rule list with each rule's source beside it, the functions
   line, the Express panel and the log-group line (boards 02, 03).
3. Refusal columns (04, 08, 12, 13, 18, 21, 23): the right-hand "other refusals"
   cards, built from a list held next to the handlers and tested against them,
   not typed. A "Not built" sentence for a task resource (SFN-45).
4. Start, two columns with "Where it begins" (07); Express run with Name and
   Input on one row and the failed-run column (09).
5. Machine pages: the graph legend panel and the StartAt border (10), the
   definition's two columns and "The life of one waiting task" (11) after SFN-39
   so the editor does not rewrite the document, the versions two columns and the
   inline version snapshot (13), the logs chip (14, SFN-14).
6. Execution pages: history left, the right-hand column with "Waiting on a
   token" and the task form (16), graph beside history and the retried-node
   panel (17), "Where it lands" (18), redrive card and the reason (19), "The
   machine has moved on" (20), "Why it failed" (25), the Map Run card and the
   children card (23).
7. Confirms: bodies and labels from the store's counts, after D1 (24).
8. The page's own gaps the boards do not draw: logging, tracing, role and tags
   on create and on the Definition tab (SFN-18), history paging (SFN-15).
9. Behaviour a board needs: DELETING (24, if D1 chooses it); `TaskTimedOut` for
   a closed task (18, 22, 25); the model's redrive reasons (19).

## 7. Tests to add

| Layer | Tests |
|---|---|
| `internal/asl` | Heartbeat after retry (SFN-4); catch notes and the exit they should produce (SFN-5); Task timeout bounds (SFN-6); `TimeoutSecondsPath` and `HeartbeatSecondsPath` read (SFN-36); `States.ALL` does not match `States.Runtime` or `States.DataLimitExceeded` (SFN-49); a retrier on `States.Timeout` matches a missed heartbeat (SFN-50); the retrier bounds (SFN-51); one test per analyser rule naming its source; the token across a retry (SFN-12); JSONPath `Assign` read by a later state (SFN-20) |
| Engine, store | Delete then recreate (SFN-1); token after stop and after a machine timeout (SFN-2, 22); Express plus callback at create (SFN-3); UpdateMapRun on a finished run (SFN-13); the 101st alias and 1,001st version (SFN-33); a queued activity task with a timeout and no worker (SFN-52) |
| SDK v2 | Validate with `maxResults`, `severity`, `type`, and each code and warning (SFN-7, 37); the `InvalidName` text (SFN-10); `CreateStateMachine` repeats (SFN-23); `UpdateStateMachine` with nothing (SFN-24); the three configuration refusals (SFN-25); `CreateActivity` repeats (SFN-26); default page 100 on every list (SFN-27); `ListExecutions` rules (SFN-28); `StartSync` `InputDetails.Included` (SFN-29); `DescribeStateMachineForExecution` members and `METADATA_ONLY` (SFN-30); labelled ARNs (SFN-31); `ValidationException` `Reason` (SFN-32); `INITIAL` (SFN-34); every `TestState` parameter (SFN-35); another account and region in an ARN under a non-default `--account-id` (SFN-40); the tag character rule and order (SFN-43); every row of section 5's Executions and Versions blocks |
| Rejection parity | The four `needState` operations, with a fresh token or execution per case |
| Restart | Retry wait, tombstoned token, claimed activity task, Map Run mid-flight; a DELETING machine, if D1 models it |
| Conformance (boto3) | Retry and catch history; heartbeat and timeout; callback round trip; activity round trip; Express sync; versions and aliases; redrive; Distributed Map; each intrinsic; validate diagnostics; every row of section 8 |
| Console handlers | Validate then create disagreeing (SFN-8); `max=lots` (SFN-11); history over 1,000 events and 105 machines (SFN-15, 27); an unedited definition save does not change the revision (SFN-39); each confirm's sentence against the store |
| Browser | `a&b` in each name field (SFN-9); the log chip does not overlap (SFN-14); a state added and tested unsaved (SFN-16); each `new` item; Express executions tab; take a task and answer it; each board's shape, by a screenshot comparison on the kit's two-column container |
| Ledger | `sdkServices` against the documented list (SFN-21); `billingDetails` row (SFN-17); unsupported integrations in `not-built.md` (SFN-45); each "Differences from AWS" row names a test (SFN-41) |
| Mutation | Re-record both baselines; then survivors in `retry.go`, `exec.go`, `analyse_state.go`, `scheduler.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today, or a conflict inside AWS's own
text. *Re-tagged 2026-10-11 where the model or a page read settled it; those are
marked and stay in the queue only for the part still open.*

| Claim | Source now | Scenario |
|---|---|---|
| An EXPRESS machine with a `.waitForTaskToken` or activity Task is refused, and where (create or run) | doc for activities (state-task page); memory for callbacks and for where | `express_callback_at_create` |
| Task `TimeoutSeconds` and `HeartbeatSeconds` must be positive, and heartbeat below timeout | **doc** (spec; state-task page) *(was memory)*; the error code for a violation: memory | `task_timeout_bounds` |
| The default `TimeoutSeconds`: 99,999,999 (state-task page) against 60 (the specification) | doc, **conflict** | `task_default_timeout` |
| A retried task gets a fresh heartbeat interval | memory | `retry_heartbeat_clock` |
| A retried callback task gets a new token | memory | `retry_task_token` |
| A caught failure's history includes `TaskStateExited` | memory | `catch_history` |
| A retry's history: which events between the two `TaskScheduled` | memory | `retry_history` |
| A token after `StopExecution`, and after a machine timeout | model for the code (`TaskTimedOut`); memory for the exact answer | `token_after_stop` |
| A garbage task token: `InvalidToken` or `TaskDoesNotExist` | model lists both | `unknown_task_token` |
| A token after its task timed out, and for how long it stays `TaskTimedOut` | **model** for the code; the duration is memory *(was doc)* | `late_token` |
| A deleted machine's executions: describable, listable, under a recreated name, and the final status of a running one | model for the DELETING lifecycle; memory for the rest | `delete_then_recreate` |
| `DeleteStateMachine` leaves the machine `DELETING` while executions run | **model** *(was doc)* | `delete_with_running` |
| `InvalidName` message text | memory | `invalid_name_message` |
| `InvalidExecutionInput` and `InvalidOutput` message text | memory | `invalid_input_message` |
| Validate: diagnostic codes, warnings, `severity`, `maxResults`, `truncated` | **model** *(was doc)*; whether `type` changes the answer, and the location format in practice: memory | `validate_diagnostics` |
| Every analyser rule's message and location as AWS words it | doc (States Language specification) for the rule, memory for the wording | `definition_refusals` (extend `test_refusals`) |
| `StateMachineAlreadyExists` on a changed role or changed tags alone | **model, in conflict**: the operation says ignored, the error shape says conflict | `recreate_changed_role` |
| `UpdateStateMachine` with no changes; with logging alone | model for no `definition` and no `roleArn`; memory for logging alone | `update_nothing`, `update_logging_only` |
| Publishing an unchanged definition answers the existing version | **model** *(was memory)* | `publish_unchanged` |
| `revisionId: INITIAL` on a created machine | **model** *(new)* | `publish_initial` |
| A repeat `CreateStateMachineAlias`, same and different routing | model: "idempotent response" for the same; the code for a different one: memory | `alias_repeat` |
| `UpdateMapRun` on a finished run | **model** *(was memory)* | `update_finished_map_run` |
| TestState on a `.waitForTaskToken` Task, with and without a mock | **model** with a mock; without a mock: memory | `test_state_callback` |
| `States.ALL` and `States.Runtime`, `States.DataLimitExceeded` | **doc** (error-handling page) *(was memory)* | `catch_all_scope` |
| The error name of a missed heartbeat | doc, **conflict** (`States.Timeout` on the Task page; `States.HeartbeatTimeout` on the error page) | `heartbeat_error_name` |
| Where an activity task's timeout and heartbeat clocks start | doc (the claim) against memory (the schedule) | `activity_timeout_clock` |
| A Parallel branch failure: the error name the parent sees | memory | `parallel_branch_failure` |
| A Choice whose variable selects nothing fails `States.Runtime` | doc (the ledger cites it as AWS's; the error-handling page lists "a Choice state without a matching rule" as a definition issue) | `choice_missing_variable` |
| An `InputPath` or `OutputPath` that selects nothing | memory | `input_path_missing` |
| The per-resource tag limit and its error | `TooManyTags` and its 400: **model**; the value: memory | `tag_limit` |
| `StopExecution` on a finished execution answers its stop date | memory (the model lists no error for it) | `stop_finished` |
| Same name, same input, still running answers the original execution; closed or different input: `ExecutionAlreadyExists`; reuse after 90 days | **model** *(was doc)* | `start_idempotent` |
| The 18 intrinsics' results and failure names | doc | `intrinsics` (extend `test_the_states_language`) |
| `ListExecutions` with `redriveFilter` and a state machine ARN | **model, in conflict** | `list_redrive_filter` |
| A stop's `...StateAborted` events | model (the types exist); memory (when) | `abort_history` |
| A labelled ARN to Describe returns the Map state's definition and `label` | **model** *(new)* | `describe_labelled_arn` |
| The Express 5-minute cap, and Express's other limits | memory (page not read) | `express_limits` |
| Length of a `Name` in bytes or code points | memory | `name_length_multibyte` |
| An unknown account or region in an ARN: the error | memory | `foreign_account_arn` |

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
| Under enforce: the execution role is not evaluated; say so or evaluate it. A decision for stage 15. `Credentials` (a role a Task assumes) is the same decision | IAM |
| Machine, version, alias and activity through CloudFormation, SAM, CDK and Terraform; a definition change is an update, not a replace; a role change with the same definition is an idempotent create (SFN-23) | CloudFormation |
| The neighbours strip: the wiring graph has no Step Functions node, so a machine shows no neighbours. Whether to add one is foundation D's call | console |
| Many parked executions across a restart | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] SFN-1 to SFN-53 each closed, with the test that failed first named beside
      it, or moved to section 8 with its test written and skipped (SFN-12, 47,
      50, 52 and 53 are expected to move).
- [ ] Decided and written into `SUPPORT.md`: what delete does to executions
      (SFN-1, D1); the token after stop (SFN-2); Express plus callback (SFN-3);
      whether a retry reuses the token (SFN-12); whether a catch writes
      `TaskStateExited` (SFN-5); a different role on a repeat create (SFN-23,
      D5); `UpdateStateMachine` with logging alone (SFN-24, D4); the refusal for
      an unsupported integration (SFN-45, D3); the page size and the console's
      paging (SFN-27, D6).
- [ ] The settings table in section 2 has no "not established" left:
      `events:putEvents.waitForTaskToken`; the tag limit's value (stays
      memory, said so).
- [ ] The language table in section 2 has no "not established" left:
      the context object's fields (AWS's list), `Credentials`' tier is C and
      said so.
- [ ] Every analyser rule has a test naming its source (the specification page,
      the guide page, or "doze-aws's own"), and no rule in
      `internal/asl/analyse*.go` is without one.
- [ ] Validate and Create disagree only on the "Not built" set, the ledger
      lists that set, and the console says "AWS would accept this" for it and
      nothing else (SFN-7, 8, 37, 45, A2).
- [ ] Every unsupported integration family is a row in `not-built.md`, and
      answers 501 `NotImplemented` with `X-Doze-Not-Built` (D5).
- [ ] The four `needState` operations' 19 model cases are replayed; the ledger's
      "33 of 37" becomes "37 of 37" or says why not.
- [ ] Both wires: one protocol (`awsJson1_0`); each of the 37 operations has an
      accepted-as-the-reference-spells-it test through the Go SDK v2, and
      `StartSyncExecution` and `TestState` through the `sync-` host.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] Each asynchronous operation decided (L7): `DeleteStateMachine` (D1),
      executions and Map Runs (async, held), redrive (held), the "eventually
      consistent" reads (immediate; listed), `GetActivityTask` (60 s, held).
- [ ] The time- and consistency-based rules (L8) are each opt-in under
      `--sfn-strict`, off by default and in memory, or listed as a difference
      with a test.
- [ ] **The fidelity table in 6a exists, was made from rendered boards beside
      rendered pages, and no row says "differs" without a decision beside it**
      (built, or a deliberate deviation with its reason). Re-rendered at the end
      of the stage in light, dark and 900 wide.
- [ ] **Icons kept** (L12): the per-service icon on the rail, the resource and
      execution headers, the activities header and the empty state; no `SF` tile
      on a page.
- [ ] **The five teaching lines on every setting** in the table in section 6
      (L11), line 4 from the settings record, line 5 live with real numbers.
- [ ] All 26 boards implemented from the foundation's shell, and each `new`
      item reads only the data named for it. The rule digest on Sfn-02 is
      generated or held by a test, not typed.
- [ ] No statement on a Step Functions screen is in the "not yet backed" list
      in section 6.
- [ ] The six confirms say only what the code does after SFN-1 and SFN-2.
- [ ] The history, the machine list and the execution list page past 1,000 or
      say they are truncated, and past the new default of 100 (SFN-15, 27).
- [ ] The four name inputs' patterns equal the service's rules (SFN-9).
- [ ] `e2e/tests/stepfunctions.spec.ts` covers every route under `/sfn` (33
      routes) with the route gate on, and the accessibility sweep is clean on
      the seven machine tabs, the four execution tabs, create and activities.
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baselines re-recorded here, against 2026-10-10's: `stepfunctions/`
      790 killed, 168 survived, 7 timed out, 112 not covered (82.5%);
      `internal/asl/` 742 killed, 117 survived, 11 timed out, 228 not covered
      (86.4%). Now ____ killed, ____ survived, with reasons.
- [ ] Idle heap unchanged: the analyser's rule list for the console is built
      on first use.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 0 | **First act of the stage**: read the rest of the guide's state-type and integration pages (Pass, Choice, Wait, Parallel, Map, the callback and `.sync` pattern pages, Express vs Standard, quotas) and move each analyser rule from `memory` to `doc` or the queue, as this review did for three | this document, sections 5 and 8 |
| 1 | Contract: setting, language and integration tiers; the decisions D1 to D6 | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for SFN-1 to SFN-53 | `internal/asl/*_test.go`, `stepfunctions/*_test.go`, `internal/console/sfn_test.go`, `sfn_more_test.go` |
| 3 | Interpreter and analyser: heartbeat on retry, catch history, timeout bounds and `...Path` fields, `States.ALL` scope, `States.Timeout` and heartbeats, retrier bounds, MISSING_END_STATE and the diagnostic codes and warnings | `internal/asl/exec.go`, `retry.go`, `analyse.go`, `analyse_state.go`, `analyse_jsonata.go`; `stepfunctions/history.go`, `scheduler.go` |
| 4 | Store and engine: delete (D1), tokens on abort and timeout, Express at create, finished Map Runs, labelled ARNs, quotas, idempotency, activity clock | `stepfunctions/store_machines.go`, `store_executions.go`, `store_tokens.go`, `store_versions.go`, `engine.go`, `actions_machines.go`, `actions_aliases.go`, `actions_versions.go`, `task.go`, `actions_mapruns.go`, `activity.go` |
| 5 | API surface: Validate's parameters; the page size; `ListExecutions`; `TestState`; `reason`; the three configuration validators; message texts; statuses; `X-Amzn-Trace-Id`; ARN account and region | `stepfunctions/actions_machines.go`, `actions_executions.go`, `actions_teststate.go`, `actions_tasks.go`, `actions_redrive.go`, `actions_tags.go`, `errors.go`, `validate.go`; `internal/modelcheck` (SFN-47) |
| 6 | Section 5 sweep; the `needState` cases | `stepfunctions/rejection_parity_test.go`, `sdk_test.go`, `sdk_errors_test.go`, `sdk_versions_test.go`, `sdk_activity_test.go`, `sdk_express_test.go`, `redrive_test.go`, `maprun_test.go`, `internal/asl/analyse_test.go`, `exec_test.go` |
| 7 | Conformance scenarios and claims table | `conformance/tests/test_stepfunctions.py`, the claims file from foundation B1 |
| 8 | Console, in the order of the build list in 6a | `internal/console/templates/sfn.html`, `sfn_more.html`, `sfn_graph.html`, `create.html` (`sfn_create`), `handlers_sfn.go`, `handlers_sfn_ops.go`, `handlers_sfn_versions.go`, `handlers_sfn_activities.go`, `client_sfn.go`, `client_sfn_history.go`, `client_sfn_ops.go`, `client_sfn_versions.go`, `client_eb.go` `prettyJSON` (SFN-39), `sfngraph/overlay.go`, `static/graph.js`, `stepfunctions/settings.go` (new) |
| 9 | Browser tests, with a shape check per board | `e2e/tests/stepfunctions.spec.ts` |
| 10 | Ledger, changelog, status | `docs/SUPPORT.md` (and the three stale rows, SFN-41), `CHANGELOG.md`, `plan/README.md` |

Step 3 comes before step 4 because SFN-4, SFN-5, SFN-6, SFN-36 and SFN-49 are in
the interpreter, which has no store and is the cheapest place to make them fail.
Step 5 carries the console's paging (D6) with SFN-27.

**Prior art.** The Amazon States Language specification (states-language.net)
is the source for every rule in step 3 and the page each analyser test should
name; three AWS developer-guide pages (*Handling errors*, *Task workflow state*,
and, for the rest of the stage, the ones listed in step 0) are the other `doc`.
AWS's own `ValidateStateMachineDefinition` is the oracle for the analyser once
an account exists: it costs nothing to call and returns the diagnostics
directly, so `definition_refusals` can record every rule in one scenario.
Nothing else is cited here; no other emulator's handling of these cases was
read for this document.
