"""SQS through boto3."""

import json

# Asked for by name rather than "All": the Approximate* counters and the two
# timestamps are the service's weather, and these are its defaults.
DEFAULTS = [
    "VisibilityTimeout", "DelaySeconds", "MaximumMessageSize",
    "MessageRetentionPeriod", "ReceiveMessageWaitTimeSeconds",
    "QueueArn", "SqsManagedSseEnabled",
]


def receive(sqs, eventually, url, want=1, **kw):
    """Receive until `want` messages have arrived. One call is not enough on
    AWS, where a short poll samples a subset of the servers."""
    got = []

    def poll():
        r = sqs.receive_message(QueueUrl=url, MaxNumberOfMessages=10, WaitTimeSeconds=1, **kw)
        got.extend(r.get("Messages", []))
        assert len(got) >= want, f"{len(got)} of {want} messages so far"
        return got

    return eventually(poll)


def test_standard_queue_lifecycle(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    name = names("queue")

    created = snapshot.match("create", sqs.create_queue(QueueName=name))
    url = created["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)

    snapshot.match("create-again", sqs.create_queue(QueueName=name))
    snapshot.match("get-url", sqs.get_queue_url(QueueName=name))
    snapshot.match("defaults", sqs.get_queue_attributes(QueueUrl=url, AttributeNames=DEFAULTS))

    snapshot.match("send", sqs.send_message(QueueUrl=url, MessageBody="hello"))
    msgs = receive(sqs, eventually, url)
    snapshot.match("received", msgs, opaque=("ReceiptHandle",))

    snapshot.match("delete-message",
                   sqs.delete_message(QueueUrl=url, ReceiptHandle=msgs[0]["ReceiptHandle"]))
    snapshot.match("delete-queue", sqs.delete_queue(QueueUrl=url))


def test_message_attributes_and_batches(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    url = sqs.create_queue(QueueName=names("queue"))["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)

    snapshot.match("send-with-attributes", sqs.send_message(
        QueueUrl=url, MessageBody="typed",
        MessageAttributes={
            "kind": {"DataType": "String", "StringValue": "order"},
            "count": {"DataType": "Number", "StringValue": "3"},
            "blob": {"DataType": "Binary", "BinaryValue": b"\x00\x01"},
        },
    ))
    msgs = receive(sqs, eventually, url, MessageAttributeNames=["All"])
    snapshot.match("received-with-attributes", msgs, opaque=("ReceiptHandle",))
    sqs.delete_message(QueueUrl=url, ReceiptHandle=msgs[0]["ReceiptHandle"])

    snapshot.match("send-batch", sqs.send_message_batch(QueueUrl=url, Entries=[
        {"Id": "a", "MessageBody": "one"},
        {"Id": "b", "MessageBody": "two", "DelaySeconds": 0},
    ]))
    msgs = receive(sqs, eventually, url, want=2)
    snapshot.match("delete-batch", sqs.delete_message_batch(QueueUrl=url, Entries=[
        {"Id": m["Body"], "ReceiptHandle": m["ReceiptHandle"]} for m in msgs
    ]), unordered=("Successful",))


def test_system_attributes_by_either_spelling(client, names, cleanup, snapshot, eventually):
    """AttributeNames is the spelling every SQS consumer written before 2023
    uses, and the one boto3 own documentation still shows. The service model
    types it as the QUEUE attribute enum, which does not contain a single
    message attribute — and AWS accepts them all the same."""
    sqs = client("sqs")
    url = sqs.create_queue(QueueName=names("queue"))["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)
    sqs.send_message(QueueUrl=url, MessageBody="x")
    receive(sqs, eventually, url, VisibilityTimeout=0)

    def attrs(**kw):
        return sorted(sqs.receive_message(
            QueueUrl=url, WaitTimeSeconds=1, VisibilityTimeout=0, **kw)["Messages"][0]["Attributes"])

    snapshot.match("current-spelling", attrs(MessageSystemAttributeNames=["SentTimestamp"]))
    snapshot.match("all", attrs(AttributeNames=["All"]))
    snapshot.match("deprecated-spelling", attrs(AttributeNames=["SentTimestamp"]))
    snapshot.match("deprecated-spelling-count", attrs(AttributeNames=["ApproximateReceiveCount"]))


def test_fifo_orders_and_deduplicates(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    url = sqs.create_queue(
        QueueName=names("queue", suffix=".fifo"),
        Attributes={"FifoQueue": "true", "ContentBasedDeduplication": "true"},
    )["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)

    for body in ("first", "second", "third"):
        snapshot.match(f"send-{body}",
                       sqs.send_message(QueueUrl=url, MessageBody=body, MessageGroupId="g"),
                       opaque=("SequenceNumber",))
    # Same body inside the deduplication window: accepted, and not delivered twice.
    snapshot.match("send-duplicate",
                   sqs.send_message(QueueUrl=url, MessageBody="first", MessageGroupId="g"),
                   opaque=("SequenceNumber",))

    # How many of one group a single receive hands over is the service's to
    # decide, so it is recorded rather than asserted — AWS gives as many of the
    # same group as it can, in order.
    first = sqs.receive_message(QueueUrl=url, MaxNumberOfMessages=10, WaitTimeSeconds=2,
                                MessageSystemAttributeNames=["MessageGroupId"])["Messages"]
    snapshot.match("first-receive", [(m["Body"], m["Attributes"]) for m in first])

    # The order is the contract: delete each one to release the group.
    bodies = []
    for m in first:
        bodies.append(m["Body"])
        sqs.delete_message(QueueUrl=url, ReceiptHandle=m["ReceiptHandle"])

    def drain():
        r = sqs.receive_message(QueueUrl=url, MaxNumberOfMessages=10, WaitTimeSeconds=1)
        for m in r.get("Messages", []):
            bodies.append(m["Body"])
            sqs.delete_message(QueueUrl=url, ReceiptHandle=m["ReceiptHandle"])
        assert len(bodies) >= 3, f"{bodies} so far"

    eventually(drain)
    assert bodies == ["first", "second", "third"]


def test_dead_letter_queue_takes_what_was_received_too_often(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    dlq = sqs.create_queue(QueueName=names("queue-dlq"))["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=dlq)
    dlq_arn = sqs.get_queue_attributes(QueueUrl=dlq, AttributeNames=["QueueArn"])["Attributes"]["QueueArn"]

    src = sqs.create_queue(QueueName=names("queue"), Attributes={
        "VisibilityTimeout": "1",
        "RedrivePolicy": json.dumps({"deadLetterTargetArn": dlq_arn, "maxReceiveCount": "1"}),
    })["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=src)
    snapshot.match("redrive-policy",
                   sqs.get_queue_attributes(QueueUrl=src, AttributeNames=["RedrivePolicy"]))
    snapshot.match("sources", sqs.list_dead_letter_source_queues(QueueUrl=dlq))

    sqs.send_message(QueueUrl=src, MessageBody="poison")
    receive(sqs, eventually, src)  # received once, never deleted

    def moved():
        # The move happens on the receive AFTER the count is exceeded.
        sqs.receive_message(QueueUrl=src, WaitTimeSeconds=1)
        r = sqs.receive_message(QueueUrl=dlq, WaitTimeSeconds=1)
        assert r.get("Messages"), "not in the DLQ yet"
        return r["Messages"]

    snapshot.match("in-the-dlq", [m["Body"] for m in eventually(moved, timeout=30)])


def test_refusals(client, names, cleanup, snapshot):
    sqs = client("sqs")
    snapshot.error("no-such-queue", lambda: sqs.get_queue_url(QueueName=names("absent")))
    snapshot.error("bad-name", lambda: sqs.create_queue(QueueName="not a queue name!"))

    url = sqs.create_queue(QueueName=names("queue"))["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)
    snapshot.error("different-attributes", lambda: sqs.create_queue(
        QueueName=names("queue"), Attributes={"VisibilityTimeout": "77"}))
    snapshot.error("bad-receipt-handle", lambda: sqs.delete_message(
        QueueUrl=url, ReceiptHandle="not-a-receipt-handle"))
    snapshot.error("duplicate-batch-ids", lambda: sqs.send_message_batch(QueueUrl=url, Entries=[
        {"Id": "a", "MessageBody": "one"}, {"Id": "a", "MessageBody": "two"}]))

    fifo = sqs.create_queue(QueueName=names("queue", suffix=".fifo"),
                            Attributes={"FifoQueue": "true"})["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=fifo)
    snapshot.error("fifo-without-group", lambda: sqs.send_message(QueueUrl=fifo, MessageBody="x"))
    snapshot.error("fifo-without-dedup", lambda: sqs.send_message(
        QueueUrl=fifo, MessageBody="x", MessageGroupId="g"))


def test_batch_refusals(client, names, cleanup, snapshot):
    """A batch is refused whole for its shape, before any entry is tried."""
    sqs = client("sqs")
    url = sqs.create_queue(QueueName=names("queue"))["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)
    body = lambda i: {"Id": f"m{i}", "MessageBody": "x"}

    snapshot.error("empty", lambda: sqs.send_message_batch(QueueUrl=url, Entries=[]))
    snapshot.error("eleven", lambda: sqs.send_message_batch(
        QueueUrl=url, Entries=[body(i) for i in range(11)]))
    snapshot.error("bad-id", lambda: sqs.send_message_batch(
        QueueUrl=url, Entries=[{"Id": "has space", "MessageBody": "x"}]))
    snapshot.error("delete-repeated-id", lambda: sqs.delete_message_batch(QueueUrl=url, Entries=[
        {"Id": "a", "ReceiptHandle": "x"}, {"Id": "a", "ReceiptHandle": "y"}]))
    snapshot.error("visibility-empty", lambda: sqs.change_message_visibility_batch(
        QueueUrl=url, Entries=[]))
    # One bad entry among good ones fails alone.
    snapshot.match("one-entry-fails", sqs.send_message_batch(QueueUrl=url, Entries=[
        body(1), {"Id": "late", "MessageBody": "x", "DelaySeconds": 901}]))
    snapshot.match("nothing-else-was-sent", sqs.get_queue_attributes(
        QueueUrl=url, AttributeNames=["DelaySeconds"]))


def test_tags_visibility_and_purge(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    url = sqs.create_queue(QueueName=names("queue"), tags={"env": "dev"})["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)

    snapshot.match("tags-from-create", sqs.list_queue_tags(QueueUrl=url))
    snapshot.match("tag", sqs.tag_queue(QueueUrl=url, Tags={"team": "a"}))
    snapshot.match("untag", sqs.untag_queue(QueueUrl=url, TagKeys=["env"]))
    snapshot.match("tags", sqs.list_queue_tags(QueueUrl=url))

    sqs.send_message(QueueUrl=url, MessageBody="x")
    first = receive(sqs, eventually, url, VisibilityTimeout=60)[0]
    snapshot.match("hidden-while-in-flight", sqs.receive_message(QueueUrl=url, WaitTimeSeconds=1))
    snapshot.match("release", sqs.change_message_visibility(
        QueueUrl=url, ReceiptHandle=first["ReceiptHandle"], VisibilityTimeout=0))
    again = receive(sqs, eventually, url, MessageSystemAttributeNames=["ApproximateReceiveCount"])
    snapshot.match("received-twice", [m["Attributes"] for m in again])

    snapshot.error("visibility-too-long", lambda: sqs.change_message_visibility(
        QueueUrl=url, ReceiptHandle=again[0]["ReceiptHandle"], VisibilityTimeout=43201))
    snapshot.match("set-attributes", sqs.set_queue_attributes(
        QueueUrl=url, Attributes={"VisibilityTimeout": "5", "DelaySeconds": "1"}))
    snapshot.match("attributes-after", sqs.get_queue_attributes(
        QueueUrl=url, AttributeNames=["VisibilityTimeout", "DelaySeconds"]))
    snapshot.error("retention-too-short", lambda: sqs.set_queue_attributes(
        QueueUrl=url, Attributes={"MessageRetentionPeriod": "59"}))
    snapshot.error("unknown-attribute", lambda: sqs.set_queue_attributes(
        QueueUrl=url, Attributes={"NoSuchAttribute": "1"}))
    snapshot.match("purge", sqs.purge_queue(QueueUrl=url))


# ---------------------------------------------------------------------------
# Stage 1: what the plan's section 8 queued, and what reading the service
# model's own documentation added. Each scenario is written once; recorded
# against a real account it becomes the oracle for the rule it states.
# ---------------------------------------------------------------------------

def make_queue(sqs, names, cleanup, label="queue", suffix="", **kw):
    url = sqs.create_queue(QueueName=names(label, suffix=suffix), **kw)["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)
    return url


def queue_arn(sqs, url):
    return sqs.get_queue_attributes(QueueUrl=url, AttributeNames=["QueueArn"])["Attributes"]["QueueArn"]


def test_attribute_ranges(client, names, cleanup, snapshot):
    """Every numeric attribute at and past its documented bound, and a value
    that is not a number. The model states them in prose, not in traits."""
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    for attr, bad in [
        ("VisibilityTimeout", "43201"), ("VisibilityTimeout", "-1"),
        ("DelaySeconds", "901"), ("MessageRetentionPeriod", "59"),
        ("MessageRetentionPeriod", "1209601"), ("MaximumMessageSize", "1023"),
        ("MaximumMessageSize", "1048577"), ("ReceiveMessageWaitTimeSeconds", "21"),
        ("KmsDataKeyReusePeriodSeconds", "59"), ("KmsDataKeyReusePeriodSeconds", "86401"),
        ("VisibilityTimeout", "soon"),
    ]:
        snapshot.error(f"{attr}-{bad}", lambda a=attr, b=bad: sqs.set_queue_attributes(
            QueueUrl=url, Attributes={a: b}))
    snapshot.match("at-the-bounds", sqs.set_queue_attributes(QueueUrl=url, Attributes={
        "VisibilityTimeout": "43200", "DelaySeconds": "900", "MessageRetentionPeriod": "60",
        "MaximumMessageSize": "1048576", "ReceiveMessageWaitTimeSeconds": "20"}))
    snapshot.error("unknown-attribute", lambda: sqs.set_queue_attributes(
        QueueUrl=url, Attributes={"NoSuchAttribute": "1"}))


def test_receive_out_of_range(client, names, cleanup, snapshot):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    snapshot.error("max-0", lambda: sqs.receive_message(QueueUrl=url, MaxNumberOfMessages=0))
    snapshot.error("max-11", lambda: sqs.receive_message(QueueUrl=url, MaxNumberOfMessages=11))
    snapshot.error("wait-21", lambda: sqs.receive_message(QueueUrl=url, WaitTimeSeconds=21))
    snapshot.error("visibility-43201", lambda: sqs.receive_message(QueueUrl=url, VisibilityTimeout=43201))


def test_recreate_after_delete(client, names, cleanup, snapshot):
    """doze-aws does not model the 60 seconds (the delete is immediate here);
    recorded against AWS this is the entry that goes into deviations.py."""
    sqs = client("sqs")
    name = names("queue")
    url = sqs.create_queue(QueueName=name)["QueueUrl"]
    sqs.delete_queue(QueueUrl=url)
    again = snapshot.outcome("create-at-once", lambda: sqs.create_queue(QueueName=name))
    if isinstance(again, dict):
        cleanup(sqs.delete_queue, QueueUrl=again["QueueUrl"])


def test_purge_twice(client, names, cleanup, snapshot):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    snapshot.match("first", sqs.purge_queue(QueueUrl=url))
    snapshot.outcome("second", lambda: sqs.purge_queue(QueueUrl=url))


def dlq_pair(sqs, names, cleanup, max_receive=1, visibility="1", **dlq_kw):
    dlq = make_queue(sqs, names, cleanup, "queue-dlq", **dlq_kw)
    arn = queue_arn(sqs, dlq)
    src = make_queue(sqs, names, cleanup, "queue", Attributes={
        "VisibilityTimeout": visibility,
        "RedrivePolicy": json.dumps({"deadLetterTargetArn": arn, "maxReceiveCount": str(max_receive)})})
    return src, dlq, arn


def dead_letter(sqs, eventually, src, dlq, body):
    sqs.send_message(QueueUrl=src, MessageBody=body)
    receive(sqs, eventually, src)

    def moved():
        sqs.receive_message(QueueUrl=src, WaitTimeSeconds=1)
        r = sqs.receive_message(QueueUrl=dlq, WaitTimeSeconds=1, VisibilityTimeout=0, MaxNumberOfMessages=10,
                                MessageSystemAttributeNames=["All"])
        assert any(m["Body"] == body for m in r.get("Messages", [])), "not in the DLQ yet"
        return r["Messages"]

    return eventually(moved, timeout=40)


def test_redrive_receive_count(client, names, cleanup, snapshot, eventually):
    """What a message says about where it came from and how often it was
    received once it is in the dead-letter queue."""
    sqs = client("sqs")
    src, dlq, _ = dlq_pair(sqs, names, cleanup)
    msgs = dead_letter(sqs, eventually, src, dlq, "poison")
    snapshot.match("in-the-dlq", [{"Body": m["Body"], "Attributes": {
        k: v for k, v in m["Attributes"].items()
        if k in ("ApproximateReceiveCount", "DeadLetterQueueSourceArn")}} for m in msgs])


def test_visibility_change_in_flight(client, names, cleanup, snapshot, eventually):
    """A queue's visibility timeout changed while a message is in flight does
    not move that message's deadline."""
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup, Attributes={"VisibilityTimeout": "30"})
    sqs.send_message(QueueUrl=url, MessageBody="x")
    receive(sqs, eventually, url)
    sqs.set_queue_attributes(QueueUrl=url, Attributes={"VisibilityTimeout": "1"})
    import time
    time.sleep(3)
    snapshot.match("still-hidden", sqs.receive_message(QueueUrl=url, WaitTimeSeconds=1))


def test_twelve_hours_from_the_receive(client, names, cleanup, snapshot, eventually):
    """"If you attempt to set the VisibilityTimeout to a value greater than the
    maximum time left, Amazon SQS returns an error"."""
    import time
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    sqs.send_message(QueueUrl=url, MessageBody="x")
    msg = receive(sqs, eventually, url, VisibilityTimeout=43200)[0]
    time.sleep(2)
    snapshot.outcome("past-the-twelve-hours", lambda: sqs.change_message_visibility(
        QueueUrl=url, ReceiptHandle=msg["ReceiptHandle"], VisibilityTimeout=43200))
    snapshot.match("within-it", sqs.change_message_visibility(
        QueueUrl=url, ReceiptHandle=msg["ReceiptHandle"], VisibilityTimeout=600))


def test_visibility_of_a_message_not_in_flight(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    sqs.send_message(QueueUrl=url, MessageBody="x")
    msg = receive(sqs, eventually, url, VisibilityTimeout=0)[0]
    snapshot.error("not-in-flight", lambda: sqs.change_message_visibility(
        QueueUrl=url, ReceiptHandle=msg["ReceiptHandle"], VisibilityTimeout=30))


def test_move_task_default_destination(client, names, cleanup, snapshot, eventually):
    """No DestinationArn: each message goes back to the queue it came from."""
    sqs = client("sqs")
    src, dlq, dlq_arn = dlq_pair(sqs, names, cleanup)
    dead_letter(sqs, eventually, src, dlq, "poison")
    snapshot.error("source-is-no-dlq", lambda: sqs.start_message_move_task(SourceArn=queue_arn(sqs, src)))
    task = sqs.start_message_move_task(SourceArn=dlq_arn)
    snapshot.match("started", task, opaque=("TaskHandle",))

    def done():
        r = sqs.list_message_move_tasks(SourceArn=dlq_arn)
        assert r["Results"] and r["Results"][0]["Status"] in ("COMPLETED", "FAILED"), r
        return r

    listed = eventually(done, timeout=60)
    snapshot.match("listed", listed, opaque=("TaskHandle", "StartedTimestamp"))
    back = receive(sqs, eventually, src, VisibilityTimeout=0)
    snapshot.match("back-at-the-source", [m["Body"] for m in back])
    snapshot.error("list-upper-limit", lambda: sqs.list_message_move_tasks(SourceArn=dlq_arn, MaxResults=11))


def test_move_task_missing_queue(client, snapshot):
    sqs = client("sqs")
    arn = "arn:aws:sqs:us-east-1:000000000000:dzc-no-such-queue"
    snapshot.error("start", lambda: sqs.start_message_move_task(SourceArn=arn))
    snapshot.error("list", lambda: sqs.list_message_move_tasks(SourceArn=arn))


def test_invalid_policy(client, names, cleanup, snapshot):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    for label, doc in [
        ("not-json", "not json"),
        ("no-effect", json.dumps({"Version": "2012-10-17", "Statement": [{"Principal": "*", "Action": "sqs:*", "Resource": "*"}]})),
        ("no-principal", json.dumps({"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "sqs:*", "Resource": "*"}]})),
        ("no-resource", json.dumps({"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": "*", "Action": "sqs:*"}]})),
    ]:
        snapshot.error(label, lambda d=doc: sqs.set_queue_attributes(QueueUrl=url, Attributes={"Policy": d}))
    snapshot.outcome("empty-statement", lambda: sqs.set_queue_attributes(
        QueueUrl=url, Attributes={"Policy": json.dumps({"Version": "2012-10-17", "Statement": []})}))


def test_tag_limits(client, names, cleanup, snapshot):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    snapshot.error("key-129", lambda: sqs.tag_queue(QueueUrl=url, Tags={"k" * 129: "v"}))
    snapshot.error("value-257", lambda: sqs.tag_queue(QueueUrl=url, Tags={"k": "v" * 257}))
    snapshot.error("aws-prefix", lambda: sqs.tag_queue(QueueUrl=url, Tags={"aws:x": "v"}))
    # "Adding more than 50 tags to a queue isn't recommended": advice, so the
    # answer is recorded rather than assumed.
    snapshot.outcome("fifty-one", lambda: sqs.tag_queue(
        QueueUrl=url, Tags={f"t{i}": "v" for i in range(51)}))


def test_message_limits(client, names, cleanup, snapshot):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    one_mib = 1048576
    snapshot.match("one-mebibyte", sqs.send_message(QueueUrl=url, MessageBody="x" * one_mib),
                   opaque=("MessageId",))
    snapshot.error("over-one-mebibyte", lambda: sqs.send_message(QueueUrl=url, MessageBody="x" * (one_mib + 1)))
    snapshot.error("bad-character", lambda: sqs.send_message(QueueUrl=url, MessageBody="a\x01b"))
    snapshot.error("eleven-attributes", lambda: sqs.send_message(QueueUrl=url, MessageBody="x", MessageAttributes={
        f"a{i}": {"DataType": "String", "StringValue": "v"} for i in range(11)}))
    snapshot.error("reserved-prefix", lambda: sqs.send_message(QueueUrl=url, MessageBody="x", MessageAttributes={
        "AWS.thing": {"DataType": "String", "StringValue": "v"}}))
    snapshot.error("data-type", lambda: sqs.send_message(QueueUrl=url, MessageBody="x", MessageAttributes={
        "a": {"DataType": "Integer", "StringValue": "1"}}))
    snapshot.error("delay-901", lambda: sqs.send_message(QueueUrl=url, MessageBody="x", DelaySeconds=901))


def test_batch_total_size(client, names, cleanup, snapshot):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    q = 262144
    snapshot.match("four-quarter-mebibytes", sqs.send_message_batch(QueueUrl=url, Entries=[
        {"Id": f"m{i}", "MessageBody": "x" * q} for i in range(4)]),
        opaque=("MessageId",))
    snapshot.error("one-byte-more", lambda: sqs.send_message_batch(QueueUrl=url, Entries=[
        {"Id": f"m{i}", "MessageBody": "x" * (q + (1 if i == 3 else 0))} for i in range(4)]))


def test_message_group_on_a_standard_queue(client, names, cleanup, snapshot, eventually):
    """The model: "In standard queues, using MessageGroupId enables fair
    queues"."""
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    snapshot.match("send", sqs.send_message(QueueUrl=url, MessageBody="x", MessageGroupId="tenant-a"),
                   opaque=("MessageId",))
    got = receive(sqs, eventually, url, MessageSystemAttributeNames=["MessageGroupId"])
    snapshot.match("received", [m["Attributes"] for m in got])
    snapshot.outcome("with-dedup-id", lambda: sqs.send_message(
        QueueUrl=url, MessageBody="x", MessageDeduplicationId="d"))


def test_trace_header_on_a_batch(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    header = "Root=1-5759e988-bd862e3fe1be46a994272793;Parent=53995c3f42cd8ad8;Sampled=1"
    res = sqs.send_message_batch(QueueUrl=url, Entries=[{
        "Id": "a", "MessageBody": "traced",
        "MessageSystemAttributes": {"AWSTraceHeader": {"DataType": "String", "StringValue": header}}}])
    snapshot.match("batch", res, opaque=("MessageId",))
    got = receive(sqs, eventually, url, MessageSystemAttributeNames=["AWSTraceHeader"])
    snapshot.match("received", [m["Attributes"] for m in got])


def test_message_attribute_prefix_selection(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    sqs.send_message(QueueUrl=url, MessageBody="x", MessageAttributes={
        "bar.one": {"DataType": "String", "StringValue": "1"},
        "bar.two": {"DataType": "String", "StringValue": "2"},
        "baz": {"DataType": "String", "StringValue": "3"}})
    got = receive(sqs, eventually, url, VisibilityTimeout=0, MessageAttributeNames=["bar.*"])
    snapshot.match("prefix", sorted(got[0].get("MessageAttributes", {})))


def test_list_queues_paging(client, names, cleanup, snapshot):
    sqs = client("sqs")
    prefix = names("page")
    for i in range(3):
        cleanup(sqs.delete_queue, QueueUrl=sqs.create_queue(QueueName=f"{prefix}-{i}")["QueueUrl"])
    first = sqs.list_queues(QueueNamePrefix=prefix, MaxResults=2)
    snapshot.match("first-page", {"count": len(first["QueueUrls"]), "more": "NextToken" in first})
    second = sqs.list_queues(QueueNamePrefix=prefix, MaxResults=2, NextToken=first["NextToken"])
    snapshot.match("second-page", {"count": len(second["QueueUrls"]), "more": "NextToken" in second})
    snapshot.error("max-1001", lambda: sqs.list_queues(MaxResults=1001))


def test_add_permission_rules(client, names, cleanup, snapshot):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    acct = "111122223333"
    snapshot.match("first", sqs.add_permission(QueueUrl=url, Label="friend", AWSAccountIds=[acct],
                                               Actions=["SendMessage", "ReceiveMessage"]))
    snapshot.error("label-in-use", lambda: sqs.add_permission(
        QueueUrl=url, Label="friend", AWSAccountIds=[acct], Actions=["SendMessage"]))
    snapshot.error("label-81", lambda: sqs.add_permission(
        QueueUrl=url, Label="a" * 81, AWSAccountIds=[acct], Actions=["SendMessage"]))
    snapshot.error("label-chars", lambda: sqs.add_permission(
        QueueUrl=url, Label="has space", AWSAccountIds=[acct], Actions=["SendMessage"]))
    snapshot.error("unknown-action", lambda: sqs.add_permission(
        QueueUrl=url, Label="u", AWSAccountIds=[acct], Actions=["DoEverything"]))
    snapshot.outcome("any-action", lambda: sqs.add_permission(
        QueueUrl=url, Label="any", AWSAccountIds=[acct], Actions=["SetQueueAttributes"]))
    snapshot.outcome("eight-actions", lambda: sqs.add_permission(
        QueueUrl=url, Label="eight", AWSAccountIds=[acct], Actions=[
            "SendMessage", "ReceiveMessage", "DeleteMessage", "ChangeMessageVisibility",
            "GetQueueAttributes", "GetQueueUrl", "PurgeQueue", "ListQueues"]))
    snapshot.outcome("non-account-principal", lambda: sqs.add_permission(
        QueueUrl=url, Label="arn", AWSAccountIds=["arn:aws:iam::111122223333:user/alice"], Actions=["SendMessage"]))
    snapshot.match("policy", {"Statements": len(json.loads(sqs.get_queue_attributes(
        QueueUrl=url, AttributeNames=["Policy"])["Attributes"]["Policy"])["Statement"])})
    snapshot.error("remove-missing", lambda: sqs.remove_permission(QueueUrl=url, Label="never-added"))
    snapshot.match("remove", sqs.remove_permission(QueueUrl=url, Label="friend"))


def test_receive_request_attempt_id(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup, suffix=".fifo", Attributes={
        "FifoQueue": "true", "ContentBasedDeduplication": "true"})
    sqs.send_message(QueueUrl=url, MessageBody="x", MessageGroupId="g")

    def first():
        r = sqs.receive_message(QueueUrl=url, WaitTimeSeconds=1, VisibilityTimeout=300,
                                ReceiveRequestAttemptId="try-1")
        assert r.get("Messages"), "not yet"
        return r["Messages"]

    got = eventually(first)
    retry = sqs.receive_message(QueueUrl=url, WaitTimeSeconds=1, VisibilityTimeout=300,
                                ReceiveRequestAttemptId="try-1").get("Messages", [])
    snapshot.match("retry-same-messages", [m["ReceiptHandle"] == got[0]["ReceiptHandle"] for m in retry])
    standard = make_queue(sqs, names, cleanup, "standard")
    snapshot.outcome("on-a-standard-queue", lambda: sqs.receive_message(
        QueueUrl=standard, ReceiveRequestAttemptId="try-1"))


def test_fifo_attribute_rules(client, names, cleanup, snapshot):
    sqs = client("sqs")
    standard = make_queue(sqs, names, cleanup, "standard")
    snapshot.outcome("scope-on-a-standard-queue", lambda: sqs.set_queue_attributes(
        QueueUrl=standard, Attributes={"DeduplicationScope": "messageGroup"}))
    fifo = make_queue(sqs, names, cleanup, "fifo", suffix=".fifo", Attributes={"FifoQueue": "true"})
    snapshot.error("per-group-needs-message-group-scope", lambda: sqs.set_queue_attributes(
        QueueUrl=fifo, Attributes={"FifoThroughputLimit": "perMessageGroupId", "DeduplicationScope": "queue"}))
    snapshot.error("scope-value", lambda: sqs.set_queue_attributes(
        QueueUrl=fifo, Attributes={"DeduplicationScope": "everywhere"}))
    snapshot.match("high-throughput", sqs.set_queue_attributes(
        QueueUrl=fifo, Attributes={"FifoThroughputLimit": "perMessageGroupId", "DeduplicationScope": "messageGroup"}))
    snapshot.error("fifo-flag-value", lambda: sqs.create_queue(
        QueueName=names("queue", suffix="-bad.fifo"), Attributes={"FifoQueue": "yes"}))


def test_redrive_allow_policy(client, names, cleanup, snapshot):
    sqs = client("sqs")
    dlq = make_queue(sqs, names, cleanup, "queue-dlq", Attributes={
        "RedriveAllowPolicy": json.dumps({"redrivePermission": "denyAll"})})
    arn = queue_arn(sqs, dlq)
    snapshot.error("denied", lambda: sqs.create_queue(QueueName=names("queue"), Attributes={
        "RedrivePolicy": json.dumps({"deadLetterTargetArn": arn, "maxReceiveCount": "5"})}))
    snapshot.error("bad-permission", lambda: sqs.set_queue_attributes(
        QueueUrl=dlq, Attributes={"RedriveAllowPolicy": json.dumps({"redrivePermission": "maybe"})}))
    snapshot.error("byqueue-without-arns", lambda: sqs.set_queue_attributes(
        QueueUrl=dlq, Attributes={"RedriveAllowPolicy": json.dumps({"redrivePermission": "byQueue"})}))


def test_queue_in_another_account(client, names, cleanup, snapshot):
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    other = url.rsplit("/", 2)
    foreign = f"{other[0]}/999999999999/{other[2]}"
    snapshot.outcome("send-to-another-accounts-url", lambda: sqs.send_message(QueueUrl=foreign, MessageBody="x"))
    snapshot.outcome("owner-id", lambda: sqs.get_queue_url(
        QueueName=names("queue"), QueueOwnerAWSAccountId="999999999999"))


def test_setting_an_attribute_back_to_its_default(client, names, cleanup, snapshot):
    """A value changed and set back reads as the default again."""
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup, Attributes={"DelaySeconds": "30"})
    sqs.set_queue_attributes(QueueUrl=url, Attributes={"DelaySeconds": "0"})
    snapshot.match("after", sqs.get_queue_attributes(QueueUrl=url, AttributeNames=["DelaySeconds"]))


def test_an_old_receipt_handle(client, names, cleanup, snapshot, eventually):
    """"If you use an old ReceiptHandle, the request will succeed, but the
    message might not be deleted." Here a handle is the receive's: a delete with
    an earlier receive's handle succeeds and leaves the message (SQS-27)."""
    import time
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup, Attributes={"VisibilityTimeout": "1"})
    sqs.send_message(QueueUrl=url, MessageBody="x")
    old = receive(sqs, eventually, url)[0]["ReceiptHandle"]
    time.sleep(2)
    new = receive(sqs, eventually, url)[0]["ReceiptHandle"]
    snapshot.match("handles-differ", {"differ": old != new})
    snapshot.outcome("delete-with-the-old-handle", lambda: sqs.delete_message(QueueUrl=url, ReceiptHandle=old))
    snapshot.match("after", {"visible": len(sqs.receive_message(QueueUrl=url, WaitTimeSeconds=3).get("Messages", []))})


def test_redrive_policy_without_max_receive_count(client, names, cleanup, snapshot):
    """The model's RedrivePolicy documents "Default: 10" for maxReceiveCount."""
    sqs = client("sqs")
    dlq = make_queue(sqs, names, cleanup, "queue-dlq")
    arn = queue_arn(sqs, dlq)
    out = snapshot.outcome("no-max-receive-count", lambda: sqs.create_queue(
        QueueName=names("queue"), Attributes={"RedrivePolicy": json.dumps({"deadLetterTargetArn": arn})}))
    if isinstance(out, dict):
        cleanup(sqs.delete_queue, QueueUrl=out["QueueUrl"])
        snapshot.match("policy", sqs.get_queue_attributes(QueueUrl=out["QueueUrl"], AttributeNames=["RedrivePolicy"]),
                       opaque=("RedrivePolicy",))


def test_add_permission_statement_limit(client, names, cleanup, snapshot):
    """The model says AddPermission is OverLimit when "the maximum number of
    permissions for the queue is reached" and not what the number is."""
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    first_refused = None
    for i in range(1, 31):
        try:
            sqs.add_permission(QueueUrl=url, Label=f"l{i}", AWSAccountIds=["111122223333"], Actions=["SendMessage"])
        except Exception as e:  # noqa: BLE001 - recorded, not judged
            first_refused = (i, getattr(e, "response", {}).get("Error", {}).get("Code"))
            break
    snapshot.match("first-refusal", {"at": first_refused[0] if first_refused else None,
                                     "code": first_refused[1] if first_refused else None})


def test_get_queue_attributes_without_names(client, names, cleanup, snapshot):
    """"if you don't specify values for this parameter, the request returns
    empty results"."""
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    snapshot.match("no-names", {"attributes": sqs.get_queue_attributes(QueueUrl=url).get("Attributes", {})})
    snapshot.match("all", {"has-arn": "QueueArn" in sqs.get_queue_attributes(
        QueueUrl=url, AttributeNames=["All"])["Attributes"]})


def test_move_task_cancel(client, names, cleanup, snapshot, eventually):
    """"An asynchronous task", with "only one active message movement task ...
    per queue", cancellable "when the current status is RUNNING"."""
    sqs = client("sqs")
    src, dlq, dlq_arn = dlq_pair(sqs, names, cleanup)
    for i in range(3):
        dead_letter(sqs, eventually, src, dlq, f"poison-{i}")
    # One message a second: three messages cannot finish before the cancel.
    task = sqs.start_message_move_task(SourceArn=dlq_arn, MaxNumberOfMessagesPerSecond=1)
    snapshot.match("started", task, opaque=("TaskHandle",))
    running = sqs.list_message_move_tasks(SourceArn=dlq_arn)["Results"][0]
    snapshot.match("running", {"status": running["Status"], "has-handle": "TaskHandle" in running})
    snapshot.outcome("second-start", lambda: sqs.start_message_move_task(SourceArn=dlq_arn))
    cancelled = sqs.cancel_message_move_task(TaskHandle=task["TaskHandle"])
    snapshot.match("cancel", {"moved-is-a-number": isinstance(cancelled["ApproximateNumberOfMessagesMoved"], int)})

    def settled():
        r = sqs.list_message_move_tasks(SourceArn=dlq_arn)["Results"][0]
        assert r["Status"] != "RUNNING" and r["Status"] != "CANCELLING", r
        return r

    final = eventually(settled, timeout=60)
    snapshot.match("final", {"status": final["Status"], "has-handle": "TaskHandle" in final})
    snapshot.error("cancel-again", lambda: sqs.cancel_message_move_task(TaskHandle=task["TaskHandle"]))


def test_queue_type_cannot_change(client, names, cleanup, snapshot):
    """The model, FifoQueue: "You can provide this attribute only during queue
    creation. You can't change it for an existing queue." """
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    snapshot.outcome("fifo-on-a-standard-queue", lambda: sqs.set_queue_attributes(
        QueueUrl=url, Attributes={"FifoQueue": "true"}))
    snapshot.outcome("still-takes-a-plain-send", lambda: sqs.send_message(QueueUrl=url, MessageBody="x"),
                     opaque=("MessageId",))


def test_an_arn_elsewhere(client, names, cleanup, snapshot):
    """A dead-letter queue is one "in this Amazon Web Services account in the
    same Region"; a move task's source that is not there is
    ResourceNotFoundException."""
    sqs = client("sqs")
    dlq = make_queue(sqs, names, cleanup, "queue-dlq")
    arn = queue_arn(sqs, dlq)
    parts = arn.split(":")
    other_account = ":".join(parts[:4] + ["999999999999"] + parts[5:])
    other_service = ":".join(parts[:2] + ["sns"] + parts[3:])
    for label, a in (("another-account", other_account), ("another-service", other_service)):
        snapshot.outcome("move-task-" + label, lambda a=a: sqs.start_message_move_task(SourceArn=a))
        snapshot.outcome("redrive-policy-" + label, lambda a=a: sqs.create_queue(
            QueueName=names("queue"), Attributes={"RedrivePolicy": json.dumps({"deadLetterTargetArn": a, "maxReceiveCount": "3"})}))


def test_content_based_deduplication_id(client, names, cleanup, snapshot, eventually):
    """The model: "Amazon SQS uses a SHA-256 hash to generate the
    MessageDeduplicationId using the body of the message"."""
    import hashlib
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup, suffix=".fifo",
                     Attributes={"FifoQueue": "true", "ContentBasedDeduplication": "true"})
    sqs.send_message(QueueUrl=url, MessageBody="same body", MessageGroupId="g")
    got = receive(sqs, eventually, url, MessageSystemAttributeNames=["MessageDeduplicationId"])
    snapshot.match("generated-id-is-sha256", {
        "matches": got[0]["Attributes"].get("MessageDeduplicationId") == hashlib.sha256(b"same body").hexdigest()})


def test_sequence_number_is_fifo_only(client, names, cleanup, snapshot, eventually):
    """The model, SequenceNumber: "This parameter applies only to FIFO
    (first-in-first-out) queues." A group id on a standard queue is a fair
    queue's, not an ordered one's."""
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    out = sqs.send_message(QueueUrl=url, MessageBody="x", MessageGroupId="tenant-a")
    got = receive(sqs, eventually, url, MessageSystemAttributeNames=["All"])
    snapshot.match("standard", {"send-has-one": "SequenceNumber" in out,
                                "receive-has-one": "SequenceNumber" in got[0]["Attributes"]})


def test_boolean_attribute_spelling(client, names, cleanup, snapshot):
    """The model: "Valid values are true and false"."""
    sqs = client("sqs")
    for v in ("TRUE", "True", "1"):
        snapshot.outcome("content-based-deduplication-" + v, lambda v=v: sqs.create_queue(
            QueueName=names("queue") + ".fifo", Attributes={"FifoQueue": "true", "ContentBasedDeduplication": v}))


def test_message_system_attributes(client, names, cleanup, snapshot):
    """The model: "the only supported message system attribute is
    AWSTraceHeader. Its type must be String"."""
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    snapshot.outcome("another-name", lambda: sqs.send_message(QueueUrl=url, MessageBody="x", MessageSystemAttributes={
        "Other": {"DataType": "String", "StringValue": "x"}}))
    snapshot.outcome("a-number", lambda: sqs.send_message(QueueUrl=url, MessageBody="x", MessageSystemAttributes={
        "AWSTraceHeader": {"DataType": "Number", "StringValue": "1"}}))


def test_list_queues_token_needs_max_results(client, names, cleanup, snapshot):
    """The model: "You must set MaxResults to receive a value for NextToken in
    the response." (Whether a token appears without it needs more than 1,000
    queues; this records the paged half.)"""
    sqs = client("sqs")
    prefix = names("queue")
    for i in range(3):
        cleanup(sqs.delete_queue, QueueUrl=sqs.create_queue(QueueName="%s-%d" % (prefix, i))["QueueUrl"])
    out = sqs.list_queues(QueueNamePrefix=prefix, MaxResults=1)
    snapshot.match("paged", {"count": len(out.get("QueueUrls", [])), "has-token": "NextToken" in out})
    out = sqs.list_queues(QueueNamePrefix=prefix)
    snapshot.match("unpaged", {"count": len(out.get("QueueUrls", [])), "has-token": "NextToken" in out})


def test_error_statuses(client, names, cleanup, snapshot):
    """The model's error shapes: ReceiptHandleIsInvalid and
    ResourceNotFoundException are httpError 404; the JSON protocol carries the
    shape's status."""
    sqs = client("sqs")
    url = make_queue(sqs, names, cleanup)
    snapshot.outcome("a-malformed-receipt-handle", lambda: sqs.delete_message(QueueUrl=url, ReceiptHandle="not-a-handle"))
    snapshot.outcome("a-task-that-is-not-there", lambda: sqs.cancel_message_move_task(TaskHandle="no-such-task"))
