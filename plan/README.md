# The road to 1.0, one service at a time

> **A working document.** This folder is removed in the commit that tags 1.0.
> What it produces outlives it elsewhere: findings become tests, contracts
> become [`docs/SUPPORT.md`](../docs/SUPPORT.md) and
> [`docs/not-built.md`](../docs/not-built.md), behaviour changes go to the
> changelog, and the recording queues move into the conformance harness.

This folder is the plan. It exists so the work does not lose focus: what gets
done, in what order, and how we know each piece is finished.

The method is deliberately plain. A shared **foundation** first. Then each
service on its own, **API and console together**, to a fixed protocol with
written acceptance criteria. Then **integrated testing** across services. A
service is not started until the one before it has met its criteria, and nothing
is called done on the strength of somebody's memory of how AWS behaves.

## Why this shape

Grounding the console design against the code, and then writing each service's
contract, turned up **460 findings, 71 of them P0**: things that change what a
deploy or a running app sees. Three things stood out.

- **They cluster by service and cross the API/console line.** SNS's API can make
  a FIFO topic and the console cannot. S3 answers correctly and the console
  renders the answer as raw XML. Sweeping "the API" and then "the UI" separately
  is what let these sit side by side.
- **Writing the contract found more than looking at the screens did.** Every
  service document reports P0s its design work never saw. Listing each operation
  and asking "functional, cosmetic, or refused?" is what found them: 49
  Parameter Store operations answering a false `InvalidAction`, five API Gateway
  updates that are not routed at all, CloudFormation resource types that report
  CREATE_COMPLETE and make nothing.
- **Much of it rests on a claim about AWS nobody recorded.** The conformance
  harness can record real AWS and compare, but `conformance/snapshots/` is empty,
  so today every scenario is *unverified*. A plan that calls itself scientific
  has to keep "we believe" and "we recorded" apart, and this one does.

## The documents

| Document | What it is |
|---|---|
| [00-foundation.md](00-foundation.md) | Work every service depends on. Done first, once. |
| [01-protocol.md](01-protocol.md) | The seven steps every service goes through, the evidence standard, and the acceptance criteria common to all of them. |
| [services/](services/) | One document per service, all with the same eleven sections: contract, where it stands, findings, what to check, console, tests, recording queue, owed to integration, acceptance criteria, files. |
| [90-integration.md](90-integration.md) | What is tested only once every service is individually done. |
| [findings-overview.md](findings-overview.md) | The first cross-service list, from the design work. Superseded in detail by the service documents; kept for the story. |

## Order, and the numbers

Each row starts only when the row above has met its acceptance criteria.

| # | Stage | Operations | Findings | P0 | P1 | P2 | P3 | Boards | Days |
|---|---|---|---|---|---|---|---|---|---|
| 0 | [Foundation](00-foundation.md) | – | 18 shared console items, 12 shared machinery items | | | | | shell | 12 |
| 1 | [SQS](services/sqs.md) | 23 | 8 | 1 | 4 | 3 | 0 | 21 | 2 |
| 2 | [Parameter Store](services/ssm.md) | 152 | 24 | 4 | 12 | 5 | 3 | 16 | 3 |
| 3 | [KMS](services/kms.md) | 54 | 34 | 5 | 13 | 10 | 6 | 20 | 5 |
| 4 | [Secrets Manager](services/secretsmanager.md) | 23 | 38 | 2 | 17 | 13 | 6 | 20 | 4 |
| 5 | [Kinesis](services/kinesis.md) | 45 | 28 | 3 | 15 | 6 | 4 | 18 | 4 |
| 6 | [S3](services/s3.md) | 112 | 37 | 4 | 16 | 12 | 5 | 26 | 7 |
| 7 | [DynamoDB](services/dynamodb.md) | 62 | 31 | 4 | 13 | 10 | 4 | 20 | 6 |
| 8 | [SNS](services/sns.md) | 42 | 29 | 3 | 12 | 9 | 5 | 19 | 4 |
| 9 | [EventBridge](services/eventbridge.md) | 57 | 30 | 8 | 8 | 10 | 4 | 24 | 6 |
| 10 | [Lambda](services/lambda.md) | 88 | 35 | 11 | 14 | 6 | 4 | 24 | 8 |
| 11 | [CloudWatch Logs](services/logs.md) | 118 | 17 | 3 | 6 | 6 | 2 | 20 | 4 |
| 12 | [CloudWatch](services/cloudwatch.md) | 55 | 21 | 1 | 9 | 6 | 5 | 21 | 5 |
| 13 | [Step Functions](services/stepfunctions.md) | 37 | 21 | 4 | 6 | 7 | 4 | 26 | 6 |
| 14 | [API Gateway](services/apigateway.md) | 227 | 30 | 4 | 13 | 8 | 5 | 30 | 8 |
| 15 | [IAM and STS](services/iam.md) | 191 | 38 | 6 | 18 | 11 | 3 | 26 | 11 |
| 16 | [CloudFormation](services/cloudformation.md) | 90 | 39 | 8 | 18 | 10 | 3 | 21 | 11 |
| 17 | [Integration](90-integration.md) | – | – | | | | | – | 7 |
| | **Total** | **1,376** | **460** | **71** | **194** | **132** | **63** | **352** | **≈ 113** |

