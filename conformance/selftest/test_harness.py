"""The harness, tested with no server and no cloud.

Everything the suite concludes passes through the normalizer and the differ. A
normalizer that erased a real difference would make doze-aws look conformant
for free, so these are the tests that the tests are not lying.
"""

import datetime
import json
import re

import pytest
from botocore.exceptions import ClientError

from harness.normalize import Normalizer, diff
from harness.snapshot import Snapshot, Store, Tally


def aws():
    return Normalizer("123456789012",
                      endpoint_patterns=[re.compile(r"https?://[a-z0-9.-]+\.amazonaws\.com")])


def doze():
    return Normalizer("000000000000", endpoints=["http://127.0.0.1:4566"])


def test_the_same_answer_from_both_clouds_normalizes_identically():
    a, d = aws(), doze()
    a.name("dzc-aaaa-1-q", "q")
    d.name("dzc-bbbb-1-q", "q")
    from_aws = a.value({
        "QueueUrl": "https://sqs.us-east-1.amazonaws.com/123456789012/dzc-aaaa-1-q",
        "Arn": "arn:aws:sqs:us-east-1:123456789012:dzc-aaaa-1-q",
        "Created": datetime.datetime(2026, 1, 1),
        "ResponseMetadata": {"HTTPStatusCode": 200, "RequestId": "x", "HTTPHeaders": {"a": "b"}},
    })
    from_doze = d.value({
        "QueueUrl": "http://127.0.0.1:4566/000000000000/dzc-bbbb-1-q",
        "Arn": "arn:aws:sqs:us-east-1:000000000000:dzc-bbbb-1-q",
        "Created": datetime.datetime(2031, 5, 5),
        "ResponseMetadata": {"HTTPStatusCode": 200, "RequestId": "y", "RetryAttempts": 0},
    })
    assert from_aws == from_doze
    assert from_aws["QueueUrl"] == "<endpoint>/<account>/<name:q>"


def test_a_longer_name_is_not_read_as_a_shorter_one_plus_a_suffix():
    n = doze()
    n.name("dzc-r-1-q", "q")
    n.name("dzc-r-1-q-dlq", "q-dlq")
    assert n.text("arn:x:dzc-r-1-q-dlq") == "arn:x:<name:q-dlq>"


def test_ids_keep_their_identity():
    # Two different ids must stay different, and the same id must stay the same.
    n = doze()
    a, b = "0f8fad5b-d9cb-469f-a165-70867728950e", "7c9e6679-7425-40de-944b-e07fc1f90ae7"
    assert n.value({"sent": a, "received": a, "other": b}) == \
        {"other": "<uuid:1>", "received": "<uuid:2>", "sent": "<uuid:2>"}


def test_the_status_code_survives_and_a_wrong_one_is_seen():
    n = doze()
    ok = n.value({"ResponseMetadata": {"HTTPStatusCode": 200, "RequestId": "r"}})
    created = n.value({"ResponseMetadata": {"HTTPStatusCode": 201, "RequestId": "r"}})
    assert diff(ok, created) == [("$.ResponseMetadata.HTTPStatusCode", 200, 201)]


def test_opaque_is_present_or_it_is_a_difference():
    n = doze()
    with_handle = n.value({"ReceiptHandle": "abc"}, opaque=("ReceiptHandle",))
    n.reset()
    without = n.value({}, opaque=("ReceiptHandle",))
    assert with_handle == {"ReceiptHandle": "<ReceiptHandle:1>"}
    assert diff(with_handle, without) == [("$.ReceiptHandle", "<ReceiptHandle:1>", "<absent>")]


def test_unordered_sorts_and_ordered_does_not():
    n = doze()
    assert n.value({"L": ["b", "a"]}, unordered=("L",)) == {"L": ["a", "b"]}
    assert diff(n.value({"L": ["a", "b"]}), n.value({"L": ["b", "a"]})) != []


def test_diff_finds_missing_extra_changed_and_retyped():
    found = diff({"a": 1, "b": 2, "c": [1, 2], "t": True}, {"a": 1, "c": [1], "d": 4, "t": 1})
    assert ("$.b", 2, "<absent>") in found
    assert ("$.d", "<absent>", 4) in found
    assert ("$.c.length", 2, 1) in found
    assert ("$.t", True, 1) in found  # True is not 1
    assert not any(p == "$.a" for p, _, _ in found)


def test_bytes_are_kept_small_and_digested_large():
    n = doze()
    assert n.value(b"hello") == {"<bytes>": "hello"}
    big = n.value(b"x" * 5000)
    assert big["length"] == 5000 and "<bytes:sha256>" in big


