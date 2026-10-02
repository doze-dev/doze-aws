"""SNS through boto3, including the delivery into SQS a real stack depends on."""

import json

# What SNS signs a notification with. Present on both; equal on neither.
SIGNING = ("Timestamp", "Signature", "SigningCertURL", "UnsubscribeURL")


def subscribed_queue(client, names, cleanup, topic_arn, label="queue", **subscribe):
    """A queue that the topic is allowed to deliver to, and subscribed."""
    sqs, sns = client("sqs"), client("sns")
    url = sqs.create_queue(QueueName=names(label))["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)
    arn = sqs.get_queue_attributes(QueueUrl=url, AttributeNames=["QueueArn"])["Attributes"]["QueueArn"]
    sqs.set_queue_attributes(QueueUrl=url, Attributes={"Policy": json.dumps({
        "Version": "2012-10-17",
        "Statement": [{
            "Effect": "Allow", "Principal": {"Service": "sns.amazonaws.com"},
            "Action": "sqs:SendMessage", "Resource": arn,
            "Condition": {"ArnEquals": {"aws:SourceArn": topic_arn}},
        }],
    })})
    sub = sns.subscribe(TopicArn=topic_arn, Protocol="sqs", Endpoint=arn,
                        ReturnSubscriptionArn=True, **subscribe)
    cleanup(sns.unsubscribe, SubscriptionArn=sub["SubscriptionArn"])
    return url, sub


def one_body(client, eventually, url):
    sqs = client("sqs")

    def poll():
        r = sqs.receive_message(QueueUrl=url, WaitTimeSeconds=1)
        assert r.get("Messages"), "nothing delivered yet"
        return r["Messages"][0]["Body"]

    return eventually(poll)


def test_topic_lifecycle(client, names, cleanup, snapshot):
    sns = client("sns")
    name = names("topic")
    created = snapshot.match("create", sns.create_topic(Name=name))
    arn = created["TopicArn"]
    cleanup(sns.delete_topic, TopicArn=arn)

    snapshot.match("create-again", sns.create_topic(Name=name))
    attrs = sns.get_topic_attributes(TopicArn=arn)
    # The default policy is a JSON document inside a string; compare it as one.
    for doc in ("Policy", "EffectiveDeliveryPolicy"):
        if doc in attrs["Attributes"]:
            attrs["Attributes"][doc] = json.loads(attrs["Attributes"][doc])
    snapshot.match("attributes", attrs)

    snapshot.match("set-display-name", sns.set_topic_attributes(
        TopicArn=arn, AttributeName="DisplayName", AttributeValue="Orders"))
    snapshot.match("tag", sns.tag_resource(ResourceArn=arn, Tags=[{"Key": "env", "Value": "dev"}]))
    snapshot.match("tags", sns.list_tags_for_resource(ResourceArn=arn))
    snapshot.match("delete", sns.delete_topic(TopicArn=arn))
    snapshot.match("delete-again", sns.delete_topic(TopicArn=arn))


def test_publish_reaches_a_queue_in_the_envelope(client, names, cleanup, snapshot, eventually):
    sns = client("sns")
    arn = sns.create_topic(Name=names("topic"))["TopicArn"]
    cleanup(sns.delete_topic, TopicArn=arn)
    url, sub = subscribed_queue(client, names, cleanup, arn)
    snapshot.match("subscribe", sub)
    snapshot.match("subscription-attributes",
                   sns.get_subscription_attributes(SubscriptionArn=sub["SubscriptionArn"]))

    snapshot.match("publish", sns.publish(
        TopicArn=arn, Message="shipped", Subject="order",
        MessageAttributes={"kind": {"DataType": "String", "StringValue": "order"}}))
    snapshot.match("envelope", json.loads(one_body(client, eventually, url)), opaque=SIGNING)


def test_raw_delivery_and_a_filter_policy(client, names, cleanup, snapshot, eventually):
    sns, sqs = client("sns"), client("sqs")
    arn = sns.create_topic(Name=names("topic"))["TopicArn"]
    cleanup(sns.delete_topic, TopicArn=arn)
    url, sub = subscribed_queue(client, names, cleanup, arn, Attributes={
        "RawMessageDelivery": "true",
        "FilterPolicy": json.dumps({"kind": ["order"]}),
    })
    snapshot.match("subscription-attributes",
                   sns.get_subscription_attributes(SubscriptionArn=sub["SubscriptionArn"]))

    kind = lambda v: {"kind": {"DataType": "String", "StringValue": v}}
    sns.publish(TopicArn=arn, Message="filtered out", MessageAttributes=kind("refund"))
    sns.publish(TopicArn=arn, Message="let through", MessageAttributes=kind("order"))

    def poll():
        r = sqs.receive_message(QueueUrl=url, WaitTimeSeconds=1, MessageAttributeNames=["All"])
        assert r.get("Messages"), "nothing delivered yet"
        return r["Messages"]

    # Raw: the body is the message, and the attributes arrive as SQS attributes.
    snapshot.match("delivered", eventually(poll), opaque=("ReceiptHandle",))


def test_refusals(client, names, cleanup, snapshot, target):
    sns = client("sns")
    absent = f"arn:aws:sns:{target.region}:{target.account}:{names('absent')}"
    snapshot.error("publish-to-no-topic", lambda: sns.publish(TopicArn=absent, Message="x"))
    snapshot.error("attributes-of-no-topic", lambda: sns.get_topic_attributes(TopicArn=absent))
    snapshot.error("bad-topic-name", lambda: sns.create_topic(Name="not a topic name!"))
    snapshot.error("not-an-arn", lambda: sns.publish(TopicArn="nonsense", Message="x"))

    arn = sns.create_topic(Name=names("topic"))["TopicArn"]
    cleanup(sns.delete_topic, TopicArn=arn)
    snapshot.error("empty-message", lambda: sns.publish(TopicArn=arn, Message=""))
    snapshot.error("bad-protocol", lambda: sns.subscribe(
        TopicArn=arn, Protocol="carrier-pigeon", Endpoint="x"))
    snapshot.error("bad-filter-policy", lambda: sns.subscribe(
        TopicArn=arn, Protocol="sqs",
        Endpoint=f"arn:aws:sqs:{target.region}:{target.account}:q",
        Attributes={"FilterPolicy": "{not json"}))
    snapshot.error("bad-attribute", lambda: sns.set_topic_attributes(
        TopicArn=arn, AttributeName="NoSuchAttribute", AttributeValue="x"))


def test_create_again_must_agree(client, names, cleanup, snapshot):
    """CreateTopic is idempotent for the same definition. What it does with a
    different one is recorded rather than asserted."""
    sns = client("sns")
    name = names("topic")
    arn = sns.create_topic(Name=name, Attributes={"DisplayName": "Orders"},
                           Tags=[{"Key": "env", "Value": "dev"}])["TopicArn"]
    cleanup(sns.delete_topic, TopicArn=arn)
    snapshot.outcome("same", lambda: sns.create_topic(
        Name=name, Attributes={"DisplayName": "Orders"}, Tags=[{"Key": "env", "Value": "dev"}]))
    snapshot.outcome("no-attributes", lambda: sns.create_topic(Name=name))
    snapshot.outcome("different-attribute", lambda: sns.create_topic(
        Name=name, Attributes={"DisplayName": "Returns"}))
    snapshot.outcome("different-tags", lambda: sns.create_topic(
        Name=name, Tags=[{"Key": "env", "Value": "prod"}]))
    snapshot.match("display-name-after", sns.get_topic_attributes(
        TopicArn=arn)["Attributes"].get("DisplayName"))


def test_publish_batch(client, names, cleanup, snapshot, eventually):
    sns, sqs = client("sns"), client("sqs")
    arn = sns.create_topic(Name=names("topic"))["TopicArn"]
    cleanup(sns.delete_topic, TopicArn=arn)
    url, _ = subscribed_queue(client, names, cleanup, arn, Attributes={"RawMessageDelivery": "true"})

    snapshot.match("publish", sns.publish_batch(TopicArn=arn, PublishBatchRequestEntries=[
        {"Id": "a", "Message": "one"}, {"Id": "b", "Message": "two"}]))
    bodies = []

    def drain():
        r = sqs.receive_message(QueueUrl=url, MaxNumberOfMessages=10, WaitTimeSeconds=1)
        for m in r.get("Messages", []):
            bodies.append(m["Body"])
            sqs.delete_message(QueueUrl=url, ReceiptHandle=m["ReceiptHandle"])
        assert len(bodies) >= 2, f"{bodies} so far"
        return sorted(bodies)

    snapshot.match("delivered", eventually(drain))
    snapshot.error("repeated-ids", lambda: sns.publish_batch(TopicArn=arn, PublishBatchRequestEntries=[
        {"Id": "a", "Message": "one"}, {"Id": "a", "Message": "two"}]))
    snapshot.error("empty", lambda: sns.publish_batch(TopicArn=arn, PublishBatchRequestEntries=[]))
    snapshot.error("eleven", lambda: sns.publish_batch(TopicArn=arn, PublishBatchRequestEntries=[
        {"Id": f"m{i}", "Message": "x"} for i in range(11)]))


def test_fifo_topic_delivers_to_a_fifo_queue(client, names, cleanup, snapshot, eventually):
    sns, sqs = client("sns"), client("sqs")
    made = snapshot.match("create", sns.create_topic(
        Name=names("topic", suffix=".fifo"),
        Attributes={"FifoTopic": "true", "ContentBasedDeduplication": "true"}))
    arn = made["TopicArn"]
    cleanup(sns.delete_topic, TopicArn=arn)

    url = sqs.create_queue(QueueName=names("queue", suffix=".fifo"),
                           Attributes={"FifoQueue": "true"})["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)
    qarn = sqs.get_queue_attributes(QueueUrl=url, AttributeNames=["QueueArn"])["Attributes"]["QueueArn"]
    sqs.set_queue_attributes(QueueUrl=url, Attributes={"Policy": json.dumps({
        "Version": "2012-10-17",
        "Statement": [{"Effect": "Allow", "Principal": {"Service": "sns.amazonaws.com"},
                       "Action": "sqs:SendMessage", "Resource": qarn,
                       "Condition": {"ArnEquals": {"aws:SourceArn": arn}}}],
    })})
    sub = sns.subscribe(TopicArn=arn, Protocol="sqs", Endpoint=qarn, ReturnSubscriptionArn=True,
                        Attributes={"RawMessageDelivery": "true"})
    cleanup(sns.unsubscribe, SubscriptionArn=sub["SubscriptionArn"])

    snapshot.match("publish", sns.publish(TopicArn=arn, Message="first", MessageGroupId="g"),
                   opaque=("SequenceNumber",))
    snapshot.error("no-group-id", lambda: sns.publish(TopicArn=arn, Message="x"))

    def poll():
        r = sqs.receive_message(QueueUrl=url, WaitTimeSeconds=1,
                                MessageSystemAttributeNames=["MessageGroupId"])
        assert r.get("Messages"), "nothing delivered yet"
        return [(m["Body"], m["Attributes"]) for m in r["Messages"]]

    snapshot.match("delivered", eventually(poll))
    snapshot.error("fifo-name-without-the-attribute", lambda: sns.create_topic(
        Name=names("plain", suffix=".fifo")))
    snapshot.error("the-attribute-without-the-fifo-name", lambda: sns.create_topic(
        Name=names("plain"), Attributes={"FifoTopic": "true"}))
