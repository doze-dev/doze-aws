# SQS

Stage 1, the pilot. Size S. Follows [../01-protocol.md](../01-protocol.md); the
common acceptance criteria there apply in full, and this page adds SQS's own.

## 1. Summary

Standard and FIFO queues over one bbolt store, on both wire protocols (Query
and JSON). Peers: Lambda polls it (event source mappings), SNS and S3 and
EventBridge deliver into it, CloudFormation and `provision` create it. It is
first because its console design is finished, it is small, and it has two real
bugs that touch a deploy.

**Estimate:** S, two focused days, with the two items moved in from Lambda and
CloudFormation. **Actual:** not timed, and the number would not be honest: the
work was several times the estimate in volume. The plan listed 8 findings; 29
were closed, and 19 of them were found by reading the service model's own
documentation after the first pass had written its limits from memory (SQS-11 to
SQS-28: 1 MiB, fair queues, the whole Query wire, error codes, the move-task
entries). **Re-estimate for the other fifteen services:** size each as one step
larger than its document says until its model documentation has been read, and
make that reading the first act of the stage (see the memory note and
protocol step 1). The 460-finding total in this plan is therefore a floor.

## 2. Contract

The model lists 23 operations. Today the ledger groups them into 18 functional
rows and one extension.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateQueue, DeleteQueue, GetQueueUrl | F | F | |
| ListQueues | F | F | `MaxResults` 1 to 1,000 and `NextToken`. Closed: SQS-6. |
| GetQueueAttributes, SetQueueAttributes | F | F | Per-attribute tier below. |
| TagQueue, UntagQueue, ListQueueTags | F | F | |
| SendMessage, SendMessageBatch | F | F | |
| ReceiveMessage | F | F | 1 to 10 (SQS-5); `ReceiveRequestAttemptId` (SQS-22); `prefix.*` name selection (SQS-21). |
| DeleteMessage, DeleteMessageBatch | F | F | |
| ChangeMessageVisibility, ChangeMessageVisibilityBatch | F | F | |
| PurgeQueue | F | F | |
| ListDeadLetterSourceQueues | F | F | |
| StartMessageMoveTask, ListMessageMoveTasks, CancelMessageMoveTask | F | F | `DestinationArn` optional (SQS-7); a move completes at once, so nothing is ever `RUNNING` and a cancel always finds a finished task. |
| AddPermission, RemovePermission | F | F | The rules of SQS-18. |
| DozePeek | extension | extension | Read without consuming. Not an AWS operation. |

Queue attributes need their own tiers, because "SetQueueAttributes is F" hides
which settings do anything:

| Attribute | Target | To confirm |
|---|---|---|
| VisibilityTimeout, DelaySeconds, MessageRetentionPeriod, MaximumMessageSize, ReceiveMessageWaitTimeSeconds | F | Ranges enforced; `MaximumMessageSize` is 1,024 to 1,048,576 (SQS-11). |
| RedrivePolicy | F | Target exists, same type, 1 to 1000, not itself. |
| RedriveAllowPolicy | F | Validated, and checked when a queue names this one as its dead-letter queue (`allowAll`, `denyAll`, `byQueue` with at most 10 ARNs). |
| FifoQueue, ContentBasedDeduplication | F | |
| DeduplicationScope | F | `messageGroup` scopes a duplicate to its group. FIFO only. |
| FifoThroughputLimit | C | Validated, FIFO only, `perMessageGroupId` needs `messageGroup`; nothing is throttled here. |
| KmsMasterKeyId, KmsDataKeyReusePeriodSeconds, SqsManagedSseEnabled | C | Stored, not enforced; the reuse period is held to 60 to 86,400 and the boolean to true/false. The console must say so. |
| Policy | F | Validated when stored (SQS-8); evaluated under soft and enforce. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `sqs/`: 45 tests across store, server, SDK v1 and v2, batch, attribute round trip, hardening, notify leak |
| Parity | `model_parity_test.go`, `rejection_parity_test.go` (7), `coverage_model_test.go`; ledger says 48 of 48 model constraints enforced, `knownGaps` empty |
| Conformance | `conformance/tests/test_sqs.py`: 8 scenarios, all **unverified** (no recordings exist) |
| Browser | `e2e/tests/sqs.spec.ts`: 17 tests |
| Mutation | No per-package baseline yet (foundation B3) |

SQS's AWS model carries no `@range`, `@length` or `@pattern` at all, so its
rules are hand-derived from the API reference. That makes every one of them a
`doc` claim and puts SQS at the front of the recording queue.

