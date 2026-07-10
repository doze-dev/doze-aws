# SNS — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

Served over the SNS Query/XML protocol. Delivery is synchronous and fans out
to SQS queues (raw or enveloped, via the peers directory) and to http(s)
webhooks with the SubscriptionConfirmation handshake.

| Operation | Tier | Notes |
|---|---|---|
| CreateTopic | F | idempotent; attributes + tags merge on re-create |
| DeleteTopic | F | drops the topic's subscriptions too |
| ListTopics | F | |
| GetTopicAttributes | F | live subscription counts + stored attribute round-trips |
| SetTopicAttributes | C→F | attributes stored and returned (DisplayName, Policy, ...); no local behavior change |
| TagResource / UntagResource / ListTagsForResource | F | |
| Subscribe | F | sqs (auto-confirmed), http/https (confirmation handshake); RawMessageDelivery, FilterPolicy; other protocols stored but undeliverable locally (logged) |
| ConfirmSubscription | F | by token, incl. the SubscribeURL flow |
| Unsubscribe | F | |
| ListSubscriptions / ListSubscriptionsByTopic | F | |
| GetSubscriptionAttributes / SetSubscriptionAttributes | F | RawMessageDelivery + FilterPolicy live; others round-trip |
| Publish | F | filter-policy evaluation, raw + enveloped delivery, message attributes |
| PublishBatch | F | per-entry subjects and message attributes |
| AddPermission / RemovePermission | C | no IAM locally: succeeds, changes nothing |
| PutDataProtectionPolicy / GetDataProtectionPolicy | C | stored and returned; not evaluated |
| Mobile push (Platform applications/endpoints), SMS + sandbox, phone-number opt-out ops | S | carrier/platform infrastructure cannot exist locally; each answers a clean coded error |

Filter policies support exact match, `prefix`, `anything-but` (list), and
`exists` — the most-used subset. The full pattern-operator set (numeric
ranges, wildcards, `$or`, FilterPolicyScope=MessageBody) arrives with the
EventBridge pattern engine in Phase 6.
