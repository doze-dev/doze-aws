# doze-aws documentation

- [getting-started.md](getting-started.md) — run it, point an SDK at it, configure it
- [cli.md](cli.md) — CLI reference: commands, flags, the `doze-aws.toml` config file, and how clients connect
- [embedding.md](embedding.md) — use doze-aws as a Go library, with a complete example
- [api-support/](api-support/) — per-service operation support tables (Functional / Cosmetic / Stub)
- [reports/](reports/) — phase-by-phase build reports

## Design ground rules

- **Lightweight above all.** Runtime dependencies are bbolt and a TOML parser;
  the AWS SDKs are test-only. Data persists under one deletable directory.
- **Real protocols, both SDK generations.** Every service speaks the actual AWS
  wire protocol and is verified against `aws-sdk-go-v2` and the legacy
  `aws-sdk-go`, with SigV2 and SigV4 accepted.
- **Honest boundaries.** Every documented operation of an implemented service
  gets a handler — functional where locally meaningful, a faithful config
  round-trip where the effect is cloud-only, and a clean error where emulation
  would be a lie. No silent no-ops.
