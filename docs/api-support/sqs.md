# SQS — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

Both wire protocols are served: AWS JSON 1.0 (modern SDKs) and the legacy
Query/XML protocol (aws-sdk-go v1 era). MD5OfMessageBody and
MD5OfMessageAttributes match AWS's algorithms — SDK client-side checksum
validation passes.

| Operation | Tier | Notes |
|---|---|---|
| CreateQueue | F | standard + FIFO (.fifo naming rule enforced), attributes, tags; idempotent re-create merges attributes |
| DeleteQueue | F | drops messages and dedup state |
| ListQueues | F | prefix filter; no pagination (local queue counts don't need it) |
| GetQueueUrl | F | |
| GetQueueAttributes | F | incl. ApproximateNumberOfMessages(NotVisible), QueueArn, RedrivePolicy |
| SetQueueAttributes | F | visibility, delay, retention, max size, receive wait, redrive policy, FIFO dedup |
| TagQueue / UntagQueue / ListQueueTags | F | |
| SendMessage / SendMessageBatch | F | delay, message attributes (String/Number/Binary), FIFO group + dedup id, content-based dedup |
| ReceiveMessage | F | long polling (notifier-driven, no spin), visibility timeout + per-receive override, FIFO group locking, system + message attribute selection |
| DeleteMessage / DeleteMessageBatch | F | |
| ChangeMessageVisibility | F | |
| ChangeMessageVisibilityBatch | F | via per-entry ChangeMessageVisibility semantics |
| PurgeQueue | F | |
| ListDeadLetterSourceQueues | F | |
| StartMessageMoveTask | F | completes synchronously (local volumes); DestinationArn required — doze-aws does not track per-message origin queues |
| ListMessageMoveTasks | F | returns the recorded (terminal) tasks |
| CancelMessageMoveTask | F | always "task is not active" — local moves complete synchronously, matching AWS's answer for a finished task |
| AddPermission / RemovePermission | C | no IAM locally: succeeds, changes nothing |
| DozePeek | — | doze extension: read-only full-queue inspection (no visibility/receive-count side effects) |

Dead-letter redrive (maxReceiveCount → DLQ move) and retention expiry run on
the receive path plus a background janitor, so write-only queues are reclaimed
too.

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what SQS refuses**. The two are different
promises, and the second is the one that decides whether code passing here also
passes on deploy.

| Input | Status |
|---|---|
| `RedrivePolicy` — target exists | ✅ refused if the queue does not exist |
| `RedrivePolicy` — FIFO↔FIFO, standard↔standard | ✅ refused if the types differ |
| `RedrivePolicy` — `maxReceiveCount` 1–1000 | ✅ range enforced, quoted or bare |
| `RedrivePolicy` — self-reference | ✅ a queue cannot be its own DLQ |
| `VisibilityTimeout` 0–43200 | ✅ |
| `DelaySeconds` 0–900 | ✅ |
| `MessageRetentionPeriod` 60–1209600 | ✅ |
| `MaximumMessageSize` 1024–262144 | ✅ |
| `ReceiveMessageWaitTimeSeconds` 0–20 | ✅ |
| Non-numeric attribute values | ✅ refused (previously silently ignored) |
| Queue name — charset | ✅ alphanumeric, `-`, `_` only (a period is legal only as the `.fifo` suffix) |
| Queue name — length ≤80 | ✅ the `.fifo` suffix counts toward the limit |
| Everything else | not yet audited |

Enforced cases are covered by `sqs/rejection_parity_test.go`, which asserts the
error **code** an SDK sees, not just that something failed.

Note SQS's own AWS service model carries no `@range`, `@length` or `@pattern`
traits at all — its constraints live only in prose — so this table is hand-derived
rather than generated. That is also why the bugs surfaced here first: nothing had
ever cross-checked them.
