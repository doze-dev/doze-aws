# doze-aws documentation

- [getting-started.md](getting-started.md) — run it, point an SDK at it, configure it
- [endpoints.md](endpoints.md) — the addresses doze-aws answers on, and which are promised
- [cli.md](cli.md) — CLI reference: commands, flags, the `doze-aws.toml` config file, and how clients connect
- [cloudformation.md](cloudformation.md) — deploying with the AWS CLI, SAM, CDK or Serverless
- [lambda.md](lambda.md) — how a function runs (host processes, no Docker), the runtimes, in-place code, logs three ways, versions, layers and function URLs
- [embedding.md](embedding.md) — use doze-aws as a Go library, with a complete example
- [SUPPORT.md](SUPPORT.md) — per-service operation support tables (Functional / Cosmetic / Stub)
- [not-built.md](not-built.md) — what doze-aws declines and why, what is merely
  deferred and what it would take, and what the binary does not do at all
- [performance.md](performance.md) — measured hot-path and whole-request costs, and what to do about the slow ones
- [storage.md](storage.md) — why bbolt, what it costs, and what pebble was measured at
- [reports/](reports/) — phase-by-phase build reports, each a dated snapshot of
  the day it was written rather than a description of today

## Design ground rules

- **Lightweight above all.** Five runtime dependencies: bbolt, a TOML parser, a
  YAML parser (for CloudFormation templates), a JSONata evaluator (Step
  Functions) and doze-names (the `.doze` zone); the AWS SDKs are test-only.
  Data persists under one deletable directory.
- **Real protocols, both SDK generations.** Every service speaks the actual AWS
  wire protocol and is verified against `aws-sdk-go-v2` and the legacy
  `aws-sdk-go`, with SigV2 and SigV4 accepted.
- **Honest boundaries.** Every documented operation of an implemented service
  gets a handler — functional where locally meaningful, a faithful config
  round-trip where the effect is cloud-only, and a clean error where emulation
  would be a lie. No silent no-ops.
