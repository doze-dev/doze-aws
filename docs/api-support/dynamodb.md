# DynamoDB — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

Full item model (S, N, B, BOOL, NULL, M, L, SS, NS, BS) with arbitrary-precision
numbers compared numerically. All five expression languages are really parsed
(lexer + recursive-descent): condition, filter, key-condition, update, and
projection expressions — including `#name`/`:value` substitution, document
paths (`a.b[0].c`), and unused-reference rejection.

| Operation | Tier | Notes |
|---|---|---|
| CreateTable | F | pk/sk schemas, GSIs + LSIs, tags; tables ACTIVE immediately (waiters pass first probe) |
| DescribeTable / ListTables / DeleteTable | F | DeletionProtection enforced |
| UpdateTable | F | GSI create (synchronous backfill) / delete, billing + protection round-trips |
| UpdateTimeToLive / DescribeTimeToLive | F | TTL enforced: lazy filtering on every read plus a janitor sweep through the normal delete path (indexes stay consistent) |
| PutItem / GetItem / DeleteItem | F | ConditionExpression, ReturnValues, ReturnValuesOnConditionCheckFailure (item inside the error), 400 KB size rule |
| UpdateItem | F | SET (arithmetic, list_append, if_not_exists), REMOVE (incl. list indexes), ADD (numbers + set union), DELETE (set subtraction); creates missing items from key attrs; key immutability enforced; ALL_OLD/ALL_NEW/UPDATED_OLD/UPDATED_NEW |
| Query | F | table or index; =, <, <=, >, >=, BETWEEN, begins_with sort conditions; FilterExpression (Limit counts pre-filter, like AWS); ScanIndexForward; paging via LastEvaluatedKey/ExclusiveStartKey; 1 MB page bound |
| Scan | F | filters, paging, Segment/TotalSegments |
| BatchGetItem / BatchWriteItem | F | 100/25 bounds; per-table projection support |
| TransactWriteItems | F | Put/Update/Delete/ConditionCheck, real single-node atomicity (one bbolt txn), CancellationReasons per item, ClientRequestToken idempotency (10-min window, IdempotentParameterMismatchException) |
| TransactGetItems | F | consistent multi-item read |
| TagResource / UntagResource / ListTagsOfResource | F | |
| DescribeLimits / DescribeEndpoints | F | canned values |
| ContinuousBackups / ContributorInsights describes+updates | C | fixed status round-trips |
| PartiQL (ExecuteStatement / BatchExecuteStatement / ExecuteTransaction) | S→F | Phase 8 |
| Streams (DescribeStream / GetRecords / GetShardIterator / ListStreams) | F | one open shard per stream-enabled table; TRIM_HORIZON / LATEST / AT_ and AFTER_SEQUENCE_NUMBER iterators; Lambda event source mappings poll it |
| Global tables, DAX, Kinesis destinations | S | multi-region/cloud infrastructure |
| Backups / exports / imports / PITR restore | S | copy the data directory instead |

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what DynamoDB refuses**. The two are different
promises, and the second is the one that decides whether code passing here also
passes on deploy.

Unlike SQS and S3, DynamoDB's own AWS service model carries the constraints as
traits, so this section is **generated rather than hand-derived**. `dzaudit
cases dynamodb` emits a violating value per constrained input;
`dynamodb/testdata/cases_dynamodb.json` commits them; and
`dynamodb/rejection_parity_test.go` replays every one.

**Every operation doze-aws dispatches is fully audited: 333/333 model-derived
constraints enforced across 27 operations, with `knownGaps` empty.**

The checks live in `dynamodb/validate.go` as one path-keyed table per
operation, run from the dispatcher before any handler sees the body. Putting
them there rather than inside each handler is what makes coverage a property of
the dispatch table instead of something every new handler has to remember.

| Operation | Constraints | Operation | Constraints |
|---|---|---|---|
| CreateTable | 68 ✅ | UpdateTable | 59 ✅ |
| TransactWriteItems | 27 ✅ | Scan | 18 ✅ |
| Query | 16 ✅ | UpdateItem | 12 ✅ |
| DeleteItem | 11 ✅ | PutItem | 11 ✅ |
| TagResource | 9 ✅ | ExecuteStatement | 8 ✅ |
| ExecuteTransaction | 8 ✅ | UpdateTimeToLive | 8 ✅ |
| GetItem | 7 ✅ | TransactGetItems | 7 ✅ |
| UpdateContinuousBackups | 7 ✅ | BatchExecuteStatement | 6 ✅ |
| DescribeContributorInsights | 6 ✅ | UntagResource | 6 ✅ |
| BatchGetItem | 5 ✅ | BatchWriteItem | 5 ✅ |
| ListTables | 5 ✅ | UpdateContributorInsights | 9 ✅ |
| DeleteTable | 3 ✅ | DescribeTable | 3 ✅ |
| DescribeTimeToLive | 3 ✅ | DescribeContinuousBackups | 3 ✅ |
| ListTagsOfResource | 3 ✅ | | |

### Nested constraints, and the trap under them

Two thirds of these constraints are not on top-level members but inside
structures — `GlobalSecondaryIndexes[].Projection.ProjectionType`,
`TransactItems[].Put.Item`, `RequestItems{}[].DeleteRequest.Key`. Mutating one
means first sending a valid enclosing structure, which reopens the
wrong-reason-refusal problem one level down: if the *exemplar* standing in for
that structure is itself invalid, every case under it is refused for the
exemplar and the whole group reads as enforced when nothing was tested.

So exemplars are **probed**. For each container an operation's cases descend
through, the baseline carrying that exemplar and no mutation at all must still
be accepted. Two real defects surfaced this way and would otherwise have
shipped as false passes — one of them a constraint table that rejected every
valid `ComparisonOperator`, because `dzaudit list` elides enums over six
members for display and that elision had leaked into the emitted JSON.

### What is not audited, and why it cannot be

| Scope | Cases | Status |
|---|---|---|
| The 27 dispatched operations | 333 | ✅ fully audited, all enforced |
| 22 stub operations | 299 | **un-auditable**: an honest `UnsupportedOperationException` refuses the baseline too, so replaying a mutation proves nothing |
| 7 operations with no handler | 61 | **un-auditable**: `InvalidAction` for the same reason |

The stubs are global tables, backups, exports/imports and Kinesis streaming —
things that are cloud infrastructure rather than local behaviour, and that
`stubActions` refuses by name with a reason. The seven without handlers are
`SearchVectors`, the resource-policy trio, the replica-autoscaling pair and
`ListContributorInsights`.

An operation that refuses every request cannot be too permissive, so nothing is
hidden by this — but it is not the same statement as "audited", and this page
does not make the stronger one.
