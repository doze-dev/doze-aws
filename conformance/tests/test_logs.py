"""CloudWatch Logs through boto3."""

import time

# Clock readings, in milliseconds since the epoch, wherever Logs reports one.
CLOCK = ("creationTime", "timestamp", "ingestionTime", "firstEventTimestamp",
         "lastEventTimestamp", "lastIngestionTime")
# Cursors and ids the service mints.
MINTED = ("nextToken", "nextForwardToken", "nextBackwardToken", "nextSequenceToken", "eventId",
          "uploadSequenceToken")
# Updated on the service's own schedule.
WEATHER = ("storedBytes",)


def group(logs, names, cleanup, label="group"):
    name = f"/{names(label)}"
    logs.create_log_group(logGroupName=name)
    cleanup(logs.delete_log_group, logGroupName=name)
    return name


def test_group_and_stream_lifecycle(client, names, cleanup, snapshot):
    logs = client("logs")
    name = f"/{names('group')}"
    snapshot.match("create-group", logs.create_log_group(
        logGroupName=name, tags={"env": "dev"}))
    cleanup(logs.delete_log_group, logGroupName=name)
    snapshot.match("describe", logs.describe_log_groups(logGroupNamePrefix=name),
                   opaque=CLOCK, drop=WEATHER)
    snapshot.match("set-retention", logs.put_retention_policy(logGroupName=name, retentionInDays=7))
    snapshot.match("with-retention", logs.describe_log_groups(logGroupNamePrefix=name),
                   opaque=CLOCK, drop=WEATHER)
    snapshot.match("drop-retention", logs.delete_retention_policy(logGroupName=name))
    snapshot.match("tags", logs.list_tags_log_group(logGroupName=name))

    snapshot.match("create-stream", logs.create_log_stream(logGroupName=name, logStreamName="app"))
    snapshot.match("streams", logs.describe_log_streams(logGroupName=name),
                   opaque=CLOCK + MINTED, drop=WEATHER)
    snapshot.match("delete-stream", logs.delete_log_stream(logGroupName=name, logStreamName="app"))
    snapshot.match("delete-group", logs.delete_log_group(logGroupName=name))


def test_events_are_written_read_and_filtered(client, names, cleanup, snapshot, eventually):
    logs = client("logs")
    name = group(logs, names, cleanup)
    logs.create_log_stream(logGroupName=name, logStreamName="app")
    now = int(time.time() * 1000)
    lines = ["INFO started", "ERROR disk full", '{"level":"error","code":507}', "INFO stopped"]
    snapshot.match("put", logs.put_log_events(
        logGroupName=name, logStreamName="app",
        logEvents=[{"timestamp": now + i, "message": m} for i, m in enumerate(lines)]),
        opaque=MINTED)

    def read():
        r = logs.get_log_events(logGroupName=name, logStreamName="app", startFromHead=True)
        assert len(r["events"]) == len(lines), f"{len(r['events'])} of {len(lines)} so far"
        return r

    snapshot.match("get", eventually(read, timeout=60), opaque=CLOCK + MINTED)

    def filtered(pattern, want):
        def poll():
            r = logs.filter_log_events(logGroupName=name, filterPattern=pattern)
            assert len(r["events"]) >= want, f"{len(r['events'])} of {want} so far"
            return [e["message"] for e in r["events"]]
        return eventually(poll, timeout=60)

    snapshot.match("filter-term", filtered("ERROR", 1))
    snapshot.match("filter-two-terms", filtered("INFO started", 1))
    snapshot.match("filter-quoted", filtered('"disk full"', 1))
    snapshot.match("filter-exclude", filtered("INFO -started", 1))
    snapshot.match("filter-json", filtered('{ $.code = 507 }', 1))
    snapshot.match("filter-json-string", filtered('{ $.level = "error" }', 1))


def test_refusals(client, names, cleanup, snapshot):
    logs = client("logs")
    absent = f"/{names('absent')}"
    snapshot.error("stream-in-absent-group", lambda: logs.create_log_stream(
        logGroupName=absent, logStreamName="app"))
    snapshot.error("delete-absent-group", lambda: logs.delete_log_group(logGroupName=absent))
    snapshot.error("events-from-absent-group", lambda: logs.get_log_events(
        logGroupName=absent, logStreamName="app"))

    name = group(logs, names, cleanup)
    snapshot.error("create-group-twice", lambda: logs.create_log_group(logGroupName=name))
    snapshot.error("retention-not-allowed", lambda: logs.put_retention_policy(
        logGroupName=name, retentionInDays=2))
    snapshot.error("events-from-absent-stream", lambda: logs.get_log_events(
        logGroupName=name, logStreamName="absent"))
    snapshot.error("put-to-absent-stream", lambda: logs.put_log_events(
        logGroupName=name, logStreamName="absent",
        logEvents=[{"timestamp": int(time.time() * 1000), "message": "x"}]))

    logs.create_log_stream(logGroupName=name, logStreamName="app")
    snapshot.error("create-stream-twice", lambda: logs.create_log_stream(
        logGroupName=name, logStreamName="app"))
    now = int(time.time() * 1000)
    snapshot.error("out-of-order", lambda: logs.put_log_events(
        logGroupName=name, logStreamName="app",
        logEvents=[{"timestamp": now, "message": "b"}, {"timestamp": now - 1000, "message": "a"}]))
    snapshot.error("bad-filter-pattern", lambda: logs.filter_log_events(
        logGroupName=name, filterPattern="{ $.code = }"))
