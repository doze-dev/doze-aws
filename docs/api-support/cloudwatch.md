# CloudWatch — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

The slice of CloudWatch a developer needs to write an alarm and believe it:
metrics with their dimensions, statistics over real samples, and alarms that
actually evaluate and actually notify. The point is not to draw graphs — it is
that the alarm you would deploy can be tested before you deploy it.

Metrics arrive four ways, and only one of them is an SDK call:

| Producer | Namespace | What it publishes |
|---|---|---|
| [Lambda](lambda.md) | `AWS/Lambda` | Invocations, Errors, Throttles, Duration, by `FunctionName` and undimensioned |
| [API Gateway](apigateway.md) | `AWS/ApiGateway` | Count, 4XXError/5XXError, Latency by `ApiName`+`Stage` (`4xx`/`5xx` by `ApiId`+`Stage` on an HTTP API) |
| [Step Functions](stepfunctions.md) | `AWS/States` | ExecutionsStarted/Succeeded/Failed/TimedOut/Aborted and ExecutionTime, by `StateMachineArn` |
| an application | its own | `PutMetricData`, an [EMF](lambda.md) line a function prints, or a [log metric filter](logs.md) |

SQS, SNS, DynamoDB and Kinesis publish nothing, deliberately — see
*Differences from AWS*.

## Three wires, one service

CloudWatch is mid-migration off Query, and the protocol an SDK uses is fixed at
code-generation time with no negotiation. doze-aws serves all three:

| Wire | Who speaks it | Shape |
|---|---|---|
| **Smithy RPC v2 CBOR** | aws-sdk-go-v2, Java, Rust, Swift, Kotlin, C++, .NET v4 | `POST /service/GraniteServiceVersion20100801/operation/<Op>`, `Smithy-Protocol: rpc-v2-cbor` |
| **AWS JSON 1.0** | **the AWS CLI (v1 and v2)**, boto3, JavaScript v3, PHP, Ruby, PowerShell | `X-Amz-Target: GraniteServiceVersion20100801.<Op>` |
| **AWS Query** | any SDK pinned below the versions in AWS's protocol table | `Action=<Op>&Version=2010-08-01`, XML back |

Serving only Query would break the entire modern audience, and because the CLI
speaks JSON 1.0, `aws cloudwatch ...` would not work at all. The signing name
is `monitoring`; the IAM action prefix is `cloudwatch`; the JSON target prefix
and RPC v2 service id are both `GraniteServiceVersion20100801`. Those are four
different strings for one service.

`PutMetricData` requests are **gzipped** by aws-sdk-go-v2 (the model marks it
`smithy.api#requestCompression`, and only that operation), so every wire
gunzips before decoding.

Errors are `awsQueryCompatible`: on Query the `<Code>` is the legacy spelling
(`InvalidParameterValue`), while JSON and CBOR keep the modern name in `__type`
and carry the legacy code in the `x-amzn-query-error` header.

