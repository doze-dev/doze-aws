# Stage 17: integrated testing

Everything that needs two services to be true. It starts only when all sixteen
services have met their own criteria, and it is seeded by the "owed to
integration" section of every service document: this page is the union of
those sections, plus what no single service could have listed.

Until stage 17 begins, this page is a collecting point. Each service stage
appends its rows here when it closes.

## 1. Every wire in the graph

The console's wiring graph knows seven kinds of node (`s3`, `sqs`, `sns`, `eb`,
`lambda`, `ddb`, `kinesis`) and five kinds of edge. Each edge kind gets a
scenario that sends something in one end and observes it at the other, and a
second that breaks the far end.

| Edge | From → to | Happy path | Broken far end |
|---|---|---|---|
| `notify` | S3 → SQS, SNS, Lambda | Put an object; the event arrives with AWS's shape | Target deleted; a FIFO queue; under enforce with no service-principal statement |
| `sub` | SNS → SQS, Lambda, HTTP | Publish; each subscriber gets it, filter policy applied, raw delivery honoured | Queue deleted; FIFO queue on a standard topic; pending confirmation |
| `target` | EventBridge rule → SQS, SNS, Lambda, log group, API destination | Put an event; input, input path and input transformer applied | Target gone; disabled rule; a `*` endpoint with no path parameter |
| `esm` | SQS, Kinesis, DynamoDB stream → Lambda | A batch is delivered and deleted on success | Function throws; times out; is deleted; visibility shorter than timeout; batch size above 10 |
| `redrive` | SQS → its dead-letter queue | The receive that reaches `maxReceiveCount` moves it | DLQ deleted; type mismatch |

**Done when** each cell is a test in the cross-service suite, and each broken
cell either refuses at configuration time as AWS does, or fails at delivery in
the way AWS does and says so where a developer will see it.

## 2. Chains

Longer paths that real stacks are made of.

- [ ] S3 → SNS → SQS → Lambda → DynamoDB, with the function writing back to S3
      under a different prefix (no loop).
- [ ] API Gateway → Lambda → SQS → Lambda → Step Functions execution.
- [ ] EventBridge schedule → Lambda → Secrets Manager and Parameter Store reads → KMS decrypt.
- [ ] CloudWatch Logs subscription filter → Lambda and → Kinesis.
- [ ] Metric filter → metric → alarm → SNS → SQS.
- [ ] Step Functions task token → SQS → a worker → SendTaskSuccess.
- [ ] Secrets Manager rotation with a real rotation function, all four steps.

For each: the trace on the wire nests the calls doze-aws made, and nothing else.

## 3. Deploy tools

The smokes exist (`go tool task test:deploy`): Terraform, SAM, CDK, Serverless,
plain CloudFormation. Stage 17 widens each from "it deploys" to "it deploys,
updates, and destroys, and the account is as it was".

- [ ] One template per tool that uses every service the tool can declare here.
- [ ] Create, update (a property change, an added resource, a removed
      resource), delete. After delete, every service lists nothing.
- [ ] A template with a deliberate failure in the middle rolls back cleanly.
- [ ] The cases the service stages found: a queue and its DLQ in one template;
      an SSM parameter whose value changes; a stage redeployed with variables;
      an exporter whose importer was deleted.

## 4. IAM across everything