## 4. Findings

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| SQS-1 | P0 | **Closed** (`internal/provision/dlq_order_test.go`, `cloudformation/dlq_order_apply_test.go`; queues apply in dependency order). A queue and its dead-letter queue declared together fail when the DLQ's name sorts after the queue's (`x`, `x-dlq`, the usual naming). `provision` applies queues in name order, so the redrive target does not exist yet; the stack rolls back. | `internal/provision/svc_sqs.go`, the `sortedNames` loop | run | n/a (ordering is ours) | A provision test with `orders` and `orders-dlq`; a CloudFormation test with the same template |
| SQS-2 | P2 | **Closed** (`e2e/tests/sqs.spec.ts`, "the access policy hint says it is evaluated"). The create form's Advanced section says of the access policy "stored and returned; doze-aws does not evaluate queue policies". The Configuration tab and the ledger say it is evaluated under soft and enforce. | `internal/console/templates/create.html`, `sqs_create` | read | n/a | Browser test asserts the hint; an enforce test shows the policy deciding |
| SQS-3 | P2 | **Closed** ("a period is legal only as the .fifo suffix"; the pattern follows the FIFO switch and the suffix counts toward 80). The create form's browser `pattern` allows a period anywhere; the service refuses one except as the `.fifo` suffix. A name like `a.b` passes the browser and is refused by the server. | `create.html`, the name input; `sqs/store.go` `validQueueName` | read | doc | Browser test types `a.b` |
| SQS-4 | P2 | **Closed** ("the dead-letter name preview does not double the .fifo suffix"; the server strips and re-adds it the same way). With FIFO on, the "create one alongside" preview builds the DLQ name by appending to what was typed, so a name typed with `.fifo` previews as `x.fifo-dlq.fifo`. What the server then creates is not established. | `create.html`, the `x-text` on the DLQ mode | read | n/a | Handler test for `name=x.fifo`, `dlq_mode=new` |
| SQS-5 | P1 | **Closed** (`sqs/request_ranges_test.go`, red on 12 cases, both protocols).  `ReceiveMessage` with `MaxNumberOfMessages` above 10 returns one message instead of refusing. | `sqs/receive.go`, about line 17 | read | doc: the API reference gives the range 1 to 10 | Rejection-parity case for 11 and for 0 |
| SQS-6 | P1 | **Closed** (`sqs/paging_test.go`; ListDeadLetterSourceQueues too).  `ListQueues` ignores `MaxResults` and never returns `NextToken`. The ledger says so; a caller that pages gets everything on page one. | `sqs/actions.go` | read (ledger) | doc | SDK test with `MaxResults: 1` over three queues |
| SQS-7 | P1 | **Closed** (`sqs/move_task_test.go`; decided: AWS semantics, provenance kept as DeadLetterQueueSourceArn).  `StartMessageMoveTask` refuses a call with no `DestinationArn` ("doze-aws does not track per-message origin queues"). AWS treats it as optional and returns each message to the queue it came from. Stricter than AWS, not more forgiving, and documented; it still breaks a correct caller. | `sqs/actions_ext.go` | read | doc | Decide first (see section 10), then an SDK test either way |
| SQS-8 | P1 | **Closed** (`sqs/policy_test.go`, nine documents on both protocols; shared validator from foundation B9).  Whether `SetQueueAttributes` refuses a `Policy` that is not a valid policy document is not established. Kinesis and SNS store anything and then silently ignore it. | `sqs/attrs.go`, `sqs/guard.go` | not yet checked | doc | Attribute test with `Policy: "not json"` and with a statement missing `Effect` |
| SQS-9 | P1 | **Closed** (`sqs/tags_test.go`, five refusals red; the 50-tag limit turned out to be advice, see SQS-16).  `TagQueue` accepts a tag key of 129 characters. AWS limits a key to 128 and a value to 256 characters, refuses the reserved `aws:` prefix, and allows 50 tags on a queue. Found by the console's atomic tag-save test, which needed a refusal to provoke and got none from SQS (Secrets Manager's model refuses it). More forgiving than AWS. | `sqs/` tag handlers | run | doc: the SQS developer guide's cost-allocation tag page | Rejection-parity cases for key 129, value 257, `aws:` prefix and a 51st tag |
| SQS-10 | P1 | **Closed** with SQS-5.  `SendMessage` accepts `DelaySeconds` 9999 and answers 200. AWS's range is 0 to 900, and neither did a batch entry's. Found writing the wire-dock test, which wanted a refused send. More forgiving than AWS. | `sqs/actions.go` `hSendMessage`; `sqs/store.go` `sendIn` | run | doc: the SendMessage API reference | Rejection-parity cases for 901 and -1 on the single send |

**Found by reading the model's own documentation (`.audit-models/sqs.json`).**
The first pass wrote these rules from memory and from older developer-guide
pages; the model's member and operation docs and its error shapes are the
current statement. Every row below was red first (`sqs/model_docs_test.go`,
`sqs/system_attrs_test.go`, `sqs/query_wire_test.go`, `sqs/receive_attempt.go`'s
tests), and the direction is named, because "stricter than AWS" breaks a
correct caller exactly as "more forgiving" breaks a deploy.

