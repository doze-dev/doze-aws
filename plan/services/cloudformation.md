# CloudFormation

Stage 16. Size L. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds CloudFormation's own.

## 1. Summary

A stack service over one bbolt store (`cloudformation/`), speaking the Query
protocol, in front of two internal packages that do the work and are part of
this stage:

- `internal/cfn` parses a template (JSON or YAML, SAM transform), evaluates
  intrinsics and parameters, classifies every resource type as mapped, ignored
  or refused, and transpiles the mapped ones into a resource graph.
- `internal/provision` applies that graph through the gateway (`Apply`), removes
  it (`Destroy`), reports what already exists (`Existing`), and reads a running
  instance back into a graph (`Export`), which `cfn.Emit` writes as a template.
  `doze-aws apply` and `doze-aws export` use the same code.

Everything is synchronous: a stack is terminal when the call returns. Peers:
every other service, because a stack creates resources in all of them. It goes
last for that reason, and most of what is wrong with it is only visible through
a second service.

The size is L and likely the largest stage. Three things make it so: 56 mapped
resource types whose per-property behaviour has never been tabulated, an apply
that converges on create but skips most kinds on update, and 21 console boards.

**Estimate:** L, five or more focused days. **Actual:** _fill in when done._

## 2. Contract

The model lists 90 operations. 23 are dispatched (`handlers` in
`cloudformation/stacks.go`), 67 are refused by name (`stubActions` in
`cloudformation/service.go`). Counted from the code on 2026-10-10; the ledger
says the same.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateStack | F | F | CFN-6, CFN-19. `TemplateURL` from the local S3. |
| UpdateStack | F | F | CFN-1, CFN-17, CFN-23, CFN-26. |
| DeleteStack | F | F | CFN-7, CFN-8. `RetainResources` and `DeletionMode`: not established. |
| DescribeStacks | F | F | CFN-10, CFN-11, CFN-20, CFN-25. |
| ListStacks | F | F | CFN-25. |
| DescribeStackEvents | F | F | CFN-12, CFN-17, CFN-18, CFN-25. |
| DescribeStackResource, DescribeStackResources, ListStackResources | F | F | CFN-16, CFN-17. |
| GetTemplate | F | F | `TemplateStage` and `ChangeSetName` are accepted; whether either changes the answer is not established. |
| GetTemplateSummary | F | F | `ResourceTypes` lists ignored and refused types too (CFN-34). |
| ValidateTemplate | F | F | CFN-9. |
| CreateChangeSet | F | F | CFN-13, CFN-15. `ChangeSetType=IMPORT`, `ResourcesToImport`: not established. |
| DescribeChangeSet, ListChangeSets | F | F | CFN-20. |
| ExecuteChangeSet | F | F | CFN-14. |
| DeleteChangeSet | F | F | |
| ListExports, ListImports | F | F | CFN-7, CFN-21. |
| SetStackPolicy, GetStackPolicy | C | C | Round-trips. An update is never checked against it. |
| UpdateTerminationProtection | C | **F** | The ledger says C; `hDeleteStack` enforces it (run). CFN-38. |
| CancelUpdateStack | S | S | Dispatched, always refused: nothing is ever in flight. |
| ContinueUpdateRollback, RollbackStack | S | S | |
| DetectStackDrift, DetectStackResourceDrift, DetectStackSetDrift, DescribeStackDriftDetectionStatus, DescribeStackResourceDrifts | S | S | |
| StackSets (17, plus ListStackInstanceResourceDrifts, ListStackSetAutoDeploymentTargets, DescribeStackInstance) | S | S | |
| Extension registry and hooks (RegisterType … DescribePublisher, DescribeChangeSetHooks, GetHookResult, ListHookResults) | S | S | |
| Resource scanning (5), generated templates (6), stack refactoring (5), Organizations access (3) | S | S | |
| EstimateTemplateCost, DescribeAccountLimits, SignalResource, RecordHandlerProgress, DescribeEvents | S | S | |

Every S row answers `400 ValidationError` with "doze-aws does not implement
<op>: <reason>" (run, `DetectStackDrift`). An action in no table answers
`InvalidAction` (run). Whether `ValidationError` is the code the common
criterion wants for an unsupported operation is a decision (CFN-24).

**Request settings on the F operations**

| Setting | Tier now | Target | To confirm |
|---|---|---|---|
| `TemplateBody` | F | F | |
| `TemplateURL` | F | F | Three URL shapes (`parseS3URL`). Fetched from the local S3 only. |
| `Parameters`, `UsePreviousValue` | F | F | An empty value means "keep"; `UsePreviousValue` itself is not read. Whether an explicit empty string can be set is not established. |
| `UsePreviousTemplate` | F | F | |
| `DisableRollback`, `OnFailure` | F | F | Tested (`rollback_test.go`). |
| `Capabilities` | C | F or listed | Stored and returned. Never required (CFN-19). |
| `Tags` | C | not established | Stored on the stack. Whether they reach the resources is not established. |
| `NotificationARNs` | C | C | Stored. Nothing is published. |
| `RollbackConfiguration`, `TimeoutInMinutes`, `RoleARN`, `StackPolicyBody`, `ClientRequestToken` | C | C | Stored or ignored; the ledger must list each. |
| `EnableTerminationProtection` on CreateStack | not established | F | `readDeployFields` does not read it. |
| `RetainResources`, `DeletionMode` on DeleteStack | not established | decide | |
| `ChangeSetType`, `ResourcesToImport`, `IncludeNestedStacks` | not established | decide | |

**Template sections and attributes**

| Part | Tier now | Note |
|---|---|---|
| `Parameters`: `Type`, `Default`, `NoEcho`, `Description` | F | `NoEcho` is not masked (CFN-10). |
| `AllowedValues`, `MinValue`, `MaxValue`, `MinLength`, `MaxLength`, `AllowedPattern`, `ConstraintDescription` | F | `CheckParameters`; run for two. A pattern RE2 cannot compile is let through. |
| Parameter types: `String`, `Number`, `CommaDelimitedList`, `List<…>` | F | Coerced on both paths. |
| AWS-specific and SSM parameter types (`AWS::EC2::…`, `AWS::SSM::Parameter::Value<…>`) | not established | `ignoredTypes` carries `AWS::SSM::Parameter::Value` as if it were a resource type. |
| `Mappings`, `Conditions` | F | Conditions evaluated to a fixed point. |
| `Outputs`, `Export.Name` | F | A conflicting export is refused at the call. |
| `Transform: AWS::Serverless-2016-10-31`, `Globals.Function` | F | Any other transform is refused (read). |
| `Rules`, `Metadata` (template level), `Hooks` | not established | Not parsed by `template.go`. |
| `DeletionPolicy: Retain`, `RetainExceptOnCreate` | F | Run for Retain on delete and on rollback. |
| `DeletionPolicy: Snapshot`, `Delete` | not established | Only the two Retain spellings are read. |
| `UpdateReplacePolicy`, `UpdatePolicy`, `CreationPolicy` | not established | Not parsed. |
| `DependsOn` | C | Linted for dangling names; not used for ordering (CFN-22). |
| `Condition` on a resource | F | A false condition is reported as ignored. |

