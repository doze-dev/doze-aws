# EventBridge — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

Content-based routing is fully functional: the pattern language
(internal/eventpattern) implements exact/prefix/suffix/equals-ignore-case/
wildcard/anything-but/numeric/exists/cidr/$or, nested fields, and event-array
any-element matching. PutEvents synchronously matches enabled rules and
delivers to targets.

| Operation | Tier | Notes |
|---|---|---|
| PutEvents | F | validates entries, matches enabled rules, delivers to SQS, SNS, Lambda, CloudWatch Logs and API destination targets with Input/InputPath/InputTransformer shaping |
| PutRule | F | EventPattern rules; `rate(...)` and `cron(...)` schedules both driven by a local ticker (the six-field AWS cron: `?`, `L`, `W`, `#`, month and day names, UTC); a malformed expression is refused with AWS's message; a schedule is armed when first seen and never replays what a restart missed |
| DeleteRule / DescribeRule / ListRules | F | |
| EnableRule / DisableRule | F | |
| PutTargets / RemoveTargets / ListTargetsByRule / ListRuleNamesByTarget | F | SQS, SNS, Lambda, CloudWatch Logs log-group and API destination target ARNs; a log-group target writes the shaped event as one log line, in a stream named for the rule, as AWS does; an API destination target carries `HttpParameters` |
| CreateEventBus / UpdateEventBus / DeleteEventBus / DescribeEventBus / ListEventBuses | F | default bus implicit; custom buses; deleting a bus removes its rules. `Description`, `KmsKeyIdentifier` and `DeadLetterConfig` are stored and reported back but inert locally — Terraform tracks them on `aws_cloudwatch_event_bus`, so dropping them would be permanent drift |
| TestEventPattern | F | the same matcher, exposed for testing patterns |
| TagResource / UntagResource / ListTagsForResource | F | rule tags by ARN |
| CreateArchive / DescribeArchive / ListArchives / UpdateArchive / DeleteArchive | F | PutEvents appends matching events to the archive's log; retention stored but not actively expired |
| StartReplay / DescribeReplay / ListReplays | F | replays the windowed archive events back through the destination bus's rules (optionally filtered by rule ARN); runs synchronously → `COMPLETED` |
| CancelReplay | S | local replays complete synchronously, so there is never a running replay to cancel |
| CreateConnection / UpdateConnection / DeauthorizeConnection / DeleteConnection / DescribeConnection / ListConnections | F | BASIC, API_KEY and OAUTH_CLIENT_CREDENTIALS with invocation header/query/body parameters; secrets stay in the connection record and are never reported (see below); VPC Lattice connectivity parameters are refused by name |
| CreateApiDestination / UpdateApiDestination / DeleteApiDestination / DescribeApiDestination / ListApiDestinations | F | `http://` endpoints accepted so a local server can be a destination; `InvocationRateLimitPerSecond` stored and reported, not enforced; deleting a connection leaves its destinations `INACTIVE` |
| Partner event sources, global endpoints, cross-account permissions, schemas registry | S | cloud infrastructure |

## API destinations

A rule target whose ARN is an API destination makes a real HTTP request: the
destination's endpoint with its `*` segments filled from the target's
`HttpParameters.PathParameterValues`, the connection's invocation parameters
merged with the target's headers and query string, the shaped event as the
body (body parameters merge into it when it is a JSON object), and the
connection's credential applied — Basic, the API key header, or a bearer token
from a real client-credentials request to the connection's authorization
endpoint, cached until it expires.

Delivery runs off the request path, with a 5 second timeout and one retry a
second later on a transport error, a 429 or a 5xx; a failure is logged with the
rule, the destination and the status. AWS retries for 24 hours with backoff and
can park the event in a dead-letter queue; one retry is the honest local
budget, and there is no DLQ.

AWS stores a connection's secret in Secrets Manager under
`events!connection/<name>/<id>` and reports that `SecretArn`. doze-aws keeps
the secret in the connection record, reports the ARN AWS would mint so a
template's `GetAtt` resolves, and never returns a password, key value, client
secret or secret parameter from `DescribeConnection` — the same shape as AWS,
without the secret existing in the local Secrets Manager.

