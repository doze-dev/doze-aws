# EventBridge

Stage 9. Size L. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds EventBridge's own.

## 1. Summary

Event buses, rules (pattern and schedule), targets, archives and replays,
connections and API destinations, over one bbolt store on one wire protocol
(JSON 1.1, `AWSEvents`). The pattern language lives in `internal/eventpattern`
and is shared with SNS filter policies; schedules use `internal/awscron` and a
one-second ticker.

Peers: it delivers into SQS, SNS, Lambda and CloudWatch Logs as
`events.amazonaws.com`, and makes real HTTP requests for API destinations.
CloudFormation and `provision` create its resources (`internal/provision/svc_events.go`,
`svc_events_http.go`). Nothing calls into it except apps and Lambda destinations.

It is L because the surface is three services in one (routing, archive and
replay, HTTP delivery), because delivery is where its worst defects sit and
none of them is visible to the caller, and because the console group is 24
boards.

**Estimate:** L, five or more focused days. **Actual:** _fill in when done._

## 2. Contract

The model lists **57** operations (`eventbridge/testdata/ops_eventbridge.json`).
40 are dispatched (`eventbridge/actions.go`, `handlers`); 17 are refused by name
(`eventbridge/eventbridge.go`, `stubActions`) with `UnsupportedOperationException`.

| Operations | Now | Target | Note |
|---|---|---|---|
| PutEvents | F | F | A bus given as an ARN, or a bus that does not exist, is accepted and dropped. See EB-1, EB-2. |
| PutRule, DescribeRule, ListRules, DeleteRule | F | F | Per-member tier below. No paging (EB-10). |
| EnableRule, DisableRule | F | F | |
| PutTargets, RemoveTargets, ListTargetsByRule, ListRuleNamesByTarget | F | F | Per-member tier below. Any ARN is accepted (EB-3). |
| CreateEventBus, UpdateEventBus, DescribeEventBus, ListEventBuses | F | F | Per-member tier below. |
| DeleteEventBus | F | F | Removes the bus's rules. Decide (EB-4). |
| TestEventPattern | F | F | |
| TagResource, UntagResource, ListTagsForResource | F | F | Rules and buses. Archives, connections and destinations: not established. |
| CreateArchive, DescribeArchive, ListArchives, UpdateArchive, DeleteArchive | F | F | Retention is C. |
| StartReplay, DescribeReplay, ListReplays | F | F | Synchronous; `COMPLETED` at once. |
| CancelReplay | S | S | Dispatched, always `IllegalStatusException`. In `not-built.md`. |
| CreateConnection, UpdateConnection, DeauthorizeConnection, DeleteConnection, DescribeConnection, ListConnections | F | F | VPC Lattice connectivity refused by name. |
| CreateApiDestination, UpdateApiDestination, DeleteApiDestination, DescribeApiDestination, ListApiDestinations | F | F | Rate limit is C. `http://` accepted, a declared difference. |
| PutPermission, RemovePermission | S | S | "cross-account permissions have no meaning locally". |
| ActivateEventSource, DeactivateEventSource, DescribeEventSource, ListEventSources, CreatePartnerEventSource, DeletePartnerEventSource, DescribePartnerEventSource, ListPartnerEventSources, ListPartnerEventSourceAccounts, PutPartnerEvents | S | S | Partner sources. |
| CreateEndpoint, DeleteEndpoint, DescribeEndpoint, ListEndpoints, UpdateEndpoint | S | S | Global endpoints. |

Count: 39 F, 18 S (17 refused by name plus CancelReplay). `not-built.md` lists
the 17 as one grouped row; whether the ledger test reconciles them one by one
is not established.

"The operation is F" hides which members do anything.

**PutRule**

| Member | Now | Target | To confirm |
|---|---|---|---|
| Name, EventBusName, EventPattern | F | F | |
| ScheduleExpression | F | F | Default bus only. One message for every malformed expression. |
| State `ENABLED`, `DISABLED` | F | F | |
| State `ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS` | accepted, rule never fires | F (treated as enabled) or refused | EB-9 |
| Description, RoleArn | C | C | Stored and returned. |
| Tags | dropped | F | EB-8 |

**PutTargets, per target**

| Member | Now | Target | To confirm |
|---|---|---|---|
| Id, Arn (SQS, SNS, Lambda, log group, API destination) | F | F | |
| Arn of any other service, or not an ARN at all | accepted, delivers nothing | refused, or C and listed | EB-3 |
| Input, InputPath, InputTransformer | F | F | Predefined variables and array paths: EB-5, EB-6. Two at once accepted: EB-12. |
| HttpParameters | F | F | API destination targets. |
| SqsParameters, DeadLetterConfig, RetryPolicy, RoleArn | dropped | F (SqsParameters), C (the rest) | EB-7 |
| EcsParameters, BatchParameters, KinesisParameters, RunCommandParameters, RedshiftDataParameters, SageMakerPipelineParameters, AppSyncParameters | validated, then dropped | C or refused with the target | not established beyond "the target struct has no field for them" |

**Buses, archives, connections, destinations**

