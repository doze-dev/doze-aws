"""Lambda through boto3: real code, really run.

The functions are Python. On AWS that is the python3.12 runtime; on doze-aws
it is whatever python3 the host has, which is why the handlers use nothing
past the standard library.
"""

import json

from harness.helpers import zip_bytes

BASIC = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
SQS_EXEC = "arn:aws:iam::aws:policy/service-role/AWSLambdaSQSQueueExecutionRole"

ECHO = '''
import os

def handler(event, context):
    if event.get("fail"):
        raise ValueError("asked to fail")
    return {"echo": event, "greeting": os.environ.get("GREETING", "unset"),
            "function": context.function_name, "version": context.function_version}
'''

FAST = {"Delay": 1, "MaxAttempts": 120}

# Clock readings and where AWS keeps the code: not ours to match.
MINTED = ("LastModified", "Location", "RepositoryType")
# Lifecycle the two sides move through at different speeds. What a function
# settles into is compared; how long it spent Pending is not.
SETTLING = ("State", "StateReason", "StateReasonCode", "LastUpdateStatus",
            "LastUpdateStatusReason", "LastUpdateStatusReasonCode")
# The exact runtime build AWS happens to be on this week.
RUNTIME_BUILD = ("RuntimeVersionConfig",)


def function(lam, names, cleanup, role, source=ECHO, label="fn", **kw):
    name = names(label)
    made = lam.create_function(
        FunctionName=name, Runtime="python3.12", Role=role, Handler="index.handler",
        Code={"ZipFile": zip_bytes({"index.py": source})}, Timeout=10, **kw)
    cleanup(lam.delete_function, FunctionName=name)
    lam.get_waiter("function_active_v2").wait(FunctionName=name, WaiterConfig=FAST)
    return name, made


def invoke(lam, name, payload, **kw):
    r = lam.invoke(FunctionName=name, Payload=json.dumps(payload).encode(), **kw)
    r["Payload"] = json.loads(r["Payload"].read() or b"null")
    return r


def test_function_lifecycle_and_invoke(client, names, cleanup, snapshot, service_role):
    lam = client("lambda")
    name, made = function(lam, names, cleanup, service_role("lambda", BASIC),
                          Description="conformance", Environment={"Variables": {"GREETING": "hello"}})
    snapshot.match("create", made, opaque=MINTED, drop=SETTLING + RUNTIME_BUILD)
    snapshot.match("configuration", lam.get_function_configuration(FunctionName=name),
                   opaque=MINTED, drop=RUNTIME_BUILD)
    got = lam.get_function(FunctionName=name)
    snapshot.match("get", got, opaque=MINTED, drop=RUNTIME_BUILD)

    snapshot.match("invoke", invoke(lam, name, {"n": 1}))
    snapshot.match("invoke-fails", invoke(lam, name, {"fail": True}), drop=("stackTrace", "requestId"))
    snapshot.match("invoke-async", lam.invoke(
        FunctionName=name, InvocationType="Event", Payload=b"{}"), drop=("Payload",))
    snapshot.match("invoke-dry-run", lam.invoke(
        FunctionName=name, InvocationType="DryRun", Payload=b"{}"), drop=("Payload",))

    snapshot.match("update-configuration", lam.update_function_configuration(
        FunctionName=name, Environment={"Variables": {"GREETING": "goodbye"}}, MemorySize=256),
        opaque=MINTED, drop=SETTLING + RUNTIME_BUILD)
    lam.get_waiter("function_updated_v2").wait(FunctionName=name, WaiterConfig=FAST)
    snapshot.match("invoke-after-update", invoke(lam, name, {"n": 2}))

    snapshot.match("delete", lam.delete_function(FunctionName=name))
    snapshot.error("get-deleted", lambda: lam.get_function(FunctionName=name))


