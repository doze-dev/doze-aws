# Phase 8 report — coverage completions

Date: 2026-07-12

Phase 8 closes the functional gaps that Phases 1–7 deferred with honest stubs.
Everything below is now Tier F (real, SDK-observable semantics), verified with
both aws-sdk-go-v2 contract tests and white-box tests. The only remaining
deferral is DynamoDB Streams (post-1.0, per the original scope decision).

## Delivered

- **DynamoDB PartiQL** (`dynamodb/partiql.go`, `partiql_parse.go`): ExecuteStatement,
  BatchExecuteStatement, ExecuteTransaction. The parser reuses the expression
  lexer; SELECT/INSERT/UPDATE/DELETE map onto the existing store operations.
  Fuzzed (`partiql_fuzz_test.go`).

- **KMS key rotation** (`kms/actions.go`): EnableKeyRotation / DisableKeyRotation /
  GetKeyRotationStatus and on-demand RotateKeyOnDemand. New backing material is
  generated; the key id embedded in each ciphertext blob means old ciphertexts
  still decrypt after rotation. Asymmetric + HMAC families were already functional
  from Phase 3.

- **Secrets Manager rotation-via-Lambda** (`secretsmanager/rotate.go`): RotateSecret
  invokes the configured rotation function through the standard four-step protocol
  (createSecret → setSecret → testSecret → finishSecret), advancing AWSCURRENT /
  AWSPENDING staging labels. Verified end-to-end against a local rotation function
  (`TestRotateSecretViaLambda`).

- **Lambda concurrency pool** (`internal/lambdaruntime/pool.go`): the runtime pool
  now scales out when concurrent demand exceeds the current warm-process count
  (up to the concurrency cap) and scales back to zero after an idle window.
  `TestPool*` covers both directions.

- **EventBridge scheduled rules** (`eventbridge/scheduler.go`, `actions.go`):
  PutRule accepts `ScheduleExpression`. `rate(...)` rules are driven by a
  once-a-second ticker that delivers the canonical "Scheduled Event" to the rule's
  targets. `cron(...)` is accepted and stored but not driven — a wall-clock cron
  isn't meaningful in an ephemeral local stack (documented). ScheduleExpression
  round-trips through Describe/List.

- **EventBridge archives + replay** (`eventbridge/archive.go`, `archive_actions.go`):
  CreateArchive/DescribeArchive/ListArchives/UpdateArchive/DeleteArchive and
  StartReplay/DescribeReplay/ListReplays. PutEvents appends every matching event
  to each archive over its bus (respecting the archive's optional pattern).
  StartReplay re-injects the events in the requested time window back through the
  destination bus's rules — optionally restricted to a set of rule ARNs — and
  completes synchronously. Retention days are stored but not actively expired
  (the stack is ephemeral). CancelReplay answers honestly: there is never a
  running replay to cancel locally. Verified end-to-end (`TestSDKArchiveAndReplay`):
  live events are archived, then replayed, and the second delivery lands in SQS.

## Still Tier S (unchanged, cloud-only)

EventBridge API destinations + connections, partner event sources, global
endpoints, cross-account permissions, and the schemas registry; DynamoDB global
tables + DAX; and the per-service stubs enumerated in each service's
`docs/api-support/*.md`.

## Status

- `go test ./...` and `go test -race ./...` green across all packages.
- `go test -short ./...` still fast (SDK/binary/soak tests gated).
- gofmt clean, `go vet` clean.
- Runtime dependencies unchanged (bbolt + BurntSushi/toml); AWS SDKs test-only.

Deferred to post-1.0: DynamoDB Streams (and the Lambda triggers that ride on it).
