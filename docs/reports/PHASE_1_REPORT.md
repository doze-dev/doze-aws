# Phase 1 report — skeleton, shared protocol packages, gateway, STS

Date: 2026-07-10

## Scope delivered

The repo exists, releases, and serves its first complete service. Everything a
later phase needs to add a service is in place:

- **Repo scaffold**: go.mod (`github.com/doze-dev/doze-aws`, Go 1.26), Taskfile
  (`go tool task`, GOWORK=off), CI (gofmt/vet + test matrix on ubuntu+macos,
  race), weekly fuzz workflow, goreleaser + checksum-verifying install.sh,
  example TOML.
- **Public packages**: `awsident` (fixed local identity), `peers` (cross-service
  directory: InProcess / UnixSockets / FromEnv / Static), root `dozeaws`
  package (`NewStack` → gateway-fronted `http.Handler` + `Close`).
- **Internal protocol packages**:
  - `awshttp` — APIError, request ids, ISO8601.
  - `awsquery` — Query-protocol params (incl. `.member.N` lists), per-action
    XML response envelope, error envelope.
  - `awsjson` — JSON 1.0/1.1 target routing, body decode, error shape
    (consumer services arrive in Phases 2–3).
  - `sigparse` — SigV2 AND SigV4 identity/scope extraction, header and
    presigned forms, parse-don't-verify; presigned expiry is the one enforced
    check. Fuzzed.
  - `gateway` — shared-endpoint router: X-Amz-Target prefix → SigV4 scope →
    Lambda path → Query Action table → S3 fallback. Body-preserving Action
    peek. Central presigned-expiry rejection.
- **STS**: all 9 documented operations (8×F, 1×S — see docs/api-support/sts.md).
  Query/XML wire format, JWT/SAML claim reflection, duration validation.
- **cmd/doze-aws**: serve (default) / `config print` / `version`; flags > TOML
  file > defaults with unknown-key rejection; graceful SIGTERM drain;
  LocalStack-compatible default port 4566.

## Key decisions

- **Result structs encode AS the `{Action}Result` element** via
  `xml.Encoder.EncodeElement` — caught by the first SDK contract test
  (double-wrapped results parse as empty structs in the SDK, silently).
- **SigV2 requests route via the Action table**, since V2 signatures carry no
  service scope. AddPermission/RemovePermission (the one SNS/SQS action-name
  collision) route to SNS; real SQS clients sign or speak JSON and never hit
  the table.
- **aws-sdk-go v1 validates client-side** (lengths, required fields), so
  server-side validation tests must use inputs that pass the client (e.g. bad
  charset rather than bad length).
- STS `Options.DataDir` accepted-and-ignored keeps the service constructor
  signature uniform for the Stack builder.

## Test evidence

- `go test ./...` green; `go test -short ./...` runs in <1s with every
  SDK/binary test skipped (gate lives in the boot helpers).
- `go test -race ./...` green.
- Contract tests: aws-sdk-go-v2 (7 tests incl. error-envelope assertions via
  smithy.APIError) and aws-sdk-go v1 (4 tests incl. awserr code matching) both
  drive the full stack through the gateway.
- Real-binary E2E: builds cmd/doze-aws, SDK call against it, SIGTERM → exit 0
  with drain log; `config print` output round-trips through `--config`.
- Fuzz seeds for `sigparse` run in the normal suite; weekly extended fuzzing in CI.

## Deviations from plan

None of substance. `internal/restxml`, `awschunk`, `checksum` are deferred to
Phase 4 (S3) where their first consumer lives — listed in the plan's Phase 4
line, noted here for the record.
