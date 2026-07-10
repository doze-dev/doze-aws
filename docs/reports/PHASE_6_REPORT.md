# Phase 6 report — EventBridge, Lambda, and cross-service wiring

Date: 2026-07-11

## Scope delivered

The last two services and the wiring that connects everything — doze-aws now
serves all 10 services and they talk to each other.

- **internal/eventpattern**: the full EventBridge content-based pattern
  language — exact/prefix/suffix/equals-ignore-case/wildcard (iterative
  `*` matcher)/anything-but (value/list/sub-operator)/numeric (ranges)/exists/
  cidr/$or, nested fields, event-array any-element matching. Fuzzed;
  table-tested against AWS's documented examples.
- **internal/peercall**: the hand-rolled typed clients services use to reach
  each other (SQSSend/Receive/Delete, LambdaInvokeAsync, SNSPublish) in the
  targets' own wire formats — so aws-sdk-go stays test-only in production code.
- **internal/lambdaruntime**: the Lambda Runtime API server + process
  supervisor. One loopback listener per function serving the four runtime
  routes; the child is spawned with AWS_LAMBDA_RUNTIME_API and the full Lambda
  env; serial invocations with deadline/timeout handling and log tailing.
- **eventbridge**: buses + rules + targets; PutEvents matches enabled rules and
  delivers to SQS/SNS/Lambda with Input/InputPath/InputTransformer shaping;
  TestEventPattern; tags. Scheduled rules and archives answer honestly (Phase 8).
- **lambda**: REST-JSON control plane (create/update/get/list/delete, versions,
  aliases, function URLs, concurrency, DLQ/destinations, tags), sync + async
  Invoke, and SQS event source mappings that poll and deliver batches
  (delete-on-success). ZipFile + the `_local_` in-place code extension.
- **Cross-service wiring** (all functional, all peer-routed):
  - EventBridge rule → SQS / SNS / Lambda targets.
  - S3 event notifications → SQS / SNS / Lambda (PutBucketNotificationConfiguration
    with event + prefix/suffix filters; upgraded from Tier C to Tier F).
  - SNS → Lambda subscriptions (auto-confirmed, invoke with the SNS event shape).
  - SQS event source mapping → Lambda.
- Stack now serves 10/10 services.

## Bug caught

- `lambdaruntime.buildCommand` built the child env slice but never assigned
  `cmd.Env` — so handlers saw an empty AWS_LAMBDA_RUNTIME_API and exited. Found
  by capturing the child's stderr through the runner's log ring buffer. (Also
  hardened: relative `./bootstrap` is now resolved against the code dir so the
  child's cwd can't affect whether it's found.)

## Test evidence

- **EventBridge (aws-sdk-go-v2)**: a rule with an SQS target driven through a
  full Stack — PutEvents actually lands the matching event (and only the
  matching one) in SQS; InputTransformer shaping verified end to end; buses,
  rule enable/disable/list, TestEventPattern, invalid-pattern + schedule-rule
  honesty.
- **Lambda (aws-sdk-go-v2)**: a real Go handler compiled at test time,
  invoked synchronously through the SDK — the full next→response Runtime API
  cycle, env-var injection, async 202, versions/aliases/function-URL/tags
  lifecycle, event-source-mapping lifecycle.
- **eventpattern**: ~30 matching cases + parse-error cases, fuzzed.
- **S3 → SQS notifications**: PutBucketNotificationConfiguration + PutObject
  through a Stack lands a correctly-shaped S3 event record in SQS, honoring a
  `.jpg` suffix filter (the `.txt` upload is filtered out).
- Full suite `-race` clean.
