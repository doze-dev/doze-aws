# CloudFormation — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

All 90 documented operations are accounted for: 23 handled, 67 refused with a
stated reason. Nothing falls through to a bare `InvalidAction`.

The narrow surface is the point. CloudFormation's 90 operations are mostly
cloud-side machinery — StackSets, drift detection, the extension registry,
resource scanning — that has no local counterpart to inspect. What deployment
tools actually call is about twenty operations, and those are real.

Verified end to end against `aws cloudformation deploy`, `sam deploy`, `cdk
bootstrap`, `cdk deploy`, `cdk destroy` and Serverless Framework output. See
[../cloudformation.md](../cloudformation.md) for the design.

| Operation | Tier | Notes |
|---|---|---|
| CreateStack | F | transpiles, provisions and returns CREATE_COMPLETE synchronously; a duplicate name is AlreadyExistsException |
| UpdateStack | F | merges parameters with the stack's existing ones; `UsePreviousTemplate` honoured |
| DeleteStack | F | **reclaims the resources the stack created**, then retains the record as DELETE_COMPLETE; idempotent; refuses while another stack imports an export |
| DescribeStacks | F | by name or StackId; a deleted stack resolves by id only, as in AWS |
| ListStacks | F | `StackStatusFilter` honoured |
| DescribeStackEvents | F | synthesized from what apply actually did — a resource already in place gets no create event; newest first, and the newest is always terminal |
| DescribeStackResource / DescribeStackResources / ListStackResources | F | logical → physical id mapping for everything the stack owns |
| GetTemplate | F | the template as deployed |
| GetTemplateSummary | F | parameters, description, resource types, declared transforms |
| ValidateTemplate | F | real parse; a malformed template is rejected |
| CreateChangeSet | F | materialises the stack in REVIEW_IN_PROGRESS, computes a resource-level Add/Modify/Remove diff; an empty diff FAILS with the exact phrase the AWS CLI special-cases |
| DescribeChangeSet | F | status, execution status, and the change list |
| ExecuteChangeSet | F | provisions and moves the stack to a terminal status |
| DeleteChangeSet / ListChangeSets | F | |
| ListExports | F | a real cross-stack export registry; `Fn::ImportValue` resolves against it |
| ListImports | F | |
| SetStackPolicy / GetStackPolicy | C | the document round-trips; nothing locally enforces it |
| UpdateTerminationProtection | C | stored and honoured by DeleteStack |
| CancelUpdateStack | S | apply is synchronous, so there is never an update in flight to cancel |
| ContinueUpdateRollback / RollbackStack | S | same reason: nothing is ever mid-flight |
| DetectStackDrift / DetectStackResourceDrift / DetectStackSetDrift | S | there is nothing to drift from locally |
| DescribeStackDriftDetectionStatus / DescribeStackResourceDrifts | S | as above |
| StackSets (17 operations) | S | StackSets need Organizations |
| Extension registry (14 operations) | S | RegisterType, PublishType, hooks and type configuration are cloud infrastructure |
| Resource scanning (5 operations) | S | scanning reads a real account |
| Generated templates (6 operations) | S | template generation reads a real account |
| Stack refactoring (5 operations) | S | a cloud-side operation |
| Organizations access (3 operations) | S | there is no Organizations locally |
| EstimateTemplateCost | S | there is no pricing API locally |
| DescribeAccountLimits | S | there are no account limits locally |
| SignalResource | S | there are no EC2 instances to signal |
| DescribeEvents | S | an alias for DescribeStackEvents that no current SDK emits |
| RecordHandlerProgress | S | used by registry resource providers |

## Deliberate divergences

**Everything is synchronous.** Nothing is ever `CREATE_IN_PROGRESS`, because
the work is finished before the call returns. The one non-terminal status that
does exist is `REVIEW_IN_PROGRESS`, because real CloudFormation materialises a
stack the moment a CREATE change set is made and deploy tools poll its events
between `CreateChangeSet` and `ExecuteChangeSet`.

**Failures surface early.** A template that cannot be transpiled returns a
`400` from `CreateStack`, where AWS would return `200` and report the failure
through events later. The failure is *also* written to the stack's status and
events, so a client that only polls still sees it.

**Physical names are logical IDs.** No `mystack-MyQueue-1A2B3C4D` suffixes —
see [../cloudformation.md](../cloudformation.md#naming).

## See also

- [../cloudformation.md](../cloudformation.md) — templates, intrinsics, SAM,
  CDK and Serverless.
- [../cli.md](../cli.md) — `doze-aws apply` and `doze-aws export`.
