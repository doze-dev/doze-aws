# Claims

One file per service: every behaviour doze-aws asserts that is not plain from
the model, with where the claim came from and the scenario that would record it.

A conformance scenario proves boto3 can drive a call. It proves nothing about
AWS until it has been recorded against an account (`CONFORMANCE_TARGET=aws
CONFORMANCE_RECORD=1`). A claim names the scenario that would make it
`recorded`, so what is believed on weaker evidence is a list with a count, not
a feeling.

```
| Claim | Source | Scenario |
|---|---|---|
| ReceiveMessage with MaxNumberOfMessages 11 answers ReadCountOutOfRange | memory | (to write) |
| A FIFO queue delivers a group's messages in the order sent | doc | test_sqs::test_fifo_orders_and_deduplicates |
```

**Source** is what the claim rests on until a recording exists.

| Source | Means | May be fixed on it |
|---|---|---|
| `model` | read from the service's published model | yes |
| `doc` | stated in AWS's documentation | yes, naming the page |
| `memory` | recalled, not checked | no: write the test, skip it, queue the recording |

**Scenario** is `module::function` for a scenario in `conformance/tests`, or
`(to write)` when none exists yet. A name that does not resolve fails
`claims_test.go`.

**Recorded** is not written down. A claim is recorded when its scenario has an
entry in `conformance/snapshots/<module>.json` and that file was recorded
against AWS (`_recorded.target` is `aws`). `bun scripts/claims.ts` prints the
count per service, after the conformance run's own summary.

Files are named for the service key the repository uses (`sqs`, `secretsmanager`).
