# CloudWatch

Stage 12. Size **XL** (was M; see section 1). Follows
[../01-protocol.md](../01-protocol.md); the common acceptance criteria there
apply in full, and this page adds CloudWatch's own. "CloudWatch" here is
metrics, alarms and dashboards; logs are [logs.md](logs.md).

**Pre-stage review written 2026-10-11** (section 12, and the board-by-board
table in 6a). It read the model's own documentation for all 55 operations and
every member of the 19 that are dispatched, ran the three wires and the
JavaScript SDK against a binary built from the checkout, and rendered all 21
boards beside the real pages. Every change to what this page said before is
marked *Corrected 2026-10-11* with the model's words. Evidence marked `run`
below was reproduced on 2026-10-11 against a binary built from the checkout at
`7cae441` (the earlier `8cd261f` runs are kept where nothing changed).

## Foundation hand-offs (stage 0, 2026-10-20)

What the foundation (stage 0, package P6) left for this stage, so it is not lost, and what it did already. The shared items are in [../00-foundation.md](../00-foundation.md); each line here says what is done, what is owed, and the test that proves it.

| Item | CloudWatch: done, and what this stage does |
|---|---|
| B6 the model sweep | 36 legacystub of 55 operations (400 `UnsupportedOperationException` with the stamp). Move each to `awshttp.NotBuilt`. |
| B8 generated cases | 522 committed, 612 regenerated (20 only in the committed file, 110 only in the new one); the service has three wires, so the parity test runs per wire. Regenerate in this stage. |
| B9 stored-policy validator | Not on `resource_policy_wiring_test.go`'s list: this service stores no resource policy document: where the model has the operation (`PutResourcePolicy`, `PutPermission`) the sweep answers it `legacystub` or `unknown`, and where it does not there is nothing to store. If this stage builds one, it calls `iampolicy.ValidateResourcePolicy` with the service's own code and wording and is added to that test. |
| B4 settings records | 0 hand-written bounded number inputs. |
| A15 fragments | No partials of its own. Nothing is owed. |
| A2 the why line | Set `Local: true` (`awshttp.APIError`) on each refusal that is doze-aws's own limit and not AWS's, which this document's findings mark; nothing sets the stamp today except the console's own 503. Test: one such limit per service answers with `X-Doze-Local-Limit` and the page says "Refused here" (`internal/console/why_line_test.go` is the worked example). |
| B3 mutation | Baseline recorded in `testdata/mutation.json` (`cloudwatch`); this stage runs `task mutate:check -- cloudwatch` after its changes, and any survivor it adds has a test or a reason. |


## 1. Summary

Metric samples, statistics over them, and metric alarms that evaluate on a
ten-second tick and notify, over one bbolt store (`cloudwatch.bolt`). It is the
only service that speaks three wire protocols: Smithy RPC v2 CBOR, AWS JSON 1.0
and AWS Query, normalised in `cloudwatch/codec.go` so handlers see one shape.
The model declares exactly these (`smithy.protocols#rpcv2Cbor`,
`aws.protocols#awsJson1_0`, `aws.protocols#awsQuery`, and
`awsQueryCompatible`), and the code serves each.

Peers. Producers publish into it through `internal/metricship`: Lambda
(`AWS/Lambda`), API Gateway (`AWS/ApiGateway`), Step Functions (`AWS/States`)
and CloudWatch Logs (metric filters, and EMF lines a function prints). It calls
out to SNS (Publish) and Lambda (async invoke) when an alarm changes state.
CloudFormation and `provision` create alarms and dashboards. SQS, SNS, DynamoDB
and Kinesis publish no metrics, on purpose; that is a listed difference and
must stay one.

It sits after Lambda, SNS and Logs because every producer and both action
targets are then already done.

**Estimate:** *Corrected 2026-10-11: was "M, three to four focused days. The
findings below include one P0 and nine P1, six of them found after the size was
set".* Reading the model's text and running the wires took the 21 findings to
**53 (2 P0, 26 P1, 17 P2, 8 P3)**, and 20 of the 21 boards differ in
shape from their pages (the tag editor, Cw-13, in two sentences; 6a). SQS was planned at S, two days and 8 findings, and
took several times that for 29 findings and six rebuilt boards. Here the final
count is about 1.8 times SQS's, the boards are the same 21 but 20 differ
where SQS's differed in six, and there are three wires where SQS had two.
**Honest size: about 20 focused days, plus or minus
four, in two stages** (breakdown in section 11): **12a** API, wires, ledger and
conformance, about twelve days; **12b** the console, about eight. Taking 12a
first is safe: no board needs more than the API fixes listed against it in the
build list.
**Actual:** _fill in when done._

## 2. Contract

The model file the coverage test reads (`cloudwatch/testdata/ops_cloudwatch.json`)
lists **55** operations: 19 dispatched (`handlers` in `cloudwatch/actions.go`)
and 36 refused by name (`notHere` in `cloudwatch/validate.go`).
`docs/SUPPORT.md` says "50 operations, handled (19) or refused (31)" and its
table does not name eight of the refused ones. See CW-12. (`dzaudit summary`
agrees: 55.)

| Operations | Now | Target | Note |
|---|---|---|---|
| PutMetricData | F | F | `Values`/`Counts` refused, `EntityMetricData` not read. See CW-16, CW-17. A gzipped body is refused on the Query wire (CW-53). Per-member tier below. |
| ListMetrics | F | F | Paginates (page size 500, `NextToken`; model: "Up to 500 results are returned for any one call"). `RecentlyActive=PT3H` honoured. *Corrected 2026-10-11: was "`IncludeLinkedAccounts` and `OwningAccount`: not established"; model: "If you are using this operation in a monitoring account, specify true to include metrics from source accounts" — cross-account observability, which a single local account does not have: accepted and ignored, a listed difference.* A series with no data for two weeks is no longer listed on AWS (model); never expires here (CW-44). |
| GetMetricStatistics | F | F | 1 440-datapoint ceiling enforced (*source was `doc`; model: "If you request more than 1,440 data points, CloudWatch returns an error"*). Statistics and `pNN` only. *Corrected 2026-10-11: "one or the other, never both, 1 to 5 and 1 to 10 names" (CW-29).* |
| GetMetricData | F | F | One `MetricStat` per query. `Expression` is refused: see the S row. *Corrected 2026-10-11: was "`NextToken`, `MaxDatapoints`, `LabelOptions`: not established"; model: `MaxDatapoints` "If you omit this, the default of 100,800 is used", paginates by `NextToken`, `ScanBy` default `TimestampDescending`; `LabelOptions` "affects a label only if you have a time-based dynamic expression in the label". Id uniqueness and the 500-query limit: CW-42.* |
| PutMetricAlarm | F | F | Per-member tier below. See CW-18, CW-19, CW-27, CW-28, CW-31, CW-32. |
| DescribeAlarms | F | F | Ignores `MaxRecords`, never returns `NextToken`. See CW-13. Parameter combinations: CW-40. |
| DescribeAlarmsForMetric | F | F | Wrong for an undimensioned metric (CW-4); ignores `ExtendedStatistic` and `Unit` (CW-30). |
| DeleteAlarms | F | F | History goes with the alarm, which AWS does not do (CW-26). *Corrected 2026-10-11: more than 100 names — model list length 0 to 100 — is accepted (CW-37); model: "If you specify any incorrect alarm names, the alarms you specify with correct names are still deleted".* |
| SetAlarmState | F | F | See CW-8, CW-9, CW-39. |
| DescribeAlarmHistory | F | F | Truncates at `MaxRecords` with no `NextToken`. See CW-13. History survives the alarm's deletion on AWS (CW-26). |
| EnableAlarmActions, DisableAlarmActions | F | F | Missing names are ignored: consistent with the model, which lists no error for either. |
| TagResource, UntagResource, ListTagsForResource | F | F | Alarms and dashboards. *Corrected 2026-10-11: was "The 50-tag limit and the `aws:` prefix rule: not established"; model: "You can associate as many as 50 tags with a CloudWatch resource" (not enforced, CW-35); the `aws:` rule is in no model text and stays `memory`. A missing resource answers `ResourceNotFound` where the model lists `ResourceNotFoundException` (CW-35).* |
| PutDashboard, GetDashboard, ListDashboards, DeleteDashboards | C | C | Body stored verbatim, never rendered. Error names wrong on JSON and CBOR (CW-14). `ListDashboards` does not paginate (CW-13). The ARN carries a region and dashboards are regional where the model says global (CW-34). `DeleteDashboards` is "atomic" where the model says best effort (CW-33). Tags are replaced on an update where the model says ignored (CW-31). Name and body rules (CW-47). |
| PutCompositeAlarm, DescribeAlarmContributors | S | S | Deferred in `not-built.md`. |
| PutAnomalyDetector, DeleteAnomalyDetector, DescribeAnomalyDetectors | S | S | Declined. |
| PutInsightRule, DeleteInsightRules, DescribeInsightRules, EnableInsightRules, DisableInsightRules, GetInsightRuleReport, ListManagedInsightRules, PutManagedInsightRules | S | S | Declined. |
| PutMetricStream, DeleteMetricStream, GetMetricStream, ListMetricStreams, StartMetricStreams, StopMetricStreams | S | S | Declined. |
| PutAlarmMuteRule, GetAlarmMuteRule, DeleteAlarmMuteRule, ListAlarmMuteRules | S | S | Deferred. |
| GetDataset, AssociateDatasetKmsKey, DisassociateDatasetKmsKey | S | S | Declined. |
| GetOTelEnrichment, StartOTelEnrichment, StopOTelEnrichment, UpdateOTelEnrichment | S | S | Refused in code; **not named** in `SUPPORT.md` or `not-built.md` (CW-12). |
| CreateResourceMetricsConfiguration, DeleteResourceMetricsConfiguration, GetResourceMetricsConfiguration, UpdateResourceMetricsConfiguration | S | S | Refused in code; **absent** from `SUPPORT.md` and `not-built.md` (CW-12). |
| PutLogAlarm, GetMetricWidgetImage | S | S | Declined. `PutLogAlarm` is the model's third alarm type (`LogAlarms` in `DescribeAlarms`, `AlarmTypes: LogAlarm`); the filters already answer an empty list for it. |

*Corrected 2026-10-11: was "Every S row answers HTTP 400
`UnsupportedOperationException` with the reason (run, JSON wire). Whether that
is the code the protocol wants for a stub ... is not established (CW-15)".* It
is decided: foundation decision D5 makes every stub HTTP 501 `NotImplemented`
with `X-Doze-Not-Built`, in each wire's own envelope. Today (run, JSON and
Query) CloudWatch answers 400 `UnsupportedOperationException`, a name that is in
none of the model's error shapes, with no header. See CW-15.

**The three wires.** One constraint table, `constraintTables` in
`cloudwatch/validate.go`, serves all three.

| Wire | Who speaks it | Error code carried | To confirm |
|---|---|---|---|
| RPC v2 CBOR | aws-sdk-go-v2, Java, Rust and others | modern shape name in `__type`; legacy in `x-amzn-query-error` when asked | CW-14 on this wire (read). Timestamps are CBOR tag 1: correct. |
| AWS JSON 1.0 | the AWS CLI, boto3, JavaScript v3, the console's own client | modern shape name in `__type`; botocore sends `x-amzn-query-mode: true` and gets `x-amzn-query-error: <legacy>;Sender` (run) | CW-14 (run). **Timestamps are ISO strings where the protocol sends epoch seconds, and the JavaScript SDK cannot read them (CW-22).** A gzipped body is accepted. |
| AWS Query | SDKs below the migration versions | legacy name in `<Code>` | *Corrected 2026-10-11: was "`ValidationError` for model constraints"; run: `<Code>InvalidParameterValue</Code>` (CW-24). `ExtendedStatistics` is never rendered (CW-23). A gzipped body is refused (CW-53).* |

*Corrected 2026-10-11: was "`PutMetricData` is gzipped by aws-sdk-go-v2 and all
three wires gunzip."* The model puts `smithy.api#requestCompression` (gzip) on
`PutMetricData` only. CBOR and JSON gunzip (run); Query does not: a form body
with `Content-Encoding: gzip` answers `InvalidParameterValue` "malformed form
body" (CW-53).

**XML names (L5).** Checked: the CloudWatch model carries no `xmlName` and no
`xmlFlattened` anywhere. Every list is `<member>`-wrapped, and the one map in a
handled response, `Datapoint.ExtendedStatistics` (`DatapointValueMap`), is
`<entry><key/><value/></entry>`. Nothing is flattened, so SQS's misnamed
flattened members cannot occur; the member that goes wrong here is dropped
altogether (CW-23). All 19 operations were posted on the Query wire with the
reference's `.member.N` spellings and accepted (run), and a second invalid
element is refused on Query as on JSON (run: a second datum's `Unit`, a second
dimension's name of 300 characters, a second tag's key of 200), so SQS-26's
one-element walk does not apply.

**PutMetricAlarm members.** "PutMetricAlarm is F" hides these:

