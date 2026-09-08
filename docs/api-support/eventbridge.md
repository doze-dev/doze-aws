# EventBridge — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

Content-based routing is fully functional: the pattern language
(internal/eventpattern) implements exact/prefix/suffix/equals-ignore-case/
wildcard/anything-but/numeric/exists/cidr/$or, nested fields, and event-array
any-element matching. PutEvents synchronously matches enabled rules and
delivers to targets.

| Operation | Tier | Notes |
|---|---|---|
| PutEvents | F | validates entries, matches enabled rules, delivers to SQS, SNS, Lambda and CloudWatch Logs targets with Input/InputPath/InputTransformer shaping |
| PutRule | F | EventPattern rules; `rate(...)` and `cron(...)` schedules both driven by a local ticker (the six-field AWS cron: `?`, `L`, `W`, `#`, month and day names, UTC); a malformed expression is refused with AWS's message; a schedule is armed when first seen and never replays what a restart missed |
| DeleteRule / DescribeRule / ListRules | F | |
| EnableRule / DisableRule | F | |
| PutTargets / RemoveTargets / ListTargetsByRule / ListRuleNamesByTarget | F | SQS, SNS, Lambda and CloudWatch Logs log-group target ARNs; a log-group target writes the shaped event as one log line, in a stream named for the rule, as AWS does |
| CreateEventBus / DeleteEventBus / DescribeEventBus / ListEventBuses | F | default bus implicit; custom buses; deleting a bus removes its rules |
| TestEventPattern | F | the same matcher, exposed for testing patterns |
| TagResource / UntagResource / ListTagsForResource | F | rule tags by ARN |
| CreateArchive / DescribeArchive / ListArchives / UpdateArchive / DeleteArchive | F | PutEvents appends matching events to the archive's log; retention stored but not actively expired |
| StartReplay / DescribeReplay / ListReplays | F | replays the windowed archive events back through the destination bus's rules (optionally filtered by rule ARN); runs synchronously → `COMPLETED` |
| CancelReplay | S | local replays complete synchronously, so there is never a running replay to cancel |
| API destinations + Connections | S | call external HTTP endpoints via cloud infrastructure |
| Partner event sources, global endpoints, cross-account permissions, schemas registry | S | cloud infrastructure |

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what EventBridge refuses**.

**284/284 model-derived constraints enforced across all 28 dispatched
operations, with `knownGaps` empty.** Removing the constraint table makes 191 of
those 284 cases slip through, so it is doing work the hand-written checks were
not.

Generated with `dzaudit cases eventbridge`, committed to
`testdata/cases_eventbridge.json`, and replayed case by case in
`rejection_parity_test.go` from a baseline the test first proves the service
accepts. The remaining model cases fall on operations with no handler — API
destinations, connections, partner sources, the schema registry — which cannot
be audited at all.

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
