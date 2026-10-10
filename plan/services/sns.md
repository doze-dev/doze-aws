# SNS

Stage 8. Size M. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds SNS's own.

## 1. Summary

Standard and FIFO topics over one bbolt store (`sns.bolt`), on the Query/XML
protocol only. A publish fans out synchronously to every confirmed subscription
whose filter policy matches. Peers: it delivers into SQS and Lambda and posts to
http(s) webhooks; S3 notifications, EventBridge targets, CloudWatch alarms,
Lambda destinations and Step Functions publish into it; CloudFormation and
`provision` create it; delivery-status records go to CloudWatch Logs.

It is small in operations and wide in consequences: three of its findings change
what a subscriber receives.

**Estimate:** M, three to four focused days. **Actual:** _fill in when done._

## 2. Contract

The model lists 42 operations (`sns/testdata/ops_sns.json`). 21 are dispatched
to real handlers, 21 are stubs, and there is one extension.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateTopic | F | F | Re-create with different attributes merges silently. See SNS-24. |
| DeleteTopic | F | F | Drops the topic's subscriptions. A missing topic answers success. |
| ListTopics | F | F | No `NextToken`. See SNS-17. |
| GetTopicAttributes | F | F | |
| SetTopicAttributes | C→F | F | Per-attribute tier below. |
| Subscribe | F | F | Per-protocol and per-attribute tier below. See SNS-12, SNS-19, SNS-21. |
| ConfirmSubscription | F | F | Ignores `TopicArn`. See SNS-22. |
| Unsubscribe | F | F | Answers success for anything. See SNS-9. |
| ListSubscriptions, ListSubscriptionsByTopic | F | F | No `NextToken` (SNS-17). `Owner` is a literal (foundation A8). |
| GetSubscriptionAttributes, SetSubscriptionAttributes | F | F | Per-attribute tier below. See SNS-19. |
| Publish | F | F | See SNS-10, SNS-11, SNS-20. |
| PublishBatch | F | F | |
| TagResource, UntagResource, ListTagsForResource | F | F | The 50-tag limit is not established. |
| AddPermission, RemovePermission | F | F | |
| PutDataProtectionPolicy, GetDataProtectionPolicy | C | C | Stored, never applied. |
| CheckIfPhoneNumberIsOptedOut, OptInPhoneNumber, ListPhoneNumbersOptedOut, GetSMSAttributes, SetSMSAttributes, GetSMSSandboxAccountStatus, CreateSMSSandboxPhoneNumber, DeleteSMSSandboxPhoneNumber, ListSMSSandboxPhoneNumbers, VerifySMSSandboxPhoneNumber, ListOriginationNumbers (11) | S | S | Declined in `not-built.md`. Answers `UnsupportedOperationException`, 400. Whether that is the code an SDK expects is not established. |
| CreatePlatformApplication, DeletePlatformApplication, ListPlatformApplications, GetPlatformApplicationAttributes, SetPlatformApplicationAttributes, CreatePlatformEndpoint, DeleteEndpoint, GetEndpointAttributes, SetEndpointAttributes, ListEndpointsByPlatformApplication (10) | S | S | Same. |
| DozeMatchSubscriptions | extension | extension | Which subscriptions a message would match, and why not. Not an AWS operation. |

`Publish` to a `PhoneNumber` or a platform `TargetArn` is not a separate
operation: it reaches the topic path and answers `NotFound`. Its target tier
is S, with the stub's code.

**Subscription protocols**

| Protocol | Now | Target | Note |
|---|---|---|---|
| sqs | F | F | Confirmed at once. |
| lambda | F | F | Confirmed at once; asynchronous invoke. Event shape incomplete (SNS-11). |
| http, https | F | F | Pending until the token is handed back. Raw delivery ignored (SNS-10). |
| email, email-json | C | C | Stored, never delivered, pending for ever. |
| sms, application, firehose | C | C | Same. Whether AWS confirms these at once is not established (SNS-23). |

**Topic attributes**

| Attribute | Now | Target | To confirm |
|---|---|---|---|
| FifoTopic | F | F | Fixed at create; name and attribute must agree. |
| ContentBasedDeduplication | F | F | SHA-256 of the message when no dedup id is given. |
| Policy | F | F | Evaluated under soft and enforce. Not validated when stored (SNS-16). |
| `SQS` / `Lambda` / `HTTP` `SuccessFeedbackRoleArn`, `SuccessFeedbackSampleRate`, `FailureFeedbackRoleArn` | F | F | The role gates nothing; setting it is the switch. |
| `Firehose` / `Application` feedback attributes | C | C | Accepted; nothing is delivered, so nothing is logged. |
| DisplayName, DeliveryPolicy | C | C | |
| EffectiveDeliveryPolicy | reported | reported | Always AWS's default document; whether it should reflect a stored `DeliveryPolicy` is not established. |
| KmsMasterKeyId, SignatureVersion, TracingConfig, ArchivePolicy, FifoThroughputScope | C | C | Accepted by `settableTopicAttribute`. `SignatureVersion` has nothing to act on: no notification is signed (SNS-13). |
| Value validation for any of the above | none | ? | Whether AWS refuses a bad value (a sample rate of 500, `ContentBasedDeduplication` on a standard topic) is not established. |

**Subscription attributes**

