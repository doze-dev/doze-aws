# CloudWatch Logs

Stage 11. Size M. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds Logs' own.

## 1. Summary

Log groups, streams and events over one bbolt store (`logs.bolt`, no fsync per
batch), on one wire protocol (AWS JSON 1.1, target `Logs_20140328`). Two workers
hang off `PutLogEvents`: the subscription fan-out (to Lambda and Kinesis) and
the metric-filter emitter (to CloudWatch). A sweeper runs every minute.

Peers. Writers: Lambda, Step Functions, API Gateway, EventBridge and SNS, all
through `internal/logship` and `internal/peercall`. Destinations: Lambda and
Kinesis (subscription filters), CloudWatch (metric filters). CloudFormation and
`provision` create groups, subscription filters and metric filters.

It is M and not S because the read path is small but three things are wrong in
ways a deploy sees (LOG-1, LOG-2, LOG-3), and the tail, which is the page a
developer has open all day, misreports a refusal as an empty group (LOG-10).

**Estimate:** M, three to four focused days. **Actual:** _fill in when done._

## 2. Contract

The model (`com.amazonaws.cloudwatchlogs`) lists 118 operations. 25 are
dispatched (`logs/actions.go`, `handlers`); 93 are refused by name with
`UnsupportedOperationException` (`logs/validate.go`, `notHere`). Nothing reaches
`InvalidAction`.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateLogGroup, DeleteLogGroup | F | F | Delete leaves metric filters behind (LOG-2). `kmsKeyId` and `logGroupClass` are dropped silently (LOG-8). The `aws/` prefix is accepted (LOG-9). |
| DescribeLogGroups, ListLogGroups | F | F | `metricFilterCount` is always 0 (LOG-4); `storedBytes` is always 0; the `logGroupClass` filter is validated and ignored. |
| PutRetentionPolicy, DeleteRetentionPolicy | F | F | 22 values; the sweeper honours them. No policy means a day here, never on AWS (a listed difference). |
| CreateLogStream, DeleteLogStream, DescribeLogStreams | F | F | `storedBytes` always 0. |
| PutLogEvents | F | F | Stream must exist; batch must be in time order. No size, count or timestamp-window limits (LOG-5). `sequenceToken` ignored, as AWS has done since 2023 (doc). |
| GetLogEvents | F | F | |
| FilterLogEvents | F | F | Pattern language is a subset (per-setting table below). `requestId` is a doze extension. |
| TagResource, UntagResource, ListTagsForResource | F | F | Tag keys are not validated on write (LOG-3). |
| TagLogGroup, UntagLogGroup, ListTagsLogGroup | F | F | The deprecated spellings. Same gap. |
| PutSubscriptionFilter, DeleteSubscriptionFilter, DescribeSubscriptionFilters | F | F | Destination existence is not checked (LOG-6). |
| PutMetricFilter, DeleteMetricFilter | F | F | Pattern and literal value are not checked on save (LOG-1). |
| DescribeMetricFilters | F | F | Ignores `limit`, `nextToken`, `metricName`, `metricNamespace` (LOG-7). |
| TestMetricFilter | F | F | `extractedValues` is always empty (LOG-7). |
| Logs Insights: queries, query definitions, scheduled queries, lookup tables, log fields and records (24) | S | S | Declined. |
| StartLiveTail (1) | S | S | Declined; `aws logs tail --follow` polls FilterLogEvents. |
| Destinations (4) | S | S | Declined. |
| Deliveries, delivery sources and destinations, configuration templates (17) | S | S | Deferred. |
| Export and import tasks (7) | S | S | Deferred. |
| Anomaly detectors and anomalies (7) | S | S | Declined. |
| Account, data-protection, index, storage-tier, resource and deletion-protection policies, bearer tokens (17) | S | S | Declined. |
| Transformers (4), integrations and S3 Table sources (7), KMS association (2), syslog configurations (3) | S | S | Declined. |

"The operation is F" hides which members do anything:

