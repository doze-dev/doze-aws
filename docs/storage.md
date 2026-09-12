# Storage: why bbolt, and what it costs

doze-aws keeps its state in [bbolt](https://github.com/etcd-io/bbolt), an
embedded B+tree. This page is the decision record: what the alternatives were
measured at, where bbolt genuinely loses, and what would change the answer.

Everything below was measured on this codebase's access pattern rather than
taken from a general benchmark, because the general benchmarks answer a
different question — see [The shape that decides it](#the-shape-that-decides-it).

## The short version

bbolt stays, and not narrowly. The two things a log-structured engine is good
at — write throughput and reclaiming space — are respectively **not doze-aws's
bottleneck** and **not a problem doze-aws has**. The two things it is bad at —
per-instance overhead and scan cost — are exactly what doze-aws does most of.

The write cost people notice is **fsync, not the engine**, and it is
addressable within bbolt by more than switching engines would give.

## The shape that decides it

A storage comparison usually assumes one database. doze-aws has **sixteen** —
one per service — and since regions became folders, sixteen *per region*. A
three-region instance holds around fifty open stores.

That multiplies any per-instance overhead by fifty, and it is the number a
general benchmark never shows:

| 48 stores (three regions) | heap | goroutines |
|---|---|---|
| bbolt | +0.2 MB (5 KB each) | **+1** |
| pebble | +12.8 MB (273 KB each) | **+1153** |

1153 goroutines is roughly 24 per database — compaction workers, WAL writers,
flush schedulers. For one database that is unremarkable. For fifty it is the
whole idle budget of a product whose goal is to be invisible when nothing is
happening (see [performance.md](performance.md): 15 MB, 0.1 wakeups/second).

## The numbers

Apple M-series laptop, `-benchtime 2s`, values ~300 bytes of JSON — a queue
definition, a table schema, an IAM policy. Nothing here is a blob; S3 objects
are files on disk, not rows in the store.

| | bbolt | pebble | |
|---|---|---|---|
| Write, durable | 7,526 µs | 3,487 µs | pebble **2.2× faster** |
| Write, NoSync | 19.5 µs | 1.67 µs | pebble **11.7× faster** |
| Point read | 233 ns | 675 ns | bbolt **2.9× faster** |
| Scan 10,000 | 55.9 µs | 688 µs | bbolt **12.3× faster** |

Pebble wins writes, as an LSM should. bbolt wins reads, and wins scans by an
order of magnitude — it is an mmap'd B+tree, so a scan is pointer-walking
memory the kernel already has, with no merge across levels.

doze-aws is read- and scan-heavy. Every console page render fans out across
services; every `List*`, `Describe*` and `Scan` is a range read; the flow graph
crawls everything. Creates are comparatively rare — you make a queue once and
then use it.

## The write cost is fsync, and bbolt can fix it

The 7,526 µs above is not bbolt being slow. It is one `fsync`, and it matches
the measured end-to-end cost of `SQS SendMessage` (7.5 ms) almost exactly.
Pebble's 3,487 µs is also an fsync — of a smaller WAL append.

Two levers inside bbolt beat what the engine swap would buy:

| | per op | |
|---|---|---|
| 16 concurrent writers, `DB.Update` | 122.9 ms | each waits on its own fsync |
| 16 concurrent writers, `DB.Batch` | 19.1 ms | **6.4× faster** — they share one |
| A 10-item batch as 10 transactions | 76.1 ms | what `SendMessageBatch` does today |
| The same 10 in one transaction | 7.55 ms | **10× faster** |

Neither is in use yet. `DB.Batch` coalesces concurrent callers into a single
transaction and a single fsync — it is not a drop-in for `DB.Update`, because
the function may run more than once and so must be idempotent, but that is a
contained change. Making the batch API operations one transaction is the fix
[performance.md](performance.md) already names.

Against those, pebble's 2.2× on durable writes is the smaller prize.

## What bbolt actually costs

Honest limits, not a defence:

**One writer per database.** A write transaction excludes all others on that
file. This is real and it has bitten this codebase twice: a second process
opening the same file blocks on the flock *forever* rather than erroring, which
is why the region-less services are built once and shared
(`regions.go`). The mitigation is structural — sixteen databases means
contention is per service, so SQS traffic never blocks a DynamoDB write.

**The file never shrinks.** Freed pages go on a free list and are reused, but
space is not returned to the filesystem. This is bbolt's best-known weakness,
so it was measured on the workload that should trigger it — twenty rounds of
filling and draining a 2,000-message queue:

| after 1 round | after 20 rounds | |
|---|---|---|
| bbolt | 2.00 MB | 2.00 MB |
| pebble | 0.71 MB | 10.55 MB |

bbolt is **completely flat**: free pages are reused and the file plateaus.
Pebble grew 15× on a workload that ends empty every round, because deletes are
tombstones until a compaction runs. It would compact eventually — but a dev
tool that runs for an afternoon lives in the transient, not the steady state.

The folklore has this one backwards for short-lived processes.

**Write amplification.** bbolt writes whole 4 KB pages, so a one-byte change
costs a page. At doze-aws's volumes this is invisible next to the fsync.

## What pebble would cost

Beyond the goroutines:

| | bbolt | pebble |
|---|---|---|
| Hello-world binary | 2.94 MB | 25.79 MB |
| Modules in the graph | 1 | 135 |

doze-aws ships as one static binary of about 30 MB. Pebble would take it past
50 MB — a 77% increase — and bring in cockroachdb/errors, redact, zstd,
protobuf and the rest. For a tool whose pitch is "no Docker, no JVM, one
binary", that is a real cost rather than a rounding error.

## What would change the answer

Not rhetorical — these are the conditions under which this should be revisited:

- **One database instead of sixteen.** If every service's data moved into a
  single keyspace under key prefixes, pebble's overhead stops being multiplied
  by fifty and its write advantage starts to matter. The price is losing the
  per-service isolation that makes "delete a service's directory to reset it"
  work, and a shared write lock across all services.
- **Values getting big.** If something started storing megabyte values in the
  key-value store rather than on disk, bbolt's whole-page writes and mmap
  growth would start to hurt.
- **A write-dominated workload.** If doze-aws were used mainly to hammer
  millions of writes rather than to develop against, the balance inverts. Fix
  the batching first and re-measure before concluding it has.

## Reproducing this

The benchmarks live outside the repo because they need pebble, which is not a
dependency. To re-run: a module importing `github.com/cockroachdb/pebble/v2`
and `go.etcd.io/bbolt`, with benchmarks for single-op writes at both durability
levels, point reads, a 10,000-key scan, and — the important one — a test that
opens 48 of each and reports heap and goroutine deltas.

The in-repo benchmarks that matter for the fsync story are already there:
`task bench`, and `BenchmarkRequestSendMessageBatch` in particular.
