# The protocol every service goes through

Seven steps, in this order, for every service. The service documents in
[services/](services/) fill in the specifics; this page is what they all share.

## The evidence standard

Every statement about how AWS behaves carries where it came from. There are
exactly four sources, and they are not interchangeable.

| Source | Meaning | Good enough to |
|---|---|---|
| `model` | AWS's published service model (Smithy): shapes, `@range`, `@length`, `@pattern`, `@enum`, required members, error shapes. Generated into each service's `validate.go` by `cmd/dzaudit`. | Enforce a constraint and name its error. |
| `recorded` | A conformance scenario run against a real account, stored in `conformance/snapshots/`. | Settle a question. This is the oracle. |
| `doc` | AWS's API reference or user guide, with the page named. | Implement against, with the claim queued for recording. |
| `memory` | Somebody (a person or a model) recalls it. | Nothing. It goes on the recording queue and is not acted on until it is `doc` or `recorded`. |

Every statement about how doze-aws behaves is `run` (reproduced against a built
binary, or held by a test) or `read` (from the code). A `read` finding is
reproduced before it is fixed.

## The seven steps

### 1. Contract

List every operation in the service's AWS model and give each one a tier.

- **F, functional.** It works locally and its effect is observable.
- **C, cosmetic.** Accepted and round-tripped, with no local effect. The ledger,
  and the console wherever the setting is shown, say so.
- **S, stub.** Refused honestly, the same way in every service: HTTP 501, the
  code `NotImplemented` in the protocol's own error envelope, a message naming
  the operation and why it is not built, and an `X-Doze-Not-Built` header
  (decision D5). Listed in [`../not-built.md`](../docs/not-built.md) as
  declined or deferred. Never `InvalidAction`, "unknown path" or a validation
  error: the operation exists and the caller's input is not wrong.