| Operation | Member | Target | To confirm |
|---|---|---|---|
| CreateLogGroup | `logGroupName`, `tags` | F | Name: 1 to 512, pattern from the model. |
| CreateLogGroup | `kmsKeyId` | C | Dropped today, not stored, not returned. Either store and return it, or refuse it (AssociateKmsKey is S). LOG-8. |
| CreateLogGroup | `logGroupClass` | ? | Enum is validated; the group always reports `STANDARD`. Not established what `INFREQUENT_ACCESS` should do here. LOG-8. |
| CreateLogGroup | `deletionProtectionEnabled` | ? | Not established whether the model's current CreateLogGroup carries it and what the handler does with it. |
| DescribeLogGroups | `logGroupNamePrefix`, `logGroupNamePattern`, `logGroupIdentifiers`, `limit`, `nextToken` | F | Prefix and pattern together: not established (AWS refuses the pair, memory). |
| DescribeLogGroups | `logGroupClass`, `accountIdentifiers`, `includeLinkedAccounts` | ? | Validated, then ignored. |
| group view | `storedBytes` | C | Always 0. The ledger must say so. |
| group view | `metricFilterCount` | F | Wrong today (LOG-4). |
| PutLogEvents | `logEvents[].timestamp`, `.message` | F | |
| PutLogEvents | `sequenceToken` | C | Ignored, as on AWS. |
| PutLogEvents | `entity` | ? | Length-checked, then ignored. |
| PutLogEvents | `logEvents[].requestId` | extension | Not an AWS member. |
| FilterLogEvents | `filterPattern`: terms, phrases, `-term`, `?term`, JSON | F | |
| FilterLogEvents | `filterPattern`: `%regex%`, `[space, delimited]` | S | Refused by name. AWS accepts both (doc). A listed difference. |
| FilterLogEvents | `logStreamNames`, `logStreamNamePrefix`, `startTime`, `endTime`, `limit`, `nextToken` | F | |
| FilterLogEvents | `interleaved`, `unmask` | C | Ignored. Output is always interleaved. |
| GetLogEvents | `startFromHead`, `startTime`, `endTime`, `limit`, `nextToken` | F | |
| GetLogEvents | `unmask` | C | Ignored. |
| PutSubscriptionFilter | `filterName`, `filterPattern`, `destinationArn` (Lambda, Kinesis) | F | |
| PutSubscriptionFilter | `destinationArn` (Firehose, a Logs destination) | S | Refused by name. |
| PutSubscriptionFilter | `distribution` | F | Kinesis partition key only. |
| PutSubscriptionFilter | `roleArn` | C | Stored and returned; gates nothing. |
| PutSubscriptionFilter | `fieldSelectionCriteria`, `applyOnTransformedLogs`, `emitSystemFields` | ? | Not established. |
| PutMetricFilter | `metricNamespace`, `metricName`, `metricValue`, `defaultValue`, `unit`, `dimensions` | F | Only the first three are checked for presence. |
| PutMetricFilter | `fieldSelectionCriteria`, `applyOnTransformedLogs`, `emitSystemFields` | ? | Not established. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `logs/`: 19 tests in 8 files (SDK v2 6, subscriptions 2, close race 3, fan-out audit 2, conformance fixes 2, metric-filter logic 2, rejection parity 1, coverage model 1). `internal/logship/`: 3. |
| Parity | `rejection_parity_test.go` replays `testdata/cases_logs.json`; the ledger says 237 of 237 model constraints enforced across the 25 dispatched operations, `knownGaps` empty. LOG-3 shows the count is of the constraints the table holds, and the table has no tag-key rule on write. |
| Root tests | `persistence_surfaces_test.go` writes a group and a subscription filter and reads them after a restart. Metric filters are not in it. |
| Console tests | `internal/console/logs_test.go`: 2 (pages, subscriptions). Metric filters have no handler test. |
| Conformance | `conformance/tests/test_logs.py`: 3 scenarios (lifecycle, write/read/filter, refusals), all **unverified** (`conformance/snapshots/` is empty). Nothing on subscription filters, metric filters, tags or retention sweep. |
| Browser | `e2e/tests/logs.spec.ts`: 4 tests. |
| Mutation | No per-package baseline (foundation B3). |

The SDK suite is Go v2 only. There is no test under a non-default account or
region (foundation B2), and group ARNs are minted from `store.id`, so that pass
is expected to hold; it has not been run.

## 4. Findings

