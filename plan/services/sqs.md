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
CloudFormation. **Actual:** _fill in when done; the
order table in the plan is re-estimated from this number._

## 2. Contract

The model lists 23 operations. Today the ledger groups them into 18 functional
rows and one extension.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateQueue, DeleteQueue, GetQueueUrl | F | F | |
| ListQueues | F | F | Ignores `MaxResults` and `NextToken` today. See SQS-6. |
| GetQueueAttributes, SetQueueAttributes | F | F | Per-attribute tier below. |
| TagQueue, UntagQueue, ListQueueTags | F | F | |
| SendMessage, SendMessageBatch | F | F | |
| ReceiveMessage | F | F | See SQS-5. |
| DeleteMessage, DeleteMessageBatch | F | F | |
| ChangeMessageVisibility, ChangeMessageVisibilityBatch | F | F | |
| PurgeQueue | F | F | |
| ListDeadLetterSourceQueues | F | F | |
| StartMessageMoveTask, ListMessageMoveTasks, CancelMessageMoveTask | F | F | `DestinationArn` is required here and optional on AWS. See SQS-7. |
| AddPermission, RemovePermission | F | F | |
| DozePeek | extension | extension | Read without consuming. Not an AWS operation. |

Queue attributes need their own tiers, because "SetQueueAttributes is F" hides
which settings do anything:

| Attribute | Target | To confirm |
|---|---|---|
| VisibilityTimeout, DelaySeconds, MessageRetentionPeriod, MaximumMessageSize, ReceiveMessageWaitTimeSeconds | F | Ranges are enforced today. |
| RedrivePolicy | F | Target exists, same type, 1 to 1000, not itself. |
| RedriveAllowPolicy | ? | It is in the attribute enum. Whether it is enforced is not established. |
| FifoQueue, ContentBasedDeduplication | F | |
| DeduplicationScope, FifoThroughputLimit | ? | Stored. Whether dedup scope changes behaviour is not established. |
| KmsMasterKeyId, KmsDataKeyReusePeriodSeconds, SqsManagedSseEnabled | C | Stored, not enforced. The console must say so. |
| Policy | F | Evaluated under soft and enforce. Whether an unparseable document is refused is not established (SQS-8). |

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
| SQS-1 | P0 | A queue and its dead-letter queue declared together fail when the DLQ's name sorts after the queue's (`x`, `x-dlq`, the usual naming). `provision` applies queues in name order, so the redrive target does not exist yet; the stack rolls back. | `internal/provision/svc_sqs.go`, the `sortedNames` loop | run | n/a (ordering is ours) | A provision test with `orders` and `orders-dlq`; a CloudFormation test with the same template |
| SQS-2 | P2 | The create form's Advanced section says of the access policy "stored and returned; doze-aws does not evaluate queue policies". The Configuration tab and the ledger say it is evaluated under soft and enforce. | `internal/console/templates/create.html`, `sqs_create` | read | n/a | Browser test asserts the hint; an enforce test shows the policy deciding |
| SQS-3 | P2 | The create form's browser `pattern` allows a period anywhere; the service refuses one except as the `.fifo` suffix. A name like `a.b` passes the browser and is refused by the server. | `create.html`, the name input; `sqs/store.go` `validQueueName` | read | doc | Browser test types `a.b` |
| SQS-4 | P2 | With FIFO on, the "create one alongside" preview builds the DLQ name by appending to what was typed, so a name typed with `.fifo` previews as `x.fifo-dlq.fifo`. What the server then creates is not established. | `create.html`, the `x-text` on the DLQ mode | read | n/a | Handler test for `name=x.fifo`, `dlq_mode=new` |
| SQS-5 | P1 | **Closed** (`sqs/request_ranges_test.go`, red on 12 cases, both protocols).  `ReceiveMessage` with `MaxNumberOfMessages` above 10 returns one message instead of refusing. | `sqs/receive.go`, about line 17 | read | doc: the API reference gives the range 1 to 10 | Rejection-parity case for 11 and for 0 |
| SQS-6 | P1 | **Closed** (`sqs/paging_test.go`; ListDeadLetterSourceQueues too).  `ListQueues` ignores `MaxResults` and never returns `NextToken`. The ledger says so; a caller that pages gets everything on page one. | `sqs/actions.go` | read (ledger) | doc | SDK test with `MaxResults: 1` over three queues |
| SQS-7 | P1 | `StartMessageMoveTask` refuses a call with no `DestinationArn` ("doze-aws does not track per-message origin queues"). AWS treats it as optional and returns each message to the queue it came from. Stricter than AWS, not more forgiving, and documented; it still breaks a correct caller. | `sqs/actions_ext.go` | read | doc | Decide first (see section 10), then an SDK test either way |
| SQS-8 | P1 | Whether `SetQueueAttributes` refuses a `Policy` that is not a valid policy document is not established. Kinesis and SNS store anything and then silently ignore it. | `sqs/attrs.go`, `sqs/guard.go` | not yet checked | doc | Attribute test with `Policy: "not json"` and with a statement missing `Effect` |
| SQS-9 | P1 | **Closed** (`sqs/tags_test.go`, five refusals red, and the 50-tag limit).  `TagQueue` accepts a tag key of 129 characters. AWS limits a key to 128 and a value to 256 characters, refuses the reserved `aws:` prefix, and allows 50 tags on a queue. Found by the console's atomic tag-save test, which needed a refusal to provoke and got none from SQS (Secrets Manager's model refuses it). More forgiving than AWS. | `sqs/` tag handlers | run | doc: the SQS developer guide's cost-allocation tag page | Rejection-parity cases for key 129, value 257, `aws:` prefix and a 51st tag |
| SQS-10 | P1 | **Closed** with SQS-5.  `SendMessage` accepts `DelaySeconds` 9999 and answers 200. AWS's range is 0 to 900, and neither did a batch entry's. Found writing the wire-dock test, which wanted a refused send. More forgiving than AWS. | `sqs/actions.go` `hSendMessage`; `sqs/store.go` `sendIn` | run | doc: the SendMessage API reference | Rejection-parity cases for 901 and -1 on the single send |