| Id | Sev | What | Direction | Source |
|---|---|---|---|---|
| SQS-11 | P0 | **Closed.** A message, a batch and `MaximumMessageSize` were 256 KiB (1,024 to 262,144, default 262,144). The model says 1,024 to 1,048,576, default 1,048,576, and 1 MiB for a batch's total. | stricter | model: SendMessage, SendMessageBatch, CreateQueue.Attributes |
| SQS-12 | P0 | **Closed.** `MessageGroupId` on a standard queue was refused. The model: "In standard queues, using MessageGroupId enables fair queues." It is held to its format there and returned as a system attribute; `MessageDeduplicationId` on a standard queue stays refused (the model says it applies only to FIFO queues; the wording is memory). | stricter | model: SendMessage.MessageGroupId |
| SQS-13 | P0 | **Closed.** The Query wire refused TagQueue, UntagQueue, SetQueueAttributes and all three batch operations on every valid request ("Member must not be null"): the model's required-member check read the model's names (`Tags`, `Entries`, `Attributes`, `TagKeys`), and the wire spells flattened lists and maps by their `xmlName` (`Tag.N.Key`, `SendMessageBatchRequestEntry.N`, `Attribute.N.Name`, `TagKey.N`). aws-sdk-go v1 speaks JSON for SQS now, so nothing had been exercising this wire. ListQueueTags also answered `<Attribute><Name>` for `<Tag><Key>`. `TestEveryOperationIsAcceptedOnTheQueryWire` posts all 23. | stricter, and wrong shape | model: xmlName and xmlFlattened traits |
| SQS-14 | P1 | **Closed.** A character outside the allowed set was `InvalidParameterValue`; the model says `InvalidMessageContents` (also per batch entry). | wrong code | model: SendMessage |
| SQS-15 | P1 | **Closed.** A malformed receipt handle was `InvalidParameterValue`; the model lists `ReceiptHandleIsInvalid` for DeleteMessage and ChangeMessageVisibility (404 on the Query wire, per its `awsQueryError`). | wrong code | model: error shapes |
| SQS-16 | P1 | **Closed.** More than 50 tags was refused. The model: "isn't recommended". Not a refusal. | stricter | model: TagQueue |
| SQS-17 | P1 | **Closed.** The move-task operations: a missing queue is `ResourceNotFoundException` (not `QueueDoesNotExist`); `ListMessageMoveTasks` `MaxResults` is 1 to 10; the `TaskHandle` is "only populated for tasks that are in RUNNING status"; `DestinationArn` is absent when none was named (it was minted as the ARN of an empty name); `ApproximateNumberOfMessagesToMove` and `MaxNumberOfMessagesPerSecond` (at most 500) are recorded; newest-first held only to the second (the key now carries nanoseconds). | wrong shape | model |
| SQS-18 | P1 | **Closed.** AddPermission: `Actions` are "the name of any action or *" (it took ten); at most seven per statement (`OverLimit`); "does not support adding a non-account principal" (it took ARNs); label at most 80 of letters, digits, hyphen, underscore (unchecked); account numbers are twelve digits. | both | model: AddPermission |
| SQS-19 | P1 | **Closed.** `ChangeMessageVisibility` past "the maximum time left" (twelve hours from the receive) is refused. | more forgiving | model: ChangeMessageVisibility |
| SQS-20 | P1 | **Closed.** A batch entry's `AWSTraceHeader` was dropped on both protocols (the path an SNS fan-out takes, so the trace stopped there); the Query spelling was `MessageSystemAttribute.entry.N` only; `MD5OfMessageSystemAttributes` was never returned. | wrong | model: SendMessageBatchRequestEntry, SendMessageResult |
| SQS-21 | P2 | **Closed.** `MessageAttributeNames` took no `prefix.*` ("all message attributes starting with a prefix, for example bar.*"). | wrong | model: ReceiveMessage |
| SQS-22 | P1 | **Closed.** `ReceiveRequestAttemptId` was ignored. A FIFO retry within five minutes returns the same messages and handles, starting the timeout again, until one is deleted or changed; on a standard queue it is refused (wording memory). In memory: a restart inside the five minutes forgets it. | missing | model: ReceiveMessage |
| SQS-23 | P1 | **Closed.** A queue URL names its owner (`/<account>/<name>`) and was resolved by name alone; `GetQueueUrl` ignored `QueueOwnerAWSAccountId`. A hard-coded account id worked locally and failed on deploy. | more forgiving | model: GetQueueUrl |
| SQS-24 | P2 | **Closed.** `KmsDataKeyReusePeriodSeconds` had no range (60 to 86,400); `FifoQueue`, `ContentBasedDeduplication` and `SqsManagedSseEnabled` read any value other than "true" as false ("Valid values are true and false"). | more forgiving | model: CreateQueue.Attributes |
| SQS-25 | P1 | **Closed** (`TestGetQueueAttributesWithNoNamesReturnsNothing`). `GetQueueAttributes` with no `AttributeNames` returned every attribute. The model: "if you don't specify values for this parameter, the request returns empty results". A client that forgot the parameter saw everything here and nothing on AWS. | more forgiving | model: GetQueueAttributes |
| SQS-26 | P1 | **Closed** (`sqs/query_shape_test.go`). The model's constraint walk read a Query request as one element of each list and a flattened map as a list of pairs, so on the Query wire only the first batch entry was checked, no attribute was, and only one system-attribute name met its enum: a second entry with no body was refused whole on JSON and answered as one failed entry on Query. The request is now rendered whole, in the JSON body's shape, before the walk. | more forgiving | model: xmlName, xmlFlattened |
| SQS-27 | P1 | **Closed** (`sqs/stale_handle_test.go`). A receipt handle was the message's, so a delete with an old one removed the message. The model: "you must use the ReceiptHandle from the most recent time you received the message. If you use an old ReceiptHandle, the request will succeed, but the message might not be deleted." The slow-worker double-processing bug was invisible locally. The receive count is now in the handle (already persisted in the message; nothing new is stored), and a delete with an earlier receive's handle succeeds and leaves the message. Handles minted before this carry no count and stay good. | more forgiving | model: DeleteMessage |
| SQS-28 | P2 | **Closed, opt-in** (`sqs/strict_test.go`). The 60-second `QueueDeletedRecently` and `PurgeQueueInProgress` rules were unmodelled. They are now there behind `--sqs-strict` / `[sqs] strict = true` (`StackConfig.SQSStrict`), off by default, with the names and times held in memory only. | more forgiving, by default | model: DeleteQueue, PurgeQueue |
| SQS-29 | P1 | **Closed** (`sqs/move_pacing_test.go`, `move_task_test.go`). A move task finished inside the call, so a test that asserted the moved messages straight after the start passed here and was racy on AWS; `RUNNING` was never seen, a cancel always failed, and the rate was a record. It is now a background task: `RUNNING` with a handle, a short start-up, the stated rate (credit-based, so a rate of 1 is one a second), one per queue, cancellable with the count moved, a restart mid-move loses the running task (memory-only by design; finished records persist), and Close stops it. | more forgiving | model: StartMessageMoveTask, ListMessageMoveTasks, CancelMessageMoveTask |
| SQS-30 | P1 | **Closed 2026-10-12** (`peer_identity_sqs_test.go`, red first: all four helpers answered `QueueDoesNotExist` under account 111122223333; `internal/peercall` `peerQueueURL`, a URL with no account). **Found 2026-10-12, caused by SQS-23.** SQS-23 made SQS compare a queue URL's account with its own. The peer callers (`internal/peercall` `SQSSend`, `SQSReceive`, `SQSDelete`, `SQSSendDetailed`) build the URL with `awsident.AccountID`, the constant default `000000000000`, so under any other `--account-id` every internal delivery through them is refused `QueueDoesNotExist`. Reproduced against a built binary with `--account-id 111122223333`: an EventBridge rule targeting a queue delivers under the default account and logs `eventbridge: rule r1 -> sqs q1: AmazonSQS.SendMessage: Bad Request: ... QueueDoesNotExist` under the other; SNS to SQS delivers in both (its own path). Lambda's SQS trigger is the same helpers (LAM-36, reproduced by that review, not by me). Stage 1 closed with this open because the second-identity pass (foundation B2) was not re-run after SQS-23. | wrong behaviour (a hard-coded identity; a regression of this stage) | run 2026-10-12; read `peercall.go` lines 61, 105, 267, 596 and `sqs/sqs.go` `checkOwner` |


