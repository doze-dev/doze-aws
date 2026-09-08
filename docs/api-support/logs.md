# CloudWatch Logs — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

The slice of CloudWatch Logs a developer reads: log groups, streams and
events, written by the services that write them on AWS and read by
`aws logs tail --follow`, `sam logs`, the SDKs and the console. Every
producer writes through the same wire an SDK uses (PutLogEvents), so the
services run in one process or several:

| Producer | Group | What a line is |
|---|---|---|
| [Lambda](lambda.md) | `/aws/lambda/<function>` | every line a function prints, stamped with the request id of the invocation that printed it, one stream per process named the way Lambda names them |
| [Step Functions](stepfunctions.md) | the machine's `loggingConfiguration` destination, or `/aws/vendedlogs/states/<machine>` for an Express machine with logging off | one history event in AWS's vended JSON record, filtered by level |
| [API Gateway](apigateway.md) | the stage's `accessLogSettings` destination; `API-Gateway-Execution-Logs_<apiId>/<stage>` | one access-log line per request in the stage's `$context` format; the execution narrative at `INFO` or `ERROR` |
| [EventBridge](eventbridge.md) | a rule target's log group | the shaped event |
| [SNS](sns.md) | `sns/us-east-1/000000000000/<topic>` and its `/Failure` group, when the topic's delivery status attributes are set | one delivery attempt in AWS's record shape |
| an application | any group it creates | whatever it puts |

Every line a service writes carries a `requestId` (a doze extension) that
FilterLogEvents can select on, so the console shows one invocation, one
execution or one request without a filter pattern.

The service speaks AWS JSON 1.1 under the `Logs_20140328` target and signs
as `logs`. Events are kept for a day by default (`[logs].retention`), or for
the group's `retentionInDays` when one is set, and capped at 100,000 per
group; the store does not fsync per batch, because logs are disposable and a
per-invocation fsync is the one thing that would make Invoke slow.

| Operation | Tier | Notes |
|---|---|---|
| CreateLogGroup / DeleteLogGroup | F | `ResourceAlreadyExistsException` on a repeat; delete takes the streams and events with it |
| DescribeLogGroups / ListLogGroups | F | prefix, pattern and identifier filters; `limit` and a name-keyed `nextToken` |
| PutRetentionPolicy / DeleteRetentionPolicy | F | only the values AWS accepts (1, 3, 5, 7, 14, 30, … 3653); the sweeper honours them |
| CreateLogStream / DeleteLogStream / DescribeLogStreams | F | prefix, `orderBy LastEventTime`, `descending`, paging; first/last event and ingestion times are real |
| PutLogEvents | F | a first put on a stream nobody created creates it, so a function's first line never bounces; accepts a per-event `requestId` (doze extension) |
| GetLogEvents | F | forward and backward tokens; the end-of-stream token repeats, which is what stops an SDK paginator |
| FilterLogEvents | F | interleaved across streams in time order, `startTime`/`endTime`, stream names or prefix, `filterPattern`, `nextToken` only while more remain, unique `eventId`s — the contract `aws logs tail` and `sam logs` poll; `requestId` (doze extension) selects one invocation |
| TagResource / UntagResource / ListTagsForResource, TagLogGroup / UntagLogGroup / ListTagsLogGroup | F | the current and the deprecated spellings; the ARN form takes the ARN without the `:*` suffix DescribeLogGroups reports, as on AWS |
| Logs Insights (StartQuery, GetQueryResults, query definitions, scheduled queries, lookup tables, log fields and records) | S | a query engine that does not exist locally; FilterLogEvents covers what a developer reads |
| StartLiveTail | S | an HTTP event stream to a tailer fleet; `aws logs tail --follow` polls FilterLogEvents, which works |
| Metric filters | S | publish to CloudWatch Metrics, which does not exist locally |
| Subscription filters, destinations | S | fan out from the logs pipeline to Kinesis, Firehose and Lambda; not built |
| Deliveries, delivery sources and destinations, configuration templates | S | vended logs from other services; not built |
| Export and import tasks | S | S3 batch jobs; not built |
| Anomaly detectors, anomalies | S | a trained model over an account's logs; not built |
| Account, data-protection, index, storage-tier, resource and deletion-protection policies, bearer tokens | S | govern an account, not a local store |
| Transformers, integrations, S3 Table sources, KMS association, syslog configurations | S | need pipelines or services that do not run locally |

Every one of the 118 operations in the `com.amazonaws.cloudwatchlogs` model
is either handled (18) or refused by name with what it would need (100).
Nothing falls through to `InvalidAction`.

## Filter patterns

The subset people type into `sam logs --filter` and `--filter-pattern`:

- `ERROR` — a term; `ERROR timeout` — every term must appear.
- `"out of memory"` — a phrase.
- `-DEBUG` — a term that must not appear; `?ERROR ?WARN` — any of.
- `{ $.level = "error" }`, `{ $.status >= 500 && $.path = "/x*" }`,
  `{ $.a = 1 || $.b IS NULL }` — JSON patterns over a message that parses as
  JSON, with `=`, `!=`, `<`, `<=`, `>`, `>=`, `IS NULL`, `NOT EXISTS`,
  `IS TRUE`, `IS FALSE`, `*` wildcards in strings, `&&` binding tighter than
  `||`, no parentheses.

Regular-expression (`%…%`) and space-delimited (`[…]`) patterns answer
`InvalidParameterException` naming the construct.

## Differences from AWS

- **Retention defaults to a day**, not never. Set `retentionInDays` on the
  group, or `[logs].retention` in the config, for longer.
- **One stream per process** rather than per execution environment; the
  console shows the request id on every line, which is the join a reader
  needs.
- **No sequence tokens.** `PutLogEvents` accepts and ignores
  `sequenceToken`, as AWS has since 2023.
- **`GetLogEvents` scans the group's time range and keeps its stream**; at
  local volumes that is a non-cost, and it keeps FilterLogEvents — the hot
  path — a single cursor range.

## Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): the CLI's own tail loop — FilterLogEvents
  polled with `startTime` advanced and deduplicated on `eventId` — receives
  every event exactly once across streams; GetLogEvents paginates and stops;
  the filter-pattern table; every typed error.
- **Lambda end to end** (`lambda/logs_sdk_test.go`): a function's stdout and
  stderr from a synchronous and an asynchronous invoke arrive under
  `/aws/lambda/<fn>` bracketed by START, END and REPORT; a stack started
  without the logs service still runs the function and echoes its output to
  the terminal.
- **CloudFormation** (`cloudformation/regression_test.go`): `AWS::Logs::LogGroup`
  maps to a real group with its retention, where it used to be ignored.

## Input validation

**167/167 model-derived constraints enforced across the 18 dispatched
operations, with `knownGaps` empty.** Generated with `dzaudit cases
cloudwatch-logs`, scoped to the dispatched operations, committed to
`testdata/cases_logs.json`, and replayed in `rejection_parity_test.go` from a
baseline the test first proves the service accepts. The tag operations' ARN
pattern has no `*`, which is how the audit found that the ARN
DescribeLogGroups reports (ending `:*`) is not the one TagResource takes.
