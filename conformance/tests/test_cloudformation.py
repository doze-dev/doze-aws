"""CloudFormation through boto3: a template in, real resources out.

Every resource is given its name through a parameter, so the stack's physical
ids are names this run chose and can be compared.
"""

import json

FAST = {"Delay": 1, "MaxAttempts": 240}

# Clocks, and a stack id that ends in a uuid the normalizer numbers by itself.
CLOCK = ("CreationTime", "LastUpdatedTime", "DeletionTime", "Timestamp", "LastUpdatedTimestamp",
         "LastCheckTimestamp", "ExecutionTime")
# Drift has never been checked on a stack this new, on either side; AWS says so
# with a block of its own.
DRIFT = ("DriftInformation",)

TEMPLATE = {
    "AWSTemplateFormatVersion": "2010-09-09",
    "Description": "conformance stack",
    "Parameters": {
        "QueueName": {"Type": "String"},
        "TopicName": {"Type": "String"},
        "Visibility": {"Type": "Number", "Default": 30, "MinValue": 0, "MaxValue": 43200},
    },
    "Resources": {
        "Queue": {"Type": "AWS::SQS::Queue", "Properties": {
            "QueueName": {"Ref": "QueueName"}, "VisibilityTimeout": {"Ref": "Visibility"}}},
        "Topic": {"Type": "AWS::SNS::Topic", "Properties": {"TopicName": {"Ref": "TopicName"}}},
        "Pointer": {"Type": "AWS::SSM::Parameter", "Properties": {
            "Name": {"Fn::Sub": "/${QueueName}/queue-arn"}, "Type": "String",
            "Value": {"Fn::GetAtt": ["Queue", "Arn"]}}},
    },
    "Outputs": {
        "QueueUrl": {"Value": {"Ref": "Queue"}},
        "QueueArn": {"Value": {"Fn::GetAtt": ["Queue", "Arn"]}},
        "TopicArn": {"Value": {"Ref": "Topic"}, "Export": {"Name": {"Fn::Sub": "${AWS::StackName}-topic"}}},
        "Joined": {"Value": {"Fn::Join": ["/", [{"Ref": "AWS::Region"}, {"Ref": "QueueName"}]]}},
    },
}


def params(**kw):
    return [{"ParameterKey": k, "ParameterValue": str(v)} for k, v in kw.items()]


def stack(cfn, names, cleanup, template=TEMPLATE, label="stack", **kw):
    name = names(label)
    made = cfn.create_stack(StackName=name, TemplateBody=json.dumps(template), **kw)

    def delete():
        cfn.delete_stack(StackName=name)
        cfn.get_waiter("stack_delete_complete").wait(StackName=name, WaiterConfig=FAST)

    cleanup(delete)
    return name, made


def test_stack_lifecycle(client, names, cleanup, snapshot):
    cfn, sqs, ssm = client("cloudformation"), client("sqs"), client("ssm")
    queue, topic = names("queue"), names("topic")
    name, made = stack(cfn, names, cleanup, Parameters=params(QueueName=queue, TopicName=topic),
                       Tags=[{"Key": "env", "Value": "dev"}])
    snapshot.match("create", made)
    cfn.get_waiter("stack_create_complete").wait(StackName=name, WaiterConfig=FAST)

    snapshot.match("describe", cfn.describe_stacks(StackName=name),
                   opaque=CLOCK, drop=DRIFT, unordered=("Outputs", "Parameters"))
    snapshot.match("resources", cfn.list_stack_resources(StackName=name),
                   opaque=CLOCK, drop=DRIFT, unordered=("StackResourceSummaries",))
    snapshot.match("one-resource", cfn.describe_stack_resource(
        StackName=name, LogicalResourceId="Queue"), opaque=CLOCK, drop=DRIFT)
    snapshot.match("template", cfn.get_template(StackName=name))
    snapshot.match("exports", [e for e in cfn.list_exports()["Exports"] if e["Name"] == f"{name}-topic"])

    # The resources are real: the queue has the timeout, the parameter the ARN.
    url = sqs.get_queue_url(QueueName=queue)["QueueUrl"]
    snapshot.match("the-queue", sqs.get_queue_attributes(
        QueueUrl=url, AttributeNames=["VisibilityTimeout"]))
    snapshot.match("the-parameter", ssm.get_parameter(Name=f"/{queue}/queue-arn")["Parameter"]["Value"])

    snapshot.match("update", cfn.update_stack(
        StackName=name, UsePreviousTemplate=True,
        Parameters=params(QueueName=queue, TopicName=topic, Visibility=45)))
    cfn.get_waiter("stack_update_complete").wait(StackName=name, WaiterConfig=FAST)
    snapshot.match("the-queue-after", sqs.get_queue_attributes(
        QueueUrl=url, AttributeNames=["VisibilityTimeout"]))
    snapshot.error("update-with-no-change", lambda: cfn.update_stack(
        StackName=name, UsePreviousTemplate=True,
        Parameters=params(QueueName=queue, TopicName=topic, Visibility=45)))
    events = cfn.describe_stack_events(StackName=name)["StackEvents"]
    snapshot.match("stack-level-events", [
        e["ResourceStatus"] for e in reversed(events) if e["LogicalResourceId"] == name])

    snapshot.match("delete", cfn.delete_stack(StackName=name))
    cfn.get_waiter("stack_delete_complete").wait(StackName=name, WaiterConfig=FAST)
    snapshot.error("describe-deleted", lambda: cfn.describe_stacks(StackName=name))
    snapshot.error("the-queue-is-gone", lambda: sqs.get_queue_url(QueueName=queue))


