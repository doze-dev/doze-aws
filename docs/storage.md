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

The write cost people notice is **fsync, not the engine**. The batch operations
now share one, which was worth more than the engine swap would have been.

SQLite was asked about separately and measured the same way: pure-Go SQLite adds
**4.40 MiB and nine modules**, Turso's embedded driver ships a native library
per platform, and its pure-Go drivers are remote-only. See
[SQLite and Turso](#sqlite-and-turso-asked-and-answered). Startup looked like
bbolt's one visible cost and turned out to be two different costs wearing one
number, both now gone: a schema-version write that fsynced per service to
confirm nothing had changed, and sixteen databases created at boot whether or
not anything used them. The binary starts in **~20 ms on a fresh data
directory**, down from 790 ms, and leaves **six files** behind instead of
seventeen. A database is created the first time its service is asked for
something.

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

### The scan row overstates what it buys

The 55.9 µs above is the ENGINE scanning 10,000 keys. It is not what a
`Scan` costs. `internal/ddb/store`'s `BenchmarkScan10k` measures the operation
— the same cursor walk, plus decoding each record into an `item.Item`:

| | per operation | allocations |
|---|---|---|
| `Scan`, 10,000 items, no filter | **24.6 ms** | 467k (≈47 per item) |
| `Scan`, 10,000 items, filtered | **27.0 ms** | 473k |
| `Query`, one hash key (~100 of 10,000) | **0.36 ms** | 6.7k |

So the engine's 55.9 µs is about **0.2%** of a 24.6 ms Scan. The other 99.8% is
per-item decode. Pebble's 688 µs would have been ~2.7% — worse, but not
visibly: 25.2 ms against 24.6 ms is not a difference a person notices.

This is the same lesson as the write path, on the other side. The write cost
people notice is fsync, not the engine; the scan cost people notice is the
per-item decode, not the engine. **"bbolt wins scans by an order of magnitude"
is true of the engine and nearly irrelevant to the operation**, and it should
not be read as one of the load-bearing reasons for the decision.

What is still load-bearing is the per-instance overhead (~50 open stores) and
the point-read advantage, neither of which this changes. The conclusion holds;
one of its four pillars does not.

The filter costs ~240 ns per item (2.4 ms across 10,000), which matches
`internal/ddb/expr`'s own `BenchmarkEvalCondition` — the two benchmarks measure
the same work at different scopes and agree, which is the cross-check that
makes either believable.

## The write cost is fsync, and bbolt can fix it

The 7,526 µs above is not bbolt being slow. It is one `fsync`, and it matches
the measured end-to-end cost of `SQS SendMessage` (7.5 ms) almost exactly.
Pebble's 3,487 µs is also an fsync — of a smaller WAL append.

### One transaction per batch operation — done

`SendMessageBatch` used to loop over the single-message store method, so ten
messages opened ten transactions:

| | per op |
|---|---|
| A 10-item batch as 10 transactions | 76.1 ms |
| The same 10 in one transaction | **7.55 ms** |

The three SQS batch operations now each run in one transaction. A ten-item
batch costs what a single send costs, because it is one fsync either way — a
9.4× improvement on `BenchmarkRequestSendMessageBatch`, and larger than
anything the engine swap offered.

### `DB.Batch` — measured, and deliberately not used

bbolt can coalesce *concurrent* callers into one transaction. It was measured
and rejected, which is recorded here so it is not rediscovered as an
obvious win:

| | 1 caller | 16 concurrent |
|---|---|---|
| `DB.Update` (what we do) | **6.95 ms** | 122.9 ms |
| `DB.Batch`, 10 ms default delay | 19.0 ms | 19.1 ms |
| `DB.Batch`, 100 µs delay | 8.31 ms | **8.39 ms** |

`DB.Batch` waits up to `MaxBatchDelay` for other callers to join, and a lone
caller pays that wait for nothing. Tuned down to 100 µs it is 14.6× faster
under concurrency — and still **20% slower for a single caller**, which is the
case [performance.md](performance.md) actually documents: a test sending a
thousand messages one at a time.

It also carries a correctness constraint. The function may run more than once,
so any side effect outside the transaction must be idempotent — that needs
checking per call site rather than being applied across the tree.

And 122.9 ms for 16 concurrent writes is not pathological: it is 16 × 7.7 ms,
the fsync cost serialised. Concurrency does not make doze-aws slower today; it
simply does not make it faster.

Against all of this, pebble's 2.2× on durable writes is the smaller prize.

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

## SQLite and Turso, asked and answered

*Recorded September 2026.* The question came up directly — bbolt underpins a lot
here, is something newer better? — so it was measured rather than argued about.

### The criteria, written before measuring

An alternative has to:

1. keep the **pure-Go static cross-compiled binary**. Five platforms from one
   `go build`, no toolchain per target, no runtime dependency to install.
2. add **no more than ~2 MB**, against a **21.5 MiB** stripped binary
   (linux/amd64, the target `testdata/lightness.json` pins and
   `binarysize_test.go` gates; the 20.4 MiB this criterion was first written
   against was darwin/arm64, which is why the target is named now).
3. not regress **write latency** on the paths `## The numbers` already measures.
4. serve the existing access patterns: many small keyspaces, read- and
   scan-heavy, one writer.

The first two are the ones that decide it, and neither needs a benchmark.

### What the options actually are

| | embedded? | pure Go? | cost |
|---|---|---|---|
| `modernc.org/sqlite` | yes | yes | **+4.40 MiB, +10 modules** |
| Turso `tursogo` | yes | no CGO, but **ships prebuilt native libraries** | a per-platform binary blob |
| Turso `tursogo-serverless`, `libsql-client-go` | **no — remote only** | yes | needs a server |

A correction worth recording, because the first draft of this section asserted
it the other way: **Turso's embedded driver does not use CGO.** It uses purego
FFI. That is genuinely clever and it does not help here — it still means
shipping a native library per platform, which is the same promise broken by a
different mechanism. "One static binary" is not a CGO claim, it is a
*self-contained* claim.

The remote drivers are pure Go and are remote: they need a libSQL server, which
is a container beside the tool whose entire pitch is not needing one.

That leaves `modernc.org/sqlite`, which is genuinely pure Go and genuinely
embedded. Measured, both binaries `-trimpath -ldflags="-s -w"`:

```
bbolt only                1,924,994 bytes
bbolt + modernc/sqlite    6,536,722 bytes     +4,611,728  (+4.40 MiB)
```

It also pulls in **nine new modules** — `modernc.org/libc`, `memory`, `mathutil`
and `sqlite`, plus `go-humanize`, `uuid`, `go-isatty`, `go-strftime` and
`bigfft`. (Ten arrive; `golang.org/x/sys` is already linked through bbolt.) The
six modules that reach the binary today would become fifteen, and the binary
would grow by **21%** of its current size.

**It fails criterion 2 by more than double, and criterion 1 in every variant
that would actually be adopted.** Write latency was never reached.

### The honest part: what is bbolt actually costing?

A spike that only measures the alternative is half a spike.

**The single-writer constraint has not bitten.** It is visible in the design —
`Regions` makes every region share one `_global` store for IAM and STS, because
a second opener on one file blocks — but each service has its own database, so
writes serialise per service rather than globally, and nothing in the soak or
the simulation has contended on it. It is a constraint that has been worked
around once, cheaply, and has produced no measured problem since.

**Startup was the one place it visibly cost** — and chasing that number found
the cost was not bbolt at all.

The first measurement said a full stack boots in 220 ms, a stateful service
takes ~13 ms and STS, which keeps nothing on disk, takes 0.11 ms. The obvious
reading is "startup is bbolt", and it was written down that way. It was wrong,
because every one of those benchmarks created its databases from scratch and
**that is the first run in a project, not the recurring cost**:

| | first measurement | after the fsync fix | after lazy opening |
|---|---|---|---|
| Cold boot — a fresh data directory | 225 ms | 225 ms | **2.8 ms** |
| Warm boot — a directory already used | 96 ms | 0.6 ms | **0.3 ms** |
| Real binary, cold, process start included | 793 ms | 793 ms | **~20 ms** |
| Untouched data directory | 2.1 MB, 17 files | same | **158 B, 3 files** |

**The warm number was the fsync.** Opening sixteen existing bbolt files takes
**441 µs, total**, so the 96 ms was never the open — it was `schemaver.Ensure`
taking a write transaction per service, and bbolt commits a meta page and
fsyncs on every writable transaction whether or not anything changed. Each
service paid a disk flush at startup to be told its schema version was already
correct. One service: 5.92 ms of a 6.98 ms boot, 85%. It reads before it writes
now — same validation, same errors, same stamping on a fresh or unversioned
database, minus the write transaction that confirmed nothing needed writing.

**The cold number was creation, and that is what lazy opening removed.** A
writable transaction on a file that does not exist yet costs milliseconds, and
standing a service up took three of them: create the file, stamp the schema,
create the buckets. Sixteen services, ~13 ms each. `internal/lazybolt` changes
the rule to *a database that does not exist is created on first use* — so a
developer who touches SQS and S3 pays for SQS and S3, and the other fifteen
services cost nothing but the memory their handlers occupy.

The rule is deliberately about **the file, not the service**: a database that
is already there still opens at boot, because it costs 27 µs and because every
error worth failing on — a bad permission, a failed lock, a schema written by a
newer binary — belongs at startup rather than in the middle of someone's first
request. A file that does not exist has no schema to migrate and no corruption
to find, so deferring it defers nothing but the cost. It also means the data
directory answers the question by itself: **the databases that exist are the
services in use.**

Lambda's three runtime clients are deferred the same way. They are 19.9 KB of
bootstrap scripts written where a function process can read them
(`LAMBDA_RUNTIME_DIR`), and a stack that never invokes a function has no use for
them, so they are written on the first invocation instead of at boot.

What is left in a data directory nobody has used is **three files, 158 bytes**:
`instance.json` (94 B) and the two encryption keys SSM and Secrets Manager
generate (32 B each). Those stay eager because they are keys — an absent key is
not equivalent to an empty one the way an absent database is, so deferring them
would be a different change with a different argument.

**One cost moved rather than vanished.** The console's rail counts fan out
across every service, so loading it once creates all sixteen databases: 259 ms
on that first load, 14 ms on every one after. That is the same work, relocated
from before the shell prompt returns to inside a page load that is already
asynchronous, and a developer who never opens the console never pays it.

Reproducible: `BenchmarkBootWarm`, `BenchmarkBootFullStack` and
`BenchmarkBootPerService` (`task bench`), `task lightness`, and
`TestAnUntouchedStackCreatesNoDatabases` / `TestUsingOneServiceCreatesOnlyItsOwnDatabase`,
which are what stop this regressing.

### The decision

**Stay on bbolt.** Not because the alternative is bad, but because the two
criteria that decide it are the two this project is named for, and SQLite fails
both. A 4.40 MiB, ten-module dependency to fix a constraint that has not yet
cost anything would be trading a measured property for a hypothetical one.

The decision is enforced rather than remembered. Adding `modernc.org/sqlite` to
`cmd/doze-aws` was tried, and `TestOnlyTheDeclaredModulesAreLinked` in
`lightness_test.go` failed immediately, naming all nine new modules — one error
line each, in a diff. Someone can still decide to take the trade; they cannot
take it by accident, and they cannot take it without the number being visible.

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
- **Startup mattering more than size.** This was the open question and it is
  closed: the binary starts in ~20 ms on a fresh data directory and storage is
  not a measurable part of it. Both halves were fixed by not writing —
  `schemaver.Ensure` reads before it writes, and `internal/lazybolt` creates a
  database on first use. Neither moved an error or a migration to the first
  request, which was the condition this would have failed on: a database that
  already exists is still opened at boot.
- **The single-writer constraint actually biting.** It has not. If two processes
  needing the same data directory, or cross-service transactions, became real
  requirements rather than hypotheticals, the calculus changes — and the cost of
  the change is now a known number rather than a guess.

## Reproducing this

The benchmarks live outside the repo because they need pebble, which is not a
dependency. To re-run: a module importing `github.com/cockroachdb/pebble/v2`
and `go.etcd.io/bbolt`, with benchmarks for single-op writes at both durability
levels, point reads, a 10,000-key scan, and — the important one — a test that
opens 48 of each and reports heap and goroutine deltas.

The COMPARISON benchmarks need pebble; the doze-aws side of every claim is now
measurable in-repo, which is what lets a future reader check this record rather
than take it:

| claim | in-repo benchmark |
|---|---|
| the write cost is fsync | `BenchmarkRequestSendMessageBatch` (`task bench`) |
| scan cost, and what the engine is worth in it | `internal/ddb/store` — `BenchmarkScan10k`, `BenchmarkScanFiltered10k`, `BenchmarkQuery10k` |
| per-item filter evaluation | `internal/ddb/expr` — `BenchmarkEvalCondition` |
| the per-request cost above the store | `internal/sigparse`, `internal/gateway` |
| boot is bbolt, not doze-aws | `BenchmarkBootPerService` (`task bench`) |
| what an untouched service costs on disk | `task lightness` → `portable.services` |

The SQLite size figures above are a two-file scratch module — a `main` importing
bbolt, then the same importing `modernc.org/sqlite` as well — built with
`-trimpath -ldflags="-s -w"` and compared with `stat`. Five minutes to redo when
somebody doubts it, which is the point of writing down the method rather than
the conclusion.

The scan benchmarks were added after this record was written, and they are what
produced the correction above: the claim about scans had rested entirely on an
out-of-repo measurement of the engine, with no benchmark of the operation it
was being used to justify.