| Member | Now | Target | To confirm |
|---|---|---|---|
| Bus Description, KmsKeyIdentifier, DeadLetterConfig | C | C | Stored and returned; the console shows none of them. |
| Bus LogConfig | validated; stored or not is not established | C | |
| Bus Tags at CreateEventBus | dropped (`CreateBus(name, nil)`) | F | EB-8 |
| Bus Policy | never set; PutPermission is S | S | The console's hint says otherwise (EB-24). |
| Archive EventPattern | F | F | Cannot be cleared (EB-14). |
| Archive RetentionDays, Description | C | C | Never expired. |
| Archive KmsKeyIdentifier | not established | C | |
| Replay window, `Destination.FilterArns` | F | F | The console sends neither. |
| Replay Description | not established | C | |
| Connection auth (Basic, API key, OAuth), invocation parameters | F | F | |
| Connection KmsKeyIdentifier | not established | C | |
| Destination endpoint, method | F | F | |
| Destination InvocationRateLimitPerSecond, Description | C | C | The form already says "stored not enforced". |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `eventbridge/`: 32 tests in 14 files (SDK v1 and v2, scheduler, scheduled delivery, API destinations and revoke, log-group target, declared fields, bus refusals, conformance fixes). `internal/eventpattern`: 2 tests. `internal/awscron`: 5. |
| Parity | `rejection_parity_test.go`, `rejection_parity_http_test.go`, `coverage_model_test.go`; ledger says 448 of 448 model constraints enforced across 40 operations, `knownGaps` empty |
| Conformance | `conformance/tests/test_eventbridge.py`: 7 scenarios, all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/eventbridge.spec.ts`: 8 tests |
| Restart | Whether a data-dir test covers archives, replays, connections and destinations is not established |
| Mutation | No per-package baseline yet (foundation B3) |

The model constrains shapes well and behaviour not at all: every rule about
what a bus does with an event is hand-derived. All of delivery is `doc` or
`memory` today.

## 4. Findings

Evidence `run` means reproduced against a binary built from this tree on
2026-10-10.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| EB-1 | P0 | `PutEvents` with `EventBusName` given as the bus's ARN returns an `EventId` and delivers nothing. `putOneEvent` uses the string as the bus name; `busOrDefault`, which strips an ARN, is not called here. | `eventbridge/actions.go`, `putOneEvent` | run | doc: the API reference gives `EventBusName` as "name or ARN" | SDK test: rule and queue on `default`, put with the bus ARN, receive one message |
| EB-2 | P0 | `PutEvents` naming a bus that does not exist returns success and drops the event. | `actions.go`, `putOneEvent` | run | memory: an entry-level error | SDK test: put to `ghost`, assert `FailedEntryCount` 1 and the entry's `ErrorCode` |
| EB-3 | P0 | `PutTargets` accepts any `Arn`, including `not-an-arn` and a Kinesis stream. `dispatch` then logs "unsupported target service" and delivers nothing; the caller sees success on both calls. | `actions.go`, `putTargets`, `dispatch` | run (accepted), read (no delivery) | doc: Kinesis, Step Functions and others are valid targets; an invalid ARN: memory | SDK test: a Kinesis target is refused or reported as cosmetic; `not-an-arn` is refused |
| EB-4 | P0 | `DeleteEventBus` on a bus with rules removes the rules and succeeds, and leaves the bus's archives behind pointing at nothing. `SUPPORT.md` states the first half as the contract. | `eventbridge/store.go`, `DeleteBus` | run | memory: AWS refuses until the rules are gone | Store test: delete a bus with a rule and an archive; assert the decided outcome |
| EB-5 | P0 | An input transformer leaves `<aws.events.rule-name>`, `<aws.events.rule-arn>`, `<aws.events.event.json>` and the other predefined names in the output as literal text. | `actions.go`, `shapeInput` | run | memory: the user guide lists predefined variables | SDK test: template with `<aws.events.rule-name>`, read the delivered body |
| EB-6 | P0 | A path with an array index (`$.detail.items[0]`) resolves to an empty string in a transformer, and with `InputPath` the event is dropped. `extractPath` splits on dots only. | `actions.go`, `extractPath` | run | memory | SDK test: both forms against an event with an array |
| EB-7 | P0 | `PutTargets` drops `SqsParameters`, `DeadLetterConfig`, `RetryPolicy` and `RoleArn`: `ListTargetsByRule` returns only `Id` and `Arn`. A FIFO queue target therefore has no `MessageGroupId`; the IaC tools see drift on the rest. | `store.go`, `target`; `actions.go`, `putTargets` | run (dropped), read (FIFO consequence) | model (the members), doc (FIFO needs the group id) | Declared-fields test for the four members; SDK test delivering to a FIFO queue |
| EB-8 | P1 | `Tags` on `PutRule` are dropped (`ListTagsForResource` returns none). `Tags` on `CreateEventBus` are dropped too: the handler calls `CreateBus(name, nil)`. | `actions.go`, `putRule`, `createEventBus` | run (rule), read (bus) | model | SDK test: create with tags, list them |
| EB-9 | P0 | A rule put with `State: ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS` is stored and never fires: matching tests `State != "ENABLED"`. | `actions.go`, `matchAndDispatch`; `scheduler.go`, `fireDueSchedules` | run | doc: the state is an enabled state | SDK test: rule in that state, put a matching event, receive it |
| EB-10 | P1 | No list operation pages. `ListRules` with `Limit: 1` returns every rule and no `NextToken`; none of the handlers reads `Limit` or `NextToken`. | `actions.go`, `archive_actions.go`, `connection_actions.go`, `apidest.go` | run (ListRules), read (the rest) | model | SDK test per list operation with `Limit: 1` over three resources |
| EB-11 | P1 | A rule accepts more than five targets (seven were stored). | `actions.go`, `putTargets` | run | memory: five per rule, `LimitExceededException` | Parity case with six targets |
| EB-12 | P1 | A target with both `Input` and `InputPath` is accepted; `Input` silently wins. | `actions.go`, `putTargets`, `shapeInput` | run | doc: at most one of the three | Parity case for each pair |
| EB-13 | P1 | `PutEvents` accepts a `Detail` that is valid JSON but not an object (`[1]`, `123`). | `actions.go`, `putOneEvent` | run | memory: `MalformedDetail` | SDK test: entry error for an array and a number |
| EB-14 | P1 | `UpdateArchive` cannot clear a pattern: an empty `EventPattern` is ignored. `DescribeArchive` never returns `StateReason`. | `archive_actions.go`, `updateArchive`, `archiveView` | read | not established | SDK test: update with an empty pattern, describe |
| EB-15 | P1 | `StartReplay` does not check that the end is after the start, that the destination is the archive's own bus, or that the destination exists. Each completes with "Replayed 0 event(s)" or replays into another bus. | `archive_actions.go`, `startReplay` | read | memory | Parity cases for the three |
| EB-16 | P1 | `UpdateEventBus` answers a missing bus with HTTP 404; every other not-found in the package is 400. Messages also differ in case ("Event bus X does not exist." against "event bus X does not exist"). | `actions.go`, `updateEventBus` | read | not established | Parity test asserting status and code for a missing bus on every bus operation |
| EB-17 | P2 | Test event counts a DISABLED rule as a match ("1 of 3 match this event") though Publish skips it. `ebMatch` never reads the rule's state. | `internal/console/handlers_eb.go`, `ebMatch` | run | n/a | Handler test: disabled matching rule, assert the verdict |
| EB-18 | P2 | A connection's description cannot be changed alone. The detail form's select always posts `auth_type`, so `UpdateConnection` sends empty credentials and is refused. The client's "description only" branch is dead. | `templates/eb_http.html`, `eb_connection_detail`; `client_eb_http.go`, `UpdateConnection` | run (the form's post, by curl; not in a browser) | n/a | Browser test: edit the description, save, read it back |
| EB-19 | P2 | Add target sends only `Id` and `Arn`. A destination whose endpoint has `*`, which the destination form invites, never delivers from a console-made target. | `handlers_eb.go`, `ebAddTarget`; `apidest_deliver.go`, `buildRequest` | read | n/a | Handler test: add a `*` destination with a path value, deliver to a local server |
| EB-20 | P2 | Add target offers queues, functions and API destinations only. SNS topics and log groups are deliverable and absent. | `templates/eb.html`, `eb_targets` | read | n/a | Browser test: a topic is in the list |
| EB-21 | P2 | Create rule with a name that exists replaces the pattern and schedule, re-enables a disabled rule, keeps targets and tags, and toasts "created". The API is an upsert by design; the form says nothing. | `handlers_eb.go`, `ebCreateRule`; `store.go`, `PutRule` | run | doc: PutRule creates or updates | Browser test: the form names the existing rule before submit |
| EB-22 | P2 | The rule page discards `Input`, `InputPath`, `InputTransformer` and `HttpParameters`, which `ListTargetsByRule` returns. A target that reshapes or drops events looks like any other. | `client_eb.go`, `GetRule` | read | n/a | Client test: the fields survive into `Target` |
| EB-23 | P2 | The archive editor cannot clear a pattern or set retention back to 0: the client sends each only when non-empty or above zero. | `client_eb.go`, `UpdateArchive` | read | n/a | Handler test, after EB-14 is decided |
| EB-24 | P3 | The Trace tab promises "the bus's own record, including its resource policy". Nothing can set a bus policy. The same handler's refusal mentions a "stream", which is not a deliverable target. | `templates/eb.html`, `eb_trace`; `handlers_eb.go`, `ebRulesByTarget` | read | n/a | Template test on the two strings |
| EB-25 | P2 | The Replays panel on a bus lists every replay on the machine. | `client_eb.go`, `ListReplays`; `handlers_eb.go`, `ebBus` | read | n/a | Handler test with replays on two buses |
| EB-26 | P3 | Every rule page says "nothing feeds this yet". Nothing in the graph can feed a rule; it is fed by events put on its bus, or by the clock. | `templates/panes.html`, `connections` | run | n/a | Template test for a rule node |
| EB-27 | P3 | The pattern builder's "Test an event" placeholder is pattern-shaped (`"source":["orders"]`); the field takes an event. | `templates/eb.html`, `pattern_builder` | read | n/a | None; fixed with the board |
| EB-28 | P2 | An INACTIVE destination's update form preselects the first connection listed, because none matches the deleted one. Pressing Update re-points it without a choice having been made. | `templates/eb_http.html`, `eb_destination_detail` | read | n/a | Browser test: the select starts empty |
| EB-29 | P2 | A delivery that fails is visible nowhere in the console. Publish toasts "Event published"; the target chip still links to a deleted queue; the only record is the process log. `SUPPORT.md` says "A target that fails fails inside your `PutEvents` call", which is not what happens: `PutEvents` returns success. | `actions.go`, `dispatch`; `docs/SUPPORT.md` | run (deleted queue, enforce denial, INACTIVE destination, unmatched InputPath) | n/a | SDK test pinning what `PutEvents` returns when a target fails; then correct the sentence |
| EB-30 | P3 | The delete-archive, delete-rule and remove-target confirms were read and are true. The bus confirm is false and is foundation A13. | `templates/eb.html` | read | n/a | Covered by A13's rule |
| EB-31 | P1 | `ListRules` on an event bus that does not exist answers an empty list. AWS answers `ResourceNotFoundException` (the API reference lists it for ListRules). More forgiving than AWS, and it is why the console drew a bus page for `/eb/no-such-bus`; the console now checks the bus list itself. | `eventbridge/store.go` `Rules`; `eventbridge/actions.go` `listRules` | run | doc: the ListRules errors list | Rejection-parity case: ListRules with a bus name that was never created |

Owned by the foundation, listed so they are not lost: the hard-coded account
and region in `busARN`, `archiveARN` and the test event (**A8**, where the
archive capturing nothing was reproduced); the unreachable `copy.go` lines
(**A7**); the bus-delete confirm sentence (**A13**); the "because AWS does"
line under hand-derived refusals such as "invalid event bus name" (**A2**).

**Moved into this stage from CloudFormation (CFN-3, P0, run).**
`AWS::Events::EventBus` is mapped, reports CREATE_COMPLETE and makes no bus.
It keeps its id and is closed here, because EventBridge is not done while a
stack that declares a bus makes nothing. Acceptance: a stack with a bus and a
rule on it leaves both, readable through `DescribeEventBus` and `DescribeRule`;
the plan's README says why it moved.

## 5. What to check

Beyond the common criteria.

**Buses**
- [ ] Name rules: the model's pattern allows `/`; the store refuses `/`, a
      space and a backslash. Establish which AWS does and make them agree.
- [ ] `default` cannot be created or deleted; creating a name twice is
      `ResourceAlreadyExistsException`.
- [ ] A name starting `aws.` is reserved on AWS (memory). Establish and decide.
- [ ] Delete with rules, with targets, with an archive (EB-4).
- [ ] Describe and update on a missing bus: status, code, message (EB-16).
- [ ] Every C member round-trips through create, update and describe, and a
      member omitted on update is cleared, as the handler's comment claims.

**Rules**
- [ ] Name 1 to 64; one of pattern or schedule required; both together: establish.
- [ ] `PutRule` on an existing name: targets and tags kept, everything else
      replaced, state back to the default. Establish each against AWS.
- [ ] Every state value (EB-9).
- [ ] `DeleteRule` with targets is refused; on a missing rule succeeds;
      `Force` on a managed rule: not established.
- [ ] Enable and disable on a missing rule.
- [ ] `ListRules` with `NamePrefix`, `Limit`, `NextToken` (EB-10).

**Patterns**
- [ ] Every operator in `internal/eventpattern`, alone and inside
      `anything-but`, against strings, numbers, booleans, null and arrays.
- [ ] `$or` at the top level and nested; an empty `$or`.
- [ ] A string never matches a number and the reverse.
- [ ] `numeric` with one bound, two bounds, and an unknown operator.
- [ ] `cidr` for IPv4 and IPv6.
- [ ] The 4096-character limit, and that `TestEventPattern` and `PutRule`
      refuse the same patterns with the same code.
- [ ] `TestEventPattern` requires the event's mandatory fields on AWS (memory).
      Here any JSON is accepted. Establish.
- [ ] The text of `InvalidEventPatternException`: AWS prefixes a fixed phrase
      (memory). Establish, because SDK callers match on it.

**Schedules**
- [ ] `rate`: singular for 1, plural otherwise, minute, hour, day, zero refused.
- [ ] `cron`: six fields, exactly one `?`, `L`, `W`, `#`, names, ranges that
      wrap, steps, year bounds.