def test_change_set(client, names, cleanup, snapshot):
    cfn = client("cloudformation")
    queue, topic = names("queue"), names("topic")
    name, change = names("stack"), names("change")

    def delete():
        cfn.delete_stack(StackName=name)
        cfn.get_waiter("stack_delete_complete").wait(StackName=name, WaiterConfig=FAST)

    cleanup(delete)
    made = cfn.create_change_set(
        StackName=name, ChangeSetName=change, ChangeSetType="CREATE",
        TemplateBody=json.dumps(TEMPLATE), Parameters=params(QueueName=queue, TopicName=topic))
    snapshot.match("create", made)
    cfn.get_waiter("change_set_create_complete").wait(
        StackName=name, ChangeSetName=change, WaiterConfig=FAST)

    snapshot.match("describe", cfn.describe_change_set(StackName=name, ChangeSetName=change),
                   opaque=CLOCK, unordered=("Changes", "Parameters"))
    # A stack that exists only as an unexecuted change set.
    snapshot.match("stack-in-review", [s["StackStatus"] for s in cfn.describe_stacks(
        StackName=name)["Stacks"]])
    snapshot.match("execute", cfn.execute_change_set(StackName=name, ChangeSetName=change))
    cfn.get_waiter("stack_create_complete").wait(StackName=name, WaiterConfig=FAST)
    snapshot.match("after", [s["StackStatus"] for s in cfn.describe_stacks(StackName=name)["Stacks"]])
    snapshot.error("execute-again", lambda: cfn.execute_change_set(
        StackName=name, ChangeSetName=change))


def test_a_resource_that_fails_rolls_the_stack_back(client, names, cleanup, snapshot, eventually):
    cfn, sqs = client("cloudformation"), client("sqs")
    queue = names("queue")
    template = {"Resources": {
        "Good": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": queue}},
        # A timeout SQS refuses, so the resource fails after Good was made.
        "Bad": {"Type": "AWS::SQS::Queue", "DependsOn": "Good",
                "Properties": {"VisibilityTimeout": 99999}},
    }}
    name = names("stack")

    def delete():
        cfn.delete_stack(StackName=name)
        cfn.get_waiter("stack_delete_complete").wait(StackName=name, WaiterConfig=FAST)

    cleanup(delete)
    # AWS accepts the call and fails the stack afterwards; a target that does
    # the work before answering may refuse here instead. Either way the stack
    # is left to be read, and what it says is what is compared.
    snapshot.outcome("create", lambda: cfn.create_stack(
        StackName=name, TemplateBody=json.dumps(template)))

    def settled():
        s = cfn.describe_stacks(StackName=name)["Stacks"][0]
        assert not s["StackStatus"].endswith("IN_PROGRESS"), s["StackStatus"]
        return s["StackStatus"]

    snapshot.match("status", eventually(settled, timeout=300, every=2))
    snapshot.error("the-good-queue-was-taken-back", lambda: sqs.get_queue_url(QueueName=queue))
    events = cfn.describe_stack_events(StackName=name)["StackEvents"]
    statuses = {(e["LogicalResourceId"], e["ResourceStatus"]) for e in events}
    # Whatever else the trail says, it must say that Bad failed and must
    # never say it was created.
    assert ("Bad", "CREATE_FAILED") in statuses, sorted(statuses)
    assert ("Bad", "CREATE_COMPLETE") not in statuses, sorted(statuses)
    snapshot.match("what-failed", sorted(s for s in statuses if s[1].endswith("FAILED")))


