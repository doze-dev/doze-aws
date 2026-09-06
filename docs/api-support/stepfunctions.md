# Step Functions — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

Standard workflows run locally against the services this stack already
serves. The Amazon States Language is parsed, statically checked and executed
by a pure interpreter (`internal/asl`) that knows nothing about AWS calls; the
service around it owns one driver goroutine that steps every execution,
writes every history event, and hands Task calls to transient workers that
never touch the store. An execution is a serialisable frame list, so a restart
resumes every suspended state — a `Wait`, a Lambda in flight, a branch parked
on a task token — from what its frame says rather than from a goroutine the
restart lost.

Step Functions is the one service here on AWS JSON 1.0 rather than 1.1, signs
as `states` while targeting `AWSStepFunctions`, and spells its members
lowercase-initial (`stateMachineArn`). Error codes are spelled exactly as SDKs
match them; `InvalidDefinition` carries the first problem and a count, so a
definition with four mistakes takes one round trip to understand.

| Operation | Tier | Notes |
|---|---|---|
| CreateStateMachine | F | STANDARD and EXPRESS accepted; idempotent on an identical definition, so an unchanged `cdk deploy` succeeds; JSONata definitions and task resources outside the local integration set are refused here, not on first execution |
| DescribeStateMachine / ListStateMachines | F | |
| UpdateStateMachine | F | running executions keep their frozen definition |
| DeleteStateMachine | F | synchronous — AWS parks the machine in DELETING until executions drain; locally it disappears at once, and the call is idempotent so a repeated `cdk destroy` does not fail |
| ValidateStateMachineDefinition | F | the analyser exposed directly; every diagnostic in document order, none returned early |
| CreateActivity / DescribeActivity / DeleteActivity / ListActivities | F | control plane only; polling arrives with GetActivityTask |
| TagResource / UntagResource / ListTagsForResource | F | tags are a `[{key,value}]` list, as on AWS, not the `{k:v}` map Lambda and DynamoDB use |
| StartExecution | F | Standard only; same name + still RUNNING + same input returns the original execution rather than conflicting; an EXPRESS machine answers UnsupportedOperationException until Express lands |
| DescribeExecution / ListExecutions | F | status filter, pagination |
| StopExecution | F | |
| DescribeStateMachineForExecution | F | answers from the execution's frozen snapshot — what it is running, not what the machine says today |
| GetExecutionHistory | F | global event ids with per-frame `previousEventId` chains; `reverseOrder`; pagination; payloads are JSON-encoded strings, as the SDK types expect |
| SendTaskSuccess / SendTaskFailure / SendTaskHeartbeat | F | tokens minted before `Parameters` are evaluated so `$$.Task.Token` resolves, and persisted before the send so an instant reply finds them |
| StartSyncExecution, TestState | S→F | Express; both need the `sync-` host prefix, which arrives with it |
| GetActivityTask | S→F | activity polling arrives after task tokens |
| RedriveExecution | S→F | arrives after go-live |
| Versions and aliases (8 operations) | S→F | arrive after go-live |
| DescribeMapRun / ListMapRuns / UpdateMapRun | S | Distributed Map is cloud-scale fan-out over an S3 item reader; not coming |

Every operation in the `com.amazonaws.sfn` model — 37 — is either handled,
staged with a reason, or refused with a reason. Nothing falls through to
`InvalidAction`, so a caller can always tell a staged gap from a typo.

## What runs, honestly

The interpreter speaks the JSONPath dialect with the full `States.*` intrinsic
set (own scanner and parser: nested calls, escaped quotes), all eight state
types, `Retry` and `Catch` with the spec's defaults and jitter, `Parallel`,
inline `Map` with `MaxConcurrency` and `ItemSelector`, and `Wait` on seconds,
timestamps and paths. Task resources are the go-live integration set —
Lambda by bare ARN or `arn:aws:states:::lambda:invoke`, `sqs:sendMessage`,
`sns:publish`, each with `.waitForTaskToken` — and CreateStateMachine refuses
anything else by name rather than letting a deploy succeed and the first
execution fail.

A Task never surfaces a Go error. Every outcome is a failure name `Retry` and
`Catch` can match: a Lambda handler that throws `MyError` is caught as
`MyError`; a Lambda API error is `Lambda.<Code>`, so the CDK's default Retry on
`Lambda.TooManyRequestsException` works; an unwired peer is
`States.TaskFailed`; deadlines are `States.Timeout` and
`States.HeartbeatTimeout`.

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
- **Deferred for now:** Express, `.sync` and `.sync:2`, activities, JSONata,
  versions and aliases, generic `aws-sdk:` integrations. Each answers
  `UnsupportedOperationException` naming what it is waiting on.

## Input validation

The service model is the source of truth for what AWS refuses, and the
constraint table in `validate.go` is generated from it rather than
hand-written.

**130/130 model-derived constraints enforced across 19 of the 22 dispatched
operations, with `knownGaps` empty.** The three not covered —
`SendTaskSuccess`, `SendTaskFailure`, `SendTaskHeartbeat` — need a live task
token, and redeeming one consumes it, so their thirteen cases have no baseline
the harness could replay.

Generated with `dzaudit cases sfn`, committed to `testdata/cases_sfn.json`,
and replayed case by case in `rejection_parity_test.go` from a baseline the
test first proves the service accepts.

### The definition is validated twice, on purpose

`ValidateStateMachineDefinition` is faithful to ASL: it accepts what AWS
accepts, including JSONata and every service integration, because refusing a
definition AWS would take breaks a working template. `CreateStateMachine`
then applies a second, stricter check — the JSONata dialect and task
resources this build cannot run are refused at create time with a message
naming the gap. The alternative, a clean deploy followed by a first execution
that fails on a `Task` state, is the failure this stack exists to prevent.

### Lowercase members are load-bearing

Every other service in this repo spells its JSON members upper-initial. Step
Functions does not, and the model-derived checks match keys exactly, so
`stateMachineArn` in the constraint table and in the IAM resource rules is
what makes a request decode to something other than an empty struct.