- [ ] A schedule on a custom bus is refused.
- [ ] First fire after arming; a disabled rule loses its clock; a restart does
      not replay what was missed.
- [ ] The scheduled event's shape: `source`, `detail-type`, `resources`, `detail`.

**Events**
- [ ] 1 to 10 entries; `Source` and `DetailType` required; `Detail` an object
      (EB-13); the 256 KB entry size limit (doc): not enforced today, establish.
- [ ] A bus by name, by ARN (EB-1), missing (EB-2).
- [ ] `Resources`, `Time` and `TraceHeader` in the delivered event; a supplied
      `Time` is ignored today (the handler always stamps now): establish.
- [ ] Per-entry failure does not fail the batch.

**Targets**
- [ ] Id 1 to 64; the same Id replaces; five per rule (EB-11).
- [ ] Each ARN kind: delivered, refused, or cosmetic (EB-3).
- [ ] Input, InputPath, InputTransformer: one at most (EB-12); a path that
      finds nothing; array indexes (EB-6); predefined variables (EB-5); how a
      string, a number, an object and null render in a template.
- [ ] A FIFO queue target with and without `SqsParameters` (EB-7).
- [ ] `RemoveTargets` with an Id that is not there: `FailedEntries` or not.
- [ ] `ListRuleNamesByTarget` on the right bus only.
- [ ] What `PutEvents` returns when a target fails (EB-29).

