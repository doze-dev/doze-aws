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
deploy or a running app sees. Stage 1 then found 29 in SQS where the plan had 8,
and reading the model's own documentation and rendering each board beside its
page, for the other fifteen services on 2026-10-11 (the "pre-stage review" in
every service document), brought the count to **951, 91 of them P0**. Three
things stood out in the first pass.

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
| [services/](services/) | One document per service, all with the same twelve sections: contract, where it stands, findings, what to check, console, tests, recording queue, owed to integration, acceptance criteria, files, and the pre-stage review. |
| [03-shell-fidelity.md](03-shell-fidelity.md) | The shell and the cross-cutting boards (rail, top bar, list pane, key bar, the wire, the palette, States) rendered beside the page: 19 boards, 1 matches. Its build list and twelve decisions. The foundation's part C, reopened. |
| [02-lessons-from-sqs.md](02-lessons-from-sqs.md) | The twelve mistakes SQS taught, as questions. Every service document answers them in its "Pre-stage review" before its stage starts. |
| [90-integration.md](90-integration.md) | What is tested only once every service is individually done. |
| [findings-overview.md](findings-overview.md) | The first cross-service list, from the design work. Superseded in detail by the service documents; kept for the story. |

## Order, and the numbers

Each row starts only when the row above has met its acceptance criteria.

| # | Stage | Operations | Findings | P0 | P1 | P2 | P3 | Boards | Days |
|---|---|---|---|---|---|---|---|---|---|
| 0 | [Foundation](00-foundation.md) | – | 18 shared console items, 12 shared machinery items | | | | | shell | 12 |
| 1 | [SQS](services/sqs.md) | 23 | 29 | 4 | 19 | 6 | 0 | 21 | 2 planned; see its document |
| 2 | [Parameter Store](services/ssm.md) | 152 | 49 | 4 | 25 | 15 | 5 | 16 | 8 to 10 |
| 3 | [KMS](services/kms.md) | 54 | 55 | 5 | 18 | 23 | 9 | 20 | 8 to 10 |
| 4 | [Secrets Manager](services/secretsmanager.md) | 23 | 61 | 3 | 28 | 21 | 9 | 20 | 8 to 11 |
| 5 | [Kinesis](services/kinesis.md) | 45 | 55 | 4 | 28 | 14 | 9 | 18 | 8 to 10 |
| 6 | [S3](services/s3.md) | 112 | 84 | 5 | 40 | 30 | 9 | 26 | about 20 |
| 7 | [DynamoDB](services/dynamodb.md) | 62 | 62 | 7 | 28 | 20 | 7 | 20 | about 23 |
| 8 | [SNS](services/sns.md) | 42 | 60 | 5 | 28 | 15 | 12 | 19 | 10 to 12 |
| 9 | [EventBridge](services/eventbridge.md) | 57 | 64 | 10 | 28 | 19 | 7 | 24 | 15 to 18 |
| 10 | [Lambda](services/lambda.md) | 88 | 74 | 12 | 34 | 21 | 7 | 24 | about 25 |
| 11 | [CloudWatch Logs](services/logs.md) | 118 | 45 | 3 | 24 | 11 | 7 | 20 | 9 to 12 |
| 12 | [CloudWatch](services/cloudwatch.md) | 55 | 53 | 2 | 26 | 17 | 8 | 21 | about 20 |
| 13 | [Step Functions](services/stepfunctions.md) | 37 | 53 | 7 | 23 | 14 | 9 | 26 | about 15 |
| 14 | [API Gateway](services/apigateway.md) | 227 | 70 | 4 | 28 | 25 | 13 | 30 | about 15 |
| 15 | [IAM and STS](services/iam.md) | 191 | 69 | 8 | 31 | 22 | 8 | 26 | about 35 |
| 16 | [CloudFormation](services/cloudformation.md) | 90 | 68 | 8 | 38 | 16 | 6 | 21 | about 37 |
| 17 | [Integration](90-integration.md) | – | – | | | | | – | 7 |
| 0b | [Shell, reopened](03-shell-fidelity.md) | – | 19 boards, 1 matches | | | | | 19 | about 36 |
| | **Total** | **1,376** | **951** | **91** | **446** | **289** | **125** | **371** | **≈ 306** |