**Read the model's own documentation first.** `.audit-models/<service>.json`
carries, in prose, the limits and ranges the traits do not (the member and
operation `documentation` strings), the wire name of every flattened list and
map (`xmlName`, `xmlFlattened`), and the error shapes with the code and status
each protocol gives them (`awsQueryError`, each operation's `errors`). It is
the current statement of the API and it is on disk. SQS's first pass wrote its
limits from memory and the older developer guide: 14 of its 24 findings were
found only by reading this text afterwards, among them a limit four times too
small, a feature refused that AWS accepts, and a whole wire protocol that
refused valid requests. A claim from the model's text is `model`; `doc` is
for what is read from a named documentation page; `memory` is for the rest.

Nothing may be absent, and nothing may be silently wrong: an operation that
returns success and does nothing is C only if it says so, otherwise it is a bug.
The contract is checked in as the service's section of
[`../SUPPORT.md`](../docs/SUPPORT.md), which the ledger tests already read.

### 2. Red first

Every finding in the service document becomes a failing test before any fix,
in the lowest layer that can show it: a store or handler test, an SDK test, a
conformance scenario, or a browser test. A finding nobody can make fail is not
a finding yet; it goes back to "unverified" with what was tried.

### 3. Fix the API

Make the tests pass in the service package. This includes model-derived
validation with `knownGaps` empty, hand-derived rules where the model is silent
(each with its `doc` source), error codes on every protocol the service speaks,
pagination, idempotency, tags, the resource policy, and state that survives a
restart.

### 4. Prove it

The gates, all of which must be green:

| Gate | Command | What it shows |
|---|---|---|
| Unit, race, vet, model parity, SDK | `GOWORK=off go tool task check:full` | The package is correct against the Go SDKs and the model. |
| Coverage floor | `go tool task coverage` | The floor in `testdata/coverage.txt` does not fall. |
| Mutation | `go tool task mutate` on the service package | The tests notice when the code is wrong, not only when it runs. |
| Conformance | `go tool task test:conformance` | boto3 can drive it, and it matches the recording where one exists. |
| Weight | `go tool task lightness` | Idle heap, binary size and the module list did not move. |
| Public surface | `go tool task api` | No export appeared or vanished by accident. |

### 5. Build the console

Implement the service's page group from the design, in the templates and
handlers the service document names. Every screen the document lists exists;
every `new` item is built from the data it says it is built from; every teaching
line comes from the validation table or the ledger, not from prose typed into a
template.

Gates: `go tool task test:e2e` with the route gate on (every route driven, or
`e2e/route-gaps.txt` says why), and the accessibility sweep (`e2e/audit/crawl.ts`)
clean on the service's pages.

### 6. Ledger and documents

`SUPPORT.md` matches the contract, including "Differences from AWS" and
"Verified against". `not-built.md` reconciles. `CHANGELOG.md` names each
behaviour change. The service document's findings table has every row closed or
moved to "owed to integration" or "recording queue" with a reason.

### 7. Hand over

Commit to main, CI green on every workflow. Update the status table in
[README.md](README.md). Record the actual time against the estimate.

## Acceptance criteria common to every service

A service is done when every box here and every box in its own document is
ticked. "N/A" is allowed only with a sentence saying why.

### Contract

- [ ] Every operation in the AWS model has a tier in `SUPPORT.md`, and the
      ledger tests pass.
- [ ] Every C row is visible as cosmetic wherever the console shows that setting.
- [ ] Every S row answers 501 `NotImplemented` with the `X-Doze-Not-Built`
      header, in the service's own error envelope, and appears in
      `not-built.md` as declined or deferred. The foundation's model sweep (B6)
      holds this for every operation in the model.

### Behaviour

- [ ] Model-derived validation: every constraint the model states is enforced,
      `knownGaps` is empty, and the rejection-parity test asserts the error
      **code**, on every protocol the service speaks.
- [ ] Hand-derived rules: each has a `doc` source written beside it and a test.
- [ ] Nothing AWS refuses is accepted. Each "accepted where AWS refuses" finding
      is closed, or is on the recording queue as `memory` with the test written
      and skipped pending the recording.
- [ ] Every list operation paginates as AWS does, or the ledger says why not.
- [ ] Create, update and delete are idempotent or refuse, as AWS does, for a
      repeat, a missing target and a conflicting target.
- [ ] Tags: add, remove, list, the per-resource limit and the key and value rules.
- [ ] The resource policy, if the service has one, is validated when stored,
      evaluated under soft and enforce, and a document that cannot be parsed is
      refused rather than stored and ignored.
- [ ] State survives a restart: a data-dir test writes, restarts and reads.
- [ ] No identity is hard-coded: the service's tests pass under a non-default
      `--account-id` and `--region`.

### Proof

- [ ] Every finding in the service document has a test that failed before its fix.
- [ ] All six gates in step 4 are green.
- [ ] Mutation: no surviving mutant in the service package that a reasonable
      test would kill; survivors that are left are listed with a reason.
- [ ] Conformance: a boto3 scenario covers every F operation family, and the
      run reports zero "answered differently". The unverified count is written
      in the service document, with the scenarios queued for recording.

### Console

- [ ] Every board in the service's page group is implemented, or the document
      says which were dropped and why.
- [ ] Each screen's refusal states show the real code and message, in the
      vocabulary from the foundation (Refused, Denied, Not implemented, Failed).
- [ ] Every destructive action confirms, and each confirm's sentence is true.
      (The findings include several that were not.)
- [ ] Every settings field carries the teaching lines, generated from the
      validation table and the ledger.
- [ ] Every `new` item works from the data its board names.
- [ ] No console text contradicts the ledger. Every hint in the service's
      templates was read against the code.
- [ ] The CLI preview on a form states exactly the call the form makes.
- [ ] `test:e2e` passes with the route gate on; the accessibility sweep is
      clean on the service's pages; keyboard paths (`j` `k` Enter `/` `c` Esc)
      work on its list and forms.
- [ ] The service's pages work under a non-default account and region.

### Close

- [ ] `SUPPORT.md`, `not-built.md`, `CHANGELOG.md` updated.
- [ ] Findings table: every row closed, or moved with a reason.
- [ ] "Owed to integration" and "Recording queue" are complete enough that
      somebody else could pick them up.
- [ ] Committed to main, CI green, status table updated, actual time recorded.

## Severity

| | Meaning |
|---|---|
| **P0** | Changes what a deploy or a running app sees: wrong data, a silent no-op, a failure AWS would not produce, or a success AWS would refuse that hides a real bug in the user's code. |
| **P1** | A parity gap an SDK caller can observe, with no data wrong. |
| **P2** | The console shows something false, loses data on a round trip, or cannot do what the API can. |
| **P3** | Wording, dead copy, a stale comment or document. |

Every finding is fixed; none is dropped for being small (decision D2). P0 and
P1 are never deferred out of a service. P2 and P3 may move to a later service
only when the fix lives in a file that service owns, and they are closed there.

## The shape of a service document

Every file in [services/](services/) has these sections, in this order, so any
two can be read side by side.

1. **Summary** — what the service is here, its size, its peers.
2. **Contract** — the operation table with current and target tier, and what
   is declined.
3. **Where it stands** — the tests, scenarios and browser specs that exist.
4. **Findings** — the table: id, severity, what, where, evidence, AWS source,
   the test to write first.
5. **What to check** — the service-specific checklist, operation family by
   operation family, beyond the common criteria.
6. **Console** — each board, the template and handler it lands in, the `new`
   items and what they are built from, and where each teaching line comes from.
7. **Tests to add** — by layer.
8. **Recording queue** — claims about AWS that are `doc` or `memory`.
9. **Owed to integration** — what needs a second service to be true.
10. **Acceptance criteria** — the service's own boxes, on top of the common ones.
11. **Files and order of work** — the breakdown, and prior art where another
    emulator or AWS's own documents solved the same problem.