**Archives and replays**
- [ ] Archive name 1 to 48, unique across buses; a pattern; no pattern.
- [ ] Capture: only real `PutEvents`, not replays; scheduled events: establish.
- [ ] Update: change and clear the pattern (EB-14), change retention.
- [ ] Delete removes the events; replays already recorded stay.
- [ ] Replay: window inclusive at both ends, `FilterArns`, a name in use,
      a missing archive, the three checks in EB-15.
- [ ] Whether a replayed event carries `replay-name` (memory). Establish.

**Connections and API destinations**
- [ ] Each auth type's required members; secrets never returned by describe or list.
- [ ] `!`-marked parameters returned without a value.
- [ ] Update with only a description keeps the credential and the state.
- [ ] Deauthorize, then update, returns to AUTHORIZED.
- [ ] Delete a connection: its destinations go INACTIVE; re-pointing reactivates.
- [ ] Delivery: `*` filled in order; an unfilled `*`; header, query and body
      merge order; Basic, API key and bearer; token cached until expiry and
      forgotten on update; one retry on a transport error, 429 and 5xx.
- [ ] OAuth with `OAuthHttpParameters`.

**Access**
- [ ] Under enforce, delivery to SQS, SNS and Lambda needs the destination's
      policy to admit `events.amazonaws.com` with the rule's ARN as
      `aws:SourceArn`; under soft it is delivered and logged.