Evidence `run` means reproduced against a binary built from this tree on
2026-10-10, through the console routes and the JSON endpoint. AWS source is
stated as it is: where the page is named but was not re-read that day, it says
`doc`; where it is recall, `memory`.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| LOG-1 | P0 | `PutMetricFilter` never compiles the pattern and never parses a literal `metricValue`. A filter with `%ERR.*%` or value `lots` is stored, listed and answered 200; the emitter skips it and writes one line to doze-aws's own log. TestMetricFilter, PutSubscriptionFilter and FilterLogEvents all refuse the same pattern. A silent no-op. | `logs/actions_metricfilters.go` `putMetricFilter`; `logs/metricemit.go` `filtersFor`; `logs/metricfilters.go` `metricValueOf` | run | pattern: n/a (the refusal is ours, AWS accepts regex, doc). Non-numeric literal refused by AWS: memory | SDK test: PutMetricFilter with `%x%` and with `[a, b]` is refused with the code FilterLogEvents uses; with `metricValue: "lots"` per the recording |
| LOG-2 | P0 | `DeleteLogGroup` removes streams, events and subscription filters and leaves metric filters. `DescribeMetricFilters` still returns them for the deleted group, and a group recreated under the same name (which Lambda does on its next line) has them back and publishing. `deleteLogGroup` also calls `fan.forget` and not `met.forget`. | `logs/store.go` `DeleteGroup`; `logs/actions.go` `deleteLogGroup` | run | doc: deleting a group deletes what is attached to it | Store test: put filter, delete group, `MetricFilters(group)` is empty; SDK test: recreate, put a matching line, no metric published |
| LOG-3 | P1 | Tag keys are not validated on write. `tagsMap` constrains map **values** only, so `TagResource`, `TagLogGroup` and `CreateLogGroup` store `bad!key`. `UntagResource` validates keys, so that tag can then never be removed, and every later console Save that would drop it is refused. | `logs/validate.go` `tagsMap`, `tagKeys` | run | model: the `Tags` map key is `TagKey` (1 to 128, pattern) | Rejection-parity case for a bad key and a 129-character key on all three writers |
| LOG-4 | P1 | `DescribeLogGroups` answers `metricFilterCount: 0` whatever the group holds. | `logs/actions.go` `groupView` | run (3 filters, count 0) | doc: DescribeLogGroups, `LogGroup.metricFilterCount` | SDK test: one filter, count 1; delete it, count 0 |
| LOG-5 | P0 | `PutLogEvents` enforces time order and nothing else: no limit on events per batch, batch bytes, event bytes, or how old or how far ahead a timestamp is, and no `rejectedLogEventsInfo`. A writer that will be refused or silently trimmed on AWS succeeds here. | `logs/actions.go` `putLogEvents` | read | doc: PutLogEvents API reference (10,000 events, 1,048,576 bytes with 26 per event, 14 days back, 2 hours ahead, batch span 24 h). Exact numbers and which are errors versus `rejectedLogEventsInfo`: record | Handler tests for 10,001 events, an oversize batch, a 15-day-old event, a +3 h event |
| LOG-6 | P1 | `PutSubscriptionFilter` accepts a Lambda function or Kinesis stream that does not exist. `SUPPORT.md` says "A Kinesis stream must exist before the filter names it"; it need not. Each matching batch then fails one delivery, logged only to doze-aws's output. | `logs/subscriptions.go` `checkDestination`; `docs/SUPPORT.md` Subscription filters | run | memory: AWS test-delivers and answers `InvalidParameterException` | SDK test with a missing stream and a missing function; then decide (section 10) and fix either the code or the ledger |
| LOG-7 | P1 | `DescribeMetricFilters` ignores `limit`, `nextToken`, `metricName` and `metricNamespace`: every filter comes back on page one, unfiltered. `TestMetricFilter` returns `extractedValues: {}` for every match. | `logs/actions_metricfilters.go` `describeMetricFilters`, `testMetricFilter` | read | doc: both API reference pages | SDK test: three filters, `limit: 1`, expects a token; `metricName` narrows; a `$.field` pattern returns the extracted value |
| LOG-8 | P1 | `CreateLogGroup` drops `kmsKeyId` (not stored, not returned) and ignores `logGroupClass` (validated, then every group reports `STANDARD`). `DescribeLogGroups` validates a `logGroupClass` filter and does not apply it. Success with no effect and nothing says so. | `logs/actions.go` `createLogGroup`, `groupView`, `describeLogGroups` | read | doc | SDK test: create with each, read back; decide C (store, return, ledger says so) or refuse |
| LOG-9 | P1 | A group named `aws/foo` is created. | `logs/actions.go` `createLogGroup` | run | doc: CreateLogGroup API reference, "can't start with the string `aws/`". The error code: record | Rejection test once the code is recorded; written and skipped until then |
| LOG-10 | P2 | The tail renders any FilterLogEvents error as "Nothing printed yet. The group … appears the first time the function writes a line." A `%regex%`, a `[a, b]`, a malformed JSON pattern and a 1025-character pattern all show it. Shared by the group page, the Lambda Logs tab and the Step Functions Logs tab. | `internal/console/handlers_logs.go` `logTailData` (`missing := err != nil`); `templates/logs.html` `log_tail` | run | n/a | Handler test: `GET /logs/tail?q=%25x%25` shows the code and message and not the "nothing printed" copy |
| LOG-11 | P2 | The Settings and Create forms offer 12 retention values; the service accepts 22. A group at 400 days (set by CLI or a template) has no option selected, so the menu shows "default", and pressing Save clears the policy. The label "the values AWS accepts" is false. | `templates/logs.html` `logs_group` settings; `templates/create.html` `logs_create`; `logs/actions.go` `validRetention` | run | model/doc for the list | Handler test: retention 400, the settings page marks 400 selected; saving unchanged keeps 400 |
| LOG-12 | P2 | The Add form cannot set `defaultValue` or `dimensions`, offers 6 of the model's 27 units, and the list shows only a filter's first transformation. A filter made by a template with a default or dimensions shows neither. | `templates/logs.html` metrics tab; `client_logs.go` `ListLogMetricFilters`, `PutLogMetricFilter` | read | model | Handler test: a filter with a default and two dimensions renders both |
| LOG-13 | P2 | A line with no request id shows `init` with the tooltip "init output, before the first invocation". True for a function's init output; false for every line an application, API Gateway or SNS wrote. | `templates/logs.html` `log_tail` | run | n/a | Handler test on an app group: no `init` label |
| LOG-14 | P2 | The count always ends "· last hour". With a request id chosen `FilterLogs` sets `startTime` to 0, so the window is the whole invocation. The tail also keeps only the newest 500 lines and shows "500 lines" without saying more exist. | `client_logs.go` `FilterLogs`; `handlers_logs.go` `logTailData`; `logs.html` `log_tail` | read | n/a | Handler tests: `?rid=` label; 501 lines says so |
| LOG-15 | P2 | A subscription's destination is rendered as a link from the ARN alone, so a function or stream that is gone is a link to a page that does not exist, with no sign the filter does nothing. | `templates/logs.html` subscriptions tab; `resourceurl.go` `resourceFromARN` | run | n/a | Handler test: destination absent from the function list is marked |
| LOG-16 | P3 | Stale text. The package comment lists metric filters among what "answers UnsupportedOperationException". CloudFormation declines `AWS::Logs::LogStream` because "a stream is created by the first PutLogEvents", which stopped being true when PutLogEvents began refusing a missing stream. | `logs/logs.go` package comment; `internal/cfn/resources.go` | read | n/a | None for the comment; a CloudFormation test declaring a LogStream decides the second (section 9) |
| LOG-17 | P3 | The toast after Create filter says "now publishes <metric>" whatever was saved. Closed by LOG-1 for unreadable filters; the wording still promises a publication that needs a matching line first. | `handlers_logs.go` `logsPutMetricFilter` | run | n/a | Browser test asserts the new wording |

