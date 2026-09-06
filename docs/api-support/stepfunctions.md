# Step Functions — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

Standard and Express workflows run locally against the services this stack
already serves. The Amazon States Language is parsed, statically checked and
executed by a pure interpreter (`internal/asl`) that knows nothing about AWS
calls; the service around it owns one driver goroutine that steps every
execution, writes every history event, and hands Task calls to transient
workers that never touch the store. A Standard execution is a serialisable
frame list, so a restart resumes every suspended state — a `Wait`, a Lambda
in flight, a branch parked on a task token, a child execution being waited
on, a Distributed Map fanning out — from what its frame says rather than from
a goroutine the restart lost. Express executions and TestState runs live in
memory only, as on AWS, where neither is listable after the fact.

Step Functions is the one service here on AWS JSON 1.0 rather than 1.1, signs
as `states` while targeting `AWSStepFunctions`, and spells its members
lowercase-initial (`stateMachineArn`). Error codes are spelled exactly as SDKs
match them; `InvalidDefinition` carries the first problem and a count, so a
definition with four mistakes takes one round trip to understand.

| Operation | Tier | Notes |
|---|---|---|
| CreateStateMachine | F | STANDARD and EXPRESS; idempotent on an identical definition, so an unchanged `cdk deploy` succeeds; task resources outside the local integration set are refused here, not on first execution |
| DescribeStateMachine / ListStateMachines | F | describe answers AWS's defaults for logging, tracing and encryption when none were given; list paginates with `maxResults` and `nextToken`, and a stale token is `InvalidToken` |
| UpdateStateMachine | F | running executions keep their frozen definition; `publish` mints a version in the same call |
| DeleteStateMachine | F | synchronous — AWS parks the machine in DELETING until executions drain; locally it disappears at once, and the call is idempotent so a repeated `cdk destroy` does not fail |
| ValidateStateMachineDefinition | F | the analyser exposed directly; every diagnostic in document order, none returned early |
| PublishStateMachineVersion / DeleteStateMachineVersion / ListStateMachineVersions | F | a version freezes the definition; `revisionId` guards a publish; a version an alias still routes to cannot be deleted |
| CreateStateMachineAlias / DescribeStateMachineAlias / UpdateStateMachineAlias / DeleteStateMachineAlias / ListStateMachineAliases | F | one or two weighted versions; StartExecution on an alias ARN picks a version by weight and records both on the execution |
| CreateActivity / DescribeActivity / DeleteActivity / ListActivities | F | control plane, paginated |
| GetActivityTask | F | long-poll, 60 s as on AWS; an activity Task state queues its input for the next worker, which answers through the SendTask* calls |
| TagResource / UntagResource / ListTagsForResource | F | tags are a `[{key,value}]` list, as on AWS, not the `{k:v}` map Lambda and DynamoDB use; an ARN nothing holds is `ResourceNotFound`, not an empty list |
| StartExecution | F | machine, version or alias ARN; same name + still RUNNING + same input returns the original execution rather than conflicting; an EXPRESS machine answers `StateMachineTypeNotSupported` and points at StartSyncExecution |
| StartSyncExecution | F | Express: runs to completion inside the call, five-minute cap, `billingDetails` and the `includedData` switch; reachable at `sync-aws.doze`, the host prefix every SDK's endpoint ruleset applies |
| TestState | F | one state in isolation, with `inspectionData` per `inspectionLevel`, `mock` results and errors, and `stateConfiguration`; the `sync-` host again |
| DescribeExecution / ListExecutions | F | status, `redriveFilter` and `mapRunArn` filters, `maxResults` and `nextToken`; `traceHeader` comes back only when StartExecution was given one; an EXPRESS machine's executions are not listable, as on AWS |
| StopExecution | F | also aborts the Map Runs the execution owns; a child started with `.sync` keeps running, as on AWS |
| RedriveExecution | F | FAILED, ABORTED or TIMED_OUT executions within 14 days; failed frames resume from the state that failed, finished branches are kept; `clientToken` idempotency |
| DescribeStateMachineForExecution | F | answers from the execution's frozen snapshot — what it is running, not what the machine says today |
| GetExecutionHistory | F | global event ids with per-frame `previousEventId` chains; `reverseOrder`; pagination; payloads are JSON-encoded strings, as the SDK types expect |
| SendTaskSuccess / SendTaskFailure / SendTaskHeartbeat | F | tokens minted before `Parameters` are evaluated so `$$.Task.Token` resolves, and persisted before the send so an instant reply finds them |
| DescribeMapRun / ListMapRuns / UpdateMapRun | F | one Map Run per Distributed Map state, with the item and execution counts AWS reports; UpdateMapRun changes `maxConcurrency` and the tolerated-failure settings of a run in flight |

