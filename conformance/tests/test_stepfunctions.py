"""Step Functions through boto3.

Most of this is the States Language itself: a definition and an input go in,
an output comes out, and nothing but the interpreter is involved. That makes
it the cleanest comparison in the suite — no clocks, no ids, no delivery.
"""

import json

# Tokens and clocks. The ARNs are made of names this run chose, and are compared.
MINTED = ("startDate", "stopDate", "creationDate", "updateDate", "timestamp", "redriveDate")


def machine(sfn, names, cleanup, role, definition, label="machine", **kw):
    made = sfn.create_state_machine(
        name=names(label), definition=json.dumps(definition), roleArn=role, **kw)
    cleanup(sfn.delete_state_machine, stateMachineArn=made["stateMachineArn"])
    return made["stateMachineArn"], made


def run(sfn, names, eventually, arn, payload, label="run"):
    """Start an execution and wait for it to stop."""
    started = sfn.start_execution(stateMachineArn=arn, name=names(label), input=json.dumps(payload))

    def finished():
        d = sfn.describe_execution(executionArn=started["executionArn"])
        assert d["status"] != "RUNNING", "still running"
        return d

    d = eventually(finished, timeout=120)
    for doc in ("input", "output"):
        if doc in d:
            d[doc] = json.loads(d[doc])
    return started, d


def test_machine_lifecycle_and_history(client, names, cleanup, snapshot, service_role, eventually):
    sfn = client("stepfunctions")
    definition = {
        "Comment": "conformance", "StartAt": "Greet",
        "States": {"Greet": {"Type": "Pass", "Parameters": {"hello.$": "$.who"}, "End": True}},
    }
    arn, made = machine(sfn, names, cleanup, service_role("states"), definition)
    snapshot.match("create", made, opaque=MINTED)
    described = sfn.describe_state_machine(stateMachineArn=arn)
    described["definition"] = json.loads(described["definition"])
    snapshot.match("describe", described, opaque=MINTED, drop=("revisionId",))

    started, done = run(sfn, names, eventually, arn, {"who": "world"})
    snapshot.match("start", started, opaque=MINTED)
    snapshot.match("done", done, opaque=MINTED, drop=("redriveCount", "redriveStatus", "redriveStatusReason"))
    history = sfn.get_execution_history(executionArn=started["executionArn"])
    snapshot.match("history", [e["type"] for e in history["events"]])
    snapshot.match("history-links", [(e["id"], e["previousEventId"]) for e in history["events"]])
    snapshot.match("list", [e["name"] for e in sfn.list_executions(stateMachineArn=arn)["executions"]])

    snapshot.match("update", sfn.update_state_machine(
        stateMachineArn=arn, definition=json.dumps({**definition, "Comment": "changed"})),
        opaque=MINTED, drop=("revisionId",))
    snapshot.match("delete", sfn.delete_state_machine(stateMachineArn=arn))


def test_the_states_language(client, names, cleanup, snapshot, service_role, eventually):
    """One machine per feature, each a pure function of its input."""
    sfn = client("stepfunctions")
    role = service_role("states")
    machines = {
        "paths": ({
            "StartAt": "Shape", "States": {"Shape": {
                "Type": "Pass", "InputPath": "$.order",
                "Parameters": {"id.$": "$.id", "first.$": "$.items[0]", "count.$": "States.ArrayLength($.items)"},
                "ResultPath": "$.summary", "OutputPath": "$.summary", "End": True}},
        }, {"order": {"id": 7, "items": ["book", "pen"]}, "noise": True}),
        "result-path-merges": ({
            "StartAt": "Stamp", "States": {"Stamp": {
                "Type": "Pass", "Result": {"ok": True}, "ResultPath": "$.check", "End": True}},
        }, {"kept": 1}),
        "choice": ({
            "StartAt": "Size", "States": {
                "Size": {"Type": "Choice", "Choices": [
                    {"And": [{"Variable": "$.total", "NumericGreaterThanEquals": 100},
                             {"Variable": "$.tier", "StringEquals": "gold"}], "Next": "Big"},
                    {"Variable": "$.total", "NumericLessThan": 10, "Next": "Small"},
                    {"Not": {"Variable": "$.coupon", "IsPresent": True}, "Next": "Plain"}],
                    "Default": "Other"},
                "Big": {"Type": "Pass", "Result": "big", "End": True},
                "Small": {"Type": "Pass", "Result": "small", "End": True},
                "Plain": {"Type": "Pass", "Result": "plain", "End": True},
                "Other": {"Type": "Pass", "Result": "other", "End": True}},
        }, {"total": 250, "tier": "gold"}),
        "intrinsics": ({
            "StartAt": "Compute", "States": {"Compute": {"Type": "Pass", "End": True, "Parameters": {
                "format.$": "States.Format('{}-{}', $.a, $.b)",
                "array.$": "States.Array($.a, $.b, 3)",
                "add.$": "States.MathAdd($.n, 5)",
                "split.$": "States.StringSplit($.csv, ',')",
                "to_string.$": "States.JsonToString($.obj)",
                "to_json.$": "States.StringToJson($.text)",
                "contains.$": "States.ArrayContains($.list, 2)",
                "range.$": "States.ArrayRange(1, 9, 3)",
                "partition.$": "States.ArrayPartition($.list, 2)",
                "unique.$": "States.ArrayUnique($.dupes)",
                "item.$": "States.ArrayGetItem($.list, 1)",
                "b64.$": "States.Base64Encode($.a)",
                "hash.$": "States.Hash($.a, 'SHA-256')",
                "merge.$": "States.JsonMerge($.obj, $.other, false)"}}},
        }, {"a": "x", "b": "y", "n": 37, "csv": "p,q,r", "obj": {"k": 1}, "other": {"j": 2},
            "text": "{\"z\": [1, 2]}", "list": [1, 2, 3], "dupes": [1, 1, 2]}),
        "map": ({
            "StartAt": "Each", "States": {"Each": {
                "Type": "Map", "ItemsPath": "$.items", "MaxConcurrency": 1,
                "ItemSelector": {"value.$": "$$.Map.Item.Value", "index.$": "$$.Map.Item.Index"},
                "ItemProcessor": {"ProcessorConfig": {"Mode": "INLINE"}, "StartAt": "Tag", "States": {
                    "Tag": {"Type": "Pass", "Parameters": {"seen.$": "$.value", "at.$": "$.index"}, "End": True}}},
                "End": True}},
        }, {"items": ["a", "b", "c"]}),
        "parallel": ({
            "StartAt": "Both", "States": {"Both": {"Type": "Parallel", "End": True, "Branches": [
                {"StartAt": "L", "States": {"L": {"Type": "Pass", "Result": "left", "End": True}}},
                {"StartAt": "R", "States": {"R": {"Type": "Pass", "Parameters": {"got.$": "$.v"}, "End": True}}}]}},
        }, {"v": 1}),
        "fail": ({
            "StartAt": "Stop", "States": {"Stop": {"Type": "Fail", "Error": "Order.Invalid", "Cause": "no items"}},
        }, {}),
        "runtime-error": ({
            "StartAt": "Reach", "States": {"Reach": {
                "Type": "Pass", "Parameters": {"x.$": "$.not.there"}, "End": True}},
        }, {}),
        "wait-and-succeed": ({
            "StartAt": "Pause", "States": {
                "Pause": {"Type": "Wait", "Seconds": 1, "Next": "Done"}, "Done": {"Type": "Succeed"}},
        }, {"carried": True}),
    }
    for label, (definition, payload) in machines.items():
        arn, _ = machine(sfn, names, cleanup, role, definition, label=label)
        _, done = run(sfn, names, eventually, arn, payload, label=f"run-{label}")
        snapshot.match(label, {k: done.get(k) for k in ("status", "output", "error", "cause")})