**Lifecycle**
- [ ] Buses, rules, targets, tags, archives with their events, replays,
      connections with their secrets, and destinations survive a restart.

## 6. Console

The design's "EventBridge" page: 24 boards. Templates: `templates/eb.html`,
`templates/eb_http.html`, the `eb_bus_create` and `eb_rule_create` blocks in
`create.html`, shared pieces in `panes.html` and `layout.html`. Handlers:
`handlers_eb.go`, `handlers_eb_http.go`, `ebRuleCreatePage` in
`handlers_create.go`. Routes: `ebRoutes` in `routes_messaging.go`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Eb-Home | A fresh stack: the default bus, no rules | `eb_bus`, `eb_rule_list` | exists |
| Eb-02-Bus-Create | Create event bus | `eb_bus_create` | form exists; the ARN preview and "what an app sends" are **new** |
| Eb-03-Bus-Create-Refused | Refused inline | `fail_inline` | exists |
| Eb-04-Bus-New | A new custom bus | `eb_bus` | exists |
| Eb-05-Rules | The default bus's rules | `eb_rule_list` | exists; the Targets column is **new** |
| Eb-06-Test-Event | Verdicts, publish, invalid JSON | `eb_bus`, `eb_rule_list`; `ebMatch`, `ebTestEvent` | exists; "would deliver to" and `match · disabled` are **new** |
| Eb-07-Rule-Create | Create rule, pattern | `eb_rule_create`, `pattern_builder` | exists; "the pattern in words" and the taken-name notice are **new** |
| Eb-08-Rule-Create-Schedule | Create rule, schedule | `eb_rule_create` | field exists; "when it fires" is **new** |
| Eb-09-Rule-Create-Refused | Refused inline | `fail_inline` | exists |
| Eb-10-Rule | A pattern rule | `eb_rule`, `eb_targets`, `connections`, `tag_editor` | exists; the Sends column and the fed-by line are **new** |
| Eb-11-Rule-Schedule | A scheduled rule, disabled | `eb_rule` | exists; the "Stopped" note is **new** |
| Eb-12-Target-Add | Add a target | `eb_targets`; `ebAddTarget` | exists; SNS topics and the path parameter are **new** |
| Eb-13-Target-Shape | What a target receives | `eb_targets`; `ebAddTarget` | **new**, the whole board |
| Eb-14-Archives | Archives and replays | `eb_archives`, `eb_replays` | exists |
| Eb-15-Archive-Detail | An archive, opened | `eb_archive_detail` | exists |
| Eb-16-Replay | After a replay | `eb_kv_detail`; `ebReplayDetail` | exists |
| Eb-17-Replay-Options | A window and a rule filter | `eb_archives`; `ebReplay` | **new**, the whole board |
| Eb-18-Trace | Trace a target back; describe the bus | `eb_trace`, `eb_rules_by_target`, `eb_kv_detail` | exists; rule state and three bus rows are **new** |
| Eb-19-Destinations | Connections and API destinations | `eb_destinations`, `eb_http_tables` | exists |
| Eb-20-Connection | A connection, opened | `eb_connection_detail` | exists; "keep current" is **new** |
| Eb-21-Connection-Create | OAuth, refused | `eb_http_body`, `eb_connection_auth_fields` | exists |
| Eb-22-Destination | A destination, INACTIVE | `eb_destination_detail` | exists; "what a delivery does" is **new** text |
| Eb-23-Confirms | Seven confirms | `hx-confirm` in both templates | exist; the bus sentence changes (A13) |
| Eb-24-Bad-Nothing-Arrived | Published, nothing arrived | no single screen; states on `eb_rule`, `eb_rule_list`, `eb_http_tables` | the states exist; the `not found` mark is **new** |

