# Contributing to doze-aws

doze-aws is local AWS as one static binary: seventeen services, no Docker, no
cgo. That constraint decides most of what follows.

## The loop

```sh
go tool task check        # ~60s — gofmt, vet, deadcode, -short tests
go tool task check:full   # ~6 min — the above plus -race ./...  (what CI runs)
```

`task` is a Go tool dependency, so there is nothing to install. `go tool task
--list` shows everything.

**`task check` before you push, `task check:full` before you claim it works.**
The split is deliberate: `check` used to run the race suite, which is five to
six minutes before every push — long enough that people stop running it. The
fast gate still catches the things that are cheap to catch, and CI runs the full
one on every push and pull request.

Wire the fast one as a hook once:

```sh
go tool task hooks:install    # .git/hooks/pre-push -> task check
```

### GOWORK=off

This module lives inside a Go workspace, so **every** `go` command needs
`GOWORK=off`. The Taskfile sets it globally; if you run `go` directly, set it
yourself or you will get confusing resolution errors:

```sh
GOWORK=off go test ./sqs/
```

### The rest

```sh
go tool task test:e2e     # Playwright against the console (builds + boots a real binary)
go tool task bench        # benchmarks for the hot paths; BENCHTIME=5s to lengthen
go tool task fuzz         # 30s on the signature parser
go tool task soak         # 2m mixed-service load; SOAK_CHAOS=1 restarts mid-load
```

For the e2e suite, note `bunx --bun` rather than plain `bunx`: the Playwright
binary carries a `#!/usr/bin/env node` shebang and plain `bunx` honours it,
which fails on a machine where `node` is a version-manager shim with no version
pinned. `--bun` runs it under bun instead. Use bun, not npm or Python, for
anything in `e2e/` and `demo/`.

To see the thing running with something in it:

```sh
go tool task build && ./bin/doze-aws &
cd demo && bun install && bun seed.ts --fast
```

`demo/seed.ts` drives 18 real SDK clients across every service and exits
non-zero on failure, which makes it a decent smoke test as well as a way to fill
a console for screenshots. Its four Lambda functions are one runtime each
(Node, Python, Ruby, provided), so they need those interpreters on PATH.

## Adding a service

A service is not just its package. Eight places have to agree, and nothing
enforces the list except the tests at the end of it:

| Where | What |
|---|---|
| `<service>/` | the package: `New(Options) (*Server, error)` returning an `http.Handler` + `io.Closer` |
| `dozeaws.go` | `Implemented`, and a `case` in `Stack.build` |
| `internal/gateway/gateway.go` | `Services`, and whichever routing rule finds it — `targetPrefixes` for a JSON protocol, `scopeServices` for the signature scope |
| `cmd/doze-aws/env.go` | `signingNames`, so `doze-aws env` prints the right endpoint variable |
| `console/catalog.go` | `catalog`, so it appears in the console nav |
| `<service>/coverage_test.go` | every operation in `testdata/ops_<model>.json` is handled, refused by name, or a written-down gap |
| `<service>/rejection_parity_test.go` | that a refusal matches what AWS would say |
| `cmd/dzaudit` | the model-derived input-validation audit |

Two ratchets will tell you if you miss one. `console/coverage_test.go` fails
when an operation stops being reachable from the console — including when you
delete a backend method nothing called. `TestEveryServiceHasConcurrentCoverage`
in the root package fails until the new service is driven by one of the two
concurrency stress tests, because the race detector only reports what actually
runs in parallel.

## What the tests are for

- **Contract tests** drive the real AWS SDK against the service. They are the
  ones that catch a wire-shape mistake, and they are gated off by `-short`.
- **`coverage_test.go`** per service holds the dispatch table against the
  operation list `dzaudit ops` derives from AWS's own model, committed as
  `testdata/ops_<model>.json` and regenerated weekly by CI. An operation with
  no handler, no named refusal and no entry in `Unreached` is a failure, not a
  TODO — and an operation AWS adds becomes one without anybody running a tool.
