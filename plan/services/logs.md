# CloudWatch Logs

Stage 11. Size **L** (was M; see section 1). Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds Logs' own.

**Reviewed 2026-10-11 before its stage, against the model's own documentation
text** (`.audit-models/cloudwatch-logs.json`), the code, a binary built from
this tree, and the dream-design boards rendered beside the real pages. The
review is the section "Pre-stage review" below; corrections to what this page
said are marked *Corrected 2026-10-11* where they are made. Evidence tags used
here: `model` (the model's documentation text or traits), `doc` (a named
documentation page that was read: the CloudWatch Logs User Guide pages
"Filter pattern syntax for metric filters, subscription filters, filter log
events, and Live Tail" and "Creating metrics from log events using filters",
fetched 2026-10-11), `memory` (not fixed on; goes to the recording queue),
`run` (reproduced against the built binary) and `read` (from the code).

## 1. Summary

Log groups, streams and events over one bbolt store (`logs.bolt`, no fsync per
batch), on one wire protocol (AWS JSON 1.1, target `Logs_20140328`; the model
declares only `awsJson1_1`, so there is no second decoder). Two workers
hang off `PutLogEvents`: the subscription fan-out (to Lambda and Kinesis) and
the metric-filter emitter (to CloudWatch). A sweeper runs every minute.

Peers. Writers: Lambda, Step Functions, API Gateway, EventBridge and SNS, all
through `internal/logship` and `internal/peercall`. Destinations: Lambda and
Kinesis (subscription filters), CloudWatch (metric filters). CloudFormation and
`provision` create groups, subscription filters and metric filters.

It was sized M because the read path is small but three things were wrong in
ways a deploy sees (LOG-1, LOG-2, LOG-3), and the tail, which is the page a
developer has open all day, misreports a refusal as an empty group (LOG-10).
The review found the document had **17 findings and the code has 45**: the
PutLogEvents limits are all in the model (not "to record"), the constraint
table drops 27 of the 225 constraints the model states (list lengths and map
keys), the filter-pattern language is a subset in more places than regex
(JSON patterns refuse valid AWS syntax and two selector forms silently match
nothing), a default Kinesis `distribution` is the wrong way round, the metric
filter's `defaultValue` and `dimensions` mean something different on AWS, a
sweep deletes events AWS would keep, and 19 of the 20 console boards
differ from their pages in shape or content (none was built from the board; the
twentieth is superseded by LOG-1).

**Estimate:** *Corrected 2026-10-11: was "M, three to four focused days"*. **L,
nine to twelve focused days**, from: API correctness and the regenerated
constraint table (items 3 to 7 of section 11) about three days; the pattern
language (regex, space-delimited, the JSON gaps, `extractedValues`) two to
three; the console, 20 boards each differing plus teaching lines on four forms
and a settings record that does not exist yet, four to five; tests,
conformance scenarios and the mutation baseline one to two. The SQS lesson
(sizing several times low) is applied: the document's earlier estimate counted
17 findings and no console shape work. **Actual:** _fill in when done._

## Pre-stage review (2026-10-11)

Answers to the twelve questions in [../02-lessons-from-sqs.md](../02-lessons-from-sqs.md).
"Read" lists what was actually read. Ids are in section 4.

**L1. The model's documentation, read.** *Findings: LOG-5, LOG-8, LOG-9,
LOG-18 to LOG-35, LOG-39.* Read: the documentation of all 25 dispatched
operations and of every member of their input and output shapes, plus the
shapes `LogGroup`, `LogGroupSummary`, `LogStream`, `OutputLogEvent`,
`FilteredLogEvent`, `InputLogEvent`, `RejectedLogEventsInfo`, `Entity`,
`RejectedEntityInfo`, `MetricFilter`, `MetricTransformation`,
`SubscriptionFilter`, `MetricFilterMatchRecord`, `StandardUnit`,
`Distribution`, `LogGroupClass`, the string shapes' length and pattern traits,
the error shapes, and the documentation of the S operations that interact with
dispatched members (`PutLogGroupDeletionProtection`, `AssociateKmsKey`,
`CreateExportTask`, `DescribeExportTasks`, `PutResourcePolicy`,
`StartLiveTail`, `PutDestination`). Every number, range, length, default,
count, name and source tag in this page was re-checked; the corrections are
marked in place (about 40). Two User Guide pages were fetched for what the
model does not say (the filter-pattern language; metric-filter semantics) and
are tagged `doc`.

**L2. Optional parameters.** *Findings: LOG-22 to LOG-30, LOG-32, LOG-33.*
Each "if you don't", "only", "at most", "can't", "must also" in the 25
operations' text was turned into a check against the code. The ones that
produced findings: GetLogEvents `endTime` (exclusive), FilterLogEvents
`startFromHead` (default true; false needs `startTime` on or after
2024-01-01), name-or-identifier "but not both", DescribeLogGroups
prefix-versus-pattern and identifier-only filtering, ListLogGroups tag and
class filters, DescribeMetricFilters prefix-only-with-group and
name-needs-namespace, PutMetricFilter one transformation and three dimensions,
`distribution` default `ByLogStream`, `defaultValue`.

**L3. Error codes.** *Findings: LOG-19, LOG-20, LOG-21, LOG-29.* Every
operation's `errors` list was read. The model's constraint refusals answer
`ValidationException` in the code and that code is in the model's list for
DeleteLogGroup and DeleteLogStream only; the other 23 list
`InvalidParameterException` (LOG-19). The 93 S operations answer 400
`UnsupportedOperationException` (LOG-20), not the agreed 501 `NotImplemented`.
`TooManyTagsException` is listed on TagResource only and is never produced
(LOG-21). DescribeMetricFilters lists `ResourceNotFoundException` and answers
an empty list for a missing group (LOG-29). `DataAlreadyAcceptedException` and
`InvalidSequenceTokenException` are listed on PutLogEvents and the model says
they are **never returned**; the code never returns them (checked, matches).
`InvalidOperationException` is listed on PutSubscriptionFilter and
PutMetricFilter and no circumstance for it is documented (not established).

**L4. Direction.** Named on every row of section 4. Of the 28 review findings
(LOG-18 to LOG-45): more forgiving 13 (LOG-18, 21, 22, 23, 25, 26, 27, 28,
29, 30, 36, 37 in part, 39); stricter, or refusing syntax AWS accepts, 2
(LOG-37, and LOG-30's empty strings, unestablished); wrong code 4 (LOG-19,
20, 26 for a destination ARN, 43); wrong shape 4 (LOG-24, 27, 34, 44);
wrong behaviour 5 (LOG-31, 32, 33, 35, and 38 as latent); console or
document only 4 (LOG-40, 41, 42, 45).

**L5. Wires.** *Checked, none beyond LOG-18, LOG-43.* The model declares one
protocol, `aws.protocols#awsJson1_1`; the service answers that and nothing
else. There is no flattened-list or `xmlName` trait in this model (JSON
only). Request validation walks the decoded body, so list and map members
are seen whole. What was read: the model's service traits, `awsjson`,
`modelcheck`. Not a protocol finding but the same family: the accessors are
lenient about JSON types (LOG-43).

**L6. Tokens.** *Findings: LOG-36; checked, no finding on the sequence token.*
The sequence token: the model says it is ignored and never rejected; the code
ignores any value (run) and returns a made-up `nextSequenceToken`
(matches). `nextToken` on the paged operations and
`nextForwardToken`/`nextBackwardToken` on GetLogEvents: the model says "the
token expires after 24 hours" and "if you have reached the end of the stream,
it returns the same token you passed in" (matches, held by an existing test).
A junk, stale or foreign token is accepted and gives four different answers
(LOG-36). `eventId` (an opaque string) is stable and unique (matches); its
length is not a model fact (LOG-40).

**L7. Asynchronous on AWS.** *Decided, section 5 "Lifecycle" and the table
below; no new finding.* The operations that are asynchronous or
eventually-consistent on AWS, from the model's text:

| Operation or field | On AWS (model) | Here | Decision |
|---|---|---|---|
| Retention deletion | "typically takes up to 72 hours after that before log events are deleted" | a sweep every minute | immediate with the difference listed (more forgiving); no switch (it deletes sooner, which no caller depends on) |
| `lastEventTimestamp`, `lastIngestionTime` | "updates on an eventual consistency basis… typically in less than an hour" | updated in the put | immediate, listed |
| Subscription delivery, metric emission | after the put returns | after the put returns, on a worker | already async |
| `CreateExportTask` (S, deferred) | asynchronous: PENDING, RUNNING, COMPLETED, CANCELLED, FAILED, `PENDING_CANCEL`; one active task per account | not built | stays S; if ever built it needs a visible RUNNING state |
| `StartQuery` (S, declined) | RUNNING then Complete | not built | declined with the engine |
| `StartLiveTail` (S, declined) | event stream, 3-hour session | not built | declined; the CLI's `--follow` polls FilterLogEvents |
| Deliveries, import tasks, anomaly detectors (S) | status models | not built | declined or deferred |

No board draws an in-progress state for a dispatched operation, so nothing
waits on a modelled delay.

**L8. Time- and consistency-based rules, and a switch for each.** *Decided.*
(a) The PutLogEvents timestamp windows (2 hours ahead, 14 days back or before
the retention, 24-hour batch span) are input validity against the wall clock,
not latency rules: **enforced by default** (LOG-5), because a caller that
replays old fixtures is refused by AWS too; a test that fakes time uses
`Options.Clock`. (b) Token expiry (24 hours), the 72-hour deletion window and
eventual consistency of `lastEventTimestamp`: **not modelled, listed as
differences**, no switch. (c) The per-account `PutLogEvents` throttle, 25 TPS
`DescribeLogStreams`, 50 TPS `CreateLogStream`: **not modelled** (no throttling
exists locally), listed. (d) "CloudWatch Logs might disable a metric filter if
it generates 1,000 different name/value pairs for your dimensions within one
hour": **not modelled**, listed. (e) The default retention of a day is a
doze choice, not an AWS rule; LOG-35 is the consequence that has to be closed.
There is no rule here whose strictness needs a `--logs-strict` switch.

**L9. State.** *Checked, none.* Events, streams, groups, tags, retention,
subscription filters and metric filters persist (bbolt). In memory only: the
compiled-filter caches, the fan-out and emitter queues (a batch in flight at
a restart is lost, which on AWS is a delivery that did not happen; listed),
and the sweeper. No new piece of state is introduced by the findings except
the deletion-protection flag (LOG-8), which is data and persists; the
`PutLogEvents` `rejectedLogEventsInfo` is computed per call and stored
nowhere. Read: `store.go`, `fanout.go`, `metricemit.go`, `logs.go`.

**L10. Boards rendered beside pages.** *Section 6a; 20 of 20 compared.* Each
board was rendered at 1440 by 900 and the real page in the same state, both
themes for the main boards, and the Tail at 900 wide. 19 differ in shape or
content and one (Logs-18) is superseded by LOG-1. Four boards had stale
facts and were corrected in the working copy (listed in 6a).

**L11. The five teaching lines.** *Findings: none new; build item 4 in 6a.*
None of the four forms has any of them: Create (name, retention), Add a
metric filter (name, pattern, namespace, metric name, value, unit), Subscribe
(name, pattern, destination) and Retention. `logs` registers no settings
record (`sqs/settings.go` is the pattern), so the kit's `setting_field` has
nothing to read.

**L12. Icons.** *Checked, none.* The page uses `awsIcon "logs"` in the group
header, the rail has the service icon; the boards' `LG` mark is the
placeholder. The shell chrome (rail, top bar, list-pane header) differs from
every board and is the foundation's; see 6a, "Shell".

**Classes of mistake this service is most at risk of**

1. **A table transcribed by hand from the generator's output.** The generator
   (`dzaudit list`) lists the list-length and map-key rules; the table in
   `validate.go` dropped 27 of them, and the committed cases pre-date them. A
   "237 of 237" ledger line was true of a stale input. Regenerate, do not
   transcribe, and add a test that fails when the table lacks a line the
   generator lists.
2. **Refusing what AWS accepts because the matcher is a subset.** Regex,
   space-delimited, unquoted JSON strings, parentheses, `[*]`: refused or,
   worse, accepted and matched against nothing (`{ $.arr[*] = "y" }` answers
   200 and no lines). Every unsupported construct must be refused by name, on
   every entry point, or implemented.
3. **Accepting a member and dropping it.** `kmsKeyId`, `logGroupClass`,
   `deletionProtectionEnabled`, `fieldSelectionCriteria`, `emitSystemFields`,
   `applyOnTransformedLogs`, dimension semantics: success with no effect.
4. **Two meanings of one word.** `defaultValue` (when nothing matched, not
   when a field is missing), `dimensions` (fields, not literals),
   `distribution` (default by stream), retention (here a day, there never),
   `endTime` (exclusive in one operation, inclusive in the other).
5. **Cleaning up half of a delete.** A group's metric filters (LOG-2), the
   bucket walk, and a recreated group inheriting them.