There is no "nothing exists" board and no "nothing selected" board: the
default bus always exists and `/eb` opens it (A7). There is no Denied board: a
bus has no policy that can be set, and the console acts as root, so no
EventBridge console action can be denied. If EB-4's decision or a later stage
gives a bus a policy, a Denied board is owed.

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Targets column on the rules list | `ruleTargets`, which the wiring graph already calls per rule |
| Sends column on a rule's targets | `ListTargetsByRule` members the client discards today (EB-22) |
| "Publishing this would deliver to" | The matching rules from `ebMatch` and their targets |
| `match · disabled` | The rule's `State`, already in the list (EB-17) |
| Bus ARN preview | The typed name and the instance identity (`awsident`), not literals (A8) |
| "What an app sends" | The typed name. Static otherwise. |
| The pattern in words | The builder's rows (`static/pattern.js`). A pure function of the form. |
| When it fires, next three times | `internal/awscron` `Parse` and `Next`, and `parseRate`; needs an endpoint or the parser exported to the console |
| Taken-name notice | The bus's rule list, already loaded for the page (EB-21) |
| Fed-by line on a rule | The rule's bus, or its schedule (EB-26) |
| "Stopped" note on a disabled schedule | The rule's `State` and `Schedule` |
| SNS topics in Add target | `ListTopics`, which the console already has |
| Path parameter field | `HttpParameters.PathParameterValues`, shown when the chosen destination's endpoint contains `*` (EB-19) |
| Target shaping form | `PutTargets` `Input`, `InputPath`, `InputTransformer` |
| Shaping preview | The Test event's document run through the same substitution delivery uses. `shapeInput` is unexported in `eventbridge`; it moves to a shared package or the console calls a new read-only endpoint. Not built until one of those exists. |
| Replay window, rule filter, name | `StartReplay` `EventStartTime`, `EventEndTime`, `Destination.FilterArns`, `ReplayName` |
| Rule state in trace results | The bus's rule list |
| Bus Description, KMS key, dead-letter rows | `DescribeEventBus`, which already returns them |
| "Keep current" on a connection | Leaving `AuthorizationType` out of `UpdateConnection` (EB-18) |
| `not found` mark on a target | The queue, function and topic lists the rule page already loads |

**Where the teaching lines come from**

| Line | Source |
|---|---|
| Bus name 1 to 256, characters | `eventbridge/validate.go`, `CreateEventBus`; the store's extra rule until section 5 settles it |
| Rule name 1 to 64; pattern up to 4096; schedule up to 256 | `validate.go`, `PutRule` |
| "A field's value is always a list"; the refusal texts | `internal/eventpattern`, `parseLeaf`, `Parse` |
| "Typed 5 is the number 5" | `static/pattern.js`, `emitCond` |
| "A string is never matched against a number"; "one element matching is enough" | `eventpattern`, `exactCond`, `leafNode.matchValue` |
| Schedule spellings, the `?` rule, day-of-week numbering, UTC | `eventbridge/scheduler.go`, `parseRate`; `internal/awscron`, `Parse` |
| "The clock starts when doze-aws first sees the rule"; nothing missed is replayed | `scheduler.go`, `fireDueSchedules`; `SUPPORT.md`, PutRule row |
| Scheduled event shape | `scheduler.go`, `fireScheduled` |
| Template and path limits (8192, 256) | `validate.go`, `PutTargets` |
| "A string goes in as it is"; "a path that finds nothing becomes an empty string" | `actions.go`, `renderScalar`, `shapeInput` |
| Archive name 1 to 48; replay name 1 to 64 | `validate.go`, `CreateArchive`, `StartReplay` |
| "Retention stored, never expired" | `SUPPORT.md`, archive row |
| "Only enabled rules with a pattern take part in a replay" | `actions.go`, `matchAndDispatch` |
| "A replay does not add to the archive" | `archive_actions.go`, `captureToArchives` |
| Connection name 1 to 64; secrets never returned; the Secret ARN | `validate.go`, `CreateConnection`; `SUPPORT.md`, "API destinations" |
| Delivery: 5 s, one retry, no dead-letter queue | `apidest_deliver.go`; `SUPPORT.md`, "API destinations" |
| Rate limit stored, not enforced | `SUPPORT.md`, API destination row |
| Enforce needs the destination's policy | `SUPPORT.md`, "Service principals" |

Every limit above is to come from the settings accessor (foundation B4), not
from text in a template.

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Eb-15: "0 means keep indefinitely on AWS" (memory).
- Eb-22: "AWS retries for 24 hours and can park the event in a dead-letter
  queue" (the ledger says it; the ledger's own source is not recorded).
- Eb-12 and Eb-24: "as on AWS" for the destination policy (doc, by way of the ledger).
- Eb-13: "A predefined name is left as written" is true today and stops being
  true when EB-5 is fixed; "no array index" likewise with EB-6. Both lines are
  rewritten with the fixes.
- Eb-13 and Eb-10: the Sends text for a target that carries two input members
  at once, which EB-12 will make impossible.