**Moved into this stage from other services.** They keep their own ids, and
are closed here because leaving them would sign SQS off too early (the plan's
README says why for each).

| Id | Sev | What | Where | Evidence |
|---|---|---|---|---|
| LAM-6 | P1 | **Closed** (`integration_sqs_batch_test.go`: no message reached the function until the poller was fixed). Lambda's poller asks `ReceiveMessage` for the trigger's `BatchSize`. Today anything above 10 quietly returns one message at a time. The moment SQS-5 makes that a refusal, every trigger with a batch size above 10 stops receiving. The poller's cap goes in the same commit as SQS-5. | `lambda/extras.go` `pollSQS` | run |
| CFN-4 (the queue part) | P0 | `AWS::SQS::QueuePolicy` is accepted and dropped. A template's grant never reaches the queue, and the policy is evaluated under soft and enforce. | `internal/cfn/` resource mapping | run |

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

**Queues**
- [ ] Name rules: 1 to 80, letters, digits, hyphen, underscore; `.fifo` only as
      the suffix and only with `FifoQueue`; the suffix counts toward 80.
- [ ] Re-creating a queue with the same attributes succeeds; with a different
      value it answers `QueueAlreadyExists` (Query) / `QueueNameExists` (JSON)
      naming the attribute.
- [ ] Deleting a queue and recreating it at once: AWS asks for 60 seconds
      (`QueueDeletedRecently`). Decide whether to model it; if not, it is a
      listed difference.
- [ ] `PurgeQueue` twice inside 60 seconds: AWS answers `PurgeQueueInProgress`.
      Same decision.
- [ ] Every attribute's range, and a non-numeric value, refused with
      `InvalidAttributeValue`; an unknown attribute name refused.
- [ ] Clearing an attribute removes it (there is a test; keep it).

**Messages**
- [ ] Body: the allowed character set, and the size limit counting the body
      **and** the attributes.
- [ ] Message attributes: at most 10, name rules, `String` / `Number` /
      `Binary` and custom type suffixes, and the MD5 of attributes that the SDK verifies.
- [ ] Batches: 1 to 10 entries, distinct ids, the id pattern, total size, and
      per-entry failures returned in `Failed` without failing the batch.
- [ ] Delay: queue-level, and per message on a standard queue; a per-message
      delay on a FIFO queue is refused.
- [ ] Receive: 1 to 10 (SQS-5), `WaitTimeSeconds` 0 to 20, `VisibilityTimeout`
      override, attribute and message-attribute selection, an empty receive.
- [ ] Visibility: `ChangeMessageVisibility` on a message not in flight, with an
      invalid or expired receipt handle, and the 12-hour total.
- [ ] A message not deleted returns with `ApproximateReceiveCount` one higher,
      and moves to the DLQ on the receive that reaches `maxReceiveCount`.

**FIFO**
- [ ] `MessageGroupId` required; `MessageDeduplicationId` required unless
      content-based dedup is on; the 5-minute dedup window; the same dedup id
      returns the original `MessageId` and `SequenceNumber`.
- [ ] Group locking: while one message of a group is in flight, no later one
      from that group is delivered; other groups are.
- [ ] Sequence numbers increase within a group.
- [ ] `DeduplicationScope` and `FifoThroughputLimit`: establish what they do
      here and set the tier.