**Intrinsic functions and pseudo parameters**

| Item | Tier now | Note |
|---|---|---|
| `Ref`, `Fn::GetAtt`, `Fn::Sub`, `Fn::Join`, `Fn::Select`, `Fn::Split`, `Fn::FindInMap`, `Fn::If`, `Fn::Equals`, `Fn::And`, `Fn::Or`, `Fn::Not`, `Fn::Base64`, `Fn::ImportValue`, `Fn::ToJsonString`, `Fn::Length`, `Condition` | F | `Scope.call` in `intrinsics.go`. |
| `Fn::GetAZs` | C | Always one zone, `<region>a`. |
| `Fn::Cidr`, `Fn::Transform`, `Fn::ForEach` | S | "unsupported intrinsic"; fails the deploy. `!Transform` is in the short-tag table. |
| `AWS::Region`, `AWS::AccountId`, `AWS::StackName`, `AWS::NoValue` | F | |
| `AWS::Partition`, `AWS::URLSuffix` | C | Constants `aws` and `amazonaws.com`, whatever the instance's suffix. |
| `AWS::StackId` | wrong | Always ends `/local`, not the stack's id (CFN-20). |
| `AWS::NotificationARNs` | not established | |
| `Fn::GetAtt` attributes per type | not established | `attributes()` in `resources.go`; no table of which attributes each type answers. |

**Resource types**

`classify` in `internal/cfn/resources.go` puts every type in one of three
outcomes. The property column is from `docs/cloudformation.md` and the mappers;
no test asserts it per property, so each cell is `read` until section 5 is done.
A property not named is dropped without a word (CFN-5).

*Mapped: 56 types (`nameProperty`). The stack owns them; they appear under Resources.*

| Type | What is made | Properties applied (read) |
|---|---|---|
| `AWS::SQS::Queue` | a queue | `QueueName`, `FifoQueue`, `ContentBasedDeduplication`, `VisibilityTimeout`, `DelaySeconds`, `MessageRetentionPeriod`, `ReceiveMessageWaitTimeSeconds`, `MaximumMessageSize`, `RedrivePolicy`, `Tags`. Dropped: `KmsMasterKeyId` (run), the rest not established. |
| `AWS::SQS::QueuePolicy` | **nothing** (CFN-4) | none |
| `AWS::SNS::Topic` | a topic | `TopicName`, `Subscription`, `Tags`. FIFO and the rest: not established. |
| `AWS::SNS::Subscription` | a subscription | Attached after all resources. Filter policy, raw delivery: not established. |
| `AWS::SNS::TopicPolicy` | **nothing** (CFN-4) | none |
| `AWS::S3::Bucket` | a bucket | versioning, object lock, CORS, lifecycle, website, notifications, `PublicAccessBlockConfiguration`, `OwnershipControls` |
| `AWS::S3::BucketPolicy` | the bucket's policy | `Bucket`, `PolicyDocument` |
| `AWS::DynamoDB::Table`, `::GlobalTable` | a table | key schema, GSIs, LSIs, TTL, deletion protection, `Tags`. Streams, billing mode, PITR: not established. |
| `AWS::Serverless::SimpleTable` | a table | `PrimaryKey`, `TableName`, `Tags` |
| `AWS::Lambda::Function`, `AWS::Serverless::Function` | a function | runtime, handler, code (three spellings), environment, timeout, memory, DLQ, `Layers`, `Tags`; SAM `Events`, `AutoPublishAlias`, `FunctionUrlConfig` |
| `AWS::Lambda::EventSourceMapping` | a trigger | SQS sources. Other sources and the batch settings: not established. |
| `AWS::Lambda::LayerVersion` | a layer version | `Content`, `CompatibleRuntimes`, `Description` |
| `AWS::Lambda::Version`, `::Alias`, `::Url` | a version, an alias, a function URL | as in `docs/cloudformation.md` |
| `AWS::Lambda::Permission` | **nothing** (CFN-4) | none |
| `AWS::Events::Rule` | a rule | pattern, schedule, state, targets, input shaping, API destination targets |
| `AWS::Events::EventBus` | **nothing** (CFN-3) | none |
| `AWS::Events::Connection`, `::ApiDestination` | a connection, a destination | as in `docs/cloudformation.md` |
| `AWS::KMS::Key`, `::Alias` | a key, renamed by its alias | Key policy, rotation, key spec: not established. |
| `AWS::SecretsManager::Secret` | a secret | `Name`, `SecretString`, `GenerateSecretString` as a placeholder. Not updated once it exists (CFN-1). |
| `AWS::SSM::Parameter` | a parameter | `Name`, `Type`, `Value`, `Description`, `Tags`. Not updated once it exists (CFN-1). |
| `AWS::Kinesis::Stream` | **nothing** (CFN-2) | none |
| `AWS::StepFunctions::StateMachine`, `AWS::Serverless::StateMachine` | a state machine | definition, substitutions, type, role, tags, logging. `DefinitionUri` refused. |
| `AWS::StepFunctions::Activity`, `::StateMachineVersion`, `::StateMachineAlias` | each of those | as in `docs/cloudformation.md` |
| `AWS::Logs::LogGroup` | a log group | `LogGroupName`, `RetentionInDays`, `Tags` |
| `AWS::Logs::SubscriptionFilter`, `::MetricFilter` | a filter | as in `docs/cloudformation.md` |
| `AWS::CloudWatch::Alarm`, `::Dashboard` | an alarm, a dashboard | Per property: not established. |
| `AWS::ApiGateway::RestApi`, `AWS::Serverless::Api` | a REST API | routes from functions; SAM `Auth` |
| `AWS::ApiGateway::Resource`, `::Method`, `::Stage`, `::Authorizer`, `::ApiKey`, `::UsagePlan`, `::UsagePlanKey` | the resource tree, stage settings, authorizers, keys and plans | as in `docs/cloudformation.md` |
| `AWS::ApiGateway::Deployment`, `::Account` | nothing of their own | A deployment happens on every apply. Says so in the docs; not in the console. |
| `AWS::ApiGatewayV2::Api`, `AWS::Serverless::HttpApi` | an HTTP API | `ProtocolType` HTTP only, CORS, quick-create `Target` |
| `AWS::ApiGatewayV2::Integration`, `::Route`, `::Stage`, `::Authorizer`, `::Deployment` | each of those | JWT, VPC links, service integrations refused by name |
| `AWS::CloudFormation::Stack` | a child stack record, its resources merged into the parent's graph | `TemplateURL`, `Parameters` |

