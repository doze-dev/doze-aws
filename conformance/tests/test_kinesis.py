"""Kinesis Data Streams through boto3.

Cost on AWS: a provisioned shard is billed by the hour — about a cent and a
half — and each stream here lives for a minute or two.
"""

FAST = {"Delay": 1, "MaxAttempts": 180}

# Positions and cursors the service mints.
MINTED = ("SequenceNumber", "StartingSequenceNumber", "EndingSequenceNumber",
          "ShardIterator", "NextShardIterator", "NextToken")
# How far behind the tip a read is.
WEATHER = ("MillisBehindLatest",)


def stream(kinesis, names, cleanup, shards=1):
    name = names("stream")
    kinesis.create_stream(StreamName=name, ShardCount=shards)
    cleanup(kinesis.delete_stream, StreamName=name, EnforceConsumerDeletion=True)
    kinesis.get_waiter("stream_exists").wait(StreamName=name, WaiterConfig=FAST)
    return name


def test_stream_lifecycle(client, names, cleanup, snapshot):
    kinesis = client("kinesis")
    name = names("stream")
    snapshot.match("create", kinesis.create_stream(StreamName=name, ShardCount=1))
    cleanup(kinesis.delete_stream, StreamName=name)
    kinesis.get_waiter("stream_exists").wait(StreamName=name, WaiterConfig=FAST)

    snapshot.match("summary", kinesis.describe_stream_summary(StreamName=name), drop=WEATHER)
    snapshot.match("shards", kinesis.list_shards(StreamName=name), opaque=MINTED)
    snapshot.match("tag", kinesis.add_tags_to_stream(StreamName=name, Tags={"env": "dev"}))
    snapshot.match("tags", kinesis.list_tags_for_stream(StreamName=name))
    snapshot.match("retention-up", kinesis.increase_stream_retention_period(
        StreamName=name, RetentionPeriodHours=48))
    kinesis.get_waiter("stream_exists").wait(StreamName=name, WaiterConfig=FAST)
    snapshot.match("summary-after", kinesis.describe_stream_summary(StreamName=name), drop=WEATHER)
    snapshot.match("delete", kinesis.delete_stream(StreamName=name))
    kinesis.get_waiter("stream_not_exists").wait(StreamName=name, WaiterConfig=FAST)
    snapshot.error("describe-deleted", lambda: kinesis.describe_stream_summary(StreamName=name))


def test_records_are_read_back_in_order(client, names, cleanup, snapshot, eventually):
    kinesis = client("kinesis")
    name = stream(kinesis, names, cleanup)

    snapshot.match("put", kinesis.put_record(StreamName=name, Data=b"one", PartitionKey="k"),
                   opaque=MINTED)
    snapshot.match("put-many", kinesis.put_records(StreamName=name, Records=[
        {"Data": b"two", "PartitionKey": "k"}, {"Data": b"three", "PartitionKey": "k"}]),
        opaque=MINTED)

    shard = kinesis.list_shards(StreamName=name)["Shards"][0]["ShardId"]
    it = snapshot.match("iterator", kinesis.get_shard_iterator(
        StreamName=name, ShardId=shard, ShardIteratorType="TRIM_HORIZON"), opaque=MINTED)
    got = []
    cursor = [it["ShardIterator"]]

    def read():
        r = kinesis.get_records(ShardIterator=cursor[0], Limit=10)
        cursor[0] = r["NextShardIterator"]
        got.extend(r["Records"])
        assert len(got) >= 3, f"{len(got)} of 3 records so far"
        return got

    snapshot.match("records", eventually(read, timeout=60), opaque=MINTED)

    latest = kinesis.get_shard_iterator(
        StreamName=name, ShardId=shard, ShardIteratorType="LATEST")["ShardIterator"]
    snapshot.match("nothing-after-latest", kinesis.get_records(ShardIterator=latest),
                   opaque=MINTED, drop=WEATHER)


def test_partition_keys_route_across_shards(client, names, cleanup, snapshot):
    kinesis = client("kinesis")
    name = stream(kinesis, names, cleanup, shards=2)
    snapshot.match("shards", kinesis.list_shards(StreamName=name), opaque=MINTED)
    # Where a key lands is MD5 of the key against the hash ranges: fixed, and
    # the same on any stream with two even shards.
    placed = {}
    for k in ("a", "b", "c", "d", "user-1", "user-2"):
        placed[k] = kinesis.put_record(StreamName=name, Data=b"x", PartitionKey=k)["ShardId"]
    snapshot.match("routing", placed)
    snapshot.match("explicit-hash-key", kinesis.put_record(
        StreamName=name, Data=b"x", PartitionKey="a", ExplicitHashKey="0"), opaque=MINTED)


def test_refusals(client, names, cleanup, snapshot):
    kinesis = client("kinesis")
    absent = names("absent")
    snapshot.error("describe-absent", lambda: kinesis.describe_stream_summary(StreamName=absent))
    snapshot.error("put-to-absent", lambda: kinesis.put_record(
        StreamName=absent, Data=b"x", PartitionKey="k"))
    snapshot.error("delete-absent", lambda: kinesis.delete_stream(StreamName=absent))

    name = stream(kinesis, names, cleanup)
    snapshot.error("create-twice", lambda: kinesis.create_stream(StreamName=name, ShardCount=1))
    snapshot.error("absent-shard", lambda: kinesis.get_shard_iterator(
        StreamName=name, ShardId="shardId-000000000099", ShardIteratorType="LATEST"))
    snapshot.error("garbage-iterator", lambda: kinesis.get_records(ShardIterator="not-an-iterator"))
    snapshot.error("at-sequence-without-one", lambda: kinesis.get_shard_iterator(
        StreamName=name, ShardId="shardId-000000000000", ShardIteratorType="AT_SEQUENCE_NUMBER"))
    snapshot.error("bad-explicit-hash-key", lambda: kinesis.put_record(
        StreamName=name, Data=b"x", PartitionKey="k", ExplicitHashKey="not-a-number"))
    snapshot.error("retention-below-current", lambda: kinesis.decrease_stream_retention_period(
        StreamName=name, RetentionPeriodHours=12))
    # "Increase" to what it already is: refused or a no-op is AWS's to say.
    snapshot.outcome("retention-not-an-increase", lambda: kinesis.increase_stream_retention_period(
        StreamName=name, RetentionPeriodHours=24))