| Attribute | Now | Target | To confirm |
|---|---|---|---|
| FilterPolicy | F | F | Parsed when stored. AWS's size and combination limits are not enforced (SNS-25). |
| RawMessageDelivery | F for sqs, ignored for http(s) | F | SNS-10. On a lambda subscription: stored; whether AWS refuses it is not established. |
| FilterPolicyScope | C | F or listed | `MessageBody` is stored and matched as attributes. Decide (SNS-18). |
| RedrivePolicy | C | ? | Stored. A subscription dead-letter queue is not implemented; decide, and set the tier. |
| DeliveryPolicy, SubscriptionRoleArn, ReplayPolicy | C | C | |
| Any other name | stored | refused | SNS-19. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `sns/`: 18 tests in 9 files (SDK v2 fan-out, tags, attributes, policy; SDK v1 fan-out and stubs; filter operators; the match preview; delivery status; five conformance fixes) |
| Parity | `rejection_parity_test.go` (1 table test), `coverage_model_test.go`; ledger says 53 of 53 model constraints enforced, `knownGaps` empty |
| Conformance | `conformance/tests/test_sns.py`: 7 scenarios, all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/sns.spec.ts`: 9 tests |
| Restart | `persistence_test.go` at the repo root creates a topic; what else of SNS it holds is not established |
| Mutation | No per-package baseline yet (foundation B3) |

The model types `Name`, `Protocol`, `AttributeName`, `Endpoint` and every
attribute value as bare strings. So the topic-name rule, the protocol set, the
settable-attribute set and every limit in section 5 are hand-derived: `doc`
claims at best.

One thing found while reproducing: the default IAM mode is `soft`. Every SNS to
SQS delivery on a fresh stack logs "would deny sqs:SendMessage … the resource
policy does not allow sns.amazonaws.com" and then delivers. That is correct, and
it means the enforce case in section 9 is one flag away for every user.

## 4. Findings

Evidence `run` means reproduced on 2026-10-10 against a binary built from this
tree, through the `aws` CLI and the console's own routes.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| SNS-1 | P2 | The console cannot create a FIFO topic. The form's `pattern` blocks a period, and `CreateTopic(name)` sends no attributes; posting `name=x.fifo` answers "Invalid parameter: Topic Name". | `internal/console/templates/create.html` `sns_create`; `client_sns.go` `CreateTopic`; `sns/store.go` `CreateTopic` | run | n/a | Handler test: `name=x`, `fifo=on` makes `x.fifo` with `FifoTopic=true` |
| SNS-2 | P2 | The publish form has no group or dedup field. Every console publish to a FIFO topic is refused: "The MessageGroupId parameter is required for FIFO topics". | `templates/sns.html` `sns_topic` (publish tab); `handlers_sns.go` `snsPublish`; `client_sns.go` `Publish`, `PublishBatch` | run | n/a | Handler test publishes to a FIFO topic with a group and reads the queue |
| SNS-3 | P2 | Count above 1 drops the message attributes. `PublishBatch` in the client has no attribute parameter. With a filter `{"k":["v"]}` and the matching attribute, a count of 2 delivered 0 messages and the receipt said "delivered to 1 of 1"; a count of 1 delivered 1. | `client_sns.go` `PublishBatch`; `handlers_sns.go` `snsPublish` | run | n/a | Handler test: batch of 2 with a matching attribute, queue depth 2 |
| SNS-4 | P2 | A batch whose every entry is refused still answers 200 and renders a receipt as if published. | `handlers_sns.go` `snsPublish`, after `PublishBatch` | run | n/a | Handler test: batch to a FIFO topic without a group renders a refusal, not a receipt |
| SNS-5 | P2 | The receipt says "delivered to N of M". It only knows the filter matched. After the queue was deleted, and for a FIFO queue that refuses every send, it still said "delivered to 2 of 2". | `templates/sns.html` `sns_receipt`; `sns/doze_match.go` | run | n/a | Browser test: delete the queue, publish, the receipt must not say delivered |
| SNS-6 | P2 | The receipt's "awaiting confirmation" section cannot fill. The match iterates `subsForTopic` (confirmed only), and `isConfirmed(sub.ARN)` is true for any stored ARN. A topic whose only subscription is pending reads "this topic has no subscribers". | `sns/doze_match.go` `dozeMatchSubscriptions`, `isConfirmed`; `sns/store.go` `subsForTopic` | run | n/a | `doze_match_test.go`: a pending subscription comes back with `Pending` true |
| SNS-7 | P3 | The no-subscribers receipt says "Add a subscription on the right". Subscribers are on another tab. | `templates/sns.html` `sns_receipt` | run | n/a | Browser test asserts the sentence |
| SNS-8 | P2 | Unsubscribe on a pending row toasts "Subscription removed" and removes nothing: the row's ARN is the literal `PendingConfirmation`. The console also throws away the real ARN that `Subscribe` returns (it asks with `ReturnSubscriptionArn=true`). | `templates/sns.html` `sns_subs`; `handlers_sns.go` `subViews`, `snsSubscribe`; `client_sns.go` `Subscribe` | run | n/a | Browser test: subscribe http, unsubscribe, the row is gone |
| SNS-9 | P1 | `Unsubscribe` answers success for `PendingConfirmation`, for a well-formed ARN that does not exist, and for any string. | `sns/store.go` `Unsubscribe`; `sns/actions.go` `unsubscribe` | run | memory: AWS answers `InvalidParameter` for a string that is not a subscription ARN; a missing one is not established | Rejection case for `"PendingConfirmation"`, skipped until recorded |
| SNS-10 | P0 | Raw message delivery is ignored for http and https. A confirmed http subscription with `RawMessageDelivery=true` received the JSON envelope. | `sns/delivery.go` `deliverHTTP` | run | doc: SNS developer guide, "Amazon SNS raw message delivery" (SQS, HTTP/S, Firehose) | SDK test with a recording server: the body is the message, `x-amz-sns-rawdelivery: true` is set |
| SNS-11 | P0 | The Lambda event omits `MessageAttributes`, so a function that reads them sees none. It also lacks `EventVersion`, `SignatureVersion`, `Signature`, `SigningCertUrl`, `UnsubscribeUrl`. | `sns/delivery.go` `deliverLambda` | read | doc: Lambda developer guide, "Using Lambda with Amazon SNS", the example event | Handler test with a fake Lambda peer asserts the record's keys and one attribute |
| SNS-12 | P0 | A FIFO queue can be subscribed to a standard topic. The subscription is accepted and every delivery is then dropped (log: "deliver to qf.fifo: status Bad Request"), while the publish answers success. | `sns/actions.go` `subscribe`; `sns/store.go` `Subscribe`; `sns/delivery.go` `deliverSQS` | run | memory: AWS refuses the subscription with `InvalidParameter` | SDK test expects the refusal; skipped until recorded, with a second test that at least a failure record is written |
| SNS-13 | P1 | The notification envelope (SQS and http) has no `SignatureVersion`, `Signature`, `SigningCertURL` or `UnsubscribeURL`. A consumer that validates the shape, or a library that verifies signatures, fails. | `sns/delivery.go` `snsNotification`, `envelope` | read | doc: SNS developer guide, "HTTP/HTTPS notification JSON format" | SDK test asserts the envelope's key set |
| SNS-14 | P3 | The filter test under a subscription says "a rule with it would fire" and its placeholder is an EventBridge event. | `templates/eb.html` `pattern_builder`, `eb_pattern_verdict` | read | n/a | Browser test on the SNS page asserts the SNS wording |
| SNS-15 | P2 | The Details tab can set only DisplayName, Policy and DeliveryPolicy. `ContentBasedDeduplication` and the delivery-status attributes, which are the only way to see a failed delivery, cannot be set from the console. | `templates/sns.html` `sns_topic` (details tab) | read | n/a | Browser test sets `SQSFailureFeedbackRoleArn` and finds the Failure log group |
| SNS-16 | P1 | `SetTopicAttributes` stores any text as `Policy` ("not json" was accepted and read back), and the guard skips a document it cannot parse. A mistyped Deny is inert. | `sns/actions_ext.go` `setTopicAttributes`; `sns/guard.go` `guardRequest` | run | memory: AWS answers `InvalidParameter` | Attribute test with `not json` and with a statement missing `Effect` |
| SNS-17 | P1 | `ListTopics`, `ListSubscriptions` and `ListSubscriptionsByTopic` return everything and never a `NextToken`. 110 topics came back in one page. | `sns/actions.go` `listTopics`, `subscriptionList` | run | doc: API reference, each returns up to 100 and a `NextToken` | SDK test over 101 topics pages twice |
| SNS-18 | P1 | `FilterPolicyScope: MessageBody` is stored and the policy is matched against attributes, so a body-scoped filter passes messages AWS would drop, and drops ones it would pass. The ledger lists it as a difference; the plan's rule is never more forgiving than AWS. | `sns/delivery.go` `deliver`; `sns/filter.go` `matchFilter` | read (ledger) | doc: SNS developer guide, "Amazon SNS subscription filter policy scope" | SDK test: body-scoped policy, a matching body and a non-matching one |
| SNS-19 | P1 | `SetSubscriptionAttributes` and `Subscribe` accept any attribute name and store it (`Nonsense=x` was accepted). | `sns/store.go` `applySubAttrs` | run | memory: AWS answers `InvalidParameter` | Rejection case, skipped until recorded |
| SNS-20 | P1 | `Publish` accepts a 300 000 byte message, a 150 character subject and a `Number` attribute of `abc`. | `sns/actions.go` `publish`, `publishBatch` | run | doc: API reference `Publish` (256 KB including attributes; subject under 100 characters); the Number rule is memory | Three rejection cases; the Number one skipped until recorded |
| SNS-21 | P1 | `Subscribe` does not look at the endpoint: protocol `sqs` with endpoint `banana` is accepted. | `sns/actions.go` `subscribe` | run | memory: AWS answers `InvalidParameter` for an endpoint that is not an ARN of that service | Rejection cases per protocol, skipped until recorded |
| SNS-22 | P1 | `ConfirmSubscription` ignores `TopicArn`: a token issued for one topic confirmed under another topic's ARN. | `sns/store.go` `ConfirmByToken`; `sns/actions.go` `confirmSubscription` | run | not established | SDK test; expectation set by the recording |
| SNS-23 | P1 | An sms, application or firehose subscription is pending for ever, and so is an email one. Nothing is sent to them, so no token can be handed back. | `sns/store.go` `Subscribe` | run (email) | memory: AWS needs no confirmation for sms, application, firehose | Decide (section 10), then an SDK test of `PendingConfirmation` per protocol |
| SNS-24 | P1 | Re-creating a topic with a different attribute or different tags merges them and answers success. | `sns/store.go` `CreateTopic` | run | memory: AWS answers `InvalidParameter` for differing attributes and for differing tags. The scenario `test_create_again_must_agree` records rather than asserts. | The scenario's four outcomes as an SDK test, skipped until recorded |
| SNS-25 | P1 | Filter policy limits are not enforced: total size, number of keys, number of value combinations. | `sns/actions.go` `validateFilterPolicy` | read | doc: SNS developer guide, "Filter policy constraints" | Rejection cases for each limit |
| SNS-26 | P2 | The Type fact reads `{{if index .Attrs "FifoTopic"}}`, true for any non-empty string. A standard topic created with `FifoTopic=false` shows "FIFO". | `templates/sns.html` `sns_topic` | run | n/a | Render test with `FifoTopic=false` |
| SNS-27 | P3 | The publish panel says "fans out to N subscribers" and counts pending ones. | `templates/sns.html` `sns_topic` (publish tab) | read | n/a | Render test with one pending subscription |
| SNS-28 | P3 | The Details tab labels the settable attributes "stored", and the same list holds `Policy`, which is evaluated. | `templates/sns.html` `sns_topic` (details tab) | read | n/a | Render test: `Policy` is not under a "stored" label |
| SNS-29 | P3 | Stale comments that contradict the code and the ledger: "the only place an orphaned subscription becomes visible" (`handlers_sns.go` `snsTopic`); "C-tier: there is no IAM in front of a local topic" (`client_sns.go` `AddTopicPermission`); "addPermission / removePermission are Tier C … change nothing" (`sns/actions_ext.go`); "Nothing locally evaluates it" (`sns/actions.go` `defaultTopicPolicy`); the `subscription.Protocol` comment naming three protocols (`sns/store.go`). | as named | read | n/a | none; fixed with the code they sit beside |

Owned by the foundation, referenced and not repeated:

- **A2.** The "comes from AWS's own service model" line shows under SNS's
  hand-derived refusals: topic name, filter policy, protocol, a label in use.
- **A5.** Tags are written one call per key (`client_tags.go`, the SNS default
  case), so a refused key leaves the earlier ones written.
- **A7.** `/sns` opens the first topic (`handlers_sns.go` `snsTopics`), so the
  "sns" `Unsel` line in `copy.go` never renders. Reproduced.
- **A8.** `subscriptionList` writes `Owner: "000000000000"` (`sns/actions.go`).
  Add this site to A8's list.
- **A11.** The publish form's CLI preview omits message attributes and the count.
- **A13.** SNS's three confirms were read against the code and are true as far
  as they go; they say nothing of what is lost. Section 6 gives the sentences.

**Moved into this stage from CloudFormation (the topic part of CFN-4, P0, run).**
`AWS::SNS::TopicPolicy` is accepted and dropped, so a template's grant never
reaches the topic, and the policy is evaluated under soft and enforce. It keeps
its id and is closed here. Acceptance: a stack with a topic and a topic policy
leaves that policy on the topic; a policy naming a topic that does not exist
fails the stack. It uses the stored-policy validator from foundation B9.

## 5. What to check

Beyond the common criteria. SNS speaks one protocol, so each line is checked
once, through SDK v2 and boto3.

**Topics**
- [ ] Name: 1 to 256 letters, digits, hyphens, underscores; `.fifo` only as the
      suffix and only with `FifoTopic=true`, and required with it.
- [ ] Re-create: same definition succeeds; different attributes, different
      tags, and no attributes at all each do what the recording says (SNS-24).
- [ ] `DeleteTopic` on a missing topic; on a topic with subscriptions, the
      subscriptions are gone from `ListSubscriptions`.
- [ ] `GetTopicAttributes`: the key set AWS returns for a standard and a FIFO
      topic, including `SubscriptionsConfirmed` and `SubscriptionsPending` live.
- [ ] `SetTopicAttributes`: every name in section 2's table accepted, an unknown
      name refused, `FifoTopic` refused, and each value rule the recording establishes.
- [ ] Paging on `ListTopics` (SNS-17).

**Subscriptions**
- [ ] Protocol set: the nine accepted, anything else `InvalidParameter`.
- [ ] Endpoint shape per protocol (SNS-21), and a FIFO queue on a standard
      topic (SNS-12).
- [ ] The same topic, protocol and endpoint twice is one subscription, with the
      same ARN and refreshed attributes.
- [ ] `ReturnSubscriptionArn` true and false, for a confirmed and a pending one.
- [ ] Attribute names: the settable set accepted, others refused (SNS-19).
- [ ] `ListSubscriptions` and `ListSubscriptionsByTopic`: a pending one reads
      `PendingConfirmation`; `Owner` is the instance's account; paging.
- [ ] `GetSubscriptionAttributes`: the key set AWS returns, including `Owner`,
      `ConfirmationWasAuthenticated`, `PendingConfirmation`.
- [ ] `Unsubscribe`: a real ARN, a missing one, a malformed one, a pending one (SNS-9).

**Confirmation**
- [ ] An http subscription posts `SubscriptionConfirmation` with `Token` and a
      `SubscribeURL` that confirms when fetched.
- [ ] `ConfirmSubscription`: a good token, a wrong one, a used one, a token
      under another topic's ARN (SNS-22), `AuthenticateOnUnsubscribe`.
- [ ] Subscribing a pending endpoint again re-sends the same token.
- [ ] Which protocols start pending (SNS-23).

**Filter policies**
- [ ] Every operator the ledger names: exact, `prefix`, `suffix`,
      `anything-but` (value, list, prefix), `numeric` ranges, `exists` true and
      false, `equals-ignore-case`, `wildcard`, `cidr`, `$or`.
- [ ] Attribute types: `String`, `Number` compared as a number, `String.Array`
      matched by any element, `Binary` never matched.
- [ ] A message with no attributes against a policy; against `exists: false`.
- [ ] A malformed policy refused at `Subscribe` and at `SetSubscriptionAttributes`;
      an empty value clears it.
- [ ] `FilterPolicyScope: MessageBody`, including a nested key (SNS-18).
- [ ] The limits (SNS-25).
- [ ] `DozeMatchSubscriptions` agrees with delivery for every case above, and
      reports pending subscriptions (SNS-6).

**Publish**
- [ ] `TopicArn` and `TargetArn`; a missing topic; a string that is not an ARN.
- [ ] Empty message refused; 256 KB counting attributes; subject length (SNS-20).
- [ ] Message attributes: the type set, `Number` syntax, an empty value, the
      reserved `AWS.` prefix.
- [ ] `MessageStructure: json`: `default` required, per-protocol bodies
      delivered. Whether this is implemented at all is not established.
- [ ] `PublishBatch`: 1 to 10, distinct ids, per-entry attributes and subject,
      per-entry failure in `Failed` without failing the batch, total size.

**Delivery**
- [ ] SQS, envelope: every key of the notification format (SNS-13), attributes
      inside it, subject present only when given.
- [ ] SQS, raw: body is the message, attributes arrive as message attributes,
      at most ten of them.
- [ ] http and https: envelope and raw (SNS-10), the `x-amz-sns-*` headers, a
      non-2xx counted as failed.
- [ ] Lambda: the event record's full key set and `MessageAttributes` (SNS-11).
- [ ] A subscriber that refuses does not fail the publish and does not stop the
      other subscribers.
- [ ] Delivery status: success and failure records for SQS, Lambda and HTTP, the
      sample rate at 0, 50 and 100, the record's shape.

**FIFO**
- [ ] `MessageGroupId` required; a dedup id required unless content-based
      deduplication is on; both messages exact.
- [ ] The publish answers a `SequenceNumber`, increasing.
- [ ] A FIFO queue receives the group and the dedup id; a standard queue on the
      same topic receives neither and still receives the message.
- [ ] The same dedup id twice within five minutes arrives once at each queue.
      Whether the second publish answers the first `MessageId` is not established.
- [ ] Batch entries carry their own group and dedup id.

**Access**
- [ ] A new topic's `Policy` is AWS's default document, with this instance's account.
- [ ] `AddPermission` writes the statement AWS writes; a label in use is
      refused; `RemovePermission` on a missing label is `NotFound`.
- [ ] A policy that cannot be parsed is refused when stored (SNS-16).
- [ ] Under enforce an explicit Deny refuses the root with `AuthorizationError`,
      403; under soft the same call succeeds and is recorded.
- [ ] Which operations the guard covers: those naming `TopicArn`. `Unsubscribe`,
      `SetSubscriptionAttributes`, tag calls and the data-protection calls name
      another parameter; whether the topic policy governs them is not established.

**Stubs and lifecycle**
- [ ] All 21 stubs answer the same code, and a `Publish` to a phone number or a
      platform endpoint answers it too.
- [ ] Topics, attributes, tags, subscriptions, pending tokens and the
      data-protection policy survive a restart.

## 6. Console

The design's "SNS" page: 19 boards. Templates: `templates/sns.html` (`sns_lp`,
`sns_home`, `sns_topic`, `sns_receipt`, `sns_subs`), the `sns_create` block in
`create.html`, `pattern_builder` in `eb.html`, shared pieces in `panes.html` and
`layout.html`. Handlers: `handlers_sns.go`, `handlers_tags.go`; client
`client_sns.go`, `client_tags.go`; routes in `routes_messaging.go`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Sns-Home | Topics, none open | `sns_home`, `empty_detail` | unreachable while a topic exists (foundation A7) |
| Sns-01-None | No topics yet | `sns_home`, `lp_empty`, `empty_detail` | exists |
| Sns-02-Create | Create, with what it makes | `sns_create`; `snsCreateTopic` | form exists; the card is **new** |
| Sns-03-Create-FIFO | FIFO and content-based dedup | `sns_create`; `snsCreateTopic`, `CreateTopic` | **new** (SNS-1) |
| Sns-04-Create-Refused | Name refused | `fail_inline` | exists; today the browser `pattern` stops it first |
| Sns-05-Subscriptions | Subscriptions tab | `sns_topic`, `sns_subs`; `snsTopic` | exists; the filter column is **new** |
| Sns-06-Subs-Empty | No subscribers | `sns_subs` | exists |
| Sns-07-Sub-Settings | Filter policy and raw delivery | `sns_subs` (`sub-cfg`), `pattern_builder`; `snsSubFilter`, `snsSubRaw`, `ebTestPattern` | exists; read-back and payload are **new** |
| Sns-08-Subscribe | Add a subscription | `sns_subs` (the add form); `snsSubscribe` | exists as a row with an advanced panel; the fit check is **new** |
| Sns-09-Subscribe-Refused | Subscribe refused | `fail_inline` | exists |
| Sns-10-Confirm | Confirm a pending subscription | `sns_subs` (confirm panel); `snsConfirm` | exists; "what the endpoint was sent" is **new** |
| Sns-11-Publish | Publish and its receipt | `sns_topic` (publish tab), `attr_editor`, `sns_receipt`; `snsPublish` | exists; pending count and section are **new** |
| Sns-12-Receipts | The four ways to arrive nowhere | `sns_receipt` | cases 1 and 2 exist; 3 is SNS-6; 4 is **new** |
| Sns-13-Publish-FIFO | FIFO publish, refused | `sns_topic` (publish tab); `snsPublish` | refusal exists; the two fields are **new** (SNS-2) |
| Sns-14-Details | Attributes, settable ones, data protection | `sns_topic` (details tab); `snsSetAttribute`, `snsDataProtection` | exists; the "Here" column and extra names are **new** |
| Sns-15-Access | Access policy, all subscriptions | `sns_topic` (details tab); `snsAddPermission`, `snsRemovePermission`, `allSubs` | exists |
| Sns-16-Tags | Tags as a draft | `tags_panel`, `tag_editor`; `tagsSave` | exists (foundation A5) |
| Sns-17-Confirms | Delete topic, unsubscribe, revoke | `hx-confirm` in `sns.html` | exists; the consequence sentences are **new** wording |
| Sns-18-Denied | Publish denied under enforce | `fail_inline` | exists |

**New items, and what each is built from**

| Item | Built from |
|---|---|
| "What this makes": ARN and type as you type | The name field, and the `region` and `account` template functions |
| FIFO and content-based dedup switches | `CreateTopic` attributes `FifoTopic`, `ContentBasedDeduplication`; the suffix rule in `sns/store.go` |
| "What one publish will need" | Static per switch; each sentence is a box in section 5 (FIFO) |
| "Fits the FIFO queue already here" | The queue list (name suffix, `ContentBasedDeduplication`) the console already reads for SQS |
| Filter policy column | `FilterPolicy` from `subscriptionAttributes`, through the existing `patternPreview` |
| "Read back" sentence | The policy JSON, for the shapes the builder can represent; nothing otherwise |
| "What the queue receives" | The envelope's shape. Must come from one place the service also uses, not a second copy in a template; until SNS-13 it shows today's keys |
| "How it fits" on subscribe | The topic's `FifoTopic` and the chosen queue's name. Removed once SNS-12 refuses the pairing; the refusal then says it |
| "What the endpoint was sent" | Static shape from `sendConfirmation`; the token is never shown, because the console does not have it |
| Pending count beside "fans out to", and the awaiting section | The `Pending` flag already on each `subView` |
| "Matched N of M" | `DozeMatchSubscriptions`, unchanged; the wording follows what it can know |
| Group and dedup fields on publish | `Publish` and `PublishBatch` take `MessageGroupId` and `MessageDeduplicationId`; shown only when `FifoTopic` is true |
| "Here" column on Details | The topic-attribute tier table in section 2, through foundation B4 |
| Extra settable names | `settableTopicAttribute` in `sns/actions_ext.go`, exposed as a list |
| Unsubscribe for a pending row | The ARN `Subscribe` returns under `ReturnSubscriptionArn`; it is not in any list call, so it is available only until the page reloads unless the service exposes it. Decide (section 10). |

**Where the teaching lines come from**

| Line | Source |
|---|---|
| Name: 1 to 256, the character set, `.fifo` | `topicName` in `sns/store.go` (hand-derived, `doc`) |
| FIFO fixed at creation | `topicAttributes` omits `FifoTopic` |
| Content-based dedup uses SHA-256 of the message | `fifoFor` in `sns/fifo.go` |
| Group and dedup id go to each FIFO queue; dedup is per queue; five minutes | `deliverSQS`; `dedupWindow` in `sqs/store.go` |
| Filter tests attributes, not the body; fields AND, values OR | `matchFilter`; ledger |
| The operator list | Ledger, "Filter policies share EventBridge's pattern engine" |
| `MessageBody` scope stored, not applied | Ledger, "Differences from AWS". Changes with SNS-18 |
| Raw delivery per protocol | `deliverSQS`, `deliverHTTP`, `deliverLambda`. The http sentence changes with SNS-10 |
| Protocols: confirmed at once, pending, never delivered | `Subscribe` in `sns/store.go`; `snsProtocols`; ledger |
| Same endpoint twice is one subscription | `Subscribe` in `sns/store.go` |
| String as text, Number as a number, Binary as base64 | `attrMatchValue`; `attr_editor` |
| Count 1 to 10 is one `PublishBatch` | `snsPublish` |
| Attribute tiers on Details | Section 2; ledger |
| Tag key 1 to 128, value 0 to 256 | `constraintTables` in `sns/validate.go` (`model`) |
| The default statement grants nothing by itself | The comment and code in `sns/guard.go` |
| Only an explicit Deny refuses the console | `iamguard.Guard.Check` |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Sns-05, Sns-10, Sns-17: "SNS lists a pending subscription with no ARN to
  address." Half true. The list hides it; `Subscribe` returns it. The boards
  drop Unsubscribe from a pending row on that basis and should offer it instead.
- Sns-02: "Creating a name that already exists is not an error: its attributes
  and tags are merged." True of the code today, and SNS-24 says it may be wrong.
- Sns-07: the http raw sentence and the envelope sample describe today's
  defects (SNS-10, SNS-13) and change when those are fixed.
- Sns-08: "delivered as an asynchronous invoke" for Lambda is from
  `peercall.LambdaInvokeAsync`'s name, not a test.
- Sns-10: "an email, sms, application or firehose subscription stays pending"
  is reproduced for email only, and SNS-23 may change it.
- Sns-12, card 4: the IAM-enforce cause was not run under enforce. The other
  two were.
- Sns-13: "any non-empty text" for a group id is what the code checks; AWS's
  rule is not established.
- Sns-14: "`SubscriptionsDeleted` always 0" is from the code; AWS's value is
  not established.
- Sns-16: "tags change nothing about how it delivers" is from reading `delivery.go`.
- Sns-17: the three consequence sentences. Delete (subscriptions go, peers
  untouched, later publishes get `NotFound`) is from `DeleteTopic`; unsubscribe
  and revoke are from `Unsubscribe` and `removePermission`. None has a test.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | Pending in the match (SNS-6); policy validation (SNS-16); attribute names (SNS-19); FIFO queue on a standard topic (SNS-12); token and topic (SNS-22); Lambda record shape with a fake peer (SNS-11) |
| SDK v2 | Paging on three list calls (SNS-17); raw http against a recording server (SNS-10); envelope key set (SNS-13); body-scoped filter (SNS-18); publish limits (SNS-20); re-create outcomes (SNS-24); unsubscribe cases (SNS-9) |
| Rejection parity | Every hand-derived rule in section 5: name, protocol, endpoint, attribute name, filter limits, message size, subject |
| Console (`internal/console`) | FIFO create (SNS-1); FIFO publish (SNS-2); batch carries attributes (SNS-3); refused batch (SNS-4); `FifoTopic=false` (SNS-26); pending count (SNS-27) |
| Restart | Subscriptions with filter and raw flag, a pending token, tags, policy, data-protection policy |
| Conformance (boto3) | The scenarios in section 8 |
| Browser | Each `new` item; the receipt in all four states (SNS-5, SNS-6, SNS-7); unsubscribe a pending row (SNS-8); the SNS wording on the filter test (SNS-14); the feedback attribute to the Failure log group (SNS-15); each confirm's sentence; the denied state under enforce |
| Identity | The package under a non-default account and region (`Owner`, the default policy, log group names) |
| Mutation | `sns/` baseline, then survivors in `delivery.go`, `filter.go`, `fifo.go`, `guard.go`, `store.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today. All 7 existing scenarios are
unverified as well and are recorded in the same pass.

