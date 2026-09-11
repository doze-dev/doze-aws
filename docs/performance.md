# Performance

Measured, not estimated. Everything here comes from `task bench`, and you can
reproduce it:

```sh
task bench                      # every benchmark, 1s each
task bench BENCHTIME=5s         # steadier numbers
task bench > new.txt && benchstat old.txt new.txt
```

Numbers below are from an Apple M5 Max, Go 1.27, September 2026. Treat them as
shape rather than absolutes — the ratios hold across machines, the constants
do not.

## The one that surprises people: writes fsync

A whole request, in at the gateway and out with a response:

| Request | Per call |
|---|---|
| `SQS SendMessage` | **7.5 ms** |
| `DynamoDB PutItem` | **7.5 ms** |
| `CloudWatch PutMetricData` | **0.034 ms** |

The 220× gap is not code quality, it is durability. SQS and DynamoDB open
bbolt with its default fsync-per-transaction, so every write waits on the
disk; CloudWatch and CloudWatch Logs open it with `NoSync`, because samples
and log lines are high-volume and disposable.

**What this means for you.** A test that sends a thousand SQS messages one at
a time spends about 7.5 seconds in fsync. If that is your bottleneck:

- **Put the data directory on a tmpfs / RAM disk.** `--data-dir` takes any
  path, and nothing about doze-aws needs the data to survive a reboot during a
  test run. This is the lever that works today.
- **Batching does not help yet, and the benchmark says so.** One
  `SendMessageBatch` of ten messages costs 71.8 ms — ten times a single send,
  not one fsync's worth. `SendMessageBatch` loops calling the store's `Send`,
  and each call opens its own bbolt transaction. On AWS batching is a real
  saving; here it is currently only an API convenience. Making the batch
  operations one transaction is a known, contained improvement
  (`BenchmarkRequestSendMessageBatch` is there to prove it when someone does).

Durability stays the default because the alternative is a local stack that
loses the resources you just created when a laptop sleeps, and that costs more
debugging time than it saves.

## Per-layer costs

What one request is made of, for anyone optimising:

| Path | Cost | Notes |
|---|---|---|
| `modelcheck.ValidateMap`, 277 constraints (DynamoDB) | 52 µs, 1,662 allocs | runs on **every request of every service** |
| `modelcheck.ValidateMap`, 464 constraints (Lambda) | 89 µs, 2,784 allocs | the largest table in the tree |
| `modelcheck.FromQuery` | 15 µs | only on the Query wire (v1-era SDKs) |
| `awsquery.Unflatten`, 20 datums | 28 µs | ditto |
| `rpcv2cbor.DecodeMap` | 225–514 MB/s | the hand-rolled CBOR decoder |
| `eventpattern.Match` | 3.2 µs | per event **per rule** |
| `ddb/expr` parse a filter | 0.8 µs | once per request |
| `ddb/expr` evaluate against one item | **0.18 µs, 1 alloc** | once per item scanned |
| `cloudwatch` exact p99 over 1,000 samples | 9.6 µs | raw samples, not a sketch |

Two of these are worth knowing about:

- **Validation is the single largest fixed cost of a request**, at roughly six
  allocations per constraint. It buys the rejection parity the ledgers
  describe — doze-aws refusing what AWS refuses — so it is not waste, but it
  is where a request's time goes.
- **`eventpattern.Match` costs the same whatever the pattern**, because it
  decodes the event from JSON on every call. EventBridge matches every event
  against every rule on the bus and re-parses both the event and the rule's
  pattern each time, so a bus with thirty rules pays that thirtyfold per
  `PutEvents`. Correct, but the obvious thing to fix first if event throughput
  ever matters.

## What is not measured here

Network, TLS and SDK client time. The benchmarks drive the handler directly,
because those three are the parts doze-aws does not control — including them
would measure the loopback interface and call it emulator performance.

Idle cost is separate and small: about 35 MB resident and well under 1% of one
core, with all seventeen services enabled.
