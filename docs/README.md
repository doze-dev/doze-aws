# doze-aws documentation

- [getting-started.md](getting-started.md) — run it, point an SDK at it, configure it
- [endpoints.md](endpoints.md) — the addresses doze-aws answers on, and which are promised
- [cli.md](cli.md) — CLI reference: commands, flags, the `doze-aws.toml` config file, and how clients connect
- [cloudformation.md](cloudformation.md) — deploying with the AWS CLI, SAM, CDK or Serverless
- [lambda.md](lambda.md) — how a function runs (host processes, no Docker), the runtimes, in-place code, logs three ways, versions, layers and function URLs
- [embedding.md](embedding.md) — use doze-aws as a Go library, with a complete example
- [COMPATIBILITY.md](COMPATIBILITY.md) — what 1.0.0 promises, surface by surface, and what it does not
- [SUPPORT.md](SUPPORT.md) — per-service operation support tables (Functional / Cosmetic / Stub)
- [not-built.md](not-built.md) — what doze-aws declines and why, what is merely
  deferred and what it would take, and what the binary does not do at all
- [performance.md](performance.md) — measured hot-path and whole-request costs, and what to do about the slow ones
- [storage.md](storage.md) — why bbolt, what it costs, and what pebble was measured at

The design ground rules live in the [project README](../README.md#design-ground-rules) — one copy, so the two cannot drift apart.