| Operation | Tier | Notes |
|---|---|---|
| PutMetricData | F | dimensions are part of a metric's identity, so two metrics with the same name and different dimensions stay distinct; `StatisticValues` and `StorageResolution` accepted; samples are kept raw, which is what makes percentiles exact |
| ListMetrics | F | every series, filterable by namespace, name and dimensions; paginated |
| GetMetricStatistics | F | Sum, Average, Minimum, Maximum, SampleCount and `pNN` percentiles over aligned periods; a period with no observations is omitted rather than reported as zero |
| GetMetricData | F | one `MetricStat` per query, `ScanBy`, `Label`; parallel `Timestamps`/`Values` as AWS returns them. Metric **math expressions** are refused — see below |
| PutMetricAlarm | F | M-of-N over completed periods with `TreatMissingData`; `Statistic` or `ExtendedStatistic`; `Tags`; actions limited to SNS topics and Lambda functions, because an action doze-aws cannot deliver would be an alarm that looks wired up and does nothing |
| DescribeAlarms | F | filters by name, prefix, state and `ActionPrefix`; `ChildrenOfAlarmName`, `ParentsOfAlarmName` and an `AlarmTypes` without `MetricAlarm` answer an empty list, which is the truthful answer when there are no composite alarms |
| DescribeAlarmsForMetric | F | which alarms watch one series — how the console says "this metric has an alarm on it" |
| DeleteAlarms | F | takes them all at once; the history goes with the alarm |
| SetAlarmState | F | fires the actions for the state you set, so an alarm's notification is testable before its metric ever breaches; the evaluator does not immediately undo a deliberate flip |
| DescribeAlarmHistory | F | configuration updates, state updates and actions, filterable by type and time, `ScanBy` in either direction |
| EnableAlarmActions / DisableAlarmActions | F | an alarm with actions off still changes state, it just notifies nothing |
| TagResource / UntagResource / ListTagsForResource | F | on alarms and dashboards; CloudWatch's tag shape is a **list** of `{Key, Value}`, not the map CloudWatch Logs takes |
| PutDashboard / GetDashboard / ListDashboards / DeleteDashboards | C | the body is stored **verbatim** and never interpreted, so a caller diffing what it wrote against what it reads back sees no change it did not make; nothing renders it locally. `DeleteDashboards` is atomic, as on AWS |
| Metric math (`Metrics` on an alarm, `Expression` on a query) | S | a single `MetricStat` is accepted, which is what CDK emits for a threshold alarm; an expression needs an evaluator doze-aws does not have |
| Composite alarms (PutCompositeAlarm, DescribeAlarmContributors) | S | evaluate a rule over other alarms' states; doze-aws evaluates metric alarms only |
| Anomaly detection (PutAnomalyDetector, DescribeAnomalyDetectors, DeleteAnomalyDetector, `ThresholdMetricId`, the anomaly comparison operators) | S | need a trained band to compare against |
| Alarm warm-up (`WarmUpConfiguration`) | S | refused by name rather than ignored: a warm-up suppresses an alarm while a resource settles, and evaluating anyway would fire an alarm the caller asked to be held back |
| Insight rules (Put/Delete/Describe/Enable/Disable, managed rules, GetInsightRuleReport) | S | Contributor Insights reads an account's log volume |
| Metric streams (Put/Get/Delete/List, Start/Stop) | S | stream metrics to a Firehose that does not exist locally |
| Alarm mute rules (Put/Get/List/Delete) | S | suppress notifications on a schedule; not built |
| Datasets and OTel enrichment (GetDataset, Associate/DisassociateDatasetKmsKey), PutLogAlarm, GetMetricWidgetImage | S | need services or a renderer that do not run locally |

Every one of the 50 operations in the `GraniteServiceVersion20100801` model is
either handled (19) or refused by name with what it would need (31). Nothing
falls through to `InvalidAction`.

## Alarms

An alarm examines the last `EvaluationPeriods` periods and alarms when
`DatapointsToAlarm` of them breach — AWS's "M out of N", defaulting to N. The
evaluator runs every ten seconds.

**The window is lagged by one period, on purpose.** The period containing
"now" is still being written to, and judging a partial period produces an alarm
that flaps as observations arrive. AWS lags for the same reason. A freshly
created alarm therefore reads `INSUFFICIENT_DATA` until one period has closed.

**A missing period is a fourth answer, not a zero.** A period with no
observations is not a period that observed zero, so each is resolved by
`TreatMissingData` before the M-of-N count: `missing` (the default — neither
breaches nor clears), `notBreaching`, `breaching`, or `ignore` (keep the
current state). This matters more than it looks: with `notBreaching`, an empty
window resolves to a datapoint that did **not** breach, so an alarm over a
quiet metric settles on `OK` rather than staying at `INSUFFICIENT_DATA`.