- **`rejection_parity_test.go`** checks that when doze-aws refuses something,
  it refuses it the way AWS does — same code, same status. A local emulator
  that accepts what the service rejects is worse than one that is missing the
  operation, because it fails on deploy instead.
- **Concurrency stress** (`stress_test.go`, `stress_rest_test.go`) runs every
  service from many goroutines under `-race`.
- **e2e** (`e2e/`) drives the console in a browser.

### When the budget fires

`task lightness` holds what doze-aws weighs against `testdata/lightness.json` —
binary bytes, embedded assets, the linked module set, per-service goroutines
and disk, and the footprint of a stack at rest. CI runs it. It is deliberately
**not** in `task check`, because a budget that runs on every pre-push is a
budget people learn to skip.

A failure is not a wall. It is asking which of two things happened:

1. **You meant it.** A new asset, a service that starts a goroutine, a
   dependency worth its size. Run `task lightness:update`, **read the diff**,
   and commit it alongside the change that caused it with a line saying why.
   The recorded number moving is the record; that is what the fixture is for.
2. **You did not.** Then the number is telling you something, and the failure
   names which: `TestOnlyTheDeclaredModulesAreLinked` names a module by path,
   `TestEmbeddedTreesFitTheirBudget` names the embed site,
   `TestEachServiceCostsWhatTheBudgetSays` names the service.

Two things worth knowing before you reach for `lightness:update`:

- **Exact numbers re-derive in both directions; observations only widen.** An
  embedded tree or a data directory is identical on any machine, so its ceiling
  follows its measurement down as well as up. Heap, RSS, CPU and boot do not —
  re-deriving those from one quiet run would ratchet the band down and fail on
  an ordinary one.
- **Never widen a ceiling by hand to make a failure go away.** If the growth is
  wanted, record it; if it is not, fix it. A ceiling edited in the same commit
  as the thing that breached it is how a budget stops meaning anything.

Adding a new budget: put the test's name in `BUDGET_TESTS` in `Taskfile.yml`.
That is the only place the list lives — CI runs `go tool task lightness` rather
than keeping its own copy.

### If you fix a bug, break it again first

Every correctness fix in this repo is expected to come with a test, and the test
is expected to have been **seen to fail**. Write the fix, write the test, then
undo the fix and watch the test go red — and check the undo actually applied,
because a sabotage that silently did not modify the file looks exactly like a
passing test. This is not ceremony; it is the only way to know the test is
attached to the thing you think it is.

## House style

- **Comments say why, not what.** The code says what. A comment earns its place
  by recording a decision, a constraint, or a trap — ideally one someone already
  fell into.
- **Match the file you are in.** Comment density, naming, and structure vary by
  package on purpose.
- **No new dependencies without a reason that survives being said out loud.**
  The binary is static, `CGO_ENABLED=0`, five platforms. A dependency that
  breaks any of that needs to buy a lot.
- **Errors a user sees are part of the product.** A refusal should say what was
  wrong and, where it is knowable, what would have worked.

## Where things are

| Path | |
|---|---|
| `cmd/doze-aws/` | the binary: flags, config, DNS names, `env`, `doctor` |
| `cmd/dzaudit/` | model-derived input-validation audit |
| `<service>/` | one directory per AWS service |
| `internal/gateway/` | routes a request to a service by target, signature scope, path or shape |
| `internal/awsjson/`, `awsquery/`, `rpcv2cbor/` | the wire protocols |
| `internal/ddb/` | DynamoDB's expression language and item model |
| `internal/asl/` | the Amazon States Language interpreter |
| `console/` | the web console (server-rendered, htmx) |
| `docs/` | per-service operation tables, and the decision records |
| `e2e/`, `demo/` | Playwright suite, and the SDK-driven seeder |

`docs/storage.md` is worth reading before touching persistence: it records why
this is bbolt, what was measured, and — since the scan benchmarks landed — which
part of that argument turned out not to hold.
