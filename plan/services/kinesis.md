# Kinesis

Stage 5. Size M. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds Kinesis's own.

Line numbers are as found on 2026-10-10 and will drift; the symbol beside each
one will not.

## 1. Summary

Kinesis Data Streams in Go over one bbolt store (`kinesis.bolt`), on one wire
protocol (AWS JSON 1.1, target prefix `Kinesis_20131202`). Real MD5 routing,
the five iterator types with the five-minute expiry, resharding with
parent/child lineage, and retention on a background sweep.

Peers: Lambda polls it (event source mappings, one iterator per shard), CloudWatch
Logs writes into it (subscription filters), it calls KMS to check a stream's key
is usable, the IAM guard evaluates its resource policy, CloudFormation creates it.
Kinesis itself dispatches to nobody.

The data plane is in good shape. What the grounding found is mostly at the
edges of the control plane: parameters accepted and dropped, list operations
that do not page or filter, and refusals AWS makes that doze-aws does not.

**Estimate:** M, three to four focused days. **Actual:** _fill in when done._
See the note on size at the end of section 11.

## 2. Contract

The model (`kinesis/testdata/ops_kinesis.json`) lists **45** operations. The
dispatch table (`kinesis/actions.go`, `handlers`) holds 37, `stubActions`
(`kinesis/kinesis.go`) holds 2, and 6 are in neither and answer a bare
`InvalidAction` (`coverage_model_test.go`, `Unreached`).

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateStream | F | F | Drops `Tags`; `ShardCount` optional. KIN-1, KIN-10. |
| DeleteStream | F | F | Ignores `EnforceConsumerDeletion`. KIN-2. |
| ListStreams | F | F | `Limit` and `ExclusiveStartStreamName` only; no `NextToken`. KIN-6. |
| DescribeStream | F | F | `Limit` ignored, `HasMoreShards` always false. KIN-6. |
| DescribeStreamSummary | F | F | |
| ListShards | F | F | `MaxResults`, `NextToken`, `ShardFilter`, `StreamCreationTimestamp` ignored. KIN-5. |
| PutRecord, PutRecords | F | F | `FailedRecordCount` is always 0: a listed difference. KIN-13. |
| GetShardIterator | F | F | Accepts a sequence number the shard never held. KIN-12. |
| GetRecords | F | F | `MillisBehindLatest` is wrong. KIN-4. |
| SplitShard, MergeShards | F | F | Accepted on an on-demand stream. KIN-11. |
| UpdateShardCount | F | F | No scaling limits. KIN-9. |
| UpdateStreamMode | F | F or C: decide | The mode's only effect is to refuse `UpdateShardCount`. KIN-18. |
| IncreaseStreamRetentionPeriod, DecreaseStreamRetentionPeriod | F | F | |
| AddTagsToStream, RemoveTagsFromStream, ListTagsForStream | F | F | No 50-tag limit; no paging. KIN-8, KIN-6. |
| TagResource, UntagResource, ListTagsForResource | F | F | Same store as above. |
| RegisterStreamConsumer, DeregisterStreamConsumer, DescribeStreamConsumer, ListStreamConsumers | F | F | Registration only. `Tags` on register ignored; no paging. KIN-1, KIN-6. |
| PutResourcePolicy, GetResourcePolicy, DeleteResourcePolicy | F | F | Stores anything. KIN-3, KIN-15. |
| StartStreamEncryption, StopStreamEncryption | C | C | The key is checked to exist and be enabled; nothing is encrypted. KIN-16. |
| EnableEnhancedMonitoring, DisableEnhancedMonitoring | C | C | |
| DescribeLimits | C | C | `OnDemandStreamCount` hard-coded. KIN-14. |
| DescribeAccountSettings, UpdateAccountSettings | C | C | |
| UpdateMaxRecordSize | C | C | Accepted and discarded; the ceiling stays 1 MiB. |
| SubscribeToShard | S | S | Declined: needs an HTTP/2 event stream. In `not-built.md`. |
| UpdateStreamWarmThroughput | S | S | Declined: a capacity hint. In `not-built.md`. |
| CreateChannel, DeleteChannel, DescribeChannel, ListChannels, UpdateChannel | absent | S | Answer `InvalidAction` today. KIN-7. |
| UpdateStreamRecordDistributionStrategy | absent | S | Same. KIN-7. |

Count by tier now: 29 F, 8 C, 2 S, 6 absent.

Settings need their own tiers, because "the operation is F" hides which
members do anything:

