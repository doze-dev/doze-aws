# Recording the conformance scenarios against real AWS

`README.md` says what recording is. This page is the checklist for doing it,
one service at a time, so that when an AWS account is available it is one
command and a review, not a day of finding out what to set.

## SQS

Everything in `plan/services/sqs.md` section 8 (the recording queue) is
`conformance/tests/test_sqs.py` (50 scenarios, 182 recorded labels) and
four scenarios in the neighbours' files that section 9 (owed to integration)
needs: `test_lambda.py::test_a_trigger_whose_queue_hides_for_less_than_the_function_runs`
and `::test_a_deleted_functions_mapping` (each makes a Python function and a
Lambda execution role, which the `service_role` fixture creates and removes; the
second sleeps 30 s), `test_s3.py::test_notification_fifo_queue` and
`test_sns.py::test_a_fifo_queue_on_a_standard_topic`. Nothing in them is
recorded yet; `uv run pytest tests/test_sqs.py` reports every response as
UNVERIFIED. The claims they would settle are in `conformance/claims/sqs.md`,
each row naming its scenario. Record the five files in one run:

```sh
uv run --frozen pytest tests/test_sqs.py \
  tests/test_lambda.py::test_a_trigger_whose_queue_hides_for_less_than_the_function_runs \
  tests/test_lambda.py::test_a_deleted_functions_mapping \
  tests/test_s3.py::test_notification_fifo_queue \
  tests/test_sns.py::test_a_fifo_queue_on_a_standard_topic
```

(with the environment of the next section).

### Before

1. **An account** you may create queues in, and credentials for it in the
   environment (`AWS_PROFILE=<name>`, or the key variables). Nothing here
   needs an administrator: `sqs:*` on `arn:aws:sqs:<region>:<account>:dzc-*`,
   `sqs:ListQueues`, and `sts:GetCallerIdentity` (the account check). The four
   neighbour scenarios add `lambda:*` on `dzc-*` functions and mappings,
   `iam:CreateRole`, `AttachRolePolicy`, `PassRole` and their deletes on `dzc-*`
   roles, `s3:CreateBucket` and bucket configuration on `dzc-*` buckets, and
   `sns:*` on `dzc-*` topics.
2. **The account id, typed.** `CONFORMANCE_AWS_ACCOUNT=<12 digits>` must equal
   the account the credentials resolve to; if it does not, nothing is created.
3. **A Region.** `CONFORMANCE_AWS_REGION` defaults to `us-east-1`, doze-aws's
   own. Use the default: queue URLs and ARNs are normalized, but a Region
   other than the one doze-aws reports would put a different value in every
   ARN the recording keeps.
4. **No SQS endpoint override and no proxy**: `AWS_ENDPOINT_URL`,
   `AWS_ENDPOINT_URL_SQS` and `CONFORMANCE_ENDPOINT` must be unset.

### The command

```sh
cd conformance
AWS_PROFILE=<profile> \
CONFORMANCE_TARGET=aws \
CONFORMANCE_AWS_ACCOUNT=<account> \
CONFORMANCE_RECORD=1 \
uv run --frozen pytest tests/test_sqs.py
```

It writes `conformance/snapshots/test_sqs.json` with `_recorded.target: aws`,
the date and the library versions. A scenario that expects a refusal (`error`)
and was not refused makes the run fail and writes nothing for it: the scenario
is wrong about AWS, and that is the first thing to read.

### What it costs

Under a cent. The scenarios make on the order of 1,500 requests (including the
`eventually` polls), and the first million requests a month are free. The
largest bodies are two 1 MiB messages and a 1 MiB batch (billed as sixteen
requests each). There is no KMS key, no Lambda, no data transfer to speak of,
and nothing that bills by the hour. `alias/aws/sqs` in
`test_kms_key_with_sqs_managed_encryption` is the AWS-managed key, which has no
monthly fee, and no message is sent to that queue.

It takes a few minutes: scenarios sleep a few seconds each, and the longest
waits are the move tasks (up to 60 s) and the dead-letter moves (up to 40 s).

### Cleanup

Every queue is named `dzc-<run>-<n>-<label>` and deleted when its test ends,
last in first out. A run that died halfway leaves what it had made. Sweep:

```sh
aws sqs list-queues --queue-name-prefix dzc- --query 'QueueUrls[]' --output text \
  | xargs -n1 aws sqs delete-queue --queue-url
```

A queue deleted by the run cannot be created again under its name for 60
seconds; the names are unique per run, so only `test_recreate_after_delete`
meets that, on purpose.

### What is compared

The normalized response of each call: the account, the endpoint and Region, the
run's queue names (`<name:queue>`), UUIDs (numbered, so identity survives) and
datetimes are replaced, and what is left is held field for field: the body,
and for a refusal the code, the message and the HTTP status. `opaque` fields
must be present and consistent but are not compared by value (receipt handles,
message ids, task handles); `drop` and `unordered` are the scenario's own
claims about weather and ordering, each with a comment saying why.

### After

1. `git diff conformance/snapshots/test_sqs.json` and read it. This is the
   first time anyone has seen what AWS answers, and the labels that matter
   most are the ones whose scenario says "whichever AWS does" (`outcome`).
2. `uv run --frozen pytest tests/test_sqs.py` against doze-aws, as usual. Every
   difference is a finding: fix doze-aws, in the direction the recording shows
   (more or less strict, named in the commit), or list it in `deviations.py`
   with its reason, if it is something that cannot exist on a laptop. Two are
   expected to differ and be listed: `test_recreate_after_delete` and
   `test_purge_twice`, because doze-aws does not enforce the 60-second rules
   unless `--sqs-strict` is on (the suite boots it without).
3. `bun scripts/claims.ts` for the count of recorded claims, and update the
   `memory` rows of `claims/sqs.md` to `doc` or `model` where the recording
   now makes them established, and `plan/services/sqs.md` section 8.

### What cannot be recorded

A running move task that survives an orderly restart of doze-aws as a
`CANCELLED` record (SQS-46) has no counterpart in AWS: there is no restart to
make. The model allows either `CANCELLED` or `FAILED`; it is a decision, not a
claim, and stays in section 4 of the plan.