Every one of the 37 operations in the `com.amazonaws.sfn` model is handled.
Nothing is staged and nothing falls through to `InvalidAction`.

## What runs, honestly

The interpreter speaks both dialects. In JSONPath it has the full `States.*`
intrinsic set (own scanner and parser: nested calls, escaped quotes), all
eight state types, `Retry` and `Catch` with the spec's defaults and jitter,
`Parallel`, inline `Map` with `MaxConcurrency` and `ItemSelector`, `Wait` on
seconds, timestamps and paths, and `Assign` with `$name` variable references
in `Parameters`, `Choice` comparisons and `Wait` paths. In JSONata
(`"QueryLanguage": "JSONata"` on the machine or on one state) it evaluates
`Arguments`, `Output`, `Assign`, `Condition`, `Items` and the `Wait` fields
with the AWS additions — `$states`, `$partition`, `$range`, `$hash`, `$random`,
`$uuid`, `$parse` — so a definition written in the current console runs
unchanged.

Task resources are the local integration set, each with `.waitForTaskToken`
where AWS offers it:

- Lambda by bare ARN or `arn:aws:states:::lambda:invoke`.
- `sqs:sendMessage`, `sns:publish`, `events:putEvents`, and the optimized
  DynamoDB `getItem`, `putItem`, `updateItem` and `deleteItem`.
- Activity ARNs, which park until a worker polls and answers.
- `states:startExecution`, plain, `.sync`, `.sync:2` or with a task token:
  the child runs in this process and the parent resumes when it finishes,
  which is why this is the one `.sync` integration that means something
  locally.
- `aws-sdk:<service>:<action>` for every service this stack serves —
  DynamoDB, EventBridge, SQS, SNS, SSM, Secrets Manager, Kinesis, IAM, Step
  Functions, S3 (`getObject`, `putObject`, `deleteObject`, `headObject`,
  `listObjectsV2`) and Lambda (`invoke`) — encoded from the service's own
  model, with errors surfaced as `<Service>.<Code>`.

A Distributed Map (`"Mode": "DISTRIBUTED"`) runs each item as a child
execution under a Map Run: `ItemReader` over an S3 object in JSON, JSON Lines
or CSV or over `listObjectsV2`, `ItemBatcher`, `MaxConcurrency`,
`ToleratedFailureCount` and `ToleratedFailurePercentage`, `Label`, and a
`ResultWriter` that lands the manifest and result files in the local S3. The
children are ordinary executions — listable with `mapRunArn`, visible in the
console — and the run's counts are what DescribeMapRun reports.

CreateStateMachine refuses a resource outside this set by name rather than
letting a deploy succeed and the first execution fail.

A Task never surfaces a Go error. Every outcome is a failure name `Retry` and
`Catch` can match: a Lambda handler that throws `MyError` is caught as
`MyError`; a Lambda API error is `Lambda.<Code>`, so the CDK's default Retry on
`Lambda.TooManyRequestsException` works; an unwired peer is
`States.TaskFailed`; deadlines are `States.Timeout` and
`States.HeartbeatTimeout`; a failed child execution is `States.TaskFailed`
with the child's error and cause in the description.

Two behaviours are AWS's and worth knowing. A `Choice` whose `Variable`
selects nothing fails the execution with `States.Runtime` — `IsPresent` is the
one safe probe — while a variable that is present but the wrong type simply
does not match. And `ResultPath` merges the result into the *raw* state input,
not the post-`InputPath` view; `"InputPath": null` means `{}`, absent means
`$`.

Differences from AWS, listed rather than hidden:

- **Task dispatch is at-least-once across a restart.** A frame persisted as
  CALLING is re-dispatched when the store reopens, so a Lambda that was in
  flight when `doze-aws` stopped is invoked again. This matches AWS's own
  task semantics; a re-invoked handler during development is not a bug.
- **Wait resolution is one second.** Frames' wake times are compared against
  the clock every second, which is AWS's resolution for `Wait` too; there is
  no timer heap that could desync from the frames, because the frames are the
  schedule.
- **DeleteStateMachine is immediate**, not DELETING (above).
- **A redeemed or expired token answers `TaskDoesNotExist`** where AWS
  distinguishes `TaskTimedOut`; the store keeps no tombstones.
- **Express executions and TestState runs do not survive a restart.** They
  are held in memory for the call that runs them, which is also where AWS
  keeps them; their history goes to the caller, not to a log group.