- Eb-23: the bus confirm's sentence depends on EB-4's decision. If the bus is
  refused while it has rules, the sentence is the old one and the new one goes.
- Eb-07: "A name that already exists replaces that rule's pattern, enables it,
  and keeps its targets and tags" is `run` here; that AWS does the same is `doc`
  for the replace and not established for tags.
- Eb-24: the six log lines are quoted from a run. They are the process log's
  wording, not a contract, and the board quotes them only until EB-29 gives the
  console something of its own to show.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | Bus delete with rules and an archive (EB-4); tags at create (EB-8); every rule state (EB-9); archive update and clear (EB-14); replay checks (EB-15) |
| SDK v2 | Put by bus ARN and to a missing bus (EB-1, EB-2); target ARN kinds (EB-3); predefined variables and array paths (EB-5, EB-6); declared target members and a FIFO queue (EB-7); paging on every list (EB-10); six targets (EB-11); two input members (EB-12); non-object detail (EB-13); what `PutEvents` returns on a failed target (EB-29) |
| Rejection parity | Status and code for a missing bus on every bus operation (EB-16); the section 5 limits the model does not carry |
| Pattern | A table test per operator and value type in `internal/eventpattern`, shared with SNS |
| Restart | Archives with events, replays, connections with secrets, destinations, targets with every stored member |
| Conformance (boto3) | One scenario per row of section 8 |
| Console handler | `ebMatch` and state (EB-17); add target with path parameters (EB-19); replays per bus (EB-25); target members reach the page (EB-22) |
| Browser | Description-only connection update (EB-18); a topic as a target (EB-20); taken-name notice (EB-21); INACTIVE destination's select (EB-28); each `new` item; each confirm's sentence; the group under a non-default account and region |
| Mutation | `eventbridge/` baseline, then survivors in `actions.go` (`shapeInput`, `extractPath`, `matchAndDispatch`), `scheduler.go`, `apidest_deliver.go`; `internal/eventpattern` |

## 8. Recording queue

Every one of these is `doc` or `memory` today.

| Claim | Source now | Scenario |
|---|---|---|
| `PutEvents` accepts a bus ARN in `EventBusName` | doc | `put_events_bus_arn` |
| `PutEvents` to a missing bus: the entry's error code and message | memory | `put_events_missing_bus` |
| `Detail` that is not an object is `MalformedDetail` | memory | `put_events_detail_not_object` |
| Entry size limit and its error | doc | `put_events_entry_too_large` |
| A supplied `Time` is kept in the delivered event | doc | `put_events_time` |
| `DeleteEventBus` with rules is refused; its code and message | memory | `delete_bus_with_rules` |
| `DeleteEventBus` on a missing bus succeeds | memory | `delete_bus_missing` |
| Bus name: `/` allowed or not; `aws.` reserved | memory | `bus_name_rules` |
| `PutRule` on an existing rule: what is kept and what is reset | doc | `put_rule_update_semantics` |
| A rule with both a pattern and a schedule | memory | `rule_pattern_and_schedule` |
| `ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS` matches ordinary events | doc | `rule_cloudtrail_state` |
| A schedule on a custom bus: code and message | doc | `schedule_on_custom_bus` |
| A malformed schedule: "Parameter ScheduleExpression is not valid." | memory | `schedule_malformed` |
| `InvalidEventPatternException` message text | memory | `pattern_error_text` |
| `TestEventPattern` requires the event's mandatory fields | memory | `test_pattern_event_shape` |
| Five targets per rule, `LimitExceededException` | memory | `targets_limit` |
| An invalid or unsupported target ARN at `PutTargets` | memory | `target_arn_validation` |
| At most one of Input, InputPath, InputTransformer | doc | `target_input_exclusive` |
| Predefined transformer variables and their names | memory | `transformer_predefined` |
| Array indexes in `InputPath` and `InputPathsMap` | memory | `transformer_array_path` |
| How a missing path renders in a template | memory | `transformer_missing_path` |
| `InputPath` that matches nothing: delivered as what | memory | `input_path_no_match` |
| A FIFO queue target without `SqsParameters` | doc | `target_fifo_queue` |
| `RemoveTargets` with an unknown Id | memory | `remove_targets_unknown` |
| "Rule can't be deleted since it has targets." | memory | `delete_rule_with_targets` |
| `UpdateArchive` with an empty pattern | memory | `archive_clear_pattern` |
| `RetentionDays` 0 keeps indefinitely | doc | `archive_retention_zero` |
| Scheduled events are archived | memory | `archive_scheduled_event` |
| A replayed event carries `replay-name` | memory | `replay_event_shape` |
| `StartReplay`: end before start; a destination that is not the archive's bus | memory | `replay_validation` |
| `UpdateConnection` with only a description keeps the credential | doc | `connection_update_description` |
| API destination retry: 24 hours, then the dead-letter queue | doc | `apidest_retry` (not recordable cheaply; stays `doc`) |
| Paging: `Limit` and `NextToken` on every list operation | model | `list_paging` |

## 9. Owed to integration