*Ignored: 13 types (`ignoredTypes`). Accepted, referenceable, not made, and not listed under Resources (CFN-16).*

| Type | Reason given |
|---|---|
| `AWS::IAM::Role` | no IAM evaluation during apply; create the role via the iam service if you need it |
| `AWS::IAM::Policy`, `::ManagedPolicy`, `::User`, `::Group`, `::ServiceLinkedRole` | no IAM evaluation during apply |
| `AWS::IAM::InstanceProfile` | no EC2 locally |
| `AWS::Logs::LogStream` | a stream is created by the first PutLogEvents |
| `AWS::CDK::Metadata` | CDK bookkeeping |
| `AWS::ECR::Repository` | no container registry locally |
| `AWS::CloudFormation::WaitCondition`, `::WaitConditionHandle` | nothing to wait for locally |
| `AWS::SSM::Parameter::Value` | "a parameter reference, not a resource" (it is not a resource type; see above) |

doze-aws serves IAM. Whether `AWS::IAM::Role` and its siblings should now be
mapped onto it is a decision this stage makes (section 10).

*Refused: everything else. The whole template fails; nothing is made.*

| Type | Message |
|---|---|
| `AWS::Serverless::Application` | `resource <id>: nested applications need the Serverless Application Repository (<type>)` |
| any other `AWS::<Service>::…` | `resource <id>: doze-aws does not serve <Service> (<type>)` (run, ECS) |
| anything else | `resource <id>: unsupported resource type (<type>)` |
| `AWS::Serverless::LayerVersion` | handled by the SAM transform according to the docs; where is not established |