- **A Distributed Map launches at most 40 children at a time** whatever
  `MaxConcurrency` says, and its item reader reads this stack's S3, not a
  cross-account bucket. The counts, statuses and result files are the same
  shape a program sees from AWS.
- **`.sync` on any service other than Step Functions is refused at create
  time**, because the job it would wait for runs nowhere locally. AWS's
  `.sync` for Batch, ECS, Glue and the rest has nothing to poll here.
- **Alias routing is weighted-random.** Two versions at 50/50 receive
  executions by coin flip, as on AWS, so over a short local run the split
  can look uneven.

## Verified against

Three SDKs and one deploy tool, in tests that run on every push:

- **aws-sdk-go-v2** (`sdk_test.go`, `sdk_errors_test.go`, `sdk_express_test.go`,
  `sdk_versions_test.go`, `sdk_activity_test.go`, `jsonata_sdk_test.go`):
  every operation, and every typed error the service can answer matched
  through the SDK's own exception types — a near-miss spelling decodes as a
  generic error no program can branch on, which is what those tests exist
  to catch. StartSyncExecution and TestState go through the SDK with the
  `sync-` host prefix its ruleset adds, which is what `sync-aws.doze` serves.
- **aws-sdk-go v1** (`sdkv1_test.go`): the older wire encoding round-trips.
- **@aws-sdk/client-sfn** (`e2e/tests/stepfunctions-sdk.spec.ts`): what the
  JavaScript types promise — timestamps decode as `Date`, payloads in
  history are strings, lists paginate — across the whole surface: versions
  and aliases, Express sync calls, activities polled from a worker, redrive,
  Distributed Map runs and a JSONata machine.
- **CDK** (`cloudformation/sfn_apply_test.go`, and a real `cdk deploy` of a
  LambdaInvoke → Choice → SqsSendMessage / SnsPublish → Wait → Parallel →
  Map chain, a `.waitForTaskToken` machine and an EXPRESS one): both
  spellings the CDK emits — `Fn::Join`ed ARNs inside `DefinitionString`,
  and `DefinitionSubstitutions` from `DefinitionBody.fromString` — resolve
  to the real function, queue and topic; a second unchanged deploy is
  "no changes"; destroy leaves no machines behind.

That pass found and fixed a join that read an earlier Parallel's settled
branches (a Map after a Parallel answered trailing nulls), list operations
ignoring `maxResults`, tag operations succeeding on an ARN nothing held, an
internal trace chain leaking as `traceHeader`, and — outside this service —
a CloudFormation mapping that dropped `S3Bucket` from a raw Lambda function's
`Code`, which is how the CDK ships every asset.

The engine sits in every other gate too: the intrinsic parser and the
definition analyser are fuzzed (`internal/asl/fuzz_test.go`, on the fuzz
workflow's matrix), the repo-level stress test (`stress_test.go`) runs
executions to SUCCEEDED under `-race` alongside every other service, and
the soak (`cmd/doze-aws/soak_test.go`) starts an execution per iteration and
asserts, at each checkpoint, that the one from five hundred operations ago
has finished.

## Input validation

The service model is the source of truth for what AWS refuses, and the
constraint table in `validate.go` is generated from it rather than
hand-written.

**229/229 model-derived constraints enforced across 33 of the 37
operations, with `knownGaps` empty.** The four not covered —
`SendTaskSuccess`, `SendTaskFailure`, `SendTaskHeartbeat` and
`RedriveExecution` — consume the state they address: redeeming a task token
spends it, and the first redrive puts the execution back to RUNNING, so their
nineteen cases have no baseline the harness could replay twice.

Generated with `dzaudit cases sfn`, committed to `testdata/cases_sfn.json`,
and replayed case by case in `rejection_parity_test.go` from a baseline the
test first proves the service accepts.

### The definition is validated twice, on purpose

`ValidateStateMachineDefinition` is faithful to ASL: it accepts what AWS
accepts, including every service integration, because refusing a definition
AWS would take breaks a working template. `CreateStateMachine` then applies a
second, stricter check — a task resource this build cannot call is refused
at create time with a message naming it. The alternative, a clean deploy
followed by a first execution that fails on a `Task` state, is the failure
this stack exists to prevent.

### Lowercase members are load-bearing

Every other service in this repo spells its JSON members upper-initial. Step
Functions does not, and the model-derived checks match keys exactly, so
`stateMachineArn` in the constraint table and in the IAM resource rules is
what makes a request decode to something other than an empty struct.
