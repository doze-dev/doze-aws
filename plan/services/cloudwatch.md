# CloudWatch

Stage 12. Size M. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds CloudWatch's own.
"CloudWatch" here is metrics, alarms and dashboards; logs are
[logs.md](logs.md).

## 1. Summary

Metric samples, statistics over them, and metric alarms that evaluate on a
ten-second tick and notify, over one bbolt store (`cloudwatch.bolt`). It is the
only service that speaks three wire protocols: Smithy RPC v2 CBOR, AWS JSON 1.0
and AWS Query, normalised in `cloudwatch/codec.go` so handlers see one shape.

Peers. Producers publish into it through `internal/metricship`: Lambda
(`AWS/Lambda`), API Gateway (`AWS/ApiGateway`), Step Functions (`AWS/States`)
and CloudWatch Logs (metric filters, and EMF lines a function prints). It calls
out to SNS (Publish) and Lambda (async invoke) when an alarm changes state.
CloudFormation and `provision` create alarms and dashboards. SQS, SNS, DynamoDB
and Kinesis publish no metrics, on purpose; that is a listed difference and
must stay one.

It sits after Lambda, SNS and Logs because every producer and both action
targets are then already done.

**Estimate:** M, three to four focused days. The findings below include one P0
and nine P1, six of them found after the size was set; see the note at the end
of section 11. **Actual:** _fill in when done._

## 2. Contract

The model file the coverage test reads (`cloudwatch/testdata/ops_cloudwatch.json`)
lists **55** operations: 19 dispatched (`handlers` in `cloudwatch/actions.go`)
and 36 refused by name (`notHere` in `cloudwatch/validate.go`).
`docs/SUPPORT.md` says "50 operations, handled (19) or refused (31)" and its
table does not name eight of the refused ones. See CW-12.

| Operations | Now | Target | Note |
|---|---|---|---|
| PutMetricData | F | F | `Values`/`Counts` refused, `EntityMetricData` not read. See CW-16, CW-17. Per-member tier below. |
| ListMetrics | F | F | Paginates (page size 500, `NextToken`). `RecentlyActive=PT3H` honoured. `IncludeLinkedAccounts` and `OwningAccount`: not established. |
| GetMetricStatistics | F | F | 1 440-datapoint ceiling enforced. Statistics and `pNN` only. |
| GetMetricData | F | F | One `MetricStat` per query. `NextToken`, `MaxDatapoints`, `LabelOptions`: not established (not read by the handler). `Expression` is refused: see the S row. |
| PutMetricAlarm | F | F | Per-member tier below. See CW-18, CW-19. |
| DescribeAlarms | F | F | Ignores `MaxRecords`, never returns `NextToken`. See CW-13. |
| DescribeAlarmsForMetric | F | F | Wrong for an undimensioned metric. See CW-4. |
| DeleteAlarms | F | F | |
| SetAlarmState | F | F | See CW-8, CW-9. |
| DescribeAlarmHistory | F | F | Truncates at `MaxRecords` with no `NextToken`. See CW-13. |
| EnableAlarmActions, DisableAlarmActions | F | F | |
| TagResource, UntagResource, ListTagsForResource | F | F | Alarms and dashboards. The 50-tag limit and the `aws:` prefix rule: not established. |
| PutDashboard, GetDashboard, ListDashboards, DeleteDashboards | C | C | Body stored verbatim, never rendered. Error names wrong on JSON and CBOR (CW-14). `ListDashboards` does not paginate (CW-13). |
| PutCompositeAlarm, DescribeAlarmContributors | S | S | Deferred in `not-built.md`. |
| PutAnomalyDetector, DeleteAnomalyDetector, DescribeAnomalyDetectors | S | S | Declined. |
| PutInsightRule, DeleteInsightRules, DescribeInsightRules, EnableInsightRules, DisableInsightRules, GetInsightRuleReport, ListManagedInsightRules, PutManagedInsightRules | S | S | Declined. |
| PutMetricStream, DeleteMetricStream, GetMetricStream, ListMetricStreams, StartMetricStreams, StopMetricStreams | S | S | Declined. |
| PutAlarmMuteRule, GetAlarmMuteRule, DeleteAlarmMuteRule, ListAlarmMuteRules | S | S | Deferred. |
| GetDataset, AssociateDatasetKmsKey, DisassociateDatasetKmsKey | S | S | Declined. |
| GetOTelEnrichment, StartOTelEnrichment, StopOTelEnrichment, UpdateOTelEnrichment | S | S | Refused in code; **not named** in `SUPPORT.md` or `not-built.md` (CW-12). |
| CreateResourceMetricsConfiguration, DeleteResourceMetricsConfiguration, GetResourceMetricsConfiguration, UpdateResourceMetricsConfiguration | S | S | Refused in code; **absent** from `SUPPORT.md` and `not-built.md` (CW-12). |
| PutLogAlarm, GetMetricWidgetImage | S | S | Declined. |

Every S row answers HTTP 400 `UnsupportedOperationException` with the reason
(run, JSON wire). Whether that is the code the protocol wants for a stub, and
what it looks like on Query and CBOR, is not established (CW-15).

**The three wires.** One constraint table, `constraintTables` in
`cloudwatch/validate.go`, serves all three.

| Wire | Who speaks it | Error code carried | To confirm |
|---|---|---|---|
| RPC v2 CBOR | aws-sdk-go-v2, Java, Rust and others | modern shape name in `__type`; legacy in `x-amzn-query-error` when asked | CW-14 on this wire |
| AWS JSON 1.0 | the AWS CLI, boto3, the console's own client | modern shape name in `__type` | CW-14 (run on this wire) |
| AWS Query | SDKs below the migration versions | legacy name in `<Code>` | `ValidationError` for model constraints |

`PutMetricData` is gzipped by aws-sdk-go-v2 and all three wires gunzip.

