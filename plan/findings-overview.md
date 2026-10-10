# What grounding the console design found in doze-aws

> The first list, from the design work, kept for the story. Each service
> document in [services/](services/) supersedes it in detail: they hold 460
> findings against the 44 rows here, with evidence re-checked and several of
> these rows corrected.

Source: the 15 page-group agents plus the SQS pilot. "Run" = reproduced against a built binary.
"Read" = code reading only. Everything here is from agent reports; spot-check before fixing.
Paths are under /Users/srini/Code/doze-dev/doze-aws.

## A. Behaviour bugs and parity gaps (the ones that change what a deploy does)

| # | Where | What | Evidence |
|---|---|---|---|
| 1 | provision/svc_sqs.go:112 | A queue whose DLQ sorts after it by name (`x`, `x-dlq`, the usual naming) fails with "dead-letter target does not exist" and rolls the whole stack back | run |
| 2 | provision/svc_ssm.go:24, internal/cfn | Updating an SSM parameter's `Value` in a stack reports UPDATE_COMPLETE and changes nothing (Version stays 1) | run |
| 3 | apigateway/deployments.go:98 | Deploying to an existing REST stage wipes variables, method settings, tags, access-log settings. AWS keeps them | run |
| 4 | apigateway/execute.go:294 | REST HTTP/HTTP_PROXY with integrationHttpMethod ANY forwards the literal method "ANY" | run |
| 5 | lambda/extras.go createMapping | No check that the queue's visibility timeout is >= the function's timeout; AWS refuses the trigger | read (grep) |
| 6 | lambda/functions.go:123 | Default MemorySize 512; AWS default is 128 | read |
| 7 | iam/authorize.go:243-262 | STS credentials minted for a role are not honoured: calls authenticate as the root/default user. AssumeRole mints for a role that does not exist. The console text claims otherwise | run |
| 8 | iam/authorize.go:243 | A deactivated access key still authenticates | run |
| 9 | iam/store.go | No two-key limit per user; key ids are 21 characters (AWS 20) | run |
| 10 | iam/store.go UpdateUser/UpdateGroup | Rename onto an existing name silently overwrites; rename orphans keys and group membership | run |
| 11 | iamguard/guard.go:212 | `aws:SecureTransport` is not supplied to resource-policy evaluation, so a "deny plain HTTP" bucket policy never denies (SUPPORT.md says it is supplied) | run |
| 12 | s3 | Bucket default retention is stored but not applied to new objects; DeleteObjects fires no ObjectRemoved notification; notification config accepts missing queue/function, overlapping rules, FIFO queue (then fails at delivery) | run |
| 13 | s3/store.go:301 etc. | Accepts what AWS refuses: bucket name with `..`, CORS method `FETCH`, lifecycle rule with no action, duplicate rule Ids, empty website suffix, past retention date, policy statement with no Principal, 129-char tag key, key over 1024 bytes | run (AWS side from memory) |
| 14 | dynamodb/validate.go, tables.go | Table names with no pattern check; sort key same name as partition key; duplicate GSI names; deleting a missing index "succeeds"; AddGSI with a key already typed differently is accepted and DescribeTable then reports the wrong type | run |
| 15 | internal/console client_ddb.go | Create can silently change the base key's attribute type; TTL is a second call so a refused TTL leaves the table made | run |
| 16 | console item editor | Edit round trip corrupts types: string sets become lists, number sets become lists of strings, binary becomes string | run |
| 17 | console layout.html:42,59 | The key/value editor turns every JSON number/boolean into a string on first touch (Secrets Manager and SSM both) | run/read |
| 18 | cloudformation/stacks.go:631 | Deleted importers still block deleting the exporter | run |
| 19 | cloudformation | A refused create (missing parameter, unserved kind) leaves a CREATE_FAILED record; resubmitting gets AlreadyExistsException. AWS makes no stack | run |
| 20 | cloudformation/rollback.go:170 | Retain resources get DELETE events in a failed create; AWS says DELETE_SKIPPED | run |
| 21 | cloudformation/changesets.go:157 | A parameter-only change set is empty (fingerprints template text) | run |
| 22 | cloudformation | Validate calls an unserved-kind template valid; Create then refuses it | run |
| 23 | cloudformation | NoEcho parameter values shown in clear in the Parameters panel and Update form | run |
| 24 | logs/actions_metricfilters.go | A metric filter with an unreadable pattern or non-numeric value saves with a success toast and never fires | run |
| 25 | logs/store.go:131-156 | Deleting a log group leaves its metric filters; recreating the group brings them back | run |
| 26 | logs/actions.go:110 | DescribeLogGroups hard-codes metricFilterCount 0 | run |
| 27 | logs | TagResource validates values only; a bad key saves and can never be removed | run |
| 28 | kms/validate.go:81, modelcheck.go:307 | Plaintext/message limits count base64 length: 3073-4096 byte inputs refused where AWS accepts up to 4096 | run |
| 29 | kms/actions_keys.go | ScheduleKeyDeletion on a pending key succeeds (AWS: KMSInvalidStateException); EnableKeyRotation/CreateAlias/UpdateKeyDescription on a pending key succeed | run |
| 30 | internal/console client_sec.go:157 | KMS create leaves an orphan aliasless key when the alias is refused | run |
| 31 | secretsmanager | UpdateSecretVersionStage/TagResource/PutResourcePolicy have no DeletedAt check; Rotation days always 0; a policy that fails to parse is stored and silently never consulted | read |
| 32 | ssm | Label rules not enforced (`aws-prod`, `ssm`, `has space` attach); 9000-byte String accepted (AWS 4KB/8KB); Policies accepted on Standard and never returned | run |
| 33 | internal/console handlers_tags.go | Tag Save is not atomic (writes one tag per call, stops at the first refusal) | run/read |
| 34 | sns | Console cannot create a FIFO topic (form pattern blocks "." and no FifoTopic attribute is sent); publish form has no group/dedup id; a FIFO queue can subscribe to a standard topic and every delivery then fails; raw delivery is a no-op for http | read |
| 35 | eventbridge/store.go:186 | DeleteBus removes rules silently and orphans archives (console confirm says rules must be deleted first); PutEvents to a nonexistent bus returns success and drops the event | run |
| 36 | internal/console handlers_eb.go:272, client_eb.go:387 | Bus/archive ARNs hard-code us-east-1 and 000000000000, so archives made from the console capture nothing under another account/region | run |
| 37 | eventbridge/actions.go:226,251 | Input transformer leaves `<aws.events.rule-name>` unsubstituted; `$.detail.items[0]` becomes empty | run |
| 38 | cloudwatch/actions_alarm.go:342 | SetAlarmState to the state already held records ALARM->ALARM and re-fires actions; manual state reverts after ~15 s; failed notifications not in History | run |
| 39 | cloudwatch/actions_alarm.go:278 | DescribeAlarmsForMetric on an undimensioned metric returns alarms on dimensioned series | run |
| 40 | internal/console info.go:23-30 | The fidelity panel is empty for CloudWatch: lookup key is "cw", ledger key is "cloudwatch" | run |
| 41 | cloudwatch | Console create silently coerces Period 0 to 60; re-submitting create over an existing name drops its other actions and re-enables disabled actions | run |
| 42 | stepfunctions, internal/asl/exec.go | Heartbeat clock not reset on retry; Catch emits no TaskStateExited so the graph never paints "caught"; Express machines accept callback/activity Tasks at create then fail at run; delete leaves executions readable | run |
| 43 | kinesis/actions.go:547 etc. | OnDemandStreamCount hard-coded 0; PutResourcePolicy stores anything; no 50-tag limit; on-demand streams never rescale; console redirects Put to a LATEST read so the new record never shows | run |
| 44 | lambda/extras.go:496, invoke.go | DeleteFunction leaves its mappings and pollers running (messages receive-count toward DLQ); trigger batch size over 10 delivers 1 at a time; LogType Tail is the process ring buffer, not the invocation | read |