6. **A console built from the board's words.** Nineteen of twenty differ.

## 2. Contract

The model (`com.amazonaws.cloudwatchlogs`) lists 118 operations (checked: 118).
25 are
dispatched (`logs/actions.go`, `handlers`); 93 are refused by name in
`logs/validate.go` `notHere` with HTTP 400 `UnsupportedOperationException`
(*Corrected 2026-10-11: was "refused by name with `UnsupportedOperationException`"
as if that were the agreed refusal; protocol step 1 and D5 say 501
`NotImplemented` with `X-Doze-Not-Built`: LOG-20*). Nothing reaches
`InvalidAction`. The 93 are: Logs Insights 24, live tail 1, destinations 4,
deliveries 17, export and import 7, anomalies 7, policies and the rest 17,
transformers 4, integrations 7, KMS 2, syslog 3 (checked against the model).

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateLogGroup, DeleteLogGroup | F | F | Delete leaves metric filters behind (LOG-2). `kmsKeyId`, `logGroupClass` and `deletionProtectionEnabled` are dropped silently (LOG-8). The `aws/` prefix is accepted (LOG-9). Up to 1,000,000 groups per Region (`LimitExceededException`, not modelled; `model`). |
| DescribeLogGroups, ListLogGroups | F | F | `metricFilterCount` is always 0 (LOG-4); `storedBytes` is always 0; the `logGroupClass` filter is validated and ignored (LOG-8, LOG-28); the filter exclusivity rules (LOG-27); ListLogGroups ignores `logGroupTags` (LOG-28). |
| PutRetentionPolicy, DeleteRetentionPolicy | F | F | 22 values (`model`); the sweeper honours them. No policy means a day here, never on AWS (`model`: "by default the log events in the log group do not expire"; a listed difference, with the consequence in LOG-35). |
| CreateLogStream, DeleteLogStream, DescribeLogStreams | F | F | `storedBytes` always 0 (*Corrected 2026-10-11: was listed as a gap; model: "no longer supported for log streams, and is always reported as zero" so 0 is right for a stream. It is wrong only for a group*). |
| PutLogEvents | F | F | Stream must exist; batch must be in time order. None of the model's other limits (LOG-5). `sequenceToken` ignored: *Corrected 2026-10-11: was "as AWS has done since 2023 (doc)"; model: "The sequence token is now ignored in PutLogEvents actions" and the error shapes "never return"; the date is `memory`*. |
| GetLogEvents | F | F | `endTime` boundary (LOG-22); token handling (LOG-36). |
| FilterLogEvents | F | F | Pattern language is a subset (LOG-1, LOG-37). `requestId` is a doze extension. `startFromHead` ignored (LOG-23). |
| TagResource, UntagResource, ListTagsForResource | F | F | Tag keys are not validated on write (LOG-3); no 50-tag limit (LOG-21); the ARN's account and region are ignored (LOG-26). |
| TagLogGroup, UntagLogGroup, ListTagsLogGroup | F | F | The deprecated spellings (`model`: "on the path to deprecation"). Same gaps. |
| PutSubscriptionFilter, DeleteSubscriptionFilter, DescribeSubscriptionFilters | F | F | Destination existence is not checked (LOG-6); `distribution` default (LOG-33); three members dropped (LOG-34). |
| PutMetricFilter, DeleteMetricFilter | F | F | Pattern and literal value are not checked on save (LOG-1); one transformation and three dimensions (LOG-30); dimension values (LOG-31); `defaultValue` (LOG-32). |
| DescribeMetricFilters | F | F | Ignores `limit`, `nextToken`, `metricName`, `metricNamespace` (LOG-7); missing group, prefix, name pair (LOG-29). |
| TestMetricFilter | F | F | `extractedValues` is always empty (LOG-7); 51 messages accepted (LOG-18). |
| Logs Insights: queries, query definitions, scheduled queries, lookup tables, log fields and records (24) | S | S | Declined. Asynchronous on AWS (RUNNING, Complete); never built. |
| StartLiveTail (1) | S | S | Declined; `aws logs tail --follow` polls FilterLogEvents. |
| Destinations (4) | S | S | Declined. |
| Deliveries, delivery sources and destinations, configuration templates (17) | S | S | Deferred. |
| Export and import tasks (7) | S | S | Deferred. Asynchronous on AWS (L7). |
| Anomaly detectors and anomalies (7) | S | S | Declined. |
| Account, data-protection, index, storage-tier, resource and deletion-protection policies, bearer tokens (17) | S | S | Declined, **except `PutLogGroupDeletionProtection`, which moves to F with LOG-8** (the model: "blocks all deletion operations until it is explicitly disabled"; without it a protected group could never be unprotected). |
| Transformers (4), integrations and S3 Table sources (7), KMS association (2), syslog configurations (3) | S | S | Declined. |

"The operation is F" hides which members do anything:

| Operation | Member | Target | To confirm |
|---|---|---|---|
| CreateLogGroup | `logGroupName`, `tags` | F | Name 1 to 512, pattern `^[\.\-_/#A-Za-z0-9]+$`, "can't start with the string `aws/`" (`model`; LOG-9); tags: at most 50, key 1 to 128, value 0 to 256 (`model`; LOG-3, LOG-18). |
| CreateLogGroup | `kmsKeyId` | C | *Corrected 2026-10-11: was "either store and return it, or refuse it".* Model: returned as `LogGroup.kmsKeyId`; "If you attempt to associate a KMS key … but the KMS key does not exist or the KMS key is disabled, you receive an InvalidParameterException". Target: stored and returned (C); the existence check is owed to KMS (section 9). |
| CreateLogGroup | `logGroupClass` | C | *Corrected 2026-10-11: was "? not established".* Model: `STANDARD` (default), `INFREQUENT_ACCESS` ("a subset of CloudWatch Logs features"), `DELIVERY` ("kept in CloudWatch Logs for only one day"); "can't be changed after a log group is created". Target: stored, returned and filterable; a `DELIVERY` group keeps a day (F, one line); `INFREQUENT_ACCESS` has no local behaviour (C). |
| CreateLogGroup | `deletionProtectionEnabled` | F | *Corrected 2026-10-11: was "? not established whether the model carries it".* It is in the model on CreateLogGroup and on `LogGroup`; "deletion protection blocks all deletion operations until it is explicitly disabled". Target F with `PutLogGroupDeletionProtection`; the refusal's code is **not established** (recording queue). |
| DescribeLogGroups | `logGroupNamePrefix`, `logGroupNamePattern`, `logGroupIdentifiers`, `limit`, `nextToken` | F | *Corrected 2026-10-11: was "prefix and pattern together: not established (AWS refuses the pair, memory)".* Model: "logGroupNamePrefix and logGroupNamePattern are mutually exclusive. Only one of these parameters can be passed"; with `logGroupIdentifiers` "the only other filter … is includeLinkedAccounts"; `limit` 1 to 50, default 50; "ASCII-sorted by log group name"; the pattern is "a case-sensitive substring search" and with it "only arn, creationTime, and logGroupName are included" (LOG-27). |
| DescribeLogGroups | `logGroupClass` | F | Filter must apply (LOG-8). |
| DescribeLogGroups, ListLogGroups | `accountIdentifiers`, `includeLinkedAccounts` | C | *Corrected 2026-10-11: was "? validated, then ignored".* Cross-account observability (monitoring and source accounts); there is one account locally, so validated and ignored is the answer, and the ledger says so. |
| ListLogGroups | `logGroupNamePattern` | F | Regex-like: "^" prefix, a vertical bar separates up to five patterns of 3 to 24 characters (`model`); the exact regex dialect is not established (recording queue). |
| ListLogGroups | `logGroupTags`, `dataSources`, `fieldIndexNames`, `logGroupClass` | F / C | `logGroupTags` and `logGroupClass` are F (LOG-28); `dataSources` and `fieldIndexNames` have no local data (C, validated, returns nothing matching when given, which is what "only log groups containing all specified field indexes" says). |
| group view | `storedBytes` | C | Always 0; model: "The number of bytes stored". The ledger must say so. |
| group view | `metricFilterCount` | F | Wrong today (LOG-4). |
| group view | `arn`, `logGroupArn` | F | *Checked:* `arn` ends `:*`, `logGroupArn` does not, as the model says; tag operations and `logGroupIdentifier` take the form **without** `:*` (pattern has no `*`, `model`). |
| PutLogEvents | `logEvents[].timestamp`, `.message` | F | Limits and rejection (LOG-5). |
| PutLogEvents | `sequenceToken` | C | Ignored, as on AWS (`model`). |
| PutLogEvents | `entity` | C | *Corrected 2026-10-11: was "? length-checked, then ignored".* Model: `keyAttributes` 2 to 4 pairs (names `Type`, `ResourceType`, `Identifier`, `Name`, `Environment`), `attributes` up to 10; response `rejectedEntityInfo.errorType` "only returned when the entity is rejected". Accepted and ignored (C) with the map constraints enforced (LOG-18). |
| PutLogEvents | `logEvents[].requestId` | extension | Not an AWS member. |
| FilterLogEvents | `filterPattern`: terms, phrases, `-term`, `?term`, JSON | F | Gaps in LOG-37. |
| FilterLogEvents | `filterPattern`: `%regex%` | F (target) | *Corrected 2026-10-11: was "S, refused by name; AWS accepts both (doc)".* Model (PutMetricFilter, PutSubscriptionFilter): "Using regular expressions in filter patterns is supported… a quota of two regular expression patterns within a single filter pattern… five per log group". Syntax: `doc`. Decision in LOG-1. |
| FilterLogEvents | `filterPattern`: `[space, delimited]` | F (target) | *Corrected 2026-10-11:* `doc` (the User Guide page), not `model`. Decision in LOG-1. |
| FilterLogEvents | `logStreamNames` (1 to 100), `logStreamNamePrefix`, `startTime`, `endTime`, `limit`, `nextToken` | F | Names and prefix together refused (`model`, matches); 101 names accepted (LOG-18). |
| FilterLogEvents | `startFromHead` | F | Ignored (LOG-23). |
| FilterLogEvents | `interleaved`, `unmask` | C | Ignored. `model`: `interleaved` "is ignored and the value is assumed to be true"; `unmask` needs `logs:Unmask`. `searchedLogStreams` is an empty list (`model`; LOG-24). |
| GetLogEvents | `startFromHead`, `startTime`, `endTime`, `limit`, `nextToken` | F | `endTime` exclusive (LOG-22). |
| GetLogEvents | `unmask` | C | Ignored. |
| PutSubscriptionFilter | `filterName`, `filterPattern`, `destinationArn` (Lambda, Kinesis) | F | Two per group, "creates or updates" by name (`model`, matches). |
| PutSubscriptionFilter | `destinationArn` (Firehose, a Logs destination) | S | Refused by name with `InvalidParameterException`; the model lists both as supported, so this is a doze limit, classed "Refused here" (foundation A2). |
| PutSubscriptionFilter | `distribution` | F | Kinesis only (`model`). *Corrected 2026-10-11: the default is `ByLogStream`; the code uses a random key when it is unset (LOG-33).* |
| PutSubscriptionFilter | `roleArn` | C | Stored and returned; gates nothing. |
| PutSubscriptionFilter | `fieldSelectionCriteria`, `applyOnTransformedLogs`, `emitSystemFields` | C | *Corrected 2026-10-11: was "? not established".* Model: returned "if it was specified when the subscription filter was created". Stored and returned (C); transformers are S, so `applyOnTransformedLogs` can have no effect (LOG-34). |
| PutMetricFilter | `metricNamespace`, `metricName`, `metricValue`, `defaultValue`, `unit`, `dimensions` | F | Exactly one transformation; up to three dimensions; 27 units; default and dimensions together (`doc`: "If you assign dimensions … you can't assign a default value") (LOG-30). |
| PutMetricFilter | `fieldSelectionCriteria`, `applyOnTransformedLogs`, `emitSystemFieldDimensions` | C | *Corrected 2026-10-11: was "? not established".* Returned when specified (`model`); LOG-34. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `logs/`: 19 tests in 8 files (SDK v2 6, subscriptions 2, close race 3, fan-out audit 2, conformance fixes 2, metric-filter logic 2, rejection parity 1, coverage model 1). `internal/logship/`: 3. (Checked 2026-10-11.) |
| Parity | `rejection_parity_test.go` replays `testdata/cases_logs.json` (237 cases); the ledger says 237 of 237 model constraints enforced across the 25 dispatched operations, `knownGaps` empty. *Corrected 2026-10-11:* the generator now lists **225 constraints and 264 cases** for those operations; 27 constraints (list lengths and map keys) are in no table and 32 cases are not in the committed file (LOG-18). The generator is not what dropped them; the hand transcription in `validate.go` did. |
| Root tests | `persistence_surfaces_test.go` writes a group and a subscription filter and reads them after a restart. Metric filters are not in it. |
| Console tests | `internal/console/logs_test.go`: 2 (pages, subscriptions). Metric filters have no handler test. |
| Conformance | `conformance/tests/test_logs.py`: 3 scenarios (lifecycle, write/read/filter, refusals), all **unverified** (`conformance/snapshots/` is empty). Nothing on subscription filters, metric filters, tags or retention sweep. |
| Browser | `e2e/tests/logs.spec.ts`: **6** tests (*Corrected 2026-10-11: was 4; the create-form CLI preview and the stale-link tests exist*). |
| Mutation | No per-package baseline (foundation B3). |