| Member | Now | Target | Note |
|---|---|---|---|
| AlarmName, AlarmDescription, Namespace, MetricName, Dimensions | F | F | *Corrected 2026-10-11: AlarmName "must contain only UTF-8 characters, and can't contain ASCII control characters" — a control character is accepted (CW-41); Dimensions are at most 30 (CW-37).* |
| Statistic (five named) | F | F | Model enum: SampleCount, Average, Sum, Minimum, Maximum. |
| ExtendedStatistic `pNN`, `pNN.N` | F | F | Exact, from raw samples; a listed difference. Percentiles are unavailable on AWS for negative values and for most statistic sets (CW-43). |
| ExtendedStatistic other forms (`tm`, `wm`, `tc`, `ts`, `iqm`, `pr`) | refused | S | Refused as "not a valid statistic", which is false: AWS accepts them. Becomes "Refused here". CW-20. *Source now `model`: "If you specify ExtendedStatistic, the following are valid values: p90 tm90 tc90 ts90 wm90 IQM PR(n:m) ... TC(X%:X%) ... TM(X%:X%) ... TS(X%:X%) ... WM(X%:X%)".* |
| Period, EvaluationPeriods, DatapointsToAlarm, Threshold | F | F | *Corrected 2026-10-11: Period was "1, 5, 10, 20, 30 or a multiple of 60" (`doc`); model, `PutMetricAlarm.Period`: "Valid values are 10, 20, 30, and any multiple of 60" — 1 and 5 are accepted here and are not valid (CW-27). The evaluation window was "at most 86 400"; model: "Period multiplied by EvaluationPeriods can't be more than 604,800 seconds. For alarms with a period of less than one hour (3,600 seconds), the total evaluation period can't be longer than one day (86,400 seconds)" — an alarm of more than 24 one-hour periods is refused here (CW-28).* |
| ComparisonOperator: the four threshold operators | F | F | |
| ComparisonOperator: the three anomaly operators | S | S | Model: "used only for alarms based on anomaly detection models". |
| TreatMissingData | F | F | Model: "breaching, notBreaching, ignore, and missing", default `missing`. "Alarms that evaluate metrics in the AWS/DynamoDB namespace always ignore missing data": moot, nothing here publishes `AWS/DynamoDB`, and a client may not. |
| Unit | F | F | Filters samples to that unit. Model: "If you don't specify Unit, CloudWatch retrieves all unit types ... the alarm's behavior is not defined" when several are published. |
| ActionsEnabled | F | F | *Corrected 2026-10-11: re-put resets it to true unless named — model: "The default is TRUE" and "the update completely overwrites the previous configuration of the alarm". Established.* |
| AlarmActions, OKActions, InsufficientDataActions: SNS topic, Lambda function | F | F | Topic existence is not checked. *Corrected 2026-10-11: was "More than five per state is accepted (CW-19)" with source `memory`; model: `ResourceList` length 0 to 5 on all three. A Lambda ARN's version or alias is dropped when the function is invoked (CW-45).* |
| Actions of any other kind (EC2, Auto Scaling, SSM) | refused | S | "Refused here". The model lists EC2 (`arn:aws:automate:...`, `arn:aws:swf:...`), Auto Scaling, SSM OpsItem and `ssm-incidents`, and `arn:aws:aiops:...`. |
| Tags | F | F | *Corrected 2026-10-11: model: "If you are using this operation to update an existing alarm, any tags you specify in this parameter are ignored." They replace the old tags here (CW-31).* |
| EvaluateLowSampleCountPercentile | dropped | C | *Corrected 2026-10-11: was "C or S", meaning "doc, not yet read"; model: "Used only for alarms based on percentiles. If you specify ignore, the alarm state does not change during periods with too few data points to be statistically significant ... Valid Values: evaluate or ignore". C: validate the two values, store, return; the threshold of "too few" is not in the model, so it cannot be built (CW-32).* |
| EvaluationInterval | dropped | refused | *Corrected 2026-10-11: was "not established"; model: "The frequency, in seconds, at which the alarm is evaluated ... This parameter is required for alarms that use EvaluationCriteria, and cannot be specified for alarms configured with MetricName or Metrics." Every alarm here is MetricName: the member is a refusal, not a drop (CW-32).* |
| EvaluationWindow (sliding or wall clock) | dropped | S | New to this page. Model: "If you omit this parameter, the alarm uses a sliding window." Accepted and dropped today (CW-32, CW-46). |
| EvaluationCriteria (PromQL) | wrong error | S | New to this page. Model: "you must specify either MetricName, a Metrics array, or an EvaluationCriteria". Today answers "ComparisonOperator is required" (CW-32). |
| Metrics (metric math), ThresholdMetricId, WarmUpConfiguration | S | S | Refused by name. |

**PutMetricData members.**