Only the first refused resource is named.

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests, `cloudformation/` | 50 tests in 25 files: service, rollback (7), apply, change-set property diff, export conflict, nested, declared fields, ids, and one apply test per deployment shape (Step Functions, API Gateway authorizers, keys, HTTP APIs, Lambda versioning, CloudWatch, Logs, S3 access, EventBridge HTTP) |
| Package tests, `internal/cfn/` | 46 tests in 11 files: transpile (15), intrinsics (11), template (7), regression (4), nested, GetAtt hosts, parameters, lint |
| Package tests, `internal/provision/` | 9 tests in 5 files: destroy (5), subset (2), existing, inline |
| Parity | `rejection_parity_test.go` (2), `coverage_model_test.go`; ledger says 182 of 182 model constraints across 22 of 23 operations, `knownGaps` empty |
| Console handler | `internal/console/cfn_rollback_test.go`: 1 test |
| Conformance | `conformance/tests/test_cloudformation.py`: 9 scenarios, all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/cloudformation.spec.ts` (6) and `cfn-starter.spec.ts` (2): 8 tests |
| Deploy smokes | `e2e/deploy/`: `sam`, `cdk`, `serverless`, `terraform`, each a `smoke.sh`, run by `run.sh` |
| Mutation | No per-package baseline (foundation B3) |

What the counts hide: there is no test of an update that changes a property of
each mapped type, none of a parameter-only update reaching a resource, none of
`NoEcho`, none of delete with a deleted importer, and the one browser test of
delete is titled "what it created goes with it".

## 4. Findings

Evidence `run` means reproduced on 2026-10-10 against a binary built from this
tree, through the console's endpoints or the wire. Owned elsewhere and not
repeated: the queue-and-DLQ ordering bug is **SQS-1**; the refusal "why" line is
**A2**; the header-button refusal falling to a toast is **A6**; the unreachable
and stale `copy.go` lines are **A7**; the delete confirm's "Everything it
created goes with it" is **A13**.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| CFN-1 | P0 | An update that changes an SSM parameter's `Value` reports UPDATE_COMPLETE and changes nothing: the value and `Version` stay. `applyParameters` skips an existing parameter unless `Force`, and no mapper sets `Force`. `applySecrets` has the same branch. | `internal/provision/svc_ssm.go` `applyParameters`; `svc_secrets.go` `applySecrets`; `internal/cfn/mapper.go` | run (SSM), read (Secrets) | doc: an update to `Value` updates the parameter | Stack test: create with `Value: a`, update to `b`, read SSM. Same for a secret. |
| CFN-2 | P0 | `AWS::Kinesis::Stream` is mapped, reported CREATE_COMPLETE with a physical id, and no stream is made. | `internal/cfn/mapper.go`, the `AWS::Kinesis::Stream` case | run | n/a | Stack test: deploy a stream, `DescribeStream` |
| CFN-3 | P0 | `AWS::Events::EventBus` is mapped, CREATE_COMPLETE, and no bus is made. | `mapper.go`, the `AWS::Events::EventBus` case | run (the console's bus list) | n/a | Stack test: deploy a bus and a rule on it, `DescribeEventBus` |
| CFN-4 | P0 | `AWS::SQS::QueuePolicy`, `AWS::SNS::TopicPolicy` and `AWS::Lambda::Permission` are mapped and dropped. A queue policy is evaluated under soft and enforce, so a template that grants a topic `SendMessage` deploys clean and is denied at run time. A policy naming a queue that does not exist is CREATE_COMPLETE. | `mapper.go`, the shared `return nil` case | run (queue policy on a missing queue), read | n/a | Stack test under enforce: topic, queue, queue policy, publish, receive |
| CFN-5 | P0 | A property a mapper does not read is dropped silently. `NotAProperty: 7` on a queue deploys; `KmsMasterKeyId` is dropped. | every `m.<kind>` in `internal/cfn/mapper*.go` | run | memory: AWS refuses an unknown property | Transpile test per type: an unknown property fails; a known-unapplied one is reported |
| CFN-6 | P0 | A create refused in transpile leaves a CREATE_FAILED record. Reproduced for a parameter with no value: the corrected resubmission answers `AlreadyExistsException` until the record is deleted. The same happens for an unserved type, which is ours to define. | `cloudformation/stacks.go` `deploy`, `recordFailure` | run | doc: a missing parameter is a ValidationError and no stack exists | Handler test: refused create, then `DescribeStacks` is not found and a second create succeeds. `TestServiceBadTemplateRecordsFailure` asserts the opposite and changes with the decision. |
| CFN-7 | P0 | A deleted stack still counts as an importer. After deleting the importer, deleting the exporter is refused "in use by <deleted stack>", for good. `ListImports` lists it too. | `stacks.go` `importerOf`, `hListImports` | run | doc | Handler test: exporter, importer, delete importer, delete exporter |
| CFN-8 | P0 | `DeleteStack` answers DELETE_COMPLETE when `Destroy` failed, and when the stored template no longer transpiles. Both are only logged; the resources stay, owned by nothing. | `stacks.go` `hDeleteStack` | read | doc: DELETE_FAILED | Handler test with a gateway that fails one delete |
| CFN-9 | P1 | `ValidateTemplate` calls a template with an unserved type valid; `CreateStack` then refuses it. It parses and checks references only. | `stacks.go` `hValidateTemplate` | run | n/a (the limit is ours) | Handler test: validate a template with `AWS::ECS::Cluster` |
| CFN-10 | P1 | A `NoEcho` parameter's value is returned in clear by `DescribeStacks` and `DescribeChangeSet`. | `stacks.go` `viewStack`; `changesets.go` `hDescribeChangeSet` | run | doc: masked as asterisks | SDK test |
| CFN-11 | P1 | A stack records only the parameters it was given. One that took its default is absent from `DescribeStacks`. | `stacks.go` `deploy` (`Parameters: params`) | run | doc | SDK test: create with no parameters, read `Parameters` |
| CFN-12 | P1 | In a failed create, a Retain resource gets DELETE_IN_PROGRESS and DELETE_COMPLETE events and that status under Resources while it still exists. A resource that failed because its name was taken, and was never made, is also reported DELETE_COMPLETE. | `cloudformation/rollback.go` `undoCreate` | run (both) | doc: DELETE_SKIPPED exists as a status | Rollback test asserts the trail for a Retain bucket |
| CFN-13 | P1 | A change set for a parameter-only change is empty (FAILED, "didn't contain changes") unless the parameter changes a physical name. The diff fingerprints template text. | `cloudformation/changesets.go` `diffChanges`; `internal/cfn/resources.go` `fingerprint` | run | doc | `propdiff_test.go`: a parameter feeding a property shows Modify |
| CFN-14 | P1 | `ExecuteChangeSet` calls `deploy` without `refuseUpdate`, so a change set updates a ROLLBACK_COMPLETE or CREATE_FAILED stack. A change set made before a later update still executes, with its stale template and parameters. | `changesets.go` `hExecuteChangeSet` | read (first), run (second) | doc for the second: other change sets are removed when the stack changes | Handler tests for both |
| CFN-15 | P1 | `CreateChangeSet` does not run `CheckReferences` or `CheckParameters`. A value outside `AllowedValues` makes an AVAILABLE change set that fails at execute. | `changesets.go` `hCreateChangeSet` | read | doc | Handler test |
| CFN-16 | P1 | Ignored types are not in `DescribeStackResources`, `ListStackResources` or the events. A template's IAM role does not exist as far as the stack says. | `stacks.go` `deploy`, the `e.Kind != cfn.Mapped` skip | run | doc | SDK test: a role is listed |
| CFN-17 | P1 | On an update every resource becomes UPDATE_COMPLETE and gets a pair of events, changed or not. A resource the update adds and fails on is UPDATE_FAILED, not CREATE_FAILED. Every resource's timestamp is the stack's. | `stacks.go` `deploy`; `deploy_support.go` `synthesizeEvents`; `rollback.go` `markFailed` | run | memory | Event-trail test for a one-property update |
| CFN-18 | P1 | A failed resource's reason is the internal wrapper and the raw wire body: `stackfile: parameters: parameter "…": aws 400: {"Message":…}`. | `internal/provision/apply.go` `Apply`; `rollback.go` `markFailed` | run | n/a | Rollback test asserts the reason is the peer's code and message |
| CFN-19 | P1 | A template with IAM resources deploys with no `Capabilities`. | `stacks.go` `hCreateStack`, `hUpdateStack`, `hCreateChangeSet` | run | doc: InsufficientCapabilitiesException | Rejection test, written and skipped until recorded |
| CFN-20 | P1 | `DescribeStacks` never returns `Description`. `DescribeChangeSet` and `ListChangeSets` build `StackId` with an empty id instead of the stack's. `AWS::StackId` always ends `/local`. | `stacks.go` `viewStack`; `changesets.go`; `internal/cfn/intrinsics.go` `pseudo` | read | doc | SDK test for each |
| CFN-21 | P1 | Importer detection is a substring search: a stack whose template contains the export's name and the word `ImportValue` anywhere counts. | `stacks.go` `importerOf`, `hListImports` | read | n/a | Handler test with the name in a comment |
| CFN-22 | P1 | Apply runs in fixed phases by kind, by name inside a phase. `DependsOn` and references do not order anything. SQS-1 is one case; any reference from an earlier phase to a later one is another. Which pairs fail is not established. | `internal/provision/apply.go` `Apply` | read | n/a | A table test: for each reference a mapper emits, the target is applied first |
| CFN-23 | P1 | For an existing resource, apply reports "skipped: exists" for buckets, tables, topics, keys, log groups, REST and HTTP APIs, and a queue with no attributes. Which property edits an update applies, per type, is not established. | `internal/provision/svc_*.go` | read | n/a | Per type: update one property of each kind, read it back |
| CFN-24 | P1 | The 67 refused operations answer `ValidationError`. The common criterion asks for the code AWS uses for an unsupported operation; nobody has decided what that is here. | `cloudformation/service.go` `ServeHTTP` | run | n/a | After the decision, one test over `stubActions` |
| CFN-25 | P1 | No list operation paginates. `NextToken` passes validation and is ignored on `DescribeStacks`, `ListStacks`, `DescribeStackEvents`, `ListStackResources`, `ListChangeSets`, `ListExports`, `ListImports`. | `stacks.go`, `changesets.go` | read | doc | SDK paginator test past one page of events |
| CFN-26 | P1 | `UpdateStack` on a REVIEW_IN_PROGRESS stack is not refused. | `stacks.go` `refuseUpdate` | read | memory | Handler test, skipped until recorded |
| CFN-27 | P2 | Validate and Load parameters show a rejection as `aws 400: <?xml …<ErrorResponse>…`. Both use `err.Error()` where every other partial uses `errText`. | `internal/console/handlers_cfn.go` `cfnValidate`, `cfnSummary` | run | n/a | Handler test: the partial holds the message and no `<` |
| CFN-28 | P2 | The Parameters panel says "The template declares no parameters" and the Update form "The deployed template declares no parameters" when the template declares some that took defaults. | `templates/cfn.html`, `cfn_stack` | run | n/a | Browser test. Closes with CFN-11 or by reading the summary. |
| CFN-29 | P2 | A `NoEcho` value is shown in the Parameters panel and prefilled in a plain input on the Update form. The create form prints a `NoEcho` default in the placeholder of its password input. | `cfn.html`, `cfn_stack` and `cfn_params` | run | n/a | Browser test |
| CFN-30 | P2 | The toast says "Change set created — review the diff, then execute it" for an empty FAILED set. | `handlers_cfn.go` `cfnCreate`, `cfnUpdate` | run | n/a | Handler test on the flash |
| CFN-31 | P2 | Deleted stacks are rendered only when the list is not empty, so deleting the last stack hides the record of it. | `cfn.html`, `cfn_home` | read | n/a | Handler test |
| CFN-32 | P2 | The change-set table prints "in place" for Add and Remove rows. | `cfn.html`, the Replacement cell | run | n/a | Handler test |
| CFN-33 | P2 | The empty Resources row says "A stack in REVIEW_IN_PROGRESS has a change set waiting" for CREATE_FAILED stacks too. | `cfn.html`, `cfn_stack` | run | n/a | Handler test |
| CFN-34 | P2 | Load parameters prints "Creates:" followed by every declared type, including ignored and refused ones. | `cfn.html`, `cfn_params`; `stacks.go` `hGetTemplateSummary` | run | n/a | Handler test with an IAM role |
| CFN-35 | P2 | Resources links every physical id whose type has a console page, including rows in DELETE_COMPLETE and resources that were never made (CFN-2). | `internal/console/client_cfn.go` `resourceLink` | run | n/a | Handler test on a rolled-back stack |
| CFN-36 | P2 | The console cannot send what the API takes: `Capabilities`, `Tags`, `DisableRollback`, `OnFailure`, `TemplateURL`, termination protection, a stack policy. Termination protection is shown and cannot be changed. The Update form has no CLI preview. | `handlers_cfn.go` `formParams`, `cfnCreate`, `cfnUpdate` | read | n/a | Decide which belong in the form; a browser test per one added |
| CFN-37 | P3 | Stale comments and ledger: "nothing local acts on" termination protection (`client_cfn.go` `Stack`, `store.go` `stackRecord`); the ledger tiers `UpdateTerminationProtection` C; `store.go`'s header says "no resource-level rollback"; `Fn::ImportValue`'s error says "doze-aws has no cross-stack export registry". | as named | read | n/a | Ledger test after the tier change |
| CFN-38 | P3 | `docs/cloudformation.md` lists `AWS::CloudWatch::*`, log groups and alarms as skipped; they are mapped. | `docs/cloudformation.md`, "The three outcomes" and "Resource types" | read | n/a | A doc test that the three lists equal `nameProperty`, `ignoredTypes` and the rest |
| CFN-39 | P3 | The Validate verdict says "Rejected" where every other refusal says "Refused". The list pane's number is the count of outputs, unlabelled. | `cfn.html`, `cfn_validate_result`, `cfn_lp` | read | n/a | With the foundation's vocabulary test |

**IAM scope, decided (D6).** CloudFormation is evaluated by IAM, in two parts that land in this stage.
(1) The control plane: every stack operation is evaluated and recorded as
`cloudformation:<Operation>` on the stack ARN. (2) What a stack does on the
caller's behalf: each call `provision` makes for a stack is evaluated against
the caller (or the stack's `RoleARN` when one is given), so creating a stack
with a queue needs `sqs:CreateQueue`, and a denial fails that resource with
the service's message and rolls the stack back, as on AWS. Those calls are
recorded against the caller, so a policy generated from a deploy covers the
whole deploy. `doze-aws apply` is not a stack and keeps acting as the root.
Acceptance: under enforce, a principal allowed `cloudformation:*` and nothing
else cannot create a stack with a queue; with `sqs:CreateQueue` added it can;
a soft-mode deploy generates a policy that then passes under enforce. How AWS
attributes the downstream call (`aws:CalledVia`) is a `doc` claim on the
recording queue. Adds about two days to this stage.

## 5. What to check

Beyond the common criteria. Query protocol only.

**Templates**
- [ ] JSON and YAML, every short tag in `template.go`'s table, both `!GetAtt` spellings.
- [ ] Each intrinsic in section 2: its argument shapes, and its error for a wrong one.
- [ ] An intrinsic that cannot resolve fails the deploy; none yields an empty string.
- [ ] `Fn::GetAtt`: a table of attributes per mapped type, each tested; an
      unknown attribute fails.
- [ ] `Ref` per mapped type follows AWS's rule for that type (URL, ARN or name).
- [ ] Every pseudo parameter, under a non-default account and region.
- [ ] Conditions: fixed point, an undefined name, a cycle.
- [ ] Parameters: each constraint and its message; each type's coercion;
      a missing value; a supplied name the template does not declare.
- [ ] Establish and tier: `Rules`, `UpdateReplacePolicy`, `DeletionPolicy:
      Snapshot`, AWS-specific parameter types, `AWS::NotificationARNs`.
- [ ] A reference to nothing is refused at validate, create, update and
      change-set creation alike, and leaves no stack.

**Resource types**
- [ ] For each of the 56 mapped types: every property is in exactly one of
      applied, cosmetic and said so, or refused. The table is checked in and a
      test fails when a mapper reads a property the table does not list.
- [ ] For each mapped type: create, update one property, drop from the
      template, each read back from the owning service.
- [ ] No mapped type reports COMPLETE with nothing made (CFN-2, CFN-3, CFN-4).
- [ ] Each ignored type appears in the stack's resources with a reason.
- [ ] Each refused type's message names the type; all refused types in a
      template are named, or the message says it names the first.
- [ ] Physical names: logical id when unnamed, sanitised per service; an
      explicit name never rewritten.
- [ ] Decide whether `AWS::IAM::*` is mapped now that IAM is served.

**Create**
- [ ] A refused create leaves no stack (CFN-6).
- [ ] A name already taken outside the stack fails that resource and rolls
      back; the existing resource is unchanged and survives the stack's delete.
- [ ] `DisableRollback`, `OnFailure=DO_NOTHING`, `OnFailure=DELETE`.
- [ ] A duplicate stack name; create over a DELETE_COMPLETE and over a
      REVIEW_IN_PROGRESS record.
- [ ] An export name another stack owns is refused before anything is made.
- [ ] `Capabilities` (CFN-19), `Tags` and where they go, `EnableTerminationProtection`.

**Update**
- [ ] No change is refused with "No updates are to be performed."
- [ ] A parameter-only update reaches every resource the parameter feeds (CFN-1).
- [ ] A dropped resource is deleted; a dropped Retain resource is kept.
- [ ] A changed physical name replaces the resource; what happens to the old
      one, with and without Retain, is established.
- [ ] A failed update restores template, parameters, resources and outputs;
      what it created is deleted; the previous template is re-applied.
- [ ] The three states an update is refused from, and REVIEW_IN_PROGRESS (CFN-26).
- [ ] Untouched resources keep their status and emit no event (CFN-17).

**Delete**
- [ ] Reclaims what the stack made; keeps Retain; keeps what it never made.
- [ ] A ROLLBACK_COMPLETE stack's delete touches nothing.
- [ ] Refused under termination protection, for a nested child, and while a
      live stack imports an export. Not refused by a deleted importer (CFN-7).
- [ ] A delete that cannot remove a resource says so (CFN-8).
- [ ] Idempotent for a missing stack. The record answers by id, not by name.

**Events and resources**
- [ ] The trail for create, update, delete and each rollback, in order, ending
      on a terminal stack event, with unique event ids.
- [ ] A failed resource's reason is readable (CFN-18).
- [ ] The 500-event bound keeps the newest.

**Change sets**
- [ ] CREATE makes a REVIEW_IN_PROGRESS stack; UPDATE on a missing stack is refused.
- [ ] Add, Modify, Modify with replacement, Remove; a property edit; a
      parameter edit (CFN-13).
- [ ] The empty set's exact status reason.
- [ ] Execute: AVAILABLE only; the outcome on the set when the stack rolls
      back; a second execute; a stale set (CFN-14).
- [ ] Validation at creation (CFN-15).

**Exports and nested stacks**
- [ ] `ListExports`, `ListImports` by real import, not substring (CFN-21).
- [ ] Nested: five deep, a cycle refused, child outputs, child names, parent
      delete marks children, child delete refused, a dropped child's record removed.

**Provision, shared with the CLI**
- [ ] Apply twice is a no-op. `Existing` knows every kind `Apply` makes.
- [ ] `Destroy` removes every kind `Apply` makes, and reports what it could not.
- [ ] `Export` then `Emit` then `Parse` then `Transpile` for every kind;
      secret and SecureString values are blank.
- [ ] Order: every reference a mapper can emit has its target applied first (CFN-22).

**Lifecycle**
- [ ] Stacks, change sets, events and nested bodies survive a restart.
- [ ] Each deploy smoke under `e2e/deploy` passes; each is widened in stage 17.

## 6. Console

The design's "CloudFormation" page: 21 boards. Templates:
`internal/console/templates/cfn.html` (`cfn_lp`, `cfn_home`, `cfn_stack`,
`cfn_validate_result`, `cfn_params`, `cfn_resource_detail`) and the `cfn_create`
block in `create.html`. Handlers: `handlers_cfn.go`; client `client_cfn.go` and
`client_cfn_export.go`; routes `cfnRoutes` in `routes_stackwide.go`; the
"Start from what's running" script is `dozeCfnStarter` in `static/editor.js`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Cfn-Home | Stacks, exports, deleted stacks | `cfn_home`, `cfnStacks` | exists |
| Cfn-01-None | No stacks yet | `cfn_home`, `empty_detail` | exists; the button is **new** |
| Cfn-02-Create | Create stack | `cfn_create` | form exists; the strip and field lines are **new** |
| Cfn-03-Create-Loaded | Validated, parameters answered, CLI open | `cfn_create`, `cfn_validate_result`, `cfn_params` | exists; the strip, the export check and the limits line are **new** |
| Cfn-04-Create-Refused | Create refused | `fail_inline` in `#cfn-validate-out` | exists |
| Cfn-05-Export | Start from what's running | `cfn_create`, `cfnExportTemplate`, `dozeCfnStarter` | exists |
| Cfn-06-Resources | Resources, parameters, outputs | `cfn_stack` default tab | exists; Retain pill and the role note are **new** |
| Cfn-07-Resource-Open | One resource opened | `cfn_resource_detail`, `cfnResource` | exists |
| Cfn-08-Events | Events | `cfn_stack`, `tab=events` | exists |
| Cfn-09-Template | Template | `cfn_stack`, `tab=template` | exists; the two notes are **new**; two columns is layout only |
| Cfn-10-Review-Pending | A stack awaiting review | `cfn_stack`, `tab=changesets` | exists; the callout is **new** |
| Cfn-11-Update | Update stack | `cfn_stack`, `tab=update`, `cfnUpdate` | form exists; full parameter list and the strip are **new** |
| Cfn-12-Update-Review | Update as a change set | same | exists; "What the diff will show" is **new** |
| Cfn-13-Update-Refused | Update refused | `fail_inline` at the form's end | exists |
| Cfn-14-ChangeSets | Change sets and one diff | `cfn_stack`, `tab=changesets&cs=` | exists |
| Cfn-15-Confirms | Four confirms | `hx-confirm` on Delete, Execute, Delete change set | exist; the Retain and rolled-back sentences and the counts are **new** |
| Cfn-16-Delete-Refused | Delete refused | `fail_inline` under the header | needs A6; today a toast |
| Cfn-17-Rollback-Create | A create that was undone | `cfn_stack`, `tab=events` | trail exists; the callout is **new** |
| Cfn-18-Rollback-Retain | A rollback that kept a bucket | same | trail exists; the callout and Retain pill are **new** |
| Cfn-19-Rollback-Update | An update that was undone | same | trail exists; the callout is **new** |
| Cfn-20-Left-Behind | A refused create's leftover | same | exists; the callout is **new**. The board goes when CFN-6 is fixed. |