The SDK suite is Go v2 only. There is no test under a non-default account or
region (foundation B2), and group ARNs are minted from `store.id`, so that pass
is expected to hold; it has not been run.

## 4. Findings

Evidence `run` means reproduced against a binary built from this tree (on
2026-10-10 for LOG-1 to LOG-17, and again on 2026-10-11 for the review's rows;
the scripts are in the review's scratchpad, `logsrev/t1.sh` to `t7.ts`).
Sources are stated as they are: `model` carries a short quote, `doc` names the
page, `memory` is not fixed on.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| LOG-1 | P0 | `PutMetricFilter` never compiles the pattern and never parses a literal `metricValue`. A filter with `%ERR.*%` or value `lots` is stored, listed and answered 200; the emitter skips it and writes one line to doze-aws's own log. TestMetricFilter, PutSubscriptionFilter and FilterLogEvents all refuse the same pattern. A silent no-op. *Reviewed 2026-10-11: reproduced; also `{ $.a = }` is stored.* **The decision is not "refuse on save"; it is implement or refuse by name:** AWS accepts both constructs, so a refusal is a doze limit ("Refused here"), and the honest end state is to implement `%regex%` (Go `regexp`; the allowed characters and operators are in the page) and space-delimited fields, with the quotas as `LimitExceededException`. | `logs/actions_metricfilters.go` `putMetricFilter`; `logs/metricemit.go` `filtersFor`; `logs/metricfilters.go` `metricValueOf` | run | **regex: model**: PutMetricFilter "Using regular expressions in filter patterns is supported… a quota of two regular expression patterns within a single filter pattern. There is also a quota of five regular expression patterns per log group". Space-delimited: **doc**, FilterAndPatternSyntax. Non-numeric literal refused by AWS: memory (the model: "The value to publish to the CloudWatch metric") | One table test over the four entry points (FilterLogEvents, TestMetricFilter, PutSubscriptionFilter, PutMetricFilter); each accepted or refused identically; the literal `lots` per the recording |
| LOG-2 | P0 | `DeleteLogGroup` removes streams, events and subscription filters and leaves metric filters. `DescribeMetricFilters` still returns them for the deleted group, and a group recreated under the same name (which Lambda does on its next line) has them back and publishing. `deleteLogGroup` also calls `fan.forget` and not `met.forget`. *Reviewed 2026-10-11: reproduced (a deleted group's filters listed; the recreated group lists them again).* | `logs/store.go` `DeleteGroup`; `logs/actions.go` `deleteLogGroup` | run | *Corrected 2026-10-11: was "doc: deleting a group deletes what is attached".* The model's DeleteLogGroup says only "permanently deletes all the archived log events associated with the log group". What the model does establish: DescribeMetricFilters lists `ResourceNotFoundException` for a missing group and CreateLogGroup "creates a log group" (a new group has no filters). That deletion takes the filters is `memory`; the test holds the consequence | Store test: put filter, delete group, `MetricFilters(group)` is empty; SDK test: recreate, put a matching line, no metric published |
| LOG-3 | P1 | Tag keys are not validated on write. `tagsMap` constrains map **values** only, so `TagResource`, `TagLogGroup` and `CreateLogGroup` store `bad!key`. `UntagResource` and `UntagLogGroup` validate keys, so that tag can then never be removed (run: both refuse `bad!key`), and every later console Save that would drop it is refused. *Corrected 2026-10-11: the cause is the hand transcription, not the generator (`dzaudit list` emits "tags map key length 1..128" and "map key pattern"); B8 needs the regeneration here (LOG-18).* | `logs/validate.go` `tagsMap`, `tagKeys` | run | model: `TagKey` 1 to 128, pattern `^([\p{L}\p{Z}\p{N}_.:/=+\-@]+)$` | Rejection-parity case for a bad key and a 129-character key on all three writers |
| LOG-4 | P1 | `DescribeLogGroups` answers `metricFilterCount: 0` whatever the group holds. | `logs/actions.go` `groupView` | run | *Corrected 2026-10-11: was "doc".* model: `LogGroup.metricFilterCount` "The number of metric filters." | SDK test: one filter, count 1; delete it, count 0 |
| LOG-5 | P0 | `PutLogEvents` enforces time order and nothing else: no limit on events per batch, batch bytes, event bytes, or how old or how far ahead a timestamp is, and no `rejectedLogEventsInfo`. A writer that will be refused or silently trimmed on AWS succeeds here. *Reviewed 2026-10-11: reproduced for 10,001 events, a 15-day-old event, a +3 h event, a 25-hour span, a 1.1 MB message and two 600 KB messages, all 200; the entity's `keyAttributes` of one pair is also accepted.* The full rule, all from the model: batch at most **1,048,576 bytes** ("the sum of all event messages in UTF-8, plus 26 bytes for each log event"); at most **10,000 events**; each event at most 1 MB; events **more than 2 hours in the future** are rejected "while processing remaining valid events" (`rejectedLogEventsInfo.tooNewLogEventStartIndex`, inclusive); events **older than 14 days or preceding the retention period** are rejected the same way (`tooOldLogEventEndIndex` exclusive, `expiredLogEventEndIndex`); out of chronological order, and a time span over **24 hours** among the valid events, "the operation fails" (`InvalidParameterException`, the only client error the operation lists for it). | `logs/actions.go` `putLogEvents`; `logs/validate.go` (no `logEvents` length) | run | *Corrected 2026-10-11: was "doc … exact numbers and which are errors versus rejectedLogEventsInfo: record".* All of it is `model`, PutLogEvents operation text and `RejectedLogEventsInfo`; nothing here waits for a recording except the failing call's **message** (memory). Coupled to LOG-38 (the writers must chunk) and LOG-35 | Handler tests for 10,001 events, a batch of 1,048,577 bytes by the 26-byte rule, a 1 MB event, a 15-day-old event (partial accept with `tooOldLogEventEndIndex`), a +3 h event (`tooNewLogEventStartIndex`), a 25 h span (refused whole) |
| LOG-6 | P1 | `PutSubscriptionFilter` accepts a Lambda function or Kinesis stream that does not exist. `SUPPORT.md` says "A Kinesis stream must exist before the filter names it"; it need not. Each matching batch then fails one delivery, logged only to doze-aws's output. *Reviewed 2026-10-11: reproduced for both a missing function and a missing stream.* | `logs/subscriptions.go` `checkDestination`; `docs/SUPPORT.md` Subscription filters | run | memory: AWS test-delivers and answers `InvalidParameterException`. The model is silent on destination existence; the operation lists `InvalidParameterException`, `InvalidOperationException`, `LimitExceededException`, `OperationAbortedException`, `ResourceNotFoundException` | SDK test with a missing stream and a missing function; then decide (section 10) and fix either the code or the ledger |
| LOG-7 | P1 | `DescribeMetricFilters` ignores `limit`, `nextToken`, `metricName` and `metricNamespace`: every filter comes back on page one, unfiltered. `TestMetricFilter` returns `extractedValues: {}` for every match. | `logs/actions_metricfilters.go` `describeMetricFilters`, `testMetricFilter` | run | *Corrected 2026-10-11: was "doc".* model: `limit` 1 to 50 "the default is up to 50 items"; "Filters results to include only those with the specified metric name… you must also include the metricNamespace"; `MetricFilterMatchRecord.extractedValues` "The values extracted from the event data by the filter" | SDK test: three filters, `limit: 1`, expects a token; `metricName` plus namespace narrows; a `$.field` pattern returns the extracted value |
| LOG-8 | P1 | `CreateLogGroup` drops `kmsKeyId` (not stored, not returned), `deletionProtectionEnabled` (a protected group is deleted by the next call) and ignores `logGroupClass` (validated, then every group reports `STANDARD`). `DescribeLogGroups` and `ListLogGroups` validate a `logGroupClass` filter and do not apply it. Success with no effect and nothing says so. *Reviewed 2026-10-11: reproduced for all three, and for both filters.* | `logs/actions.go` `createLogGroup`, `groupView`, `describeLogGroups`, `listLogGroups` | run | *Corrected 2026-10-11: was "doc".* model (see section 2, member table): `kmsKeyId` returned on `LogGroup`; class "can't be changed after a log group is created", `DELIVERY` "kept … for only one day"; deletion protection "blocks all deletion operations until it is explicitly disabled" | SDK test: create with each, read back; filter by class; delete a protected group is refused; `PutLogGroupDeletionProtection` lifts it |
| LOG-9 | P1 | A group named `aws/foo` is created. | `logs/actions.go` `createLogGroup` | run | *Corrected 2026-10-11: was "doc".* model: "Log group names can't start with the string `aws/`". The code: the operation lists `InvalidParameterException` among its client errors, the only candidate; the recording confirms it. `/aws/lambda/…` (leading slash) is unaffected | Rejection test for `aws/foo`; a test that `/aws/lambda/x` is still accepted; the code asserted once recorded |
| LOG-10 | P2 | The tail renders any FilterLogEvents error as "Nothing printed yet. The group … appears the first time the function writes a line." A `%regex%`, a `[a, b]`, a malformed JSON pattern and a 1025-character pattern all show it. Shared by the group page, the Lambda Logs tab and the Step Functions Logs tab. *Reviewed 2026-10-11: reproduced on a group with twelve lines.* | `internal/console/handlers_logs.go` `logTailData` (`missing := err != nil`); `templates/logs.html` `log_tail` | run | n/a | Handler test: `GET /logs/tail?q=%25x%25` shows the code and message and not the "nothing printed" copy |
| LOG-11 | P2 | The Settings and Create forms offer 12 retention values; the service accepts 22. A group at 400 days (set by CLI or a template) has no option selected, so the menu shows "default", and pressing Save clears the policy. The label "the values AWS accepts" is false. *Reviewed 2026-10-11: reproduced (group at 400 days: badge "400d retention", menu "default").* | `templates/logs.html` `logs_group` settings; `templates/create.html` `logs_create`; `logs/actions.go` `validRetention` | run | model: the 22 values in `PutRetentionPolicy.retentionInDays` and `LogGroup.retentionInDays` | Handler test: retention 400, the settings page marks 400 selected; saving unchanged keeps 400 |
| LOG-12 | P2 | The Add form cannot set `defaultValue` or `dimensions` and offers 6 of the model's 27 units; a filter made by a template with a default or dimensions shows neither. *Corrected 2026-10-11: was "and the list shows only a filter's first transformation"; model: `metricTransformations` is exactly one, so there is nothing else to show (LOG-30 refuses a second).* | `templates/logs.html` metrics tab; `client_logs.go` `ListLogMetricFilters`, `PutLogMetricFilter` | run | model: 27 `StandardUnit` values; "one metric filter can include as many as three dimensions" | Handler test: a filter with a default and two dimensions renders both |
| LOG-13 | P2 | A line with no request id shows `init` with the tooltip "init output, before the first invocation". True for a function's init output; false for every line an application, API Gateway or SNS wrote. *Reviewed 2026-10-11: reproduced with an app group's line.* | `templates/logs.html` `log_tail` | run | n/a | Handler test on an app group: no `init` label |
| LOG-14 | P2 | The count always ends "· last hour". With a request id chosen `FilterLogs` sets `startTime` to 0, so the window is the whole invocation. The tail also keeps only the newest 500 lines and shows "500 lines" without saying more exist. *Extended 2026-10-11: `FilterLogs` asks FilterLogEvents for `limit: 10000` once and never follows `nextToken`, and the default direction is ascending, so with more than 10,000 lines in the window the "newest 500" are the newest of the **oldest** 10,000.* | `client_logs.go` `FilterLogs`; `handlers_logs.go` `logTailData`; `logs.html` `log_tail` | read (the count: run) | model: FilterLogEvents "startFromHead … default value is true"; "might return fewer events than the limit" | Handler tests: `?rid=` label; 501 lines says so; 10,001 lines show the newest |
| LOG-15 | P2 | A subscription's destination is rendered as a link from the ARN alone, so a function or stream that is gone is a link to a page that does not exist, with no sign the filter does nothing. *Reviewed 2026-10-11: reproduced (function deleted, chip still a link).* | `templates/logs.html` subscriptions tab; `resourceurl.go` `resourceFromARN` | run | n/a | Handler test: destination absent from the function list is marked |
| LOG-16 | P3 | Stale text. The package comment lists metric filters among what "answers UnsupportedOperationException". CloudFormation declines `AWS::Logs::LogStream` because "a stream is created by the first PutLogEvents", which stopped being true when PutLogEvents began refusing a missing stream. | `logs/logs.go` package comment; `internal/cfn/resources.go` | read | n/a | None for the comment; a CloudFormation test declaring a LogStream decides the second (section 9) |
| LOG-17 | P3 | The toast after Create filter says "now publishes <metric>" whatever was saved. Closed by LOG-1 for unreadable filters; the wording still promises a publication that needs a matching line first. | `handlers_logs.go` `logsPutMetricFilter` | run | n/a | Browser test asserts the new wording |

**Found by the pre-stage review (2026-10-11).** Every row was reproduced
against the built binary unless it says `read`. The direction is named,
because "stricter than AWS" breaks a correct caller exactly as "more
forgiving" breaks a deploy.

| Id | Sev | What and where | Evidence | Direction | Source | Test to write first |
|---|---|---|---|---|---|---|
| LOG-18 | P1 | **The constraint table drops 27 of the 225 constraints the generator lists** (`logs/validate.go` `constraintTables`): list lengths (`logEvents` 1 to 10,000, `logStreamNames` 1 to 100, `logGroupIdentifiers` 1 to 50, `accountIdentifiers` 0 to 20, `metricTransformations` 1 to 1, `logEventMessages` 1 to 50, `tagKeys` 0 to 50, `UntagLogGroup.tags` 1 to ∞, `ListLogGroups` `logGroupTags` 1 to 5, `dataSources` 1 to 5, `fieldIndexNames` 1 to 20) and map rules (`tags` 1 to 50 entries with key 1 to 128 and a key pattern, `entity.keyAttributes` 2 to 4 entries, `entity.attributes` 0 to 10, their key lengths, `dimensions` key length). The committed `cases_logs.json` is 237 cases; `dzaudit cases` now produces 264 for the same 25 operations. The ledger's "237 of 237" is true of a stale input. | run (51 tags in one call, 101 stream names, 2 transformations, 51 test messages and 10,001 events all answer 200) | more forgiving | model: the traits (`dzaudit list cloudwatch-logs`, e.g. "tags map length 1..50", "logEvents list length 1..10000") | Regenerate `testdata/cases_logs.json`; replay; a test that fails when `constraintTables` lacks a line `dzaudit list` emits for a dispatched operation; hand cases for the maxima the generator does not emit |
| LOG-19 | P1 | **Constraint refusals answer `ValidationException`.** The model lists `ValidationException` on DeleteLogGroup and DeleteLogStream only; the other 23 dispatched operations list `InvalidParameterException`, which is what an SDK caller's typed handler catches. `logs/logs.go` passes `modelcheck.CodeJSON`. The boards (Logs-04, 06, 12, 14, 16) show `ValidationException`. | run | wrong code | model: each operation's `errors` list. What AWS answers is **memory** (`InvalidParameterException` with the same "1 validation error detected" text) | Recording first (`test_refusals`); then one rejection-parity assertion per operation of the code the model lists |
| LOG-20 | P1 | **The 93 refused operations answer 400 `UnsupportedOperationException` with no `X-Doze-Not-Built` header.** Protocol step 1 and decision D5: 501 `NotImplemented`, the header, the operation named, listed in `not-built.md`. `awshttp.NotBuilt` exists and is not called. `logs/logs.go` `ServeHTTP`. | run | wrong code and status (our rule) | doze decision D5; the model has no such operation-level error | `TestEveryModelOperationIsAccountedFor` extended: each S operation answers 501 with the header (foundation B6) |
| LOG-21 | P1 | **No 50-tag limit across calls.** A single request is held to 50 by the table (once LOG-18 lands); the group's total is not: 111 tags were stored. `TooManyTagsException` is never produced. `logs/actions.go` `addTags`. | run | more forgiving | model: TagResource "You can associate as many as 50 tags with a CloudWatch Logs resource"; `TooManyTagsException` "A resource can have no more than 50 tags", listed on TagResource only (TagLogGroup lists `InvalidParameterException` and `ResourceNotFoundException`, so its code past 50 is not established) | SDK test: 30 then 21 tags through TagResource, the second refused with `TooManyTagsException`; the same through TagLogGroup recorded |
| LOG-22 | P1 | **GetLogEvents `endTime` is inclusive.** An event whose timestamp equals `endTime` is returned. FilterLogEvents is inclusive on both ends (correct). `logs/actions.go` `getLogEvents` → `store.Scan`. | run | more forgiving (an extra event at the boundary) | model: GetLogEvents `endTime` "Events with a timestamp equal to or later than this time are not included"; `startTime` "equal to this time or later … are included" | SDK test: events at 1, 2, 3; `startTime`=1, `endTime`=3 returns two |
| LOG-23 | P1 | **FilterLogEvents ignores `startFromHead`.** Always ascending. AWS: default true; `false` returns the latest first, and is "supported only when startTime is on or after Jan 1, 2024 00:00:00 UTC"; with an earlier `startTime` it answers `InvalidParameterException`. `logs/actions.go` `filterLogEvents` passes `backward=false`. With no `startTime` and `false`: not established. | run | missing / more forgiving | model: FilterLogEvents `startFromHead` | SDK test: false with a 2024+ `startTime` is newest first; with `startTime`=1 is refused |
| LOG-24 | P2 | **FilterLogEvents `searchedLogStreams` is a populated list** (`logStreamName`, `searchedCompletely`). `logs/actions.go` `filterLogEvents`. | run | wrong shape | model: "As of May 15, 2020, this parameter is no longer supported. This parameter returns an empty list." | SDK test: the list is empty |
| LOG-25 | P1 | **`logGroupName` and `logGroupIdentifier` together are accepted** (the name wins) on GetLogEvents, FilterLogEvents and DescribeLogStreams. Neither is refused with `InvalidParameterException` (correct). `logs/actions.go` `groupOf`. | run | more forgiving | model: "You must include one of these two parameters, but you can't include both" | Rejection-parity: both set |
| LOG-26 | P1 | **A resource ARN's account and region are ignored.** `TagResource`, `UntagResource` and `ListTagsForResource` act on the local group named by anything containing `:log-group:` (an ARN of another account and region tagged it, and an S3-shaped `arn:aws:s3:::bucket:log-group:/t/tags` listed its tags); `logGroupIdentifier` likewise. A hard-coded account id works locally and fails on deploy (SQS-23's class). `logs/actions.go` `groupOfARN`, `groupOf`. A destination ARN answers `InvalidParameterException` "resourceArn must be a log group ARN"; no destination can exist here, so `ResourceNotFoundException` is the truthful answer. | run | more forgiving; wrong code (destination) | model: "The ARN format of a log group is arn:aws:logs:Region:account-id:log-group:log-group-name" | Handler tests: another account, another region, a non-logs ARN; each refused (`ResourceNotFoundException` for a group that is not this account's) |
| LOG-27 | P1 | **DescribeLogGroups filters.** `logGroupNamePrefix` and `logGroupNamePattern` together are combined (AWS refuses the pair); `logGroupIdentifiers` with another filter is combined (AWS: only `includeLinkedAccounts` may accompany it); with `logGroupNamePattern` the response carries every field (AWS: `arn`, `creationTime`, `logGroupName` only). `logs/actions.go` `describeLogGroups`. | run | more forgiving; wrong shape | model: "mutually exclusive. Only one of these parameters can be passed"; "the only other filter that you can choose to specify is includeLinkedAccounts"; "only arn, creationTime, and logGroupName are included in the response" | Rejection-parity cases; shape test |
| LOG-28 | P1 | **ListLogGroups ignores `logGroupTags`, `logGroupClass`, `dataSources`, `fieldIndexNames`**: every group is returned. A caller that filters by tag gets all of them. `logs/actions.go` `listLogGroups`. The tag filter's value forms (`!` negation and `*` ends) are in the model's pattern `^!?\*?(…)\*?$`. | run | more forgiving | model: "If you specify more than one filter type, the results include log groups that satisfy all filters"; `logGroupTags` "Multiple filters are combined with AND logic" | SDK test: tag filter, with a negated value and a prefix-wildcard value |
| LOG-29 | P1 | **DescribeMetricFilters:** a missing `logGroupName` answers an empty list (the operation lists `ResourceNotFoundException`); `filterNamePrefix` is applied without a group (AWS uses it "only if you also include the logGroupName"); `metricName` without `metricNamespace` is accepted (AWS: "you must also include" the other). `logs/actions_metricfilters.go`. | run | more forgiving | model: DescribeMetricFilters member text and `errors` | SDK tests for the three |
| LOG-30 | P1 | **PutMetricFilter accepts what the model forbids:** two `metricTransformations` (model 1 to 1; the second publishes too), four dimensions ("as many as three"), and a `defaultValue` with dimensions (`doc`: "you can't assign a default value"). `logs/actions_metricfilters.go`. Also stricter: an empty `metricNamespace`, `metricName` or `metricValue` is refused ("a metric transformation needs …") where the model's length is 0 to 255 / 100; whether AWS refuses an empty one is not established. | run | more forgiving (three); stricter (empty strings, unestablished) | model: `metricTransformations` "length 1..1"; `dimensions` "as many as three dimensions"; **doc**: "Creating metrics from log events using filters" | Rejection-parity cases; the empty-string case recorded |
| LOG-31 | P1 | **A dimension's value is published literally.** `dimensions: {"Level": "$.level"}` publishes the dimension `Level` = `$.level` (run: `ListMetrics`). On AWS the value names the field to read and each distinct value found is a separate metric. `logs/metricemit.go` `evaluate` passes `t.Dimensions`. | run | wrong behaviour | model: "The fields to use as dimensions for the metric… Each different value found for a dimension is treated as a separate metric". The `$.field` spelling of the reference is `memory` | SDK test: a JSON line `{"level":"error"}` publishes `Level=error`; a line without the field publishes nothing |
| LOG-32 | P1 | **`defaultValue` is emitted at the wrong time.** The code uses it only when a *matching* line's `$.field` does not resolve; on AWS it is "the value to emit when a filter pattern does not match a log event" (the User Guide: "reported … during a period when logs are ingested but no matching logs are found"). A filter with `defaultValue: 0` and a non-matching line publishes nothing here (run: the metric never appears). `logs/metricfilters.go` `metricValueOf`, `metricemit.go`. The existing comment "which is AWS's rule" is not what the model says. | run | wrong behaviour | model: `MetricTransformation.defaultValue`; **doc** page above | SDK test: filter `ERROR` default 0; put a non-matching line; the metric has a 0 datum |
| LOG-33 | P1 | **Subscription `distribution` defaults the wrong way.** Unset, the Kinesis record's partition key is random; the model's default is by log stream. `logs/fanout.go` `deliver`. SUPPORT.md says "keyed by the log stream under `distribution: ByLogStream` and randomly otherwise". Board Logs-13 said the same (corrected in the working copy). | run (three puts, three random keys) | wrong behaviour | model: "By default, log data is grouped by log stream, but the grouping can be set to random" | Handler test with a Kinesis peer: unset keys by stream; `Random` varies |
| LOG-34 | P2 | **Three members are dropped on both filter kinds:** `fieldSelectionCriteria`, `applyOnTransformedLogs`, `emitSystemFields` (subscription) / `emitSystemFieldDimensions` (metric). Not stored, not returned. | run | wrong shape (missing) | model: `SubscriptionFilter` and `MetricFilter` "Returns the … value if it was specified when the … filter was created" | Round-trip test; C in the ledger |
| LOG-35 | P1 | **The sweep deletes events AWS keeps.** An event with a timestamp older than the default retention (24 hours) is accepted (200) in a group with no retention policy and is gone within a minute; `firstEventTimestamp` still reports it. On AWS a group with no policy never expires and events up to 14 days old are accepted. The same for a group with `retentionInDays: 1` and a 2-day-old event (AWS rejects it at put with `expiredLogEventEndIndex`; here it is accepted and swept). Backfilled or replayed data vanishes. `logs/store.go` `Sweep`. | run (events 10 days, 2 days and 1 hour old: only the last survived 75 seconds) | wrong behaviour (data loss) | model: CreateLogGroup "by default the log events in the log group do not expire"; PutLogEvents "older than 14 days or preceding the log group's retention period are rejected". The default of a day is ours | Store test: put a 2-day-old event, `SweepNow`, it is there while the group has no policy; decision: the default retention applies to **ingestion time** or the default becomes "never, capped by `maxEvents`" (section 10) |
| LOG-36 | P2 | **A junk, stale or foreign `nextToken` is accepted** and answers four different ways: GetLogEvents the newest page, FilterLogEvents an empty page, DescribeLogGroups an empty page, DescribeLogStreams the first page again (an SDK paginator that restarts can spin). Tokens are raw keys or a last name (`pageByName`, `f/<key>`). | run | more forgiving | model: "The token expires after 24 hours"; each operation lists `InvalidParameterException`. The code and message for an invalid token are `memory` | One test per operation once the code is recorded |
| LOG-37 | P1 | **The pattern language refuses or mis-matches valid AWS syntax.** (a) JSON string values must be quoted: `{ $.level = error }` is refused (the page: strings with only alphanumerics and `_` need no quotes). (b) Parentheses: `{ ($.a = "x") && ($.n = 3) }` refused (the page supports them: parentheses bind first, then `&&`, then the OR operator). (c) `$.arr[*]` and `$.*` wildcard selectors answer 200 and match **nothing** (silent). (d) `$.['a.b']` keys. (e) `" "` is "match everything"; here it matches only lines containing a space. (f) `?term` mixed with other terms: "all question mark terms will be ignored"; here one of them is required. (g) Unquoted wildcard strings such as `123.123.*`. `logs/filter.go`. | run for (a), (b), (c), (e); read for (d), (f), (g) | stricter; more forgiving for (c) | **doc**: FilterAndPatternSyntax | A table test of every example on that page against all four entry points |
| LOG-38 | P1 | **The writers send one unbounded batch.** `internal/logship` hands a stream's whole pending list to one `PutLogEvents`; once LOG-5 lands a burst over 10,000 events or 1,048,576 bytes (message bytes plus 26 each), or a span over 24 hours, is refused whole and the lines are lost (as LAM-6 was for SQS). Must land in the same commit as LOG-5, with chunking at both limits. `internal/logship/logship.go` `ship`; the Step Functions, API Gateway, EventBridge and SNS writers are checked for the same. | read | latent (becomes a loss with LOG-5) | model: the PutLogEvents limits | `logship` test: 25,000 lines in one flush arrive as three batches; the existing 3 stay green |
| LOG-39 | P3 | **No 1 MB page cap.** GetLogEvents and FilterLogEvents pages "can contain up to 1 MB of log events or up to 10,000 log events"; here only the count. A page may also be partial or empty on AWS; callers must keep following a token. | read | more forgiving | model: GetLogEvents, FilterLogEvents | Listed as a difference unless cheap; the console follows tokens |
| LOG-40 | P3 | **`docs/SUPPORT.md` sentences that are false:** "the ids 56-digit decimals as on AWS" (`eventId` is 23 digits: a 13-digit timestamp and a 10-digit sequence; run `17917202241110000004106`); "A Kinesis stream must exist before the filter names it" (LOG-6); "keyed … randomly otherwise" (LOG-33); "no parentheses" is true of the code and not of AWS (LOG-37); "237/237" (LOG-18). The id length itself is not a model fact (memory). | run | n/a | model silent on id length | Ledger test reads the sentences; fix with the code |
| LOG-41 | P2 | **The empty state shows raw backticks:** "…and \`aws logs tail --follow\` reads the same lines you see here." The board sets it in monospace. `internal/console/copy.go` `logs.None`. | run (screenshot) | n/a (console) | n/a | Handler test: the empty page has no backtick |
| LOG-42 | P2 | **A refused tag save drops the typed row.** The board promises "the first refusal stops the save, and the rows you typed stay"; the page redraws from the stored tags and the new row is gone ("Nothing was changed. Refused: writing "release"."). Shared `tag_editor`; foundation A5 owns it, listed here so the Logs boards are not signed off on it. | run (screenshot) | n/a (console) | n/a | Browser test: refused value, the row is still in the editor |
| LOG-43 | P3 | **JSON types are read leniently.** `retentionInDays: "7"` is read as 0 and answered "Invalid retentionInDays value: 0"; `limit: "5"` is ignored. `awsjson.Int`, `Str`, `Bool` return the zero value for a wrong type. Shared with the other JSON services (foundation); the model-checked members are not type-checked either. | run | wrong code (a type error is a `SerializationException`, `memory`) | model: member types | Foundation; one Logs case in rejection parity |
| LOG-44 | P3 | **A group's `storedBytes` is always 0** and `DescribeLogGroups` omits `kmsKeyId`, `dataProtectionStatus`, `inheritedProperties`, `deletionProtectionEnabled`, `bearerTokenAuthenticationEnabled`. Listed so the group view is built once against the model's `LogGroup` (C rows in the ledger). | run | wrong shape | model: `LogGroup` | Shape test against the model's member list |
| LOG-45 | P3 | **The forms' rows touch.** On Add a metric filter, Subscribe and Retention, a row's label sits flush under the previous row's input and the submit button flush under the last input (screenshots). The `.fgrid` rows have no vertical gap inside `.form-b`. `templates/logs.html`, `static/app.css`. | run (screenshot) | n/a (console) | n/a | Browser screenshot assertion on the form's row gap |

Owned by the foundation, not repeated here:

- **A2** — the "because AWS does" line is printed for Logs' doze-only refusals:
  regex and space-delimited patterns (run), a Firehose destination (run), a
  function subscribing to its own group (run; whether AWS refuses that is not
  established, section 8).
- **A5** — tag Save writes one key at a time and stops at the first refusal.
  LOG-3 is what makes it bite here; LOG-42 is its other face.
- **A11** — *Corrected 2026-10-11: was "the Create form's CLI preview omits the
  `put-retention-policy`".* Done: the preview shows both lines when a retention
  is chosen (`logs.spec.ts` "create form command preview" holds it; run). The
  board's "Two calls, not one" panel is the only part left.
- **A13** — the Delete confirm, "and everything in it", is false until LOG-2 is fixed.
- **B6, D5** — LOG-20 (the 93 refusals).
- **B8** — LOG-18 (regenerate the tables and cases).

Not a bug, recorded so it is not rediscovered: deleting a group a function
still prints to brings it back on the next line, with no retention and no tags
(`internal/logship` `ship`; run, with a scratch test against the real service).

## 5. What to check

Beyond the common criteria. One protocol (`awsJson1_1`, the only one the model
declares), so each line is checked once, through the Go SDK and through boto3.
*Corrected 2026-10-11: each bullet below was re-read against the model; the
changes are marked.*

**Groups**
- [ ] Name: 1 to 512, the model's pattern, and the `aws/` prefix (LOG-9).
- [ ] Create twice answers `ResourceAlreadyExistsException`; delete of a missing
      group answers `ResourceNotFoundException` (both in the operations'
      `errors` lists).
- [ ] Delete removes streams, events, subscription filters, metric filters and
      tags, and both workers forget the group (LOG-2).
- [ ] `kmsKeyId`, `logGroupClass`, `deletionProtectionEnabled`: each has a tier
      and behaves as its tier says (LOG-8).
- [ ] DescribeLogGroups: prefix, pattern, identifiers, `limit` 1 to 50, token
      paging across more than 50 groups; prefix and pattern together **refused**
      (LOG-27). *Corrected: was "(name and ARN, with and without `:*`)"; the
      identifier pattern `^[\w#+=/:,.@-]*$` has no `*`, so the `arn` field's
      `…:*` form is refused and `logGroupArn` (no suffix) is the form to
      pass.*
- [ ] ListLogGroups: the `a|^b` pattern form, `limit` to 1000, paging, the tag
      and class filters (LOG-28).
- [ ] `metricFilterCount` is true (LOG-4); `storedBytes` is 0 and the ledger says so.

**Retention**
- [ ] All 22 values accepted, any other refused with the message naming the list.
- [ ] DeleteRetentionPolicy returns the group to the instance default.
- [ ] The sweep drops lines past retention, honours `[logs].retention` for
      groups with none, and caps a group at 100,000 events oldest first; an
      event older than the default is **not** dropped while the group has no
      policy (LOG-35).
- [ ] A stream's `firstEventTimestamp` after its first lines were swept: not
      established (run: it is not updated; the model's text is "the time of the
      first event").

**Streams**
- [ ] Name: 1 to 512, no `:` or `*`. Create twice, delete missing.
- [ ] DescribeLogStreams: prefix, `orderBy` both ways (default `LogStreamName`),
      `descending`, paging, and `LastEventTime` with a prefix refused.
- [ ] `firstEventTimestamp`, `lastEventTimestamp`, `lastIngestionTime` are set in
      the put (AWS: eventually consistent, listed); `storedBytes` is 0 for a
      stream, as the model says.

**Events**
- [ ] PutLogEvents: missing group, missing stream, empty batch, empty message,
      out-of-order batch; every limit in LOG-5, with `rejectedLogEventsInfo`.
- [ ] GetLogEvents: forward and backward tokens, the repeated end token,
      `startFromHead`, `startTime` inclusive and `endTime` **exclusive**
      (LOG-22), `limit` 1 to 10,000.
- [ ] FilterLogEvents: interleaving across streams, names and prefix (and both
      together refused), `nextToken` only while more remain, unique `eventId`s,
      `searchedLogStreams` **empty** (LOG-24), `startFromHead` (LOG-23).
      *Corrected: was "`searchedLogStreams`" as a thing to return.*

**Filter patterns** (one language, four users: FilterLogEvents, TestMetricFilter,
subscription filters, metric filters)
- [ ] Each form in the ledger's list matches and fails to match as stated,
      including `-term`, `?term`, phrases, `&&` over `||` and parentheses,
      `IS NULL`, `IS TRUE`, `IS FALSE`, `NOT EXISTS`, `= *`, string globs,
      array indexes and `[*]`, `$.*`, `$.['a.b']` (LOG-37).
- [ ] Case-sensitive (`doc`: "Filter patterns are case sensitive"). Whether a
      term matches a whole word or a substring: not established (the page's
      examples do not decide it; recording queue). *Corrected: was
      "case-sensitive here, whether AWS matches whole words not established".*
- [ ] `%regex%` and `[space, delimited]`: implemented or refused by name on all
      four entry points with the same code, and none is stored (LOG-1); regex
      quotas are two per pattern and five per group (`model`).
- [ ] Every unsupported construct is refused by all four entry points with the
      same code, and none is stored (LOG-1).

**Subscription filters**
- [ ] Two per group; a third refused with `LimitExceededException` (in the
      operation's list); the same name replaces without counting.
- [ ] Destination kinds: Lambda, Kinesis; Firehose and a Logs destination
      refused by name; a missing destination per the decision on LOG-6.
- [ ] The envelope: gzip then base64 under `awslogs.data` for Lambda (`model`:
      "Base64 encoded and compressed with the GZIP format"), raw gzip bytes for
      Kinesis; the six fields `messageType`, `owner`, `logGroup`, `logStream`,
      `subscriptionFilters`, `logEvents` are `memory`; `eventId` length is
      `memory` (the code makes 23 digits; the ledger said 56: LOG-40);
      partition key **by log stream unless `distribution` is `Random`**
      (LOG-33). *Corrected: was "partition key by `distribution`" and
      "56-digit ids" as facts.*
- [ ] `roleArn` round-trips and gates nothing, and the ledger says so.
- [ ] A function subscribing to its own group is refused; the refusal is
      classed "Refused here" until recorded (not in the model).

**Metric filters**
- [ ] 100 per group; the 101st refused with `LimitExceededException`; the same
      name replaces.
- [ ] A literal value, a `$.field`, a nested field, a missing field with and
      without `defaultValue`, a non-numeric literal (LOG-1); `defaultValue`
      when **nothing matched** (LOG-32).
- [ ] Unit: all 27 accepted, any other refused. *Corrected: was "Dimensions reach
      the datum"; dimension values are fields to read (LOG-31), at most three,
      not with a default (LOG-30).*
- [ ] Exactly one transformation (LOG-30). *Corrected: was "Several
      transformations on one filter each publish"; the model's length is 1 to 1.*
- [ ] DescribeMetricFilters filters and pages (LOG-7), a missing group refused
      (LOG-29); with no group it lists all.
- [ ] TestMetricFilter: `eventNumber`, `eventMessage`, `extractedValues` (LOG-7),
      at most 50 messages (LOG-18).
- [ ] A filter does not count lines written before it existed (`doc`: "Filters
      do not retroactively filter data").
- [ ] Metric filters are supported only for Standard-class groups (`doc`); the
      local behaviour for the other classes: decided with LOG-8.

**Tags**
- [ ] Both spellings; the ARN **without** `:*` (the pattern excludes `*`:
      *Corrected: was "with and without `:*`"*); key and value rules on every
      writer (LOG-3); **at most 50 per resource** (`model`; LOG-21, *Corrected:
      was "not established"*); `TooManyTagsException` on TagResource; the ARN's
      account and region (LOG-26).

**Lifecycle**
- [ ] Groups, streams, events, retention, tags, subscription filters **and
      metric filters** survive a restart.
- [ ] Close during a put, a sweep and a delivery does not panic or leak (three
      tests exist; keep them).

## 6. Console

The design's "CloudWatch Logs" page. 20 boards. Templates:
`internal/console/templates/logs.html` (`log_tail`, `metric_filter_test`,
`logs_lp`, `logs_home`, `logs_group`) and the `logs_create` block in
`create.html`; shared pieces in `panes.html` and `layout.html`. Handlers:
`handlers_logs.go`, client `client_logs.go`, routes `logsRoutes` in
`routes_compute.go` (13 routes, checked). `log_tail` is also rendered by
`lambdaLogs` and `sfnLogs`, so a change to it lands on three pages.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Logs-Home | Groups, none open | `logs_home`, `logs_lp` | exists; the footer line on what the number means is **new** |
| Logs-01-None | No log groups yet | `logs_home`, `empty_detail`, `copy.go` | exists (LOG-41) |
| Logs-02-Create | Create | `logs_create` | form exists; teaching lines and "Who writes to a group" are **new** |
| Logs-03-Create-Retention | Create with retention, cli open | `logs_create`, `cli` | form exists; the second cli line **exists** (A11 done); the "Two calls, not one" panel is **new** |
| Logs-04-Create-Refused | Create refused | `fail_inline` | exists |
| Logs-05-Tail | Tail | `logs_group`, `log_tail` | exists; "Written by" and the time range are **new** |
| Logs-06-Tail-Filter | Tail with a pattern; a pattern refused | `log_tail` | filter exists; the refusal shown inline is **new** (LOG-10); "What the box reads" is **new** |
| Logs-07-Tail-Invocation | One invocation | `log_tail` | exists; the count wording is **new** (LOG-14); "Reading a REPORT line" is **new** |
| Logs-08-Tail-Empty | A new group; the quiet states | `log_tail` | exists; "What fills it" is **new** |
| Logs-09-Streams | Streams | `logs_group` streams | exists; the per-stream `tail` link is **new** |
| Logs-10-Metric-Filters | List and Add form | `logs_group` metrics | exists; teaching lines, "2 of 100", "Where it goes" are **new** |
| Logs-11-Metric-Test | Test a pattern | `metric_filter_test`, `logsTestMetricFilter` | exists (as a panel under the form; the board makes it its own view) |
| Logs-12-Metric-Refused | Add refused | `fail_inline` | exists |
| Logs-13-Subscriptions | List and Subscribe form | `logs_group` subscriptions | exists; teaching lines, "2 of 2 used", "What a destination receives" are **new** |
| Logs-14-Subscribe-Refused | Subscribe refused | `fail_inline` | exists |
| Logs-15-Retention | Settings | `logs_group` settings | menu exists; all 22 values, the spoken unit and "How it fits this group" are **new** (LOG-11) |
| Logs-16-Tags | Tags as a draft, refused | `tag_editor`, `fail_inline` | exists (foundation A5; LOG-42) |
| Logs-17-Confirms | Delete group, delete stream, remove subscription, remove metric filter | the four `hx-confirm`s | exist as one-line questions; the bodies and the "Keep it" buttons are **new** (foundation A13) |
| Logs-18-Bad-Unfiring | A filter that saves and never fires | `logs_group` metrics | **Superseded if LOG-1 is closed by refusing or implementing**, which is the plan. Kept only for filters already in a data directory; see below. |
| Logs-19-Bad-Gone | A group that came back; a destination that went | `logs_group` | the "not in this stack" mark is **new** (LOG-15) |

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Teaching lines on Create, Add a metric filter, Subscribe, Retention | A **new** `logs/settings.go` registering a record per setting (the pattern is `sqs/settings.go`; the kit's `setting_field` reads it): lengths, patterns and enums from `logs/validate.go` `constraintTables`; the retention set from `logs/actions.go` `validRetention`; tiers from the ledger. *Corrected 2026-10-11: was "the settings table (foundation B4)" as though it existed for Logs; it does not.* |
| "Who writes to a group" | The producer table in `docs/SUPPORT.md`. Static. |
| Second cli line | The form's `name` and `days`; the call `CreateLogGroup` in `client_logs.go` already makes. **Exists.** |
| "Written by" a function | The group name's `/aws/lambda/` prefix against `ListFunctions` |
| Time range on the tail | `FilterLogs`' existing `since` argument, a query parameter on `/logs/tail` |
| Refusal shown in the tail | The error `FilterLogs` already returns, through `failView` |
| "What the box reads" | The filter-pattern list in `docs/SUPPORT.md`, which `logs/filter.go` implements |
| Count wording for one invocation, and "newest 500 of N" | `rid` in `logTailData`; `len(out.Events)` before the trim in `FilterLogs` (and following `nextToken`, LOG-14) |
| Per-stream `tail` | FilterLogEvents `logStreamNames`; a new argument on `FilterLogs` |
| "2 of 100", "2 of 2 used" | List length against `maxMetricFiltersPerGroup` and `maxSubscriptionsPerGroup`, exported to the console through the settings accessor |
| "Where it goes" | The form's namespace and metric name; a link to CloudWatch |
| "What a destination receives" | Static, from `logs/fanout.go` `envelope` and `deliver` (and LOG-33's default) |
| All 22 retention values, with the current one always present | `validRetention` |
| "How it fits this group" | `ListLogMetricFilters` and `ListLogSubscriptions`, which the settings tab does not load today |
| "not in this stack" | The function and stream lists the subscriptions tab already loads |
| Marks on a filter that cannot fire | `TestLogMetricFilter` on the stored pattern; a number parse of the stored literal |
| Extracted values in the tester | `TestMetricFilter.extractedValues` (needs LOG-7) |

**Where each teaching line's facts come from**

| Field | Range and rule | What it does | Enforced or stored |
|---|---|---|---|
| Log group name | `groupName` in `validate.go`; not starting `aws/` (LOG-9) | `logs_create` copy, ledger producer table | model |
| Retention | `validRetention`; default from `logs.DefaultRetention` and `[logs].retention`; sweep period from `Server.sweeper`; cap from `DefaultMaxEvents` | ledger, "Differences from AWS" | ledger row |
| Filter name (both forms) | `filterName` constraints | upsert by name: `PutMetricFilter`, `PutSubscription` in the store | model |
| Pattern (both forms, tail, tester) | `filterPattern` length 0 to 1,024; `filter.go` `compile` | ledger "Filter patterns" | ledger |
| Namespace, metric name | `reMetricName` `^[^:*$]*$`, length 0 to 255 | `metricemit.go` `evaluate` | model |
| Value | length 0 to 100; `metricValueOf` | `metricfilters.go` | model for length, code for meaning |
| Default value, dimensions | `defaultValue` double; at most three dimensions (LOG-30) | LOG-31, LOG-32 | model |
| Unit | `standardUnits` (27) | `metricship.Datum` | model |
| Destination | `checkDestination` | ledger "Subscription filters" | ledger |
| Distribution | `Random` or `ByLogStream`, default `ByLogStream` | `fanout.go` `deliver` | model |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships. *Corrected 2026-10-11: sources re-read.*

- Logs-06, -11, -14, -18: "AWS accepts these" of regex and space-delimited
  patterns. *Was `doc`, not recorded.* Regex: **model** (PutMetricFilter,
  PutSubscriptionFilter). Space-delimited: **doc** (the User Guide page).
- Logs-13: a delivery "is nested under" the app's PutLogEvents on the wire.
  `read` (`trace.Step` with `Via`), not seen in a browser.
- Logs-13: under enforce the destination must admit `logs.amazonaws.com`.
  `read` from the ledger; no test was run.
- Logs-13: "Keyed by a random value" was **wrong** (LOG-33); corrected in the
  working copy.
- Logs-14: the own-group refusal sits among the refusals without saying whose
  rule it is. Resolved by A2 and the recording.
- Logs-15: "On AWS that means lines are kept for ever." *Was `doc`;* **model**
  ("by default the log events in the log group do not expire").
- Logs-10, -14: "100" and "two, here and on AWS". *Was `doc`;* **model**
  (PutMetricFilter "maximum number of metric filters … is 100";
  PutSubscriptionFilter "up to two subscription filters").
- Logs-11: "does not show the number a `$.field` value would read" was a
  statement about doze that the model contradicts (`extractedValues`);
  corrected in the working copy.
- Logs-17: "Data points already published stay in CloudWatch" and "a function
  still printing makes the stream again". `read`; the group case was run, the
  stream case was not.
- Logs-17: the Delete sentence lists streams, lines, subscription filters,
  metric filters and tags. It becomes true only when LOG-2 is fixed.
- Logs-19, right half: "a destination is not looked up when the filter is
  made". True today (run); false if LOG-6 is decided the other way, and the
  board is then only about a destination deleted afterwards.
- Logs-07: "Billed Duration … rounded up". `read`, `internal/lambdaruntime`.
- Logs-04, -06, -12, -14, -16: the code shown for a constraint refusal is
  `ValidationException`; what AWS answers is `memory` (LOG-19). Not edited.
- Logs-16: "the rows you typed stay" is not true of the page (LOG-42).

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering every board from the working copy
(`console-design/project/Logs-*.dc.html`, 1440 by 900, light; Home, 02, 05, 10
and 15 also dark) and the real page in the same state against a fresh data
directory seeded with the boards' own data (the "harbour" set: five groups,
three streams, two subscription filters, two metric filters, tags), and the
Tail at 900 wide. Screenshots: `scratchpad/dcheck/logs/design-*.png` and
`real-*.png`. **Every board but Logs-18 differs**; the first pass of the console was built
from the sentences of the old single-board sketches and not from these boards.
"Differs" means: build it (the item numbers are the build list below).

*Shell (all 20 boards).* The boards draw a 56-pixel marks-only rail, a top bar
`doze-aws · harbour` with the jump box and the endpoint, a list pane headed
"Log groups" with a `New` button and no filter box, and a key bar at the foot
of the page. The page has a 208-pixel labelled rail, `doze-aws console`, a
region selector and a theme switch, and a list pane headed
"CloudWatch Logs (4) ⓘ [+]" with a filter box and its own key line. This is
the shell's, owned by the foundation, and it applies to every service; the
per-service icons stay (L12). **Decision for a human:** whether the shell
follows the board before Logs' stage or after; the table below does not
repeat it.

| Board | Verdict | What differs |
|---|---|---|
| Logs-Home | differs: build 1 | The list pane has no "A number is the group's retention in days. Without one, lines are kept for a day." footer (the `lp_foot`); the group names are truncated harder because of the wider rail. Dark: matches in kind. |
| Logs-01-None | differs: build 1, 2 | The detail pane has the icon, the paragraph with **raw backticks** around `aws logs tail --follow` (LOG-41), and a button; the board has the bold title "No log groups yet", the sentence in monospace and `c` as the shortcut in the foot bar. The list pane says "No log groups yet / New log group →"; the board says "No log groups yet" only. |
| Logs-02-Create | differs: build 4, 5 | **Shape**: the board is two columns, the form with teaching lines under each field and a right-hand "Who writes to a group" panel (`new`); the page is one column with no teaching lines. **Content**: the subtitle "Retention can be changed later. The name cannot." is the page's two-sentence paragraph instead; labels read "Name" and "Retention the values AWS accepts" against "Log group name" and "Retention · days"; no unit echo; no line four ("1 – 512 characters · letters, digits and . - _ / # · not starting with aws/ · enforced"), no "How long a line is kept…", no "AWS keeps lines for ever…" neighbour sentence. Cancel/Create sit right-aligned in a card against left-aligned buttons under the form. The `cli` button is a pill above the buttons against a button on the right. |
| Logs-03-Create-Retention | differs: build 4, 5 | The page's cli preview is correct (two lines when a retention is picked, held by a test) and sits in a card; the board's sits under the buttons with `$` prompts and "Run against this endpoint, they do what Create log group does." The right-hand "Two calls, not one" panel and the "14 days · two weeks" unit echo are missing; the retention sentence with the sweep period and the 100 000-line cap is missing. |
| Logs-04-Create-Refused | differs: build 6 | **Shape**: the board puts the refusal **above** the form with "What you typed is kept…" below it and "Other refusals this form can come back with" in a right-hand column; the page puts the refusal **below** the buttons and has neither the sentence nor the column. **Content**: the footer is "doze-aws refused the request: it breaks a rule of the service. The input is what to change." against "doze-aws refuses this because AWS does — the rule comes from AWS's own service model, so the same call would have failed on deploy." (the page's is the foundation's vocabulary; the board's names the rule's source, which A2 wants). The code shown is `ValidationException` on both (LOG-19). The name is kept (matches). |
| Logs-05-Tail | differs: build 3, 7 | The board has "Written by harbour-order-validator `new`" under the chips, a wide full-width filter box with the Filter and pause buttons on its right, a "1 hour / 6 hours / 24 hours" range selector (`new`), and the foot sentence "Newest at the bottom, polled every 1.5 s…". The page has a 210-pixel filter box that truncates its placeholder, no written-by row, no range, no foot sentence, request ids are plain text where the board's are underlined links, and a line with no request id says `init` on **any** group (LOG-13). The count matches ("12 lines · last hour"). The log rows, red error line and START/END/REPORT lines match. Dark: matches in kind. 900 wide: tab "Metric filters" wraps to two lines, the chips stack, long lines wrap (acceptable; the console says it is built for 1024+). |
| Logs-06-Tail-Filter | differs: build 3, 6 | **Shape**: the board is two columns (tail left, "What the box reads" and "A pattern this build cannot read" right) and a JSON pattern drops START/END/REPORT with the sentence "A JSON pattern reads only lines that parse as JSON…". The page is one column. **Content**: a refused pattern shows "Nothing printed yet. The group … appears the first time the function writes a line." with a group that has twelve lines (LOG-10); the board shows the refusal with its code and message beside the box. The "0 lines matching · last hour" count is wrong for a refused pattern. |
| Logs-07-Tail-Invocation | differs: build 3 | The chip sits to the right of the Filter button (matches in kind). The page says "5 lines · last hour" where the board says "5 lines" (LOG-14); the three cards beneath ("Reading a REPORT line", "The same view, from the function", "The count") are missing. |
| Logs-08-Tail-Empty | differs: build 3 | The page shows "No lines in the last hour." in a card with a 210-pixel filter box; the board has the same state with two right-hand panels, "What fills it" (the two CLI calls, with the refusal rules) and "The other quiet states", and the sentence "The group is made and empty. It stays on this page and fills as lines arrive, with no reload." |
| Logs-09-Streams | differs: build 3 | **Shape**: the board is table plus a right-hand column ("What a stream is here", "Before the first line") and a `new` note under the table; the page is the table alone. **Content**: the per-stream `tail` link is missing; the page's rows are sorted newest-first as the board's are; timestamps show the date (`2026-10-11 17:45`) as the board's do. |
| Logs-10-Metric-Filters | differs: build 4, 8 | **Shape**: the board is two columns (list and Add form left, "Where it goes", "2 of 100" with a bar, "Evaluated as lines arrive" right) with the form as a 2-column grid and a "test a pattern first →" link; the page is one column with the tester **below** the form as a third panel and the form's rows touching (LOG-45). **Content**: no teaching lines under any field (line 4 "up to 255 · no : * $ · enforced"); no "the model has 27 units; this form offers 6"; no default value or dimensions fields (the board is silent too, LOG-12); rows are sorted by name on both (the board's order was wrong and is corrected). |
| Logs-11-Metric-Test | differs: build 8 | The board is a separate view with the tester left and "What it answers", "Other answers" and the refusal right; the page is the third panel of the metrics tab, with the result below it. The `match` badge and "—" rows exist (page: "0 of 5 lines matched." as plain text above rows, board: "1 of 5 lines matched." in the card). The sample box prefill is the tail's five newest lines, newest first, on both. The `extractedValues` column does not exist yet (LOG-7). |
| Logs-12-Metric-Refused | differs: build 6 | As Logs-04: the refusal is above the form with "What you typed is kept" and a right-hand "Other refusals" column on the board; below the button with neither on the page. The typed values are kept (matches). |
| Logs-13-Subscriptions | differs: build 4, 9 | **Shape**: two columns on the board (list and form left, three cards right); one on the page. **Content**: no teaching lines; no "2 of 2 used"; no "What a destination receives", "One attempt", "Under IAM enforce"; destination chips are the page's conn-chips with a colour bar where the board has the service mark and a link. Rows are sorted by name on both (the board's order was wrong and is corrected). |
| Logs-14-Subscribe-Refused | differs: build 6 | As Logs-04. The page's footer for `LimitExceededException` is the generic one; the board says "A group holds two filters, here and on AWS. Remove one on this tab, or save under the name of one it already has to replace it." |
| Logs-15-Retention | differs: build 4, 5 | **Shape**: the board has the menu with "7 days · a week" echo, the 22 values as chips with the current one marked, a sentence on the sweep, line four, the "'default' is not 'never expire'" card and a right-hand column ("How it fits this group", "Other answers", "From the CLI, not this form"). The page is one select with 12 options (LOG-11: 400 days shows "default") and a Save button touching it. Dark: matches in kind. |
| Logs-16-Tags | differs: build 3, 10 | The board has a "What a tag value may be" card and "Other refusals" column; the page has the shared tag editor with the refusal **above** the rows and a "Nothing was changed. Refused: writing "release"." line; **the typed row is gone** where the board promises it stays (LOG-42). Neither shows the key rule (1 to 128) or the 50-tag limit (the board is silent: LOG-3, LOG-21). |
| Logs-17-Confirms | differs: build 11 | The board's confirms are cards with a bold question, a body and "Keep it" / "Delete log group" buttons, and a "What does not ask" card. The page's are one-line questions with Cancel / Delete (run, all four). The group confirm's body must name metric filters (corrected on the board) and is true only after LOG-2. |
| Logs-18-Bad-Unfiring | superseded (decision) | Not built as drawn. With LOG-1 closed by implementing or refusing on save, a filter that cannot fire cannot be created; it survives only for a data directory that already holds one, where the Metric filters tab marks the row ("pattern not read here", "value is not a number"). The page today has no mark (run: the rows show `%slot.*%` and `fast` plainly). Rows are sorted by name (corrected on the board). |
| Logs-19-Bad-Gone | differs: build 12 | The left half is a narrative with no page equivalent (it describes what happens). The right half (a "not in this stack" mark on a subscription whose function went) is missing: the chip is still a link (LOG-15). The "default retention" badge after a recreate matches (run). |

**Where the design was wrong** (corrected in the working copy on 2026-10-11 in
Logs-02, -03, -04, -10, -11, -13, -17 and -18; the published canvas still
carries the old text until it is republished)
- **Logs-13**: "Kinesis … Keyed by a random value" is not AWS's default; the model:
  "By default, log data is grouped by log stream". Now: "Keyed by the log stream
  by default (distribution ByLogStream), or by a random value with
  distribution Random, which spreads across shards."
- **Logs-10, Logs-13, Logs-18**: rows in an order the model says the service
  does not return ("The results are ASCII-sorted by filter name"). Rows
  reordered (`slot-missing` before `validator-errors`; `archive` before
  `errors-to-notifier`; `duration`, `slot-regex`, `validator-errors`).
- **Logs-11**: "It does not show the number a `$.field` value would read from a
  matched line." The model's TestMetricFilter answers `extractedValues`. Now:
  "Which lines the pattern takes, and the values it extracted from each
  (TestMetricFilter answers extractedValues for a match)." The match table
  needs a column for it (build 8).
- **Logs-17**: the group confirm lists streams, lines, subscription filters and
  tags and leaves out metric filters, which LOG-2's fix removes. Now includes
  "its metric filters".
- **Logs-02, -03, -04**: the name rule "1 – 512 characters · letters, digits
  and . - _ / #" omits "can't start with the string aws/" (`model`). Now: "…
  / # · not starting with aws/ · enforced".
- **Left alone on purpose**: the code `ValidationException` on Logs-04, -06,
  -12, -14, -16 (LOG-19, `memory` until recorded); "Without one, lines are kept
  for a day" on Logs-Home (a statement about this stack, true; LOG-35 is the
  code's to fix, not the sentence's); the board's silence on `defaultValue`,
  `dimensions` and tag keys/limit (a design gap, not a false fact; build 8, 10).

**Build list, in order** (the console half of section 11, step 10; none of it
changes behaviour except where a board needs one, which is named)
1. **Shell and list pane**: the footer line (Home), the empty-state copy
   without backticks (LOG-41) and in the board's voice, "New" in the pane
   header. (Shell chrome itself: the foundation; see the decision above.)
2. **Create**: two columns with the "Who writes to a group" panel and the
   board's subtitle; the "Two calls, not one" panel beside the cli preview
   (Logs-02, -03).
3. **Tail and group header**: "Written by" (the `/aws/lambda/` prefix against
   `ListFunctions`), the range selector (a query parameter, `FilterLogs`'
   `since`), the wide filter box, the foot sentence, the count wording and the
   cap sentence (LOG-14, with `nextToken` followed), the three cards of
   Logs-07, the right-hand panels of Logs-08, 09 and the per-stream `tail`
   link; `init` only where a request id could exist (LOG-13). Behaviour:
   FilterLogs follows tokens and `startFromHead` (LOG-23 is its dependency).
4. **Teaching lines, all five, on the four forms**, from a new
   `logs/settings.go` record per setting (L11): Create (name, retention),
   Add a metric filter (name, pattern, namespace, metric name, value, unit,
   and default value and dimensions if the design is extended, decision
   below), Subscribe (name, pattern, destination, distribution if offered),
   Retention. Line four carries "enforced" or "stored" from the record's tier
   (so `kmsKeyId` and the C members read as cosmetic wherever shown).
5. **Retention**: all 22 values with the current one always present (LOG-11),
   the spoken unit, the "'default' is not 'never expire'" card, "How it fits
   this group" (needs `ListLogMetricFilters` and `ListLogSubscriptions` on the
   settings tab).
6. **Refusals**: the refusal above the form with "What you typed is kept…",
   the right-hand "Other refusals this form can come back with" column on
   Create, Add a metric filter and Subscribe, the tail's inline refusal
   (LOG-10), and the vocabulary from A2 per rule.
7. **Tail 900 wide**: tab labels that do not wrap, the chips on one line
   (the 1024 floor is the console's; confirm the decision).
8. **Metric filters**: two columns with "Where it goes", "2 of 100" and
   "Evaluated as lines arrive"; the tester as its own view with "What it
   answers" and the `extractedValues` column (needs LOG-7); the form's row gap
   (LOG-45); the unit list stays 6 of 27 with the sentence, or offers all 27
   (decision: offering all 27 is cheaper than the sentence).
9. **Subscriptions**: two columns with "What a destination receives", "One
   attempt", "Under IAM enforce"; the destination as a mark and a link; "not in
   this stack" (LOG-15, build 12).
10. **Tags**: the "What a tag value may be" card (and the key rule and the 50
    limit once LOG-3 and LOG-21 are closed); keep the draft rows on a refusal
    (LOG-42, foundation A5).
11. **Confirms**: bodies and "Keep it" buttons on the four, true to the code
    (after LOG-2: metric filters named).
12. **Bad days**: the "not in this stack" mark; the default-retention badge
    after a recreate already matches.

**Decisions this review asks of a human** (also in section 10): (a) shell
chrome before or after Logs' stage; (b) whether the metric-filter form gains
`defaultValue` and `dimensions` (the design is silent, the model has both, and
a template can set them); (c) whether a missing `%regex%` and space-delimited
language is implemented in this stage or refused by name with "Refused here"
(LOG-1); (d) the default retention and LOG-35; (e) deletion protection F or
declined (LOG-8).

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store | Delete removes metric filters (LOG-2); metric-filter count per group (LOG-4); a 2-day-old event survives a sweep in a group with no policy (LOG-35) |
| Handler, SDK v2 | Pattern and value refused (or accepted) on PutMetricFilter (LOG-1); PutLogEvents limits and `rejectedLogEventsInfo` (LOG-5); missing destination (LOG-6); DescribeMetricFilters paging, filters, missing group, `extractedValues` (LOG-7, LOG-29); `kmsKeyId`, `logGroupClass`, deletion protection (LOG-8); `aws/` prefix (LOG-9); `endTime`, `startFromHead`, `searchedLogStreams` (LOG-22 to LOG-24); name and identifier together, ARN identity (LOG-25, LOG-26); DescribeLogGroups and ListLogGroups filters (LOG-27, LOG-28); one transformation, three dimensions, dimension values, `defaultValue` (LOG-30 to LOG-32); distribution default (LOG-33); the 50-tag limit (LOG-21) |
| Rejection parity | Regenerate `testdata/cases_logs.json` (264 cases at the review) so the list lengths and map keys are in it; tag keys on TagResource, TagLogGroup, CreateLogGroup (LOG-3); the new constraint-table guard (LOG-18); the code on every case (LOG-19) |
| Pattern language | One table over all four entry points: every example of the User Guide page, accepted or refused identically (LOG-37) |
| Writers | `logship`: 25,000 lines chunk at 10,000 events and 1,048,576 bytes (LOG-38) |
| Restart | Metric filters, tags, retention and deletion protection added to `persistence_surfaces_test.go` |
| Second identity | The package suite under another account and region (foundation B2); the ARN tests of LOG-26 |
| Console handler | Tail refusal (LOG-10); retention 400 round trip (LOG-11); default and dimensions shown (LOG-12); no `init` on an app group (LOG-13); count wording and the 500 cap (LOG-14); missing destination marked (LOG-15); empty-state copy (LOG-41); a metric-filter handler test, which does not exist |
| Conformance (boto3) | Subscription filter to Lambda and to Kinesis; metric filter with a literal and a `$.field`; tags both spellings; retention values; every row in section 8 |
| Browser | Each `new` item; each of the four confirms' sentences and bodies; the tail refusal; Settings at 400 days; keyboard paths on the list and Create; the typed tag row stays (LOG-42) |
| Fidelity | The 6a table re-made from renders after the build; each board's real page beside it |
| Mutation | `logs/` baseline, then survivors in `filter.go`, `metricfilters.go`, `store.go` `Sweep` and `Scan` |

## 8. Recording queue

Every one of these is `doc` or `memory` today. *Corrected 2026-10-11: rows
whose source is now the model are marked and need no recording of the
number, only of the code or message where one is named.*

| Claim | Source now | Scenario |
|---|---|---|
| PutLogEvents limits: events per batch, batch bytes, event bytes, 14 days back, 2 hours ahead, 24 h span; which are errors and which are `rejectedLogEventsInfo` | **model** (was doc); the message of the failing call: memory | `test_logs::put_limits` |
| A group name starting `aws/` is refused, and with what code | model for the rule (was doc); code memory | `reserved_prefix` |
| DeleteLogGroup removes the group's metric and subscription filters | memory (the model says only "archived log events"; was doc) | `delete_group_takes_filters` |
| PutMetricFilter with a non-numeric literal `metricValue` | memory | `metric_value_not_number` |
| PutMetricFilter and PutSubscriptionFilter with a malformed pattern: code and message | memory | `bad_pattern_on_put` |
| A regex and a space-delimited pattern are accepted by all four users | regex **model**, space-delimited **doc** (was doc) | `pattern_forms` |
| A term matches as a whole word or as a substring | memory; case-sensitivity is **doc** | `term_matching` |
| PutSubscriptionFilter to a missing function, a missing stream, and a function with no permission for `logs.amazonaws.com` | memory | `subscription_missing_destination` |
| A function subscribed to its own log group: accepted or refused | memory | `subscription_own_group` |
| Two subscription filters per group; the third's code | **model** for two and `LimitExceededException` listed (was doc) | `subscription_limit` |
| 100 metric filters per group; the 101st's code | **model** (was doc) | `metric_filter_limit` |
| `metricFilterCount` and `storedBytes` in DescribeLogGroups | **model** (was doc) | `group_counters` |
| TestMetricFilter `extractedValues` for a `$.field` pattern, and the key spelling for a space-delimited pattern | **model** that it is returned (was doc); key spelling memory | `test_metric_filter_extracts` |
| DescribeLogGroups with prefix and pattern together | **model**: refused; the code memory (was memory) | `describe_prefix_and_pattern` |
| CreateLogGroup with `kmsKeyId` for a key that does not exist (`InvalidParameterException`, model); with `logGroupClass` | model for the code; existence needs KMS | `create_group_options` |
| Tags per group limit, and the code past it | **model**: 50, `TooManyTagsException` on TagResource (was memory); TagLogGroup past 50: memory | `tag_limit` |
| No retention policy means never expire | **model** (was doc) | `retention_default` |
| A deleted Lambda log group is recreated on the next invocation, without retention | memory | owed to integration; needs Lambda |
| DescribeLogStreams ordered by `LastEventTime` with a prefix is refused | **model** (was doc) | `streams_order_with_prefix` |
| The code an unsupported operation would carry | n/a; settled by D5 (501 `NotImplemented`) | none |
| A constraint violation answers `InvalidParameterException` or `ValidationException` (LOG-19) | memory | `test_refusals` |
| An invalid or expired `nextToken`: code and message (LOG-36) | memory | `invalid_next_token` |
| DeleteLogGroup of a deletion-protected group: the code (LOG-8) | memory | `deletion_protection` |
| FilterLogEvents `startFromHead: false` with no `startTime` (LOG-23) | memory | `filter_from_tail` |
| ListLogGroups `logGroupNamePattern`: the regex dialect (`.`, wildcards) | memory | `list_groups_pattern` |
| `eventId` length (the ledger said 56 digits) | memory | `event_id_shape` |
| The test message a new subscription filter sends its destination (`CONTROL_MESSAGE`) | memory | `subscription_control_message` |
| A dimension value's `$.field` reference syntax and an absent field (LOG-31) | the model says "fields"; the spelling memory | `metric_dimensions` |
| An empty `metricNamespace`, `metricName` or `metricValue` (LOG-30) | memory | `metric_empty_members` |

## 9. Owed to integration

| What | With |
|---|---|
| A subscription filter delivers to a function and to a stream; a missing destination; delivery under enforce with and without a `logs.amazonaws.com` statement | Lambda, Kinesis, IAM |
| A metric filter's datum arrives in CloudWatch with its unit and dimensions (and with a default value on a period nothing matched, LOG-32), and an alarm on it changes state | CloudWatch |
| A function's lines arrive under `/aws/lambda/<name>` with request ids; a deleted group or stream is recreated; `[lambda].quiet` | Lambda |
| The other writers land where the ledger says and chunk at the PutLogEvents limits: Step Functions (configured group, and `/aws/vendedlogs/states/` for Express), API Gateway (access and execution), EventBridge (log-group target), SNS (delivery status) | each of them (LOG-38) |
| `log_tail` on the Lambda and Step Functions pages shows a refusal as a refusal (LOG-10 is fixed in the shared partial; their specs must be re-run) | Lambda, Step Functions |
| `AWS::Logs::LogGroup`, `SubscriptionFilter`, `MetricFilter` through CloudFormation, SAM, CDK and Terraform (`RetentionInDays`, `KmsKeyId`, `LogGroupClass`, `DeletionProtectionEnabled`, `Tags`; dimensions and `DefaultValue` on the metric filter); what a declared `AWS::Logs::LogStream` should do now that PutLogEvents needs one (LOG-16) | CloudFormation |
| A template that declares a group **and** a function: retention survives the function's first line | CloudFormation, Lambda |
| Export round trip: a group with filters exported by `doze-aws export` and applied again | CloudFormation |
| `kmsKeyId` for a key that does not exist or is disabled is `InvalidParameterException` | KMS |
| Sweep and the 100,000 cap under a function printing continuously | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] LOG-1 to LOG-45 each closed, with the test that failed first named beside it.
- [ ] Decided and written into `SUPPORT.md`:
      - a missing subscription destination: refused at put, or accepted and the
        ledger sentence corrected (LOG-6);
      - `kmsKeyId`: stored and returned, cosmetic (LOG-8);
      - `logGroupClass` and `deletionProtectionEnabled`: a tier each, and
        `PutLogGroupDeletionProtection` moved from S to F or declined with
        the consequence stated (LOG-8);
      - the `aws/` prefix (LOG-9);
      - the PutLogEvents limits modelled, and any deliberately not (LOG-5),
        with the writers chunking (LOG-38);
      - what an unreadable pattern answers on PutMetricFilter, the same as on
        the other three entry points, and whether `%regex%` and
        space-delimited are implemented or refused by name (LOG-1);
      - the default retention against event timestamps (LOG-35);
      - the refusal code family for a constraint violation (LOG-19);
      - the time-based rules of L8, listed under "Differences from AWS".
- [ ] The per-member table in section 2 has no "?" left, and every C member
      reads as cosmetic in the ledger and wherever the console shows it. (The
      review resolved every "?" that was in it.)
- [ ] Every "not established" in this document is resolved or is a row in
      section 8 with its test written and skipped: the deletion-protection
      refusal code; the token refusal code; whole-word term matching; a swept
      stream's `firstEventTimestamp`; the own-group refusal on AWS; the
      message of the PutLogEvents failures; the `eventId` length.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] A filter pattern is refused or accepted identically by FilterLogEvents,
      TestMetricFilter, PutSubscriptionFilter and PutMetricFilter, held by one
      table test over all four, built from the User Guide page's examples.
- [ ] After DeleteLogGroup nothing of the group remains in any bucket, held by a
      store test that walks every bucket.
- [ ] `constraintTables` is generated, or a test fails when it lacks a line
      `dzaudit list cloudwatch-logs` emits for a dispatched operation; the
      committed cases are regenerated and `knownGaps` is empty.
- [ ] A data directory that already holds an unreadable metric filter still
      opens, and the Metric filters tab marks that filter (the part of Logs-18
      that survives LOG-1).
- [ ] **Fidelity:** section 6a exists, was made from rendered boards beside
      rendered pages (light and dark, and 900 wide for the main detail page),
      and no row says "differs" without a decision beside it: each is built,
      or is a deliberate deviation with its reason written in the row.
- [ ] **Icons:** the AWS icon from `internal/console/static/aws/logs.svg`
      stays in the rail, the list-pane header and the group header; no board's
      `LG` mark is built.
- [ ] **Teaching lines:** all five on every setting of Create, Add a metric
      filter, Subscribe and Retention, written from `logs/settings.go`'s
      records, with line four "enforced" or "stored" from the tier.
- [ ] **Wires:** one protocol, `awsJson1_1`; every one of the 25 operations
      has an accepted-as-the-reference-spells-it test and the rejection-parity
      case asserts the code; the S operations answer 501 `NotImplemented`
      with the header.
- [ ] **Asynchronous operations decided (L7):** the table in the pre-stage
      review is in `SUPPORT.md` "Differences from AWS"; no board draws an
      in-progress state; export, query and live-tail stay S.
- [ ] Boards: 19 of 20 implemented; Logs-18 reduced as above. No statement on a
      Logs screen is in the "not yet backed" list in section 6.
- [ ] The four confirms say only what the code does, including what a running
      function does to a deleted group, and carry the board's bodies.
- [ ] The Settings menu shows whatever retention the group has, and Save with
      nothing changed changes nothing.
- [ ] `log_tail` behaves the same on all three pages that render it.
- [ ] `e2e/tests/logs.spec.ts` covers every route under `/logs` with the route
      gate on (13 routes in `logsRoutes`), and the accessibility sweep is clean
      on all six tabs and Create.
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1 against the L estimate.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: member tiers, the decisions in section 10, the refusal shape | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for LOG-1 to LOG-9 and LOG-18 to LOG-37 | `logs/*_test.go`, `persistence_surfaces_test.go` |
| 3 | Regenerate the constraint table and cases; the guard test | `logs/validate.go`, `logs/testdata/cases_logs.json`, `logs/rejection_parity_test.go` (`cmd/dzaudit` already emits the lines) |
| 4 | Delete takes everything; counts; reserved prefix; deletion protection; group view | `logs/store.go`, `logs/actions.go`, `logs/metricfilters.go` |
| 5 | PutLogEvents limits and `rejectedLogEventsInfo`, with the writers chunking (same commit) | `logs/actions.go`, `internal/logship/logship.go`, the other writers |
| 6 | The pattern language: one gate for all four entry points, JSON gaps, regex, space-delimited, `extractedValues`, literal value check | `logs/filter.go`, `logs/actions_metricfilters.go`, `logs/subscriptions.go`, `logs/metricemit.go` |
| 7 | Reads: `endTime`, `startFromHead`, `searchedLogStreams`, name-or-identifier, ARN identity, describe and list filters, tokens | `logs/actions.go` |
| 8 | Metric and subscription members: one transformation, dimensions, `defaultValue`, `distribution`, the dropped members | `logs/actions_metricfilters.go`, `logs/metricemit.go`, `logs/fanout.go`, `logs/subscriptions.go` |
| 9 | Tags: keys, the 50 limit, `TooManyTagsException` | `logs/validate.go`, `logs/actions.go` |
| 10 | Retention against event timestamps; the S refusal shape | `logs/store.go`, `logs/logs.go` |
| 11 | Subscription destination decision | `logs/subscriptions.go`, `docs/SUPPORT.md` |
| 12 | Section 5 sweep | `logs/sdk_test.go`, `logs/subscriptions_test.go`, `logs/metricfilters_test.go` |
| 13 | Settings records | `logs/settings.go`, `internal/console/settings_test.go` |
| 14 | Conformance scenarios and claims | `conformance/tests/test_logs.py`, the claims file from foundation B1 |
| 15 | Console: the 12 items of the build list in 6a | `internal/console/templates/logs.html`, `templates/create.html` (`logs_create`), `copy.go`, `handlers_logs.go`, `client_logs.go`, `routes_compute.go`, `logs_test.go`, `static/app.css` |
| 16 | Browser tests, fidelity table re-made | `e2e/tests/logs.spec.ts`; re-run `lambda.spec.ts` and the Step Functions specs for `log_tail` |
| 17 | Stale text; ledger, changelog, status | `logs/logs.go`, `internal/cfn/resources.go`, `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 to 6 are the P0s and P1s that change what a deploy sees and come
before anything else in the package. Step 15 depends on foundation A2 (the
refusal classes), A5 (tags), A13 (confirms) and the settings accessor; if any
of those slipped, the teaching lines and the tail refusal wait for it and the
rest of step 15 does not. The shell decision (6a) is outside this stage.

**Prior art.** AWS's "Filter pattern syntax for metric filters, subscription
filters, filter log events, and Live Tail" page in the CloudWatch Logs user
guide is one page for all four users of the language, which is the argument for
step 6's single gate (and its examples are the test table). The PutLogEvents
operation text lists the batch limits in its opening section and, as the
model's own documentation, is the source for LOG-5 until a recording replaces
it. LocalStack's logs service models the sequence-token refusal and a subset of
the pattern language; neither is a reason to copy a subset here when the
model's text states the rest.