"Operations" is every operation in the service's AWS model, whatever its tier.
"Boards" is the service's page group in the console design.

**On the days.** They are focused working days for one line of work, on the
standard in [01-protocol.md](01-protocol.md), taken from each service
document's own breakdown, plus four for bringing CloudFormation and API
Gateway under IAM (D6). About 113 in total: on the order of five months
single-threaded. That is low confidence and probably optimistic, for three
reasons: the estimates were made before any stage has been run to this
standard; several stages hide a design change (STS sessions in IAM, the mapping
layer in Lambda, per-type property tables in CloudFormation); and many findings
wait on a decision or a recording. SQS is the calibration: its document records
its actual time, and this column is re-estimated from that number.

Work inside a stage can run in parallel (the API fixes and the console pages
touch different files). Stages do not overlap, because the point of the order
is that each service is signed off against finished neighbours.

## Why this order, and what was moved

Leaf services first, then storage, messaging, compute and observability, then
the two that touch everything. Writing the documents showed five places where
strict "one service at a time" would sign something off too early, so these
were moved:

| What | From | To | Why |
|---|---|---|---|
| The guard passes every condition key (`aws:SecureTransport` and others) | IAM, stage 15 | Foundation B10 | Otherwise seven services' enforce tests are signed off against a guard that drops keys, and re-run later. |
| Lambda's poller asks SQS for more than 10 messages | Lambda, stage 10 | SQS, stage 1 | The moment SQS refuses more than 10 (SQS-5), every trigger with a batch size above 10 stops receiving. Same commit. |
| `AWS::Kinesis::Stream` reports CREATE_COMPLETE and makes no stream | CloudFormation, stage 16 | Kinesis, stage 5 | A service is not done while a stack that declares it makes nothing. |
| `AWS::Events::EventBus` likewise | CloudFormation | EventBridge, stage 9 | Same. |
| `AWS::SQS::QueuePolicy`, `AWS::SNS::TopicPolicy`, `AWS::Lambda::Permission` are accepted and dropped | CloudFormation | SQS, SNS, Lambda | A template's grant never lands, and the policy is evaluated under soft and enforce. |
| `provision` overwrites a value on update; orders by reference | CloudFormation | Foundation B12, then Parameter Store and Secrets Manager | `internal/provision` is also `doze-aws apply`, which must keep adopting. The mode is settled once, before stage 1 touches the file. |

Two preconditions to keep in view: DynamoDB must fix `UpdateTable` ignoring
`StreamSpecification` (DDB-1) before Lambda's stream triggers mean anything,
which the order already satisfies; and IAM's stage is nearer two large stages
than one, because honouring STS credentials means sessions, a store, expiry and
every guard accepting a role principal.

## Status

Updated when a stage meets its criteria, in the same commit as the work.