Owned by the foundation, not repeated here:

- **A2** — the "because AWS does" line is printed for Logs' doze-only refusals:
  regex and space-delimited patterns (run), a Firehose destination (run), a
  function subscribing to its own group (run; whether AWS refuses that is not
  established, section 8).
- **A5** — tag Save writes one key at a time and stops at the first refusal.
  LOG-3 is what makes it bite here.
- **A11** — the Create form's CLI preview shows `create-log-group` and omits the
  `put-retention-policy` the form also sends.
- **A13** — the Delete confirm, "and everything in it", is false until LOG-2 is fixed.

Not a bug, recorded so it is not rediscovered: deleting a group a function
still prints to brings it back on the next line, with no retention and no tags
(`internal/logship` `ship`; run, with a scratch test against the real service).

## 5. What to check

Beyond the common criteria. One protocol, so each line is checked once, through
the Go SDK and through boto3.

**Groups**
- [ ] Name: 1 to 512, the model's pattern, and the `aws/` prefix (LOG-9).
- [ ] Create twice answers `ResourceAlreadyExistsException`; delete of a missing
      group answers `ResourceNotFoundException`.
- [ ] Delete removes streams, events, subscription filters, metric filters and
      tags, and both workers forget the group (LOG-2).
- [ ] `kmsKeyId`, `logGroupClass`, `deletionProtectionEnabled`: each has a tier
      and behaves as its tier says (LOG-8).
- [ ] DescribeLogGroups: prefix, pattern, identifiers (name and ARN, with and
      without `:*`), `limit` 1 to 50, token paging across more than 50 groups,
      prefix and pattern together.
- [ ] ListLogGroups: the `a|^b` pattern form, `limit` to 1000, paging.
- [ ] `metricFilterCount` is true (LOG-4); `storedBytes` is 0 and the ledger says so.

**Retention**
- [ ] All 22 values accepted, any other refused with the message naming the list.
- [ ] DeleteRetentionPolicy returns the group to the instance default.
- [ ] The sweep drops lines past retention, honours `[logs].retention` for
      groups with none, and caps a group at 100,000 events oldest first.
- [ ] A stream's `firstEventTimestamp` after its first lines were swept: not established.

**Streams**
- [ ] Name: 1 to 512, no `:` or `*`. Create twice, delete missing.
- [ ] DescribeLogStreams: prefix, `orderBy` both ways, `descending`, paging,
      and `LastEventTime` with a prefix refused.
- [ ] `firstEventTimestamp`, `lastEventTimestamp`, `lastIngestionTime` are real.

