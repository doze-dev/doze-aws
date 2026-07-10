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
