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
| PutRule | F | EventPattern rules; ScheduleExpression rules answer honestly (Phase 8) |
| DeleteRule / DescribeRule / ListRules | F | |
| EnableRule / DisableRule | F | |
| PutTargets / RemoveTargets / ListTargetsByRule / ListRuleNamesByTarget | F | SQS, SNS, and Lambda target ARNs |
| CreateEventBus / DeleteEventBus / DescribeEventBus / ListEventBuses | F | default bus implicit; custom buses; deleting a bus removes its rules |
| TestEventPattern | F | the same matcher, exposed for testing patterns |
| TagResource / UntagResource / ListTagsForResource | F | rule tags by ARN |
| Scheduled rules (rate/cron), Archives + Replay | S→F | Phase 8 |
| API destinations + Connections | S | call external HTTP endpoints via cloud infrastructure |
| Partner event sources, global endpoints, cross-account permissions, schemas registry | S | cloud infrastructure |
