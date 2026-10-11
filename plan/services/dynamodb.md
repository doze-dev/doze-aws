# DynamoDB

Stage 7. Size **XL** (was L; re-estimated 2026-10-11, see section 1). Follows
[../01-protocol.md](../01-protocol.md); the common acceptance criteria there
apply in full, and this page adds DynamoDB's own.

**Pre-stage review, 2026-10-11.** Every claim below was checked against the
model's documentation text (`.audit-models/dynamodb.json`) and against a
binary built from `main`; corrections are marked *Corrected 2026-10-11* where
they stand, new findings are DDB-32 to DDB-62 in section 4, the board-by-board
comparison is section 6a, and section 12 answers the twelve questions of
[../02-lessons-from-sqs.md](../02-lessons-from-sqs.md).

## 1. Summary

Tables, items, secondary indexes, TTL, transactions, PartiQL and Streams over
one bbolt store (`internal/ddb/store`), speaking AWS JSON 1.0 on two target
prefixes: `DynamoDB_20120810` and `DynamoDBStreams_20120810`. The item model and
the five expression languages are real (`internal/ddb/item`, `internal/ddb/expr`).

Peers: Lambda polls a table's stream through an event source mapping;
CloudFormation and `provision` create tables; Step Functions and API Gateway
call the item operations as service integrations. Nothing calls into DynamoDB
except through its API, so the service itself has no peer dependencies.

The expression path is in good shape: conditions, reserved words, unused
references, batch bounds and transaction rules were refused correctly when
probed. The rest was not as good as the first pass said.
*Corrected 2026-10-11: was "the item path is in good shape"; run: the legacy
parameters (`Expected`, `AttributeUpdates`, `ScanFilter`, `AttributesToGet`)
are accepted and silently ignored, a table named by ARN is refused, and
`ExecuteTransaction` is not atomic* (DDB-32, DDB-33, DDB-36). Table
administration accepts a long list of things AWS refuses, and `UpdateTable`
silently ignores most of what it is given. The console page works, shows facts
that are false for any table it did not create, has two ways of quietly changing
the type of stored data, and is, board for board, the shape of the old design
rather than the new one (section 6a).

**Estimate:** XL, about twenty-three focused days (nineteen to thirty).
**Actual:** _fill in when done._ *Corrected 2026-10-11: was "L, five or more
focused days", written for 31 findings and a console that "exists". There are
62 findings, three of them P0 that were not on the list, and 15 of 20 boards
do not match the design. Where the days go: the table rules and the asynchronous table
model 4; the legacy parameters and the expression surface 3; PartiQL's grammar
and its transaction 3; streams 2; the model's list constraints, the Query and
Scan rules and the item limits 2; conformance, mutation and the ledger 3; the
console, 6.*

## 2. Contract

