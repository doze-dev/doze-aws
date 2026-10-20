# SNS

Stage 8. Size **L** (was M; see section 1). Follows [../01-protocol.md](../01-protocol.md);
the common acceptance criteria there apply in full, and this page adds SNS's own.

**Reviewed 2026-10-11, before its stage.** Sections 2 to 11 were re-read against
`.audit-models/sns.json`'s own documentation, the code, a running binary and
rendered boards. Corrections are marked in place as *Corrected 2026-10-11*, with
what the line said before and the model's words. New findings are SNS-30 to
SNS-61 (section 4; SNS-61 sits in section 5). The board-beside-page comparison is section 6a. Sources used
below: `model` (the model's documentation or traits), `doc` (an AWS page that
was fetched and read on 2026-10-11, named), `memory` (nothing is fixed on it),
`run` and `read` for doze-aws behaviour.

## Pre-stage review (2026-10-11)

Answers to the twelve questions of [../02-lessons-from-sqs.md](../02-lessons-from-sqs.md).
Read for this review: the `smithy.api#documentation` of all 42 operations and of
every member of the input and output shapes of the 21 implemented ones, every
error shape with its `awsQueryError`, the `length`/`range` traits, the service
traits; the whole `sns/` package; `internal/modelcheck` `FromQuery`;
`internal/console/templates/sns.html`, the `sns_create` block, `handlers_sns.go`,
`client_sns.go`, `resourceurl.go`; `internal/provision/svc_sns.go` and
`internal/cfn/mapper.go`; `docs/SUPPORT.md`'s SNS section. Run: a binary built
from this tree, driven with curl, the aws CLI, a recording webhook and Playwright.

| | Answer |
|---|---|
| **L1** model docs read | **Findings.** Limits and rules in this document were written from memory or the developer guide and the model's text says otherwise or says more: the publish size is the topic's `MaximumMessageSize` (1,024 to 1,048,576, default 262,144) and that attribute is refused by `SetTopicAttributes` (SNS-30); dedup is at the topic, not per queue (SNS-31); `MessageStructure` (SNS-33); standard-topic group ids (SNS-34); group and dedup id lengths (SNS-35); batch id rules (SNS-36); tokens last two days (SNS-38); which protocols confirm (SNS-23, corrected); the tag limit exists as an error shape (SNS-45); `SubscriptionsDeleted` and `EffectiveDeliveryPolicy` (SNS-48); `FifoThroughputScope` is not cosmetic (SNS-31). 31 claims corrected in place and marked, and 7 board texts. |
| **L2** optional parameters | **Findings.** `Publish`'s three alternative targets: `TargetArn` skips the policy under enforce (SNS-32), `PhoneNumber` falls into the topic path (SNS-47); `ReturnSubscriptionArn` read and its answer discarded by the console (SNS-8); `AuthenticateOnUnsubscribe` ignored (SNS-38); `CreateTopic`'s `Attributes`, `DataProtectionPolicy` (dropped, SNS-50) and `Tags`; `GetTopicAttributes`'s "returns `MaximumMessageSize` only if you explicitly set it" (SNS-30, SNS-48); `MessageGroupId` "optional" on a standard topic (SNS-34). `GetSMSAttributes`'s "if you don't use this parameter, returns all": a stub, n/a. |
| **L3** error codes | **Findings.** Tag operations on a missing topic: `NotFound`, model `ResourceNotFound` 404 (SNS-45); `ListSubscriptionsByTopic` on a missing topic answers 200 (SNS-46); batch codes `InvalidBatchEntryId`, `BatchRequestTooLong`, and `EmptyBatchRequest` used for an entry with no `Id` (SNS-36); `ValidationError` is not in SNS's model, `ValidationException` is, on Publish and PublishBatch only (SNS-56); `ParameterValueInvalid` (SNS-53); stubs (SNS-47); `TagLimitExceeded` (SNS-45); `TopicLimitExceeded` (SNS-58). The model's full code and status table is in section 5, "Codes". |
| **L4** direction | Named on every row in section 4. Of the 60 rows: 29 more forgiving than AWS (a success AWS refuses; one of them, SNS-30, is also stricter: a feature AWS accepts is refused), 10 missing (a documented feature absent), 17 wrong (shape, code, a value or a false statement), 4 wording. By severity: P0 5, P1 28, P2 15, P3 12; SNS-30 to SNS-61 add 2, 16, 7 and 7. |
| **L5** every wire | **Checked, one finding.** The model declares `awsQuery` only (no JSON protocol); SNS has one wire, so "both wires" is n/a and says so. xml names: `MessageAttributeMap` key `Name`, value `Value` (the only `xmlName`s); no `xmlFlattened`; lists are `member`, maps are `entry`/`key`/`value` (the topic and subscription attribute maps). All 21 handlers decode through `awsquery` and answer in those names. **The request walk is not whole** (SNS-37, foundation B13): one merged element per list, nondeterministic. No test posts each of the 21 operations spelled as the reference spells them (section 7). |
| **L6** tokens | **Findings.** Confirmation token: topic not checked (SNS-22), no lifetime (SNS-38), single synchronous send (SNS-39); `NextToken` on three lists (SNS-17); subscription ARN of a pending subscription (SNS-8, SNS-9); `SequenceNumber` (SNS-55). A foreign or malformed `NextToken` is `InvalidParameter` (listed, wording memory). |
| **L7** asynchronous | **Findings and decisions** in "L7 decisions" below: delivery (SNS-57), confirmation (SNS-39), retries (SNS-41), subscription dead-letter queue (SNS-42), filter policy propagation, replay and archive (not built, listed). |
| **L8** latency rules | **Findings.** Token validity two days (SNS-38); `StaleTag` (SNS-59); topic and subscription counts (SNS-58); filter policy changes "up to 15 minutes" (`doc`, immediate here, listed, never strict). One switch decided in "L8 decisions". |
| **L9** state | **Checked.** See "L9" below. |
| **L10** boards | **Findings in section 6a.** All 19 boards rendered at 1440 by 900 beside the real page in the same state: 2 match, 3 cannot match yet, 14 differ; 7 boards carried facts the model contradicts and were corrected in the working copy. Dark theme and 900 wide rendered for create, detail and publish: no defect beyond the shell's 1024 notice. |
| **L11** five lines | **Finding.** The SNS console teaches nothing today: the create form is a name input with a `pattern`, the Details tab has one `stored` badge, no field carries a range, a default or enforced/stored. No SNS settings records exist (`internal/settings`). The settings that need one are listed in section 6, "Settings records". |
| **L12** icons | **Checked, one finding.** Rail: icon. Resource header: `awsIcon "sns"`. List pane header: the markup has the icon and `app.css` line 1051 (`.lp-h .aws-ic { display: none; }`) hides it, for every service (SNS-60). Where a board draws `SN`, the page keeps its icon. |

**L7 decisions** (each needs a human yes before the stage; recommended answer first)
1. *Publish fan-out.* Keep it synchronous inside `Publish` (a test that asserts straight after a publish passes here and on AWS, the lesson of SQS-29 in reverse), and bound it: the webhook attempt keeps its timeout but a hung endpoint no longer holds the caller (SNS-57). Listed as a difference.
2. *Confirmation.* Move to a background send with a short retry (SNS-39): `Subscribe` answers at once, the POST is retried for a few seconds so an app that subscribes before it listens still gets its token.
3. *Delivery retries and the subscription dead-letter queue.* Model them minimally: a failed attempt (non-2xx other than 429/5xx counts as permanent; 429 and 5xx retry per the delivery policy, scaled down) and, when the subscription has a `RedrivePolicy`, the message goes to that queue (SNS-41, SNS-42). Recommended; the alternative is C with the difference listed and `RedrivePolicy` refused as "stored, not applied" on the form.
4. *Replay and archive* (`ArchivePolicy`, `ReplayPolicy`, `ReplayStatus`, `BeginningArchiveTime`): C, listed; the model documents them and nothing here replays.

**L8 decisions.** One switch, `--sns-strict` / `[sns] strict = true`, off by default, same shape as `--sqs-strict`: it refuses a confirmation token older than two days (SNS-38) and `StaleTag` (SNS-59). Everything else time-based here is immediate and listed (filter policy propagation, dedup is five minutes and is modelled, throttles at 10 and 30 and 100 TPS are not).

**L9: what persists.** Persists (bolt, application-visible): topics, attributes, tags, subscriptions with filter, raw flag, attributes and pending tokens (plus their creation time once SNS-38 needs it), the data-protection policy, and the FIFO dedup window once SNS-31 moves it to the topic (it is the application's data: a restart inside five minutes must not re-deliver). In memory (the process's): the retry schedule of an in-flight delivery, a confirmation being retried, the delivery-log shipper's buffer (lost on a crash, accepted), nothing else. A restart test today holds only the topic list and a `Policy` (`persistence_test.go`, `persistence_policies_test.go`).

**Classes of mistake this service is most at risk of**
1. **A board that teaches today's defect as the rule.** Five boards and three teaching lines state what the code does where AWS differs ("deduplication per queue", "the body is not matched", "the setting is stored and the envelope still posts", "stays pending"). When the code is fixed the board is wrong, and while it is not, the console lies about AWS. The statements are listed in section 6 and 6a; each is rewritten with its fix.
2. **Delivery shape.** Consumers parse what a subscriber receives (envelope keys, `x-amz-sns-*` headers, the Lambda record). Three shapes are incomplete (SNS-11, SNS-13, SNS-40) and the raw switch does nothing for http (SNS-10).
3. **Silent no-ops.** Inputs accepted and dropped: `CreateTopic`'s attributes in the provision path (SNS-51), `DataProtectionPolicy` (SNS-50), `MessageStructure` (SNS-33), `TargetArn` skipping the policy (SNS-32), a refused batch rendered as a receipt (SNS-4).
4. **A feature refused that AWS accepts**, from limits written from memory: `MaximumMessageSize` (SNS-30), a standard-topic `MessageGroupId` (SNS-34).
5. **The request walk** (SNS-37): unchecked second elements of every list on the one wire this service has.
6. **Cross-service coupling to SQS FIFO and Lambda**: dedup at the topic against standard queues (SNS-31), group ids to fair queues (SNS-34), the Lambda record, a FIFO queue on a standard topic (SNS-12).

## 1. Summary

Standard and FIFO topics over one bbolt store (`sns.bolt`), on the Query/XML
protocol only (`model`: the service declares `awsQuery` and nothing else). A
publish fans out synchronously to every confirmed subscription whose filter
policy matches. Peers: it delivers into SQS and Lambda and posts to http(s)
webhooks; S3 notifications, EventBridge targets, CloudWatch alarms, Lambda
destinations and Step Functions publish into it; CloudFormation and `provision`
create it; delivery-status records go to CloudWatch Logs.

It is small in operations and wide in consequences. *Corrected 2026-10-11: was
"three of its findings change what a subscriber receives"; nine do (SNS-10, 11,
13, 31, 33, 34, 40, 42, 53), and one makes a template undeployable (SNS-51).*