There is no Denied board. The console acts as root and a stack has no resource
policy, so no stack action can be denied in its own right. A peer's denial
during apply is a failed resource, not a denied call; that is section 9.

**New items, and what each is built from**

| Item | Built from |
|---|---|
| "Create a stack" on the empty pane | `copy.go`'s `CTA` and `CTAPath`, unset for `cfn` (A7) |
| What this deploy will do: made, accepted, refused | `GetTemplateSummary`'s `ResourceTypes` against `cfn.IsMappable`, `ignoredTypes` and `classify`. `classify` and `ignoredTypes` are unexported: an exported classifier is needed. |
| Export names and "no other stack claims either" | the template's `Outputs` (`cfn.Parse`) against `ListExports` |
| The limits line under a parameter | `cfn.Parse`'s `Parameter` (AllowedValues, Min, Max, pattern). `GetTemplateSummary` here does not return them. |
| Every declared parameter on Update | `backend.TemplateSummary(ctx, "", stack)`, which exists and is unused for this |
| What an update does; what the diff will show | Static text. Each sentence is a section 5 box. |
| Retain pill, "Kept", the role note, the Template notes | the stored template (`GetTemplate`, `cfn.Parse`): `DeletionPolicy` and the type's class |
| Counts in the Execute confirm | the change list already fetched for the tab |
| The rolled-back stack's delete sentence | the stack's status |
| Rollback and leftover callouts | the stack's status, and the reason on its ROLLBACK_IN_PROGRESS or failed event |