## B. Console wording that is false or stale (cheap to fix, all in templates/copy)

- panes.html:176 "doze-aws refuses this because AWS does, the rule comes from AWS's own service model" is printed for every coded refusal, including doze-only limits and hand-derived rules (seen on SQS, SSM, SNS, CFN, Logs, SFN, CW, APIGW).
- panes.html:172 "A policy decided this" is shown for non-IAM 403s (S3 BlockPublicPolicy, object lock).
- panes.html:173-174 "a bug in the emulator" is shown for a user function failing (Secrets rotation) and for documented 501 families (API Gateway).
- Refused/Denied colours: app.css:633-634 has Refused amber and Denied purple; the design (and brief) have them the other way round.
- Create-form hints that contradict the Configuration tab or the ledger: SQS "doze-aws does not evaluate queue policies"; S3 "not evaluated locally"; Secrets "invokes it on the schedule"; KMS "New backing material yearly".
- False confirms: S3/others "Policies and keys follow" on user rename; Step Functions delete "executions go with it" and stop "token stays unredeemable"; EventBridge bus delete "rules must be deleted first"; CFN delete "everything it created goes with it" (Retain); instance profile "remove its role first" (not enforced).
- API Gateway: "edits change the definition only until deploy" and "callers get it straight away" are false locally (the live API is served).
- Unreachable copy.go Unsel/None lines for sqs, sns, kinesis, ddb, kms, ssm, eb, cw, apigw, iam (the console opens the first resource whenever any exist).
- Fields that always read 0 / wrong: DynamoDB billing row hard-coded PAY_PER_REQUEST; table size always 0 B; Lambda code size 0 B; Lambda LastModified always "now"; Kinesis Arrived column UTC while the filter says local time.
- docs: SUPPORT.md says IAM default is `off` (it is `soft`); SUPPORT.md:2032 Kinesis "must exist before the filter names it" (not checked); docs/cloudformation.md:229 lists AWS::CloudWatch::* as skipped (mapped); Lambda concurrency ledger row says "no throttling" (it throttles).

## C. Unverified against real AWS (worth a place on the account-recording list)

Visibility-timeout vs function-timeout refusal at CreateEventSourceMapping; FIFO queue subscribed to a
standard topic refused at Subscribe; DeleteConflict for an instance profile holding a role; whether
UpdateAlias across key types and SetAlarmState same-state fire actions; Object Lock enabling on an
existing bucket; ARN/limit figures marked "from memory" on individual boards.