| Setting | Where | Now | Target | To confirm |
|---|---|---|---|---|
| `ShardCount` | CreateStream | F | F | Whether AWS refuses a provisioned create without it: not established (KIN-10). |
| `StreamModeDetails.StreamMode` | CreateStream | F | F | On-demand starts at 4 shards. |
| `StreamModeDetails.StreamMode` | UpdateStreamMode | stored; gates one call | decide | Nothing rescales an on-demand stream here (KIN-18). |
| `Tags` | CreateStream, RegisterStreamConsumer | dropped | F | KIN-1. |
| `MaxRecordSizeInKiB` | CreateStream, UpdateMaxRecordSize | dropped | C, said so | Whether it should be echoed in `DescribeStreamSummary`: not established. |
| `WarmThroughputMiBps` | CreateStream, UpdateStreamMode | dropped | C, said so | Inconsistent with `UpdateStreamWarmThroughput` being S. |
| `RetentionPeriodHours` | Increase/Decrease | F | F | 24 to 8760 enforced; swept once a minute. |
| `ExplicitHashKey` | PutRecord(s) | F | F | |
| `SequenceNumberForOrdering` | PutRecord | accepted, unused | F by construction | Sequences are stream-wide and monotonic, so ordering holds without it. Whether AWS validates the value: not established. |
| `EncryptionType`, `KeyId` | Start/StopStreamEncryption | C plus a real key check | C | |
| `ShardLevelMetrics` | Enable/DisableEnhancedMonitoring | C | C | |
| `Policy` | PutResourcePolicy | F under soft and enforce; not validated | F | KIN-3. |
| `EnforceConsumerDeletion` | DeleteStream | ignored | F | KIN-2. |
| `ShardFilter`, `MaxResults`, `NextToken` | ListShards | ignored | F | KIN-5. |
| `StreamId` | every stream operation | pattern-checked, then unused | not established | A request that names only `StreamId` is refused for lacking a name or ARN. What AWS does with it is not established. |
| Stream status | DescribeStream* | always `ACTIVE` | decide | AWS passes through `CREATING`, `UPDATING`, `DELETING`. Either model it or list it as a difference. |
| Consumer status | consumer operations | always `ACTIVE` | decide | Same decision. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `kinesis/`: 18 tests. `sdk_test.go` 12 (lifecycle, routing, explicit hash key, PutRecords order, iterator types, AT_TIMESTAMP bounds, split, merge, retention bounds, tags and consumers, validation, empty-shard split), `kms_test.go` 4, one coverage test, one parity test |
| Parity | `rejection_parity_test.go` replays `testdata/cases_kinesis.json`; the ledger says 356 of 356 model constraints enforced, `knownGaps` empty. 43 cases are skipped with a reason: GetRecords 11, SplitShard 17, MergeShards 15 |
| Coverage of the model | `coverage_model_test.go` passes only because six operations are listed as `Unreached` |
| Repo-level | `upgrade_test.go` (fixture has one stream), `identity_test.go` (ARN under another account and region), `persistence_surfaces_test.go` (a Logs filter onto a stream) |
| Conformance | `conformance/tests/test_kinesis.py`: 5 scenarios, all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/kinesis.spec.ts`: 17 tests |
| Mutation | No per-package baseline yet (foundation B3) |

There is no package-level restart test and no test of the resource policy in
`kinesis/` itself.

## 4. Findings

Evidence `run` means reproduced against a binary built from this tree on
2026-10-10, through the wire or through the console's own routes.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| KIN-1 | P0 | `CreateStream` with `Tags` succeeds and stores none; `ListTagsForStream` then returns an empty list. `RegisterStreamConsumer` ignores `Tags` the same way. | `kinesis/actions.go` `hCreateStream`, `hRegisterConsumer`; `store.go` `Create` | run | model (`Tags` is a member of both inputs) | SDK test: create with two tags, list them |
| KIN-2 | P0 | `DeleteStream` deletes a stream that has a registered consumer, and never reads `EnforceConsumerDeletion`. | `actions.go` `hDeleteStream`; `store.go` `Delete` | run | doc: DeleteStream API reference, `EnforceConsumerDeletion` ("if not set and the stream has registered consumers, the call fails with ResourceInUseException") | SDK test: register, delete without the flag, expect the refusal; with the flag, expect success |
| KIN-3 | P0 | `PutResourcePolicy` stores any non-empty string. `not json` is saved, the console toasts "Resource policy saved", and the guard skips a document it cannot parse, so under enforce a broken Deny silently allows. | `actions.go` `hPutResourcePolicy`; `guard.go` `guardRequest` (the `iampolicy.Parse` error is dropped) | run | memory for the exact code AWS answers; the common criteria already require refusal | Handler test: `not json`, valid JSON that is not a policy, a statement with no `Effect` |
| KIN-4 | P1 | `MillisBehindLatest` is "now minus the arrival of the last record returned". A reader that has just read the newest record is told it is behind by that record's age (3039 ms three seconds after a put), and 0 only when a read returns nothing. | `store.go` `Fetch` (`behind`); `records.go` `hGetRecords` | run | doc: GetRecords API reference ("zero indicates that record processing is caught up") | SDK test: put, wait, read to the tip, expect 0; read half a backlog, expect a positive number |
| KIN-5 | P1 | `ListShards` ignores `MaxResults`, `NextToken`, `ShardFilter` and `StreamCreationTimestamp`. `MaxResults: 1` returns every shard; `AFTER_SHARD_ID` returns the shard it names. A consumer library that asks for open shards only gets closed ones too. | `records.go` `hListShards` | run | model (members exist), doc for each filter type | SDK test per filter type on a stream that has been split once; paging with `MaxResults: 1` |
| KIN-6 | P1 | Paging elsewhere. `ListStreams` returns `HasMoreStreams` and no `NextToken` (run). `DescribeStream` ignores `Limit` and always says `HasMoreShards: false` (read). `ListStreamConsumers` ignores `MaxResults` and `NextToken` (read). `ListTagsForStream` ignores `Limit` and `ExclusiveStartTagKey`, `HasMoreTags` always false (read). | `actions.go` `hListStreams`, `hDescribeStream`, `hListConsumers`, `hListTagsForStream` | run / read | doc; which token an SDK paginator sends for `ListStreams` is not established | SDK paginator test for each, three resources, page size 1 |
| KIN-7 | P1 | Six model operations have neither a handler nor a named refusal and answer `InvalidAction: unknown Kinesis action`, which a caller cannot tell from a typo. The coverage test records them as `Unreached` instead of failing. | `kinesis.go` `ServeHTTP`, `stubActions`; `coverage_model_test.go` | run (`CreateChannel`) | model | Empty the `Unreached` list; the coverage test fails until each is in `stubActions` |
| KIN-8 | P1 | A stream accepts more than 50 tags (51 saved from the console in one go). | `actions.go` `addTags` | run | doc: AddTagsToStream API reference (50 tags per stream) | Handler test: 51st tag refused; code from the recording |
| KIN-9 | P1 | `UpdateShardCount` accepts any target: 2 to 50 succeeded. AWS documents bounds (no more than double, no less than half, the account shard limit, a daily count). | `reshard.go` `hUpdateShardCount` | run | doc: UpdateShardCount API reference | Decide which bounds to model (section 10), then a parity case for each |
| KIN-10 | P1 | `CreateStream` with no `ShardCount` and no mode makes a one-shard provisioned stream. | `actions.go` `hCreateStream` (`awsjson.Int(p, "ShardCount", 1)`) | run | doc says the count is required for a provisioned stream; the answer AWS gives is memory | Skipped parity case pending the recording |
| KIN-11 | P1 | `SplitShard` succeeds on an on-demand stream; `MergeShards` has no mode check either. | `reshard.go` `hSplitShard`, `hMergeShards` | run (split), read (merge) | memory | Skipped SDK test pending the recording |
| KIN-12 | P1 | `GetShardIterator` with `AT_SEQUENCE_NUMBER` or `AFTER_SEQUENCE_NUMBER` accepts a sequence number far outside the shard's range and hands back an iterator. | `records.go` `hGetShardIterator` | run | memory (`InvalidArgumentException`) | Skipped parity case pending the recording |
| KIN-13 | P1 | A record over 1 MiB is refused with `InvalidArgumentException` and "record 0: data of 1048577 bytes exceeds the 1048576 byte limit", on a single `PutRecord` too, because it shares the batch path. The model table allows `Data` up to 10 MiB, so the model check never fires first. | `store.go` `Put`; `validate.go` `PutRecord` | run | not established: the code and message AWS gives at the stream's own limit | Record first; then a parity case |
| KIN-14 | P1 | `DescribeLimits` returns `OnDemandStreamCount: 0` whatever exists, while `OpenShardCount` on the same answer is live. The console's Account table then shows "0 / 50" for an on-demand stream. | `actions.go` `hDescribeLimits` | run | model | Handler test: one on-demand stream, count is 1 |
| KIN-15 | P1 | `GetResourcePolicy` on a stream that exists and has no policy answers `ResourceNotFoundException: Stream t3 … not found`. | `actions.go` `hGetResourcePolicy` | run | not established: what AWS returns for "no policy" | Record first |
| KIN-16 | P1 | `StopStreamEncryption` succeeds on a stream that is not encrypted, and with any `KeyId`. | `actions.go` `hStopEncryption` | run | not established | Record first |
| KIN-17 | P1 | The sweep removes a closed shard as soon as it holds no records, with no regard to retention. A split of an empty shard therefore loses its parent within a minute: children name a `ParentShardId` that `ListShards` no longer returns, and an iterator held on the parent answers `ResourceNotFoundException` in place of a null iterator and `ChildShards`. | `store.go` `Sweep` | read | doc/memory: a closed shard is kept for the retention period | Store test with a fake clock: split an empty shard, sweep, list |
| KIN-18 | P1 | An on-demand stream is never resized. The mode's whole effect is that `UpdateShardCount` is refused; the ledger calls `UpdateStreamMode` functional. | `reshard.go` `hUpdateShardCount`; grep of `modeOnDemand` | read, run | n/a (a contract decision) | Ledger test once the tier is decided |
| KIN-19 | P2 | Put from the console redirects to Records with `start=latest`. A LATEST iterator starts after the newest record, so the record just written is never shown: "Nothing in this window… showing 0 of 0 read". The comment above the redirect says the opposite is intended. The browser test for put navigates to Records itself, so it never sees this. | `internal/console/handlers_kinesis.go` `kinesisPut` (both redirects) | run | n/a | Browser test: put, follow the redirect, expect the row |
| KIN-20 | P2 | The Arrived column is UTC with no label; the "Up to" filter beside it is labelled "local time" and parsed as local. A user in IST filters by a time five and a half hours from what the column shows. | `client_kinesis.go` `decodeRecord`; `templates/kinesis.html` `kinesis_filters` | run | n/a | Browser test under a non-UTC `TZ`: filter by a time read off the column |
| KIN-21 | P2 | Details says "On-demand streams scale themselves; the count is not yours to set". Nothing scales them (KIN-18). Merge and Scale are hidden on an on-demand stream while Split stays on the Shards tab. | `templates/kinesis.html` `kinesis_details`, `kinesis_stream` | read, run | n/a | Browser test asserts the wording and that the three controls agree |
| KIN-22 | P2 | Opening the Records tab of a stream whose KMS key is disabled returns a 400 with only the error fragment: no page, no chrome. The Read button's path is fine (the refusal lands in the form). | `handlers_kinesis.go` `kinesisRecords` calling `c.fail` on a full-page GET | run | n/a | Browser test: disable the key, click the tab, expect the page with the refusal in it |
| KIN-23 | P2 | The wiring strip says "nothing feeds this yet" for every stream, always. The graph only draws stream-to-function edges; an SDK producer and a Logs subscription filter are invisible to it. | `client_flow.go` (no edge into a `kinesis` node); `templates/panes.html` `connections` | read | n/a | Console test: a Logs filter onto a stream shows as "Fed by" |
| KIN-24 | P2 | The console cannot do what the API can at create: no mode (on-demand), no tags. It also sends a blank or zero shard count as 1, hiding the refusal. | `templates/kinesis.html` `kinesis_create`; `client_kinesis.go` `CreateStream` | read | n/a | Browser test for an on-demand create once the form has it |
| KIN-25 | P3 | The metrics panel says "There is no CloudWatch here". CloudWatch is local now; what is missing is the producer of `AWS/Kinesis` shard metrics, as the ledger says. | `templates/kinesis.html` `kinesis_details` | read | n/a | Browser test asserts the ledger's sentence |
| KIN-26 | P3 | Stale comments say the resource policy is not evaluated. | `client_kinesis.go` (comment above `ShardMetrics`); `kinesis/actions.go` (comment above `encTypeOf`) | read | n/a | none; corrected with KIN-3 |
| KIN-27 | P3 | The ledger says validation covers "32 of the 35 dispatched operations"; `handlers` has 37 entries. Which number is stale is not established. | `docs/SUPPORT.md`, Kinesis "Input validation" | read | n/a | The ledger test, once it counts |
| KIN-28 | P3 | The console's stream list drops a stream whose describe fails, with no sign. Its PutRecords path formats "N refused (code)" for a partial failure the service can never produce. | `client_kinesis.go` `ListStreams`; `handlers_kinesis.go` `kinesisPut` | read | n/a | none; tidy with the console work |

**Owned by the foundation, seen here.** Not repeated above.

| Foundation item | The Kinesis instance |
|---|---|
| A2 | The "rule comes from AWS's own service model" line under `ResourceInUseException` for a duplicate stream, under the KMS refusals, and under the hand-written retention and record-size rules (run). |
| A5 | Tags are saved one `AddTagsToStream` per key (`client_tags.go`); a refused value leaves earlier keys written. |
| A7 | `copy.go`'s Kinesis `Unsel` line never renders: `kinesisStreams` opens the first stream. |
| A11 | With Count above 1 the form calls `PutRecords` and the preview shows `put-record`. |
| A12 | "Retention set" when the value is unchanged and no call is made (`client_kinesis.go` `SetRetention`). "Scaled to N shards" when N is already the count is the same case. |
| A13 | All five `hx-confirm` sentences were read against the code and are true today. The delete sentence must stay true after KIN-2: the console will have to pass `EnforceConsumerDeletion` or say why it was refused. |
| Stage 0 criterion | `SUPPORT.md`'s Kinesis policy row says the policy "round-trips only under the default `off`"; the default is `soft`. |

**Moved into this stage from CloudFormation (CFN-2, P0, run).**
`AWS::Kinesis::Stream` is mapped, reports CREATE_COMPLETE and makes no stream.
It keeps its id and is closed here, because Kinesis is not done while a stack
that declares a stream makes nothing. Acceptance: a stack with one stream leaves
a stream that `DescribeStreamSummary` returns, with the declared shard count,
mode and retention; the plan's README says why it moved.

## 5. What to check

Beyond the common criteria. One protocol, so each line is checked once.

**Streams**
- [ ] Name: 1 to 128, `[a-zA-Z0-9_.-]`; refused by the model check with `ValidationException`, never by the older hand rule in `validStreamName`.
- [ ] A duplicate create is `ResourceInUseException`, for the same and for different arguments.
- [ ] Create with `Tags`, with `StreamModeDetails` alone, with `ShardCount` alone, with both, with neither (KIN-1, KIN-10).
- [ ] On-demand create ignores a supplied `ShardCount` and opens 4; establish whether AWS refuses the combination.
- [ ] Shards tile the key space with no gap and no overlap for 1, 2, 3, 7 and 100 shards; the last absorbs the remainder.
- [ ] Delete: missing stream; with consumers, with and without `EnforceConsumerDeletion` (KIN-2); records, shards, consumers and the policy all go.
- [ ] Every operation accepts `StreamARN` in place of `StreamName`; an ARN for another account or region is handled as AWS handles it (not established).
- [ ] Stream status: decide `ACTIVE`-only or modelled, and write it down.

**Writing**
- [ ] Partition key 1 to 256 **characters**: confirm the check counts characters, not bytes, with a multi-byte key.
- [ ] `ExplicitHashKey`: 0, 2^128 − 1, 2^128, negative, non-numeric; it overrides the partition key.
- [ ] Record size: exactly 1 MiB accepted, one byte over refused, with the code AWS uses (KIN-13); whether the partition key counts toward the limit is not established.
- [ ] `PutRecords`: 1, 500 and 501 entries; an empty list; the 5 MiB request ceiling (not established here); order of results matches order of entries.
- [ ] A put to a stream whose only shard covering the key is closed cannot happen; prove it after split, merge and rescale.
- [ ] A put under a disabled, pending-deletion and missing key: three different codes (held by `kms_test.go`; keep).

**Reading**
- [ ] Each iterator type on an empty shard, a shard with records, and a closed shard.
- [ ] `AT_TIMESTAMP` before the first record, between two, after the last, and in the future.
- [ ] A sequence number below the trim horizon, above the tip, and from another shard (KIN-12).
- [ ] Iterator expiry at five minutes: `ExpiredIteratorException`, and the message shape.
- [ ] A malformed iterator and an iterator for a deleted stream.
- [ ] `GetRecords` `Limit` 1, 10000, 10001; the 10 MiB response ceiling with large records.
- [ ] `MillisBehindLatest` at the tip, mid-backlog and on an empty read (KIN-4).
- [ ] A drained closed shard returns a null `NextShardIterator` and `ChildShards` naming both parents after a merge.
- [ ] The 43 skipped parity cases for GetRecords, SplitShard and MergeShards each run against a stream made for that case.

**Resharding**
- [ ] Split point: at the start, at the end, one inside either edge, outside the range; a closed shard; a one-key shard.
- [ ] Merge: non-adjacent, the same shard twice, either one closed, arguments in either order.
- [ ] `UpdateShardCount`: to the same count (a no-op with no new shards), up, down, the bounds decided for KIN-9, an on-demand stream, a scaling type other than `UNIFORM_SCALING`.
- [ ] After each reshard, records for one partition key read in order through parent then child.
- [ ] Split and merge on an on-demand stream (KIN-11).
- [ ] An empty closed parent survives until retention (KIN-17).

**Retention**
- [ ] 23, 24, 8760, 8761; increase to a lower value and decrease to a higher one are refused; equal values: establish what AWS does.
- [ ] After a decrease, records older than the new window go at the next sweep; `TRIM_HORIZON` starts at the oldest survivor.
- [ ] The sweep does not stall on one unreadable stream record.

**Tags**
- [ ] Both tag APIs read and write the same set.
- [ ] 50th accepted, 51st refused (KIN-8); key 1 to 128, value 0 to 256; the allowed characters (not established here).
- [ ] `ListTagsForStream` paging (KIN-6).

**Consumers**
- [ ] Duplicate name refused; name pattern; the per-stream limit (20 on AWS, doc) decided.
- [ ] Describe and deregister by name and by consumer ARN; a missing consumer; a consumer of a deleted stream.
- [ ] `SubscribeToShard` answers the code decided for S rows, with the reason.

**Stored settings**
- [ ] Each C setting reads back exactly as set, through `DescribeStream` and `DescribeStreamSummary`.
- [ ] Enhanced monitoring with `ALL`, with a mix, disable of one that is not on.
- [ ] `StartStreamEncryption` on a stream already encrypted, and with an alias, a key id and a key ARN.
- [ ] `DescribeLimits` counts on-demand streams (KIN-14).

**Policy**
- [ ] Stored only if it parses as a policy (KIN-3); empty removes; get with none set (KIN-15).
- [ ] Under enforce: a Deny on `kinesis:PutRecord` refuses the root on `PutRecord` and not on `PutRecords` (run today, keep as a test); a `logs.amazonaws.com` statement admits the Logs writer.
- [ ] Under soft: the same calls succeed and are recorded.
- [ ] The guard sees the stream when it is named by `StreamARN`, `ResourceARN` and a consumer ARN.

**Lifecycle**
- [ ] A restart keeps streams, shard lineage, records, the sequence counter, consumers, tags, the policy and every C setting.
- [ ] An iterator issued before a restart still works after it, within its five minutes.

## 6. Console

The design's "Kinesis" page. 18 boards. Templates: `internal/console/templates/kinesis.html`
(which holds its own create block), shared pieces in `panes.html` and
`layout.html`. Handlers: `handlers_kinesis.go`, `handlers_tags.go`; client:
`client_kinesis.go`, `client_tags.go`, `client_flow.go`; routes: `kinesisRoutes`
in `routes_messaging.go` (8 GET, 14 POST).

The real tabs are Shards, Records, Details and Tags. Put record and Retention
live on Shards; consumers, encryption, metrics, mode, the policy and the
account table live on Details. The boards keep that.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Kinesis-Home | A stream, Shards tab: shard table, wiring strip, put and retention panels | `kinesis_stream`, `kinesis_head`, `connections`; `kinesisStream`, `kinesisShardDepth` | exists; teaching lines and the reader check under Retention are **new** |
| Kinesis-01-None | No streams yet | `kinesis_home`, `lp_empty`, `empty_detail`; `kinesisStreams` | exists |
| Kinesis-02-Create | New stream, two shards | `kinesis_create`; `createPage`, `kinesisCreate` | form exists; "Where keys land" and the teaching lines are **new** |
| Kinesis-03-Create-Shards | Four shards, try a key | `kinesis_create` | "Try a partition key" is **new** |
| Kinesis-04-Create-Refused | Refused inline | `fail_inline` | exists |
| Kinesis-05-Shards-Resharded | After a split: closed parent, children, lineage | `kinesis_stream` | exists; "What a split did" is static text |
| Kinesis-06-Put | Put record, a batch of five | `kinesis_stream` (the put form), `cli`; `kinesisPut` | form exists; "Where the 5 land" and "After Put" are **new** (the second is KIN-19's fix) |
| Kinesis-07-Put-Refused | Put and retention refused | `fail_inline`; `kinesisPut`, `kinesisRetention` | exists |
| Kinesis-08-Records | Records, all shards merged | `kinesis_records`, `kinesis_filters`, `kinesis_record_table`, `kinesis_record_rows`, `kinesis_record_foot`; `kinesisRecords`, `kinesisRecordsQuery` | exists; "What this read did" is **new** |
| Kinesis-09-Record-Full | One record in the drawer | `kinesis_record_detail`; `kinesisRecord` | exists |
| Kinesis-10-Records-Routed | Filtered by partition key | `kinesis_filters`, `kinesis_record_foot`; `recordQuery` | exists; "What this read did" is **new** |
| Kinesis-11-Records-Empty | Two empty results, and following | `kinesis_record_table`, `kinesis_record_rows` | exists; the Start-at table is static text from `startAt.iteratorInput` |
| Kinesis-12-Details-Layout | Shard count, merge, capacity mode | `kinesis_details`; `kinesisDetails`, `kinesisScale`, `kinesisMerge`, `kinesisMode` | exists; "2 now → 4 after" and the teaching lines are **new** |
| Kinesis-13-Details-Stored | Encryption, metrics, consumers, policy, account | `kinesis_details`, `policy_builder`; `kinesisEncryption`, `kinesisMetrics`, `kinesisConsumerAdd`, `kinesisConsumerDel`, `kinesisPolicy` | exists |
| Kinesis-14-Tags | Tags as a draft | `kinesis_tags`, `tags_panel`, `tag_editor`; `kinesisTags`, `tagsSave` | exists (see foundation A5) |
| Kinesis-15-Confirms | Delete, split, merge, rescale, deregister | the five `hx-confirm`s in `kinesis.html` | exists; restyled by the foundation's confirm |
| Kinesis-16-Denied | Put denied under enforce | `fail_inline` | exists (message reproduced under `--iam-mode enforce`) |
| Kinesis-17-Bad-Days | Disabled key; stream deleted under the page; record aged out | `fail_inline`; `ReadOne`'s own error | exists; case 1 on a tab load is KIN-22 |

There is no "streams, none open" board, on purpose: the route never shows that
state. If foundation A7 decides lists open with nothing selected, one board is
added here and `copy.go`'s `Unsel` line becomes reachable.

**New items, and what each is built from**

| Item | Board | Built from |
|---|---|---|
| Teaching lines under each field | Home, 02, 03, 06, 12 | The settings table (foundation B4): ranges from `kinesis/validate.go`, the retention and record-size bounds from `store.go` constants, tier from the ledger |
| Where keys land | 02, 03 | The shard count typed. A pure function of the form: the even split in `store.go` `tileShards` |
| Try a partition key | 03 | `ShardForKey` (`client_kinesis.go`) over the shards the form would create. Needs MD5 in the browser or one small endpoint; neither exists |
| Where the N land | 06 | `ShardForKey` over `ListShards`, with the `-1`, `-2` suffix `kinesisPut` applies |
| After Put | 06 | The redirect `kinesisPut` already makes, with the existing `5m` start option in place of `latest` |
| The reader check under Retention and Shard count | Home, 12 | The wiring graph's `esm` edge from this stream to a function |
| 2 now → 4 after | 12 | The open shards from `ListShards` and the parent rule in `reshard.go` `hUpdateShardCount` (`parentAt`) |
| What this read did | 08, 10 | The `RecordQuery` that ran and the per-shard counts `readOneShard` already returns |

**Where the teaching lines' facts come from**

| Line | Source |
|---|---|
| Stream name 1 to 128, the character set | model (`validate.go`, `CreateStream`) |
| Shards 1 and up | model (`ShardCount` range, no maximum) |
| "A partition key is hashed to exactly one shard… order holds per key" | ledger (MD5 routing), doc |
| "AWS gives a shard 1 MiB/s or 1 000 records/s of writes" | **doc** (Kinesis quotas page); on the recording queue as a citation to check, since it cannot be recorded cheaply |
| "doze-aws does not throttle" | ledger, "Differences from AWS" |
| "Created PROVISIONED"; "a stream created on-demand starts with 4" | code (`store.go` `Create`), ledger |
| Partition key 1 to 256 | model |
| "Left empty, the console sends `console`" | code (`kinesisPut`) |
| Data up to 1 048 576 bytes | code (`maxRecordSize`), ledger; AWS side is KIN-13 |
| Retention 24 to 8 760, default 24, swept once a minute | code (`store.go` constants, `kinesis.go` `sweeper`), ledger |
| "Reading does not remove it" | `copy.go`, doc |
| "Raising calls Increase…, lowering Decrease…" | code (`SetRetention`) |
| Target shards, UNIFORM_SCALING only | model (enum), code |
| "Each new shard names the old one that owned its first hash key" | code (`parentAt`); the ledger calls it an approximation of AWS's plan |
| Mode sentences | code; rewritten once KIN-18 is decided |
| Encryption "recorded, not applied", key checked | ledger (tier C and "Differences") |
| Metrics "nothing produces the AWS/Kinesis shard metrics" | ledger (KIN-25 is the template disagreeing) |
| Consumers: registration real, `SubscribeToShard` refused, with the message | code (`stubActions`), ledger |
| Policy under soft and enforce; the `logs.amazonaws.com` statement | ledger |
| Tag value 0 to 256 | model |
| What each Start-at does | code (`startAt.iteratorInput`, `windows`) |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Home, 03, 12: "a Lambda mapping polls each shard on its own and follows new
  shards without a restart". From the ledger; not run end to end. Lambda's
  poller also starts every shard at `TRIM_HORIZON` (`lambda/extras.go`
  `pollKinesis`); whether it honours `StartingPosition` is not established.
- Home: "to miss a record it would have to fall behind by more than 24 h". True
  of retention; says nothing about the reader's real position, which the console does not know.
- 12: "Nothing resizes the stream by itself here". Read, not run over time; and
  it changes if KIN-18 is decided the other way.
- 12, 15: "Switching keeps the shards you have" (run) is doze-aws's behaviour.
  What AWS does to the shard set on a mode switch is not established, so the
  board must not imply parity.
- 14: "the stream itself does not act on [tags]". Read.
- 15: the delete confirm lists "the 1 registered consumer" as removed. True
  today, and exactly what KIN-2 changes.
- 17: "a reader that polls the stream" is stopped by a disabled key. Put and
  the console's read are run; the Lambda poller is read.
- 13: the consumer ARN's numeric suffix and the KMS key id are sample data.
- 08, 10: "These calls are not on the wire" rests on the foundation's statement
  that the wire shows the app's traffic only.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | Tags at create (KIN-1); delete with consumers (KIN-2); policy validation (KIN-3); `MillisBehindLatest` with a fake clock (KIN-4); sweep and an empty closed shard (KIN-17); on-demand count in limits (KIN-14); tag limit (KIN-8) |
| SDK v2 | `ListShards` filters and paging (KIN-5); paginators for streams, consumers, tags, `DescribeStream` (KIN-6); `UpdateShardCount` bounds (KIN-9); per-key order across split, merge and rescale in one test |
| Coverage | `Unreached` emptied (KIN-7) |
| Rejection parity | The 43 skipped cases, each on its own stream; skipped-pending-recording cases for KIN-10, KIN-11, KIN-12, KIN-13, KIN-15, KIN-16 |
| Guard | Deny and Allow under enforce and soft; the three ways a request names a stream; an unparseable stored document from before the fix |
| Restart | Everything in section 5 "Lifecycle" through the foundation's helper (B5) |
| Identity | The package suite under a non-default account and region (B2), including consumer ARNs |
| Conformance (boto3) | Every row of section 8; a consumer loop that walks a reshard the way the KCL does; a paginator over shards |
| Browser | Put then the redirect (KIN-19); time filter under a non-UTC zone (KIN-20); on-demand wording and controls (KIN-21); Records tab with a disabled key (KIN-22); "Fed by" with a Logs filter (KIN-23); on-demand and tags at create (KIN-24); each `new` item; each refusal on boards 04, 07, 12, 13, 14; the denied put under enforce; the five confirms' sentences |
| Mutation | `kinesis/` baseline, then survivors in `reshard.go`, `store.go` (`shardFor`, `Fetch`, `Sweep`), `iterator.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today.