The DynamoDB model lists **58 operations** (`dynamodb/testdata/ops_dynamodb.json`,
checked by `TestEveryModelOperationIsAccountedFor`). The dispatcher handles 29
(`handlers` in `dynamodb/tables.go`) and refuses 29 by name (`stubActions` in
`dynamodb/dynamodb.go`). The DynamoDB Streams model adds **4 operations**,
all handled (`streamHandlers` in `dynamodb/streams.go`). 62 in all.
*Corrected 2026-10-11 (source tags): the 58 are `model` (`.audit-models/dynamodb.json`
holds 58 operations, counted). The four Streams operations are not in any model on
disk: `.audit-models/` has no DynamoDB Streams file, so their shapes, limits and
errors are `doc` (the developer guide's Streams page, read) or `memory`, and
`dynamodb/validate.go` has no constraint table for them at all, so nothing
validates a Streams request (DDB-58).* The model also sets
`clientEndpointDiscovery` on the service with `InvalidEndpointException`
(status 421), and `clientDiscoveredEndpoint required:false` on 42 of the 58 operations (not on `DescribeEndpoints`, PartiQL, Contributor Insights, exports, imports, replica auto scaling or `SearchVectors`), so discovery is optional on every operation that has it.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateTable | F | F | Per-setting tier below. Accepts invalid schemas: DDB-4, DDB-5, DDB-6, DDB-7, and drops all but the last of a GSI's multi-attribute key (DDB-37); index counts and `INCLUDE` rules unenforced (DDB-50); a table named by ARN is refused (DDB-36). Answers `ACTIVE` where the model says `CREATING` (DDB-52). |
| DescribeTable | F | F | `TableSizeBytes` is always 0 and `TableId` is the name: DDB-17. Reports a wrong key type after DDB-4. Several members are fixed or absent: DDB-61. |
| ListTables | F | F | Ignores `Limit` and `ExclusiveStartTableName`: DDB-9. |
| DeleteTable | F | F | Deletion protection enforced. The response says `ACTIVE`, not `DELETING` (DDB-52); the table's stream records survive it and appear in a table created again under the name (DDB-34). |
| UpdateTable | F | F | Per-setting tier below. Ignores `StreamSpecification` (DDB-1) and `TableClass` (DDB-13); index delete and update of a missing index succeed (DDB-8); no one-change-at-a-time rule, no `UPDATING` (DDB-43, DDB-52). |
| UpdateTimeToLive, DescribeTimeToLive | F | F | DDB-16, DDB-53. |
| PutItem, GetItem, UpdateItem, DeleteItem | F | F | Empty key values and mistyped index keys accepted: DDB-2, DDB-3. The legacy parameters are silently ignored: DDB-32. `ReturnValues` values the model refuses are accepted on Put and Delete: DDB-57. Key sizes and nesting depth unchecked: DDB-54. |
| Query, Scan | F | F | A filter on a key attribute, `Select` and `ProjectionExpression` combinations, `Segment` without `TotalSegments`, `begins_with` on a Number sort key accepted (DDB-44, DDB-56); a local index does not fetch from the table (DDB-45). |
| BatchGetItem, BatchWriteItem | F | F | Duplicate keys in a BatchGetItem accepted: DDB-10. A failing BatchWriteItem leaves its earlier writes applied: DDB-35. |
| TransactWriteItems, TransactGetItems | F | F | The 4 MB aggregate is not checked: DDB-55. |
| ExecuteStatement, BatchExecuteStatement, ExecuteTransaction | F | F, after DDB-33 | PartiQL, translated onto the classic operations. `ExecuteTransaction` is **not** atomic (DDB-33); `Limit`/`NextToken` ignored (DDB-46); a grammar subset (DDB-47); batch error codes (DDB-48). |
| TagResource, UntagResource, ListTagsOfResource | F | F | DDB-11. |
| DescribeLimits | F | C | Canned numbers. The ledger calls it F; it is cosmetic and should say so. *Corrected 2026-10-11 (source): the numbers (80,000 per account, 40,000 per table) are the documented default quotas, `doc`: "Quotas in Amazon DynamoDB", Throughput default quotas; the model's own example shows 20,000 and 10,000, which is an example, not a default.* |
| DescribeEndpoints | F | decide | Returns AWS's own us-east-1 hostname: DDB-14. Target is either the instance's address or S. The model's `Endpoint.Address` reads "IP address of the endpoint". |
| DescribeContinuousBackups, UpdateContinuousBackups | C | C | The update does not round-trip: DDB-12. A missing table is `ResourceNotFoundException`; the model says `TableNotFoundException`: DDB-51. |
| DescribeContributorInsights, UpdateContributorInsights | C | C | Same, and no table check: DDB-12, DDB-51. |
| CreateGlobalTable, DescribeGlobalTable, DescribeGlobalTableSettings, ListGlobalTables, UpdateGlobalTable, UpdateGlobalTableSettings | S | S | One region locally. |
| DescribeTableReplicaAutoScaling, UpdateTableReplicaAutoScaling | S | S | Belongs to global tables. |
| CreateBackup, DeleteBackup, DescribeBackup, ListBackups, RestoreTableFromBackup, RestoreTableToPointInTime | S | S | Copy the data directory. |
| ExportTableToPointInTime, DescribeExport, ListExports, ImportTable, DescribeImport, ListImports | S | S | |
| DescribeKinesisStreamingDestination, EnableKinesisStreamingDestination, DisableKinesisStreamingDestination, UpdateKinesisStreamingDestination | S | S | |
| GetResourcePolicy, PutResourcePolicy, DeleteResourcePolicy | S | S | Table resource policies are not stored or evaluated. |
| ListContributorInsights | S | S | |
| SearchVectors | S | S | |
| Streams: ListStreams, DescribeStream, GetShardIterator, GetRecords | F | F | One open shard per stream. No-change writes emit a record, TTL deletions carry no `userIdentity`, the ARN never changes, and a deleted table's records outlive it: DDB-34, DDB-40, DDB-41, DDB-42, DDB-58. |

The S rows answer `UnsupportedOperationException` with status 400 and a reason.
*Corrected 2026-10-11: was "whether that is the code an SDK expects ... is not
established". Run: `CreateBackup` answers 400 `UnsupportedOperationException`
(`X-Amzn-Errortype` the same). The model has no such error shape (no operation
lists it, `grep` of the model finds none), and plan decision D5 says every S
row answers 501 `NotImplemented` with `X-Doze-Not-Built`. `modelsweep_test.go`
calls this class `legacystub`. DynamoDB is still in it (DDB-49).* An unknown
action answers 400 `InvalidAction`; the code DynamoDB gives is `memory`.
`docs/not-built.md` has two DynamoDB lines covering global tables, DAX, Kinesis
destinations, backups, exports, imports and restore; resource policies, replica
auto scaling, `ListContributorInsights` and `SearchVectors` are refused but not
listed there.

Table settings need their own tiers, because "CreateTable is F" hides which
members do anything:

| Setting | On | Now | Target | To confirm |
|---|---|---|---|---|
| KeySchema, AttributeDefinitions | CreateTable | F | F | Enforced on every write. Schema validation at create is thin: DDB-4, DDB-6. |
| GlobalSecondaryIndexes | CreateTable; UpdateTable `Create`, `Delete` | F | F | Synchronous backfill. Duplicate names: DDB-7. |
| GlobalSecondaryIndexUpdates `Update` | UpdateTable | ignored | C | Succeeds and changes nothing, even for an index that does not exist: DDB-8. |
| LocalSecondaryIndexes | CreateTable | F | F | Must share the table's partition key (checked). Allowed on a table with no sort key (DDB-6). |
| Projection `ALL`, `KEYS_ONLY`, `INCLUDE` | index | F | F | `Select` rules are enforced on a global index (run: `ALL_ATTRIBUTES` on a `KEYS_ONLY` GSI is refused). *Corrected 2026-10-11: was "that a read through the index returns exactly the projected attributes is not established"; model (Query.Select): "If you query a local secondary index, then for each matching item in the index, DynamoDB fetches the entire item from the parent table" and "If you query or scan a global secondary index, you can only request attributes that are projected into the index". Run: a global index returns exactly the projection (right); a local index also returns only the projection (wrong, DDB-45).* `INCLUDE` needs one to twenty `NonKeyAttributes`, at most 100 across the table (model; unenforced, DDB-50). |
| BillingMode, ProvisionedThroughput | CreateTable, UpdateTable | C | C | Validated, stored, reported. Nothing is metered or throttled. |
| ProvisionedThroughput on an index | CreateTable | C | C | Required or forbidden by billing mode, as on AWS. |
| DeletionProtectionEnabled | CreateTable, UpdateTable | F | F | |
| StreamSpecification | CreateTable | F | F | |
| StreamSpecification | UpdateTable | ignored | F | DDB-1. |
| SSESpecification | CreateTable, UpdateTable | C | C | A description only. |
| Tags | CreateTable | F | F | |
| TableClass | CreateTable, UpdateTable | dropped | C | Not stored, not reported: DDB-13. |
| WarmThroughput, OnDemandThroughput | CreateTable, UpdateTable | ignored | C or refuse | *Established 2026-10-11 (read, `warm()`): a fixed `WarmThroughput` (12,000 read and 4,000 write on demand, 3,000 and 1,000 provisioned) is reported whatever was sent, and `OnDemandThroughput` is dropped. Model: `MaxReadRequestUnits` is "greater than or equal to 1", or -1 to remove.* |
| ResourcePolicy, GlobalTable members, ReplicaUpdates, MultiRegionConsistency, GlobalTableWitnessUpdates, VectorIndexes, VectorIndexUpdates | CreateTable, UpdateTable | ignored | refuse | *Established 2026-10-11 (read): none is in `createTableReq` or `updateTable`'s request struct, so each is dropped and the call answers 200. An accepted and ignored member is a silent no-op (DDB-43).* |
| Legacy parameters: `Expected`, `ConditionalOperator`, `AttributeUpdates`, `AttributesToGet`, `QueryFilter`, `ScanFilter` | the item operations | ignored | F | *Added 2026-10-11, run: all six are accepted and ignored (DDB-32). `KeyConditions` alone is refused, as "KeyConditionExpression is required".* |
| TimeToLiveSpecification | UpdateTimeToLive | F | F | |
| PointInTimeRecoverySpecification | UpdateContinuousBackups | ignored | C | DDB-12. |
| ContributorInsightsAction, ContributorInsightsMode | UpdateContributorInsights | ignored | C | DDB-12. |
| ReturnConsumedCapacity, ReturnItemCollectionMetrics | item operations | ignored | decide | Enum validated, nothing returned: DDB-15. |
| ConsistentRead | GetItem, Query, Scan, BatchGetItem | accepted | C | One node, so every read is consistent. Refused on a global secondary index, as on AWS. *Source `model` (Query, Scan: "If you query a global secondary index with ConsistentRead set to true, you will receive a ValidationException").* |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `dynamodb/`: 23 tests and one fuzz target across 11 files (SDK v2 9, SDK coverage 2, SDK v1 1, billing 2, SSE 3, conformance fixes 2, streams 1, TTL 1, coverage 1, rejection parity 1) |
| Engine tests | `internal/ddb/`: 30 tests (expr 9, item 8, keyenc 4, store 2, txn 7) and 9 benchmarks |
| Parity | `rejection_parity_test.go` replays `testdata/cases_dynamodb.json`; the ledger says 333 of 333 model constraints enforced across 27 operations, `knownGaps` empty |
| Conformance | `conformance/tests/test_dynamodb.py`: 7 scenarios, all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/dynamodb.spec.ts`: 9 tests |
| Console unit | `internal/console/ddb_skeleton_test.go`: 3 tests (the add-item skeleton, the key-move refusal) |
| Mutation | No per-package baseline yet (foundation B3) |

Three things about the parity number. It is true, and it is why the table-name
finding exists: the model gives the request member that names a table the shape
`TableArn`, a length of 1 to 1024 and no pattern, because the same member also
accepts an ARN. *Corrected 2026-10-11: was "so the 3 to 255 naming rule is not
in the model at all". The model has it: shape `TableName` is
`length 3..255` and `pattern ^[a-zA-Z0-9_.-]+$`, used for the name in a
response and for a global table or a restore target. Only the request members
are `TableArn`. The rule is `model`, and it is applied by hand to the name
given to CreateTable.* Second, model constraints are per member; every rule that
relates two members (a key schema to its attribute definitions, one index name
to another) is hand-derived, and almost none of those exist yet.
`internal/ddb/store/store_test.go` has two tests for the layer where they belong.

Third, and new: **"333 of 333" counts only the string, number and enum
constraints.** *Added 2026-10-11 (DDB-38): the model carries `length` on
lists and maps as well, 25 shapes (22 on request members), among them: `KeySchema` at
least 1, `TransactItems` 1 to 100 on both transaction operations,
`BatchGetItem.RequestItems` 1 to 100 and `Keys` 1 to 100,
`BatchWriteItem.RequestItems` 1 to 25 and each `WriteRequests` 1 to 25,
`AttributesToGet` at least 1, `PreparedStatementParameters` at least 1,
`NonKeyAttributes` 1 to 20, `BatchExecuteStatement.Statements` 1 to 25,
`ExecuteTransaction.TransactStatements` 1 to 100, `SearchVector` 1 to 4,096.
None is in `validate.go`. The handlers refuse a few of them in their own words
("TransactWriteItems accepts 1-100 operations, got 101"); the rest are not
refused at all: `GetItem` with `AttributesToGet: []` answered 200.*

## 4. Findings

Evidence `run` means reproduced against a binary built from `main` on
2026-10-10, through the API with curl or through the console's own routes.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| DDB-1 | P0 | `UpdateTable` ignores `StreamSpecification`. Enabling a stream on an existing table returns success with no stream; disabling one leaves it running. A deploy that adds a stream and then a Lambda mapping fails at the mapping, or never fires. | `dynamodb/tables.go` `updateTable` (the request struct has no such member) | run (again 2026-10-11) | *Corrected 2026-10-11, was `doc`; now `model`: UpdateTable.StreamSpecification, "You receive a ValidationException if you try to enable a stream on a table that already has a stream, or if you try to disable a stream on a table that doesn't have a stream." Also `doc` (Streams page, read): "If you disable and then re-enable a stream on the table, a new stream is created with a different stream descriptor."* | SDK test: create without a stream, `UpdateTable` to enable, assert `LatestStreamArn`; enable again and assert `ValidationException`; disable and assert it is gone and the second disable is refused; re-enable and assert a different ARN |
| DDB-2 | P0 | A key attribute holding an empty string is stored. `PutItem` with `pk: ""` succeeds, and so does the same through the console. | `internal/ddb/store/store.go` `primaryKey` | run (again 2026-10-11, and through the console: **Save item on the untouched Add-item skeleton stores an item whose `orderId` is `""`**) | *Corrected 2026-10-11, was `doc`; now `model`: PutItem.Item, "Empty String and Binary attribute values are allowed. Attribute values of type String and Binary must have a length greater than zero if the attribute is used as a key attribute for a table or index." The same text makes an empty string legal in a non-key attribute, which the code accepts (right). Message text: `memory`.* | Store test and rejection-parity case for an empty S and an empty B key, on PutItem, UpdateItem, BatchWriteItem and TransactWriteItems, and for an empty non-key string (accepted); a console test that the skeleton alone is refused |
| DDB-3 | P0 | An item whose index key attribute has the wrong type, or is an empty string, is accepted and silently left out of the index. On AWS the write is refused, so a type bug in the caller shows up here only as a Query that returns nothing. | `internal/ddb/store/store.go` `indexKeyFor` | run | *Corrected 2026-10-11, was `memory`; now `model` and `doc`: PutItem.Item, "If you specify any attributes that are part of an index key, then the data types for those attributes must match those of the schema in the table's attribute definition"; GSI page (read): "If you try to add an item to the GameScores table and specify a different data type for either GameTitle or TopScore, DynamoDB returns a ValidationException because of the data type mismatch." A missing index key is sparse (`doc`, same page). An empty string as an index key: the PutItem text above.* | Store test: GSI on `g` (S), put `g` as N and as `""`, expect `ValidationException`; put without `g`, expect success and absence from the index. No longer skipped |
| DDB-4 | P0 | An index may redeclare an attribute the table already keys on with a different type. `UpdateTable` with a GSI on `lineNo` typed S, on a table whose sort key `lineNo` is N, succeeds, and `DescribeTable` then reports the table's own sort key as S while the store still enforces N. Every client that builds keys from the description then fails. | `dynamodb/tables.go` `describe` (one `attrTypes` map for table and indexes), `indexFromWire` | run | `memory` for the refusal (the model does not say a redeclared attribute is refused; UpdateTable.AttributeDefinitions says only that it "must include the key element(s) of the new index"); the `describe` half is ours and needs no source | Handler test: the UpdateTable above is refused; a describe test that a table key's type never changes. The describe half is fixable now |
| DDB-5 | P1 | Table names are not checked beyond the model's 1 to 1024. `a`, `bad name!` and a 300-character name are all created. | `internal/ddb/store/store.go` `CreateTable`; no rule in `dynamodb/validate.go` | run | *Corrected 2026-10-11, was `doc`; now `model`: shape `TableName`, `length 3..255`, `pattern ^[a-zA-Z0-9_.-]+$`; also `doc` (naming rules page, read): "Table names and index names must be between 3 and 255 characters long". The exact error text: `memory`. The request member is `TableArn`, which is why the generated table does not carry it.* | Rejection-parity cases for a 2-character name, a 256-character name and a space; an ARN is accepted (DDB-36) |
| DDB-6 | P1 | Key schema and attribute definitions are not cross-checked. Accepted: a sort key with the same name as the partition key; two `HASH` elements (the last wins, the first is dropped); an attribute definition no key uses; a local secondary index on a table with no sort key. | `dynamodb/tables.go` `keyParts`, `createTable` | run (again 2026-10-11: also a RANGE first, three elements, and one attribute as both HASH and RANGE, all accepted; the key schema is rebuilt from the last of each, and the attribute definitions that no key uses vanish from `DescribeTable`) | *Corrected 2026-10-11, was "doc for each rule"; `model` for these: CreateTable.KeySchema, "For a simple primary key (partition key), you must provide exactly one element with a KeyType of HASH. For a composite primary key (partition key and sort key), you must provide exactly two elements, in this order: The first element must have a KeyType of HASH, and the second element must have a KeyType of RANGE"; LocalSecondaryIndex "The key schema must begin with the same partition key as the table". `memory`, not fixed on: a definition no key uses, an LSI on a table with no sort key, the same attribute twice. A GSI may have up to four of each (DDB-37).* | One handler test per case, table-driven |
| DDB-7 | P1 | Duplicate index names are accepted, at `CreateTable` and at `UpdateTable` `Create`. Two indexes named `by-sku` were created and both listed. | `dynamodb/tables.go` `createTable`, `updateTable` | run | *Corrected 2026-10-11, was `memory`; now `model`: LocalSecondaryIndex.IndexName and GlobalSecondaryIndex.IndexName, "The name must be unique among all other indexes on this table."* | Handler test for both paths |
| DDB-8 | P1 | `UpdateTable` on an index that does not exist succeeds: `Delete` of a missing index, and `Update` of any index, are no-ops. | `dynamodb/tables.go` `updateTable` | run | *Corrected 2026-10-11, was `memory`; now `model`: error `ResourceNotFoundException`, "The operation tried to access a nonexistent table or index." (`IndexNotFoundException` is in the model, but UpdateTable does not list it.)* | SDK test for both |
| DDB-9 | P1 | `ListTables` ignores `Limit` and `ExclusiveStartTableName` and never returns `LastEvaluatedTableName`. A caller that pages gets everything on page one. | `dynamodb/tables.go` `listTables` | run | *Corrected 2026-10-11, was `doc`; now `model`: ListTables, "each page returning a maximum of 100 table names", `Limit` 1 to 100 "If this parameter is not specified, the limit is 100", and "If you do not receive a LastEvaluatedTableName value in the response, this means that there are no more table names to be retrieved."* | SDK test with `Limit: 1` over three tables, then `ExclusiveStartTableName` |
| DDB-10 | P1 | `BatchGetItem` with the same key twice returns the item twice. `BatchWriteItem` refuses the same mistake. | `dynamodb/items.go` `batchGet` | run | *Corrected 2026-10-11, was `memory`; now `model`: BatchGetItem, "BatchGetItem will result in a ValidationException if the same key is specified multiple times." The `more than 100` case has a quoted message, "Too many items requested for the BatchGetItem call."; the code says "BatchGetItem accepts 1-100 keys, got 101" (DDB-59).* | Handler test |
| DDB-11 | P1 | Tags: 51 tags on one table accepted; a key starting `aws:` accepted; the ARN's region and account are not read, so an ARN for another account tags this one's table. | `dynamodb/tables.go` `tagResource`, `tableFromARN` | run | *Corrected 2026-10-11, was "doc for the limit of 50; memory for the other two"; `model` for the first two: Tag, "You can add up to 50 tags to a single DynamoDB table. Amazon Web Services-assigned tag names and values are automatically assigned the aws: prefix, which the user cannot assign."; TagResource lists `LimitExceededException`. Not "isn't recommended" wording, so unlike SQS-16 this one is a refusal. The ARN for another account or region is `memory` for the code; `ResourceNotFoundException` is `model`.* | Handler tests; the ARN case belongs with the second-identity pass (foundation B2) |
| DDB-12 | P1 | The two C families do not round-trip. `UpdateContinuousBackups` enabling point-in-time recovery answers `DISABLED`; `UpdateContributorInsights` answers `DISABLED` and does not check that the table exists. Terraform's `point_in_time_recovery` would plan the same change forever. The ledger says "fixed status round-trips", which is not what happens. | `dynamodb/tables.go` `describeContinuousBackups`, `describeContributorInsights` (each also serves the update) | run | *Corrected 2026-10-11, was `doc`; now `model`: UpdateContinuousBackups, "A successful UpdateContinuousBackups call returns the current ContinuousBackupsDescription", "Continuous backups are ENABLED on all tables at table creation" (so `ContinuousBackupsStatus: ENABLED` is right), `RecoveryPeriodInDays` 1 to 35, "If no value is provided, the value will default to 35". The unknown-table code is `TableNotFoundException`, not `ResourceNotFoundException` (DDB-51).* | SDK test: enable with a period, describe, expect `ENABLED` and the period; unknown table refused with the model's code |
| DDB-13 | P1 | `TableClass` is dropped on create and on update and is absent from the description. | `dynamodb/tables.go` `createTableReq`, `updateTable`, `describe` | run (also on CreateTable) | *Corrected 2026-10-11, was `doc`; now `model`: CreateTable.TableClass, UpdateTable.TableClass, TableDescription.TableClassSummary.* | SDK test: set, describe, expect `TableClassSummary` |
| DDB-14 | P1 | `DescribeEndpoints` returns `dynamodb.us-east-1.amazonaws.com` whatever the instance's address or region. An SDK with endpoint discovery turned on would be told to talk to AWS. Not reproduced with an SDK; raise to P0 if it is. The literal region is foundation A8's subject. | `dynamodb/tables.go` `describeEndpoints` | run (the response), read (the consequence) | *Corrected 2026-10-11, was `doc`; now `model`: the service's `clientEndpointDiscovery` and, on 42 operations including every table and item one, `clientDiscoveredEndpoint required:false`, so discovery is opt-in and the P1 stands.* | SDK v2 test with endpoint discovery enabled: every call still reaches the test server |
| DDB-15 | P1 | `ReturnConsumedCapacity` is validated and then ignored: no response carries `ConsumedCapacity`. The ledger lists capacity as not modelled; a caller that reads the member gets nil. | `dynamodb/items.go`, every item handler | run (again 2026-10-11: `PutItem`, `GetItem` and `Query` with `ReturnConsumedCapacity` answer no `ConsumedCapacity`) | *Corrected 2026-10-11, was `doc`; now `model`: "ConsumedCapacity is only returned if the request asked for it" and the three values `INDEXES`, `TOTAL`, `NONE`. `ReturnItemCollectionMetrics` is the same: ignored; the model returns it "only" for a table with a local index, so a table without one is correct as it is.* | Decide first (section 10), then an SDK test either way |
| DDB-16 | P1 | TTL accepts a key attribute as the TTL attribute, and accepts a disable that names a different attribute from the one enabled. What AWS does with either is not established. | `dynamodb/tables.go` `updateTTL` | run | `memory`, kept. *Added 2026-10-11: the model's one rule here is the time rule, "Any additional UpdateTimeToLive calls for the same table during this one hour duration result in a ValidationException" (DDB-53); it says nothing about a key attribute.* | Written and skipped until recorded |
| DDB-17 | P1 | `DescribeTable` reports `TableSizeBytes: 0` always and `TableId` equal to the table name. | `dynamodb/tables.go` `describe` | run | *Corrected 2026-10-11, was `doc`; now `model`: TableDescription.TableSizeBytes "DynamoDB updates this value approximately every six hours"; TableDescription.TableId, "in UUID format, generated by DynamoDB when the table is created", `pattern ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`. A size of 0 on an empty table is right; on a table with items it is not.* | Decide whether to compute size; a describe test for a UUID-shaped id that survives a restart |
| DDB-18 | P2 | The console's create form can change the table's key type. Its attribute definitions are one map keyed by name, so an index whose partition key is named like the table's and typed differently overwrites it: a String `pk` became N with no message. | `internal/console/client_ddb.go` `CreateTable` (`defs`) | run | n/a | Console handler test: base `pk` S, index on `pk` N; expect a refusal naming the conflict |
| DDB-19 | P2 | Create is two calls. When the TTL call is refused the form shows the refusal and stays put, but the table already exists; pressing Create again says "Table already exists". | `internal/console/client_ddb.go` `CreateTable`, `handlers_ddb.go` `ddbCreateTable` | run | n/a | Handler test with a 300-character TTL attribute: either nothing is created, or the response lands on the new table and says what was not applied |
| DDB-20 | P2 | Edit item changes stored types. The editor shows plain JSON and writes it back whole: a string set came back as a list of strings, a number set as a list of **strings**, binary as a string. | `internal/console/client_ddb.go` `avToPlain`, `toAV`, `PutItemJSON` | run | n/a | Console test: put SS, NS and B through the API, save unchanged through Edit, read back and compare types |
| DDB-21 | P2 | Edit through a narrow index loses attributes. Query results from a `KEYS_ONLY` or `INCLUDE` index carry only the projected attributes; the drawer's Edit sends that partial item as a whole-item PutItem. | `internal/console/client_ddb.go` `itemsFromAV`, `templates/ddb.html` `ddb_item_rows` | read | n/a | Browser test: KEYS_ONLY index, query it, Edit and Save unchanged, then GetItem |
| DDB-22 | P2 | The Details tab states facts it never read. Billing is the literal `PAY_PER_REQUEST` (a PROVISIONED 5/2 table shows it). Deletion protection, the stream, encryption and local secondary indexes are not read from `DescribeTable` at all, so an LSI cannot be chosen in Query. | `templates/ddb.html` `ddb_details`; `client_ddb.go` `Table`, `DescribeTable` | run | n/a | Handler test: a provisioned, protected, stream-enabled table with an LSI renders each fact |
| DDB-23 | P2 | A scan that returns nothing says "No items yet — add one, or write via the SDK", on a table with items, when the filter matched none. When a small limit ends the page early there is a cursor and no "Load more", so later matches cannot be reached. | `templates/ddb.html` `ddb_item_table` (the empty branch) | run | n/a | Handler test: three items, a filter matching the last, `limit=1`; expect the scanned count and a way on |
| DDB-24 | P2 | After Save item, Update item, Delete item and Delete selected, the list is replaced by an unfiltered scan from the top while the form still shows the query that was run. | `handlers_ddb.go` `ddbItemsScan` | read | n/a | Browser test: query an index, delete one row, assert the remaining rows are still the query's |
| DDB-25 | P2 | A filter binding named `:pk` or `:sk` is overwritten by the query builder's own, so the filter compares against the key value instead of what was typed. | `client_ddb.go` `QueryItems` (the shared `vals` map) | read | n/a | Unit test on `QueryItems` with a filter `qty > :pk` |
| DDB-26 | P2 | The Size row always reads "0 B" (DDB-17 underneath). | `templates/ddb.html` `ddb_details` | run | n/a | Covered by DDB-17's decision; remove the row or compute it |
| DDB-27 | P2 | Opening a table that does not exist returns status 400 and a bare error fragment with no page around it. A bookmark to a deleted table lands there. | `handlers_ddb.go` `ddbTable` calling `fail` on a full-page GET | run | n/a | Handler test: GET `/ddb/nope` without the htmx header renders the shell |
| DDB-28 | P3 | The create form drops an index row that has a name and no partition key, without saying so. Reachable only past the browser's `required`. | `client_ddb.go` `CreateTable` | run | n/a | Handler test expecting a refusal |
| DDB-29 | P3 | "Load 50 more" is a literal. The next page ignores the limit that was typed and reads 50. | `templates/ddb.html` `ddb_item_rows`; `handlers_ddb.go` `scanNextVals` | read | n/a | Handler test: `limit=3`, follow the cursor, count rows |
| DDB-30 | P3 | Copy. The empty state says "turn on a stream and every write can drive a Lambda"; the console has no stream control. The create subtitle says indexes and TTL can be added "later via the SDK"; the Details tab adds both. | `internal/console/copy.go` (`ddb`), `templates/create.html` `ddb_create` | read | n/a | The copy test from foundation A7; a browser assertion on the subtitle |
| DDB-31 | P3 | The table page never asks for its neighbours, so no wiring strip is drawn, though the graph has table nodes and the comment in `panes.html` (`connections`) says DynamoDB renders one. | `handlers_ddb.go` `ddbTable` (no `Conn`), `templates/ddb.html` `ddb_table` | read | n/a | Handler test: a table whose stream feeds a function renders the strip |

**Found by reading the model's own documentation, and by running the code
against it (2026-10-11).** `.audit-models/dynamodb.json`, every operation's
and member's documentation and its error list, read in full; each row below was
reproduced on a binary built from `main` that day unless it says `read`. The
first pass wrote its rules from memory and the developer guide; the model is the
current statement. Direction is named, because "stricter than AWS" breaks a
correct caller exactly as "more forgiving" breaks a deploy.

| Id | Sev | What | Where | Evidence | Direction | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| DDB-32 | P0 | **The legacy parameters are accepted and silently ignored.** `Expected` and `ConditionalOperator` on Put, Delete and Update; `AttributeUpdates` on Update; `AttributesToGet` on Get, Query, Scan and each BatchGet table; `QueryFilter`, `ScanFilter`. A conditional put through the old `Expected` API overwrites; a `ScanFilter` returns everything; an `AttributeUpdates` update changes nothing and answers 200. `KeyConditions` alone is refused ("KeyConditionExpression is required"). The section 5 line "never silently ignored" was a checkbox, and this is what is behind it. | `dynamodb/items.go`: `exprCommon` has none of these members; `putItem`, `updateItem`, `deleteItem`, `getItem`, `query`, `scan`, `batchGet` | run: `PutItem` with `Expected {pk: {Exists: false}}` on an existing item answered 200 and replaced it; `Scan` with `ScanFilter` returned all 3 of 3; `UpdateItem` with `AttributeUpdates` returned the old item | more forgiving (a silent no-op; wrong data) | model: every one is a member of its input, documented "This is a legacy parameter. Use ConditionExpression instead."; `ExpectedAttributeValue` and `Condition` give the full semantics (13 operators, `Exists`, `AttributeValueList`) | SDK v1 test (it still sends them): `Expected` with `Exists: false` on an existing item is `ConditionalCheckFailedException`; `AttributeUpdates` PUT/ADD/DELETE; `ScanFilter`; `AttributesToGet`. **Decide first:** translate to expressions (the model defines every operator) or refuse with `ValidationException`. Never ignore |
| DDB-33 | P0 | **`ExecuteTransaction` is not atomic.** The statements run one after another through the single-statement path; the first refusal returns that statement's own error and the writes before it stay. AWS answers `TransactionCanceledException` with per-statement `CancellationReasons`, and writes nothing. `ClientRequestToken` (1 to 36) is ignored, so there is no idempotency window; a transaction mixing reads and writes is accepted (the model refuses it). `docs/SUPPORT.md` says it "runs as TransactWriteItems" (DDB-60). | `dynamodb/partiql.go` `executeTransaction` ("best-effort atomicity ... is a follow-on") | run: `INSERT tx1` then `INSERT k2` (existing): `DuplicateItemException` for the second, and `GetItem tx1` found it | wrong (non-atomic; wrong code) | model: ExecuteTransaction, "transactional reads or writes ... The entire transaction must consist of either read statements or write statements, you cannot mix both"; errors list `TransactionCanceledException`, `TransactionInProgressException`, `IdempotentParameterMismatchException`; `ClientRequestToken` length 1 to 36, `idempotencyToken` | SDK test: two INSERTs, the second a duplicate: `TransactionCanceledException`, reasons `[None, DuplicateItem]`, the first absent. Then: token replay; mixed read and write refused |
| DDB-34 | P0 | **A deleted table's stream records survive it and are served to the next table of that name.** `DeleteTable` removes the data and index buckets and not `stream:<table>`; a table created again under the name starts with the old table's records (and sequence numbers), reachable by `TRIM_HORIZON` and by an event source mapping. A test loop that drops and recreates a streamed table delivers last run's writes to this run's function. Section 5 says "starts empty". | `internal/ddb/store/store.go` `DeleteTable`; `store/stream.go` `streamBucket` keyed by table name | run: put `old1`, delete, create again, `GetRecords` from `TRIM_HORIZON` returned the `INSERT old1` record, sequence 1 | wrong (data from another incarnation) | model: DeleteTable, "the corresponding stream on that table goes into the DISABLED state, and the stream is automatically deleted after 24 hours"; doc (Streams page, read): "If you disable a stream, any shards that are open will be closed. The data in the stream will continue to be readable for 24 hours." | Store test: put, delete, create, `TRIM_HORIZON`: no records and a new ARN. Decide whether the old ARN stays readable (doc: 24 hours) or is dropped; a stream's log belongs to the stream, not the table name |
| DDB-35 | P1 | **A failing `BatchWriteItem` leaves its earlier writes applied, and the order across tables is random.** Requests are validated one at a time as they are applied; AWS rejects the whole batch for a missing table, a key that does not match the schema, an item over 400 KB, or a key over its size limit. The loop over tables is a Go map, so which table's writes happen before a failure differs from run to run. | `dynamodb/items.go` `batchWrite` (second loop) | run: puts `b1`, `b2`, then a `pk` of the wrong type: `ValidationException`, and `b1`, `b2` were in the table | more forgiving (partial effect behind an error) | model: BatchWriteItem, "If one or more of the following is true, DynamoDB rejects the entire batch write operation: One or more tables specified in the BatchWriteItem request does not exist. Primary key attributes specified on an item in the request do not match those in the corresponding table's primary key schema. ... Any individual item in a batch exceeds 400 KB. ... Any individual items with keys exceeding the key length limits." | Handler test: three requests, the last invalid: none written; a missing second table: none written |
| DDB-36 | P1 | **A table named by ARN is refused.** Every operation takes "the name of the table ... You can also provide the Amazon Resource Name (ARN) of the table in this parameter", and `BatchGetItem` and `BatchWriteItem` key their `RequestItems` by name or ARN and echo it back. The handlers pass the string to the store as a name. | every handler's `req.TableName`; `store.GetTable` | run: `GetItem` with `arn:aws:dynamodb:us-east-1:000000000000:table/T`: `ResourceNotFoundException`, "Table: arn:aws:dynamodb:... not found"; `DescribeTable` likewise | stricter | model: the member documentation on 29 operations (counted from the model), e.g. GetItem.TableName; BatchGetItem.RequestItems, "A map of one or more table names or table ARNs"; ConsumedCapacity.TableName, "If you had specified the Amazon Resource Name (ARN) of a table in the input, you'll see the table ARN in the response." | Handler test per operation family with an ARN; an ARN for another account or region is `ResourceNotFoundException` (the code is `model`, the choice `memory`); `BatchGetItem` keyed by ARN answers keyed by ARN |
| DDB-37 | P1 | **A global index's multi-attribute key is accepted and cut to its last element.** A GSI may have up to four partition-key and four sort-key attributes. `keyParts` keeps the last `HASH` and the last `RANGE`, so the index is built on one attribute and the others vanish without a word; `Query` then asks for one partition key. | `dynamodb/tables.go` `keyParts`, `indexFromWire`; `store.Index` has one `Hash` and one `Range` | run: GSI `[HASH b, HASH c]` created, 200; `describe` shows one key attribute (read) | wrong shape (silently truncated) | model: CreateTable.GlobalSecondaryIndexes, "Each global secondary index supports up to 4 partition keys and up to 4 sort keys"; CreateGlobalSecondaryIndexAction.KeySchema. doc (GSI page, read): "you must specify all partition key attributes using equality conditions ... sort key attributes ... left-to-right ... Inequality conditions ... must be the last condition" | **Decide first:** build it (store index key becomes a list; Query rules above) or refuse a multi-element GSI key with a stated reason. Then an SDK test either way. Never truncate |
| DDB-38 | P1 | **The model's list and map length constraints are not in the generated tables.** 25 list and map shapes carry `length`; `dzaudit cases` emits string, number and enum constraints only, so "333 of 333" is true of a smaller set than it reads. Some are refused by hand in the handler's own words; the rest are not refused. | `dynamodb/validate.go` (no `[]` or `{}` length entry); `cmd/dzaudit/cases.go` | run: `GetItem` with `AttributesToGet: []` answered 200; `BatchExecuteStatement` with 26 statements answered 200; `TransactWriteItems` with 101: "TransactWriteItems accepts 1-100 operations, got 101"; `KeySchema: []`: "table needs a HASH key" | more forgiving, and wrong message | model: `KeySchema` min 1; `TransactWriteItemList` and `TransactGetItemList` 1 to 100; `BatchGetRequestMap`, `KeyList` 1 to 100; `BatchWriteItemRequestMap`, `WriteRequests` 1 to 25; `PartiQLBatchRequest` 1 to 25; `ParameterizedStatements` 1 to 100; `AttributeNameList`, `PreparedStatementParameters` min 1; `NonKeyAttributeNameList` 1 to 20 | Generator test: a list or map with one element too many and one too few, per operation, on the rejection-parity replay; the ledger count restated |
| DDB-39 | P1 | **Expired items are hidden at once; AWS keeps showing them until it deletes them.** Every read path, and the condition check of a write, treats an item past its TTL as absent. On AWS it is there, "typically" for up to days: it is read, queried, scanned, and **updated** (an `UpdateItem` on an expired item changes the item; here it builds a new one from the key and the old attributes are lost (read); `attribute_not_exists` succeeds here and fails there). An application that forgets the filter works here and shows expired rows on deploy. | `internal/ddb/store/store.go` `expired`; `store/items.go` `loadItem`; `store/query.go` | run: item with `ttl: 1000`, `GetItem` answered `{}`, `Scan` `Select: COUNT` excluded it | stricter (hides what AWS shows); the opposite of a missing rule | model: UpdateTimeToLive, "Items that have expired and not been deleted will still show up in reads, queries, and scans"; doc (TTL page, read): "You can still update the expired items that are pending deletion ... Use filter expressions to remove expired items from Scan and Query results." Corrected: section 5 "invisible to every read path" and section 6's "reads hide it at once" | **Decide (L8):** default as today, hiding at once (the sweep is a minute, AWS's is days: more forgiving by default), and `--ddb-strict` / `[dynamodb] strict = true` showing an expired item until the sweep, updatable, the condition seeing it. Test both. The Ddb-03 and Ddb-16 boards state today's behaviour |
| DDB-40 | P1 | **A write that changes nothing emits a stream record.** | `internal/ddb/store/stream.go` `captureStream` (called from `writeItem` for every put and update) | run: `PutItem` of the same item twice produced `INSERT` then `MODIFY` with equal images | more forgiving (extra record; a Lambda invoked for nothing) | doc (Streams page, read): "If you perform a PutItem or UpdateItem operation that does not change any data in an item, DynamoDB Streams does not write a stream record for that operation." | Store test: identical put and an update that sets the same value: no record; a real change: a record |
| DDB-41 | P1 | **A TTL deletion reaches the stream as an ordinary delete.** AWS marks the record with `userIdentity` of type `Service` and principal `dynamodb.amazonaws.com`, which is what a mapping's filter criteria and every archival handler tests for. | `dynamodb/streams.go` `streamRecordWire` (no `userIdentity`); `store.SweepTTL` calls `DeleteItem` | read | missing | doc (Streams and Time to Live page, read): "`Records[<index>].userIdentity.type` `"Service"`; `...principalId` `"dynamodb.amazonaws.com"`" | Sweep test: the record carries it, a user delete's does not; Lambda filter-criteria integration (section 9) |
| DDB-42 | P1 | **A table has one stream ARN for its whole life.** The label is the table's creation time, so a stream enabled, disabled and enabled again is the same stream, and a table dropped and created within the same second is the same ARN. AWS makes a new stream, with a new descriptor, each time it is enabled. A mapping or an iterator cannot tell the two apart (L6). | `internal/ddb/store/stream.go` `StreamLabel`, `StreamARN` | read; run: the ARN's label equalled the table's creation second | wrong | doc (Streams page, read): "When you set StreamEnabled to true, DynamoDB creates a new stream with a unique stream descriptor assigned to it. If you disable and then re-enable a stream on the table, a new stream is created with a different stream descriptor." | Enable, disable, enable: two ARNs; a shard iterator from the first is `ResourceNotFoundException`/expired on the second (decide with DDB-58) |
| DDB-43 | P1 | **`UpdateTable` has no rules and ignores most of its members.** One index change, or a throughput change, per call; the table is `UPDATING` and a second call is refused; a stream cannot be enabled twice. Run: a GSI create and a billing change in one call, and two GSI creates in one call, are accepted; enabling a stream on a streamed table is accepted; the empty request answers 200. The members dropped without a word: `StreamSpecification` (DDB-1), `TableClass` (DDB-13), a GSI `Update`'s throughput, `OnDemandThroughput`, `WarmThroughput`, `ReplicaUpdates`, `MultiRegionConsistency`, `GlobalTableWitnessUpdates`, `VectorIndexUpdates`. | `dynamodb/tables.go` `updateTable` | run | more forgiving; silent no-op | model: UpdateTable, "You can only perform one of the following operations at once: Modify the provisioned throughput settings of the table. Remove a global secondary index from the table. Create a new global secondary index on the table."; "You can create or delete only one global secondary index per UpdateTable operation"; "While it's UPDATING, you can't issue another UpdateTable request"; BillingMode "When switching from pay-per-request to provisioned capacity, initial provisioned capacity values must be set"; errors `ResourceInUseException`, `LimitExceededException` | Table-driven handler test per rule; every member either read, refused or cosmetic (section 10 box). The empty request: `memory`, skipped |
| DDB-44 | P1 | **A `Query` filter on a key attribute is accepted.** | `dynamodb/items.go` `query` (no check between `kc` and `filter`) | run: `FilterExpression: "sk > :s"` on the table's sort key returned 2 items | more forgiving (fails on deploy) | model: Query.FilterExpression, "A FilterExpression does not allow key attributes. You cannot define a filter expression based on a partition key or a sort key." Corrected: section 5 "(doc)" | Handler test on the table and on an index key; a `Scan` filter on a key stays accepted |
| DDB-45 | P1 | **A local index does not fetch from the table.** `Select: ALL_ATTRIBUTES` on a `KEYS_ONLY` local index returns the projection; a `ProjectionExpression` naming an attribute the index does not project returns `{}` per item. AWS reads the item from the table for both. (A global index is right: it is refused for `ALL_ATTRIBUTES` on a narrow projection.) | `internal/ddb/store/query.go` `projectIndex` (same for local and global); `dynamodb/items.go` `indexReadRules` | run: LSI `KEYS_ONLY`, `ALL_ATTRIBUTES` returned `{g, pk, sk}`; `ProjectionExpression: extra` returned `[{},{},{}]` | wrong shape (missing attributes) | model: Query.Select, "If you query a local secondary index, then for each matching item in the index, DynamoDB fetches the entire item from the parent table" and "If any of the requested attributes are not projected into the local secondary index, DynamoDB fetches each of these attributes from the parent table" | Store test for both on an LSI; the default `Select` on an index stays `ALL_PROJECTED_ATTRIBUTES` |
| DDB-46 | P1 | **PartiQL `Limit`, `NextToken` and `ConsistentRead` are ignored, and a SELECT is unbounded.** There is no page end at 1 MB, no `LastEvaluatedKey`, no `NextToken`. | `dynamodb/partiql.go` (`runPartiQL` reads only `Statement` and `Parameters`) | run: `SELECT` with `Limit: 1` returned 8 items; `NextToken: "abc"` returned page one | wrong | model: ExecuteStatement.Limit, "The maximum number of items to evaluate (not necessarily the number of matching items)"; NextToken length 1 to 32,768, "Set this value to get remaining results"; LastEvaluatedKey | Handler test: three items, `Limit: 1`, follow the token, three items in three pages |
| DDB-47 | P1 | **PartiQL is a subset, and refuses the rest as `ValidationException`.** `WHERE` takes `attr = value` joined by `AND` only. AWS accepts `OR`, `IN [...]`, `<`, `>`, `BETWEEN`, `begins_with`, `contains`, `ORDER BY`, `SELECT ... FROM "t"."index"` (here the table is read and the index token is ignored, read: `parseSelect` takes the first token after `FROM`), `UPDATE ... REMOVE`, `RETURNING ... *`, `SET` functions (`list_append`, `set_add`, `set_delete`), set literals `<<...>>`, and document paths. The ledger's tier says F. | `dynamodb/partiql_parse.go` (354 lines, hand-written) | run: each refused with "only `attr = value` equality conditions are supported", "conditions must be joined with AND" or "UPDATE requires SET ... WHERE ..." | stricter | doc (PartiQL select and update pages, read): the syntax blocks and the worked examples ("WHERE OrderID = 100 OR OrderID = 200", "WHERE OrderID IN [100, 300, 234]", "REMOVE AwardDetail.Grammys[2]", "RETURNING ALL OLD *") | Corpus test from the two pages' examples, each run against the classic operation it must equal. **Decide the scope**, because a full parser is days (section 1) |
| DDB-48 | P1 | **`BatchExecuteStatement` answers its own codes, not the model's.** A failed statement carries `Error.Code` `DuplicateItemException` or `ResourceNotFoundException`; the model's enum is `DuplicateItem`, `ResourceNotFound`, `ConditionalCheckFailed`, `ValidationError` and seven more. `TableName` is not set on a failed response. A batch of reads and writes is accepted; a read that does not name the whole key is accepted. | `dynamodb/partiql.go` `batchExecuteStatement` | run | wrong code, wrong shape; more forgiving | model: `BatchStatementErrorCodeEnum`; BatchStatementResponse.TableName, "The table name associated with a failed PartiQL batch statement"; BatchExecuteStatement, "The entire batch must consist of either read statements or write statements, you cannot mix both in one batch" and "Each read statement in a BatchExecuteStatement must specify an equality condition on all key attributes" | Handler test per code; mixed batch refused (`ValidationException`) |
| DDB-49 | P1 | **The S rows answer 400 `UnsupportedOperationException`, not 501 `NotImplemented`.** DynamoDB is still a `legacystub` service in the model sweep. Four families are refused and not listed in `docs/not-built.md` (resource policies, replica auto scaling, `ListContributorInsights`, `SearchVectors`; section 2 says so). | `dynamodb/dynamodb.go` `ServeHTTP` (`stubActions`) | run: `CreateBackup`: 400, `X-Amzn-Errortype: UnsupportedOperationException` | wrong code | model: no operation lists `UnsupportedOperationException`; plan decision D5 | The sweep baseline moves the 29 rows from `legacystub` to `notbuilt`; the console renders them as Not built (foundation A2) |
| DDB-50 | P2 | **Index counts and projection rules are not enforced.** 21 global and 6 local indexes are created; `INCLUDE` with no `NonKeyAttributes` is accepted; 100 across the table and 20 per index are not checked. | `dynamodb/tables.go` `createTable`, `indexFromWire` | run | more forgiving | model: CreateTable.LocalSecondaryIndexes "(the maximum is 5)", GlobalSecondaryIndexes "(the maximum is 20)", "The total count of attributes provided in NonKeyAttributes, summed across all of the secondary indexes, must not exceed 100", `NonKeyAttributeNameList` 1 to 20; doc (quotas page, read) the same. The code for the count is `memory` | Handler tests at 5, 6, 20, 21 and 101 attributes; the code goes to the recording queue |
| DDB-51 | P2 | **The continuous-backup and contributor-insight describes carry the wrong code and a thin shape.** `DescribeContinuousBackups` and `UpdateContinuousBackups` on a missing table answer `ResourceNotFoundException`; the model lists `TableNotFoundException`. With recovery on, the description has no `EarliestRestorableDateTime`, `LatestRestorableDateTime` or `RecoveryPeriodInDays`. `DescribeContributorInsights` has no rule list or `LastUpdateDateTime`, and neither contributor operation checks the index name or echoes the mode. | `dynamodb/tables.go` `describeContinuousBackups`, `describeContributorInsights` | run | wrong code, wrong shape | model: DescribeContinuousBackups and UpdateContinuousBackups errors; PointInTimeRecoveryDescription; DescribeContributorInsightsOutput | With DDB-12's tests |
| DDB-52 | P2 | **Nothing is asynchronous, and the shapes that say so are wrong.** `CreateTable` answers `ACTIVE` where the model says "immediately returns a response with a TableStatus of CREATING"; `DeleteTable` answers `ACTIVE` where its own example says `DELETING`; an index added by `UpdateTable` is `ACTIVE` with no `Backfilling`; the table is never `UPDATING`. A waiter passes on its first probe (the ledger says so on purpose). **Decision (L7), below.** | `dynamodb/tables.go` `describe` (hard-coded `"ACTIVE"`); `store.DeleteTable` | run: `DeleteTable` response `TableStatus: ACTIVE` | more forgiving (the in-between state is unobservable) | model: CreateTable, "CreateTable is an asynchronous operation ... You can perform read and write operations only on an ACTIVE table"; DeleteTable, "the specified table is in the DELETING state until DynamoDB completes the deletion ... DynamoDB might continue to accept data read and write operations"; TableDescription.TableStatus; GlobalSecondaryIndexDescription.Backfilling, IndexStatus | See section 12, L7 |
| DDB-53 | P2 | **TTL: the one-hour rule and the status.** A second `UpdateTimeToLive` within an hour is refused by AWS whatever it asks for; here only a request for the state the table is already in is refused, so enable-then-disable is accepted. `DescribeTimeToLive` is `ENABLED` or `DISABLED` at once; the model has `ENABLING` and `DISABLING`. The text "TimeToLive is already enabled" is `memory`. | `dynamodb/tables.go` `updateTTL`, `describeTTL` | run: enable, disable, enable in a row: all 200 | more forgiving (the second call fails on deploy) | model: UpdateTimeToLive, "It can take up to one hour for the change to fully process. Any additional UpdateTimeToLive calls for the same table during this one hour duration result in a ValidationException."; `TimeToLiveStatus` `ENABLING`, `DISABLING`, `ENABLED`, `DISABLED` | Opt-in under `--ddb-strict` (L8); `ENABLING` as in L7 |
| DDB-54 | P2 | **Key value sizes and document depth are not checked.** A 2,049-byte partition key and a 33-level map are stored. | `internal/ddb/store/store.go` `primaryKey`; `internal/ddb/item` | run | more forgiving | model: BatchWriteItem, "For a partition key, the limit is 2048 bytes and for a sort key, the limit is 1024 bytes"; doc (naming rules page, read): "complex data structures up to 32 levels deep" | Store tests at the limit and one past, on every write path |
| DDB-55 | P2 | **The 4 MB aggregate of a transaction is not checked**, on write or on read. | `internal/ddb/store/txn.go` | run: 11 puts of 390 KB were written, then read back by `TransactGetItems` | more forgiving | model: TransactWriteItems and TransactGetItems, "The aggregate size of the items in the transaction cannot exceed 4 MB" | Handler test; the code is `memory` (`ValidationException` is the natural one) |
| DDB-56 | P2 | **Query and Scan parameter rules.** `Select` `ALL_PROJECTED_ATTRIBUTES` on a table; `ProjectionExpression` together with a `Select` other than `SPECIFIC_ATTRIBUTES`; `SPECIFIC_ATTRIBUTES` with no projection; `Segment` without `TotalSegments` and the reverse; `Segment` not below `TotalSegments`; an `ExclusiveStartKey` for another segment; `begins_with` on a Number sort key; a non-projected attribute asked of a global index. All accepted. | `dynamodb/items.go` `query`, `scan`; `store/query.go` | run | more forgiving | model: Select, "ALL_PROJECTED_ATTRIBUTES - Allowed only when querying an index", "If you use the ProjectionExpression parameter, then the value for Select can only be SPECIFIC_ATTRIBUTES. Any other value for Select will return an error"; Scan.TotalSegments "If you specify TotalSegments, you must also specify Segment", Segment "must be less than the value provided for TotalSegments"; KeyConditionExpression, "You cannot use this function with a sort key that is of type Number"; Scan.ExclusiveStartKey | Table-driven handler test; messages `memory` |
| DDB-57 | P2 | **`ReturnValues` values the operation does not recognise are accepted** on `PutItem` and `DeleteItem` (`ALL_NEW`, `UPDATED_NEW` answered 200 and returned nothing). | `dynamodb/items.go` `putItem`, `deleteItem` | run | more forgiving | model: PutItem.ReturnValues, "PutItem does not recognize any values other than NONE or ALL_OLD"; DeleteItem the same | Rejection-parity cases |
| DDB-58 | P2 | **The Streams API has no validation, and its tokens are loose (L6).** No constraint table exists (no model on disk). `GetRecords` `Limit` above 1,000 is accepted (the model's `LimitExceededException` says it is refused); `ShardId` is not looked at; an iterator names a table, not a stream, and never expires; records beyond 1,000 per table are dropped where AWS keeps 24 hours; `ListStreams` and `DescribeStream` ignore their paging members; `TrimmedDataAccessException` and `ExpiredIteratorException` cannot happen. | `dynamodb/streams.go`; `store/stream.go` `streamMaxRecords` | run: `GetRecords` `Limit: 5000`; an iterator minted for `ShardId: shardId-bogus` worked | more forgiving; wrong | model: LimitExceededException, "GetRecords was called with a value of more than 1000 for the limit request parameter. More than 2 processes are reading from the same streams shard at the same time"; doc (Streams page, read): "The stream records within a shard are removed automatically after 24 hours". Iterator lifetime and the other codes: `memory` | Store and handler tests; fetch the Streams model into `.audit-models/` first (foundation B2's list) |
| DDB-59 | P3 | **Two messages the model quotes.** `BatchGetItem` over 100 keys: "Too many items requested for the BatchGetItem call." (the code says "BatchGetItem accepts 1-100 keys, got 101"). A cancelled transaction's message carries no reason list. | `dynamodb/items.go` `batchGet`, `transactionCanceled` | run | wrong message | model: BatchGetItem, "If you request more than 100 items, BatchGetItem returns a ValidationException with the message 'Too many items requested for the BatchGetItem call.'"; the reason-list suffix is `memory` | Exact-message test for the first |
| DDB-60 | P3 | **The ledger says things that are false or stale.** "ExecuteTransaction runs as TransactWriteItems" (DDB-33); "Verified against ... the dispatch table against ops_dynamodb.json" is true and "333/333" is the narrower number (DDB-38); `UpdateTable` F with no mention of its dropped members; `DescribeLimits` and `DescribeEndpoints` F (section 2). The 1 MB page bound is measured on the stored JSON's bytes, not DynamoDB's item size, so a page ends at a different item than on AWS. | `docs/SUPPORT.md`; `store/query.go` `scannedBytes` | read | n/a | n/a | Ledger test for each; the page bound goes to "Differences from AWS" |
| DDB-61 | P3 | **`DescribeTable` reports fixed or absent members.** `WarmThroughput` is the same whatever was sent (DDB-43); `OnDemandThroughput`, `TableClassSummary` (DDB-13), `LastIncreaseDateTime`, `LastDecreaseDateTime`, `BillingModeSummary.LastUpdateToPayPerRequestDateTime`, `Backfilling` and a global index's `ItemCount` and `IndexSizeBytes` are absent; `NumberOfDecreasesToday` is always 0. | `dynamodb/tables.go` `describe`, `warm` | run | wrong shape (thin) | model: TableDescription and its members | Describe test per member that has a source |
| DDB-62 | P2 | **The console loses a draft on a refused tag save, and prints the refused key back.** The Tags panel replaces itself with the stored tags ("No tags yet") and a line "Nothing was changed. Refused: writing "kkkk...(129)"". The board keeps the draft rows under the refusal. | `templates/panes.html` `tags_panel`, the save handler (foundation A5) | run: Save changes with a 129-character key | n/a | n/a | Browser test: after a refusal the typed rows are still there, and the key is not echoed whole |

Owned by the foundation, listed so they are not lost:

| Foundation | What it is here |
|---|---|
| A2 | The line under a refusal says "the rule comes from AWS's own service model" for hand-derived rules (`missing the key lineNo in the item`, `table x has deletion protection enabled`, `Table already exists`). The console's own refusals (invalid JSON, the key-move guard) have no code and no line. |
| A5 | Saving tags writes one tag at a time and stops at the first refusal. DynamoDB's `TagResource` takes a batch. |
| A6 | A deletion-protection refusal from the header's Delete button has no inline slot and arrives as a toast cut at 140 characters. |
| A7 | `/ddb` opens the first table, so the "Select a table…" line never renders. |
| A8 | `describeEndpoints` holds a literal region (DDB-14). |
| A11 | The create form's CLI preview omits the indexes and the TTL call. |

**Corrections to the grounding report.** Three things said then are restated
here. The item-size refusal number on a board was an illustration, not a
captured response; only the message's shape is real. The report listed "an
empty-string key is accepted" as one line among parity gaps; it stores wrong
data and is P0 (DDB-2). And the report did not test `UpdateTable` at all beyond
indexes; DDB-1, DDB-12 and DDB-13 were found while writing this page, and the
stream row on board Ddb-18 can only ever show a stream set at create until DDB-1
is fixed.

## 5. What to check

Beyond the common criteria. The item path was probed and mostly held; these
lines exist so that "mostly" becomes a test.

**Tables**
- [ ] Name: 3 to 255, `[a-zA-Z0-9_.-]`; the same rule wherever a table is named
      by name, and an ARN accepted where the model allows one. *Corrected 2026-10-11 (source):
      `model` for the rule (shape `TableName`); the ARN is DDB-36, which is
      refused today.*
- [ ] Key schema: exactly one `HASH`, at most one `RANGE`, distinct names, `HASH`
      first; every key attribute defined once, and no definition unused.
      *Corrected 2026-10-11: was stated for every index; `model`: a table and a local index
      have exactly this, but a **global** index may have up to four `HASH` and
      four `RANGE` (DDB-37), and "no definition unused" is `memory`.*
- [ ] A key attribute's type is S, N or B, and one name has one type across the
      table and every index.
- [ ] Create with a name in use: `ResourceInUseException`. Describe, update and
      delete of a missing table: `ResourceNotFoundException`. *Corrected 2026-10-11: `model`,
      with two exceptions: the continuous-backup operations name
      `TableNotFoundException` (DDB-51), and `DeleteTable` on a table already
      `DELETING` "no error is returned".*
- [ ] `BillingMode` and `ProvisionedThroughput`: the five refusals in
      `billingFor`, and a switch in each direction through `UpdateTable`.
- [ ] Deletion protection: on at create, on and off by update, delete refused
      while on, with the code and message AWS uses (not established).
- [ ] `UpdateTable` with no change at all: what AWS answers is not established.
- [ ] `ListTables` pages (DDB-9); `Limit` above 100 is refused by the model.
- [ ] Every member in section 2's "not established" rows is either read or refused.

**Indexes**
- [ ] Global: name 3 to 255 and pattern (model); unique within the table;
      projection type and, for `INCLUDE`, `NonKeyAttributes` present.
- [ ] Local: needs a table sort key, shares the partition key, cannot be added
      or removed by `UpdateTable`.
- [ ] The limits on index count per table: 5 local and 20 global, 100 projected
      non-key attributes in all, 1 to 20 per index. *Corrected 2026-10-11: was "(doc): decide
      whether to enforce"; `model` ("the maximum is 5", "the maximum is 20"), so
      they are enforced (DDB-50).*
- [ ] Add by `UpdateTable`: existing items are indexed before the call returns;
      delete reclaims the index; one index operation per call. *Corrected 2026-10-11: was "(not
      established)"; `model`: "You can create or delete only one global secondary
      index per UpdateTable operation". On AWS the index is `CREATING` with
      `Backfilling` until it is complete and the table is `UPDATING` (DDB-43,
      DDB-52).*
- [ ] An item missing an index key is absent from the index and comes back when
      the attribute is set; an item whose index key changes moves.
- [ ] A read through a `KEYS_ONLY` and an `INCLUDE` **global** index returns
      exactly the projected attributes; `Select: ALL_ATTRIBUTES` on one is
      refused. A read through a **local** index fetches what it needs from the
      table. *Corrected 2026-10-11: was one line for both kinds of index; `model`
      (Query.Select), DDB-45.*
- [ ] `ConsistentRead` on a global index is refused; on a local index accepted.

**Items**
- [ ] Key values: present, correctly typed, not empty (DDB-2), within the key
      size limits (2048 bytes partition, 1024 sort). *Corrected 2026-10-11: was "doc; not
      established here"; `model` (BatchWriteItem) and `doc` (naming rules page);
      unchecked today (DDB-54).*
- [ ] The 400 KB rule on PutItem and on the result of UpdateItem, counting
      attribute names.
- [ ] Every type round-trips: S, N, B, BOOL, NULL, M, L, SS, NS, BS; empty sets
      and duplicate set members refused; nesting depth 32 (`doc`, naming rules
      page; 33 is accepted today, DDB-54). An empty string or binary is allowed
      anywhere but in a key (`model`, PutItem.Item), and the code is right.
- [ ] Numbers: 38 significant digits, the exponent range (1E-130 to
      9.9999999999999999999999999999999999999E+125, `doc`), and that `1.0` and
      `1` are one key.
- [ ] `ReturnValues` on each operation, including the values an operation does
      not allow (`model`: "PutItem does not recognize any values other than NONE or
      ALL_OLD", and DeleteItem; accepted today, DDB-57);
      `ReturnValuesOnConditionCheckFailure` puts the item in the error
      (`model`: `ConditionalCheckFailedException.Item`).
- [ ] UpdateItem: each clause; two paths that overlap; a key attribute in any
      clause; an update that creates the item.
- [ ] Legacy parameters (`Expected`, `ConditionalOperator`, `AttributeUpdates`,
      `KeyConditions`, `ScanFilter`, `QueryFilter`, `AttributesToGet`): work, or
      are refused, and never silently ignored. Mixed with an expression: refused.
      *Corrected 2026-10-11: was "Not established"; run: all but `KeyConditions` are silently
      ignored (DDB-32); the refusal on mixing is `memory`.*

**Expressions**
- [ ] Reserved words refused unaliased; an unused `#name` or `:value` refused;
      an undefined one refused. (All three held when probed; keep the tests.)
- [ ] Every function and operator in the condition grammar, with operand type
      errors.
- [ ] Key conditions: equality on the partition key only; the seven sort-key
      forms; a non-key attribute refused.
- [ ] A filter may not name a key attribute in a Query; in a Scan it may.
      *Corrected 2026-10-11: `model` (Query.FilterExpression), not `doc`; accepted today,
      DDB-44.*

**Query and Scan**
- [ ] `Limit` counts items read before the filter (held when probed).
- [ ] A page ends at 1 MB with `LastEvaluatedKey`; the key resumes exactly,
      forwards and backwards, on a table and on an index with duplicate index keys.
- [ ] `ExclusiveStartKey` that does not fit the schema is refused.
- [ ] `Select: COUNT`; `ProjectionExpression` with `Select` (the model: only
      `SPECIFIC_ATTRIBUTES`); `ScanIndexForward`; `begins_with` never on a Number
      sort key; `Segment` and `TotalSegments` together, `Segment` below
      `TotalSegments` (DDB-56).
- [ ] Parallel scan: every item appears in exactly one segment.

**Batches and transactions**
- [ ] BatchWriteItem 1 to 25, BatchGetItem 1 to 100, duplicates refused in both
      (DDB-10), a missing table fails the whole call.
- [ ] `UnprocessedItems` and `UnprocessedKeys` are always present and, here,
      always empty; the 16 MB response bound (`model`, BatchGetItem and
      BatchWriteItem). A failing batch writes nothing (DDB-35).
- [ ] TransactWriteItems: 1 to 100, one operation per item, per-item
      `CancellationReasons` in order, the idempotency token's ten-minute window
      and `IdempotentParameterMismatchException`, the 4 MB total. *Corrected 2026-10-11: all
      `model` (TransactWriteItems: "valid for 10 minutes after the first request
      that uses it is completed"; "The aggregate size of the items in the
      transaction cannot exceed 4 MB"); the 4 MB is unchecked (DDB-55). The code
      for `None` in a reason is the literal string "None" and its message is
      omitted (`model`).*
- [ ] TransactGetItems: 1 to 100, a missing item is an empty entry in order.

**PartiQL**
- [ ] SELECT with a full key is a point read; otherwise a filtered scan, and
      `Limit` and `NextToken` behave as Scan's paging does. *Corrected 2026-10-11: was "(not
      established)"; `model` (ExecuteStatement) says they do; run: they are
      ignored (DDB-46). The grammar is DDB-47.*
- [ ] INSERT of an existing key is refused; UPDATE and DELETE need the full key.
- [ ] `?` parameters: too few, too many, wrong type.
- [ ] BatchExecuteStatement returns per-statement errors without failing the
      batch (with the model's codes, DDB-48); ExecuteTransaction is all or
      nothing. *Corrected 2026-10-11: run: it is not (DDB-33).*

**TTL**
- [ ] Enable when enabled and disable when disabled are refused (held).
      *Corrected 2026-10-11: that is the code's rule, and the model's is a time rule: "Any
      additional UpdateTimeToLive calls for the same table during this one hour
      duration result in a ValidationException" (DDB-53, opt-in under
      `--ddb-strict`, L8).*
- [ ] Only a Number attribute expires; a string of digits does not.
- [ ] An expired item is invisible to every read path, including indexes,
      batches, transactions and PartiQL, before the janitor removes it.
      *Corrected 2026-10-11: this is what the code does and it is **not what AWS does**:
      `model`, "Items that have expired and not been deleted will still show up
      in reads, queries, and scans". Decision DDB-39.*
- [ ] `ItemCount` and the console's count with expired items present: decide
      which they report.
- [ ] A TTL removal reaches the stream, and how the record is marked.
      *Corrected 2026-10-11: was "(not established; AWS marks it with a service identity)";
      `doc` (Streams and Time to Live page): `userIdentity` type `Service`,
      principal `dynamodb.amazonaws.com`. Missing today (DDB-41).*

**Streams**
- [ ] Each view type carries exactly the images it names.
- [ ] Enable, disable and re-enable through `UpdateTable` (DDB-1); a re-enable
      makes a new stream ARN (`doc`, Streams page; the ARN is the table's
      creation time today, DDB-42); enable on a streamed table and disable on one
      with no stream are `ValidationException` (`model`).
- [ ] Iterators: `TRIM_HORIZON`, `LATEST`, `AT_SEQUENCE_NUMBER`,
      `AFTER_SEQUENCE_NUMBER`; a malformed one refused; one for a deleted table.
- [ ] A write that changes nothing emits no record. *Corrected 2026-10-11: `doc` (Streams
      page, read), and run: it does emit one (DDB-40).*
- [ ] Record retention: AWS keeps 24 hours (`doc`). *Corrected 2026-10-11: was "what is kept
      here ... is not established"; read: the newest 1,000 records per table
      (`streamMaxRecords`), counted, not timed, and per table name, not per
      stream (DDB-34, DDB-58).*

**Tags**
- [ ] 50 per table, key 1 to 128, value 0 to 256, `aws:` reserved (DDB-11):
      all `model` (the Tag shape's documentation). `LimitExceededException`
      is the code the operation lists.
- [ ] The ARN is parsed, and one for another account or region is refused.

**Lifecycle**
- [ ] Everything above survives a restart: tables, indexes, TTL setting, tags,
      the stream's records and sequence numbers, the transaction token window.
- [ ] Deleting a table removes its items, indexes and stream; a table created
      again under the same name starts empty. *Corrected 2026-10-11: run: the stream's records are
      not removed (DDB-34). The idempotency-token window is the application's
      state (L9) and persists, as it does (`txBucket`); the TTL sweep is the
      process's and does not.*

## 6. Console

The design's "DynamoDB" page: 20 boards. Templates: `templates/ddb.html`
(`ddb_lp`, `ddb_home`, `ddb_table`, `ddb_details`, `ddb_explorer`,
`ddb_filter_vals`, `ddb_item_table`, `ddb_item_rows`), the `ddb_create` block in
`create.html`, shared pieces in `panes.html` and `layout.html`. Handlers:
`handlers_ddb.go`; client: `client_ddb.go`; routes: `routes_storage.go`
`ddbRoutes` (3 GET, 11 POST).

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Ddb-Home | The first table, Items, scanned | `ddb_table`, `ddb_explorer`, `ddb_item_table`; `ddbTables`, `ddbTable` | exists; the facts line is **new** |
| Ddb-01-None | No tables yet | `ddb_home`, `lp_empty`, `empty_detail` | exists (copy: DDB-30) |
| Ddb-02-Create | Create | `ddb_create`; `ddbCreateTable` | form exists; teaching lines, the capacity note and "How this table is read" are **new** |
| Ddb-03-Create-Variant | Sort key, an index, TTL, CLI open | `ddb_create`, `cli` | form and preview exist. *Corrected 2026-10-11: A11 is half built; the preview already shows the `update-time-to-live` call when TTL is on (run), and still omits the indexes.* |
| Ddb-04-Create-Refused | Refused; the late TTL refusal | `fail_inline` | exists (DDB-19) |
| Ddb-05-Items-Empty | A new table | `ddb_item_table` | exists |
| Ddb-06-Item-Open | An item in the drawer | `ddb_item_rows` (the drawer is built in the row's click handler) | exists; the two notes are **new** |
| Ddb-07-Scan-Filter | Filter with typed bindings, discarded count | `ddb_explorer`, `ddb_filter_vals`; `ddbExplore` | exists; "a filter that matches nothing" is **new** (DDB-23) |
| Ddb-08-Scan-Slow | A scan in flight, wide items, load more | `shell.js` slow-request bar; `ddb_item_rows` | exists |
| Ddb-09-Query | Query on an index | `ddb_explorer`; `ddbExplore`, `QueryItems` | exists |
| Ddb-10-Read-Refused | Get and PartiQL refused | `ddb_explorer`, `fail_inline` | exists |
| Ddb-11-Bulk | Selection bar, transactional switch | `ddb_explorer` (`bulkbar`); `ddbBatchGet`, `ddbBatchDelete` | exists |
| Ddb-12-Item-Add | Add item with the key skeleton | `ddb_table` (the put dialog); `ddbPutItem` | exists; "how the JSON is read" is **new** |
| Ddb-13-Item-Add-Refused | Refused | `fail_inline` | exists |
| Ddb-14-Item-Edit | Edit refused for moving the key; Update item | `ddb_table` (put and update dialogs); `ddbPutItem`, `ddbUpdateItem` | exists; "this item holds a set" is **new** (DDB-20) |
| Ddb-15-Confirms | Five confirms; delete refused by protection | `hx-confirm`; `ddbDeleteTable` | confirms exist; *Corrected 2026-10-11: the inline refusal is built (run: a purple "Refused ValidationException" under the header on the protected table), A6 is done* |
| Ddb-16-Details | Key schema, TTL on, an index, add index | `ddb_details`; `ddbSetTTL`, `ddbAddGSI`, `ddbDeleteGSI` | exists; the sentences are **new**; the Size row is dropped (DDB-26) |
| Ddb-17-Details-Fresh | No TTL, no indexes; add index refused | `ddb_details`, `fail_inline` | exists |
| Ddb-18-Details-Stream | A table made from code: provisioned, protected, stream | `ddb_details` | Billing is wrong today (DDB-22); four rows and the neighbour chip are **new** |
| Ddb-19-Tags | Tags as a draft, one refused | `tag_editor`; `tagsSave` | exists (A5) |

Not drawn, and why: a list with nothing selected (foundation A7); a denied
state (the console acts as root and DynamoDB has no resource policy here, so no
console action can be denied).

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Facts line under the name: item count, key, indexes, TTL attribute | `DescribeTable` and `DescribeTimeToLive`, both already called by `client_ddb.go` `DescribeTable` |
| How this table is read | The create form's key and index fields. A pure function of the form. |
| Teaching lines under each field | The settings table (foundation B4). DynamoDB needs more than ranges from it: see below. |
| Billing, deletion protection, stream, stream ARN, encryption rows | `BillingModeSummary`, `ProvisionedThroughput`, `DeletionProtectionEnabled`, `StreamSpecification`, `LatestStreamArn`, `SSEDescription` in the `DescribeTable` response; needs fields on `console.Table` |
| "stream read by" chip | `Neighbors("ddb", name)`: the wiring graph's `esm` edge, whose source resolves from the stream ARN to the table (`client_flow.go`, `resourceurl.go`) |
| A filter that matches nothing; more to read | `Count`, `ScannedCount` and the cursor from the Scan or Query |
| This item holds a set | The item's stored attribute types, which `itemsFromAV` has before it flattens them |
| How the JSON is read | Static; it restates `toAV`. Remove it if DDB-20 is fixed by a typed editor. |
| The delete refusal under the header | Foundation A6 |

**Where the teaching lines' facts come from**

| Line | Source |
|---|---|
| Table name 3 to 255, letters digits `_ . -`, "checked by this form" | The browser `pattern` in `ddb_create`. After DDB-5 it reads "enforced" and comes from the hand-derived rule. |
| Partition and sort key: fixed at creation; Get needs the whole key; Query names the partition key | doc. Key attribute name 1 to 255 and type S, N, B: model (`CreateTable` in `validate.go`). "Enforced": `primaryKey`. |
| Index name 3 to 255 and pattern, enforced | model |
| Index: every write keeps it in step; an item without its keys is not in it | read (`store.go` `writeItem`, `indexKeyFor`); doc for the sparse rule |
| Index: all attributes copied | The console always sends `ProjectionType: ALL` (`client_ddb.go`) |
| Index added later is complete when the call returns | Ledger, UpdateTable row |
| TTL: Unix seconds in a Number; reads hide it at once; a janitor sweeps every minute | read: `store.go` `expired`, `dynamodb.go` `janitor`. Ledger, UpdateTimeToLive row. |
| TTL attribute name 1 to 255 | model |
| TTL is switched, not set | run: `TimeToLive is already enabled` |
| Capacity: stored and reported, not metered | Ledger, "Differences from AWS" |
| Deletion protection: enforced | Ledger; run |
| Stream: one open shard; a mapping can read it | Ledger, Streams row |
| Encryption: reported only | Ledger, "Differences from AWS" |
| Limit counts items read, before the filter | run; ledger, Query row |
| Item at most 400 KB; number up to 38 digits | read: `item.MaxItemSize`, `decimal.go`; both doc |
| Tag key 1 to 128, value 0 to 256 | model |
| The two switch positions of "transactional" | read: `BatchGetItems`, `BatchDeleteItems` |

Foundation B4 as written returns range, enum, default and tier per setting.
Four of DynamoDB's lines are not a range: a rule enforced only by the browser,
a fact about when a setting can change, a tier that differs by operation
(`StreamSpecification`), and settings the console can read and not write. The
accessor needs a "source of enforcement" and a "settable from" field, or these
stay typed prose.

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Ddb-18: "An item removed by TTL … reaches the stream too." *Run 2026-10-11 at the
  code: it does, as an ordinary `REMOVE` without `userIdentity`; the board now says
  what AWS adds (DDB-41, `doc`).*
- Ddb-18: "Each record carries the item as it was and as it is now." *`model`
  (StreamSpecification.StreamViewType).*
- Ddb-03, Ddb-16: "Reads hide an expired item at once; a background janitor …
  sweeping every minute." True of the code (run, read); **not AWS** (DDB-39). Say
  so beside it once the decision is made.
- Ddb-16: "Fixed once the table exists." doc.
- Ddb-16: "An exact count, including items that have expired and not yet been
  swept." read (`CountItems` counts keys); section 5 asks for a decision.
- Ddb-15: "Items stop expiring from then on. Nothing already removed comes back."
  Not run.
- Ddb-15: after Delete table the page opens "on the first table left". Read
  from the redirect.
- Ddb-13: the number in "item size 412160 exceeds the 409600 byte limit" is
  illustrative.
- Ddb-09: "'Scanned' counts what the key condition selected." *Source `model`
  (Query.ScannedCount, "The number of items evaluated, before any QueryFilter is
  applied").* And, corrected in the working copy: "Rows are the base table's
  items, whichever index found them" was false for a narrow index (DDB-21, DDB-45).
- Ddb-08: "The rows below … stay until the new one lands." htmx behaviour, not
  checked. The bar shows for any non-GET request slower than 300 ms, not only a scan.
- Ddb-04, Ddb-10, Ddb-13, Ddb-17, Ddb-19: each refusal carries today's "the rule
  comes from AWS's own service model" line, which A2 replaces.
- Ddb-02: "Unique in this region and account." doc.

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each of the 20 boards of the dream design
(`console-design/project/Ddb-*.dc.html`, 1440 by 900, light; dark for Home,
Create and Details) and the real page in the same state, side by side: the real
console booted on its own port with five tables, a GSI, a TTL setting, a
provisioned, protected, streamed table and tags made through the API, then each
page driven with Playwright, a dialog given 500 ms. Screenshots:
`scratchpad/dcheck/ddb/design-*.png` and `real-*.png`. The shell (the labelled
rail, the region and theme controls, the 900-wide banner) is the foundation's and
differs from every board in every service; it is not compared here, as in SQS.
What follows is the page.

| Board | Verdict | What differs |
|---|---|---|
| Ddb-01 None | **matches** | The list pane and the empty detail are the foundation's templates; the heading line "No tables yet" above the sentence is not drawn (`empty_detail`, as in Sqs-01). **The design's sentence was wrong**: "turn on a stream" (the console has no stream control, and until DDB-1 UpdateTable ignores it). Corrected in the working copy; `copy.go` still says it (DDB-30). |
| Ddb-Home | **differs** | The facts line under the name ("6 items · key orderId + lineNo · index by-depot · arn …", `new`) is not there: the page has the header, an `arn` chip cut at 40 characters and the tabs. The explorer is a bordered card with a labelled "Filter expression" field and a "Limit" label, where the board has one inline row (segmented control, expression, "limit", Scan). The trailing paragraph "Columns are the attributes the page's items carry…" is missing. The bin appears on hover only. |
| Ddb-02 Create | **differs** | One column of four panels, where the board is two columns (form, and on the right "How this table is read" and the "Capacity" card). **None of the teaching lines exist**: no purpose sentence under a field, no range and "enforced" line (name 3 to 255, key name 1 to 255), no neighbour line. The subtitle reads "Add secondary indexes and TTL here — or later via the SDK" (DDB-30); the board's is "or later from the table's Details tab". The Sort key row has one line of sentence ("A second key dimension for ordered items") where the board has two and a line 4. |
| Ddb-03 Create-Variant | **differs** | Everything in Ddb-02, plus: the cli opens as an inline block under the form (a card on the right in the board); the index and TTL panels have no sentence or line 4; "How this table is read" has no entry for the index or for TTL. **The board's note was wrong**: it said the TTL setting is not in the preview, and it is (run: two lines, create-table and update-time-to-live); corrected in the working copy. |
| Ddb-04 Create-Refused | **differs (small)** | The refusal is inline under the Create button, the form is kept, and the code and message are the service's: matches. Missing: the link to the existing table ("harbour-orders →") and, for the late TTL refusal, "The table →" (DDB-19 reproduced: the table exists after the refusal, and the page offers no way to it). The footer sentence is the kit's, not the board's. |
| Ddb-05 Items-Empty | **differs** | The facts line (index, "expires on expiresAt") is missing. The empty state is a bare database glyph and the sentence, outside a card. |
| Ddb-06 Item-Open | **differs** | The drawer is a side drawer with the JSON and the two buttons; missing the two notes (`new`: "Plain JSON, read out of DynamoDB's types…", "Edit item replaces the whole item…"), the "esc" label, the highlighted row, and the syntax colours. |
| Ddb-07 Scan-Filter | **differs** | The filter, the typed binding row and the result line "1 item returned · 6 scanned, 83% discarded by the filter" match. Missing: the limit's line 4 ("1 and up · default 50 · counts items read, before the filter · enforced"), the sentence beside the binding row, the paragraph under the result ("The filter ran after the read…"), and the `new` empty states. **DDB-23 reproduced**: limit 1 with a filter matching nothing on the first page prints "No items yet — add one, or write via the SDK" and gives no way on. |
| Ddb-08 Scan-Slow | **matches** | Wide items collapse to a single Attributes column of compact JSON, cut with an ellipsis, with Load more. The slow-request bar is `shell.js` (300 ms); not captured here. |
| Ddb-09 Query | **differs** | The index select, the partition-key field, the sort-key operator, the filter and the result line match. Missing: the key type under each key field ("depot · String", "shippedAt · Number"), the sort-key pair on one line, and line 4 under the form. The board's footnote was wrong for a narrow index (corrected). |
| Ddb-10 Read-Refused | **matches** | The refusal is inside the form under the hint, the results below are left as they were, on Get and on PartiQL. The footer sentence is the kit's. |
| Ddb-11 Bulk | **matches** | The bar above the table, "2 selected", Re-read, Delete selected, the "transactional" switch with its two sentences, Clear; after a re-read "2 items read in one snapshot (TransactGetItems)" (run). The cards under the table in the board are annotation. The two refusal texts are the code's, verbatim (`client_ddb.go:820`, `:886`). |
| Ddb-12 Item-Add | **differs** | A dialog with a CodeMirror editor (valid-JSON bar, Format), prefilled with the key skeleton: matches. Missing: "orderId is a String and lineNo a Number…" and the "How the JSON is read" panel (`new`). **Run: Save item on the untouched skeleton stores an item whose key is `""`** (DDB-2): the dialog's own prefill is a write AWS refuses. |
| Ddb-13 Item-Add-Refused | **matches** | The refusal under Save item inside the dialog, the text kept. (A missing `lineNo`: "missing the key lineNo in the item".) |
| Ddb-14 Item-Edit | **differs** | Edit item and Update item dialogs are as drawn, with the typed binding rows appearing as tokens are typed. Missing: "This item holds a set" (`new`, DDB-20). Run: Edit through a `KEYS_ONLY` index shows only the projected attributes and Save would drop the rest (DDB-21). |
| Ddb-15 Confirms | **differs, deliberately** | The confirm is the kit's: the sentence is the title, the buttons are Cancel and a verb ("Delete"), with no "Are you sure?" and no "Confirm". The sentences are the board's exactly. The "After it:" captions are annotation. The protected-table refusal is inline under the header (matches; the board's `new` is built). Decision: keep the kit's confirm; the board's "Confirm" is stale against it. |
| Ddb-16 Details | **differs** | Two columns, as drawn. The key schema is a two-column table where the board has three (label, value, sentence); the rows "Sort key none", "Key schema enforced" and every sentence are missing; the Size row is still there ("0 B"; the board drops it, DDB-26); Billing is the literal PAY_PER_REQUEST (DDB-22). TTL has no date example or line 4. The index table has no bin until hover, and no "Query it from Items → Query → Index" note. Add index has no line 4 and no "Existing items are indexed before the call returns…". |
| Ddb-17 Details-Fresh | **differs** | As Ddb-16, plus the TTL form's two sentences and line 4. The add-index refusal is inline under the button (matches; run, "ix"). **The board's "as on AWS" was unsourced**; corrected. |
| Ddb-18 Details-Stream | **cannot match yet** | Waits on DDB-22 (`console.Table` carrying billing mode, capacity, deletion protection, stream specification and ARN, SSE: run, the provisioned and protected, streamed table shows PAY_PER_REQUEST and no other row), on the neighbour chip (`Neighbors("ddb", name)`, foundation D), and on DDB-1 for the stream to be settable at all. |
| Ddb-19 Tags | **differs** | The tag rows (key, value, bin), "Add tag" and "Save changes" are the kit's and match foundation A5. Missing: the line "key 1 – 128 characters · value 0 – 256 · enforced"; "Unsaved changes" and Cancel (run: Save is disabled until a change, then the controls the board shows). **DDB-62**: after a refusal the draft is gone and the key is printed back. |

At 900 wide, the main page: the rail collapses to marks, the console's own
notice appears, the key column wraps ("HRB–2026–04811" over three lines), and
the explorer and the details stay two-column. In dark: Home, Create and Details
are the board's shape with the kit's colours. Both are the foundation's, but the
wrapped key is this page's (build list 9).

**Where the design is wrong** (corrected in the working copy on 2026-10-11 in
Ddb-01, Ddb-03, Ddb-09, Ddb-17 and Ddb-18; the published canvas still carries the old text)
- "turn on a stream and every write can drive a Lambda" on Ddb-01: the console
  has no stream control and UpdateTable ignores a stream specification (DDB-1,
  DDB-30). Now "create it with a stream, from your code or template".
- "The preview … the TTL setting [is] not in it" on Ddb-03: it is, in the
  template's second line (run). The board's preview now has both lines and the note
  says the index is what is missing.
- "Rows are the base table's items, whichever index found them" on Ddb-09: a row
  carries what the index projects; the console's own indexes project everything,
  an index made from code may not (DDB-21, DDB-45).
- "TTL is switched rather than set, as on AWS" on Ddb-17: the model's rule is a
  time rule (a second change within an hour is refused); "already enabled" is
  the code's own, and `memory` for AWS (DDB-53).
- "An item removed by TTL is an ordinary delete … so it reaches the stream too"
  on Ddb-18: on AWS the record carries `userIdentity` (DDB-41, `doc`).
- Not wrong but now conditional: Ddb-03's and Ddb-16's "reads hide an expired item
  at once" is the code and not AWS (DDB-39); and Ddb-16's "Status ACTIVE … a
  waiter passes on its first check" is the L7 decision below.

**Build list, in order** (templates, handlers and CSS; the one behaviour a board
needs is named)
1. `console.Table` carries what `DescribeTable` already returns (billing mode
   and capacity, deletion protection, stream specification, ARN and label, SSE,
   local indexes, table class, item count, TTL status) and the Details rows read
   it (DDB-22, DDB-26). Needs nothing else.
2. The facts line under the name, on every table page (Home, Ddb-05, and the
   rest): items, key, indexes, TTL attribute; the ARN beside it, whole.
3. Create as two columns: the five teaching lines on every field from the
   settings record (name 3 to 255 and pattern; key name 1 to 255; index name; TTL
   name 1 to 255; billing; "enforced" or "stored"), "How this table is read"
   drawn from the key fields, the Capacity card, the cli as a right-hand card,
   the subtitle fixed (DDB-30). The one-outcome create (DDB-19) lands here.
4. Details as three columns with a sentence on each row, "Sort key none" and "Key
   schema enforced" rows, the TTL card's example and line 4, the index table's
   bin and "Query it from…" note, the add-index line 4 and its sentence.
5. Explorer: the limit's line 4, the typed-binding sentence, the key type under
   each Query key field, the post-result paragraph, and the two empty states with
   "Load more" (DDB-23); a mutation keeps the query (DDB-24); the limit travels with
   the cursor (DDB-29).
6. Add and Edit dialogs: the field-type sentence and "How the JSON is read"; "This
   item holds a set" and the typed editor decision (DDB-20, DDB-21); the
   skeleton is refused as it stands (DDB-2 is the fix; the dialog needs nothing).
7. The drawer's two notes and the highlighted row.
8. Tags: the line 4 and, on refusal, the draft kept and no echo of the key
   (DDB-62).
9. The key column does not wrap (`nowrap` on the key cells), so 900 wide is legible.
10. The links in the refusals: to the existing table (Ddb-04), and to the table
    just created (late TTL).
11. Ddb-18: the neighbour chip and the stream rows, once DDB-1 and foundation D
    are in. **Behaviour a board needs: DDB-1** (UpdateTable enables and disables
    a stream), and DDB-42 so the ARN the row shows changes on re-enable.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store (`internal/ddb/store`) | Empty key values (DDB-2); mistyped index key (DDB-3); index maintenance on key change and delete; restart with a stream and a TTL setting; a delete and re-create leaves no stream record (DDB-34); a no-change write emits none (DDB-40); a TTL sweep's record carries `userIdentity` (DDB-41); a local index fetches from the table (DDB-45); key size and depth (DDB-54); an expired item under both settings of `--ddb-strict` (DDB-39) |
| Handler (`dynamodb/`) | Name rule (DDB-5); the schema cases (DDB-4, DDB-6, DDB-7); index delete and update of a missing index (DDB-8); duplicate batch keys (DDB-10); tag limit and ARN (DDB-11); every member in section 2's "ignored" rows (DDB-43); the legacy parameters (DDB-32); an ARN for every operation family (DDB-36); a failing BatchWriteItem writes nothing (DDB-35); a multi-attribute index key (DDB-37); the list and map lengths (DDB-38); `UpdateTable` one change at a time (DDB-43); a Query filter on a key (DDB-44); the Query and Scan rules (DDB-56); `ReturnValues` on Put and Delete (DDB-57); the 4 MB transaction (DDB-55) |
| SDK v2 | Stream enable, disable, re-enable by `UpdateTable` (DDB-1, DDB-42); `ListTables` paging (DDB-9); PITR and Contributor Insights round trip (DDB-12, DDB-51); `TableClass` (DDB-13); endpoint discovery (DDB-14); consumed capacity (DDB-15); `ExecuteTransaction` atomic, with a token (DDB-33); PartiQL `Limit` and `NextToken` (DDB-46); `BatchExecuteStatement` codes (DDB-48); the PartiQL corpus (DDB-47) |
| SDK v1 | `Expected`, `AttributeUpdates`, `ScanFilter`, `AttributesToGet`, `QueryFilter` and `KeyConditions` (DDB-32): the v1 client still sends them, which is why this is the layer |
| Sweep baseline | The 29 S rows move from `legacystub` to `notbuilt` (DDB-49) |
| Rejection parity | The hand-derived rules join the replay, each with its `model` or `doc` source beside it; the generator emits list and map lengths first (DDB-38), and the Streams model is fetched so its constraints replay too (DDB-58) |
| Restart | Tables, indexes, tags, TTL, stream records, transaction tokens |
| Conformance (boto3) | One scenario per row of section 8 |
| Console unit (`internal/console`) | Create with a conflicting index key (DDB-18); late TTL refusal (DDB-19); `QueryItems` binding collision (DDB-25); empty page with a cursor (DDB-23); limit carried by the cursor (DDB-29); missing table as a page (DDB-27); Details facts for a table made from code (DDB-22) |
| Browser | Edit round trip of SS, NS, B (DDB-20); Edit through a KEYS_ONLY index (DDB-21); a mutation keeps the query (DDB-24); each `new` item; each confirm's sentence; the delete refusal inline |
| Mutation | `dynamodb/` and `internal/ddb/...` baselines, then survivors in `tables.go`, `store.go`, `query.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today, except where it says `model`
(established from the model's text, but still unrecorded: nothing here has met
AWS). *Corrected 2026-10-11 (sources): the table's first column was mostly `doc` with no page
named; each is re-tagged below, and a `doc` now names the page read.* Scenarios
go in `conformance/tests/test_dynamodb.py`.

| Claim | Source now | Scenario |
|---|---|---|
| Table name rule | model (shape `TableName`); text memory | `table_name_rules` |
| A key schema of the wrong element count or order | model (CreateTable.KeySchema); text memory | `key_schema_rules` |
| An attribute definition no key uses; the same attribute as HASH and RANGE | memory | `key_schema_unused_definition` |
| A local index on a table with no sort key | memory (was "doc", no page named) | `lsi_needs_sort_key` |
| Duplicate index names, at create and at update | model | `duplicate_index_name` |
| An index redeclaring an attribute with another type | memory | `attribute_type_conflict` |
| Delete or update of an index that does not exist | model (`ResourceNotFoundException`: "nonexistent table or index") | `update_missing_index` |
| More than one index operation in one UpdateTable; a billing change with an index change | model | `one_index_change_per_update` |
| UpdateTable with nothing to change | memory | `update_table_noop` |
| A second UpdateTable while `UPDATING` | model (`ResourceInUseException`) | `update_while_updating` |
| An empty-string key value, as S and as B; an empty string in a non-key attribute | model; text memory | `empty_key_value` |
| A mistyped index key attribute on a write; an empty-string index key | model and doc (GSI page) | `index_key_type_mismatch`, `index_key_empty` |
| Deleting a protected table: code and message | doc that it is refused (Working with tables page); code and message memory | `delete_protected_table` |
| `DeleteTable` answers `DELETING`; a second delete of a `DELETING` table is not an error | model | `delete_table_status` |
| `CreateTable` answers `CREATING`; operations on a table not yet `ACTIVE` | model | `create_table_status` |
| Enabling a stream by UpdateTable; enable twice and disable twice are `ValidationException`; re-enable gives a new ARN | model; doc (Streams page) | `stream_enable_disable` |
| A write that changes nothing emits no stream record | doc (Streams page) | `stream_noop_write` |
| A TTL removal's stream record: `userIdentity` | doc (Streams and Time to Live page) | `ttl_stream_record` |
| A deleted table's stream; an old ARN readable for 24 hours | model (DeleteTable), doc (Streams page) | `stream_after_delete` |
| TTL on a key attribute; disable naming another attribute | memory | `ttl_attribute_rules` |
| A second UpdateTimeToLive within the hour | model | `ttl_one_hour` |
| `TimeToLive is already enabled`: exact code and text | memory (the code names it) | `ttl_already_enabled` |
| An expired item is still read, queried, scanned and updatable until deleted | model; doc (TTL page) | `ttl_expired_still_visible` |
| BatchGetItem with a duplicate key; over 100 keys, with the quoted message | model | `batch_get_duplicates` |
| BatchWriteItem rejects the whole batch for a missing table, a key mismatch, a 400 KB item, a long key | model | `batch_write_whole_batch` |
| 51 tags; an `aws:` key | model | `tag_limits` |
| A tag ARN for another account or region | memory (the code `ResourceNotFoundException` is model) | `tag_foreign_arn` |
| A table named by ARN, on every operation family | model | `table_by_arn` |
| ListTables paging | model | `list_tables_paging` |
| PITR and Contributor Insights updates read back; their missing-table codes | model | `pitr_round_trip`, `contributor_insights_round_trip` |
| `TableClassSummary` after create and update | model | `table_class` |
| `TableId` shape; `TableSizeBytes` on a fresh table | model | `describe_table_fields` |
| `ConsumedCapacity` shape for each `ReturnConsumedCapacity` value | model | `consumed_capacity` |
| A filter naming a key attribute in a Query | model | `query_filter_on_key` |
| A local index fetches the item from the table | model | `lsi_fetches_from_table` |
| Legacy parameters: each, and mixed with an expression (the latter memory) | model; memory | `legacy_parameters`, `legacy_with_expression` |
| `ExecuteTransaction` all or nothing, with a token; no mixed reads and writes | model | `execute_transaction_atomic` |
| `BatchExecuteStatement` error codes and `TableName` | model | `batch_execute_error_codes` |
| PartiQL `Limit` and `NextToken` | model | `partiql_paging` |
| PartiQL grammar: `OR`, `IN`, comparison, `BETWEEN`, `ORDER BY`, `FROM "t"."i"`, `REMOVE`, `RETURNING` | doc (PartiQL select and update pages) | `partiql_grammar` |
| A multi-attribute global index: create, describe, query | model; doc (GSI page) | `multi_attribute_gsi` |
| Index counts: 5 local, 20 global, 100 projected attributes: the code | memory (the limits are model) | `index_count_limits` |
| Key size, document depth, transaction 4 MB, batch 16 MB | model (key size, 4 MB, 16 MB); doc (depth 32, naming rules page) | `size_limits` |
| `GetRecords` `Limit` above 1,000 | model (LimitExceededException) | `get_records_limit` |
| Shard iterator lifetime and the Streams error codes | memory | `stream_iterator_rules` |
| The code for an operation the service does not offer in a region | memory; moot once D5 holds | `unsupported_operation_code` |

## 9. Owed to integration

| What | With |
|---|---|
| A stream-fed function: a batch per write, in order, and the iterator advancing whatever the function returns | Lambda |
| A mapping created on a table with no stream is refused; one created before DDB-1's fix can never see a stream enabled later | Lambda |
| A deleted table's mapping stops polling; a deleted function's mapping stops reading | Lambda |
| A TTL expiry drives the function, and a filter on `userIdentity` selects it (DDB-41) | Lambda |
| A table dropped and created again under a name, with a mapping on it: the new stream is a new stream and the old table's records are not delivered (DDB-34, DDB-42) | Lambda |
| A table by ARN in `TableName` from a CloudFormation custom resource or a Step Functions task (DDB-36) | CloudFormation, Step Functions |
| The "stream read by" chip and the map's `esm` edge for a table | Lambda, foundation D |
| A table with indexes, TTL, a stream, tags and PITR in one template, through CloudFormation, SAM, CDK and Terraform, with a second apply that plans nothing | CloudFormation |
| `getItem`, `putItem`, `updateItem`, `deleteItem` as Step Functions integrations, including the error names a state can catch | Step Functions |
| A table behind an API Gateway service integration | API Gateway |
| Item and index actions under IAM enforce: `dynamodb:Query` on an index ARN, the `LeadingKeys` condition | IAM |
| Scan and index backfill under load; a restart mid-backfill | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] DDB-1 to DDB-62 each closed, with the test that failed first named beside
      it, or moved with a reason. *Corrected 2026-10-11: was DDB-1 to DDB-31.*
- [ ] DDB-4 and DDB-16 rest on `memory`. Each has its test written, and is
      either raised to `doc` with the page named and fixed, or left skipped on
      the recording queue. None is fixed on memory alone. *Corrected 2026-10-11: was "DDB-3,
      DDB-4, DDB-7, DDB-8, DDB-10 and DDB-16"; the model's text settles
      DDB-3, DDB-7, DDB-8 and DDB-10, and they are fixed on `model`.*
- [ ] **A fidelity table exists (section 6a), made from rendered boards beside
      rendered pages, and no row says "differs" without a decision beside it.**
      Today: 5 match, 13 differ, 1 differs deliberately, 1 cannot match yet.
- [ ] **The per-service AWS icons stay**: the rail, the list pane header and the
      resource header carry the DynamoDB icon (`awsIcon "ddb"`); the board's
      "DB" mark is its placeholder.
- [ ] **The five teaching lines are on every setting** the create form and the
      Details tab show (name and unit; the value in words; what it does; range,
      default and enforced or stored; the neighbour check with real numbers),
      from the settings record, none typed into a template.
- [ ] **Each protocol and wire is decided per operation.** DynamoDB speaks one
      wire (AWS JSON 1.0) on two target prefixes. `DynamoDB_20120810` is
      validated from the model; `DynamoDBStreams_20120810` is not validated at
      all until its model is on disk (DDB-58). One accepted-as-spelled test per
      Streams operation, and the whole of the request validated in the same shape.
- [ ] **Each asynchronous operation is decided (L7)**: CreateTable,
      DeleteTable, UpdateTable (`UPDATING`), an index add (`CREATING`,
      `Backfilling`), UpdateTimeToLive (`ENABLING`), a stream enable. Section 12.
- [ ] **Each time- and consistency-based rule is behind `--ddb-strict`,
      off by default (L8)**: expired items visible until swept (DDB-39), one
      TTL change per hour (DDB-53), an index that is eventually consistent.
- [ ] Decided and written into `SUPPORT.md`: `DescribeEndpoints` (DDB-14),
      consumed capacity (DDB-15), whether `TableSizeBytes` is computed (DDB-17),
      whether `ItemCount` includes expired items; and, added 2026-10-11, the
      legacy parameters (DDB-32), whether a deleted table's old stream stays
      readable (DDB-34), the multi-attribute index (DDB-37), expired items and
      `--ddb-strict` (DDB-39, DDB-53), the PartiQL scope (DDB-47), the
      asynchronous table states (DDB-52). The index-count limits are no longer a
      decision: the model states them (DDB-50).
- [ ] The S rows answer 501 `NotImplemented` with `X-Doze-Not-Built` (D5,
      DDB-49); resource policies, replica auto scaling,
      `ListContributorInsights` and `SearchVectors` are in `not-built.md`.
      *Corrected 2026-10-11: was "the code is established"; the model has no code for it.*
- [ ] `DescribeLimits` is C in the ledger. The ledger's "fixed status
      round-trips" wording for the two C families is true after DDB-12, and its
      `ExecuteTransaction` sentence is true after DDB-33 (DDB-60).
- [ ] The settings table in section 2 has no "not established", "ignored" or
      "dropped" left: every `CreateTable` and `UpdateTable` member is read,
      refused, or cosmetic and reported back.
- [ ] Projection: a test shows a read through each projection type on a global
      index returns exactly the projected attributes, and on a local index
      returns what the request asked for, from the table (DDB-45).
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped. Each line there marked "not established" has
      been run and the result written beside it. *Corrected 2026-10-11: the lines are
      annotated; those the model settles are fixed on `model`.*
- [ ] The hand-derived table rules live in one place, each with its `model` or
      `doc` source, and are replayed by the rejection-parity test; the list and
      map lengths are generated (DDB-38).
- [ ] A stream can be enabled, disabled and re-enabled on an existing table.
- [ ] No console action changes the type of a stored attribute or of a key:
      DDB-18, DDB-20 and DDB-21 each have a round-trip test.
- [ ] The console's create is one outcome: a table with everything asked for,
      or no table.
- [ ] The Details tab shows nothing it did not read: billing, protection,
      stream, encryption, local indexes and table class come from `DescribeTable`,
      and every C setting reads as cosmetic.
- [ ] A local secondary index can be queried from the console.
- [ ] All 20 boards implemented from the foundation's shell, and each `new`
      item reads only the data named for it.
- [ ] No statement on a DynamoDB screen is in the "not yet backed" list in
      section 6.
- [ ] The five confirms say only what the code does.
- [ ] The settings accessor (foundation B4) covers every teaching line in
      section 6 with no sentence typed into a template.
- [ ] `e2e/tests/dynamodb.spec.ts` covers all 14 routes under `/ddb` with the
      route gate on, and the accessibility sweep is clean on Items, Details,
      Tags, create, both dialogs and the drawer.
- [ ] Conformance: scenarios for every row in section 8 exist (about 45), on the
      DynamoDB wire and on Streams. Zero "answered differently". The unverified
      count is written here: ____ (all of them, until there is an AWS account).
- [ ] Mutation baseline recorded here, for `dynamodb/` and `internal/ddb/...`:
      ____ killed, ____ survived, with reasons.
- [ ] Idle heap unchanged by the new rules (`task lightness`).
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 0 | **Decisions that block the order**, in writing: the legacy parameters (translate or refuse, DDB-32), the multi-attribute index (build or refuse, DDB-37), the asynchronous states (L7, DDB-52), `--ddb-strict` (DDB-39, DDB-53), the PartiQL scope (DDB-47), consumed capacity (DDB-15) | this page, `docs/SUPPORT.md` |
| 1 | Contract: setting tiers, decisions, the missing `not-built.md` lines, the S rows moved to D5 | `docs/SUPPORT.md`, `docs/not-built.md`, `dynamodb/dynamodb.go` |
| 2 | Fetch the Streams model into `.audit-models/`; make `dzaudit` emit list and map lengths; red tests for DDB-1 to DDB-31 and DDB-32 to DDB-62 | `.audit-models/`, `cmd/dzaudit/cases.go`, `dynamodb/sdk_test.go`, `dynamodb/sdk_coverage_test.go`, `dynamodb/sdkv1_test.go`, `dynamodb/rejection_parity_test.go`, `internal/ddb/store/store_test.go`, new handler test files in `dynamodb/` |
| 3 | Table rules: name, key schema (including the multi-attribute index), definitions, index names, counts, type conflicts, ARNs for a table | `dynamodb/tables.go` (`keyParts`, `indexFromWire`, `createTable`, `updateTable`, `describe`), a new hand-derived rules file beside `dynamodb/validate.go`, `internal/ddb/store/store.go` (`CreateTable`, `UpdateTable`, `GetTable`) |
| 4 | The table lifecycle: `CREATING`, `UPDATING`, `DELETING`, index `Backfilling`, with the janitor-style background step and a restart rule (L7, L9); `UpdateTable`: stream specification, table class, index update, one change at a time, unread members | `dynamodb/tables.go`, `internal/ddb/store/stream.go`, `internal/ddb/store/store.go` (`Table`) |
| 5 | Item rules: empty keys, index key types, key size, depth, `ReturnValues`, the legacy parameters | `internal/ddb/store/store.go` (`primaryKey`, `indexKeyFor`), `internal/ddb/store/items.go`, `internal/ddb/item`, `dynamodb/items.go` (`exprCommon`) |
| 6 | Query and Scan rules, the local-index fetch, `Select` and `ProjectionExpression`, segments, expired items under `--ddb-strict` | `dynamodb/items.go`, `internal/ddb/store/query.go`, `internal/ddb/store/store.go` (`expired`) |
| 7 | Batches and transactions: the whole-batch rejection, duplicates, the 4 MB, `ExecuteTransaction` atomic with a token, `BatchExecuteStatement` codes | `dynamodb/items.go`, `dynamodb/partiql.go`, `internal/ddb/store/txn.go` |
| 8 | PartiQL: paging, then the grammar | `dynamodb/partiql_parse.go`, `dynamodb/partiql.go` |
| 9 | Streams: a stream per enable, the log keyed by stream, no-change writes, TTL `userIdentity`, iterators, limits, validation | `dynamodb/streams.go`, `internal/ddb/store/stream.go`, `internal/ddb/store/store.go` (`DeleteTable`) |
| 10 | Listing, tags, the C families, endpoints, capacity | `dynamodb/tables.go` (`listTables`, `tagResource`, `tableFromARN`, `describeContinuousBackups`, `describeContributorInsights`, `describeEndpoints`), `dynamodb/items.go` (`batchGet`) |
| 11 | Section 5 sweep | `dynamodb/*_test.go`, `internal/ddb/**/*_test.go`, `dynamodb/streams_test.go`, `dynamodb/ttl_test.go` |
| 12 | Conformance scenarios and claims table | `conformance/tests/test_dynamodb.py`, the claims file from foundation B1 |
| 13 | Console client: one-outcome create, typed item editing, the facts `Table` lacks, query fixes | `internal/console/client_ddb.go` |
| 14 | Console handlers and templates, in the build list's order (section 6a) | `internal/console/handlers_ddb.go`, `internal/console/templates/ddb.html`, the `ddb_create` block in `internal/console/templates/create.html`, `internal/console/templates/panes.html` (tags), `internal/console/copy.go`, `internal/console/static/*.css` |
| 15 | Console and browser tests, and a rendered comparison against each board | `internal/console/ddb_skeleton_test.go` and a new test file beside it, `e2e/tests/dynamodb.spec.ts` |
| 16 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 to 11 are the API and can be done without touching the console. Step 13
depends on step 4 and step 9 (the stream row) and on foundation A2, A5, A6 and
B4. *Corrected 2026-10-11: was "steps 3 to 6" and "step 9 depends on step 4"; the order was
re-cut for the 31 new findings.*

**Prior art.** The AWS console's item editor has two views, a form and
"DynamoDB JSON", and keeps the attribute's type beside its value in both; that
is the shape that fixes DDB-20, because plain JSON has no way to say "set" or
"binary". AWS's console also prefills the key attributes when adding an item,
which `ItemSkeleton` already copies. For the table rules, the source is the
developer guide's naming rules, data types and constraints pages; each rule's
test names the page.

*Added 2026-10-11, from memory and not verified here:* **dynalite**
(a Node implementation, the long-time local DynamoDB) models the table's
`CREATING`, `DELETING` and `UPDATING` windows with options (`createTableMs`,
`deleteTableMs`, `updateTableMs`, default about half a second), which is the
precedent for deciding L7 as "a small visible delay and a setting" rather than
immediate. **DynamoDB Local**, AWS's own Java build, is the reference for what a
local table does with the legacy parameters and with capacity; run its answers
beside the recording queue's rows before fixing anything on them, because it is
an emulator and not AWS.

## 12. Pre-stage review (2026-10-11)

Written before the stage starts, as [../02-lessons-from-sqs.md](../02-lessons-from-sqs.md)
asks. Read: the model's documentation text for all 58 operations, their inputs
and outputs, and the 35 error shapes (`.audit-models/dynamodb.json`, printed
cleaned of markup); the code of `dynamodb/` and `internal/ddb/`; and the
console's `ddb` templates, handlers and client. Run: a binary built from `main`
(about 60 requests, listed in the findings' Evidence column) and the Playwright
console on five tables. Documentation pages read in full, for `doc`: Quotas;
Naming rules and data types; Basic operations on tables; Time to live; DynamoDB
Streams and Time to Live; DynamoDB Streams; Global secondary indexes; PartiQL
select; PartiQL update.

### The twelve questions

| | Answer |
|---|---|
| **L1** Read the model before writing a rule | **Findings.** Every number, range, length, default, count and name in sections 2, 5, 6 and 8 was checked. 49 places carry a *Corrected 2026-10-11* mark: 15 of DDB-1 to DDB-17 re-sourced from `doc` or `memory` to `model` (DDB-1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 17), and the rest change what a line says (the settings rows, DescribeLimits's numbers, the S-row code, the index counts, the 400 KB, key-size and depth lines, section 5's "never silently ignored", "starts empty" and "invisible to every read path", section 1's "the item path is in good shape"). Two were wrong in a way that mattered: the document said the table-name rule "is not in the model at all" (it is, as shape `TableName`), and "333 of 333" is true of a narrower set than it reads (DDB-38). Three claims stand on `memory` and are not fixed: DDB-4's refusal, DDB-16, the deletion-protection message. |
| **L2** Optional parameters have meanings | **Findings.** DDB-32 (the legacy parameters), DDB-43 (the `UpdateTable` members and its one-at-a-time rule), DDB-44, DDB-56 (`Select`, `ProjectionExpression`, `Segment`), DDB-46 (`Limit`, `NextToken`), DDB-57 (`ReturnValues`), DDB-15 (`ReturnConsumedCapacity`). Checked, none further: `ConsistentRead` (held), `ScanIndexForward` (held), `ReturnValuesOnConditionCheckFailure` (held). One model oddity: BatchWriteItem's and AttributeValueUpdate's text still says "string and binary type attributes must have lengths greater than zero"; PutItem's says empty values are allowed except in a key. The code follows PutItem, which is the current rule (`doc`: naming rules page, "can be an empty string or empty binary value if the attribute is not used for a table or index key"). |
| **L3** Error codes are in the model | **Findings.** DDB-48 (`BatchStatementErrorCodeEnum`), DDB-51 (`TableNotFoundException` for the continuous-backup operations), DDB-33 (`TransactionCanceledException` for `ExecuteTransaction`), DDB-49 (`UnsupportedOperationException` is not in the model), DDB-50 and DDB-58 (`LimitExceededException`). Checked, none: `ResourceInUseException` on create, `ResourceNotFoundException` on describe, update and delete, `ConditionalCheckFailedException` with its `Item`, `IdempotentParameterMismatchException`, `TransactionCanceledException` and its `None` code. The model lists **no** `ValidationException` (it is the service framework's); that code is `memory` for every message and it is the code every first-pass refusal uses. |
| **L4** Name the direction | **Findings**; each row in section 4 has a Direction column. 15 are more forgiving (DDB-32, 35, 38, 40, 43, 44, 48, 50, 52 to 58), 3 stricter (DDB-36, 39, 47: a valid request refused or a thing hidden), 11 wrong code, shape or missing (DDB-33, 34, 37, 41, 42, 45, 46, 49, 51, 59, 61), and 2 are the ledger and the console (DDB-60, DDB-62). The two most dangerous are both forgiving and silent: the legacy parameters and the non-atomic transaction. |
| **L5** Every wire, one operation at a time | **Findings, then checked.** One wire (AWS JSON 1.0; the model's `aws.protocols#awsJson1_0`), so there is no second decoder to rot, and no `xmlName` or `xmlFlattened` (checked: only an `xmlNamespace` appears, on one shape, and a JSON wire ignores it). It has two target prefixes. `DynamoDBStreams_20120810` has no model on disk and no constraint table, so its four operations are served and **not validated** (DDB-58); fetching the model is step 2 of section 11. The model-derived walk reads the JSON body whole, and so sees every element of a list (no SQS-26 trap here). Not a flattened-members service. |
| **L6** Tokens are per use | **Findings.** DDB-42 (a stream ARN is the table's for life, so an iterator or a mapping cannot tell two streams apart), DDB-34 (a deleted table's log is served to the next one), DDB-58 (an iterator names a table, not a stream, never expires, and its `ShardId` is not read), DDB-33 (`ClientRequestToken` on `ExecuteTransaction` ignored). Checked, none: `LastEvaluatedKey` and `ExclusiveStartKey` (resumed exactly, held in probes), the `TransactWriteItems` token (replay and mismatch both right), `ListTables`' `ExclusiveStartTableName` (DDB-9, not a token problem). |
| **L7** Asynchronous means observable | **Findings: DDB-52, DDB-53, DDB-43.** The model's asynchronous operations: `CreateTable` (CREATING, ACTIVE), `UpdateTable` (UPDATING; an index CREATING with `Backfilling`), `DeleteTable` (DELETING), `UpdateTimeToLive` (ENABLING, DISABLING, "up to one hour"), the stream a table opens, `CreateBackup` and the other backup, export and import operations (stubs), `TagResource` (eventually consistent). **Decision proposed, to confirm:** model all four table-and-index windows with a small visible delay and a setting, default 0 and the default the ledger already promises ("waiters pass on their first probe"), so a board that draws "Backfilling" and a test of the in-between state are possible by turning it up: `--ddb-create-ms`, `--ddb-update-ms`, `--ddb-delete-ms` (dynalite's precedent, `memory`). Reads and writes during `CREATING` are refused (the model: "You can perform read and write operations only on an ACTIVE table"; the code is `memory`, `ResourceNotFoundException` or `ResourceInUseException`); during `DELETING` they are accepted (the model: "might continue to accept data read and write operations"). `ENABLING` for TTL the same way. The in-flight state is the process's (L9) and is not persisted: a restart settles every table to its target state. Tags and the other `eventually consistent` documentation are listed differences. |
| **L8** Latency rules are opt-in strictness | **Findings: DDB-39, DDB-53; listed.** The time- and consistency-based rules, each under one switch (`--ddb-strict`, `[dynamodb] strict = true`, off): expired items are visible until swept (DDB-39); one `UpdateTimeToLive` per hour (DDB-53); a global index is eventually consistent ("updated asynchronously ... a fraction of a second", the GSI page); `DescribeTable` immediately after `CreateTable` may answer `ResourceNotFoundException` (the model says so; the code never does); `ItemCount` and sizes lag by up to six hours; a policy or a tag lists the old value for seconds. The last four are listed differences, not behaviours to build. The decrease limit per day on provisioned capacity (4, then one an hour, 27 a day; Quotas page) is capacity and is not modelled. |
| **L9** State in memory unless losing it changes what the application sees | **Checked, with one finding.** Persisted and right: tables, items, indexes, tags, TTL setting, the transaction-token window (`txBucket`), stream records and sequence numbers. In memory and right: the janitor. **New state this review adds:** a table's `CREATING`, `UPDATING`, `DELETING` and an index's `Backfilling` are the process's, not persisted, settled at start (L7); the stream's identity (DDB-42) must be persisted, because an ARN a mapping holds has to survive a restart. A stream record's count cap is a count, not a time (DDB-58), so it behaves the same across a restart. |
| **L10** The board is the spec, and its shape counts | **Findings.** Section 6a: 20 boards rendered beside 20 pages: 5 match, 13 differ, 1 differs deliberately, 1 cannot match yet. The design was wrong about 5 facts (corrected in the working copy). The create form carries none of the teaching it was designed for. |
| **L11** The five lines, all five | **Findings.** None of the create or Details settings carries them (build list 3 and 4); the settings record needs the two fields foundation B4 already names ("source of enforcement" and "settable from"), and a sixth case: a rule enforced only by the browser's `pattern` today (the table name, DDB-5). |
| **L12** Icons | **Checked, none.** `awsIcon "ddb"` is in the rail, the list pane header and the resource header (rendered). The boards' "DB" mark is the placeholder; the page keeps the icon. |

### Classes of mistake this service is most at risk of

1. **A parameter accepted and not read.** Six of the new findings (DDB-32, 43,
   46, 56, 57, 58) and three old ones (DDB-1, 13, 15) are this: the legacy
   parameters, the `UpdateTable` members, `Limit`, `NextToken`, `ReturnValues`,
   `ReturnConsumedCapacity`, `TableClass`, the `Segment`/`TotalSegments` pair. The decoder is permissive by construction
   (`encoding/json` into a struct that names only what the first pass thought
   of). The fix is a rule, not a list: a request member the handler does not read
   is a finding by test (decode strictly in the test; every unknown top-level
   key of a model input is either read, refused or tiered C).
2. **A rule written for the common index and applied to both.** Local and global
   indexes, table and index key schemas, one HASH and several. Every place the
   code says "an index" it has to say which.
3. **Behaviour that is friendlier than AWS and invisible.** Expired items hidden,
   an index complete on return, every table `ACTIVE` at once, a transaction that
   half applies. Each makes a local test pass that AWS fails.
4. **State that outlives its owner.** A stream log under a table name, a
   transaction token, a sweep. Anything keyed by a name rather than an identity.
5. **A validation count that reads as coverage.** "333 of 333" is a count of a
   generator's output. Anything the generator does not emit (list lengths,
   cross-member rules, the whole of Streams) is outside it, and the ledger says
   it is not.
6. **A wire protocol with a second service model.** Streams.

### Open decisions (need a person)

| # | Decision | Recommendation |
|---|---|---|
| 1 | Legacy parameters: translate to the expression forms, or refuse (DDB-32) | Translate: the model defines every operator, SDK v1 and boto3's resource layer still send them, and a refusal is a deploy-time failure for a correct caller |
| 2 | Multi-attribute global indexes: build, or refuse with a stated reason (DDB-37) | Refuse now (honest 501-class, "not built: multi-attribute index keys"); build when a user brings a template that has one |
| 3 | Asynchronous table states: the settings and their defaults (DDB-52) | As L7 above; default 0 |
| 4 | Expired items and `--ddb-strict` (DDB-39, DDB-53) | Default as today; strict shows them until the sweep |
| 5 | PartiQL: how much grammar (DDB-47) | The statement forms in the two pages read, in one parser, before anything else in PartiQL |
| 6 | A deleted table's old stream: readable for 24 hours, or gone (DDB-34) | Gone at once; a local stream's retention is not a thing anyone tests |
| 7 | `ReturnConsumedCapacity` (DDB-15) | Return a structurally valid `ConsumedCapacity` with the item sizes' real units, marked cosmetic in the ledger |
