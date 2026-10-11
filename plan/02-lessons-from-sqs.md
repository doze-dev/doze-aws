# What went wrong in SQS, as questions every service answers first

Stage 1 closed 29 findings in SQS where the plan had listed 8, and then had to
rebuild six console boards that had been ticked done. Almost none of it was
hard. Nearly all of it was one of the twelve mistakes below, made because a rule
was written from memory instead of read, or because a thing was built from a
board's *words* instead of the board. This page is the list, so that the
service documents are corrected **before** their stages start and the same
mistakes are not made fifteen more times.

Each service document after SQS has a section "Pre-stage review" that answers
every question here for that service, with the finding ids it produced (written
2026-10-11; SQS's own sections 4 and 6a are the worked example it follows). A
stage does not start until that section is complete. See
[`services/sqs.md`](services/sqs.md) sections 4 and 6a for the worked example.

## The facts

**L1. Read the model's own documentation before writing any rule.**
`.audit-models/<service>.json` carries, in prose, the limits, ranges, lengths,
defaults and "can't"s that its traits do not. SQS was written from memory and
the developer guide: a limit four times too small, a feature refused that AWS
accepts, tags capped at a number the model calls "not recommended". Every
number, range, default, length, pattern, count and name in the service
document is checked against that text; the source tag becomes `model`.
`doc` is for a named documentation page actually read. `memory` is for the
rest, and nothing is fixed on `memory`.

**L2. Optional parameters have meanings.** "If you don't specify values for
this parameter, the request returns empty results" (`GetQueueAttributes`) was
answered here with everything. Read every operation's and member's
documentation for "if you don't", "optional", "default", "only", "at most",
"up to", "must", "can't", "requires", "unless", "applies only to". Each is a
rule with a direction (L4).

**L3. Error codes are in the model.** Each operation lists its `errors`; each
error shape carries its code and the HTTP status each protocol gives it
(`awsQueryError`). The default `InvalidParameterValue` was used where the
model names `InvalidMessageContents`, `ReceiptHandleIsInvalid`,
`ResourceNotFoundException`. For every refusal in the document, the code and
status are the model's, or the document says they are not established.

**L4. Name the direction of every finding.** "Stricter than AWS" breaks a
correct caller exactly as "more forgiving" breaks a deploy. SQS refused 256 KiB
and above (AWS: 1 MiB), refused a group id on a standard queue (AWS: fair
queues), and accepted old receipt handles (AWS: succeeds, does not delete).
Both directions are findings, and the document says which.

**L5. Every wire the service speaks, one operation at a time.**
A service on two protocols has two decoders, and the one the current SDKs do
not use rots. SQS's Query wire refused six operations on every valid request
for a long time because the SDK that exercised it had moved to JSON. For each
protocol the model declares: one accepted-as-the-reference-spells-it test per
operation; request validation sees the whole request in the same shape on both
(the Query walk read one element of each list); responses carry the model's
XML names (`xmlName`, `xmlFlattened`). Services whose model has flattened
members are listed in the document; the others are checked and say so.

**L6. Tokens are per use.** A receipt handle belongs to a receive; so do an
upload id, a shard iterator, a pagination token, a task token, a version id, a
change-set id. Say what a stale or foreign one does, from the model, and test
that it does it.

## The behaviour

**L7. Asynchronous on AWS means observable on AWS.** SQS move tasks finished
inside the call, so a test that asserted straight after the start passed here
and raced there, and a design board showing "Redriving, 2 moved so far" could
not be built. List the service's operations that are asynchronous on AWS
(status models: CREATING/ACTIVE, PENDING, RUNNING, deletion windows,
propagation) and decide each: modelled with a small, visible delay and a
rate, or immediate with the difference listed. Any board that draws an
in-progress state needs the first.

**L8. Rules that exist because of AWS's latency are opt-in strictness.**
Re-creating a name within 60 seconds of a delete, a second purge: more forgiving
here by default (a minute in every local test loop is a worse tool) and one
setting away (`--sqs-strict`). For each service list the time- and
consistency-based rules; each gets the same switch, off by default, in memory.

**L9. State is in memory unless losing it changes what the application sees.**
A message and its timers, a dedup window, a finished record are the application's
data and persist. A running background task, a 60-second window, a replay
cache are the process's and do not. doze reaps an idle backend after five
minutes without a client connection, so a restart is routine; say for each new
piece of state which it is.

## The console

**L10. The board is the spec, and its shape counts.** SQS was ticked "all 21
boards" with every test green and six boards differing: a side panel built as
an inline list, a two-column page built as one, confirms with no body, a dialog
built as a one-line question. For each board: render it (it opens under
`file://` at 1440 by 900) and the real page in the same state, side by side, in
light and dark and at 900 wide, and write the comparison table: matches /
differs / the design is wrong. A "differs" is built or deliberately decided in
writing. The design is never a compromise: where it is wrong about a fact
(262 144), the design is corrected, not copied and not ignored.

**L11. The five lines of the teaching pattern, all five.** Name and unit; the
value in words; one sentence of what it does; range, default and
**enforced or stored**; the neighbour check with real numbers, live in the
form. SQS had four and half of the fifth. The kit's `setting_field` now writes
line four from the settings record; a setting has a record.

**L12. Icons.** The per-service AWS icons in `internal/console/static/aws/*.svg`
stay: in the rail, the list pane header and the resource header. Where a board
draws a two-letter mark (`SQ`, `S3`) it is the board's placeholder for that
service's icon and the page keeps the icon. Nothing else about the board's
chrome is exempt.

## The close

**L13. A stricter rule has callers.** SQS-23 made SQS compare a queue URL's
account with its own, which was right, and broke every internal delivery to a
queue under any other `--account-id`: `internal/peercall` named the default
account in a URL, under a comment that said the account was never compared. A
stage that makes a service refuse something it used to accept greps for who
relied on the old behaviour (including comments that say they do), across
`peercall`, `provision`, the console clients and the other services' delivery
paths, and runs the two-identity pass (`E2E_ACCOUNT_ID`, `E2E_REGION`) at
the close, not once in the foundation. Stage 1 was signed off without it and
SQS-30 was found a day later by another service's review.

## How a document answers

For each of L1 to L12, one of: *findings* (ids, severity, direction, source), or
*checked, none* with what was read (the operations and members whose
documentation was read; the boards that were rendered). "None" with nothing
read is not an answer.
