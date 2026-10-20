# SQS claims

The worked example. The "recording queue" of `plan/services/sqs.md` seeds it;
the scenarios already in `test_sqs.py` carry the rest.

`model` below is the service model's own documentation text (`.audit-models/sqs.json`:
member and operation docs, and the error shapes with their `awsQueryError` codes),
which turned out to be a better source than the developer-guide pages these rules
were first written from: see `plan/services/sqs.md` SQS-11 to SQS-24.

| Claim | Source | Scenario |
|---|---|---|
| A queue is created with the documented defaults (30 s visibility, 4 day retention, 1 MiB, no delay) | model | test_sqs::test_standard_queue_lifecycle |
| CreateQueue again with the same attributes answers the existing queue; different attributes are refused `QueueNameExists` | model | test_sqs::test_refusals |
| A message sent is received once, and is gone once its receipt handle is deleted | doc | test_sqs::test_standard_queue_lifecycle |
| Message attributes round-trip with their data types and the MD5 of them | doc | test_sqs::test_message_attributes_and_batches |
| A batch is refused whole for its shape (empty, over ten, a bad or repeated id) before any entry is tried | model | test_sqs::test_batch_refusals |
| One entry that fails in a batch fails alone; the others are sent | model | test_sqs::test_batch_refusals |
| `AttributeNames` accepts the message-system attribute names it was deprecated for | model | test_sqs::test_system_attributes_by_either_spelling |
| A FIFO queue delivers a group's messages in the order sent and drops a duplicate inside the dedup window | model | test_sqs::test_fifo_orders_and_deduplicates |
| A FIFO send without a group id, or without a dedup id when content-based dedup is off, is refused | model | test_sqs::test_refusals |
| A message received more than maxReceiveCount times moves to the dead-letter queue | model | test_sqs::test_dead_letter_queue_takes_what_was_received_too_often |
| A message in flight is hidden until its visibility timeout, and ChangeMessageVisibility releases it | model | test_sqs::test_tags_visibility_and_purge |
| Tags set at creation and by TagQueue are listed; UntagQueue removes them | model | test_sqs::test_tags_visibility_and_purge |
| Every attribute's range (delay 0–900, visibility 0–43,200, retention 60–1,209,600, size 1,024–1,048,576, wait 0–20, KMS reuse 60–86,400) and a non-number are refused `InvalidAttributeValue` | model | test_sqs::test_attribute_ranges |
| `ReceiveMessage` with `MaxNumberOfMessages` 0 or 11 is refused (the code it is refused with is `ReadCountOutOfRange`) | memory | test_sqs::test_receive_out_of_range |
| Re-creating a queue within 60 s of deleting it is refused `QueueDeletedRecently` (doze-aws deletes at once unless `--sqs-strict` is on, so recorded against the default it is a listed difference, entered in `deviations.py`) | model | test_sqs::test_recreate_after_delete |
| A second `PurgeQueue` within 60 s is refused `PurgeQueueInProgress` (doze-aws purges at once unless `--sqs-strict` is on; listed difference) | model | test_sqs::test_purge_twice |
| A redriven message's `ApproximateReceiveCount` after it moves to the dead-letter queue, and its `DeadLetterQueueSourceArn` | memory | test_sqs::test_redrive_receive_count |
| Changing the queue's visibility timeout leaves messages already in flight on the timeout they were received with | model | test_sqs::test_visibility_change_in_flight |
| `ChangeMessageVisibility` past the twelve hours from the receive ("greater than the maximum time left") is refused | model | test_sqs::test_twelve_hours_from_the_receive |
| `ChangeMessageVisibility` on a message that is not in flight is refused `MessageNotInflight` | model | test_sqs::test_visibility_of_a_message_not_in_flight |
| `StartMessageMoveTask` with no destination returns each message to the queue it came from; a source that is no queue's DLQ is refused; listing a finished task has no `TaskHandle` | model | test_sqs::test_move_task_default_destination |
| The move-task operations answer `ResourceNotFoundException` for a queue that is not there | model | test_sqs::test_move_task_missing_queue |
| `ListMessageMoveTasks` `MaxResults` is at most 10 | model | test_sqs::test_move_task_default_destination |
| `SetQueueAttributes` with a `Policy` that is not a policy document, or has no Effect, Principal or Resource, is refused `InvalidAttributeValue` | doc | test_sqs::test_invalid_policy |
| `SetQueueAttributes` with a `Policy` whose `Statement` is an empty array: accepted or refused (doze-aws refuses it) | memory | test_sqs::test_invalid_policy |
| `TagQueue` refuses a key over 128 characters, a value over 256 and the `aws:` prefix | doc | test_sqs::test_tag_limits |
| `TagQueue` with more than 50 tags: the model says "isn't recommended" (doze-aws accepts it) | model | test_sqs::test_tag_limits |
| A message and a batch are at most 1 MiB, counting attributes; a bad character is `InvalidMessageContents`; 11 attributes, an `AWS.` prefix and an unknown data type are refused | model | test_sqs::test_message_limits |
| A batch of messages totalling more than 1 MiB is refused `BatchRequestTooLong` | model | test_sqs::test_batch_total_size |
| `MessageGroupId` is accepted on a standard queue (fair queues) and comes back as a system attribute; `MessageDeduplicationId` there is refused (doze-aws refuses it) | model | test_sqs::test_message_group_on_a_standard_queue |
| A batch entry's `AWSTraceHeader` is kept and comes back on receive, with `MD5OfMessageSystemAttributes` | model | test_sqs::test_trace_header_on_a_batch |
| `MessageAttributeNames` takes `prefix.*` | model | test_sqs::test_message_attribute_prefix_selection |
| `ListQueues` takes `MaxResults` 1–1,000 and returns a `NextToken` when there is more | model | test_sqs::test_list_queues_paging |
| `AddPermission`: a label of at most 80 letters, digits, hyphens and underscores, in use refused; any action name or `*`; at most seven actions; a non-account principal refused; `RemovePermission` on a missing label refused | model | test_sqs::test_add_permission_rules |
| `ReceiveRequestAttemptId` returns the same messages and handles on a retry within five minutes; on a standard queue it is refused (doze-aws refuses it) | model | test_sqs::test_receive_request_attempt_id |
| `DeduplicationScope` and `FifoThroughputLimit`: values, FIFO only (doze-aws refuses them on a standard queue), and `perMessageGroupId` needs `messageGroup`; a boolean attribute other than true/false is refused | model | test_sqs::test_fifo_attribute_rules |
| `RedriveAllowPolicy` `denyAll` refuses a source naming the queue; a bad `redrivePermission` and `byQueue` without `sourceQueueArns` are refused | model | test_sqs::test_redrive_allow_policy |
| A queue URL whose account is another's, and a `QueueOwnerAWSAccountId` that is not this account, name no queue here | model | test_sqs::test_queue_in_another_account |
| Setting an attribute back to its default reads as the default | doc | test_sqs::test_setting_an_attribute_back_to_its_default |
| An old receipt handle after a re-receive: "the request will succeed, but the message might not be deleted" (doze-aws: the same, a delete with an earlier receive's handle succeeds and leaves the message) | model | test_sqs::test_an_old_receipt_handle |
| `RedrivePolicy` without `maxReceiveCount`: the model documents "Default: 10" (doze-aws refuses it) | model | test_sqs::test_redrive_policy_without_max_receive_count |
| The number of permissions at which `AddPermission` is `OverLimit` (the model names the error, not the number) | model | test_sqs::test_add_permission_statement_limit |
| `GetQueueAttributes` with no `AttributeNames` returns an empty result | model | test_sqs::test_get_queue_attributes_without_names |
| A move task is asynchronous: started, it is `RUNNING` with a handle; one runs per queue at a time; `CancelMessageMoveTask` on a running one answers the count moved and the status becomes `CANCELLED`, and cancelling again is refused | model | test_sqs::test_move_task_cancel |
| `SetQueueAttributes` cannot change `FifoQueue`: "You can't change it for an existing queue" (the code doze-aws refuses with, `InvalidAttributeName`, is memory) | model | test_sqs::test_queue_type_cannot_change |
| An ARN naming another account or service is not a queue here: a move task answers `ResourceNotFoundException`, a `RedrivePolicy` is refused | model | test_sqs::test_an_arn_elsewhere |
| Content-based deduplication generates the SHA-256 of the body as the `MessageDeduplicationId` | model | test_sqs::test_content_based_deduplication_id |
| `SequenceNumber` "applies only to FIFO" queues: a standard queue's send with a group id has none | model | test_sqs::test_sequence_number_is_fifo_only |
| A boolean attribute's "valid values are true and false": `TRUE`, `True`, `1` (doze-aws refuses them) | model | test_sqs::test_boolean_attribute_spelling |
| "The only supported message system attribute is AWSTraceHeader. Its type must be String" (doze-aws refuses another name or type; whether AWS checks the header's format is memory) | model | test_sqs::test_message_system_attributes |
| "You must set MaxResults to receive a value for NextToken" | model | test_sqs::test_list_queues_token_needs_max_results |
| `ReceiptHandleIsInvalid` and `ResourceNotFoundException` are 404 on the JSON protocol too (the shapes' `httpError`) | model | test_sqs::test_error_statuses |
| A read-only attribute (`QueueArn`, `CreatedTimestamp`) in `SetQueueAttributes`: refused or ignored (doze-aws ignores it; the model is silent) | memory | test_sqs::test_read_only_attributes |
| `KmsMasterKeyId` together with `SqsManagedSseEnabled=true`: "only one server-side encryption option is supported per queue" (doze-aws stores both; what AWS answers is not established) | model | test_sqs::test_kms_key_with_sqs_managed_encryption |
| `ChangeMessageVisibility` with a receipt handle from an earlier receive of the same message: accepted and re-hides it (doze-aws), or refused (the model documents the old handle for `DeleteMessage` only; SQS-46) | memory | test_sqs::test_an_old_receipt_handle |
| An `AWSTraceHeader` that is not "a correctly formatted AWS X-Ray trace header string": refused or kept (doze-aws keeps it; the format is not in the model; SQS-46) | memory | test_sqs::test_message_system_attributes |
| `maxReceiveCount` below 1 or above 1,000 in a `RedrivePolicy` (doze-aws refuses 0 and 1,001; the model states neither bound; SQS-46) | memory | test_sqs::test_redrive_max_receive_count_range |
| A standard queue named `*.fifo`, and a FIFO queue whose name does not end in `.fifo` (doze-aws refuses both; the model states neither; SQS-46) | memory | test_sqs::test_fifo_suffix_on_a_standard_queue |
| A Query-protocol `SendMessageBatch` whose entries are numbered 1 and 3: sent whole (doze-aws), the gap ignored, or refused (SQS-53) | memory | test_sqs::test_query_batch_with_a_gap |
| A `NextToken` from one `ListQueues` listing given to another listing (doze-aws does not detect it; the model is silent) | memory | test_sqs::test_list_queues_paging |
| `ContentBasedDeduplication` set on a standard queue: refused or stored (doze-aws refuses it) | memory | test_sqs::test_fifo_attribute_rules |
| A trigger whose queue's visibility timeout is shorter than the function's timeout is refused on create and on update; equal is accepted (doze-aws since LAM-5; the code is the model's `InvalidParameterValueException`, the message is memory) | doc | test_lambda::test_a_trigger_whose_queue_hides_for_less_than_the_function_runs |
| The event source mapping of a deleted function stops reading its queue, rather than receiving messages it can give to no one (doze-aws: it keeps receiving them; the model says only that `DeleteEventSourceMapping` is how a mapping is deleted) | memory | test_lambda::test_a_deleted_functions_mapping |
| A FIFO queue is refused as an S3 event notification destination (doze-aws since the S3 refusal in `refuseFIFODestinations`; the code and text are memory) | doc | test_s3::test_notification_fifo_queue |
| A FIFO queue cannot be subscribed to a standard topic (SNS-12; doze-aws accepts the subscription and drops every delivery) | memory | test_sns::test_a_fifo_queue_on_a_standard_topic |