**Teaching lines, and where their facts come from**

| Line | Source |
|---|---|
| Stack name: a letter, then letters, digits, hyphens, up to 128 | `stackName` in `stacks.go`; the model's pattern |
| No stack-name prefix, no random suffix | `SUPPORT.md`, "Physical names are logical IDs" |
| A SAM template is understood; any other Transform is refused | `Transpile`, read (section 6, not yet backed) |
| A blank input takes the default (create) or keeps the value (update) | `formParams`, `hUpdateStack`'s merge |
| A number, enforced; one of the allowed values, enforced | `Parameter.check` |
| Nothing deploys until you execute; never CREATE_IN_PROGRESS | `StatusReviewInProgress`; `SUPPORT.md`, "Everything is synchronous" |
| Read whole, made, if one fails, recorded | `deploy`, `refuseTaken`, `settleFailure`, `refuseExportConflict` |
| Retain is kept by a rollback, a dropping update and a delete | `retained` in `rollback.go`; run for rollback and delete |
| A parameter on its own may show nothing | CFN-13. The line is removed when CFN-13 is fixed. |
| The stack records only the values it was given | CFN-11. Removed when fixed. |
| A stack keeps its latest 500 events | `boundEvents` |

The parameter lines are not in the foundation's settings table (B4): a
template's parameters are the user's, not the service model's. This stage adds
a second source to that accessor, or builds these lines beside it.

