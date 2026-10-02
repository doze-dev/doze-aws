"""Fixtures for the conformance scenarios. See README.md for the whole picture.

    client     client("sqs") — a boto3 client for whichever target is in play
    names      names("queue") — a name unique to this run, and stable in a snapshot
    cleanup    cleanup(fn, **kw) — undo it at the end of the test, last in first out
    snapshot   snapshot.match("label", response) — hold it against real AWS
    eventually eventually(fn) — retry until fn stops raising AssertionError
"""

import os
import pathlib
import secrets
import time

import boto3
import botocore
import pytest
from botocore.exceptions import ClientError

from deviations import DEVIATIONS
from harness.snapshot import Snapshot, Store, Tally
from harness.target import open_target

HERE = pathlib.Path(__file__).parent
COMMITTED = HERE / "snapshots"

RECORD = os.environ.get("CONFORMANCE_RECORD") == "1"
REQUIRE = os.environ.get("CONFORMANCE_REQUIRE_SNAPSHOTS") == "1"
SNAPSHOT_DIR = pathlib.Path(os.environ.get("CONFORMANCE_SNAPSHOT_DIR", COMMITTED))

RUN = secrets.token_hex(4)
TALLY = Tally()
STORE = Store(SNAPSHOT_DIR)
_counter = iter(range(1, 1_000_000))


@pytest.fixture(scope="session")
def target():
    t = open_target()
    if RECORD and t.name != "aws" and SNAPSHOT_DIR.resolve() == COMMITTED.resolve():
        t.close()
        pytest.exit(
            "refusing to record: snapshots/ holds what REAL AWS answered. Recording "
            "doze-aws into it would make the suite compare doze-aws with itself. "
            "Set CONFORMANCE_TARGET=aws, or point CONFORMANCE_SNAPSHOT_DIR elsewhere.",
            returncode=2,
        )
    yield t
    t.close()


@pytest.fixture(scope="session")
def normalizer(target):
    return target.normalizer()


@pytest.fixture(scope="session")
def client(target):
    made = {}

    def get(service, **kw):
        if kw:
            return target.client(service, **kw)
        if service not in made:
            made[service] = target.client(service)
        return made[service]

    return get


@pytest.fixture
def names(normalizer):
    """names("queue") -> "dzc-<run>-<n>-queue", recorded as <name:queue>.

    The prefix is what a sweep of a real account looks for after a run that
    died before its cleanup.
    """
    n = next(_counter)

    def make(label, suffix=""):
        value = f"dzc-{RUN}-{n}-{label}{suffix}"
        normalizer.name(value, label + suffix)
        return value

    return make


@pytest.fixture
def cleanup():
    undo = []

    def register(fn, *a, **kw):
        undo.append((fn, a, kw))

    yield register
    for fn, a, kw in reversed(undo):
        try:
            fn(*a, **kw)
        except ClientError:
            pass  # already gone is the common case, and the test has its verdict


@pytest.fixture
def snapshot(request, target, normalizer):
    snap = Snapshot(
        STORE, normalizer,
        module=request.module.__name__.rsplit(".", 1)[-1],
        test=request.node.name,
        record=RECORD, target=target.name, tally=TALLY,
        # Deviations are doze-aws's. Against AWS there are none to make.
        deviations=DEVIATIONS if target.name == "doze" else None,
        versions={"boto3": boto3.__version__, "botocore": botocore.__version__,
                  "region": target.region},
    )
    yield snap
    # A scenario that already failed has said what is wrong; a recording made
    # from half of one would be worse than none.
    if getattr(request.node, "failed_call", False):
        return
    snap.finish()


@pytest.fixture
def eventually(target):
    """Retry until fn stops raising AssertionError, and return what it returned.

    Real AWS is eventually consistent in places doze-aws is not. A scenario
    that polls is correct against both; one that does not is only correct here.
    """
    def wait(fn, timeout=None, every=0.25):
        deadline = time.time() + (timeout or (60 if target.name == "aws" else 10))
        while True:
            try:
                return fn()
            except AssertionError:
                if time.time() > deadline:
                    raise
                time.sleep(every)

    return wait


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    if call.when == "call" and outcome.get_result().failed:
        item.failed_call = True


def pytest_sessionfinish(session, exitstatus):
    STORE.flush()
    if REQUIRE and TALLY.unverified and session.exitstatus == 0:
        session.exitstatus = 1


def pytest_terminal_summary(terminalreporter):
    t = TALLY
    if not (t.verified or t.unverified or t.recorded or t.mismatched or t.deviations):
        return
    tr = terminalreporter
    tr.section("conformance")
    if RECORD:
        tr.line(f"recorded {t.recorded} responses into {SNAPSHOT_DIR}")
        return
    tr.line(f"{t.verified:5d}  matched what real AWS answered")
    tr.line(f"{t.mismatched:5d}  answered differently")
    tr.line(f"{t.unverified:5d}  UNVERIFIED — boto3 drove the call, nothing to compare it with")
    if t.unverified and not REQUIRE:
        tr.line("       (record them: CONFORMANCE_TARGET=aws CONFORMANCE_RECORD=1)")
    if t.unverified and REQUIRE:
        tr.line("       CONFORMANCE_REQUIRE_SNAPSHOTS=1: unverified responses fail the run")
    if t.deviations:
        tr.line(f"{len(t.deviations):5d}  differ ON PURPOSE (deviations.py):")
        for key, why in t.deviations:
            tr.line(f"         {key}")
            tr.line(f"           {why}")
