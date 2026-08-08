# Kinesis — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

doze-aws implements Kinesis Data Streams natively in Go. There is no JVM, no
Node sidecar and no third-party mock process — which is worth stating plainly,
because the usual local-Kinesis story is a downloaded Scala binary supervised by
the emulator.

The AWS JSON 1.1 protocol is served under the `Kinesis_20131202` target prefix.
Partition-key routing is the real algorithm — MD5 of the key read as a 128-bit
big-endian integer, matched against each shard's hash range — so a key lands on
the same shard number locally as it does in the cloud.

| Operation | Tier | Notes |
|---|---|---|
| CreateStream | F | provisioned (honours ShardCount) and on-demand (starts at 4 shards); shards tile the hash space with no gaps; a duplicate is ResourceInUseException, as in AWS |
| DeleteStream | F | drops records, shards and consumer registrations |
| ListStreams | F | prefix-free lexical order with ExclusiveStartStreamName + Limit; returns both StreamNames and StreamSummaries |
| DescribeStream | F | full shard list with hash ranges, sequence ranges, and parent/adjacent lineage; ExclusiveStartShardId honoured |
| DescribeStreamSummary | F | incl. OpenShardCount and live ConsumerCount |
| ListShards | F | ExclusiveStartShardId honoured; no pagination (local shard counts don't need it) |
| PutRecord | F | partition-key routing, ExplicitHashKey override, 1 MiB record ceiling, 256-char key limit |
| PutRecords | F | up to 500 records, written in one transaction — FailedRecordCount is always 0 because there is no local throttling to cause a partial write |
| GetRecords | F | Limit + 10 MiB response ceiling, MillisBehindLatest, null NextShardIterator plus ChildShards on a drained closed shard |
| GetShardIterator | F | TRIM_HORIZON, LATEST, AT_TIMESTAMP, AT_SEQUENCE_NUMBER (inclusive), AFTER_SEQUENCE_NUMBER (exclusive); iterators expire after 5 minutes with ExpiredIteratorException |
| SplitShard | F | closes the parent at the current end of stream and opens two children; split point validated against the parent's range |
| MergeShards | F | adjacency is enforced — merging a gap would silently drop hash space; the child names both parents |
| UpdateShardCount | F | UNIFORM_SCALING; closes every open shard and re-tiles, each child naming the parent that owned its starting hash |
| UpdateStreamMode | F | PROVISIONED ↔ ON_DEMAND |
| IncreaseStreamRetentionPeriod | F | rejects a request that would shorten the window |
| DecreaseStreamRetentionPeriod | F | rejects a request that would lengthen it; 24h–8760h bounds enforced |
| AddTagsToStream / RemoveTagsFromStream / ListTagsForStream | F | |
| TagResource / UntagResource / ListTagsForResource | F | the ARN-addressed tag API |
| RegisterStreamConsumer | F | duplicate registration is ResourceInUseException |
| DeregisterStreamConsumer / DescribeStreamConsumer | F | addressable by (StreamARN, ConsumerName) or by ConsumerARN |
| ListStreamConsumers | F | |
| StartStreamEncryption / StopStreamEncryption | C | the KeyId round-trips and is echoed in DescribeStream; records live in the data directory either way |
| EnableEnhancedMonitoring / DisableEnhancedMonitoring | C | shard-level metrics are stored and echoed; there is no CloudWatch locally to publish them to |
| DescribeLimits | C | reports live OpenShardCount against a nominal quota |
| DescribeAccountSettings / UpdateAccountSettings | C | no account-level quotas locally |
| UpdateMaxRecordSize | C | accepted; the 1 MiB ceiling on the put path is not raised |
| PutResourcePolicy / GetResourcePolicy / DeleteResourcePolicy | C | the policy document round-trips; there is no IAM evaluation until the IAM service lands |
| SubscribeToShard | S | enhanced fan-out delivers over an HTTP/2 event stream; register the consumer and poll GetRecords instead |
| UpdateStreamWarmThroughput | S | a capacity hint with no local meaning |

## Retention

Records really expire. A background sweep (once a minute by default) drops
records past their stream's retention window and reclaims closed shards once
they have been fully drained, so a long-running local stack does not grow
without bound. `TRIM_HORIZON` starts below the oldest *surviving* record rather
than at sequence zero, so a consumer that reconnects after a sweep sees exactly
what is still there.

## Resharding and ordering

Resharding is the part of Kinesis where correctness is easy to fake and hard to
get right, so it is modelled properly. A shard is never mutated in place: it is
closed at the current end of the stream, keeps its records and gains an
`EndingSequenceNumber`, and children are opened to cover its hash range from
that point on.

A consumer therefore sees the real contract — drain the parent, receive a null
`NextShardIterator` and a `ChildShards` list, then start on the children. That
handover is what keeps records for a single partition key in order across a
reshard, and it is what the KCL relies on.

## Lambda event source mappings

A Lambda event source mapping whose `EventSourceArn` names a Kinesis stream is
polled per shard, with the shard list refreshed whenever a shard drains — so a
reshard mid-run is picked up without restarting the mapping. Events arrive in
the standard `aws:kinesis` record shape. Delivery is at-least-once and the
iterator advances regardless of the invocation result, matching the DynamoDB
stream poller.

Because doze-aws also implements DynamoDB, a KCL application's lease table works
against the same endpoint.
