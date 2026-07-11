# EventBridge — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

Content-based routing is fully functional: the pattern language
(internal/eventpattern) implements exact/prefix/suffix/equals-ignore-case/
wildcard/anything-but/numeric/exists/cidr/$or, nested fields, and event-array
any-element matching. PutEvents synchronously matches enabled rules and
delivers to targets.

| Operation | Tier | Notes |
|---|---|---|
| PutEvents | F | validates entries, matches enabled rules, delivers to SQS/SNS/Lambda targets with Input/InputPath/InputTransformer shaping |
| PutRule | F | EventPattern rules; `rate(...)` schedules driven by a local ticker; `cron(...)` accepted and stored but not driven (wall-clock cron isn't useful in an ephemeral stack) |
| DeleteRule / DescribeRule / ListRules | F | |
| EnableRule / DisableRule | F | |
| PutTargets / RemoveTargets / ListTargetsByRule / ListRuleNamesByTarget | F | SQS, SNS, and Lambda target ARNs |
| CreateEventBus / DeleteEventBus / DescribeEventBus / ListEventBuses | F | default bus implicit; custom buses; deleting a bus removes its rules |
| TestEventPattern | F | the same matcher, exposed for testing patterns |
| TagResource / UntagResource / ListTagsForResource | F | rule tags by ARN |
| CreateArchive / DescribeArchive / ListArchives / UpdateArchive / DeleteArchive | F | PutEvents appends matching events to the archive's log; retention stored but not actively expired |
| StartReplay / DescribeReplay / ListReplays | F | replays the windowed archive events back through the destination bus's rules (optionally filtered by rule ARN); runs synchronously → `COMPLETED` |
| CancelReplay | S | local replays complete synchronously, so there is never a running replay to cancel |
| API destinations + Connections | S | call external HTTP endpoints via cloud infrastructure |
| Partner event sources, global endpoints, cross-account permissions, schemas registry | S | cloud infrastructure |
