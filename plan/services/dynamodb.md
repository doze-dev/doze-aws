# DynamoDB

Stage 7. Size L. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds DynamoDB's own.

## 1. Summary

Tables, items, secondary indexes, TTL, transactions, PartiQL and Streams over
one bbolt store (`internal/ddb/store`), speaking AWS JSON 1.0 on two target
prefixes: `DynamoDB_20120810` and `DynamoDBStreams_20120810`. The item model and
the five expression languages are real (`internal/ddb/item`, `internal/ddb/expr`).

Peers: Lambda polls a table's stream through an event source mapping;
CloudFormation and `provision` create tables; Step Functions and API Gateway
call the item operations as service integrations. Nothing calls into DynamoDB
except through its API, so the service itself has no peer dependencies.

The item path is in good shape: conditions, reserved words, unused references,
batch bounds and transaction rules all refused correctly when probed. The table
path is not. Table administration accepts a long list of things AWS refuses,
and `UpdateTable` silently ignores a stream specification. The console page
works, shows two facts that are false for any table it did not create, and has
two ways of quietly changing the type of stored data.

**Estimate:** L, five or more focused days. **Actual:** _fill in when done._

## 2. Contract

The DynamoDB model lists **58 operations** (`dynamodb/testdata/ops_dynamodb.json`,
checked by `TestEveryModelOperationIsAccountedFor`). The dispatcher handles 29
(`handlers` in `dynamodb/tables.go`) and refuses 29 by name (`stubActions` in
`dynamodb/dynamodb.go`). The DynamoDB Streams model adds **4 operations**,
all handled (`streamHandlers` in `dynamodb/streams.go`). 62 in all.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateTable | F | F | Per-setting tier below. Accepts invalid schemas: DDB-4, DDB-5, DDB-6, DDB-7. |
| DescribeTable | F | F | `TableSizeBytes` is always 0 and `TableId` is the name: DDB-17. Reports a wrong key type after DDB-4. |
| ListTables | F | F | Ignores `Limit` and `ExclusiveStartTableName`: DDB-9. |
| DeleteTable | F | F | Deletion protection enforced. |
| UpdateTable | F | F | Per-setting tier below. Ignores `StreamSpecification` (DDB-1) and `TableClass` (DDB-13); index delete and update of a missing index succeed (DDB-8). |
| UpdateTimeToLive, DescribeTimeToLive | F | F | DDB-16. |
| PutItem, GetItem, UpdateItem, DeleteItem | F | F | Empty key values and mistyped index keys accepted: DDB-2, DDB-3. |
| Query, Scan | F | F | |
| BatchGetItem, BatchWriteItem | F | F | Duplicate keys in a BatchGetItem accepted: DDB-10. |
| TransactWriteItems, TransactGetItems | F | F | |
| ExecuteStatement, BatchExecuteStatement, ExecuteTransaction | F | F | PartiQL, translated onto the classic operations. |
| TagResource, UntagResource, ListTagsOfResource | F | F | DDB-11. |
| DescribeLimits | F | C | Canned numbers. The ledger calls it F; it is cosmetic and should say so. |
| DescribeEndpoints | F | decide | Returns AWS's own us-east-1 hostname: DDB-14. Target is either the instance's address or S. |
| DescribeContinuousBackups, UpdateContinuousBackups | C | C | The update does not round-trip: DDB-12. |
| DescribeContributorInsights, UpdateContributorInsights | C | C | Same, and no table check: DDB-12. |
| CreateGlobalTable, DescribeGlobalTable, DescribeGlobalTableSettings, ListGlobalTables, UpdateGlobalTable, UpdateGlobalTableSettings | S | S | One region locally. |
| DescribeTableReplicaAutoScaling, UpdateTableReplicaAutoScaling | S | S | Belongs to global tables. |
| CreateBackup, DeleteBackup, DescribeBackup, ListBackups, RestoreTableFromBackup, RestoreTableToPointInTime | S | S | Copy the data directory. |
| ExportTableToPointInTime, DescribeExport, ListExports, ImportTable, DescribeImport, ListImports | S | S | |
| DescribeKinesisStreamingDestination, EnableKinesisStreamingDestination, DisableKinesisStreamingDestination, UpdateKinesisStreamingDestination | S | S | |
| GetResourcePolicy, PutResourcePolicy, DeleteResourcePolicy | S | S | Table resource policies are not stored or evaluated. |
| ListContributorInsights | S | S | |
| SearchVectors | S | S | |
| Streams: ListStreams, DescribeStream, GetShardIterator, GetRecords | F | F | One open shard per stream. |