| Member | Now | Target | Note |
|---|---|---|---|
| Namespace, MetricName, Dimensions, Value, Unit, Timestamp | F | F | `AWS/` refused for a client. *Corrected 2026-10-11: the model says only "you should not specify a namespace that begins with AWS/" — advice, as SQS-16's "isn't recommended" was; the refusal is `memory`, and stays as is until recorded (queue).* Timestamp bounds not checked (CW-21; now `model`). *Dimension count: model, at most 30 (CW-37); names "must contain only ASCII characters, must include at least one non-whitespace character, and cannot start with a colon (:)" (CW-41).* |
| StatisticValues | F | F | Its mean stands in for percentiles. *Corrected 2026-10-11: on AWS percentiles from a statistic set exist only when "The SampleCount value of the statistic set is 1 and Min, Max, and Sum are all equal. The Min and Max are equal, and Sum is equal to Min multiplied by SampleCount" (CW-43).* |
| StorageResolution | C | C | *Corrected 2026-10-11: was "whether it changes anything is not established"; model: "Valid values are 1 and 60 ... if you do not specify it the default of 60 is used". Read into the datum and dropped; any other number is accepted (CW-41). It decides which periods are valid on read and the 3-hour retention of sub-minute data; both are listed differences.* |
| Values, Counts | refused | F | CW-16. Model: "up to 150 unique values", "it must include the same amount of values as the Values array", "If you omit the Counts array, the default of 1 is used". |
| EntityMetricData, StrictEntityValidation | not read | not established | CW-17. Model: "up to two EntityMetricData objects", `KeyAttributes` map of 2 to 4, `StrictEntityValidation` "required when EntityMetricData is included". |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `cloudwatch/`: 53 tests in 13 files (aggregate 7, alarms 5, evaluate 6, statistics 6, producers 6, listmetrics 5, sdk v2 4, three-wire 4, sdk v1 3, getmetricdata 3, conformance fixes 2, coverage 1, rejection parity 1) |
| Parity | `rejection_parity_test.go` replays `testdata/cases_cloudwatch.json`; the ledger says 183 of 183 constraints on the 19 dispatched operations, on each wire, `knownGaps` empty. `coverage_model_test.go` holds the 55-operation split. *Corrected 2026-10-11: that figure does not reproduce. `dzaudit list cloudwatch` prints 168 constraint lines for the 19 operations, 18 of them list or map sizes (`Dimensions` at most 30 on four operations, `DimensionFilters` at most 10, the three action lists at most 5, `AlarmNames` at most 100 on four, `Statistics` 1 to 5, `ExtendedStatistics` 1 to 10, the entity maps) that `constraintTables` does not hold: foundation B8 taught the generator to read them, and "CloudWatch 45" waits for this stage to regenerate (CW-37).* |
| Provisioning | `cloudformation/cloudwatch_apply_test.go`, `internal/provision/svc_cloudwatch.go` |
| Console handler | `internal/console/cw_test.go`: 1 test |
| Conformance | `conformance/tests/test_cloudwatch.py`: 3 scenarios, all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/cloudwatch.spec.ts`: 3 tests |
| Query wire | Posted by tests for `PutMetricData`, `ListMetrics` and one error only; the other 17 operations were never posted on it until the review (all accepted, run) (CW-49) |
| Restart | None in the package |
| Second identity | None in the package |
| Mutation | A baseline exists in `testdata/mutation.json` since foundation B3: 336 killed, 99 lived, 55 not covered (*Corrected 2026-10-11: was "No per-package baseline yet"*); it predates every fix here, so the stage records a new one |

The model states lengths, ranges and enums, so most constraint rules are
`model`. Everything about *when an alarm moves and what it writes* is
hand-derived, and almost all of it is `memory` today. *Corrected 2026-10-11:
more of it is `model` than this page thought: SetAlarmState's "when the updated
state differs", the alarm period set and ceiling, the five actions, the
retention schedule, the timestamp window and the history's survival of a
deletion are all in the model's operation and member text.*

## 4. Findings

Evidence `run` means reproduced against a binary built from the checkout (the
`8cd261f` build for the original rows, the `7cae441` checkout on 2026-10-11 for
the rest).

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| CW-1 | P2 | The State tab shows `0/1 × 60s` for any alarm created without `DatapointsToAlarm`. The service omits the member unless the caller set it (right, for Terraform), and the template prints the zero. Every console-made alarm reads 0 of N. Seen again 2026-10-11: `Window 0/1 × 3600s`. | `templates/cw.html` `cw_alarm` (Window fact); `cloudwatch/responses.go` `viewOf` | run | n/a | Handler test: create from the form, render, assert "1/1" |
| CW-2 | P2 | The chart's Min is wrong for a flat series: one point of 12 reads `Last 12, Min 11.5`. The value is nudged to scale the line and then printed. | `internal/console/client_cw.go` `Series.summarise` (`s.Min -= 0.5`) | run | n/a | Unit test on `summarise` with one point |
| CW-3 | P2 | The alarm's Metric tab renders an empty Statistic dropdown ("Select..."). The handler never sets `Stat` or `Stats`. Seen again 2026-10-11. | `internal/console/handlers_cw.go` `cwAlarm`, the `metric` case | run | n/a | Handler test asserts seven options and the alarm's statistic selected |
| CW-4 | P1 | `DescribeAlarmsForMetric` for a metric with no dimensions returns alarms that watch a dimensioned series of the same name. The filter only runs when dimensions were given. Re-run 2026-10-11: an alarm on `{Stage=prod}` and one with none were both returned for a call with none. Direction: more forgiving. | `cloudwatch/actions_alarm.go` `describeAlarmsForMetric` | run | *Corrected 2026-10-11: was `doc` (the code's own comment); model, `DescribeAlarmsForMetric.Dimensions`: "If the metric has any associated dimensions, you must specify them in order for the call to succeed." (`model`)* | SDK test: alarm on `{Stage:prod}`, ask with no dimensions, expect none |
| CW-5 | P3 | The reason written on a return to OK reads "3 out of the last 3 datapoints were not were greater than the threshold (60)". It is in `StateReason` and in the notification's `NewStateReason`. Seen again 2026-10-11. The whole reason format also differs from AWS's; that part is `memory` and queued. | `cloudwatch/evaluate.go` `evaluate`, `comparisonWords` | run | n/a (grammar); memory (format) | Evaluate test asserts the sentence for each operator |
| CW-6 | P2 | The create form turns Period or Evaluation periods of 0, a negative or a non-number into the default and creates the alarm. Period `0` makes a 60 s alarm. | `internal/console/handlers_cw.go` `atoiOr`, `cwCreateAlarm` | run | model (`@range` min 1) | Handler test posts `period=0`, expects the service's refusal |
| CW-7 | P2 | Submitting the create form over an existing name replaces the alarm and toasts "created". In a run it dropped the OK and INSUFFICIENT_DATA actions, `Unit` and `DatapointsToAlarm`, and switched disabled actions back on. *Corrected 2026-10-11: the last half is what AWS does — model: "the update completely overwrites the previous configuration" and ActionsEnabled "The default is TRUE" — so the finding is that the form does not say so first, not that the service does it.* | `internal/console/handlers_cw.go` `cwCreateAlarm`; `client_cw.go` `PutAlarm` | run | model | Handler test: alarm made by the SDK with all three action lists and actions off, re-submitted from the form, read back |
| CW-8 | P1 | `SetAlarmState` to the state the alarm already has records "Alarm updated from ALARM to ALARM" and fires the actions again. Re-run 2026-10-11 (two calls, two StateUpdate rows). Direction: wrong (an action AWS does not run). | `cloudwatch/actions_alarm.go` `setAlarmState`; `evaluate.go` `transition` | run | *Corrected 2026-10-11: was `memory`; model, `SetAlarmState`: "When the updated state differs from the previous value, the action configured for the appropriate state is invoked." (`model`). Whether a same-state call writes a history row is not established.* | SDK test: set ALARM twice, count Action rows and messages. Written first, not skipped. |
| CW-9 | P3 | A hand-set state survives one evaluator pass and is reverted on the next: 15 s in a run, 10 to 20 s by construction. The comment says the flag "clears as soon as the metric would have produced the same state"; it does the opposite, staying set while the metric agrees and then swallowing the first pass on which it does not. The ledger's "does not immediately undo" is true but tells nobody how long. | `cloudwatch/evaluate.go` `EvaluateNow` | run | *Corrected 2026-10-11: was `memory` ("the next periodic alarm check, in about a minute"); model, `SetAlarmState`: "Metric alarms returns to their actual state quickly, often within seconds." — ten to twenty seconds is consistent with the model; "about a minute" was wrong.* | Clocked test: set, tick twice, assert the state after each; then fix the comment and write the duration into `SUPPORT.md` |
| CW-10 | P1 | A notification that fails is logged and nothing else. History has no row for it, so `DescribeAlarmHistory` cannot tell a caller the topic refused. Reproduced with a topic policy under enforce and with a topic that does not exist (again 2026-10-11: the `harbour-checkout-errors` alarm on a missing topic has no Action row). | `cloudwatch/actions_fire.go` `deliver` | run | memory (AWS is recalled as writing a "Failed to execute action" history item). The model confirms only that `HistoryItemType` has an `Action` member. | SDK test: alarm on a missing topic, set state, expect an Action row naming the failure. Skipped until recorded. |
| CW-11 | P2 | The console drops `HistoryData`, the evaluator's record of why an alarm moved (statistic, period, recent datapoints, threshold, evaluated, breaching). | `internal/console/client_cw.go` `AlarmHistory` | read | n/a | Client test asserts the field is carried |
| CW-12 | P3 | `SUPPORT.md` says 50 operations (19 and 31). The model file has 55 (19 and 36). The four OTel enrichment operations are not named in any row; the four ResourceMetricsConfiguration operations appear nowhere in `SUPPORT.md` or `not-built.md`. The common criterion "every operation has a tier in `SUPPORT.md`" fails today. Confirmed 2026-10-11 against `docs/not-built.md`. | `docs/SUPPORT.md` CloudWatch section; `docs/not-built.md` | run (counted from `testdata/ops_cloudwatch.json`) | model | A ledger test that every name in `notHere` appears in `SUPPORT.md` and `not-built.md` |
| CW-13 | P1 | Three list operations do not paginate. `DescribeAlarms --max-records 1` returned all five alarms and no token. `DescribeAlarmHistory --max-records 1` returned one item and no `NextToken`, so the rest are unreachable. `ListDashboards` reads no token. Re-run 2026-10-11: `DescribeAlarms` with `NextToken: "!!!"` answers 200 and the first page; the model lists `InvalidNextToken` for `DescribeAlarms`, `DescribeAlarmHistory` and `GetMetricData`. | `cloudwatch/actions_alarm.go` `describeAlarms`, `describeAlarmHistory`; `actions_dashboards.go` `listDashboardsAction` | run (first two), read (third) | model. *Corrected 2026-10-11: `MaxRecords` is 1 to 100 on both; `ListDashboards`: "ListDashboards returns up to 1000 results on one page"; no default page size is stated for the two alarm operations (AWS's default is `memory`).* | SDK paginator test over three alarms with `MaxRecords: 1`; same for history; 1 001 dashboards |
| CW-14 | P1 | Dashboard errors carry the legacy name on the modern wires. An invalid body answers `__type: InvalidParameterInput` and a missing dashboard `__type: ResourceNotFound` on JSON, and `x-amzn-query-error` is absent for the first. The model's shapes are `DashboardInvalidInputError` (query code `InvalidParameterInput`, 400, with a `dashboardValidationMessages` member) and `DashboardNotFoundError` (query code `ResourceNotFound`, 404). An SDK matching on the typed error misses; Query is right. *Corrected 2026-10-11: the original sentence "with those as the Query spellings only" had the two columns the wrong way round.* | `cloudwatch/actions_dashboards.go` `putDashboardAction`; `dashboards.go` `errNoDashboard` | run (JSON, again 2026-10-11), read (CBOR) | model (`awsQueryError` trait on `DashboardInvalidInputError` and `DashboardNotFoundError`) | Three-wire test asserting the code per wire for both errors |
| CW-15 | P3 | *Corrected 2026-10-11: was "What a stub should answer is not established ... Either CloudWatch changes or A2's rule reads the code as well."* Decided by D5 (foundation, "The refusal, decided"): HTTP 501, `NotImplemented`, a message naming the operation, `X-Doze-Not-Built: declined` or `deferred`, in each wire's own envelope. CloudWatch answers 400 `UnsupportedOperationException` with no header on JSON and Query (run); `UnsupportedOperationException` is in none of the model's error shapes. A2's rule is the header, so the console reads these stubs as "Failed" or "Refused" rather than "Not built". Direction: wrong code. | `cloudwatch/cloudwatch.go` `ServeHTTP` | run (JSON, Query), read (CBOR) | model (no such shape); foundation D5 | Three-wire test for one stub per wire, asserting 501, the code and the header. The foundation's one-function writer is used, not a CloudWatch copy. |
| CW-16 | **P0** | `PutMetricData` refuses a datum that carries `Values` and `Counts`: "The parameter MetricDatum.Value or MetricDatum.StatisticValues is required." Both members are in the model, and a caller that batches observations this way cannot publish at all. Re-run on JSON and Query 2026-10-11. | `cloudwatch/actions.go` `parseDatum` | run | model: "Values and Counts ... up to 150 values per metric with one PutMetricData request, and supports retrieving percentile statistics on this data" | SDK test publishing `Values:[1,2,3] Counts:[1,1,2]`, then `SampleCount` 4 and `Sum` 9, and a `p50` |
| CW-17 | P1 | `EntityMetricData` is validated (one constraint) and then never read. A request that carries only it is refused for a missing `MetricData`; one that carries both stores half. | `cloudwatch/actions.go` `putMetricData` | read | model: "You can include up to two EntityMetricData objects, each of which can contain a single Entity and associated metrics. The limit of metrics allowed, 1000, is the sum of both"; `StrictEntityValidation` "required when EntityMetricData is included" | SDK test with `EntityMetricData` only. Decide first: F (store the metrics, ignore the entity), or refused by name. |
| CW-18 | P1 | Two `PutMetricAlarm` members are checked and then thrown away: `EvaluateLowSampleCountPercentile` and `EvaluationInterval`. `DescribeAlarms` does not return them, so Terraform or CloudFormation reading back sees a change it did not make. *Corrected 2026-10-11: re-run, and the meanings are now read (`model`): `EvaluateLowSampleCountPercentile` takes `evaluate` or `ignore` — "bogus" is accepted here — and is C (store and return); `EvaluationInterval` "cannot be specified for alarms configured with MetricName or Metrics", so it is a refusal. The CloudFormation mapper (`internal/cfn/mapper_cloudwatch.go`) also drops `EvaluateLowSampleCountPercentile` silently.* | `cloudwatch/actions_alarm.go` `putMetricAlarm`; `alarms.go` `alarm`; `internal/cfn/mapper_cloudwatch.go` | run | model (PutMetricAlarm.EvaluateLowSampleCountPercentile, .EvaluationInterval) | SDK round-trip test for the first; a refusal test for the second |
| CW-19 | P1 | Six actions on one state are accepted. Re-run 2026-10-11. *Corrected 2026-10-11: now one instance of CW-37.* | `cloudwatch/actions_alarm.go` `checkActions` | run | *Corrected 2026-10-11: was `memory` (five per state); model: `ResourceList` length 0 to 5 on `AlarmActions`, `OKActions`, `InsufficientDataActions`. (`model`)* | Rejection case with six, on all three wires |
| CW-20 | P3 | An `ExtendedStatistic` of `tm99` or `wm90` is refused as "The value tm99 is not a valid statistic." It is valid on AWS; doze-aws does not compute it. The message must say so, and the row belongs in the ledger. | `cloudwatch/aggregate.go` `parseStat` | read | *Corrected 2026-10-11: was `doc` (statistics definitions); model, `PutMetricAlarm.ExtendedStatistic` lists the forms (`model`). The statistics definitions page itself was not read.* | Test asserts the message names the limit as doze-aws's own |
| CW-21 | P1 | A datapoint stamped in 2030 is accepted, and so is one from 1999 (run 2026-10-11). Direction: more forgiving. | `cloudwatch/actions.go` `parseDatum` | run | *Corrected 2026-10-11: was `memory` (two weeks back, two hours ahead); model, `PutMetricData`: "You can specify time stamps that are as much as two weeks before the current date, and as much as 2 hours after the current day and time." (`model`) What a refusal is called is not stated; `InvalidParameterValue` is the model's likeliest of the four listed.* | Rejection case each side, on all three wires |

Owned by the foundation, referenced not repeated:

- **A2**, the "why" line. CloudWatch's examples of each state: Refused
  (`Period must be 10, 20, 30, or a multiple of 60` once CW-27 is fixed),
  Refused here (the action-target message, metric math, `tm99`), Not implemented
  (any S row; see CW-15).
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
- **B8**, list and map sizes, which CW-37 is the CloudWatch half of.

No finding was dropped from the board report. Corrections to it are in section 11.

**Found by the pre-stage review, 2026-10-11** (the model's own text, and the
three wires and the JavaScript SDK run against a binary). Direction is named in
every row, because "stricter than AWS" breaks a correct caller exactly as "more
forgiving" breaks a deploy.

| Id | Sev | What | Where | Evidence | Direction | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| CW-22 | **P0** | On the JSON wire every timestamp is an RFC 3339 string (`"2026-10-11T12:04:00Z"`). `awsJson1_0` sends timestamps as epoch seconds, and **the JavaScript SDK v3 (`@aws-sdk/client-cloudwatch` 3.1129.0, which speaks `awsJson1_0`) fails every response that carries one**: `TypeError: Expected real number, got implicit NaN` on `DescribeAlarms`, `DescribeAlarmHistory`, `ListDashboards`, and on `GetMetricStatistics` and `GetMetricData` as soon as there is a datapoint. boto3 and the AWS CLI parse the strings, so the console and the CLI hide it. CBOR (tag 1) and Query (ISO 8601) are right. | `cloudwatch/responses.go` (every `time.Time`); `actions_dashboards.go` `dashboardEntryView.LastModified` (a string). *Checked 2026-10-11 by the integrator, against a built binary: the shared encoder is not at fault. KMS `CreationDate`, Secrets Manager `CreatedDate`, DynamoDB `CreationDateTime`, SSM `LastModifiedDate` and Step Functions `creationDate` all come back as JSON numbers. CloudWatch is the one service whose JSON types render a `time.Time` as a string; the fix is CloudWatch's own.* | run (JS SDK, 5 operations) | wrong shape | model: the service declares `awsJson1_0`; the `Timestamp` shape carries no `timestampFormat`, so the protocol default (epoch seconds) applies. *Corrected 2026-10-11: the other JSON-wire services were checked (KMS, Secrets Manager, DynamoDB, SSM, Step Functions: epoch numbers); none shares this defect.* | A raw JSON assertion that each timestamp member of the five operations is a JSON number; a JavaScript SDK spec in `e2e/` (the SDK is already a dependency) |
| CW-23 | P1 | The Query wire never returns `Datapoint.ExtendedStatistics`: a `GetMetricStatistics` for `p50` answers a `<member>` with only `<Timestamp>`. The struct tag is `xml:"-"`. A percentile asked over Query is silently empty. | `cloudwatch/responses.go` `datapointView.Extended` | run | missing | model: `DatapointValueMap` is a map, no `xmlFlattened`: `<ExtendedStatistics><entry><key>p50</key><value>-1</value></entry></ExtendedStatistics>` | Query-wire test: `p50` and `Average` in one datapoint, both present |
| CW-24 | P1 | A model constraint violation answers `InvalidParameterValue` on Query (`<Code>`) and in `x-amzn-query-error: InvalidParameterValue;Sender` on JSON in query mode. The model's `ValidationException` has `awsQueryError` code `ValidationError` (400). `queryCodes` maps both `ValidationException` and `ValidationError` to `InvalidParameterValue`, under a comment that says they "are not shapes in the model"; `ValidationException` is. | `cloudwatch/errors.go` `queryCodes`; `codec.go` `validate` | run | wrong code | model: `ValidationException` `{"code":"ValidationError","httpResponseCode":400}` | Three-wire test: the code for `EvaluationPeriods: 0` is `ValidationException` (JSON, CBOR) and `ValidationError` (Query, and the header) |
| CW-25 | P1 | `DescribeAlarms` and `DescribeAlarmsForMetric` return no `StateTransitionedTimestamp`. One timestamp is stored (`StateUpdatedMs`), moved by every transition and by `SetAlarmState` to the same state; the model has two. | `cloudwatch/alarms.go` `alarm`; `responses.go` `alarmView` | run (member absent) | missing | model: `StateTransitionedTimestamp` "The date and time that the alarm's StateValue most recently changed."; `StateUpdatedTimestamp` "The time stamp of the last update to the value of either the StateValue or EvaluationState parameters." | SDK test: set ALARM, wait, set ALARM again: transitioned stays, updated moves |
| CW-26 | P1 | Deleting an alarm deletes its history, and `DescribeAlarmHistory` for the name answers an empty list. The code's comment, the ledger ("the history goes with the alarm"), the delete confirm ("Its history goes with it."), section 5 and boards Cw-12 and Cw-18 all say so. The model says the opposite. | `cloudwatch/alarms.go` `deleteAlarms`; `internal/console/templates/cw.html` (confirm) | run | wrong (data AWS keeps) | model, `DescribeAlarmHistory`: "CloudWatch retains the history of an alarm even if you delete the alarm." (how long is `memory`: the model gives no window) | SDK test: create, set state, delete, `DescribeAlarmHistory{AlarmName}` returns the rows, and a re-created alarm's history shows both lives |
| CW-27 | P1 | An alarm `Period` of 1 or 5 is accepted. `checkPeriod` is one function for the alarm and for both reads. | `cloudwatch/actions_metrics.go` `checkPeriod` | run | more forgiving | model, `PutMetricAlarm.Period`: "Valid values are 10, 20, 30, and any multiple of 60." (1 and 5 are valid only on the reads, for high-resolution metrics: `GetMetricStatistics.Period`) | Rejection case for 1, 5 and 45 on `PutMetricAlarm`; 1 and 5 still pass on `GetMetricStatistics` |
| CW-28 | P1 | The evaluation window is capped at 86 400 s for every period. 3 600 s × 48, and 86 400 s × 7 (exactly 604 800), are refused. Direction: stricter. | `cloudwatch/actions_alarm.go` `maxEvaluationWindow` | run | stricter | model, `PutMetricAlarm.Period`: "Period multiplied by EvaluationPeriods can't be more than 604,800 seconds. For alarms with a period of less than one hour (3,600 seconds), the total evaluation period can't be longer than one day (86,400 seconds)." | Cases at 59 s × 1 441 (refused), 3 600 s × 48 (accepted), 86 400 s × 7 (accepted), × 8 (refused) |
| CW-29 | P1 | `GetMetricStatistics` accepts `Statistics` and `ExtendedStatistics` together, six statistic names, and a `Statistics` list that repeats a name. | `cloudwatch/actions_metrics.go` `requestedStats` | run | more forgiving | model: "you must specify either Statistics or ExtendedStatistics, but not both"; `Statistics` length 1 to 5, `ExtendedStatistics` 1 to 10; `InvalidParameterCombinationException` is in the operation's errors | Cases: both; six; none (already refused); on all three wires |
| CW-30 | P1 | `DescribeAlarmsForMetric` ignores `ExtendedStatistic` and `Unit`: a call for `p99` returns the `Average` alarm and the `p95` alarm, a call for `Bytes` returns the `Count` alarm. | `cloudwatch/actions_alarm.go` `describeAlarmsForMetric` | run | more forgiving | model: "To filter the results, specify a statistic, period, or unit." | SDK test with three alarms on one metric, one filter each |
| CW-31 | P1 | `Tags` on a re-put replace the old tags, for an alarm and for a dashboard (run: tag `a` became `b`). | `cloudwatch/actions_alarm.go` `putMetricAlarm`; `dashboards.go` `putDashboard` | run | wrong | model, `PutMetricAlarm.Tags`: "If you are using this operation to update an existing alarm, any tags you specify in this parameter are ignored."; `PutDashboard.Tags`: "If you specify Tags when updating an existing dashboard, the tag updates are ignored." | Put with tag `a`, re-put with `b`, list: `a` only |
| CW-32 | P1 | Four `PutMetricAlarm` members answer wrongly. `EvaluationWindow` (sliding or wall clock) is accepted and dropped. `EvaluationCriteria` (PromQL) is answered "The parameter ComparisonOperator is required". `EvaluationInterval` is accepted and dropped where it must be refused (CW-18's second half). `EvaluateLowSampleCountPercentile: "bogus"` is accepted. | `cloudwatch/actions_alarm.go` `putMetricAlarm` | run | more forgiving; wrong code | model: `EvaluationWindow` "Specify either a sliding window or a wall clock window. If you omit this parameter, the alarm uses a sliding window."; `EvaluationCriteria` "you must specify either MetricName, a Metrics array, or an EvaluationCriteria"; `EvaluationInterval` "cannot be specified for alarms configured with MetricName or Metrics"; "Valid Values: evaluate or ignore" | Each member, on all three wires: refused by name with "Refused here" (the first three), a 400 for the fourth |
| CW-33 | P1 | `DeleteDashboards` with one name that does not exist deletes none and answers `ResourceNotFound` (run). The ledger says "`DeleteDashboards` is atomic, as on AWS"; the function's comment says AWS "answer[s] ResourceNotFound without deleting anything". | `cloudwatch/dashboards.go` `deleteDashboards`; `docs/SUPPORT.md` | run | stricter | model, `DeleteDashboards`: "If there is an error during this call, the operation attempts to delete as many dashboards as possible." The operation's error list has no not-found shape: `ConflictException`, `InternalServiceFault`, `InvalidParameterValueException`. What it answers for the missing name is not established. | Delete `a` and `ghost`: `a` is gone; the code for `ghost` is queued |
| CW-34 | P1 | The dashboard ARN carries a region (`arn:aws:cloudwatch:us-east-1:000000000000:dashboard/x`), and dashboards are per region (a dashboard put in `us-east-1` is `ResourceNotFound` in `us-west-2`, run). | `cloudwatch/dashboards.go` `dashboardARN`; the per-region store | run | wrong shape; wrong scope | model: dashboard ARN `arn:aws:cloudwatch::account-id:dashboard/dashboard-name`; `PutDashboard`: "All dashboards in your account are global, not region-specific." | ARN assertion with `--region eu-west-1`; put in one region, get in another |
| CW-35 | P1 | The tag operations answer `ResourceNotFound` for a missing alarm or dashboard; the model lists `ResourceNotFoundException` for all three. An alarm ARN of another account or region is accepted and tags the local alarm of that name (run). 60 tags are accepted on one alarm (run). | `cloudwatch/actions_dashboards.go` `taggable`, `withTags`, `tagResource` | run | wrong code; more forgiving | model: `TagResource`/`UntagResource`/`ListTagsForResource` errors `ResourceNotFoundException` (code `ResourceNotFoundException`, 404, members `ResourceType` and `ResourceId`); `TagResource`: "You can associate as many as 50 tags with a CloudWatch resource." (the `aws:` prefix rule is `memory` and not built) | A missing alarm per wire; a foreign ARN; the 51st tag |
| CW-36 | P1 | `Value: NaN` and `Infinity` are accepted over Query (and CBOR can carry them), stored, and then every read of that series answers HTTP 500 "internal error", because the JSON response cannot encode them. `1e200` is accepted too. | `cloudwatch/actions.go` `parseDatum` | run (Query put, JSON read) | more forgiving; a failure AWS would not produce | model, `PutMetricData.Value`: "Values must be in the range of -2^360 to 2^360. In addition, special values (for example, NaN, +Infinity, -Infinity) are not supported." Same text on `Values`. | Query put of `NaN`, `Infinity`, `-Infinity`, `1e200`: each refused; a series is never poisoned |
| CW-37 | P1 | Every list-level size in the model is missing from `constraintTables` (B8). Run on 2026-10-11: 31 dimensions on `PutMetricData` and on `PutMetricAlarm` accepted; 101 names on `DescribeAlarms` accepted and on `DeleteAlarms` answered "No alarms matched"; six actions (CW-19); six statistics (CW-29). The full list: `Dimensions` ≤ 30 (`PutMetricData`, `PutMetricAlarm`, `GetMetricStatistics`, `DescribeAlarmsForMetric`), `DimensionFilters` ≤ 10 (`ListMetrics`), the three action lists ≤ 5, `AlarmNames` ≤ 100 (four operations), `Statistics` 1 to 5, `ExtendedStatistics` 1 to 10, `KeyAttributes` 2 to 4 and `Attributes` ≤ 10 on an entity, and the map-key lengths. | `cloudwatch/validate.go` `constraintTables`; `testdata/cases_cloudwatch.json` | run | more forgiving | model: the list `length` traits (`dzaudit list cloudwatch`) and the member text ("You can use up to 30 dimensions per metric") | Regenerate `cases_cloudwatch.json`, run, and watch the new cases fail first |
| CW-38 | P1 | Samples published with different units for one metric are aggregated into one datapoint carrying the first unit seen: `Seconds 10`, `Milliseconds 1000` and a unitless `7` came back as `SampleCount 3, Sum 1017, Unit Seconds`. | `cloudwatch/aggregate.go` `bucket.add`; `actions_metrics.go` | run | wrong data | model: "Metric data points that specify a unit of measure, such as Percent, are aggregated separately."; `GetMetricStatistics.Unit`: "If you omit Unit, all data that was collected with any unit is returned, along with the corresponding units" | Three units, one period: three datapoints, each with its unit; an alarm without a unit |
| CW-39 | P2 | `SetAlarmState` with a `StateReasonData` that is not JSON is accepted and stored as given. | `cloudwatch/actions_alarm.go` `setAlarmState` | run | more forgiving | model: the operation lists `InvalidFormatFault` ("Data was not syntactically valid JSON.", `InvalidFormat`, 400); `StateReasonData` "in JSON format" | `StateReasonData: "not json{"` refused as `InvalidFormatFault` (`InvalidFormat` on Query) |
| CW-40 | P2 | `DescribeAlarms` accepts `AlarmNames` together with `AlarmNamePrefix`, and `ChildrenOfAlarmName` or `ParentsOfAlarmName` with other filters (all answered, applying both). | `cloudwatch/actions_alarm.go` `describeAlarms` | run | more forgiving | model: `AlarmNamePrefix` "If this parameter is specified, you cannot specify AlarmNames."; `ChildrenOfAlarmName` "you cannot specify any other parameters in the request except for MaxRecords and NextToken. If you do so, you receive a validation error." The error's name is not stated. | Each combination refused |
| CW-41 | P2 | Rules that are in the members' text and not in a trait are not enforced on the alarm and `PutMetricData` inputs: a dimension name starting with a colon, and a non-ASCII dimension value, namespace (accepted); `StorageResolution` 7 (accepted); an alarm name with an ASCII control character (accepted); 1 001 metrics in one call (accepted; `maxBody` is 8 MiB where the model's limit is 1 MB). | `cloudwatch/actions.go` `parseDatum`, `putMetricData`; `codec.go` `maxBody` | run | more forgiving | model: dimension names "must contain only ASCII characters, must include at least one non-whitespace character, and cannot start with a colon (:)"; `Namespace` "You can use ASCII characters for the namespace, except for control characters"; `StorageResolution` "Valid values are 1 and 60"; `AlarmName` "can't contain ASCII control characters"; `MetricData` "no more than 1000 metrics per call"; "limited to 1 MB in size for HTTP POST requests" (before or after gzip is not stated) | One case each |
| CW-42 | P2 | `GetMetricData` accepts two queries with the same `Id`, an `Id` starting with an upper-case letter, and 501 queries; never paginates (`MaxDatapoints` ignored, `NextToken` never returned, `StatusCode` never `PartialData`). | `cloudwatch/actions_metrics.go` `getMetricData` | run | more forgiving | model: `Id` "This name must be unique within a single call to GetMetricData ... The first character must be a lowercase letter."; "as many as 500 MetricDataQuery structures"; `MaxDatapoints` "If you omit this, the default of 100,800 is used" | Cases for each; two queries over 100 800 points page |
| CW-43 | P2 | A percentile is computed over negative values and over a statistic set whose observations cannot be recovered (the mean stands in: `SampleCount 10, Min 1, Max 50` answered a `p99` of 10). Separately `parseStat` accepts `p1e1`, `p+5` and `P95`. | `cloudwatch/aggregate.go` `parseStat`, `bucket.add`, `percentile` | run | more forgiving | model: "Percentile statistics are not available for metrics when any of the metric values are negative numbers."; "you can only retrieve percentile statistics for this data if ... SampleCount value of the statistic set is 1 and Min, Max, and Sum are all equal. The Min and Max are equal, and Sum is equal to Min multiplied by SampleCount". What the datapoint then carries is not stated (omitting the statistic is the likeliest). | The three cases; `parseStat` table |
| CW-44 | P2 | A series is never removed from `ListMetrics` (the sweeper drops samples only), and a `NextToken` that is valid base64 but names nothing answers 200 with an empty page; one minted under another filter is accepted too (read: the token is a bare store key). | `cloudwatch/samples.go` `sweep`; `actions.go` `decodeToken` | run (token), read (sweep) | more forgiving | model: "ListMetrics doesn't return information about metrics if those metrics haven't reported data in the past two weeks."; the model lists only `InternalServiceFault` and `InvalidParameterValueException` for `ListMetrics`, so `InvalidNextToken` (which `DescribeAlarms`, `DescribeAlarmHistory` and `GetMetricData` do list) is `memory` here | A series whose last sample is 15 days old is gone; a token of garbage |
| CW-45 | P2 | An alarm action naming a Lambda version or alias (`...:function:fn:prod`) invokes the function unqualified: `classifyAction` keeps field 6 and `LambdaInvokeAsync` takes a name. | `cloudwatch/actions_fire.go` `classifyAction`, `deliver`; `internal/peercall` | read | wrong | model, `PutMetricAlarm.AlarmActions`: "Invoke a specific version of a Lambda function: arn:aws:lambda:region:account-id:function:function-name:version-number; Invoke a function by using an alias: ...:function-name:alias-name" | Alarm on `fn:2`, state change, the invoke path carries `Qualifier=2` |
| CW-46 | P2 | The window an alarm judges is wall-clock aligned and one period behind (`alignDown(now, period)`): data published now is invisible until its period closes, up to one period plus ten seconds (an hour for `Period: 3600`). The page and the ledger say "AWS lags for the same reason" (`memory`). The model's default is a sliding window that "advances each time the alarm is evaluated", and the wall-clock window is an option with its own rules. | `cloudwatch/evaluate.go` `evaluate` | read | not established; plausibly later than AWS | model, `EvaluationWindow`: "A sliding window advances each time the alarm is evaluated, forming a rolling time window. A wall clock window aligns the evaluated range to fixed clock boundaries"; `WallClockWindow` "the alarm's period must be 1 minute (60 seconds), 5 minutes (300 seconds), 1 hour (3,600 seconds), 1 day (86,400 seconds), or 1 week (604,800 seconds)". How the sliding window rounds is not stated. | Recording scenario first (`alarm_window_alignment`); then a clocked test of whichever is decided |
| CW-47 | P2 | `PutDashboard` checks only that the body is JSON (`[]`, `123` and `{"widgets":[{"type":"nope"}]}` are accepted, with `DashboardValidationMessages: []`), and the name is not checked (a 256-character name, and `has space/slash`, are accepted). `DashboardValidationMessages` is a list of strings; the model's is a list of `{DataPath, Message}`. | `cloudwatch/actions_dashboards.go` `putDashboardAction` | run | more forgiving; wrong shape | model, `PutDashboard.DashboardName`: "The maximum length is 255, and valid characters are A-Z, a-z, 0-9, "-", and "_"."; `DashboardValidationMessages`: "If this result includes error messages, the input was not valid and the operation failed." The body's rules are on a page this review did not read (`doc`, queued). `ListDashboards.DashboardNamePrefix` adds "." to the characters: the two texts disagree, which is itself for the recording. | Name cases; the response shape with a warning; the body rules after the page is read |
| CW-48 | P2 | **`memory`, queued, not fixed.** The notification's `Region` and the Subject line carry the region code (`us-east-1`); AWS is recalled as writing the display name (`US East (N. Virginia)`). An alarm's Lambda action is recalled as receiving an event of its own shape (`source`, `alarmArn`, `alarmData`), not the SNS-style JSON this sends. | `cloudwatch/actions_fire.go` `alarmPayload`, `fireActions` | read | wrong (unestablished) | memory: no model text. | Recording scenario `alarm_notification_payload` first; the test is written and skipped |
| CW-49 | P2 | The Query wire has no test per operation. L5 asks for one accepted-as-the-reference-spells-it test per operation per wire; 17 of 19 had none. (All 19 were accepted when posted by hand: run.) The first run missed that Query does not gunzip (CW-53) and drops a response member (CW-23) because nothing posts those. | `cloudwatch/threewire_test.go` | read | test debt | n/a | `TestEveryOperationIsAcceptedOnTheQueryWire` posting all 19 with the reference's spellings, as `sqs/query_wire_test.go` does |
| CW-50 | P3 | `StateReasonData` and `HistoryData` are not bounded: a 1 440-period alarm wrote a `StateReasonData` of 19 083 characters (run). The model: `StateReasonData` 0 to 4 000, `HistoryData` 1 to 4 095. | `cloudwatch/evaluate.go` `evaluate` | run | wrong shape | model: the two length traits | Alarm over 1 440 periods: both fit, and say so |
| CW-51 | P3 | The ledger is wrong on three counts: the retention schedule ("1-second data for 3 hours, 1-minute for 15 days and 1-hour for 63 days"), "`DeleteDashboards` is atomic, as on AWS", "the history goes with the alarm". | `docs/SUPPORT.md` CloudWatch section | read | n/a | model: `GetMetricData`/`GetMetricStatistics`: "Data points with a period of less than 60 seconds are available for 3 hours ... 60 seconds (1-minute) are available for 15 days ... 300 seconds (5-minute) are available for 63 days ... 3600 seconds (1 hour) are available for 455 days (15 months)" | The ledger test (CW-12) greps the three sentences out |
| CW-52 | P3 | The sample cap claims to drop "the oldest WITHIN each series"; the code takes the first `over` keys in key order, so one series' oldest samples go first, and then the next series' entirely. | `cloudwatch/samples.go` `sweep` | read | comment wrong | n/a (ours) | A two-series store over the cap: each loses its oldest, or the comment changes |
| CW-53 | P1 | The Query wire does not gunzip. A `PutMetricData` form body with `Content-Encoding: gzip` answers `InvalidParameterValue` "malformed form body: invalid URL escape". JSON and CBOR do (run). The ledger and the codec's comment say all three do. A Query-era SDK compresses a large `PutMetricData` (memory: botocore's request compression is on by default above 10 KiB, which a 1 000-metric call exceeds). | `cloudwatch/codec.go` `parseQuery`; `internal/awsquery` | run | stricter (a valid request refused) | model: `smithy.api#requestCompression {"encodings":["gzip"]}` on `PutMetricData`; the trait is protocol-independent | A gzipped Query body is accepted; so is one with `Content-Encoding: gzip` and a body that is not |