**Events**
- [ ] PutLogEvents: missing group, missing stream, empty batch, empty message,
      out-of-order batch; every limit in LOG-5.
- [ ] GetLogEvents: forward and backward tokens, the repeated end token,
      `startFromHead`, time bounds, `limit` 1 to 10,000.
- [ ] FilterLogEvents: interleaving across streams, names and prefix (and both
      together refused), `nextToken` only while more remain, unique `eventId`s,
      `searchedLogStreams`.

**Filter patterns** (one language, three users: FilterLogEvents, subscription
filters, metric filters)
- [ ] Each form in the ledger's list matches and fails to match as stated,
      including `-term`, `?term`, phrases, `&&` over `||`, `IS NULL`,
      `NOT EXISTS`, `= *`, string globs, array indexes.
- [ ] Term matching is substring and case-sensitive here. Whether AWS matches
      whole words is not established (section 8).
- [ ] Every unsupported construct is refused by all four entry points with the
      same code, and none is stored (LOG-1).

**Subscription filters**
- [ ] Two per group; a third refused; the same name replaces without counting.
- [ ] Destination kinds: Lambda, Kinesis; Firehose and a Logs destination
      refused by name; a missing destination per the decision on LOG-6.
- [ ] The envelope: gzip, the six fields, 56-digit ids, base64 under
      `awslogs.data` for Lambda, raw bytes for Kinesis, partition key by
      `distribution`.
- [ ] `roleArn` round-trips and gates nothing, and the ledger says so.
- [ ] A function subscribing to its own group is refused; the refusal is
      classed "Refused here" or "Refused" per the recording.

**Metric filters**
- [ ] 100 per group; the 101st refused; the same name replaces.
- [ ] A literal value, a `$.field`, a nested field, a missing field with and
      without `defaultValue`, a non-numeric literal (LOG-1).
- [ ] Unit: all 27 accepted, any other refused. Dimensions reach the datum.
- [ ] Several transformations on one filter each publish.
- [ ] DescribeMetricFilters filters and pages (LOG-7); with no group it lists all.
- [ ] TestMetricFilter: `eventNumber`, `eventMessage`, `extractedValues` (LOG-7).
- [ ] A filter does not count lines written before it existed.

**Tags**
- [ ] Both spellings; the ARN with and without `:*`; key and value rules on
      every writer (LOG-3); the per-resource limit, which is not established.

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
`routes_compute.go`. `log_tail` is also rendered by `lambdaLogs` and `sfnLogs`,
so a change to it lands on three pages.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Logs-Home | Groups, none open | `logs_home`, `logs_lp` | exists; the footer line on what the number means is **new** |
| Logs-01-None | No log groups yet | `logs_home`, `empty_detail`, `copy.go` | exists |
| Logs-02-Create | Create | `logs_create` | form exists; teaching lines and "Who writes to a group" are **new** |
| Logs-03-Create-Retention | Create with retention, cli open | `logs_create`, `cli` | form exists; the second cli line is **new** (foundation A11) |
| Logs-04-Create-Refused | Create refused | `fail_inline` | exists |
| Logs-05-Tail | Tail | `logs_group`, `log_tail` | exists; "Written by" and the time range are **new** |
| Logs-06-Tail-Filter | Tail with a pattern; a pattern refused | `log_tail` | filter exists; the refusal shown inline is **new** (LOG-10); "What the box reads" is **new** |
| Logs-07-Tail-Invocation | One invocation | `log_tail` | exists; the count wording is **new** (LOG-14); "Reading a REPORT line" is **new** |
| Logs-08-Tail-Empty | A new group; the quiet states | `log_tail` | exists; "What fills it" is **new** |
| Logs-09-Streams | Streams | `logs_group` streams | exists; the per-stream `tail` link is **new** |
| Logs-10-Metric-Filters | List and Add form | `logs_group` metrics | exists; teaching lines, "2 of 100", "Where it goes" are **new** |
| Logs-11-Metric-Test | Test a pattern | `metric_filter_test`, `logsTestMetricFilter` | exists |
| Logs-12-Metric-Refused | Add refused | `fail_inline` | exists |
| Logs-13-Subscriptions | List and Subscribe form | `logs_group` subscriptions | exists; teaching lines, "2 of 2 used", "What a destination receives" are **new** |
| Logs-14-Subscribe-Refused | Subscribe refused | `fail_inline` | exists |
| Logs-15-Retention | Settings | `logs_group` settings | menu exists; all 22 values, the spoken unit and "How it fits this group" are **new** (LOG-11) |
| Logs-16-Tags | Tags as a draft, refused | `tag_editor`, `fail_inline` | exists (foundation A5) |
| Logs-17-Confirms | Delete group, delete stream, remove subscription, remove metric filter | the four `hx-confirm`s | exist; the sentences are rewritten (foundation A13) |
| Logs-18-Bad-Unfiring | A filter that saves and never fires | `logs_group` metrics | **Dropped if LOG-1 is fixed by refusing on save**, which is the plan. Kept only for filters already in a data directory; see below. |
| Logs-19-Bad-Gone | A group that came back; a destination that went | `logs_group` | the "not in this stack" mark is **new** (LOG-15) |

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Teaching lines on Create, Add a metric filter, Subscribe, Retention | The settings table (foundation B4): lengths, patterns and enums from `logs/validate.go` `constraintTables`; the retention set from `logs/actions.go` `validRetention`; tiers from the ledger |
| "Who writes to a group" | The producer table in `docs/SUPPORT.md`. Static. |
| Second cli line | The form's `name` and `days`; the call `CreateLogGroup` in `client_logs.go` already makes |
| "Written by" a function | The group name's `/aws/lambda/` prefix against `ListFunctions` |
| Time range on the tail | `FilterLogs`' existing `since` argument, a query parameter on `/logs/tail` |
| Refusal shown in the tail | The error `FilterLogs` already returns, through `failView` |
| "What the box reads" | The filter-pattern list in `docs/SUPPORT.md`, which `logs/filter.go` implements |
| Count wording for one invocation, and "newest 500 of N" | `rid` in `logTailData`; `len(out.Events)` before the trim in `FilterLogs` |
| Per-stream `tail` | FilterLogEvents `logStreamNames`; a new argument on `FilterLogs` |
| "2 of 100", "2 of 2 used" | List length against `maxMetricFiltersPerGroup` and `maxSubscriptionsPerGroup`, exported to the console through the settings accessor |
| "Where it goes" | The form's namespace and metric name; a link to CloudWatch |
| "What a destination receives" | Static, from `logs/fanout.go` `envelope` and `deliver` |
| All 22 retention values, with the current one always present | `validRetention` |
| "How it fits this group" | `ListLogMetricFilters` and `ListLogSubscriptions`, which the settings tab does not load today |
| "not in this stack" | The function and stream lists the subscriptions tab already loads |
| Marks on a filter that cannot fire | `TestLogMetricFilter` on the stored pattern; a number parse of the stored literal |

