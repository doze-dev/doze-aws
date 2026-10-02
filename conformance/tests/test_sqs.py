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