def test_versions_and_aliases(client, names, cleanup, snapshot, service_role):
    lam = client("lambda")
    name, _ = function(lam, names, cleanup, service_role("lambda", BASIC),
                       Environment={"Variables": {"GREETING": "one"}})

    v1 = snapshot.match("publish-v1", lam.publish_version(FunctionName=name, Description="first"),
                        opaque=MINTED, drop=SETTLING + RUNTIME_BUILD)
    snapshot.match("publish-unchanged", lam.publish_version(FunctionName=name),
                   opaque=MINTED, drop=SETTLING + RUNTIME_BUILD)

    lam.update_function_configuration(
        FunctionName=name, Environment={"Variables": {"GREETING": "two"}})
    lam.get_waiter("function_updated_v2").wait(FunctionName=name, WaiterConfig=FAST)
    snapshot.match("publish-v2", lam.publish_version(FunctionName=name),
                   opaque=MINTED, drop=SETTLING + RUNTIME_BUILD)

    # A published version is frozen: v1 still says what it said.
    snapshot.match("invoke-v1", invoke(lam, name, {}, Qualifier=v1["Version"]))
    snapshot.match("invoke-latest", invoke(lam, name, {}))

    snapshot.match("create-alias", lam.create_alias(
        FunctionName=name, Name="live", FunctionVersion="1"))
    snapshot.match("invoke-alias", invoke(lam, name, {}, Qualifier="live"))
    snapshot.match("repoint-alias", lam.update_alias(
        FunctionName=name, Name="live", FunctionVersion="2"))
    snapshot.match("invoke-alias-repointed", invoke(lam, name, {}, Qualifier="live"))
    snapshot.match("versions", [v["Version"] for v in lam.list_versions_by_function(
        FunctionName=name)["Versions"]])
    snapshot.match("aliases", lam.list_aliases(FunctionName=name))

    snapshot.error("alias-to-absent-version", lambda: lam.create_alias(
        FunctionName=name, Name="ghost", FunctionVersion="9"))
    snapshot.error("alias-twice", lambda: lam.create_alias(
        FunctionName=name, Name="live", FunctionVersion="1"))
    snapshot.error("change-a-published-version", lambda: lam.update_function_configuration(
        FunctionName=f"{name}:1", MemorySize=512))
    snapshot.match("delete-alias", lam.delete_alias(FunctionName=name, Name="live"))
    snapshot.match("delete-v1", lam.delete_function(FunctionName=name, Qualifier="1"))


def test_permissions(client, names, cleanup, snapshot, service_role, target):
    lam = client("lambda")
    name, _ = function(lam, names, cleanup, service_role("lambda", BASIC))
    snapshot.error("no-policy-yet", lambda: lam.get_policy(FunctionName=name))

    added = lam.add_permission(
        FunctionName=name, StatementId="sns", Action="lambda:InvokeFunction",
        Principal="sns.amazonaws.com",
        SourceArn=f"arn:aws:sns:{target.region}:{target.account}:{names('topic')}")
    snapshot.match("add", json.loads(added["Statement"]))
    snapshot.match("policy", json.loads(lam.get_policy(FunctionName=name)["Policy"]))
    snapshot.error("add-twice", lambda: lam.add_permission(
        FunctionName=name, StatementId="sns", Action="lambda:InvokeFunction",
        Principal="sns.amazonaws.com"))
    snapshot.match("remove", lam.remove_permission(FunctionName=name, StatementId="sns"))
    snapshot.error("remove-again", lambda: lam.remove_permission(
        FunctionName=name, StatementId="sns"))


