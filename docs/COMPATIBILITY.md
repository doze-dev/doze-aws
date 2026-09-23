# What 1.0.0 promises

doze-aws follows [semantic versioning](https://semver.org). This page says what
that covers, surface by surface, because "1.0" on its own does not tell you
whether your `eval "$(doze-aws env)"` or your log-scraping wrapper is safe.

Within 1.x, everything below keeps working. Breaking any of it means 2.0.0.

Everything on this page is enforced by a test, and where it is, the test is
named. A promise nothing checks is a sentence, not a contract.

## The Go API

**Covered:** every exported symbol in
[`testdata/api.txt`](../testdata/api.txt) — 370 of them, across 22 packages.
Function signatures, struct fields, interface methods, constants.

`TestThePublicAPIIsWhatWeSaidItWas` compares the tree against that file on
every run, so a new export is a failing test rather than an accident, and a
removed one cannot happen quietly. `task api` prints the surface; `task
api:update` re-records it.

Adding an export is a minor release. Removing or changing one is 2.0.0.

**Not covered:** anything under `internal/`, which Go itself prevents you from
importing. That includes the console, the CloudFormation transpiler, the
declarative `provision` model, every service's storage layer and the data
migration — all of which were exported before 1.0.0 and are not now.

The shape to rely on is the one [embedding.md](embedding.md) documents: a
service is `New(Options) → *Server`, which is an `http.Handler` and an
`io.Closer`, and `dozeaws.NewStack` assembles them.

## The CLI

**Covered:** the subcommands in [cli.md](cli.md) — `dns-setup`, `doctor`,
`env`, `apply`, `export`, `version`, `config print` — their flag names, and the
shape of what they write to stdout.

`doze-aws env` in particular: it prints a shell block of `export` lines that
`eval` accepts, carrying the endpoint, region, credentials and the per-service
`AWS_ENDPOINT_URL_*` hostnames. New variables may appear; the ones there now
keep their names and meanings.

**Not covered:** the exact prose of human-facing output — the startup summary,
`doctor`'s report, error wording. Parse `env`, not the banner.

## Logs, and the one line to parse

**Covered:** logs go to **stderr** in logfmt, and `msg=listening` is emitted
once the server is accepting connections. The e2e suite waits on it and so does
anything wrapping this binary; `cmd/doze-aws/firstrun_test.go` fails if it
disappears.

The startup summary a person reads goes to **stdout** instead, so
`doze-aws 2>/dev/null` leaves just the summary and a log parser sees only logs.

**Not covered:** the other log lines, their fields, or their order.

## The console

**Covered:** the console is served under the **`/_console`** prefix, chosen
because it can never collide with a valid S3 bucket name. Tooling may rely on
that prefix and on `--console=false` turning it off.

**Not covered:** the HTML, the routes beneath the prefix, and the JSON of
`/_console/api/*`. The one exception is `/_console/api/glance`, which the
sibling `doze` dashboard decodes: fields may be **added**, never changed or
removed, and a decoder that ignores unknown fields stays working.

## The data directory

**Covered:** the layout, so a directory written by 1.x is readable by every
later 1.x.

```
<data-dir>/
  .gitignore        # written once, contains *, so local state is never committed
  instance.json     # {"account":…,"region":…,"created":…}
  <region>/<service>/…
  _global/<service>/…   # iam and sts, which are genuinely region-less
```

`instance.json` records the account and region the stored bytes belong to, and
booting against a directory stamped with a different account fails with
`dozeaws.ErrAccountChanged` rather than silently serving the wrong ARNs.

The on-disk format carries a schema version — `internal/schemaver`, currently
**v1**, stamped in every store. A database from a newer binary is refused
rather than opened hopefully. If a 1.x release ever needs v2 it will migrate
v1 forward automatically and say so before it does; a release that could not
migrate would be 2.0.0.

Deleting the directory resets everything, and is always safe.

**Not covered:** the bbolt files themselves — bucket names, key encodings, the
value formats. Read them through the AWS APIs.

## Configuration

**Covered:** `doze-aws.toml`, auto-loaded from the working directory or named
with `--config`, and its precedence: **defaults → config file → flags**, lowest
to highest. Paths inside the file resolve against the file, not your shell,
which is what makes a checked-in config work from any directory.

Keys that exist keep their names and meanings. New ones may be added, and every
key stays optional.

## The `.doze` hostname contract

Stated in full in [endpoints.md](endpoints.md), and promised:

- **`aws.<instance>.doze`** and the AWS-shaped hostnames beneath it.
- **`--listen host:port`** serves exactly what you ask for, with no DNS.
- **Every URL is minted from the request's `Host`**, so a queue URL you read
  back is reachable the way you reached the server.

Not promised, and not a default: `127.0.0.1:4566` as the address doze-aws binds
on its own, or `aws.doze` as a machine-wide name.

## What each operation does

[SUPPORT.md](SUPPORT.md) records every operation at one of three tiers —
functional, cosmetic round-trip, or a stub that refuses cleanly. Within 1.x an
operation does not move **down** a tier: something functional does not quietly
become a cosmetic round-trip. Moving up is a minor release, and the 81 stubs
each carry an argument in [not-built.md](not-built.md).

The tiers are checked rather than asserted: the parity suites replay every
constraint AWS's own service models describe, and the totals in SUPPORT.md are
compared against what those suites measure.

## What is deliberately not promised

- **Performance numbers.** [performance.md](performance.md) is measured and
  gated so it cannot rot, but a budget is not a guarantee.
- **The binary's size**, beyond it staying small enough to be the argument it
  is.
- **Windows.** The `.doze` setup is Unix-only, so there are no Windows builds.
- **Being LocalStack.** Where doze-aws differs from AWS on purpose, the service
  section in SUPPORT.md says so under "Differences from AWS".
