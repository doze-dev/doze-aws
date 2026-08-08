# CloudFormation, SAM, CDK and Serverless

doze-aws speaks CloudFormation, so the deployment tool you already use works
against it unmodified. All four of these are verified against a running
doze-aws, not inferred:

```sh
aws cloudformation deploy --template-file template.yaml --stack-name shop
sam deploy --stack-name shop --s3-bucket artifacts
cdk bootstrap && cdk deploy
serverless package && aws cloudformation deploy \
  --template-file .serverless/cloudformation-template-update-stack.json --stack-name sls-dev
```

There is no doze-specific file format. There used to be — a `stack.yaml`
dialect — and it was removed, because a format only doze-aws speaks is a format
nobody wants to learn.

## How it works

CloudFormation here is a **front end**, not a second provisioning engine. A
template is parsed, its intrinsics are evaluated, and its resources are mapped
onto an internal resource graph that a convergent apply turns into real local
resources. That reuse is why the whole thing is a few thousand lines rather
than the ~42,000 LocalStack spends on the same job.

Every stack operation is **synchronous**. `CreateStack` transpiles, provisions,
records what happened and returns `CREATE_COMPLETE`; the events deploy tools
poll are synthesized afterwards from what apply actually did. A deploy tool's
first poll therefore succeeds, which is both faster and more honest than
reporting `IN_PROGRESS` for work that already finished.

One consequence worth knowing: a bad template comes back as a `400` from
`CreateStack` itself, where real CloudFormation would return `200` and report
the failure later through events. Both are also recorded on the stack, so a
client that only polls still sees them — but locally, failing at your terminal
beats burying it in an event trail.

## The three outcomes

Every resource lands in exactly one bucket, and every one is reported:

| Outcome | Meaning |
|---|---|
| **mapped** | doze-aws models it. It is provisioned. |
| **skipped** | No local analogue, but the template is still valid without it — IAM roles, log groups, ECR repositories, alarms. **Accepted and printed.** |
| **unsupported** | The type belongs to a service doze-aws does not serve. The template **fails** rather than deploying half of itself. |

```
CloudFormation template: 3 resources mapped, 2 skipped, 0 unsupported
  ≈ Role (AWS::IAM::Role) — no IAM evaluation during apply
  ≈ Logs (AWS::Logs::LogGroup) — there is no CloudWatch Logs locally
```

The "skipped" tier is a deliberate exception to the project's no-silent-no-op
rule. Real templates are full of `AWS::IAM::Role`; refusing them would fail
essentially every template. So they are accepted — and every one is printed, so
the gap surfaces here rather than in production.

**A skipped resource keeps its identity.** `Role: !GetAtt ExecutionRole.Arn`
appears in almost every function, and CDK's bootstrap `!Ref`s an ECR repository
from an output. Skipped resources get a synthesized name and a plausible ARN so
those references resolve; nothing consumes them.

## Naming

CloudFormation generates physical names like `mystack-MyQueue-1A2B3C4D`.
doze-aws uses **the logical ID**, unless the template sets an explicit name
property (`QueueName`, `BucketName`, `TableName`, `FunctionName`, …).

That is a deliberate divergence: locally you want
`aws sqs receive-message --queue-url .../MyQueue`, not to go hunting for a
random suffix. Templates that set explicit names behave identically to AWS.

A **derived** name is sanitised to the target service's rules — a logical ID
like `ServerlessDeploymentBucket` becomes `serverlessdeploymentbucket`, because
S3 requires lowercase. An **explicit** name is never rewritten, so a template
real CloudFormation would reject is rejected here too.

## Intrinsic functions

Both JSON and YAML are accepted, and YAML short-form tags are normalised during
parsing — `!Ref`, `!GetAtt`, `!Sub`, `!If` all work, in both the dotted
(`!GetAtt Queue.Arn`) and list (`!GetAtt [Queue, Arn]`) spellings.