**PutMetricAlarm members.** "PutMetricAlarm is F" hides these:

| Member | Now | Target | Note |
|---|---|---|---|
| AlarmName, AlarmDescription, Namespace, MetricName, Dimensions | F | F | |
| Statistic (five named) | F | F | |
| ExtendedStatistic `pNN`, `pNN.N` | F | F | Exact, from raw samples; a listed difference. |
| ExtendedStatistic other forms (`tm`, `wm`, `tc`, `ts`, `iqm`, `pr`) | refused | S | Refused as "not a valid statistic", which is false: AWS accepts them. Becomes "Refused here". CW-20. |
| Period, EvaluationPeriods, DatapointsToAlarm, Threshold | F | F | |
| ComparisonOperator: the four threshold operators | F | F | |
| ComparisonOperator: the three anomaly operators | S | S | |
| TreatMissingData | F | F | |
| Unit | F | F | Filters samples to that unit. |
| ActionsEnabled | F | F | |
| AlarmActions, OKActions, InsufficientDataActions: SNS topic, Lambda function | F | F | Topic existence is not checked. More than five per state is accepted (CW-19). |
| Actions of any other kind (EC2, Auto Scaling, SSM) | refused | S | "Refused here". |
| Tags | F | F | |
| EvaluateLowSampleCountPercentile | dropped | C or S | Validated for length, then not stored and not returned. CW-18. |
| EvaluationInterval | dropped | not established | Range-checked 10 to 3 600, then not stored. What it is on AWS is not established. CW-18. |
| Metrics (metric math), ThresholdMetricId, WarmUpConfiguration | S | S | Refused by name. |

**PutMetricData members.**