def test_refusals(client, names, cleanup, snapshot, service_role, target):
    sfn = client("stepfunctions")
    role = service_role("states")
    good = {"StartAt": "A", "States": {"A": {"Type": "Succeed"}}}
    make = lambda label, definition, **kw: lambda: sfn.create_state_machine(
        name=names(label), definition=json.dumps(definition), roleArn=role, **kw)

    snapshot.error("no-start", make("bad", {"States": {"A": {"Type": "Succeed"}}}))
    snapshot.error("start-names-no-state", make("bad", {"StartAt": "Z", "States": {"A": {"Type": "Succeed"}}}))
    # A machine that can never end. The linter calls it an error; whether
    # CreateStateMachine does is recorded rather than assumed.
    cleanup(lambda: sfn.delete_state_machine(
        stateMachineArn=f"arn:aws:states:{target.region}:{target.account}:stateMachine:{names('loop')}"))
    snapshot.outcome("no-terminal-state", make("loop", {
        "StartAt": "A", "States": {"A": {"Type": "Pass", "Next": "A"}}}), opaque=MINTED)
    snapshot.error("unknown-state-type", make("bad", {"StartAt": "A", "States": {"A": {"Type": "Shrug"}}}))
    snapshot.error("next-names-no-state", make("bad", {
        "StartAt": "A", "States": {"A": {"Type": "Pass", "Next": "Nowhere"}}}))
    snapshot.error("definition-not-json", lambda: sfn.create_state_machine(
        name=names("bad"), definition="{nope", roleArn=role))
    snapshot.error("role-is-not-an-arn", lambda: sfn.create_state_machine(
        name=names("bad"), definition=json.dumps(good), roleArn="not-an-arn"))
    snapshot.error("bad-name", lambda: sfn.create_state_machine(
        name="has space", definition=json.dumps(good), roleArn=role))

    arn, _ = machine(sfn, names, cleanup, role, good)
    # The same name and the same definition is the same machine; a different
    # definition under that name is a conflict.
    snapshot.outcome("create-again-same", make("machine", good))
    snapshot.error("create-again-different", make(
        "machine", {"StartAt": "B", "States": {"B": {"Type": "Succeed"}}}))

    absent = f"arn:aws:states:{target.region}:{target.account}:stateMachine:{names('absent')}"
    snapshot.error("start-absent-machine", lambda: sfn.start_execution(stateMachineArn=absent))
    snapshot.error("describe-absent-machine", lambda: sfn.describe_state_machine(stateMachineArn=absent))
    snapshot.error("input-not-json", lambda: sfn.start_execution(stateMachineArn=arn, input="{nope"))
    snapshot.error("describe-absent-execution", lambda: sfn.describe_execution(
        executionArn=absent.replace(":stateMachine:", ":execution:") + ":never-ran"))

    run_name = names("run")
    sfn.start_execution(stateMachineArn=arn, name=run_name, input='{"a": 1}')
    snapshot.outcome("same-name-same-input", lambda: sfn.start_execution(
        stateMachineArn=arn, name=run_name, input='{"a": 1}'), opaque=MINTED)
    snapshot.error("same-name-different-input", lambda: sfn.start_execution(
        stateMachineArn=arn, name=run_name, input='{"a": 2}'))