Supported: `Ref`, `Fn::GetAtt`, `Fn::Sub` (including `${Logical.Attr}` and the
`${!Literal}` escape), `Fn::Join`, `Fn::Select`, `Fn::Split`, `Fn::FindInMap`,
`Fn::If`, `Fn::Equals`, `Fn::And`, `Fn::Or`, `Fn::Not`, `Fn::Base64`,
`Fn::GetAZs`, `Fn::ImportValue`, `Fn::ToJsonString`, `Fn::Length`, `Condition`.

Pseudo-parameters: `AWS::Region`, `AWS::AccountId`, `AWS::Partition`,
`AWS::StackName`, `AWS::StackId`, `AWS::URLSuffix`, `AWS::NoValue`.

`Conditions` are evaluated to a fixed point, so one may reference another
declared after it. A resource whose condition is false is not created, and is
reported as skipped. Parameters are coerced by declared type on both the
supplied and default paths, so `Ref` on a `CommaDelimitedList` or any
`List<...>` yields a list rather than a string.

**An intrinsic that cannot be resolved is an error, never an empty string.**
A `!Ref` to an undeclared parameter, a `!GetAtt` to an attribute doze-aws does
not model, or an `Fn::ImportValue` with no export all fail the deploy. A queue
created with a blank name because a substitution silently missed is the worst
outcome available, so it is designed out.

## Stacks

Stacks are real records: they persist, they own their resources, and they can be
torn down.

| Operation | Behaviour |
|---|---|
| `CreateStack` / `UpdateStack` | transpile, provision, record, return a terminal status |
| `DeleteStack` | **reclaims the resources the stack created**, then retains the record as `DELETE_COMPLETE` |
| `CreateChangeSet` | materialises the stack in `REVIEW_IN_PROGRESS` and computes a resource-level diff |
| `ExecuteChangeSet` | provisions the change set |
| `DescribeStackEvents` | synthesized from what apply really did; the newest event is always terminal |
| `ListExports` / `Fn::ImportValue` | a real cross-stack export registry |

Deletion is the capability a plain transpiler could not have, and it is what
makes a local stack feel like a stack rather than an accumulating pile. A
deleted stack stays queryable **by StackId** as `DELETE_COMPLETE` and disappears
**by name**, which is both what AWS does and what `cdk destroy` waits for.

An empty change set fails with the exact phrase the AWS CLI special-cases, so a
no-op redeploy reports *"No changes to deploy"* rather than erroring.

A stack whose export another stack imports cannot be deleted.

## Resource types

Mapped:

| Type | Notes |
|---|---|
| `AWS::SQS::Queue` | FIFO, visibility, delay, retention, `RedrivePolicy` → DLQ + maxReceiveCount, tags |
| `AWS::SNS::Topic` | inline `Subscription` list |
| `AWS::SNS::Subscription` | standalone; attaches after every resource exists, so declaration order does not matter |
| `AWS::S3::Bucket` | versioning, object lock, CORS, lifecycle, website, notifications (queue/topic/lambda, with prefix and suffix filters) |
| `AWS::DynamoDB::Table`, `::GlobalTable` | key schema, GSIs, LSIs, TTL, deletion protection |
| `AWS::Lambda::Function` | runtime, handler, code, env, timeout, memory, DLQ |
| `AWS::Lambda::EventSourceMapping` | SQS sources become function triggers |
| `AWS::Events::Rule` | pattern, schedule, state, targets with `InputPath` / `Input` / `InputTransformer` |
| `AWS::KMS::Key`, `::Alias` | the alias renames the key, since keys are addressed by alias |
| `AWS::SecretsManager::Secret` | `SecretString`, or `GenerateSecretString`'s template as a placeholder |
| `AWS::SSM::Parameter` | |
| `AWS::Kinesis::Stream` | accepted; the resource graph has no streams section yet |
| `AWS::Serverless::Api`, `AWS::ApiGateway::RestApi`, `AWS::ApiGatewayV2::Api` | a REST API; routes arrive from the functions that bind to it |
| `AWS::ApiGateway::Deployment`, `::Stage`, `::Resource`, `::Method`, `::Account` | recognised; the resource tree is rebuilt from routes at apply time |
| `AWS::Lambda::Permission`, `::Version`, `::Alias`, `::Url`, `::LayerVersion` | recognised and referenceable |
| `AWS::S3::BucketPolicy`, `AWS::SQS::QueuePolicy`, `AWS::SNS::TopicPolicy` | recognised; no local policy evaluation |

