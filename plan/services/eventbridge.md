# EventBridge

Stage 9. Size L, **re-estimated XL after the pre-stage review** (section 1).
Follows [../01-protocol.md](../01-protocol.md); the common acceptance criteria
there apply in full, and this page adds EventBridge's own.

## Pre-stage review (2026-10-11)

Done before the stage starts, so that it starts from facts. It follows
[../02-lessons-from-sqs.md](../02-lessons-from-sqs.md): every claim in this
document was checked against the model's own documentation
(`.audit-models/eventbridge.json`, every operation and member read, not
grepped), against the user-guide pages named below (each fetched and read on
2026-10-11), and against the code and a binary built from this tree. Source
tags are the four of the protocol: `model`, `doc` (page named), `memory`
(nothing is fixed on it), `run`/`read` for doze-aws behaviour.

**Pages read for `doc`:** `eb-quota.html`, `eb-transform-target-input.html`,
`eb-targets.html`, `eb-putevents.html`, `eb-archive.html`,
`eb-replay-cancel.html`, `eb-rule-retry-policy.html`, `eb-rule-dlq.html`,
`eb-create-rule-schedule.html`, `eb-scheduled-rule-pattern.html`,
`eb-create-pattern-operators.html`, `eb-event-patterns-arrays.html`,
`eb-api-destinations.html`, `eb-tagging.html`, `eb-event-bus-perms.html`
(all under `docs.aws.amazon.com/eventbridge/latest/userguide/`). `eb-events-structure.html` returned no content and was not used; `eb-events.html`,
`eb-create-event-bus.html` and `eb-event-patterns.html` were read and say nothing that
is used here.

**What changed in this document.** 41 claims were corrected in place (the
list is K1 to K41 below, and each is marked in the text where it lives), 34
findings were added (EB-32 to EB-65: 31 in the API and delivery, 3 in the
console), one was withdrawn (EB-2: the code does what AWS does), and the
board-by-board fidelity table (6a) was made from rendered boards beside
rendered pages. The headline: **seven of the eight original P0s stand and one
(EB-2) was backwards, and the service has three more P0s the plan did not know
about** (an input transformer that delivers invalid JSON, a `Time` that is
thrown away, and a replay window that is measured on the wrong clock).

### Corrections made in place

