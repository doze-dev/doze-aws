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
| `modelcheck.ValidateMap`, 277 constraints (DynamoDB) | 17 µs, 1 alloc | runs on **every request of every service** |
| `modelcheck.ValidateMap`, 464 constraints (Lambda) | 29 µs, 1 alloc | the largest table in the tree |
| `modelcheck.FromQuery` | 15 µs | only on the Query wire (v1-era SDKs) |
| `awsquery.Unflatten`, 20 datums | 28 µs | ditto |
| `rpcv2cbor.DecodeMap` | 225–514 MB/s | the hand-rolled CBOR decoder |
| `eventpattern.MatchDoc` against a decoded event | 35–130 ns, **0 allocs** | per event **per rule** |
| `eventpattern.Decode` one event | 3.5 µs | once per `PutEvents`, not per rule |
| `ddb/expr` parse a filter | 0.8 µs | once per request |
| `ddb/expr` evaluate against one item | **0.18 µs, 1 alloc** | once per item scanned |
| `cloudwatch` exact p99 over 1,000 samples | 9.6 µs | raw samples, not a sketch |

Two of these were worth knowing about, and both have since been fixed. The
before-and-after is kept because the shapes recur.

- **Validation was the single largest fixed cost of a request**, at roughly six
  allocations per constraint — 89 µs and 2,784 allocations for Lambda's table.
  Two things caused it, and neither was the checking. Constraint paths were
  split on `.` and run through a regexp on *every* request even though a path
  is a compile-time constant, and every resolved path allocated its own result
  slice. Paths are now parsed once into a `sync.Map`, and the walk appends into
  one buffer reused across the table. **29 µs and a single allocation**,
  whatever the table's size — 3× faster and effectively allocation-free.
  The buffer reuse is guarded by `internal/modelcheck/walk_test.go`: a stale
  buffer checks constraint *N* against the sites of the constraints before it,
  which is silent for an absent member, so those fixtures are built so it
  cannot be.
- **`eventpattern.Match` cost the same whatever the pattern** — 3.5 µs for the
  cheapest and the most expensive alike — because it decoded the event from
  JSON on every call, and the decode was all of it. Matching a *decoded* event
  costs 35–130 ns and allocates nothing. EventBridge matches every event
  against every rule on the bus, and re-parsed both the event and the rule's
  pattern each time, so a bus with thirty rules paid that thirtyfold per
  `PutEvents`. The event is now decoded once per `PutEvents` (`Decode` +
  `MatchDoc`) and compiled patterns are cached by their text
  (`eventbridge/patterncache.go` — keyed by text so there is nothing to
  invalidate). `BenchmarkBusFanout` measures a 30-rule bus both ways:

  | | ns/op | allocs/op |
  |---|---|---|
  | parse + match per rule (the old shape) | 191,325 | 4,680 |
  | compiled once, decoded once | 7,086 | 79 |

  **27× faster, 59× fewer allocations.** `Match` still exists and still decodes;
  it is the right call for a one-off, and `Parse` is still what validates a
  pattern a user submits.

## What is not measured here

Network, TLS and SDK client time. The benchmarks drive the handler directly,
because those three are the parts doze-aws does not control — including them
would measure the loopback interface and call it emulator performance.

## Idle cost

All seventeen services enabled, nothing being asked of them.

| | |
|---|---|
| Physical footprint | **15 MB** |
| CPU | **0.1% of one core** |
| Wakeups | **0.1/second** |
| Goroutines | 25 |

**Quote the footprint, not RSS.** `ps` reports ~40 MB for the same process,
because on macOS RSS counts file-backed pages — the binary's own text, the
system libraries — that are shared and not the process's to give back. `vmmap`
puts the dirty total at 15 MB, which is what Activity Monitor calls Memory and
what Go's own `MemStats.Sys` agrees with to within a megabyte.

**The CPU figure is the floor, not a target for more work.** A 30-second
profile of an idle process collects 30ms of samples and *every one of them* is
the Go runtime parking and waiting — `pthread_cond_wait`, `kevent`,
`findRunnable`. Not one sample lands in doze-aws code.

**Wakeups are the number that CPU% hides.** A process using no measurable CPU
can still wake the core hundreds of times a minute, and a core that is woken
never reaches its deeper idle states — which is what a laptop's battery
notices. doze-aws idled at 2.4 wakeups/second because two schedulers ticked
once a second each regardless of whether anything was scheduled:

- The Step Functions engine's ticker walks the loaded executions looking for
  Wait states and deadlines that have come due. With no executions loaded it
  woke every second to look at an empty map. It now runs only while there are
  runs to time, and parks on its nudge and delivery channels otherwise.
- The EventBridge scheduler re-read every bus's rules once a second — a bbolt
  read transaction per bus, forever — to be told again that nothing is
  scheduled. It now backs off to `idleScan` (5s) once it finds no schedule
  rules and returns to one second the moment one exists.

Together: **2.4 → 0.1 wakeups/second**, about one every ten seconds. The
EventBridge backoff costs arming latency and not a missed fire: a rule is armed
when the scheduler first sees it and its interval starts there, so a rule
created during an idle stretch begins up to 5s late and every tick after it is
exact.

**Cutting goroutines is not worth doing.** It is the obvious next lever and the
numbers say no: a blocked ticker goroutine costs **2.8 KB** of stack, measured,
so folding the nine per-service janitors into one shared sweeper would save
~25 KB — 0.2% of the footprint — and about five wakeups a minute. The price is
moving janitor ownership out of nine services and back through the shutdown
path that already had a DB-close race once. The goroutines that remain are one
janitor per service plus queue workers parked on channels, and a goroutine
parked on a channel costs nothing at all.

What the 15 MB is made of, from a heap profile of an idle process:

| | |
|---|---|
| Live heap | 2.2 MB |
| GC metadata | 3.0 MB |
| Goroutine stacks | 0.7 MB |
| Runtime, spans, other | the rest |

It was 20 MB until the constraint tables stopped compiling their regular
expressions during init. 527 patterns across ten services were compiled at
startup whether or not the process was ever asked to serve those APIs, and they
were 3.1 MB of a 6.7 MB live heap — 46% of everything an idle doze-aws held.
`modelcheck.Pattern` keeps the source and compiles on first match instead.

Two things that did **not** help, recorded so nobody spends the afternoon:

- `debug.FreeOSMemory()` after startup moves retained memory 16.5 MB → 16.4 MB.
  Go's scavenger has already returned what it can.
- Lazy patterns did nothing for startup: 237 ms before, 245 ms after, which is
  noise. Compiling 527 regexes is not slow, it is just memory you keep forever.