def test_a_queue_drives_the_function(client, names, cleanup, snapshot, service_role, eventually):
    """An event source mapping, proved by what the function logs: the message
    body, which only an invocation with that record could have printed."""
    lam, sqs, logs = client("lambda"), client("sqs"), client("logs")
    source = '''
def handler(event, context):
    for record in event["Records"]:
        print("CONSUMED", record["body"], record["eventSource"])
'''
    name, _ = function(lam, names, cleanup, service_role("lambda", BASIC, SQS_EXEC), source)
    cleanup(logs.delete_log_group, logGroupName=f"/aws/lambda/{name}")
    url = sqs.create_queue(QueueName=names("queue"))["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)
    arn = sqs.get_queue_attributes(QueueUrl=url, AttributeNames=["QueueArn"])["Attributes"]["QueueArn"]

    made = lam.create_event_source_mapping(FunctionName=name, EventSourceArn=arn, BatchSize=1)
    cleanup(lam.delete_event_source_mapping, UUID=made["UUID"])
    snapshot.match("create-mapping", made, opaque=MINTED, drop=("State", "StateTransitionReason"))

    def enabled():
        got = lam.get_event_source_mapping(UUID=made["UUID"])
        assert got["State"] == "Enabled", got["State"]
        return got

    snapshot.match("mapping", eventually(enabled, timeout=120), opaque=MINTED)
    sqs.send_message(QueueUrl=url, MessageBody="order-42")

    def logged():
        try:
            r = logs.filter_log_events(logGroupName=f"/aws/lambda/{name}", filterPattern="CONSUMED")
        except logs.exceptions.ResourceNotFoundException:
            raise AssertionError("the function has not logged yet")
        assert r["events"], "no CONSUMED line yet"
        return [e["message"].strip() for e in r["events"]]

    snapshot.match("consumed", eventually(logged, timeout=120))
    # A message the function handled is deleted from the queue for it.
    def drained():
        r = sqs.receive_message(QueueUrl=url, WaitTimeSeconds=1)
        assert not r.get("Messages"), "still on the queue"
        return True

    snapshot.match("queue-is-empty", eventually(drained, timeout=60))


def test_refusals(client, names, cleanup, snapshot, service_role, target):
    lam = client("lambda")
    absent = names("absent")
    role = service_role("lambda", BASIC)
    code = {"ZipFile": zip_bytes({"index.py": ECHO})}
    snapshot.error("get-absent", lambda: lam.get_function(FunctionName=absent))
    snapshot.error("invoke-absent", lambda: lam.invoke(FunctionName=absent))
    snapshot.error("delete-absent", lambda: lam.delete_function(FunctionName=absent))
    snapshot.error("absent-mapping", lambda: lam.get_event_source_mapping(
        UUID="00000000-0000-4000-8000-000000000000"))

    for leaf in ("bad-runtime", "not-a-zip", "bad-role"):
        cleanup(lam.delete_function, FunctionName=names(leaf))
    snapshot.error("bad-runtime", lambda: lam.create_function(
        FunctionName=names("bad-runtime"), Runtime="cobol85", Role=role,
        Handler="index.handler", Code=code))
    snapshot.error("not-a-zip", lambda: lam.create_function(
        FunctionName=names("not-a-zip"), Runtime="python3.12", Role=role,
        Handler="index.handler", Code={"ZipFile": b"this is not a zip"}))
    snapshot.error("role-is-not-an-arn", lambda: lam.create_function(
        FunctionName=names("bad-role"), Runtime="python3.12", Role="not-an-arn",
        Handler="index.handler", Code=code))

    name, _ = function(lam, names, cleanup, role)
    snapshot.error("create-twice", lambda: lam.create_function(
        FunctionName=name, Runtime="python3.12", Role=role, Handler="index.handler", Code=code))
    snapshot.error("absent-qualifier", lambda: lam.invoke(FunctionName=name, Qualifier="9"))
    snapshot.error("payload-not-json", lambda: lam.invoke(FunctionName=name, Payload=b"{not json"))
    # Fifteen minutes was the ceiling for years; the current service model
    # allows more. Whichever AWS does with 901, do that.
    snapshot.outcome("timeout-past-fifteen-minutes", lambda: lam.update_function_configuration(
        FunctionName=name, Timeout=901), opaque=MINTED, drop=SETTLING + RUNTIME_BUILD)
    snapshot.error("timeout-of-a-day", lambda: lam.update_function_configuration(
        FunctionName=name, Timeout=86400))
    snapshot.error("mapping-to-absent-queue", lambda: lam.create_event_source_mapping(
        FunctionName=name,
        EventSourceArn=f"arn:aws:sqs:{target.region}:{target.account}:{absent}"))
