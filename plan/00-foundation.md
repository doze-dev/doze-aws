# Stage 0: the foundation

Work that every service depends on. It is done once, first, so that sixteen
services do not each fix the same shared defect in a slightly different way.

It has three parts: **A** shared console behaviour, **B** the evidence, test
machinery and shared service code the per-service protocol leans on, **C** the
console shell from the design. A cross-cutting part **D** (the map, the fidelity dialog, the "cannot
reach" banner) is listed here and scheduled after the services, because it is
about all of them at once.

Paths are relative to the repo root. Line numbers are as found on 2026-10-10
and will drift; the symbol named beside each one will not.

## Progress

Updated in the same commit as the work. "Landed" means the item's "done when"
is met and its test is named; "partly" says what is left.

| Item | State | Commit | Note |
|---|---|---|---|
| B6 the 501 refusal and its proof | landed | 4241369, 9fa0829 | `awshttp.NotBuilt`; no Go v1/v2, boto3 or JavaScript SDK retries it. |
| B6 the model sweep | partly | 0223f75 | Harness and a frozen baseline of 653 entries (542 legacy refusals, 111 false "unknown") are in. Each service moves its entries to `notbuilt` in its own stage. |
| B7 blob lengths in bytes | landed | bed7d64 | KMS, Kinesis, Secrets Manager. SSM and Lambda have a blob member only on operations the service refuses. |
| B2 suites under a second identity | partly | (next commit) | The e2e suite boots under `E2E_ACCOUNT_ID`/`E2E_REGION`. Of 238 tests, 234 passed on the first run; the 4 failures were two test literals (fixed) and two **product bugs**: EventBridge archives made from the console (hard-coded bus ARN) and the S3 share link (signs for `us-east-1`). Each is fixed in its stage, and then the second pass joins CI. The Go side already has `TestEveryServiceMintsTheConfiguredIdentity`. |
| A1 a refusal's code, from every wire shape | landed | (next commit) | S3's bare `<Error>` root and the code-in-a-header shape of Lambda and API Gateway. `TestParseRefusalReadsEveryShape`, and `TestTheConsoleCanReadEveryServicesRefusal` drives a real refusal from each of 18 services. Red for the header shape; the S3 shape is pinned by the table. |
| B3 mutation per package | landed | (next commit) | `task mutate:record` / `mutate:check`; baselines in `testdata/mutation.json`. |
| A8 identity written into code | partly | (next commit) | The check and a frozen list of 12 sites are in; each site is fixed in its service's stage. |
| B11 paging, once | landed | (next commit) | `internal/awspage` and `dozetest.AssertPaginates`. Services adopt in their stages. |
| B10 the guard passes every condition key | landed | (next commit) | `iamguard.RequestContext`, one function for both paths; D7 decides `aws:SecureTransport`. Enforce tests for the services that evaluate a resource policy re-run green. |
| B9 one stored-policy validator | partly | (next commit) | `iampolicy.ValidateResourcePolicy` and its table test are in. S3 and IAM already validate their own; the other five wire it in their stages. |
| B12 `provision` has two callers | landed | (next commit) | Resolved without a mode: `Force` already carries it. Pinned by `TestApplyAdoptsWhatIsThereAndDeletesNothing`. CFN-1 is a one-line mapper change in the CloudFormation stage. |
| A2 the "why" line follows the state | landed | (next commit) | A refusal is classified from facts the server stamped (`X-Doze-Not-Built`, `X-Doze-Denied`, `X-Doze-Local-Limit`), never from a status or a code's spelling. Five states, each with its own label and sentence: Refused, Refused here, Denied, Not built, Failed. `TestCallStateClassifiesFromFacts`. **Found on the way:** SQS, SNS and S3 rebuilt the guard's error and dropped the stamp, so a policy denial would have read as a refusal; `TestEveryServicesDenialIsStampedAsOne` was red for those three and is green. |
| A3 Refused purple, Denied amber | landed | (next commit) | `--refused` replaces `--denied`; Not built is dashed and neutral, Failed stays red. Wire rows, inspector and `fail_inline` read one copy table (`stateCopyFor`). |
| A4 the key/value editor keeps types | landed | (next commit) | A row holds its JSON type and the token it was written as, so `5` stays `5`, `1.0` stays `1.0` and a 20-digit integer is not rounded; key order is kept; a repeated key makes the tab unavailable rather than dropping a value. A per-row type menu is how a type changes on purpose, and a number row holding words is saved as text and says so. Two e2e tests in `secretsmanager.spec.ts`, red without the change. |
| A5 saving tags is one write per direction | landed | (next commit) | `SetResourceTags` / `RemoveResourceTags` replace the per-key calls: one Untag, then one Tag (Kinesis chunked at its ten per call). A refused save redraws the editor from the resource and says what was applied. Three tests in `tags_batch_test.go` over six services. Found on the way: SQS accepts a 129-character tag key (SQS-9, added to its stage). |
| A6 a header button's refusal lands under the header | landed | (next commit) | A new rung in `errSlot` places a refusal from `.det-h` directly under it, in full. `s3.spec.ts` deletes a non-empty bucket; red without the rung. The full suite runs at the end of stage 0 for any test that leaned on the old toast. |
| A7 copy nobody can reach | landed | (next commit) | Three named cases (unselected, first, home) and each service is assigned one; `copy.go` holds only what its case can show. `TestEveryLineOfEmptyCopyIsReachable` drives all 16 services empty and then with one resource. Removed 14 `Unsel` lines that could never show, EventBridge's and IAM's lines entirely (the default bus and the access log always exist), CloudWatch's `None`. API Gateway and CloudFormation have create forms and the catalogue now says so. |
| A9 the fidelity dialog finds every ledger | landed | (next commit) | `cw` maps to the CloudWatch ledger and the API Gateway panel carries REST and HTTP sections. `TestEveryRailServiceHasALedger` opens the panel for every rail entry. |
| A10 a cascade hop shows the status it answered | landed | (next commit) | `trace.Event` carries `Status` and `Denied`, read from the error through `HTTPStatuser`/`Denier`; `peercall` errors carry both. A denial on an alarm's Publish is a 403 and reads as Denied. `TestAFailedStepRecordsTheStatusItWasAnswered`, `TestCascadeStatusIsWhatTheTargetAnswered`. |
| B8 what the generator drops | open | | Found so far: see the note under B8. |

## A. Shared console behaviour

Each item was reported by more than one service's grounding. "Seen in" names them.

### A1. A refusal's code is lost for two of the three wire formats — P2

- **What.** `parseRefusal` (`internal/console/traffic.go`, about line 1008 to
  1031) reads `__type` or `code` from JSON and `Error>Code` from XML. S3's error
  document has `<Error>` as its **root**, so the path never matches: every S3
  refusal renders as raw XML under a bare "Refused". Lambda puts its code in the
  `x-amzn-errortype` header, and the console client (`internal/console/client.go`,
  about 181 to 195) drops response headers, so every Lambda refusal has no code.
- **Seen in.** S3 (run), Lambda (read).
- **Done when.** One function turns any service's error response into
  `{code, message, status}` for all three shapes (JSON body, XML with either
  root, header-carried code), with a table test holding one real response from
  each of the 17 services, captured from a running binary.

### A2. The "why" line under a refusal says the same thing whatever happened — P2

- **What.** `fail_inline` (`internal/console/templates/panes.html`, about 164 to
  179) prints "doze-aws refuses this because AWS does — the rule comes from
  AWS's own service model" for every coded refusal. It is false for hand-derived
  rules (SQS and SSM names, SNS filter policies), for doze-only limits (Logs
  regex patterns, Step Functions `.sync` on unserved services), and for
  duplicates and state errors. The classification in
  `internal/console/handlers_surfaces.go` (about 265 to 272) also sends any code
  containing "NotAuthorized" to Denied (KMS's reserved-alias refusal is input,
  not policy), sends S3's BlockPublicPolicy and object-lock 403s to "a policy
  decided this", and sends every 501 and a user function's own failure to
  "a bug in the emulator".
- **Seen in.** SQS, SSM, SNS, Kinesis, KMS, S3, Logs, Step Functions,
  CloudFormation, CloudWatch, API Gateway, Secrets Manager.
- **Done when.** A refusal is classified from facts, not from the code's
  spelling, into exactly one of:

  | State | Decided by | The line under it |
  |---|---|---|
  | Refused | a 4xx from model-derived or documented validation | "AWS refuses this too." Names the source: the service model, or the documented rule. |
  | Refused here | a 4xx from a limit that is doze-aws's own | "AWS would accept this; doze-aws does not serve it." Links the ledger row. |
  | Denied | the IAM guard's decision (the `X-Doze-*` decision headers), nothing else | "A policy decided this." Links the access log. |
  | Not implemented | a 501 from a declined or deferred family | "Not built here." Links `not-built.md`'s reason. |
  | Failed | a 5xx that is doze-aws's own | "This is doze-aws failing, and worth reporting." |
  | Function error | a peer the user owns answered with an error | Names the function; never "a bug in the emulator". |

  Each service's refusal carries which rule produced it, so the first two can be
  told apart; no service carries that today (Step Functions' `refuseUnrunnable`,
  Logs' Firehose and own-group refusals, CloudFormation's "does not serve ECS",
  EventBridge's and SNS's hand-derived rules all look alike on the wire). Three
  cases the table must also cover: a refusal with **no code** (the console's own,
  such as invalid item JSON); a **gateway answer that is not a 200** on an Invoke
  tab, which is the user's API talking, not a refusal; and "Not implemented",
  which is read from the `X-Doze-Not-Built` header, not the status (decision D5:
  every stub answers 501 `NotImplemented` with that header; today API Gateway
  answers 501, CloudWatch and DynamoDB 400 `UnsupportedOperationException`,
  CloudFormation `ValidationError`, Parameter Store a false `InvalidAction`).
  A test per state, and one per service for a known example of each state it can
  produce.

### A3. Refused is purple, Denied is amber — P3, decision D3

- **What.** `internal/console/static/app.css` (about 633 to 634) has Refused
  amber and Denied purple. The design has them the other way round, and red only
  for Failed.
- **Done when.** The colours follow the decision, each state has a word as well
  as a colour, and the wire, the inspector and `fail_inline` agree.

### A4. The key/value editor turns numbers and booleans into strings — P0

- **What.** `internal/console/templates/layout.html` (about 32 to 59): `parse`
  stringifies every value and `sync` writes it back as a string. Editing one row
  of `{"retries": 5, "jitter": true}` saves `"retries": "4", "jitter": "true"`.
  It is the default editor for any flat JSON object in Secrets Manager and
  Parameter Store.
- **Seen in.** Secrets Manager, Parameter Store (run).
- **Done when.** A value keeps its JSON type through an edit unless the user
  changes the type on purpose. A browser test edits one field of a mixed-type
  object and reads the stored value back byte for byte.

### A5. Saving tags is not atomic — P1

- **What.** `internal/console/handlers_tags.go` (about 67) writes one tag per
  call and stops at the first refusal, leaving a random subset written and the
  editor showing the old draft.
- **Seen in.** Parameter Store (run); Logs, SNS, Kinesis, Secrets Manager (read).
- **Done when.** One Save is one TagResource and one UntagResource where the
  service takes a batch; where it cannot, the editor re-renders from what the
  service now holds and says which keys were refused. A test per service.

### A6. A refusal from a header button has nowhere to land — P2

- **What.** The inline-error ladder in `internal/console/static/shell.js`
  (about 200 to 290) has no slot in `.det-h`, where Delete lives. A
  deletion-protection or export-in-use refusal falls to a toast, stripped and
  cut at 140 characters.
- **Seen in.** DynamoDB, CloudFormation.
- **Done when.** A refusal from any header action renders inline under the
  header, in full. A test deletes a protected resource and reads the whole message.

### A7. Copy nobody can reach — P3

- **What.** `internal/console/copy.go` holds a "nothing selected" line per
  service. Most services never render it, for one of two reasons: they open the
  first resource whenever one exists (SQS, SNS, S3, Kinesis, DynamoDB, KMS,
  EventBridge, API Gateway), or their home is a page of its own (CloudWatch,
  Parameter Store's "Query a path", Lambda's account and layers, IAM's access log). Several `None` lines are stale as well
  ("IAM is off by default"; "no create form on purpose" for API Gateway and
  CloudFormation, which have one).
- **Done when.** One rule with three named cases (nothing selected, first
  resource, a home page of its own), each service assigned to one, and `copy.go`
  holding exactly the lines its case can show. A test renders every line in `copy.go`.

### A8. An account and a region are written into the console — P0

- **What.** `busARN` (`internal/console/handlers_eb.go`, about 272) and
  `archiveARN` (`client_eb.go`, about 387) build ARNs from `us-east-1` and
  `000000000000`; so does the test event (`handlers_eb.go`, about 165), the
  Lambda create role (`client_lambda.go`, about 310) and, in the service itself,
  `mappingView` (`lambda/extras.go`, about 323) and the `awsRegion` in the SQS
  event a function receives (`pollSQS`, about 492); KMS's `defaultKeyPolicy` and
  `KeyMetadata.AWSAccountId`; SNS's `subscriptionList` owner; DynamoDB's
  `describeEndpoints`, which returns AWS's real hostname. Under another
  `--account-id` or `--region`: archives made from the console capture nothing,
  the event tester gives wrong verdicts for patterns on `account` or `region`,
  and with enforce on **every new KMS key is locked out at birth**.
- **Seen in.** EventBridge, Lambda, KMS (run); SNS, DynamoDB (read).
- **Done when.** No literal account or region remains in `internal/console` or
  any service package outside `awsident`. A build-time check greps for both
  literals. The browser suite runs once under a non-default identity.

  **The check landed** as `identityliterals_test.go`, which reads the source
  and freezes today's sites in `testdata/identity_literals.txt`, both ways, so
  a new literal fails and a fixed one has to leave the list. Seventeen sites in eleven
  files (the first scan found twelve; matching the region as a substring found the
  S3 share link's signing scope and CloudFormation's internal S3 call), each fixed in the stage that owns it (KMS `defaultKeyPolicy` and
  `AWSAccountId`, SNS `subscriptionList`, Lambda `mappingView`, `functions.go`
  and the event's `awsRegion`, EventBridge's console ARNs and test event,
  `client_lambda.go`'s role, `provision`'s role, `asl/inspect.go`). Four files
  hold the digits for another reason (a shard id, an all-zero UUID, a flag's
  help text) and are in the test's `notIdentity` map with the reason.

### A9. The fidelity dialog cannot find two ledgers — P2

- **What.** `internal/console/info.go` (about 23 to 30) has no `cw` to
  `cloudwatch` mapping, so CloudWatch's dialog says "No ledger for this service",
  and it maps `apigw` to the REST section only, so an HTTP API shows REST rows.
- **Done when.** Every service key resolves to its section, HTTP APIs to
  `apigatewayv2`, and a test opens the dialog for every rail entry.

### A10. A cascade error is always shown as 500 — P2

- **What.** `cascadeStatus` (`internal/console/traffic.go`, about 234) maps
  every error in a cascaded call to 500, so a policy denial on an alarm's
  Publish reads as a server error on the wire.
- **Done when.** A cascaded call shows the status it answered.

### A11. A form's CLI preview is not always the call the form makes — P3

- **What.** The preview omits parts of several forms: DynamoDB create (indexes,
  TTL), Logs create (the retention call), HTTP API create (CORS).
- **Done when.** A rule and a test: for every form with a preview, the command
  shown produces the same request the form submits.

### A12. Toasts — P3

- **What.** A toast lasts 3.2 s. The design holds it while the pointer is on it
  and long enough to read. Several toasts report success for a no-op (Kinesis
  "Retention set" with no call, Step Functions "Execution stopped" on a finished
  one, Parameter Store "removed" for an absent label).
- **Done when.** Lifetime and hover follow the design, an error is never only a
  toast, and no toast claims an effect that did not happen.

### A13. Confirms that say things that are not true — P2

- **What.** Found so far: Step Functions delete ("its executions go with it")
  and stop ("a parked token stays unredeemable"); EventBridge bus delete ("its
  rules must be deleted first"); CloudFormation delete ("everything it created
  goes with it", false for Retain); IAM rename ("policies and keys follow");
  instance profile delete ("remove its role first", not enforced); Lambda layer
  version delete ("functions already configured with it keep running", false on
  the next cold start).
- **Done when.** Each service's stage reads every `hx-confirm` in its templates
  against the code. The foundation's part is the confirm component from the
  design: it names the thing, says what is lost, and the button is the verb.

### A14. Links built from names are not escaped — P2

- **What.** Templates build URLs with `printf "?name=%s"`, so a `+` in a name
  arrives as a space. Secrets Manager allows `+` and `=` in names; Parameter
  Store's templates use the same pattern.
- **Done when.** Every link built from a resource name goes through one
  escaping helper, and a test opens a resource whose name holds `+ = / %` in
  every service that allows them.

### A15. A partial re-render shows less than the page it replaces — P2

- **What.** Each HTMX handler passes its own subset of the page's data to the
  fragment it re-renders. After a save in Secrets Manager the Reveal and Copy
  toolbar is gone, the version count is wrong, and the resource policy reads
  "Not set"; after a save in Parameter Store a SecureString is shown in clear
  with its badge lost.
- **Seen in.** Secrets Manager, Parameter Store (run).
- **Done when.** One view model per detail fragment, built by one function that
  both the full page and every partial call, and a test per service that
  compares the fragment after an action with the same region of a full reload.

### A16. A failed full-page load has no page — P2

- **What.** `c.fail` on a full-page GET returns the bare error fragment with no
  chrome: `/ddb/nope`, or Kinesis's Records tab with a disabled key.
- **Done when.** A failed GET renders inside the shell with the list pane, and
  a test loads a missing resource in every service.

### A17. Times have no stated zone — P3

- **What.** Kinesis formats arrival times with `.UTC()` and shows them without a
  zone beside a filter labelled "local time".
- **Done when.** One rule for every time the console shows (local, with the
  zone on hover and UTC available), one formatter, and no `.UTC()` format
  call left in `internal/console`.

### A18. "Nothing feeds this yet" where the graph cannot know — P2

- **What.** The neighbours strip states that nothing feeds a resource whenever
  the wiring graph has no edge into it. For an EventBridge rule, a Kinesis
  stream fed by an SDK producer or a Logs subscription, a state machine and an
  alarm, the graph cannot see the producer, so the sentence is a guess stated as
  a fact.
- **Done when.** The strip says "nothing feeds this" only for node kinds whose
  every producer the graph models; otherwise it shows what it knows and says no
  more. State machines and alarms, which are not nodes, take their strip from
  their own data or have none.

## B. Evidence, test machinery and shared service code

### B1. A claims table per service

A file per service, `conformance/claims/<service>.md` or the equivalent in the
harness, listing each behaviour claim with its source (`model`, `recorded`,
`doc`, `memory`) and the scenario that would record it. The per-service
"recording queue" sections seed it.

**Done when.** The format exists, SQS's is filled in as the worked example, and
the conformance summary prints how many claims per service are `recorded`.

### B2. Run the suites under a second identity

**Done when.** `check:full` and `test:e2e` each have a pass under a non-default
`--account-id` and `--region`, in CI.

### B3. Mutation per package

`task mutate` runs gremlins. The protocol needs it scoped to one service
package with a recorded baseline.

**Done when.** `task mutate -- ./sqs/...` (or the equivalent) runs in reasonable
time, and the baseline survivors per package are written down.

**Landed as `task mutate:record` and `task mutate:check`**
(`scripts/mutation-baseline.ts`, state in `testdata/mutation.json`). `record`
runs gremlins on one package and keeps its counts and its lived mutants as
`file:line:type`; `check` runs it again and fails on a survivor the baseline
does not have, so "did this stage leave the suite weaker" has an answer that is
not a percentage. A mutant that now dies is reported, not an error. A package
takes minutes; the baselines for the packages below were recorded in one
unattended run. Packages that
hold a service's logic outside its own directory get their own baseline:
`internal/asl`, `internal/ddb`, `internal/s3store`, `internal/lambdaruntime`,
`internal/iampolicy`, `internal/iamguard`, `internal/cfn`, `internal/provision`.

### B4. Settings tables the console can read

The teaching lines need, per setting: AWS name, unit, range or enum, default,
and tier. The range and enum are already in each service's generated
`validate.go`; the tier is in the ledger.

Several services showed the generated table is not enough on its own. S3's and
SQS's models carry almost no ranges, so their rows are hand-written with a
`doc` source each. Logs has limits that live only in code (`validRetention`,
the per-group filter caps). DynamoDB needs "enforced by what" and a tier that
differs by operation (`StreamSpecification` on create against update). API
Gateway's settings are patch paths, not flat attributes. CloudFormation's
template parameters are the user's, not the model's, and are a second source.

**Done when.** One accessor returns that record for a service's setting
(name, unit, range or enum, default, tier per operation, what enforces it, and
its source), built lazily on first use (idle heap unchanged, held by
`task lightness`), with a test that every setting a console form shows has a
record.

### B5. A restart test helper

**Done when.** One helper writes through a service, restarts it over the same
data directory, and reads back, and the upgrade fixture still passes.

### B6. The model sweep: nothing absent — P0

Every service document found operations that are neither functional nor
honestly refused: 49 in Parameter Store answer a false `InvalidAction`, 36 in
Lambda answer "unknown path", 20 in API Gateway have no tier (five of them
updates that Terraform and CDK send), 6 in Kinesis, 6 in IAM and STS. The
coverage helper `dozetest.Coverage` allows an `Unreached` list, which is how
they pass today.

**The refusal, decided (D5).** HTTP 501, code `NotImplemented` in the
protocol's own envelope (Query XML, JSON 1.0 and 1.1, REST JSON, REST XML,
CBOR), a message naming the operation and the reason from `not-built.md`, and
the header `X-Doze-Not-Built: declined` or `deferred`. One function per
protocol writes it; no service writes its own. Before any service is changed,
a test drives one refused operation through each SDK the repo already tests
with (Go v1 and v2, boto3, the JavaScript SDK) and asserts the caller sees the
error **once**: the belief that no default retryer retries a 501 is checked
here, not assumed. If one does, the status is reconsidered before the rule spreads.

**Done when.** One generic test, run for every service: each operation in the
service's `ops_*.json` answers 2xx, a modelled error, or that refusal, and
nothing else. `Unreached` is empty for every service or the
helper no longer has it. The ledger's operation counts are generated from the
same file, so they cannot drift (five services' counts were wrong).

### B7. Blob lengths are measured in base64 — P0

`internal/modelcheck` applies a `@length` on a blob to the base64 text, not the
bytes. KMS refuses a 3,073 to 4,096-byte plaintext that AWS accepts.

**Done when.** Blob lengths are measured in bytes, with a test at each
boundary, and every service with a length on a blob member is re-run.

### B8. What the generator drops

Logs found the tag-key rule unenforced on write although the parity count says
every constraint is; Parameter Store asks whether the names-list length is in
the model and dropped.

**Done when.** `cmd/dzaudit` is checked for map-key constraints and list-length
constraints, and each service's table is regenerated; any new constraint
becomes a failing parity case in the owning service's stage.

**Found while doing B6 and B7.** The weekly drift job regenerates
`shapes_*.json`, `ops_*.json` and `iam_*.json`, and nothing else. The
committed `cases_*.json` and `routes_*.json` have never been regenerated
against the current models, and have drifted: a fresh `dzaudit cases kms` has
472 cases against 263 committed, and a fresh `routes` for Lambda has 88
operations against 48. The routes files are also, by their parity tests'
reading, "the routed subset", so they cannot simply be regenerated; B6 added
`modelroutes_*.json` for the complete bindings and put those under the drift
job. Each service's stage regenerates its own `cases_*.json` and fixes what the
new cases find, which is where the "routed and has no baseline" failures in
API Gateway v2 come from. Until then the drift job should also diff the cases,
so the staleness is visible rather than silent.

### B9. One validator for a stored resource policy — P1

SNS, Kinesis, KMS, Secrets Manager and API Gateway store a policy that is not
a policy (`"not json"`, `{"hello":1}`) and the guard then silently ignores it.
SQS is not established.

**Done when.** One function validates a resource policy when it is stored, the
way IAM's own create does. A document that cannot be parsed is refused with the
service's own code.

**Landed as `iampolicy.ValidateResourcePolicy`**, which returns a typed
`Problem` (not an error) so each service says it in its own code and words: SQS
`InvalidAttributeValue`, KMS and Secrets Manager `MalformedPolicyDocumentException`,
API Gateway `BadRequestException`, Kinesis and SNS their own, each a `doc`
claim in that service's document until recorded. It checks what every
resource policy shares: a policy at all, a Statement, only the two Versions IAM
accepts, Effect, Action or NotAction, a Principal or NotPrincipal (the rule an
identity policy does not have), a Resource or NotResource, and not both of a
pair. The service's own rules (a key policy's Resource is `*`, a bucket policy
names its own bucket) run after it. **Wiring is per service, in its stage**,
because the code and the wording are the service's: SQS-8, SNS-16, KIN-3,
KMS-3, SM (policy), AGW-7.

### B10. The guard passes every condition key the ledger says it does — P0

`internal/iamguard` hands resource-policy evaluation four context keys.
`aws:SecureTransport`, `aws:SourceIp`, `aws:UserAgent` and others never arrive,
so a "deny plain HTTP" bucket policy never denies. This is here, not in the
IAM stage, because fixing it at stage 15 would mean re-running the enforce
tests of every service already signed off.

**Done when.** Every key `SUPPORT.md` lists as supplied is supplied, with a test
per key, and suite pass B2 includes a caller that is **not** the root (a user
key at minimum; a role session once the IAM stage makes those real).

**Landed.** `iamguard.RequestContext` builds the context once, and both the
guard (every service's resource policy) and the IAM service (identity
policies) call it. The resource-policy path had four keys and the identity path
seven; it is now the same seven on both: `aws:PrincipalArn`,
`aws:PrincipalAccount`, `aws:RequestedRegion`, `aws:SecureTransport`,
`aws:username` (a user's name, absent for a role or the root),
`aws:SourceIp`, `aws:UserAgent`, with `aws:SourceAccount` and `aws:SourceArn`
added by the guard for service principals. Tested per key, red against the old
guard.

**Decision D7: `aws:SecureTransport` is `true`.** Every local request is plain
HTTP, so measuring the transport would make "deny when SecureTransport is
false" (CDK's `enforceSSL`, Serverless's deployment bucket, the standard S3
hardening policy) refuse every request. On AWS the same SDK uses the HTTPS
endpoint by default and the policy never fires, so the local endpoint stands in
for that one. The identity-policy path used to report `false` (and so never
matched an Allow that required TLS); it now agrees. Not decided by recording:
it is a choice, and `docs/SUPPORT.md` says so.

### B11. Paging, once

No list operation pages in SQS, Parameter Store, SNS, EventBridge, Kinesis,
CloudWatch, IAM, CloudFormation, DynamoDB's `ListTables`, or either API
Gateway plane. Most ignore the limit and return everything with no token.

**Done when.** One helper implements limit and opaque token over a sorted
listing, and a shared test drives any list operation through it with a limit
of one. Each service adopts it in its own stage.

**Landed.** `internal/awspage`: `Slice(items, key, token, limit)` returns the
page after the token and the next token, `Limit` resolves requested, default
and maximum, and a token this package did not mint is `ErrBadToken` for the
service to map to its own code. The cursor is the last key, not an offset, so a
deletion or an insertion between two pages moves nothing; Step Functions'
existing offset `page()` was one such and is replaced in its stage. The shared
test is `dozetest.AssertPaginates(t, fetch, want, opts)`: it walks the
operation with limits of one and two and one above the count, and fails unless
every item comes back exactly once, no page exceeds the limit, the last carries
no token, and (optionally) a token it did not issue is refused. It is itself
held to catching the four ways a list fails today (ignores the limit, repeats a
page, never ends, drops the last page).

### B12. `provision` has two callers

`internal/provision` serves CloudFormation and `doze-aws apply`, which adopts
and converges on purpose. Two stack fixes land there early: ordering a queue
after its dead-letter target (SQS-1) and overwriting a value on update (CFN-1,
which also affects secrets).

**Resolved: no mode is needed.** Reading the code found the difference is
already carried, per resource, by the `Force` field on `Parameter` and
`Secret` (an adopting apply leaves a live value alone unless the file says
force) and, for creates, by `refuseTaken` in `cloudformation/adopt.go`.
CFN-1 is not a missing mode; it is that the transpiler's mapper never sets
`Force`, because "Force is a field only a hand-written Stack sets" (the comment
in `internal/provision/destroy_test.go` says so). So the fix is a line in
`internal/cfn/mapper.go`, in the CloudFormation stage. SQS-1 is a pure
improvement for both callers: order a queue after the queue its redrive policy
names. Neither needs `Apply` to know who is calling.

**Done when.** The behaviour both callers rely on is pinned by a test, so the
next change to the file has to pass through it: `TestApplyAdoptsWhatIsThereAndDeletesNothing`
(an existing queue is converged in place, a live value stands without force
and is overwritten with it, a resource the file does not mention is untouched).

## C. The console shell

The "One anatomy" page of the design. Built once, used by every service page.

| Piece | Board | Notes |
|---|---|---|
| Rail of marks, list pane, detail pane | Anatomy | Names are links, buttons are separate. |
| Header: name, one primary action, facts as a sentence | Anatomy | |
| Neighbours strip | Anatomy | From the wiring graph only. Its nodes are `s3`, `sqs`, `sns`, `eb` (a rule), `lambda`, `ddb` and `kinesis`; its edges are `notify`, `sub`, `target`, `esm` and `redrive` (`internal/console/client_flow.go`). |
| Tabs, composer at the foot, the wire underneath | Anatomy | The wire shows the app's traffic only. |
| Refusal, confirm, empty, slow, toast | States | Uses A2, A3, A12, A13. |
| Field with teaching lines; live strip; neighbour check | Teaching | Uses B4. |
| Create page frame | Create | |
| Dark theme | Dark | |
| The notice below 1024 px | Narrow | Phones are declared unsupported. |
| Palette: jump, resolve an ARN, and create | Palette | Create entries are `new`. |

**Done when.** SQS's pages (stage 1) are built entirely from these pieces with
no service-specific chrome, and the accessibility sweep is clean on them.

## D. Cross-cutting pages, scheduled after the services

| Piece | Board | Built from |
|---|---|---|
| The map, and the map at scale | Map, Map-Large | The wiring graph and its connected components. A Flows page existed and was deleted; this is `new`. The two boards name five node kinds; the graph has seven (tables and streams are nodes), so the boards are corrected when this is built. |
| "Differences from AWS" in the fidelity dialog | Fidelity | The section already in `SUPPORT.md`. |
| The "cannot reach doze-aws" banner | Bad-Down | The console's own poll failing. Browser-side. |
| Connect | Connect | Exists; restyled. |

## Acceptance criteria for stage 0

- [ ] A1 to A18 each meet their "done when", with the test named.
- [ ] B1 to B12 each meet their "done when".
- [ ] The shell pieces in C exist and SQS's list and one detail tab render in them.
- [ ] `docs/SUPPORT.md` no longer says the IAM default is `off` (it is `soft`;
      the Kinesis, KMS, S3 and Secrets Manager sections say `off`),
      and the other stale statements in [findings-overview.md](findings-overview.md)
      section B that are not owned by a service are corrected.
- [ ] `check:full`, `lightness`, `api`, `coverage`, `test:e2e` (route gate on),
      `test:conformance` all green. Idle heap and binary size unchanged, or the
      change is explained in the commit.
- [ ] The existing console still works for the sixteen services not yet
      rebuilt: the browser suite passes without edits to their specs beyond
      what A1 to A13 change on purpose.
- [ ] `CHANGELOG.md` lists each user-visible change.

## Files

| Area | Files |
|---|---|
| Refusals | `internal/console/traffic.go`, `client.go`, `handlers_surfaces.go`, `templates/panes.html`, `static/app.css`, `static/shell.js` |
| Editors | `templates/layout.html` (key/value, attributes), `handlers_tags.go`, `templates/panes.html` (tags) |
| Identity | `internal/console/handlers_eb.go`, `client_eb.go`, `client_lambda.go`, `lambda/extras.go`, `awsident/` |
| Copy and ledger | `internal/console/copy.go`, `info.go`, `docs/SUPPORT.md` |
| Settings tables | `cmd/dzaudit/`, each service's `validate.go`, `docs/ledger.go`, a new accessor under `internal/` |
| Shared service code | `internal/modelcheck/`, `internal/iamguard/`, `internal/iampolicy/`, `internal/provision/`, `internal/dozetest/`, a paging helper and a policy validator under `internal/` |
| View models and links | every `handlers*.go` that re-renders a fragment, `templates/workspace.html`, a link helper in `console.go` |
| Shell | `templates/layout.html`, `panes.html`, `workspace.html`, `static/app.css`, `static/shell.js` |
| Tests | `internal/console/*_test.go`, `e2e/tests/shell.spec.ts`, `layout.spec.ts`, `tags.spec.ts`, `e2e/audit/crawl.ts`, `.github/workflows/` |

## Prior art

- **LocalStack** keeps one error-serialising layer per protocol
  (`aws/protocol/serializer.py`) rather than per service; A1 is the same idea
  in the other direction, one parser for every shape.
- **AWS's own console** (Cloudscape) is where the form-field anatomy comes from:
  label, description, control, constraint text, error text, in that order. The
  teaching lines are that pattern with the constraint text generated instead of typed.
- **moto** documents per-operation support in generated tables. The ledger here
  is stricter (three tiers, reconciled by a test), and B4 reuses it instead of
  inventing a second source.