"Operations" is every operation in the service's AWS model, whatever its tier.
"Boards" is the service's page group in the console design.

**On the days.** They are focused working days for one line of work, on the
standard in [01-protocol.md](01-protocol.md), taken from each service
document's own breakdown **as re-made on 2026-10-11**, after each document was
read against its model and its boards were rendered. The first table said
**about 113**; the same documents now say **about 270** for the services and
**about 36** for the shell (the range is wider: roughly 245 to 375 in all),
which is on the order of fourteen months single-threaded. The
earlier number was written before any stage had been run to this standard and
before the models' own text was read. SQS was estimated at two days and took
several times that, and every service document was found low by the same
factor, for the same reasons: more findings than listed (the model's prose
holds limits its traits do not), a console that was built from the boards'
sentences and not their shape, and asynchronous operations that need a state
machine. The days column is the sum of the documents' own estimates; it is
**honest, not compressed**. The figure to decide on is the scope (see "What the
review found", below), not the arithmetic.

A stage of 20 or more days (S3, DynamoDB, Lambda, CloudWatch, IAM, CloudFormation)
is two or three sub-stages in its document: API, then conformance, then console.
Each sub-stage meets its own acceptance boxes, so the order above is by service
and not by size.

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

## What the review found (2026-10-11)

Before stage 2, each of the fifteen documents after SQS was read against its
model's own documentation and every board of its page group was rendered beside
the real page (the twelve questions are in [02-lessons-from-sqs.md](02-lessons-from-sqs.md);
each document has its "Pre-stage review" and its "6a" fidelity table). It
corrected about 600 claims in place (many `doc` or `memory` tags that are really
`model`), added **491 findings**, and corrected stale facts in about 110 board
files of the design's working copy. Nothing in the code or tests changed.

**The console.** Of 331 boards in the fifteen page groups, **22 match**.
The rest differ in shape or content, and none is "deliberately different"
without a reason in writing. The design is the specification (D1) and the
tables are its build lists. The shell itself (rail, top bar, list pane header,
key bar, the wire page) was nobody's: [03-shell-fidelity.md](03-shell-fidelity.md)
rendered the 19 cross-cutting boards beside the page and **1 matches**. The
foundation's part C was ticked "landed" and is reopened, at about 36 days
(28 to 45), with twelve decisions of yours in that document (the rail's default
state comes first).

**P0s the plan did not have** († marks the ones re-checked against the code
after the review; the rest were reproduced by their reviewer against a built
binary, and none of them against AWS):

| Service | Finding |
|---|---|
| SNS | **SNS-32** under enforce, `Publish` with `TargetArn` skips the topic policy; an explicit Deny does not apply. **SNS-51** `provision` and CloudFormation cannot create a FIFO topic or set any topic attribute. |
| DynamoDB | **DDB-32** `Expected`, `ConditionalOperator`, `AttributeUpdates`, `ScanFilter`, `QueryFilter`, `AttributesToGet` are accepted and ignored; a conditional put through `Expected` overwrote an item. **DDB-33** `ExecuteTransaction` is not atomic. **DDB-34** a deleted table's stream records are served to the next table of that name. |
| S3 | **S3-39** † the lifecycle rule's filter reads only `Prefix`; an `And`, `Tag` or size filter makes it bucket-wide and it expires objects the rule never named. |
| Lambda | **LAM-36** † every SQS trigger fails under a non-default account (`peercall` builds queue URLs from the constant default account, and SQS has compared the owner since SQS-23). |
| EventBridge | **EB-32** the input transformer delivers invalid JSON (string variables unquoted). **EB-36** a supplied `Time` is discarded. |
| Kinesis | **KIN-34** a consumer ARN resolves to its stream: tags and a resource policy set on a consumer land on the stream. |
| IAM | **IAM-51** under enforce, `GetCallerIdentity` and `GetSessionToken` are denied for a user with no policy. **IAM-40** `PermissionsBoundary` is accepted and dropped, so Terraform plans a permanent diff. |
| CloudWatch | **CW-22** † the JSON wire sends timestamps as ISO strings where `awsJson1_0` sends epoch numbers (the other JSON services were checked and send numbers; the fix is CloudWatch's own). |
| Secrets Manager | **SM-42** the rotation function is invoked with step names `createSecret` etc. where the user guide names `create_secret` etc. (that real rotation templates compare on the strings is `memory`). SM-42 to SM-47 are one design question: rotation runs inline here and is asynchronous on AWS. |
| CloudWatch Logs | **LOG-35** † the sweeper deletes events older than the default day within a minute, in a group with no retention. |

Nothing above is confirmed by a recording: there is no AWS account yet.
Findings that rest on a `model` quote say so; the ones that rest on a page or
on memory are queued for recording and not fixed on.

**What is cross-cutting, and where it lands** (the foundation, not each stage):

| What | Where it lands |
|---|---|
| `modelcheck.FromQuery` keeps one element of each list: SNS, IAM, STS and CloudFormation validate one element, chosen at random | Foundation **B13** (new), before SNS |
| Stubs answer `400 UnsupportedOperationException` in KMS, Kinesis, DynamoDB, CloudWatch, Step Functions and Parameter Store; D5 says 501 `NotImplemented` with `X-Doze-Not-Built` | Each stage moves its entries (the B6 sweep); written once as a rule here |
| The generated constraint tables are a snapshot: the "N of N enforced" claims count the replayed cases, not the model (list lengths and map keys are missing in Kinesis, EventBridge, Logs, DynamoDB, SSM, CloudWatch, API Gateway; Lambda covers 47 of 88 operations) | B8's regeneration, per service in its stage; the document of each says the number |
| A constraint on a string measured in bytes where AWS counts characters (SM-61) | `modelcheck`, with B13 |
| A shared policy validator would be stricter than AWS for KMS (a statement with no `Action` or `Resource` is accepted and inert) | Foundation **B9** now says each service decides which problems it refuses |
| The Streams model is not in `.audit-models/` | DynamoDB step 2: fetch it |
| The console shell is not the boards' shell | [03-shell-fidelity.md](03-shell-fidelity.md) |

**Decisions, from the reviews, that are yours** (each service document lists its
own at the end of section 12; these are the ones that repeat or that change
scope):

1. **Asynchronous or immediate, service by service (L7).** Secrets Manager
   rotation, KMS on-demand rotation, EventBridge replays, Step Functions
   `DeleteStateMachine`, DynamoDB table states, SNS delivery, Kinesis stream
   and consumer status, CloudFormation stacks (about 4 days more), Lambda
   mapping deletion. The SQS pattern exists (`sqs/move_task.go`). Each review
   recommends one; none is built.
2. **Strict switches (L8)**, off by default, in memory: `--sns-strict`,
   `--ddb-strict`, `--lambda-strict`, `--iam-strict`, `--apigateway-strict`.
   CloudWatch's review proposes none.
3. **Which stubs become real** (IAM's OIDC and SAML providers, password policy,
   login profiles and the credential report; DynamoDB's legacy parameters;
   API Gateway's Cognito and JWT authorizers; KMS's Ed25519, ML-DSA and ECDH,
   which the Go standard library can do).
4. **Whether to split the 20-day-plus stages** into API, conformance and console
   sub-stages, each with its own acceptance boxes (S3, DynamoDB, Lambda,
   CloudWatch, IAM, CloudFormation). Recommended.
5. **The shell**: the twelve decisions in [03-shell-fidelity.md](03-shell-fidelity.md);
   the first is the rail's default state, which the design and the page have
   never agreed on and nobody decided in writing.
6. **The estimate itself.** About 306 days single-threaded (270 for the services, 36 for the shell)
   against the 113 the plan carried. Some of that is the plan being low; some is that the
   standard is now checked. If the scope is to be cut, the cut belongs here,
   by service tier, not in the acceptance boxes.

## Status

Updated when a stage meets its criteria, in the same commit as the work.

| Stage | Contract | Findings fixed | Proved | Console | Ledger | Done | Actual days |
|---|---|---|---|---|---|---|---|
| Foundation | – | | | | – | | |
| SQS | 2026-10-11 | 24 of 24, plus LAM-6 and CFN-4's queue part | 35 scenarios; **0 recorded** (no AWS account) | built, route gate and sweep clean | SUPPORT.md, CHANGELOG, claims | 2026-10-11: check:full, coverage 81.0% (floor 80.5%), lightness, api, conformance 150 passed, e2e 259 of 260 (the one a flake that passes alone) with the route gate at 370 of 371, mutation 631 / 80 | not timed; see the SQS document, section 1 |
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
| D2 | What does "the API in entirety" mean? | **Nothing silent, and everything fixed.** Every operation in the model is functional, or cosmetic and says so, or refused honestly. Every finding is fixed (460 at the first count, 951 after the pre-stage reviews of 2026-10-11), P0 to P3; none is dropped for being small. It does not mean every operation is implemented: about 720 stay refused, each with its reason in `not-built.md`. | Protocol steps 1 and 2; severity rule |
| D3 | Refused is purple, Denied is amber | **Yes**, the design's mapping. Today `app.css` has them the other way round. Not asked again on 2026-10-10; standing as the default until changed. | Foundation A3 |
| D4 | Real AWS recordings | Each service carries a recording queue (about 400 claims across the sixteen). Until an account exists a claim is `doc` or `memory`, never `recorded`; a fix that rests on `memory` is written as a test and skipped. | Every service document, section 8 |
| D5 | What does a refused (S) operation answer? | **HTTP 501, code `NotImplemented`** in the protocol's own envelope, a message naming the operation and why, and an `X-Doze-Not-Built` header. Uniform across services. That no SDK retries a 501 is verified by test before the rule spreads. | Foundation B6 and A2; protocol step 1 |
| D6 | Are CloudFormation and API Gateway evaluated by IAM? | **Yes, all three parts:** the control plane of both; the calls a stack makes, evaluated against the caller; and `execute-api:Invoke` on `AWS_IAM` methods. | API Gateway (stage 14), IAM (15), CloudFormation (16) |
| D7 | What is `aws:SecureTransport` for a local request? | **`true`.** Every local request is plain HTTP; measuring it would make the standard deny-when-not-TLS policy refuse everything, and it never fires for an SDK on AWS. The local endpoint stands in for the HTTPS one. | Foundation B10 |

## Rules that hold throughout

- **Never more forgiving than AWS, and never stricter.** Accepting what AWS
  refuses is a bug; refusing what AWS accepts is the same bug the other way
  round. The one exception is a rule that exists because of AWS's latency,
  which is opt-in strict (see below).
- **Weight is a feature.** Validation tables are built lazily; `task lightness`
  holds idle heap and binary size, and no stage may move them without saying why.
- **Red first.** A finding becomes a failing test before it becomes a fix.
- **Nothing on memory.** A claim about AWS that is only remembered is queued
  for recording. Its test is written and skipped; the code is not changed.
- **One service at a time.** Cross-service behaviour is written on the service's
  "owed to integration" list and left for stage 17, except where the table
  above moved it.
- **The design is the specification for the console**, and the code is the
  specification for the design. Where a board and the code disagree about a
  *fact*, the board is wrong until proven otherwise and is corrected. About
  *shape* (columns, side panels, dialogs, sentences, states) the board is
  followed exactly: SQS was ticked done with six boards differing, and that is
  the mistake not to repeat. Every console stage writes a board-by-board table
  from a rendered board beside the rendered page. The per-service AWS icons
  stay. Every service document lists the board sentences that describe a bug
  as it behaves today; they are rewritten when the bug is fixed.
- **Read the model's text before writing a rule.** A number, a range, a
  default or an error code comes from `.audit-models/<svc>.json`'s
  documentation and error shapes (`model`), or from a named page (`doc`).
  Both directions of a gap are findings: stricter than AWS breaks a correct
  caller exactly as forgiving breaks a deploy.
- **Asynchronous on AWS is observable here.** A status that AWS moves through
  over time is modelled with a visible state and a rate, or is listed as
  immediate. A rule that exists because of AWS's latency is on by a setting
  and off by default.
- **Commit to main** when a stage meets its criteria, CI green.

The console design itself is at
<https://claude.ai/artifact/1WSqsF1GUQJdrhscUzzcTw>: one page group per service,
with each board named in the matching service document.