**Moved from this page's earlier notes, now closed as not findings** (read in
the model): `ListMetrics` `IncludeLinkedAccounts`/`OwningAccount` (single account,
a listed difference); `LabelOptions` (affects only time-based dynamic labels);
`StorageResolution` semantics (C, validated 1 or 60, CW-41).

## 5. What to check

Beyond the common criteria. Each line is checked on **all three** wires unless
it says otherwise. 50 items. *Corrected 2026-10-11: items below carry the
model's words where the first draft said "not established"; nothing is ticked.*

**Publishing**
- [ ] Namespace: 1 to 255, not starting with a colon, ASCII (CW-41); `AWS/`
      refused for a client and accepted from a peer (*the refusal is `memory`:
      the model says "you should not"; queue*).
- [ ] A datum needs exactly one of `Value`, `StatisticValues`, `Values`
      (with optional `Counts`); two together are refused (CW-16).
- [ ] `Values` and `Counts` of different lengths; more than 150 values; a
      count of zero. *Model: up to 150 unique values; "must include the same
      amount of values as the Values array"; the default count is 1; a zero
      count is not stated (queue).*
- [ ] `StatisticValues`: all four required (model), `SampleCount` above zero
      and minimum not above maximum (`memory`, kept as the code has them).
- [ ] Dimensions: both halves required; at most 30 (CW-37); names ASCII, not
      starting with a colon (CW-41); the same name twice (not established).
- [ ] Unit: the 27-value enum; an absent unit.
- [ ] Timestamp bounds on both sides (CW-21): two weeks back, two hours ahead.
- [ ] Value range ±2^360, and no NaN or infinity (CW-36).
- [ ] Number of data in one call: 1 000 across `MetricData` and
      `EntityMetricData`; the 1 MB request; compressed or not (CW-41, CW-53).
- [ ] `StorageResolution` 1 and 60, default 60; any other value refused (CW-41).
- [ ] A gzipped body on each wire (CW-53).
- [ ] `EntityMetricData` (CW-17).

**Reading**
- [ ] `ListMetrics`: namespace, name and dimension filters (at most 10
      dimension filters); a dimension filter with a name only; 501 series pages
      at 500; an invalid token is `InvalidNextToken` (CW-44); `RecentlyActive`;
      a series with no data for two weeks is gone (CW-44).
- [ ] `GetMetricStatistics`: period granularity; start not before end; the
      1 440 ceiling; a period with no observations is omitted; `Unit` filter;
      *Statistics or ExtendedStatistics, not both (CW-29); percentiles carried
      on the Query wire (CW-23)*.
