# SQS claims

The worked example. The "recording queue" of `plan/services/sqs.md` seeds it;
the scenarios already in `test_sqs.py` carry the rest.

| Claim | Source | Scenario |
|---|---|---|
| A queue is created with the documented defaults (30 s visibility, 4 day retention, 256 KiB, no delay) | doc | test_sqs::test_standard_queue_lifecycle |
| CreateQueue again with the same attributes answers the existing queue; different attributes are refused `QueueNameExists` | doc | test_sqs::test_refusals |
| A message sent is received once, and is gone once its receipt handle is deleted | doc | test_sqs::test_standard_queue_lifecycle |
| Message attributes round-trip with their data types and the MD5 of them | doc | test_sqs::test_message_attributes_and_batches |
| A batch is refused whole for its shape (empty, over ten, a bad or repeated id) before any entry is tried | doc | test_sqs::test_batch_refusals |
| One entry that fails in a batch fails alone; the others are sent | doc | test_sqs::test_batch_refusals |
| `AttributeNames` accepts the message-system attribute names it was deprecated for | doc | test_sqs::test_system_attributes_by_either_spelling |
| A FIFO queue delivers a group's messages in the order sent and drops a duplicate inside the dedup window | doc | test_sqs::test_fifo_orders_and_deduplicates |
| A FIFO send without a group id, or without a dedup id when content-based dedup is off, is refused | doc | test_sqs::test_refusals |
| A message received more than maxReceiveCount times moves to the dead-letter queue | doc | test_sqs::test_dead_letter_queue_takes_what_was_received_too_often |
| A message in flight is hidden until its visibility timeout, and ChangeMessageVisibility releases it | doc | test_sqs::test_tags_visibility_and_purge |
| Tags set at creation and by TagQueue are listed; UntagQueue removes them | doc | test_sqs::test_tags_visibility_and_purge |
| Every attribute's range, and the code each out-of-range value is refused with | doc | (to write) |
| `ReceiveMessage` with `MaxNumberOfMessages` 0 or 11 is refused `ReadCountOutOfRange` | memory | (to write) |
| Re-creating a queue within 60 s of deleting it is refused `QueueDeletedRecently` | doc | (to write) |
| A second `PurgeQueue` within 60 s is refused `PurgeQueueInProgress` | doc | (to write) |
| A redriven message's `ApproximateReceiveCount` after it moves to the dead-letter queue | memory | (to write) |
| Changing the visibility timeout leaves messages already in flight on the timeout they were received with | memory | (to write) |
| `StartMessageMoveTask` with no destination returns each message to the queue it came from | doc | (to write) |
| `SetQueueAttributes` with a `Policy` that is not a policy document is refused | memory | (to write) |
| `TagQueue` refuses a key over 128 characters, a value over 256, the `aws:` prefix and a 51st tag | doc | (to write) |