| Stage | Contract | Findings fixed | Proved | Console | Ledger | Done | Actual days |
|---|---|---|---|---|---|---|---|
| Foundation | – | | | | – | | |
| SQS | | | | | | | |
| Parameter Store | | | | | | | |
| KMS | | | | | | | |
| Secrets Manager | | | | | | | |
| Kinesis | | | | | | | |
| S3 | | | | | | | |
| DynamoDB | | | | | | | |
| SNS | | | | | | | |
| EventBridge | | | | | | | |
| Lambda | | | | | | | |
| CloudWatch Logs | | | | | | | |
| CloudWatch | | | | | | | |
| Step Functions | | | | | | | |
| API Gateway | | | | | | | |
| IAM and STS | | | | | | | |
| CloudFormation | | | | | | | |
| Integration | – | | | – | – | | |

## Decisions

Decided on 2026-10-10. Each is written into the documents it affects.
Decisions that belong to one service are in section 10 of its document (API
Gateway has five, CloudFormation four, and several others one or two each).

| # | Decision | Decided | Where it lands |
|---|---|---|---|
| D1 | Does 1.0 wait for the console redesign? | **Yes.** A service is done when its API and its console pages both meet their criteria. | Every service document, step 5 of the protocol |
| D2 | What does "the API in entirety" mean? | **Nothing silent, and everything fixed.** Every operation in the model is functional, or cosmetic and says so, or refused honestly. All 460 findings are fixed, P0 to P3; none is dropped for being small. It does not mean every operation is implemented: about 720 stay refused, each with its reason in `not-built.md`. | Protocol steps 1 and 2; severity rule |
| D3 | Refused is purple, Denied is amber | **Yes**, the design's mapping. Today `app.css` has them the other way round. Not asked again on 2026-10-10; standing as the default until changed. | Foundation A3 |
| D4 | Real AWS recordings | Each service carries a recording queue (about 400 claims across the sixteen). Until an account exists a claim is `doc` or `memory`, never `recorded`; a fix that rests on `memory` is written as a test and skipped. | Every service document, section 8 |
| D5 | What does a refused (S) operation answer? | **HTTP 501, code `NotImplemented`** in the protocol's own envelope, a message naming the operation and why, and an `X-Doze-Not-Built` header. Uniform across services. That no SDK retries a 501 is verified by test before the rule spreads. | Foundation B6 and A2; protocol step 1 |
| D6 | Are CloudFormation and API Gateway evaluated by IAM? | **Yes, all three parts:** the control plane of both; the calls a stack makes, evaluated against the caller; and `execute-api:Invoke` on `AWS_IAM` methods. | API Gateway (stage 14), IAM (15), CloudFormation (16) |
| D7 | What is `aws:SecureTransport` for a local request? | **`true`.** Every local request is plain HTTP; measuring it would make the standard deny-when-not-TLS policy refuse everything, and it never fires for an SDK on AWS. The local endpoint stands in for the HTTPS one. | Foundation B10 |

## Rules that hold throughout

- **Never more forgiving than AWS.** Accepting what AWS refuses is a bug.
- **Weight is a feature.** Validation tables are built lazily; `task lightness`
  holds idle heap and binary size, and no stage may move them without saying why.
- **Red first.** A finding becomes a failing test before it becomes a fix.
- **Nothing on memory.** A claim about AWS that is only remembered is queued
  for recording. Its test is written and skipped; the code is not changed.
- **One service at a time.** Cross-service behaviour is written on the service's
  "owed to integration" list and left for stage 17, except where the table
  above moved it.
- **The design is the specification for the console**, and the code is the
  specification for the design. Where a board and the code disagree, the board
  is wrong until proven otherwise. Every service document lists the board
  sentences that describe a bug as it behaves today; they are rewritten when
  the bug is fixed.
- **Commit to main** when a stage meets its criteria, CI green.

The console design itself is at
<https://claude.ai/artifact/1WSqsF1GUQJdrhscUzzcTw>: one page group per service,
with each board named in the matching service document.