**Estimate:** *Corrected 2026-10-11: was "M, three to four focused days". The
plan listed 29 findings and the model's own text adds 31, two of them P0
(SNS-32, SNS-51) and sixteen P1. SQS's first estimate was low by about a factor
of four for the same reason. **L, ten to twelve focused days:** the API and
validation work 3 days (SNS-30 to 49, 53, 56: topic-level dedup, the
`MaximumMessageSize` coupling, `MessageStructure`, attribute and value
validation, batch rules, the whole-request walk's cases); delivery shape and
asynchrony 2 days (envelope, Lambda record, headers, retries, the subscription
dead-letter queue, background confirmation); provision and CloudFormation 1 day
(SNS-51, the topic policy of CFN-4); the console 4 days (14 boards differ in
shape, the settings records, the five teaching lines on about 25 settings, three
confirms); conformance, mutation and the sweep 1 day.* **Actual:** _fill in when done._

## 2. Contract

The model lists 42 operations (`sns/testdata/ops_sns.json`; counted again from
`.audit-models/sns.json`: 42). 21 are dispatched to real handlers, 21 are
stubs, and there is one extension. *Verified 2026-10-11.*

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateTopic | F | F | *Corrected 2026-10-11: was "re-create with different attributes merges silently". Model: "This action is idempotent, so if the requester already owns a topic with the specified name, that topic's ARN is returned without creating a new topic." That text describes no merge at all; whether differing attributes or tags are refused is not in the model and stays `memory` (SNS-24).* `Attributes` take any name and any value (SNS-44); `DataProtectionPolicy` is dropped (SNS-50); 100,000 standard and 1,000 FIFO topics (`TopicLimitExceeded` 403) are not modelled (SNS-58). |
| DeleteTopic | F | F | Drops the topic's subscriptions. A missing topic answers success. *Verified, `model`: "deleting a topic that does not exist does not result in an error".* |
| ListTopics | F | F | *Corrected 2026-10-11: source was `doc`; `model`: "Each call returns a limited list of topics, up to 100 ... a NextToken is also returned". No `MaxResults`.* Not built. See SNS-17. |
| GetTopicAttributes | F | F | The key set differs from the model's (SNS-48). |
| SetTopicAttributes | C→F | F | Per-attribute tier below. `MaximumMessageSize` is refused (SNS-30). |
| Subscribe | F | F | Per-protocol and per-attribute tier below. See SNS-12, 19, 21, 23, 39, 42, 43. |
| ConfirmSubscription | F | F | Ignores `TopicArn` (SNS-22), no token lifetime (SNS-38). |
| Unsubscribe | F | F | Answers success for anything. See SNS-9. |
| ListSubscriptions, ListSubscriptionsByTopic | F | F | No `NextToken` (SNS-17). `Owner` is a literal (foundation A8). `ByTopic` answers 200 for a missing topic (SNS-46). |
| GetSubscriptionAttributes, SetSubscriptionAttributes | F | F | Per-attribute tier below. See SNS-19, 43, 48. |
| Publish | F | F | See SNS-10, 11, 20, 30 to 35, 53. The three targets: `TopicArn`, `TargetArn`, `PhoneNumber` (SNS-32, SNS-47). |
| PublishBatch | F | F | SNS-3, 4, 36. |
| TagResource, UntagResource, ListTagsForResource | F | F | *Corrected 2026-10-11: was "the 50-tag limit is not established". `model`: error shape `TagLimitExceeded` (400), "Can't add more than 50 tags to a topic", on TagResource, UntagResource and CreateTopic; and TagResource's text, "Adding more than 50 tags to a topic isn't recommended". The two sentences disagree; recorded (`tag_limit`). A missing topic is `ResourceNotFound` 404 (SNS-45).* |
| AddPermission, RemovePermission | F | F | A label in use is refused; the account and action names are not checked (SNS-61 in section 5). |
| PutDataProtectionPolicy, GetDataProtectionPolicy | C | C | Stored, never applied. *`model`: "no longer available to new customers"; `DataProtectionPolicy` max length 30,720 (not enforced, SNS-50).* |
| CheckIfPhoneNumberIsOptedOut, OptInPhoneNumber, ListPhoneNumbersOptedOut, GetSMSAttributes, SetSMSAttributes, GetSMSSandboxAccountStatus, CreateSMSSandboxPhoneNumber, DeleteSMSSandboxPhoneNumber, ListSMSSandboxPhoneNumbers, VerifySMSSandboxPhoneNumber, ListOriginationNumbers (11) | S | S | Declined in `not-built.md`. *Corrected 2026-10-11: was "answers `UnsupportedOperationException`, 400; whether that is the code an SDK expects is not established". Decision D5: HTTP 501 `NotImplemented` with `X-Doze-Not-Built`, through `awshttp.NotBuilt`; none of the 21 operations' error lists names `UnsupportedOperation` (`model`). The stub writes its own 400 today (run, SNS-47).* |
| CreatePlatformApplication, DeletePlatformApplication, ListPlatformApplications, GetPlatformApplicationAttributes, SetPlatformApplicationAttributes, CreatePlatformEndpoint, DeleteEndpoint, GetEndpointAttributes, SetEndpointAttributes, ListEndpointsByPlatformApplication (10) | S | S | Same. |
| DozeMatchSubscriptions | extension | extension | Which subscriptions a message would match, and why not. Not an AWS operation. |

`Publish` to a `PhoneNumber` or a platform `TargetArn` is not a separate
operation: it reaches the topic path and answers `NotFound` (run: "topic does
not exist: " with an empty name). Its target tier is S, with the stub's refusal
(SNS-47). *`model`: Publish's text names all three targets ("a text message (SMS
message) directly to a phone number, or a message to a mobile platform endpoint
(when you specify the TargetArn)").*

**Subscription protocols**

*`model` (Subscribe.Protocol): http, https, email, email-json, sms, sqs,
application, lambda, firehose: nine, with no enum trait.* *`model`
(Subscribe): "If the endpoint type is HTTP/S or email, or if the endpoint and
the topic are not in the same Amazon Web Services account, the endpoint owner
must run the ConfirmSubscription action ... Confirmation tokens are valid for
two days."*

| Protocol | Now | Target | Note |
|---|---|---|---|
| sqs | F | F | Confirmed at once (same account). A queue ARN of another account needs confirmation on AWS (`model`); here one account, so listed. |
| lambda | F | F | Confirmed at once; asynchronous invoke (`doc`, Lambda developer guide "Using Lambda with Amazon SNS": "Amazon SNS invokes your function asynchronously"). Event shape incomplete (SNS-11). |
| http, https | F | F | Pending until the token is handed back (`model`). Raw delivery ignored (SNS-10); headers incomplete (SNS-40). |
| email, email-json | C | C | Pending (`model`: email needs confirmation); stored, never delivered, so pending for ever; correct. |
| sms, application, firehose | C | C | *Corrected 2026-10-11: was "same; whether AWS confirms these at once is not established (SNS-23)". `model`: only HTTP/S, email and cross-account endpoints are confirmed by the owner; these three are confirmed at once. Here they stay pending for ever, which is wrong (SNS-23, now `model`).* Stored, never delivered, listed. |

**Topic attributes**

| Attribute | Now | Target | To confirm |
|---|---|---|---|
| FifoTopic | F | F | Fixed at create (`model`: absent from SetTopicAttributes's list); name must end `.fifo` for a FIFO topic (`model`). That a standard topic may not end `.fifo` is `memory`. |
| ContentBasedDeduplication | F | F | SHA-256 of the body, not the attributes (`model`). FIFO topics only (`model`: "The following attributes apply only to FIFO topics"). |
| **MaximumMessageSize** | **refused** | **F** | *Added 2026-10-11, `model`: 1,024 to 1,048,576, default 262,144; "A topic with a MaximumMessageSize above 256 KiB must have 100 or fewer subscriptions, and each subscription must be an Amazon SQS, Amazon Data Firehose, or Lambda subscription ... If the topic doesn't meet these requirements when you set a value above 256 KiB, Amazon SNS returns an InvalidParameter error." Returned by `GetTopicAttributes` only when set. SNS-30.* |
| FifoThroughputScope | C | **F** | *Corrected 2026-10-11: was C. `model`: "Topic ... the scope of message deduplication is across the entire topic. This is the default ... MessageGroup – The scope of deduplication is within each individual message group". It changes what a subscriber receives (SNS-31). The throughput is not throttled here (listed).* |
| Policy | F | F | Evaluated under soft and enforce. Not validated when stored (SNS-16). Applies to `TargetArn` publishes (SNS-32). |
| `SQS` / `Lambda` / `HTTP` `SuccessFeedbackRoleArn`, `SuccessFeedbackSampleRate`, `FailureFeedbackRoleArn` | F | F | The role gates nothing; setting it is the switch. *`model`: sample rate "percentage (0-100)"; "After you configure the FailureFeedbackRoleArn attribute, then all failed message deliveries generate CloudWatch Logs". The range is not enforced (SNS-44).* |
| `Firehose` / `Application` feedback attributes | C | C | Accepted; nothing is delivered, so nothing is logged. |
| DisplayName, DeliveryPolicy | C | C | DeliveryPolicy: stored and ignored while `EffectiveDeliveryPolicy` reads as if it applied (SNS-41). |
| EffectiveDeliveryPolicy | reported | reported | *Corrected 2026-10-11: was "whether it should reflect a stored DeliveryPolicy is not established". `model`: "The JSON serialization of the effective delivery policy, taking system defaults into account" and, on a subscription, "that takes into account the topic delivery policy and account system defaults". It should reflect the stored policy (SNS-48).* |
| KmsMasterKeyId, TracingConfig, ArchivePolicy | C | C | Accepted by `settableTopicAttribute`. `TracingConfig` "only supported on standard topics" (`model`); the FIFO-only and standard-only rules are not enforced (SNS-44). |
| SignatureVersion | C | C | `model`: "1" default, "2" selects SHA256withRSA. Nothing is signed (SNS-13), so nothing acts on it; the range is `1` or `2` (SNS-44). |
| Value validation for any of the above | none | **F** | *Corrected 2026-10-11: was "whether AWS refuses a bad value is not established". `model` states the ranges and enums (sample rate 0 to 100, `MaximumMessageSize`, `FifoThroughputScope` Topic or MessageGroup, `SignatureVersion`, `TracingConfig` PassThrough or Active, booleans). That the service refuses with `InvalidParameter` is listed for SetTopicAttributes and is the model's only code; wording is `memory` (SNS-44).* |

**Subscription attributes**

*`model` (Subscribe.Attributes, SetSubscriptionAttributes): DeliveryPolicy,
FilterPolicy, FilterPolicyScope, RawMessageDelivery, RedrivePolicy,
SubscriptionRoleArn, and, FIFO only, ReplayPolicy and ReplayStatus. Read-only
on `GetSubscriptionAttributes`: ConfirmationWasAuthenticated,
EffectiveDeliveryPolicy, Owner, PendingConfirmation, SubscriptionArn, TopicArn.*

| Attribute | Now | Target | To confirm |
|---|---|---|---|
| FilterPolicy | F | F | Parsed when stored. *Corrected 2026-10-11: limits were unnumbered. `doc` ("Filter policy constraints", read): at most 5 keys, 150 value combinations, 256 KB, 200 policies per topic and 10,000 per account; wildcard complexity 100 points; numbers -10^9 to 10^9 with five digits. `model`: `FilterPolicyLimitExceeded` 403 is the per-topic and per-account count. None enforced (SNS-25).* |
| RawMessageDelivery | F for sqs, ignored for http(s) | F | SNS-10. *`model`: "enables raw message delivery to Amazon SQS or HTTP/S endpoints"; `doc` ("Amazon SNS raw message delivery", read) adds Firehose. On a lambda subscription it is not named, so refusal is not established.* A value that is not `true` or `false` is read as false (SNS-43). |
| FilterPolicyScope | C | F | `model`: `MessageAttributes` (default) or `MessageBody`. `MessageBody` is stored and matched as attributes (SNS-18). Any other value is accepted (SNS-43). |
| RedrivePolicy | C | **F** | *Corrected 2026-10-11: was "? ... a subscription dead-letter queue is not implemented; decide". `model`: "sends undeliverable messages to the specified Amazon SQS dead-letter queue"; `doc` (dead-letter queues, read): same account and Region, `deadLetterTargetArn`. A documented feature that is absent (SNS-42). Decision in "L7 decisions".* |
| DeliveryPolicy, SubscriptionRoleArn, ReplayPolicy, ReplayStatus | C | C | Stored. `SubscriptionRoleArn` "is required for Firehose delivery stream subscriptions" (`model`; not checked, Firehose is C). |
| Any other name | stored | refused | SNS-19. *The names above are `model`; that anything else is refused with `InvalidParameter` is `memory`.* |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `sns/`: 18 tests in 9 files (SDK v2 fan-out, tags, attributes, policy; SDK v1 fan-out and stubs; filter operators; the match preview; delivery status; five conformance fixes) *Verified*. No test posts each of the 21 operations spelled as the reference spells them. |
| Parity | `rejection_parity_test.go` (1 table test), `coverage_model_test.go`; ledger says 53 of 53 model constraints enforced, `knownGaps` empty. *Read 2026-10-11: every one of the 53 is a `required` member or one of three `length` traits (an ARN 1 to 1,011, a tag key 1 to 128, a tag value 0 to 256). The rest of what the model says is in its prose (section 4, SNS-30 to 61). "53 of 53" is true and is the small part.* |
| Conformance | `conformance/tests/test_sns.py`: 7 scenarios, all **unverified** (`conformance/snapshots/` is empty) *Verified* |
| Browser | `e2e/tests/sns.spec.ts`: 9 tests *Verified* |
| Restart | *Corrected 2026-10-11: was "creates a topic; what else of SNS it holds is not established". `persistence_test.go` lists the topic after a restart; `persistence_policies_test.go` holds the topic `Policy` and a Deny across one. Subscriptions, filter, raw flag, pending token, tags, data-protection policy: bolt holds them and no test says so.* |
| Mutation | A baseline exists for `sns/` in `testdata/mutation.json` (foundation B3, 1e93133); this stage re-records it. *Corrected 2026-10-11: was "no per-package baseline yet".* |

*Corrected 2026-10-11: was "The model types `Name`, `Protocol`, `AttributeName`,
`Endpoint` and every attribute value as bare strings, so the topic-name rule,
the protocol set, the settable-attribute set and every limit in section 5 are
hand-derived: `doc` claims at best." They are bare strings, and the model's
documentation states every one of those rules in words: the name character
set and the 1 to 256 length, the nine protocols, the settable attribute names
per operation, the limits in section 5. Those are `model`. What stays `memory`:
the wording of refusals and the code for a bad value.*

One thing found while reproducing: the default IAM mode is `soft`. Every SNS to
SQS delivery on a fresh stack logs "would deny sqs:SendMessage … the resource
policy does not allow sns.amazonaws.com" and then delivers. That is correct, and
it means the enforce case in section 9 is one flag away for every user.

## 4. Findings

Evidence `run` means reproduced against a binary built from this tree, through
the `aws` CLI, curl, a recording webhook and the console's own routes (on
2026-10-10 for SNS-1 to 29, re-run on 2026-10-11 where marked).

| Id | Sev | What | Where | Evidence | AWS source | Direction | Test to write first |
|---|---|---|---|---|---|---|---|
| SNS-1 | P2 | The console cannot create a FIFO topic. The form's `pattern` blocks a period, and `CreateTopic(name)` sends no attributes; posting `name=x.fifo` answers "Invalid parameter: Topic Name". | `internal/console/templates/create.html` `sns_create`; `client_sns.go` `CreateTopic`; `sns/store.go` `CreateTopic` | run (re-run 2026-10-11) | n/a | missing | Handler test: `name=x`, `fifo=on` makes `x.fifo` with `FifoTopic=true` |
| SNS-2 | P2 | The publish form has no group or dedup field. Every console publish to a FIFO topic is refused: "The MessageGroupId parameter is required for FIFO topics". | `templates/sns.html` `sns_topic` (publish tab); `handlers_sns.go` `snsPublish`; `client_sns.go` `Publish`, `PublishBatch` | run (re-run) | n/a | missing | Handler test publishes to a FIFO topic with a group and reads the queue |
| SNS-3 | P2 | Count above 1 drops the message attributes. `PublishBatch` in the client has no attribute parameter. With a filter `{"k":["v"]}` and the matching attribute, a count of 2 delivered 0 messages and the receipt said "delivered to 1 of 1"; a count of 1 delivered 1. | `client_sns.go` `PublishBatch`; `handlers_sns.go` `snsPublish` | run (re-run: "total is not present") | n/a | wrong | Handler test: batch of 2 with a matching attribute, queue depth 2 |
| SNS-4 | P2 | A batch whose every entry is refused still answers 200 and renders a receipt as if published. | `handlers_sns.go` `snsPublish`, after `PublishBatch` | run; read 2026-10-11 | n/a | wrong | Handler test: batch to a FIFO topic without a group renders a refusal, not a receipt |
| SNS-5 | P2 | The receipt says "delivered to N of M". It only knows the filter matched. After the queue was deleted, and for a FIFO queue that refuses every send, it still said "delivered to 2 of 2". | `templates/sns.html` `sns_receipt`; `sns/doze_match.go` | run (re-run: "✓ Published · delivered to 2 of 3 subscribers") | n/a | wrong | Browser test: delete the queue, publish, the receipt must not say delivered |
| SNS-6 | P2 | The receipt's "awaiting confirmation" section cannot fill. The match iterates `subsForTopic` (confirmed only), and `isConfirmed(sub.ARN)` is true for any stored ARN. A topic whose only subscription is pending reads "this topic has no subscribers". | `sns/doze_match.go` `dozeMatchSubscriptions`, `isConfirmed`; `sns/store.go` `subsForTopic` | run; read | n/a | wrong | `doze_match_test.go`: a pending subscription comes back with `Pending` true |
| SNS-7 | P3 | The no-subscribers receipt says "Add a subscription on the right". Subscribers are on another tab. | `templates/sns.html` `sns_receipt` | run (re-run) | n/a | wording | Browser test asserts the sentence |
| SNS-8 | P2 | Unsubscribe on a pending row toasts "Subscription removed" and removes nothing: the row's ARN is the literal `PendingConfirmation`. The console also throws away the real ARN that `Subscribe` returns (it asks with `ReturnSubscriptionArn=true`). | `templates/sns.html` `sns_subs`; `handlers_sns.go` `subViews`, `snsSubscribe`; `client_sns.go` `Subscribe` | run; read | model: Subscribe.ReturnSubscriptionArn, "the response also includes the pending subscription ARN value for subscriptions that aren't yet confirmed" | wrong | Browser test: subscribe http, unsubscribe, the row is gone |
| SNS-9 | P1 | `Unsubscribe` answers success for `PendingConfirmation`, for a well-formed ARN that does not exist, and for any string. | `sns/store.go` `Unsubscribe`; `sns/actions.go` `unsubscribe` | run (re-run) | *Corrected 2026-10-11: was memory only. `model`: Unsubscribe lists `InvalidParameter` and `NotFound` (404), nothing else about the subscription. Which one a malformed string and a missing ARN each get is `memory`; that a missing subscription is `NotFound` follows from the list.* | more forgiving | Rejection case for `"PendingConfirmation"` and a missing ARN, wording skipped until recorded |
| SNS-10 | P0 | Raw message delivery is ignored for http and https. A confirmed http subscription with `RawMessageDelivery=true` received the JSON envelope. | `sns/delivery.go` `deliverHTTP` | run (re-run with a recording webhook: the body was the envelope, no `x-amz-sns-rawdelivery`) | *`doc`, "Amazon SNS raw message delivery" (read): for HTTP/S, "the HTTP header x-amz-sns-rawdelivery with its value set to true is added", "the message body, client IP, and the required headers are delivered. When you specify message attributes, it won't be sent." `model` names SQS and HTTP/S.* | wrong | SDK test with a recording server: the body is the message, `x-amz-sns-rawdelivery: true` is set, attributes are not sent |
| SNS-11 | P0 | The Lambda event omits `MessageAttributes`, so a function that reads them sees none. It also lacks `EventVersion`, `SignatureVersion`, `Signature`, `SigningCertUrl`, `UnsubscribeUrl`. | `sns/delivery.go` `deliverLambda` | read | *`doc`, Lambda developer guide "Invoking Lambda functions with Amazon SNS" (read): `EventVersion` "1.0", `EventSubscriptionArn`, `EventSource` "aws:sns", `Sns{SignatureVersion, Timestamp, Signature, SigningCertURL, MessageId, Message, MessageAttributes{Type,Value}, Type, UnsubscribeUrl, TopicArn, Subject}`; `Number` and `String.Array` attributes arrive typed `String`. The key spellings `SigningCertURL` and `UnsubscribeUrl` are as the page prints them.* | missing | Handler test with a fake Lambda peer asserts the record's keys and one attribute |
| SNS-12 | P0 | *2026-10-20: the SDK test exists and is skipped (`TestAFIFOQueueCannotBeSubscribedToAStandardTopic`, root package, red: accepted), with its recording scenario `test_sns::test_a_fifo_queue_on_a_standard_topic`; sqs.md SQS-62.* A FIFO queue can be subscribed to a standard topic. The subscription is accepted and every delivery is then dropped (log: "deliver to qf.fifo: status Bad Request"), while the publish answers success. | `sns/actions.go` `subscribe`; `sns/store.go` `Subscribe`; `sns/delivery.go` `deliverSQS` | run (re-run: subscription accepted) | memory: AWS refuses the subscription with `InvalidParameter`. *`doc` (FIFO message grouping, read) confirms the converse: a FIFO topic delivers to standard queues and the group id "is not passed to Amazon SQS standard queues".* The model says nothing of a FIFO queue on a standard topic. | more forgiving | SDK test expects the refusal; skipped until recorded, with a second test that at least a failure record is written |
| SNS-13 | P1 | The notification envelope (SQS and http) has no `SignatureVersion`, `Signature`, `SigningCertURL` or `UnsubscribeURL`. A consumer that validates the shape, or a library that verifies signatures, fails. | `sns/delivery.go` `snsNotification`, `envelope` | read; run (re-run: absent on the wire) | *`doc`, "HTTP/HTTPS notification JSON format" and "Fanout to Amazon SQS" (read): `Type`, `MessageId`, `TopicArn`, `Subject` (absent when none), `Message`, `Timestamp`, `SignatureVersion`, `Signature`, `SigningCertURL`, `UnsubscribeURL`, `MessageAttributes` (absent when none). Signing needs a key: a stable local self-signed certificate and `SigningCertURL` served from the stack; decision (SNS-13 owns it).* | missing | SDK test asserts the envelope's key set |
| SNS-14 | P3 | The filter test under a subscription says "a rule with it would fire" and its placeholder is an EventBridge event. | `templates/eb.html` `pattern_builder`, `eb_pattern_verdict` | read; run (re-run: placeholder `{"source":["orders"],"detail-type":["OrderPlaced"],"detail":{}}`) | n/a | wording | Browser test on the SNS page asserts the SNS wording |
| SNS-15 | P2 | The Details tab can set only DisplayName, Policy and DeliveryPolicy. `ContentBasedDeduplication` and the delivery-status attributes, which are the only way to see a failed delivery, cannot be set from the console. (And `MaximumMessageSize`, SNS-30.) | `templates/sns.html` `sns_topic` (details tab) | read; run (re-run) | n/a | missing | Browser test sets `SQSFailureFeedbackRoleArn` and finds the Failure log group |
| SNS-16 | P1 | `SetTopicAttributes` stores any text as `Policy` ("not json" was accepted and read back), and the guard skips a document it cannot parse. A mistyped Deny is inert. | `sns/actions_ext.go` `setTopicAttributes`; `sns/guard.go` `guardRequest` | run (re-run) | *`model`: SetTopicAttributes lists `InvalidParameter`; the wording is memory.* | more forgiving | Attribute test with `not json` and with a statement missing `Effect` |
| SNS-17 | P1 | `ListTopics`, `ListSubscriptions` and `ListSubscriptionsByTopic` return everything and never a `NextToken`. 110 topics came back in one page. | `sns/actions.go` `listTopics`, `subscriptionList` | run | *Corrected 2026-10-11: was `doc`. `model`: "Each call returns a limited list ... up to 100. If there are more ..., a NextToken is also returned." No `MaxResults` input on these three.* | more forgiving | SDK test over 101 topics pages twice |
| SNS-18 | P1 | `FilterPolicyScope: MessageBody` is stored and the policy is matched against attributes, so a body-scoped filter passes messages AWS would drop, and drops ones it would pass. The ledger lists it as a difference; the plan's rule is never more forgiving than AWS. | `sns/delivery.go` `deliver`; `sns/filter.go` `matchFilter` | read (ledger) | *`model`: "MessageAttributes (default) – The filter is applied on the message attributes. MessageBody – The filter is applied on the message body." `doc` ("filter policy constraints", read): payload-based filtering accepts a nested policy and counts leaf keys toward the limit of five.* | wrong | SDK test: body-scoped policy, a matching body and a non-matching one |
| SNS-19 | P1 | `SetSubscriptionAttributes` and `Subscribe` accept any attribute name and store it (`Nonsense=x` was accepted). | `sns/store.go` `applySubAttrs` | run (re-run) | *The settable set is `model` (list in section 2). Refusal with `InvalidParameter` is `memory`.* | more forgiving | Rejection case, skipped until recorded |
| SNS-20 | P1 | `Publish` accepts a 300 000 byte message, a 150 character subject and a `Number` attribute of `abc`. | `sns/actions.go` `publish`, `publishBatch` | run (re-run: 300 000 and 1 048 577 bytes both 200) | *Corrected 2026-10-11: was "doc: API reference Publish (256 KB including attributes; subject under 100 characters); the Number rule is memory". `model`: "By default, a message can be at most 256 KiB in size (262,144 bytes, not 262,144 characters). When you publish to a topic, the maximum size is determined by the topic's MaximumMessageSize attribute ... Amazon SNS validates the combined size of the message body and message attributes against this value and returns an InvalidParameter error"; Subject: "UTF-8 text with no line breaks or control characters, and less than 100 characters long". The Number rule is now `doc` (SNS-53).* The size is the topic's, not 256 KB (SNS-30). | more forgiving | Rejection cases: body+attributes over 262,144; a subject of 100; a subject with a newline |
| SNS-21 | P1 | `Subscribe` does not look at the endpoint: protocol `sqs` with endpoint `banana` is accepted. | `sns/actions.go` `subscribe` | run (re-run) | *The shapes are `model` (Subscribe.Endpoint: "For the http protocol, the (public) endpoint is a URL beginning with http:// ... For the sqs protocol, the endpoint is the ARN of an Amazon SQS queue ... lambda ... firehose ... application ... sms ... a phone number"). The code `InvalidParameter` is the model's only validation code for Subscribe; wording is memory.* | more forgiving | Rejection cases per protocol, skipped until recorded |
| SNS-22 | P1 | `ConfirmSubscription` ignores `TopicArn`: a token issued for one topic confirmed under another topic's ARN. | `sns/store.go` `ConfirmByToken`; `sns/actions.go` `confirmSubscription` | run; read (re-run) | *`model`: `TopicArn` is a required member ("The ARN of the topic for which you wish to confirm a subscription") and `NotFound` is listed. What a mismatch answers is not established.* | more forgiving | SDK test; expectation set by the recording |
| SNS-23 | P1 | An sms, application or firehose subscription is pending for ever, and so is an email one. Nothing is sent to them, so no token can be handed back. | `sns/store.go` `Subscribe` | run (re-run: sms pending) | *Corrected 2026-10-11: was "memory: AWS needs no confirmation for sms, application, firehose". `model` (Subscribe): "If the endpoint type is HTTP/S or email, or if the endpoint and the topic are not in the same AWS account, the endpoint owner must run ConfirmSubscription". `doc` (creating a subscription, read): "HTTP(S) endpoints, email addresses, and AWS resources in other AWS accounts require confirmation". Email stays pending (correct); sms, application and firehose are confirmed at once (wrong here).* | wrong (stricter: pending for ever) | SDK test of `PendingConfirmation` per protocol: sqs, lambda, sms, application, firehose confirmed at once; http, https, email pending |
| SNS-24 | P1 | Re-creating a topic with a different attribute or different tags merges them and answers success. | `sns/store.go` `CreateTopic` | run | *`model`: "if the requester already owns a topic with the specified name, that topic's ARN is returned without creating a new topic" (no merge). memory: AWS answers `InvalidParameter` for differing attributes and for differing tags. The scenario `test_create_again_must_agree` records rather than asserts.* | more forgiving | The scenario's four outcomes as an SDK test, skipped until recorded; one that asserts the topic is not changed |
| SNS-25 | P1 | Filter policy limits are not enforced: total size, number of keys, number of value combinations. | `sns/actions.go` `validateFilterPolicy` | read | *Corrected 2026-10-11: was "doc: ... Filter policy constraints" without numbers. `doc` (read): 5 keys (leaf keys for a nested body policy), 150 combinations (the product of each array's length), 256 KB, 200 policies per topic, 10,000 per account (exceeding it fails the Subscribe or SetSubscriptionAttributes that attaches the policy), wildcard complexity 100 points and 3 wildcards per pattern, numeric values -10^9 to 10^9 with five digits. `model`: `FilterPolicyLimitExceeded` 403 is the count; the code for an over-complex single policy is `memory` (`InvalidParameter`).* | more forgiving | Rejection cases for each limit |
| SNS-26 | P2 | The Type fact reads `{{if index .Attrs "FifoTopic"}}`, true for any non-empty string. A standard topic created with `FifoTopic=false` shows "FIFO". | `templates/sns.html` `sns_topic` | run (re-run: `Type FIFO` for `FifoTopic=false`) | n/a | wrong | Render test with `FifoTopic=false` |
| SNS-27 | P3 | The publish panel says "fans out to N subscribers" and counts pending ones. | `templates/sns.html` `sns_topic` (publish tab) | run (re-run: "fans out to 4 subscribers", one pending) | n/a | wrong | Render test with one pending subscription |
| SNS-28 | P3 | The Details tab labels the settable attributes "stored", and the same list holds `Policy`, which is evaluated. | `templates/sns.html` `sns_topic` (details tab) | read | n/a | wording | Render test: `Policy` is not under a "stored" label |
| SNS-29 | P3 | Stale comments that contradict the code and the ledger: "the only place an orphaned subscription becomes visible" (`handlers_sns.go` `snsTopic`); "C-tier: there is no IAM in front of a local topic" (`client_sns.go` `AddTopicPermission`); "addPermission / removePermission are Tier C … change nothing" (`sns/actions_ext.go`); "Nothing locally evaluates it" (`sns/actions.go` `defaultTopicPolicy`); the `subscription.Protocol` comment naming three protocols (`sns/store.go`). *Added 2026-10-11: "lambda-until-Phase-6" in `deliver`'s default branch (`sns/delivery.go`); the ledger's "other protocols stored but undeliverable locally" on the `Subscribe` row (it names sms, application and firehose as pending, SNS-23) and "each answers a clean coded error" on the stub row (SNS-47); the package comment lists three delivery paths.* | as named | read | n/a | wording | none; fixed with the code they sit beside |

**Found by reading the model's own documentation (`.audit-models/sns.json`)
and the pages it points at, 2026-10-11.** Every row cites the words it comes
from. None is fixed on `memory`; where a code or wording is `memory` the row says
so and the test is written and skipped until recorded.

| Id | Sev | What | Where | Evidence | Source | Direction | Test to write first |
|---|---|---|---|---|---|---|---|
| SNS-30 | P1 | `MaximumMessageSize` is a documented topic attribute (the large-message feature) and it is **refused**: `SetTopicAttributes` answers "Invalid parameter: AttributeName". `CreateTopic` stores any value (`1` accepted), applies no range, and the publish limit is not enforced at all. The coupling is absent: above 256 KiB the topic may have at most 100 subscriptions, each sqs, firehose or lambda, else `InvalidParameter` (on the Set and on a later Subscribe). A queue's own `MaximumMessageSize` does not apply to what SNS delivers. | `sns/actions_ext.go` `topicAttributes`; `sns/store.go` `CreateTopic`; `sns/actions.go` `publish`, `publishBatch`, `subscribe` | run: Set refused; Create with `1` accepted; 1 048 577 bytes published | model: SetTopicAttributes and CreateTopic.Attributes, "Valid values are 1024 to 1048576 (1 MiB). The default is 262144 (256 KiB). A topic with a MaximumMessageSize above 256 KiB must have 100 or fewer subscriptions, and each subscription must be an Amazon SQS, Amazon Data Firehose, or Lambda subscription"; GetTopicAttributes, "returns this attribute only if you explicitly set it". `doc`, "Publishing large messages" (read): `InvalidParameter` on a publish over the limit; the queue's own size is not applied. | stricter (Set), more forgiving (Create, Publish) | SDK tests: Set 1048576 succeeds; 1023 and 1048577 refused; a 300 000 byte publish succeeds above 262 144 only after the Set, is refused before; an http subscription refuses the Set |
| SNS-31 | P1 | Deduplication on a FIFO topic is **not at the topic**: the dedup id and group go to each subscribed queue and SQS does the rest. A standard queue on a FIFO topic receives every duplicate (run: 2 publishes with one dedup id, standard queue 2, FIFO queue 1); a queue subscribed after the first publish gets the second; the duplicate publish is answered with a new `MessageId` and `SequenceNumber`. `FifoThroughputScope` (Topic, MessageGroup) is C and changes nothing, though it sets the dedup scope. | `sns/fifo.go` `fifoFor`; `sns/delivery.go` `deliverSQS`; `sns/actions_ext.go` | run (2026-10-11) | model: Publish.MessageDeduplicationId, "If a message with a particular MessageDeduplicationId is sent successfully, subsequent messages within the deduplication scope and interval, with the same MessageDeduplicationId, are accepted successfully but aren't delivered"; "The scope of deduplication depends on the FifoThroughputScope attribute, when set to Topic the message deduplication scope is across the entire topic, when set to MessageGroup ... within each individual message group". `doc` ("message deduplication for FIFO topics", read): same. Whether the duplicate answers the first `MessageId` is not established. | more forgiving | SDK test: FIFO topic, one standard and one FIFO queue, same dedup id twice: each queue 1; a scope of MessageGroup with two groups delivers both; the window persists across a restart |
| SNS-32 | P0 | Under IAM enforce, `Publish` with `TargetArn` set to a topic ARN **skips the topic policy**: the guard reads `TopicArn` only, so an explicit Deny on `SNS:Publish` is not applied (`TopicArn` publish: `AuthorizationError`; `TargetArn` publish: 200). | `sns/guard.go` `guardRequest` (`form.Get("TopicArn")`); `sns/actions.go` `publish` (`TargetArn` fallback) | run (enforce instance, 2026-10-11) | model: Publish.TargetArn, "If you don't specify a value for the TargetArn parameter, you must specify a value for the PhoneNumber or TopicArn parameters"; `AuthorizationError` 403 on Publish. (Reading, not a quote: the policy is the topic's, whichever parameter names the topic.) | more forgiving | Enforce test: a Deny on `SNS:Publish`; `TargetArn` publish is refused as the `TopicArn` one is |
| SNS-33 | P1 | `MessageStructure: json` is ignored. A publish with `Message={"default":"D","sqs":"S"}` delivers the whole JSON text to a raw queue; a missing `default`, an unknown value (`xml`) and invalid JSON are all accepted. Per-entry `MessageStructure` on `PublishBatch` the same. | `sns/actions.go` `publish`, `publishBatch`; `sns/delivery.go` | run | model: Publish.MessageStructure and Message, "the value of the Message parameter must: be a syntactically valid JSON object; and contain at least a top-level JSON key of "default" with a value that is a string ... Keys ... that correspond to supported transport protocols must have simple JSON string values ... Non-string values will cause the key to be ignored. Keys that do not correspond to supported transport protocols are ignored. Duplicate keys are not allowed. Failure to parse or validate any key or value ... will cause the Publish call to return an error (no partial delivery)"; "Valid value: json". `doc` ("message attributes", read): "Message attributes are sent only when the message structure is String, not JSON." | wrong, more forgiving | SDK tests: the protocol's own key reaches that protocol and `default` the rest; no `default` refused; a non-object refused; a duplicate key refused |
| SNS-34 | P1 | A standard topic's `MessageGroupId` is accepted and **not forwarded** to its SQS standard subscriptions (run: the queue message carried no group). It is not validated either (SNS-35). | `sns/delivery.go` `deliverSQS` (group only `if fifoFrom`); `sns/fifo.go` | run | model: Publish.MessageGroupId, "Standard topics: The MessageGroupId is optional and is forwarded only to Amazon SQS standard subscriptions to activate fair queues. The MessageGroupId is not used for, or sent to, any other endpoint types ... the same validation rules apply as for FIFO topics" (the same feature as SQS-12) | missing | SDK test: standard topic, standard queue, a group id: the queue message has it; an http subscriber's body does not |
| SNS-35 | P1 | `MessageGroupId` and `MessageDeduplicationId` have no length or character check: 129 characters and a space are accepted on a FIFO topic. | `sns/fifo.go` `fifoFor` | run | model: "can contain up to 128 alphanumeric characters (a-z, A-Z, 0-9) and punctuation (!"#$%&'()*+,-./:;<=>?@[\]^_`{\|}~)" (both members, Publish and PublishBatchRequestEntry). The error code is `InvalidParameter` (listed; wording memory). | more forgiving | Rejection-parity cases for 129 characters and for a space, on Publish and on a batch entry |
| SNS-36 | P1 | `PublishBatch` rules unenforced or mis-coded: an entry `Id` of `a b!` and of 81 characters is accepted (`InvalidBatchEntryId`); a batch over the topic's size (`BatchRequestTooLong`) is not checked; an entry with no `Id` makes `n` 0 and answers `EmptyBatchRequest`; an entry 2 with no `Message` is accepted and published empty (SNS-37). | `sns/actions.go` `publishBatch` | run | model: PublishBatchRequestEntry.Id, "This identifier can have up to 80 characters. The following characters are accepted: alphanumeric characters, hyphens(-), and underscores (_)"; errors `BatchEntryIdsNotDistinct`, `BatchRequestTooLong`, `EmptyBatchRequest`, `InvalidBatchEntryId`, `TooManyEntriesInBatchRequest` (all 400); "The combined size of all messages in the batch, including each message's body and attributes, must not exceed the topic's MaximumMessageSize"; "you should check for batch errors even when the call returns an HTTP status code of 200" | more forgiving, wrong code | Rejection cases: id pattern, 81 characters, entry with no id, no message, a 300 000 byte total |
| SNS-37 | P1 | The model-constraint walk reads each Query list as **one merged element**: `FromQuery` writes every `member.N` into element 0, so the last value written wins and the winner is the map's iteration order. `TagResource` with a 129-character second key was refused in 6 of 8 runs and accepted in 2; `UntagResource` never checked the second key; a batch entry 2 without `Message` passes `PublishBatchRequestEntries[].Message` required. The error path reports `.1.` whatever the entry. Foundation B13 owns the fix (every element, in index order); SNS owns the cases. | `internal/modelcheck/modelcheck.go` `FromQuery`, `insertQuery`; `sns/validate.go` | run (8 runs, 2026-10-11) | model: TagKey `length` 1 to 128, TagValue 0 to 256 on every `Tags` and `TagKeys` element; `Id` and `Message` required on every batch entry | more forgiving | After B13: rejection-parity cases regenerated with the violation in element 2 and 3 only, for `CreateTopic.Tags`, `TagResource.Tags`, `UntagResource.TagKeys`, `PublishBatch` entries and their attributes |
| SNS-38 | P2 | A confirmation token has no lifetime and `AuthenticateOnUnsubscribe` is ignored (`ConfirmationWasAuthenticated` is never reported). A used token answers `NotFound` "invalid confirmation token" 404; the model lists `NotFound` and `InvalidParameter` for the call and does not say which a bad token gets. | `sns/store.go` `Subscribe`, `ConfirmByToken`; `sns/actions.go` `confirmSubscription` | read; run | model: Subscribe, "Confirmation tokens are valid for two days"; ConfirmSubscriptionInput.AuthenticateOnUnsubscribe, "Disallows unauthenticated unsubscribes of the subscription ... then only the topic owner and the subscription owner can unsubscribe"; GetSubscriptionAttributes, "ConfirmationWasAuthenticated – true if the subscription confirmation request was authenticated" | more forgiving | Under `--sns-strict`: a token older than two days is refused (time from the injected clock); the attribute reflects the flag |
| SNS-39 | P1 | The `SubscriptionConfirmation` is posted **once, synchronously, inside `Subscribe`** (5 s timeout). A webhook slower than that stalls the call (run: `Subscribe` took 4 s against a 4 s endpoint) and one that is not listening yet never receives the token; the subscription stays pending until the same endpoint is subscribed again. | `sns/delivery.go` `sendConfirmation`; `sns/actions.go` `subscribe` | run (2026-10-11) | model: Subscribe's answer is `pending confirmation` and the token is "valid for two days", so the call does not wait on the endpoint; that SNS retries the confirmation is `memory`. | wrong | SDK test with an endpoint that starts listening after `Subscribe` returns: it still gets the token; `Subscribe` against a hung endpoint returns promptly |
| SNS-40 | P1 | HTTP(S) deliveries carry `Content-Type` and `x-amz-sns-message-type` only. The three other headers are absent, `UnsubscribeConfirmation` is never sent, and the `SubscriptionConfirmation` has no `MessageId`, `SignatureVersion`, `Signature` or `SigningCertURL`. | `sns/delivery.go` `deliverHTTP`, `sendConfirmation` | run (recording webhook, 2026-10-11) | `doc`, "HTTP/HTTPS headers" and "HTTP/HTTPS notification JSON format" (read): `x-amz-sns-message-type` (SubscriptionConfirmation, Notification, UnsubscribeConfirmation), `x-amz-sns-message-id`, `x-amz-sns-topic-arn`, `x-amz-sns-subscription-arn`, `Content-Type: text/plain; charset=UTF-8`, `User-Agent: Amazon Simple Notification Service Agent`; `model` Unsubscribe: "a final cancellation message is delivered to the endpoint" | missing | SDK test with a recording server asserts the header set and, after an unauthenticated unsubscribe, the final message |
| SNS-41 | P2 | A failed delivery is attempted once. `DeliveryPolicy` is stored and ignored, and `EffectiveDeliveryPolicy` reports AWS's default (`numRetries` 3, 20 s) as though it applied. An http 5xx or 429 is retryable on AWS and every other status is a permanent failure; here both are one failed attempt in the log. | `sns/delivery.go` `deliverHTTP`, `deliverSQS`; `sns/actions.go` `defaultDeliveryPolicy` | read | `doc`, "Amazon SNS message delivery retries" (read): "Amazon SNS considers all 5XX errors and 429 ... as retryable ... All other errors are considered as permanent failures"; the four phases and nine policy attributes, `headerContentType`. `model`: DeliveryPolicy "defines how Amazon SNS retries failed deliveries to HTTP/S endpoints". | more forgiving | Decision first (L7 decisions); then a recording server answering 503 twice then 200: delivered on the third attempt; 400: one attempt |
| SNS-42 | P1 | A subscription's `RedrivePolicy` is stored and never used: a failed delivery (deleted queue, FIFO queue refusing, non-2xx) goes to the log and nowhere else; the value is not validated (`garbage` accepted). | `sns/store.go` `applySubAttrs`; `sns/delivery.go` | run | model: Subscribe.Attributes, "RedrivePolicy – When specified, sends undeliverable messages to the specified Amazon SQS dead-letter queue. Messages that can't be delivered due to client errors ... or server errors ... are held in the dead-letter queue". `doc` (dead-letter queues, read): `deadLetterTargetArn`, same account and Region; a client-side error is not retried. | missing | After the decision: an http subscription with a DLQ and a 400 endpoint: the message reaches the queue; garbage `RedrivePolicy` refused |
| SNS-43 | P1 | Subscription attribute **values** are unchecked, and one silently changes behaviour: `RawMessageDelivery=banana` is accepted and turns raw delivery off (`v == "true"`); `FilterPolicyScope=Banana`, `RedrivePolicy=garbage`, `DeliveryPolicy=garbage` are stored. | `sns/store.go` `applySubAttrs`; `sns/actions.go` `setSubscriptionAttributes` | run | model: FilterPolicyScope `MessageAttributes` (default) or `MessageBody`; RawMessageDelivery "When set to true, enables"; `InvalidParameter` is listed on both calls. | more forgiving | Rejection cases per attribute, wording skipped until recorded |
| SNS-44 | P1 | Topic attribute values are unchecked on both paths, and `CreateTopic` checks nothing at all (an unknown name and `MaximumMessageSize=1` accepted): `HTTPSuccessFeedbackSampleRate=500` (the delivery log then silently samples at 100), `TracingConfig=banana`, `FifoThroughputScope=banana`, `SignatureVersion=7`, `ContentBasedDeduplication=true` on a standard topic, a 300-character `DisplayName` are accepted. | `sns/actions_ext.go` `setTopicAttributes`; `sns/store.go` `CreateTopic`; `sns/deliverylog.go` | run | model: "sample rate percentage (0-100)"; `FifoThroughputScope` "Topic ... MessageGroup"; `TracingConfig` "PassThrough ... Active ... only supported on standard topics"; `SignatureVersion` "1 ... 2"; "The following attributes apply only to FIFO topics: ArchivePolicy, ContentBasedDeduplication, FifoThroughputScope". Code `InvalidParameter` listed; wording memory. `DisplayName` length is not in the model. | more forgiving | Rejection cases per attribute on `CreateTopic` and `SetTopicAttributes`; the FIFO-only names refused on a standard topic skipped until recorded |
| SNS-45 | P1 | Tag operations on a missing topic answer `NotFound`; the model's code is `ResourceNotFound` (404), a different shape. The 50-tag limit is not enforced (60 tags accepted), and the model both lists `TagLimitExceeded` and calls more than 50 "not recommended". | `sns/actions_ext.go` `tagResource`, `untagResource`, `listTagsForResource` | run | model: TagResource/UntagResource/ListTagsForResource errors `ResourceNotFound` (404), `TagLimitExceeded` ("Can't add more than 50 tags to a topic"), `ConcurrentAccess`, `StaleTag`, `TagPolicy`; TagResource: "Adding more than 50 tags to a topic isn't recommended" | wrong code; more forgiving (or not, pending the recording) | Rejection case for a missing topic (code and 404); the 51st tag skipped until recorded (`tag_limit`) |
| SNS-46 | P1 | `ListSubscriptionsByTopic` for a topic that does not exist answers 200 with an empty list. | `sns/actions.go` `listSubscriptionsByTopic`, `subscriptionList` | run | model: ListSubscriptionsByTopic errors include `NotFound` (404) | more forgiving | SDK test: missing topic is `NotFound` |
| SNS-47 | P1 | The 21 stubs answer `UnsupportedOperationException` with HTTP 400 and no `X-Doze-Not-Built` header (decision D5: 501 `NotImplemented`, the header, through `awshttp.NotBuilt`). `Publish` with a `PhoneNumber` or an endpoint `TargetArn` answers `NotFound` "topic does not exist: ". | `sns/actions_ext.go` `stubHandler`; `sns/actions.go` `publish` | run (2026-10-11) | model: no stubbed operation's error list names `UnsupportedOperation`; decision D5 | wrong code | The foundation's generic test (B6) over `ops_sns.json`; a `Publish` to a phone number answers the refusal |
| SNS-48 | P2 | Attribute key sets differ from the model's. `GetSubscriptionAttributes` lacks `Owner`, `ConfirmationWasAuthenticated`, `EffectiveDeliveryPolicy` and reports `FilterPolicy` as an empty string when none. `GetTopicAttributes` reports `SubscriptionsDeleted` as a constant 0 (an unsubscribe leaves 0, run) and an `EffectiveDeliveryPolicy` that ignores a stored `DeliveryPolicy`. | `sns/actions.go` `getSubscriptionAttributes`, `getTopicAttributes` | run (2026-10-11) | model: GetSubscriptionAttributes lists ConfirmationWasAuthenticated, DeliveryPolicy, EffectiveDeliveryPolicy, FilterPolicy, FilterPolicyScope, Owner, PendingConfirmation, RawMessageDelivery, RedrivePolicy, SubscriptionArn, TopicArn; GetTopicAttributes, "SubscriptionsDeleted – The number of deleted subscriptions for the topic" | wrong shape | SDK test of the key sets for a standard and a FIFO topic and for a confirmed and a pending subscription; delete count after an unsubscribe |
| SNS-49 | P3 | The topic-name pattern is `^[A-Za-z0-9_-]{1,256}(\.fifo)?$`: a FIFO name may be 261 characters. | `sns/store.go` `topicName` | run (256 + `.fifo` accepted) | model: CreateTopic.Name, "must be between 1 and 256 characters long. For a FIFO (first-in-first-out) topic, the name must end with the .fifo suffix"; whether the suffix counts is the model's reading of "the name" and the same as SQS-3 | more forgiving | Rejection case for a 257-character FIFO name |
| SNS-50 | P2 | `CreateTopic`'s `DataProtectionPolicy` is silently dropped (`GetDataProtectionPolicy` answers empty), and `PutDataProtectionPolicy` has no 30,720 limit (40 000 characters accepted). | `sns/actions.go` `createTopic`; `sns/actions_ext.go` `putDataProtectionPolicy` | run | model: CreateTopic.DataProtectionPolicy and PutDataProtectionPolicyInput, "Maximum length of 30,720"; "no longer available to new customers" | silent no-op; more forgiving | SDK test: create with a policy, get it back; 30,721 refused |
| SNS-51 | P0 | **A template cannot create a FIFO topic.** `provision.Topic` holds only `Subscriptions` and `Tags`; `applyTopics` calls `CreateTopic` with a name and no attributes, so `orders.fifo` with `FifoTopic: true` is refused "Invalid parameter: Topic Name" and the apply stops. `DisplayName`, `KmsMasterKeyId`, `ContentBasedDeduplication`, `DeliveryPolicy`, `TracingConfig` and every subscription attribute beyond filter and raw are dropped; `AWS::SNS::Subscription` with email, sms, application or firehose is refused ("doze-aws supports sqs, lambda and http(s) subscriptions"). | `internal/provision/model.go` `Topic`, `Subscription`; `internal/provision/svc_sns.go` `applyTopics`; `internal/cfn/mapper.go` `topic`, `subscriptionFrom` | run (`doze-aws apply` of a FIFO template against a running instance, 2026-10-11) | model: CreateTopic.Attributes; protocols (Subscribe) | missing | Provision and CloudFormation tests: a FIFO topic with content-based deduplication, a subscription with `FilterPolicyScope` and `RedrivePolicy`, an email subscription |
| SNS-52 | P2 | The console shows an http subscription as an SQS queue: `resourceFromARN` treats any `http(s)://` string as a queue URL, so `http://localhost:9000/hooks/orders` renders as "orders", a link to `/sqs/orders`, and a chip in the wiring strip. | `internal/console/resourceurl.go` `resourceFromARN`; `handlers_sns.go` `subViews` | run (2026-10-11) | n/a | wrong | Handler test: an http subscription's row has no link and no strip chip |
| SNS-53 | P1 | Message attribute rules are unchecked: type `Banana`, an empty value, `Number` `abc`, an attribute name of 300 characters or starting `AWS.`, and more than 10 attributes to a raw SQS subscription are accepted. A `Number` or `String.Array` attribute reaches a Lambda typed `String` on AWS; here the type is passed through. | `sns/actions.go` `messageAttributes`, `publish`, `publishBatch`; `sns/delivery.go` | run (type, value, Number) | model: MessageAttributeValue, "Name, type, and value must not be empty or null"; `InvalidParameterValueException` code `ParameterValueInvalid`, "The parameter of an entry in a request doesn't abide by the specification" (Publish and PublishBatch). `doc` ("message attributes" and "raw message delivery", read): name A-Z a-z 0-9 _ - . up to 256, no leading, trailing or doubled period, no `AWS.` or `Amazon.` prefix in any casing; types String, String.Array, Number, Binary; Number -10^9 to 10^9, five digits; "a maximum of 10 message attributes can be sent" to a raw SQS subscription, more are "discarded as client side errors". Which code each gets (`InvalidParameter` or `ParameterValueInvalid`) is memory. | more forgiving | Rejection cases per rule on Publish and on a batch entry; the Lambda record's type for a Number |
| SNS-54 | P3 | The console's attribute editor offers String, Number and Binary; `String.Array` (model and doc) cannot be sent from it. | `templates/layout.html` `attr_editor` | read | model: "Amazon SNS supports the following logical data types: String, String.Array, Number, and Binary" | missing | Browser test sends a `String.Array` and a filter matches an element |
| SNS-55 | P3 | `SequenceNumber` is 20 digits taken from the clock; the model says 128 bits. | `sns/fifo.go` `sequenceNumber` | run | model: PublishResponse.SequenceNumber, "The length of SequenceNumber is 128 bits. SequenceNumber continues to increase for each MessageGroupId" | wrong shape | Test: increasing within a group, 38 digits |
| SNS-56 | P2 | Model-constraint refusals answer code `ValidationError`. SNS's model has no `ValidationError`: its `ValidationException` (code `ValidationException`) is on Publish and PublishBatch only, and every other operation lists `InvalidParameter`. The message path always reads `.1.` (SNS-37). | `internal/modelcheck` `CodeQuery`; `sns/sns.go` `ServeHTTP` | run | model: error lists per operation; `ValidationException` "Indicates that a parameter in the request is invalid". What AWS answers for a tag key of 129 characters is not established. | wrong code (probable) | Recording `tag_key_too_long`, `publish_missing_message`; until then the rejection-parity test asserts the code the model lists |
| SNS-57 | P3 | Fan-out is sequential and synchronous: a webhook that hangs costs the publisher up to 5 s per publish per such subscriber; several slow subscribers add. | `sns/delivery.go` `deliver`, `deliverHTTP` (`httpDeliveryClient`, 5 s) | read; run (Subscribe, SNS-39) | model: "When a messageId is returned, the message is saved and Amazon SNS immediately delivers it to subscribers" | more forgiving (to ordering and latency, not to content) | Decision (L7 decisions); a publish against a hung webhook returns inside the bound |
| SNS-58 | P3 | `TopicLimitExceeded` (100,000 standard and 1,000 FIFO topics, 403) and `SubscriptionLimitExceeded` (403) are not modelled. Decision: not modelled and listed; no local test reaches 100,000. | n/a | read | model: CreateTopic, "Users can create at most 100,000 standard topics (at most 1,000 FIFO topics)"; error shapes. The subscription number is not in the model. | more forgiving | none; the ledger lists it |
| SNS-59 | P3 | `StaleTag` ("A tag has been added to a resource with the same ARN as a deleted resource. Wait a short while and then retry") is the SNS form of SQS's `QueueDeletedRecently`; not modelled. Decision: opt-in under `--sns-strict`, off by default (L8). | `sns/store.go` | read | model: StaleTagException on CreateTopic, DeleteTopic, TagResource, UntagResource | more forgiving, by default | `sns/strict_test.go`: delete, re-create, tag at once is `StaleTag` under strict only |
| SNS-60 | P3 | The list pane header's icon is in the markup and hidden: `.lp-h .aws-ic { display: none; }`. L12 keeps the icon in the rail, the list pane header and the resource header. Shell-wide; one rule. | `internal/console/static/app.css` line 1051 | run (`<img class="aws-ic" src=".../sns.svg">` present, not drawn) | n/a | wrong | Browser test: the list pane header's icon is visible on SNS and SQS |

Owned by the foundation, referenced and not repeated:

- **A2.** The "comes from AWS's own service model" line shows under SNS's
  hand-derived refusals: topic name, filter policy, protocol, a label in use.
  *Re-read 2026-10-11: the topic-name rule and the batch rules are the model's
  documentation, and the line is true of them; the filter-policy message
  ("invalid FilterPolicy: field ...") and the label-in-use refusal are doze's
  wording and the line is not.*
- **A5.** Tags are written one call per key (`client_tags.go`, the SNS default
  case), so a refused key leaves the earlier ones written.
- **A7.** `/sns` opens the first topic (`handlers_sns.go` `snsTopics`), so the
  "sns" `Unsel` line in `copy.go` never renders. Reproduced.
- **A8.** `subscriptionList` writes `Owner: "000000000000"` (`sns/actions.go`).
  Add this site to A8's list.
- **A11.** The publish form's CLI preview omits message attributes and the count.
- **A13.** SNS's three confirms were read against the code and are true as far
  as they go; they say nothing of what is lost. Section 6 gives the sentences.
  *Checked on the rendered page 2026-10-11: delete topic and unsubscribe have no
  body and the buttons read "Cancel" and "Delete"; revoke has a one-sentence body.*
- **B13.** `FromQuery` keeps one element of each list (SNS-37). Lands before
  this stage.

**Moved into this stage from CloudFormation (the topic part of CFN-4, P0, run).**
`AWS::SNS::TopicPolicy` is accepted and dropped, so a template's grant never
reaches the topic, and the policy is evaluated under soft and enforce. It keeps
its id and is closed here. *Read again 2026-10-11: `internal/cfn/mapper.go`
lists it with `AWS::Lambda::Permission` and `AWS::Events::EventBus` as
"recognised and namable — they carry no stack-file state of their own", and
`resources.go` maps it to no physical name.* Acceptance: a stack with a topic
and a topic policy leaves that policy on the topic; a policy naming a topic
that does not exist fails the stack. It uses the stored-policy validator from
foundation B9. SNS-51 (a template cannot create a FIFO topic) rides with it:
same files, same stage.

## 5. What to check

Beyond the common criteria. SNS speaks one protocol (`model`: `awsQuery`), so
each line is checked once, through SDK v2 and boto3. *Corrected 2026-10-11: the
numbers and rules below were first written from memory; the corrections are in
place and SNS-30 to SNS-60 in section 4.*

**Codes** (`model`, every error shape with its `awsQueryError`)

| Code | Status | Where it belongs |
|---|---|---|
| `InvalidParameter` | 400 | The model's general refusal; listed on every implemented operation |
| `ParameterValueInvalid` (`InvalidParameterValueException`) | 400 | Publish, PublishBatch: "The parameter of an entry in a request doesn't abide by the specification" |
| `NotFound` | 404 | Topic or subscription missing; not the tag operations |
| `ResourceNotFound` | 404 | TagResource, UntagResource, ListTagsForResource (SNS-45) |
| `AuthorizationError` | 403 | A policy Deny |
| `InvalidSecurity` | 403 | "must use an HTTPS endpoint and sign your request using Signature Version 4"; not applicable locally |
| `TopicLimitExceeded`, `SubscriptionLimitExceeded`, `FilterPolicyLimitExceeded`, `ReplayLimitExceeded` | 403 | Counts; the third is the per-topic and per-account filter policy count (SNS-25, SNS-58) |
| `TagLimitExceeded`, `TagPolicy`, `StaleTag`, `ConcurrentAccess` | 400 | Tags (SNS-45, SNS-59) |
| `BatchEntryIdsNotDistinct`, `BatchRequestTooLong`, `EmptyBatchRequest`, `InvalidBatchEntryId`, `TooManyEntriesInBatchRequest` | 400 | PublishBatch (SNS-36) |
| `ValidationException` | 400 | Publish, PublishBatch only (SNS-56) |
| `InternalError` | 500 | |
| `EndpointDisabled`, `PlatformApplicationDisabled`, `KMS*`, `OptedOut`, `Throttled` (429), `UserError`, `InvalidState` | 400 and others | Mobile push, SMS, KMS: not reachable here |

**Topics**
- [ ] Name: 1 to 256 letters, digits, hyphens, underscores; `.fifo` only as the
      suffix and only with `FifoTopic=true`, and required with it. *Corrected
      2026-10-11: the 256 counts the suffix (SNS-49).*
- [ ] Re-create: same definition returns the same ARN and creates nothing
      (`model`); different attributes, different tags, and no attributes at all
      each do what the recording says (SNS-24).
- [ ] `DeleteTopic` on a missing topic succeeds (`model`); on a topic with
      subscriptions, the subscriptions are gone from `ListSubscriptions`.
- [ ] `GetTopicAttributes`: the key set the model lists for a standard and a
      FIFO topic, including `SubscriptionsConfirmed`, `SubscriptionsPending` and
      `SubscriptionsDeleted` live, `MaximumMessageSize` only when set (SNS-48).
- [ ] `SetTopicAttributes`: every name in section 2's table accepted, an unknown
      name refused, `FifoTopic` refused, `MaximumMessageSize` 1,024 to 1,048,576
      with its subscription coupling, and each value rule in SNS-44.
- [ ] `CreateTopic` applies the same attribute rules as `SetTopicAttributes`,
      and `DataProtectionPolicy` is kept (SNS-50).
- [ ] Paging on `ListTopics` (SNS-17).

**Subscriptions**
- [ ] Protocol set: the nine accepted, anything else `InvalidParameter`.
- [ ] Endpoint shape per protocol (SNS-21), and a FIFO queue on a standard
      topic (SNS-12).
- [ ] The same topic, protocol and endpoint twice is one subscription, with the
      same ARN and refreshed attributes (`memory`: the model is silent).
- [ ] `ReturnSubscriptionArn` true and false, for a confirmed and a pending one;
      the pending one answers the string `pending confirmation` when false
      (`model`).
- [ ] Attribute names: the settable set accepted, others refused (SNS-19);
      values (SNS-43).
- [ ] `ListSubscriptions` and `ListSubscriptionsByTopic`: a pending one reads
      `PendingConfirmation`; `Owner` is the instance's account; paging; a missing
      topic is `NotFound` (SNS-46).
- [ ] `GetSubscriptionAttributes`: the key set the model lists, including
      `Owner`, `ConfirmationWasAuthenticated`, `PendingConfirmation` (SNS-48).
- [ ] `Unsubscribe`: a real ARN, a missing one, a malformed one, a pending one (SNS-9).

**Confirmation**
- [ ] An http subscription posts `SubscriptionConfirmation` with `Token` and a
      `SubscribeURL` that confirms when fetched (`doc`; `GET` of the URL answers
      200 here).
- [ ] The post does not hold `Subscribe`, and is retried (SNS-39).
- [ ] `ConfirmSubscription`: a good token, a wrong one, a used one, a token
      under another topic's ARN (SNS-22), a token older than two days under
      strict (SNS-38), `AuthenticateOnUnsubscribe`.
- [ ] Subscribing a pending endpoint again re-sends the same token.
- [ ] Which protocols start pending: http, https, email (SNS-23).

**Filter policies**
- [ ] Every operator the ledger names: exact, `prefix`, `suffix`,
      `anything-but` (value, list, prefix), `numeric` ranges, `exists` true and
      false, `equals-ignore-case`, `wildcard`, `cidr`, `$or`. (`doc`, the filter
      policy page lists: AND, OR, key matching, numeric exact, anything-but and
      range, string exact, anything-but, prefix-with-anything-but,
      equals-ignore-case, IP address, prefix, suffix.)
- [ ] Attribute types: `String`, `Number` compared as a number, `String.Array`
      matched by any element, `Binary` never matched (`doc`: "Amazon SNS ignores
      message attributes with the Binary data type").
- [ ] A message with no attributes against a policy; against `exists: false`.
- [ ] A malformed policy refused at `Subscribe` and at `SetSubscriptionAttributes`;
      an empty value clears it.
- [ ] `FilterPolicyScope: MessageBody`, including a nested key (SNS-18).
- [ ] The limits (SNS-25): 5 keys, 150 combinations, 256 KB, 200 per topic.
- [ ] `DozeMatchSubscriptions` agrees with delivery for every case above, and
      reports pending subscriptions (SNS-6).

**Publish**
- [ ] `TopicArn` and `TargetArn`; a missing topic is `NotFound`; none of
      `TopicArn`, `TargetArn`, `PhoneNumber` is `InvalidParameter` (memory); a
      string that is not an ARN.
- [ ] Empty message refused; 262,144 bytes by default counting body and
      attributes, the topic's `MaximumMessageSize` otherwise; subject under 100
      characters with no line break (SNS-20, SNS-30). *Corrected 2026-10-11: was
      "256 KB".*
- [ ] Message attributes: the type set, `Number` syntax and range, an empty
      value, the reserved `AWS.` and `Amazon.` prefixes, name length (SNS-53).
- [ ] `MessageStructure: json`: `default` required, per-protocol bodies
      delivered, a non-string value ignored, a duplicate key refused (SNS-33).
      *Corrected 2026-10-11: was "whether this is implemented at all is not
      established"; it is not, and the model states the rules.*
- [ ] `PublishBatch`: 1 to 10, distinct ids of at most 80 characters, per-entry
      attributes and subject, per-entry failure in `Failed` (`Id`, `Code`,
      `Message`, `SenderFault`) without failing the batch, total size
      (SNS-36).

**Delivery**
- [ ] SQS, envelope: every key of the notification format (SNS-13), attributes
      inside it, subject present only when given.
- [ ] SQS, raw: body is the message, attributes arrive as message attributes,
      at most ten of them (`doc`).
- [ ] http and https: envelope and raw (SNS-10), the four `x-amz-sns-*`
      headers (SNS-40), a 5xx or 429 retried and anything else a permanent
      failure (SNS-41).
- [ ] Lambda: the event record's full key set and `MessageAttributes` (SNS-11),
      `Number` and `String.Array` typed `String`.
- [ ] A subscriber that refuses does not fail the publish and does not stop the
      other subscribers, and goes to the subscription's dead-letter queue when
      it has one (SNS-42).
- [ ] Delivery status: success and failure records for SQS, Lambda and HTTP, the
      sample rate at 0, 50 and 100, the record's shape.

**FIFO**
- [ ] `MessageGroupId` required, up to 128 characters (SNS-35); a dedup id
      required unless content-based deduplication is on; both messages exact.
- [ ] The publish answers a `SequenceNumber`, increasing within a group
      (`model`: 128 bits, SNS-55).
- [ ] A FIFO queue receives the group and the dedup id; a standard queue on the
      same topic receives neither and still receives the message (`doc`: "Message
      group ID is not passed to Amazon SQS standard queues").
- [ ] *Corrected 2026-10-11: was "the same dedup id twice within five minutes
      arrives once at each queue". `model`: the topic accepts the second publish
      and delivers it to no subscriber, within the scope `FifoThroughputScope`
      names (SNS-31).* Whether the second publish answers the first `MessageId`
      is not established.
- [ ] Batch entries carry their own group and dedup id.
- [ ] On a standard topic a group id is optional, validated as on a FIFO topic,
      and forwarded to SQS standard subscriptions only (SNS-34).

**Access**
- [ ] A new topic's `Policy` is AWS's default document, with this instance's
      account (`memory` for the document's shape; `model`: "By default, only the
      topic owner can publish or subscribe to the topic").
- [ ] `AddPermission` writes the statement AWS writes; a label in use is
      refused; account ids and action names are checked (`model`: "Any Amazon
      SNS action name, for example Publish"; accounts "must have Amazon Web
      Services account"; the check is `memory`, SNS-61 below);
      `RemovePermission` on a missing label: the model lists `NotFound` and
      `InvalidParameter` and does not say which.
- [ ] A policy that cannot be parsed is refused when stored (SNS-16).
- [ ] Under enforce an explicit Deny refuses the root with `AuthorizationError`,
      403 (`model`); under soft the same call succeeds and is recorded. A
      `TargetArn` publish is governed too (SNS-32).
- [ ] Which operations the guard covers: those naming `TopicArn`. `Unsubscribe`,
      `SetSubscriptionAttributes`, tag calls and the data-protection calls name
      another parameter; whether the topic policy governs them is not
      established. *Checked 2026-10-11: under enforce `TagResource` and
      `Subscribe` pass a Deny on `SNS:Publish`, as they should; no Deny on them
      was tried.*

*SNS-61, P2, more forgiving, run: `AddPermission` accepts `AWSAccountId=banana`
and `ActionName=Frobnicate`. `memory` for the refusal; the SQS model rules
(twelve digits, a named action) are the precedent. Test skipped until recorded.*

**Stubs and lifecycle**
- [ ] All 21 stubs answer the same refusal (501 `NotImplemented`, the header),
      and a `Publish` to a phone number or a platform endpoint answers it too
      (SNS-47).
- [ ] Topics, attributes, tags, subscriptions, pending tokens, the dedup window
      and the data-protection policy survive a restart.

## 6. Console

The design's "SNS" page: 19 boards. Templates: `templates/sns.html` (`sns_lp`,
`sns_home`, `sns_topic`, `sns_receipt`, `sns_subs`), the `sns_create` block in
`create.html`, `pattern_builder` in `eb.html`, shared pieces in `panes.html`,
`kit.html` and `layout.html`. Handlers: `handlers_sns.go`, `handlers_tags.go`;
client `client_sns.go`, `client_tags.go`; routes in `routes_messaging.go`.
*Read 2026-10-11: the SNS page does not use the kit (`res_header`,
`setting_field`, `res_wire`); it is the first thing the console stage moves.*

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Sns-Home | Topics, none open | `sns_home`, `empty_detail` | unreachable while a topic exists (foundation A7) |
| Sns-01-None | No topics yet | `sns_home`, `lp_empty`, `empty_detail` | exists; matches (6a) |
| Sns-02-Create | Create, with what it makes | `sns_create`; `snsCreateTopic` | form exists; the card is **new** |
| Sns-03-Create-FIFO | FIFO and content-based dedup | `sns_create`; `snsCreateTopic`, `CreateTopic` | **new** (SNS-1) |
| Sns-04-Create-Refused | Name refused | `fail_inline` | exists; today the browser `pattern` stops it first |
| Sns-05-Subscriptions | Subscriptions tab | `sns_topic`, `sns_subs`; `snsTopic` | exists as rows with badges; the table and the filter column are **new** |
| Sns-06-Subs-Empty | No subscribers | `sns_subs` | exists; the two explanatory cards are **new** |
| Sns-07-Sub-Settings | Filter policy and raw delivery | `sns_subs` (`sub-cfg`), `pattern_builder`; `snsSubFilter`, `snsSubRaw`, `ebTestPattern` | exists stacked; the two-column panel, read-back and payload are **new** |
| Sns-08-Subscribe | Add a subscription | `sns_subs` (the add form); `snsSubscribe` | exists as a row with an advanced panel; the panel and the fit check are **new** |
| Sns-09-Subscribe-Refused | Subscribe refused | `fail_inline` | exists inline at the form's foot; the side list is **new** |
| Sns-10-Confirm | Confirm a pending subscription | `sns_subs` (confirm panel); `snsConfirm` | exists; "what the endpoint was sent" is **new** |
| Sns-11-Publish | Publish and its receipt | `sns_topic` (publish tab), `attr_editor`, `sns_receipt`; `snsPublish` | exists; the side column, pending count and section are **new** |
| Sns-12-Receipts | The four ways to arrive nowhere | `sns_receipt` | cases 1 and 2 exist; 3 is SNS-6; 4 is **new** |
| Sns-13-Publish-FIFO | FIFO publish, refused | `sns_topic` (publish tab); `snsPublish` | refusal exists; the two fields are **new** (SNS-2) |
| Sns-14-Details | Attributes, settable ones, data protection | `sns_topic` (details tab); `snsSetAttribute`, `snsDataProtection` | exists stacked; the "Here" column, side column and extra names are **new** |
| Sns-15-Access | Access policy, all subscriptions | `sns_topic` (details tab); `snsAddPermission`, `snsRemovePermission`, `allSubs` | exists; the side list is **new** |
| Sns-16-Tags | Tags as a draft | `tags_panel`, `tag_editor`; `tagsSave` | exists (foundation A5); the side column is **new** |
| Sns-17-Confirms | Delete topic, unsubscribe, revoke | `hx-confirm` in `sns.html` | exists without bodies; the consequence sentences and verbs are **new** wording |
| Sns-18-Denied | Publish denied under enforce | `fail_inline` | exists; matches (6a) |

**New items, and what each is built from**

| Item | Built from |
|---|---|
| "What this makes": ARN and type as you type | The name field, and the `region` and `account` template functions |
| FIFO and content-based dedup switches | `CreateTopic` attributes `FifoTopic`, `ContentBasedDeduplication`; the suffix rule in `sns/store.go` |
| "What one publish will need" | Static per switch; each sentence is a box in section 5 (FIFO). *Corrected 2026-10-11: the last sentence ("deduplication happens per subscribed queue") is rewritten with SNS-31.* |
| "Fits the FIFO queue already here" | The queue list (name suffix, `ContentBasedDeduplication`) the console already reads for SQS |
| Filter policy column | `FilterPolicy` from `subscriptionAttributes`, through the existing `patternPreview` |
| "Read back" sentence | The policy JSON, for the shapes the builder can represent; nothing otherwise |
| "What the queue receives" | The envelope's shape. Must come from one place the service also uses, not a second copy in a template; until SNS-13 it shows today's keys |
| "How it fits" on subscribe | The topic's `FifoTopic` and the chosen queue's name. Removed once SNS-12 refuses the pairing; the refusal then says it |
| "What the endpoint was sent" | Static shape from `sendConfirmation`; the token is never shown, because the console does not have it |
| Pending count beside "fans out to", and the awaiting section | The `Pending` flag already on each `subView` |
| "Matched N of M" | `DozeMatchSubscriptions`, unchanged; the wording follows what it can know |
| Group and dedup fields on publish | `Publish` and `PublishBatch` take `MessageGroupId` and `MessageDeduplicationId`; shown only when `FifoTopic` is `"true"` (SNS-26) |
| "Here" column on Details | The topic-attribute tier table in section 2, through foundation B4 |
| Extra settable names | `settableTopicAttribute` in `sns/actions_ext.go`, exposed as a list; *added 2026-10-11: `MaximumMessageSize` (SNS-30), with its 100-subscription coupling as the fifth line* |
| Unsubscribe for a pending row | The ARN `Subscribe` returns under `ReturnSubscriptionArn` (`model`: "the response also includes the pending subscription ARN value"); it is not in any list call, so it is available only until the page reloads unless the service exposes it. Decide (section 10). |

**Where the teaching lines come from**

| Line | Source |
|---|---|
| Name: 1 to 256, the character set, `.fifo` | `topicName` in `sns/store.go`. *Corrected 2026-10-11: was "hand-derived, `doc`"; `model` (CreateTopic.Name).* |
| FIFO fixed at creation | `topicAttributes` omits `FifoTopic` |
| Content-based dedup uses SHA-256 of the message | `fifoFor` in `sns/fifo.go`; `model` ("the body of the message (but not the attributes)") |
| Group and dedup id go to each FIFO queue; dedup at the topic, five minutes, across the topic or within a group | *Corrected 2026-10-11: was "dedup is per queue; `dedupWindow` in `sqs/store.go`". That sentence taught a difference as the rule; it is rewritten with SNS-31 and cites `model`.* |
| Filter tests attributes (default scope) or the body (`MessageBody`); fields AND, values OR | `matchFilter`; ledger. *Corrected 2026-10-11: "attributes, not the body" is true of the default scope only (SNS-18).* |
| The operator list | Ledger, "Filter policies share EventBridge's pattern engine" |
| `MessageBody` scope stored, not applied | Ledger, "Differences from AWS". Changes with SNS-18 |
| Raw delivery per protocol | `deliverSQS`, `deliverHTTP`, `deliverLambda`. The http sentence changes with SNS-10 |
| Protocols: confirmed at once, pending, never delivered | `Subscribe` in `sns/store.go`; `snsProtocols`; ledger. *Corrected 2026-10-11: sms, application and firehose are confirmed at once (SNS-23).* |
| Same endpoint twice is one subscription | `Subscribe` in `sns/store.go` (`memory`) |
| String as text, Number as a number, Binary as base64 | `attrMatchValue`; `attr_editor` |
| Count 1 to 10 is one `PublishBatch` | `snsPublish` |
| Attribute tiers on Details | Section 2; ledger |
| Tag key 1 to 128, value 0 to 256 | `constraintTables` in `sns/validate.go` (`model`) |
| The default statement grants nothing by itself | The comment and code in `sns/guard.go` |
| Only an explicit Deny refuses the console | `iamguard.Guard.Check` |

**Settings records** (L11; `internal/settings`, foundation B4; none exists for SNS
today). Each carries name, unit, range or enum, default, enforced or stored, and
its source (`model`): topic name (1 to 256, enforced); `FifoTopic` (fixed at
creation, enforced); `ContentBasedDeduplication` (false, FIFO only, enforced);
`MaximumMessageSize` (1,024 to 1,048,576 bytes, default 262,144, enforced once
SNS-30; line five: subscriptions and their protocols against the 100 limit);
`FifoThroughputScope` (Topic or MessageGroup, default Topic, enforced once
SNS-31); `DisplayName`, `DeliveryPolicy`, `KmsMasterKeyId`, `SignatureVersion`,
`TracingConfig`, `ArchivePolicy` (stored); `Policy` (evaluated); the five
`*SuccessFeedbackSampleRate` (0 to 100, default 100, enforced) and the role ARNs
(the switch); subscription `FilterPolicy`, `FilterPolicyScope`,
`RawMessageDelivery`, `RedrivePolicy`, `DeliveryPolicy`; publish: `Subject`
(under 100 characters, no line break), `MessageGroupId` and
`MessageDeduplicationId` (up to 128), `Count` (1 to 10), a message attribute's
name and `Number` range. About 25 records; line five is real only for
`FifoTopic` against the queue list, `MaximumMessageSize` against the
subscriptions, the filter policy against the limit of 5 keys and 150
combinations, and a standard publish against a FIFO queue.

**Statements on the boards not yet backed.** Remove or prove each before the
board ships. *Re-read 2026-10-11 against the model; changes marked.*

- Sns-05, Sns-10, Sns-17: "SNS lists a pending subscription with no ARN to
  address." Half true. The list hides it; `Subscribe` returns it
  (`model`). *The three boards now say "no ARN in the list (Subscribe returns it
  when asked)"; the decision on Unsubscribe for a pending row is open.*
- Sns-02: "Creating a name that already exists is not an error: its attributes
  and tags are merged." *Contradicted by the model: "that topic's ARN is
  returned without creating a new topic". The board now says that.*
- Sns-07: the http raw sentence and the envelope sample describe today's
  defects (SNS-10, SNS-13) and change when those are fixed.
- Sns-08: "delivered as an asynchronous invoke" for Lambda. *Now `doc`: the
  Lambda developer guide.* "sms, application and firehose ... stay pending".
  *False (`model`); corrected on the board.*
- Sns-10: "an email, sms, application or firehose subscription stays pending".
  *False for sms, application, firehose (SNS-23); corrected on the board.*
- Sns-12, card 4: the IAM-enforce cause was not run under enforce. The other
  two were. A FIFO queue on a standard topic is `memory` (SNS-12).
- Sns-13: "any non-empty text" for a group id. *False: up to 128 alphanumeric
  and punctuation characters (`model`); corrected on the board.*
- Sns-14: "`SubscriptionsDeleted` always 0" is from the code; *the model says it
  counts deleted subscriptions (SNS-48).* "Policy takes any text" is the
  defect of SNS-16.
- Sns-16: "tags change nothing about how it delivers" is from reading
  `delivery.go`. *The board omits the limit: more than 50 tags is
  `TagLimitExceeded` in the model and "not recommended" in the same model
  (SNS-45); left off the board until recorded.* The refused path
  `tags.1.key` is the merged-element path of SNS-37; with the walk fixed it
  names the entry.
- Sns-17: the three consequence sentences. Delete (subscriptions go, peers
  untouched, later publishes get `NotFound`: `model` for the last) is from
  `DeleteTopic`; unsubscribe and revoke are from `Unsubscribe` and
  `removePermission`. None has a test.
- Sns-03 and the "What one publish will need" card: "Deduplication happens per
  subscribed queue, not at the topic". *True of the code and false of AWS
  (SNS-31); the board now describes AWS and waits on the fix.*

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each board (`console-design/project/Sns-*.dc.html`,
file:// at 1440 by 900) and the real page in the equivalent state (a binary
built from this tree, seeded with the boards' own topics; screenshots in
`scratchpad/dcheck/sns/`). Both themes were rendered for create, subscriptions
and publish; 900 wide for the same three. Shell chrome is one shared row: the
boards draw a two-letter mark rail, a "Topics 3 / New" list pane and a type
badge in the header; the real shell draws the full rail with icons (kept,
L12), "SNS (3)" with a filter and a "+", and the type in the fact strip. A
shell decision, not an SNS one; not re-opened here.

| Board | Verdict | What differs |
|---|---|---|
| Sns-Home | cannot match yet | Waits on foundation A7: `/sns` opens the first topic, so the nothing-selected state with "Select a topic to publish to it ..." and "The number is subscriptions." never renders. |
| Sns-01 None | matches | Copy is `copy.go`'s and identical; the "New topic →" link and the button are there. |
| Sns-02 Create | differs | Board: two columns, a "General" card with the name field, "1 – 256 characters", "The last part of its ARN ...", "letters, digits, hyphen, underscore · enforced" and "A standard topic: the name does not end in .fifo", the CLI panel, and a right-hand "What this makes" card (ARN, Type, Subscribers, redrawn as you type) plus the re-create note. Real: one 660 px column with the name input and a `pattern`, a collapsed `cli` chip; no teaching lines, no card. Waits on settings records (L11). |
| Sns-03 Create FIFO | cannot match yet | Needs SNS-1 (the console's `CreateTopic` takes no attributes) and the two switches with their lines, "Fits the FIFO queue already here" and the right-hand "What one publish will need". Its last sentence is corrected (SNS-31). |
| Sns-04 Create refused | differs | Board: the refusal is a banner above the form and a right column "Other names the same form is refused with" holds two more cards and two paragraphs. Real: one callout below the buttons, the typed name kept; the browser `pattern` stops most names first. The "why" line is the shell's (A2): "doze-aws refused the request: it breaks a rule of the service." |
| Sns-05 Subscriptions | differs | Board: a **table** (Endpoint with the service mark and a link, Protocol, Filter policy *new*, Raw, Status chip, Settings / Unsubscribe as text actions), a type badge, "Delete topic". Real: card rows with `sqs` / `filtered` / `raw` / `pending` badges, the ARN at the right, a gear and a trash icon, "Delete". The pending panel and its text match. The wiring strip draws a false fourth chip "orders" for the http endpoint (SNS-52). |
| Sns-06 Subs empty | differs | Board adds two cards, "What a topic with no subscribers does" and "What a subscription is", and lists the topic's queues including the dead-letter queue in the add row. Real: the empty sentence and the add row only. |
| Sns-07 Sub settings | differs | Board: the row is selected and a **two-column** panel opens under it: Filter policy (builder, "Read back" *new*, operator list with enforced, "Test an event" with an SNS-shaped sample and verdict, "Save filter") left; Delivery (raw switch, the three-protocol sentence, "What the queue receives" envelope *new*) right. Real: an accordion under the row, one column: builder, an EventBridge placeholder in "Test an event" (SNS-14), the raw switch; no read-back, no envelope. Three sentences wait on SNS-10, 13, 18. |
| Sns-08 Subscribe | differs | Board: an "Add a subscription" panel (esc) opening beside a right column "How it fits" *new*: protocol and queue, a block saying what each protocol does (corrected), the filter editor, "Deliver raw messages", Cancel / Subscribe. Real: one inline row at the foot of the subscribers panel with a sliders icon for the filter and raw options. "How it fits" waits on the SNS-12 decision. |
| Sns-09 Subscribe refused | differs | Board: the refusal banner at the top of the panel and a right column "Other refusals this form can come back with" (four cards). Real: the same refusal, inline below the editor, policy kept. Message text identical. The banner line "the rule comes from AWS's own service model" is not true of a filter-policy message (A2). |
| Sns-10 Confirm | differs | Board: the pending panel, a "If the token is wrong or missing" pair (client-side empty, `NotFound`), "After confirm", and "What the endpoint was sent" *new* with the posted JSON. Real: the panel and the `NotFound` refusal render, with the shell's "why" line; the other three are absent. The board's `NotFound` for a bad token is the code's, not the model's choice (SNS-38). |
| Sns-11 Publish | differs | Board: two columns, a compact editor, the attributes rows, "cli", ⌘⏎, Count, Publish, and the receipt beneath ("matched 2 of 3", "filtered out", "awaiting confirmation" *new*); right "What each field does" and "Reading the receipt". Real: one column with a tall editor, the receipt says "delivered to 2 of 3 subscribers" (SNS-5) and the awaiting section never fills (SNS-6); "fans out to 4 subscribers" counts the pending one (SNS-27); no side column. |
| Sns-12 Receipts | differs | Board: a page of four receipts. Real: cases 1 and 2 render with today's wording ("Add a subscription on the right", SNS-7); case 3 never fills (SNS-6); case 4 and the "where to look" paragraph do not exist. |
| Sns-13 Publish FIFO | cannot match yet | Needs SNS-2: the form has no group or dedup field, so every FIFO publish from the console is refused (rendered, same words, inline callout). The board's own text is corrected (group id up to 128). |
| Sns-14 Details | differs | Board: the attribute table with a **"Here" column** *new* and a right column: "Attributes you can set" (stored, with `ContentBasedDeduplication` and `SQSFailureFeedbackRoleArn` *new*, three sentences) over "Data protection policy". Real: one column; the table has two columns with copy buttons, "Attributes you can set" is a three-name select under it, then the access policy, the data protection policy, all subscriptions. `MaximumMessageSize` is on neither (SNS-30). |
| Sns-15 Access | differs | Board: the access table, the grant form left with its refusal above it, a right column of two other refusals, and "All subscriptions" with endpoint *names*. Real: same table and form, one column; refusal below the form; "All subscriptions" shows the endpoint ARNs. |
| Sns-16 Tags | differs | Board: two columns: the draft editor with its refusal banner above, and a right column "The same tab with nothing on it" and "What a tag is here" (limits, enforced). Real: the shared tag editor (Key, Value, Add tag, Save changes) in one column. |
| Sns-17 Confirms | differs | Board: three dialogs with bodies and "Keep it" / verb buttons ("Delete topic", "Unsubscribe", "Revoke"). Real: "Delete topic “x”?" and "Unsubscribe “x” from this topic?" with no body and "Cancel" / "Delete" or "Unsubscribe"; revoke has a one-sentence body. The mechanism exists (`data-confirm-verb`, `data-confirm-cancel`, a body after the question). |
| Sns-18 Denied | matches | The Denied callout is word for word (`AuthorizationError`, 403, the resource-policy sentence, "Access log →"). The board's "Where the denial comes from" panel is annotation; the statement can be read from the Policy attribute. |

**Where the design is wrong** (corrected in the working copy on 2026-10-11 in
Sns-02, Sns-03, Sns-05, Sns-08, Sns-10, Sns-13 and Sns-17; the published canvas
still carries the old text until it is republished)
- Sns-08, Sns-10: "email, sms, application and firehose ... stay pending". `model`:
  only HTTP/S, email and cross-account endpoints are confirmed by their owner;
  the sentences now say sms, application and firehose are confirmed at once and
  email and email-json stay pending (SNS-23).
- Sns-13: a group id is "any non-empty text". `model`: up to 128 alphanumeric
  characters and punctuation (SNS-35). Now says so.
- Sns-03: "Deduplication happens per subscribed queue, not at the topic" and
  "The queue ... drops a dedup ID it has seen". `model`: the topic accepts a
  repeated dedup id and delivers it to nobody, across the topic or within a
  group by `FifoThroughputScope` (SNS-31). Now says so; the console cannot
  teach it until the service does it.
- Sns-02: "its attributes and tags are merged". `model`: the existing topic's
  ARN is returned without creating a new topic (SNS-24).
- Sns-05, Sns-17: "no ARN to address". `model`: `Subscribe` returns the ARN of
  a pending subscription when asked with `ReturnSubscriptionArn`; the list
  does not. Now says "no ARN in the list".
- Sns-09 (not edited, an A2 item): the banner under a filter-policy refusal
  claims the model as the source. The model has no filter policy rule; the
  source is the policy page and the shared pattern engine.
- Sns-16 (not edited): "tags.1.key" for the third row is the merged-element
  path of SNS-37; and the board is silent on the 50-tag limit the model both
  names and discourages. Decided with the recording (SNS-45).
- Sns-14 (not edited): lists no `MaximumMessageSize`, the one documented
  attribute whose refusal here is a feature AWS accepts (SNS-30). A board is
  owed for it: a number field in "Attributes you can set" with its five lines,
  the fifth being the subscriptions and protocols the 100-subscription rule
  counts.

**Build list, in order**
1. Move the topic page onto the kit: `res_header` (name, a **Standard** or
   **FIFO** badge from `FifoTopic == "true"` (SNS-26), "Delete topic", the ARN
   chip, the four facts) and `res_wire` on every tab; `lp_head` keeps its icon
   (SNS-60).
2. SNS settings records (about 25, section 6) and `setting_field` for every
   shown setting; line four from the record's tier.
3. Create: two columns, the name field's lines, "What this makes", the FIFO
   and content-based deduplication switches, "Fits the FIFO queue already here",
   "What one publish will need" (corrected sentences), the CLI preview with
   `--attributes`. Behaviour: the console's `CreateTopic` takes attributes (SNS-1).
4. Refusals: the banner above the form on create, subscribe and tags, with the
   right column of other refusals; wording from `copy.go`, not typed into the
   template. Behaviour: none.
5. Subscriptions: the table (Endpoint with service mark and link, Protocol,
   Filter policy through `patternPreview`, Raw, Status, row actions); no
   Unsubscribe on a pending row until the decision; the false http chip
   removed (SNS-52); the type badge.
6. Settings panel: two columns, the builder, "Read back", an SNS-worded test
   (SNS-14), the raw switch with the envelope sample from the one definition
   (SNS-13).
7. Subscribe: the "Add a subscription" panel with the protocol facts and
   "How it fits" (after the SNS-12 decision), the filter editor, raw.
8. Confirm: "If the token is wrong", "After confirm", "What the endpoint was
   sent" (static shape from `sendConfirmation`; the token is never shown).
9. Publish: two columns with "What each field does" and "Reading the receipt";
   the compact editor; group and dedup fields when FIFO (SNS-2); attributes and
   `String.Array` in the batch path (SNS-3, SNS-54); a refused batch is a
   refusal (SNS-4); the receipt in four states with matched N of M, never
   "delivered" (SNS-5, SNS-6, SNS-7, SNS-27). Behaviour: `PublishBatch` takes
   attributes and ids.
10. Details: two columns, the "Here" column from the settings records, "Attributes
    you can set" with every settable name including `MaximumMessageSize`, the
    data protection panel, then access policy and all subscriptions (endpoint
    names), with the right column of refusals.
11. Tags: two columns, "The same tab with nothing on it", "What a tag is here".
12. The three confirms: bodies and verbs through the existing
    `data-confirm-verb` and `data-confirm-cancel`.
13. `/sns` with no topic selected (A7) once the foundation lands it.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | Pending in the match (SNS-6); policy validation (SNS-16); attribute names and values (SNS-19, SNS-43, SNS-44); FIFO queue on a standard topic (SNS-12); token and topic (SNS-22); Lambda record shape with a fake peer (SNS-11); `TargetArn` under enforce (SNS-32); `MessageStructure` (SNS-33); group forwarding (SNS-34); batch rules (SNS-36) |
| SDK v2 | Paging on three list calls (SNS-17); raw http against a recording server, headers included (SNS-10, SNS-40); envelope key set (SNS-13); body-scoped filter (SNS-18); publish limits and `MaximumMessageSize` (SNS-20, SNS-30); re-create outcomes (SNS-24); unsubscribe cases (SNS-9); topic-level dedup with a standard and a FIFO queue (SNS-31); key sets (SNS-48); subscription dead-letter queue (SNS-42) |
| The Query wire | One test posting each of the 21 operations spelled as the reference spells them (`member`, `entry`/`key`/`value`, `MessageAttributes.entry.N.Name`/`Value`), modelled on `TestEveryOperationIsAcceptedOnTheQueryWire`; the same rules on a second element (SNS-37, after B13) |
| Rejection parity | Every hand-derived rule in section 5: name, protocol, endpoint, attribute name and value, filter limits, message size, subject, group and dedup id, batch ids; regenerated cases put each violation in element 2 and 3 |
| Console (`internal/console`) | FIFO create (SNS-1); FIFO publish (SNS-2); batch carries attributes (SNS-3); refused batch (SNS-4); `FifoTopic=false` (SNS-26); pending count (SNS-27); an http endpoint is not a queue (SNS-52) |
| Provision, CloudFormation | A FIFO topic and its attributes, subscription attributes, an email subscription, the topic policy (SNS-51, CFN-4) |
| Restart | Subscriptions with filter and raw flag, a pending token, tags, policy, data-protection policy, the dedup window |
| Strict | `sns/strict_test.go`: token age and `StaleTag` (SNS-38, SNS-59) |
| Conformance (boto3) | The scenarios in section 8 |
| Browser | Each `new` item; the receipt in all four states (SNS-5, SNS-6, SNS-7); unsubscribe a pending row (SNS-8); the SNS wording on the filter test (SNS-14); the feedback attribute to the Failure log group (SNS-15); each confirm's sentence and verb; the denied state under enforce; both themes and 900 wide for create, subscriptions and publish |
| Identity | The package under a non-default account and region (`Owner`, the default policy, log group names) |
| Mutation | `sns/` baseline, then survivors in `delivery.go`, `filter.go`, `fifo.go`, `guard.go`, `store.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today unless the source says `model`.
All 7 existing scenarios are unverified as well and are recorded in the same
pass. *Sources re-tagged 2026-10-11.*

| Claim | Source now | Scenario |
|---|---|---|
| Topic name rule and its error | model (rule), memory (wording) | `test_sns::topic_name_rules` |
| Re-create with different attributes, different tags, none | memory | `create_again_must_agree` (exists) |
| The nine protocols; anything else refused | model (the nine), memory (code) | `subscribe_protocols` |
| Endpoint shape per protocol | model (shape), memory (code) | `subscribe_endpoint_shapes` |
| A FIFO queue on a standard topic is refused | memory | `fifo_queue_on_standard_topic` |
| A FIFO topic delivers to a standard queue and to Lambda; Lambda on a FIFO topic | doc (FIFO pages; Lambda page: "FIFO topics aren't supported" as a trigger) | `fifo_topic_subscribers` |
| Which protocols start pending | model | `pending_by_protocol` |
| An unknown subscription attribute name is refused; a bad value | memory | `subscription_attribute_names` |
| `RawMessageDelivery` on a lambda subscription | not established | `raw_delivery_by_protocol` |
| Raw delivery to http: body and headers | doc (read) | `raw_delivery_http` |
| The notification envelope's key set | doc (read) | `envelope_shape` |
| The Lambda event's key set and attribute types | doc (read) | `lambda_event_shape` |
| `Unsubscribe`: missing, malformed, `PendingConfirmation` | model (codes listed), memory (which) | `unsubscribe_cases` |
| `ConfirmSubscription` with another topic's ARN; a used token; an expired token; the code for a bad token | not established | `confirm_token_cases` |
| Paging at 100 on the three list calls | model | `list_paging` |
| Message size by `MaximumMessageSize`, subject length, `Number` syntax, each code | model, doc (read), memory (codes) | `publish_limits` |
| `MaximumMessageSize` Set, its 100-subscription coupling | model, doc (read) | `maximum_message_size` |
| Filter policy limits | doc (read) | `filter_policy_limits` |
| `FilterPolicyScope: MessageBody` matching | model, doc (read) | `filter_policy_body_scope` |
| An invalid `Policy` at `SetTopicAttributes` | memory | `invalid_topic_policy` |
| Value rules for topic attributes; `ContentBasedDeduplication` on a standard topic | model (ranges), memory (refusal) | `topic_attribute_values` |
| `GetTopicAttributes` and `GetSubscriptionAttributes` key sets; `SubscriptionsDeleted` | model | `attribute_key_sets` |
| FIFO: dedup at the topic; a repeated dedup id's answer; scope MessageGroup | model, doc (read); the answer's `MessageId` not established | `fifo_dedup_and_group_rules` |
| `MessageStructure: json` | model | `message_structure_json` |
| A standard topic's group id to a standard queue | model | `standard_topic_group_id` |
| Batch rules: id pattern, `BatchRequestTooLong`, per-entry failure | model | `publish_batch_rules` |
| The 50-tag limit and its error; `ResourceNotFound` | model (both sentences) | `tag_limit` |
| The refusal for the SMS and mobile-push operations nobody builds | n/a (ours, D5) | none; the foundation's generic test |
| Whether the topic policy governs calls that name a subscription or `ResourceArn` | not established | `policy_scope` |
| What a 129-character tag key answers (`ValidationError` or `InvalidParameter`) | not established | `tag_key_too_long` |
| A subscription `RedrivePolicy` moves a failed delivery | model, doc (read) | `subscription_dlq` |
| `AddPermission` with a bad account id or action name | memory | `add_permission_rules` |

## 9. Owed to integration

| What | With |
|---|---|
| A FIFO queue subscribed to a standard topic, seen from the queue's side | SQS |
| A FIFO topic into a standard queue: dedup at the topic, the standard queue's own `MaximumMessageSize` not applied to what SNS sends (`doc`) | SQS |
| Under enforce, a queue with no `sns.amazonaws.com` statement refuses the delivery; with one conditioned on `aws:SourceArn` it accepts | SQS, IAM |
| Under enforce, a function with no permission for `sns.amazonaws.com` is not invoked | Lambda, IAM |
| The function receives the full SNS event, attributes included, through a real runtime | Lambda |
| A deleted queue or function: the subscription remains and each delivery fails quietly (or goes to the subscription's dead-letter queue) | SQS, Lambda |
| A bucket notification into a topic needs a `s3.amazonaws.com` statement under enforce | S3, IAM |
| A rule target, an alarm action, a Lambda destination and a Step Functions task each publish, and fan out | EventBridge, CloudWatch, Lambda, Step Functions |
| Delivery-status records land in the Success and Failure log groups | CloudWatch Logs |
| Topic, subscription and topic policy from one template, through CloudFormation, SAM, CDK and Terraform, including a FIFO topic and subscription attributes (SNS-51) | CloudFormation |
| The wiring graph's `sub` edge appears on both pages and goes when the subscription does | console |
| Fan-out to many subscribers under load; a restart between publish and delivery; a hung webhook | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] SNS-1 to SNS-60 each closed, with the test that failed first named beside
      it. *Corrected 2026-10-11: was "SNS-1 to SNS-29".* SNS-61 (AddPermission
      values) is closed or recorded.
- [ ] Decided and written into `SUPPORT.md`: body-scoped filters (SNS-18);
      which protocols start pending (SNS-23, settled by the model); subscription
      `RedrivePolicy` (SNS-42); re-create with a different definition
      (SNS-24); `MessageStructure: json` (SNS-33, settled by the model);
      whether a pending subscription's ARN is exposed to the console (SNS-8);
      the delivery decisions of "L7 decisions"; `--sns-strict` (SNS-38,
      SNS-59); the 50-tag question (SNS-45); the envelope signing decision
      (SNS-13).
- [ ] The three tier tables in section 2 have no "?" and no "not established",
      and every C attribute and C protocol reads as cosmetic on the Details tab
      and in the subscribe form.
- [ ] The stubs answer 501 `NotImplemented` with `X-Doze-Not-Built` through
      `awshttp.NotBuilt`, and the stub row in section 2 says so.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped. This resolves, by name: topic-attribute value
      rules, `RawMessageDelivery` on lambda, the token under another topic, a
      repeated dedup id's answer, the policy's scope over subscription and tag
      calls, and what the restart test holds.
- [ ] A publish to a topic reaches SQS, http and Lambda subscribers in the
      shape section 5 names, raw and enveloped, and the three P0 delivery
      findings (SNS-10, SNS-11, SNS-12) plus SNS-32 each have a test in the
      package, not only in integration.
- [ ] A template with a FIFO topic, its attributes and a topic policy deploys
      and the policy is on the topic (SNS-51, CFN-4).
- [ ] **The fidelity table (6a) exists, was made from rendered boards beside
      rendered pages, and no row says "differs" without a decision beside it.**
      Today 14 rows say differs and 3 cannot match yet; the build list in 6a is
      the decision, and each row is re-rendered when its item is built.
- [ ] **Icons kept**: the rail, the list pane header (SNS-60) and the resource
      header draw `sns.svg`; no board's two-letter mark is built.
- [ ] **The five teaching lines on every setting** the page shows (about 25
      records, section 6), line four from the settings record and line five live
      with real numbers.
- [ ] **Both wires.** n/a with a sentence: the model declares one (`awsQuery`).
      The Query wire is held by a test that posts all 21 operations as the
      reference spells them and by the rules on a second element (section 7).
- [ ] **Each asynchronous operation decided (L7)**: delivery, confirmation,
      retries, the subscription dead-letter queue, replay and archive, filter
      policy propagation; each is built with a visible state or listed as
      immediate with the difference.
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
| 0 | Foundation B13 (the whole request walk) lands first; the settings records' package (B4) is in place | `internal/modelcheck/modelcheck.go` |
| 1 | Contract: the three tier tables, the decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for SNS-1 to SNS-60 | `sns/*_test.go`, `internal/console/*_test.go`, `internal/provision/*_test.go`, `e2e/tests/sns.spec.ts` |
| 3 | Delivery shape: raw http, envelope keys, headers, Lambda record, message attributes, `MessageStructure` | `sns/delivery.go`, `sns/actions.go` |
| 4 | Subscribe and unsubscribe rules: FIFO pairing, endpoint, attribute names and values, pending by protocol, token and topic, background confirmation | `sns/actions.go`, `sns/store.go`, `sns/delivery.go` |
| 5 | Publish rules and the topic's size: `MaximumMessageSize`, group and dedup ids, batch rules, `TargetArn` under the guard | `sns/actions.go`, `sns/actions_ext.go`, `sns/guard.go`, `sns/fifo.go` |
| 5a | Topic-level dedup and `FifoThroughputScope` with the window persisted | `sns/fifo.go`, `sns/store.go` |
| 6 | Policy validation; identity in `Owner`; attribute values; key sets | `sns/actions_ext.go`, `sns/guard.go`, `sns/actions.go` |
| 7 | Paging; filter limits and body scope; re-create; missing-topic codes; stubs through `awshttp.NotBuilt` | `sns/actions.go`, `sns/store.go`, `sns/filter.go`, `internal/eventpattern/` (read only unless body scope needs it) |
| 7a | Retries and the subscription dead-letter queue (after the decision); strict | `sns/delivery.go`, `sns/strict.go`, `cmd/doze-aws` flag |
| 8 | The match reports pending | `sns/doze_match.go`, `sns/doze_match_test.go` |
| 9 | Section 5 sweep, parity cases, the Query-wire test | `sns/rejection_parity_test.go`, `sns/sdk_test.go`, `sns/sdk_coverage_test.go`, `sns/testdata/cases_sns.json` |
| 10 | Provision and CloudFormation: topic attributes, subscription attributes, protocols, the topic policy | `internal/provision/model.go`, `internal/provision/svc_sns.go`, `internal/cfn/mapper.go`, `internal/cfn/resources.go` |
| 11 | Conformance scenarios and claims table | `conformance/tests/test_sns.py`, the claims file from foundation B1 |
| 12 | Console: the kit, settings records, 14 differing boards, FIFO create and publish, batch attributes, receipt, pending rows, Details | `internal/console/templates/sns.html`, `create.html`, `eb.html` (`pattern_builder` wording by caller), `kit.html`, `layout.html` (`attr_editor`), `resourceurl.go`, `handlers_sns.go`, `client_sns.go`, `app.css` (SNS-60) |
| 13 | Browser tests, re-rendering each board against its row in 6a | `e2e/tests/sns.spec.ts` |
| 14 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 to 5a come before the console on purpose: five boards (Sns-03, Sns-07,
Sns-08, Sns-10, Sns-12) carry sentences that describe today's defects and are
rewritten once those are fixed.

**Prior art.** AWS's SNS developer guide is the source for every hand-derived
rule in section 5 that the model's text does not carry (raw delivery, the
notification JSON format and headers, filter policy constraints, delivery
retries, dead-letter queues, message attributes, large messages, FIFO topics),
and each rule's test names the page; the pages read for this review are named
in the findings. The Lambda developer guide's SNS page carries the example
event that SNS-11 is checked against. LocalStack's SNS implements topic-level
FIFO deduplication, the subscription `RedrivePolicy` and filter-policy scope
(`MessageBody`) as real behaviour rather than stored attributes, which is the
precedent for building SNS-18, SNS-31 and SNS-42 instead of listing them
(`memory`: the project's behaviour, not a page read).