- [ ] `GetMetricData`: `ScanBy` both ways; `ReturnData: false`; `Label`; two
      queries with the same `Id` (refused, CW-42); `MaxDatapoints` default
      100 800 and `NextToken` (CW-42); `Expression` refused as doze-aws's own
      limit.
- [ ] Percentiles: `p0`, `p100`, `p99.9`, `p101`, `P95`, and a percentile over
      `StatisticValues` records and over negative values (CW-43).
- [ ] Period boundaries: a sample exactly on a boundary lands in one bucket,
      the later one (model: "StartTime ... inclusive", "EndTime ... exclusive").
- [ ] Mixed units are separate datapoints (CW-38).

**Alarm definition**
- [ ] Every member in the per-member table round-trips through
      `DescribeAlarms`, on each wire, with nothing added and nothing lost
      (CW-18, CW-25).
- [ ] `Statistic` and `ExtendedStatistic` together refused; neither refused.
- [ ] Period 10, 20, 30 and multiples of 60 (CW-27); `Period × EvaluationPeriods`
      at 86 400 and one over under an hour, 604 800 and one over from an hour up
      (CW-28); `DatapointsToAlarm` equal to and above `EvaluationPeriods`.
- [ ] `TreatMissingData`: the four values, and a fifth.
- [ ] The three anomaly operators, `Metrics`, `ThresholdMetricId`,
      `WarmUpConfiguration`, `EvaluationWindow`, `EvaluationCriteria` and
      `EvaluationInterval` each refused, with a message that says the limit is
      doze-aws's (CW-32).
- [ ] Actions: an SNS ARN, a Lambda ARN with and without a qualifier (CW-45),
      any other ARN, a malformed ARN, six on one state (CW-19), a topic that
      does not exist (decide: refuse at put, or accept as AWS is believed to).
- [ ] Re-putting an alarm keeps its state, reason, state timestamp and tags
      (tags in the request are ignored, CW-31), and writes "updated".
- [ ] Re-putting resets `ActionsEnabled` to true unless the caller says
      otherwise (*model: established*).
- [ ] An alarm name of 255 and 256; a name with a control character (CW-41); a
      name with a colon or a slash, and what the ARN and the tag lookup
      (`taggable`) then do.

**Evaluation**
- [ ] The window and its alignment: decided after recording (CW-46). Until
      then: the window is lagged by one period; a breach in the filling period
      does not move the alarm.
- [ ] M of N for 1/1, 2/3, 3/3, with each missing-data treatment and with
      missing periods at the start, middle and end.
- [ ] All periods missing: `missing` gives INSUFFICIENT_DATA, `notBreaching`
      OK, `breaching` ALARM, `ignore` no move.
