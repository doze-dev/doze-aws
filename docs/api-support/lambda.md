# Lambda — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

Functions run as **real supervised local processes** speaking the AWS Lambda
Runtime API (no Docker). The official runtime interface clients speak the
protocol unmodified: `provided.*`/Go run a `bootstrap`/binary directly,
`python3.x` runs `python -m awslambdaric`, `nodejs*` runs
`npx aws-lambda-ric`. One process per function, serial invocations in this
phase (scale-out pool in Phase 8).

Code: `Code.ZipFile` (unpacked under the data dir) **and** a doze extension —
`Code.S3Bucket == "_local_"` with `Code.S3Key` an absolute path to a directory
or binary, used in place for edit-and-reinvoke with no copy.

| Operation | Tier | Notes |
|---|---|---|
| CreateFunction / UpdateFunctionConfiguration / UpdateFunctionCode | F | zip + _local_ packaging; env vars; DLQ/DestinationConfig; layers stored |
| GetFunction / ListFunctions / DeleteFunction | F | |
| Invoke (RequestResponse) | F | real process, X-Amz-Function-Error on handler error, Tail log result |
| Invoke (Event) | F | async with configurable retries → DLQ / OnFailure destination |
| Put/Get/Update/List/DeleteFunctionEventInvokeConfig | F | async destinations (OnSuccess/OnFailure → SQS/SNS/Lambda) + MaximumRetryAttempts (honored) / MaximumEventAgeInSeconds (stored) |
| Invoke (DryRun) | F | 204 |
| PublishVersion / aliases (Create/Get/List/Delete) | F | local version numbering |
| Function URL config (Create/Get/Delete) | F | URL served on the gateway |
| PutFunctionConcurrency / GetFunctionConcurrency / Delete | C | stored; no throttling locally |
| TagResource / UntagResource / ListTags | F | |
| CreateEventSourceMapping (SQS) | F | polls the queue, delivers batches (batch-size honored), delete-on-success, visibility-timeout retry on failure |
| Get/List/Update/DeleteEventSourceMapping | F | |
| DynamoDB/Kinesis event source mappings | S | Streams are deferred; Kinesis is not implemented |
| Container images, SnapStart, provisioned concurrency semantics | S | config accepted where trivial; execution semantics are cloud-only |

Child processes get `AWS_LAMBDA_RUNTIME_API`, `_HANDLER`, the function's env,
test credentials, and `AWS_ENDPOINT_URL*` pointing back at the doze-aws
endpoint — so handlers reach every sibling service unmodified.