| Claim | Source now | Scenario |
|---|---|---|
| Topic name rule and its error | doc | `test_sns::topic_name_rules` |
| Re-create with different attributes, different tags, none | memory | `create_again_must_agree` (exists) |
| The nine protocols; anything else refused | doc | `subscribe_protocols` |
| Endpoint shape per protocol | memory | `subscribe_endpoint_shapes` |
| A FIFO queue on a standard topic is refused | memory | `fifo_queue_on_standard_topic` |
| Which protocols start pending | memory | `pending_by_protocol` |
| An unknown subscription attribute name is refused | memory | `subscription_attribute_names` |
| `RawMessageDelivery` on a lambda subscription | not established | `raw_delivery_by_protocol` |
| Raw delivery to http: body and headers | doc | `raw_delivery_http` |
| The notification envelope's key set | doc | `envelope_shape` |
| The Lambda event's key set | doc | `lambda_event_shape` |
| `Unsubscribe`: missing, malformed, `PendingConfirmation` | memory | `unsubscribe_cases` |
| `ConfirmSubscription` with another topic's ARN; a used token | not established | `confirm_token_cases` |
| Paging at 100 on the three list calls | doc | `list_paging` |
| Message 256 KB, subject length, `Number` syntax, each code | doc, memory | `publish_limits` |
| Filter policy limits | doc | `filter_policy_limits` |
| `FilterPolicyScope: MessageBody` matching | doc | `filter_policy_body_scope` |
| An invalid `Policy` at `SetTopicAttributes` | memory | `invalid_topic_policy` |
| Value rules for topic attributes; `ContentBasedDeduplication` on a standard topic | not established | `topic_attribute_values` |
| `GetTopicAttributes` and `GetSubscriptionAttributes` key sets; `SubscriptionsDeleted` | not established | `attribute_key_sets` |
| FIFO: a repeated dedup id's answer; group id rules | not established | `fifo_dedup_and_group_rules` |
| `MessageStructure: json` | doc | `message_structure_json` |
| The 50-tag limit and its error | doc | `tag_limit` |
| The code for the SMS and mobile-push operations nobody builds | n/a (ours) | none; the box in section 10 decides it |
| Whether the topic policy governs calls that name a subscription or `ResourceArn` | not established | `policy_scope` |

