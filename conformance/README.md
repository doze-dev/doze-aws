# doze-aws conformance suite

boto3 scenarios, run against doze-aws and held against what real AWS answered.

Everything else in this repository tests doze-aws with the Go SDKs, against an
expectation written by whoever wrote the code under test. This suite exists for
the two things that cannot find:

- **what a different SDK does.** boto3 has its own defaults — a CRC32 on every
  S3 upload, checksum validation on every download, its own retry and endpoint
  rules — and it is the SDK most people will point at doze-aws.
- **what AWS actually does.** Each scenario is written once. Pointed at a real
  account it *records*; pointed at doze-aws it *compares*. The recording is the
  oracle, and nobody here wrote it.

This is the only Python in doze-aws, and it is here as the thing under test,
not as tooling.

## Running

```sh
go tool task test:conformance          # from the repo root
cd conformance && uv run pytest        # the same thing
uv run pytest tests/test_sqs.py -k fifo
```

It builds the binary, boots it on a free port over an empty data dir, and runs
every scenario through boto3. `CONFORMANCE_ENDPOINT=http://127.0.0.1:4566`
uses one that is already running instead.

## Reading the result

The run ends with three numbers, and they are different claims:

```
  107  matched what real AWS answered
    0  answered differently
   21  UNVERIFIED — boto3 drove the call, nothing to compare it with
```

**Unverified is not passed.** A scenario with no recording still proves boto3
can drive doze-aws through it, and still fails on what the scenario itself
asserts — a call that must be refused, an order that must hold. But nothing
was compared with AWS, and the count says so. `CONFORMANCE_REQUIRE_SNAPSHOTS=1`
turns any unverified response into a failed run; that is the setting to move to
once the recordings exist.

## Differences that are on purpose

A difference the suite finds is a bug until somebody decides otherwise, and
`deviations.py` is where that decision is written down, with its reason. A
listed difference does not fail the run. It is counted and printed in the
summary every time:

```
    1  differ ON PURPOSE (deviations.py):
         test_x::test_y::some-label
```

It ratchets both ways: a difference that is not listed fails, and so does a
listing that no longer differs.

The list is empty and is meant to stay empty. doze-aws is not more forgiving
than AWS: anything it accepts that AWS refuses is a failure somebody meets on
deploy. The only entries that belong are things that cannot exist locally.

## Recording against real AWS

```sh
CONFORMANCE_TARGET=aws \
CONFORMANCE_AWS_ACCOUNT=123456789012 \
CONFORMANCE_RECORD=1 \
uv run pytest tests
```

- `CONFORMANCE_AWS_ACCOUNT` is required and must be the account the ambient
  credentials resolve to. If it is not, nothing is created.
- `CONFORMANCE_AWS_REGION` defaults to `us-east-1`, doze-aws's own region.
- Every resource is named `dzc-<run>-<n>-<label>` and deleted when its test
  ends. After a run that died halfway, that prefix is what to look for.
- The scenarios stay inside what costs fractions of a cent: on-demand tables,
  empty queues and topics, a few kilobytes of S3 and one 5 MiB multipart part.
  A scenario that needs something billed by the hour or the month (a KMS key, a
  Kinesis shard) should say so at the top of its file.

Recordings land in `snapshots/`, one JSON file per test module, and are
committed. Each file says what it was recorded against; only a recording made
against real AWS is ever compared, and recording doze-aws into `snapshots/` is
refused, so the suite cannot end up comparing doze-aws with itself.

## Writing a scenario

```python
def test_standard_queue_lifecycle(client, names, cleanup, snapshot, eventually):
    sqs = client("sqs")
    url = snapshot.match("create", sqs.create_queue(QueueName=names("queue")))["QueueUrl"]
    cleanup(sqs.delete_queue, QueueUrl=url)
    snapshot.error("no-such-queue", lambda: sqs.get_queue_url(QueueName=names("absent")))
```

| | |
|---|---|
| `client("sqs")` | a boto3 client for whichever target is in play |
| `names("queue")` | a name unique to this run; appears in a snapshot as `<name:queue>` |
| `cleanup(fn, **kw)` | undo it when the test ends, last in first out |
| `snapshot.match(label, response)` | hold a response against the recording |
| `snapshot.error(label, fn)` | `fn` must be refused; hold the code, message and status |
| `snapshot.outcome(label, fn)` | hold whichever happened, where that is the question |
| `eventually(fn)` | retry until `fn` stops raising `AssertionError` |

`match` takes three ways to say a difference is not a finding. Each is a claim
about AWS, so each deserves the comment explaining it:

- `opaque=("ReceiptHandle",)` — the value is the service's own business, but it
  must be present, and the same one each time it appears.
- `drop=("ItemCount",)` — the key is weather: it changes on its own schedule.
- `unordered=("Items",)` — the list has no promised order.

What is normalized without being asked: the account, the endpoint, the run's
names, UUIDs (numbered, so identity survives), datetimes, and
`ResponseMetadata` down to its status code.

Three rules that keep a scenario true on both sides:

1. **It must be correct against AWS first.** Poll with `eventually` wherever
   AWS is eventually consistent, even though doze-aws is not.
2. **Refuse with something only the service can refuse.** botocore checks
   required members, types and lengths itself and never sends the call.
3. **One finding, one scenario.** A scenario that stops at its first
   difference hides the rest; put the suspicious call on its own.

## Layout

```
conftest.py              the fixtures above, and the summary
deviations.py            where doze-aws differs from AWS on purpose, and why
harness/target.py        doze (build + boot) | aws (guarded by the account allowlist)
harness/normalize.py     what makes two clouds comparable, and the differ
harness/snapshot.py      record / compare / report
selftest/                the harness, tested with no server at all
tests/test_<service>.py  the scenarios
snapshots/               what real AWS answered
```
