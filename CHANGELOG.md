# Changelog

Hand-written, because the generated alternative was 334 commit subjects in
order. A section per version, newest first; `release.yml` pulls the section
matching the tag and makes it the GitHub release notes, and fails the release
if the tag has no section.

Versions are [semantic](https://semver.org). From 1.0.0 on, the Go API is a
promise: what `testdata/api.txt` records cannot be removed or changed without a
2.0.0. `docs/COMPATIBILITY.md` says what else is covered.

## 1.0.0

**Seventeen services, and the first release that promises anything.**

0.x shipped ten services and no compatibility statement. This one covers
seventeen and freezes the Go API, the CLI, the data directory layout and the
`.doze` hostname contract.

### Breaking

- **The Go API is 370 exported symbols, down from 1,867.** If you imported
  doze-aws as a library at 0.x, the packages you called are still there and
  still work — `New(Options) → *Server`, `dozeaws.NewStack`, `peers`,
  `awsident` — but everything that was never part of that contract is gone.
  What left: the thirteen `Store` types and their ~260 methods, 94 domain-model
  types (`sqs.Message`, `lambda.Function`, `iam.Role`, `apigateway.RestAPI` and
  the rest) that no exported function took or returned, the `console` and
  `provision` packages, the CloudFormation transpiler, and the 0.x
  data-directory migration, all now under `internal/`. The migration still runs
  automatically at startup — upgrading from 0.1.0–0.3.0 works as before; it is
  simply no longer something you can call.
  `awsident.ARN` and `awsident.GlobalARN` are deleted — use an
  `Identity`. They were the package-level helpers that always used the default
  account, and under `--account-id` the console displayed that default
  everywhere: computed ARNs, queue URLs and the credential scopes used for
  routing.
- **`internal/trace` is now `trace`.** The one thing that became MORE public:
  five services expose `SetTraceSink(trace.Sink)` and no caller outside the
  module could name the argument, so the method was decoration.
- **No Windows binaries.** The matrix listed `windows` and the build did not
  compile — the `.doze` zone locks its registry with `syscall.Flock`. v0.3.0
  shipped Windows because that dependency did not exist yet. Supporting it
  means porting the resolver setup, which is a project, not a build flag.

- **About sixty requests that used to succeed are now refused, because AWS
  refuses them.** If something that worked at 0.x fails after upgrading, it
  would have failed on deploy. Found by driving doze-aws with boto3
  (`conformance/`); almost none of these rules are in AWS's service models,
  which is how a model-derived audit missed them.
  - **SQS**: `CreateQueue` on an existing name with different attributes
    (`QueueNameExists` — it used to rewrite the queue); a batch that is empty,
    over ten, or repeats an `Id`; a visibility timeout past twelve hours; an
    attribute SQS does not have.
  - **SNS**: a topic name outside `[A-Za-z0-9_-]`; an empty message; an
    unknown topic attribute; a malformed `PublishBatch`; a FIFO name without
    `FifoTopic`, or the reverse.
  - **DynamoDB**: a bare reserved word in any expression (`SET name = :v`);
    a consistent read, or `ALL_ATTRIBUTES`, on a GSI that cannot give it; two
    writes to one key in a `BatchWriteItem`; `UpdateTimeToLive` to the state
    the table is already in.
  - **S3**: a bucket policy that is not JSON, or names another bucket.
  - **SSM**: a parameter name with characters SSM does not allow or in the
    `aws`/`ssm` namespace; `PutParameter` creating without a `Type`;
    `GetParametersByPath` without a leading slash.
  - **EventBridge**: `DeleteRule` on a rule that still has targets; a
    schedule on a custom bus; `rate(5 minute)`.
  - **IAM**: a permissions document as a trust policy, or a trust document as
    a permissions policy.
  - **CloudWatch Logs**: `PutLogEvents` to a stream nobody created (it used to
    create it), and a batch out of time order.
  - **Secrets Manager, KMS**: a secret name with characters it does not allow,
    a secret given both a string and a binary, and writes under `alias/aws/`.
  - **Lambda**: an update addressed to a published version or an alias (it
    used to change `$LATEST` instead); an invoke whose payload is not JSON;
    an event source mapping to a queue that does not exist.
  - **CloudFormation**: a stack name outside `[a-zA-Z][-a-zA-Z0-9]*`; a
    template whose `Ref`, `Fn::GetAtt` or `DependsOn` names nothing it
    declares; an `UpdateStack` that changes nothing ("No updates are to be
    performed.").
  - **API Gateway**: a path part or HTTP method it does not accept, and a
    deployment of an API with no methods or a method with no integration.
  - **CloudWatch**: `PutMetricData` under `AWS/` from a client; a datum
    with both a value and statistics; an alarm with both `Statistic` and
    `ExtendedStatistic`.
  - **Step Functions**: a `roleArn` that is not a role's ARN.

- **A stack that fails rolls back, and the call that started it answers 200.**
  `CreateStack` used to answer `400` when a resource failed, leave the stack
  in `CREATE_FAILED`, and leave whatever had been created in place. It now
  does what CloudFormation does: the call is accepted, what the failed create
  made is deleted, and the stack ends `ROLLBACK_COMPLETE`. A failed
  `UpdateStack` re-applies the previous template and ends
  `UPDATE_ROLLBACK_COMPLETE`, keeping the template it had before.
  `DisableRollback`, `OnFailure=DO_NOTHING` and `OnFailure=DELETE` are
  honoured. If you scripted against the `400`, poll the stack instead — as you
  would on AWS. A rollback deletes only what the failed deploy itself created.
- **A stack no longer takes over a resource it did not make.** A template
  naming a queue, table, bucket, function, topic, secret, parameter, log
  group, rule, alias, alarm, dashboard, state machine, connection or API
  destination that already existed used to adopt it — change it to match, and
  delete it with the stack. AWS fails that create (`Resource of type ... with
  identifier ... already exists.`) and rolls back, and so does doze-aws now,
  leaving the existing resource untouched. An update that adds such a name
  fails and rolls back the same way. `doze-aws apply` with a stackfile still
  converges onto what is there; it describes the account, it does not own it.
- **`UpdateStack` deletes what the new template drops.** A resource taken out
  of a template used to stay running, owned by no stack.
- **`DeletionPolicy: Retain` is honoured**, by `DeleteStack`, by an update
  that drops the resource, and by a rollback. It was parsed and ignored, so a
  retained bucket went with its stack.
- **Template parameter constraints are enforced**: `AllowedValues`,
  `MinValue`/`MaxValue`, `MinLength`/`MaxLength` and `AllowedPattern`. A value
  the template forbids is refused at the call and no stack is made.

### Added since 0.3.0

- **SNS FIFO topics deliver.** They were accepted and delivered nothing: the
  group id never reached the FIFO queue. `Publish` returns a `SequenceNumber`.
- **`conformance/`**: 92 boto3 scenarios across all 17 services, written to be
  recorded against a real AWS account and compared with doze-aws. No
  recordings yet; the suite reports every response as unverified until there
  are.

- **Seven services**: IAM (three enforcement modes, resource policies,
  least-privilege generation), CloudFormation and SAM (transpiled onto the
  existing convergent apply — `sam deploy` and `cdk deploy` work unmodified),
  API Gateway (REST v1 and HTTP v2, deploy and invoke), Step Functions
  (Standard and Express, JSONPath and JSONata, versions, aliases, activities,
  Distributed Map), CloudWatch Logs, CloudWatch Metrics and Alarms, Kinesis.
- **The `.doze` zone.** doze-aws answers on a name derived from the directory —
  `aws.harbour.doze` — set up once per machine with one sudo prompt. In a
  container or CI, `--listen` skips it.
- **The console opens on the wire**: every call your app made, newest first,
  with the work each one caused nested underneath, a request id you can quote,
  and copy-as-curl on each row. Plus per-service pages that read and write real
  resources, and a fidelity ledger saying which operations are real.
- **An honest support ledger.** `docs/SUPPORT.md` records all 385 operation
  rows at one of three tiers — functional, cosmetic round-trip, or a stub that
  refuses cleanly — and `docs/not-built.md` gives an argument for each of the
  81 stubs. Both are parsed at runtime by the console and checked by tests, so
  neither can drift from the code.
- **Model-derived rejection parity.** Every constraint in AWS's own service
  models is replayed as a mutation of a baseline the service is first proved to
  accept, because a request refused for the wrong reason looks exactly like a
  pass.
- **Measured budgets that fail the build**: the binary (22,622,368 bytes for
  linux/amd64), what it links (six modules), each embedded tree, boot time,
  idle footprint, and the exported API surface.

### Changed

- Console: the SQS Consume tab keeps the messages a receive returned, with a
  countdown on each, until they are deleted, released or their visibility
  timeout lapses; acting on one used to hide the rest while they stayed
  invisible on the queue.
- Console: a success message is a toast that stays seven seconds and waits
  while hovered or focused, wherever the action came from. A banner is kept
  only for a value you must copy before it leaves (a new access key's secret).
  Errors stay beside the control that failed.
- Console: detail and output panels scroll into view when a button fills them,
  and detail panels have a close button. Traffic, DynamoDB item and SQS message
  rows no longer nest controls inside a button role; a real button in each row
  carries the keyboard and screen-reader behaviour.
- Console: below 768px it says it is built for desktop widths.
- A ranged S3 `GetObject` no longer carries the full-object checksum. boto3
  validates whatever checksum arrives against the bytes it received, so every
  ranged read of an object it had uploaded raised `FlexibleChecksumError`.
- SQS accepts message attribute names in `ReceiveMessage.AttributeNames`
  (`SentTimestamp` and the rest) — the spelling boto3's documentation uses,
  refused until now — returns `SenderId` on every message, and
  `SequenceNumber` on FIFO sends and receives.
- EventBridge buses can be tagged. `TagResource` used to refuse a bus ARN.
- Lambda's control plane is routed from AWS's own service model (chi), and
  the operation a request matched is what validation, the IAM guard and the
  wire page read. Three visible effects:
  - A Lambda path carries its operation's API date, as an SDK sends it —
    concurrency under `2017-10-31` (read under `2019-09-30`), account settings
    under `2016-08-19`. The old dispatch ignored the date.
  - A known path with the wrong method is a `405`; some used to be `404`.
  - `ListFunctionUrlConfigs` returns `{"FunctionUrlConfigs": [...]}` (empty
    for a function with no URL) instead of a `404`.
- API Gateway's control planes (REST and HTTP) are routed the same way, from
  the model's tables. A known path with the wrong method is a `405` (some
  were `404`), and an unknown path is a `404` naming the path. The families
  doze-aws does not model (`models`, `domainnames`, `portals`, …) are still
  refused with `501`, now as explicit routes.
- S3 is routed from the model's table too, and the operation a request is —
  not a second `q.Has` chain — decides its validation and its handler. A
  virtual-hosted request is validated exactly as a path-style one is (it never
  was). Requests that used to fall through to the nearest handler are refused
  with a `405`: an object-only operation sent to a bucket (`GET /bucket?retention`
  used to list it), a request with no bucket, and a DELETE of `?logging`,
  `?accelerate`, `?requestPayment` or `?notification` — none of which is an S3
  operation.
- The console is routed with chi, group by group (`routes_<area>.go`), and an
  unmatched GET is the console's own not-found page at any depth. A route list
  held to the 371 the old router served (`testdata/routes.txt`) guards the move.
- With the console enabled, an S3 key with two slashes in a row (or a `.`
  segment) reaches S3 as it was sent. The top-level `http.ServeMux` used to
  clean the path and redirect it, so the key you wrote was not the key you got.
- **Enforce mode authorizes S3 and Lambda as AWS names the permission.** The
  action was guessed from the method and path, and was wrong for about forty
  operations — a policy that allowed `lambda:AddPermission` denied
  AddPermission (the guess was `lambda:CreatePermission`), one that allowed
  `s3:PutBucketCORS` could not delete a bucket's CORS, and an access log
  generated policies full of actions AWS has never heard of (`s3:GetAcl`,
  `lambda:GetPermission`). The operation is now the one the router matches and
  the action is the one AWS publishes: from the service model's own
  `aws.iam#iamAction` for Lambda, from the Service Authorization Reference for
  S3 (`dzaudit iam`). A request naming `?versionId` is `s3:GetObjectVersion`
  (and its kin), and a virtual-hosted request is authorized as the object it
  names, not as a bucket called after its key. The weekly drift check now
  refreshes the tables. If a policy you wrote for AWS was denied here, this is
  why; if you wrote one against the old names (`lambda:CreatePermission`), it
  grants nothing on AWS either.
- API Gateway ids are random. They were taken from the clock, so two REST
  APIs, HTTP APIs, keys or usage plans created in the same instant got the
  same id and the second silently replaced the first.
- REST `DeleteDeployment` refuses a deployment a stage still serves, and one
  that does not exist, as AWS and the HTTP plane already did. It used to
  delete it and leave the stage pointing at nothing.
- `DescribeStacks` without a name lists live stacks only. Deleted ones still
  answer to their id and still appear in `ListStacks`, as on AWS.
- A Kinesis shard closed by a reshard always reports its
  `EndingSequenceNumber`. One that never held a record left it out, so
  `ListShards` and the console showed the parent as still open.
- Console, found by driving every route from a browser:
  - Pasting an ARN into ⌘K finds its resource.
  - The SQS page's counts outside the message panel follow sends and
    receives without a reload.
  - The SQS composer sends a burst: Copies above one goes as a single
    `SendMessageBatch`.
  - Secrets Manager "Make current" no longer blanks the page.
  - Removing a secret's resource policy asks first.
  - The IAM authorization export loads, and an IAM user can be renamed from
    its page.
  - A truncated Kinesis record opens in full, and stream encryption can be
    turned off.
  - Aborting an S3 multipart upload updates the list in place.
  - Drawers scroll on a short window, so an S3 object's tags and lock
    settings are reachable.
  - A result panel shows an AWS refusal as its code and message, not the
    XML envelope.
  - A success banner no longer reappears when you move to another tab right
    after it.
- Console, found by crawling every page with axe and a screenshot at two
  widths and two themes:
  - Every link the wire renders opens a real page. A dashboard and a metric
    namespace were linked as alarms, a KMS alias as a key id, a secret with a
    slash as its last segment, and a Lambda concurrency call was named
    `PutObject`.
  - Step Functions calls on the wire link to their machine, and the service
    filter offers every service rather than eleven.
  - The HTTP API Tags tab works; it sent the REST API ARN shape and was
    refused.
  - Accessibility: the page declares its language, has a main landmark and a
    level-one heading; every code editor, switch, select and icon button has
    a name; scrolling code and log panes are reachable from the keyboard;
    text meets WCAG AA contrast in both themes.
  - Deleting messages, revoking an SQS or SNS permission, removing an API
    response, detaching a key from a plan and repointing a live stage ask
    first. A declined repoint puts the picker back.
  - An EventBridge bus can be deleted from its page; log groups have a New
    button.
  - Service names fit their list header, log groups show the part of the
    name that tells them apart, and latencies read `0.22ms`, not
    `0.215416ms`.
- A Lambda function's output is all there when `Invoke` returns. Under load
  the last lines of an invocation could arrive after its `END`, or under the
  next invocation's request id, because nothing made the output pipe drain
  before the result was acted on.
- A function process that died leaving a line without its newline no longer
  hangs its runner. The reaper deadlocked on its own lock, so the invocation
  waited out its whole timeout and every later invoke of that function
  blocked.
- A stack whose resource fails says so in its events. The trail used to
  report every resource, the failed one included, as `CREATE_COMPLETE`.
- Cold start is ~20 ms and an untouched data directory is 158 bytes across
  three files. Databases are created on first use, so a stack nobody speaks to
  creates none. It was 793 ms and 2.1 MB across seventeen files.
- Data is laid out per region, with IAM and STS under `_global`.

## 0.3.0

The glance API the sibling `doze` dashboard reads, and queue URLs that respect
a path prefix. One commit after 0.2.0.

## 0.2.0

74 commits. The console arrived, topology-agnostic and with reachable function
endpoints; DynamoDB Streams with S3→Lambda proven end to end; the god-files
were split and the protocol codecs de-duplicated.

## 0.1.0

First release: ten services — S3, DynamoDB, SQS, SNS, STS, KMS, SSM Parameter
Store, Secrets Manager, EventBridge, Lambda — speaking the real wire protocols
to both AWS SDK generations, with Lambda running functions as local processes.
These three releases also shipped Windows binaries, which built at the time.