**Statements on the boards not yet backed.** Remove or prove each.

| Board | Statement | State |
|---|---|---|
| Cfn-02 | Any other Transform is refused | read, not run |
| Cfn-04 | The export-conflict message | read; `export_conflict_test.go` holds the behaviour, the text not checked |
| Cfn-05 | Secret and SecureString values are left blank | docs, not run |
| Cfn-05 | A name in the exported template that is already running fails that resource | follows from taken names; not run end to end |
| Cfn-12 | `console-52219` | an example; the real default is `console-` and the time mod 100000 |
| Cfn-14 | "what the AWS CLI reads as nothing to deploy" | doc claim, section 8 |
| Cfn-16 | The nested-stack refusal's text and ARN | read, not run |
| Cfn-16 | Deleting the parent removes the child's resources | read |
| Cfn-17, Cfn-19 | ROLLBACK_FAILED names the resources; UPDATE_ROLLBACK_FAILED says why | read |
| Cfn-04, Cfn-20 | "AWS would take this template" for an ECS cluster | true by definition of the limit; the callout's colour depends on A2's "Refused here" |
| Cfn-08 | "no change means apply found the resource as the template describes it" | read; false for a parameter or secret that was skipped (CFN-1) |
| Cfn-06, Cfn-18 | Resource rows show DELETE_COMPLETE for what still exists | true today; the boards change with CFN-12 |

## 7. Tests to add

| Layer | Tests |
|---|---|
| Transpile (`internal/cfn`) | Unknown and unapplied properties per type (CFN-5); the property table against the mappers; every `GetAtt` attribute; every pseudo parameter; `AWS::StackId` (CFN-20) |
| Provision | Update of each kind (CFN-1, CFN-23); order by reference (CFN-22, SQS-1); a destroy that fails; `Existing` for every kind |
| Stack handler | CFN-2, 3, 4 (something is made); CFN-6, 7, 8, 9, 14, 15, 21, 26; event trails (CFN-12, 17, 18) |
| SDK v2 | CFN-10, 11, 16, 20, 25; `Capabilities` (CFN-19) |
| Rejection parity | The 182 cases stay; add the stub code once CFN-24 is decided |
| Restart | A stack with a change set, a nested stack, and a rolled-back stack |
| Console handler | CFN-27 to CFN-35; every `hx-confirm` in `cfn.html` read against the code |
| Conformance (boto3) | Every row in section 8; parameter constraints; `NoEcho`; change-set shapes; delete with Retain; exports and imports |
| Browser | Each of the 21 boards; each `new` item; refusals inline at the three places they land; keyboard on the list and both forms |
| Deploy smokes | Unchanged here; widened in stage 17 |
| Mutation | Baselines for `cloudformation/`, `internal/cfn/`, `internal/provision/` |

## 8. Recording queue

Every one of these is `doc` or `memory` today.

| Claim | Source now | Scenario |
|---|---|---|
| A `NoEcho` value reads back as asterisks | doc | `noecho_masked` |
| `DescribeStacks` returns defaulted parameters | doc | `parameters_include_defaults` |
| A missing parameter is refused and no stack exists; its message | doc, message memory | `missing_parameter` |
| A supplied parameter the template does not declare; its message | memory | `undeclared_parameter` |
| Each parameter-constraint message | memory | `parameter_constraints` |
| An unknown resource property is refused, and when | memory | `unknown_property` |
| IAM resources without `Capabilities` is InsufficientCapabilitiesException | doc | `capabilities_required` |
| A Retain resource in a rollback is DELETE_SKIPPED | doc | `rollback_retain_trail` |
| A taken name's reason text, per type | doc | `taken_name` (exists, unverified) |
| The trail of a one-property update: which resources emit events | memory | `update_trail` |
| A resource added by an update that fails is CREATE_FAILED | memory | `update_adds_failing_resource` |
| The ROLLBACK_IN_PROGRESS reason, "Rollback requested by user." | memory | `rollback_reason` |
| A parameter-only change set lists the resources it changes | doc | `changeset_parameter_only` |
| The empty change set's status and reason | doc | `changeset_empty` (in `test_change_set`, unverified) |
| Other change sets are removed when the stack is updated | doc | `changeset_stale` |
| Updating a ROLLBACK_COMPLETE stack's message | doc | `update_rolled_back` |
| Updating a REVIEW_IN_PROGRESS stack | memory | `update_in_review` |
| "No updates are to be performed." | doc | `update_no_change` |
| Deleting an exporter with a live importer; its message | doc | `delete_exporter` |
| A delete that cannot remove a resource is DELETE_FAILED | doc | `delete_failed` |
| `Description`, and `StackId` on a change set | doc | `describe_fields` |
| What `AWS::StackId` resolves to | doc | `pseudo_parameters` |
| The code for termination protection on delete | doc | `termination_protection` |
| Pagination sizes on events and stacks | doc | `pagination` |