A transition writes history and fires the actions for the state entered. An
SNS topic receives AWS's own alarm JSON — `AlarmName`, `NewStateValue`,
`OldStateValue`, `NewStateReason`, `StateChangeTime`, `Trigger` — so a
subscriber written against the cloud parses it unchanged.

## Differences from AWS

- **Retention is flat.** AWS keeps 1-second data for 3 hours, 1-minute for 15
  days and 1-hour for 63 days. doze-aws keeps everything for 24 hours
  (`[cloudwatch].retention`) and caps the sample count, because a local store
  is disposable and resolution-dependent expiry would only be a way to lose
  data mid-test.
- **Percentiles are exact**, computed from retained raw samples rather than
  from AWS's approximating sketch. A pNN here and a pNN on AWS will not agree
  to the last decimal; the local one is the more accurate of the two.
- **SQS, SNS, DynamoDB and Kinesis publish no built-in metrics**, and that is
  a decision rather than an omission. Their metrics are all measurements of
  scale — `ApproximateNumberOfMessagesVisible`, `ConsumedReadCapacityUnits`,
  `IncomingRecords` — and locally those numbers are whatever your own test just
  put there. An alarm on one would fire on a fixture rather than on a
  condition, which is worse than having no datapoint: a silent metric reads as
  `INSUFFICIENT_DATA`, and an alarm that says so is telling the truth.
  Kinesis's `EnableEnhancedMonitoring` and DynamoDB's Contributor Insights are
  stored and echoed but enable nothing, and say so where they are defined.
- **The evaluator ticks every ten seconds** rather than on AWS's own schedule,
  so a transition is visible in seconds instead of minutes.
- **No cross-account or cross-region metrics.** There is one account and one
  region locally, so `AccountId` on a query is accepted and ignored.

## Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): the CBOR wire end to end — publish,
  list, chart, alarm, and the alarm's transition — through the SDK that
  actually speaks RPC v2.
- **aws-sdk-go v1** (`sdkv1_test.go`): the Query wire, including the
  `.member.N` flattening that made the list-dropping bug visible.
- **A raw JSON 1.0 test** (`threewire_test.go`) standing in for the AWS CLI,
  which is the wire most people's tooling uses.
- **Producers** (`producers_test.go`): a real Lambda invocation raises
  `AWS/Lambda` Invocations and Duration; an EMF line a function prints becomes
  a custom metric; a served API Gateway request raises Count and 4XXError; a
  workflow raises `AWS/States`; a log line matching a metric filter increments
  a metric; and a stack with CloudWatch disabled still runs all of them.
- **Alarms to SNS** (`alarms_test.go`): a queue subscribed to the alarm's topic
  receives AWS's alarm JSON.
- **CloudFormation** (`cloudformation/cloudwatch_apply_test.go`): a template
  with an alarm, a dashboard and a metric filter deploys, the alarm reaches
  `ALARM` through the real evaluator, and the lot round-trips through export →
  emit → transpile.
- **The console** (`console/cw_test.go`): the pane's own handlers, asserted on
  rendered content rather than status.

## Input validation

**183/183 model-derived constraints enforced across the 19 dispatched
operations, on each of the three wires, with `knownGaps` empty.** Generated
with `dzaudit cases cloudwatch`, committed to `testdata/cases_cloudwatch.json`,
and replayed in `rejection_parity_test.go` from a baseline the test first
proves the service accepts — because a request refused for the WRONG reason
looks exactly like a pass.

One table serves all three wires: the paths describe the JSON shape, CBOR
decodes to it natively, and Query is rebuilt into it by `modelcheck.FromQuery`.
`MetricData[].Dimensions[].Name` is the load-bearing case — it only resolves on
the Query wire because `FromQuery` keeps `.member.` containers as lists, which
is a bug this suite found.

Sixteen cases are declared **out of scope** rather than replayed: they reach
into metric math and alarm warm-up, which are refused wholesale. Replaying one
would be this suite's own trap in its most convincing form — the request *is*
refused, so a runner records the constraint as enforced when it was refused for
the feature and never checked at all.