- [ ] A matrix: for every service, one allowed and one denied action, under
      soft (recorded, not refused) and enforce (refused with AWS's message),
      for a user key, a role session and the root. CloudFormation and API
      Gateway are in the matrix or out of it by decision D6, not by omission.
- [ ] On a fresh stack in the default mode (soft), no delivery between two
      services logs a "would deny": every wire in section 1 carries the
      statement it needs, or the console says which one is missing.
- [ ] Every service's resource policy: an explicit Deny beats an identity Allow;
      a same-account resource Allow admits a principal with no identity policy.
- [ ] Every service principal that delivers across services
      (`sns.amazonaws.com`, `events.amazonaws.com`, `s3.amazonaws.com`,
      `logs.amazonaws.com`, `lambda.amazonaws.com`) needs its statement under
      enforce, and the failure names it.
- [ ] The policy generated from a recorded run, attached to a fresh user, lets
      the same run pass under enforce and nothing more.
- [ ] Condition keys the ledger says are supplied are supplied
      (`aws:SecureTransport` was found missing).

## 5. The whole binary

- [ ] `go tool task soak` for the long run, with chaos restarts: no lost
      message, no duplicate beyond what AWS allows, no leaked goroutine or file.
- [ ] Restart in the middle of each chain in section 2: it resumes or fails as AWS would.
- [ ] The data-directory upgrade fixture, extended with one resource from every
      service as it stands at 1.0.
- [ ] `go tool task lightness`: idle heap, binary size and module list against
      the numbers at the start of stage 0. Any growth is explained.
- [ ] A non-default `--account-id` and `--region` through every chain.
- [ ] The whole browser suite and the whole conformance suite, on Linux, macOS
      and Windows.

## 6. The console, as one thing

- [ ] The map and the map at scale (foundation part D), tested with a stack of
      seven resources and one of fifty.
- [ ] The wire shows each chain in section 2 as one nested story.
- [ ] Every neighbour strip and neighbour check agrees with the graph.
- [ ] The fidelity dialog opens for every rail entry and shows the right ledger.
- [ ] Accessibility sweep over every page of every service, light and dark.
- [ ] A first-run walk: empty binary, Connect, create one of each, delete all.

## 7. Real AWS

When the account exists.

- [ ] Every scenario in every service's recording queue is recorded.
- [ ] `CONFORMANCE_REQUIRE_SNAPSHOTS=1` in CI: an unverified response fails the run.
- [ ] Every difference the recordings expose is fixed, or is in
      `conformance/deviations.py` with its reason and in the service's
      "Differences from AWS".
- [ ] No claim in any service document is still marked `memory`.

## Acceptance criteria for stage 17

- [ ] Sections 1 to 6 complete.
- [ ] Section 7 complete, or 1.0 ships with the unverified count printed in the
      release notes and `COMPATIBILITY.md` saying what that number means.
- [ ] Every "owed to integration" row in every service document is closed here.
- [ ] CI green on every workflow, three operating systems.

## Collected from the service stages

Rows are added as each service closes. Seeded from the findings so far.

| From | What | With | Status |
|---|---|---|---|
| SQS | Visibility shorter than the function's timeout is refused at trigger creation | Lambda | open |
| SQS | A deleted function's poller stops | Lambda | open |
| SQS | Queue and DLQ in one template | CloudFormation | open |
| S3 | A FIFO queue or a missing target as a notification destination | SQS, Lambda | open |
| S3 | `DeleteObjects` sends one removal event per key | SQS, SNS, Lambda | open |
| SNS | A FIFO queue subscribed to a standard topic | SQS | open |
| EventBridge | A target that is gone; a destination with a `*` endpoint | SQS, Lambda | open |
| Lambda | Trigger batch size above 10 | SQS | open |
| CloudWatch | An alarm's Publish denied under enforce reads as 500 on the wire | SNS, IAM | open |
| Logs | A subscription to a stream or function that does not exist | Kinesis, Lambda | open |
| Secrets Manager | A rotation function that fails is the function's failure, not the emulator's | Lambda | open |
| CloudFormation | An SSM parameter's value changed by an update | Parameter Store | open |
| IAM | STS role credentials are honoured by every service's guard | all | open |
| CloudFormation | For each of the 56 mapped resource types: what a stack declares can be read back through the service's own API, property by property | every service | open |
| CloudFormation | Deploy, then **update**, through each tool: a patched method or integration (API Gateway's unrouted updates), a changed parameter value, a redeployed stage | API Gateway, Parameter Store | open |
| CloudFormation | A policy resource (queue, topic, function permission) lands and is evaluated under enforce | SQS, SNS, Lambda, IAM | open |
| Secrets Manager | A rotation function that calls back into Secrets Manager while `RotateSecret` holds the request | Lambda | open |
| DynamoDB | A stream switched on by `UpdateTable` feeds a function | Lambda | open |
| Lambda | A trigger on `function:alias` reaches the alias's version (the SAM `AutoPublishAlias` shape) | SQS, CloudFormation | open |
| Lambda | Stream triggers resume where they left off across a restart | Kinesis, DynamoDB | open |
| S3 | A failed notification delivery is visible somewhere other than the process log | SQS, SNS, Lambda | open |
| EventBridge | Target parameters (FIFO group id, dead-letter config, retry policy) reach the target | SQS | open |
| Step Functions | A state machine or an alarm has no node in the wiring graph; decide whether the map shows them | console | open |
| Kinesis | A Logs subscription feeding a stream appears as an edge | Logs, console | open |
