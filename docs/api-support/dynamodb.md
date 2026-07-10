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
| Streams (DescribeStream / GetRecords / GetShardIterator / ListStreams) | S | deferred post-1.0 (user decision); StreamSpecification stored inert |
| Global tables, DAX, Kinesis destinations | S | multi-region/cloud infrastructure |
| Backups / exports / imports / PITR restore | S | copy the data directory instead |
