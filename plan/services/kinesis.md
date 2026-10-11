# Kinesis

Stage 5. Size **L** (was M; see section 1 and the note on size in section 11).
Follows [../01-protocol.md](../01-protocol.md); the common acceptance criteria
there apply in full, and this page adds Kinesis's own.

Line numbers are as found on 2026-10-10 and will drift; the symbol beside each
one will not.

**Pre-stage review, 2026-10-11.** The document was read against the model's own
documentation (`.audit-models/kinesis.json`, every operation and every member
of its inputs and outputs), against the code, and against rendered boards beside
rendered pages. Corrections are marked in place as *Corrected 2026-10-11*.
Findings KIN-1 to KIN-28 are the first pass; KIN-29 to KIN-55 were found by the
review (section 4, second table). Section 6a is the board-by-board comparison;
section 12 answers the twelve questions of
[../02-lessons-from-sqs.md](../02-lessons-from-sqs.md). Source tags: `model` is
the model's documentation or traits (quoted), `memory` is not fixed on, `run`
and `read` are about doze-aws. No documentation page outside the model was
read, so nothing here is tagged `doc`.

## 1. Summary

Kinesis Data Streams in Go over one bbolt store (`kinesis.bolt`), on one wire
protocol (AWS JSON 1.1, target prefix `Kinesis_20131202`; `model`: the service
declares `awsJson1_1` only). Real MD5 routing, the five iterator types with the
five-minute expiry (`model`: "A shard iterator expires 5 minutes after it is
returned"), resharding with parent/child lineage, and retention on a background
sweep.

Peers: Lambda polls it (event source mappings, one iterator per shard), CloudWatch
Logs writes into it (subscription filters), it calls KMS to check a stream's key
is usable, the IAM guard evaluates its resource policy, CloudFormation creates it.
Kinesis itself dispatches to nobody.

*Corrected 2026-10-11: was "The data plane is in good shape. What the grounding
found is mostly at the edges of the control plane".* The data plane has real
gaps too: a `DryRun` put writes the record (KIN-29), the record-size rule is
wrong in three ways (KIN-13, KIN-30, KIN-31), the partition key is counted in
bytes (KIN-32), a consumer ARN acts on the stream's policy and tags (KIN-34),
and the stream's resource policy is skipped by any request that names no stream
(KIN-40). The control plane has the parameters accepted and dropped, the
unpaged lists and the refusals AWS makes that doze-aws does not, as first
found.

**Estimate:** ~~M, three to four focused days~~ *Corrected 2026-10-11:* **L,
eight to ten focused days**: the API about four to five (55 findings, not 28;
paging with expiring tokens on four operations; a status model for streams and
consumers if decided; a per-stream record-size limit; a consumer-keyed tag and
policy store; the strict-mode switch), the console about four (17 of 18 boards
differ in shape or content, section 6a, and two of them need a new endpoint),
the gates one. The plan's four days was for 28 findings and 18 boards whose
fidelity had not been measured; the SQS lesson was that sizing came out several
times low. **Actual:** _fill in when done._ See the note on size at the end of
section 11.

## 2. Contract

The model (`kinesis/testdata/ops_kinesis.json`, identical to the operation list
of `.audit-models/kinesis.json`) lists **45** operations. The dispatch table
(`kinesis/actions.go`, `handlers`) holds 37, `stubActions`
(`kinesis/kinesis.go`) holds 2, and 6 are in neither and answer a bare
`InvalidAction` (`coverage_model_test.go`, `Unreached`). *Checked 2026-10-11:
counts are right (`run`: CreateChannel and UpdateStreamRecordDistributionStrategy
answer `InvalidAction`).*

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateStream | F | F | Drops `Tags`; `ShardCount` optional. KIN-1, KIN-10. *Corrected 2026-10-11: also drops `MaxRecordSizeInKiB`, `WarmThroughputMiBps` and `RecordDistributionStrategy` (`run`; a bad enum value answers 200) and accepts 100 001 shards. KIN-30, KIN-38, KIN-39.* Asynchronous on AWS (`model`: "immediately returns and sets the stream status to CREATING"): KIN-47. |
| DeleteStream | F | F | Ignores `EnforceConsumerDeletion`. KIN-2. Asynchronous on AWS (`model`: "the specified stream is in the DELETING state until Kinesis Data Streams completes the deletion"): KIN-47. |
| ListStreams | F | F | `Limit` and `ExclusiveStartStreamName` only; no `NextToken`. KIN-6. *Corrected 2026-10-11: the model marks the operation `paginated` on `NextToken`/`NextToken` with `Limit` as the page size, so which token an SDK paginator sends is established.* |
| DescribeStream | F | F | `Limit` ignored, `HasMoreShards` always false. KIN-6. |
| DescribeStreamSummary | F | F | *Corrected 2026-10-11: does not return `MaxRecordSizeInKiB`, `WarmThroughput`, `RecordDistributionStrategy`, `ChannelCount` or `StreamId`, all members of the model's summary (KIN-30, KIN-39).* |
| ListShards | F | F | `MaxResults`, `NextToken`, `ShardFilter`, `StreamCreationTimestamp` ignored. KIN-5, KIN-42. |
| PutRecord, PutRecords | F | F | `FailedRecordCount` is always 0: a listed difference. KIN-13. *Corrected 2026-10-11: `DryRun` ignored (KIN-29); request total not bounded (KIN-31); key counted in bytes (KIN-32).* |
| GetShardIterator | F | F | Accepts a sequence number the shard never held. KIN-12. *Corrected 2026-10-11: and `DryRun` is ignored (KIN-29), and a sequence number over 2^64 is refused with the wrong message (KIN-43).* |
| GetRecords | F | F | `MillisBehindLatest` is wrong. KIN-4. *Corrected 2026-10-11: `DryRun` ignored (KIN-29); a request that names no stream bypasses the resource policy (KIN-40).* |
| SplitShard, MergeShards | F | F | Accepted on an on-demand stream. KIN-11. *Corrected 2026-10-11: `run` for both, not just split; the model says `ValidationException`.* Asynchronous on AWS (UPDATING): KIN-47. |
| UpdateShardCount | F | F | No scaling limits. KIN-9. |
| UpdateStreamMode | F | F or C: decide | The mode's only effect is to refuse `UpdateShardCount`. KIN-18. *Corrected 2026-10-11: and `SplitShard`/`MergeShards` are not refused on on-demand (KIN-11), and `WarmThroughputMiBps` is accepted for a switch to PROVISIONED where the model says it is "only valid when the stream mode is being updated to on-demand" (KIN-39).* |
| IncreaseStreamRetentionPeriod, DecreaseStreamRetentionPeriod | F | F | *Corrected 2026-10-11: a value equal to the current one is accepted; the model says "Must be more than" and "Must be less than the current retention period" (KIN-36).* |
| AddTagsToStream, RemoveTagsFromStream, ListTagsForStream | F | F | No 50-tag limit; no paging. KIN-8, KIN-6. *Corrected 2026-10-11: no key-length, empty-key, map-length or `TagKeys` length check either (KIN-33).* |
| TagResource, UntagResource, ListTagsForResource | F | F | Same store as above. *Corrected 2026-10-11: a consumer ARN reaches the stream's tags (KIN-34); `TagResource` accepts a `[{Key, Value}]` list the model does not define (KIN-55).* |
| RegisterStreamConsumer, DeregisterStreamConsumer, DescribeStreamConsumer, ListStreamConsumers | F | F | Registration only. `Tags` on register ignored; no paging. KIN-1, KIN-6. *Corrected 2026-10-11: no 20-per-stream limit (KIN-35); status is always ACTIVE where the model has CREATING and DELETING (KIN-47).* |
| PutResourcePolicy, GetResourcePolicy, DeleteResourcePolicy | F | F | Stores anything. KIN-3, KIN-15. *Corrected 2026-10-11: a policy put on a consumer ARN is stored on the stream (KIN-34).* |
| StartStreamEncryption, StopStreamEncryption | C | C | The key is checked to exist and be enabled; nothing is encrypted. KIN-16. *Corrected 2026-10-11: asynchronous on AWS (UPDATING) and limited to 25 changes per rolling 24 hours each (KIN-47, KIN-48); `EncryptionType` NONE is accepted where "The only valid value is KMS" (KIN-44).* |
| EnableEnhancedMonitoring, DisableEnhancedMonitoring | C | C | *Corrected 2026-10-11: `ALL` is stored as a literal name, so one metric cannot be disabled after it (KIN-45).* |
| DescribeLimits | C | C | `OnDemandStreamCount` hard-coded. KIN-14. |
| DescribeAccountSettings, UpdateAccountSettings | C | C | *Corrected 2026-10-11: the answer has members the model does not (`MaxRecordSize`, `TotalStreamCount`, `DefaultStreamModeDetails`) and lacks the one it has (`MinimumThroughputBillingCommitment`); `UpdateAccountSettings` answers `{}` where the model echoes the commitment (KIN-37). Not "no effect": the commitment is the on-demand warm-throughput and 50-consumer switch.* |
| UpdateMaxRecordSize | C | **F (decide)** | ~~Accepted and discarded; the ceiling stays 1 MiB.~~ *Corrected 2026-10-11: the model: "You can ingest and digest single records up to 10240 KiB", the member is 1 024 to 10 240 KiB, and `DescribeStreamSummary` returns it. It is a limit, not a label; discarding it refuses a record AWS accepts. It also answers 200 for a stream that does not exist (`run`). KIN-30.* |
| SubscribeToShard | S | S | Declined: needs an HTTP/2 event stream. In `not-built.md`. *Corrected 2026-10-11: answers 400 `UnsupportedOperationException`, the legacy refusal the model sweep counts as `legacystub`; the agreed form is 501 `NotImplemented` with `X-Doze-Not-Built` (KIN-41).* |
| UpdateStreamWarmThroughput | S | S or C: decide | Declined: a capacity hint. In `not-built.md`. *Corrected 2026-10-11: `model`: "only supported for data streams with the on-demand capacity mode in accounts that have MinimumThroughputBillingCommitment enabled", asynchronous (UPDATING). Same refusal-form finding (KIN-41). Inconsistent with `WarmThroughputMiBps` being accepted on create and mode switch.* |
| CreateChannel, DeleteChannel, DescribeChannel, ListChannels, UpdateChannel | absent | S | Answer `InvalidAction` today. KIN-7. *Model: a channel delivers a stream to S3 or S3 Tables, "only supported for data streams with the on-demand capacity mode", asynchronous (CREATING to ACTIVE), needs an IAM role and a Glue schema; "A stream cannot be deleted while it has active channels" is moot while none can exist. S is right.* |
| UpdateStreamRecordDistributionStrategy | absent | S **or F: decide** | Same. KIN-7. *Model: on-demand only; `AUTO` "distributes records evenly across shards and ignores any partition key and ExplicitHashKey"; `USER_PARTITION_KEY` is the default; "takes effect immediately"; "Attempting to set AUTO on a provisioned stream results in an InvalidArgumentException". It is a routing rule, a few lines in `shardFor`, and `CreateStream` and `DescribeStreamSummary` carry the same member: F is cheaper than S plus a separate refusal. Decision D-K1.* |

Count by tier now: 29 F, 8 C, 2 S, 6 absent. *Verified 2026-10-11.*

Settings need their own tiers, because "the operation is F" hides which
members do anything:

| Setting | Where | Now | Target | To confirm |
|---|---|---|---|---|
| `ShardCount` | CreateStream | F | F | ~~Whether AWS refuses a provisioned create without it: not established (KIN-10).~~ *Corrected 2026-10-11: `model` (CreateStream): "For the data streams with a provisioned mode, you must specify the number of shards for the data stream." Refuse a provisioned create without it. The code AWS answers is `InvalidArgumentException` by the operation's error list; the message is `memory`. And "Create more shards than are authorized for your account" is `LimitExceededException` (KIN-38).* |
| `StreamModeDetails.StreamMode` | CreateStream | F | F | On-demand starts at 4 shards: `memory` (the model does not give a number). |
| `StreamModeDetails.StreamMode` | UpdateStreamMode | stored; gates one call | decide | Nothing rescales an on-demand stream here (KIN-18). `model`: "With the on-demand mode, Kinesis Data Streams automatically manages the shards". |
| `Tags` | CreateStream, RegisterStreamConsumer | dropped | F | KIN-1. `model`: "A set of up to 50 key-value pairs"; `kinesis:AddTagsToStream` is also needed on create and `kinesis:TagResource` on a consumer; "Tags will take effect from the CREATING status of the stream, but you can't make any updates to the tags until the stream is in ACTIVE state". |
| `MaxRecordSizeInKiB` | CreateStream, UpdateMaxRecordSize | dropped | **F** | ~~C, said so; whether it should be echoed in `DescribeStreamSummary`: not established.~~ *Corrected 2026-10-11: `model`: 1 024 to 10 240 KiB, "the maximum record size of a single record ... that you can write to, and read from a stream", and `DescribeStreamSummary.MaxRecordSizeInKiB` returns it. The default is 1 MiB (`GetRecords`: "Each data record can be up to 1 MiB in size by default"). KIN-30.* |
| `WarmThroughputMiBps` | CreateStream, UpdateStreamMode | dropped | C, said so | Inconsistent with `UpdateStreamWarmThroughput` being S. *`model`: valid for on-demand only, in accounts with the commitment enabled; returned as `WarmThroughput {TargetMiBps, CurrentMiBps}`. KIN-39.* |
| `RecordDistributionStrategy` | CreateStream, UpdateStreamRecordDistributionStrategy | dropped, any value accepted | decide (D-K1) | `model`: AUTO or USER_PARTITION_KEY, on-demand only, default USER_PARTITION_KEY; summary returns it "only present for streams that use the on-demand capacity mode". KIN-39. |
| `RetentionPeriodHours` | Increase/Decrease | F | F | 24 to 8760 enforced; swept once a minute. *`model` (Decrease: "The minimum value of a stream's retention period is 24 hours"; Increase: "The maximum value ... is 8760 hours (365 days)"). The model contradicts itself once: `StreamDescription.RetentionPeriodHours` says "Maximum value of 168". Equal values: KIN-36.* |
| `ExplicitHashKey` | PutRecord(s) | F | F | `model`: pattern `^(0\|([1-9]\d{0,38}))$`; "overriding the partition key hash". Under AUTO it is ignored (KIN-39). |
| `SequenceNumberForOrdering` | PutRecord | accepted, unused | F by construction | Sequences are stream-wide and monotonic, so ordering holds without it. *`model`: "Guarantees strictly increasing sequence numbers, for puts from the same client and to the same partition key ... If this parameter is not set, records are coarsely ordered based on arrival time." The model does not say a stale value is refused; the pattern is checked.* |
| `DryRun` | PutRecord(s), GetRecords, GetShardIterator, SubscribeToShard | ignored; the write happens | F | KIN-29. `model`: error `DryRunOperationException`: "The request was rejected because the DryRun parameter was specified." |
| `EncryptionType`, `KeyId` | Start/StopStreamEncryption | C plus a real key check | C | *`model`: "The only valid value is KMS" (KIN-44); `KeyId` may be a key id, a key ARN, an alias ARN, `alias/Name` or `alias/aws/kinesis`; asynchronous, 25 per 24 hours (KIN-47, KIN-48).* |
| `ShardLevelMetrics` | Enable/DisableEnhancedMonitoring | C | C | 1 to 7 names, `ALL` "enables every metric" (KIN-33, KIN-45). |
| `Policy` | PutResourcePolicy | F under soft and enforce; not validated | F | KIN-3. `model`: "It must include the identity of the principal and the actions allowed on this resource." |
| `EnforceConsumerDeletion` | DeleteStream | ignored | F | KIN-2. |
| `ShardFilter`, `MaxResults`, `NextToken` | ListShards | ignored | F | KIN-5, KIN-42. |
| `StreamId` | every stream operation | pattern-checked, then unused | **inert, as AWS** | ~~not established~~ *Corrected 2026-10-11: `model`, on every member: "Not Implemented. Reserved for future use." Nothing to decide; a request that names only `StreamId` is refused for lacking a name or ARN, which is the model's "you must use either the StreamARN or the StreamName parameter, or both".* |
| Stream status | DescribeStream* | always `ACTIVE` | decide | *`model`: CREATING, DELETING, ACTIVE, UPDATING, each with the operations that set it (section 12, L7). Decision D-K2.* |
| Consumer status | consumer operations | always `ACTIVE` | decide | *`model`: CREATING, DELETING, ACTIVE; "A consumer can't read data while in the CREATING or DELETING states". Same decision, D-K2.* |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `kinesis/`: 18 tests. `sdk_test.go` 12 (lifecycle, routing, explicit hash key, PutRecords order, iterator types, AT_TIMESTAMP bounds, split, merge, retention bounds, tags and consumers, validation, empty-shard split), `kms_test.go` 4, one coverage test, one parity test. *Verified 2026-10-11 by count.* |
| Parity | `rejection_parity_test.go` replays `testdata/cases_kinesis.json`; the ledger says 356 of 356 model constraints enforced, `knownGaps` empty. 43 cases are skipped with a reason: GetRecords 11, SplitShard 17, MergeShards 15. *Corrected 2026-10-11: "356 of 356" is cases replayed, not constraints. The file has 399 cases over 35 operations (399 − 43 = 356); `validate.go` has 283 table entries; the model now has 295 constraints on the 37 dispatched operations. The generator and the table omit every map-length, map-key-length and list-length constraint and one enum: at least 14 (`run`: 201 tags, a 129-character tag key, an empty key, 51 `TagKeys`, 8 metric names and an unknown `RecordDistributionStrategy` are all accepted). KIN-33.* |
| Coverage of the model | `coverage_model_test.go` passes only because six operations are listed as `Unreached` |
| Repo-level | `upgrade_test.go` (fixture has one stream), `identity_test.go` (ARN under another account and region), `persistence_surfaces_test.go` (a Logs filter onto a stream) |
| Conformance | `conformance/tests/test_kinesis.py`: 5 scenarios, all **unverified** (`conformance/snapshots/` is empty). *Verified 2026-10-11.* |
| Browser | `e2e/tests/kinesis.spec.ts`: 17 tests. *Verified.* |
| Mutation | No per-package baseline yet (foundation B3) |

There is no package-level restart test and no test of the resource policy in
`kinesis/` itself. *Verified: no `restart` or `policy` test under `kinesis/`.*

## 4. Findings

Evidence `run` means reproduced against a binary built from this tree on
2026-10-10 (rows KIN-1 to KIN-28) or 2026-10-11 (rows marked so and the second
table), through the wire or through the console's own routes.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| KIN-1 | P0 | `CreateStream` with `Tags` succeeds and stores none; `ListTagsForStream` then returns an empty list. `RegisterStreamConsumer` ignores `Tags` the same way. | `kinesis/actions.go` `hCreateStream`, `hRegisterConsumer`; `store.go` `Create` | run (again 2026-10-11) | model (`Tags` is a member of both inputs: "A set of up to 50 key-value pairs") | SDK test: create with two tags, list them; register a consumer with a tag, `ListTagsForResource` on the consumer ARN (KIN-34) |
| KIN-2 | P0 | `DeleteStream` deletes a stream that has a registered consumer, and never reads `EnforceConsumerDeletion`. | `actions.go` `hDeleteStream`; `store.go` `Delete` | run (22 consumers deleted with the stream, 2026-10-11) | model (DeleteStream.EnforceConsumerDeletion: "If this parameter is unset (null) or if you set it to false, and the stream has registered consumers, the call to DeleteStream fails with a ResourceInUseException") *Corrected 2026-10-11: was `doc`* | SDK test: register, delete without the flag, expect the refusal; with the flag, expect success. The console's delete must send the flag after this (foundation A13) |
| KIN-3 | P0 | `PutResourcePolicy` stores any non-empty string. `not json` is saved, the console toasts "Resource policy saved", and the guard skips a document it cannot parse, so under enforce a broken Deny silently allows. | `actions.go` `hPutResourcePolicy`; `guard.go` `guardRequest` (the `iampolicy.Parse` error is dropped) | run (again 2026-10-11: `Put` 200, `Get` returns `not json`) | *Corrected 2026-10-11:* the code is `model` by elimination: the operation's errors are AccessDenied, InvalidArgument, LimitExceeded, ResourceInUse, ResourceNotFound, and `InvalidArgumentException` is "A specified parameter exceeds its restrictions, is not supported, or can't be used"; the message is `memory`. `model` also: "It must include the identity of the principal and the actions allowed" | Handler test: `not json`, valid JSON that is not a policy, a statement with no `Effect`, a statement with no `Principal`, no `Action` |
| KIN-4 | P1 | `MillisBehindLatest` is "now minus the arrival of the last record returned". A reader that has just read the newest record is told it is behind by that record's age (3039 ms three seconds after a put; 8 ms straight after in the 2026-10-11 run), and 0 only when a read returns nothing. | `store.go` `Fetch` (`behind`); `records.go` `hGetRecords` | run | model (GetRecords.MillisBehindLatest: "A value of zero indicates that record processing is caught up, and there are no new records to process at this moment") *Corrected: was `doc`* | SDK test: put, wait, read to the tip, expect 0; read half a backlog, expect a positive number |
| KIN-5 | P1 | `ListShards` ignores `MaxResults`, `NextToken`, `ShardFilter` and `StreamCreationTimestamp`. `MaxResults: 1` returns every shard; `AFTER_SHARD_ID` returns the shard it names; `AT_LATEST` returns closed shards (`run` 2026-10-11). A consumer library that asks for open shards only gets closed ones too. | `records.go` `hListShards` | run | model: each filter type is defined in `ShardFilter.Type` ("AT_LATEST - the response includes only the currently open shards", "FROM_TIMESTAMP - ... all closed shards whose end timestamp is greater than or equal to the given timestamp and also all open shards", "FROM_TRIM_HORIZON - (default) ... all the shards within the retention period"). *Corrected: was `doc`* | SDK test per filter type on a stream that has been split once; paging with `MaxResults: 1`; see KIN-42 for the argument rules |
| KIN-6 | P1 | Paging elsewhere. `ListStreams` returns `HasMoreStreams` and no `NextToken` (run). `DescribeStream` ignores `Limit` and always says `HasMoreShards: false` (read). `ListStreamConsumers` ignores `MaxResults` and `NextToken` (run: 22 returned for `MaxResults: 2`). `ListTagsForStream` ignores `Limit` and `ExclusiveStartTagKey`, `HasMoreTags` always false (run: `Limit: 5` returned 205). | `actions.go` `hListStreams`, `hDescribeStream`, `hListConsumers`, `hListTagsForStream` | run | *Corrected 2026-10-11: model, all of it.* ListStreams: `paginated` NextToken/NextToken, page size `Limit`, default 100, "If you specify a value greater than 100, at most 100 results are returned"; DescribeStream `Limit` default 100, same cap; ListStreamConsumers `MaxResults` default 100, same cap; ListShards `MaxResults` default 1000, same cap; ListTagsForStream `Limit` 1 to 50 and `HasMoreTags`. Tokens: "Tokens expire after 300 seconds ... you get ExpiredNextTokenException". The model's range for these limits is 1 to 10 000 (so 10 001 is a constraint failure) but 101 is not a failure: it returns 100 | SDK paginator test for each, three resources, page size 1; a token held 301 seconds (fake clock) answers `ExpiredNextTokenException`; a limit of 101 returns 100 |
| KIN-7 | P1 | Six model operations have neither a handler nor a named refusal and answer `InvalidAction: unknown Kinesis action`, which a caller cannot tell from a typo. The coverage test records them as `Unreached` instead of failing. | `kinesis.go` `ServeHTTP`, `stubActions`; `coverage_model_test.go` | run (`CreateChannel`, `UpdateStreamRecordDistributionStrategy`, 2026-10-11) | model | Empty the `Unreached` list; the coverage test fails until each is in `stubActions` (and, for `UpdateStreamRecordDistributionStrategy`, until D-K1 is decided) |
| KIN-8 | P1 | A stream accepts more than 50 tags (51 saved from the console in one go; 201 over the wire, 2026-10-11). | `actions.go` `addTags` | run | model (AddTagsToStream: "You can assign up to 50 tags to a data stream"; "You can add up to 50 tags per resource"; the operation's errors include `LimitExceededException`: "The requested resource exceeds the maximum number allowed") *Corrected: was `doc`; the code is `LimitExceededException` by the error list, the message is `memory`.* The limit is per resource, not per request, and the request's own map allows 200 (KIN-33) | Handler test: 51st tag across two calls refused with `LimitExceededException`; replacing an existing key at 50 is accepted ("overwrites any existing tags that correspond to the specified tag keys") |
| KIN-9 | P1 | `UpdateShardCount` accepts any target: 2 to 50 succeeded (and 1 to 50, 50 to 1, 2026-10-11). | `reshard.go` `hUpdateShardCount` | run | model (UpdateShardCount: "By default, you cannot: Scale more than ten times per rolling 24-hour period per stream; Scale up to more than double your current shard count; Scale down below half your current shard count; Scale up to more than 10000 shards in a stream; Scale a stream with more than 10000 shards down unless the result is less than 10000 shards; Scale up to more than the shard limit for your account; Make over 10 TPS") *Corrected: was `doc`.* Errors: `InvalidArgumentException`, `LimitExceededException`, `ResourceInUseException`, `ValidationException` | Parity cases for 2x+1, floor(n/2)−1, 10 001 and the account limit; the ten-per-24-hours and TPS rules are time rules and go behind the strict switch (KIN-48) |
| KIN-10 | P1 | `CreateStream` with no `ShardCount` and no mode makes a one-shard provisioned stream. | `actions.go` `hCreateStream` (`awsjson.Int(p, "ShardCount", 1)`) | run | model (CreateStream: "For the data streams with a provisioned mode, you must specify the number of shards for the data stream") *Corrected: was "doc says the count is required; the answer AWS gives is memory".* The code is `InvalidArgumentException` by the error list; `ValidationException` is also listed (model constraint). Which is `memory`: record | Parity case for no `ShardCount` with no mode, and with `PROVISIONED` |
| KIN-11 | P1 | `SplitShard` and `MergeShards` succeed on an on-demand stream (both `run` 2026-10-11: split closed the parent; merge of two open shards of an on-demand stream answered 200 and made a child). | `reshard.go` `hSplitShard`, `hMergeShards` | run | *Corrected 2026-10-11: model, was `memory`.* "This API is only supported for the data streams with the provisioned capacity mode" on SplitShard, MergeShards and UpdateShardCount, and the shape `ValidationException`: "Specifies that you tried to invoke this API for a data stream with the on-demand capacity mode. This API is only supported for data streams with the provisioned capacity mode." | SDK test: on-demand stream, split and merge each answer `ValidationException` (UpdateShardCount already does) |
| KIN-12 | P1 | `GetShardIterator` with `AT_SEQUENCE_NUMBER` or `AFTER_SEQUENCE_NUMBER` accepts a sequence number far outside the shard's range and hands back an iterator. | `records.go` `hGetShardIterator` | run | `memory` that AWS refuses it. The model gives only the pattern (`^(0\|([1-9]\d{0,128}))$`) and the operation's errors (`InvalidArgumentException`, `ResourceNotFoundException`, `ProvisionedThroughputExceededException`, `DryRunOperationException`, `AccessDeniedException`, `InternalFailureException`): `InvalidArgumentException` is the only candidate | Skipped parity case pending the recording |
| KIN-13 | P1 | A record over 1 MiB is refused with `InvalidArgumentException` and "record 0: data of 1048577 bytes exceeds the 1048576 byte limit", on a single `PutRecord` too, because it shares the batch path. The model table allows `Data` up to 10 MiB, so the model check never fires first. *Corrected 2026-10-11: the limit is compared to `Data` alone; the model counts the key.* | `store.go` `Put`; `validate.go` `PutRecord` | run | *Corrected: was "not established".* model (PutRecord.Data: "When the data blob (the payload before base64-encoding) is added to the partition key size, the total size must not exceed the maximum record size (10 MiB)"; the default is 1 MiB, see KIN-30). The code at the limit is `InvalidArgumentException` (the only fitting code in the errors list); the message is `memory` | Handler test: data of 1 048 576 − 1 plus a 2-byte key is refused with the default limit; exactly at the limit is accepted |
| KIN-14 | P1 | `DescribeLimits` returns `OnDemandStreamCount: 0` whatever exists, while `OpenShardCount` on the same answer is live. The console's Account table then shows "0 / 50" for an on-demand stream. | `actions.go` `hDescribeLimits` | run (again 2026-10-11) | model (`OnDemandStreamCount`: "the number of data streams with the on-demand capacity mode"; `OnDemandStreamCountLimit`; also `ChannelCount` and `ChannelCountLimit`, optional) | Handler test: one on-demand stream, count is 1 |
| KIN-15 | P1 | `GetResourcePolicy` on a stream that exists and has no policy answers `ResourceNotFoundException: Stream t3 … not found`. | `actions.go` `hGetResourcePolicy` | run (again) | not established: what AWS returns for "no policy". *The model adds a hint: the output's `Policy` is `required`, and the `ResourceNotFoundException` text is "The requested resource could not be found. The stream might not be specified correctly" and PutResourcePolicy's: "If you receive a ResourceNotFoundException, check to see if you passed a valid stream or consumer resource", so it is about the resource, not the policy. The message "Stream … not found" for a stream that exists is wrong either way.* | Record first |
| KIN-16 | P1 | `StopStreamEncryption` succeeds on a stream that is not encrypted, and with any `KeyId`. | `actions.go` `hStopEncryption` | run (again) | not established. *Model: `KeyId` and `EncryptionType` are required on Stop; nothing says a mismatch is refused.* | Record first |
| KIN-17 | P1 | The sweep removes a closed shard as soon as it holds no records, with no regard to retention. A split of an empty shard therefore loses its parent within a minute: children name a `ParentShardId` that `ListShards` no longer returns, and an iterator held on the parent answers `ResourceNotFoundException` in place of a null iterator and `ChildShards`. | `store.go` `Sweep` | read; *run 2026-10-11: split an empty shard, 66 s later `ListShards` has two shards and `GetShardIterator` on the parent answers "Shard shardId-000000000000 in stream sw … does not exist"* | *Corrected: model for the implication, `memory` for the timing.* ListShards: "This action does not list expired shards", which implies a closed shard is listed until it expires. The retention period is the only expiry the model names | Store test with a fake clock: split an empty shard, sweep, list |
| KIN-18 | P1 | An on-demand stream is never resized. The mode's whole effect is that `UpdateShardCount` is refused; the ledger calls `UpdateStreamMode` functional. | `reshard.go` `hUpdateShardCount`; grep of `modeOnDemand` | read, run | n/a (a contract decision). `model`: "With the on-demand mode, Kinesis Data Streams automatically manages the shards in order to provide the necessary throughput" | Ledger test once the tier is decided |
| KIN-19 | P2 | Put from the console redirects to Records with `start=latest`. A LATEST iterator starts after the newest record, so the record just written is never shown: "Nothing in this window… showing 0 of 0 read". The comment above the redirect says the opposite is intended. The browser test for put navigates to Records itself, so it never sees this. | `internal/console/handlers_kinesis.go` `kinesisPut` (both redirects) | run (2026-10-10); read again 2026-10-11 | n/a. `model` for LATEST: "Start reading just after the most recent record in the shard" | Browser test: put, follow the redirect, expect the row |
| KIN-20 | P2 | ~~The Arrived column is UTC with no label; the "Up to" filter beside it is labelled "local time" and parsed as local.~~ *Corrected 2026-10-11: stale. `internal/console/timefmt.go` `clock()` formats `t.Local()` and `zoned()` puts the UTC time in the hover; the CHANGELOG records "every displayed time is local and consistent (Kinesis and API Gateway wrote UTC beside local ones)". The column and the filter now agree (`run`: 17:40:21 in the table and "+0530 IST" in the drawer on this machine). What remains: the zone is the server's, not the browser's, and the drawer prints Go's `time.String()` form (KIN-54).* Close with a test under a non-UTC `TZ`. | `client_kinesis.go` `decodeRecord`; `templates/kinesis.html` `kinesis_filters` | read, run | n/a | Browser test under a non-UTC `TZ`: filter by a time read off the column |
| KIN-21 | P2 | Details says "On-demand streams scale themselves; the count is not yours to set". Nothing scales them (KIN-18). Merge and Scale are hidden on an on-demand stream while Split stays on the Shards tab. | `templates/kinesis.html` `kinesis_details`, `kinesis_stream` | read, run | n/a. *`model`: SplitShard is refused on on-demand too (KIN-11), so Split must be hidden as well* | Browser test asserts the wording and that the three controls agree |
| KIN-22 | P2 | Opening the Records tab of a stream whose KMS key is disabled returns a 400 with only the error fragment: no page, no chrome. The Read button's path is fine (the refusal lands in the form). *Corrected 2026-10-11: `run` shows the shell with a page titled "That could not be opened" and the refusal in it (a failed page load is now a page in the shell, per the CHANGELOG), but not the stream page: no tabs, no list pane, no Read form. The board puts the refusal inside Records.* | `handlers_kinesis.go` `kinesisRecords` calling `c.fail` on a full-page GET | run | n/a | Browser test: disable the key, click the tab, expect the page with the refusal in it |
| KIN-23 | P2 | ~~The wiring strip says "nothing feeds this yet" for every stream, always.~~ *Corrected 2026-10-11: the strip no longer says it (`panes.html` `connections` was changed by the foundation: "the upstream side never says 'nothing feeds this' ... a stream is put to"); the real page draws `stream → drains nowhere yet` or `stream → DRAINS TO fn esm`. What remains true: the graph only draws stream-to-function edges; a Logs subscription filter onto a stream is invisible to it, so "Fed by" can never name Logs. The boards (Home, 05) still draw "Fed by nothing feeds this yet" (corrected in the working copy, section 6a).* | `client_flow.go` (no edge into a `kinesis` node); `templates/panes.html` `connections` | read, run | n/a | Console test: a Logs filter onto a stream shows as "Fed by" |
| KIN-24 | P2 | The console cannot do what the API can at create: no mode (on-demand), no tags. It also sends a blank or zero shard count as 1, hiding the refusal. | `templates/kinesis.html` `kinesis_create`; `client_kinesis.go` `CreateStream` | read, run (2026-10-11 render: name and shards only) | n/a | Browser test for an on-demand create once the form has it |
| KIN-25 | P3 | The metrics panel says "There is no CloudWatch here". CloudWatch is local now; what is missing is the producer of `AWS/Kinesis` shard metrics, as the ledger says. | `templates/kinesis.html` `kinesis_details` | read, run (2026-10-11 render shows the sentence) | n/a | Browser test asserts the ledger's sentence |
| KIN-26 | P3 | Stale comments say the resource policy is not evaluated. | `client_kinesis.go` (comment above `ShardMetrics`); `kinesis/actions.go` (comment above `encTypeOf`: "no IAM evaluation until the IAM service lands") | read | n/a | none; corrected with KIN-3 |
| KIN-27 | P3 | The ledger says validation covers "32 of the 35 dispatched operations"; `handlers` has 37 entries. Which number is stale is not established. *Corrected 2026-10-11: established. `constraintTables` has 35 entries; the two dispatched operations without one are `DescribeLimits` and `DescribeAccountSettings`, which take no members. "32 of 35" is stale; the true line is "35 of 37 dispatched, the other two have no input". And "356/356" is replayed cases (KIN-33).* | `docs/SUPPORT.md`, Kinesis "Input validation" | read | n/a | The ledger test, once it counts |
| KIN-28 | P3 | The console's stream list drops a stream whose describe fails, with no sign. Its PutRecords path formats "N refused (code)" for a partial failure the service can never produce. | `client_kinesis.go` `ListStreams`; `handlers_kinesis.go` `kinesisPut` | read | n/a | none; tidy with the console work |

### Found by the pre-stage review, 2026-10-11 (KIN-29 to KIN-55)

Every row below has a direction (L4), a source from the model's own text where
there is one, and what was run. "Test to write first" is the first red test.

| Id | Sev | What | Where | Evidence | Direction | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| KIN-29 | P1 | `DryRun: true` is ignored on `PutRecord`, `PutRecords`, `GetRecords` and `GetShardIterator`: the record is written. | `records.go` (no read of `DryRun`) | run: a `PutRecord` with `DryRun: true` returned 200 with a sequence number and the record was read back | more forgiving, and writes | model: `DryRunOperationException` is in the error list of PutRecord, PutRecords, GetRecords, GetShardIterator and SubscribeToShard: "The request was rejected because the DryRun parameter was specified"; member: "Checks if your request will succeed. DryRun is an optional parameter." The status is 400 (every client error here). | SDK test: `PutRecord` with `DryRun` answers `DryRunOperationException` and no record exists |
| KIN-30 | P1 | `MaxRecordSizeInKiB` is dropped at `CreateStream` and discarded by `UpdateMaxRecordSize`, which also answers 200 for a stream that does not exist and for `StreamName` (a member the operation does not have). `DescribeStreamSummary` never returns it. The ceiling is a constant 1 MiB, so a 5 MiB record, which AWS accepts once the stream's limit is raised, is refused. | `actions.go` `hCreateStream`, `hUpdateMaxRecordSize`, `hDescribeStreamSummary`; `store.go` `maxRecordSize` | run: create with 5 000, summary shows no member; `UpdateMaxRecordSize` 4096 then a 5 MiB put refused; update on a missing ARN answered 200 | stricter (the limit), more forgiving (the missing stream) | model: UpdateMaxRecordSize: "You can ingest and digest single records up to 10240 KiB"; range 1 024 to 10 240 ("If you specify a value that is out of this range, UpdateMaxRecordSize sends back an ValidationException message"); errors include `ResourceNotFoundException` and `ResourceInUseException`; `GetRecords`: "up to 10 MiB in size, but the average throughput for your stream cannot exceed 1 MiB per second" | Store test: raise to 5 120 KiB, put 5 MiB accepted; at 1 024, 1 MiB + 1 refused; summary returns the member; update on a missing stream is `ResourceNotFoundException` |
| KIN-31 | P1 | A `PutRecords` request has no total-size ceiling: eleven 1 MB records (11 MB) were accepted. | `store.go` `Put` | run | more forgiving | model: PutRecords: "Each record in the request can be as large as 10 MiB, up to a limit of 10 MiB for the entire request, including partition keys". *This corrects the document's "the 5 MiB request ceiling" (sections 5 and 8): the model says 10 MiB.* | Handler test: entries totalling 10 MiB + 1 including keys answer `InvalidArgumentException` (the only fitting code in the error list) |
| KIN-32 | P1 | A partition key is held to 256 **bytes**, not 256 characters: a 100-character key of three-byte characters (300 bytes) is refused with `ValidationException ... Member must have length less than or equal to 256`. | the model check (`modelcheck` `KindLength` on `PartitionKey`, bytes); `store.go` `Put` `len(e.PartitionKey) > 256` | run | stricter | model: PutRecord.PartitionKey: "Partition keys are Unicode strings with a maximum length limit of 256 characters for each key" | Handler test: 256 three-byte characters accepted, 257 refused, on `PutRecord` and `PutRecords`; the same for `ExclusiveStartTagKey` and every other `length` check (the checker's unit for strings is the question to settle once, for all services) |
| KIN-33 | P1 | The constraint table lacks 14 model constraints (13 list-length, map-length and map-key-length, and one enum), so `knownGaps` empty is untrue: `Tags` map length 1 to 200 and key length 1 to 128 (AddTagsToStream, CreateStream, RegisterStreamConsumer, TagResource), `TagKeys` 1 to 50 (RemoveTagsFromStream, UntagResource), `ShardLevelMetrics` 1 to 7 (Enable, Disable), `Records` 1 to 500 (PutRecords; enforced by hand, with another code, KIN-46), `CreateStream.RecordDistributionStrategy` enum. | `kinesis/validate.go`; `cmd/dzaudit` (emits them in `list`, not into the table); `testdata/cases_kinesis.json` has none of them | run: 201 tags, a 129-character key, an empty key, an empty map, 51 `TagKeys`, 8 metric names and `RecordDistributionStrategy: BOGUS` all answered 200 | more forgiving | model: traits `length`, and the enum | Regenerate the table and cases; each of the 14 gets a parity case; the replay count in the ledger becomes "cases" not "constraints" |
| KIN-34 | P0 | A consumer ARN is resolved to its stream. `TagResource`, `UntagResource` and `ListTagsForResource` on a consumer ARN read and write the **stream's** tags; `PutResourcePolicy` on a consumer ARN stores the policy on the **stream**, replacing the stream's own (`GetResourcePolicy` on the stream then returns the consumer's document); `RegisterStreamConsumer`'s `Tags` are dropped (KIN-1). | `store.go` `streamFromARN` (cuts at the first `/` after the stream name); `actions.go` `hTagResource`, `hPutResourcePolicy`, `hGetResourcePolicy`, `hDeleteResourcePolicy`, `hListTagsForResource` | run | wrong data | model: `PutResourcePolicy`: "Attaches a resource-based policy to a data stream or registered consumer"; TagResource's `ResourceARN` pattern `...(stream\|channel)/\S+` matches a consumer ARN; RegisterStreamConsumer.Tags: "you must also have the kinesis:TagResource permission for the consumer that will be registered" | Store test: tag a consumer, the stream's tags are unchanged; a policy on a consumer does not change the stream's |
| KIN-35 | P1 | No limit on consumers per stream: 22 registered. | `store.go` `RegisterConsumer` | run | more forgiving | model: RegisterStreamConsumer: "With On-demand Advantage streams, you can register up to 50 consumers per stream ... With On-demand Standard and Provisioned streams, you can register up to 20 consumers per stream"; "you cannot have more than 5 consumers in a CREATING status at the same time. Registering a 6th consumer while there are 5 in a CREATING status results in a LimitExceededException"; errors include `LimitExceededException` | SDK test: the 21st registration answers `LimitExceededException`; decision D-K3 for the 50 |
| KIN-36 | P1 | `IncreaseStreamRetentionPeriod` and `DecreaseStreamRetentionPeriod` to the value the stream already has answer 200. The console, asked for the current value, sends nothing and says "Retention set" (foundation A12). | `actions.go` `setRetention` (`hours < st.RetentionHours` only); `client_kinesis.go` `SetRetention` | run | more forgiving | model: Increase: "Must be more than the current retention period"; Decrease: "Must be less than the current retention period"; `InvalidArgumentException` is the error. *Was `memory` in section 8.* | Handler test: increase to 24 on a 24-hour stream and decrease to 24 on a 24-hour stream answer `InvalidArgumentException` |
| KIN-37 | P1 | `DescribeAccountSettings` answers `MaxRecordSize`, `TotalStreamCount` and `DefaultStreamModeDetails`, none of which is in the model, and never `MinimumThroughputBillingCommitment` ({`Status`, `StartedAt`, `EndedAt`, `EarliestAllowedEndAt`}); `UpdateAccountSettings` answers `{}` where the model echoes the commitment. The console's Account table reads the invented members. | `actions.go` `hDescribeAccountSettings`, `hUpdateAccountSettings`; `client_kinesis.go` `KinesisLimits`; `templates/kinesis.html` | run | wrong shape | model: DescribeAccountSettings output; UpdateAccountSettings: "Updating account settings is a synchronous operation"; the input status enum is ENABLED, DISABLED and the output adds ENABLED_UNTIL_EARLIEST_ALLOWED_END: "Commitments have a minimum duration of 24 hours" (a time rule, KIN-48) | SDK test: update to ENABLED then describe returns `Status: ENABLED` with `StartedAt`; the console's Account table drops the two invented rows |
| KIN-38 | P1 | `CreateStream` accepts 100 001 shards (`run`) while `DescribeLimits` answers `ShardLimit: 10000`, and stores them as one record. | `store.go` `Create`; `actions.go` `hDescribeLimits` | run | more forgiving | model: CreateStream: "You receive a LimitExceededException ... when you try to ... Create more shards than are authorized for your account"; UpdateShardCount: "the default limit for shard count per stream is 10000 per account per region" | Handler test: the shard count above the reported limit answers `LimitExceededException`; one limit constant feeds both |
| KIN-39 | P2 | Members dropped on create and mode switch: `RecordDistributionStrategy` (no enum check, not stored, not echoed, and `AUTO` does not change routing), `WarmThroughputMiBps` (dropped; accepted on a switch to PROVISIONED), and `DescribeStreamSummary`'s `WarmThroughput`, `RecordDistributionStrategy`, `MaxRecordSizeInKiB`, `ChannelCount`, `StreamId` are never returned. | `actions.go` `hCreateStream`, `hUpdateStreamMode`, `hDescribeStreamSummary` | run | wrong shape, more forgiving | model: CreateStream.RecordDistributionStrategy ("only supported for streams that use the on-demand capacity mode"; "Attempting to set AUTO on a provisioned stream results in an InvalidArgumentException" is on the Update operation); UpdateStreamMode.WarmThroughputMiBps ("only valid when the stream mode is being updated to on-demand") | Handler tests per member once D-K1 is decided |
| KIN-40 | P1 | The guard finds the stream from `StreamName`, `StreamARN` and `ResourceARN` only. Under enforce, `DescribeStreamConsumer` by `ConsumerARN` and `GetRecords` with only a `ShardIterator` (the request names no stream) are allowed under a stream-wide Deny. | `guard.go` `guardRequest` | run (`--iam-mode enforce`, 2026-10-11: `PutRecord` 403, `DescribeStreamSummary` 403, `DescribeStreamConsumer` 200, `GetRecords` 200 with a pre-issued iterator and 403 once `StreamARN` is sent) | more forgiving | model: PutResourcePolicy applies to "a data stream or registered consumer"; the iterator names its stream (this code's token) | Guard test: a Deny on the stream refuses `GetRecords` by iterator and `DescribeStreamConsumer`/`DeregisterStreamConsumer` by consumer ARN |
| KIN-41 | P1 | The two refusals (`SubscribeToShard`, `UpdateStreamWarmThroughput`) answer 400 `UnsupportedOperationException` with no `X-Doze-Not-Built`: the model sweep's `legacystub`. The six absent operations will be the same unless built as `awshttp.NotBuilt`. | `kinesis.go` `stubActions`, `ServeHTTP` | run, read (`modelsweep_test.go`) | wrong code and status | protocol step 1 (decision D5): 501 `NotImplemented`, `X-Doze-Not-Built`; `awshttp.NotBuilt` doc: "UnsupportedOperationException is a real AWS code ... a handler written against AWS could mistake 'not built here' for a genuine refusal" | Sweep baseline for `kinesis` has only `handled` and `notbuilt` |
| KIN-42 | P1 | `ListShards` argument rules from the model are absent: a request with only `NextToken` is refused ("either StreamName or StreamARN must be supplied"); `NextToken` with `StreamName`, `StreamCreationTimestamp` or `ExclusiveStartShardId` is accepted; `ShardFilter` `AFTER_SHARD_ID` without `ShardId` and `AT_TIMESTAMP` or `FROM_TIMESTAMP` without `Timestamp` are accepted; `StreamCreationTimestamp` (and `ListStreamConsumers`'s) is ignored; no token expires. | `records.go` `hListShards`, `resolveStream`; `actions.go` `hListConsumers` | run | stricter (NextToken alone), more forgiving (the rest) | model: ListShards: "You cannot specify this parameter if you specify the NextToken parameter" (StreamName, ExclusiveStartShardId, StreamCreationTimestamp); "Don't specify StreamName or StreamCreationTimestamp if you specify NextToken because the latter unambiguously identifies the stream"; "If you specify the AFTER_SHARD_ID type, you must also provide the value for the optional ShardId property"; "If you specify the AT_TIMESTAMP or FROM_TIMESTAMP_ID type, you must also provide the value for the optional Timestamp property"; "Tokens expire after 300 seconds" | SDK/KCL-shaped test: page with `NextToken` alone; each exclusion refused with `InvalidArgumentException` (code `memory`) |
| KIN-43 | P2 | A sequence number is parsed as `uint64` (20 digits). The model's pattern allows 129 digits, so `StartingSequenceNumber: 99999999999999999999999` is refused with "StartingSequenceNumber is required for AT_SEQUENCE_NUMBER", which is also the wrong message. | `iterator.go` `parseSeq`; `records.go` | run | stricter, wrong message | model: pattern `^(0\|([1-9]\d{0,128}))$` on `SequenceNumber`, `StartingSequenceNumber`, `SequenceNumberForOrdering` | Handler test: a 56-digit number is accepted and treated as past the tip |
| KIN-44 | P2 | `StartStreamEncryption` and `StopStreamEncryption` accept `EncryptionType: NONE`; Start with `NONE` stores KMS. | `actions.go` `hStartEncryption` | run | more forgiving | model: "The encryption type to use. The only valid value is KMS" (both operations) | Parity case: `NONE` is `InvalidArgumentException` (code: the error list's only fit) |
| KIN-45 | P2 | `ALL` in `ShardLevelMetrics` is stored as the literal name `ALL`. After enabling `ALL`, disabling `IncomingBytes` leaves `ALL` and the answer lists `ALL` in current and desired. | `actions.go` `setMonitoring` | run | wrong data | model: "The value ALL enables every metric" / "disables every metric"; `CurrentShardLevelMetrics`: "the metrics that are in the enhanced state before the operation". The expanded form AWS returns is `memory` | Handler test: enable ALL, disable one: six remain; the answer names them |
| KIN-46 | P2 | Violations of the same kind answer different codes. Model constraints answer `ValidationException` (`GetRecords` `Limit` 10 001: "1 validation error detected ... Member must have value less than or equal to 10000"), but hand checks answer `InvalidArgumentException` (`PutRecords` 501 records, the record size, `ExplicitHashKey` past 2^128 − 1), and `PutRecords` with an empty list answers `ValidationException` with a message that is not the model's shape. The model's own prose for `GetRecords` is "If you specify a value that is greater than 10,000, GetRecords throws InvalidArgumentException", and `GetRecords`' error list has no `ValidationException` at all. | `store.go` `Put`; `records.go` `hPutRecords`; `modelcheck` | run | wrong code (which is unestablished) | model prose vs the ledger's rule that "AWS rejects a model-constraint violation at the protocol layer" (`memory`) | Record both, then one rule; until then a parity case per operation pinned to the recorded code |
| KIN-47 | P2 | Nothing is asynchronous: a stream is ACTIVE the moment `CreateStream` returns, immediately after a split, a rescale, a key change and a delete; a consumer is ACTIVE at once. A test written against AWS that waits for ACTIVE passes here and a test that does not wait passes here and races there. | `actions.go` (`"StreamStatus": "ACTIVE"`), `store.go` (`Status: "ACTIVE"`) | run | more forgiving | model: section 12, L7, with each quote | Decision D-K2 first; then a status test per operation |
| KIN-48 | P2 | The time- and count-based rules are not modelled: 25 key changes per rolling 24 hours (Start and Stop each), ten rescales per 24 hours per stream, five streams in CREATING, five consumers in CREATING, five calls per second per account on most control operations, the 24-hour commitment minimum. | n/a | read | more forgiving | model (section 12, L8) | One strict-mode switch, off by default, per L8; a test per rule |
| KIN-49 | P3 | A consumer's ARN suffix is unix seconds, so deregister and register of the same name within one second give the same ARN. | `store.go` `RegisterConsumer` | run | more forgiving | model: "If you delete a consumer and then create a new one with the same name, it won't have the same ARN. That's because consumer ARNs contain the creation timestamp." The pattern allows any digits `:[0-9]+` | Store test with a fake clock inside one second |
| KIN-50 | P3 | `DescribeStreamConsumer` and `DeregisterStreamConsumer`: a name and an ARN that disagree are resolved by the ARN with no refusal; a name with no stream ARN, and no name at all, answer `ResourceNotFoundException` "Consumer c1 not found." / "Consumer  not found." (an empty name in the message). | `store.go` `FindConsumer` | run | more forgiving, wrong code | model: "You may also provide all three parameters, as long as they don't conflict with each other" (so a conflict is refused; the code is `memory`, `InvalidArgumentException` by the error list) | Handler test per combination |
| KIN-51 | P3 | An iterator holds a stream name, so an iterator issued for a stream that was deleted and re-created with the same name reads the new stream. | `iterator.go` | run | more forgiving | `memory` (the model says nothing about identity); L6 | Record, then a test |
| KIN-52 | P2 | The account and region in an ARN are ignored: a stream ARN for another account and region describes the local stream; a `StreamName` with a conflicting `StreamARN` is accepted. Hard-coding an account id works here and fails on deploy (the SQS-23 class). | `store.go` `streamFromARN`; `records.go` `resolveStream` | run | more forgiving | `memory` (analogy to SQS-23; the model gives the ARN pattern only) | Identity test under a non-default account: an ARN for the default account is `ResourceNotFoundException` |
| KIN-53 | P1 (pending) | The service answers `application/x-amz-cbor-1.1` with `SerializationException: malformed JSON body`. The model declares `awsJson1_1` only. | `internal/awsjson` | run (an empty CBOR map) | n/a | `memory`: the Java SDK v1 and KCL 1.x are said to send CBOR to Kinesis unless it is disabled. Not fixed on; if recorded true, the wire has a second decoder (L5) | Recording: a KCL 1.x consumer against the endpoint |
| KIN-54 | P3 | The record drawer prints the arrival time as Go's `time.String()` ("2026-10-11 17:40:21.689363968 +0530 IST") where the table prints a clock; the Records table's Shard column wraps `shardId-` and the twelve digits onto two lines at 1440 wide. | `templates/kinesis.html` `kinesis_record_detail`, `kinesis_record_table` | run (renders) | n/a | n/a | Browser test asserts the drawer's time reads like the table's |
| KIN-55 | P3 | `TagResource` accepts `Tags` as a list of `{Key, Value}` as well as the model's map. | `actions.go` `hTagResource` | read | more forgiving | model: `Tags` is a `TagMap` | Parity case: list form is `SerializationException` or `ValidationException` |

**Owned by the foundation, seen here.** Not repeated above.

| Foundation item | The Kinesis instance |
|---|---|
| A2 | The "rule comes from AWS's own service model" line under `ResourceInUseException` for a duplicate stream, under the KMS refusals, and under the hand-written retention and record-size rules (run). *Checked 2026-10-11: the real page now prints the generic "doze-aws refused the request: it breaks a rule of the service. The input is what to change." under every refusal. The boards (04, 07, 17) still carry the model line: the design is wrong there, section 6a.* |
| A5 | Tags are saved one `AddTagsToStream` per key (`client_tags.go`); a refused value leaves earlier keys written. *Run 2026-10-11: a refusal now reads "Nothing was changed. Refused: writing "team"." and the editor reloads the stored tags; the draft is not kept (the board says it is).* |
| A7 | `copy.go`'s Kinesis `Unsel` line never renders: `kinesisStreams` opens the first stream. |
| A11 | With Count above 1 the form calls `PutRecords` and the preview shows `put-record`. |
| A12 | "Retention set" when the value is unchanged and no call is made (`client_kinesis.go` `SetRetention`). "Scaled to N shards" when N is already the count is the same case. *Model: AWS refuses an increase or decrease to the current value (KIN-36); the console hides a refusal the SDK would see.* |
| A13 | All five `hx-confirm` sentences were read against the code and are true today. The delete sentence must stay true after KIN-2: the console will have to pass `EnforceConsumerDeletion` or say why it was refused. |
| Stage 0 criterion | ~~`SUPPORT.md`'s Kinesis policy row says the policy "round-trips only under the default `off`"; the default is `soft`.~~ *Corrected 2026-10-11: the row now reads "evaluated on every request naming the stream under IAM `soft` and `enforce` ... Under `off` it round-trips and nothing evaluates it". Closed.* |

**Moved into this stage from CloudFormation (CFN-2, P0, run).**
`AWS::Kinesis::Stream` is mapped, reports CREATE_COMPLETE and makes no stream
(`internal/cfn/mapper.go`: "Streams have no stack-file section yet ... return
nil", `read`).
It keeps its id and is closed here, because Kinesis is not done while a stack
that declares a stream makes nothing. Acceptance: a stack with one stream leaves
a stream that `DescribeStreamSummary` returns, with the declared shard count,
mode and retention; the plan's README says why it moved.

## 5. What to check

Beyond the common criteria. One protocol, so each line is checked once.
*Corrected 2026-10-11: "one protocol" is the model's `awsJson1_1`; whether a
second wire (CBOR) is spoken by real clients is KIN-53, `memory`.*

**Streams**
- [ ] Name: 1 to 128, `[a-zA-Z0-9_.-]`; refused by the model check with `ValidationException`, never by the older hand rule in `validStreamName`.
- [ ] A duplicate create is `ResourceInUseException`, for the same and for different arguments.
- [ ] Create with `Tags`, with `StreamModeDetails` alone, with `ShardCount` alone, with both, with neither (KIN-1, KIN-10). *Neither, or `PROVISIONED` without a count, is refused (`model`: "you must specify the number of shards").*
- [ ] On-demand create ignores a supplied `ShardCount` and opens 4; establish whether AWS refuses the combination. *Still `memory`; the model does not say.*
- [ ] Shards tile the key space with no gap and no overlap for 1, 2, 3, 7 and 100 shards; the last absorbs the remainder.
- [ ] Delete: missing stream; with consumers, with and without `EnforceConsumerDeletion` (KIN-2); records, shards, consumers and the policy all go.
- [ ] Every operation accepts `StreamARN` in place of `StreamName`; an ARN for another account or region is handled as AWS handles it (not established; KIN-52 asks for a `ResourceNotFoundException`, `memory`). *`model`: "you must use either the StreamARN or the StreamName parameter, or both".*
- [ ] Stream status: decide `ACTIVE`-only or modelled, and write it down (D-K2).
- [ ] The shard limit: a create past `DescribeLimits.ShardLimit` is `LimitExceededException` (KIN-38).

**Writing**
- [ ] Partition key 1 to 256 **characters** ~~: confirm the check counts characters, not bytes, with a multi-byte key~~ *Corrected 2026-10-11: confirmed the wrong way: it counts bytes (KIN-32). Test 256 three-byte characters accepted and 257 refused.*
- [ ] `ExplicitHashKey`: 0, 2^128 − 1, 2^128, negative, non-numeric; it overrides the partition key.
- [ ] Record size: exactly the stream's limit accepted, one byte over refused (KIN-13), with the partition key counted; ~~whether the partition key counts toward the limit is not established~~ *Corrected 2026-10-11: it counts (`model`). The limit is the stream's `MaxRecordSizeInKiB`, 1 024 default, up to 10 240 (KIN-30).*
- [ ] `PutRecords`: 1, 500 and 501 entries; an empty list; ~~the 5 MiB request ceiling (not established here)~~ *Corrected 2026-10-11: 10 MiB for the request, partition keys included (`model`, KIN-31)*; order of results matches order of entries.
- [ ] `DryRun` on each operation that has it writes nothing and answers `DryRunOperationException` (KIN-29).
- [ ] A put to a stream whose only shard covering the key is closed cannot happen; prove it after split, merge and rescale.
- [ ] A put under a disabled, pending-deletion and missing key: three different codes (held by `kms_test.go`; keep). *`model`: `KMSDisabledException`, `KMSInvalidStateException`, `KMSNotFoundException`; the model also has `KMSAccessDeniedException` ("The ciphertext references a key that doesn't exist or that you don't have access to") which may be what a deleted key answers: `memory`.*

**Reading**
- [ ] Each iterator type on an empty shard, a shard with records, and a closed shard. *`model`: "If the shard is closed, GetShardIterator returns a valid iterator for the last sequence number of the shard"; `run` 2026-10-11 agrees (LATEST on a closed shard, then the null iterator and `ChildShards`).*
- [ ] `AT_TIMESTAMP` before the first record, between two, after the last, and in the future. *`model`: "If a record with this exact time stamp does not exist, the iterator returned is for the next (later) record"; "older than the current trim horizon" gives TRIM_HORIZON. The future case is still `memory`.*
- [ ] A sequence number below the trim horizon, above the tip, and from another shard (KIN-12); and one of 56 digits (KIN-43).
- [ ] Iterator expiry at five minutes: `ExpiredIteratorException`, and the message shape. *`model`: "The provided iterator exceeds the maximum age allowed."*
- [ ] A malformed iterator and an iterator for a deleted stream; for a stream deleted and re-created under the same name (KIN-51).
- [ ] `GetRecords` `Limit` 1, 10000, 10001 (code: KIN-46); the 10 MiB response ceiling with large records.
- [ ] `MillisBehindLatest` at the tip, mid-backlog and on an empty read (KIN-4).
- [ ] A drained closed shard returns a null `NextShardIterator` and `ChildShards` naming both parents after a merge. *`model`: ChildShards "returned ... only when the end of the current shard is reached".*
- [ ] The 43 skipped parity cases for GetRecords, SplitShard and MergeShards each run against a stream made for that case.

**Resharding**
- [ ] Split point: at the start, at the end, one inside either edge, outside the range; a closed shard; a one-key shard.
- [ ] Merge: non-adjacent, the same shard twice, either one closed, arguments in either order.
- [ ] `UpdateShardCount`: to the same count (a no-op with no new shards), up, down, the bounds of KIN-9, an on-demand stream, a scaling type other than `UNIFORM_SCALING`.
- [ ] After each reshard, records for one partition key read in order through parent then child.
- [ ] Split and merge on an on-demand stream are `ValidationException` (KIN-11).
- [ ] An empty closed parent survives until retention (KIN-17).

**Retention**
- [ ] 23, 24, 8760, 8761; increase to a lower value and decrease to a higher one are refused; equal values are refused (KIN-36, `model`).
- [ ] After a decrease, records older than the new window go at the next sweep; `TRIM_HORIZON` starts at the oldest survivor. *`model`: "any data already in the stream that is older than 24 hours is inaccessible".*
- [ ] The sweep does not stall on one unreadable stream record.

**Tags**
- [ ] Both tag APIs read and write the same set, for a stream and (separately) for a consumer (KIN-34).
- [ ] 50th accepted, 51st refused with `LimitExceededException` (KIN-8); key 1 to 128, value 0 to 256; the allowed characters. *Corrected 2026-10-11: from the model's text: "Unicode letters, digits, white space, or these symbols: _ . : / = + - @" (TagResource.Tags) and "_ . / = + - % @" (Tag.Key, Tag.Value); the two lists differ on `:` and `%`, so enforce the union and record.*
- [ ] `ListTagsForStream` paging (KIN-6): `Limit` 1 to 50, `ExclusiveStartTagKey`, `HasMoreTags`.
- [ ] `RemoveTagsFromStream` of a key that is not there is ignored (`model`: "If you specify a tag that does not exist, it is ignored").

**Consumers**
- [ ] Duplicate name refused; name pattern; the per-stream limit of 20 (KIN-35, `model`; 50 behind D-K3).
- [ ] Describe and deregister by name and by consumer ARN; a missing consumer; a consumer of a deleted stream; a conflicting name and ARN (KIN-50).
- [ ] `SubscribeToShard` answers 501 `NotImplemented` with the reason (KIN-41).

**Stored settings**
- [ ] Each C setting reads back exactly as set, through `DescribeStream` and `DescribeStreamSummary`.
- [ ] Enhanced monitoring with `ALL`, with a mix, disable of one that is not on (KIN-45).
- [ ] `StartStreamEncryption` on a stream already encrypted, and with an alias, a key id and a key ARN; `EncryptionType` other than KMS (KIN-44).
- [ ] `DescribeLimits` counts on-demand streams (KIN-14); `DescribeAccountSettings` has the model's shape (KIN-37).

**Policy**
- [ ] Stored only if it parses as a policy (KIN-3); empty removes; get with none set (KIN-15).
- [ ] Under enforce: a Deny on `kinesis:PutRecord` refuses the root on `PutRecord` and not on `PutRecords` (run today, keep as a test); a `logs.amazonaws.com` statement admits the Logs writer.
- [ ] Under soft: the same calls succeed and are recorded.
- [ ] The guard sees the stream when it is named by `StreamARN`, `ResourceARN`, a consumer ARN and a shard iterator (KIN-40: the last two are not seen today, `run`).

**Lifecycle**
- [ ] A restart keeps streams, shard lineage, records, the sequence counter, consumers, tags, the policy and every C setting.
- [ ] An iterator issued before a restart still works after it, within its five minutes.

## 6. Console

The design's "Kinesis" page. 18 boards. Templates: `internal/console/templates/kinesis.html`
(which holds its own create block), shared pieces in `panes.html` and
`layout.html`. Handlers: `handlers_kinesis.go`, `handlers_tags.go`; client:
`client_kinesis.go`, `client_tags.go`, `client_flow.go`; routes: `kinesisRoutes`
in `routes_messaging.go` (8 GET, 14 POST). *Verified 2026-10-11: 22 routes.*

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
| Kinesis-16-Denied | Put denied under enforce | `fail_inline`; message reproduced under `--iam-mode enforce` | exists; "The statement that did it" is **new** |
| Kinesis-17-Bad-Days | Disabled key; stream deleted under the page; record aged out | `fail_inline`; `ReadOne`'s own error | exists; case 1 on a tab load is KIN-22 |

There is no "streams, none open" board, on purpose: the route never shows that
state. If foundation A7 decides lists open with nothing selected, one board is
added here and `copy.go`'s `Unsel` line becomes reachable.

**New items, and what each is built from**

| Item | Board | Built from |
|---|---|---|
| Teaching lines under each field | Home, 02, 03, 06, 12 | The settings table (foundation B4): ranges from `kinesis/validate.go`, the retention and record-size bounds from `store.go` constants, tier from the ledger. *Corrected 2026-10-11: the five lines of every setting are listed in section 12, L11, with the model's range, default and enforced-or-stored.* |
| Where keys land | 02, 03 | The shard count typed. A pure function of the form: the even split in `store.go` `tileShards` |
| Try a partition key | 03 | `ShardForKey` (`client_kinesis.go`) over the shards the form would create. Needs MD5 in the browser or one small endpoint; neither exists |
| Where the N land | 06 | `ShardForKey` over `ListShards`, with the `-1`, `-2` suffix `kinesisPut` applies |
| After Put | 06 | The redirect `kinesisPut` already makes, with the existing `5m` start option in place of `latest` |
| The reader check under Retention and Shard count | Home, 12 | The wiring graph's `esm` edge from this stream to a function |
| 2 now → 4 after | 12 | The open shards from `ListShards` and the parent rule in `reshard.go` `hUpdateShardCount` (`parentAt`) |
| What this read did | 08, 10 | The `RecordQuery` that ran and the per-shard counts `readOneShard` already returns |
| What a split did | 05 | Static text; each sentence checked in section 5 (reshard) |
| Why it read one shard | 10 | `ShardForKey` and the share of the key space |
| The statement that did it | 16 | `GetResourcePolicy` for the stream and the Sid in the denial message (`(DenyConsolePuts)`) |
| What each Start at does | 11 | `startAt.iteratorInput`, static |

**Where the teaching lines' facts come from**

*Corrected 2026-10-11: sources re-tagged against the model.*

| Line | Source |
|---|---|
| Stream name 1 to 128, the character set | model (`CreateStream.StreamName`) |
| Shards 1 and up | model (`ShardCount` range min 1, no maximum in the trait). *Also, from the prose: not more than the account's shard limit (`LimitExceededException`) and 10 000 per stream (KIN-38).* |
| "A partition key is hashed to exactly one shard… order holds per key" | model (PutRecord.PartitionKey: "all data records with the same partition key map to the same shard within the stream"). *Not for `PutRecords`: "PutRecords doesn't guarantee the ordering of records".* |
| "AWS gives a shard 1 MiB/s or 1 000 records/s of writes" | **model** (CreateStream: "Each shard can support writes up to 1,000 records per second, up to a maximum data write total of 1 MiB per second"; PutRecords agrees). *Corrected 2026-10-11: was `doc` (quotas page). The model contradicts itself in PutRecord ("10 MiB per second"); two of three say 1 MiB. Reads: "up to five transactions per second ... 2 MiB per second".* |
| "doze-aws does not throttle" | ledger, "Differences from AWS" |
| "Created PROVISIONED"; "a stream created on-demand starts with 4" | code (`store.go` `Create`), ledger. *The 4 is `memory` for AWS.* |
| Partition key 1 to 256 | model (characters; KIN-32) |
| "Left empty, the console sends `console`" | code (`kinesisPut`) |
| Data up to 1 048 576 bytes | ~~code (`maxRecordSize`), ledger; AWS side is KIN-13~~ *Corrected: model: the default limit, with the key counted, raisable to 10 240 KiB per stream (KIN-13, KIN-30).* |
| Retention 24 to 8 760, default 24, swept once a minute | model (range, default: "By default, data records are accessible for 24 hours"); code and ledger for the sweep |
| "Reading does not remove it" | `copy.go`; model (retention: "the length of time data records are accessible after they are added") |
| "Raising calls Increase…, lowering Decrease…" | code (`SetRetention`) |
| Target shards, UNIFORM_SCALING only | model (enum), code. *And the bounds of KIN-9, which the board lacks.* |
| "Each new shard names the old one that owned its first hash key" | code (`parentAt`); the ledger calls it an approximation of AWS's plan |
| Mode sentences | code; rewritten once KIN-18 is decided. *Model: on-demand refuses Split, Merge and UpdateShardCount.* |
| Encryption "recorded, not applied", key checked | ledger (tier C and "Differences") |
| Metrics "nothing produces the AWS/Kinesis shard metrics" | ledger (KIN-25 is the template disagreeing) |
| Consumers: registration real, `SubscribeToShard` refused, with the message | code (`stubActions`), ledger. *The message changes with KIN-41.* |
| Policy under soft and enforce; the `logs.amazonaws.com` statement | ledger |
| Tag value 0 to 256 | model. *Also key 1 to 128, at most 50 per stream, the character set (section 5).* |
| What each Start-at does | model (GetShardIterator: TRIM_HORIZON, LATEST, AT_TIMESTAMP, each described); code (`startAt.iteratorInput`, `windows`) |

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
  today, and exactly what KIN-2 changes: the console must send
  `EnforceConsumerDeletion`.
- 17: "a reader that polls the stream" is stopped by a disabled key. Put and
  the console's read are run; the Lambda poller is read.
- 13: the consumer ARN's numeric suffix and the KMS key id are sample data.
- 08, 10: "These calls are not on the wire" rests on the foundation's statement
  that the wire shows the app's traffic only.
- *Added 2026-10-11:* 05, 12, 15: "the parent stays readable until retention
  reclaims it" is false today (KIN-17, `run`); it is the target.
- *Added:* 07: the split-closed-shard refusal "shard … is already closed"
  (`InvalidArgumentException`) is the code's own; the code AWS answers for a
  closed shard is `memory`.
- *Added:* 03: "Try a partition key" names a percentage of the key space; its
  figure is computed from MD5 in the page, which has no MD5 yet.

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering every board of the page group
(`console-design/project/Kinesis-*.dc.html`, 1440 by 900, light; Home, 02/03,
08 and 12 also dark) and the real page in the same state (Playwright through the
console on a fresh instance, the same data as the boards: three streams of
2, 4 and 1 shards, nine records, a consumer, a policy, two tags, a Lambda
mapping), and looking at both. Dark was looked at on Home and on the create page (08 and 12 were rendered
in dark and not inspected separately): the real page renders correctly in dark
and the differences are the same in both themes. At 900 wide the real stream page renders with the shell's
"built for 1024 px" banner and stays usable (the shard id wraps onto two
lines in the table). The real shell (rail with names, list pane header, the
foundation's top bar) is the foundation's, not Kinesis's: the boards use the
rail marks and the "Streams / New" header, and that is not repeated per row.
The service icon (`internal/console/static/aws/kinesis.svg`) is in the rail, in
the resource header and on the empty state in the real page, where a board
draws "KN": kept.

The board's "Other refusals ..." and "If the page was stale" blocks are
annotations of what a form can come back with, as in the SQS table; they are
not page content and are not counted as differences.

| Board | Verdict | What differs |
|---|---|---|
| Home (Shards tab) | **differs** | Shape matches (header with ARN chip and Delete, tabs, wiring strip, shard table with the same columns, Put and Retention side by side). Content: Retention has no unit echo ("1 day"), no line 4 ("24 – 8 760 · default 24 · enforced · swept once a minute"), no reader callout ("Reader: harbour-stock-forecast ... fall behind by more than 24 h") and no "Raising it calls IncreaseStreamRetentionPeriod…" sentence; Put record has no teaching lines; the note "Depth is a read, capped at 1 000" is missing; the shard table's Split buttons carry an icon. **The design was wrong** in drawing "Fed by nothing feeds this yet" (the foundation's decision: the graph cannot see producers); corrected in the working copy. |
| 01 None | **matches** | The "c also starts a new stream" line under the button is the shell's footer hint in the real page. |
| 02 Create | **differs** | The design is a two-column page with a subtitle ("Shard count, retention and capacity mode can all be changed afterwards"), a General card (name, "1 – 128 characters · enforced"), a Capacity card (shards with "2 shards, each owning half of the key space", the paragraph on what a shard is, "1 and up · the form starts at 1 · enforced", the 1 MiB/s sentence, "The stream is created PROVISIONED. On-demand is a switch under Details."), a right-hand "Where keys land" panel (**new**) and a Cancel button. The real page is one 520 px card: name, shards, one sentence, Create stream. |
| 03 Create, four shards | **differs** | As 02, plus "Try a partition key" (**new**, needs MD5 in the browser or an endpoint) and the Lambda sentence (not backed, section 6). |
| 04 Create refused | **differs deliberately / differs** | The refusal is inline in the card under the button in the real page and under the name field in the board; the real footer is the foundation's generic line (A2), the board's "the rule comes from AWS's own service model" is the old text: **design wrong** for the duplicate (a duplicate is the service's state, not a model rule). The typed values stay. The form has no General and Capacity cards (as 02). |
| 05 Resharded | **differs** | Closed parent with a chip, children with "split of …", Split buttons only on open shards: matches. Missing: the "What a split did" panel (**new**, static) and the note "Closed shards are dimmed and sit above the children that replaced them". The board's "stays readable until retention has reclaimed it" is the target (KIN-17), false today. |
| 06 Put | **differs** | The design is two columns: the form with "11 characters", "1 – 256 characters · enforced · left empty, the console sends console", "up to 1 048 576 bytes with the key · enforced", the CLI call open, and on the right "Where the 5 land" (**new**) and "After Put" (**new**, depends on KIN-19). The real page: the form without any of those lines, beside Retention, the CLI behind a button. The design draws the shard table without header or Split buttons here and with them on Home: an inconsistency in the design, not built. |
| 07 Put refused | **differs** | The inline refusal under the form matches (message, code, vocabulary). Retention in the board has the unit echo ("375 days") and line 4; the real has neither. The board's footer line is the old model sentence (A2). |
| 08 Records | **differs** (nearly matches) | Filter bar, note, table, columns and footer match, word for word. Missing: "What this read did" (**new**). The real Shard column wraps the id onto two lines (KIN-54). |
| 09 Record full | **differs** | Right-hand drawer: matches. Real kv table prints "arrived" as Go's time string and has no "esc" label, no "Shown as the bytes were put…" sentence, and the page behind has no "A listing shows the first 400 bytes…" note. |
| 10 Records routed | **differs** | Routed footer ("showing 2 of 4 read · routed to shardId-… by partition key") matches; "Why it read one shard" and "What this read did" (**new**) are missing. The real "Up to" is a datetime-local input. |
| 11 Records empty | **differs** | Each of the three states exists and reads the same ("Nothing in this window…", "Nothing matched in the N records read…", "following — new records appear here"). The right-hand "What each Start at does" table and "Follow polls every 2 seconds" panel (**new**, static) are missing. |
| 12 Details, layout | **differs** | The design is two columns: Shard count (with line 4, the reader callout, "2 now → 4 after" **new**, Apply) and Merge shards on the left, Capacity mode, "On an on-demand stream" and the refusals on the right, and a link to "Encryption, metrics, … further down". The real page: Merge and Shard count side by side, Capacity mode full width, no teaching lines, no callout, no preview. **The design's line 4 for the shard count was incomplete** ("1 and up · UNIFORM_SCALING only"): the model also bounds it to at most double and at least half (KIN-9); corrected in the working copy. |
| 13 Details, stored | **differs** | Design: two columns (Encryption, Metrics, Account | Consumers, Policy). Real: one column in the order Encryption, Metrics, Consumers, Policy, Account. The policy is the foundation's builder with a JSON tab in the real page and a raw box in the board (**differs deliberately**, shared with SQS and SNS). Missing: "ledger tier C · cosmetic" under Encryption. **The design was wrong** on the Account table: "max record size" and "default stream mode" are not model members (KIN-37), and "UpdateMaxRecordSize … discarded" is the bug KIN-30; corrected in the working copy. The SubscribeToShard message on the board is the old refusal (KIN-41); corrected to the agreed form. |
| 14 Tags | **differs** | Foundation A5 shape (rows, Add tag, Save changes): matches. Missing: the "What a tag is here" panel with line 4. The real refusal appears above the editor ("Nothing was changed. Refused: writing "team".") and the editor reloads the stored tags; the board keeps the draft on the page. |
| 15 Confirms | **differs** | The real confirms are a title and one sentence ("Delete stream “x”?" / "Its records go with it.") with Cancel and the verb. The board has bodies with counts and consequences ("the 9 records, the shards and the 1 registered consumer …", "Anything putting to it afterwards gets ResourceNotFoundException"), and "Keep it" buttons. The delete sentence must send `EnforceConsumerDeletion` (A13). "What does not ask" is annotation. |
| 16 Denied | **differs** | The "Denied AccessDeniedException" callout with the real message and "A policy decided this, not the input… The fix is the policy. Access log →" appears below the form in the real page, above the fields in the board, in the same words. Missing: "The statement that did it" panel (**new**) and the closing paragraph about soft mode and batches. |
| 17 Bad days | **differs** (1); matches (2); not reproducible in one step (3) | Case 1 (disabled key): the board draws the refusal inside Records; the real tab load is a full page "That could not be opened" (KIN-22). The Read button path lands in the form. Case 2 (stream deleted): the refusal is inline under the Put form: matches. Case 3 (record aged out): reached by URL it is a page; through the drawer it is a fragment: not rendered. |

**Where the design is wrong** (corrected in the working copy on 2026-10-11 in
Kinesis-Home, -05, -06, -07, -12, -13 and -14; the published canvas still
carries the old text until it is republished)
- Home and 05: "Fed by nothing feeds this yet". The strip cannot know producers
  (foundation, `panes.html`); it draws only the feeds it knows. The segment is
  removed.
- 06 and 07: "up to 1 048 576 bytes · enforced" and "One byte over 1 MiB": the
  model counts the partition key in the same limit, and the limit is the
  stream's (1 024 KiB by default, 10 240 at most). Now "with the key" and "the
  1 MiB default; the partition key counts toward it too".
- 07: "Applying the number it already has is not refused": AWS refuses an
  increase or a decrease to the current value (`model`, KIN-36). Now says the
  console sends nothing and AWS would refuse.
- 12: shard-count line 4 omitted the double and half bounds (`model`); "A stream
  created on-demand through the API starts with 4" is `memory` for AWS, now "here";
  the on-demand note said only Merge and Scale are hidden, and `model` says
  Split is refused too.
- 13: the Account rows "max record size" and "default stream mode" are not
  members of `DescribeLimits` or `DescribeAccountSettings`; replaced by the
  model's `MinimumThroughputBillingCommitment`. "UpdateMaxRecordSize … discarded"
  is the bug, not the design. The `SubscribeToShard` message is the legacy one.
- 14: line 4 gave only the value's range; the model gives key 1 to 128 and at
  most 50 tags per stream.
- 04, 07, 17: "doze-aws refuses this because AWS does — the rule comes from AWS's
  own service model, so the same call would have failed on deploy" appears under
  refusals that are not model constraints (a duplicate stream, the retention
  range, a disabled key). The foundation replaced it with a generic line (A2).
  Not edited here (it is in four boards and the footer is the foundation's);
  the integrator removes it from the three.
- 03, 12: "will poll each of the four shards on its own, and follow the new shards"
  is the ledger's claim, not run end to end (section 6, not backed).

**Build list, in order** (nothing built yet)
1. The five lines on every setting (section 12, L11), from the settings record's
   tier (kit `setting_field`): Home (Retention, Put), 02, 03, 06, 12, 14.
2. The create page as the board draws it: subtitle, General and Capacity cards,
   Cancel, "Where keys land" (pure function of the form). Add the mode and tags
   fields (KIN-24) with the same cards.
3. The right-hand panels that are static or built from data already read: "What
   a split did", "What each Start at does", "Follow polls every 2 seconds",
   "What a tag is here", "The statement that did it" (needs `GetResourcePolicy`).
4. The Put page: character count, "Where the 5 land" and "After Put" (with KIN-19:
   redirect to Records with the window the board names), the CLI call open.
5. The Details page as two columns in the board's order; the reader callout and
   "2 now → 4 after" (needs the `esm` edge and the `parentAt` plan as a function);
   on-demand: hide Split, Merge and Scale together (KIN-21).
6. Confirms with bodies and the board's verbs, from the same counts the page
   has (records, shards, consumers); the delete confirm sends
   `EnforceConsumerDeletion`.
7. "What this read did" and "Why it read one shard" from the `RecordQuery` that
   ran; the drawer's note and its "arrived" in the table's form (KIN-54).
8. The Records tab load with a refusal as a page with the Read form (KIN-22).
9. "Try a partition key" (03): MD5 in the browser, or one small endpoint
   returning a shard for a key and a shard count; the route gate and
   `testdata/routes.txt` move with it. This is the one item that needs
   something that does not exist.
10. Re-render every board against its page at 1440 in both themes and at 900,
    and replace "differs" with "matches" in this table with the page in hand.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | Tags at create (KIN-1); delete with consumers (KIN-2); policy validation (KIN-3); `MillisBehindLatest` with a fake clock (KIN-4); sweep and an empty closed shard (KIN-17); on-demand count in limits (KIN-14); tag limit (KIN-8); `DryRun` (KIN-29); record size with the key and per-stream limit (KIN-13, KIN-30, KIN-31); a three-byte-character key (KIN-32); a consumer's tags and policy apart from the stream's (KIN-34); consumer limit (KIN-35); retention equal (KIN-36); account settings shape (KIN-37); shard limit (KIN-38); `ALL` metrics (KIN-45) |
| SDK v2 | `ListShards` filters and paging, `NextToken` alone, expiry (KIN-5, KIN-42); paginators for streams, consumers, tags, `DescribeStream` (KIN-6); `UpdateShardCount` bounds (KIN-9); per-key order across split, merge and rescale in one test; on-demand split and merge (KIN-11) |
| Coverage | `Unreached` emptied (KIN-7); sweep baseline has only `handled` and `notbuilt` for kinesis (KIN-41) |
| Rejection parity | The 43 skipped cases, each on its own stream; the 14 missing constraints regenerated (KIN-33); skipped-pending-recording cases for KIN-12, KIN-15, KIN-16, KIN-46, KIN-50 |
| Guard | Deny and Allow under enforce and soft; the ways a request names a stream (`StreamARN`, `ResourceARN`, consumer ARN, iterator: KIN-40); an unparseable stored document from before the fix |
| Restart | Everything in section 5 "Lifecycle" through the foundation's helper (B5); the status settle (D-K2) |
| Identity | The package suite under a non-default account and region (B2), including consumer ARNs (KIN-52) |
| Conformance (boto3) | Every row of section 8; a consumer loop that walks a reshard the way the KCL does; a paginator over shards; `DryRun`; a 5 MiB record on a raised stream |
| Browser | Put then the redirect (KIN-19); time filter under a non-UTC zone (KIN-20, now a regression test); on-demand wording and controls (KIN-21); Records tab with a disabled key (KIN-22); "Fed by" with a Logs filter (KIN-23); on-demand and tags at create (KIN-24); each `new` item; each refusal on boards 04, 07, 12, 13, 14; the denied put under enforce; the five confirms' sentences and bodies; the fidelity table's "matches" rows rendered again |
| Mutation | `kinesis/` baseline, then survivors in `reshard.go`, `store.go` (`shardFor`, `Fetch`, `Sweep`), `iterator.go` |

## 8. Recording queue

Every one of these is `model` or `memory` today (no `doc` page was read); a row
says which. The review moved eighteen of the 25 original claims from `memory` or
`doc` to `model` (wholly, or for the rule), and added fourteen rows, six of them
wholly `memory`.

| Claim | Source now | Scenario |
|---|---|---|
| Delete with registered consumers and no `EnforceConsumerDeletion` is `ResourceInUseException` | model *(was doc)* | `test_kinesis::delete_with_consumers` |
| A resource policy that does not parse, or is not a policy, is refused; the code is `InvalidArgumentException` | model for the code (error list), memory for the message *(was memory)* | `invalid_resource_policy` |
| `GetResourcePolicy` with none set | memory | `get_policy_when_none` |
| `MillisBehindLatest` is 0 at the tip and positive behind it | model *(was doc)* | `millis_behind_latest` |
| Each `ShardFilter` type's result set after a split | model *(was doc)* | `list_shards_filters` |
| `ListStreams`, `ListShards`, `ListStreamConsumers`, `ListTagsForStream` tokens and limits; a token after 300 seconds | model *(was doc)* | `paging` |
| The 51st tag; the code `LimitExceededException` | model *(was doc)* | `tag_limit` |
| `UpdateShardCount` bounds and their codes | model for the rule, code by the error list *(was doc)* | `update_shard_count_bounds` |
| Provisioned create with no `ShardCount` | model *(was "doc, answer memory")*; the message memory | `create_without_shard_count` |
| On-demand create with a `ShardCount` | memory | `create_on_demand_with_count` |
| Split and merge on an on-demand stream are `ValidationException` | model *(was memory)* | `reshard_on_demand` |
| What a mode switch does to the existing shards | memory | `mode_switch_shards` |
| A sequence number outside the shard at `GetShardIterator` | memory | `iterator_bad_sequence` |
| `AT_TIMESTAMP` in the future | memory | `iterator_future_timestamp` |
| A record one byte over the limit: the code and message; the key counts toward the limit (model) | model for the key, memory for the message | `record_too_large` |
| `PutRecords` 501 entries (model) and the 10 MiB request ceiling including keys (model; was "5 MiB, doc") | model | `put_records_limits` |
| `StopStreamEncryption` on an unencrypted stream, and with the wrong key | memory | `stop_encryption_mismatch` |
| A closed shard stays listed for the retention period | model for "does not list expired shards", memory for the timing *(was doc)* | `closed_shard_lifetime` (slow: needs a day; may stay `memory`) |
| Increase or decrease retention to the current value is refused | model *(was memory)* | `retention_same_value` |
| An expired iterator's code (model) and message (memory) | model, memory *(was doc)* | `expired_iterator` (needs five minutes) |
| A request naming only `StreamId` | model: reserved, inert *(was memory)* | `stream_id_only` |
| An unknown or unsupported action: the code AWS-facing SDKs expect for S rows | memory | shared with the foundation |
| Consumer limit per stream (20, 50 with the commitment) and the code `LimitExceededException`; five in CREATING | model *(was doc)* | `consumer_limit` |
| Per-shard write limits (1 MiB/s, 1 000 records/s) quoted on boards 02 | model *(was doc)* | none: a citation |
| Stream and consumer status transitions, per operation | model *(was doc)* | `status_transitions` |
| `DryRun` answers `DryRunOperationException` and writes nothing | model | `dry_run` |
| A record of 5 MiB on a stream raised to 5 120 KiB; refused above the stream's limit | model | `max_record_size` |
| A consumer ARN's tags and policy are the consumer's own | model | `consumer_tags_and_policy` |
| `DescribeAccountSettings` and `UpdateAccountSettings` shape and the commitment's 24 hours | model | `account_settings` |
| `ListShards` with `NextToken` alone; each exclusion's code | model for the rule, memory for the code | `list_shards_next_token` |
| `ALL` in enhanced monitoring, then disabling one | model for the rule, memory for the expanded list | `enhanced_monitoring_all` |
| `EncryptionType` other than KMS | model for the rule, memory for the code | `encryption_type_none` |
| A model-constraint violation: `ValidationException` or `InvalidArgumentException` (GetRecords `Limit` 10 001, PutRecords 501) | memory (the ledger's rule) against model prose | `constraint_violation_code` |
| `application/x-amz-cbor-1.1` from a KCL 1.x or Java SDK v1 client | memory | `cbor_wire` |
| An ARN for another account or region; a name and a conflicting ARN | memory | `foreign_arn` |
| An iterator after the stream was deleted and re-created | memory | `iterator_recreated_stream` |
| A closed shard split again: the code | memory | `split_closed_shard` |
| A missing key on a put: `KMSNotFoundException` or `KMSAccessDeniedException` | memory | `put_deleted_key` |
| 25 key changes in 24 hours; ten rescales in 24 hours | model | `strict_limits` (behind the strict switch) |

## 9. Owed to integration

| What | With |
|---|---|
| An event source mapping honours `StartingPosition`; today the poller opens every shard at `TRIM_HORIZON` | Lambda |
| A mapping follows a split, a merge and a rescale without a restart, and keeps per-key order | Lambda |
| A mapping on a stream whose key is disabled: what the function and the mapping's state show | Lambda, KMS |
| A mapping on a deleted stream stops polling | Lambda |
| A mapping reads a stream that is not ACTIVE (CREATING, UPDATING, DELETING) as the model says reads and writes continue | Lambda (after D-K2) |
| A Logs subscription filter delivers one gzip record per batch; the stream must exist when the filter is made (the overview lists this as not checked) | CloudWatch Logs |
| Under enforce a Logs filter needs the `logs.amazonaws.com` statement; without it delivery is denied and visible | CloudWatch Logs, IAM |
| The wiring graph draws Logs → stream so "Fed by" can be true (KIN-23) | CloudWatch Logs, console |
| Key states: disabled, pending deletion, deleted, re-enabled, each seen by a producer and a consumer | KMS |
| `AWS::Kinesis::Stream` through CloudFormation: which properties are applied (mode, retention, tags, encryption) is not established | CloudFormation |
| Identity policies on `kinesis:*` actions, and the resource policy combined with them, for a non-root caller; `kinesis:AddTagsToStream` needed to tag at create, `kinesis:TagResource` for a consumer (`model`) | IAM |
| A KCL application: lease table in DynamoDB, checkpoints, a reshard mid-run; and whether a KCL 1.x speaks CBOR (KIN-53) | DynamoDB |
| Whether EventBridge or API Gateway can target a stream here | not established |
| Retention sweep under load, and a restart mid-sweep | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] KIN-1 to **KIN-55** each closed, with the test that failed first named beside it, or moved with a reason. *Corrected 2026-10-11: was KIN-28.*
- [ ] All 45 operations have a tier in `SUPPORT.md`; `coverage_model_test.go` has an empty `Unreached` list; the new S rows are in `not-built.md`; the two existing S rows answer 501 `NotImplemented` with `X-Doze-Not-Built` (KIN-41).
- [ ] Decided and written into `SUPPORT.md`: the tier of `UpdateStreamMode` and what on-demand means here (KIN-18); which `UpdateShardCount` bounds are modelled (KIN-9); stream and consumer status (D-K2); the consumer limit (D-K3); whether a provisioned create needs `ShardCount` (KIN-10, now `model`: yes); the tier of `UpdateMaxRecordSize` (F, KIN-30) and of `UpdateStreamRecordDistributionStrategy` (D-K1).
- [ ] The settings table in section 2 has no "not established", "decide" or "dropped" left: `MaxRecordSizeInKiB`, `WarmThroughputMiBps`, `RecordDistributionStrategy`, `SequenceNumberForOrdering` each have a tier and a sentence (`StreamId` is inert, `model`).
- [ ] Every box in section 5 ticked, or moved to the recording queue with its test written and skipped.
- [ ] Every list operation pages, with tokens that expire at 300 seconds; `ListShards` honours every filter type and every argument rule (KIN-42).
- [ ] The 43 skipped parity cases run, or each has a reason that is still true; the 14 missing constraints are generated and replayed (KIN-33).
- [ ] A package-level restart test exists and covers lineage, the sequence counter, consumers and the policy.
- [ ] KIN-27 resolved: the ledger's operation counts are produced by the ledger test, not typed.
- [ ] **The design-fidelity table (6a) exists, was made from rendered boards beside rendered pages, and no row says "differs" without a decision beside it (built, or deliberate with its reason); the "design is wrong" corrections are in the canvas.**
- [ ] **Icons kept**: the service's icon from `internal/console/static/aws/kinesis.svg` in the rail, the list pane and the resource header; a board's "KN" is a placeholder.
- [ ] **The five teaching lines on every setting** listed in section 12 (L11), line 4 written from the settings record and the ledger, line 5 live with real numbers.
- [ ] **One protocol per operation**: every one of the 37 dispatched operations has an accepted-as-the-reference-spells-it test on `awsJson1_1`; whether a second wire is owed (KIN-53) is recorded.
- [ ] **Each asynchronous operation decided** (L7, section 12): stream create, delete, split, merge, rescale, key start and stop, warm throughput, consumer register and deregister, each either modelled with a visible short delay or immediate with the difference in "Differences from AWS".
- [ ] **Each time- or count-based rule decided and behind one switch** (L8): 25 key changes per 24 hours, ten rescales per 24 hours, five in CREATING, the per-second limits; off by default, in memory.
- [ ] All 18 boards implemented from the foundation's shell with no Kinesis-only chrome, and each `new` item reads only the data named for it.
- [ ] No statement on a Kinesis screen is in the "not yet backed" list in section 6.
- [ ] After a put the user sees the record they put (KIN-19); every time on a Kinesis page says its zone, and the filter and the column agree (KIN-20, closed; test it).
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
| 1 | Contract: tiers for 45 operations, the settings table, the decisions in section 10 and D-K1 to D-K3 | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for KIN-1 to KIN-18 and KIN-29 to KIN-55 | `kinesis/sdk_test.go`, `kinesis/rejection_parity_test.go`, `kinesis/coverage_model_test.go`, new `kinesis/store_test.go`, `kinesis/guard_test.go`, `kinesis/restart_test.go`, `kinesis/dryrun_test.go`, `kinesis/paging_test.go` |
| 2a | Regenerate the constraint table and cases (list, map and key lengths; the enum): `dzaudit` and `validate.go` | `kinesis/validate.go`, `kinesis/testdata/cases_kinesis.json`, `cmd/dzaudit` if the generator drops them |
| 3 | Silent drops and wrong answers: tags at create, delete with consumers, policy validation, `MillisBehindLatest`, on-demand count, `DryRun`, record size and the stream limit, consumer tags and policy, account settings | `kinesis/actions.go`, `kinesis/store.go`, `kinesis/records.go`, `kinesis/guard.go` |
| 4 | Listing: `ListShards` filters, tokens and rules, the other three pagers (use `internal/awspage`, plus a 300-second expiry in the token) | `kinesis/records.go`, `kinesis/actions.go`, `kinesis/store.go` |
| 5 | Named refusals for the six absent operations and the form of the two existing ones | `kinesis/kinesis.go` (`stubActions` through `awshttp.NotBuilt`) |
| 6 | Reshard rules: bounds, mode checks, the sweep and empty closed shards | `kinesis/reshard.go`, `kinesis/store.go` (`Sweep`) |
| 6a | Status model and the strict switch (if decided): CREATING, UPDATING, DELETING, consumer CREATING; limits per 24 hours | `kinesis/store.go`, `kinesis/kinesis.go` (`Options`), `internal/config`, `docs/SUPPORT.md` |
| 7 | Section 5 sweep; the 43 skipped cases; restart and identity passes | `kinesis/rejection_parity_test.go`, `kinesis/testdata/cases_kinesis.json`, `kinesis/sdk_test.go` |
| 8 | Conformance scenarios and claims table | `conformance/tests/test_kinesis.py`, the claims file from foundation B1 |
| 9 | Console fixes: redirect, on-demand wording and controls, tab-load failure, create form, account table | `internal/console/handlers_kinesis.go`, `client_kinesis.go`, `templates/kinesis.html` |
| 10 | Console build: the 18 boards and the `new` items, in the order of the build list in 6a | `templates/kinesis.html`, `handlers_kinesis.go`, `client_kinesis.go`, `client_flow.go` (the Logs edge), `routes_messaging.go` if the key-routing endpoint is added, `testdata/routes.txt` |
| 11 | Browser tests | `e2e/tests/kinesis.spec.ts` |
| 12 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

**Prior art.** The ledger already names the usual local-Kinesis story: a
separate mock process supervised by the emulator. That is the reason this
package exists, and it means there is no in-tree precedent to copy for the
listing and reshard rules; the model's own documentation for `ListShards`,
`UpdateShardCount`, `DeleteStream` and `GetRecords` is the source, and each
test names the member. The consumer contract in section 5 (drain the parent, take
the null iterator and `ChildShards`, then read the children) is the one the
Kinesis Client Library relies on, so a KCL-shaped consumer loop is the right
conformance scenario for it. Paging with a token is `internal/awspage`
(sorted key as the cursor); Kinesis needs one thing it does not have, an
expiry, which is a time stamp inside the token.

**On the size.** *Corrected 2026-10-11: was "M holds for the API if the six
absent operations are refused by name and KIN-9 is settled by decision. Two
things could push it to L".* It is L. The review found 27 findings the plan did
not have (one of them P0, thirteen P1) and the plan's 28 were mostly found from
the code, not from the model; 17 of 18 boards differ from their pages. The
three items that need something that does not exist remain: the settings
accessor from B4, an MD5 routing helper for the browser (board 03), and a Logs
edge in the wiring graph. The recording queue has 39 claims, 13 of them
wholly `memory` and 8 partly. Add the status model if D-K2 says "modelled" (it touches every read
and write path) and the per-stream record limit (it touches the store's one
write path and the 10 MiB response ceiling).

## 12. Pre-stage review (2026-10-11)

Done by reading the model's documentation (every operation's text, every member
of every input and output, the 17 error shapes, the 96 non-structure shapes with their enums and traits), then the
code (`kinesis/`, the console's templates, handlers and client), then by running
a binary built from this tree and rendering every board beside its page.
Decisions that need a person are D-K1 to D-K3 at the end.

### The twelve questions

| | Answer |
|---|---|
| **L1** Read the model's documentation | **Findings.** Corrected in place: the 5 MiB request ceiling (10 MiB), the partition key not counting (it counts), `UpdateMaxRecordSize` as cosmetic (a limit), "which token an SDK paginator sends" (established), the quotas page citation (model), `StreamId` "not established" (inert), the consumer limit (model), retention equal values (model), split and merge on on-demand (model), `ShardCount` required (model prose). New findings from the text: KIN-29 to KIN-33, KIN-35 to KIN-39, KIN-42 to KIN-45. Read: all 45 operations' text and members, the 17 error shapes, the 96 non-structure shapes. |
| **L2** Optional parameters | **Findings.** Read for "if you don't", "only", "at most", "must", "can't": ListStreams/DescribeStream/ListStreamConsumers `Limit` default 100 and cap 100; ListShards `MaxResults` default 1000 cap 1000 (KIN-6); `NextToken` excludes `StreamName`, `StreamCreationTimestamp`, `ExclusiveStartShardId` (KIN-42); `ShardFilter` type-specific required members (KIN-42); `GetRecords.Limit` default 10 000, "greater than 10,000, GetRecords throws InvalidArgumentException" (KIN-46); `DeleteStream.EnforceConsumerDeletion` unset is false (KIN-2); `PutRecord.PartitionKey` optional and ignored under `AUTO`, `ExplicitHashKey` ignored under `AUTO` (KIN-39); `WarmThroughputMiBps` only for on-demand (KIN-39); `RecordDistributionStrategy` default `USER_PARTITION_KEY` (KIN-39); `DryRun` (KIN-29); `EncryptionType` "only valid value is KMS" (KIN-44); `ShardCount` "must" for provisioned (KIN-10). |
| **L3** Error codes | **Findings.** The model's codes by condition: `ResourceNotFoundException` (stream, shard, consumer, policy's resource); `ResourceInUseException` (duplicate create, delete with consumers, stream not ACTIVE for merge, duplicate consumer, SubscribeToShard twice); `LimitExceededException` (tags over 50, consumers over 20, shards over the account's, streams in CREATING over 5, rate); `InvalidArgumentException` (retention not more/less, AUTO on provisioned, bad policy by elimination, size over the limit); `ValidationException` (on-demand stream on Split/Merge/UpdateShardCount, UpdateMaxRecordSize out of range, UpdateAccountSettings); `ExpiredIteratorException`; `ExpiredNextTokenException` (ListShards, ListStreams, ListStreamConsumers, ListChannels); `ProvisionedThroughputExceededException` (not modelled); `DryRunOperationException` (KIN-29); `KMS*` seven codes; `AccessDeniedException` (403 here). All are HTTP 400 except `InternalFailureException` (server); the model has no `awsQueryError` or `httpError` traits, so the status is the protocol's. Wrong or inconsistent here: KIN-3, KIN-8, KIN-10, KIN-11, KIN-35, KIN-36, KIN-41, KIN-46, KIN-50. |
| **L4** Direction | Named on every new row (second table of section 4). Stricter: KIN-30 (the limit), KIN-32, KIN-42 (NextToken alone), KIN-43, KIN-17. More forgiving: KIN-29, KIN-31, KIN-33, KIN-35, KIN-36, KIN-38, KIN-40, KIN-44, KIN-49 to KIN-52, KIN-55. Wrong shape: KIN-37, KIN-39. Wrong code: KIN-41, KIN-46. Wrong data: KIN-34, KIN-45. |
| **L5** Every wire | **Findings.** One protocol, `awsJson1_1` (`model`: `aws.protocols#awsJson1_1`, `eventStreamHttp: h2` for `SubscribeToShard`, declined). No Query wire, no XML: `xmlName` and `xmlFlattened` do not occur (only a service `xmlNamespace`), so SQS-13 and SQS-26 have no analogue here; request validation sees the JSON body whole. Timestamps are epoch seconds with a fraction (`ApproximateArrivalTimestamp`), blobs base64: checked. Open: KIN-53, CBOR from KCL 1.x and the Java SDK v1, `memory`, answered today with `SerializationException`. The `SubscribeToShard` event stream is the declined second wire. Whether the 37 dispatched operations are each accepted as the reference spells them is one SDK test per operation, none of which is counted here; section 5 lists what to check. |
| **L6** Tokens per use | **Findings.** Shard iterator: self-contained (stream, shard, sequence, issue time), valid five minutes from issue, any number of uses; a stale one is `ExpiredIteratorException` (`model`); one for a deleted and re-created stream reads the new one (KIN-51, `memory`). `NextToken`: does not exist yet; the model gives 300 seconds and `ExpiredNextTokenException`, and a token for another listing is not defined (KIN-6, KIN-42). Sequence number: unique stream-wide in the model's `PutRecord` output text, "specific to a shard" in its prose; pattern 129 digits (KIN-43). Consumer ARN: carries the creation timestamp so a re-registered consumer has a new ARN (KIN-49). `StreamCreationTimestamp`: distinguishes a re-created stream of the same name on ListShards and ListStreamConsumers; ignored (KIN-42). |
| **L7** Asynchronous on AWS | **Findings (KIN-47, decision D-K2).** From the model, per operation: CreateStream CREATING then ACTIVE ("You should perform read and write operations only on an ACTIVE stream"); DeleteStream DELETING, reads and writes may continue; SplitShard, MergeShards, UpdateShardCount UPDATING, reads and writes continue; Start/StopStreamEncryption UPDATING ("normally takes a few seconds ... can take minutes", then "up to 5 seconds after ACTIVE" before records are encrypted); UpdateStreamWarmThroughput UPDATING; RegisterStreamConsumer CREATING (five at most), DeregisterStreamConsumer DELETING; channels CREATING, UPDATING; UpdateAccountSettings is **synchronous** (stated). Not stated: UpdateStreamMode, retention, tags (tags: "you can't make any updates to the tags until the stream is in ACTIVE state"). The boards draw no in-progress state, so none waits on this. Recommendation: modelled with a short visible delay (default 1 second, 0 turns it off), reads and writes continue as the model says, and `ResourceInUseException` for the operations that need ACTIVE. |
| **L8** Latency rules | **Findings (KIN-48).** From the model: 25 key changes per rolling 24 hours for Start and for Stop; ten rescales per 24 hours per stream; five streams in CREATING; five consumers in CREATING; "five transactions per second per account" on the control operations (DescribeLimits one, DescribeStream ten, DescribeStreamSummary twenty, ListShards a thousand per stream, GetRecords five per shard); the 24-hour commitment minimum; a closed shard's life is retention. All behind one switch (`--kinesis-strict` / `[kinesis] strict = true`), off by default, state in memory only. The five-minute iterator expiry and the 300-second token are contracts, enforced always. |
| **L9** State | **Checked, with findings.** Persisted (the application's data): streams, shards with lineage, records, the sequence counter, consumers, tags, the policy, the stream limit (new), the distribution strategy (new), the commitment (new). Memory only (the process's): the sweep, the status settle timers (a restart settles to ACTIVE at start, an in-progress change is not resumed), the strict-mode counters, TPS windows. Iterators and tokens are self-contained, so they survive a restart within their life. A new piece of state for each of KIN-30, KIN-37, KIN-39 and D-K2 must say which it is. |
| **L10** Boards rendered | **Findings.** All 18 boards rendered and compared (6a): 1 matches, 17 differ in shape or content, 7 boards corrected in the working copy. |
| **L11** Five lines | **Findings.** None of the settings on a Kinesis page carries them today. The lines, from the model and the ledger (line 4 is range, default and enforced or stored; line 5 only where the wiring graph or the same stream's data supports it): see the table below. |
| **L12** Icons | **Checked, none.** `kinesis.svg` appears in the rail, the stream header and the empty state of the real page (rendered); the board's "KN" mark is the placeholder. The list pane header has the foundation's plain "Kinesis (3)" in the real page and "Streams / New" in the board, with no mark in either: not an icon question. |

### The five teaching lines, setting by setting (L11)

Lines 1 and 2 (name and unit; the value in words) come from the member name and
the typed value. Line 3 is one sentence. Line 4 and line 5 are below.

| Setting | Line 4: range, default, enforced or stored | Line 5: the neighbour check, live |
|---|---|---|
| Stream name | 1 to 128, `[a-zA-Z0-9_.-]`, enforced (`model`) | none |
| Shards (`ShardCount`) | 1 and up; required for PROVISIONED; not more than the account's `ShardLimit` (10 000 here); enforced once KIN-10 and KIN-38 are built (`model`) | the reader: "N shards, each read by one poller of `harbour-stock-forecast`" from the `esm` edge |
| Partition key | 1 to 256 characters, enforced (KIN-32), `console` when empty (code) | "lands on shardId-… at 65.5 % of the key space" from the stream's own shards |
| Data | at most 1 048 576 bytes **including the key** by default; the stream's `MaxRecordSizeInKiB` (1 024 to 10 240) raises it; enforced once KIN-30 is built | the stream's current limit, from `DescribeStreamSummary` |
| Count | 1 to 500 per `PutRecords`, 10 MiB for the request including keys (`model`), enforced; the key is suffixed `-1`, `-2` (code) | where the N land, from `ShardForKey` |
| Retention (`RetentionPeriodHours`) | 24 to 8 760, default 24, "must be more/less than the current" (`model`), enforced; swept once a minute (code) | the reader's lag: "to miss a record it would have to fall behind by more than N h" from the `esm` edge |
| Target shards (`TargetShardCount`) | 1 and up, at most double and at least half the current count, at most 10 000 per stream, `UNIFORM_SCALING` only (`model`), enforced once KIN-9 is built; ten per 24 hours behind strict | "2 now → 4 after" and "the poller follows the new shards without a restart" once Lambda's stage has shown it |
| Stream mode | `PROVISIONED` or `ON_DEMAND`, enforced; on-demand refuses split, merge and rescale (`model`) | the shards kept on a switch (code; AWS's behaviour `memory`) |
| KMS key (`KeyId`) | 1 to 2 048; `EncryptionType` KMS only (`model`); the key must exist and be enabled, enforced (code); the records are not enciphered, stored | the key's state from KMS (`Enabled`, `Disabled`, `PendingDeletion`) |
| Shard-level metrics | 1 to 7 names from the model's list, `ALL` expands, stored (ledger) | none: nothing produces the metrics |
| Consumer name | 1 to 128, `[a-zA-Z0-9_.-]`, unique per stream, at most 20 per stream (`model`), enforced once KIN-35 is built | "N of 20 registered" from the stream |
| Resource policy | JSON with a principal and actions (`model`), enforced when stored once KIN-3 is built; evaluated under soft and enforce (ledger) | the Logs writer: "a `logs.amazonaws.com` statement is needed under enforce" if a Logs filter targets this stream (needs the Logs edge) |
| Tag key and value | key 1 to 128, value 0 to 256, at most 50 per stream, the character set (`model`), enforced once KIN-8 and KIN-33 are built | "N of 50 used" |
| Records: Start at, Records | the window and the limit are the explorer's own (50, 100, 500); the iterator types are the model's | "what this read did" with the real calls |

### Classes of mistake this service is most at risk of

1. **A number from memory where the model states another.** The 5 MiB request
   ceiling, the 1 MiB per record that is a default and not a ceiling, 50
   consumers or 20, 10 000 shards: each is in the model's prose and the first
   pass read none of it. Read each operation's text again at the start of the
   stage, with a pencil, before writing a test.
2. **The wrong unit, silently**: characters counted as bytes (KIN-32), a
   sequence number as `uint64` (KIN-43), a timestamp in seconds. Settle the
   checker's string unit once for every service.
3. **A member accepted and dropped.** Tags, `MaxRecordSizeInKiB`, `DryRun`,
   `RecordDistributionStrategy`, `WarmThroughputMiBps`, a consumer's tags and
   policy: the page, the SDK and the CloudFormation template all read back
   what was not stored. After KIN-1, grep every member of every input for a
   read.
4. **Generated tables that omit what the generator cannot emit.** The
   "356 of 356" counted what was replayed; 14 constraints were never in the
   table and the ledger said `knownGaps` empty. Count constraints from the
   model, not from the table.
5. **One name for two things.** A consumer ARN reached through the stream's
   name (KIN-34, KIN-40); an iterator through a stream's name (KIN-51); a
   `StreamName` with a conflicting ARN. Everything that takes an ARN or an
   iterator resolves it to the right resource first.
6. **Time as a state nobody modelled**: statuses, the 24-hour counters, token
   expiry. The risk is a console board that draws "UPDATING" for something that
   never is, and a test that passes here and races there.
7. **Building the console from the board's words.** The two-column Details page
   and the right-hand panels were built as one column and none. The build list
   in 6a is the order; the table is the gate.

### Decisions needed from a person

- **D-K1.** `UpdateStreamRecordDistributionStrategy` and `RecordDistributionStrategy`:
  build as F (store the setting, `AUTO` picks the shard by round robin and
  ignores the key, on-demand only, `InvalidArgumentException` on provisioned),
  or refuse by name. Recommended F: it is a few lines in `shardFor`.
- **D-K2.** Statuses: modelled with a short delay (recommended, 1 second,
  configurable, 0 is immediate) or immediate with the difference listed. If
  modelled, the stream page shows CREATING, UPDATING and DELETING and the
  console needs a refresh behaviour for them; no board draws one yet.
- **D-K3.** Consumers per stream: 20 always (the model's "Standard and
  Provisioned"), or 50 when `UpdateAccountSettings` has enabled the
  commitment ("On-demand Advantage"). Recommended: 20, and 50 only for
  on-demand streams once the commitment is ENABLED.