def test_validate_template(client, snapshot):
    cfn = client("cloudformation")
    snapshot.match("valid", cfn.validate_template(TemplateBody=json.dumps(TEMPLATE)),
                   unordered=("Parameters",))
    yaml = "Resources:\n  Q:\n    Type: AWS::SQS::Queue\n    Properties:\n      QueueName: !Sub '${AWS::StackName}-q'\n"
    snapshot.match("yaml-with-short-forms", cfn.validate_template(TemplateBody=yaml))
    snapshot.error("not-a-template", lambda: cfn.validate_template(TemplateBody="{nope"))
    snapshot.error("no-resources", lambda: cfn.validate_template(
        TemplateBody=json.dumps({"Description": "empty"})))
    snapshot.error("ref-to-nothing", lambda: cfn.validate_template(TemplateBody=json.dumps({
        "Resources": {"Q": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Ref": "Ghost"}}}}})))
    snapshot.error("getatt-of-nothing", lambda: cfn.validate_template(TemplateBody=json.dumps({
        "Resources": {"Q": {"Type": "AWS::SQS::Queue"}},
        "Outputs": {"O": {"Value": {"Fn::GetAtt": ["Ghost", "Arn"]}}}})))
    snapshot.error("resource-without-a-type", lambda: cfn.validate_template(
        TemplateBody=json.dumps({"Resources": {"Q": {"Properties": {}}}})))


def test_refusals(client, names, cleanup, snapshot):
    cfn = client("cloudformation")
    absent = names("absent")
    body = json.dumps(TEMPLATE)
    snapshot.error("describe-absent", lambda: cfn.describe_stacks(StackName=absent))
    snapshot.error("update-absent", lambda: cfn.update_stack(StackName=absent, TemplateBody=body))
    snapshot.error("resources-of-absent", lambda: cfn.list_stack_resources(StackName=absent))
    # Deleting a stack that is not there is not an error.
    snapshot.match("delete-absent", cfn.delete_stack(StackName=absent))

    for leaf in ("missing-param", "bad-param", "unknown-param", "both-bodies"):
        cleanup(cfn.delete_stack, StackName=names(leaf))
    snapshot.error("missing-parameter", lambda: cfn.create_stack(
        StackName=names("missing-param"), TemplateBody=body, Parameters=params(QueueName="q")))
    snapshot.error("parameter-out-of-range", lambda: cfn.create_stack(
        StackName=names("bad-param"), TemplateBody=body,
        Parameters=params(QueueName="q", TopicName="t", Visibility=50000)))
    snapshot.error("parameter-not-in-template", lambda: cfn.create_stack(
        StackName=names("unknown-param"), TemplateBody=body,
        Parameters=params(QueueName="q", TopicName="t", Nope="x")))
    snapshot.error("bad-stack-name", lambda: cfn.create_stack(
        StackName="has_underscore", TemplateBody=body, Parameters=params(QueueName="q", TopicName="t")))
    snapshot.error("no-template", lambda: cfn.create_stack(StackName=names("both-bodies")))

    name, _ = stack(cfn, names, cleanup, Parameters=params(
        QueueName=names("queue"), TopicName=names("topic")))
    cfn.get_waiter("stack_create_complete").wait(StackName=name, WaiterConfig=FAST)
    snapshot.error("create-twice", lambda: cfn.create_stack(
        StackName=name, TemplateBody=body,
        Parameters=params(QueueName=names("queue"), TopicName=names("topic"))))
    snapshot.error("absent-resource", lambda: cfn.describe_stack_resource(
        StackName=name, LogicalResourceId="Ghost"))
    snapshot.error("absent-change-set", lambda: cfn.describe_change_set(
        StackName=name, ChangeSetName=absent))