**Where each teaching line's facts come from**

| Field | Range and rule | What it does | Enforced or stored |
|---|---|---|---|
| Log group name | `groupName` in `validate.go` | `logs_create` copy, ledger producer table | model |
| Retention | `validRetention`; default from `logs.DefaultRetention` and `[logs].retention`; sweep period from `Server.sweeper`; cap from `DefaultMaxEvents` | ledger, "Differences from AWS" | ledger row |
| Filter name (both forms) | `filterName` constraints | upsert by name: `PutMetricFilter`, `PutSubscription` in the store | model |
| Pattern (both forms, tail, tester) | `filterPattern` length; `filter.go` `compile` | ledger "Filter patterns" | ledger |
| Namespace, metric name | `reMetricName`, length 255 | `metricemit.go` `evaluate` | model |
| Value | length 100; `metricValueOf` | `metricfilters.go` | model for length, code for meaning |
| Unit | `standardUnits` | `metricship.Datum` | model |
| Destination | `checkDestination` | ledger "Subscription filters" | ledger |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Logs-06, -11, -14, -18: "AWS accepts these" of regex and space-delimited
  patterns. `doc`, not recorded.
- Logs-13: a delivery "is nested under" the app's PutLogEvents on the wire.
  `read` (`trace.Step` with `Via`), not seen in a browser.
- Logs-13: under enforce the destination must admit `logs.amazonaws.com`.
  `read` from the ledger; no test was run.
- Logs-14: the own-group refusal sits among the refusals without saying whose
  rule it is. Resolved by A2 and the recording.
- Logs-15: "On AWS that means lines are kept for ever." `doc`.
- Logs-10, -14: "100" and "two, here and on AWS". `doc`.
- Logs-17: "Data points already published stay in CloudWatch" and "a function
  still printing makes the stream again". `read`; the group case was run, the
  stream case was not.
- Logs-17: the Delete sentence lists streams, lines, subscription filters and
  tags. It becomes complete only when LOG-2 is fixed.
- Logs-19, right half: "a destination is not looked up when the filter is
  made". True today (run); false if LOG-6 is decided the other way, and the
  board is then only about a destination deleted afterwards.