Skipped with a reason: `AWS::IAM::*`, `AWS::Logs::*`, `AWS::CloudWatch::*`,
`AWS::ECR::Repository`, `AWS::CDK::Metadata`,
`AWS::CloudFormation::WaitCondition*`.

Everything else fails the template, naming the service.

## Function code

Three spellings work, which is what lets every tool deploy:

| Spelling | Used by |
|---|---|
| `Code: {S3Bucket: _local_, S3Key: /path/to/dir}` | hand-written templates — the code runs in place, so edit-and-reinvoke works |
| `Code: {ZipFile: <inline>}` | small inline functions |
| `Code: {S3Bucket: <bucket>, S3Key: <key>}` | `sam deploy` and `cdk deploy`, which stage the zip in S3 — doze-aws fetches and unpacks it, as real Lambda does |

SAM's `CodeUri` accepts a local path or an `s3://bucket/key` produced by
packaging. `InlineCode` is dropped: there is no build step locally, so apply
reports a missing code path rather than creating a function that cannot run.

## SAM

`Transform: AWS::Serverless-2016-10-31` is understood, and `Globals.Function`
supplies defaults that an explicit property overrides.

| SAM resource / event | Result |
|---|---|
| `AWS::Serverless::Function` | a Lambda function |
| `AWS::Serverless::SimpleTable` | a DynamoDB table from `PrimaryKey` |
| `Events` of type `SQS` | a function trigger |
| `Events` of type `SNS` | a topic subscription |
| `Events` of type `Schedule` / `ScheduleV2` | an EventBridge rule |
| `Events` of type `EventBridgeRule` / `CloudWatchEvent` | an EventBridge rule |
| `Events` of type `Api` / `HttpApi` | a route on a REST API, deployed and callable — see [api-support/apigateway.md](api-support/apigateway.md) |
| `AWS::Serverless::Api`, `::HttpApi` | an API the function's routes attach to |
| `AWS::Serverless::StateMachine` | refused — no Step Functions yet |

A SAM `Api` event becomes a `method + path -> function` route. Several functions
may bind to the same API, and the API is deployed to SAM's default `Prod` stage
unless the template names another.

## CDK

`cdk bootstrap` works: it creates the `CDKToolkit` stack, the staging bucket,
and the `/cdk-bootstrap/hnb659fds/version` parameter `cdk deploy` checks. The
bootstrap template's ECR repository is skipped, since there is no container
registry locally — which is only a problem for Docker image assets.

`cdk deploy`, `cdk destroy` and multi-stack apps with cross-stack references all
work. Templates are published to the staging bucket and fetched from there via
`TemplateURL`, exactly as against real AWS.

## Serverless Framework

`serverless.yml` is not parsed directly — it is a framework configuration with a
large plugin ecosystem, and it *generates* CloudFormation. Run `serverless
package` and deploy what it emits:

```sh
serverless package
aws cloudformation deploy \
  --template-file .serverless/cloudformation-template-update-stack.json \
  --stack-name my-service-dev
```

One CloudFormation front end covers all three ecosystems; parsing each
framework's own DSL would be an endless maintenance surface.

## Exporting

`doze-aws export` writes the running stack as a CloudFormation template:

```sh
doze-aws export > template.yaml
```

Click a stack together in the console, export it, commit it — and what you
commit is something the rest of your tooling reads. Secret and SecureString
values are deliberately left blank.

## What this is not

There is no drift detection, no rollback, no StackSets, no resource registry,
and no nested stacks. Those describe cloud-side machinery with no local
counterpart to inspect; each is refused by name with the reason. See
[api-support/cloudformation.md](api-support/cloudformation.md).

## See also

- [cli.md](cli.md) — `apply`, `export`, and the server flags.
- [api-support/cloudformation.md](api-support/cloudformation.md) — the
  operation-by-operation support table.