| Id | Where | Was | Now |
|---|---|---|---|
| K1 | s2 PutEvents row, EB-2 | a bus that does not exist is accepted and dropped: a defect | **not a defect.** `doc` eb-putevents: "If you use PutEvents to publish an event to an event bus that does not exist, EventBridge ... will drop the event. Although EventBridge will send a 200 response, it will not fail the request or include the event in the FailedEntryCount". EB-2 withdrawn |
| K2 | EB-1 source | `doc` | `model`: PutEventsRequestEntry.EventBusName "The name or ARN of the event bus to receive the event" (and the shape pattern holds a bus *name* to `[.-_A-Za-z0-9]`, no `/`) |
| K3 | EB-3 source | doc / memory | `doc` eb-targets lists Kinesis, Step Functions, ECS, Firehose, Glue and about twenty more as targets; Kinesis and Step Functions are **local services**, so "delivers nothing" is a gap, not an impossibility; `memory` stays for "a malformed ARN is refused" (the model gives `TargetArn` length 1 to 1600 and no pattern) |
| K4 | EB-4 source | memory | `model` for the requirement ("All rules associated with this event bus need to be deleted"); the model lists **no** error for it (DeleteEventBus errors: ConcurrentModificationException, InternalException), so the refusal's code is `memory` |
| K5 | EB-5 source | memory | `doc` eb-transform-target-input: five predefined variables, named, reserved |
| K6 | EB-6 source | memory | `doc` (same page and eb-targets: "array indices, wildcards (*), forward slashes" are supported), **but the model contradicts itself**: PutTargets "you must use JSON dot notation, not bracket notation", InputTransformer.InputPathsMap "You must use JSON dot notation, not bracket notation", and Target.InputPath "You may use JSON dot notation or bracket notation". Recorded, not settled |
| K7 | EB-7 source | model, doc | `model`: Target.SqsParameters "If you specify a fair or FIFO queue as a target, the queue must have content-based deduplication enabled"; `doc` eb-targets: "Amazon SQS fair and FIFO queues: use SqsParameters to specify the message group". Adds a fact the plan lacked: the queue must have content-based dedup |
| K8 | EB-8 source | model | `model`, with the quote: PutRule "If you are updating an existing rule, any tags you specify in the PutRule operation are ignored"; CreateEventBus.Tags "Tags to associate with the event bus" |
| K9 | EB-9 source | doc | `model`: PutRule.State "ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS: The rule is enabled for all events, including ... management events delivered through CloudTrail"; "ENABLED ... matches events against the rule, except for ... management events". Same bug a second time in `internal/cfn/mapper.go:846` (EB-60) |
| K10 | EB-10 source/detail | model | `model` with the numbers: `Limit` 1 to 100, `NextToken` 1 to 2048 characters, ListEndpoints calls it `MaxResults`, "The maximum number of results per page for requests is 100"; an expired token is "an HTTP 400 InvalidToken error" (text only: no such error shape is in the model) |
| K11 | EB-11 source | memory | `model` + `doc`: PutTargets "Each rule can have up to five (5) targets associated with it at one time"; eb-quota "Targets per rule 5, not adjustable"; `LimitExceededException` is in PutTargets' errors, so the code is `model` too. The per-request limit is stated two ways: text "10", trait `TargetList` max 100 (EB-52) |
| K12 | EB-12 source | doc | `model`: "Input, InputPath, and InputTransformer are mutually exclusive and optional parameters of a target"; the refusal's code is not in the model (`memory`) |
| K13 | EB-13 source | memory | `model`: Detail "A valid JSON object"; ErrorCode list names `MalformedDetail` "The JSON provided is not valid". The code for "valid JSON but not an object" is `memory` |
| K14 | EB-15 source | memory | `model` + `doc` for "the destination is the archive's own bus" (ReplayDestination.Arn "You can replay events only to the event bus specified to create the archive"; eb-archive "Archive events can only be replayed to the source event bus"); end-before-start and a missing destination stay `memory` (StartReplay's errors name no validation error) |
| K15 | EB-16 | "every other not-found in the package is 400" (source n/a) | `model`: ResourceNotFoundException carries no `httpError` trait, awsJson1_1 gives every client error 400, so the 404 is wrong and the 400s are right |
| K16 | EB-17 | n/a | `model`: DisableRule "A disabled rule won't match any events", so Publish skipping it and the verdict saying `match` disagree on the model's own terms |
| K17 | EB-21 source | doc | `model`: PutRule "If you are updating an existing rule, the rule is replaced with what you specify ... If you omit arguments in PutRule, the old values for those arguments are not kept. Instead, they are replaced with null values" |
| K18 | EB-24 | "Nothing can set a bus policy" | true only because PutPermission is a stub; the bus **has** a resource policy in the model (PutPermission, DescribeEventBus.Policy, "cannot exceed 10 KB"). EB-56 |
| K19 | EB-29 | the ledger's "A target that fails fails inside your PutEvents call" is wrong because PutEvents returns success | the **behaviour is right and the sentence is wrong**: `doc` eb-putevents and eb-rule-retry-policy: delivery is separate, retried for 24 hours up to 185 times, then dropped or sent to the DLQ; PutEvents never reports it. Fix `SUPPORT.md`, not the handler |
| K20 | EB-31 source | doc | `model`: ListRules errors "InternalException, ResourceNotFoundException" |
| K21 | s2 StartReplay "Synchronous; COMPLETED at once" | a target | `model`: ReplayState STARTING, RUNNING, CANCELLING, COMPLETED, CANCELLED, FAILED; "replays them using 1 minute intervals"; `doc` cancellable "while its status is Starting or Running". Needs a decision (EB-53) |
| K22 | s2 CancelReplay "S, always IllegalStatusException" | S | it is **not an S** in the protocol's sense (no 501, no `X-Doze-Not-Built`); it answers as AWS would for a completed replay. The tier is wrong in the ledger and `not-built.md` line 109; it becomes F with EB-53 |
| K23 | s2 PutPermission / RemovePermission "no meaning locally" | S | the model gives the bus a resource policy; the protocol requires a service that has one to validate it, store it, return it. EB-56 asks for the decision |
| K24 | s2 "Bus LogConfig: validated; stored or not is not established" | unknown | **established by read and run: dropped.** CreateEventBus, UpdateEventBus and DescribeEventBus never touch it (EB-50) |
| K25 | s2 "Archive KmsKeyIdentifier, Connection KmsKeyIdentifier, Replay Description: not established" | unknown | **established by read and run: dropped** (EB-50) |
| K26 | s2 "model ... 40 operations ... 448 of 448 constraints enforced, `knownGaps` empty" | taken from the ledger | **overstated**: walking the model's input shapes against `constraintTables` leaves 86 constraints with no table entry (52 length, 21 pattern, 8 required, 3 enum, 2 range), among them every list length (EB-52) |
| K27 | s2 "Archive ... Retention is C" | cosmetic | confirmed C; `model` RetentionDays "Default value is 0. If set to 0, events are retained indefinitely"; range min 0, no max; `doc` eb-archive "By default, EventBridge stores events in an archive indefinitely" |
| K28 | s2 API destination "Rate limit is C" | C, no default | still C; `doc` eb-create-rule-schedule: "This value must be greater than zero. By default, this value is set to 300"; eb-quota "Rate of invocations per API destination 300 per second". Not returned by Describe when unset (EB-47) |
| K29 | s2 "`http://` accepted, a declared difference" | declared | confirmed and now sourced: `doc` eb-create-rule-schedule "The endpoint URL must start with https"; the **model's pattern accepts `http://`** (the member is named `HttpsEndpoint`, its pattern does not require the scheme) |
| K30 | s3 "Restart: whether a data-dir test covers ... is not established" | unknown | established: covered: a rule (`persistence_test.go:176`), a connection with its destination and a cron rule (`persistence_surfaces_test.go:119`). **Not covered**: buses and their tags, targets with members, archives and their events, replays |
| K31 | s5 "the 256 KB entry size limit (doc)" | 256 KB | **1 MB**: `model` PutEvents "the total entry size must be less than 1MB"; `doc` eb-putevents "The total request size—that is, the sum of all event entries in the request—must be less than 1 MB (1,048,576 bytes)", and how it is counted (EB-38). The 256 KB figure is the older limit |
| K32 | s5 "Name rules: the model's pattern allows `/` ... Establish" | open | `model`: CreateEventBus.Name "Custom event bus names can't contain the / character, but you can use the / character in partner event bus names"; "You can't use the name default for a custom event bus". The store's `/` refusal is right; the backslash and space refusals are `memory` (the pattern holds them out anyway) |
| K33 | s5 "both together: establish" | open | `model`: "A rule can have both an EventPattern and a ScheduleExpression, in which case the rule triggers on matching events as well as on a schedule"; run: accepted, both fire |
| K34 | s5 "`TestEventPattern` requires the event's mandatory fields (memory)" | memory | `model`: "the following fields are mandatory: id account source time region resources detail-type". EB-41 |
| K35 | s5 "window inclusive at both ends" | open | `model` is split: StartReplay "Only events that occurred between the EventStartTime and EventEndTime are replayed"; Replay.EventEndTime "Any event with a creation time prior to the EventEndTime specified is replayed" (an exclusive end). Unsettled: recording queue |
| K36 | s5 "whether a replayed event carries `replay-name` (memory)" | memory | `doc` eb-archive: "it adds a metadata field to the event, `replay-name`"; "EventBridge also uses this field to make sure that replayed events aren't sent to archives" |
| K37 | s5 "`DeleteRule` ... on a missing rule succeeds; `Force` on a managed rule: not established" | open | `model`: "If you call delete rule multiple times for the same rule, all calls will succeed" (matches); "When you call delete rule for a non-existent custom eventbus, ResourceNotFoundException is returned" (does **not** match: EB-33); Force: ManagedRuleException, no managed rules exist locally |
| K38 | s6 "Eb-15: 0 means keep indefinitely on AWS (memory)" | memory | `model` + `doc` (K27). The statement may stay on the board |
| K39 | s6 "Eb-22: AWS retries for 24 hours and can park the event in a DLQ (the ledger's source is not recorded)" | unsourced | `model` ("the 24-hour retry window ... consider using a dead-letter queue") and `doc` eb-api-destinations, eb-rule-retry-policy. **Also**: the retried codes are 401, 407, 409, 429 and 5xx; doze retries 429, 5xx and an OAuth 401 (EB-61) |
| K40 | s6 "Eb-13 ... 'no array index' ... 'A predefined name is left as written' are true today" | true of the code | **wrong as statements of AWS** (K5, K6). The board is corrected in the working copy; see 6a |
| K41 | s8 sources | many `memory` | re-tagged in place, see section 8 |

### L1 to L12, answered

| Q | Answer | Findings, or what was read |
|---|---|---|
| L1 limits from the model | findings | EB-11, EB-35, EB-38, EB-39, EB-40, EB-41, EB-52; K1 to K41. Every operation's documentation and every member of every input and output shape was read (about 2,400 lines of cleaned text) |
| L2 optional parameters | findings | EB-36 (`Time` omitted means the call's time), EB-48 (every `Update*AuthRequestParameters` member is optional), EB-51 (filters are "exclusive"), EB-47 (`InvocationRateLimitPerSecond` omitted means 300), EB-21 (omitted PutRule members become null), EB-33 (DeleteRule on a missing bus) |
| L3 error codes | findings | EB-35 (`InvalidArgument`, not `ValidationException`), EB-55 (`NotImplemented`, 501), EB-16 (status), EB-33, EB-34. **Not established by the model**: `ValidationException` is in no EventBridge operation's error list. The refusals doze makes with it (DeleteRule with targets, DeleteEventBus on default, CreateEventBus name, TagResource on a non-rule ARN, StartReplay's checks) are `memory` and are on the recording queue; the generated shape refusals ("1 validation error detected: ...") are the common frontend's and stay |
| L4 direction | findings | stated on every row; the stricter ones are EB-34 (DescribeEventBus by ARN), EB-42 (valid patterns refused), EB-48 (partial update refused); the rest are more forgiving or wrong |
| L5 every wire | **checked, none beyond one** | the model declares `awsJson1_1` only (no `awsQuery`); all 57 operations are on `X-Amz-Target: AWSEvents.<Op>`; the server also answers a `1.0` content type (harmless) and refuses a Query-form post with `MissingAction`. No `xmlName`/`xmlFlattened` (JSON). The walk the SQS Query wire got wrong has a JSON equivalent here: list lengths and map members are missing from the tables (EB-52) |
| L6 tokens | findings | EB-10 and EB-51 (no list ever returns or reads a `NextToken`; what a stale one does is text only: HTTP 400 `InvalidToken`), EB-57 (a connection ARN's id, a target ARN's account and region are ignored) |
| L7 asynchronous | findings | EB-53 (replays), EB-54 (archive CREATING, connection CREATING/AUTHORIZING/DEAUTHORIZING/DELETING, rule and target propagation, delivery itself). Each is decided in the finding |
| L8 time-based rules | **checked, none that refuse** | no AWS refusal depends on time. The model's time rules are propagation ("Allow a short period of time for changes to take effect", on PutRule, PutTargets, RemoveTargets, EnableRule, DisableRule, DeleteRule, CreateArchive), a replay's 90-day deletion (`doc`), an archive's retention expiry (stored, never enforced: C) and a partner source's two-week expiry (S). All are accepted-as-immediate or not modelled; none needs a strict switch. Listed in EB-54 |
| L9 memory or disk | **checked** | on disk: buses, rules, targets, tags, archives and their events, replays, connections with secrets, destinations (one bbolt file). In memory: the schedule clock (armed when first seen), the OAuth token cache, the compiled-pattern cache, in-flight API-destination deliveries and their one retry. When replays become asynchronous (EB-53) the running replay is the process's and the finished record is the application's, as for SQS move tasks |
| L10 the board is the spec | findings | 6a: 24 boards rendered beside real pages; 0 match, 21 differ, 3 cannot match yet |
| L11 five teaching lines | findings | 6a: no EventBridge setting has line 4 or 5 today (the destination form's "stored not enforced" is a fragment of line 4); the table in 6a lists each setting and its five lines |
| L12 icons | **checked** | the rail, the list pane header and the resource header carry `awsIcon "eb"` on every page; the boards' `EB` mark is the placeholder. Kept. The boards draw a two-letter mark per kind in the Targets and Trace rows (`SQ`, `SN`, `λ`, `EB`); the page draws a service-coloured bar and the name (`conn-chip`). Kept as the page has it, and where a mark is drawn it is the service icon, not two letters |

### Classes of mistake this service is most at risk of

1. **Accepted, then dropped.** The service is full of members that are
   validated and then thrown away (SqsParameters, RetryPolicy, DeadLetterConfig,
   RoleArn on targets; Tags at create; LogConfig; two KmsKeyIdentifiers; a
   replay's Description; a supplied `Time`; the filters on three list
   operations) and of targets that are accepted and deliver nothing. The call
   succeeds and the application's test passes. Every finding of this class is
   P0 or P1 because nothing says it happened.
2. **The model's prose contradicts the user guide, and the code follows
   neither.** Array indices (K6), five targets "per request" against ten
   (EB-52), the replay window's end (K35), the model's "must use dot
   notation". Where the two disagree the document records both and the
   recording queue decides; nothing is fixed on one of them alone.
3. **Time.** Delivery, archive capture and replay are asynchronous on AWS and
   immediate here, and the one clock that matters (the event's own `time`) is
   not the clock the service uses (EB-36, EB-37).
4. **Templates and wire fidelity of the delivered body.** The input
   transformer's JSON quoting rules are the most-used feature that is wrong
   (EB-32), and the user guide gives worked examples that become tests as they
   stand.
5. **Generated tables read as complete.** The validation tables come from the
   traits; list lengths and map members are not in them, and the ledger's
   "448 of 448" reads as "everything" (EB-52).
6. **A board that states doze-aws's limitation as a fact about AWS** (Eb-13's
   "no array index", Eb-17's "when each event was archived").
7. **Forms that lose what was typed on a refusal**, and refusals that land
   behind a sticky bar (EB-64, EB-65).

### Decisions this stage needs from a human

D-EB1 **Replays**: asynchronous with a visible STARTING/RUNNING and
one-minute steps (and a rate), or immediate with the difference listed
(EB-53). D-EB2 **DeleteEventBus with rules**: refuse (the model's text) or
keep removing them (EB-4). D-EB3 **PutPermission / RemovePermission**: C
(store, return, 10 KB check) or leave S (EB-56). D-EB4 **Targets that are
local services** (Kinesis, Step Functions): deliver, or refuse, or C and
listed (EB-3). D-EB5 **Quotas**: which of the account quotas (100 buses, 300
rules per bus, 3,000 connections, 3,000 API destinations, 2,048-character
pattern default) are enforced and which are listed (section 2 note).
D-EB6 **The failed-delivery surface** (EB-29): a record the console can show,
or Eb-24 says plainly there is none.

## 1. Summary

Event buses, rules (pattern and schedule), targets, archives and replays,
connections and API destinations, over one bbolt store on one wire protocol
(JSON 1.1, `AWSEvents`). The pattern language lives in `internal/eventpattern`
and is shared with SNS filter policies; schedules use `internal/awscron` and a
one-second ticker.

Peers: it delivers into SQS, SNS, Lambda and CloudWatch Logs as
`events.amazonaws.com`, and makes real HTTP requests for API destinations.
CloudFormation and `provision` create its resources (`internal/provision/svc_events.go`,
`svc_events_http.go`). Nothing calls into it except apps and Lambda destinations.

It is large because the surface is three services in one (routing, archive and
replay, HTTP delivery), because delivery is where its worst defects sit and
none of them is visible to the caller, and because the console group is 24
boards of which none matches its design.

**Estimate:** *Corrected 2026-10-11: was "L, five or more focused days";
re-estimated XL, **15 to 18 focused days**.* The plan had 31 findings; the
review found 34 more and withdrew one, and the console is 24 boards of which
21 differ in shape and 3 cannot be built yet. By work: contract, validation
tables (86 missing constraints) and the behaviour fixes in the API, 4 days; delivery (the
transformer rewrite, dynamic parameters, FIFO and SqsParameters, Kinesis and
Step Functions targets, retry/DLQ decision), 3 days; archives, replays as
asynchronous tasks, paging on seven list operations, 3 days; the console (24
boards, 21 to rebuild in shape, seven confirms, the teaching lines for about
fourteen settings, a shared substitution function and an exported schedule
parser), 5 to 6 days; conformance scenarios, mutation and the restart tests,
2 days. SQS was estimated S at two days and took several times that; this
number follows the rule recorded there (one step up until the model's text
has been read) and is already after that reading. **Actual:** _fill in when
done._

## 2. Contract

The model lists **57** operations (`eventbridge/testdata/ops_eventbridge.json`;
counted again from the model, 57). 40 are dispatched (`eventbridge/actions.go`,
`handlers`); 17 are refused by name (`eventbridge/eventbridge.go`,
`stubActions`) with `UnsupportedOperationException`. *Corrected 2026-10-11: the
refusal is not the protocol's S shape (501 `NotImplemented` and
`X-Doze-Not-Built`): it is HTTP 400 `UnsupportedOperationException` with no
header (run). EB-55.*

| Operations | Now | Target | Note |
|---|---|---|---|
| PutEvents | F | F | A bus given as an ARN is accepted and dropped (EB-1, `model`). *Corrected 2026-10-11: a bus that does not exist is accepted and dropped **as on AWS** (`doc` eb-putevents); was listed as a defect, EB-2 withdrawn.* `Time`, a missing `Detail`, the 1 MB total and `DetailType` 128 are not held (EB-35 to EB-39) |
| PutRule, DescribeRule, ListRules, DeleteRule | F | F | Per-member tier below. No paging (EB-10). `DeleteRule` on a missing bus succeeds (EB-33) |
| EnableRule, DisableRule | F | F | |
| PutTargets, RemoveTargets, ListTargetsByRule, ListRuleNamesByTarget | F | F | Per-member tier below. Any ARN is accepted (EB-3); five per rule not held (EB-11) |
| CreateEventBus, UpdateEventBus, DescribeEventBus, ListEventBuses | F | F | Per-member tier below. `DescribeEventBus` by ARN fails (EB-34) |
| DeleteEventBus | F | F | Removes the bus's rules. Decide (EB-4, `model` text says they must be deleted first) |
| TestEventPattern | F | F | The mandatory event fields are not held (EB-41) |
| TagResource, UntagResource, ListTagsForResource | F | F | Rules and buses only (`model`: "In EventBridge, rules and event buses can be tagged"). Archives, connections and destinations are **not taggable**, so the plan's open question is closed: a TagResource on one is `ResourceNotFoundException`-class (the code is `memory`); the console's "is not the ARN of a rule or an event bus" is `ValidationException` today. 50 per resource not held (EB-40) |
| CreateArchive, DescribeArchive, ListArchives, UpdateArchive, DeleteArchive | F | F | Retention is C. `State` is `ENABLED` at once (EB-54) |
| StartReplay, DescribeReplay, ListReplays | F | F (asynchronous: decision D-EB1) | *Corrected 2026-10-11: was "Synchronous; COMPLETED at once" as the target.* The model has six states and one-minute steps (EB-53) |
| CancelReplay | S | F with EB-53 | *Corrected 2026-10-11: was "S ... In not-built.md". It answers `IllegalStatusException` as AWS does for a finished replay; it is not a stub (K22).* |
| CreateConnection, UpdateConnection, DeauthorizeConnection, DeleteConnection, DescribeConnection, ListConnections | F | F | VPC Lattice connectivity refused by name (`ValidationException` today: EB-55). Update's partial credential refused (EB-48) |
| CreateApiDestination, UpdateApiDestination, DeleteApiDestination, DescribeApiDestination, ListApiDestinations | F | F | Rate limit is C. `http://` accepted, a declared difference (K29) |
| PutPermission, RemovePermission | S | C or F (decision D-EB3) | *Corrected 2026-10-11: the justification "cross-account permissions have no meaning locally" is not what the model says: the bus has a resource policy, `DescribeEventBus` returns it, and the protocol requires it stored, validated and evaluated (EB-56).* |
| ActivateEventSource, DeactivateEventSource, DescribeEventSource, ListEventSources, CreatePartnerEventSource, DeletePartnerEventSource, DescribePartnerEventSource, ListPartnerEventSources, ListPartnerEventSourceAccounts, PutPartnerEvents | S | S | Partner sources. The model's own "not here" answer is `OperationDisabledException` ("not available in this region"); the protocol's answer is 501 |
| CreateEndpoint, DeleteEndpoint, DescribeEndpoint, ListEndpoints, UpdateEndpoint | S | S | Global endpoints (`EndpointEventBusList` is exactly two buses; a Route 53 health check) |

Count: 39 F, 18 S (17 refused by name plus CancelReplay). `not-built.md` lists
the 17 as one grouped row (line 71) and CancelReplay on its own (line 109);
whether the ledger test reconciles them one by one is not established. *With
D-EB3 and EB-53 the S count falls to 15 and 1 respectively; the 17-row group
splits.*

**Quotas, from `doc` eb-quota.html (Custom Event Bus - Classic).** Decision
D-EB5 for each: event buses 100 per account, rules 300 per bus (100 in two
regions), targets per rule **5, not adjustable** (enforce: EB-11), event
pattern size **2,048 characters by default, adjustable** (the model's trait is
4,096 and is what the tables hold: the account default is the lower, listed),
API destinations 3,000, connections 3,000, rules containing wildcards 30 per
bus, concurrent replays **10** (`doc` eb-archive), event bus policy 10,240
characters, PutEvents entries per request 10 (`model`; the quota page's
"1 to 100" is the other bus type). Replays are deleted after 90 days (`doc`).

"The operation is F" hides which members do anything.

**PutRule**

| Member | Now | Target | To confirm |
|---|---|---|---|
| Name, EventBusName, EventPattern | F | F | |
| ScheduleExpression | F | F | Default bus only (`doc` eb-create-rule-schedule: "You can only create scheduled rules using the default event bus"; the model is silent). One message for every malformed expression. Syntax read against eb-scheduled-rule-pattern and run (47 cases: 28 cron, 16 rate, 3 others): matches the page in all of them (one expectation of mine was wrong, not the code) |
| State `ENABLED`, `DISABLED` | F | F | |
| State `ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS` | accepted, rule never fires | F (treated as enabled) | EB-9. *Corrected 2026-10-11: the "or refused" option is gone: the model defines the value and says it is not valid on partner buses.* |
| Description, RoleArn | C | C | Stored and returned. `RoleArn` is F for AWS's own use (`model`: "the IAM role to be used for this target when the rule is triggered"); nothing local assumes it |
| Tags | dropped | F | EB-8 (create only; ignored on update) |

**PutTargets, per target**

| Member | Now | Target | To confirm |
|---|---|---|---|
| Id, Arn (SQS, SNS, Lambda, log group, API destination) | F | F | |
| Arn of Kinesis or Step Functions | accepted, delivers nothing | F (D-EB4) | EB-3. Both are local services; `KinesisParameters.PartitionKeyPath` is `model`-documented ("the default is to use the eventId as the partition key") |
| Arn of any other service, or not an ARN at all | accepted, delivers nothing | refused, or C and listed | EB-3 |
| Input, InputPath, InputTransformer | F | F | Predefined variables, array paths and string quoting: EB-5, EB-6, EB-32. Two at once accepted: EB-12. `Input` "Valid JSON text" not checked (EB-59) |
| HttpParameters | F | F | API destination targets (and API Gateway: not here). Dynamic `$.` values: EB-45. The connection's values win: EB-46 |
| SqsParameters, DeadLetterConfig, RetryPolicy, RoleArn | dropped | F (SqsParameters), C (the rest) | EB-7. `DeadLetterConfig` and `RetryPolicy` are what AWS does with a failed delivery (`doc` eb-rule-dlq, eb-rule-retry-policy); C is honest only if the console says nothing retries here |
| EcsParameters, BatchParameters, KinesisParameters, RunCommandParameters, RedshiftDataParameters, SageMakerPipelineParameters, AppSyncParameters | validated, then dropped | C or refused with the target | `KinesisParameters` with D-EB4; the rest follow the target ARN: none of those services is local |

**Buses, archives, connections, destinations**

| Member | Now | Target | To confirm |
|---|---|---|---|
| Bus Description, KmsKeyIdentifier, DeadLetterConfig | C | C | Stored and returned; the console shows none of them. `UpdateEventBus` clears an omitted one: the handler's comment ("leaves the rest") and its code disagree, and the model is silent (`memory`) |
| Bus LogConfig | **dropped** (K24) | C | EB-50 |
| Bus Tags at CreateEventBus | dropped (`CreateBus(name, nil)`) | F | EB-8 |
| Bus Policy | never set; PutPermission is S | C or F | EB-56. The console's hint says otherwise (EB-24) |
| Archive EventPattern | F | F | Cannot be cleared (EB-14) |
| Archive RetentionDays, Description | C | C | Never expired. *Corrected 2026-10-11: 0 means indefinitely (`model`).* |
| Archive KmsKeyIdentifier | **dropped** (K25) | C | EB-50 |
| Replay window, `Destination.FilterArns` | F | F | The console sends neither. The window is read on the wrong clock (EB-37) |
| Replay Description | **dropped** (K25) | C | EB-50 |
| Connection auth (Basic, API key, OAuth), invocation parameters | F | F | Partial update refused (EB-48); mixed families accepted (EB-49) |
| Connection KmsKeyIdentifier | **dropped** (K25) | C | EB-50 |
| Destination endpoint, method | F | F | |
| Destination InvocationRateLimitPerSecond, Description | C | C | The form already says "stored not enforced". Default 300 (EB-47) |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `eventbridge/`: 32 tests in 14 files (SDK v1 and v2, scheduler, scheduled delivery, API destinations and revoke, log-group target, declared fields, bus refusals, conformance fixes). `internal/eventpattern`: 2 tests. `internal/awscron`: 5. |
| Parity | `rejection_parity_test.go`, `rejection_parity_http_test.go`, `coverage_model_test.go`; ledger says 448 of 448 model constraints enforced across 40 operations, `knownGaps` empty. *Corrected 2026-10-11: true of the cases the generator makes; 86 constraints in the model's input shapes have no table entry (K26, EB-52).* |
| Conformance | `conformance/tests/test_eventbridge.py`: 7 scenarios, all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/eventbridge.spec.ts`: 8 tests |
| Restart | Covered: a rule, a connection with its destination, a cron rule. Not covered: buses and tags, targets and their members, archives with events, replays (K30) |
| Mutation | No per-package baseline yet (foundation B3) |

The model constrains shapes well and behaviour not at all: every rule about
what a bus does with an event is hand-derived. *Corrected 2026-10-11: that
overstates it; the model's prose carries about thirty behaviour statements
(PutRule's replace semantics, DeleteRule, the transformer's dot notation,
PutEvents' requirements, the replay states), and they are quoted in the
findings.* Delivery is `doc` for what AWS does (eb-targets, eb-rule-dlq,
eb-rule-retry-policy, eb-putevents) and was `memory` in this document until
today.

## 4. Findings

Evidence `run` means reproduced against a binary built from this tree on
2026-10-10 or, for the rows added 2026-10-11, the same day.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| EB-1 | P0 | `PutEvents` with `EventBusName` given as the bus's ARN returns an `EventId` and delivers nothing. `putOneEvent` uses the string as the bus name; `busOrDefault`, which strips an ARN, is not called here. More forgiving than AWS (a wrong silent drop). | `eventbridge/actions.go`, `putOneEvent` | run (again 2026-10-11: queue empty) | **model**: PutEventsRequestEntry.EventBusName "The name or ARN of the event bus" | SDK test: rule and queue on `default`, put with the bus ARN, receive one message |
| EB-2 | ~~P0~~ | **Withdrawn 2026-10-11.** `PutEvents` naming a bus that does not exist returns success and drops the event: **that is what AWS does** ("EventBridge will send a 200 response, it will not fail the request or include the event in the FailedEntryCount"). The test pins the behaviour instead. | `actions.go`, `putOneEvent` | run | **doc**: eb-putevents.html, "Handling failures with PutEvents" | SDK test: put to `ghost`, assert `FailedEntryCount` 0 and an `EventId` |
| EB-3 | P0 | `PutTargets` accepts any `Arn`, including `not-an-arn` and a Kinesis stream. `dispatch` then logs "unsupported target service" and delivers nothing; the caller sees success on both calls. Kinesis and Step Functions are local services, so the drop is not forced. More forgiving. | `actions.go`, `putTargets`, `dispatch` | run (accepted), read (no delivery) | **doc**: eb-targets.html lists them as valid targets; refusing a malformed ARN: memory | SDK test: a Kinesis target delivers (or is refused or reported as cosmetic: D-EB4); `not-an-arn` is refused |
| EB-4 | P0 | `DeleteEventBus` on a bus with rules removes the rules and succeeds, and leaves the bus's archives behind pointing at nothing. `SUPPORT.md` states the first half as the contract. More forgiving. | `eventbridge/store.go`, `DeleteBus` | run | **model**: DeleteEventBus "All rules associated with this event bus need to be deleted"; the refusal's code: memory (no error shape in the model for it) | Store test: delete a bus with a rule and an archive; assert the decided outcome |
| EB-5 | P0 | An input transformer leaves `<aws.events.rule-name>`, `<aws.events.rule-arn>`, `<aws.events.event.json>`, `<aws.events.event>` and `<aws.events.event.ingestion-time>` in the output as literal text. The five are reserved: "you can't create variables with these names". | `actions.go`, `shapeInput` | run (2026-10-11: delivered body holds `<aws.events.rule-name>` verbatim) | **doc**: eb-transform-target-input.html, "Predefined variables" | SDK test: template with each of the five, read the delivered body; a user variable with a reserved name refused |
| EB-6 | P0 | A path with an array index (`$.detail.arr[0]`) resolves to an empty string in a transformer, and with `InputPath` the event is dropped. `extractPath` splits on dots only. The model's prose is split (K6). | `actions.go`, `extractPath` | run (2026-10-11: `"first": ,`) | **doc**: eb-transform-target-input.html and eb-targets.html ("array indices, wildcards (*), forward slashes"); model: contradicts itself | SDK test: both forms against an event with an array; recording scenario `transformer_array_path` decides the model's dot-notation sentence |
| EB-7 | P0 | `PutTargets` drops `SqsParameters`, `DeadLetterConfig`, `RetryPolicy` and `RoleArn`: `ListTargetsByRule` returns only `Id` and `Arn`. A FIFO queue target therefore has no `MessageGroupId`; the IaC tools see drift on the rest. Missing. | `store.go`, `target`; `actions.go`, `putTargets` | run (dropped, 2026-10-11), read (FIFO consequence) | **model** (the members; SqsParameters: queue must have content-based deduplication), **doc** eb-targets.html (FIFO: SqsParameters names the group) | Declared-fields test for the four members; SDK test delivering to a FIFO queue with and without content-based dedup |
| EB-8 | P1 | `Tags` on `PutRule` are dropped (`ListTagsForResource` returns none). `Tags` on `CreateEventBus` are dropped too: the handler calls `CreateBus(name, nil)`. Missing. | `actions.go`, `putRule`, `createEventBus` | run (both, 2026-10-11) | **model**: PutRule, CreateEventBus.Tags; on update "any tags you specify ... are ignored" | SDK test: create with tags, list them; update with other tags, list unchanged |
| EB-9 | P0 | A rule put with `State: ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS` is stored and never fires: matching tests `State != "ENABLED"`. Wrong. | `actions.go`, `matchAndDispatch`; `scheduler.go`, `fireDueSchedules` | run | **model**: PutRule.State | SDK test: rule in that state, put a matching event, receive it |
| EB-10 | P1 | No list operation pages. `ListRules` with `Limit: 1` returns every rule and no `NextToken`; none of the handlers reads `Limit` or `NextToken`. A garbage `NextToken` is accepted. More forgiving. | `actions.go`, `archive_actions.go`, `connection_actions.go`, `apidest.go` | run (ListRules, ListEventBuses, ListConnections; 2026-10-11), read (the rest) | **model**: `Limit` 1 to 100, `NextToken` 1 to 2048, "The maximum number of results per page for requests is 100"; an expired token: "HTTP 400 InvalidToken" (text only) | SDK test per list operation with `Limit: 1` over three resources; a token from one query used on another |
| EB-11 | P1 | A rule accepts more than five targets (seven were stored; 101 in one call, 2026-10-11). | `actions.go`, `putTargets` | run | **model**: PutTargets "up to five (5) targets"; `LimitExceededException` in its errors. **doc** eb-quota.html (5, not adjustable) | Parity case with six targets, with six across two calls, and a replacement of an existing Id (not counted) |
| EB-12 | P1 | A target with both `Input` and `InputPath` is accepted; `Input` silently wins. | `actions.go`, `putTargets`, `shapeInput` | run | **model**: "mutually exclusive and optional parameters of a target"; the code: memory | Parity case for each pair |
| EB-13 | P1 | `PutEvents` accepts a `Detail` that is valid JSON but not an object (`[1]`, `123`, `"s"`: all three, 2026-10-11). | `actions.go`, `putOneEvent` | run | **model**: Detail "A valid JSON object"; `MalformedDetail` in the ErrorCode list; the code for non-object: memory | SDK test: entry error for an array and a number |
| EB-14 | P1 | `UpdateArchive` cannot clear a pattern: an empty `EventPattern` is ignored. `DescribeArchive` never returns `StateReason`. | `archive_actions.go`, `updateArchive`, `archiveView` | read | not established: the model does not say whether an omitted member clears (contrast PutRule: "replaced with null values") | SDK test: update with an empty pattern, describe |
| EB-15 | P1 | `StartReplay` does not check that the end is after the start, that the destination is the archive's own bus, or that the destination exists. Each completes with "Replayed 0 event(s)" or replays into another bus. | `archive_actions.go`, `startReplay` | run (2026-10-11: end before start, ghost bus: `COMPLETED`) | **model** + **doc** for the own-bus rule; end-before-start and missing destination: memory | Parity cases for the three |
| EB-16 | P1 | `UpdateEventBus` answers a missing bus with HTTP 404; every other not-found in the package is 400. Messages also differ in case ("Event bus X does not exist." against "event bus X does not exist"). | `actions.go`, `updateEventBus` | run | **model**: no `httpError` trait; awsJson1_1 gives 400 | Parity test asserting status and code for a missing bus on every bus operation |
| EB-17 | P2 | Test event counts a DISABLED rule as a match ("1 of 3 match this event") though Publish skips it. `ebMatch` never reads the rule's state. | `internal/console/handlers_eb.go`, `ebMatch` | run | **model**: DisableRule "A disabled rule won't match any events" | Handler test: disabled matching rule, assert the verdict |
| EB-18 | P2 | A connection's description cannot be changed alone **from the console**. The detail form's select always posts `auth_type` and empty credential boxes, so `UpdateConnection` is refused ("authParameters.basicAuthParameters.password ... length greater than or equal to 1"). The API accepts a description-only call, as the model allows. | `templates/eb_http.html`, `eb_connection_detail`; `client_eb_http.go`, `UpdateConnection` | run in a browser (2026-10-11, shot `real-Eb-20-Connection-desc-only`) | **model**: UpdateConnectionRequest requires only `Name` | Browser test: edit the description, save, read it back |
| EB-19 | P2 | Add target sends only `Id` and `Arn`. A destination whose endpoint has `*`, which the destination form invites, never delivers from a console-made target. | `handlers_eb.go`, `ebAddTarget`; `apidest_deliver.go`, `buildRequest` | read | n/a | Handler test: add a `*` destination with a path value, deliver to a local server |
| EB-20 | P2 | Add target offers queues, functions and API destinations only. SNS topics and log groups are deliverable and absent. | `templates/eb.html`, `eb_targets` | read, run (2026-10-11: the select has three kinds) | n/a | Browser test: a topic is in the list |
| EB-21 | P2 | Create rule with a name that exists replaces the pattern and schedule, re-enables a disabled rule, keeps targets and tags, and toasts "created". The API is an upsert by design; the form says nothing. | `handlers_eb.go`, `ebCreateRule`; `store.go`, `PutRule` | run | **model**: PutRule "the rule is replaced ... omitted ... replaced with null values" | Browser test: the form names the existing rule before submit |
| EB-22 | P2 | The rule page discards `Input`, `InputPath`, `InputTransformer` and `HttpParameters`, which `ListTargetsByRule` returns. A target that reshapes or drops events looks like any other. | `client_eb.go`, `GetRule` | read | n/a | Client test: the fields survive into `Target` |
| EB-23 | P2 | The archive editor cannot clear a pattern or set retention back to 0: the client sends each only when non-empty or above zero. | `client_eb.go`, `UpdateArchive` | read | n/a | Handler test, after EB-14 is decided |
| EB-24 | P3 | The Trace tab promises "the bus's own record, including its resource policy". Nothing can set a bus policy **because PutPermission is a stub** (EB-56). The same handler's refusal mentions a "stream", which is not a deliverable target (it is a valid AWS target and a local service: D-EB4). | `templates/eb.html`, `eb_trace`; `handlers_eb.go`, `ebRulesByTarget` | read | n/a | Template test on the two strings |
| EB-25 | P2 | The Replays panel on a bus lists every replay on the machine. | `client_eb.go`, `ListReplays`; `handlers_eb.go`, `ebBus` | read | n/a | Handler test with replays on two buses |
| EB-26 | P3 | Every rule page says "nothing feeds this yet". *Corrected 2026-10-11: no longer so; the rule page shows only "<rule> drains to ..." and no fed-by segment (run).* The design wants "fed by events put on the bus" or "the clock". | `templates/panes.html`, `connections` | run | n/a | Template test for a rule node |
| EB-27 | P3 | The pattern builder's "Test an event" placeholder is pattern-shaped (`"source":["orders"]`); the field takes an event. Run 2026-10-11: it reads `{"source":["orders"],"detail-type":["OrderPlaced"],"detail":{}}`, an event with arrays in it, which TestEventPattern treats as an event whose `source` is a list. | `templates/eb.html`, `pattern_builder` | read, run | n/a | None; fixed with the board |
| EB-28 | P2 | An INACTIVE destination's update form preselects the first connection listed, because none matches the deleted one. Pressing Update re-points it without a choice having been made. Run 2026-10-11: the select shows `carrier-key · API_KEY` on a destination whose connection was deleted. | `templates/eb_http.html`, `eb_destination_detail` | run | n/a | Browser test: the select starts empty |
| EB-29 | P2 | A delivery that fails is visible nowhere in the console. Publish toasts "Event published"; the target chip still links to a deleted queue; the only record is the process log. `SUPPORT.md` says "A target that fails fails inside your `PutEvents` call", which is not what happens: `PutEvents` returns success. *Corrected 2026-10-11 (K19): `PutEvents` returning success is AWS's behaviour; the sentence is what is false.* | `actions.go`, `dispatch`; `docs/SUPPORT.md` | run (deleted queue, enforce denial, INACTIVE destination, unmatched InputPath) | **doc**: eb-putevents.html; eb-rule-retry-policy.html (24 hours, 185 attempts, then dropped or DLQ) | SDK test pinning what `PutEvents` returns when a target fails; then correct the sentence |
| EB-30 | P3 | The delete-archive, delete-rule and remove-target confirms were read and are true. The bus confirm is false and is foundation A13. *2026-10-11: and now the **model** says the confirm's sentence ("Its rules must be deleted first") is the rule AWS has (EB-4); it is the code that is wrong, not only the sentence.* | `templates/eb.html` | read | **model**: DeleteEventBus | Covered by A13's rule, after D-EB2 |
| EB-31 | P1 | `ListRules` on an event bus that does not exist answers an empty list. AWS answers `ResourceNotFoundException`. More forgiving than AWS, and it is why the console drew a bus page for `/eb/no-such-bus`; the console now checks the bus list itself. Also: `ListRuleNamesByTarget` (run, 2026-10-11) and `ListArchives` with an `EventSourceArn` naming no bus answer empty, and the model lists `ResourceNotFoundException` for both. | `eventbridge/store.go` `Rules`; `eventbridge/actions.go` `listRules` | run | **model**: ListRules, ListRuleNamesByTarget, ListArchives errors | Rejection-parity case: each of the three with a bus name that was never created |

### Found 2026-10-11, by reading the model's documentation, the user guide and the board beside the page

Each row was reproduced or read before it is written here; the test to write
first is the red test of protocol step 2. Direction is named on every row.

| Id | Sev | What | Direction | Where | Evidence | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| EB-32 | P0 | The input transformer inserts a string unquoted where the template is a JSON object: `{"instance": <instance>}` delivers `{"instance": i-1}`, which is not JSON. AWS quotes string values for you and does not quote objects or arrays. | wrong shape | `actions.go` `shapeInput`, `renderScalar` | run (2026-10-11, SQS body) | **doc** eb-transform-target-input.html: "EventBridge automatically adds quotes to string variable values during transformation, to ensure the transformation output is valid JSON. EventBridge does not add quotes to variables that represent JSON objects or arrays" | The page's own table as test cases: "Simple string", "String with escaped quotes", "Simple JSON", "JSON with strings and variables", "a mix of variables and static information", reserved variables in JSON and in a string |
| EB-33 | P1 | `DeleteRule` on an event bus that does not exist succeeds. | more forgiving | `store.go` `DeleteRule` (no bus check) | run | **model**: DeleteRule "When you call delete rule for a non-existent custom eventbus, ResourceNotFoundException is returned" | Parity case; the repeat-delete case stays a success |
| EB-34 | P1 | `DescribeEventBus` given the bus's ARN answers "event bus arn:... does not exist"; the member is `EventBusNameOrArn` and every rule-side operation already strips an ARN. (`DeleteEventBus` and `UpdateEventBus` take a name only: right.) | stricter | `actions.go` `describeEventBus` | run | **model**: DescribeEventBusRequest.Name "The name or ARN of the event bus to show details for" | SDK test: describe by name and by ARN |
| EB-35 | P1 | A `PutEvents` entry without `Source` or `DetailType` is `ValidationException`; the model lists `InvalidArgument` ("A specified parameter is not valid") and the entry codes `MalformedDetail`, `InvalidAccountIdException`, `NotAuthorizedFor...`. A missing `Detail` is accepted as `{}`, though "Detail, DetailType, and Source are required ... EventBridge fails that entry"; and when **no** entry has all three "EventBridge fails the entire request" (here: 200 with per-entry errors). | wrong code, more forgiving | `actions.go` `putOneEvent` | run | **model**: PutEventsRequestEntry, PutEventsResultEntry.ErrorCode; the whole-request code is not in the model (PutEvents errors: InternalException): memory | SDK test: each missing member, one bad entry among good, all entries bad |
| EB-36 | P0 | A supplied `Time` is discarded: the event's `time` is the put time. `If no time stamp is provided, the time stamp of the PutEvents call is used` means a supplied one is kept. Patterns on `time`, archives and replays all read it. | wrong | `actions.go` `putOneEvent` (`"time": awshttp.ISO8601(s.now())`) | run (Time 2020-01-01 delivered as 2026-10-11) | **model**: PutEventsRequestEntry.Time "per RFC3339. If no time stamp is provided, the time stamp of the PutEvents call is used" | SDK test: put with a Time, receive it on a queue and match a `time` prefix pattern |
| EB-37 | P0 | An archive stores each event under the time it **arrived**, and a replay's window is applied to that, not to the event's `time`. An event put with `Time` in 2020 and a replay over 2020 replays 0. The end bound is inclusive here; the model says "prior to". | wrong | `archive_actions.go` `captureToArchives`; `archive.go` `ReplayEvents` | run | **model**: Replay.EventStartTime "determined by the time in the event as described in Time"; DescribeReplay "A replay processes events to replay based on the time in the event"; end: Replay.EventEndTime "prior to" against StartReplay "between" (unsettled: K35) | SDK test: Time in the past, archive, window around it, count; the boundary event |
| EB-38 | P1 | The PutEvents request size is not held: 300 KB and 1.1 MB entries are accepted. AWS: the sum over the request is less than 1 MB (1,048,576 bytes); counted as 14 for `Time` when given, plus the UTF-8 bytes of `Source`, `DetailType`, `Detail` and each resource. Not the 256 KB the plan had. | more forgiving | `actions.go` `putEvents` | run | **model**: "the total entry size must be less than 1MB"; **doc** eb-putevents.html "Calculating PutEvents event entry size"; the error code: memory | Table test of the byte count; 1,048,575 and 1,048,576 |
| EB-39 | P2 | `DetailType` of 129 characters and `Detail` nested past 1,000 levels are accepted. | more forgiving | `actions.go` `putOneEvent` | run (129) | **model** (text, no trait): DetailType "a maximum of 128 characters"; PutEvents "only process nested JSON up to 1000 levels deep" | Entry-error cases for 129 and 1,001 |
| EB-40 | P1 | `TagResource` accepts a 51st tag and an `aws:` key. A tag on an archive, connection or destination ARN is `ValidationException` here; those cannot be tagged on AWS (`model`: "rules and event buses can be tagged"). | more forgiving | `actions.go` `tagResource` | run (51 tags accepted) | **model**: TagResource "as many as 50 tags with a resource"; **doc** eb-tagging.html (`aws:` prohibited; characters letters, digits, spaces and `. : + = @ _ / -`). The code for the 51st: memory (TagResource's errors have no limit shape) | Parity cases: 51 tags (one call and two), `aws:x`, a bad character |
| EB-41 | P1 | `TestEventPattern` accepts an event without the mandatory fields, answers `false` for `[1]`, and `ValidationException` for text that is not JSON. | more forgiving | `actions.go` `testEventPattern` | run | **model**: TestEventPatternRequest.Event "the following fields are mandatory: id account source time region resources detail-type" | SDK test: each field missing; the code for it is memory |
| EB-42 | P1 | `anything-but` with a **list** under `prefix`, `suffix`, `wildcard` or `equals-ignore-case` is refused as `InvalidEventPatternException` ("prefix expects a string"). All four are valid. | stricter | `internal/eventpattern/eventpattern.go` | run | **doc** eb-create-pattern-operators.html ("Anything-but matching on prefixes", "on suffixes", "using wildcards", "ignoring case": each with a list) | Table test from the page's own examples |
| EB-43 | P1 | Wildcard escapes: `\*` and `\\` should mean the literal characters; `a\*b` does not match `a*b` here. `**` is accepted ("consecutive wildcard characters are not supported") and `\n` is accepted ("escaping other characters is not supported"). | wrong, more forgiving | `internal/eventpattern/eventpattern.go` | run | **doc** eb-create-pattern-operators.html, "Matching using wildcards". The refusal's code is InvalidEventPatternException by the page's complexity paragraph | Table test |
| EB-44 | P2 | Complexity limits are not modelled: more than 1,000 `$or` combinations, a numeric value outside plus or minus 2^53 (the page says "EventBridge rejects the rule at creation time"), more than 30 rules with wildcards per bus. | more forgiving | `internal/eventpattern/eventpattern.go`; `store.go` | run (the first two) | **doc** eb-create-pattern-operators.html, eb-quota.html. Each one's refusal is `InvalidEventPatternException` for PutRule, CreateArchive, UpdateArchive and TestEventPattern (`doc`) | Table cases; a rule count test |
| EB-45 | P1 | Dynamic path parameters are sent as literal text: a target's `HttpParameters.PathParameterValues` of `$.detail.orderId` goes into the URL as `$.detail.orderId`. The same reaches header and query values, and `SqsParameters.MessageGroupId`. | missing | `apidest_deliver.go` `buildRequest`; `actions.go` `dispatch` | read | **doc** eb-targets.html, "Dynamic path parameters": "The entire value must be a JSON path ... EventBridge replaces these paths at runtime with data from the event payload"; not from a transformed value | Delivery test to a local server, value `$.detail.id`; a value that is only part path is literal |
| EB-46 | P1 | API destination parameter precedence is reversed: the connection's header and query values are set first and the target's second, so the target wins. AWS: "In case of any conflicting keys, values from the Connection take precedence". | wrong | `apidest_deliver.go` `buildRequest` | read | **model**: Target.HttpParameters; CreateConnection.InvocationHttpParameters "the parameters added for the connection take precedence" | Delivery test with a clashing header and query key; a body key (connection already wins) |
| EB-47 | P2 | `DescribeApiDestination` and the list return no `InvocationRateLimitPerSecond` when none was sent; AWS reports 300. `UpdateApiDestination` cannot clear one. The console's field is `min="0"` against the model's 1. | missing | `apidest.go` `destinationView`, `createApiDestination` | run | **doc** eb-create-rule-schedule.html ("By default, this value is set to 300"), eb-quota.html; **model** range min 1 | SDK test: describe after create without a limit |
| EB-48 | P1 | `UpdateConnection` with `BasicAuthParameters` holding only `Username` is refused ("BasicAuthParameters need Username and Password"); every member of `Update*AuthRequestParameters` is optional in the model. An update that gives an auth type but no parameters succeeds and keeps the credential. `InvocationHttpParameters` is replaced by "nothing" when a re-authorize omits it. | stricter | `connection.go` `parseAuthParameters` (shared with create) | run | **model**: UpdateConnectionBasicAuthRequestParameters (no `required`), UpdateConnectionRequest ("Updates settings for a connection"); the omit-means-keep semantics: memory | SDK test: username only; password only; description only; invocation parameters omitted |
| EB-49 | P2 | `CreateConnection` accepts `ApiKeyAuthParameters` beside `BasicAuthParameters` and ignores the second. | more forgiving | `connection.go` `parseAuthParameters` | run | **model**: CreateConnectionAuthRequestParameters "You must include only authorization parameters for the AuthorizationType you specify" (code: memory) | Parity case |
| EB-50 | P1 | Members accepted and **dropped**: `LogConfig` (bus), `KmsKeyIdentifier` (archive and connection), `Description` (replay). The `CreateEventBus` response carries only the ARN (the model returns Description, KmsKeyIdentifier, DeadLetterConfig, LogConfig); `DescribeEventBus` has no `CreationTime`, `LastModifiedTime`, `LogConfig`, `ManagedBy`; `DescribeConnection` no `StateReason` and a `LastAuthorizedTime` that moves on a description edit; `DescribeReplay` returns `"FilterArns": null`. | missing, wrong shape | `actions.go`, `archive_actions.go`, `connection_actions.go` | run | **model**: the request and response shapes | Declared-fields test per member (the existing `declared_fields_test.go` shape) |
| EB-51 | P1 | List filters are ignored: `ListEventBuses` `NamePrefix`; `ListArchives` `State`; `ListReplays` `State` and `EventSourceArn`. The model: "Filter parameters are exclusive". | more forgiving | `actions.go`, `archive_actions.go` | run | **model**: ListArchives, ListReplays, ListEventBuses | SDK test per filter; the exclusive rule's refusal is memory |
| EB-52 | P1 | 86 constraints the model states for the dispatched operations have no entry in `constraintTables`: 52 lengths (every **list** length: `Targets` 1 to 100, `Ids` 1 to 100, `InputPathsMap` up to 100, header, query and body parameter lists up to 100; map entry lengths), 21 patterns (header, query and body parameter keys and values; the seven `Description` patterns of `.*` are not counted), 8 required (nested members, e.g. `RunCommandTargets[].Key`), 3 enums, 2 ranges. 101 targets, 101 ids, 101 path pairs and 101 header parameters are accepted. The ledger's "448 of 448 ... knownGaps empty" is true of the generated cases only. `PutEvents`' 1 to 10 is enforced by hand with the message "PutEvents accepts 1-10 entries". | more forgiving | `eventbridge/validate.go`; `dzaudit cases` | run (four lengths), the walk (`scratchpad/eb2` comparison) | **model**: the traits | A test that walks the model and the tables and lists the difference; then a case per member |
| EB-53 | P1 | Replays are synchronous: `COMPLETED` before `StartReplay` returns, `STARTING`, `RUNNING` and `CANCELLING` are never seen, `CancelReplay` can never succeed, and 10 concurrent replays (`LimitExceededException`) is not modelled. Replays are made in one-minute steps by event time on AWS. **Decide (D-EB1):** asynchronous with a visible start-up and a step per minute-of-event-time at a rate, one record that persists and a running one that does not (as SQS-29), `CancelReplay` cancels; or immediate with the difference listed. A test that asserts the replayed messages straight after `StartReplay` passes here and races there. | more forgiving | `archive_actions.go` `startReplay`, `cancelReplay` | read, run | **model**: ReplayState; StartReplay, DescribeReplay; CancelReplay/IllegalStatusException "a replay can be canceled only when the state is Running or Starting"; **doc** eb-archive.html (10 concurrent), eb-replay-cancel.html | After the decision: a state-sequence test and a cancel test; or the listed difference |
| EB-54 | P2 | The other asynchronous states and windows, each **decided immediate, with the difference listed** unless D-EB1 is read the other way: archive `CREATING` (ENABLED at once; the doc says allow ten minutes before replaying), connection `CREATING`, `AUTHORIZING`, `DEAUTHORIZING` (AUTHORIZED, DEAUTHORIZED at once; DeleteConnection returns the literal `DELETING` where the model's text says "the state of the connection before it was deleted"), rule and target changes ("Allow a short period of time for changes to take effect"), and delivery itself (in the PutEvents call, in order, once; AWS: separate, retried, at least once). `EventCount` and `SizeBytes` reconcile within 24 hours on AWS and are exact here. Replays deleted after 90 days: not modelled. | more forgiving | several | read, run | **model** + **doc** eb-archive.html | A listed-difference sentence per item in `SUPPORT.md`; the `DELETING` literal gets a recording scenario |
| EB-55 | P1 | The 17 refused-by-name operations answer HTTP 400 `UnsupportedOperationException` with no `X-Doze-Not-Built`; the connectivity (VPC Lattice) refusal is `ValidationException`. The protocol (D5) is 501 `NotImplemented`. | wrong code | `eventbridge.go` `ServeHTTP`; `connection.go` | run | protocol step 1 | The model sweep's baseline moves the 17 from `legacystub` to `notbuilt` |
| EB-56 | P1 | The bus's resource policy is the one feature of the model the service has no answer to: `PutPermission` (`Action`, `Principal`, `StatementId`, `Condition`, or `Policy`), `RemovePermission` (`StatementId`, `RemoveAllPermissions`), `DescribeEventBus.Policy`, "cannot exceed 10 KB", `PolicyLengthExceededException`. `AWS::Events::EventBusPolicy` and Terraform's event permission use it. **Decide (D-EB3).** | missing | `eventbridge.go` `stubActions`; `store.go` `bus.Policy` (read but never set) | read, run | **model**: PutPermission, RemovePermission, DescribeEventBus; **doc** eb-event-bus-perms.html | If C or F: round trip a policy; refuse a document over the limit and one that does not parse |
| EB-57 | P2 | ARNs are resolved by their last segment: a target ARN with another account or region delivers to the local queue of that name (run); a connection ARN's id is ignored (`destinationConnection`); a replay's `EventSourceArn` is read as an archive name. The SQS-23 class. | more forgiving | `actions.go` `dispatch`; `apidest.go`; `archive_actions.go` | run (target) | **model**: the ARN patterns | Target ARN with account `111122223333`: not delivered locally |
| EB-58 | P1 | Under enforce the IAM resource for every EventBridge operation is `arn:...:rule/<Name or EventBusName>`: `CreateEventBus` and `DescribeEventBus` resolve to a rule ARN; a rule on a custom bus resolves to `rule/<rule>`, not `rule/<bus>/<rule>`; `PutTargets` (the member is `Rule`, which is not read) resolves to the bus name when one is given and to nothing otherwise; `PutEvents` resolves to nothing (the bus is inside `Entries[]`). A policy scoped to `event-bus/*` or `rule/mybus/*` is evaluated against the wrong resource. | wrong | `internal/iamguard/resolve.go` `resourceRules["eventbridge"]` | read | **model**: ARN formats (`event-bus/<name>`, `rule/<bus>/<name>`); reproduce under enforce first | Enforce test with a policy on `event-bus/mybus` and one on `rule/mybus/*` |
| EB-59 | P2 | `Target.Input` "Valid JSON text" is not checked: `not json` is accepted and delivered as the body. | more forgiving | `actions.go` `putTargets` | run | **model**: Target.Input; the code: memory | Parity case |
| EB-60 | P1 | CloudFormation: `State: ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS` is read as disabled (`strings.EqualFold(state, "ENABLED")`); `Tags`, `RoleArn`, `SqsParameters`, `DeadLetterConfig`, `RetryPolicy` of a rule's targets are dropped; a target that is not SQS, SNS, Lambda or an API destination fails the stack ("doze-aws delivers to SQS, SNS, Lambda and API destinations only") while `PutTargets` accepts the same ARN. `AWS::Events::EventBus` has a Ref mapping and no mapper (CFN-3, below); `AWS::Events::Archive`, `EventBusPolicy` and `Endpoint` have no mapping at all and what a stack that declares one does is not established. | wrong, missing | `internal/cfn/mapper.go:846`, `resources.go:465` | read | **model**: PutRule.State | CloudFormation test with each state; a stack with an Archive |
| EB-61 | P3 | API destination requests differ from the documented ones: retried codes are 401, 407, 409, 429 and 5xx (here 429, 5xx and an OAuth 401); `User-Agent` is Go's (AWS: `Amazon/EventBridge/ApiDestinations`); the 33 headers AWS removes (`Cache-Control`, `Origin`, `Range`, ...) and the two it will not let a target override are not handled; `Retry-After` is not read. One retry is the declared budget. | wrong | `apidest_deliver.go` | read | **doc** eb-api-destinations.html | Delivery test with a clashing `User-Agent` and a 409 |
| EB-62 | P3 | The CloudWatch Logs target writes the shaped event with a timestamp of now; the user guide: without an InputTransformer "the event payload is used as the log message, and the source of the event as the timestamp"; with one "the template must be `{"timestamp":<timestamp>,"message":<message>}`". | wrong | `actions.go` `dispatch` (`:logs:` branch) | read | **doc** eb-targets.html, "CloudWatch Logs groups as targets" | Log-group target test with and without a transformer |

### Console findings from rendering the pages (2026-10-11)

| Id | Sev | What | Where | Evidence | Test to write first |
|---|---|---|---|---|---|
| EB-63 | P3 | Patterns show `<` for `<` (`>` and `&` likewise): the Rules list's Match cell, the rule page's `pattern.json` and its copy button. `prettyJSON` re-marshals with HTML escaping. | `internal/console/client_eb.go` `prettyJSON` (`json.MarshalIndent`) | run (shot `real-Eb-05-Rules`: `detail={level:[{numeric:[<…`) | Handler test: a numeric `<` pattern on the rule page |
| EB-64 | P2 | A refusal on a create form **resets the form**: `@htmx:after:request="$el.reset()"` runs on a failed request too. The typed name, client id and secret are gone, the Authorization select falls back to Basic's fields, and the archive form's flex row squeezes its input to a sliver beside the callout. Eb-21 promises "What you typed is kept, secrets included". | `templates/eb_http.html` (both create forms), `templates/eb.html` (`eb_archives`) | run (shots `real-Eb-21-Connection-Create`, `real-Eb-14-Archives-refused`) | Browser test: refuse, assert the value is still there |
| EB-65 | P2 | The refusal on Create rule renders below the sticky action bar and is hidden: at 1440 by 900 only "Refused InvalidEventPatternException" shows and the message is behind the bar until the page is scrolled. The design puts the refusal above the form. | `templates/panes.html` `fail_inline`; `create.html` `form_actions` (sticky) | run (shot `real-Eb-09-Rule-Create-Refused`) | Browser test: the refusal is in the viewport after the click |

Owned by the foundation, listed so they are not lost: the hard-coded account
and region in `busARN`, `archiveARN` and the test event (**A8**, where the
archive capturing nothing was reproduced; `handlers_eb.go` `busARN` and
`ebMatch` read: still literals); the unreachable `copy.go` lines (**A7**); the
bus-delete confirm sentence (**A13**); the "because AWS does" line under
hand-derived refusals such as "invalid event bus name" and "event pattern must
have at least one field" (**A2**: both messages are doze's, and whether AWS
refuses `{}` is `memory`).

**Moved into this stage from CloudFormation (CFN-3, P0, run).**
`AWS::Events::EventBus` is mapped, reports CREATE_COMPLETE and makes no bus.
It keeps its id and is closed here, because EventBridge is not done while a
stack that declares a bus makes nothing. Acceptance: a stack with a bus and a
rule on it leaves both, readable through `DescribeEventBus` and `DescribeRule`;
the plan's README says why it moved. *2026-10-11: `internal/cfn/mapper.go`
maps Rule, Connection and ApiDestination only; the Ref mapping for the bus is
`resources.go:465`.*

## 5. What to check

Beyond the common criteria. *Nothing is ticked: the 2026-10-11 review did not
implement anything. Lines changed by the review are marked.*

**Buses**
- [ ] Name rules: *Corrected 2026-10-11: settled by the model (K32): a custom
      bus's name cannot contain `/` and cannot be `default`; the model's shape
      pattern allows `/` because partner bus names may.* The backslash and
      space refusals are `memory` and the shape pattern holds them out.
- [ ] `default` cannot be created or deleted; creating a name twice is
      `ResourceAlreadyExistsException` (`model`: in CreateEventBus's errors).
      The code for `default` is `memory`.
- [ ] A name starting `aws.` is reserved on AWS (memory). Establish and decide.
      The model only says partner bus names must match the partner event
      source (`^aws\.partner(/...)`).
- [ ] Delete with rules, with targets, with an archive (EB-4; the model:
      rules "need to be deleted"). Delete of a missing bus succeeds (`model`:
      no `ResourceNotFoundException` in DeleteEventBus's errors).
- [ ] Describe and update on a missing bus: status, code, message (EB-16);
      describe by ARN (EB-34).
- [ ] Every C member round-trips through create, update and describe, and a
      member omitted on update is cleared, as the handler's comment claims
      (the model is silent: `memory`).

**Rules**
- [ ] Name 1 to 64 (`model`); one of pattern or schedule required; both
      together: *Corrected: allowed and both fire (K33)*.
- [ ] `PutRule` on an existing name: *Corrected: the rule is replaced, omitted
      members become null, tags in the call are ignored (`model`, K17);
      targets are untouched; state goes back to the default.*
- [ ] Every state value (EB-9).
- [ ] `DeleteRule` with targets is refused (`model`: "you must remove all
      targets"; the code `memory`); on a missing rule succeeds (`model`); on a
      missing bus is `ResourceNotFoundException` (EB-33, `model`); `Force` on a
      managed rule: no managed rules exist locally. *But `doc` eb-archive: an
      archive creates a managed rule on its bus; listing it is not modelled
      (P3, recording queue).*
- [ ] Enable and disable on a missing rule (`model`: `ResourceNotFoundException`).
- [ ] `ListRules` with `NamePrefix`, `Limit`, `NextToken` (EB-10, EB-31).

**Patterns**
- [ ] Every operator in `internal/eventpattern`, alone and inside
      `anything-but`, against strings, numbers, booleans, null and arrays.
      *The review ran 39 cases from `doc` eb-create-pattern-operators; 27
      agree; ten disagree and are EB-42, EB-43, EB-44; two are not
      established (`exists` on an intermediate node, and an array of objects
      in the event against a nested pattern: neither is described on the pages
      read; recording queue).*
- [ ] `$or` at the top level and nested; an empty `$or` (refused, matches the
      page's "at least one"); more than 1,000 combinations (EB-44).
- [ ] A string never matches a number and the reverse (`memory`; the arrays
      page says only that an array matches on a non-empty intersection).
- [ ] `numeric` with one bound, two bounds, and an unknown operator; plus or
      minus 2^53 (EB-44).
- [ ] `cidr` for IPv4 and IPv6 (run: both match the page).
- [ ] The 4096-character limit (`model`), the account default of 2,048
      (`doc`, listed), and that `TestEventPattern` and `PutRule` refuse the
      same patterns with the same code.
- [ ] `TestEventPattern` requires the event's mandatory fields (`model`,
      EB-41). *Corrected: was memory.*
- [ ] The text of `InvalidEventPatternException`: AWS prefixes a fixed phrase
      (memory). Establish, because SDK callers match on it.

**Schedules**
- [ ] `rate`: singular for 1, plural otherwise, minute, hour, day, zero
      refused (`doc` eb-scheduled-rule-pattern; run: 16 rate cases agree).
- [ ] `cron`: six fields, exactly one `?`, `L`, `W`, `#`, names, ranges that
      wrap, steps, year 1970 to 2199 (`doc`; run: 28 cron cases agree; one
      expectation of mine was wrong, not the code). "Cron expressions that
      lead to rates faster than 1 minute are not supported" (a test).
- [ ] A schedule on a custom bus is refused (`doc`; the message is memory).
- [ ] First fire after arming; a disabled rule loses its clock; a restart does
      not replay what was missed (`doc`: "A rate expression starts when you
      create the scheduled event rule").
- [ ] The scheduled event's shape: `source`, `detail-type`, `resources`,
      `detail` (memory; the user guide pages read do not give it).

**Events**
- [ ] 1 to 10 entries (`model`); `Source`, `DetailType` and `Detail` required
      per entry (EB-35); `Detail` an object (EB-13); *the 1 MB request size
      (EB-38). Corrected: was "256 KB (doc)".*
- [ ] A bus by name, by ARN (EB-1), missing (accepted and dropped: `doc`).
- [ ] `Resources`, `Time` and `TraceHeader` in the delivered event; *a
      supplied `Time` is ignored today (EB-36, `model`).* `TraceHeader` is
      dropped (the model: an X-Ray header; what a target sees is `memory`).
- [ ] Per-entry failure does not fail the batch (`doc` eb-putevents:
      "By default, if an individual entry within a request fails, EventBridge
      continues processing the rest").

**Targets**
- [ ] Id 1 to 64; the same Id replaces; five per rule (EB-11).
- [ ] Each ARN kind: delivered, refused, or cosmetic (EB-3).
- [ ] Input, InputPath, InputTransformer: one at most (EB-12); a path that
      finds nothing (`doc`: "that variable isn't created and won't appear in
      the output": what that makes of the template is `memory`); array indexes
      (EB-6); predefined variables (EB-5); how a string, a number, an object
      and null render in a template (EB-32 and the page's examples).
- [ ] A FIFO queue target with and without `SqsParameters` (EB-7).
- [ ] `RemoveTargets` with an Id that is not there: `FailedEntries` or not
      (`memory`).
- [ ] `ListRuleNamesByTarget` on the right bus only; on a missing bus
      (EB-31).
- [ ] What `PutEvents` returns when a target fails: success (`doc`, EB-29).

**Archives and replays**
- [ ] Archive name 1 to 48 (`model`), unique across buses (`memory`); a
      pattern; no pattern ("all events are sent ... except replayed events",
      `model`).
- [ ] Capture: only real `PutEvents`, not replays (`model`); scheduled events:
      establish (a scheduled rule fires its targets directly, not through the
      bus: so not archived here).
- [ ] Update: change and clear the pattern (EB-14), change retention. The
      source bus cannot change (`doc`; there is no member for it).
- [ ] Delete removes the events; replays already recorded stay (`doc`: replays
      are deleted after 90 days, not on archive deletion).
- [ ] Replay: *window by event time (EB-37); the end bound (K35)*,
      `FilterArns`, a name in use (`model`: `ResourceAlreadyExistsException`),
      a missing archive, the checks in EB-15; ten concurrent (EB-53).
- [ ] *Corrected: a replayed event carries `replay-name` (`doc`, K36).*

**Connections and API destinations**
- [ ] Each auth type's required members; secrets never returned by describe or
      list (`model`: the response structures carry names only).
- [ ] `!`-marked parameters returned without a value (console convention;
      `model`: `IsValueSecret`).
- [ ] Update with only a description keeps the credential and the state
      (EB-48; `model` allows it; keeping is `memory`).
- [ ] Deauthorize, then update, returns to AUTHORIZED.
- [ ] Delete a connection: its destinations go INACTIVE; re-pointing
      reactivates (code; not stated by the model or the pages read: `memory`).
- [ ] Delivery: `*` filled in order (EB-45 for dynamic values); an unfilled
      `*`; header, query and body merge order (EB-46: the connection wins);
      Basic, API key and bearer; token cached until expiry and forgotten on
      update (`doc`: "OAUTH tokens are refreshed when a 401 or 407 response is
      returned"); one retry (declared; EB-61).
- [ ] OAuth with `OAuthHttpParameters`; the method is GET, POST or PUT
      (`model`; run: DELETE refused with the enum message).

**Access**
- [ ] Under enforce, delivery to SQS, SNS and Lambda needs the destination's
      policy to admit `events.amazonaws.com` with the rule's ARN as
      `aws:SourceArn`; under soft it is delivered and logged (`model`: "For
      Lambda and Amazon SNS resources, EventBridge relies on resource-based
      policies"; `doc` eb-rule-dlq shows the SourceArn condition). The IAM
      resource of EventBridge's own operations: EB-58.

**Lifecycle**
- [ ] Buses, rules, targets, tags, archives with their events, replays,
      connections with their secrets, and destinations survive a restart (the
      existing tests cover three of the nine: section 3).

## 6. Console

The design's "EventBridge" page: 24 boards. Templates: `templates/eb.html`,
`templates/eb_http.html`, the `eb_bus_create` and `eb_rule_create` blocks in
`create.html`, shared pieces in `panes.html` and `layout.html`. Handlers:
`handlers_eb.go`, `handlers_eb_http.go`, `ebRuleCreatePage` in
`handlers_create.go`. Routes: `ebRoutes` in `routes_messaging.go`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Eb-Home | A fresh stack: the default bus, no rules | `eb_bus`, `eb_rule_list` | exists; the note under Rules and "The number is rules." are missing (6a) |
| Eb-02-Bus-Create | Create event bus | `eb_bus_create` | form exists; the ARN preview, "what an app sends" and the teaching lines are **new** |
| Eb-03-Bus-Create-Refused | Refused inline | `fail_inline` | exists; shape differs (6a) |
| Eb-04-Bus-New | A new custom bus | `eb_bus` | exists; the note is missing |
| Eb-05-Rules | The default bus's rules | `eb_rule_list` | exists; the Targets column is **new** |
| Eb-06-Test-Event | Verdicts, publish, invalid JSON | `eb_bus`, `eb_rule_list`; `ebMatch`, `ebTestEvent` | exists; "would deliver to" and `match · disabled` are **new** |
| Eb-07-Rule-Create | Create rule, pattern | `eb_rule_create`, `pattern_builder` | exists; "the pattern in words" and the taken-name notice are **new** |
| Eb-08-Rule-Create-Schedule | Create rule, schedule | `eb_rule_create` | field exists; "when it fires" is **new** |
| Eb-09-Rule-Create-Refused | Refused inline | `fail_inline` | exists; the refusal is hidden behind the sticky bar (EB-65) |
| Eb-10-Rule | A pattern rule | `eb_rule`, `eb_targets`, `connections`, `tag_editor` | exists; the Sends column and the fed-by line are **new** |
| Eb-11-Rule-Schedule | A scheduled rule, disabled | `eb_rule` | exists; the "Stopped" note is **new** |
| Eb-12-Target-Add | Add a target | `eb_targets`; `ebAddTarget` | exists as an inline select; SNS topics and the path parameter are **new** |
| Eb-13-Target-Shape | What a target receives | `eb_targets`; `ebAddTarget` | **new**, the whole board; cannot match yet (6a) |
| Eb-14-Archives | Archives and replays | `eb_archives`, `eb_replays` | exists |
| Eb-15-Archive-Detail | An archive, opened | `eb_archive_detail` | exists |
| Eb-16-Replay | After a replay | `eb_kv_detail`; `ebReplayDetail` | exists |
| Eb-17-Replay-Options | A window and a rule filter | `eb_archives`; `ebReplay` | **new**, the whole board; cannot match yet |
| Eb-18-Trace | Trace a target back; describe the bus | `eb_trace`, `eb_rules_by_target`, `eb_kv_detail` | exists; rule state and three bus rows are **new** |
| Eb-19-Destinations | Connections and API destinations | `eb_destinations`, `eb_http_tables` | exists; shape differs |
| Eb-20-Connection | A connection, opened | `eb_connection_detail` | exists; "keep current" is **new** |
| Eb-21-Connection-Create | OAuth, refused | `eb_http_body`, `eb_connection_auth_fields` | exists; the form resets on refusal (EB-64) |
| Eb-22-Destination | A destination, INACTIVE | `eb_destination_detail` | exists; "what a delivery does" is **new** text |
| Eb-23-Confirms | Seven confirms | `hx-confirm` in both templates | exist as one sentence; the bus sentence changes (A13, D-EB2) |
| Eb-24-Bad-Nothing-Arrived | Published, nothing arrived | no single screen; states on `eb_rule`, `eb_rule_list`, `eb_http_tables` | the states exist; the `not found` mark is **new**; cannot match yet |

There is no "nothing exists" board and no "nothing selected" board: the
default bus always exists and `/eb` opens it (A7). There is no Denied board: a
bus has no policy that can be set, and the console acts as root, so no
EventBridge console action can be denied. If EB-4's decision or EB-56 gives a
bus a policy, a Denied board is owed.

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Targets column on the rules list | `ruleTargets`, which the wiring graph already calls per rule |
| Sends column on a rule's targets | `ListTargetsByRule` members the client discards today (EB-22) |
| "Publishing this would deliver to" | The matching rules from `ebMatch` and their targets |
| `match · disabled` | The rule's `State`, already in the list (EB-17) |
| Bus ARN preview | The typed name and the instance identity (`awsident`), not literals (A8) |
| "What an app sends" | The typed name. Static otherwise. |
| The pattern in words | The builder's rows (`static/pattern.js`). A pure function of the form. |
| When it fires, next three times | `internal/awscron` `Parse` and `Next`, and `parseRate`; needs an endpoint or the parser exported to the console |
| Taken-name notice | The bus's rule list, already loaded for the page (EB-21) |
| Fed-by line on a rule | The rule's bus, or its schedule (EB-26) |
| "Stopped" note on a disabled schedule | The rule's `State` and `Schedule` |
| SNS topics in Add target | `ListTopics`, which the console already has |
| Path parameter field | `HttpParameters.PathParameterValues`, shown when the chosen destination's endpoint contains `*` (EB-19); a value may be a `$.` path (EB-45) |
| Target shaping form | `PutTargets` `Input`, `InputPath`, `InputTransformer` |
| Shaping preview | The Test event's document run through the same substitution delivery uses. `shapeInput` is unexported in `eventbridge`; it moves to a shared package or the console calls a new read-only endpoint. Not built until one of those exists. |
| Replay window, rule filter, name | `StartReplay` `EventStartTime`, `EventEndTime`, `Destination.FilterArns`, `ReplayName` |
| Rule state in trace results | The bus's rule list |
| Bus Description, KMS key, dead-letter rows | `DescribeEventBus`, which already returns them |
| "Keep current" on a connection | Leaving `AuthorizationType` out of `UpdateConnection` (EB-18) |
| `not found` mark on a target | The queue, function and topic lists the rule page already loads |

**Where the teaching lines come from**

| Line | Source |
|---|---|
| Bus name 1 to 256, characters | `eventbridge/validate.go`, `CreateEventBus`; the store's extra rule until section 5 settles it (settled: `/` is refused for a custom bus, K32) |
| Rule name 1 to 64; pattern up to 4096; schedule up to 256 | `validate.go`, `PutRule`. *Add: an account's default pattern quota is 2,048 (`doc`), listed* |
| "A field's value is always a list"; the refusal texts | `internal/eventpattern`, `parseLeaf`, `Parse`; `doc` eb-event-patterns-arrays |
| "Typed 5 is the number 5" | `static/pattern.js`, `emitCond` |
| "A string is never matched against a number"; "one element matching is enough" | `eventpattern`, `exactCond`, `leafNode.matchValue`; the second is `doc` |
| Schedule spellings, the `?` rule, day-of-week numbering, UTC | `eventbridge/scheduler.go`, `parseRate`; `internal/awscron`, `Parse`; `doc` eb-scheduled-rule-pattern |
| "The clock starts when doze-aws first sees the rule"; nothing missed is replayed | `scheduler.go`, `fireDueSchedules`; `SUPPORT.md`, PutRule row |
| Scheduled event shape | `scheduler.go`, `fireScheduled` |
| Template and path limits (8192, 256) | `validate.go`, `PutTargets` |
| "A string goes in as it is"; "a path that finds nothing becomes an empty string" | `actions.go`, `renderScalar`, `shapeInput`. *These two lines stop being true with EB-32; the page's own wording is the source after the fix* |
| Archive name 1 to 48; replay name 1 to 64 | `validate.go`, `CreateArchive`, `StartReplay` |
| "Retention stored, never expired" | `SUPPORT.md`, archive row |
| "Only enabled rules with a pattern take part in a replay" | `actions.go`, `matchAndDispatch` |
| "A replay does not add to the archive" | `archive_actions.go`, `captureToArchives`; `doc` eb-archive |
| Connection name 1 to 64; secrets never returned; the Secret ARN | `validate.go`, `CreateConnection`; `SUPPORT.md`, "API destinations" |
| Delivery: 5 s, one retry, no dead-letter queue | `apidest_deliver.go`; `SUPPORT.md`, "API destinations"; `doc` eb-api-destinations (5 seconds) |
| Rate limit stored, not enforced; default 300 | `SUPPORT.md`, API destination row; `doc` (EB-47) |
| Enforce needs the destination's policy | `SUPPORT.md`, "Service principals" |

Every limit above is to come from the settings accessor (foundation B4), not
from text in a template.

**Statements on the boards not yet backed.** Remove or prove each before the
board ships. *Re-read 2026-10-11 against the model, the pages and the code.*

- Eb-15: "0 means keep indefinitely on AWS". *Backed: `model` + `doc` (K27, K38).*
- Eb-22: "AWS retries for 24 hours and can park the event in a dead-letter
  queue". *Backed: `model` and `doc` (K39).*
- Eb-12 and Eb-24: "as on AWS" for the destination policy. *Backed for Lambda
  and SNS by the model's PutTargets text; for SQS by `doc` eb-rule-dlq's
  SourceArn example.*
- Eb-13: *"A predefined name is left as written" and "No array index, no
  wildcard" were statements of doze-aws's gaps given as facts about AWS.
  Corrected in the working copy (6a); true text: EB-5, EB-6, EB-32.* "A string
  goes in as it is, so the quotes are yours to write" was wrong the same way.
- Eb-13 and Eb-10: the Sends text for a target that carries two input members
  at once, which EB-12 will make impossible.
- Eb-13: "With Part of it ... a path that finds nothing means the event is not
  delivered at all". True of the code; what AWS does is `memory`
  (`input_path_no_match`). Kept on purpose, marked as doze-aws behaviour.
- Eb-17: *"Chosen by when each event was archived" was wrong (EB-37).
  Corrected in the working copy.*
- Eb-23: the bus confirm's sentence depends on EB-4's decision. *The model's
  text ("need to be deleted") is the old sentence; the board describes silent
  removal. If D-EB2 follows the model the board's bus entry is wrong.*
- Eb-07: "A name that already exists replaces that rule's pattern, enables it,
  and keeps its targets and tags". *Backed for the replace and the enable
  (`model`); targets are untouched (the model is silent); tags are untouched
  because tags in the call are ignored (`model`).*
- Eb-24: the six log lines are quoted from a run. They are the process log's
  wording, not a contract, and the board quotes them only until EB-29 gives the
  console something of its own to show.

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each board from the working copy
(`console-design/project/Eb-*.dc.html`, 1440 by 900, light and dark) and the
real page in the same state (a stack seeded with the boards' own names:
`harbour-bus`, `on-order-placed`, `stock-low`, `nightly-digest`,
`checkout-archive`, `partner-api`, ...) beside it, with the screenshots of
both in `scratchpad/dcheck/eb/` (`design-<board>.png`, `real-<board>.png`).
The real pages were driven by a temporary Playwright spec
(`e2e/tests/zz-eb-fidelity.spec.ts`, deleted) on its own port. The shell
(rail, list-pane header, top bar) is the foundation's and differs from the
boards' marks-only rail on every page; that is a shell decision, not an
EventBridge one, and is not re-opened here (same as 6a of SQS). The per-service
AWS icons stay (L12): the boards' `EB` mark is the placeholder for the icon
the real page draws in the rail, the list header and the resource header.

| Board | Verdict | What differs |
|---|---|---|
| Eb-Home | differs | The page has the right shape (Rules panel left, Test event right, tabs, ARN chip). Missing: the sentence under the empty Rules panel ("An event no rule matches is accepted and goes nowhere. The default bus is where your app's PutEvents lands when it names no bus, and it cannot be deleted.") and "The number is rules." under the list. The real detail field is the editor with a Format button and a validity line, where the board draws a plain field (the editor is better; keep) |
| Eb-02 Bus create | differs | One field labelled "Bus name" (board: "Name"). Missing: the "Its ARN will be ..." line (**new**, from `awsident`), the right-hand "What an app sends" panel (**new**), and teaching lines 4 and 5 under the field ("1 – 256 characters · letters, digits and . - _ · enforced · default is taken"). The footer is the sticky action bar (board: inline buttons) |
| Eb-03 Bus create refused | differs | The refusal sits below the form's action bar (board: above the form); its closing line reads "doze-aws refused the request: it breaks a rule of the service. The input is what to change." (board: "doze-aws refuses this because AWS does ..."; the real wording is the safer one for a message that is doze's, A2). Missing: the field's own line ("The default bus is always there. Pick a name of your own."), "What you typed is kept ..." and the whole "Other refusals this form can come back with" / "What an SDK call gets" column. The typed value is kept (run) |
| Eb-04 New bus | differs | Missing the note under Rules ("Rules on this bus see only events sent to harbour-bus ..."). Header actions (API destinations, Create rule, Delete) match |
| Eb-05 Rules | differs | Real: Rule, Match, State, with Test event as a right-hand panel. Board: Rule, Match, **Targets** (new), State, and the Test event strip full width *below* the list. The Match cell shows `<` for `<` (EB-63). The note "Rules are listed by name. Match is ..." is missing. **The boards disagree with each other**: Eb-Home puts Test event at the right, Eb-05 below the list, Eb-06 above it. The right-hand placement is what exists and what Eb-Home draws; it is taken as the decision (below) |
| Eb-06 Test event | differs | Verdicts (`match`, `no match`, `schedule`), "1 of 3 match this event" and the not-valid-JSON note exist and read as drawn. Missing: "Publishing this would deliver to" (**new**), `match · disabled` (**new**, EB-17), the right-hand column "Other answers" and the paragraph about the test event's fields. The Test event sits at the right, not above the list |
| Eb-07 Rule create | differs | Builder rows, JSON toggle, "Test an event" with its verdict line and the CLI row exist. Missing: the right-hand "The pattern in words" (**new**) and "Reading a pattern" cards; the sentences under Rule name ("A name that already exists replaces ...", "1 – 64 characters ... enforced"); "A dot descends: detail.state ..." beside Add field. The page is one column where the board is two. The tall form runs under the sticky bar: the verdict line is partly hidden at 900 high |
| Eb-08 Rule create schedule | differs | Real shows the Event pattern builder expanded above the Schedule field; the board collapses it to one row labelled "leave empty for a schedule-only rule". Missing: "When it fires" with the six fields and the next three times (**new**), "What the target receives", "Two spellings", and teaching lines 4 and 5 ("up to 256 characters · enforced · a malformed one is refused") |
| Eb-09 Rule create refused | differs | The refusal is under the sticky bar with its message hidden (EB-65); the board puts it above the form with "What you typed is kept" under the editor and the right-hand "Other refusals this form can come back with" and "A name that is taken" (**new**) |
| Eb-10 Rule | differs | Real shows `on-order-placed → DRAINS TO ...` only. Missing: the "FED BY events put on default" segment (**new**), the sentence "An event matches when source is ... and detail-type is ... A pattern is edited by creating the rule again under the same name." (**new**), and the Targets table's **Sends** column (**new**; EB-22). Tags and Add target match. The remove-target and Replay buttons appear on hover only (board: always) |
| Eb-11 Rule schedule | differs | Missing the "Stopped" card (**new**), the "What it receives when it does fire" line, and the "FED BY the clock" segment (**new**) |
| Eb-12 Target add | differs | A different shape: the board is a dedicated view with a destination list (kind mark, name, "already a target"), a Path parameter field (**new**, EB-19) and the right-hand "Before an event can arrive" card; the page is an inline select of three kinds (no SNS: EB-20). A decision is needed: the inline select is what the rule page has and what its other boards (Eb-10, Eb-11) draw beside it, so **the page keeps the inline select, grows the SNS and log-group kinds, the "already a target" mark and the path parameter field; the dedicated view is dropped** unless the rule page is redrawn |
| Eb-13 Target shape | cannot match yet | Waits on: `ListTargetsByRule` members in the client (EB-22), a shared `shapeInput` (or a read-only endpoint) for "What it would send", and the fixes of EB-32, EB-5 and EB-6 so that the card's sentences are true. The board's facts are corrected (below) |
| Eb-14 Archives | differs | Two panels (Archives, Replays) match. Missing: the note under the archive form ("An archive made here has no pattern ...; 1 – 48 characters ... enforced") and "A replay runs at once and is finished when it appears. The list is every replay on this machine, not only this bus's" (which EB-25 will make untrue). A refusal breaks the form's layout (EB-64). The board's "What Archive can come back with" cards are static documentation; the page shows the one inline refusal |
| Eb-15 Archive detail | differs | The detail panel opens below both lists with the pattern builder, retention and Save: the right shape. Missing: the right-hand "Two things worth knowing" card, teaching lines 4 and 5 under Retention ("0 or more · stored, not enforced"), "Changes what is captured from now on. Events already captured stay." The header line "14 events · 7 day retention · ENABLED" matches |
| Eb-16 Replay | differs | The kv panel (Reason, Replayed from, to, Source, State, Window from, to) matches in content; Source prints `archive/checkout-archive` (board: the name) and every time is in the machine's zone with no label. Missing: the right-hand "What Replay did", "COMPLETED, and nothing arrived" and "Replay less of it" cards |
| Eb-17 Replay options | cannot match yet | Waits on `ebReplay` taking a window, a rule filter and a name (the client's `StartReplay` has no filter), on EB-37 (window by event time) and on D-EB1. The board's "Chosen by when each event was archived" was wrong and is corrected |
| Eb-18 Trace | differs | One column where the board has two. Rules-firing list lacks the state badge (**new**); the bus panel has Arn and Name only (board: Description, KMS key, Dead-letter queue, **new**, and EB-50's LogConfig). The helper text promises "its resource policy" (EB-24) |
| Eb-19 Destinations | differs | The tables sit side by side and are cramped: connection and destination names wrap in the first column ("carrier–/key"), the Actions column of the right table is clipped, delete icons show on hover only. The board stacks the two tables full width. The create forms always show Headers, Query and Body (board: collapsed) and lack the sentence "Basic, API key header, or OAuth client credentials ... The other two →" |
| Eb-20 Connection | differs | The detail panel with ARN, Secret ARN, Authorization, Description, Headers (`X-Token=••••`), Created matches; the re-authorize form matches in fields. Missing: the "– keep current –" option (**new**, EB-18, which the console still refuses: run), the "left empty: kept" placeholders, and the right-hand cards "Description on its own", "What Deauthorize does", "Headers, query, body". The refusal text on a description-only save is the one in the board ("what it comes back with today") |
| Eb-21 Connection create | differs | The form **resets** after the refusal (EB-64), so "What you typed is kept, secrets included" is false; the fields are not grouped as "Sent with every call"; no "Other refusals" column. The refusal shows below the form (board: above) |
| Eb-22 Destination | differs | INACTIVE note and the update form exist; the right-hand "What a delivery does" card and "Other answers, on update and on create" are missing. The connection select preselects the first connection (EB-28) |
| Eb-23 Confirms | differs | Seven confirms exist as one sentence with "Cancel" and the verb ("Delete", "Remove"). The board has a body in each (counts: "Its 14 captured events go with it", "Its 2 targets are removed with it", "Its 1 API destination goes INACTIVE ...") and "Keep it" / "Delete bus" style buttons (`data-confirm-cancel`, `data-confirm-verb` exist in `shell.js`). The bus confirm says "Its rules must be deleted first." which the code does not hold (A13, EB-4) |
| Eb-24 Bad day | cannot match yet | No single screen: needs the failed-delivery surface (D-EB6, EB-29), the `not found` mark (**new**) and `match · disabled` (EB-17). Until then the board's third column is the process log, which the console cannot show |
| Dark (main boards) | matches | Create bus, Rules, Rule, Create rule and Destinations rendered in both themes: the same shapes, the same hierarchy, no unreadable text (`real-dark-*.png` beside `design-*-dark.png`) |
| 900 wide (main detail pages) | differs, below the supported width | The console shows its own "built for a window 1024 px wide or more" banner. At 900 the Rules list clips its State column (and "sou…" in Match), the rule page reflows to one column correctly. The Narrow board is not an EventBridge board |

**Teaching lines (L11), setting by setting.** Lines: 1 name and unit; 2 the
value in words; 3 one sentence of what it does; 4 range, default, enforced or
stored; 5 the neighbour check with real numbers, live. None of the settings
has lines 4 and 5; the destination form's "per second, stored not enforced" is
a fragment of 4. What each needs:

| Setting | Line 4 (source) | Line 5 (the neighbour) |
|---|---|---|
| Bus name | 1 – 256 · letters, digits and `. - _` · enforced (`model`) · `default` is taken | the existing bus names, live |
| Bus description, KMS key, dead-letter ARN, log config | 0 – 512 (`model`) · stored, not enforced (C) | the queue named exists, or "no such queue here" |
| Rule name | 1 – 64 · letters, digits, `. - _` · enforced | the rule list: "exists and is DISABLED; saving replaces its pattern and enables it; its 2 targets and 2 tags stay" |
| Event pattern | up to 4096 (`model`; an account starts at 2,048, `doc`) · enforced | the characters used, live; "matches the test event / does not" |
| Schedule | up to 256 · enforced · a malformed one is refused | the next three times in UTC (`awscron`) |
| Target input (path, constant, template) | 0 – 256 path, 0 – 8192 constant, 1 – 8192 template, up to 100 names (`model`) · enforced | the template's variables against the names mapped; "What it would send" for the test event |
| Archive name | 1 – 48 · enforced | the existing archives |
| Archive retention | 0 or more days (`model`) · stored, not enforced | "0 keeps indefinitely on AWS" |
| Replay name, window, filter | 1 – 64 · enforced; window by event time | "the archive holds 14 events; 9 fall in this window" |
| Connection name | 1 – 64 · enforced | the existing connections |
| Destination endpoint | up to 2048 · `*` is a path parameter · doze-aws accepts `http://`, AWS requires `https` (`doc`) | the connection's type; whether a `*` has a target value |
| Destination rate limit | 1 or more (`model`) · default 300 (`doc`) · stored, not enforced | n/a |

**Where the design is wrong** (corrected in the working copy on 2026-10-11 in
Eb-13 and Eb-17; the published canvas carries the old text until it is
republished). The edits, each minimal:

1. **Eb-13, "How it reads", three sentences.** Was: "A string goes in as it is,
   so the quotes are yours to write; anything else goes in as JSON." / "A path
   is `$` then dotted keys. No array index, no wildcard. A path that finds
   nothing becomes an empty string." / "doze-aws replaces only the names you
   map. A predefined name such as `<aws.events.rule-name>` is left as written."
   Now: strings are quoted for you where the template needs JSON, objects and
   arrays are not; array indices and wildcards are part of the path syntax;
   the five predefined names are listed, reserved, and not to be mapped
   (`doc` eb-transform-target-input.html, eb-targets.html). The "finds nothing
   becomes an empty string" sentence was removed because the page says only
   that the variable "isn't created and won't appear in the output".
2. **Eb-17, one sentence.** Was: "Chosen by when each event was archived."
   Now: "Chosen by each event's own time: the Time it was sent with, or the
   moment it was put when it had none." (`model`, EB-37).
3. Not edited, but wrong **once D-EB2 follows the model**: Eb-23's bus entry
   ("It has no rules. An archive taken from it stays ..." and "Why the bus
   line is different from today's") describes silent removal; the model's
   text says rules must be deleted first.
4. Not edited, but to be written with the teaching lines: Eb-02 and Eb-07's
   "Up to 4096 characters" is the model's ceiling; an account's default is
   2,048 (`doc`). Eb-19 and Eb-22 show `http://` endpoints with no mention
   that AWS requires `https`.
5. Not wrong, but the banner "doze-aws refuses this because AWS does" on Eb-03
   and Eb-09 sits over messages that are doze's wording (A2).

**Build list, in order** (console work; behaviour changes named)
1. Teaching lines 4 and 5 on every setting in the table above (kit
   `setting_field`, settings records for EventBridge from B4); the right-hand
   two-column form page with its cards (the SQS Create/Config shapes already
   exist in the kit).
2. Client data the boards need first: `ListTargetsByRule` members (EB-22),
   `DescribeEventBus` fields, rule state in `ebMatch` (EB-17), replays per bus
   (EB-25), `ListTopics` for the target chooser (EB-20).
3. Eb-02 and Eb-03: ARN preview from `awsident`, "What an app sends", the
   field lines, the refusal above the form.
4. Eb-05, Eb-06: Targets column, notes, `<` (EB-63), "would deliver to",
   `match · disabled`; Test event stays at the right (decision).
5. Eb-07, Eb-08, Eb-09: two-column form page, "pattern in words", "Reading a
   pattern", the taken-name notice, "When it fires" (export `awscron` Parse and
   Next, or a read-only endpoint), refusal visible and typed values kept
   (EB-64, EB-65).
6. Eb-10, Eb-11: fed-by, pattern in words, Sends column, Stopped card.
7. Eb-12: the chooser (kinds with their marks, "already a target", path
   parameter with `$.` allowed), the enforce card. *Behaviour:* EB-44 and
   EB-45 first.
8. Eb-13: after EB-32, EB-5, EB-6 and the shared substitution function.
9. Eb-14 to Eb-17: notes, cards, retention lines, the replay panel (window,
   filter, name). *Behaviour:* EB-37, EB-53.
10. Eb-18: two columns, state badge, the three bus rows (plus LogConfig).
11. Eb-19 to Eb-22: stack the tables full width, collapse the optional fields,
    the cards, "keep current" (*behaviour:* EB-48 so that it is accepted),
    keep typed values on refusal (EB-64).
12. Eb-23: bodies with real counts, "Keep it" and the verbs; the bus entry
    after D-EB2.
13. Eb-24: after D-EB6; or the board says plainly there is no console record.
14. Re-render every board beside its page in both themes and at 900, update
    this table, and run the accessibility sweep.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | Bus delete with rules and an archive (EB-4); tags at create (EB-8, EB-40); every rule state (EB-9); archive update and clear (EB-14); replay checks (EB-15); delete of a rule on a missing bus (EB-33); a time-ordered replay (EB-37) |
| SDK v2 | Put by bus ARN and the pinned drop to a missing bus (EB-1, EB-2); `Time` kept (EB-36); entry codes and the all-bad request (EB-35); size (EB-38); target ARN kinds (EB-3); the transformer page's table (EB-32), predefined variables and array paths (EB-5, EB-6); declared target members and a FIFO queue (EB-7); paging on every list (EB-10), filters (EB-51); six targets (EB-11); two input members (EB-12); non-object detail (EB-13); describe by ARN (EB-34); partial connection update (EB-48); what `PutEvents` returns on a failed target (EB-29) |
| Rejection parity | Status and code for a missing bus on every bus operation (EB-16); the lengths and maps the tables lack (EB-52, walked from the model); the section 5 limits the model does not carry |
| Pattern | A table test per operator and value type in `internal/eventpattern`, shared with SNS: the `doc` pages' examples verbatim (EB-42, EB-43, EB-44) |
| Delivery | A local HTTP server: parameter precedence (EB-46), dynamic values (EB-45), the retried codes (EB-61), the log group's timestamp (EB-62) |
| Restart | Buses and tags, targets with every stored member, archives with events, replays, connections with secrets, destinations (three of the nine are covered) |
| Enforce | The IAM resource of each EventBridge operation (EB-58) |
| Conformance (boto3) | One scenario per row of section 8 |
| Console handler | `ebMatch` and state (EB-17); add target with path parameters (EB-19); replays per bus (EB-25); target members reach the page (EB-22); `prettyJSON` (EB-63) |
| Browser | Description-only connection update (EB-18); a topic as a target (EB-20); taken-name notice (EB-21); INACTIVE destination's select (EB-28); a refusal keeps what was typed (EB-64) and is in view (EB-65); each `new` item; each confirm's sentence; the group under a non-default account and region |
| Mutation | `eventbridge/` baseline, then survivors in `actions.go` (`shapeInput`, `extractPath`, `matchAndDispatch`), `scheduler.go`, `apidest_deliver.go`; `internal/eventpattern` |

## 8. Recording queue

Every one of these is `doc` or `memory` today, unless the source column says
`model`. *Re-tagged 2026-10-11 (K41); a row that became `model` is still queued
when the model's text is ambiguous or its code is `memory`.*

| Claim | Source now | Scenario |
|---|---|---|
| `PutEvents` accepts a bus ARN in `EventBusName` | model | `put_events_bus_arn` |
| `PutEvents` to a missing bus: 200, no entry error, the event dropped | doc (eb-putevents); *was memory, and the expectation was reversed* | `put_events_missing_bus` |
| `Detail` that is not an object: the entry's code | model for the rule, memory for the code (`MalformedDetail`) | `put_events_detail_not_object` |
| Entry or request size limit and its error | model + doc (1 MB, sum over the request); the code memory. *Was "256 KB, doc"* | `put_events_entry_too_large` |
| An entry with no `Source`, `DetailType` or `Detail`; every entry bad | model (codes, "fails the entire request"); the whole-request code memory | `put_events_missing_members` |
| A supplied `Time` is kept in the delivered event | model | `put_events_time` |
| `DeleteEventBus` with rules is refused; its code and message | model (text), memory (code) | `delete_bus_with_rules` |
| `DeleteEventBus` on a missing bus succeeds | model (no error shape) | `delete_bus_missing` |
| `DeleteRule` on a missing bus is `ResourceNotFoundException`; on a missing rule succeeds | model | `delete_rule_missing_bus` |
| Bus name: `/` refused for a custom bus; `aws.` reserved | model (`/`), memory (`aws.`) | `bus_name_rules` |
| `PutRule` on an existing rule: omitted members become null, tags ignored | model | `put_rule_update_semantics` |
| A rule with both a pattern and a schedule | model | `rule_pattern_and_schedule` |
| `ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS` matches ordinary events | model | `rule_cloudtrail_state` |
| A schedule on a custom bus: code and message | doc (eb-create-rule-schedule); code memory | `schedule_on_custom_bus` |
| A malformed schedule: "Parameter ScheduleExpression is not valid." | memory | `schedule_malformed` |
| `InvalidEventPatternException` message text | memory | `pattern_error_text` |
| `TestEventPattern` requires the event's mandatory fields; the code when missing | model, memory | `test_pattern_event_shape` |
| Five targets per rule, `LimitExceededException` | model + doc | `targets_limit` |
| An invalid or unsupported target ARN at `PutTargets` | memory | `target_arn_validation` |
| At most one of Input, InputPath, InputTransformer | model; code memory | `target_input_exclusive` |
| `Input` must be valid JSON; its refusal | model (text), memory (code) | `target_input_json` |
| Predefined transformer variables and their names | doc | `transformer_predefined` |
| Array indexes in `InputPath` and `InputPathsMap`; the model's "dot notation, not bracket notation" | doc against model: the two disagree | `transformer_array_path` |
| A string variable is quoted in a JSON position; an object is not | doc | `transformer_quoting` |
| How a missing path renders in a template | doc ("isn't created and won't appear"), memory for the result | `transformer_missing_path` |
| `InputPath` that matches nothing: delivered as what | memory | `input_path_no_match` |
| A FIFO queue target without `SqsParameters`; without content-based dedup | doc, model | `target_fifo_queue` |
| Dynamic `$.` values in `HttpParameters` and `SqsParameters` | doc | `target_dynamic_path` |
| `RemoveTargets` with an unknown Id | memory | `remove_targets_unknown` |
| "Rule can't be deleted since it has targets." | memory | `delete_rule_with_targets` |
| `UpdateArchive` with an empty pattern | memory | `archive_clear_pattern` |
| `RetentionDays` 0 keeps indefinitely | model + doc | `archive_retention_zero` |
| Scheduled events are archived | memory | `archive_scheduled_event` |
| A replayed event carries `replay-name`; replays are not archived | doc | `replay_event_shape` |
| A replay's window is by the event's time; the end is exclusive or inclusive | model (two statements), unsettled | `replay_window` |
| `StartReplay`: end before start; a destination that is not the archive's bus; the eleventh concurrent replay | memory; model + doc; doc | `replay_validation` |
| Replay states and steps; `CancelReplay` on STARTING or RUNNING | model + doc | `replay_states` |
| `UpdateConnection` with only a description, or only a username, keeps the rest | model allows; keeping memory | `connection_update_partial` |
| `DeleteConnection` returns `DELETING` or the state before | model (text) against memory | `connection_delete_state` |
| Parameter precedence between connection and target | model | `apidest_precedence` |
| API destination: rate limit default 300; `https` required; retried codes | doc | `apidest_defaults` |
| API destination retry: 24 hours, then the dead-letter queue | model + doc (`apidest_retry` is not recordable cheaply; stays doc) | `apidest_retry` |
| TagResource: the 51st tag; an `aws:` key; an archive ARN | model, doc, memory (the codes) | `tags_limits` |
| An archive creates a managed rule on its bus, visible in ListRules | doc | `archive_managed_rule` |
| Paging: `Limit` and `NextToken` on every list operation; an expired token | model (`InvalidToken` text only) | `list_paging` |
| `anything-but` with lists; wildcard escapes; consecutive wildcards; `$or` over 1,000; numbers over 2^53 | doc | `pattern_operators` |
| The IAM resource of each operation under enforce | memory | `iam_resources` |

## 9. Owed to integration

| What | With |
|---|---|
| Delivery under enforce needs the queue policy to admit `events.amazonaws.com` for the rule; soft delivers and logs | SQS, IAM |
| The same for a function permission and a topic policy | Lambda, SNS, IAM |
| A FIFO queue as a target, with `SqsParameters`, and the queue's content-based dedup requirement; an SQS queue encrypted with an AWS-owned key is not a valid target (`doc` eb-targets) | SQS |
| A target whose queue, topic or function was deleted: what the caller and the console see; the DLQ if D-EB4 and EB-7 make `DeadLetterConfig` F | SQS, SNS, Lambda |
| Kinesis and Step Functions as targets (D-EB4) | Kinesis, Step Functions |
| A log-group target writes one line per event in a stream named for the rule, with the event's own time | CloudWatch Logs |
| A schedule invoking a function that reads secrets and parameters | Lambda, Secrets Manager, Parameter Store, KMS |
| A function's async destination putting events on a bus | Lambda |
| A bus, rule, targets with input transformers, an archive, a connection and a destination in one template; tags on the rule and the bus surviving it; the three types with no mapping (`Archive`, `EventBusPolicy`, `Endpoint`) | CloudFormation, and through SAM, CDK and Terraform |
| A wiring-graph edge for every delivered target kind, including log groups and API destinations | the map (foundation D) |
| Whether a delivery made by the bus appears on the wire, and as what | the wire (foundation C) |
| The IAM resource resolution for EventBridge (EB-58) | IAM (`internal/iamguard`) |
| Replay of a large archive while events are being put; a restart mid-replay | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] EB-1 to EB-65 each closed, with the test that failed first named beside
      it (EB-2 is withdrawn: its box is the test that pins the behaviour).
- [ ] Decided and written into `SUPPORT.md`: bus delete with rules (EB-4,
      D-EB2); what a target ARN that is not delivered does (EB-3, D-EB4);
      that `PutEvents` does not report a failed delivery and the ledger
      sentence changes (EB-29); the CloudTrail state (EB-9); five targets
      (EB-11); the two bus-name rules; replays synchronous or not (EB-53,
      D-EB1); `PutPermission` (EB-56, D-EB3); which quotas are enforced
      (D-EB5); the failed-delivery surface (D-EB6).
- [ ] The three member tables in section 2 have no "not established" left:
      the seven target parameter blocks, bus `LogConfig`, archive and
      connection `KmsKeyIdentifier`, replay `Description` (answered by read
      and run in K24 and K25; each now a row to close in EB-50).
- [ ] Tags on archives, connections and destinations: *closed by the model:
      not taggable;* a TagResource on one is refused with a code the
      recording queue settles.
- [ ] `not-built.md` reconciles every S operation by name, and the ledger
      test holds that; the refusals are 501 `NotImplemented` with
      `X-Doze-Not-Built` (EB-55).
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] A restart test covers every resource kind in section 3's "Restart" row
      (nine kinds; three are covered today).
- [ ] No list operation returns more than `Limit`; every filter is applied;
      every list length and map entry the model states is in a table or in
      a named hand check (EB-52), held by a test that walks the model.
- [ ] Every C member reads as cosmetic where the console shows it: rate limit,
      retention, and the three bus rows on Eb-18.
- [ ] **The fidelity table exists and no row says "differs" without a
      decision.** 6a is rewritten after the build with every board "matches",
      "differs, deliberately" with its reason, or "design is wrong" with the
      board corrected; the three "cannot match yet" rows are closed or the
      board is dropped with the reason.
- [ ] **Icons kept**: the rail, the list header and the resource header carry
      `awsIcon "eb"`; the Targets and Trace rows use each service's icon.
- [ ] **The five teaching lines on every setting** in the 6a table (names,
      value in words, one sentence, range/default/enforced-or-stored, the
      live neighbour check), generated from the settings records and the
      ledger, not typed into a template.
- [ ] **One protocol (`awsJson1_1`), every operation accepted as the
      reference spells it**: one test per operation over the wire (the model
      sweep covers the 57), and request validation sees the whole request:
      the list and map constraints of EB-52.
- [ ] **Each asynchronous operation decided (L7)**: replay (EB-53), archive and
      connection states and delivery (EB-54), each as "modelled with a small
      visible delay and a rate" or "immediate, difference listed" in
      `SUPPORT.md`; the Eb-16 and Eb-17 boards agree with the decision.
- [ ] All 24 boards implemented from the foundation's shell, each `new` item
      reading only the data named for it. The shaping preview ships only if it
      runs the service's own substitution.
- [ ] No statement on an EventBridge screen is in the "not yet backed" list in
      section 6.
- [ ] The seven confirms say only what the code does, after EB-4 is decided,
      and carry the counts the boards draw.
- [ ] A failed delivery is visible in the console somewhere other than a log
      line, or Eb-24 says plainly that it is not (EB-29).
- [ ] The pattern builder round-trips every pattern in the pattern table test,
      or falls back to JSON and says so.
- [ ] `e2e/tests/eventbridge.spec.ts` covers every route under `/eb` with the
      route gate on, and the accessibility sweep is clean on the three bus
      tabs, the rule page, both create forms and the destinations page.
- [ ] The group works under a non-default account and region (uses A8).
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: member tiers, the decisions in section 10 | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for EB-1 to EB-16 and EB-32 to EB-65 | `eventbridge/sdk_test.go`, `sdk_coverage_test.go`, `declared_fields_test.go`, `rejection_parity_test.go`, new `eventbridge/store_test.go`, `internal/eventpattern/eventpattern_test.go` |
| 3 | Validation: the model walk test, the missing lengths, maps and nested members (EB-52) | `eventbridge/validate.go`, `cmd/dzaudit` |
| 4 | Events: bus by ARN, `Time`, entry codes, size, detail shape | `eventbridge/actions.go` (`putOneEvent`, `busOrDefault`, `putEvents`) |
| 5 | Targets: ARN kinds, stored members, limits, exclusive input, Kinesis and Step Functions | `eventbridge/actions.go` (`putTargets`, `listTargetsByRule`, `dispatch`), `store.go` (`target`), `internal/peercall` |
| 6 | Shaping: predefined variables, array paths, quoting; dynamic parameters; precedence | `eventbridge/actions.go` (`shapeInput`, `extractPath`, `renderScalar`), `apidest_deliver.go`; the substitution moves to a shared package for the console preview |
| 7 | Rules and buses: tags, states, delete, status codes, describe by ARN, `LogConfig` and the other dropped members | `eventbridge/actions.go`, `store.go` |
| 8 | Paging and filters on every list | `eventbridge/actions.go`, `archive_actions.go`, `connection_actions.go`, `apidest.go` |
| 9 | Archives and replays: event time, states, cancel, concurrency | `eventbridge/archive.go`, `archive_actions.go` |
| 10 | Patterns: lists under anything-but, escapes, complexity limits | `internal/eventpattern/eventpattern.go` |
| 11 | Connections: partial update, mixed families, KMS, `DELETING` | `eventbridge/connection.go`, `connection_actions.go` |
| 12 | Stubs and the bus policy (D-EB3), 501 | `eventbridge/eventbridge.go`, `store.go` |
| 13 | Section 5 sweep; pattern table; restart | `internal/eventpattern/eventpattern_test.go`, `internal/awscron/awscron_test.go`, `eventbridge/scheduler_test.go`, `apidest_sdk_test.go`, `persistence_surfaces_test.go` |
| 14 | IAM resources and CloudFormation (EB-58, EB-60, CFN-3) | `internal/iamguard/resolve.go`, `internal/cfn/mapper.go`, `resources.go` |
| 15 | Conformance scenarios and claims table | `conformance/tests/test_eventbridge.py`, the claims file from foundation B1 |
| 16 | Console client: target members, replays per bus, archive clear, connection update, topics | `internal/console/client_eb.go`, `client_eb_http.go` |
| 17 | Console pages, in the order of the 6a build list | `internal/console/templates/eb.html`, `eb_http.html`, `create.html` (`eb_bus_create`, `eb_rule_create`), `handlers_eb.go`, `handlers_eb_http.go`, `handlers_create.go`, `routes_messaging.go`, `static/pattern.js` |
| 18 | Browser tests and the fidelity re-render | `e2e/tests/eventbridge.spec.ts` |
| 19 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 4 to 12 change what a running app receives. Each lands with its
`CHANGELOG.md` line, because a stack that passed before may now be refused
(a sixth target, a tag past fifty, a pattern with `**`, an event with no
`Detail`) or deliver differently (a transformer's quotes, a connection's
precedence, a `Time`).

**Prior art.** AWS's EventBridge user guide is the source for the pattern
operators, the input transformer and its predefined variables, and the target
list; each hand-derived rule's test names the page, and the pages are the ones
listed at the top of this document. AWS publishes its pattern matcher as open
source (`event-ruler`), which is the reference for operator edge cases the
guide does not spell out. Nothing else is cited here: how other emulators
handle bus deletion, target validation or replays was not checked.