## 9. Owed to integration

| What | With |
|---|---|
| A FIFO queue subscribed to a standard topic, seen from the queue's side | SQS |
| Under enforce, a queue with no `sns.amazonaws.com` statement refuses the delivery; with one conditioned on `aws:SourceArn` it accepts | SQS, IAM |
| Under enforce, a function with no permission for `sns.amazonaws.com` is not invoked | Lambda, IAM |
| The function receives the full SNS event, attributes included, through a real runtime | Lambda |
| A deleted queue or function: the subscription remains and each delivery fails quietly | SQS, Lambda |
| A bucket notification into a topic needs a `s3.amazonaws.com` statement under enforce | S3, IAM |
| A rule target, an alarm action, a Lambda destination and a Step Functions task each publish, and fan out | EventBridge, CloudWatch, Lambda, Step Functions |
| Delivery-status records land in the Success and Failure log groups | CloudWatch Logs |
| Topic, subscription and topic policy from one template, through CloudFormation, SAM, CDK and Terraform | CloudFormation |
| The wiring graph's `sub` edge appears on both pages and goes when the subscription does | console |
| Fan-out to many subscribers under load; a restart between publish and delivery | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] SNS-1 to SNS-29 each closed, with the test that failed first named beside it.
- [ ] Decided and written into `SUPPORT.md`: body-scoped filters (SNS-18);
      which protocols start pending (SNS-23); subscription `RedrivePolicy`;
      re-create with a different definition (SNS-24); `MessageStructure: json`;
      whether a pending subscription's ARN is exposed to the console (SNS-8).