- [ ] `Unit` on the alarm ignores samples of another unit.
- [ ] A new alarm is INSUFFICIENT_DATA with "Unchecked: Initial alarm
      creation" (`memory`) and the actions of the state it then reaches run
      (model: "The alarm is then evaluated and its state is set appropriately.
      Any actions associated with the new state are then executed."); whether
      INSUFFICIENT_DATA actions run at creation is not established.
- [ ] The reason sentence for each operator and each direction (CW-5), and
      `StateReasonData` parses as JSON (*its keys are `memory`: the model names
      none*) and fits 4 000 (CW-50).
- [ ] An alarm on a metric nobody publishes stays INSUFFICIENT_DATA and writes
      no history.

**State by hand, and actions**
- [ ] `SetAlarmState`: each state; an unknown state; a missing alarm
      (`ResourceNotFound`, 404 — model); a reason of 1 023 and 1 024;
      `StateReasonData` of 4 000 and 4 001, and not JSON (CW-39,
      `InvalidFormat`).
- [ ] The same state twice (CW-8: no actions).
- [ ] How long a hand-set state lasts, when the metric agrees and when it does
      not (CW-9).
- [ ] Actions fire once per transition, for the state entered, with AWS's
      payload: every key in `alarmPayload`, `Statistic` upper-cased, the
      Subject line, the region's spelling (CW-48). A percentile alarm's
      `Trigger` (`StatisticType`, `ExtendedStatistic`) is not established.
- [ ] Actions off: state changes, history records it, nothing is sent;
      enabling writes "Alarm actions enabled" once and does not replay
      (`memory`).
- [ ] A failed delivery (CW-10).
- [ ] Enable and disable with a name that does not exist; with an empty list.

**History**
- [ ] The three item types; `HistoryItemType` filter; `StartDate` and
      `EndDate` bounds (inclusive or not: not established); `ScanBy`;
      `MaxRecords` 100 and 101; paging (CW-13).
- [ ] Two entries in one millisecond keep their order.
- [ ] *Corrected 2026-10-11: was "Deleting an alarm removes its history;
      recreating it starts clean."* Deleting an alarm keeps its history, and a
      re-created alarm's history shows both (CW-26; model: "CloudWatch retains
      the history of an alarm even if you delete the alarm").
- [ ] `DeleteAlarms`: all exist, some exist (the right ones are deleted), none
      exist; more than 100 names refused (CW-37).

**Dashboards and tags**
- [ ] A dashboard body round-trips byte for byte; a body that is not JSON is
      refused with the right name per wire (CW-14); valid JSON that is not a
      dashboard: *model: "DashboardValidationMessages", the body's rules are on
      a page not yet read (CW-47)*.
- [ ] Dashboard name rules (255; A-Z a-z 0-9 - _), `DashboardNamePrefix`,
      paging at 1 000 (CW-13), and `DeleteDashboards` with one missing name
      deleting the rest (CW-33).
- [ ] The dashboard ARN has no region, and a dashboard is visible from every
      region (CW-34).
- [ ] Tags on an alarm and on a dashboard: add, overwrite, remove a missing
      key, 50 and 51 tags (CW-35), an `aws:` key (`memory`), an ARN for neither
      kind, an ARN in another account or region (CW-35).

**Producers** (in-package, with the peer faked; the real ones are section 9)
- [ ] A peer may write `AWS/`; a stack without CloudWatch still runs its producers.

**Lifecycle**
- [ ] Alarms, their state, `StateSetManually`, history, dashboards and tags
      survive a restart. Samples survive a clean stop; the store is `NoSync`,
      so what a crash loses is written into `SUPPORT.md`.
- [ ] The sweep drops samples past retention and past the sample cap, oldest
      first (CW-52), and a series with no samples for two weeks leaves
      `ListMetrics` (CW-44).
- [ ] Every ARN (alarm, dashboard, `AWSAccountId` and `Region` in the payload)
      follows a non-default `--account-id` and `--region`.

## 6. Console

The design's "CloudWatch" page: 21 boards. Templates: `templates/cw.html`
(`cw_lp`, `cw_browser`, `cw_chart`, `cw_home`, `cw_metric`, `cw_alarm`,
`cw_state`), the `cw_create` block in `create.html`, shared `fail_inline`,
`tags_panel` and `tag_editor` in `panes.html`. Handlers: `handlers_cw.go`.
Client: `client_cw.go`, the `cw` cases in `client_tags.go`. Routes: `cwRoutes`
in `routes_compute.go` (four GET, four POST). The console speaks JSON 1.0 to
the service (`client_cw.go`), which is why CW-22 never showed in it.

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
| Cw-18-Delete | Delete confirm | `hx-confirm` in `cw_alarm`; `cwDeleteAlarm` | exists with a one-line body; *Corrected 2026-10-11: was "the confirm is restyled and counts history rows" — it is not; the body and the count are new, and the sentence is corrected for CW-26* |
| Cw-19-No-Notification | State changed, nothing sent | `cw_alarm` `history` | the page exists; the explainer and the Denied callout are **new** |
| Cw-20-No-Data | An alarm on a metric nothing publishes | `cw_alarm` `metric` | exists; the explanation is **new** |

**New items, and what each is built from**

| Item | Board | Built from |
|---|---|---|
| Alarms column in the metric browser | Cw-Home | `ListAlarms` and `ListMetrics`, matched on namespace, name and exact dimensions in the console. Not `DescribeAlarmsForMetric` until CW-4 is fixed. |
| Create alarm from a metric row | Cw-02 | The metric key `/cw/create?metric=` already reads |
| Datapoints to alarm field | Cw-04, 05 | `PutAlarm` already sends it when set; `cwCreateAlarm` must read it |
| On OK topic | Cw-04, 05 | `PutAlarm` already sends `OKActions`; the handler must read it |
| Teaching lines under each field | Cw-04, 05, 09 | Foundation B4 over `constraintTables["PutMetricAlarm"]` (regenerated, CW-37), plus the hand rules below |
| How this alarm decides | Cw-04, 05 | The form's own numbers and three constants: the ten-second tick, the one-period lag (CW-46), the missing-data rule. A pure function of the form. |
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
| Period: 10, 20, 30 or a multiple of 60 | *Corrected 2026-10-11: was "1, 5, 10, 20, 30 or a multiple of 60 — `checkPeriod` (hand rule; `doc`, queued)"; model, `PutMetricAlarm.Period`.* `checkPeriod` is split in two (CW-27): the alarm's set, and the reads' set where 1 and 5 are valid for high-resolution metrics. |
| Evaluation periods at least 1; Period × Evaluation periods at most 86 400 under an hour, 604 800 from an hour up | *Corrected 2026-10-11: was "at most 86 400 — `maxEvaluationWindow` (`doc`, queued)"; model, `PutMetricAlarm.Period`.* |
| Datapoints 1 to N, default N | model and `putMetricAlarm` |
| Actions: at most five per state | model (`ResourceList` length 0 to 5) |
| Statistics offered | `cwStats` in `handlers_cw.go`, which must be a subset of what `parseStat` accepts; a test should pin it |
| Missing data: four values, default `missing`, and what each does | `treatMissingValues` and `evaluate` |
| Evaluated every ten seconds, over completed periods | `evalInterval`, `evaluate` (what AWS does is CW-46) |
| Percentiles are exact; samples kept 24 hours | "Differences from AWS" in `SUPPORT.md`; the AWS schedule is in the model (CW-51) |
| An alarm with no action still changes state; SNS or Lambda only | `fireActions`, `checkActions` |
| A metric is its namespace, name and exact dimensions | `seriesKey` |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Cw-14: "AWS describes SetAlarmState as setting the state temporarily, for
  testing." *Corrected 2026-10-11: was `memory`; model: "Temporarily sets the
  state of an alarm for testing purposes." (`model`).*
- Cw-14, Cw-15: the hold is "one pass, then the pass after", and the return is
  "between :18 and :28". True of today's code (CW-9) and must follow whatever
  CW-9 decides.
- Cw-14: "Setting the state the alarm already has still counts as a change and
  runs the actions again." *Corrected 2026-10-11: true today and not AWS's
  (CW-8, `model`); the board is corrected to say no actions run.*
- Cw-05, Cw-14: "95 % of a period's observations are lower than this."
  AWS's definition, `memory`.
- Cw-14: the Subject line `ALARM: "name" in us-east-1` — the region's spelling
  is `memory` (CW-48).
- Cw-19: the Denied callout on the alarm page does not exist today; the text is
  real but appears only in the wire's inspector and the log.
- Cw-19: "at create time only checked that it names an SNS topic or a Lambda
  function." True (`classifyAction`); changes if section 5 decides to check
  that the topic exists.
- Cw-18: "and its 4 history entries". The count comes from a call capped at 50.
  *Corrected 2026-10-11: the entries now stay (CW-26); the board says so.*
- Cw-18: "Deleting one that is already gone is refused as ResourceNotFound".
  True of doze-aws; what AWS answers for a missing name is not established
  (the model lists `ResourceNotFound` and also says "the alarms you specify
  with correct names are still deleted").
- Cw-07, Cw-08: "samples are kept for 24 hours by default" is the default of a
  setting; the page should read the configured retention.
- Cw-04, Cw-05: the "N of M" figures on the boards are illustrative.
- Cw-09: "The period still filling is not judged" is doze-aws's evaluator;
  whether AWS judges a partial period is CW-46.
- Cw-12: "Why it moved": the keys of `HistoryData` are doze-aws's (`memory` for
  AWS's).

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each of the 21 boards from the working copy of
the dream design (`console-design/project/Cw-*.dc.html`, 1440 by 900) and the
real page in the same state, side by side: light for all, dark for the home,
create, metric and alarm pages, and 900 wide for the alarm page (the fact strip
wraps; nothing is clipped). The real pages were driven from a seeded instance
(`harbour-*` alarms and metrics, an SNS topic with an SQS subscriber) on a binary
built from the checkout. Screenshots: `scratchpad/dcheck/cw/real-*.png` against
`scratchpad/shots/Cw-*.png`. The icons stay: every real page draws the AWS
CloudWatch icon in the rail, the list pane head and the resource header where
the boards draw "CW" (L12). The shell's own differences (a rail with names and
counts, a list pane with a count, an info button, `+` and a filter, the region
and theme in the top bar) are the foundation's and are not repeated per board.

None of the 21 boards matches in full, and 20 differ in shape (Cw-13, the tag
editor, differs only in two sentences). The first pass built the *content* of the
pages that existed and none of the *shape* of the boards: every board but the
tag editor is a two-column page or a dialog with a side panel in the design,
and a single column of panels here.

| Board | Verdict | What differs |
|---|---|---|
| Cw-Home | differs | Alarms table: the Condition column prints the raw operator (`Sum GreaterThanThreshold 5`, `GreaterThanOrEqualToThreshold 1`) where the board prints `Sum > 5` and `Sum ≥ 1`, and the alarm name wraps. Metrics browser: one full-width table per namespace stacked in one column; the board has a two-column grid of namespace cards with a right-aligned **Alarms** count column (new). The "No AWS/SQS, AWS/SNS, AWS/DynamoDB or AWS/Kinesis here" note is missing. The "evaluated every ten seconds over completed periods" line is there. |
| Cw-01-None | differs | The board is the shell's `empty_detail`: a centred icon, "No alarms yet", the paragraph "An alarm watches one metric over a window and notifies a topic or a function ... testable before you deploy it.", a **New alarm** button and a Metrics card. The page is the full home with a "Create alarm" button and a Metrics panel; that paragraph is not rendered (A7: `cw_home` never calls `empty_detail`). |
| Cw-02-Metrics-Only | differs | No per-row **Create alarm** link, no "Metrics are here, alarms are not" note, no namespace grid. |
| Cw-03-Create-None | differs | The board draws a bordered callout "No metrics to watch yet" and a second note "Who publishes one" (new). The page prints the same sentence as bare centred text and has no second note. |
| Cw-04-Create | differs; **the design was wrong** (period set, ceiling: corrected) | Two columns in the design: the form on the left, **Notify** (ALARM topic with "delivers to 1 subscription", an On OK topic, the no-action sentence) and **How this alarm decides** (the live strip and four lines) on the right. One column here: General, Condition, Notify, in that order. Teaching lines: the page has line 1 for most fields and line 4's sentence for Missing data only; the board has lines 1 to 4 on every field with `enforced` markers and line 5 (the neighbour check "has data in 0 of the last 60 one-minute periods"). Missing: Datapoints to alarm, On OK topic, the value in words ("1 minute"), the sentence on each statistic, the operator's meaning. The operator select shows `>` where the board shows `> GreaterThanThreshold`. The "A name that already exists is replaced" footnote is missing. The CLI preview is there (`cli`). |
| Cw-05-Create-Replace | differs; **the design was wrong** (as 04) | As 04, and the "This replaces an alarm" callout ("harbour-pick-time-slow exists ... an action on INSUFFICIENT_DATA is dropped, and actions are switched back on") is missing: the form posts, replaces and toasts. The p95 sentence and the "9 of the last 12 periods" line are missing. |
| Cw-06-Create-Refused | differs, partly deliberately | The refusal callout is under the Cancel / Create row at the bottom of the form (the page scrolled so the header was off screen); the board puts it above the form, and a one-line message beside the Period field ("45 seconds is none of ..."). The page's footer sentence is the foundation's A2 wording ("doze-aws refused the request: it breaks a rule of the service. The input is what to change."), which is deliberate; the board's "because AWS does — the rule comes from AWS's own service model" is the older text. The side column "Other refusals this form can come back with" is design-only teaching and is not built. The code's message still reads "must be 1, 5, 10, 20, 30, or a multiple of 60" (CW-27). **The design's three facts were wrong** and are corrected in the working copy: the period set, the ceiling and its "3 600 s × 25 periods" example. |
| Cw-07-Metric | differs | No `Stage=prod` chip beside the title (it is a copy chip under it) and no **All metrics** button. The chart has no first-point and last-point time labels (new). The window note "The window sets the period" is missing. "Alarms on this metric" is a table below the chart, where the board has a right-hand column with `ALARM  Sum > 5  1 × 60 s`; the condition prints the raw operator. The two teaching notes ("Two metrics, one name", "A missing minute is not a zero") are missing. The statistic and window selectors do not say "changing either redraws the chart". Last/Min/Max/Points are there. |
| Cw-08-Metric-Quiet | differs | "Nothing in the last 1h" is in the chart panel (rendered as one sentence, the board's has a heading). The note "Why a metric with no points is listed" is missing. "Create an alarm on it" is a text link in a panel under the chart; the board has a dark button in the right column. |
| Cw-09-Alarm-State | differs | Two columns in the design (left: "Why it is in ALARM since …" and the **Settings** table, one row per setting with a sentence, Missing data marked new; right: Actions with "Nothing is set for OK or INSUFFICIENT_DATA, so a return to OK is silent", Set state, the actions switch). One column here: State, Actions, Set state, Actions are enabled. The State fact strip shows `0/1 × 3600s` (CW-1). The Set-state control is a `<select>`, the board a segmented OK / ALARM / INSUFFICIENT_DATA control with a "What it does" link. The reason line has no "Why it is in ALARM" heading. |
| Cw-10-Alarm-Fresh | differs | As 09, and "What happens next" (new) and the Settings summary are missing; the page shows the reason and the fact strip only. |
| Cw-11-Alarm-Metric | differs | The Statistic select is empty ("Select...", CW-3). No dashed threshold line and no red breaching points (new). The right column ("What the alarm reads", "Chart period and alarm period") is missing, and so are the point-time labels. |
| Cw-12-Alarm-History | differs; **the design was wrong** (history on delete: corrected) | A flat three-column table here; the board has an expandable row ("Why it moved": statistic · period, recent datapoints, threshold, evaluated · breaching — needs CW-11) and a right-hand column ("Three kinds of row", "A change with no Action row", "14:41:08 and 14:41:23"). The board's "History goes with the alarm when it is deleted" is the opposite of the model (CW-26) and is corrected. The Type badge and the summaries match. |
| Cw-13-Alarm-Tags | matches (shape); copy differs | The tag editor is the shell's draft editor (A5): rows, **Add tag**, **Save changes** disabled until a change. The board's two sentences ("A draft you submit ...", "Tags belong to the alarm, not to a create. Creating the alarm again does not clear them.") are missing; the second is `model` since CW-31 (a re-put's tags are ignored). |
| Cw-14-SetState | differs; **the design was wrong** (same state: corrected) | The board is the State tab with the Set-state panel composing, and a right column: "It is a temporary override", "What pressing it does" (four timed steps), and "What harbour-ops-alerts will receive" (Subject and a payload excerpt). The page has the panel and none of the right column. The blank reason's default text ("Set from the doze-aws console") is not shown under the field; the field carries a generic placeholder ("testing the notification"). The board's last line, "Setting the state the alarm already has still counts as a change and runs the actions again", stated doze-aws's bug as AWS's and is corrected (CW-8). |
| Cw-15-SetState-Done | differs | No inline History excerpt, no "What it did" panel (three timed lines), no "The reason is the text you gave. An evaluation writes its own." The page shows the new state, the reason and a toast ("Alarm “…” set to ALARM"). |
| Cw-16-SetState-Refused | differs | The refusal sits under the form fields inside the panel, which is right, and the foundation's wording is deliberate (as 06). The board's right column ("Other refusals", "What is not a refusal") is design-only teaching; "What you typed is kept" is true of the page. The field has no `up to 1 023 characters · enforced` line (teaching line 4). |
| Cw-17-Actions-Off | differs | The `actions off` badge is there. The inline History excerpt and the line "The 14:46:02 move has no Action row under it. That is what actions off looks like in History." are missing; the Actions list does not say "Both are still configured. None fires while actions are off." |
| Cw-18-Delete | differs; **the design was wrong** (history on delete: corrected) | Both are a styled confirm. The page's body is one line, "Its history goes with it.", and the buttons are **Cancel** and **Delete**; the board has a body of three sentences with the history count, the buttons **Keep it** and **Delete alarm**, and a note under the dialog. "Its history goes with it" and the board's "removed from this machine ... 4 history entries" were both false for AWS (CW-26): the page sentence is a finding, the board is corrected. |
| Cw-19-No-Notification | cannot match yet | Waits on CW-10 (a failed delivery recorded in History, so the explainer has a row to point at) and on the Denied callout, which reads the `AlarmAction` row on the wire. The neighbour strip ("watches λ harbour-order-validator ... on ALARM notifies SN harbour-ops-alerts") and the wire tail ("calls that touched this alarm") do not exist on the page; the page is the flat History table (a missing Action row on the checkout alarm confirmed). |
| Cw-20-No-Data | differs | The page is the Metric tab with the empty chart; the board adds the header neighbour line (`AWS/SQS · … · Maximum > 0 · 1 × 60 s`), "Why this alarm stays INSUFFICIENT_DATA", "SQS publishes no metrics here", and "To alarm on a backlog locally". All are built from the producer table and the ledger, so this waits on nothing. |

**Where the design was wrong** (corrected in the working copy on 2026-10-11 in
Cw-04, Cw-05, Cw-06, Cw-12, Cw-14 and Cw-18; the published canvas still carries
the old text until it is republished)

- The alarm `Period` set "1 · 5 · 10 · 20 · 30 · n × 60" in Cw-04 and Cw-05, and
  "1, 5, 10, 20, 30" in Cw-06's message and field note: 1 and 5 are not valid
  for an alarm (CW-27, model: "Valid values are 10, 20, 30, and any multiple of
  60"). Now "10 · 20 · 30 · n × 60" and "The parameter Period must be 10, 20,
  30, or a multiple of 60." (the code's message follows in CW-27).
- The ceiling "Period × this ≤ 86 400" in Cw-04 and Cw-05, and Cw-06's example
  "3 600 s × 25 periods ... A day of periods is the most an alarm may look back
  over": AWS allows 604 800 s from a 3 600 s period up (CW-28). Now "≤ 86 400
  under an hour, 604 800 from an hour up", and the example is "60 s × 1 441
  periods", 86 460 s.
- "History goes with the alarm when it is deleted" (Cw-12) and "its 4 history
  entries are removed from this machine" (Cw-18): the model says history is
  retained (CW-26). Now "History is kept when the alarm is deleted:
  DescribeAlarmHistory with its name still returns it." and "Its 4 history
  entries stay".
- "Setting the state the alarm already has still counts as a change and runs the
  actions again" (Cw-14): the model invokes actions only "when the updated state
  differs from the previous value" (CW-8). Now "runs no actions: AWS invokes
  them only when the new state differs from the previous one".
- Left as they are, listed in "not yet backed": the Subject line's region
  spelling (Cw-14), "95 % of a period's observations" (Cw-05, Cw-14).

**Build list, in order**

1. API prerequisites a board needs, in this order: CW-1 (the window fact), CW-3
   (the statistic list), CW-11 (`HistoryData` carried), CW-26 (history kept: the
   confirm's sentence and the count), CW-8 and CW-10 (Cw-14, 15, 19), CW-7 (the
   replace callout reads what a re-put changes), CW-25 (`StateTransitionedTimestamp`
   for "since …").
2. Teaching lines for the create form and the Settings table from one settings
   record (the kit's `setting_field`): line 4 from the regenerated
   `constraintTables` (CW-37), the period set and ceiling (CW-27, CW-28) and the
   ledger's enforced or stored. Ten settings: name, description, metric,
   statistic, operator, threshold, period, evaluation periods, datapoints,
   missing data; and the two topics.
3. Create (Cw-04, 05, 06): two columns, Datapoints to alarm, On OK topic,
   **How this alarm decides**, the neighbour check ("data in N of the last M
   periods"), the subscription note, the replace callout, the operator labels,
   and the refusal above the form with the field-level line.
4. Alarm page (Cw-09, 10, 17): two columns, "Why it is in …", the Settings table
   with sentences, the actions note, the segmented state control, "What it
   does"; "What happens next" for a fresh alarm; the actions-off excerpt.
5. Set state (Cw-14, 15, 16): "What pressing it does", the payload preview
   (the payload shape pinned by a test, CW-48's region spelling decided first),
   "What it did", the refusal's line 4.
6. History (Cw-12): the expandable "Why it moved" row, the side column.
7. Metric (Cw-07, 08, 11): the right column, point-time labels, the window note,
   the threshold line and breaching points, "All metrics", "Create an alarm on
   it" as a button.
8. Delete (Cw-18): the body with the count, the verbs.
9. Home and empty states (Cw-Home, 01, 02, 03): the Alarms column, the SQS/SNS
   note, per-row **Create alarm**, `empty_detail` for the empty case (A7),
   the "Who publishes one" note, the operator glyphs in the tables.
10. Bad days (Cw-19, 20): Cw-20 first (nothing blocks it); Cw-19 after CW-10 and
    the denied callout.
11. Re-render all 21 against the corrected boards, fill the verdict column
    again, and look at dark and 900 wide for the create and alarm pages.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Wire (all three) | JSON timestamps are numbers, and a JavaScript SDK spec reads `DescribeAlarms`, history, `ListDashboards`, statistics and data (CW-22); Query returns `ExtendedStatistics` (CW-23); the code for a constraint violation per wire (CW-24); a gzipped Query body (CW-53); every operation posted on Query (CW-49); one stub per wire, 501 and the header (CW-15) |
| Store, handler | `Values`/`Counts` (CW-16); `EntityMetricData` (CW-17); dropped alarm members (CW-18, CW-32); six actions and every other list size (CW-19, CW-37); `tm99` message (CW-20); timestamp bounds and value range (CW-21, CW-36); `DescribeAlarmsForMetric` exact set and its other filters (CW-4, CW-30); the alarm period set and ceiling (CW-27, CW-28); Statistics xor ExtendedStatistics (CW-29); tags ignored on re-put (CW-31); mixed units (CW-38); percentile availability (CW-43); text rules (CW-41) |
| Evaluate | Reason sentences (CW-5); the hand-set hold, clocked (CW-9); same-state set (CW-8); failed delivery leaves a history row (CW-10); `StateTransitionedTimestamp` (CW-25); long windows fit the model's lengths (CW-50) |
| SDK v2 (CBOR), SDK v1 (Query), raw JSON, JavaScript v3 | Paginators for alarms, history, dashboards, metric data (CW-13, CW-42); dashboard error names per wire (CW-14); alarm round trip of every member; history after delete (CW-26); `ResourceNotFoundException` on the tag operations (CW-35); `InvalidFormat` (CW-39) |
| Rejection parity | Every hand rule in section 5 on all three wires, asserting the code; `cases_cloudwatch.json` regenerated for the list sizes |
| Restart | Alarm with state, manual flag, history (also after the alarm's deletion), tags; dashboard; samples after a clean stop |
| Second identity and region | Alarm ARN, dashboard ARN with no region (CW-34), payload account and region; a dashboard visible from every region |
| Ledger | Every `notHere` name appears in `SUPPORT.md` and `not-built.md` (CW-12); the three false sentences are gone (CW-51) |
| Console handler | Window fact (CW-1); `summarise` (CW-2); Metric tab selects (CW-3); form coercion (CW-6); re-create over an existing alarm (CW-7); `HistoryData` carried (CW-11); `cwStats` within `parseStat` |
| Conformance (boto3) | The scenarios in section 8 |
| Browser | Each `new` item; each refusal on Cw-06 and Cw-16; the delete confirm's sentence; actions off then a state change with no Action row; the fidelity dialog (A9); keyboard paths on the alarm list and create form; the screenshots in 6a taken again, in light, dark and 900 wide |
| Mutation | `cloudwatch/` baseline, then survivors in `evaluate.go`, `aggregate.go`, `actions_alarm.go` |

## 8. Recording queue

Every row is `doc`, `model` (where the model text is the only source and a
recording would settle the wording of the error), or `memory` today. Scenario
names are in `conformance/tests/test_cloudwatch.py`. *Corrected 2026-10-11:
source column re-read against the model.*

| Claim | Source now | Scenario |
|---|---|---|
| Period must be 10, 20, 30 or a multiple of 60 for an alarm, and its error | model (*was doc, with 1 and 5*) | `period_granularity` |
| Period × EvaluationPeriods above 604 800 (86 400 under an hour) is refused, and its error | model (*was doc, 86 400*) | `evaluation_window_ceiling` |
| `GetMetricStatistics` over 1 440 datapoints is refused, and its code (`InvalidParameterCombination` or `InvalidParameterValue`) | model for the limit, memory for the code | `statistics_datapoint_ceiling` |
| `DescribeAlarmsForMetric` matches the exact dimension set | model (*was doc*) | `alarms_for_metric_exact_dimensions` |
| `SetAlarmState` to the current state fires nothing, and whether it writes a history row | model for the actions, not established for the row (*was memory*) | `set_state_same_state` |
| A hand-set state returns "quickly, often within seconds" | model (*was memory, "about a minute"*) | `set_state_reverts` |
| A failed action writes a "Failed to execute action" history item | memory | `failed_action_history` |
| The reason sentences for ALARM, OK and INSUFFICIENT_DATA, and `StateReasonData` keys (incl. `evaluatedDatapoints` as a list of objects) | memory | `state_reason_format` |
| History summaries: "Alarm updated from X to Y", "Successfully executed action …", creation and update | memory | `history_summaries` |
| A new alarm's first reason is "Unchecked: Initial alarm creation"; whether INSUFFICIENT_DATA actions run at creation | memory | `initial_state_reason` |
| Five actions per state at most, and its error | model for the limit, memory for the code (*was memory*) | `action_count_limit` |
| An alarm naming a topic that does not exist is accepted | memory | `alarm_missing_topic` |
| Re-putting an alarm resets `ActionsEnabled` to true when omitted; keeps state; ignores `Tags` | model (*was memory*) | `reput_keeps_state` |
| Timestamp bounds on `PutMetricData`, and the code | model for the bounds, memory for the code (*was memory*) | `put_metric_data_timestamp_bounds` |
| `Values`/`Counts` semantics and limits (150, equal lengths, default count 1) | model (*was doc*) | `put_metric_data_values_counts` |
| Dimension count (30) and data-per-call (1 000) limits, the 1 MB size before or after gzip | model, size memory (*was memory*) | `put_metric_data_limits` |
| `EvaluateLowSampleCountPercentile` values (`evaluate`, `ignore`); `EvaluationInterval` refused on a MetricName alarm | model (*was not established*) | `alarm_optional_members` |
| `tm`, `wm`, `tc`, `ts`, `iqm`, `pr` are accepted as `ExtendedStatistic` | model (*was doc*) | `extended_statistic_forms` |
| A percentile is "the value below which N % of the data falls" | memory | n/a (wording; cite the definitions page) |
| The alarm notification payload and Subject, key for key; the region's spelling; the percentile `Trigger` shape; the event a Lambda action receives | memory (*was doc*: the page behind it was not read) | `alarm_notification_payload` |
| `DescribeAlarmHistory` date bounds, default page size and paging | memory | `history_paging` |
| `DeleteAlarms` with some names missing (the rest are deleted; whether the call errors) | model for the first half (*was memory*), memory for the error | `delete_partial` |
| `DeleteDashboards` with a missing name: the rest are deleted, and what it answers | model for the first half, memory for the code (*was "atomic", memory*) | `delete_dashboards_partial` |
| Dashboard errors: names on each protocol, and valid JSON that is not a dashboard; the body's rules | model for the names, doc (page not read) for the body (*was model and memory*) | `dashboard_errors` |
| A dashboard's ARN has no region, and a dashboard is visible from every region | model | `dashboard_global` |
| The code a real unsupported call answers with, per protocol | n/a: decided (D5) | `unsupported_operation_shape` |
| Tag limits on an alarm (50), `aws:` keys, a foreign ARN, and the code for a missing resource | model for 50 and the code name, memory for `aws:` and foreign ARNs (*was memory*) | `alarm_tag_limits` |
| A client publishing to `AWS/` is refused, and its message | memory (*was doc*; the model says "should not") | `aws_namespace_reserved` |
| The alarm's evaluation window: sliding or aligned, the lag, how many seconds before a datapoint is seen | memory | `alarm_window_alignment` |
| A series with no data for two weeks is not listed; a metric takes up to 15 minutes to appear | model for both, no recording of the actual delay | `list_metrics_expiry`, `list_metrics_lag` |
| A percentile over a negative value or a statistic set is not available, and what the datapoint carries | model for the rule, memory for the shape | `percentile_availability` |
| Mixed units in one metric are returned as separate datapoints | model | `mixed_units` |
| `SetAlarmState` with `StateReasonData` that is not JSON is `InvalidFormat` | model for the code name | `set_state_invalid_format` |
| The code for a constraint violation on Query (`ValidationError`) | model | `validation_error_code` |
| History is retained after `DeleteAlarms`, and for how long | model for the retention, memory for the window | `history_after_delete` |

## 9. Owed to integration

| What | With |
|---|---|
| A real invocation raises Invocations, Duration, Errors and Throttles, dimensioned and not; an alarm on Errors reaches ALARM through the real evaluator | Lambda |
| A served request raises Count, 4XXError, 5XXError, Latency (and the HTTP API names) | API Gateway |
| An execution raises the `AWS/States` counts and ExecutionTime | Step Functions |
| A metric filter and an EMF line each become a metric an alarm can watch | Logs, Lambda |
| An alarm's Publish reaches a subscribed queue with AWS's payload | SNS, SQS |
| An alarm invoking a function: the event the function receives (CW-48), and the version or alias in the action's ARN (CW-45) | Lambda |
| Under enforce, the topic policy must name `cloudwatch.amazonaws.com`, with `aws:SourceArn` the alarm's ARN; the function permission likewise. A denial shows as denied, not 500 (A10; already row "CloudWatch" in [../90-integration.md](../90-integration.md)). | SNS, Lambda, IAM |
| A deleted topic or function: what the alarm then does, and what History says (CW-10) | SNS, Lambda |
| A template with an alarm, a dashboard and a metric filter, through CloudFormation, SAM, CDK and Terraform, reading back with no drift (CW-18; the CloudFormation mapper drops `EvaluateLowSampleCountPercentile`, and `DashboardArn` as `GetAtt` carries no region, CW-34) | CloudFormation |
| `docs/cloudformation.md` lists `AWS::CloudWatch::*` as skipped; it is mapped | CloudFormation |
| ~~The JSON encoder's timestamps in the other JSON-wire services~~ checked 2026-10-11: the other services send numbers; CW-22 is CloudWatch's alone | – |
| The evaluator under many alarms, and a restart mid-transition | soak |
| SQS, SNS, DynamoDB and Kinesis publish nothing: a test per service that no `AWS/<service>` series appears after traffic | each |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] CW-1 to CW-53 each closed, with the test that failed first named beside it.
- [ ] `SUPPORT.md` says 55 operations, 19 and 36, names every refused
      operation, and `not-built.md` reconciles with `notHere` by test (CW-12);
      its three false sentences are gone (CW-51).
- [ ] Decided and written into `SUPPORT.md`: the stub answer (CW-15, D5);
      `EntityMetricData` (CW-17); `EvaluateLowSampleCountPercentile` (C) and
      `EvaluationInterval` (refused) (CW-18); the non-percentile extended
      statistics (CW-20); whether an action target must exist; how long a
      hand-set state lasts (CW-9); what a crash loses of the samples; the
      alarm window, once recorded (CW-46); the region's spelling in the payload
      (CW-48); dashboards global or regional (CW-34).
- [ ] Neither per-member table in section 2 has "not established", "dropped"
      or "refused" without a tier beside it.
- [ ] Resolved, each by a test or a ledger line: the dimension-count,
      data-per-call, `Values` and request-size limits (CW-37, CW-41); duplicate
      dimension names; the tag limit and `aws:` prefix; `StateReasonData` as
      JSON (CW-39); history date bounds; `DeleteAlarms` over 100 names; a
      non-dashboard JSON body (CW-47); whether an emptied series leaves
      `ListMetrics` (CW-44); the percentile `Trigger` shape.
- [ ] Every box in section 5 ticked on all three wires, or moved to the
      recording queue with its test written and skipped.
- [ ] **The fidelity table (6a) exists, was made from rendered boards beside
      rendered pages, and no row says "differs" without a decision beside it**
      (today: 20 differ in shape and one in copy, none decided; the build list is the decision for each,
      and a deliberate deviation is written there with its reason).
- [ ] **Icons kept**: the rail, the list pane head and the resource header on
      every CloudWatch page draw the AWS CloudWatch icon, never the board's
      two-letter mark.
- [ ] **The five teaching lines on every setting** of the create form and the
      Settings table (name, description, metric, statistic, operator,
      threshold, period, evaluation periods, datapoints, missing data, the two
      topics): name and unit, the value in words, one sentence, range and
      default and **enforced or stored**, the neighbour check with real
      numbers; line 4 written from the settings record, not typed.
- [ ] **Every wire, per operation**: all 19 operations posted on Query, JSON and
      CBOR with the reference's spellings, accepted (CW-49); timestamps, maps
      and compression right on each (CW-22, CW-23, CW-53); the error code per
      wire asserted for every refusal (CW-14, CW-24, CW-35, CW-39).
- [ ] **Each asynchronous operation decided** (L7, table in section 12): alarm
      creation and evaluation, the notification, `SetAlarmState`'s return,
      `ListMetrics`' fifteen minutes, the delay on old timestamps, history
      retention. Each is modelled with a visible state or immediate with the
      difference listed in `SUPPORT.md`.
- [ ] **Each time- and consistency-based rule decided** (L8, table in section
      12), with a switch where one is wanted, off by default and in memory.
- [ ] Pagination: `DescribeAlarms`, `DescribeAlarmHistory`, `ListDashboards`,
      `GetMetricData` and `ListMetrics` each driven by an SDK paginator to the
      last page, and a stale or foreign token refused (CW-13, CW-42, CW-44).
- [ ] Dashboard and alarm errors carry the modern name on CBOR and JSON and the
      legacy name on Query, asserted per wire.
- [ ] No producer for SQS, SNS, DynamoDB or Kinesis was added, and the
      "Differences from AWS" line still says why.
- [ ] A restart test covers alarms, state, history (including after deletion),
      dashboards and tags.
- [ ] All 21 boards implemented from the foundation's shell with no
      CloudWatch-only chrome, and each `new` item reads only the data named for it.
- [ ] No statement on a CloudWatch screen is in the "not yet backed" list in
      section 6.
- [ ] The create form sends exactly what it shows: Period and Evaluation
      periods are never coerced (CW-6), and creating over an existing name says
      so first and loses nothing the form does not show (CW-7).
- [ ] The alarm page shows a failed notification where it shows a delivered one.
- [ ] The delete confirm says only what the code does (history stays, CW-26).
- [ ] `e2e/tests/cloudwatch.spec.ts` covers all eight routes under `/cw` with
      the route gate on, and the accessibility sweep is clean on Home, a
      metric, all four alarm tabs and create.
- [ ] A JavaScript SDK spec reads every response type that carries a timestamp
      (CW-22).
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____ (3 today).
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] `task lightness` unchanged: the evaluator and sweeper tickers are the
      service's idle cost and no fix here adds a third.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files | Days |
|---|---|---|---|
| 1 | Contract: operation count, the eight unnamed stubs, member tiers, decisions; the three false ledger sentences | `docs/SUPPORT.md`, `docs/not-built.md` | 0.5 |
| 2 | Red tests for CW-1 to CW-53 | `cloudwatch/*_test.go`, `internal/console/cw_test.go`, `docs/` ledger test, `e2e/` JavaScript spec | 2 |
| 3 | Wires: JSON timestamps (CloudWatch's own response types; the other JSON services were checked and are right), Query `ExtendedStatistics`, `ValidationError`, gzip on Query, 501 stubs, the all-operations Query test | `cloudwatch/responses.go`, `codec.go`, `errors.go`, `cloudwatch.go` | 1.5 |
| 4 | Tables: regenerate `cases_cloudwatch.json`, list sizes, text rules, value range, the alarm period set and ceilings, `Statistics` xor | `cloudwatch/validate.go`, `testdata/cases_cloudwatch.json`, `actions.go`, `actions_metrics.go`, `actions_alarm.go` | 1.5 |
| 5 | Publishing: `Values`/`Counts` with percentile expansion, `EntityMetricData`, timestamp bounds | `cloudwatch/actions.go`, `samples.go`, `aggregate.go` | 1.5 |
| 6 | Reading: units kept apart, percentile availability, `GetMetricData` ids and paging, `ListMetrics` expiry and tokens | `cloudwatch/aggregate.go`, `actions_metrics.go`, `actions.go`, `store.go`, `samples.go` | 1.5 |
| 7 | Alarm definition and behaviour: dropped and refused members, tags on re-put, exact-dimension match and its filters, reason sentences, same-state set, the hold, failed-action history, history kept after delete, `StateTransitionedTimestamp`, Lambda qualifier | `cloudwatch/actions_alarm.go`, `alarms.go`, `responses.go`, `evaluate.go`, `actions_fire.go`, `internal/peercall` | 2.5 |
| 8 | Pagination of four operations and tokens; dashboards: ARN, global, names, best-effort delete, errors per wire | `cloudwatch/actions_alarm.go`, `actions_dashboards.go`, `dashboards.go`, `errors.go` | 1.5 |
| 9 | Section 5 sweep, parity cases, restart, second identity; conformance scenarios and claims table | `cloudwatch/rejection_parity_test.go`, `threewire_test.go`, `sdk_test.go`, `sdkv1_test.go`, `conformance/tests/test_cloudwatch.py`, the claims file from foundation B1 | 2 |
| 10 | Console fixes CW-1, 2, 3, 6, 7, 11 | `internal/console/handlers_cw.go`, `client_cw.go`, `templates/cw.html`, `templates/create.html` (`cw_create`) | 1 |
| 11 | Console boards, in the build-list order of 6a | the same four, `client_flow.go` (topic subscriptions), `client_tags.go`, `routes_compute.go`, `static/app.css` | 6 |
| 12 | Browser tests, the sweep, fidelity re-render; ledger, changelog, status | `e2e/tests/cloudwatch.spec.ts`, `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` | 1.5 |

Steps 1 to 9 are stage 12a, steps 10 to 12 are 12b. The column adds to 23 days
for one person who has read this page; the red tests, the regenerated tables and
the section 5 sweep overlap by about three, so the figure in section 1 is
**about 20** (12a about twelve, 12b about eight). SQS's lesson is that a figure
like this is a floor until the stage's own reading is done; this one was made
after the reading. Several items wait on a decision (CW-15's retry check,
CW-17, CW-46, CW-48) and on recordings (CW-8's history row, CW-10, CW-21's
code), which end the stage as skipped tests.

*Corrected 2026-10-11: was "M was set before CW-13 to CW-21 were known. Steps 3
to 6 are now about two days on their own ... Either re-size to L, or keep M by
moving the dashboards half of CW-13 and CW-14 behind the alarms work."* That
advice was not enough: re-size, and split.

**For the foundation.**

- A7 lists CloudWatch among services that "open the first resource whenever one
  exists". It does not: `cw_home` always renders the alarms table and the
  metric browser and never calls `empty_detail`, so `copy.go`'s `None` and
  `Unsel` lines for `cw` are unreachable whether or not alarms exist. The rule
  A7 picks needs a third case: a service whose home is a page of its own.
  (*A7 is marked landed in the foundation's table with the CloudWatch `None`
  line removed; Cw-01 still draws the empty-detail paragraph, so the build list
  item 9 decides it again.*)
- A2's "Not implemented" is decided by the header (D5). CloudWatch's stubs
  answer 400 `UnsupportedOperationException` with none (CW-15): CloudWatch
  moves.
- Foundation C names seven wiring-graph node kinds. An alarm is not one, so the
  neighbours strip on an alarm page comes from the alarm's own action lists,
  not from the graph.
- **The JSON encoder (CW-22).** `internal/awsjson` writes `time.Time` through
  `encoding/json`, which is an RFC 3339 string. Any service whose JSON response
  has a timestamp member and is read by a strict SDK (JavaScript v3, Java, .NET)
  fails the same way. KMS, SSM, Secrets Manager and EventBridge are on that
  encoder or have their own; each stage should check its timestamps against the
  SDK that reads them. This is an item for the foundation's list.
- **The generator (B8).** `dzaudit list` and the committed tables disagree for
  CloudWatch on 18 constraints; "183 of 183" in the ledger cannot be
  reproduced. The drift job prints counts and never fails on them; for this
  service it should have.

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
  undocumented duration rather than wrong behaviour. *Corrected 2026-10-11: the
  model says a hand-set metric alarm returns "quickly, often within seconds",
  so ten to twenty seconds is not even undocumented; CW-9 is a comment and a
  ledger line.*

**Prior art.** Only what is known first-hand from this repo: the comment on
`seriesKey` in `cloudwatch/store.go` records that moto keys a metric without
its dimension set, which is the failure CW-4 reintroduces on the read side.
AWS's alarm-evaluation and statistics-definitions pages in the CloudWatch user
guide are the `doc` source for sections 5 and 8, and each hand rule's test
names the page (none was read in this review; the model's text was).

## 12. Pre-stage review (2026-10-11)

Answers L1 to L12 of [../02-lessons-from-sqs.md](../02-lessons-from-sqs.md). The
model is `.audit-models/cloudwatch.json` (55 operations, 612 KB). **Read:** the
documentation of every operation and of every member of the input and output
shapes of the 19 dispatched operations (and, for the S rows, enough to name
what each is: composite alarms, anomaly detectors, Contributor Insights,
metric streams, mute rules, datasets, OTel enrichment, resource metrics
configuration, log alarms); every error shape with its `awsQueryError` code and
status; every operation's `errors` list; the enums and the scalar traits
(`dzaudit list cloudwatch`); the traits for `xmlName`, `xmlFlattened`,
`timestampFormat` and `requestCompression`. **Run:** the three wires by hand
(about 120 requests), the AWS CLI v1 (1.46.1), the JavaScript SDK v3 (3.1129.0),
and all 21 boards beside the real pages.

| | Answer |
|---|---|
| **L1** the model's own text | **Findings:** CW-26, CW-27, CW-28, CW-29, CW-30, CW-31, CW-32, CW-33, CW-34, CW-35, CW-36, CW-37, CW-38, CW-39, CW-40, CW-41, CW-42, CW-43, CW-44, CW-45, CW-47, CW-50, CW-51, CW-53. **Claims corrected in this document:** 46 places carry *Corrected 2026-10-11*, among them: the alarm period set; the evaluation ceiling; five actions (source `memory` to `model`); the timestamp window (`memory` to `model`); SetAlarmState's "differs" rule; the hold's duration ("about a minute" was wrong); the retention schedule in the ledger (5-minute for 63 days, hour for 455); the 1 440 ceiling's source; `EvaluateLowSampleCountPercentile` and `EvaluationInterval`; the extended statistic forms; `StorageResolution`; the tag limit; `ActionsEnabled` on re-put; "deleting an alarm removes its history"; "DeleteDashboards is atomic"; "all three wires gunzip"; "a series with no samples leaves ListMetrics"; the dashboard error columns (CW-14); the `ListDashboards` page size; `ValidationError` on Query; 183 of 183; "statistics and percentiles in one call"; `DescribeAlarmsForMetric` dimension rule; two-weeks, 15-minute and 3-hour rules. **Not establishable from the model, kept `memory`:** the alarm quota (the model names `LimitExceeded`, "The quota for alarms for this customer has already been reached", and no number), whether `DeleteAlarms` answers an error for a missing name, the history item text and the failed-action item, the initial reason string, `StateReasonData`'s keys, the `AWS/` refusal's wording (the model says "should not"), the percentile definition, the default `MaxRecords`, the `aws:` tag prefix, how a sliding window rounds. |
| **L2** optional parameters | **Findings:** CW-29 (`Statistics` or `ExtendedStatistics`), CW-30 (`DescribeAlarmsForMetric` filters), CW-31 (`Tags` ignored on update), CW-32 (`EvaluationInterval`, `EvaluationWindow`, `EvaluationCriteria`, `EvaluateLowSampleCountPercentile`), CW-40 (`DescribeAlarms` combinations), CW-42 (`GetMetricData` `MaxDatapoints` and ids), CW-41 (`StorageResolution` default and values), CW-4. **Defaults read and held:** `ActionsEnabled` TRUE, `TreatMissingData` `missing`, `ScanBy` `TimestampDescending`, `ReturnData` true, `Counts` 1, `StorageResolution` 60, `RecentlyActive` only `PT3H`, `AlarmTypes` omitted means metric alarms only (the code does this), `AlarmName` omitted on history means all alarms (the code does this), `Unit` omitted means every unit (CW-38), `DatapointsToAlarm` "used only if you are setting an M out of N alarm". |
| **L3** error codes | **Findings:** CW-14 (dashboard shapes), CW-15 (stubs), CW-24 (`ValidationError`), CW-35 (`ResourceNotFoundException`), CW-39 (`InvalidFormatFault`), CW-33 (no not-found shape on `DeleteDashboards`), CW-44 (the model does not list `InvalidNextToken` for `ListMetrics`: the token's code is `memory`). **Held:** `SetAlarmState` and `DeleteAlarms` `ResourceNotFound` 404; `InvalidParameterCombination` for `Value` with `StatisticValues` and `Statistic` with `ExtendedStatistic` (both in the operation's list; the exclusivity text itself is `memory`); `LimitExceeded` is never raised (quota not in the model: a listed difference). Statuses: 400 for every validation shape, 404 `ResourceNotFound` and `ResourceNotFoundException`, 409 `ResourceConflict` and `ConflictException`, 429 `ConcurrentModificationException`, 500 `InternalServiceError` — all in `queryCodes`, which spells the shape `ResourceConflict` as `ResourceConflictException` (nothing raises it; correct it with CW-24). |
| **L4** direction | Every row in section 4 names it. The 32 new rows, by direction: more forgiving 13 (CW-27, 29, 30, 32, 36, 37, 39, 40, 41, 42, 43, 44, 47); stricter 3 (CW-28, 33, 53); wrong code, shape, scope or data 10 (CW-22, 24, 26, 31, 34, 35, 38, 45, 48, 50); missing 2 (CW-23, 25); not established, plausibly later than AWS 1 (CW-46); test, ledger or comment only 3 (CW-49, 51, 52). Rows with two directions are listed under the first (CW-32, 35 and 47 are also wrong code or shape). Among the 21 earlier rows, CW-4, 8, 19 and 21 are more forgiving and CW-16, 17 and 18 are missing or dropped. |
| **L5** every wire | Declared: `awsJson1_0`, `awsQuery` with `awsQueryCompatible`, `rpcv2Cbor`; served: all three. **Findings:** CW-22 (JSON timestamps, P0), CW-23 (Query drops a map), CW-24 (Query validation code), CW-53 (Query gzip), CW-49 (no per-operation Query test), CW-14 (error names per wire), CW-15. **xml names:** checked, none flattened and none renamed: the model has no `xmlName` or `xmlFlattened` trait at all; lists are `<member>`, the one map is `<entry>`. **Request walk:** `modelcheck.FromQuery` is documented as keeping one element of each list, and `awsquery.Unflatten` feeds the handlers; a second invalid element is nevertheless refused on Query as on JSON (run: datum 2's `Unit`, dimension 2's name of 300 characters, tag 2's key of 200), so SQS-26's trap is not here. All 19 operations were accepted on Query with two dimensions, two data and the reference's spellings (run). |
| **L6** tokens | `NextToken` is the only token: `ListMetrics` (the store key, base64: a stale or foreign one answers an empty page, CW-44), `DescribeAlarms`, `DescribeAlarmHistory`, `ListDashboards`, `GetMetricData` (read by none: CW-13, CW-42). The model lists `InvalidNextToken` for `DescribeAlarms`, `DescribeAlarmHistory`, `GetMetricData`, `DescribeInsightRules` and others, not for `ListMetrics` or `ListDashboards`. No receipt handle, upload id or version id exists in this service. |
| **L7** asynchronous on AWS | Decided in the table below. Findings: CW-46 (the window), CW-9 (the hold), CW-10 (the delivery). |
| **L8** latency rules | Decided in the table below. |
| **L9** persistence | Alarms, their state, `StateSetManually`, history, dashboards, tags: persisted and synced (`putAlarm`). Samples and the series catalogue: persisted, `NoSync`, so a crash loses the tail (written into `SUPPORT.md` by this stage). **The process's, not persisted:** in-flight deliveries (a restart mid-notification drops it; AWS retries for a day, `memory`), the evaluator's tick position, the sweeper's. **Checked:** `StateSetManually` is persisted though it is a process-shaped hold (ten to twenty seconds): after a restart one pass is still swallowed; harmless, listed. No new state in the fixes needs persisting except the history after deletion (CW-26), which is the application's. |
| **L10** the board's shape | 6a: 20 differ in shape and Cw-13 in copy; six boards' facts were wrong and are corrected; one cannot match yet (Cw-19). |
| **L11** five teaching lines | Today the create form has line 1 on most fields and line 4's wording once (Missing data), and no line 4 marker ("enforced"/"stored") anywhere. The board has lines 1 to 4 on every field and line 5 once, under the Condition group ("has data in 0 of the last 60 one-minute periods"), and a value-in-words line on the period and the evaluation periods ("1 minute", "The last 1 minute"). Source of line 4 for each: 6a build list item 2. The one line that has to be computed live is line 5 ("has data in N of the last M periods"). |
| **L12** icons | Checked on 12 real pages: the AWS CloudWatch icon is in the rail, the list pane head and the resource header; the boards' "CW" is the placeholder. None needs a change. |

**L7. Asynchronous on AWS, decided**

| Operation or behaviour | On AWS | Here | Decision |
|---|---|---|---|
| `PutMetricAlarm` creation | "the alarm state is immediately set to INSUFFICIENT_DATA ... The alarm is then evaluated and its state is set appropriately. Any actions associated with the new state are then executed." | INSUFFICIENT_DATA with "Unchecked: Initial alarm creation", then the next ten-second pass | **Modelled, and visible** (Cw-10 "What happens next"). Keep. |
| Alarm evaluation | on AWS's schedule; the window is CW-46 | every ten seconds, over completed periods | **Modelled**; the difference listed; the window decided after recording. |
| The notification | asynchronous, retried | a goroutine, once | **Modelled as one attempt**; failure written to History (CW-10, `memory` until recorded). |
| `SetAlarmState`'s return | "quickly, often within seconds" | ten to twenty seconds | **Modelled**; the duration written in the ledger (CW-9). |
| A new metric in `ListMetrics` | "it can take up to fifteen minutes for the metric to appear" | immediate | **Immediate, difference listed**. A fifteen-minute wait in a test loop is the worse tool. |
| Old timestamps become readable | "24 hours ago or longer can take at least 48 hours ... between 3 and 24 hours ago can take as much as 2 hours" | immediate | **Immediate, difference listed** (and CW-21 stops what the window refuses). |
| Roll-up of old data | 1-minute data re-aggregated to 5 minutes after 15 days, to 1 hour after 63 | none; everything raw for 24 hours | **Listed difference** (retention is flat, CW-51's corrected schedule). |
| `PutDashboard`, `DeleteAlarms`, tags | synchronous | synchronous | n/a |
| Metric streams, anomaly training, Contributor Insights | asynchronous | refused (S) | n/a |

**L8. Time- and consistency-based rules**

| Rule | On AWS | Here | Switch |
|---|---|---|---|
| Metric appears in `ListMetrics` after up to 15 minutes | model | immediate | none proposed (see decision 5 below) |
| A series with no data for two weeks is not listed | model | never expires (CW-44) | none: implement; two weeks is not a wait |
| Data-point availability lag for old timestamps | model | immediate | none proposed |
| Sub-minute data kept 3 hours; 1-minute 15 days; 5-minute 63 days; 1-hour 455 days | model | 24 hours, flat, configurable | existing `[cloudwatch].retention` |
| `SetAlarmState` reverts in seconds | model | ten to twenty seconds | none |
| Alarm history is kept after delete | model (window `memory`) | not kept (CW-26) | none: implement |
| The alarm quota (`LimitExceeded`) | model names the error | never raised | none: the number is `memory` |

**Classes of mistake this service is most at risk of**

1. **A hand rule written as AWS's and tagged `doc` without a page.** Section 5's
   period set and ceiling, the retention schedule and "atomic, as on AWS" were
   all confidently wrong, and each was in the ledger or a code comment. The
   model's operation and member text corrects most of them; the rest need the
   recording.
2. **Three encoders, one of them shared.** CW-22 hid behind the CLI, boto3 and
   the console, which all read strings; CW-23 and CW-53 hid because nothing
   posted the Query wire for 17 operations. Any fix to a response shape needs
   all three wires and the JavaScript SDK.
3. **A generator that moved and a ledger that did not.** B8 added list and map
   sizes; the committed tables and "183 of 183" were not regenerated, so about a
   tenth of the constraints (18 of 168) are unenforced and the count reads full.
4. **Behaviour stated as AWS's in a board or a sentence.** Cw-12, Cw-14 and
   Cw-18 each described doze-aws's current behaviour as the cloud's. The
   boards are the spec, so a wrong board becomes a wrong page.
5. **Time semantics from memory.** The evaluation window, its lag and the hold
   are the part of the service a developer observes first and the part with the
   least text; they need the recordings before the console draws a strip that
   explains them.
6. **A silent drop is a worse failure than a refusal.** Four alarm members and
   one response member were accepted and dropped; Terraform and CloudFormation
   see drift. Each remaining S/C member should be refused or round-tripped.

**Decisions needed from a person**

1. **D5 for CloudWatch (CW-15):** 400 `UnsupportedOperationException` becomes
   501 `NotImplemented` with the header, on all three wires. The foundation's
   retry check (does any SDK's default retryer retry a 501?) is the gate; for
   CBOR and JSON the SDKs are the ones this repo tests.
2. **Where CW-22 is fixed.** *Settled 2026-10-11 by checking, not by choice:*
   the shared encoder is not at fault (KMS, Secrets Manager, DynamoDB, SSM and
   Step Functions send epoch numbers), so the fix is CloudWatch's own response
   types and touches no other service.
3. **`EntityMetricData` (CW-17):** store the metrics and ignore the entity (F),
   or refuse by name (S).
4. **The alarm window (CW-46):** keep the aligned window with its lag, or
   change to a sliding window, on the strength of a recording.
5. **A strict mode (L8):** whether a `--cloudwatch-strict` that makes
   `ListMetrics` wait is wanted. The review proposes none: unlike a queue's 60
   seconds, the wait is 15 minutes and nothing depends on it.
6. **The notification payload's region spelling (CW-48)**, once recorded.
7. **Splitting the stage in two (section 1):** 12a before 12b.
