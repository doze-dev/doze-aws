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
- **Use the batch operations.** A `SendMessageBatch` of ten messages costs
  **7.6 ms** — the same as a single send, because it is one transaction and
  therefore one fsync. Ten individual sends cost 75 ms.

  This used to be a note saying batching did *not* help: the handler looped
  over the store's `Send`, so ten messages opened ten transactions and cost
  71.8 ms. `SendMessageBatch`, `DeleteMessageBatch` and
  `ChangeMessageVisibilityBatch` now each run in one transaction, which is a
  **9.4× improvement** and makes batching worth reaching for here the way it is
  on AWS.

  Per-entry semantics are unchanged: one oversized message still fails on its
  own and the other nine are written, because the batch reports per entry
  rather than aborting the transaction.

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

**These numbers are now measured by a test rather than by hand.** Every figure
in this section used to be a one-afternoon measurement written down and never
checked again — the binary could have doubled and nothing would have noticed.
`task lightness` reproduces them, `testdata/lightness.json` holds them with a
ceiling each, and CI fails if one breaches. Run it yourself:

```sh
go tool task lightness
```

| | | where it comes from |
|---|---|---|
| Retained memory | **11.2 MB** | `MemStats.Sys − HeapReleased`, `local.shapes.full.retained_bytes` |
| Peak RSS | 29.0 MB | `Rusage.Maxrss`, `local.shapes.full.max_rss_bytes` |
| Live heap | 3.6 MB | `local.shapes.full.heap_alloc_bytes` |
| CPU | **2.1 ms per 3 idle seconds** | `Rusage` utime+stime, `local.shapes.full.cpu_micros` |
| Wakeups | 24 per 3 idle seconds | `/sched/latencies:seconds`, `local.shapes.full.sched_events` |
| Goroutines | **25** | 23 service-owned plus the runtime's own |
| Boot | **210 ms** | `local.shapes.full.boot_millis` |

The goroutine figure is not a total that has to be trusted: `task lightness`
measures each service's contribution as a delta across its own `NewStack`, so
the budget records logs 4, stepfunctions 3, apigateway/cloudwatch/eventbridge/
lambda 2 each, eight services 1 each, and cloudformation/iam/sts 0. A count
that moves names the service it moved in.

**Wakeups are counted from the scheduler, not from `Rusage.Nvcsw`.** Nvcsw is
the field that looks right and is not: the runtime's `sysmon` parks and wakes at
up to 10 ms intervals regardless of what the program does, which floors it near
a thousand per ten-second window and buries the handful that are ours. `sysmon`
is not a goroutine, which is exactly why the scheduler's own runnable-transition
count is the right proxy.

**Measured in a child process, which is what makes three of these honest.**
`Maxrss` is a high-water mark for the whole process and CPU time is cumulative,
so read from inside a test binary that also runs a 4,000-round bounds test they
report what *that* did. The measurement re-execs itself with one stack and
nothing else, `GOMAXPROCS` pinned to 4 so a laptop and a CI runner are
comparing the same quantity.

**Quote the footprint, not RSS.** RSS reads far higher for the same process,
because on macOS it counts file-backed pages — the binary's own text, the
system libraries — that are shared and not the process's to give back. `vmmap`'s
dirty total is what Activity Monitor calls Memory, and Go's own
`MemStats.Sys − HeapReleased` agrees with it to within a megabyte. That is why
the budget records both and quotes the first.

**The two sets of numbers on this page are not the same measurement**, and the
difference is worth stating rather than smoothing over. The table above is the
*test binary* with `GOMAXPROCS=4`; the prose below was measured by hand against
the *real binary* on an unpinned laptop, where the scheduler allocates more
per-P structures. Expect the hand figures to sit a few megabytes higher. Both
are honest; only the first is reproducible, which is why it is the one a
ceiling is attached to.

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

## Startup

`task bench` now measures it — `BenchmarkBootFullStack`, `BenchmarkBootTwoServices`
and a per-service breakdown — where before, the only startup figure anywhere was
the line above, hand-timed once.

**There are two startup numbers and they differ by four hundred times.** Which
one you get depends only on whether the data directory already exists.

| | |
|---|---|
| **First run in a project** — creates sixteen databases | **225 ms** |
| **Every run after** — the files are there | **0.6 ms** in-process, **27 ms** as a binary |
| `--services sqs,s3`, first run | 27 ms |
| One stateful service, first run | ~13 ms |
| STS, the one stateless service | **0.11 ms** |

**Cold start is bbolt; warm start is not.** Creating a database costs ~13 ms and
sixteen of them is 208 of the 225 — STS, which keeps nothing on disk, starts
120× faster than its neighbours, and that is the control that makes it a
finding. But *opening* sixteen existing databases takes **441 µs in total**, so
none of the recurring cost was ever storage.

It used to be 96 ms anyway, and that was `schemaver.Ensure`: a write
transaction per service at startup, and bbolt commits a meta page and fsyncs on
every writable transaction whether or not anything changed. Sixteen services
each paid a disk flush to be told their schema version was already right — 85%
of a warm boot. It reads before it writes now, and the recurring number went
from 96 ms to 0.6.

The lesson is in the measurement rather than the fix: the first benchmarks all
used a fresh directory per iteration, which answers "how long to create a stack"
when the question was "how long to start one". Both are worth knowing and only
one of them happens more than once.

What follows: warm start is fast enough to launch a stack per test run without
thinking about it, `--services` is still worth reaching for on a cold first run,
and the remaining 27 ms of a real warm start is process start rather than
anything in this repo. The disk shape is unchanged — every stateful service
writes a 131,072-byte file before it is asked for anything, so an untouched
seventeen-service data directory is about 2.1 MB.