## 9. Owed to integration

| What | With |
|---|---|
| A queue and its DLQ in one template (SQS-1) | SQS |
| An SSM parameter's value, and a secret's, changed by an update (CFN-1) | Parameter Store, Secrets Manager |
| A stream declared in a template exists (CFN-2) | Kinesis |
| A custom bus and a rule on it (CFN-3) | EventBridge |
| A queue policy, a topic policy and a Lambda permission from a template decide access under enforce (CFN-4) | SQS, SNS, Lambda, IAM |
| An IAM role in a template, if the decision is to map it | IAM |
| A peer's denial during apply under enforce: the failed resource and its reason | IAM |
| Per mapped type, the property table read back from the owning service (CFN-5, CFN-23) | all fourteen |
| A failed resource's reason for each peer's error shape (CFN-18, foundation A1) | all |
| `TemplateURL` and nested children fetched from a staged object | S3 |
| Function code from S3 and from `_local_` | Lambda, S3 |
| `Export`, `Emit`, redeploy into an empty instance, for a stack with every kind | all |
| The four deploy smokes widened from "it deploys" to "it deploys, updates, and deletes clean" | all |
| The stack's resources on the map | the wiring graph |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] CFN-1 to CFN-39 each closed, with the test that failed first named beside it.
- [ ] No mapped type reports COMPLETE with nothing made. `AWS::Kinesis::Stream`,
      `AWS::Events::EventBus`, `AWS::SQS::QueuePolicy`, `AWS::SNS::TopicPolicy`
      and `AWS::Lambda::Permission` are each functional, or ignored and listed
      as such, or refused.
- [ ] The per-type property table is checked in beside the mappers, a test
      holds it against them, and it has no "not established" cell.
- [ ] Decided and written into `SUPPORT.md`: whether a refused create leaves a
      record (CFN-6); whether `AWS::IAM::*` is mapped; whether `Capabilities`
      is required (CFN-19); the code for a refused operation (CFN-24); what an
      unknown property does (CFN-5); what `Tags` on a stack reach.
- [ ] Every "not established" in section 2 resolved to F, C or S and tested:
      `RetainResources`, `DeletionMode`, `ChangeSetType=IMPORT`,
      `IncludeNestedStacks`, `EnableTerminationProtection` on create,
      `TemplateStage`, `ChangeSetName` on GetTemplate, an explicit empty
      parameter value, `Rules`, template `Metadata`, `Hooks`,
      `DeletionPolicy: Snapshot`, `UpdateReplacePolicy`, `UpdatePolicy`,
      `CreationPolicy`, AWS-specific and SSM parameter types,
      `AWS::NotificationARNs`, the `GetAtt` attribute table, and where
      `AWS::Serverless::LayerVersion` is handled.
- [ ] `UpdateTerminationProtection` is F in the ledger. `SetStackPolicy` stays
      C and the console says so if it ever shows one.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] An update of every mapped kind changes the resource, or the ledger says
      which properties are create-only.
- [ ] All 21 boards implemented from the foundation's shell, or the document
      says which were dropped. Cfn-20 is dropped if CFN-6 removes the record.
- [ ] The resource classifier the console uses is the one `Transpile` uses.
- [ ] No statement on a CloudFormation screen is in the "not yet backed" table.
- [ ] The four confirms say only what the code does, including for Retain and
      for a rolled-back stack.
- [ ] `e2e/tests/cloudformation.spec.ts` and `cfn-starter.spec.ts` cover every
      route under `/cfn` with the route gate on.
- [ ] `docs/cloudformation.md`'s three lists are generated from, or tested
      against, `nameProperty` and `ignoredTypes`.
- [ ] Conformance: a scenario for every row in section 8. Zero "answered
      differently". The unverified count is written here: ____ (9 today).
- [ ] Mutation baselines recorded here for the three packages: ____.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract and decisions; the property table | `docs/SUPPORT.md`, `docs/not-built.md`, `docs/cloudformation.md`, a table beside `internal/cfn/resources.go` |
| 2 | Red tests for CFN-1 to CFN-26 | `cloudformation/*_test.go`, `internal/cfn/*_test.go`, `internal/provision/*_test.go` |
| 3 | Types that make nothing; dropped properties | `internal/cfn/mapper.go`, `mapper_*.go`, `resources.go`, `internal/provision/model.go` |
| 4 | Update convergence and ordering | `internal/provision/apply.go`, `svc_ssm.go`, `svc_secrets.go`, the other `svc_*.go`, `existing.go`, `destroy.go` |
| 5 | Stack lifecycle: refused create, delete, importers, events, reasons | `cloudformation/stacks.go`, `rollback.go`, `deploy_support.go`, `adopt.go`, `stacks_nested.go` |
| 6 | Describe fields, `NoEcho`, parameters, pagination, capabilities | `cloudformation/stacks.go`, `store.go`, `internal/cfn/parameters.go`, `intrinsics.go` |
| 7 | Change sets | `cloudformation/changesets.go`, `internal/cfn/resources.go` (`fingerprint`) |
| 8 | Section 5 sweep, parity cases | `cloudformation/rejection_parity_test.go`, `service_test.go`, `validate.go` |
| 9 | Conformance scenarios and claims | `conformance/tests/test_cloudformation.py`, the claims file from foundation B1 |
| 10 | Console | `internal/console/templates/cfn.html`, `create.html` (`cfn_create`), `handlers_cfn.go`, `client_cfn.go`, `client_cfn_export.go`, `static/editor.js`, `copy.go` |
| 11 | Browser tests | `e2e/tests/cloudformation.spec.ts`, `cfn-starter.spec.ts` |
| 12 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 and 4 change `internal/provision`, which `doze-aws apply` also uses.
`apply` converges onto what exists on purpose; a stack must not. Where the two
differ (overwriting a parameter's value, adopting a taken name) the difference
is an argument to `Apply`, not a second code path.

**Prior art.** AWS's own resource and property reference is the source for the
property table: each type's page lists its properties, which are create-only
("Update requires: Replacement"), and its `Ref` and `Fn::GetAtt` values, which
is exactly the three columns this stage has to fill. The AWS CLI's `deploy`
command is the consumer of the empty change set's reason and of "No updates are
to be performed", so its behaviour is the test for both. LocalStack orders
CloudFormation resources by dependency, as the SQS document notes; that is the
general answer to CFN-22.