| Claim | Source now | Scenario |
|---|---|---|
| Delete with registered consumers and no `EnforceConsumerDeletion` is `ResourceInUseException` | doc | `test_kinesis::delete_with_consumers` |
| A resource policy that does not parse, or is not a policy, is refused; the code | memory | `invalid_resource_policy` |
| `GetResourcePolicy` with none set | memory | `get_policy_when_none` |
| `MillisBehindLatest` is 0 at the tip and positive behind it | doc | `millis_behind_latest` |
| Each `ShardFilter` type's result set after a split | doc | `list_shards_filters` |
| `ListStreams`, `ListShards`, `ListStreamConsumers`, `ListTagsForStream` tokens and limits | doc | `paging` |
| The 51st tag; the code | doc | `tag_limit` |
| `UpdateShardCount` bounds and their codes | doc | `update_shard_count_bounds` |
| Provisioned create with no `ShardCount` | doc, answer memory | `create_without_shard_count` |
| On-demand create with a `ShardCount` | memory | `create_on_demand_with_count` |
| Split and merge on an on-demand stream | memory | `reshard_on_demand` |
| What a mode switch does to the existing shards | memory | `mode_switch_shards` |
| A sequence number outside the shard at `GetShardIterator` | memory | `iterator_bad_sequence` |
| `AT_TIMESTAMP` in the future | memory | `iterator_future_timestamp` |
| A record one byte over the limit: code and message; whether the key counts | memory | `record_too_large` |
| `PutRecords` 501 entries and the 5 MiB request ceiling | doc | `put_records_limits` |
| `StopStreamEncryption` on an unencrypted stream, and with the wrong key | memory | `stop_encryption_mismatch` |
| A closed shard stays listed for the retention period | doc | `closed_shard_lifetime` (slow: needs a day; may stay `doc`) |
| Increase or decrease retention to the current value | memory | `retention_same_value` |
| An expired iterator's message | doc | `expired_iterator` (needs five minutes) |
| A request naming only `StreamId` | memory | `stream_id_only` |
| An unknown or unsupported action: the code AWS-facing SDKs expect for S rows | memory | shared with the foundation |
| Consumer limit per stream (20) and the code | doc | `consumer_limit` |
| Per-shard write limits (1 MiB/s, 1 000 records/s) quoted on boards 02 | doc | none: a citation, checked against the quotas page |
| Stream and consumer status transitions | doc | `status_transitions` |