The S rows answer `UnsupportedOperationException` with status 400 and a reason.
Whether that is the code an SDK expects for an unsupported operation on this
protocol is **not established** (the foundation's table speaks of a 501).
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
| Projection `ALL`, `KEYS_ONLY`, `INCLUDE` | index | F | F | `Select` rules are enforced. That a read through the index returns exactly the projected attributes is not established. |
| BillingMode, ProvisionedThroughput | CreateTable, UpdateTable | C | C | Validated, stored, reported. Nothing is metered or throttled. |
| ProvisionedThroughput on an index | CreateTable | C | C | Required or forbidden by billing mode, as on AWS. |
| DeletionProtectionEnabled | CreateTable, UpdateTable | F | F | |
| StreamSpecification | CreateTable | F | F | |
| StreamSpecification | UpdateTable | ignored | F | DDB-1. |
| SSESpecification | CreateTable, UpdateTable | C | C | A description only. |
| Tags | CreateTable | F | F | |
| TableClass | CreateTable, UpdateTable | dropped | C | Not stored, not reported: DDB-13. |
| WarmThroughput, OnDemandThroughput | CreateTable, UpdateTable | not established | C or refuse | A fixed `WarmThroughput` is reported whatever was sent. |
| ResourcePolicy, GlobalTable members, ReplicaUpdates, MultiRegionConsistency, GlobalTableWitnessUpdates, VectorIndexes, VectorIndexUpdates | CreateTable, UpdateTable | not established | refuse | The handlers do not read them. An accepted and ignored member is a silent no-op. |
| TimeToLiveSpecification | UpdateTimeToLive | F | F | |
| PointInTimeRecoverySpecification | UpdateContinuousBackups | ignored | C | DDB-12. |
| ContributorInsightsAction, ContributorInsightsMode | UpdateContributorInsights | ignored | C | DDB-12. |
| ReturnConsumedCapacity, ReturnItemCollectionMetrics | item operations | ignored | decide | Enum validated, nothing returned: DDB-15. |
| ConsistentRead | GetItem, Query, Scan, BatchGetItem | accepted | C | One node, so every read is consistent. Refused on a global secondary index, as on AWS. |

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

Two things about the parity number. It is true, and it is why the table-name
finding exists: the model gives `TableName` a length of 1 to 1024 and no
pattern, because the same member also accepts an ARN, so the 3 to 255 naming
rule is not in the model at all. And model constraints are per member; every
rule that relates two members (a key schema to its attribute definitions, one
index name to another) is hand-derived, and almost none of those exist yet.
`internal/ddb/store/store_test.go` has two tests for the layer where they belong.

## 4. Findings

Evidence `run` means reproduced against a binary built from `main` on
2026-10-10, through the API with curl or through the console's own routes.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| DDB-1 | P0 | `UpdateTable` ignores `StreamSpecification`. Enabling a stream on an existing table returns success with no stream; disabling one leaves it running. A deploy that adds a stream and then a Lambda mapping fails at the mapping, or never fires. | `dynamodb/tables.go` `updateTable` (the request struct has no such member) | run | doc: UpdateTable API reference | SDK test: create without a stream, `UpdateTable` to enable, assert `LatestStreamArn`; then disable and assert it is gone |
| DDB-2 | P0 | A key attribute holding an empty string is stored. `PutItem` with `pk: ""` succeeds, and so does the same through the console. | `internal/ddb/store/store.go` `primaryKey` | run | doc: the developer guide's data-type rules say key attributes cannot be empty. Message text: memory | Store test and rejection-parity case for an empty S and an empty B key, on PutItem, UpdateItem, BatchWriteItem and TransactWriteItems |
| DDB-3 | P0 | An item whose index key attribute has the wrong type, or is an empty string, is accepted and silently left out of the index. On AWS the write is refused, so a type bug in the caller shows up here only as a Query that returns nothing. | `internal/ddb/store/store.go` `indexKeyFor` | run | memory. A missing index key is legitimately sparse (doc); a mistyped one being refused is recalled, not sourced | Store test: GSI on `g` (S), put `g` as N; skipped until the claim is `doc` or `recorded` |
| DDB-4 | P0 | An index may redeclare an attribute the table already keys on with a different type. `UpdateTable` with a GSI on `lineNo` typed S, on a table whose sort key `lineNo` is N, succeeds, and `DescribeTable` then reports the table's own sort key as S while the store still enforces N. Every client that builds keys from the description then fails. | `dynamodb/tables.go` `describe` (one `attrTypes` map for table and indexes), `indexFromWire` | run | memory | Handler test: the UpdateTable above is refused; a describe test that a table key's type never changes |
| DDB-5 | P1 | Table names are not checked beyond the model's 1 to 1024. `a`, `bad name!` and a 300-character name are all created. | `internal/ddb/store/store.go` `CreateTable`; no rule in `dynamodb/validate.go` | run | doc: naming rules, 3 to 255, `[a-zA-Z0-9_.-]`. The exact error text: memory | Rejection-parity cases for a 2-character name, a 256-character name and a space |
| DDB-6 | P1 | Key schema and attribute definitions are not cross-checked. Accepted: a sort key with the same name as the partition key; two `HASH` elements (the last wins, the first is dropped); an attribute definition no key uses; a local secondary index on a table with no sort key. | `dynamodb/tables.go` `keyParts`, `createTable` | run | doc for each rule; messages: memory | One handler test per case, table-driven |
| DDB-7 | P1 | Duplicate index names are accepted, at `CreateTable` and at `UpdateTable` `Create`. Two indexes named `by-sku` were created and both listed. | `dynamodb/tables.go` `createTable`, `updateTable` | run | memory | Handler test for both paths |
| DDB-8 | P1 | `UpdateTable` on an index that does not exist succeeds: `Delete` of a missing index, and `Update` of any index, are no-ops. | `dynamodb/tables.go` `updateTable` | run | memory (`ResourceNotFoundException`) | SDK test for both |
| DDB-9 | P1 | `ListTables` ignores `Limit` and `ExclusiveStartTableName` and never returns `LastEvaluatedTableName`. A caller that pages gets everything on page one. | `dynamodb/tables.go` `listTables` | run | doc | SDK test with `Limit: 1` over three tables |
| DDB-10 | P1 | `BatchGetItem` with the same key twice returns the item twice. `BatchWriteItem` refuses the same mistake. | `dynamodb/items.go` `batchGet` | run | memory | Handler test |
| DDB-11 | P1 | Tags: 51 tags on one table accepted; a key starting `aws:` accepted; the ARN's region and account are not read, so an ARN for another account tags this one's table. | `dynamodb/tables.go` `tagResource`, `tableFromARN` | run | doc for the limit of 50; memory for the other two | Handler tests; the ARN case belongs with the second-identity pass (foundation B2) |
| DDB-12 | P1 | The two C families do not round-trip. `UpdateContinuousBackups` enabling point-in-time recovery answers `DISABLED`; `UpdateContributorInsights` answers `DISABLED` and does not check that the table exists. Terraform's `point_in_time_recovery` would plan the same change forever. The ledger says "fixed status round-trips", which is not what happens. | `dynamodb/tables.go` `describeContinuousBackups`, `describeContributorInsights` (each also serves the update) | run | doc | SDK test: enable, describe, expect `ENABLED`; unknown table refused |
| DDB-13 | P1 | `TableClass` is dropped on create and on update and is absent from the description. | `dynamodb/tables.go` `createTableReq`, `updateTable`, `describe` | run | doc | SDK test: set, describe, expect `TableClassSummary` |
| DDB-14 | P1 | `DescribeEndpoints` returns `dynamodb.us-east-1.amazonaws.com` whatever the instance's address or region. An SDK with endpoint discovery turned on would be told to talk to AWS. Not reproduced with an SDK; raise to P0 if it is. The literal region is foundation A8's subject. | `dynamodb/tables.go` `describeEndpoints` | run (the response), read (the consequence) | doc | SDK v2 test with endpoint discovery enabled: every call still reaches the test server |
| DDB-15 | P1 | `ReturnConsumedCapacity` is validated and then ignored: no response carries `ConsumedCapacity`. The ledger lists capacity as not modelled; a caller that reads the member gets nil. | `dynamodb/items.go`, every item handler | run | doc | Decide first (section 10), then an SDK test either way |
| DDB-16 | P1 | TTL accepts a key attribute as the TTL attribute, and accepts a disable that names a different attribute from the one enabled. What AWS does with either is not established. | `dynamodb/tables.go` `updateTTL` | run | memory | Written and skipped until recorded |
| DDB-17 | P1 | `DescribeTable` reports `TableSizeBytes: 0` always and `TableId` equal to the table name. | `dynamodb/tables.go` `describe` | run | doc: size is refreshed about every six hours; `TableId` is a UUID | Decide whether to compute size; a describe test for a UUID-shaped id that survives a restart |
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
      by name, and an ARN accepted where the model allows one.
- [ ] Key schema: exactly one `HASH`, at most one `RANGE`, distinct names, `HASH`
      first; every key attribute defined once, and no definition unused.
- [ ] A key attribute's type is S, N or B, and one name has one type across the
      table and every index.
- [ ] Create with a name in use: `ResourceInUseException`. Describe, update and
      delete of a missing table: `ResourceNotFoundException`.
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
- [ ] The limits on index count per table (doc): decide whether to enforce.
- [ ] Add by `UpdateTable`: existing items are indexed before the call returns;
      delete reclaims the index; one index operation per call (not established).
- [ ] An item missing an index key is absent from the index and comes back when
      the attribute is set; an item whose index key changes moves.
- [ ] A read through a `KEYS_ONLY` and an `INCLUDE` index returns exactly the
      projected attributes; `Select: ALL_ATTRIBUTES` on one is refused.
- [ ] `ConsistentRead` on a global index is refused; on a local index accepted.

**Items**
- [ ] Key values: present, correctly typed, not empty (DDB-2), within the key
      size limits (doc: 2048 bytes partition, 1024 sort; not established here).
- [ ] The 400 KB rule on PutItem and on the result of UpdateItem, counting
      attribute names.
- [ ] Every type round-trips: S, N, B, BOOL, NULL, M, L, SS, NS, BS; empty sets
      and duplicate set members refused; nesting depth (doc: 32; not established here).
- [ ] Numbers: 38 significant digits, the exponent range, and that `1.0` and `1`
      are one key.
- [ ] `ReturnValues` on each operation, including the values an operation does
      not allow; `ReturnValuesOnConditionCheckFailure` puts the item in the error.
- [ ] UpdateItem: each clause; two paths that overlap; a key attribute in any
      clause; an update that creates the item.
- [ ] Legacy parameters (`Expected`, `AttributeUpdates`, `KeyConditions`,
      `ScanFilter`, `QueryFilter`, `AttributesToGet`): work, or are refused, and
      never silently ignored. Mixed with an expression: refused. Not established.

**Expressions**
- [ ] Reserved words refused unaliased; an unused `#name` or `:value` refused;
      an undefined one refused. (All three held when probed; keep the tests.)
- [ ] Every function and operator in the condition grammar, with operand type
      errors.
- [ ] Key conditions: equality on the partition key only; the seven sort-key
      forms; a non-key attribute refused.
- [ ] A filter may not name a key attribute in a Query (doc); in a Scan it may.

**Query and Scan**
- [ ] `Limit` counts items read before the filter (held when probed).
- [ ] A page ends at 1 MB with `LastEvaluatedKey`; the key resumes exactly,
      forwards and backwards, on a table and on an index with duplicate index keys.
- [ ] `ExclusiveStartKey` that does not fit the schema is refused.
- [ ] `Select: COUNT`; `ProjectionExpression` with `Select`; `ScanIndexForward`.
- [ ] Parallel scan: every item appears in exactly one segment.

**Batches and transactions**
- [ ] BatchWriteItem 1 to 25, BatchGetItem 1 to 100, duplicates refused in both
      (DDB-10), a missing table fails the whole call.
- [ ] `UnprocessedItems` and `UnprocessedKeys` are always present and, here,
      always empty; the 16 MB response bound (doc; not established here).
- [ ] TransactWriteItems: 1 to 100, one operation per item, per-item
      `CancellationReasons` in order, the idempotency token's ten-minute window
      and `IdempotentParameterMismatchException`, the 4 MB total (not established).
- [ ] TransactGetItems: 1 to 100, a missing item is an empty entry in order.

**PartiQL**
- [ ] SELECT with a full key is a point read; otherwise a filtered scan, and
      `Limit` and `NextToken` behave as Scan's paging does (not established).
- [ ] INSERT of an existing key is refused; UPDATE and DELETE need the full key.
- [ ] `?` parameters: too few, too many, wrong type.
- [ ] BatchExecuteStatement returns per-statement errors without failing the
      batch; ExecuteTransaction is all or nothing.

**TTL**
- [ ] Enable when enabled and disable when disabled are refused (held).
- [ ] Only a Number attribute expires; a string of digits does not.
- [ ] An expired item is invisible to every read path, including indexes,
      batches, transactions and PartiQL, before the janitor removes it.
- [ ] `ItemCount` and the console's count with expired items present: decide
      which they report.
- [ ] A TTL removal reaches the stream, and how the record is marked (not
      established; AWS marks it with a service identity).

**Streams**
- [ ] Each view type carries exactly the images it names.
- [ ] Enable, disable and re-enable through `UpdateTable` (DDB-1); a re-enable
      makes a new stream ARN.
- [ ] Iterators: `TRIM_HORIZON`, `LATEST`, `AT_SEQUENCE_NUMBER`,
      `AFTER_SEQUENCE_NUMBER`; a malformed one refused; one for a deleted table.
- [ ] A write that changes nothing emits no record (doc; not established here).
- [ ] Record retention: AWS keeps 24 hours. What is kept here, and whether the
      log is ever trimmed, is not established.

**Tags**
- [ ] 50 per table, key 1 to 128, value 0 to 256, `aws:` reserved (DDB-11).
- [ ] The ARN is parsed, and one for another account or region is refused.

**Lifecycle**
- [ ] Everything above survives a restart: tables, indexes, TTL setting, tags,
      the stream's records and sequence numbers, the transaction token window.
- [ ] Deleting a table removes its items, indexes and stream; a table created
      again under the same name starts empty.

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
| Ddb-03-Create-Variant | Sort key, an index, TTL, CLI open | `ddb_create`, `cli` | form and preview exist (preview: A11) |
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
| Ddb-15-Confirms | Five confirms; delete refused by protection | `hx-confirm`; `ddbDeleteTable` | confirms exist; the inline refusal is **new** (A6) |
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

- Ddb-18: "An item removed by TTL … reaches the stream too." From the ledger;
  not run.
- Ddb-18: "Each record carries the item as it was and as it is now." doc for
  the view type; not run here.
- Ddb-16: "Fixed once the table exists." doc.
- Ddb-16: "An exact count, including items that have expired and not yet been
  swept." read (`CountItems` counts keys); section 5 asks for a decision.
- Ddb-15: "Items stop expiring from then on. Nothing already removed comes back."
  Not run.
- Ddb-15: after Delete table the page opens "on the first table left". Read
  from the redirect.
- Ddb-13: the number in "item size 412160 exceeds the 409600 byte limit" is
  illustrative.
- Ddb-09: "'Scanned' counts what the key condition selected." doc.
- Ddb-08: "The rows below … stay until the new one lands." htmx behaviour, not
  checked. The bar shows for any non-GET request slower than 300 ms, not only a scan.
- Ddb-04, Ddb-10, Ddb-13, Ddb-17, Ddb-19: each refusal carries today's "the rule
  comes from AWS's own service model" line, which A2 replaces.
- Ddb-02: "Unique in this region and account." doc.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store (`internal/ddb/store`) | Empty key values (DDB-2); mistyped index key (DDB-3); index maintenance on key change and delete; restart with a stream and a TTL setting |
| Handler (`dynamodb/`) | Name rule (DDB-5); the schema cases (DDB-4, DDB-6, DDB-7); index delete and update of a missing index (DDB-8); duplicate batch keys (DDB-10); tag limit and ARN (DDB-11); every "not established" member in section 2 |
| SDK v2 | Stream enable, disable, re-enable by `UpdateTable` (DDB-1); `ListTables` paging (DDB-9); PITR and Contributor Insights round trip (DDB-12); `TableClass` (DDB-13); endpoint discovery (DDB-14); consumed capacity (DDB-15) |
| Rejection parity | The hand-derived rules join the replay, each with its `doc` source beside it |
| Restart | Tables, indexes, tags, TTL, stream records, transaction tokens |
| Conformance (boto3) | One scenario per row of section 8 |
| Console unit (`internal/console`) | Create with a conflicting index key (DDB-18); late TTL refusal (DDB-19); `QueryItems` binding collision (DDB-25); empty page with a cursor (DDB-23); limit carried by the cursor (DDB-29); missing table as a page (DDB-27); Details facts for a table made from code (DDB-22) |
| Browser | Edit round trip of SS, NS, B (DDB-20); Edit through a KEYS_ONLY index (DDB-21); a mutation keeps the query (DDB-24); each `new` item; each confirm's sentence; the delete refusal inline |
| Mutation | `dynamodb/` and `internal/ddb/...` baselines, then survivors in `tables.go`, `store.go`, `query.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today. Scenarios go in
`conformance/tests/test_dynamodb.py`.

| Claim | Source now | Scenario |
|---|---|---|
| Table name rule and its error text | doc; text memory | `table_name_rules` |
| Sort key named like the partition key; two HASH elements; unused attribute definition | doc; text memory | `key_schema_rules` |
| A local index on a table with no sort key | doc | `lsi_needs_sort_key` |
| Duplicate index names, at create and at update | memory | `duplicate_index_name` |
| An index redeclaring an attribute with another type | memory | `attribute_type_conflict` |
| Delete or update of an index that does not exist | memory | `update_missing_index` |
| More than one index operation in one UpdateTable | memory | `one_index_change_per_update` |
| UpdateTable with nothing to change | memory | `update_table_noop` |
| An empty-string key value, as S and as B | doc; text memory | `empty_key_value` |
| A mistyped index key attribute on a write | memory | `index_key_type_mismatch` |
| An empty-string index key attribute | memory | `index_key_empty` |
| Deleting a protected table: code and message | memory | `delete_protected_table` |
| Enabling a stream by UpdateTable; re-enable gives a new ARN | doc | `stream_enable_disable` |
| A write that changes nothing emits no stream record | doc | `stream_noop_write` |
| A TTL removal's stream record and how it is marked | doc | `ttl_stream_record` |
| TTL on a key attribute; disable naming another attribute | memory | `ttl_attribute_rules` |
| `TimeToLive is already enabled`: exact code and text | memory (the code names it) | `ttl_already_enabled` |
| BatchGetItem with a duplicate key | memory | `batch_get_duplicates` |
| 51 tags; an `aws:` key; an ARN for another account | doc; memory; memory | `tag_limits` |
| ListTables paging | doc | `list_tables_paging` |
| PITR and Contributor Insights updates read back | doc | `pitr_round_trip`, `contributor_insights_round_trip` |
| `TableClassSummary` after create and update | doc | `table_class` |
| `TableId` shape; `TableSizeBytes` on a fresh table | doc | `describe_table_fields` |
| `ConsumedCapacity` shape for each `ReturnConsumedCapacity` value | doc | `consumed_capacity` |
| A filter naming a key attribute in a Query | doc | `query_filter_on_key` |
| Legacy parameters mixed with expressions | doc | `legacy_with_expression` |
| The code for an operation the service does not offer in a region | memory | `unsupported_operation_code` |
| PartiQL `Limit` and `NextToken` | doc | `partiql_paging` |
| Key size, nesting depth, transaction and batch byte limits | doc | `size_limits` |

## 9. Owed to integration

| What | With |
|---|---|
| A stream-fed function: a batch per write, in order, and the iterator advancing whatever the function returns | Lambda |
| A mapping created on a table with no stream is refused; one created before DDB-1's fix can never see a stream enabled later | Lambda |
| A deleted table's mapping stops polling; a deleted function's mapping stops reading | Lambda |
| A TTL expiry drives the function | Lambda |
| The "stream read by" chip and the map's `esm` edge for a table | Lambda, foundation D |
| A table with indexes, TTL, a stream, tags and PITR in one template, through CloudFormation, SAM, CDK and Terraform, with a second apply that plans nothing | CloudFormation |
| `getItem`, `putItem`, `updateItem`, `deleteItem` as Step Functions integrations, including the error names a state can catch | Step Functions |
| A table behind an API Gateway service integration | API Gateway |
| Item and index actions under IAM enforce: `dynamodb:Query` on an index ARN, the `LeadingKeys` condition | IAM |
| Scan and index backfill under load; a restart mid-backfill | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] DDB-1 to DDB-31 each closed, with the test that failed first named beside
      it, or moved with a reason.
- [ ] DDB-3, DDB-4, DDB-7, DDB-8, DDB-10 and DDB-16 rest on `memory`. Each has
      its test written, and is either raised to `doc` with the page named and
      fixed, or left skipped on the recording queue. None is fixed on memory alone.
- [ ] Decided and written into `SUPPORT.md`: `DescribeEndpoints` (DDB-14),
      consumed capacity (DDB-15), whether `TableSizeBytes` is computed (DDB-17),
      the index-count limits, whether `ItemCount` includes expired items.
- [ ] The S rows' error code is established (recorded, or `doc` with the page)
      and matches; resource policies, replica auto scaling,
      `ListContributorInsights` and `SearchVectors` are in `not-built.md`.
- [ ] `DescribeLimits` is C in the ledger. The ledger's "fixed status
      round-trips" wording for the two C families is true after DDB-12.
- [ ] The settings table in section 2 has no "not established", "ignored" or
      "dropped" left: every `CreateTable` and `UpdateTable` member is read,
      refused, or cosmetic and reported back.
- [ ] Projection: a test shows a read through each projection type returns
      exactly the projected attributes.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped. Each line there marked "not established" has
      been run and the result written beside it.
- [ ] The hand-derived table rules live in one place, each with its `doc`
      source, and are replayed by the rejection-parity test.
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
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here, for `dynamodb/` and `internal/ddb/...`:
      ____ killed, ____ survived, with reasons.
- [ ] Idle heap unchanged by the new rules (`task lightness`).
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: setting tiers, decisions, the missing `not-built.md` lines | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for DDB-1 to DDB-17 | `dynamodb/sdk_test.go`, `dynamodb/sdk_coverage_test.go`, `dynamodb/rejection_parity_test.go`, `internal/ddb/store/store_test.go`, a new handler test file in `dynamodb/` |
| 3 | Table rules: name, key schema, definitions, index names, type conflicts | `dynamodb/tables.go` (`keyParts`, `indexFromWire`, `createTable`, `updateTable`, `describe`), a new hand-derived rules file beside `dynamodb/validate.go`, `internal/ddb/store/store.go` (`CreateTable`, `UpdateTable`) |
| 4 | `UpdateTable`: stream specification, table class, index update, unread members | `dynamodb/tables.go`, `internal/ddb/store/stream.go`, `internal/ddb/store/store.go` (`Table`) |
| 5 | Item rules: empty keys, index key types | `internal/ddb/store/store.go` (`primaryKey`, `indexKeyFor`), `internal/ddb/store/items.go` |
| 6 | Listing, batches, tags, the C families, endpoints, capacity | `dynamodb/tables.go` (`listTables`, `tagResource`, `tableFromARN`, `describeContinuousBackups`, `describeContributorInsights`, `describeEndpoints`), `dynamodb/items.go` (`batchGet`) |
| 7 | Section 5 sweep | `dynamodb/*_test.go`, `internal/ddb/**/*_test.go`, `dynamodb/streams_test.go`, `dynamodb/ttl_test.go` |
| 8 | Conformance scenarios and claims table | `conformance/tests/test_dynamodb.py`, the claims file from foundation B1 |
| 9 | Console client: one-outcome create, typed item editing, the facts `Table` lacks, query fixes | `internal/console/client_ddb.go` |
| 10 | Console handlers and templates | `internal/console/handlers_ddb.go`, `internal/console/templates/ddb.html`, the `ddb_create` block in `internal/console/templates/create.html`, `internal/console/copy.go` |
| 11 | Console and browser tests | `internal/console/ddb_skeleton_test.go` and a new test file beside it, `e2e/tests/dynamodb.spec.ts` |
| 12 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 to 6 are the API and can be done without touching the console. Step 9
depends on step 4 (the stream row) and on foundation A2, A5, A6 and B4.

**Prior art.** The AWS console's item editor has two views, a form and
"DynamoDB JSON", and keeps the attribute's type beside its value in both; that
is the shape that fixes DDB-20, because plain JSON has no way to say "set" or
"binary". AWS's console also prefills the key attributes when adding an item,
which `ItemSkeleton` already copies. For the table rules, the source is the
developer guide's naming rules, data types and constraints pages; each rule's
test names the page.