- Logs-07: "Billed Duration … rounded up". `read`, `internal/lambdaruntime`.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store | Delete removes metric filters (LOG-2); metric-filter count per group (LOG-4) |
| Handler, SDK v2 | Pattern and value refused on PutMetricFilter (LOG-1); PutLogEvents limits (LOG-5); missing destination (LOG-6); DescribeMetricFilters paging and filters, `extractedValues` (LOG-7); `kmsKeyId` and `logGroupClass` (LOG-8); `aws/` prefix (LOG-9) |
| Rejection parity | Tag keys on TagResource, TagLogGroup, CreateLogGroup (LOG-3); regenerate `testdata/cases_logs.json` so the map-key constraint is in it |
| Restart | Metric filters, tags and retention added to `persistence_surfaces_test.go` |
| Second identity | The package suite under another account and region (foundation B2) |
| Console handler | Tail refusal (LOG-10); retention 400 round trip (LOG-11); default and dimensions shown (LOG-12); no `init` on an app group (LOG-13); count wording and the 500 cap (LOG-14); missing destination marked (LOG-15); a metric-filter handler test, which does not exist |
| Conformance (boto3) | Subscription filter to Lambda and to Kinesis; metric filter with a literal and a `$.field`; tags both spellings; retention values; every row in section 8 |
| Browser | Each `new` item; each of the four confirms' sentences; the tail refusal; Settings at 400 days; keyboard paths on the list and Create |
| Mutation | `logs/` baseline, then survivors in `filter.go`, `metricfilters.go`, `store.go` `Sweep` and `Scan` |

## 8. Recording queue

Every one of these is `doc` or `memory` today.

| Claim | Source now | Scenario |
|---|---|---|
| PutLogEvents limits: events per batch, batch bytes, event bytes, 14 days back, 2 hours ahead, 24 h span; which are errors and which are `rejectedLogEventsInfo` | doc | `test_logs::put_limits` |
| A group name starting `aws/` is refused, and with what code | doc | `reserved_prefix` |
| DeleteLogGroup removes the group's metric and subscription filters | doc | `delete_group_takes_filters` |
| PutMetricFilter with a non-numeric literal `metricValue` | memory | `metric_value_not_number` |
| PutMetricFilter and PutSubscriptionFilter with a malformed pattern: code and message | memory | `bad_pattern_on_put` |
| A regex and a space-delimited pattern are accepted by all three users | doc | `pattern_forms` |
| A term matches as a whole word or as a substring; case sensitivity | memory | `term_matching` |
| PutSubscriptionFilter to a missing function, a missing stream, and a function with no permission for `logs.amazonaws.com` | memory | `subscription_missing_destination` |
| A function subscribed to its own log group: accepted or refused | memory | `subscription_own_group` |
| Two subscription filters per group; the third's code | doc | `subscription_limit` |
| 100 metric filters per group; the 101st's code | doc | `metric_filter_limit` |
| `metricFilterCount` and `storedBytes` in DescribeLogGroups | doc | `group_counters` |
| TestMetricFilter `extractedValues` for a `$.field` pattern | doc | `test_metric_filter_extracts` |
| DescribeLogGroups with prefix and pattern together | memory | `describe_prefix_and_pattern` |
| CreateLogGroup with `kmsKeyId` for a key that does not exist; with `logGroupClass` | memory | `create_group_options` |
| Tags per group limit, and the code past it | memory | `tag_limit` |
| No retention policy means never expire | doc | `retention_default` |
| A deleted Lambda log group is recreated on the next invocation, without retention | memory | owed to integration; needs Lambda |
| DescribeLogStreams ordered by `LastEventTime` with a prefix is refused | doc | `streams_order_with_prefix` |
| The code an unsupported operation would carry (the 93 are ours to choose; 400 `UnsupportedOperationException` today) | n/a | none; settled by foundation A2's "Not implemented" |

## 9. Owed to integration

| What | With |
|---|---|
| A subscription filter delivers to a function and to a stream; a missing destination; delivery under enforce with and without a `logs.amazonaws.com` statement | Lambda, Kinesis, IAM |
| A metric filter's datum arrives in CloudWatch with its unit and dimensions, and an alarm on it changes state | CloudWatch |
| A function's lines arrive under `/aws/lambda/<name>` with request ids; a deleted group or stream is recreated; `[lambda].quiet` | Lambda |
| The other writers land where the ledger says: Step Functions (configured group, and `/aws/vendedlogs/states/` for Express), API Gateway (access and execution), EventBridge (log-group target), SNS (delivery status) | each of them |
| `log_tail` on the Lambda and Step Functions pages shows a refusal as a refusal (LOG-10 is fixed in the shared partial; their specs must be re-run) | Lambda, Step Functions |
| `AWS::Logs::LogGroup`, `SubscriptionFilter`, `MetricFilter` through CloudFormation, SAM, CDK and Terraform; what a declared `AWS::Logs::LogStream` should do now that PutLogEvents needs one (LOG-16) | CloudFormation |
| A template that declares a group **and** a function: retention survives the function's first line | CloudFormation, Lambda |
| Export round trip: a group with filters exported by `doze-aws export` and applied again | CloudFormation |
| Sweep and the 100,000 cap under a function printing continuously | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] LOG-1 to LOG-17 each closed, with the test that failed first named beside it.
- [ ] Decided and written into `SUPPORT.md`:
      - a missing subscription destination: refused at put, or accepted and the
        ledger sentence corrected (LOG-6);
      - `kmsKeyId`: cosmetic or refused (LOG-8);
      - `logGroupClass` and `deletionProtectionEnabled`: a tier each;
      - the `aws/` prefix (LOG-9);
      - the PutLogEvents limits modelled, and any deliberately not (LOG-5);
      - what an unreadable pattern answers on PutMetricFilter, the same as on
        the other three entry points (LOG-1).
