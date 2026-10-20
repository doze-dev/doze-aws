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
| B6 the model sweep | the sweep is complete; the burn-down is each service's stage (2026-10-20) | 0223f75, fc76f02 | `TestEveryModelOperationIsAnsweredTruthfully` sends every operation of 18 models (1,372) over the real wire and classifies the answer from stamps, never from a code's spelling: **649 handled, 0 notbuilt, 612 legacystub, 111 unknown, 0 fault, 0 noroute** (2026-10-20, unchanged by P6). The baseline in `testdata/modelsweep.json` is frozen **both ways** (a worse answer is a regression; a better one fails until it is recorded), a 5xx or an unaddressable operation is never accepted, and a service dropped from the sweep must take its entries. By service, what each stage moves (legacystub / unknown of the operations): API Gateway REST 51 / 8 of 124, HTTP 64 / 2 of 103, CloudFormation 67 / 0 of 90, CloudWatch 36 / 0 of 55, DynamoDB 29 / 0 of 58, EventBridge 17 / 0 of 57, IAM 85 / 4 of 180, Kinesis 2 / 6 of 45, KMS 17 / 1 of 54, Lambda 0 / 36 of 88, Logs 93 / 0 of 118, S3 37 / 3 of 112, Secrets Manager 3 / 0 of 23, SNS 21 / 0 of 42, Parameter Store 90 / 49 of 152, STS 0 / 2 of 11; SQS and Step Functions have none. **No service has moved a stub to `awshttp.NotBuilt`**, which is the one thing that moves `notbuilt` off zero: it is each service's stage, because the reason text and declined-or-deferred are the service's (`docs/not-built.md`). `Unreached` is still in five services' `coverage_model_test.go` (Kinesis, Parameter Store, KMS, IAM/STS); each empties in its stage. **Still unverified:** that none of API Gateway's 72 `NotFoundException` answers counted as handled is an unbuilt operation behind a parent lookup (the empty request cannot tell them apart); API Gateway's stage drives each with a real parent. |
| B7 blob lengths in bytes | landed | bed7d64 | KMS, Kinesis, Secrets Manager. SSM and Lambda have a blob member only on operations the service refuses. |
| B2 suites under a second identity | landed 2026-10-12 | 84239b9 | The e2e suite boots under `E2E_ACCOUNT_ID`/`E2E_REGION`. Of 238 tests, 234 passed on the first run; the 4 failures were two test literals (fixed) and two **product bugs**: EventBridge archives made from the console (hard-coded bus ARN) and the S3 share link (signs for `us-east-1`). Each is fixed in its stage, and then the second pass joins CI. The Go side already has `TestEveryServiceMintsTheConfiguredIdentity`. |
| A1 a refusal's code, from every wire shape | landed | ef6fb04 | S3's bare `<Error>` root and the code-in-a-header shape of Lambda and API Gateway. `TestParseRefusalReadsEveryShape`, and `TestTheConsoleCanReadEveryServicesRefusal` drives a real refusal from each of 18 services. Red for the header shape; the S3 shape is pinned by the table. |
| B3 mutation per package | landed | ae30f74, c5d7e33 | `task mutate:record` / `mutate:check`; baselines in `testdata/mutation.json`. |
| A8 identity written into code | landed 2026-10-12 | ae30f74, 84239b9, 60e8865 | The check and a frozen list of 12 sites are in; each site is fixed in its service's stage. |
| B11 paging, once | landed | 65753f1 | `internal/awspage` and `dozetest.AssertPaginates`. Services adopt in their stages. |
| B10 the guard passes every condition key | landed | a27f866, c5d7e33 | `iamguard.RequestContext`, one function for both paths; D7 decides `aws:SecureTransport`. Enforce tests for the services that evaluate a resource policy re-run green. |
| B9 one stored-policy validator | the validator landed; SQS wires it; five services wire it in their stages (2026-10-20: held by a ratchet) | 909f9ed | `iampolicy.ValidateResourcePolicy` and its table test are in. S3 and IAM already validate their own; SQS calls it (`SQS-8`, closed). **2026-10-20:** `resource_policy_wiring_test.go` holds, both ways, which services call it: SQS must, S3 and IAM are marked their own, and **SNS (SNS-16), Kinesis (KIN-3), KMS (KMS-35), Secrets Manager (SM-5) and API Gateway (AGW-7)** are pending, each in its own stage with its own code and wording. A service that wires it fails the test until it is taken off the list, so the list only shrinks. **Why it is not done now:** the code and the message are each service's (`InvalidAttributeValue` for SQS, `MalformedPolicyDocumentException` for KMS and Secrets Manager, `BadRequestException` for API Gateway, Kinesis's and SNS's own), and the validator returns a typed `Problem` so each stage decides from its own model's text which shared problems it refuses (KMS's model says a key-policy statement with no `Action` or `Resource` is accepted and inert, so refusing it would be stricter than AWS: KMS-35). Test that proves each stage: the stored-document table of the SQS worked example (`sqs/policy_test.go`) over that service's wire(s), with the code asserted. |
| B12 `provision` has two callers | landed | 260e30f | Resolved without a mode: `Force` already carries it. Pinned by `TestApplyAdoptsWhatIsThereAndDeletesNothing`. CFN-1 is a one-line mapper change in the CloudFormation stage. |
| A2 the "why" line follows the state | landed for what the foundation can state; the rule's source is each service's stage (2026-10-20) | 4ced5c3, ff29ca2, 788f865, 8a73322 | A refusal is classified from facts the server stamped (`X-Doze-Not-Built`, `X-Doze-Denied`, `X-Doze-Local-Limit`, `X-Amz-Function-Error`), never from a status or a code's spelling. Six states, each with its own label and sentence: Refused, Refused here, Denied, Not built, Failed, It threw. `TestCallStateClassifiesFromFacts`. **Found on the way:** SQS, SNS and S3 rebuilt the guard's error and dropped the stamp, so a policy denial would have read as a refusal; `TestEveryServicesDenialIsStampedAsOne` was red for those three and is green. **2026-10-20, the page itself:** `internal/console/why_line_test.go` drives each kind through the product's own stamping (`awshttp.StampHeaders`, `awshttp.NotBuilt`, `APIError.Stub`) into a console action's inline failure and into the wire inspector, and holds the whole text on the page: refused, a 403 no policy decided (still Refused), denied, a local limit, not built declined and deferred, a legacy stub, failed, threw, the console's own 503 for a service the stack does not run, and a failure with no code (no why line). Red: a function error said "your function" (it now names the function) and the 503 said "AWS would accept this ... a limit of its own" (it now says the stack was started without that service). **Not the foundation's, and where each lands:** (1) "Refused" does not name its source (the model or a documented rule) because no service stamps which rule refused; each service's stage lists its hand-derived and doze-only rules and sets `Local: true` on the doze-only ones (the stamp exists; only the console's own 503 sets it). Test per service: one known local limit answers with `X-Doze-Local-Limit` and the page says "Refused here". (2) "Refused here" and "Not built" do not link the ledger row or `not-built.md`'s reason: the console has no per-operation ledger anchor; that is the fidelity dialog (part D). (3) Stubs move from `X-Doze-Legacy-Stub` to `awshttp.NotBuilt` in each stage (B6). |
| A3 Refused purple, Denied amber | landed (the chip, 2026-10-12) | 4ced5c3 | `--refused` replaces `--denied`; Not built is dashed and neutral, Failed stays red. Wire rows, inspector and `fail_inline` read one copy table (`stateCopyFor`). |
| A4 the key/value editor keeps types | landed | 2cd1ead, 788f865 | A row holds its JSON type and the token it was written as, so `5` stays `5`, `1.0` stays `1.0` and a 20-digit integer is not rounded; key order is kept; a repeated key makes the tab unavailable rather than dropping a value. A per-row type menu is how a type changes on purpose, and a number row holding words is saved as text and says so. Two e2e tests in `secretsmanager.spec.ts`, red without the change. |
| A5 saving tags is one write per direction | landed | c1a3313, 33c5b69, 175ad34 | `SetResourceTags` / `RemoveResourceTags` replace the per-key calls: one Untag, then one Tag (Kinesis chunked at its ten per call). A refused save redraws the editor from the resource and says what was applied. Three tests in `tags_batch_test.go` over six services. Found on the way: SQS accepts a 129-character tag key (SQS-9, added to its stage). |
| A6 a header button's refusal lands under the header | landed | 5d5c1d0, 788f865 | A new rung in `errSlot` places a refusal from `.det-h` directly under it, in full. `s3.spec.ts` deletes a non-empty bucket; red without the rung. The full suite runs at the end of stage 0 for any test that leaned on the old toast. |
| A7 copy nobody can reach | landed | 83e2b9f | Three named cases (unselected, first, home) and each service is assigned one; `copy.go` holds only what its case can show. `TestEveryLineOfEmptyCopyIsReachable` drives all 16 services empty and then with one resource. Removed 14 `Unsel` lines that could never show, EventBridge's and IAM's lines entirely (the default bus and the access log always exist), CloudWatch's `None`. API Gateway and CloudFormation have create forms and the catalogue now says so. |
| A9 the fidelity dialog finds every ledger | landed | 4abb7aa | `cw` maps to the CloudWatch ledger and the API Gateway panel carries REST and HTTP sections. `TestEveryRailServiceHasALedger` opens the panel for every rail entry. |
| A10 a cascade hop shows the status it answered | landed | 7540f47, 8ee3182, af44a48, 788f865 | `trace.Event` carries `Status` and `Denied`, read from the error through `HTTPStatuser`/`Denier`; `peercall` errors carry both. A denial on an alarm's Publish is a 403 and reads as Denied. `TestAFailedStepRecordsTheStatusItWasAnswered`, `TestCascadeStatusIsWhatTheTargetAnswered`. |
| A11 a form's preview is the call the form makes | landed | 1ba6113, 788f865 | A preview can carry further commands (shown when their field is set), name a menu option's attribute (`$metric@data-ns`) and read a control the form includes with `hx-include`. `TestEveryCLIPreviewMatchesItsForm` holds every input of every previewed form to the preview or to a named omission. Fixed: DynamoDB TTL, Logs retention, HTTP API CORS, S3 versioning, CloudFormation change set, CloudWatch alarm (was a bare name), STS (five modes had no preview), Step Functions alias second version, test-state (its `--definition` was always empty), SQS dedup id. Still omitted, named: DynamoDB index rows. |
| A12 toasts | landed | 07c8517, 33c5b69, 788f865 | Lifetime and hover already followed the design (7 s, 10 s for an error, held on hover and focus; the 3.2 s in the finding was stale). Two real defects found and fixed: a 5xx that is not ours was swapped into the action's target and blanked the pane (htmx 4 swaps every status), and a failure that is not a refusal was only a toast, cut at 140 characters. It is drawn where the action was, whole. Toasts that claim a no-op are per-service and are in their stages. |
| A13 the confirm component | landed | 07c8517, 09ce198, 788f865 | The dialog's title names the thing, the sentence after the question is what follows, and the button is the verb (destructive verbs in red; `data-confirm-verb` where the sentence does not begin with the action). No call site was reshaped. Each service's stage still reads its own confirms against the code. |
| A14 links built from names are escaped | landed (for all templates, 2026-10-12) | 0d87bb0 | 16 `printf` builders in Logs, Secrets Manager and Parameter Store now go through `urlquery`. `TestALinkFromANameSurvivesItsCharacters` creates a secret `app/db+prod=1` and a log group `/app/a#b` and parses every link to them; red before. |
| A15 a fragment shows what the page shows | the shared machinery landed; two more defects fixed; one region owed (2026-10-20) | f797c16, 788f865, 47de238 | Secrets Manager and Parameter Store (where it was found) build their detail from one view function used by the page and every fragment (`secretDetailView`, `paramDetailView`); `fragments_test.go` compares each fragment with the same region of a full reload. **2026-10-20, the walk:** `fragment_walk_test.go` takes every template named in a `c.partial` call (82, read from the source by `TestEveryPartialIsClassified`, so a new one fails until it is classified) and either walks it or says why it has no region of a page (an answer to a call, loaded into a drawer on demand, rows of a walked table, loaded into its slot from the route that also answers a refresh). 30 routes over 16 services are driven as htmx requests (POST actions and the live-region GETs) and each fragment, element by element and with its out-of-band counters, is compared with the element of the same id in a full load. Red: it found two services drawing less than their page after an action, now on one view function each: **SQS Configuration** (after Save the ARN and URL chips were empty; `sqsConfigView`) and the **KMS key** (after any action the re-encrypt destination menu was empty; `kmsKeyView`). The other 28 routes were already equal. **Owed to a stage:** `apigw_method` (API Gateway: a method's panel is redrawn by five edits and the page opens it by POST; the case goes with that stage's console work). **For every service's stage:** the walk compares one state per route; a stage that adds a state a board draws (empty, a policy set, a failure) adds its case to `fragmentRegions` and keeps the walk green. The list of services with partials, by what the walk found, is in each service document's "Foundation hand-offs". |
| A16 a failed page load is a page | landed, with the list pane (2026-10-20) | c14b3fc, 788f865, 07132a8 | A navigation (a GET asking for HTML, with no `HX-Request`) that fails is drawn in the shell with the failure and the way back; an htmx request keeps its placed fragment. `TestAMissingResourceIsAPageInTheShell` loads a missing resource in all 15 services; it found that `/eb/no-such-bus` drew a bus page (EB-31: ListRules answers an empty list for a bus that does not exist). **2026-10-20: the list pane.** No board draws a missing-resource page (`Logs-19-Bad-Gone` and `Ssm-09-Edit-Gone` are other states, `Bad-Down` is the console's own poll failing), so it is a proper page in the shell: the service's list pane beside the failure, from `listPaneData` (the listing the service's own pages use; left out, with the failure still drawn, when the listing is what failed, because a pane built from a failed listing says "none"). `TestAMissingResourceKeepsItsListPane` (red for all 15 services; green with a seeded stack for 15, including IAM) asserts the pane is there, lists what the stack has and marks nothing open. CloudWatch alarms and Logs groups had answered the generic "that address does not exist" page for a missing resource (their list had loaded and held no entry); they fail like every other service now. Rendered and read for a missing queue, bucket, alarm and log group, light and dark, at 1440 (the pane) and 1024 (the name switcher). |
| A17 times have one rule | landed (with a guard, 2026-10-12) | bc716bb | Every displayed time is local and goes through `timefmt.go`; `TestDisplayedTimesGoThroughOneFormatter` fails on a display layout written anywhere else or a UTC display. Fixed: Kinesis stream, consumer and record times and API Gateway "created" were UTC beside local ones (and `ago` read them as local, so they were wrong by the offset). A time says its zone, its offset and the same instant in UTC on hover (`zoned`, read back from the string the formatter produced, so no view model carries the instant twice): the 13 relative-time cells and the 21 stamps that are the whole of their element. UTC is there on hover and is not a toggle; a stamp inside a sentence has no hover. |
| A18 the strip says only what the graph knows | landed (siblings too, 2026-10-12) | 0493610, 788f865 | The graph holds configuration and cannot see an SDK client, so for every node kind the strip is drawn for a producer exists that it does not model: the upstream side now never says "nothing feeds this" and collapses when empty. The queue's downstream hint says "no Lambda trigger reads this queue". State machines and alarms are not nodes and have no strip. `TestTheStripNeverClaimsNothingFeedsAResource`, red for all five. |
| B8 what the generator drops | the generator is honest; regenerating each service's cases is its stage (2026-10-20) | 921d0e0, 33c5b69, e651c44, c5d7e33, bd82ef7 | **2026-10-20:** nothing is dropped in silence now. `dzaudit cases` names every constraint it builds no case (or only part of one) for, sorted, as `Op Path [rule]: why` on stderr: a pattern nothing on hand breaks, an unparseable pattern, a list whose element is itself constrained or a structure, a maximum above the cap of 200 (the minimum's case stood without a word that the maximum's did not), a map key too long to write out, an empty list on a wire that cannot spell one, and the members the walk did not look into because they nest deeper than `maxDepth` (`drops.go`, `drops_test.go`; a model that drops nothing prints nothing). **Regeneration against the current models** (committed, regenerated, cases only in the committed file, cases only in the new one): API Gateway REST 126, 288, 16, 178; HTTP 94, 379, 94, 379; CloudFormation 182, 800, 12, 630; CloudWatch 522, 612, 20, 110; DynamoDB 333, 707, 45, 419; EventBridge 448, 608, 12, 172; IAM 702, 1268, 7, 573; Kinesis 399, 566, 36, 203; KMS 263, 487, 23, 247; Lambda 621, 1024, 49, 452; Logs 237, 1118, 5, 886; S3 296, 498, 55, 257; Secrets Manager 152, 179, 2, 29; SNS 53, 88, 0, 35; **SQS 51, 51, 0, 0 (byte-identical: the only no-op, nothing to commit)**; Parameter Store 100, 1752, 7, 1659; Step Functions 248, 248, 5, 5 (the same count; five values changed, so not a no-op); STS 111, 141, 0, 30. Every other service's regeneration changes what its parity test sees (the new cases find gaps; the dropped ones were stale), so it is regenerated in that service's stage with the failures it finds, as the line below says. The weekly drift job prints these counts. **Earlier, 2026-10-12:** `dzaudit` reads a list's length, a map's size and every map KEY's rule (the model's `key` member was never parsed). A case is built only around an element that is valid on its own; the rest are in `list` and counted on stderr. A case is built around a value that satisfies its own shape (a tag value at its minimum length that matches its pattern), which is what makes a tag-key rule reachable at all; regenerated and run through the Logs harness, the new cases apply and find accepted over-long and off-pattern tag keys and over-long lists. An empty list is emitted only for a JSON protocol, where it can be sent. Against the models this finds about 650 constraints that were in no audit (SSM 229, Logs 101, DynamoDB 71, Lambda 63, CloudWatch 45, IAM 32, EventBridge 31, CloudFormation 29, Kinesis 18, KMS 15, Secrets Manager 9, STS 6, SQS 4, Step Functions 2). Each service regenerates its `cases_*.json` in its stage. The drift job now prints committed against regenerated case counts, which it never failed on and now says. |
| B5 a restart test helper | landed | 8cbed7c | `persistsAcrossRestart` writes through the API, closes the stack, opens a new one over the same data directory and reads back; the two existing durability tests are the first callers and the upgrade fixture is untouched and green. |
| B4 settings the console can read | the record and the lazy registry landed; each service adds its own (2026-10-20: unchanged, and why) | 64966f6 | `internal/settings`: a record per setting (name, unit, range or enum, default, tier per operation, what enforces it, source), registered per service and built on first lookup (`TestNothingIsBuiltUntilAsked`). SQS is the worked example and reads the numbers from `attrRange` and the `def*` constants rather than restating them; `TestFormConstraintsAreTheServicesOwnRange` holds every SQS number input's min and max to its record. Each service adds its records and its form rows in its stage; the check that every shown setting has one is that table. **2026-10-20, the ratchet:** `number_inputs_test.go` freezes, per template, how many hand-written `<input type="number">` with a min or a max remain (22 in 11 templates: API Gateway 2, DynamoDB 2, EventBridge 3, IAM 2, Kinesis 4, KMS 1, Lambda 5, S3 1, Secrets Manager 1, SNS 1), in both directions: a new one written by hand fails (make it a `setting_field` with a record) and a converted one fails until the count is lowered, so a stage's B4 progress is a number going down. Each service's stage also writes its record table (name, unit, range or enum, default, tier per operation, what enforces it, source) from its own `validate.go` and ledger, and registers it with `settings.Register`; only SQS has. The tables that are not flat attributes (API Gateway's patch paths, CloudFormation's template parameters, Logs' limits that live in code, DynamoDB's tier that differs by operation) are named in those services' documents. |
| B1 a claims table per service | landed | 17af7bb | `conformance/claims/README.md` is the format; `sqs.md` is the worked example (21 claims: 12 on existing scenarios, 9 to write). `TestEveryClaimNamesARealScenarioOrSaysItIsToBeWritten` holds every row; `task claims`, and the end of `task test:conformance`, print recorded / model / doc / memory / to-write per service. Today nothing is recorded: `conformance/snapshots` is empty until an account exists. |
| C the shell kit | landed | 5c2bc52, 788f865 | `kit.html`: `res_header` (name, badges, the destructive action, copyable ids, fact strip) and `setting_field` (bounds, unit and default from `internal/settings`, plus one taught sentence). The narrow-window notice already existed at 768 px; it is the one notice, moved to 1024 px. SQS's header and its five number fields render through them; `TestFormConstraintsAreTheServicesOwnRange` now reads the rendered pages. The rest of the anatomy (rail, list pane, tabs, dock, neighbours strip, the five states, dark theme, palette create entries from the catalogue) already existed. `res_wire` is the wire underneath a resource: the app's calls to that one resource, newest first and live, loaded by the slot so a page's handler does not have to know about it (`TestTheWireUnderAResourceIsThatResourcesCalls`, and a browser test). SQS has it on every tab. The rule that SQS's pages are built entirely from the kit is stage 1's done-when. |
| B3 the first baselines | landed; the remaining `internal/` packages recorded 2026-10-20 | 1e93133 | `testdata/mutation.json` holds baselines for 31 packages: all of `internal/` that carries logic, and SQS, SNS, KMS, Kinesis, Secrets Manager, SSM, Logs, CloudWatch, EventBridge, STS, Step Functions (3.3 hours), IAM, DynamoDB and S3. Lambda, API Gateway and CloudFormation are recorded by `task mutate:record -- <pkg>` in their own stages, which is where the protocol's proof gates run it. A package of that size is a background job of an hour or more: Step Functions was 3.3 hours on this machine. **2026-10-20: sixteen more**, each run by `task mutate:record` in a copy of the tree (so a running baseline never meets a half-edited one), and **then made to earn it**: 69 survivors in the first run, and every one a reasonable test could kill got one (the commits "tests: kill the surviving mutants..."). Before and after, killed / lived / efficacy: `awschunk` 21/5/80.8% to 24/1/96%, `awshost` 22/7/75.9% to 28/1/96.6%, `config` 24/11/68.6% to 35/0/100%, `emf` 18/3/85.7% to 20/1/95.2%, `gateway` 15/3/83.3% to 18/0/100%, `httpevent` 24/2/92.3% to 25/1/96.2%, `lazybolt` 8/1 to 9/0, `logship` 9/5/64.3% to 15/0/100%, `metricship` 2/8/20% to 11/0/100%, `rpcv2cbor` 103/21/83.1% to 130/1/99.2%, `schemaver` 6/1 to 7/0, `lightness` 8/11/42.1% to 22/1/95.7%, `dozetest` 28/4/87.5% to 31/0/100%, `sigparse` 18/1, `settings` 2/1, `auditkit` 6/1. **The ten that still live are equivalent mutants or nearly, with the reason:** `sigparse.go:126` (`secs < 0` to `<= 0`: differs only when the expiry is 0 and now is the signing instant), `settings.go:125` (a sort's `<` to `<=` over unique names), `httpevent.go:64` (an empty binary body encodes to the empty string either way), `awschunk.go:81` (`len(p) > remain` at equality slices to the same slice), `awshost.go:130` (a `]` at index 0 gives the same result by either branch), `emf.go:73` (a two-byte line cannot hold the six-byte key), `rpcv2cbor encode.go:171` (the same sort), `lightness.go:327` (`atLeast` at equality returns the same number), `auditkit repeat.go:29` (`Times <= 0` against `< 0`: a zero repeat is empty either way). Recorded in `testdata/mutation.json` (50 packages in all). **Not recorded, and why:** `internal/console` (26,885 lines; its suite is about 150 s a run, so a baseline is days, and the browser suite is its test), `internal/migrate` (no tests of its own; the root package tests it), and Lambda, API Gateway and CloudFormation (their own stages, each an hour or more). |
| B13 a Query request is validated whole | landed 2026-10-12 | c1ada87 | Found in SQS (SQS-26). `FromQuery` keeps one element of each list; SNS, IAM, STS and CloudFormation call it directly. Before SNS's stage. |
| D-a the wire page, D-b the first-run wire, D-e the "cannot reach" banner | landed 2026-10-20 | branch worktree-agent-a239bd30f7b78bf69 | The wire to Main, Wire-Inspect, Wire-Refused, Wire-Empty and Bad-Down, from the recorder and the product only; the deck is gone. Two data paths closed on the way: an S3 notification says what emitted it, and an SQS mapping's invoke of an SDK's message is on the wire (it was on it nowhere), since 2026-10-20 as a child of the send (internal/msgcause), and a function error under a mapping reads "it threw" (trace.Event.FnError). Left, and why: [03-shell-fidelity.md](03-shell-fidelity.md) section 4, items 13, 15 and 16 (S3 to a FIFO queue, two states for one chip, the inspector's widths); item 14, the nested invoke, is closed. |
| B14 the fidelity pairs as a task | landed (the machinery 2026-10-16; the board path decided and enforced 2026-10-20) | 1d3798e, c74627e | `go tool task console:fidelity -- <service|shell|Board-Name> [--dark] [--w900] [--id2]` builds the binary, boots it, seeds the harbour fixture, renders every board and drives the real page to its state, writes `board-*.png`, `page-*.png`, `report.html` and `results.json`, and checks that every line of the board's text is on the page (the allow-lists in `e2e/fidelity/allow/` each carry a reason kind and are never for a missing sentence). A board with no driver, an orphaned driver, a board that does not render and a page that lacks board text are each a failing row; the list of boards is the directory listing, never a table. **Decided 2026-10-16 and wired 2026-10-20: the boards live in `plan/design/boards/` in this repository and nowhere else.** The runner has no other path and no environment override; a checkout where the directory is missing, holds no `*.dc.html`, or lacks `con.css`, and a service the design draws no board for, each exit 2 with `FAIL: no design boards: ...` instead of comparing nothing and passing, and the unit tests that read the real boards fail (`the checkout has its boards`) where they used to skip. Verified by running `bun fidelity/run.ts sqs` with the directory missing and with it empty. Documented in `e2e/README.md`, `plan/01-protocol.md` and `plan/design/README.md`. **Result today:** `console:fidelity -- sqs` 19 of 19 match; `-- shell` 0 of 13 (4 more are skips with their reason: the map twice, the "cannot reach" banner, the Grounding document board), because those boards are the wire page and the foundation-D pieces ([03-shell-fidelity.md](03-shell-fidelity.md)); every other service's table is empty on purpose, which is its stage's to-do list (a board with no driver fails). `FIDELITY_PORT_MIN` and `FIDELITY_PORT_MAX` choose the port range when several worktrees run it at once. |
| D-c the fidelity dialog | landed 2026-10-20 | 6a5da62, ccb6c33, 095628d | Board Fidelity on every service: 940 wide, "<service> · what is real here", the tiers counted, the table (the emulator's own calls last, with a dash), and "Differences from AWS" as cards, read from `docs/SUPPORT.md` when the dialog opens (`docs.Differences`, `docs.Extensions`; nothing at idle). ⓘ beside the name in a resource's header (kit `res_header`; SQS) and in the list header when nothing is open or a service has not adopted the kit (D12). `TestEveryServicesPanelCarriesItsDifferencesFromTheLedger` and `e2e/tests/info-dialog.spec.ts` open all 16. Fidelity: 36 of 36 lines (5 allowed, each with its reason). **Open:** Kms-19 draws the same dialog a second way (1360 wide, "Fidelity", "What is real" as a right column of tinted summaries, no source sentences for four of them); the page builds the Fidelity board for KMS too, and Kms-19 passes with 9 allowed lines naming that choice. |
| D-d Connect | landed 2026-10-20 | ccb6c33, 095628d | Board Connect: three columns (two balanced under 1000 px), Copy copies what the block shows, the region/endpoint/caller are the instance's (a second-identity test and spec), no `then` block and no account line (the board draws neither). Connect: 50 of 50 lines (1 allowed: the board's Terraform ellipsis). The board draws no per-language SDK snippets, `doze-aws env` or `doctor`; none was added. |

## Audit of stage 0 (2026-10-12)

Stage 0 was called done item by item, and stage 1 was signed off on top of it.
On 2026-10-12 two independent audits tested every claim in this file: each
named test run and read for what it proves, and each behaviour reproduced
against a built binary, in the browser where it is visual, and under a second
identity. What they found, and where each stands now:

| Item | The file said | The audit found | Now |
|---|---|---|---|
| A2 | landed | The "done when" is not met: no service sets the local-limit stamp, `awshttp.NotBuilt` has no caller, and "not built" was recognised by a code's spelling, so CloudFormation's stubbed `ListStackSets` read "Refused… the input is what to change". | Every legacy stub table stamps `X-Doze-Legacy-Stub` (thirteen services), and the console and the sweep read the stamp, not the code. The rest (a service setting `Local`; stubs moving to `NotBuilt`) is each service's stage. **2026-10-20:** the text on the page is held per kind with the product's own stamps (`why_line_test.go`); a function error names the function and the console's own 503 no longer reads as a limit of doze-aws. |
| A3 | landed | The wire inspector's status chip was red for any status of 400 or more. | The chip and each wire row carry the state (2c6cc3e). |
| A8 | partly | KMS still locked a new key out at birth under another account with enforce on, the EventBridge console's archives captured nothing, the S3 share link failed, and the check skipped `module/`. | All 17 sites fixed, the frozen list is empty, `module/` is checked (ef18e70). **A second audit (2026-10-16) found one the list could not see:** Lambda's `CreateFunction` response named the default account and Region (the response was built from a record never stamped); fixed, with a test over every response that shows a function (824788b). |
| A11 | landed | DynamoDB's preview omitted the index rows, under a tooltip promising equality. | The preview builds them (9a0713c, f4eea12). |
| A12, A6 | landed | A refusal could land inside a closed disclosure, off-screen, with no toast (SQS create). | `errSlot` skips a closed `<details>` (385c8c1). |
| A13 | landed | Ten dialogs never trapped focus: the effect that switches the trap ran once. | All fixed, with a guard test and a browser test (f4eea12). |
| A14 | landed | The test covered two services and only hrefs; the tag editor broke on `+` and `#`, the S3 inspector on `+ # &`, alarms with `/` could not be opened. | About 505 template sites and 137 Go sites escaped, and the guard reads every template with no exemptions (955cbec, f4eea12). One left: a log group with `#` cannot have its tags edited, because the service refuses the ARN as the model says. |
| A15 | partly | Two of sixteen services are on one view model. | **2026-10-20:** every fragment is walked against its page (30 routes, 16 services), which found and fixed two more (SQS Configuration, KMS key); one region is owed (API Gateway's `apigw_method`). |
| A16 | landed | Holds, and no missing-resource page draws the list pane the "done when" asks for. | **2026-10-20:** the list pane is drawn (`TestAMissingResourceKeepsItsListPane`); CloudWatch alarms and Logs groups now fail like the others. |
| A17 | landed | Twenty relative times had no zone hover. | All fixed, and a guard fails on a new one (979d495). **Four more found 2026-10-16** (IAM last-used and minted expiry, S3 retention-until and multipart start), which the guard's field-name list did not know; added to it and fixed (824788b). The guard is name-based: a new time field needs its name added. |
| A18 | landed | Two sibling sentences ("drains nowhere yet", "Nothing sends to them"). | Reworded, and the test scans every template (3c13f12). |
| B2 | partly | The second-identity browser pass was not in CI. | It passes (288 of 288 under 111122223333 in eu-west-1) and is in `e2e.yml` (698eb67). |
| B3 | landed | "All of `internal/` that carries logic" was false: `peercall`, `rpcv2cbor`, `gateway`, `config`, `sigparse` and others have no baseline. | `peercall` and `modelcheck` recorded 2026-10-16; **2026-10-20: the rest** (`awschunk`, `awshost`, `config`, `emf`, `gateway`, `httpevent`, `lazybolt`, `logship`, `metricship`, `rpcv2cbor`, `schemaver`, `settings`, `sigparse`, `auditkit`, `dozetest`, `lightness`), with the survivors killed or reasoned. Exceptions named in the Progress row. |
| B6 | partly, but "Met" in the box below | No service calls `NotBuilt`; the sweep counted CloudFormation's 67 and Secrets Manager's 3 stubs as handled. | Counted from the stamp: 612 legacy stubs, 111 unknown, 0 not built (fc76f02). The box below says partly. **2026-10-20:** unchanged, and the Progress row says whose each is. |
| B7 | landed | Only KMS had a boundary test. | Secrets Manager and Kinesis too; Kinesis had two real bugs (the partition key not counted in a record's size, the 10 MiB request limit not enforced) (560006e). |
| B8 | landed | No service's committed cases are regenerated (SQS was 49 of 51). | SQS regenerated (51); the rest are each service's stage, and so is a list's own maximum (50 tags) on the Query services, which needs it. **2026-10-20:** regenerated against the current models, SQS is byte-identical and every other service changes (counts in the Progress row); the generator now names what it drops. |
| B13 | to build | Reproduced: a valid first list member and a bad second were accepted 2 to 7 times in 8 by SNS, IAM and CloudFormation, by the map's random order. | `FromQuery` keeps every element in index order; 20-run tests per Query service (c1ada87). |
| C | landed | Holds for the kit. The shell is not the boards' shell. | [03-shell-fidelity.md](03-shell-fidelity.md): 19 cross-cutting boards, 1 matches; about 36 days and twelve decisions. **Now (2026-10-20): the shell is built (S1 to S12), `console:fidelity -- sqs` is 19 of 19, and the box in the acceptance list is ticked.** The wire page and the other foundation-D pieces remain (`-- shell`: 0 of 13). |

Also built on the way: `dzaudit docs` (part E's first "to build", 09cca7e).

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

  **Status, 2026-10-20.** The test per service is one walk over all of them
  (`fragment_walk_test.go`: 30 routes, 16 services, every `c.partial` template
  classified, see the Progress row), and it found and fixed SQS's Configuration
  and KMS's key detail. What is left is `apigw_method` (API Gateway's stage) and
  the states a stage's boards add; Secrets Manager and Parameter Store are on
  one view model each, SQS and KMS now too, and the rest are equal to their page
  in the state the walk drives.

### A16. A failed full-page load has no page — P2

- **What.** `c.fail` on a full-page GET returns the bare error fragment with no
  chrome: `/ddb/nope`, or Kinesis's Records tab with a disabled key.
- **Done when.** A failed GET renders inside the shell with the list pane, and
  a test loads a missing resource in every service.

  **Met, 2026-10-20** (`TestAMissingResourceIsAPageInTheShell` and
  `TestAMissingResourceKeepsItsListPane`, 15 services each). No board draws the
  page, so it is a proper page in the shell: the failure, the way back, and the
  service's list pane from `listPaneData`, left out only when the listing is
  what failed.

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

*Added 2026-10-11, from KMS's pre-stage review (KMS-35).* The checks above are
what *most* resource policies share, not what every service's model accepts.
KMS's model says a key-policy statement with no `Action` or no `Resource` is
accepted and inert, so refusing it would be stricter than AWS and would break
a correct caller (the direction of L4). The validator already returns a typed
`Problem` and leaves the decision to the service; the rule is that **each
service's stage decides, from its own model's text, which of the shared
problems it refuses**, and says so in its pre-stage review. The wiring above
is not "refuse every `Problem`".

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

### B13. A Query request is validated whole — P1 *(added 2026-10-11, from SQS)*

`modelcheck.FromQuery` rebuilds the Query protocol's flattened keys
(`Tags.member.1.Key`, `Tags.member.2.Key`) into a tree, and keeps **one
element** of every list: its comment says a constraint applies to every
element, so the first is the rule. That is true of a rule and false of a
request. Element 2 is never looked at, so a second batch entry with an
over-long tag key, a bad name or a repeated id passes validation and reaches
the handler. It is the same blind spot as the unflattened map in the same
function, one level up. SQS found it (SQS-26) and walked its own request whole in
`sqs/codec.go`; the shared function still drops the rest.

Four services call it directly and have the bug until it is fixed once, here:
SNS (`PublishBatch`, tags, `Attributes`), IAM (`Tags.member.N`, policy arns),
STS (`Tags`, `PolicyArns`, `TransitiveTagKeys`) and CloudFormation
(`Parameters.member.N`, `Tags.member.N`, `Capabilities`). CloudWatch is
awsJson1_0 and does not use it.

**Done when.** `FromQuery` keeps every element, in index order (a gap in the
indices is compacted, never merged: a hole would let one key allocate billions
of slots and make `required` refuse members that were never sent), and the constraint walker checks each; a
test per shape (scalar list, list of structures, map as `entry`, nested list
in a list) puts the violation in element 2 and 3 and not in element 1; the
Query services' rejection-parity cases are regenerated to put it there too
(B8's "a case is built around an element that is valid on its own" is
extended to a second element); SQS's own walk is removed if it is now the
same thing. Lands before SNS's stage; IAM, STS and CloudFormation get it for
free.

### B14. The fidelity gate, as machinery — P1 *(added 2026-10-11, from SQS)*

SQS was ticked "all 21 boards" with every test green and six boards differing
in shape, because nothing in the repo could see a board. The protocol now
requires a rendered board beside a rendered page (01-protocol, step 5), and
that was done by hand, on a scratch Playwright spec, for every service's
review. The part that is mechanical is made a tool so a stage does not
reinvent it:

- **Built** (1d3798e), as `e2e/fidelity/` and `go tool task console:fidelity`: per
  service a driver file, one entry per board, drives the real page into the
  board's state (the harbour fixture in `fidelity/harbour.ts`) and writes
  `board-<name>.png` and `page-<name>.png` (1440 by 900; `--dark`, `--w900`
  and `--id2` for the other variants) and `report.html`.
- The board list is the directory listing of `plan/design/boards/`, so a board
  with no driven state is a failing row, not a silent one, and so is a driver
  for a board that is gone. **That directory is the only place the runner reads
  boards from**: no environment variable, no second path (decided 2026-10-16,
  enforced 2026-10-20). A missing or empty directory, a missing `con.css` and a
  service with no board each exit 2 with `FAIL: no design boards: ...`, and the
  unit tests fail where they used to skip. It is documented in
  `e2e/README.md`.
- The comparison is not a pixel diff (the boards use two-letter marks where the
  pages use the AWS icons, and the data differs). It is the structural check:
  every line of the board's visible text must be on the page, modulo
  `fidelity/allow/<board>.txt`, whose every entry names a reason kind and is
  never for a sentence the page lacks. The pictures are still read by a person.
- Icons: the per-service AWS icons in `internal/console/static/aws/*.svg` are
  kept (rail, list-pane header, resource header); the marks are hidden before
  the text is read, so a placeholder against an icon never reads as a
  difference.

**Done when.** `task console:fidelity -- <service>` writes the pairs for every
board of the service and fails on a board with no driven state; SQS is the
worked example and its pairs are the ones the 6a table was written from.

## E. What the SQS stage taught the foundation *(2026-10-11)*

The twelve mistakes are in [02-lessons-from-sqs.md](02-lessons-from-sqs.md);
this is where they touch shared code and shared pieces rather than one service.

| Lesson | What it does to the foundation |
|---|---|
| L1 read the model's documentation | **To build:** a `docs` subcommand on `cmd/dzaudit` that prints a service's documentation as plain text (operation, member, error shapes with their code and status), so the read is one command and not a JSON walk each time. Not a gate: a person reads it. The fifteen pre-stage reviews each wrote this dump by hand. |
| L3 error codes are in the model | **To build:** the rejection-parity harness asserts a code today; it should take the expected code from the operation's `errors` list where the table has one (B8 already reads the member; the same data carries the code and the status each protocol gives it). |
| L5 every wire | B13. And: a service on two protocols has one parity test per protocol, named so a protocol with none is visible in `go test -v`. |
| L6 tokens are per use | Nothing shared. Each service's document names its tokens. |
| L7 asynchronous is observable | The pattern is `sqs/move_task.go`: a started record, a worker that moves state at a stated rate, running state in memory, the finished record persisted, a stop on `Close`. Each service with an asynchronous operation copies the pattern and does not invent one. |
| L8 latency rules are opt-in | One switch per service with a rule: `--<svc>-strict`, `[<svc>] strict`, `StackConfig.<Svc>Strict`, read through `config` the way `SQSStrict` is. The settings record carries it; the fidelity dialog lists what the switch changes. |
| L9 state in memory unless it changes what the app sees | Said per piece of state in each service's document; the idle-reap fact (backends are reaped after `idle_timeout` with zero client connections, so a restart is routine) is the test. |
| L10 the board is the spec | B14, and the "Console" acceptance box below. |
| L11 five teaching lines | The kit's `setting_field` writes line four from the settings record, "enforced" or "stored" included, and takes a live neighbour callout. Already built; every form row uses it. |
| L12 icons | The AWS icons stay. Stated once here so no service's stage redraws the rail or a header with a board's two-letter mark. |

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
| The map, and the map at scale | Map, Map-Large | The wiring graph and its connected components. A Flows page existed and was deleted; this is `new`. The two boards name five node kinds; the graph has seven (tables and streams are nodes), so the boards are corrected when this is built. **Built 2026-10-20** (`internal/console/map.go`): the boards are corrected, both match under `console:fidelity`, and hot is the D6 decision (a call to either end in the last minute from the recorder ring). |
| "Differences from AWS" in the fidelity dialog | Fidelity | The section already in `SUPPORT.md`. |
| The "cannot reach doze-aws" banner | Bad-Down | The console's own poll failing. Browser-side. |
| Connect | Connect | Exists; restyled. |

## Acceptance criteria for stage 0

Status at the end of the first pass (2026-10-10). A box is ticked only when the
criterion is met as written; the rest say what is left and why.

Re-stated on 2026-10-20 (package P6), every box against its "done when".

- [x] B13 (a Query request is validated whole) and B14 (the fidelity pairs as a
      task), added 2026-10-11 from the SQS stage. B13 landed 2026-10-12. B14
      landed: the machinery 2026-10-16, and 2026-10-20 the boards are read from
      `plan/design/boards/` only and a missing, empty or incomplete directory
      fails loudly (see the Progress row).
- [~] A1 to A18 each meet their "done when", with the test named. **Met:** A1,
      A3, A4, A5, A6, A7, A8, A9, A10, A11, A12, A13, A14, A16 (with the list
      pane, 2026-10-20), A17, A18. **Partly:** A2 (the page says what each kind
      is, held per kind; the rule's *source* and the ledger link are each
      service's stage: no service stamps which rule refused, and the stamp
      for a doze-only limit is set by none) and A15 (one walk over every
      fragment, two defects fixed; `apigw_method` is owed to API Gateway's
      stage, and each stage adds the states its boards draw).
- [~] B1 to B12 each meet their "done when". **Met:** B1, B2, B3 (50 packages
      carry a baseline: 34 before and 16 more on 2026-10-20; named
      exceptions: `internal/console`, whose own suite is about 150 s a run, so
      a baseline is days and the browser suite is its test; `internal/migrate`,
      tested only from the root package; and Lambda, API Gateway and
      CloudFormation, recorded in their own stages), B5, B7, B10, B11, B12.
      **Partly, and why it is not the foundation's:** B4 (the record, the lazy
      registry and SQS as the worked example; 22 hand-written bounded number
      inputs remain, frozen by a ratchet that only goes down; each service writes
      its records), B6 (the sweep is complete and honest and frozen both ways;
      no service has moved a stub to `NotBuilt`, which is each stage's, because
      the reason and declined-or-deferred are the service's), B8 (the generator
      names everything it drops; only SQS's cases are current, and the other
      seventeen change what their parity tests see, so each regenerates in its
      stage), B9 (the validator; SQS wires it, S3 and IAM validate their own,
      and five services wire it in their stages, held by a ratchet).
- [x] The shell pieces in C exist and SQS's list and one detail tab render in
      them. **Now true, with evidence (2026-10-20):** S1 to S12 are built
      ([03-shell-fidelity.md](03-shell-fidelity.md)) and `console:fidelity --
      sqs` matches 19 of 19 boards (the list, every detail tab, the states).
      What does not match is the cross-cutting wire page and its neighbours
      (`console:fidelity -- shell`: 0 of 13, with 4 skipped for a reason),
      which are foundation part D, scheduled after the services and not part of
      this box.
- [x] `docs/SUPPORT.md` no longer says the IAM default is `off`. The other stale
      statements in [findings-overview.md](findings-overview.md) section B are
      either fixed (the refusal copy, the colours, the unreachable `copy.go`
      lines, the create-form claim for API Gateway and CloudFormation) or
      belong to a service's stage and are in its doc.
- [~] `check:full`, `lightness`, `api`, `coverage`, `test:e2e` (route gate
      on), `test:conformance`: see the results recorded in the last commit of
      the stage. **On 2026-10-12, after the audit's fixes, all green:**
      `check:full`; the browser suite 288 of 288 with the route gate at 370 of
      371 (the one listed in `route-gaps.txt`), and 288 of 288 again under
      account 111122223333 in eu-west-1; conformance 160 passed, 831 responses
      unverified (no AWS account); coverage 81.2% against 80.5%; `lightness`;
      `api`. Idle heap and binary size are explained in the commit that
      re-recorded the budgets. **On 2026-10-16, on the merged tree after the shell
      rebuild:** `check:full`; browser suite 387 passed (and 386 plus one flake
      under the second identity, since fixed at its cause: the wire inspector
      lost slow calls); conformance 160 passed; coverage 81.3%; `lightness`; `api`;
      `console:fidelity -- sqs` 19 of 19. **On 2026-10-20, package P6 (twice for
      the second identity):** `check:full` green (the first run found a test of
      P6's own leaving the mutation sweep's fixtures behind and two DynamoDB
      fixtures named with 2 characters, both fixed); `lightness` and `api`
      pass; coverage 81.5% against the floor of 80.5%; conformance 160 passed,
      831 unverified; the browser suite 419 of 419 with the route gate at 370 of
      371, and under account 111122223333 in eu-west-1 418 of 419 on the first
      run (the key-bar spec "every key the cfgedit page names does what it says"
      saw its form still visible after Esc once, under load; it passes alone,
      five of five, and the second full run was 419 of 419: a flake, noted and
      not explained) and 419 of 419 on the second; `console:fidelity -- sqs` 19
      of 19; `bun test fidelity` 59 pass.
- [x] The existing console still works for the sixteen services not yet
      rebuilt: the browser suite passes with the edits to its specs that A6, A12
      and A13 change on purpose (a refusal from a header button is inline; the
      confirm's question is its title).
- [x] `CHANGELOG.md` lists each user-visible change.

## Deep check of the first pass (2026-10-10)

Done before closing: three independent reviews of the whole diff (service-side
Go; console Go and templates; browser-side script, style and specs), screenshots
of the changed pages in both themes, the regenerated cases run through a real
parity harness, and every gate again. What it found is fixed in `e651c44`,
`175ad34`, `c5d7e33`, `788f865` and `55e99a6`. The ones worth remembering:

- **Looking found what no test did.** The key/value editor's value column was a
  sliver (the type menu's wrapper took the row); a second narrow-window notice
  had been added beside one that already existed and hid it; a header refusal
  ran edge to edge. Each now has an assertion on what is drawn, not only on what
  is in the document.
- **A failing poll took focus every three seconds.** A live region whose
  resource had been deleted drew a failure and pulled focus on every tick, and
  every poll cleared the errors of whatever the user was doing. Part of this
  predated the pass; the pass had widened it to plain 5xx answers.
- **B8 did not reach the rule it was written for.** A case was built only around
  an element with no rules of its own, so every tag map (whose value has a
  length and a pattern) was skipped. Fixed, and proved by running the
  regenerated Logs cases.
- **Tests that proved less than they said**: the aws:SecureTransport test passed
  with the key removed; the preview test looked for a field across every
  template; the mutation check compared survivors as a set and missed a second
  one on a line that already had one; the fragment test's timestamp pattern
  matched nothing.

Known and left, each on purpose:

| What | Why it is left |
|---|---|
| `awshttp.NotBuilt`, `iampolicy.ValidateResourcePolicy` and `awspage` have no caller in the shipped binary; the sweep's `notbuilt` column is 0 of 1,372 | They are the foundation's tools. Each service adopts them in its stage, which is what moves its sweep entries. |
| The sweep counts any coded 4xx as handled, so an unbuilt sub-resource operation behind a parent lookup (API Gateway's 72 `NotFoundException` answers) may read as handled | Suspected, not traced. API Gateway's stage checks each against its dispatch. |
| A generated list case repeats one sample; a service that rejects duplicates would refuse it for that | The owning stage sees it as a case that fails for the wrong reason and gives that member a distinct sample. |
| `aws:SourceIp` and `aws:UserAgent` reach a resource policy on an internal peer hop as loopback values | Whether AWS supplies them for a service principal's call is `memory`. Queued for recording in IAM's doc. |
| A connection that never answers draws nothing | It is the "cannot reach doze-aws" banner of part D. |
| The key/value editor: choosing null and switching back loses the text; switching text to true/false makes it `true` | Both are a type the user chose. The row says when a value does not fit its type. |
| `copy_reach_test` tells a home page from a first-resource page only by whether the title names the seeded resource | Enough to hold the assignment; a stronger marker needs each home page to declare itself, which is the console stage's. |
| `Execute change set` is a plain button though a change set can delete resources | A wording decision for CloudFormation's stage (A13's per-service read). |

Gates after the check, on this machine: `check`, the race suite, `lightness`,
`api`, the browser suite with the route gate (249 pass, 369 of 370 routes), the
same suite under account 123456789012 in eu-west-1 (247 pass; the two failures
are the EventBridge archive ARN and the S3 share-link region, both known),
conformance (123 pass, 717 responses unverified, nothing recorded). `coverage`
reads below the floor here and did before the pass; the floor is CI's. The
demo seed completes except two Lambda invocations that need a Node this
machine does not have pinned.

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