**Moved into this stage from other services.** They keep their own ids, and
are closed here because leaving them would sign SQS off too early (the plan's
README says why for each).

| Id | Sev | What | Where | Evidence |
|---|---|---|---|---|
| LAM-6 | P1 | **Closed** (`integration_sqs_batch_test.go`: no message reached the function until the poller was fixed). Lambda's poller asks `ReceiveMessage` for the trigger's `BatchSize`. Today anything above 10 quietly returns one message at a time. The moment SQS-5 makes that a refusal, every trigger with a batch size above 10 stops receiving. The poller's cap goes in the same commit as SQS-5. | `lambda/extras.go` `pollSQS` | run |
| CFN-4 (the queue part) | P0 | **Closed** (`cloudformation/queue_policy_apply_test.go`; a policy for a queue the template does not declare is applied against a queue that must exist, and the export writes the policy back out).  `AWS::SQS::QueuePolicy` is accepted and dropped. A template's grant never reaches the queue, and the policy is evaluated under soft and enforce. | `internal/cfn/` resource mapping | run |

SQS-1 touches `internal/provision`, which `doze-aws apply` also uses and which
must keep adopting existing resources. The ordering is a pure improvement for
both callers, so it needs no mode (foundation B12 resolved that), and
`TestApplyAdoptsWhatIsThereAndDeletesNothing` pins what `apply` must keep doing.

Owned elsewhere, listed so they are not lost: the queue-visibility versus
function-timeout check (Lambda, LAM-5), a FIFO queue accepted as an S3
notification target or subscribed to a standard topic (S3, SNS), a deleted
function's poller still receiving (Lambda, LAM-1). All are in section 9.

## 5. What to check

Beyond the common criteria. Each line is checked on **both** protocols.

**Ticked 2026-10-11**, each against a test that was red first or an existing one
named here. The Query wire is held by `sqs/query_wire_test.go` (all 23
operations accepted, spelled as the API reference spells them) and
`TestTheQueryWireHoldsTheSameRules`; the rules themselves are in
`sqs/section5_test.go`, `model_docs_test.go`, `system_attrs_test.go`,
`permission_test.go`, `fifo_attrs_test.go`, `move_task_test.go`,
`policy_test.go`, `tags_test.go`, `visibility_rules_test.go`, and across a
restart `persistence_sqs_test.go`. The numbers in this section were first
written from memory; the corrections are SQS-11 to SQS-29 in section 4.

**Queues**
- [x] Name rules: 1 to 80, letters, digits, hyphen, underscore; `.fifo` only as
      the suffix and only with `FifoQueue`; the suffix counts toward 80.
- [x] Re-creating a queue with the same attributes succeeds; with a different
      value it answers `QueueAlreadyExists` (Query) / `QueueNameExists` (JSON)
      naming the attribute.
- [x] Deleting a queue and recreating it at once: AWS asks for 60 seconds
      (`QueueDeletedRecently`). **Decided: opt-in.** A delete here is immediate, so by default
      there is no minute to wait out (a listed difference; the entry goes into
      `deviations.py` when `test_recreate_after_delete` is recorded). `--sqs-strict`
      / `[sqs] strict = true` refuses it as AWS does (`sqs/strict_test.go`), for the
      person who wants the failure before the deploy. Off by default because a
      minute in every local test loop is a worse tool.
- [x] `PurgeQueue` twice inside 60 seconds: AWS answers `PurgeQueueInProgress`.
      **Decided: opt-in**, the same switch (`test_purge_twice`).