- [ ] The per-member table in section 2 has no "?" left, and every C member
      reads as cosmetic in the ledger and wherever the console shows it.
- [ ] Every "not established" in this document is resolved or is a row in
      section 8 with its test written and skipped: `deletionProtectionEnabled`;
      prefix with pattern; the `fieldSelectionCriteria` family on both filter
      kinds; `entity`; the tag limit; term matching; a swept stream's
      `firstEventTimestamp`; the own-group refusal on AWS.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] A filter pattern is refused or accepted identically by FilterLogEvents,
      TestMetricFilter, PutSubscriptionFilter and PutMetricFilter, held by one
      table test over all four.
- [ ] After DeleteLogGroup nothing of the group remains in any bucket, held by a
      store test that walks every bucket.
- [ ] A data directory that already holds an unreadable metric filter still
      opens, and the Metric filters tab marks that filter (the part of Logs-18
      that survives LOG-1).
- [ ] Boards: 19 of 20 implemented; Logs-18 reduced as above. No statement on a
      Logs screen is in the "not yet backed" list in section 6.
- [ ] The four confirms say only what the code does, including what a running
      function does to a deleted group.
- [ ] The Settings menu shows whatever retention the group has, and Save with
      nothing changed changes nothing.
- [ ] `log_tail` behaves the same on all three pages that render it.
- [ ] `e2e/tests/logs.spec.ts` covers every route under `/logs` with the route
      gate on (13 routes in `logsRoutes`), and the accessibility sweep is clean
      on all six tabs and Create.
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: member tiers, the six decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for LOG-1 to LOG-9 | `logs/*_test.go`, `persistence_surfaces_test.go` |
| 3 | Delete takes everything; counts | `logs/store.go`, `logs/actions.go`, `logs/metricfilters.go` |
| 4 | One pattern gate for all four entry points; literal value check | `logs/filter.go`, `logs/actions_metricfilters.go`, `logs/subscriptions.go`, `logs/metricemit.go` |
| 5 | Tag keys; regenerate the cases | `logs/validate.go`, `logs/testdata/cases_logs.json`, `logs/rejection_parity_test.go`, `cmd/dzaudit/` if the generator is what dropped the map key |
| 6 | PutLogEvents limits; group options; reserved prefix; describe paging and filters; `extractedValues` | `logs/actions.go`, `logs/actions_metricfilters.go` |
| 7 | Subscription destination decision | `logs/subscriptions.go`, `docs/SUPPORT.md` |
| 8 | Section 5 sweep | `logs/sdk_test.go`, `logs/subscriptions_test.go`, `logs/metricfilters_test.go` |
| 9 | Conformance scenarios and claims | `conformance/tests/test_logs.py`, the claims file from foundation B1 |
| 10 | Console: tail (LOG-10, 13, 14), settings (LOG-11), metric filters (LOG-12), subscriptions (LOG-15), toast (LOG-17), the `new` items | `internal/console/templates/logs.html`, `templates/create.html` (`logs_create`), `handlers_logs.go`, `client_logs.go`, `routes_compute.go`, `logs_test.go` |
| 11 | Browser tests | `e2e/tests/logs.spec.ts`; re-run `lambda.spec.ts` and the Step Functions specs for `log_tail` |
| 12 | Stale text; ledger, changelog, status | `logs/logs.go`, `internal/cfn/resources.go`, `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 and 4 are the P0s and come before anything else in the package. Step 10
depends on foundation A2 (the refusal classes), A5 (tags), A11 (cli) and B4
(the settings accessor); if any of those slipped, the teaching lines and the
tail refusal wait for it and the rest of step 10 does not.

**Prior art.** AWS's "Filter pattern syntax for metric filters, subscription
filters, filter log events, and Live Tail" page in the CloudWatch Logs user
guide is one page for all four users of the language, which is the argument for
step 4's single gate. The PutLogEvents API reference lists the batch limits in
its opening section and is the source for LOG-5 until a recording replaces it.
