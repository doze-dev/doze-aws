# Phase 2 report — SQS + SNS port and coverage completion

Date: 2026-07-10

## Scope delivered

The two battle-tested pure-Go services moved from doze-modules
(`modules/sqs/sqssrv`, `modules/sns/snssrv`) into public doze-aws packages,
reshaped onto the uniform service pattern (`Options{DataDir, Peers, Logf,
Clock}` → `*Server` implementing `http.Handler` + `io.Closer`), and completed
to the full API surface.

- **sqs**: dual-protocol port intact (JSON 1.0 + legacy Query/XML, MD5
  algorithms, FIFO group locking + dedup GC, DLQ redrive, notifier-driven long
  polling, retention janitor). New in the port: queue **tags** (CreateQueue
  tags, TagQueue/UntagQueue/ListQueueTags in both protocols),
  **ListDeadLetterSourceQueues**, **message move tasks**
  (Start/List/CancelMessageMoveTask — synchronous completion, destination
  required, honest cancel semantics), AddPermission/RemovePermission as Tier C.
- **sns**: Query/XML port intact (fanout, filter policies, raw delivery,
  webhook confirmation handshake). SQS delivery now goes through
  **peers.Directory** instead of reading DOZE_SQS_SOCKET — the service is
  env-agnostic; deployments choose the wiring (Stack wires InProcess,
  doze-modules will wire sockets). New: topic **tags** + attribute
  round-trips (SetTopicAttributes/CreateTopic attributes surface in
  GetTopicAttributes), DataProtectionPolicy round-trip, PublishBatch per-entry
  message attributes, permission no-ops, and **honest Tier S stubs** for the
  entire mobile-push/SMS surface.
- **Stack**: sqs + sns wired with `peers.InProcess(gw.Handler)` — late-bound,
  construction-order independent. `Implemented = [sqs, sns, sts]`.
- **E2E**: the real-binary test now runs a cross-service scenario (STS
  identity, then SNS topic → SQS queue fanout) through one port.

## Key decisions / findings

- **SNS responses must always emit the `{Action}Result` element** even when
  empty: aws-sdk-go-v2's TagResource deserializer requires the node (found by
  contract test; some other ops tolerate its absence). Real SNS emits it too.
- SQS keeps its own dual-protocol codec rather than adopting
  internal/awsjson+awsquery — per-request protocol switching and the
  battle-tested param readers made a rewrite all risk, no reward. The shared
  packages remain the default for services without a legacy-protocol split.
- AddPermission/RemovePermission route to SNS in the gateway's bare-Action
  table (name collision with SQS); SQS callers reach them via signed or JSON
  requests, which route earlier.

## Test evidence

- Ports carry their white-box suites (store, hardening: dedup GC, retention
  sweep, long-poll wakeup) plus dual-protocol raw-wire tests, all updated to
  the public constructors, plus a new persistence-across-reopen test.
- **Both SDK generations as contract tests**: aws-sdk-go-v2 (SQS: full CRUD +
  FIFO + DLQ + long-poll + move tasks + DLQ sources; SNS: fanout raw +
  enveloped, filter policy, webhook handshake, tags/attributes) and
  aws-sdk-go v1 (SQS legacy Query round-trip with client-side MD5 validation,
  tags, coded XML error envelope; SNS publish→SQS fanout, list surfaces,
  Tier-S coded errors).
- `go test ./...`, `-short` (<1s, all socket tests skipped), `-race`: green.
