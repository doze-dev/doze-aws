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
CloudFormation template: 4 resources mapped, 1 skipped, 0 unsupported
  ≈ Role (AWS::IAM::Role) — no IAM evaluation during apply
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
| `AWS::S3::Bucket` | versioning, object lock, CORS, lifecycle, website, notifications (queue/topic/lambda, with prefix and suffix filters), `PublicAccessBlockConfiguration` (applied before the policy, so a public policy under `BlockPublicPolicy` fails the deploy as on AWS), `OwnershipControls` |
| `AWS::DynamoDB::Table`, `::GlobalTable` | key schema, GSIs, LSIs, TTL, deletion protection |
| `AWS::Lambda::Function` | runtime, handler, code, env, timeout, memory, DLQ, `Layers` |
| `AWS::Lambda::EventSourceMapping` | SQS sources become function triggers |
| `AWS::Lambda::LayerVersion` | `Content` in the same three spellings as function code (`_local_` may name a directory laid out like an unpacked layer), `CompatibleRuntimes`, `Description`. Each deploy publishes a version when the content changed and keeps the latest one when it did not, so a redeploy does not pile up versions; a function listing the layer gets the version this deploy settled on |
| `AWS::Lambda::Version` | publishes the function on every deploy; unchanged code and configuration keep their version. `Ref` and `Version` are a placeholder (`$published`) that an alias in the same template consumes, because the number is not known until the function is published |
| `AWS::Lambda::Alias` | `Name`, `Description`, `FunctionVersion` (the version this deploy publishes, or an explicit number); a weighted `RoutingConfig` collapses to that version, which is where a gradual deployment ends up. `Ref` and `AliasArn` are the real alias ARN, and invoking or triggering it runs the frozen version |
| `AWS::Lambda::Url` | `AuthType` (`NONE` and `AWS_IAM` are both served, without a signature check) and `Cors`; `FunctionUrl` is the real URL the gateway serves, shaped from the endpoint doze-aws listens on. A URL on a qualified ARN addresses the function |
| `AWS::Events::Rule` | pattern, schedule, state, targets with `InputPath` / `Input` / `InputTransformer`; an API destination target (`!GetAtt Dest.Arn`) with `HttpParameters` (path values, headers, query) |
| `AWS::Events::Connection` | `AuthorizationType` BASIC / API_KEY / OAUTH_CLIENT_CREDENTIALS with `AuthParameters` and `InvocationHttpParameters`; `Arn` is the name-form ARN the service resolves, `SecretArn` the one AWS would mint. VPC Lattice connectivity parameters are refused by name. An export blanks the secret values |
| `AWS::Events::ApiDestination` | `ConnectionArn` (a `GetAtt` on the connection, resolved to the minted ARN at apply), `InvocationEndpoint`, `HttpMethod`, `InvocationRateLimitPerSecond` (stored, not enforced) |
| `AWS::KMS::Key`, `::Alias` | the alias renames the key, since keys are addressed by alias |
| `AWS::SecretsManager::Secret` | `SecretString`, or `GenerateSecretString`'s template as a placeholder |
| `AWS::SSM::Parameter` | |
| `AWS::Kinesis::Stream` | accepted; the resource graph has no streams section yet |
| `AWS::Serverless::Api`, `AWS::ApiGateway::RestApi`, `AWS::ApiGatewayV2::Api` | a REST API; routes arrive from the functions that bind to it |
| `AWS::ApiGateway::Stage` | `StageName`, and the stage's `AccessLogSetting` (destination and format) and `MethodSettings` (logging level, data trace, metrics per resource path and method), patched onto the deployed stage the way CloudFormation patches them |
| `AWS::ApiGateway::Resource`, `::Method` | the resource tree as CDK and hand-written templates declare it: paths rebuilt from `ParentId`/`PathPart` up to `!GetAtt Api.RootResourceId`, each method one route. `Integration.Type` `AWS_PROXY` (the function the `Uri` names) or `MOCK` (the first `IntegrationResponses` entry's status, `method.response.header.*` parameters and `application/json` template — a CDK CORS preflight); other types are refused by name. `AuthorizationType` `NONE`, `AWS_IAM` (unchecked locally) or `CUSTOM` with `AuthorizerId`; `ApiKeyRequired` |
| `AWS::ApiGateway::Authorizer` | `TOKEN` and `REQUEST` Lambda authorizers: `AuthorizerUri`, `IdentitySource`, `IdentityValidationExpression`, `AuthorizerResultTtlInSeconds`. `Ref` is the authorizer's name, resolved to the id the service mints at apply. `COGNITO_USER_POOLS` is refused by name |
| SAM `Auth` on `AWS::Serverless::Api` and on an `Api` event | `Authorizers` with `FunctionArn`, `FunctionPayloadType`, `Identity` (`Header`, `ValidationExpression`, `ReauthorizeEvery`; `Headers`/`QueryStrings` for REQUEST), `DefaultAuthorizer`, `ApiKeyRequired`; an event's `Auth.Authorizer` (`NONE` opts out of the default) and `Auth.ApiKeyRequired`. Cognito authorizers are refused by name |
| `AWS::ApiGateway::ApiKey`, `::UsagePlan`, `::UsagePlanKey` | a key (`Name`, `Value`, `Enabled`, `Description`), a plan (`UsagePlanName`, `ApiStages` by `!Ref Api`, `Throttle`, `Quota`), and the join between them by `Ref`. `Ref` on a key or plan is its name, resolved to the minted id at apply. SAM `Auth.UsagePlan` (`CreateUsagePlan` `PER_API` or `SHARED`, `UsagePlanName`, `Throttle`, `Quota`) makes the key and plan SAM would, on the API's stage |
| `AWS::ApiGateway::Deployment`, `::Account` | recognised; a deployment happens on every apply, and the account record only holds a role ARN |
| `AWS::Lambda::Permission` | recognised and referenceable; nothing locally gates an invocation on the policy |
| `AWS::S3::BucketPolicy` | the document lands on the bucket (stored; not evaluated as an access control) |
| `AWS::SQS::QueuePolicy`, `AWS::SNS::TopicPolicy` | recognised; no local policy evaluation |
| `AWS::StepFunctions::StateMachine` | `DefinitionString` or `Definition`, `DefinitionSubstitutions` applied after intrinsics (what the CDK emits), type, role, tags, `LoggingConfiguration` (the CDK's `logs` property; history is vended to the group it names); `DefinitionUri` is refused — inline the definition for a local deploy |
| `AWS::StepFunctions::StateMachineVersion` | publishes the machine's revision on every deploy; an unchanged definition keeps its version. Its `Ref` is a placeholder only an alias in the same template can consume, because the version number is not known until the machine is published |
| `AWS::StepFunctions::StateMachineAlias` | `Name`, `Description`, and the version named by `RoutingConfiguration` or `DeploymentPreference`; every alias routes all of its traffic to the version this deploy publishes, which is where a gradual deployment ends up. `Ref` and `Arn` are the real alias ARN |
| `AWS::StepFunctions::Activity` | `Name` and tags; `Ref` and `Arn` are the activity ARN a Task's `Resource` names |
| `AWS::Logs::LogGroup` | `LogGroupName` (the logical id when absent), `RetentionInDays`, tags; `Ref` is the name and `Arn` ends in `:*` as CloudWatch Logs reports it. Lambda creates `/aws/lambda/<fn>` itself on the first line, so a template needs one only to set retention |
| `AWS::Logs::SubscriptionFilter` | `LogGroupName`, `FilterName` (the logical id when absent), `FilterPattern`, `DestinationArn` (a Lambda function or a Kinesis stream; Firehose is refused by name), `Distribution`. A group only Lambda would create is declared for it, so the filter lands before the first line; a Kinesis stream must already exist |

Skipped with a reason: `AWS::IAM::*`, `AWS::Logs::LogStream`,
`AWS::CloudWatch::*`,
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
| `AWS::Serverless::Function` | a Lambda function; `AutoPublishAlias` becomes a version and an alias at it (`<Fn>Alias<name>`), `FunctionUrlConfig` a function URL (`<Fn>Url`, so `!GetAtt MyFnUrl.FunctionUrl` resolves), `DeploymentPreference` is dropped because the shift is instant |
| `AWS::Serverless::LayerVersion` | a layer from `ContentUri` (a local directory or zip, or an `s3://` reference); `RetentionPolicy` is dropped |
| `AWS::Serverless::SimpleTable` | a DynamoDB table from `PrimaryKey` |
| `Events` of type `SQS` | a function trigger |
| `Events` of type `SNS` | a topic subscription |
| `Events` of type `Schedule` / `ScheduleV2` | an EventBridge rule |
| `Events` of type `EventBridgeRule` / `CloudWatchEvent` | an EventBridge rule |
| `Events` of type `Api` / `HttpApi` | a route on a REST API, deployed and callable — see [api-support/apigateway.md](api-support/apigateway.md) |
| `AWS::Serverless::Api`, `::HttpApi` | an API the function's routes attach to; `StageName`, `AccessLogSetting` and `MethodSettings` reach the stage |
| `AWS::Serverless::StateMachine` | a state machine; `Logging` becomes its `LoggingConfiguration`, `Policies` and `Tracing` are dropped, and `Events` is refused by name — start executions directly or from a Lambda |

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