- [x] Every attribute's range, and a non-numeric value, refused with
      `InvalidAttributeValue`; an unknown attribute name refused.
- [x] Clearing an attribute removes it (there is a test; keep it).

**Messages**
- [x] Body: the allowed character set, and the size limit counting the body
      **and** the attributes.
- [x] Message attributes: at most 10, name rules, `String` / `Number` /
      `Binary` and custom type suffixes, and the MD5 of attributes that the SDK verifies.
- [x] Batches: 1 to 10 entries, distinct ids, the id pattern, total size, and
      per-entry failures returned in `Failed` without failing the batch.
- [x] Delay: queue-level, and per message on a standard queue; a per-message
      delay on a FIFO queue is refused.
- [x] Receive: 1 to 10 (SQS-5), `WaitTimeSeconds` 0 to 20, `VisibilityTimeout`
      override, attribute and message-attribute selection, an empty receive.
- [x] Visibility: `ChangeMessageVisibility` on a message not in flight, with an
      invalid or expired receipt handle, and the 12-hour total.
- [x] A message not deleted returns with `ApproximateReceiveCount` one higher,
      and moves to the DLQ on the receive that reaches `maxReceiveCount`.

**FIFO**
- [x] `MessageGroupId` required; `MessageDeduplicationId` required unless
      content-based dedup is on; the 5-minute dedup window; the same dedup id
      returns the original `MessageId` and `SequenceNumber`.
- [x] Group locking: while one message of a group is in flight, no later one
      from that group is delivered; other groups are.
- [x] Sequence numbers increase within a group.
- [x] `DeduplicationScope` and `FifoThroughputLimit`: establish what they do
      here and set the tier.

**Redrive**
- [x] `RedrivePolicy` validation: all five rules in the ledger.
- [x] `RedriveAllowPolicy`: establish whether it is enforced and set the tier.
- [x] `ListDeadLetterSourceQueues` lists exactly the queues pointing at this one.
- [x] A move task: with and without `MaxNumberOfMessagesPerSecond`, the task
      record, and what happens to each message's receive count (reset to 0 here;
      **not established**, so in the recording queue: `test_redrive_receive_count`).

**Access**
- [x] `AddPermission` writes the statement AWS writes; a label in use is
      refused; `RemovePermission` on a missing label.
- [x] Under enforce, a `Deny` in the queue policy refuses the root; an SNS
      service principal needs its own statement.

**Lifecycle**
- [x] Retention: a message older than the period is dropped on a queue nobody reads.
- [x] Everything above survives a restart, including in-flight timers and dedup state.

## 6. Console