- [ ] The three tier tables in section 2 have no "?" and no "not established",
      and every C attribute and C protocol reads as cosmetic on the Details tab
      and in the subscribe form.
- [ ] The stubs' error code is checked against what an SDK does with it, and
      the stub row in section 2 says which code and why.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped. This resolves, by name: topic-attribute value
      rules, `EffectiveDeliveryPolicy`, `RawMessageDelivery` on lambda, the
      50-tag limit, the token under another topic, a repeated dedup id's answer,
      `MessageStructure: json`, the policy's scope over subscription and tag
      calls, the two attribute key sets, and what the restart test holds.
- [ ] A publish to a topic reaches SQS, http and Lambda subscribers in the
      shape section 5 names, raw and enveloped, and the three P0s (SNS-10,
      SNS-11, SNS-12) each have a test in the package, not only in integration.
- [ ] All 19 boards implemented from the foundation's shell, and each `new`
      item reads only the data named for it.
- [ ] No statement on an SNS screen is in the "not yet backed" list in section 6.
- [ ] The receipt never says "delivered". Its four states each have a browser test.
- [ ] The envelope shown on the subscription panel and the envelope the service
      sends come from one definition.
- [ ] The console can do, for a FIFO topic, everything it can for a standard
      one: create, publish, batch.