def _snap(tmp_path, record, tally=None, target="aws"):
    return Snapshot(Store(tmp_path), doze(), "mod", "test_x", record=record,
                    target=target, tally=tally or Tally(), versions={})


def test_record_then_compare_then_catch_a_change(tmp_path):
    rec = _snap(tmp_path, record=True)
    rec.match("create", {"Name": "a", "ResponseMetadata": {"HTTPStatusCode": 200}})
    rec.finish()
    rec.store.flush()
    assert json.loads((tmp_path / "mod.json").read_text())["_recorded"]["target"] == "aws"

    tally = Tally()
    same = _snap(tmp_path, record=False, tally=tally)
    same.match("create", {"Name": "a", "ResponseMetadata": {"HTTPStatusCode": 200}})
    same.finish()
    assert (tally.verified, tally.unverified, tally.mismatched) == (1, 0, 0)

    changed = _snap(tmp_path, record=False, tally=tally)
    changed.match("create", {"Name": "b", "ResponseMetadata": {"HTTPStatusCode": 200}})
    with pytest.raises(AssertionError, match=r"\$\.Name"):
        changed.finish()
    assert tally.mismatched == 1


def test_no_recording_is_unverified_and_never_verified(tmp_path):
    tally = Tally()
    s = _snap(tmp_path, record=False, tally=tally)
    s.match("create", {"Name": "a"})
    s.finish()
    assert (tally.verified, tally.unverified) == (0, 1)


def test_a_recording_not_made_against_aws_is_not_compared(tmp_path):
    rec = _snap(tmp_path, record=True, target="doze")
    rec.match("create", {"Name": "a"})
    rec.finish()
    rec.store.flush()

    tally = Tally()
    s = _snap(tmp_path, record=False, tally=tally)
    s.match("create", {"Name": "something else entirely"})
    s.finish()  # does not raise: there is nothing trustworthy to compare with
    assert (tally.verified, tally.unverified) == (0, 1)


def test_a_recorded_call_the_scenario_never_reached_is_a_mismatch(tmp_path):
    rec = _snap(tmp_path, record=True)
    rec.match("create", {"Name": "a"})
    rec.match("delete", {"Name": "a"})
    rec.finish()
    rec.store.flush()

    s = _snap(tmp_path, record=False)
    s.match("create", {"Name": "a"})
    with pytest.raises(AssertionError, match="never reached"):
        s.finish()


def _deviating(tmp_path, tally, listed):
    return Snapshot(Store(tmp_path), doze(), "mod", "test_x", record=False, target="doze",
                    tally=tally, versions={}, deviations=listed)


def test_a_listed_deviation_is_counted_and_does_not_fail(tmp_path):
    tally = Tally()
    s = _deviating(tmp_path, tally, {"mod::test_x::lenient": "on purpose"})
    s.error("lenient", lambda: None)  # AWS refuses; this target does not
    s.finish()
    assert tally.deviations == [("mod::test_x::lenient", "on purpose")]


def test_an_unlisted_difference_still_fails_beside_a_listed_one(tmp_path):
    s = _deviating(tmp_path, Tally(), {"mod::test_x::lenient": "on purpose"})
    s.error("lenient", lambda: None)
    s.error("also-lenient", lambda: None)
    with pytest.raises(AssertionError, match="also-lenient"):
        s.finish()


def test_a_deviation_that_no_longer_differs_fails(tmp_path):
    def refused():
        raise ClientError({"Error": {"Code": "Nope", "Message": "no"},
                           "ResponseMetadata": {"HTTPStatusCode": 400}}, "Op")

    s = _deviating(tmp_path, Tally(), {"mod::test_x::lenient": "on purpose"})
    s.error("lenient", refused)  # fixed: it is refused now
    with pytest.raises(AssertionError, match="remove the entry"):
        s.finish()


def test_error_captures_the_refusal_and_insists_on_one(tmp_path):
    s = _snap(tmp_path, record=True)

    def refused():
        raise ClientError({"Error": {"Code": "Nope", "Message": "no such thing"},
                           "ResponseMetadata": {"HTTPStatusCode": 404}}, "GetThing")

    s.error("missing", refused)
    assert s.captured["missing"] == {
        "Error": {"Code": "Nope", "Message": "no such thing"},
        "ResponseMetadata": {"HTTPStatusCode": 404},
    }
    # A call that should have been refused and was not is held to the end, so
    # the refusals after it still run — and then it fails the scenario.
    assert s.error("allowed", lambda: None) is None
    with pytest.raises(AssertionError, match="the call succeeded"):
        s.finish()