**Redrive**
- [ ] `RedrivePolicy` validation: all five rules in the ledger.
- [ ] `RedriveAllowPolicy`: establish whether it is enforced and set the tier.
- [ ] `ListDeadLetterSourceQueues` lists exactly the queues pointing at this one.
- [ ] A move task: with and without `MaxNumberOfMessagesPerSecond`, the task
      record, and what happens to each message's receive count (not established).

**Access**
- [ ] `AddPermission` writes the statement AWS writes; a label in use is
      refused; `RemovePermission` on a missing label.
- [ ] Under enforce, a `Deny` in the queue policy refuses the root; an SNS
      service principal needs its own statement.

**Lifecycle**
- [ ] Retention: a message older than the period is dropped on a queue nobody reads.
- [ ] Everything above survives a restart, including in-flight timers and dedup state.

## 6. Console

The design's "SQS" page. 21 boards, 19 screens. Templates: `templates/sqs.html`,
the `sqs_create` block in `create.html`, shared pieces in `panes.html` and
`layout.html`. Handlers: the `sqs*` functions in `handlers.go`, routes in
`routes_messaging.go`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Sqs-01-None | No queues yet | `sqs_home`, `empty_detail` | exists |
| Sqs-02-Pick | Queues, none open | `sqs_home` | exists (see foundation A7) |
| Sqs-03-Create | Create, with the life of a message | `sqs_create` | form exists; the strip and teaching lines are **new** |
| Sqs-04-Create-FIFO | FIFO, Advanced open | `sqs_create` | form exists; "how a group behaves" is **new** |
| Sqs-05-Create-Refused | Refused inline | `fail_inline` | exists |
| Queue | Messages: peek, one opened | `sqs_queue`, `message_panel` | exists |
| Sqs-08-Send | Composer | `sqs_queue` | exists |
| Sqs-09-Send-Refused | A send refused | `fail_inline` | exists |
| Sqs-20-Denied | A send denied under enforce | `fail_inline` | exists |
| Sqs-10-Consume-Empty, Sqs-11-Consume-Held | Receive and hold | `sqs_consume`, `sqs_consumed` | exists |
| Bad-DLQ | A dead-letter queue | `sqs_queue` | exists |
| Sqs-14-Redrive-Confirm, Sqs-15-Redrive-Done | Redrive | `message_panel` | exists; the confirm naming the destination is restyled |
| Sqs-16-Confirms | Delete queue, purge, delete message | `hx-confirm` | exists |
| Sqs-17-Config | Configuration in plain words | `sqs_config` | table exists; the sentences and "In this stack" are **new** |
| Sqs-18-Config-Edit | Editing, reader checked | `sqs_config` | form exists; "what changes" and the reader check are **new** |
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

**Statements on the boards not yet backed.** Remove or prove each before the
board ships: a redriven message's receive count; whether a visibility change
affects messages already held ("takes effect on the next receive" is the claim);
the text of a failed move task.

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
| `StartMessageMoveTask` with no destination | doc | `move_task_default_destination` |
| An invalid `Policy` at `SetQueueAttributes` | memory | `invalid_policy` |
| Message attribute limits and MD5 | doc | `message_attributes` |

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

- [ ] SQS-1 to SQS-8 each closed, with the test that failed first named beside it.
- [ ] LAM-6 closed in the same commit as SQS-5: a trigger with batch size 20
      still receives, in receives of at most 10.
- [ ] The queue part of CFN-4 closed: a stack with a queue and an
      `AWS::SQS::QueuePolicy` leaves that policy on the queue, and a policy
      naming a queue that does not exist fails the stack.
- [ ] `doze-aws apply` over an existing queue still adopts it: `TestApplyAdoptsWhatIsThereAndDeletesNothing` (foundation B12) stays green.
- [ ] Decided and written into `SUPPORT.md`: `DestinationArn` (SQS-7), the
      60-second delete and purge rules, `RedriveAllowPolicy`, `DeduplicationScope`,
      `FifoThroughputLimit`.
- [ ] Every box in section 5 ticked on both protocols, or moved to the
      recording queue with its test written and skipped.
- [ ] The attribute tier table in section 2 has no "?" left, and every C
      attribute reads as cosmetic in the create form and the Configuration tab.
- [ ] All 21 boards implemented from the foundation's shell with no SQS-only
      chrome, and each `new` item reads only the data named for it.
- [ ] No statement on an SQS screen is in the "not yet backed" list in section 6.
- [ ] The three confirms say only what the code does.
- [ ] The reader check appears in both states and names the real function.
- [ ] `e2e/tests/sqs.spec.ts` covers every route under `/sqs` with the route
      gate on, and the accessibility sweep is clean on all four tabs and create.
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1, and the plan's order table re-estimated.

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