- [ ] The three confirms say what is lost and only what the code does.
- [ ] `e2e/tests/sns.spec.ts` covers every route under `/sns` with the route
      gate on, and the accessibility sweep is clean on all four tabs and create.
- [ ] Conformance: a scenario exists for every row in section 8. Zero
      "answered differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: the three tier tables, the decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for SNS-1 to SNS-28 | `sns/*_test.go`, `internal/console/*_test.go`, `e2e/tests/sns.spec.ts` |
| 3 | Delivery shape: raw http, envelope keys, Lambda record | `sns/delivery.go` |
| 4 | Subscribe and unsubscribe rules: FIFO pairing, endpoint, attribute names, pending by protocol, token and topic | `sns/actions.go`, `sns/store.go` |
| 5 | Policy validation; identity in `Owner` | `sns/actions_ext.go`, `sns/guard.go`, `sns/actions.go` |
| 6 | Paging; publish limits; filter limits and body scope; re-create | `sns/actions.go`, `sns/store.go`, `sns/filter.go`, `internal/eventpattern/` (read only unless body scope needs it) |
| 7 | The match reports pending | `sns/doze_match.go`, `sns/doze_match_test.go` |
| 8 | Section 5 sweep, parity cases | `sns/rejection_parity_test.go`, `sns/sdk_test.go`, `sns/sdk_coverage_test.go`, `sns/testdata/cases_sns.json` |
| 9 | Conformance scenarios and claims table | `conformance/tests/test_sns.py`, the claims file from foundation B1 |
| 10 | Console: FIFO create and publish, batch attributes, receipt, pending rows, Details | `internal/console/templates/sns.html`, `create.html`, `eb.html` (`pattern_builder` wording by caller), `handlers_sns.go`, `client_sns.go` |
| 11 | Browser tests | `e2e/tests/sns.spec.ts` |
| 12 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 and 4 come before the console on purpose: four boards (Sns-07, Sns-08,
Sns-10, Sns-12) carry sentences that describe today's defects and are rewritten
once those are fixed.

**Prior art.** AWS's SNS developer guide is the source for every hand-derived
rule in section 5 (raw delivery, the notification JSON format, filter policy
constraints and scope, FIFO topics), and each rule's test names the page. The
Lambda developer guide's SNS page carries the example event that SNS-11 is
checked against.
