# Phase 7 report — hardening and v0.1.0 readiness

Date: 2026-07-11

## Scope delivered

doze-aws is feature-complete (10/10 services, all cross-service wiring
functional) and ready to tag v0.1.0.

- **Soak/chaos harness** (`cmd/doze-aws/soak_test.go`, build-tagged `soak`, run
  via `go tool task soak`): a mixed cross-service workload (S3 puts + SQS sends
  + DynamoDB writes) under sustained load for a configurable duration. Smoke-run
  clean.
- **User docs**: getting-started (run/point-an-SDK/configure/persistence),
  embedding (stack + single-service + peers wiring), a docs index, and the
  per-service api-support tables completed in each phase.
- **Fuzz CI** now covers six targets (sigparse, awschunk, ddb decimal/keyenc/
  expr, eventpattern) on the weekly schedule.
- Release plumbing (goreleaser, checksum-verifying install.sh, the CI matrix)
  landed in Phase 1 and is unchanged; a `v*` tag ships static binaries for
  linux/darwin/windows × amd64/arm64.

## v0.1.0 status

- `go test ./...` and `go test -race ./...` green across all 23 packages.
- `go test -short ./...` runs in ~2s (every SDK/binary/soak test gated).
- gofmt clean, `go vet` clean.
- Runtime dependencies remain just bbolt + BurntSushi/toml; the AWS SDKs are
  test-only.

## Deferred to Phase 8 (tracked, honest stubs today)

DynamoDB PartiQL, KMS key rotation mechanics, EventBridge scheduled rules +
archives/replay, Secrets Manager rotation-via-Lambda, and a Lambda scale-out
concurrency pool. Each answers a clear error with a named reason until then.