The design's "SQS" page. 21 boards, 19 screens. Templates: `templates/sqs.html`,
the `sqs_create` block in `create.html`, shared pieces in `panes.html` and
`layout.html`. Handlers: the `sqs*` functions in `handlers.go`, routes in
`routes_messaging.go`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Sqs-01-None | No queues yet | `sqs_home`, `empty_detail` | exists |
| Sqs-02-Pick | Queues, none open | `sqs_home` | exists (see foundation A7) |
| Sqs-03-Create | Create, with the life of a message | `sqs_create` | **Built**: the strip (`.life`, a pure function of the form's numbers) and teaching lines from the settings record |
| Sqs-04-Create-FIFO | FIFO, Advanced open | `sqs_create` | **Built**: "How a FIFO queue behaves", five sentences, each held in section 5 |
| Sqs-05-Create-Refused | Refused inline | `fail_inline` | exists |
| Queue | Messages: peek, one opened | `sqs_queue`, `message_panel` | exists |
| Sqs-08-Send | Composer | `sqs_queue` | exists |
| Sqs-09-Send-Refused | A send refused | `fail_inline` | exists |
| Sqs-20-Denied | A send denied under enforce | `fail_inline` | exists |
| Sqs-10-Consume-Empty, Sqs-11-Consume-Held | Receive and hold | `sqs_consume`, `sqs_consumed` | exists |
| Bad-DLQ | A dead-letter queue | `sqs_queue` | exists |
| Sqs-14-Redrive-Confirm, Sqs-15-Redrive-Done | Redrive | `message_panel` | exists; the confirm now says every message moves, held and delayed ones included, and names the destination when there is one |
| Sqs-16-Confirms | Delete queue, purge, delete message | `hx-confirm` | exists |
| Sqs-17-Config | Configuration in plain words | `sqs_config` | **Built**: a sentence on each row, receive wait, largest message and encryption rows, and "In this stack" |
| Sqs-18-Config-Edit | Editing, reader checked | `sqs_config` | **Built**: "What saving does" (the form against the stored values); the reader check is on the view, in both states |
| Sqs-21-Config-Refused | Edit refused | `fail_inline` | exists |
| Sqs-19-Tags | Tags as a draft | `tag_editor` | exists (see foundation A5) |

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Teaching lines under each field | The settings table (foundation B4): range and default from `sqs/attrs.go` `attrRange`, tier from the ledger |
| The life of one message | The form's own numbers. A pure function of the form. |
| How a group behaves | Static, FIFO only. Each sentence is checked in section 5 (group locking). |
| "In this stack": reader's timeout against visibility | The wiring graph's `esm` edge, the function's `Timeout`, the queue's `VisibilityTimeout` |
| What changes | The form against the stored attributes |

The reader check says, when visibility is shorter than the function timeout,
that AWS refuses that trigger and doze-aws does not check. That sentence is a
`doc` claim until recorded, and stops being true once Lambda's stage adds the
check; the text is then "AWS refuses this trigger, and so does doze-aws".

**Statements on the boards not yet backed.** Resolved: a redriven message's
receive count is not stated anywhere on a board (it is in the recording queue);
a visibility change leaving held messages alone is stated and held by
`TestAQueueVisibilityChangeLeavesHeldMessagesAlone`; the text of a failed move
task is the service's own `FailureReason`, shown as it came.

The reader check's refusal half ("AWS refuses this trigger") is **not** on the
board: the Lambda model does not contain it and the developer-guide statement is
memory (LAM-5 holds the wording). The board says what is established: the
comparison, AWS's six-times guidance, and that doze-aws does not check the
trigger when it is created.

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each board from the dream design
(`console-design/project/*.dc.html`, the canvas at claude.ai/artifact/1WSqsF1GUQJdrhscUzzcTw)
and the real page side by side. The first pass built the *content* of the
boards (the teaching lines, the life of a message, the reader check, what
changes) and not their *shape*, and no board was compared before the stage was
called done. This table is the comparison, and the gate: a board is "matches"
only when it was looked at.

| Board | Verdict | What differs |
|---|---|---|
| Sqs-01 None, Sqs-02 Pick | matches | copy is `copy.go`'s; the list note is there |
| Sqs-03 Create | **matches** (built 2026-10-11, after the comparison; rendered at 1440 in light and dark and at 900) | *Was:*  The life of one message is a right-hand panel in the design (dot per step, a bar for the hidden time, the dead-letter queue named, "receive count goes to 2", and the closing sentence "a received message that is deleted never reaches step 3"); here it is a plain list under Delivery without those. Line 4 of the teaching pattern ("0 – 43 200 · default 30 · **enforced**") has no enforced/stored marker. The dead-letter paragraph ("a message that keeps coming back is probably the one your consumer cannot handle…") is missing. |
| Sqs-04 Create FIFO | **matches** | *Was:*  Five sentences here against the design's three (the extra two are grounded). The design's third ("one stuck message holds back only its own group; a dead-letter queue lets it out of the way") is missing. **The design is wrong on size**: it shows 1 024 – 262 144; the model says 1 048 576 (SQS-11). |
| Sqs-05 Create refused | matches | inline refusal, what you typed kept |
| Sqs-08 Send, Sqs-09 Send refused | matches | **design text is stale**: "exceeds MaximumMessageSize 262144" (now 1 048 576, and the real wording is "Message must be shorter than N bytes (the body and its attributes were M)") |
| Sqs-10, Sqs-11 Consume | matches | untouched |
| Sqs-14 Redrive confirm | **matches** | *Was:*  A dialog with the destination picked inside it ("Move all 3 messages back?", "Move them to", Cancel / Redrive). Here the picker is a select in the header and the confirm is one sentence. |
| Sqs-15 Redrive done | **matches** (move tasks are asynchronous since 2026-10-11; the board's running row renders and is tested) | *Was:* the design shows "Redriving → X · 2 moved so far" and a task that was seen running. A move here finishes inside the call, so that state never exists. It needs the asynchronous move task (the open item). |
| Sqs-16 Confirms | **matches** | *Was:*  The design's confirms have bodies and "Keep it" buttons ("The queue and the 3 messages waiting in it are removed from this machine. Anything sending to it afterwards gets NonExistentQueue." / "Every waiting and in-flight message is dropped. The queue and its settings stay." / "It is gone for good."). Here delete queue and purge are a question with no body. |
| Sqs-17 Config | **matches** | *Was:*  Two columns in the design (settings left, "In this stack" and the queue policy right); one stacked column here. The Type row's sentence is missing. "In this stack" does not compute the six-times figure ("which here would be 180 s"). |
| Sqs-18 Config edit | **matches** | *Was:*  "What changes" is a right-hand panel with the intro line "Only the settings you touched. Everything is shown in the units you would say aloud." and the board's name; here it is "What saving does" below the form. The live neighbour callout inside the form ("Shorter than the reader's timeout") is missing from the edit form: it is only on the read view. The "After receives" sentence ("With 3, a message that keeps coming back is set aside on its third receive") is missing on the edit form. The unit echo is not shown below 60 s ("20 seconds") or for zero ("none"). |
| Sqs-19 Tags | matches | foundation A5 |
| Sqs-20 Denied, Sqs-21 Refused | matches | untouched |
| Bad-DLQ, Queue | differs, deliberately | The design's sentence strip ("3 waiting · 0 in flight · kept 4 days") against the shell's fact strip (Type / Visible / In flight / Visibility timeout / Retention / Dead-letter) from the foundation's `res_header`. A shell decision, not an SQS one; not re-opened here. |

**Where the design was wrong** (corrected in the working copy on 2026-10-11 in Sqs-04, Sqs-09, Sqs-18 and Sqs-21; the published canvas still carries the old text until it is republished)
- 262 144 in three boards: 1 048 576 (SQS-11).
- "AWS does not let that trigger be attached to a queue like this" on Sqs-18: the
  Lambda model does not say it and the developer-guide wording is memory (LAM-5).
  The console says what is established (the comparison, AWS's six-times guidance,
  that doze-aws does not check). The design should say the same.
- "Takes effect on the next receive": true of this emulator (held by
  `TestAQueueVisibilityChangeLeavesHeldMessagesAlone`) and not established for AWS.

**Build list, in order** (items 1 to 5 done 2026-10-11 and checked against rendered boards; 6 waits for the asynchronous move task)
1. Teaching line 4 on every setting: enforced or stored, from the settings record's tier (kit `setting_field`).
2. The edit form's live neighbour callout and the "After receives" sentence.
3. Confirm bodies and the verbs (delete queue, purge, delete message), and the redrive dialog with its picker.
4. Two-column Configuration (settings / "In this stack" and the queue policy) and the right-hand "What changes" and "life of one message" panels.
5. The life-of-a-message steps with their numbers, the dead-letter paragraph, the third FIFO sentence, the six-times figure, the Type sentence, the unit echo under a minute.
6. Redriving → "N moved so far": built with the asynchronous move task (SQS-29).

## 7. Tests to add

| Layer | Tests |
|---|---|
| Provision | Queue and DLQ in one spec, in both name orders (SQS-1) |
| Store, handler | Receive 0 and 11 (SQS-5); policy validation (SQS-8); `x.fifo` with a DLQ alongside (SQS-4) |
| SDK v2 | `ListQueues` paging (SQS-6); move task with and without a destination (SQS-7) |
| Rejection parity | Every range in section 5 on Query and JSON |
| Restart | In-flight timer, dedup window, move-task record |
| Conformance (boto3) | FIFO dedup and group order; batch partial failure; redrive round trip; delay and long poll; attribute MD5; purge and delete timing |
| Browser | Name with a period (SQS-3); policy hint (SQS-2); each `new` item; the reader check in both states; each confirm's sentence |
| Mutation | `sqs/` baseline, then survivors in `attrs.go`, `receive.go`, `store.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today.

| Claim | Source now | Scenario |
|---|---|---|
| Every attribute range and its error code | doc | `test_sqs::attribute_ranges` |
| `MaxNumberOfMessages` 11 is `ReadCountOutOfRange` | memory (the code name) | `receive_out_of_range` |
| `QueueDeletedRecently` within 60 s | doc | `recreate_after_delete` |
| `PurgeQueueInProgress` within 60 s | doc | `purge_twice` |
| A redriven message's `ApproximateReceiveCount` | memory | `redrive_receive_count` |
| A visibility change and messages already in flight | memory | `visibility_change_in_flight` |
| `StartMessageMoveTask` with no destination returns each message to its origin; a source that is no queue's DLQ is refused; a message with no origin fails the task | doc | `move_task_default_destination` |
| An invalid `Policy` at `SetQueueAttributes` | memory | `invalid_policy` |
| Message attribute limits and MD5 | doc | `message_attributes` |
| `MaximumMessageSize` default and range (1 MiB), a batch total of 1 MiB | model | `test_message_limits`, `test_batch_total_size` |
| `MessageGroupId` accepted on a standard queue; `MessageDeduplicationId` there refused (wording memory) | model, memory | `test_message_group_on_a_standard_queue` |
| The code for a body character outside the set (`InvalidMessageContents`) and for a malformed handle (`ReceiptHandleIsInvalid`, 404 on the Query wire) | model | `test_message_limits`, `test_refusals` |
| More than 50 tags accepted | model | `test_tag_limits` |
| `AddPermission`: any action, seven at most (`OverLimit`), no ARN principal, label rules | model | `test_add_permission_rules` |
| The twelve hours from the receive | model | `test_twelve_hours_from_the_receive` |
| `ReceiveRequestAttemptId` replay, and refusal on a standard queue | model, memory | `test_receive_request_attempt_id` |
| A queue URL's account and `QueueOwnerAWSAccountId` | model | `test_queue_in_another_account` |
| Boolean and KMS attribute values; `DeduplicationScope` on a standard queue | model | `test_fifo_attribute_rules` |
| An old receipt handle after the message was received again: the request succeeds but the message "might not be deleted" (here a delete with an earlier receive's handle succeeds and leaves it; change-visibility with one is still accepted) | model | `test_an_old_receipt_handle` |
| The statement count at which `AddPermission` is `OverLimit` (the model says a limit exists, not its value); the in-flight limit of about 120,000 `OverLimit` on ReceiveMessage (not modelled, a listed difference) | model | `test_add_permission_statement_limit` |
| `RedrivePolicy` without `maxReceiveCount`: the model says the default is 10; doze-aws refuses it | model | `test_redrive_policy_without_max_receive_count` |

## 9. Owed to integration

| What | With |
|---|---|
| A trigger whose queue hides messages for less time than the function may run is refused | Lambda |
| A deleted function's poller stops; its messages are not received on its behalf | Lambda |
| A FIFO queue as an S3 notification target | S3 |
| A FIFO queue subscribed to a standard topic; a missing `sns.amazonaws.com` statement under enforce | SNS |
| A queue and its DLQ in one template, through CloudFormation, SAM, CDK and Terraform | CloudFormation |
| Redrive under load, and a restart mid-move | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [x] SQS-1 to SQS-29 each closed, with the test that failed first named beside it (section 4).
- [x] **SQS-30 closed** (2026-10-12): the four peer helpers deliver, receive and delete under `--account-id 111122223333` in `TestPeerHelpersReachAQueueUnderAnotherAccount`, `check:full`, `api` and `lightness` green, and the browser suite re-run under account 111122223333 in eu-west-1: 261 passed, route gate 368 of 371, and the two failures are the two known before this change (the EventBridge replay from the console, whose bus and archive ARNs are written with the default account at `handlers_eb.go:285` and `client_eb.go:388`; and the S3 share link, which signs for `us-east-1`). Found after stage 1 was signed off. The fix is not an identity threaded through `peercall`: the URL stays in process and names no account (LAM-36's cause as well).
- [x] LAM-6 closed in the same commit as SQS-5: a trigger with batch size 20
      still receives, in receives of at most 10.
- [x] The queue part of CFN-4 closed: a stack with a queue and an
      `AWS::SQS::QueuePolicy` leaves that policy on the queue, and a policy
      naming a queue that does not exist fails the stack.
- [x] `doze-aws apply` over an existing queue still adopts it: `TestApplyAdoptsWhatIsThereAndDeletesNothing` (foundation B12) stays green.
- [x] Decided and written into `SUPPORT.md`: `DestinationArn` (SQS-7), the
      60-second delete and purge rules, `RedriveAllowPolicy`, `DeduplicationScope`,
      `FifoThroughputLimit`.
- [x] Every box in section 5 ticked on both protocols, or moved to the
      recording queue with its test written and skipped.
- [x] The attribute tier table in section 2 has no "?" left, and every C
      attribute reads as cosmetic in the create form and the Configuration tab.
- [ ] All 21 boards implemented from the foundation's shell with no SQS-only
      chrome, and each `new` item reads only the data named for it. **21 of 21
      (2026-10-11):** the board-by-board comparison in 6a found six differing in
      shape and they were rebuilt and re-rendered, and Sqs-15's "Redriving, N moved so far" waited on the asynchronous move task, which is now built.
- [x] No statement on an SQS screen is in the "not yet backed" list in section 6.
- [x] The three confirms say only what the code does (delete queue, purge, delete message; and the redrive confirm, corrected).
- [x] The reader check appears in both states and names the real function.
- [x] `e2e/tests/sqs.spec.ts` covers every route under `/sqs` with the route
      gate on, and the accessibility sweep is clean on all four tabs and create.
- [x] Conformance: scenarios for every row in section 8 exist (35 in `test_sqs.py`). Zero "answered
      differently". The unverified count is written here: **139 responses**, all of them: nothing is recorded until there is an AWS account.
- [x] Mutation baseline recorded here: **708 killed, 58 survived, 5 timed out, 73 not covered (92.4%)**, `testdata/mutation.json`, recorded 2026-10-11 against the code with receipt handles, strict mode and asynchronous move tasks. It was 631 / 80 for the first stage-1 code; the edges and behaviours found standing were tested (`sqs/boundaries_test.go`, `sqs/edges_test.go`) and it was recorded again. Why the 58 stand:
      - Mostly `CONDITIONALS_BOUNDARY` and `CONDITIONALS_NEGATION` on guards whose other side a request cannot reach (a value the handler has already range-checked, a bolt error branch, the wait before a move starts, the edge of the five-minute attempt-id window, which is memory).
      - `codec.go` and `responses.go`: the Query wire's decoder loops and XML writers, whose effect is an empty element nobody reads.
      - `move_task.go`: the pacing guards (`moveStartDelay > 0`, a credit under one). They change when a message moves, not whether.
      - A few in `md5.go` (the handle's no-count sentinel and the digest's binary branch) were tested after the run and now die; the baseline still lists them, which the check allows.
      - The 73 not covered are lines no test runs: error branches of the store and the janitor.
- [x] Actual time recorded in section 1 (not timed; the size was several times the estimate), and the plan's order table re-estimated.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: attribute tiers, decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for SQS-1 to SQS-8 | `internal/provision/*_test.go`, `sqs/*_test.go`, `cloudformation/*_test.go` |
| 3 | DLQ ordering: apply a queue after the queue its redrive policy names (no mode; foundation B12) | `internal/provision/svc_sqs.go` |
| 3a | Queue policy as a resource | `internal/cfn/resources.go` and the SQS mapper beside it |
| 3b | Poller cap, with SQS-5 | `lambda/extras.go` |
| 4 | Receive range, paging, policy validation, move task | `sqs/receive.go`, `sqs/actions.go`, `sqs/attrs.go`, `sqs/guard.go`, `sqs/actions_ext.go`, `sqs/store_ext.go`, `sqs/validate_overrides.go` |
| 5 | Section 5 sweep, parity cases | `sqs/rejection_parity_test.go`, `sqs/server_test.go`, `sqs/sdk_test.go` |
| 6 | Conformance scenarios and claims table | `conformance/tests/test_sqs.py`, the claims file from foundation B1 |
| 7 | Console | `internal/console/templates/sqs.html`, `create.html`, `handlers.go` (`sqs*`), `client.go`, `client_sqs.go`, `held.go` |
| 8 | Browser tests | `e2e/tests/sqs.spec.ts` |
| 9 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

**Prior art.** LocalStack models the two 60-second rules as opt-in strictness
(`SQS_DELAY_RECENTLY_DELETED`, `SQS_DELAY_PURGE_RETRY`, both off by default),
which is the precedent for making them a decision instead of assuming. LocalStack orders CloudFormation
resources by dependency rather than by name, which is the general fix for
SQS-1: the redrive target is a dependency, so order by that. AWS's own SQS
developer guide is the source for every hand-derived rule in section 5, and
each rule's test names the page.