| What | With |
|---|---|
| Delivery under enforce needs the queue policy to admit `events.amazonaws.com` for the rule; soft delivers and logs | SQS, IAM |
| The same for a function permission and a topic policy | Lambda, SNS, IAM |
| A FIFO queue as a target, with `SqsParameters` | SQS |
| A target whose queue, topic or function was deleted: what the caller and the console see | SQS, SNS, Lambda |
| A log-group target writes one line per event in a stream named for the rule | CloudWatch Logs |
| A schedule invoking a function that reads secrets and parameters | Lambda, Secrets Manager, Parameter Store, KMS |
| A function's async destination putting events on a bus | Lambda |
| A bus, rule, targets with input transformers, an archive, a connection and a destination in one template; tags on the rule and the bus surviving it | CloudFormation, and through SAM, CDK and Terraform |
| A wiring-graph edge for every delivered target kind, including log groups and API destinations | the map (foundation D) |
| Whether a delivery made by the bus appears on the wire, and as what | the wire (foundation C) |
| Replay of a large archive while events are being put; a restart mid-replay | soak |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] EB-1 to EB-30 each closed, with the test that failed first named beside it.
- [ ] Decided and written into `SUPPORT.md`: bus delete with rules (EB-4); what
      a target ARN that is not delivered does (EB-3); whether `PutEvents`
      reports a failed delivery or the ledger sentence changes (EB-29); the
      CloudTrail state (EB-9); five targets (EB-11); the two bus-name rules.
- [ ] The three member tables in section 2 have no "not established" left:
      the seven target parameter blocks, bus `LogConfig`, archive and
      connection `KmsKeyIdentifier`, replay `Description`.
- [ ] Tags on archives, connections and destinations: established, and F or
      refused.
- [ ] `not-built.md` reconciles all 18 S operations, each by name, and the
      ledger test holds that.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] A restart test covers every resource kind in section 3's "Restart" row.
- [ ] No list operation returns more than `Limit`.
- [ ] Every C member reads as cosmetic where the console shows it: rate limit,
      retention, and the three bus rows on Eb-18.
- [ ] All 24 boards implemented from the foundation's shell, each `new` item
      reading only the data named for it. The shaping preview ships only if it
      runs the service's own substitution.
- [ ] No statement on an EventBridge screen is in the "not yet backed" list in
      section 6.
- [ ] The seven confirms say only what the code does, after EB-4 is decided.
- [ ] A failed delivery is visible in the console somewhere other than a log
      line, or Eb-24 says plainly that it is not (EB-29).
- [ ] The pattern builder round-trips every pattern in the pattern table test,
      or falls back to JSON and says so.
- [ ] `e2e/tests/eventbridge.spec.ts` covers every route under `/eb` with the
      route gate on, and the accessibility sweep is clean on the three bus
      tabs, the rule page, both create forms and the destinations page.
- [ ] The group works under a non-default account and region (uses A8).
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: member tiers, the decisions in section 10 | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for EB-1 to EB-16 | `eventbridge/sdk_test.go`, `sdk_coverage_test.go`, `declared_fields_test.go`, `rejection_parity_test.go`, new `eventbridge/store_test.go` |
| 3 | Events: bus by ARN, missing bus, detail shape | `eventbridge/actions.go` (`putOneEvent`, `busOrDefault`) |
| 4 | Targets: ARN kinds, stored members, limits, exclusive input | `eventbridge/actions.go` (`putTargets`, `listTargetsByRule`, `dispatch`), `store.go` (`target`) |
| 5 | Shaping: predefined variables, array paths | `eventbridge/actions.go` (`shapeInput`, `extractPath`, `renderScalar`) |
| 6 | Rules and buses: tags, states, delete, status codes | `eventbridge/actions.go`, `store.go` |
| 7 | Paging on every list | `eventbridge/actions.go`, `archive_actions.go`, `connection_actions.go`, `apidest.go` |
| 8 | Archives and replays | `eventbridge/archive.go`, `archive_actions.go` |
| 9 | Section 5 sweep; pattern table; restart | `internal/eventpattern/eventpattern_test.go`, `internal/awscron/awscron_test.go`, `eventbridge/scheduler_test.go`, `apidest_sdk_test.go`, `persistence_surfaces_test.go` |
| 10 | Conformance scenarios and claims table | `conformance/tests/test_eventbridge.py`, the claims file from foundation B1 |
| 11 | Console client: target members, replays per bus, archive clear, connection update | `internal/console/client_eb.go`, `client_eb_http.go` |
| 12 | Console pages | `internal/console/templates/eb.html`, `eb_http.html`, `create.html` (`eb_bus_create`, `eb_rule_create`), `handlers_eb.go`, `handlers_eb_http.go`, `handlers_create.go`, `routes_messaging.go`, `static/pattern.js` |
| 13 | Browser tests | `e2e/tests/eventbridge.spec.ts` |
| 14 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 to 6 change what a running app receives. Each lands with its
`CHANGELOG.md` line, because a stack that passed before may now be refused.

**Prior art.** AWS's EventBridge user guide is the source for the pattern
operators, the input transformer and its predefined variables, and the target
list; each hand-derived rule's test names the page. AWS publishes its pattern
matcher as open source (`event-ruler`), which is the reference for operator
edge cases the guide does not spell out. Nothing else is cited here: how other
emulators handle bus deletion, target validation or replays was not checked.