## 9. Owed to integration

| What | With |
|---|---|
| An event source mapping honours `StartingPosition`; today the poller opens every shard at `TRIM_HORIZON` | Lambda |
| A mapping follows a split, a merge and a rescale without a restart, and keeps per-key order | Lambda |
| A mapping on a stream whose key is disabled: what the function and the mapping's state show | Lambda, KMS |
| A mapping on a deleted stream stops polling | Lambda |
| A Logs subscription filter delivers one gzip record per batch; the stream must exist when the filter is made (the overview lists this as not checked) | CloudWatch Logs |
| Under enforce a Logs filter needs the `logs.amazonaws.com` statement; without it delivery is denied and visible | CloudWatch Logs, IAM |
| The wiring graph draws Logs → stream so "Fed by" can be true (KIN-23) | CloudWatch Logs, console |
| Key states: disabled, pending deletion, deleted, re-enabled, each seen by a producer and a consumer | KMS |
| `AWS::Kinesis::Stream` through CloudFormation: which properties are applied (mode, retention, tags, encryption) is not established | CloudFormation |
| Identity policies on `kinesis:*` actions, and the resource policy combined with them, for a non-root caller | IAM |
| A KCL application: lease table in DynamoDB, checkpoints, a reshard mid-run | DynamoDB |
| Whether EventBridge or API Gateway can target a stream here | not established |
| Retention sweep under load, and a restart mid-sweep | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] KIN-1 to KIN-28 each closed, with the test that failed first named beside it, or moved with a reason.
- [ ] All 45 operations have a tier in `SUPPORT.md`; `coverage_model_test.go` has an empty `Unreached` list; the six new S rows are in `not-built.md`.
- [ ] Decided and written into `SUPPORT.md`: the tier of `UpdateStreamMode` and what on-demand means here (KIN-18); which `UpdateShardCount` bounds are modelled (KIN-9); stream and consumer status; the consumer limit; whether a provisioned create needs `ShardCount` (KIN-10).
- [ ] The settings table in section 2 has no "not established", "decide" or "dropped" left: `StreamId`, `MaxRecordSizeInKiB`, `WarmThroughputMiBps`, `SequenceNumberForOrdering` each have a tier and a sentence.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its test written and skipped.
- [ ] Every list operation pages; `ListShards` honours every filter type.
- [ ] The 43 skipped parity cases run, or each has a reason that is still true.
- [ ] A package-level restart test exists and covers lineage, the sequence counter, consumers and the policy.
- [ ] KIN-27 resolved: the ledger's operation counts are produced by the ledger test, not typed.
- [ ] All 18 boards implemented from the foundation's shell with no Kinesis-only chrome, and each `new` item reads only the data named for it.
- [ ] No statement on a Kinesis screen is in the "not yet backed" list in section 6.
- [ ] After a put the user sees the record they put (KIN-19); every time on a Kinesis page says its zone, and the filter and the column agree (KIN-20).
- [ ] An on-demand stream's three reshard controls agree with each other and with the API (KIN-11, KIN-21).
- [ ] The five confirms say only what the code does, including delete after KIN-2.
- [ ] Every C setting on Details reads as cosmetic, in the ledger's words (KIN-25).
- [ ] `e2e/tests/kinesis.spec.ts` covers all 22 routes under `/kinesis` with the route gate on; the accessibility sweep is clean on the four tabs, create and the record drawer.
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered differently". The unverified count is written here: ____ (5 today).
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: tiers for 45 operations, the settings table, the decisions in section 10 | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for KIN-1 to KIN-18 | `kinesis/sdk_test.go`, `kinesis/rejection_parity_test.go`, `kinesis/coverage_model_test.go`, new `kinesis/store_test.go`, `kinesis/guard_test.go`, `kinesis/restart_test.go` |
| 3 | Silent drops and wrong answers: tags at create, delete with consumers, policy validation, `MillisBehindLatest`, on-demand count | `kinesis/actions.go`, `kinesis/store.go`, `kinesis/records.go`, `kinesis/guard.go` |
| 4 | Listing: `ListShards` filters and paging, the other three pagers | `kinesis/records.go`, `kinesis/actions.go`, `kinesis/store.go` |
| 5 | Named refusals for the six absent operations | `kinesis/kinesis.go` (`stubActions`) |
| 6 | Reshard rules: bounds, mode checks, the sweep and empty closed shards | `kinesis/reshard.go`, `kinesis/store.go` (`Sweep`) |
| 7 | Section 5 sweep; the 43 skipped cases; restart and identity passes | `kinesis/rejection_parity_test.go`, `kinesis/testdata/cases_kinesis.json`, `kinesis/sdk_test.go` |
| 8 | Conformance scenarios and the claims table | `conformance/tests/test_kinesis.py`, the claims file from foundation B1 |
| 9 | Console fixes: redirect, time zone, on-demand wording and controls, tab-load failure, create form | `internal/console/handlers_kinesis.go`, `client_kinesis.go`, `templates/kinesis.html` |
| 10 | Console build: the 18 boards and the `new` items | `templates/kinesis.html`, `handlers_kinesis.go`, `client_kinesis.go`, `client_flow.go` (the Logs edge), `routes_messaging.go` if the key-routing endpoint is added, `testdata/routes.txt` |
| 11 | Browser tests | `e2e/tests/kinesis.spec.ts` |
| 12 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

**Prior art.** The ledger already names the usual local-Kinesis story: a
separate mock process supervised by the emulator. That is the reason this
package exists, and it means there is no in-tree precedent to copy for the
listing and reshard rules; AWS's API reference pages for `ListShards`,
`UpdateShardCount`, `DeleteStream` and `GetRecords` are the source, and each
test names its page. The consumer contract in section 5 (drain the parent, take
the null iterator and `ChildShards`, then read the children) is the one the
Kinesis Client Library relies on, so a KCL-shaped consumer loop is the right
conformance scenario for it.

**On the size.** M holds for the API if the six absent operations are refused
by name and KIN-9 is settled by decision. Two things could push it to L: the
recording queue is long (25 claims, 12 of them `memory`), and three console
items need something that does not exist yet (the settings accessor from B4, an
MD5 routing helper for the browser, and a Logs edge in the wiring graph).