## Differences from AWS

- **Delivery is synchronous and in-process.** `PutEvents` matches every rule
  on the bus and delivers to its targets before returning, so there is no
  propagation delay and no at-least-once duplicate to defend against. A target
  that fails fails inside your `PutEvents` call.
- **Schedules tick at one second.** Cron and rate expressions are evaluated by
  a single driver against the clock, which is AWS's own resolution for
  minute-granularity rules and finer than it for nothing.
- **No partner sources, no global endpoints, no schema registry.** Each needs
  an account relationship, a second region, or a discovery service — none of
  which has a local shape.
- **Archive and replay are local.** Events are archived to the data directory
  and replayed from it, so the retention you set is bounded by the disk you
  have rather than by a service quota.

## Verified against

- **aws-sdk-go-v2** (`sdk_test.go`, `coverage_test.go`): rule and bus
  administration, delivery to SQS, archive and replay, and input transformers.
- **aws-sdk-go v1** (`sdkv1_test.go`): the rule lifecycle through the older
  client.
- **API destinations** (`apidest_sdk_test.go`, `apidest_revoke_test.go`): real
  HTTP delivery with Basic, API-key and OAuth connections, the retry path, and
  — the one worth having — that deauthorizing a connection or deactivating a
  destination actually stops delivery rather than continuing with stale
  credentials.
- **Scheduling** (`scheduler_test.go`, `scheduled_delivery_test.go`): rate and
  cron parsing, which schedules are due, and a scheduled rule delivering the
  event it was supposed to deliver to the target it was pointed at.
- **Targets** (`logs_target_sdk_test.go`): a rule writing to a CloudWatch
  Logs group.
- **Refusals are named** (`bus_refusal_sdk_test.go`): an unsupported operation
  answers by name rather than falling through to a generic error.
- **Model-derived rejection parity** (`rejection_parity_test.go`) and the
  dispatch table against `testdata/ops_eventbridge.json`.

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what EventBridge refuses**.

**448/448 model-derived constraints enforced across all 40 dispatched
operations, with `knownGaps` empty.** Removing the constraint table let the
majority of them through when that was last measured — at 438 cases, 284 of them
— so it is doing work the hand-written checks were not. That figure is from a
one-off experiment against an older fixture and has not been re-run; it is kept
because it is the only number here that says the audit *found* something rather
than *covered* something, and marked as dated rather than quietly restated
against a total it was not measured from.

Generated with `dzaudit cases eventbridge`, committed to
`testdata/cases_eventbridge.json`, and replayed case by case in
`rejection_parity_test.go` from a baseline the test first proves the service
accepts. The remaining model cases fall on operations with no handler — partner
sources, global endpoints, cross-account permissions — which cannot be audited
at all.

### Targets carry fifteen nested parameter blocks

`PutTargets` is the widest input in the service: a target may carry
`EcsParameters`, `BatchParameters`, `RunCommandParameters`, `HttpParameters`,
`RedshiftDataParameters`, `SageMakerPipelineParameters`, `InputTransformer` and
more, each with its own required members and constraints. A case at
`Targets[].EcsParameters.Group` needs the whole chain above it to be *valid*, or
the request is refused for the missing chain rather than for the mutation. So
the harness carries an exemplar per container and probes each one against the
baseline before any case runs — `Targets[].RunCommandParameters` was caught this
way, missing its required `RunCommandTargets`.

### CancelReplay has no acceptable baseline

doze-aws replays an archive synchronously inside `StartReplay`, so a replay is
`COMPLETED` the instant it exists and none is ever cancellable; every
`CancelReplay` request is refused, valid ones included. Rather than skip its
four cases, the harness requires the baseline to fail with exactly
`IllegalStatusException` — which proves it cleared validation — and then
requires every mutation to fail with `ValidationException` specifically. That
is stricter than the usual path, where any 4xx after a single mutation is
attributed to the mutation.