| Member | Now | Target | Note |
|---|---|---|---|
| Namespace, MetricName, Dimensions, Value, Unit, Timestamp | F | F | `AWS/` refused for a client. Timestamp bounds not checked (CW-21). Dimension count not checked (not established). |
| StatisticValues | F | F | Its mean stands in for percentiles. |
| StorageResolution | C | C | Read into the datum; whether it changes anything is not established. |
| Values, Counts | refused | F | CW-16. |
| EntityMetricData, StrictEntityValidation | not read | not established | CW-17. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `cloudwatch/`: 53 tests in 13 files (aggregate 7, alarms 5, evaluate 6, statistics 6, producers 6, listmetrics 5, sdk v2 4, three-wire 4, sdk v1 3, getmetricdata 3, conformance fixes 2, coverage 1, rejection parity 1) |
| Parity | `rejection_parity_test.go` replays `testdata/cases_cloudwatch.json`; the ledger says 183 of 183 constraints on the 19 dispatched operations, on each wire, `knownGaps` empty. `coverage_model_test.go` holds the 55-operation split. |
| Provisioning | `cloudformation/cloudwatch_apply_test.go`, `internal/provision/svc_cloudwatch.go` |
| Console handler | `internal/console/cw_test.go`: 1 test |
| Conformance | `conformance/tests/test_cloudwatch.py`: 3 scenarios, all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/cloudwatch.spec.ts`: 3 tests |
| Restart | None in the package |
| Second identity | None in the package |
| Mutation | No per-package baseline yet (foundation B3) |

The model states lengths, ranges and enums, so most constraint rules are
`model`. Everything about *when an alarm moves and what it writes* is
hand-derived, and almost all of it is `memory` today.

## 4. Findings

Evidence `run` means reproduced against a binary built from `8cd261f`.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| CW-1 | P2 | The State tab shows `0/1 × 60s` for any alarm created without `DatapointsToAlarm`. The service omits the member unless the caller set it (right, for Terraform), and the template prints the zero. Every console-made alarm reads 0 of N. | `templates/cw.html` `cw_alarm` (Window fact); `cloudwatch/responses.go` `viewOf` | run | n/a | Handler test: create from the form, render, assert "1/1" |
| CW-2 | P2 | The chart's Min is wrong for a flat series: one point of 12 reads `Last 12, Min 11.5`. The value is nudged to scale the line and then printed. | `internal/console/client_cw.go` `Series.summarise` (`s.Min -= 0.5`) | run | n/a | Unit test on `summarise` with one point |
| CW-3 | P2 | The alarm's Metric tab renders an empty Statistic dropdown. The handler never sets `Stat` or `Stats`. | `internal/console/handlers_cw.go` `cwAlarm`, the `metric` case | run | n/a | Handler test asserts seven options and the alarm's statistic selected |
| CW-4 | P1 | `DescribeAlarmsForMetric` for a metric with no dimensions returns alarms that watch a dimensioned series of the same name. The filter only runs when dimensions were given. | `cloudwatch/actions_alarm.go` `describeAlarmsForMetric` | run | doc (the code's own comment says AWS matches the exact set); queued | SDK test: alarm on `{Stage:prod}`, ask with no dimensions, expect none |
| CW-5 | P3 | The reason written on a return to OK reads "3 out of the last 3 datapoints were not were greater than the threshold (60)". It is in `StateReason` and in the notification's `NewStateReason`. The whole reason format also differs from AWS's; that part is `memory` and queued. | `cloudwatch/evaluate.go` `evaluate`, `comparisonWords` | run | n/a (grammar); memory (format) | Evaluate test asserts the sentence for each operator |
| CW-6 | P2 | The create form turns Period or Evaluation periods of 0, a negative or a non-number into the default and creates the alarm. Period `0` makes a 60 s alarm. | `internal/console/handlers_cw.go` `atoiOr`, `cwCreateAlarm` | run | model (`@range` min 1) | Handler test posts `period=0`, expects the service's refusal |
| CW-7 | P2 | Submitting the create form over an existing name replaces the alarm and toasts "created". In a run it dropped the OK and INSUFFICIENT_DATA actions, `Unit` and `DatapointsToAlarm`, and switched disabled actions back on. | `internal/console/handlers_cw.go` `cwCreateAlarm`; `client_cw.go` `PutAlarm` | run | n/a | Handler test: alarm made by the SDK with all three action lists and actions off, re-submitted from the form, read back |
| CW-8 | P1 | `SetAlarmState` to the state the alarm already has records "Alarm updated from ALARM to ALARM" and fires the actions again. | `cloudwatch/actions_alarm.go` `setAlarmState`; `evaluate.go` `transition` | run | memory (the API reference is recalled as "when the updated state differs from the previous value, the action is invoked") | SDK test: set ALARM twice, count Action rows and messages. Skipped until recorded. |
| CW-9 | P3 | A hand-set state survives one evaluator pass and is reverted on the next: 15 s in a run, 10 to 20 s by construction. The comment says the flag "clears as soon as the metric would have produced the same state"; it does the opposite, staying set while the metric agrees. The ledger's "does not immediately undo" is true but tells nobody how long. | `cloudwatch/evaluate.go` `EvaluateNow` | run | memory (AWS: "the next periodic alarm check, in about a minute") | Clocked test: set, tick twice, assert the state after each; then fix the comment and write the duration into `SUPPORT.md` |
| CW-10 | P1 | A notification that fails is logged and nothing else. History has no row for it, so `DescribeAlarmHistory` cannot tell a caller the topic refused. Reproduced with a topic policy under enforce and with a topic that does not exist. | `cloudwatch/actions_fire.go` `deliver` | run | memory (AWS is recalled as writing a "Failed to execute action" history item) | SDK test: alarm on a missing topic, set state, expect an Action row naming the failure. Skipped until recorded. |
| CW-11 | P2 | The console drops `HistoryData`, the evaluator's record of why an alarm moved (statistic, period, recent datapoints, threshold, evaluated, breaching). | `internal/console/client_cw.go` `AlarmHistory` | read | n/a | Client test asserts the field is carried |
| CW-12 | P3 | `SUPPORT.md` says 50 operations (19 and 31). The model file has 55 (19 and 36). The four OTel enrichment operations are not named in any row; the four ResourceMetricsConfiguration operations appear nowhere in `SUPPORT.md` or `not-built.md`. The common criterion "every operation has a tier in `SUPPORT.md`" fails today. | `docs/SUPPORT.md` CloudWatch section; `docs/not-built.md` | run (counted from `testdata/ops_cloudwatch.json`) | model | A ledger test that every name in `notHere` appears in `SUPPORT.md` and `not-built.md` |
| CW-13 | P1 | Three list operations do not paginate. `DescribeAlarms --max-records 1` returned all five alarms and no token. `DescribeAlarmHistory --max-records 1` returned one item and no `NextToken`, so the rest are unreachable. `ListDashboards` reads no token. | `cloudwatch/actions_alarm.go` `describeAlarms`, `describeAlarmHistory`; `actions_dashboards.go` `listDashboardsAction` | run (first two), read (third) | model (the members exist) | SDK paginator test over three alarms with `MaxRecords: 1`; same for history |
| CW-14 | P1 | Dashboard errors carry the legacy name on the modern wires. An invalid body answers `InvalidParameterInput` and a missing dashboard `ResourceNotFound` on JSON; the service's own table (`queryCodes`) says the modern names are `DashboardInvalidInputError` and `DashboardNotFoundError`, with those as the Query spellings only. An SDK matching on the typed error misses. | `cloudwatch/actions_dashboards.go` `putDashboardAction`; `dashboards.go` `errNoDashboard` | run (JSON), read (CBOR) | model (`awsQueryError` trait, as transcribed in `errors.go`) | Three-wire test asserting the code per wire for both errors |
| CW-15 | P3 | What a stub should answer is not established. Today: 400 `UnsupportedOperationException` on JSON. The foundation's A2 classifies "Not implemented" from a 501. Either CloudWatch changes or A2's rule reads the code as well. | `cloudwatch/cloudwatch.go` `ServeHTTP` | run (JSON), read (other wires) | not established | Three-wire test for one stub, written once the decision is made |
| CW-16 | **P0** | `PutMetricData` refuses a datum that carries `Values` and `Counts`: "The parameter MetricDatum.Value or MetricDatum.StatisticValues is required." Both members are in the model, and a caller that batches observations this way cannot publish at all. | `cloudwatch/actions.go` `parseDatum` | run | model | SDK test publishing `Values:[1,2,3] Counts:[1,1,2]`, then `SampleCount` 4 and `Sum` 9 |
| CW-17 | P1 | `EntityMetricData` is validated (one constraint) and then never read. A request that carries only it is refused for a missing `MetricData`; one that carries both stores half. | `cloudwatch/actions.go` `putMetricData` | read | model | SDK test with `EntityMetricData` only. Decide first: F, or refused by name. |
| CW-18 | P1 | Two `PutMetricAlarm` members are checked and then thrown away: `EvaluateLowSampleCountPercentile` and `EvaluationInterval`. `DescribeAlarms` does not return them, so Terraform or CloudFormation reading back sees a change it did not make. | `cloudwatch/actions_alarm.go` `putMetricAlarm`; `alarms.go` `alarm` | read | model (the members exist); their meaning is `doc`, not yet read | SDK round-trip test for each |
| CW-19 | P1 | Six actions on one state are accepted. | `cloudwatch/actions_alarm.go` `checkActions` | run | memory (five per state) | Rejection case with six. Skipped until recorded or found in the model. |
| CW-20 | P3 | An `ExtendedStatistic` of `tm99` or `wm90` is refused as "The value tm99 is not a valid statistic." It is valid on AWS; doze-aws does not compute it. The message must say so, and the row belongs in the ledger. | `cloudwatch/aggregate.go` `parseStat` | read | doc (CloudWatch statistics definitions); queued | Test asserts the message names the limit as doze-aws's own |
| CW-21 | P1 | A datapoint stamped in 2030 is accepted. Whether one older than the retention window is accepted and then swept is not established. | `cloudwatch/actions.go` `parseDatum` | run | memory (two weeks back, two hours ahead) | Rejection case each side. Skipped until recorded. |

Owned by the foundation, referenced not repeated:

- **A2**, the "why" line. CloudWatch's examples of each state: Refused
  (`Period must be 1, 5, 10, 20, 30, or a multiple of 60`), Refused here (the
  action-target message, metric math, `tm99`), Not implemented (any S row; see CW-15).
- **A5**, tags saved one call at a time: the `cw` case in `client_tags.go`
  sends one `TagResource` per key.
- **A7**, unreachable copy. The foundation's description is wrong for
  CloudWatch: see the correction at the end of section 11.
- **A9**, the fidelity dialog (`cw` to `cloudwatch`). Reproduced: `/info/cw`
  is empty, `/info/cloudwatch` has the table.
- **A10**, a denied alarm Publish shown as 500 on the wire. Reproduced.
- **A11**, the CLI preview: the form's template is
  `cloudwatch put-metric-alarm --alarm-name $name` and nothing else.
- **A3 / D3**, colours.

No finding was dropped from the board report. Corrections to it are in section 11.

## 5. What to check

Beyond the common criteria. Each line is checked on **all three** wires unless
it says otherwise. 50 items.

**Publishing**
- [ ] Namespace: 1 to 255, not starting with a colon; `AWS/` refused for a
      client and accepted from a peer.
- [ ] A datum needs exactly one of `Value`, `StatisticValues`, `Values`
      (with optional `Counts`); two together are refused (CW-16).
- [ ] `Values` and `Counts` of different lengths; a count of zero; more than
      150 values. Establish each.
- [ ] `StatisticValues`: all four required, `SampleCount` above zero, minimum
      not above maximum.
- [ ] Dimensions: both halves required; the count limit (not established);
      the same name twice (not established).
- [ ] Unit: the 27-value enum; an absent unit.
- [ ] Timestamp bounds on both sides (CW-21).
- [ ] Number of data in one call, and the request size limit (not established).
- [ ] `StorageResolution` 1 and 60; any other value. Establish, and set its tier.
- [ ] A gzipped body on each wire.
- [ ] `EntityMetricData` (CW-17).

**Reading**
- [ ] `ListMetrics`: namespace, name and dimension filters; a dimension filter
      with a name only; 501 series pages at 500; an invalid token is
      `InvalidNextToken`; `RecentlyActive`.
- [ ] `GetMetricStatistics`: period granularity; start not before end; the
      1 440 ceiling; a period with no observations is omitted; `Unit` filter;
      five statistics and percentiles in one call.
- [ ] `GetMetricData`: `ScanBy` both ways; `ReturnData: false`; `Label`; two
      queries with the same `Id` (not established); `MaxDatapoints` and
      `NextToken` (not established); `Expression` refused as doze-aws's own limit.
- [ ] Percentiles: `p0`, `p100`, `p99.9`, `p101`, `P95`, and a percentile over
      `StatisticValues` records.
- [ ] Period boundaries: a sample exactly on a boundary lands in one bucket,
      the later one.

**Alarm definition**
- [ ] Every member in the per-member table round-trips through
      `DescribeAlarms`, on each wire, with nothing added and nothing lost
      (CW-18).
- [ ] `Statistic` and `ExtendedStatistic` together refused; neither refused.
- [ ] Period granularity; `Period × EvaluationPeriods` at 86 400 and one over;
      `DatapointsToAlarm` equal to and above `EvaluationPeriods`.
- [ ] `TreatMissingData`: the four values, and a fifth.
- [ ] The three anomaly operators, `Metrics`, `ThresholdMetricId` and
      `WarmUpConfiguration` each refused, with a message that says the limit
      is doze-aws's.
- [ ] Actions: an SNS ARN, a Lambda ARN with and without a qualifier, any
      other ARN, a malformed ARN, six on one state (CW-19), a topic that does
      not exist (decide: refuse at put, or accept as AWS is believed to).
- [ ] Re-putting an alarm keeps its state, reason, state timestamp and tags,
      and writes "updated".
- [ ] Re-putting resets `ActionsEnabled` to true unless the caller says
      otherwise. Establish whether AWS does the same.
- [ ] An alarm name of 255 and 256; a name with a colon or a slash, and what
      the ARN and the tag lookup (`taggable`) then do.

**Evaluation**
- [ ] The window is lagged by one period: a breach in the filling period does
      not move the alarm.
- [ ] M of N for 1/1, 2/3, 3/3, with each missing-data treatment and with
      missing periods at the start, middle and end.
- [ ] All periods missing: `missing` gives INSUFFICIENT_DATA, `notBreaching`
      OK, `breaching` ALARM, `ignore` no move.
- [ ] `Unit` on the alarm ignores samples of another unit.
- [ ] A new alarm is INSUFFICIENT_DATA with "Unchecked: Initial alarm
      creation" and does not fire INSUFFICIENT_DATA actions on creation.
- [ ] The reason sentence for each operator and each direction (CW-5), and
      `StateReasonData` parses as JSON with the documented keys.
- [ ] An alarm on a metric nobody publishes stays INSUFFICIENT_DATA and writes
      no history.

**State by hand, and actions**
- [ ] `SetAlarmState`: each state; an unknown state; a missing alarm
      (`ResourceNotFound`, 404); a reason of 1 023 and 1 024; `StateReasonData`
      of 4 000 and 4 001, and whether it must be JSON (not established).
- [ ] The same state twice (CW-8).
- [ ] How long a hand-set state lasts, when the metric agrees and when it does
      not (CW-9).
- [ ] Actions fire once per transition, for the state entered, with AWS's
      payload: every key in `alarmPayload`, `Statistic` upper-cased, the
      Subject line. A percentile alarm's `Trigger` (`StatisticType`,
      `ExtendedStatistic`) is not established.
- [ ] Actions off: state changes, history records it, nothing is sent;
      enabling writes "Alarm actions enabled" once and does not replay.
- [ ] A failed delivery (CW-10).
- [ ] Enable and disable with a name that does not exist; with an empty list.

**History**
- [ ] The three item types; `HistoryItemType` filter; `StartDate` and
      `EndDate` bounds (inclusive or not: not established); `ScanBy`;
      `MaxRecords` 100 and 101; paging (CW-13).
- [ ] Two entries in one millisecond keep their order.
- [ ] Deleting an alarm removes its history; recreating it starts clean.
- [ ] `DeleteAlarms`: all exist, some exist, none exist; more than 100 names
      (not established).

**Dashboards and tags**
- [ ] A dashboard body round-trips byte for byte; a body that is not JSON is
      refused with the right name per wire (CW-14); valid JSON that is not a
      dashboard: establish whether AWS refuses or returns validation messages.
- [ ] Dashboard name rules, `DashboardNamePrefix`, paging (CW-13), and
      `DeleteDashboards` with one missing name deleting none.
- [ ] Tags on an alarm and on a dashboard: add, overwrite, remove a missing
      key, 50 and 51 tags, an `aws:` key, an ARN for neither kind, an ARN in
      another account or region.

**Producers** (in-package, with the peer faked; the real ones are section 9)
- [ ] A peer may write `AWS/`; a stack without CloudWatch still runs its producers.

**Lifecycle**
- [ ] Alarms, their state, `StateSetManually`, history, dashboards and tags
      survive a restart. Samples survive a clean stop; the store is `NoSync`,
      so what a crash loses is written into `SUPPORT.md`.
- [ ] The sweep drops samples past retention and past the sample cap, oldest
      first, and a series with no samples left leaves `ListMetrics`
      (not established).
- [ ] Every ARN (alarm, dashboard, `AWSAccountId` and `Region` in the payload)
      follows a non-default `--account-id` and `--region`.

## 6. Console

The design's "CloudWatch" page: 21 boards. Templates: `templates/cw.html`
(`cw_lp`, `cw_browser`, `cw_chart`, `cw_home`, `cw_metric`, `cw_alarm`), the
`cw_create` block in `create.html`, shared `fail_inline`, `tags_panel` and
`tag_editor` in `panes.html`. Handlers: `handlers_cw.go`. Client:
`client_cw.go`, the `cw` cases in `client_tags.go`. Routes: `cwRoutes` in
`routes_compute.go` (four GET, four POST).

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Cw-Home | Alarms table and metric browser | `cw_home`, `cw_browser`; `cwHome` | exists; the Alarms column and the "No AWS/SQS…" note are **new** |
| Cw-01-None | No alarms, no metrics | `cw_home`, `lp_empty` | list-pane line and metrics empty text exist; the detail-pane copy from `copy.go` is never rendered today (A7) |
| Cw-02-Metrics-Only | Metrics, no alarms | `cw_home` | exists; the per-row "Create alarm" is **new** |
| Cw-03-Create-None | Create with nothing to watch | `cw_create`, the `else` branch | exists; "Who publishes one" is **new** |
| Cw-04-Create | Create alarm | `cw_create`; `cwCreateAlarm` | form exists; teaching lines, Datapoints, On OK, the strip and two hint lines are **new** |
| Cw-05-Create-Replace | p95 over three periods, over an existing name | `cw_create` | form exists; the replace callout is **new** (and needs CW-7 fixed) |
| Cw-06-Create-Refused | Refused inline | `fail_inline` | exists |
| Cw-07-Metric | A metric's chart and its alarms | `cw_metric`, `cw_chart`; `cwMetric` | exists; point-time labels and the window note are **new** |
| Cw-08-Metric-Quiet | Nothing in the window, nothing watching | `cw_metric` | exists; the "why it is listed" note is **new** |
| Cw-09-Alarm-State | State, settings, actions, set state, actions switch | `cw_alarm`, default tab; `cwAlarm` | exists; the settings table with sentences and the Missing data row are **new** |
| Cw-10-Alarm-Fresh | A new alarm, INSUFFICIENT_DATA | `cw_alarm` | exists; "What happens next" is **new** |
| Cw-11-Alarm-Metric | Metric tab | `cw_alarm` `metric`, `cw_chart` | exists, with CW-3; threshold line and breaching points are **new** |
| Cw-12-Alarm-History | History tab | `cw_alarm` `history` | exists; the expandable row is **new** (needs CW-11) |
| Cw-13-Alarm-Tags | Tags as a draft | `tags_panel`, `tag_editor` | exists (A5) |
| Cw-14-SetState | Set state, composing | `cw_alarm`; `cwSetState` | form exists; "What pressing it does" and the payload preview are **new** |
| Cw-15-SetState-Done | After it | `cw_alarm` | exists; "What it did" is **new** |
| Cw-16-SetState-Refused | Refused inline | `fail_inline` | exists |
| Cw-17-Actions-Off | Actions disabled | `cw_alarm`; `cwSetActions` | exists |
| Cw-18-Delete | Delete confirm | `hx-confirm` in `cw_alarm`; `cwDeleteAlarm` | exists; the confirm is restyled and counts history rows |
| Cw-19-No-Notification | State changed, nothing sent | `cw_alarm` `history` | the page exists; the explainer and the Denied callout are **new** |
| Cw-20-No-Data | An alarm on a metric nothing publishes | `cw_alarm` `metric` | exists; the explanation is **new** |

**New items, and what each is built from**

| Item | Board | Built from |
|---|---|---|
| Alarms column in the metric browser | Cw-Home | `ListAlarms` and `ListMetrics`, matched on namespace, name and exact dimensions in the console. Not `DescribeAlarmsForMetric` until CW-4 is fixed. |
| Create alarm from a metric row | Cw-02 | The metric key `/cw/create?metric=` already reads |
| Datapoints to alarm field | Cw-04, 05 | `PutAlarm` already sends it when set; `cwCreateAlarm` must read it |
| On OK topic | Cw-04, 05 | `PutAlarm` already sends `OKActions`; the handler must read it |
| Teaching lines under each field | Cw-04, 05, 09 | Foundation B4 over `constraintTables["PutMetricAlarm"]`, plus the hand rules below |
| How this alarm decides | Cw-04, 05 | The form's own numbers and three constants: the ten-second tick, the one-period lag, the missing-data rule. A pure function of the form. |
| "Data in N of the last M periods" | Cw-04, 05 | `GetMetricData` on the chosen metric at the chosen period |
| "Delivers to N subscriptions" | Cw-04, 05, 15 | The wiring graph's `sub` edges from the topic |
| This replaces an alarm | Cw-05 | The alarm list already on the page. What it says is dropped must be re-derived after CW-7. |
| Who publishes one; No AWS/SQS… | Cw-03, Home, 20 | The producer table and "Differences from AWS" in `SUPPORT.md` |
| Point-time labels | Cw-07, 11 | `Series.Points` first and last |
| The window sets the period | Cw-07 | `MetricSeries` (window over 60, floored to a minute) |
| Threshold line, breaching points | Cw-11 | `Alarm.Threshold`, `Alarm.Operator` and the series, only when the chart's statistic is the alarm's |
| Missing data row; N of N for datapoints | Cw-09 | `Alarm.Missing`, already read; `Alarm.Evaluation` when `Datapoints` is zero (CW-1) |
| What happens next | Cw-10 | The alarm's period and missing-data setting, and the same "data in N of M" query |
| Why it moved | Cw-12 | `HistoryData` (CW-11) |
| What pressing it does; What it did | Cw-14, 15 | The alarm's action lists, `ActionsEnabled`, and the hold rule once CW-9 fixes its length |
| What the topic will receive | Cw-14 | The alarm's own fields, in the shape `alarmPayload` sends. Needs the payload shape exposed to the console or duplicated with a test that pins the two together. |
| Three causes of a missing Action row | Cw-19 | The alarm's action lists, `ActionsEnabled`, and the wire's `AlarmAction` entry for this alarm (its `Via` is `cloudwatch:<name>`). Replaced by a real History row once CW-10 is fixed. |
| Function link from a `FunctionName` dimension | Cw-19 | Namespace `AWS/Lambda` and the dimension value, through `resourceURL` |

**Where the teaching lines come from**

| Line | Source |
|---|---|
| Alarm name 1 to 255; description to 1 024; reason to 1 023 | `constraintTables` (model) |
| Period: 1, 5, 10, 20, 30 or a multiple of 60 | `checkPeriod` (hand rule; `doc`, queued) |
| Evaluation periods at least 1; Period × Evaluation periods at most 86 400 | model for the first; `maxEvaluationWindow` for the second (`doc`, queued) |
| Datapoints 1 to N, default N | model and `putMetricAlarm` |
| Statistics offered | `cwStats` in `handlers_cw.go`, which must be a subset of what `parseStat` accepts; a test should pin it |
| Missing data: four values, default `missing`, and what each does | `treatMissingValues` and `evaluate` |
| Evaluated every ten seconds, over completed periods | `evalInterval`, `evaluate` |
| Percentiles are exact; samples kept 24 hours | "Differences from AWS" in `SUPPORT.md` |
| An alarm with no action still changes state; SNS or Lambda only | `fireActions`, `checkActions` |
| A metric is its namespace, name and exact dimensions | `seriesKey` |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Cw-14: "AWS describes SetAlarmState as setting the state temporarily, for
  testing." `memory`.
- Cw-14, Cw-15: the hold is "one pass, then the pass after", and the return is
  "between :18 and :28". True of today's code (CW-9) and must follow whatever
  CW-9 decides.
- Cw-14: "Setting the state the alarm already has still counts as a change and
  runs the actions again." True today; false once CW-8 is fixed.
- Cw-05, Cw-14: "95 % of a period's observations are lower than this."
  AWS's definition, `memory`.
- Cw-19: the Denied callout on the alarm page does not exist today; the text is
  real but appears only in the wire's inspector and the log.
- Cw-19: "at create time only checked that it names an SNS topic or a Lambda
  function." True (`classifyAction`); changes if section 5 decides to check
  that the topic exists.
- Cw-18: "and its 4 history entries". The count comes from a call capped at 50.
- Cw-07, Cw-08: "samples are kept for 24 hours by default" is the default of a
  setting; the page should read the configured retention.
- Cw-04, Cw-05: the "N of M" figures on the boards are illustrative.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | `Values`/`Counts` (CW-16); `EntityMetricData` (CW-17); dropped alarm members (CW-18); six actions (CW-19); `tm99` message (CW-20); timestamp bounds (CW-21); `DescribeAlarmsForMetric` exact set (CW-4) |
| Evaluate | Reason sentences (CW-5); the hand-set hold, clocked (CW-9); same-state set (CW-8); failed delivery leaves a history row (CW-10) |
| SDK v2 (CBOR), SDK v1 (Query), raw JSON | Paginators for alarms, history, dashboards (CW-13); dashboard error names per wire (CW-14); one stub per wire (CW-15); alarm round trip of every member |
| Rejection parity | Every hand rule in section 5 on all three wires, asserting the code |
| Restart | Alarm with state, manual flag, history, tags; dashboard; samples after a clean stop |
| Second identity | Alarm ARN, dashboard ARN, payload account and region |
| Ledger | Every `notHere` name appears in `SUPPORT.md` and `not-built.md` (CW-12) |
| Console handler | Window fact (CW-1); `summarise` (CW-2); Metric tab selects (CW-3); form coercion (CW-6); re-create over an existing alarm (CW-7); `HistoryData` carried (CW-11); `cwStats` within `parseStat` |
| Conformance (boto3) | The scenarios in section 8 |
| Browser | Each `new` item; each refusal on Cw-06 and Cw-16; the delete confirm's sentence; actions off then a state change with no Action row; the fidelity dialog (A9); keyboard paths on the alarm list and create form |
| Mutation | `cloudwatch/` baseline, then survivors in `evaluate.go`, `aggregate.go`, `actions_alarm.go` |

## 8. Recording queue

Every row is `doc` or `memory` today. Scenario names are in
`conformance/tests/test_cloudwatch.py`.

| Claim | Source now | Scenario |
|---|---|---|
| Period must be 1, 5, 10, 20, 30 or a multiple of 60, and its error | doc | `period_granularity` |
| Period × EvaluationPeriods above 86 400 is refused, and its error | doc | `evaluation_window_ceiling` |
| `GetMetricStatistics` over 1 440 datapoints is refused | doc | `statistics_datapoint_ceiling` |
| `DescribeAlarmsForMetric` matches the exact dimension set | doc | `alarms_for_metric_exact_dimensions` |
| `SetAlarmState` to the current state fires nothing | memory | `set_state_same_state` |
| A hand-set state lasts until the next evaluation, about a minute | memory | `set_state_reverts` |
| A failed action writes a "Failed to execute action" history item | memory | `failed_action_history` |
| The reason sentences for ALARM, OK and INSUFFICIENT_DATA, and `StateReasonData` keys | memory | `state_reason_format` |
| History summaries: "Alarm updated from X to Y", "Successfully executed action …", creation and update | memory | `history_summaries` |
| A new alarm's first reason is "Unchecked: Initial alarm creation" | memory | `initial_state_reason` |
| Five actions per state at most | memory | `action_count_limit` |
| An alarm naming a topic that does not exist is accepted | memory | `alarm_missing_topic` |
| Re-putting an alarm resets `ActionsEnabled` to true when omitted; keeps state and tags | memory | `reput_keeps_state` |
| Timestamp bounds on `PutMetricData` | memory | `put_metric_data_timestamp_bounds` |
| `Values`/`Counts` semantics and limits | doc | `put_metric_data_values_counts` |
| Dimension count and data-per-call limits | memory | `put_metric_data_limits` |
| `EvaluateLowSampleCountPercentile` values; what `EvaluationInterval` is | not established | `alarm_optional_members` |
| `tm`, `wm`, `tc`, `ts`, `iqm`, `pr` are accepted as `ExtendedStatistic` | doc | `extended_statistic_forms` |
| A percentile is "the value below which N % of the data falls" | memory | n/a (wording; cite the definitions page) |
| The alarm notification payload and Subject, key for key; the percentile `Trigger` shape | doc | `alarm_notification_payload` |
| `DescribeAlarmHistory` date bounds and paging | memory | `history_paging` |
| `DeleteAlarms` with some names missing; `DeleteDashboards` atomic | memory | `delete_partial` |
| Dashboard errors: names on each protocol, and valid JSON that is not a dashboard | model and memory | `dashboard_errors` |
| The code a real unsupported call answers with, per protocol | not established | `unsupported_operation_shape` |
| Tag limits on an alarm | memory | `alarm_tag_limits` |
| A client publishing to `AWS/` is refused, and its message | doc | `aws_namespace_reserved` |

## 9. Owed to integration

| What | With |
|---|---|
| A real invocation raises Invocations, Duration, Errors and Throttles, dimensioned and not; an alarm on Errors reaches ALARM through the real evaluator | Lambda |
| A served request raises Count, 4XXError, 5XXError, Latency (and the HTTP API names) | API Gateway |
| An execution raises the `AWS/States` counts and ExecutionTime | Step Functions |
| A metric filter and an EMF line each become a metric an alarm can watch | Logs, Lambda |
| An alarm's Publish reaches a subscribed queue with AWS's payload | SNS, SQS |
| An alarm invoking a function: the event the function receives | Lambda |
| Under enforce, the topic policy must name `cloudwatch.amazonaws.com`, with `aws:SourceArn` the alarm's ARN; the function permission likewise. A denial shows as denied, not 500 (A10; already row "CloudWatch" in [../90-integration.md](../90-integration.md)). | SNS, Lambda, IAM |
| A deleted topic or function: what the alarm then does, and what History says (CW-10) | SNS, Lambda |
| A template with an alarm, a dashboard and a metric filter, through CloudFormation, SAM, CDK and Terraform, reading back with no drift (CW-18) | CloudFormation |
| `docs/cloudformation.md` lists `AWS::CloudWatch::*` as skipped; it is mapped | CloudFormation |
| The evaluator under many alarms, and a restart mid-transition | soak |
| SQS, SNS, DynamoDB and Kinesis publish nothing: a test per service that no `AWS/<service>` series appears after traffic | each |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] CW-1 to CW-21 each closed, with the test that failed first named beside it.
- [ ] `SUPPORT.md` says 55 operations, 19 and 36, names every refused
      operation, and `not-built.md` reconciles with `notHere` by test (CW-12).
- [ ] Decided and written into `SUPPORT.md`: the stub answer (CW-15);
      `EntityMetricData` (CW-17); `EvaluateLowSampleCountPercentile` and
      `EvaluationInterval` (CW-18); the non-percentile extended statistics
      (CW-20); whether an action target must exist; how long a hand-set state
      lasts (CW-9); what a crash loses of the samples.
- [ ] Neither per-member table in section 2 has "not established", "dropped"
      or "refused" without a tier beside it.
- [ ] Resolved, each by a test or a ledger line: `ListMetrics`
      `IncludeLinkedAccounts` and `OwningAccount`; `GetMetricData` `NextToken`,
      `MaxDatapoints`, `LabelOptions` and duplicate ids; `StorageResolution`;
      the dimension-count, data-per-call, `Values` and request-size limits;
      duplicate dimension names; the tag limit and `aws:` prefix;
      `StateReasonData` as JSON; history date bounds; `DeleteAlarms` over 100
      names; a non-dashboard JSON body; whether an emptied series leaves
      `ListMetrics`; the percentile `Trigger` shape.
- [ ] Every box in section 5 ticked on all three wires, or moved to the
      recording queue with its test written and skipped.
- [ ] Pagination: `DescribeAlarms`, `DescribeAlarmHistory`, `ListDashboards`
      and `ListMetrics` each driven by an SDK paginator to the last page.
- [ ] Dashboard and alarm errors carry the modern name on CBOR and JSON and the
      legacy name on Query, asserted per wire.
- [ ] No producer for SQS, SNS, DynamoDB or Kinesis was added, and the
      "Differences from AWS" line still says why.
- [ ] A restart test covers alarms, state, history, dashboards and tags.
- [ ] All 21 boards implemented from the foundation's shell with no
      CloudWatch-only chrome, and each `new` item reads only the data named for it.
- [ ] No statement on a CloudWatch screen is in the "not yet backed" list in
      section 6.
- [ ] The create form sends exactly what it shows: Period and Evaluation
      periods are never coerced (CW-6), and creating over an existing name says
      so first and loses nothing the form does not show (CW-7).
- [ ] The alarm page shows a failed notification where it shows a delivered one.
- [ ] The delete confirm says only what the code does.
- [ ] `e2e/tests/cloudwatch.spec.ts` covers all eight routes under `/cw` with
      the route gate on, and the accessibility sweep is clean on Home, a
      metric, all four alarm tabs and create.
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____ (3 today).
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] `task lightness` unchanged: the evaluator and sweeper tickers are the
      service's idle cost and no fix here adds a third.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: operation count, the eight unnamed stubs, member tiers, decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for CW-1 to CW-21 | `cloudwatch/*_test.go`, `internal/console/cw_test.go`, `docs/` ledger test |
| 3 | Publishing: `Values`/`Counts`, `EntityMetricData`, timestamp bounds | `cloudwatch/actions.go`, `samples.go`, `aggregate.go` |
| 4 | Alarm definition: dropped members, action count, statistic message | `cloudwatch/actions_alarm.go`, `alarms.go`, `responses.go`, `aggregate.go` |
| 5 | Alarm behaviour: exact-dimension match, reason sentences, same-state set, the hold, failed-action history | `cloudwatch/actions_alarm.go`, `evaluate.go`, `actions_fire.go` |
| 6 | Pagination and error names per wire; the stub answer | `cloudwatch/actions_alarm.go`, `actions_dashboards.go`, `dashboards.go`, `errors.go`, `cloudwatch.go`, `codec.go` |
| 7 | Section 5 sweep, parity cases, restart, second identity | `cloudwatch/rejection_parity_test.go`, `threewire_test.go`, `sdk_test.go`, `sdkv1_test.go`, `testdata/cases_cloudwatch.json` |
| 8 | Conformance scenarios and claims table | `conformance/tests/test_cloudwatch.py`, the claims file from foundation B1 |
| 9 | Console fixes CW-1, 2, 3, 6, 7, 11 | `internal/console/handlers_cw.go`, `client_cw.go`, `templates/cw.html`, `templates/create.html` (`cw_create`) |
| 10 | Console boards and `new` items | the same four, `client_flow.go` (topic subscriptions), `client_tags.go`, `routes_compute.go` |
| 11 | Browser tests | `e2e/tests/cloudwatch.spec.ts` |
| 12 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

**Size.** M was set before CW-13 to CW-21 were known. Steps 3 to 6 are now
about two days on their own, on three wires, and several wait on a decision.
Either re-size to L, or keep M by moving the dashboards half of CW-13 and
CW-14 behind the alarms work and accepting that the recording-gated items
(CW-8, CW-10, CW-19, CW-21) end the stage as skipped tests.

**For the foundation.**

- A7 lists CloudWatch among services that "open the first resource whenever one
  exists". It does not: `cw_home` always renders the alarms table and the
  metric browser and never calls `empty_detail`, so `copy.go`'s `None` and
  `Unsel` lines for `cw` are unreachable whether or not alarms exist. The rule
  A7 picks needs a third case: a service whose home is a page of its own.
- A2's "Not implemented" is decided by a 501. CloudWatch's stubs answer 400
  `UnsupportedOperationException` (CW-15). One of the two has to move.
- Foundation C names seven wiring-graph node kinds. An alarm is not one, so the
  neighbours strip on an alarm page comes from the alarm's own action lists,
  not from the graph.

**Corrections to the board report.**

- It gave the ledger as "12 F, 1 C, 8 S"; those are table rows. The operation
  count is 55, and the ledger's own "50" is wrong (CW-12).
- It filed the pagination, `Values`/`Counts`, dropped-member, error-name and
  timestamp gaps under nothing: they were found while writing this page
  (CW-13 to CW-21), not during the board work.
- Its "failed notifications are not in History" was reproduced only under
  enforce. It is now also reproduced with a topic that does not exist.
- It called the OK-reason bug and the hand-set hold "findings" of equal weight;
  here they are P3, because the first is wording and the second is an
  undocumented duration rather than wrong behaviour.

**Prior art.** Only what is known first-hand from this repo: the comment on
`seriesKey` in `cloudwatch/store.go` records that moto keys a metric without
its dimension set, which is the failure CW-4 reintroduces on the read side.
AWS's alarm-evaluation and statistics-definitions pages in the CloudWatch user
guide are the `doc` source for sections 5 and 8, and each hand rule's test
names the page.
