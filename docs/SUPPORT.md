# doze-aws — API support

What every service implements, at what tier, and what it does not implement
with the reason. One document because it is one question: a reader deciding
whether to trust this emulator should not have to open eighteen files to find
out that the one operation they need is a stub.

There are **eighteen sections for seventeen services**. API Gateway is one
service with two separate APIs — REST (v1) and HTTP (v2) — and they differ
enough in operations, routing and authorizers that one table for both would
answer neither question. Everywhere else in the project the count is
seventeen, which is what `doze-aws` serves and what the banner reports.

This table is also shipped code. The console renders its fidelity panel by
parsing the sections below at runtime, so the format is an interface — see
docs/ledger.go.

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

## Contents

- [API Gateway](#api-gateway--api-support)
- [API Gateway v2 (HTTP APIs)](#api-gateway-v2-http-apis--api-support)
- [CloudFormation](#cloudformation--api-support)
- [CloudWatch](#cloudwatch--api-support)
- [DynamoDB](#dynamodb--api-support)
- [EventBridge](#eventbridge--api-support)
- [IAM](#iam--api-support)
- [Kinesis](#kinesis--api-support)
- [KMS](#kms--api-support)
- [Lambda](#lambda--api-support)
- [CloudWatch Logs](#cloudwatch-logs--api-support)
- [S3](#s3--api-support)
- [Secrets Manager](#secrets-manager--api-support)
- [SNS](#sns--api-support)
- [SQS](#sqs--api-support)
- [SSM](#ssm--api-support)
- [Step Functions](#step-functions--api-support)
- [STS](#sts--api-support)

<!-- svc:apigateway -->
## API Gateway — API support

doze-aws implements the REST (v1) **create → deploy → invoke** path: 54 of API
Gateway's 124 operations, covering everything needed to stand an API up and
actually call it. The remaining families are refused by name.

The surface is deliberate. Much of API Gateway's operation count is edge
machinery — custom domains, client certificates, VPC links, SDK generation,
usage metering — with no local counterpart. What a developer needs locally is
for a deployed API to answer, and for the gates a template puts in front of it
(a Lambda authorizer, an API key) to gate.

### Two planes

The **control plane** is CRUD over a path tree, at `/restapis/...`.

The **execute-api plane** is where a deployed API answers. `/restapis` is
already the control plane, so a deployed API is served at:

```
/_aws/execute-api/{apiId}/{stage}/{path...}
{apiId}.execute-api.<host>/{stage}/{path...}     (virtual-host style)
```

Both shapes match LocalStack's, so existing habits and test helpers transfer.

```sh
curl http://abc123def0.execute-api.us-east-1.aws.harbour.doze/prod/orders/42

# or, reached at an address rather than a name:
curl http://127.0.0.1:4566/_aws/execute-api/abc123def0/prod/orders/42
```

### Routing precedence

Path matching follows API Gateway's own order, which is **not** first-match:

1. an exact literal segment beats a path parameter — `/users/me` wins over `/users/{id}`
2. a path parameter beats a greedy proxy
3. among greedy `{proxy+}` resources the **deepest** wins, so `/api/{proxy+}` beats `/{proxy+}`

Getting this wrong sends `/users/me` to the `/users/{id}` handler, which is the
kind of bug that only surfaces under real traffic. It is covered by tests.

### Integrations

| Type | Tier | Notes |
|---|---|---|
| AWS_PROXY | F | the one that matters. Full proxy event: path, method, headers and multi-value headers, query and multi-value query, path parameters, stage variables, request context, base64 body when it is not valid UTF-8. The function's `{statusCode, headers, body, isBase64Encoded}` drives the response |
| MOCK | F | answers from the integration's own response templates and static header parameters — enough for CORS preflights |
| HTTP / HTTP_PROXY | F | forwards to a real endpoint, `{param}` placeholders expanded, query and headers passed through |
| AWS (non-proxy) | S | needs Velocity mapping templates. Emulating VTL badly would silently produce the wrong backend request, so it is refused |

A function that does not return the proxy-integration response shape gets a
**502** naming what it returned — the same failure AWS produces, and far more
useful than a blank 500.

### Operations

| Operation | Tier | Notes |
|---|---|---|
| CreateRestApi / GetRestApi / GetRestApis / UpdateRestApi / DeleteRestApi | F | a root resource is created with the API; `UpdateRestApi` honours the patch document for name, description, version, apiKeySource and policy |
| CreateResource / GetResource / GetResources / UpdateResource / DeleteResource | F | full paths are recomputed on every tree change; a duplicate path part under one parent conflicts; deleting takes the subtree; the root cannot be deleted |
| PutMethod / GetMethod / DeleteMethod | F | `ANY` supported; a re-put preserves the attached integration and responses, so a Terraform or CloudFormation update does not silently unwire the backend |
| PutIntegration / GetIntegration / DeleteIntegration | F | |
| PutMethodResponse / GetMethodResponse / DeleteMethodResponse | F | |
| PutIntegrationResponse / GetIntegrationResponse / DeleteIntegrationResponse | F | |
| CreateDeployment / GetDeployment / GetDeployments / DeleteDeployment | F | `stageName` creates the stage in the same call, as the CLI and most templates do |
| CreateStage / GetStage / GetStages / UpdateStage / DeleteStage | F | stage variables reach the proxy event; the response carries a usable `invokeUrl`; UpdateStage keeps `accessLogSettings` and every method-setting path (`/*/*/logging/loglevel`, `logging/dataTrace`, `metrics/enabled`, throttling, caching), reported with AWS's defaults filled in, and refuses a patch path it does not know |
| GetTags / TagResource / UntagResource | F | REST API ARNs |
| GetAccount / UpdateAccount | F | the CloudWatch role reads back as set; throttle settings are nominal, nothing is throttled locally |
| CreateApiKey / GetApiKey / GetApiKeys / UpdateApiKey / DeleteApiKey | F | a value is minted when none is given (20 to 128 characters when it is); `includeValue` reveals it; enable/disable, description and customerId patch; deleting a key detaches it from every plan (see below) |
| CreateUsagePlan / GetUsagePlan / GetUsagePlans / UpdateUsagePlan / DeleteUsagePlan | F | `apiStages` name an existing API; `throttle` and `quota` are stored and reported, not enforced; `UpdateUsagePlan` patches name, description, `/apiStages` (`apiId:stage`), throttle and quota |
| CreateUsagePlanKey / GetUsagePlanKey / GetUsagePlanKeys / DeleteUsagePlanKey | F | attach and detach keys; the plan-key views are derived from the plan's key list |
| GetUsage / UpdateUsage | S | doze-aws does not meter requests, so there is no usage to report or reset |
| ImportApiKeys | S | reads a CSV of keys; create them one at a time |
| Custom domains and base path mappings (12 operations) | S | there is no DNS or TLS termination locally |
| Client certificates (5 operations) | S | certificate material is cloud infrastructure |
| VPC links (5 operations) | S | there is no VPC locally |
| CreateAuthorizer / GetAuthorizer / GetAuthorizers / UpdateAuthorizer / DeleteAuthorizer | F | TOKEN and REQUEST Lambda authorizers, run on the data plane (see below); a duplicate name conflicts; `COGNITO_USER_POOLS` is refused by name, there being no user pool locally; `PutMethod` with `CUSTOM` must name an authorizer that exists |
| Request validators and models (10 operations) | S | request validation is schema work with no local consumer yet |
| Documentation parts and versions (10 operations) | S | documentation exports are a publishing feature |
| SDK and export generation (5 operations) | S | code generation is a cloud-side service |
| Gateway responses (4 operations) | S | response customisation with no local consumer yet |

### Deployments serve the live API

Real API Gateway snapshots the API into a deployment, so a change is invisible
until you redeploy. doze-aws serves the **live** API instead: locally you want
an edit to take effect immediately, and a stale snapshot is a debugging trap
rather than a feature. The deployment record still exists so the control plane,
CloudFormation and Terraform all behave.

### Lambda authorizers

A method whose `authorizationType` is `CUSTOM` is gated by the authorizer it
names, before the integration runs and in the order AWS runs its gates
(authorizer, then API key). A `TOKEN` authorizer reads its identity source
(the `Authorization` header by default), checks the token against
`identityValidationExpression` when one is set, and invokes the function
with `{type: TOKEN, authorizationToken, methodArn}`. A `REQUEST` authorizer
reads every source in its comma-separated `identitySource` — headers, query
strings, path parameters, stage variables, `context.identity.sourceIp` and
`context.identity.userAgent` — and invokes the function with the request
shaped as a proxy event plus `type: REQUEST` and `methodArn`.

The function's answer is an IAM policy, evaluated for the method's ARN
(`arn:aws:execute-api:<region>:<account>:<apiId>/<stage>/<METHOD>/<path>`)
with `*` and `?` globs on the resource; an explicit Deny wins, an Allow must
match, and nothing matching is a deny. Its `principalId` and `context` land
on the integration event under `requestContext.authorizer`, as on AWS, and
its `usageIdentifierKey` feeds the API key check when the API's key source
is `AUTHORIZER`. The answer is cached per authorizer and identity values for
`authorizerResultTtlInSeconds` (default 300; 0 disables); updating or
deleting the authorizer drops its cache.

Errors follow AWS: a missing identity source or a token failing validation
is `401 {"message":"Unauthorized"}` with no invoke; a deny is `403 "User is
not authorized to access this resource with an explicit deny"`; an invoke
failure or an answer without `principalId` or `policyDocument` is a 500. AWS
answers that 500 with a null message; doze-aws names the cause
(`Authorizer error: …`) because the cause is the thing you need. The
execution log records the authorizer invoked, whether the verdict came from
the cache, and the principal.

### API keys

A method with `apiKeyRequired` is served only when the request carries a
key — `x-api-key`, or the authorizer's `usageIdentifierKey` when the API's
`apiKeySource` is `AUTHORIZER` — that exists, is enabled, and is attached to
a usage plan covering this API's stage. Anything else is `403
{"message":"Forbidden"}`, as on AWS. The key lands on the integration event
under `requestContext.identity.apiKey` and `apiKeyId`, and the execution
log's usage-plan lines name it. Throttle and quota are stored and reported
and never enforced: nothing is metered locally, which is also why
`GetUsage` and `UpdateUsage` are refused.

### Logging

A stage logs the way its settings say, to the [CloudWatch Logs](logs.md)
service, after the response and off the request's path.

- **Access log**: with `accessLogSettings` set (the CDK's
  `accessLogDestination` and `accessLogFormat`, SAM's `AccessLogSetting`,
  or an UpdateStage patch of `/accessLogSettings/destinationArn` and
  `/format`), every request writes one line in the format given to the
  destination group, with the `$context` variables a local request can
  answer filled in — `requestId`, `httpMethod`, `resourcePath`, `path`,
  `status`, `responseLength`, `responseLatency`, `integrationLatency`,
  `requestTime`, `stage`, `apiId`, `identity.sourceIp`, `identity.userAgent`,
  `error.message` — and `-` for the rest, as AWS writes an absent value. A
  403 for an unmatched path is logged like any other request.
- **Execution log**: with a method setting's `logging/loglevel` at `INFO`
  (the stage-wide `*/*` or the method's own, the method winning), every
  request writes the narrative API Gateway writes — the request as it
  arrived, the integration it went to, what came back, and the status it
  ended with — to `API-Gateway-Execution-Logs_<apiId>/<stage>`, each line
  prefixed with the request id. `ERROR` writes it only for a request that
  failed or answered 4xx/5xx; `logging/dataTrace` adds the bodies.
- The account's `cloudwatchRoleArn` is kept and read back; nothing needs
  it locally, but a deploy that sets it before enabling logs sees what it
  set.

Streams are one per stage per process, named
`<apiId>/<stage>/<date>/<hex>`. The console's stage table links both groups.

### HTTP API (v2)

Implemented, in this package, at the `/v2/apis/...` control plane: routes by
key, Lambda integrations in payload format 1.0 and 2.0, HTTP proxy
integrations, CORS, REQUEST authorizers, stages served at `$default`. Its
ledger is [apigatewayv2.md](apigatewayv2.md). An HTTP API and a REST API do
not see each other: `GetRestApis` lists REST APIs, `GetApis` HTTP APIs, as on
AWS.

### See also

- [../cloudformation.md](../cloudformation.md) — SAM `Api` events and the
  `AWS::ApiGateway::*` resource types.
- [lambda.md](lambda.md) — the function runtime behind a proxy integration.

### Differences from AWS

- **No custom domains and no TLS.** A deployed API answers on the shared
  endpoint at its invoke path, not at `d-xxxx.execute-api...` over HTTPS.
  Domain names, base path mappings and client certificates are refused by
  name — all three need DNS and certificate material that has no local form.
- **No VPC links**, for the same reason: there is no VPC.
- **Usage is not metered.** API keys and usage plans gate a method that
  requires a key, which is the behaviour worth having; the quota and throttle
  half is not enforced, so `GetUsage` has nothing to report and
  `UpdateUsage` nothing to reset.
- **Authorizer results are cached the way AWS caches them**, bounded and with
  a TTL — but the cache is per-process, so restarting doze-aws clears it.

### Verified against

- **A deployed API actually serving** (`sdk_test.go`, `execute_test.go`): the
  control-plane CRUD, then a real HTTP request reaching a Lambda function
  through the resource/method tree, with AWS's own path-precedence rules —
  greedy proxy last.
- **Authorizers** (`authorize_test.go`, `apikey_test.go`): a TOKEN or REQUEST
  Lambda authorizer gating a method with the policy the function returns, and
  an API key gating a method that requires one.
- **The authorizer cache** (`authcache_bound_test.go`,
  `authcache_concurrency_test.go`): bounded under unique tokens, expired
  entries evicted before live ones, and correct under one shared token from
  many goroutines — a cache that grows forever was one of the audit's findings.
- **HTTP APIs (v2)** (`v2_execute_test.go`, `v2_sdk_test.go`,
  `v2_audit_test.go`): payload formats 1.0 and 2.0, route precedence, CORS,
  quick-create, and an HTTP API serving into Lambda.
- **Invoke URLs** (`invoke_url_test.go`): the URL follows the request host and
  honours `X-Forwarded-Proto`, so what a client is told to call is what it can
  reach.
- **Stage logging** (`logging_sdk_test.go`): access and execution logs written
  to CloudWatch Logs.
- **Routes against their own tables** (`validate_hook_test.go`): every route
  matches its own template and every constraint table has a route — the check
  that stops a validation table being attached to nothing.
- **Model-derived rejection parity** (`rejection_parity_test.go`, and
  `v2_parity_test.go` for HTTP APIs) and the dispatch table against
  `testdata/ops_api-gateway.json`.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what API Gateway refuses**.

**118/126 model-derived constraints enforced across all 47 routed operations that
have constrained input, with `knownGaps` empty.** Removing the constraint table
makes 29 of those 118 cases slip through. The remaining eight cannot be put on
this wire at all — see below.

Generated with `dzaudit cases api-gateway`, committed to
`testdata/cases_apigateway.json`, and replayed case by case in
`rejection_parity_test.go` from a baseline the test first proves the service
accepts.

#### The operation has to be worked out before it can be validated

Every other service here names its operation on the wire — a target header, or
an `Action` parameter — so validation is a map lookup. API Gateway names it
nowhere: the operation **is** the method and the path. So `validate.go` carries
a route table alongside the constraint tables, and both are generated from the
same model bindings. A route and the constraints it selects therefore cannot
drift: if AWS says `CreateResource` is `POST
/restapis/{restApiId}/resources/{parentId}`, that is what the matcher looks for
and what the harness sends.

Routes are ordered most-specific first, so `/methods/{m}/integration` wins over
`/methods/{m}`. Nothing but the generator enforces that order, and getting it
wrong would be invisible — the wrong constraint table simply has fewer rules, so
a shadowed route reads as a permissive service rather than a broken matcher. So
`TestEveryRouteMatchesItsOwnTemplate` builds a concrete path from every route's
own template and asserts it resolves back to that route.

#### An omitted path label is an empty segment, not an absent field

This was a real gap, found by the audit. restJson1 binds `restApiId` and its
kin into the URI, so a caller who omits one sends `GET /restapis//resources` —
the member is not missing from a body, it arrives as an empty string. The
validator recorded it as present, which meant **every `@required` path label
passed vacuously**: a label is never absent from the map the router builds. Now
an empty label is treated as omitted, and the seven affected cases are enforced.

#### Eight cases cannot be expressed on this wire

Omitting the **last** label of a URI does not produce an invalid request. It
produces a shorter path, which is a different and entirely valid operation:
`GET /restapis` is `GetRestApis`, not a broken `GetRestApi`. The same is true
for `GetResource`, `GetDeployment`, `GetStage`, `GetAuthorizer`, `GetApiKey`,
`GetUsagePlan` and `GetUsagePlanKey`. There is nothing for the
service to refuse, and AWS does not refuse it either, so these are listed in
`unexpressible` with the reason and counted separately — a gap means AWS
enforces something doze-aws does not, and this is not that.

#### Not audited

The operations doze-aws answers with 501 (models, request validators,
documentation, gateway responses, GetUsage, ImportApiKeys, domain names, VPC
links) have no handler to validate input. `GetRestApis` is routed but has no constrained input in the model at all.

<!-- svc:apigatewayv2 -->
## API Gateway v2 (HTTP APIs) — API support

doze-aws implements the HTTP API **create → route → serve** path: 37 of
apigatewayv2's 103 operations, covering an API with CORS, its integrations,
routes, REQUEST authorizers, stages, deployments and tags, served at the
execute-api plane the way AWS serves them. WebSocket APIs and the families
with no local counterpart are refused by name. The v1 REST API surface is
[apigateway.md](apigateway.md); an HTTP API is the same service with the
`/v2/` control plane, and shares its Lambda authorizer machinery, its access
logs and its console.

### Where an HTTP API answers

```
/_aws/execute-api/{apiId}/{path...}               the $default stage, at the root
/_aws/execute-api/{apiId}/{stage}/{path...}       a named stage
{apiId}.execute-api.<host>/{path...}              virtual-host style, likewise
```

`GetApi` reports the first form as `apiEndpoint`. A stage must exist before
anything answers; with `autoDeploy` (the console's and CloudFormation's
default) every route change is live at once, and without it `CreateDeployment`
with a `stageName` publishes. Locally the live definition is what serves either
way, as for a REST API — the deployment records exist so the control plane,
CloudFormation and the CDK behave.

### Routing

A route key is `$default` or `<METHOD> /<path>` with `{param}` and `{proxy+}`
segments, `ANY` matching every method. Selection follows AWS: a literal
segment beats a parameter, a parameter beats a greedy proxy, the deepest proxy
wins, an exact method beats `ANY`, and `$default` catches whatever nothing else
matched. No match is `404 {"message":"Not Found"}`.

### Integrations

| Type | Tier | Notes |
|---|---|---|
| AWS_PROXY | F | a Lambda function, by ARN or invoke URI, in payload format **2.0** (the function-URL event: `rawPath`, `rawQueryString`, `cookies`, `requestContext.http`, `pathParameters`, `stageVariables`; a bare return value is a 200 JSON body, an object with `statusCode` is the whole response) or **1.0** (the REST proxy event with `version: "1.0"` and `requestContext.routeKey`; a malformed return is a 502) |
| HTTP_PROXY | F | forwards to the URL, `{param}` placeholders expanded, the request's method unless `integrationMethod` names one, headers and query passed through, the backend's response returned as is |
| AWS, HTTP, MOCK | S | WebSocket API integration kinds, refused at create |
| AWS service integrations (`integrationSubtype`) | S | SQS-SendMessage and friends need request-parameter mapping; integrate through a function |
| VPC links (`connectionType: VPC_LINK`) | S | there is no VPC locally |

### CORS

`corsConfiguration` on the API is evaluated on the data plane: an `OPTIONS`
preflight from an allowed origin and method is answered 204 with the allow
headers, max age and credentials flag; one from another origin is a bare 204;
every other response to an allowed origin carries `Access-Control-Allow-Origin`
and the expose and credentials headers. `DeleteCorsConfiguration` stops it.

### Authorizers

A `REQUEST` Lambda authorizer gates a route whose `authorizationType` is
`CUSTOM`. Its `identitySource` selection expressions —
`$request.header.X`, `$request.querystring.X`, `$request.path.X`,
`$stageVariables.X`, `$context.identity.sourceIp`,
`$context.identity.userAgent` — must all be present or the request is
`401 {"message":"Unauthorized"}` with no invoke. The function gets the
request in `authorizerPayloadFormatVersion` (2.0 with `type: REQUEST`,
`routeArn` and `identitySource`; 1.0 with `methodArn`) and answers the
simple response `{isAuthorized, context}` when `enableSimpleResponses` is set,
or an IAM policy evaluated for the route's ARN otherwise. A denial is
`403 {"message":"Forbidden"}`; the context lands on the integration event
under `requestContext.authorizer.lambda` (2.0) or `requestContext.authorizer`
(1.0), with `principalId` when a policy named one. Answers are cached per
authorizer and identity values for `authorizerResultTtlInSeconds`;
`ResetAuthorizersCache`, updating and deleting the authorizer drop the cache.
`JWT` authorizers and `authorizationType: JWT` are refused by name: there is
no identity provider locally to issue or verify tokens. `AWS_IAM` is stored
and not checked — the execute-api call is not signed locally.

### Operations

| Operation | Tier | Notes |
|---|---|---|
| CreateApi / GetApi / GetApis / UpdateApi / DeleteApi | F | `protocolType` HTTP; `WEBSOCKET` is refused by name. `target` quick-creates an integration, a route (`routeKey` or `$default`) and an auto-deploying `$default` stage, as the CLI's `--target` does. `UpdateApi` merges the fields sent. `GetApis` lists HTTP APIs only, and the v1 surface does not see them, as on AWS |
| DeleteCorsConfiguration | F | |
| CreateRoute / GetRoute / GetRoutes / UpdateRoute / DeleteRoute | F | a key must be well formed and unique in the API; a target must name an integration of the API; `CUSTOM` needs an `authorizerId` that exists |
| CreateIntegration / GetIntegration / GetIntegrations / UpdateIntegration / DeleteIntegration | F | AWS_PROXY and HTTP_PROXY (above); an integration a route targets cannot be deleted |
| CreateAuthorizer / GetAuthorizer / GetAuthorizers / UpdateAuthorizer / DeleteAuthorizer | F | REQUEST (above); a duplicate name conflicts; an authorizer a route names cannot be deleted |
| ResetAuthorizersCache | F | |
| CreateStage / GetStage / GetStages / UpdateStage / DeleteStage | F | `autoDeploy`, `stageVariables` (on the event), `accessLogSettings` (an access log line per request to the named group, as for a REST stage; HTTP APIs have no execution log, as on AWS), `defaultRouteSettings` and `routeSettings` stored and reported — nothing is throttled locally |
| DeleteAccessLogSettings / DeleteRouteSettings | F | |
| CreateDeployment / GetDeployment / GetDeployments / UpdateDeployment / DeleteDeployment | F | a deployment a stage points at cannot be deleted |
| GetTags / TagResource / UntagResource | F | `arn:aws:apigateway:<region>::/apis/<id>` |
| Domain names and API mappings (11 operations) | S | there is no DNS or TLS termination locally |
| VPC links (5 operations) | S | there is no VPC locally |
| Models and GetModelTemplate (6 operations) | S | request validation has no local consumer |
| Route responses and integration responses (10 operations) | S | WebSocket API machinery; an HTTP API answers with its integration's response |
| DeleteRouteRequestParameter | S | update the route's `requestParameters` instead |
| ImportApi / ReimportApi / ExportApi | S | OpenAPI import and export are a publishing feature |
| Portals, portal products, product pages, routing rules (26 operations) | S | the developer-portal surface has no local counterpart |

### CloudFormation and SAM

`AWS::ApiGatewayV2::Api` (with `CorsConfiguration` and the `Target` quick
create), `::Integration`, `::Route` (its `Target` naming an Integration of the
template), `::Stage`, `::Deployment` and `::Authorizer` map onto the same
route-shaped stack file a REST API uses, with the protocol marked, and apply
through the v2 control plane. `AWS::Serverless::HttpApi` and a function's
`HttpApi` events do the same: the implicit API is `ServerlessHttpApi` at
`$default`, an event with no `Path` is the `$default` route, and the `Auth`
block's Lambda authorizers carry over. `!GetAtt Api.ApiEndpoint` is the
execute-api address. See [../cloudformation.md](../cloudformation.md).

### Differences from AWS

- **HTTP APIs only.** WebSocket APIs are not served: they need a persistent
  connection registry and a management API that has no local counterpart, and
  a half-served WebSocket API is worse than an absent one.
- **No custom domains and no TLS.** An HTTP API answers on the shared endpoint
  at its `$default` stage rather than at a domain name, so domain names, API
  mappings and VPC links are refused by name.
- **No developer portal.** Portals, portal products, product pages and routing
  rules are a console surface with nothing behind it locally.
- **Authorizer results are cached per-process**, so restarting doze-aws clears
  them.

### Verified against

Served by the `apigateway` package, so the tests live there.

- **An HTTP API actually serving** (`apigateway/v2_sdk_test.go`,
  `apigateway/v2_execute_test.go`): a route reaching Lambda, payload formats
  1.0 and 2.0, route precedence, and CORS answered from the route
  configuration.
- **Quick-create and route settings** (`apigateway/v2_audit_test.go`): a
  quick-create call making the route, stage and integration it implies, and
  route settings deleted by key.
- **CloudFormation deployment** (`cloudformation/apigwv2_apply_test.go`): what
  a CDK `HttpApi` and a SAM `HttpApi` event actually emit, deployed for real.
- **Model-derived rejection parity** (`apigateway/v2_parity_test.go`) and the
  dispatch table against `apigateway/testdata/ops_apigatewayv2.json`.

### Input validation

**88/94 model-derived constraints enforced across all 36 routed operations
that have constrained input, with `knownGapsV2` empty.** The remaining six
cannot be put on this wire: omitting the last path label of a `Get*` produces
the collection's `Get*s`, not an invalid request.

Generated with `dzaudit cases apigatewayv2` and `dzaudit routes
apigatewayv2`, filtered to the operations served and spelled the way the
wire spells them (lowerCamel), committed to `testdata/cases_apigatewayv2.json`
and `testdata/routes_apigatewayv2.json`, and replayed case by case in
`v2_parity_test.go` from a baseline the test first proves the service
accepts. The route table and constraint tables live in `v2_validate.go`.

<!-- svc:cloudformation -->
## CloudFormation — API support

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

### Deliberate divergences

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

### See also

- [../cloudformation.md](../cloudformation.md) — templates, intrinsics, SAM,
  CDK and Serverless.
- [../cli.md](../cli.md) — `doze-aws apply` and `doze-aws export`.

### Differences from AWS

- **Apply is synchronous.** A stack converges before the call returns, so
  there is never an operation in flight: `CancelUpdateStack`,
  `ContinueUpdateRollback` and `RollbackStack` have nothing to act on and are
  refused rather than faked. It also means `CREATE_IN_PROGRESS` is not a state
  you can observe.
- **Only local resource types.** A template referencing a resource doze-aws
  does not serve is reported rather than silently skipped. There is no
  extension registry, so custom resource providers and hooks have nothing to
  register into.
- **No drift detection.** Drift is the difference between a template and an
  account that changed underneath it; the data directory is the account, and
  nothing else writes to it.
- **No StackSets and no Organizations.** Both need an org tree that does not
  exist locally.

### Verified against

- **The service, end to end** (`service_test.go`): CreateStack provisions for
  real, the change-set flow, and the resources it made are the resources the
  other services report.
- **Convergence** (`apply_test.go`): applying the same template twice is a
  no-op, and a property-only edit is seen by a change set (`propdiff_test.go`).
- **Real deployment shapes** (`sfn_apply_test.go`, `apigw_authorizer_apply_test.go`,
  `apigwv2_apply_test.go`, `lambda_versioning_apply_test.go`,
  `cloudwatch_apply_test.go`, `logs_subscription_apply_test.go`,
  `s3_access_apply_test.go`, `nested_apply_test.go`): what `sam deploy` and
  `cdk deploy` actually emit — nested stacks, authorizers, HTTP APIs, alarms
  and metric filters, subscription filters, bucket access settings — deployed
  and then deleted.
- **Intrinsics** (`internal/cfn/intrinsics_test.go`): `Ref`, `Fn::GetAtt`, `Fn::Sub`,
  `Fn::Join` and the rest, including `Ref: AWS::NoValue` dropping a property
  rather than setting it to null.
- **Templates** (`internal/cfn/template_test.go`): YAML short tags, JSON, and bad templates
  refused as bad.
- **Exports** (`export_conflict_test.go`): a second stack cannot claim an
  existing export, and a stack can update without losing its own.
- **Round-trip** (`emit_roundtrip_test.go`): every resource kind this serves
  can be exported and re-applied.
- **Model-derived rejection parity** (`rejection_parity_test.go`).

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what CloudFormation refuses**.

**182/182 model-derived constraints enforced across 22 of the 23 dispatched
operations, with `knownGaps` empty.** Removing the constraint table makes 155 of
those 182 cases slip through, so it is doing work the hand-written checks were
not. The twenty-third, `DescribeStackResources`, has no constrained members in
the model at all — nothing to enforce rather than nothing enforced.

Generated with `dzaudit cases cloudformation`, committed to
`testdata/cases_cloudformation.json`, and replayed case by case in
`rejection_parity_test.go` from a baseline the test first proves the service
accepts. CloudFormation speaks the Query protocol, so the harness builds the
nested shape the model's paths describe and flattens it into form keys on the
way out — the same translation `modelcheck.FromQuery` performs in the other
direction on the service side.

Unlike every other audit here, this one stands up a whole doze-aws stack rather
than the one handler: a stack provisions for real, so each of the 182 cases
creates an actual SQS queue in the sibling service.

#### Two operations have no acceptable baseline, for opposite reasons

`CancelUpdateStack` refuses everything, because doze-aws applies a stack
synchronously inside `CreateStack`/`UpdateStack` and no stack is ever
mid-update. Its four cases still run: the baseline must be refused with "has no
update in progress", which proves it cleared validation, and every mutation must
then be refused with a message the validator wrote. The Query protocol spells
*every* refusal `ValidationError`, state errors included, so on this operation
the error code proves nothing and only the message can carry it.

`ExecuteChangeSet` needs a change set that actually has a change, and executing
one moves the stack out from under the next case, so each case builds its own
from an edited template.

Writing that harness turned up a real bug. Change sets diffed by resource
*identity* — logical id, type, physical name — so editing a queue's
`VisibilityTimeout` matched on all three and the set landed in FAILED carrying
"the submitted information didn't contain changes", the phrase the AWS CLI
special-cases. A local `cdk deploy` was told there was nothing to do. Resources
now carry a fingerprint of their declared properties and an edit registers as a
Modify; an unchanged template still reports no changes, which
`TestChangeSetSeesAPropertyOnlyEdit` checks in both directions.

<!-- svc:cloudwatch -->
## CloudWatch — API support

The slice of CloudWatch a developer needs to write an alarm and believe it:
metrics with their dimensions, statistics over real samples, and alarms that
actually evaluate and actually notify. The point is not to draw graphs — it is
that the alarm you would deploy can be tested before you deploy it.

Metrics arrive four ways, and only one of them is an SDK call:

| Producer | Namespace | What it publishes |
|---|---|---|
| [Lambda](lambda.md) | `AWS/Lambda` | Invocations, Errors, Throttles, Duration, by `FunctionName` and undimensioned |
| [API Gateway](apigateway.md) | `AWS/ApiGateway` | Count, 4XXError/5XXError, Latency by `ApiName`+`Stage` (`4xx`/`5xx` by `ApiId`+`Stage` on an HTTP API) |
| [Step Functions](stepfunctions.md) | `AWS/States` | ExecutionsStarted/Succeeded/Failed/TimedOut/Aborted and ExecutionTime, by `StateMachineArn` |
| an application | its own | `PutMetricData`, an [EMF](lambda.md) line a function prints, or a [log metric filter](logs.md) |

SQS, SNS, DynamoDB and Kinesis publish nothing, deliberately — see
*Differences from AWS*.

### Three wires, one service

CloudWatch is mid-migration off Query, and the protocol an SDK uses is fixed at
code-generation time with no negotiation. doze-aws serves all three:

| Wire | Who speaks it | Shape |
|---|---|---|
| **Smithy RPC v2 CBOR** | aws-sdk-go-v2, Java, Rust, Swift, Kotlin, C++, .NET v4 | `POST /service/GraniteServiceVersion20100801/operation/<Op>`, `Smithy-Protocol: rpc-v2-cbor` |
| **AWS JSON 1.0** | **the AWS CLI (v1 and v2)**, boto3, JavaScript v3, PHP, Ruby, PowerShell | `X-Amz-Target: GraniteServiceVersion20100801.<Op>` |
| **AWS Query** | any SDK pinned below the versions in AWS's protocol table | `Action=<Op>&Version=2010-08-01`, XML back |

Serving only Query would break the entire modern audience, and because the CLI
speaks JSON 1.0, `aws cloudwatch ...` would not work at all. The signing name
is `monitoring`; the IAM action prefix is `cloudwatch`; the JSON target prefix
and RPC v2 service id are both `GraniteServiceVersion20100801`. Those are four
different strings for one service.

`PutMetricData` requests are **gzipped** by aws-sdk-go-v2 (the model marks it
`smithy.api#requestCompression`, and only that operation), so every wire
gunzips before decoding.

Errors are `awsQueryCompatible`: on Query the `<Code>` is the legacy spelling
(`InvalidParameterValue`), while JSON and CBOR keep the modern name in `__type`
and carry the legacy code in the `x-amzn-query-error` header.

| Operation | Tier | Notes |
|---|---|---|
| PutMetricData | F | dimensions are part of a metric's identity, so two metrics with the same name and different dimensions stay distinct; `StatisticValues` and `StorageResolution` accepted; samples are kept raw, which is what makes percentiles exact |
| ListMetrics | F | every series, filterable by namespace, name and dimensions; paginated |
| GetMetricStatistics | F | Sum, Average, Minimum, Maximum, SampleCount and `pNN` percentiles over aligned periods; a period with no observations is omitted rather than reported as zero |
| GetMetricData | F | one `MetricStat` per query, `ScanBy`, `Label`; parallel `Timestamps`/`Values` as AWS returns them. Metric **math expressions** are refused — see below |
| PutMetricAlarm | F | M-of-N over completed periods with `TreatMissingData`; `Statistic` or `ExtendedStatistic`; `Tags`; actions limited to SNS topics and Lambda functions, because an action doze-aws cannot deliver would be an alarm that looks wired up and does nothing |
| DescribeAlarms | F | filters by name, prefix, state and `ActionPrefix`; `ChildrenOfAlarmName`, `ParentsOfAlarmName` and an `AlarmTypes` without `MetricAlarm` answer an empty list, which is the truthful answer when there are no composite alarms |
| DescribeAlarmsForMetric | F | which alarms watch one series — how the console says "this metric has an alarm on it" |
| DeleteAlarms | F | takes them all at once; the history goes with the alarm |
| SetAlarmState | F | fires the actions for the state you set, so an alarm's notification is testable before its metric ever breaches; the evaluator does not immediately undo a deliberate flip |
| DescribeAlarmHistory | F | configuration updates, state updates and actions, filterable by type and time, `ScanBy` in either direction |
| EnableAlarmActions / DisableAlarmActions | F | an alarm with actions off still changes state, it just notifies nothing |
| TagResource / UntagResource / ListTagsForResource | F | on alarms and dashboards; CloudWatch's tag shape is a **list** of `{Key, Value}`, not the map CloudWatch Logs takes |
| PutDashboard / GetDashboard / ListDashboards / DeleteDashboards | C | the body is stored **verbatim** and never interpreted, so a caller diffing what it wrote against what it reads back sees no change it did not make; nothing renders it locally. `DeleteDashboards` is atomic, as on AWS |
| Metric math (`Metrics` on an alarm, `Expression` on a query) | S | a single `MetricStat` is accepted, which is what CDK emits for a threshold alarm; an expression needs an evaluator doze-aws does not have |
| Composite alarms (PutCompositeAlarm, DescribeAlarmContributors) | S | evaluate a rule over other alarms' states; doze-aws evaluates metric alarms only |
| Anomaly detection (PutAnomalyDetector, DescribeAnomalyDetectors, DeleteAnomalyDetector, `ThresholdMetricId`, the anomaly comparison operators) | S | need a trained band to compare against |
| Alarm warm-up (`WarmUpConfiguration`) | S | refused by name rather than ignored: a warm-up suppresses an alarm while a resource settles, and evaluating anyway would fire an alarm the caller asked to be held back |
| Insight rules (Put/Delete/Describe/Enable/Disable, managed rules, GetInsightRuleReport) | S | Contributor Insights reads an account's log volume |
| Metric streams (Put/Get/Delete/List, Start/Stop) | S | stream metrics to a Firehose that does not exist locally |
| Alarm mute rules (Put/Get/List/Delete) | S | suppress notifications on a schedule; not built |
| Datasets and OTel enrichment (GetDataset, Associate/DisassociateDatasetKmsKey), PutLogAlarm, GetMetricWidgetImage | S | need services or a renderer that do not run locally |

Every one of the 50 operations in the `GraniteServiceVersion20100801` model is
either handled (19) or refused by name with what it would need (31). Nothing
falls through to `InvalidAction`.

### Alarms

An alarm examines the last `EvaluationPeriods` periods and alarms when
`DatapointsToAlarm` of them breach — AWS's "M out of N", defaulting to N. The
evaluator runs every ten seconds.

**The window is lagged by one period, on purpose.** The period containing
"now" is still being written to, and judging a partial period produces an alarm
that flaps as observations arrive. AWS lags for the same reason. A freshly
created alarm therefore reads `INSUFFICIENT_DATA` until one period has closed.

**A missing period is a fourth answer, not a zero.** A period with no
observations is not a period that observed zero, so each is resolved by
`TreatMissingData` before the M-of-N count: `missing` (the default — neither
breaches nor clears), `notBreaching`, `breaching`, or `ignore` (keep the
current state). This matters more than it looks: with `notBreaching`, an empty
window resolves to a datapoint that did **not** breach, so an alarm over a
quiet metric settles on `OK` rather than staying at `INSUFFICIENT_DATA`.

A transition writes history and fires the actions for the state entered. An
SNS topic receives AWS's own alarm JSON — `AlarmName`, `NewStateValue`,
`OldStateValue`, `NewStateReason`, `StateChangeTime`, `Trigger` — so a
subscriber written against the cloud parses it unchanged.

### Differences from AWS

- **Retention is flat.** AWS keeps 1-second data for 3 hours, 1-minute for 15
  days and 1-hour for 63 days. doze-aws keeps everything for 24 hours
  (`[cloudwatch].retention`) and caps the sample count, because a local store
  is disposable and resolution-dependent expiry would only be a way to lose
  data mid-test.
- **Percentiles are exact**, computed from retained raw samples rather than
  from AWS's approximating sketch. A pNN here and a pNN on AWS will not agree
  to the last decimal; the local one is the more accurate of the two.
- **SQS, SNS, DynamoDB and Kinesis publish no built-in metrics**, and that is
  a decision rather than an omission. Their metrics are all measurements of
  scale — `ApproximateNumberOfMessagesVisible`, `ConsumedReadCapacityUnits`,
  `IncomingRecords` — and locally those numbers are whatever your own test just
  put there. An alarm on one would fire on a fixture rather than on a
  condition, which is worse than having no datapoint: a silent metric reads as
  `INSUFFICIENT_DATA`, and an alarm that says so is telling the truth.
  Kinesis's `EnableEnhancedMonitoring` and DynamoDB's Contributor Insights are
  stored and echoed but enable nothing, and say so where they are defined.
- **The evaluator ticks every ten seconds** rather than on AWS's own schedule,
  so a transition is visible in seconds instead of minutes.
- **No cross-account or cross-region metrics.** There is one account and one
  region locally, so `AccountId` on a query is accepted and ignored.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): the CBOR wire end to end — publish,
  list, chart, alarm, and the alarm's transition — through the SDK that
  actually speaks RPC v2.
- **aws-sdk-go v1** (`sdkv1_test.go`): the Query wire, including the
  `.member.N` flattening that made the list-dropping bug visible.
- **A raw JSON 1.0 test** (`threewire_test.go`) standing in for the AWS CLI,
  which is the wire most people's tooling uses.
- **Producers** (`producers_test.go`): a real Lambda invocation raises
  `AWS/Lambda` Invocations and Duration; an EMF line a function prints becomes
  a custom metric; a served API Gateway request raises Count and 4XXError; a
  workflow raises `AWS/States`; a log line matching a metric filter increments
  a metric; and a stack with CloudWatch disabled still runs all of them.
- **Alarms to SNS** (`alarms_test.go`): a queue subscribed to the alarm's topic
  receives AWS's alarm JSON.
- **CloudFormation** (`cloudformation/cloudwatch_apply_test.go`): a template
  with an alarm, a dashboard and a metric filter deploys, the alarm reaches
  `ALARM` through the real evaluator, and the lot round-trips through export →
  emit → transpile.
- **The console** (`internal/console/cw_test.go`): the pane's own handlers, asserted on
  rendered content rather than status.

### Input validation

**183/183 model-derived constraints enforced across the 19 dispatched
operations, on each of the three wires, with `knownGaps` empty.** Generated
with `dzaudit cases cloudwatch`, committed to `testdata/cases_cloudwatch.json`,
and replayed in `rejection_parity_test.go` from a baseline the test first
proves the service accepts — because a request refused for the WRONG reason
looks exactly like a pass.

One table serves all three wires: the paths describe the JSON shape, CBOR
decodes to it natively, and Query is rebuilt into it by `modelcheck.FromQuery`.
`MetricData[].Dimensions[].Name` is the load-bearing case — it only resolves on
the Query wire because `FromQuery` keeps `.member.` containers as lists, which
is a bug this suite found.

Sixteen cases are declared **out of scope** rather than replayed: they reach
into metric math and alarm warm-up, which are refused wholesale. Replaying one
would be this suite's own trap in its most convincing form — the request *is*
refused, so a runner records the constraint as enforced when it was refused for
the feature and never checked at all.

<!-- svc:dynamodb -->
## DynamoDB — API support

Full item model (S, N, B, BOOL, NULL, M, L, SS, NS, BS) with arbitrary-precision
numbers compared numerically. All five expression languages are really parsed
(lexer + recursive-descent): condition, filter, key-condition, update, and
projection expressions — including `#name`/`:value` substitution, document
paths (`a.b[0].c`), and unused-reference rejection.

| Operation | Tier | Notes |
|---|---|---|
| CreateTable | F | pk/sk schemas, GSIs + LSIs, tags; tables ACTIVE immediately (waiters pass first probe) |
| DescribeTable / ListTables / DeleteTable | F | DeletionProtection enforced |
| UpdateTable | F | GSI create (synchronous backfill) / delete, billing + protection round-trips |
| UpdateTimeToLive / DescribeTimeToLive | F | TTL enforced: lazy filtering on every read plus a janitor sweep through the normal delete path (indexes stay consistent) |
| PutItem / GetItem / DeleteItem | F | ConditionExpression, ReturnValues, ReturnValuesOnConditionCheckFailure (item inside the error), 400 KB size rule |
| UpdateItem | F | SET (arithmetic, list_append, if_not_exists), REMOVE (incl. list indexes), ADD (numbers + set union), DELETE (set subtraction); creates missing items from key attrs; key immutability enforced; ALL_OLD/ALL_NEW/UPDATED_OLD/UPDATED_NEW |
| Query | F | table or index; =, <, <=, >, >=, BETWEEN, begins_with sort conditions; FilterExpression (Limit counts pre-filter, like AWS); ScanIndexForward; paging via LastEvaluatedKey/ExclusiveStartKey; 1 MB page bound |
| Scan | F | filters, paging, Segment/TotalSegments |
| BatchGetItem / BatchWriteItem | F | 100/25 bounds; per-table projection support |
| TransactWriteItems | F | Put/Update/Delete/ConditionCheck, real single-node atomicity (one bbolt txn), CancellationReasons per item, ClientRequestToken idempotency (10-min window, IdempotentParameterMismatchException) |
| TransactGetItems | F | consistent multi-item read |
| TagResource / UntagResource / ListTagsOfResource | F | |
| DescribeLimits / DescribeEndpoints | F | canned values |
| ContinuousBackups / ContributorInsights describes+updates | C | fixed status round-trips |
| PartiQL (ExecuteStatement / BatchExecuteStatement / ExecuteTransaction) | F | INSERT, SELECT (a full key is a GetItem, anything else a filtered Scan), UPDATE and DELETE with `?` parameters, translated onto the classic operations so they share storage and conditional semantics; ExecuteTransaction runs as TransactWriteItems |
| Streams (DescribeStream / GetRecords / GetShardIterator / ListStreams) | F | one open shard per stream-enabled table; TRIM_HORIZON / LATEST / AT_ and AFTER_SEQUENCE_NUMBER iterators; Lambda event source mappings poll it |
| Global tables, DAX, Kinesis destinations | S | multi-region/cloud infrastructure |
| Backups / exports / imports / PITR restore | S | copy the data directory instead |

### Differences from AWS

- **Capacity is not modelled.** There is no provisioned throughput and no
  on-demand metering, so nothing is throttled, `ConsumedCapacity` is not
  meaningful, and code that retries on
  `ProvisionedThroughputExceededException` is not exercised.
- **No backups, exports or point-in-time restore.** Copy the data directory
  instead — it is the whole state, and that is a better local backup than
  anything this could emulate.
- **Streams are served, and nothing reads them but you.** The Streams API
  works and Lambda event source mappings consume it; there is no cross-region
  replication behind it, which is what global tables would need.
- **Encryption at rest is a description.** `SSESpecification` round-trips and
  `DescribeTable` reports it; items live in the data directory either way.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`, `coverage_test.go`): CRUD with condition
  and update expressions, query and scan including GSIs, batch and transaction
  operations, table administration, and PartiQL including its batch and
  transaction forms.
- **aws-sdk-go v1** (`sdkv1_test.go`): the round trip through the older client.
- **PartiQL under a fuzzer** (`partiql_fuzz_test.go`): `FuzzPartiQL` drives the
  parser at malformed statements, which have to be refused rather than
  mis-parsed or panic.
- **TTL** (`ttl_test.go`) and **Streams** (`streams_test.go`): expiry actually
  removes items, and the stream records it.
- **SSE round-trips** (`sse_test.go`), including that an unencrypted table
  reports no SSE description rather than an empty one.
- **Model-derived rejection parity** (`rejection_parity_test.go`) and the
  dispatch table against `testdata/ops_dynamodb.json`.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what DynamoDB refuses**. The two are different
promises, and the second is the one that decides whether code passing here also
passes on deploy.

Unlike SQS and S3, DynamoDB's own AWS service model carries the constraints as
traits, so this section is **generated rather than hand-derived**. `dzaudit
cases dynamodb` emits a violating value per constrained input;
`dynamodb/testdata/cases_dynamodb.json` commits them; and
`dynamodb/rejection_parity_test.go` replays every one.

**Every operation doze-aws dispatches is fully audited: 333/333 model-derived
constraints enforced across 27 operations, with `knownGaps` empty.**

The checks live in `dynamodb/validate.go` as one path-keyed table per
operation, run from the dispatcher before any handler sees the body. Putting
them there rather than inside each handler is what makes coverage a property of
the dispatch table instead of something every new handler has to remember.

| Operation | Constraints | Operation | Constraints |
|---|---|---|---|
| CreateTable | 68 ✅ | UpdateTable | 59 ✅ |
| TransactWriteItems | 27 ✅ | Scan | 18 ✅ |
| Query | 16 ✅ | UpdateItem | 12 ✅ |
| DeleteItem | 11 ✅ | PutItem | 11 ✅ |
| TagResource | 9 ✅ | ExecuteStatement | 8 ✅ |
| ExecuteTransaction | 8 ✅ | UpdateTimeToLive | 8 ✅ |
| GetItem | 7 ✅ | TransactGetItems | 7 ✅ |
| UpdateContinuousBackups | 7 ✅ | BatchExecuteStatement | 6 ✅ |
| DescribeContributorInsights | 6 ✅ | UntagResource | 6 ✅ |
| BatchGetItem | 5 ✅ | BatchWriteItem | 5 ✅ |
| ListTables | 5 ✅ | UpdateContributorInsights | 9 ✅ |
| DeleteTable | 3 ✅ | DescribeTable | 3 ✅ |
| DescribeTimeToLive | 3 ✅ | DescribeContinuousBackups | 3 ✅ |
| ListTagsOfResource | 3 ✅ | | |

#### Nested constraints, and the trap under them

Two thirds of these constraints are not on top-level members but inside
structures — `GlobalSecondaryIndexes[].Projection.ProjectionType`,
`TransactItems[].Put.Item`, `RequestItems{}[].DeleteRequest.Key`. Mutating one
means first sending a valid enclosing structure, which reopens the
wrong-reason-refusal problem one level down: if the *exemplar* standing in for
that structure is itself invalid, every case under it is refused for the
exemplar and the whole group reads as enforced when nothing was tested.

So exemplars are **probed**. For each container an operation's cases descend
through, the baseline carrying that exemplar and no mutation at all must still
be accepted. Two real defects surfaced this way and would otherwise have
shipped as false passes — one of them a constraint table that rejected every
valid `ComparisonOperator`, because `dzaudit list` elides enums over six
members for display and that elision had leaked into the emitted JSON.

#### What is not audited, and why it cannot be

| Scope | Cases | Status |
|---|---|---|
| The 27 dispatched operations | 333 | ✅ fully audited, all enforced |
| 29 stub operations | 360 | **un-auditable**: an honest `UnsupportedOperationException` refuses the baseline too, so replaying a mutation proves nothing |

The stubs are global tables and their replica auto scaling, backups,
exports/imports, Kinesis streaming, table resource policies (stored nowhere;
the IAM service reads identity policies only), `ListContributorInsights`
(CloudWatch) and `SearchVectors` (a vector index) — cloud infrastructure
rather than local behaviour, each refused by name with a reason. A frozen
list of the model's 58 operations (`model_coverage_test.go`) fails the build
if one is ever neither handled nor refused, so nothing answers a bare
`InvalidAction`.

An operation that refuses every request cannot be too permissive, so nothing is
hidden by this — but it is not the same statement as "audited", and this page
does not make the stronger one.

<!-- svc:eventbridge -->
## EventBridge — API support

Content-based routing is fully functional: the pattern language
(internal/eventpattern) implements exact/prefix/suffix/equals-ignore-case/
wildcard/anything-but/numeric/exists/cidr/$or, nested fields, and event-array
any-element matching. PutEvents synchronously matches enabled rules and
delivers to targets.

| Operation | Tier | Notes |
|---|---|---|
| PutEvents | F | validates entries, matches enabled rules, delivers to SQS, SNS, Lambda, CloudWatch Logs and API destination targets with Input/InputPath/InputTransformer shaping |
| PutRule | F | EventPattern rules; `rate(...)` and `cron(...)` schedules both driven by a local ticker (the six-field AWS cron: `?`, `L`, `W`, `#`, month and day names, UTC); a malformed expression is refused with AWS's message; a schedule is armed when first seen and never replays what a restart missed |
| DeleteRule / DescribeRule / ListRules | F | |
| EnableRule / DisableRule | F | |
| PutTargets / RemoveTargets / ListTargetsByRule / ListRuleNamesByTarget | F | SQS, SNS, Lambda, CloudWatch Logs log-group and API destination target ARNs; a log-group target writes the shaped event as one log line, in a stream named for the rule, as AWS does; an API destination target carries `HttpParameters` |
| CreateEventBus / UpdateEventBus / DeleteEventBus / DescribeEventBus / ListEventBuses | F | default bus implicit; custom buses; deleting a bus removes its rules. `Description`, `KmsKeyIdentifier` and `DeadLetterConfig` are stored and reported back but inert locally — Terraform tracks them on `aws_cloudwatch_event_bus`, so dropping them would be permanent drift |
| TestEventPattern | F | the same matcher, exposed for testing patterns |
| TagResource / UntagResource / ListTagsForResource | F | rule tags by ARN |
| CreateArchive / DescribeArchive / ListArchives / UpdateArchive / DeleteArchive | F | PutEvents appends matching events to the archive's log; retention stored but not actively expired |
| StartReplay / DescribeReplay / ListReplays | F | replays the windowed archive events back through the destination bus's rules (optionally filtered by rule ARN); runs synchronously → `COMPLETED` |
| CancelReplay | S | local replays complete synchronously, so there is never a running replay to cancel |
| CreateConnection / UpdateConnection / DeauthorizeConnection / DeleteConnection / DescribeConnection / ListConnections | F | BASIC, API_KEY and OAUTH_CLIENT_CREDENTIALS with invocation header/query/body parameters; secrets stay in the connection record and are never reported (see below); VPC Lattice connectivity parameters are refused by name |
| CreateApiDestination / UpdateApiDestination / DeleteApiDestination / DescribeApiDestination / ListApiDestinations | F | `http://` endpoints accepted so a local server can be a destination; `InvocationRateLimitPerSecond` stored and reported, not enforced; deleting a connection leaves its destinations `INACTIVE` |
| Partner event sources, global endpoints, cross-account permissions, schemas registry | S | cloud infrastructure |

### API destinations

A rule target whose ARN is an API destination makes a real HTTP request: the
destination's endpoint with its `*` segments filled from the target's
`HttpParameters.PathParameterValues`, the connection's invocation parameters
merged with the target's headers and query string, the shaped event as the
body (body parameters merge into it when it is a JSON object), and the
connection's credential applied — Basic, the API key header, or a bearer token
from a real client-credentials request to the connection's authorization
endpoint, cached until it expires.

Delivery runs off the request path, with a 5 second timeout and one retry a
second later on a transport error, a 429 or a 5xx; a failure is logged with the
rule, the destination and the status. AWS retries for 24 hours with backoff and
can park the event in a dead-letter queue; one retry is the honest local
budget, and there is no DLQ.

AWS stores a connection's secret in Secrets Manager under
`events!connection/<name>/<id>` and reports that `SecretArn`. doze-aws keeps
the secret in the connection record, reports the ARN AWS would mint so a
template's `GetAtt` resolves, and never returns a password, key value, client
secret or secret parameter from `DescribeConnection` — the same shape as AWS,
without the secret existing in the local Secrets Manager.

### Differences from AWS

- **Delivery is synchronous and in-process.** `PutEvents` matches every rule
  on the bus and delivers to its targets before returning, so there is no
  propagation delay and no at-least-once duplicate to defend against. A target
  that fails fails inside your `PutEvents` call.
- **Schedules tick at one second.** Cron and rate expressions are evaluated by
  a single driver against the clock, which is AWS's own resolution for
  minute-granularity rules and finer than it for nothing.
- **No partner sources, no global endpoints, no schema registry.** Each needs
  an account relationship, a second region, or a discovery service — none of
  which has a local shape.
- **Archive and replay are local.** Events are archived to the data directory
  and replayed from it, so the retention you set is bounded by the disk you
  have rather than by a service quota.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`, `coverage_test.go`): rule and bus
  administration, delivery to SQS, archive and replay, and input transformers.
- **aws-sdk-go v1** (`sdkv1_test.go`): the rule lifecycle through the older
  client.
- **API destinations** (`apidest_sdk_test.go`, `apidest_revoke_test.go`): real
  HTTP delivery with Basic, API-key and OAuth connections, the retry path, and
  — the one worth having — that deauthorizing a connection or deactivating a
  destination actually stops delivery rather than continuing with stale
  credentials.
- **Scheduling** (`scheduler_test.go`, `scheduled_delivery_test.go`): rate and
  cron parsing, which schedules are due, and a scheduled rule delivering the
  event it was supposed to deliver to the target it was pointed at.
- **Targets** (`logs_target_sdk_test.go`): a rule writing to a CloudWatch
  Logs group.
- **Refusals are named** (`bus_refusal_sdk_test.go`): an unsupported operation
  answers by name rather than falling through to a generic error.
- **Model-derived rejection parity** (`rejection_parity_test.go`) and the
  dispatch table against `testdata/ops_eventbridge.json`.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what EventBridge refuses**.

**448/448 model-derived constraints enforced across all 40 dispatched
operations, with `knownGaps` empty.** Removing the constraint table let the
majority of them through when that was last measured — at 438 cases, 284 of them
— so it is doing work the hand-written checks were not. That figure is from a
one-off experiment against an older fixture and has not been re-run; it is kept
because it is the only number here that says the audit *found* something rather
than *covered* something, and marked as dated rather than quietly restated
against a total it was not measured from.

Generated with `dzaudit cases eventbridge`, committed to
`testdata/cases_eventbridge.json`, and replayed case by case in
`rejection_parity_test.go` from a baseline the test first proves the service
accepts. The remaining model cases fall on operations with no handler — partner
sources, global endpoints, cross-account permissions — which cannot be audited
at all.

#### Targets carry fifteen nested parameter blocks

`PutTargets` is the widest input in the service: a target may carry
`EcsParameters`, `BatchParameters`, `RunCommandParameters`, `HttpParameters`,
`RedshiftDataParameters`, `SageMakerPipelineParameters`, `InputTransformer` and
more, each with its own required members and constraints. A case at
`Targets[].EcsParameters.Group` needs the whole chain above it to be *valid*, or
the request is refused for the missing chain rather than for the mutation. So
the harness carries an exemplar per container and probes each one against the
baseline before any case runs — `Targets[].RunCommandParameters` was caught this
way, missing its required `RunCommandTargets`.

#### CancelReplay has no acceptable baseline

doze-aws replays an archive synchronously inside `StartReplay`, so a replay is
`COMPLETED` the instant it exists and none is ever cancellable; every
`CancelReplay` request is refused, valid ones included. Rather than skip its
four cases, the harness requires the baseline to fail with exactly
`IllegalStatusException` — which proves it cleared validation — and then
requires every mutation to fail with `ValidationException` specifically. That
is stricter than the usual path, where any 4xx after a single mutation is
attributed to the mutation.

<!-- svc:iam -->
## IAM — API support

AWS documents **180** IAM operations. 176 are accounted for — handled, or
refused by name with the reason — and **four are not**: `AcquireRole`,
`GetAccountProperties`, `GetRoleTemplateVersion` and `PutAccountProperties`
answer a bare `InvalidAction`, which tells a caller their action was
unrecognised rather than that doze-aws does not serve it.

They are AWS additions that landed after this service was written, and they are
listed in `iam/coverage_model_test.go` so the gap cannot grow without the build
failing. That count used to read "all 176" because the total was typed by hand;
it comes from `testdata/ops_iam.json` now, which CI regenerates weekly.

### The three modes

IAM is the one service where full fidelity by default would be actively
hostile — turn enforcement on under an existing test suite and everything fails
at once. So enforcement is a dial:

| Mode | Behaviour | Cost |
|---|---|---|
| `off` | Full CRUD; every API works; nothing is evaluated. | None — the middleware is not installed at all |
| `soft` *(default)* | Every request is evaluated and recorded. Nothing is blocked; would-be denials are logged. | One evaluation per request |
| `enforce` | Denials are real and answer `AccessDenied`. | One evaluation per request |

```sh
doze-aws                        # soft: observe, never block
doze-aws --iam-mode enforce     # enforce
doze-aws --iam-mode off         # evaluate nothing
```

**Why soft is the default.** "It worked locally and 403s on deploy because the
Lambda permission was missing" is not a bug you suspect and then switch a flag
to investigate — it is a bug that ambushes you at deploy, and a diagnostic for
it only pays if it is already running. Soft can be the default because it
cannot refuse: both halves, the middleware and every service's guard, log and
return. And it is silent until it has something to say — with no IAM identities
created the caller is the account root, which is admitted, so a stack nobody
has written a policy for never prints a line.

Both LocalStack and moto default to permissive too. What doze-aws adds is the
middle rung being *useful* rather than merely quiet — and being the rung you
land on without asking.

### Least-privilege generation

Soft mode records every `(principal, action, resource)` tuple a workload
actually exercised. Two doze extension actions read that back:

| Action | Purpose |
|---|---|
| `DozeAccessLog` | Every recorded decision — principal, action, resource, verdict, count. `Reset=true` clears it. |
| `DozeGeneratePolicy` | Emits a policy document granting exactly what was used. `Principal=<arn>` filters; `ScopeToResources=true` produces one statement per resource. |

Run a test suite in soft mode, ask for the policy, commit it. `UnresolvedResources`
in the response counts the calls whose resource ARN could not be determined, so
a scoped policy never quietly pretends to be narrower than it is.

### Evaluation

The engine implements AWS's ordering exactly: an explicit `Deny` anywhere wins,
then any `Allow` grants, otherwise the request is implicitly denied. It covers
`Action`/`NotAction`, `Resource`/`NotResource`, wildcards (`*` and `?`),
permissions boundaries as a ceiling, and group-inherited policies for users.

Condition operators: `StringEquals`/`NotEquals`/`EqualsIgnoreCase`/`Like`/`NotLike`,
`Numeric*`, `Date*`, `Bool`, `IpAddress`/`NotIpAddress`, `Arn*`, `Null`, the
`...IfExists` suffix, and the `ForAllValues:`/`ForAnyValue:` set quantifiers.
Unknown operators never match rather than being guessed at.

Context keys supplied automatically: `aws:PrincipalArn`, `aws:PrincipalAccount`,
`aws:username`, `aws:SourceIp`, `aws:UserAgent`, `aws:SecureTransport`,
`aws:RequestedRegion`.

#### The resource boundary

Enforcement resolves the action from every request exactly — JSON services from
`X-Amz-Target`, Query services from `Action`, S3 and Lambda from method and
path. The **resource** is resolved where it can be read unambiguously (queue,
table, stream, key, secret, parameter, bucket, object, function) and left
**empty** otherwise.

An empty resource matches only `"Resource": "*"` statements. doze-aws will not
invent an ARN to make a scoped policy appear to match — a wrong allow or a wrong
deny is worse than an honest "could not determine". The access log marks these
with `ResourceKnown=false`.

Virtual-hosted-style S3 addressing is deliberately not decoded for resource
extraction, since splitting it correctly needs the configured S3 host.

### Resource policies

Seven services carry a policy on the resource itself — a bucket policy, a
queue policy, a topic policy, a function's permissions (`AddPermission`), a
key policy, a secret's resource policy, a stream's resource policy — and
under `soft` and `enforce` each one is evaluated **by the service that owns
the resource**, on every request that names it — so under the default they are
evaluated, and a policy that would have bitten you says so in the log. With IAM
`off` the policies are stored and returned and nothing consults them, which is
what every other local emulator does.

The rule is AWS's same-account rule:

- an explicit `Deny` in the identity policies or the resource policy denies;
- an `Allow` in either grants — a user with no identity policy at all gets
  in when the resource policy names it, and a user the identity policies
  allow gets in when the resource policy says nothing about it;
- otherwise the request is denied.

Naming the **account** (`arn:aws:iam::000000000000:root`, or the bare
account id — what SQS and SNS `AddPermission` write) admits the root
credentials and delegates to the identity policies of the account's users:
it does not, by itself, let an ungranted user in. Naming a user or role ARN
grants that identity directly. `NotPrincipal`, `*`, glob patterns on the ARN
and `Service` principals match as on AWS.

**KMS is the exception**, as it is on AWS: the key policy gates the identity
policies. An identity policy counts only while the key policy contains an
`Allow` for the account root covering the action — the default key policy's
one statement (`"Enable IAM policies"`). A key policy that names nobody in
the account locks everyone out, root and `PutKeyPolicy` included. That is
the real lockout AWS warns about, reproduced deliberately.

#### Service principals

A service calling a sibling on behalf of a resource — S3 delivering a
notification to Lambda or SNS, SNS fanning out to SQS or Lambda, EventBridge
invoking a target, Logs shipping to Kinesis or Lambda, API Gateway invoking
an integration, Secrets Manager invoking a rotation function — calls as
that service's principal (`s3.amazonaws.com`, `sns.amazonaws.com`, …) with
`aws:SourceArn` set to the resource it acts for. A service principal has no
identity policy, so only the target's resource policy can admit it.

**Under `enforce`, service-to-service triggers therefore need permissions
exactly as they do on AWS.** An S3 notification does not invoke a function
until `AddPermission` grants `s3.amazonaws.com` for the bucket; an SNS
subscription does not reach a queue until the queue policy allows
`sns.amazonaws.com` for the topic. A stack that worked with IAM off will
lose its triggers when switched to `enforce`, and the soft-mode log says
which permission is missing before that happens. Lambda destinations and
event-source-mapping polls are the exception: they run under the function's
execution role on AWS, which doze-aws does not model, and pass.

#### The handoff

The gateway middleware evaluates the identity policies and cannot evaluate
resource policies — it never sees a peer call, it resolves resources
loosely, and it can render only one error shape. The two halves meet
through request headers, a doze extension a client cannot forge (whatever a
client sends under `X-Doze-*` is stripped first):

| Header | Written by | Meaning |
|---|---|---|
| `X-Doze-Iam-Mode` | middleware | `soft` or `enforce` |
| `X-Doze-Principal` | middleware, or the peer transport | the caller: a user or role ARN, the account root, or a service principal |
| `X-Doze-Identity` | middleware | the identity verdict: `allowed`, `implicitDeny`, `root`, `service` |
| `X-Doze-Source-Arn` | peer transport | the resource a service call is made for |
| `X-Doze-Resource-Decision`, `-Matched-By` | the service, on the response | the combined verdict and the statement that decided, for the access log |

An implicit deny by the identity policies on a request naming a resource of
one of the seven services is not answered by the middleware; the service
finishes the question. An explicit deny, or an implicit deny on a request
naming no resource (`ListQueues`, `CreateKey`), is final at the middleware.
The access log records both halves: `Source=identity` for the middleware's
verdict, `Source=resource` for the service's, which is the one that counts.

#### What the services resolve for themselves

The middleware reads the resource off the wire loosely; a service that
knows better asks for the identity verdict again on the pair it means
(`X-Doze-Action`/`X-Doze-Resource` say what was evaluated). So a KMS key
named by alias, or inside a `Decrypt` ciphertext, is authorized on the key
ARN; `ReEncrypt` consults the source key for `kms:ReEncryptFrom` and the
destination for `kms:ReEncryptTo`; an S3 bucket addressed in the host is
authorized as the object request it is; a copy's source is authorized for
`s3:GetObject` against the source bucket's policy; a Lambda function named
by ARN or with a qualifier is authorized on that ARN; the batch operations
(`SendMessageBatch`, `PublishBatch`, ...) are authorized as the action they
batch, since the batch names are not IAM actions. A function URL, even with
`AuthType NONE`, needs a statement granting `lambda:InvokeFunctionUrl` to
everyone, as on AWS.

#### Not covered

Cross-account principals (there is one account; `aws:SourceAccount` is
always the local one, and `aws:SourceOwner` is never supplied, so SNS's
default topic policy, which conditions on it, delegates to identity policies
as it does on AWS), VPC endpoint policies, S3 access point policies, and
Lambda layer permissions (stored, not evaluated — a layer is fetched by the
function that names it, in the same account).

The handoff assumes the services run in one process, which the `doze-aws`
binary always does. An embedder wiring services over sockets
(`peers.UnixSockets`, `peers.FromEnv`) and putting a second stack's
middleware in front of them would have that middleware strip the peer's
service principal and treat the call as the root's; run the guard in each
service (`Options.IAMMode`) with no middleware between peers instead.

### Managed policies

AWS-managed policies are **synthesized from their naming convention** rather
than vendored. moto ships AWS's corpus as a multi-megabyte generated file;
doze-aws derives the same documents in a few hundred lines, and covers policies
for services that did not exist when the code was written.

| Name pattern | Document |
|---|---|
| `AdministratorAccess` | `*` on `*` |
| `ReadOnlyAccess` | `Get*`, `List*`, `Describe*`, `BatchGet*` |
| `PowerUserAccess` | `NotAction` excluding `iam:*`, `organizations:*`, `account:*` |
| `Amazon<Service>FullAccess` | `<prefix>:*` |
| `Amazon<Service>ReadOnlyAccess` | the read verbs, prefixed |
| `AWSLambda<Source>ExecutionRole` | the source's read actions plus `logs:*` |

A name matching no pattern answers `NoSuchEntity` rather than being invented, so
a template referencing a policy doze-aws cannot model fails loudly.

Customer-managed policies are stored properly, with up to five versions and the
same default-version and delete-guard rules as AWS.

### Operation support

| Operation | Tier | Notes |
|---|---|---|
| CreateUser / GetUser / UpdateUser / DeleteUser / ListUsers | F | paths, tags, rename; delete refuses while policies or keys remain |
| TagUser / UntagUser / ListUserTags | F | |
| CreateGroup / GetGroup / UpdateGroup / DeleteGroup / ListGroups | F | GetGroup returns members |
| AddUserToGroup / RemoveUserFromGroup / ListGroupsForUser | F | group policies are inherited during evaluation |
| CreateRole / GetRole / UpdateRole / UpdateRoleDescription / DeleteRole / ListRoles | F | trust policy validated at create time |
| UpdateAssumeRolePolicy | F | |
| TagRole / UntagRole / ListRoleTags | F | |
| CreateServiceLinkedRole / DeleteServiceLinkedRole | F | synthesized under `/aws-service-role/<service>/` |
| CreatePolicy / GetPolicy / DeletePolicy / ListPolicies | F | delete refuses while attached; `OnlyAttached` honoured |
| CreatePolicyVersion / GetPolicyVersion / DeletePolicyVersion / ListPolicyVersions | F | five-version ceiling; the default version cannot be deleted |
| SetDefaultPolicyVersion | F | |
| TagPolicy / UntagPolicy / ListPolicyTags | F | |
| Attach/Detach {User,Group,Role}Policy | F | idempotent; attachment counts tracked |
| ListAttached{User,Group,Role}Policies | F | |
| ListEntitiesForPolicy | F | `EntityFilter` honoured |
| Put/Get/Delete/List {User,Group,Role}Policy | F | inline policies, validated on write |
| Put/Delete {User,Role}PermissionsBoundary | F | enforced as a ceiling during evaluation |
| CreateAccessKey / ListAccessKeys / UpdateAccessKey / DeleteAccessKey | F | the key id is how enforcement resolves a principal |
| GetAccessKeyLastUsed | F | real data — the middleware records it per request |
| CreateInstanceProfile / GetInstanceProfile / DeleteInstanceProfile / ListInstanceProfiles | F | |
| AddRoleToInstanceProfile / RemoveRoleFromInstanceProfile / ListInstanceProfilesForRole | F | one role per profile, as in AWS |
| TagInstanceProfile / UntagInstanceProfile / ListInstanceProfileTags | F | |
| CreateAccountAlias / DeleteAccountAlias / ListAccountAliases | F | |
| GetAccountSummary | F | live counts |
| GetAccountAuthorizationDetails | F | full principal dump with inline and attached policies |
| SimulateCustomPolicy / SimulatePrincipalPolicy | F | the same engine enforcement uses, so they cannot disagree |
| GetContextKeysForCustomPolicy / GetContextKeysForPrincipalPolicy | F | real analysis of the referenced condition keys |
| GetAccountPasswordPolicy | F | answers `NoSuchEntity`, as AWS does for an account that never set one |
| DozeAccessLog / DozeGeneratePolicy | — | doze extensions; see above |
| MFA devices (8 operations) | S | there is no MFA fleet locally |
| SAML and OIDC providers (17 operations) | S | federation needs a real identity provider |
| Server, signing and SSH credentials (12 operations) | S | certificate material is cloud infrastructure |
| Login profiles and password policy (7 operations) | S | there is no console sign-in locally |
| Credential and access reports (5 operations) | S | derived from CloudTrail history |
| Organizations and delegation (16 operations) | S | there is no Organizations locally |
| Service-specific credentials (5 operations) | S | for CodeCommit and Keyspaces, which doze-aws does not serve |

### See also

- [../cloudformation.md](../cloudformation.md) — deploying with the AWS CLI, SAM, CDK or Serverless.
- [cli.md](../cli.md) — the `--iam-mode` flag.

### Differences from AWS

- **Evaluation is on, enforcement is opt-in.** The default mode is `soft`:
  every request that names a principal is evaluated and the verdict logged,
  and nothing is ever denied. `enforce` denies for real; `off` skips
  evaluation. That default is deliberate — an emulator that denies by surprise
  is one people turn off entirely.
- **One principal.** Every caller is the same local identity, so a policy is
  evaluated against one subject. Conditions that turn on who is asking cannot
  discriminate here, even though the policy language for them is implemented.
- **No federation, no console, no Organizations.** SAML and OIDC providers,
  login profiles and password policy, MFA devices, SSH and signing
  credentials, and the whole Organizations surface are refused by name. Each
  needs an identity provider, a sign-in page or an org tree that does not
  exist locally.
- **No credential reports.** They are derived from CloudTrail history, and
  there is none.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): user and role lifecycle, trust policies,
  managed policy versions, and the attach/detach surface.
- **The three modes** (`enforce_test.go`): `off` never denies, `enforce`
  allows and denies for real, and an explicit deny beats an allow — the rule
  everything else depends on.
- **Resource policies** (`resource_policy_test.go`, `resource_policy_2_test.go`,
  `resource_policy_audit_test.go`): queue, topic, bucket, key, secret and
  stream policies evaluated under `enforce`, key policies gating identity
  policies, and batch actions evaluated as their base action rather than as a
  separate one nobody wrote a policy for.
- **Spoofing** (`spoofing_test.go`): a client cannot supply its own
  `SourceArn` or the internal handoff headers — they are stripped on the way
  in, because a header a caller can set is not one anything should trust.
- **Model-derived rejection parity** (`rejection_parity_test.go`) and the
  dispatch table against `testdata/ops_iam.json`.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what IAM refuses**.

**702/702 model-derived constraints enforced across 89 of the 93 dispatched
operations, with `knownGaps` empty.** Removing the constraint table makes 319 of
those 702 cases slip through, so it is doing work the hand-written checks were
not.

Of the four with no cases, `GetAccountSummary` and `GetAccountPasswordPolicy`
take no constrained input, and `DozeAccessLog` and `DozeGeneratePolicy` are
doze-aws's own additions — they are not in AWS's model, so the model has nothing
to say about them.

Generated with `dzaudit cases iam`, committed to `testdata/cases_iam.json`, and
replayed case by case in `rejection_parity_test.go` from a baseline the test
first proves the service accepts. IAM speaks the Query protocol, so the harness
builds the nested shape the model's paths describe and flattens it into form
keys on the way out.

#### Nearly every operation needs its own resource

This is the widest audit here, and almost all of the harness is `prepare`. IAM
is a graph of named things that reference each other, and most of its operations
either create a name that must not already exist or consume one that must:

- A group `DeleteGroup` deletes is a group the next `DeleteGroup` case cannot.
- `UpdateUser` and `UpdateGroup` **rename**, so the old name is gone afterwards.
- A user is capped at two access keys and a managed policy at five versions, so
  `CreateAccessKey` and `CreatePolicyVersion` exhaust a shared fixture within a
  handful of cases.
- An instance profile holds at most one role.
- Every `Untag*` needs a tag to be there, and takes it.

Operations also run in alphabetical order, which is nothing like the order that
would make them work. So rather than one fixture that erodes as the suite runs,
each mutating case builds and addresses its own resource — and where the case is
*about* the field `prepare` would set, `prepare` leaves it alone, or the harness
would overwrite the violating value with a valid one and the case would prove
nothing.

Two names are read back from the response rather than recomputed in the test:
the access key id, and the role name `CreateServiceLinkedRole` generates. A test
that re-derives the service's own naming rule cannot catch that rule being
wrong.

<!-- svc:kinesis -->
## Kinesis — API support

doze-aws implements Kinesis Data Streams natively in Go. There is no JVM, no
Node sidecar and no third-party mock process — which is worth stating plainly,
because the usual local-Kinesis story is a downloaded Scala binary supervised by
the emulator.

The AWS JSON 1.1 protocol is served under the `Kinesis_20131202` target prefix.
Partition-key routing is the real algorithm — MD5 of the key read as a 128-bit
big-endian integer, matched against each shard's hash range — so a key lands on
the same shard number locally as it does in the cloud.

| Operation | Tier | Notes |
|---|---|---|
| CreateStream | F | provisioned (honours ShardCount) and on-demand (starts at 4 shards); shards tile the hash space with no gaps; a duplicate is ResourceInUseException, as in AWS |
| DeleteStream | F | drops records, shards and consumer registrations |
| ListStreams | F | prefix-free lexical order with ExclusiveStartStreamName + Limit; returns both StreamNames and StreamSummaries |
| DescribeStream | F | full shard list with hash ranges, sequence ranges, and parent/adjacent lineage; ExclusiveStartShardId honoured |
| DescribeStreamSummary | F | incl. OpenShardCount and live ConsumerCount |
| ListShards | F | ExclusiveStartShardId honoured; no pagination (local shard counts don't need it) |
| PutRecord | F | partition-key routing, ExplicitHashKey override, 1 MiB record ceiling, 256-char key limit |
| PutRecords | F | up to 500 records, written in one transaction — FailedRecordCount is always 0 because there is no local throttling to cause a partial write |
| GetRecords | F | Limit + 10 MiB response ceiling, MillisBehindLatest, null NextShardIterator plus ChildShards on a drained closed shard |
| GetShardIterator | F | TRIM_HORIZON, LATEST, AT_TIMESTAMP, AT_SEQUENCE_NUMBER (inclusive), AFTER_SEQUENCE_NUMBER (exclusive); iterators expire after 5 minutes with ExpiredIteratorException |
| SplitShard | F | closes the parent at the current end of stream and opens two children; split point validated against the parent's range |
| MergeShards | F | adjacency is enforced — merging a gap would silently drop hash space; the child names both parents |
| UpdateShardCount | F | UNIFORM_SCALING; closes every open shard and re-tiles, each child naming the parent that owned its starting hash |
| UpdateStreamMode | F | PROVISIONED ↔ ON_DEMAND |
| IncreaseStreamRetentionPeriod | F | rejects a request that would shorten the window |
| DecreaseStreamRetentionPeriod | F | rejects a request that would lengthen it; 24h–8760h bounds enforced |
| AddTagsToStream / RemoveTagsFromStream / ListTagsForStream | F | |
| TagResource / UntagResource / ListTagsForResource | F | the ARN-addressed tag API |
| RegisterStreamConsumer | F | duplicate registration is ResourceInUseException |
| DeregisterStreamConsumer / DescribeStreamConsumer | F | addressable by (StreamARN, ConsumerName) or by ConsumerARN |
| ListStreamConsumers | F | |
| StartStreamEncryption / StopStreamEncryption | C | the KeyId round-trips and is echoed in DescribeStream; records live in the data directory either way |
| EnableEnhancedMonitoring / DisableEnhancedMonitoring | C | shard-level metrics are stored and echoed; CloudWatch is local now, but nothing produces the `AWS/Kinesis` shard metrics this would turn on |
| DescribeLimits | C | reports live OpenShardCount against a nominal quota |
| DescribeAccountSettings / UpdateAccountSettings | C | no account-level quotas locally |
| UpdateMaxRecordSize | C | accepted; the 1 MiB ceiling on the put path is not raised |
| PutResourcePolicy / GetResourcePolicy / DeleteResourcePolicy | F | the stream's resource policy, evaluated on every request naming the stream under IAM `soft` and `enforce`; a Logs subscription filter writing to the stream needs a `Service: logs.amazonaws.com` statement under `enforce`, as on AWS. Round-trips only under the default `off` |
| SubscribeToShard | S | enhanced fan-out delivers over an HTTP/2 event stream; register the consumer and poll GetRecords instead |
| UpdateStreamWarmThroughput | S | a capacity hint with no local meaning |

### Retention

Records really expire. A background sweep (once a minute by default) drops
records past their stream's retention window and reclaims closed shards once
they have been fully drained, so a long-running local stack does not grow
without bound. `TRIM_HORIZON` starts below the oldest *surviving* record rather
than at sequence zero, so a consumer that reconnects after a sweep sees exactly
what is still there.

### Resharding and ordering

Resharding is the part of Kinesis where correctness is easy to fake and hard to
get right, so it is modelled properly. A shard is never mutated in place: it is
closed at the current end of the stream, keeps its records and gains an
`EndingSequenceNumber`, and children are opened to cover its hash range from
that point on.

A consumer therefore sees the real contract — drain the parent, receive a null
`NextShardIterator` and a `ChildShards` list, then start on the children. That
handover is what keeps records for a single partition key in order across a
reshard, and it is what the KCL relies on.

### Lambda event source mappings

A Lambda event source mapping whose `EventSourceArn` names a Kinesis stream is
polled per shard, with the shard list refreshed whenever a shard drains — so a
reshard mid-run is picked up without restarting the mapping. Events arrive in
the standard `aws:kinesis` record shape. Delivery is at-least-once and the
iterator advances regardless of the invocation result, matching the DynamoDB
stream poller.

Because doze-aws also implements DynamoDB, a KCL application's lease table works
against the same endpoint.

### Differences from AWS

- **Nothing throttles.** There are no shard-level quotas, so `PutRecords`
  answers `FailedRecordCount: 0` every time and a consumer never sees
  `ProvisionedThroughputExceededException`. Code that handles partial batch
  failure is not exercised here — that is the one thing worth knowing before
  trusting a local pass.
- **Enhanced fan-out is not served.** `SubscribeToShard` needs an HTTP/2 event
  stream; register the consumer and poll `GetRecords` instead, which is what
  the shared-throughput path does anyway.
- **Encryption is a label.** `StartStreamEncryption` round-trips the `KeyId`
  and `DescribeStream` echoes it; records live in the data directory the way
  every other service's do. The KMS key is checked to exist and to be usable
  — a disabled or pending-deletion key stops producers and consumers — but the
  records are not wrapped with it.
- **Account-level settings and warm throughput are accepted and inert**, since
  there are no account quotas locally to raise or report against.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): stream lifecycle, partition-key routing
  to the right shard, `ExplicitHashKey` overriding it, `PutRecords` batches,
  and resharding with the parent/child lineage a consumer walks.
- **The KMS boundary** (`kms_test.go`): an unknown key is refused, a disabled
  key stops producers and consumers, a key pending deletion is an invalid
  state, and a stream without encryption needs no KMS at all.
- **Model-derived rejection parity** (`rejection_parity_test.go`) and the
  dispatch table against `testdata/ops_kinesis.json`.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what Kinesis refuses**.

**356/356 model-derived constraints enforced across 32 of the 35 dispatched
operations, with `knownGaps` empty.** Before this table, 137 were enforced by
hand-written checks and 219 were not.

Generated rather than hand-derived: `dzaudit cases kinesis` emits a violating
value per constrained input, `kinesis/testdata/cases_kinesis.json` commits them,
and `kinesis/rejection_parity_test.go` replays every one from a baseline it
first proves the service accepts.

#### What is not covered, and why

| Operation | Cases | Why |
|---|---|---|
| `GetRecords` | 11 | needs a live shard iterator, which expires and is consumed by the read |
| `SplitShard` | 17 | reshapes the fixture stream every other operation addresses |
| `MergeShards` | 15 | reshapes the fixture stream every other operation addresses |

43 cases in total. They are skipped **with a reason recorded in the test**
rather than dropped, because a case nobody ran is not a case that passed. The
harness also counts unbuildable cases separately from enforced ones, so a hole
in the audit can never read as coverage.

`SubscribeToShard` and `UpdateStreamWarmThroughput` are refused by name with a
reason, so their 26 cases cannot be audited at all — an operation that refuses
every request, including a valid one, tells you nothing about its validation.

#### One behaviour change

An invalid `StreamName` or `ExplicitHashKey` now answers **`ValidationException`**
rather than `InvalidArgumentException`. Both carry a `@pattern` in the service
model, and AWS rejects a model-constraint violation at the protocol layer,
before the service sees it — the message is verbatim AWS's shape:

    1 validation error detected: Value 'bad name!' at 'streamName' failed to
    satisfy constraint: Member must satisfy regular expression pattern: ...

`InvalidArgumentException` remains what Kinesis answers for an argument that is
well formed but wrong, such as a numeric hash key outside the shard's range.

<!-- svc:kms -->
## KMS — API support

All three key families carry real standard-library crypto: symmetric keys are
AES-256-GCM with the encryption context as authenticated data; RSA/ECC keys
really sign, verify, and (RSA) encrypt; HMAC keys really MAC. GetPublicKey
returns genuine SPKI DER — signatures verify outside KMS.

| Operation | Tier | Notes |
|---|---|---|
| CreateKey | F | SYMMETRIC_DEFAULT, RSA_2048/3072/4096, ECC_NIST_P256/P384/P521, HMAC_224/256/384/512. ECC_SECG_P256K1 → honest error (no stdlib secp256k1) |
| DescribeKey / ListKeys | F | by id, ARN, alias, alias ARN |
| Encrypt / Decrypt | F | SYMMETRIC_DEFAULT (blob embeds key id; Decrypt needs no KeyId) and RSAES_OAEP_SHA_1/SHA_256 (KeyId required, like real KMS) |
| ReEncrypt | F | |
| GenerateDataKey(WithoutPlaintext) | F | AES_128/AES_256/NumberOfBytes |
| GenerateDataKeyPair(WithoutPlaintext) | F | RSA/ECC pairs, private key wrapped by the symmetric key |
| GenerateRandom | F | |
| Sign / Verify | F | RSASSA_PKCS1_V1_5_SHA_256/384/512, RSASSA_PSS_SHA_256/384/512, ECDSA_SHA_256/384/512; RAW and DIGEST message types |
| GetPublicKey | F | real SPKI DER |
| GenerateMac / VerifyMac | F | HMAC_SHA_224/256/384/512, constant-time compare |
| EnableKey / DisableKey | F | DisabledException on use |
| ScheduleKeyDeletion / CancelKeyDeletion | F | 7–30 day window, janitor finalizes; cancelled keys land Disabled |
| CreateAlias / UpdateAlias / DeleteAlias / ListAliases | F | |
| TagResource / UntagResource / ListResourceTags | F | |
| UpdateKeyDescription | F | |
| GetKeyPolicy / PutKeyPolicy / ListKeyPolicies | F | the key policy, evaluated on every request naming the key under IAM `soft` and `enforce`. As on AWS it gates the identity policies: they count only while the key policy allows the account root (the default policy's one statement), and a policy that names nobody locks everyone out, root and `PutKeyPolicy` included. Stored and returned only under the default `off` |
| EnableKeyRotation / DisableKeyRotation / GetKeyRotationStatus | F | symmetric keys only; `RotationPeriodInDays` stored and reported (AWS's default when omitted); the scheduled rotation itself is not run by a clock locally — RotateKeyOnDemand is the switch |
| RotateKeyOnDemand / ListKeyRotations | F | fresh backing material for a symmetric key, kept alongside the old so earlier ciphertexts still decrypt; the rotation list records each |
| Grants (Create/Retire/Revoke/List) | S | grants are IAM machinery |
| Custom key stores, ImportKeyMaterial, multi-region replication, DeriveSharedSecret | S | cloud-infrastructure-only |

### Differences from AWS

- **The crypto is real; the key custody is not.** Keys live in the data
  directory, not in an HSM, and anything with read access to that directory
  has the key material. That is the trade a local emulator makes, and it is
  why the data directory is the security boundary rather than the API.
- **Scheduled rotation has no clock.** `EnableKeyRotation` and
  `RotationPeriodInDays` are stored and reported, and nothing fires on the
  schedule — `RotateKeyOnDemand` is the switch, and it does the real thing.
  Symmetric keys only, as on AWS.
- **No key store you did not create here.** Custom key stores, imported key
  material and multi-region replicas are refused by name — each needs
  infrastructure (CloudHSM, an external key manager, another region) that has
  no local counterpart.
- **Every caller is the same principal**, so a key policy is evaluated against
  one identity. Under IAM `soft` it is evaluated and logged; under `enforce` it
  denies. What it cannot do is distinguish two callers.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): a symmetric round trip with encryption
  context, the data-key envelope, asymmetric sign/verify including RSA-PSS,
  HMAC, aliases and lifecycle, rotation, and `KeyUsage` actually enforced
  rather than recorded.
- **aws-sdk-go v1** (`sdkv1_test.go`): encrypt/decrypt and the error code the
  older clients branch on.
- **Administration** (`coverage_test.go`): key admin, rotation flags and
  policies, and data-key pairs.
- **Ciphertext under a fuzzer** (`blob_fuzz_test.go`): `FuzzOpenBlob` feeds
  corrupted and truncated blobs at the parser, which has to refuse them rather
  than mis-decrypt or panic.
- **Model-derived rejection parity** (`rejection_parity_test.go`), which needs
  more setup than any other service here — `Decrypt` needs ciphertext this key
  produced, `Verify` a signature over the message it is given — because an
  invented blob is refused for the wrong reason, which reads exactly like a
  pass.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what KMS refuses**.

**263/263 model-derived constraints enforced across all 36 dispatched
operations, with `knownGaps` empty.** Removing the table makes the audit fail,
so it is doing work the hand-written checks were not.

Generated with `dzaudit cases kms`, committed to `testdata/cases_kms.json`, and
replayed case by case in `rejection_parity_test.go` from a baseline the test
first proves the service accepts. 209 further cases fall on operations with no
handler — custom key stores, multi-region replication, imported key material —
which cannot be audited at all.

#### The fixtures are real cryptographic material

KMS needs more setup than any other service here, and none of it can be faked.
`Decrypt` needs ciphertext this key actually produced, `Verify` needs a
signature over the message it is given, `VerifyMac` needs a real MAC. An
invented blob is refused as invalid — a refusal for the wrong reason, which
reads exactly like a pass. So the fixture creates three keys (symmetric,
RSA_2048 for sign/verify, HMAC_256 for MACs) and then performs the operations
whose *output* the later baselines consume.

Three operations also get a throwaway key rather than the fixture's:
`ScheduleKeyDeletion`, `CancelKeyDeletion` and `DisableKey` would otherwise
disable the key every cryptographic baseline after them alphabetically depends
on.

<!-- svc:lambda -->
## Lambda — API support

Functions run as **real supervised local processes** speaking the AWS Lambda
Runtime API — no Docker, no image pulls. Each process is a child of doze-aws
with `AWS_LAMBDA_RUNTIME_API` pointing at a loopback server that hands it
invocations and takes back responses, which is exactly what the official
runtime interface clients and every `provided.*` bootstrap already speak. Up
to five processes per function serve concurrent invocations; an idle process
is reaped after `[lambda].idle-timeout` (10 minutes by default).

The user guide is [docs/lambda.md](../lambda.md). This page is the ledger.

### Runtimes

| Runtime | What runs | What the machine needs |
|---|---|---|
| `provided`, `provided.al2`, `provided.al2023`, `go1.x` | `./bootstrap` (or the handler's name) from the code directory, made executable if the zip dropped the bit | nothing |
| `python3.x` | `python3 <embedded client> <handler>` — a 200-line Runtime API client shipped inside doze-aws, materialised under `<data-dir>/shims`; `a/b/mod.fn` handlers, `LAMBDA_TASK_ROOT` on `sys.path`, the `logging` module wired the way `awslambdaric` wires it (request id on every record, JSON records under `AWS_LAMBDA_LOG_FORMAT=JSON`), a context object with every field AWS exposes | a `python3` on `PATH`, or `[lambda.runtimes] python = "/path"` |
| `nodejs*` | `node <embedded client> <handler>` — CommonJS and ESM (`.mjs`, `.cjs`, `package.json` `type`), nested handler paths, async and callback handlers, `uncaughtException` posted as an invocation error, console output prefixed the way Lambda prefixes it | a `node` on `PATH`, or `[lambda.runtimes] nodejs` |
| `ruby*` | `ruby <embedded client>` — `file.method` handlers taking `event:` and `context:` | a `ruby` on `PATH`, or `[lambda.runtimes] ruby` |
| `java*` | `java -cp <package jars> com.amazonaws.services.lambda.runtime.api.client.AWSLambda <handler>` — the AWS Java runtime interface client, which the package has to carry (`com.amazonaws:aws-lambda-java-runtime-interface-client`); CreateFunction refuses a package without it, naming the coordinate. The client's HTTP layer is a native library shipped for Linux, so on macOS the function needs a `Command` override | `java` |
| `dotnet*` | `dotnet <assembly>.dll` for a function published as a self-hosting executable (the project references `Amazon.Lambda.RuntimeSupport` and calls `LambdaBootstrap` from `Main`); a class-library handler is refused, naming the package and the change | `dotnet` |
| anything, any language | `Command: ["..."]` on CreateFunction or UpdateFunctionConfiguration (a doze extension) replaces the launch line entirely; the process still has to speak the Runtime API | whatever the command needs |

A missing interpreter is a warning at CreateFunction (AWS would accept the
function too) and a `Runtime.LaunchError` function error on the first invoke,
not a timeout. The interpreter's version is the host's: a `python3.12`
function runs on whatever `python3` is, which is the trade every host-process
emulator makes. Memory is not limited and `/tmp` is the host's.

The child's environment is Lambda's: `AWS_LAMBDA_RUNTIME_API`, `_HANDLER`,
`AWS_LAMBDA_FUNCTION_NAME`, `AWS_LAMBDA_FUNCTION_VERSION`,
`AWS_LAMBDA_FUNCTION_MEMORY_SIZE`, `AWS_LAMBDA_LOG_GROUP_NAME`,
`AWS_LAMBDA_LOG_STREAM_NAME`, `AWS_LAMBDA_INITIALIZATION_TYPE`,
`AWS_EXECUTION_ENV`, `LAMBDA_TASK_ROOT`, `LAMBDA_RUNTIME_DIR`, the region,
test credentials with a session token, `TZ=UTC`, the function's own
variables, and `AWS_ENDPOINT_URL*` pointing back at doze-aws so handlers reach
every sibling service unmodified. Each invocation carries
`Lambda-Runtime-Aws-Request-Id`, `-Deadline-Ms`, `-Invoked-Function-Arn`
(qualified when the invoke was), `-Trace-Id` and `-Client-Context`.

### Code

`Code.ZipFile` and `Code.S3Bucket/S3Key` (what `sam deploy` and `cdk deploy`
stage) are fetched and unpacked under the data dir, as on AWS. The doze
extension `Code.S3Bucket == "_local_"` with `S3Key` an absolute path to a
directory or binary runs the code **in place**: edit, invoke, no upload. A
warm process keeps the old code until its idle timeout or a
configuration update restarts it.

Container images are refused by name, on both the API and the CloudFormation
path — doze-aws runs functions as local processes and pulls no images. When
what you needed the image for was a binary or a toolchain rather than the
packaging, `Command` below is the way in.

### Command: run anything that speaks the Runtime API

`Command: ["..."]` on CreateFunction or UpdateFunctionConfiguration is a doze
extension, and it is the escape hatch for everything the runtime table above
does not cover. It **replaces the launch line entirely**: `Runtime` becomes a
label, no interpreter is resolved, and no embedded client is used.

```jsonc
{
  "FunctionName": "scorer",
  "Runtime": "provided.al2023",          // a label; Command decides what runs
  "Role": "arn:aws:iam::000000000000:role/r",
  "Code": { "S3Bucket": "_local_", "S3Key": "/abs/path/to/scorer" },
  "Command": ["/abs/path/to/scorer", "--serve"]
}
```

The one rule is the contract, not the language: the process has to speak the
**Lambda Runtime API**. It polls `GET $AWS_LAMBDA_RUNTIME_API/2018-06-01/runtime/invocation/next`
and posts back a response or an error, which is exactly what AWS's own runtime
interface clients do — `AWS_LAMBDA_RUNTIME_API` is in its environment, and a
function built for `provided.al2023` already does this unmodified.

Reach for it when:

- **the runtime has no mapping here** — Rust, a custom bootstrap, a language
  this table does not list;
- **the interpreter is not the host's** — a specific Python from a venv or a
  pinned toolchain, rather than whatever `python3` is;
- **a Java function on macOS**, where the AWS Java client's HTTP layer is a
  Linux-only native library (see the runtime table);
- **what a container image would have carried** — a binary with its own
  dependencies, invoked directly.

One thing it does not do: `Command` is stored with the function and used on
every launch, but **nothing reads it back**. `GetFunctionConfiguration` does
not report it, because AWS has no field to report it in, and no doze extension
exposes it either. It is visible where you set it, and otherwise only in what
the process turns out to be.

### Environment variables

`Environment.Variables` works as it does on AWS, on CreateFunction and
UpdateFunctionConfiguration, and reads back through GetFunctionConfiguration.

What is worth knowing is the **precedence**, because doze-aws injects more
into a function's environment than AWS does. The child's environment is built
in three passes, each overwriting the last:

1. Lambda's own set — `AWS_LAMBDA_FUNCTION_NAME`, `_HANDLER`,
   `LAMBDA_TASK_ROOT`, the region, test credentials, `TZ=UTC`;
2. the `AWS_ENDPOINT_URL*` variables that point a handler's SDK back at
   doze-aws, which is what makes a function reach its sibling services with no
   code change;
3. **the function's own variables**, which therefore win over both.

So setting `AWS_ENDPOINT_URL` yourself overrides the injected one — useful when
a handler should talk to something else, and the reason a function that
suddenly cannot reach SQS is worth checking here first. Layer search paths are
applied last but only ever appended, so a `PYTHONPATH` the function sets keeps
precedence over a layer's.

### Logs

Everything a function prints — stdout, stderr, init output, and Lambda's own
`START`/`END`/`REPORT` lines — is attributed to the invocation that printed
it and kept by the [CloudWatch Logs](logs.md) service under
`/aws/lambda/<name>`, in a stream per process named the way Lambda names
them. `aws logs tail /aws/lambda/<name> --follow`, `sam logs`, the SDKs'
FilterLogEvents and GetLogEvents, and the console's Logs tab all read it.
Every line is also echoed to doze-aws's own log as `lambda[<name>] <line>`
unless `[lambda].quiet = true` (`-lambda-quiet`). Invoke's `LogType: Tail`
still answers the last 4 KB in `X-Amz-Log-Result`, and every invocation
answers `X-Amzn-RequestId`.

Attribution follows Lambda's rule: output is credited to the current
invocation until the next one is polled, so a line printed after the
response belongs to the request that returned.

| Operation | Tier | Notes |
|---|---|---|
| CreateFunction / UpdateFunctionConfiguration / UpdateFunctionCode | F | zip, S3-staged and `_local_` packaging; env vars; DLQ/DestinationConfig; layers checked to exist; runtime checked for an interpreter (warns); `Command` extension |
| GetFunction / GetFunctionConfiguration / ListFunctions / DeleteFunction | F | a qualifier (`name:2`, `name:live`, `?Qualifier=`) answers the version or alias; `DeleteFunction?Qualifier=N` removes one version, deleting the function removes them all |
| Invoke (RequestResponse) | F | real process, `X-Amz-Function-Error` on a handler error, `X-Amz-Log-Result` tail, `X-Amzn-RequestId`, `X-Amz-Executed-Version`; `Qualifier` runs the version an alias or number names; `ClientContext` reaches the function |
| Invoke (Event) | F | async with configurable retries → DLQ / OnFailure destination |
| Invoke (DryRun) | F | 204 |
| Put/Get/Update/List/DeleteFunctionEventInvokeConfig | F | async destinations (OnSuccess/OnFailure → SQS/SNS/Lambda) + MaximumRetryAttempts (honored) / MaximumEventAgeInSeconds (stored) |
| PublishVersion / ListVersionsByFunction | F | a version freezes the code (copied under the data dir — a `_local_` directory too, so an in-place edit reaches `$LATEST` and not the version) and the configuration AWS snapshots; publishing an unchanged function answers the version it already has; `RevisionId` mismatch is a 412 |
| CreateAlias / GetAlias / ListAliases / UpdateAlias / DeleteAlias | F | name, version, description; a second create is a `ResourceConflictException`; an alias at a version that does not exist is refused. `RoutingConfig` weights are accepted and not stored: an alias runs one version |
| CreateFunctionUrlConfig / GetFunctionUrlConfig / UpdateFunctionUrlConfig / DeleteFunctionUrlConfig | F | **served**: a plain HTTP request to the URL becomes the payload-format-2.0 event and the answer is decoded by AWS's rule (`statusCode` object as a response, anything else as a 200 JSON body). Two addresses route: `<endpoint>/_aws/lambda-url/<id>/…` and `https://<id>.lambda-url.us-east-1.on.aws/` by Host. `AuthType` `NONE` and `AWS_IAM` are both served without a signature check; `InvokeMode` is `BUFFERED`; a config on a qualified ARN addresses the function |
| PutFunctionConcurrency / GetFunctionConcurrency / Delete | C | stored; no throttling locally |
| TagResource / UntagResource / ListTags | F | |
| CreateEventSourceMapping (SQS) | F | polls the queue, delivers batches (batch-size honored), delete-on-success, visibility-timeout retry on failure |
| Get/List/Update/DeleteEventSourceMapping | F | |
| DynamoDB/Kinesis event source mappings | F | both are polled for real — one iterator per shard for Kinesis, refreshed on reshard |
| AddPermission / RemovePermission / GetPolicy | F | the API behind `AWS::Lambda::Permission`; service and account principals, SourceArn/SourceAccount synthesized into ArnLike/StringEquals conditions. Under IAM `soft` and `enforce` the policy gates every request naming the function: an S3 notification, SNS delivery, EventBridge target or API Gateway integration calls as its service principal with `aws:SourceArn`, and does not invoke until a statement admits it, as on AWS |
| PublishLayerVersion / GetLayerVersion / GetLayerVersionByArn | F | inline `ZipFile`, S3-staged content, or `_local_` naming a zip or a directory laid out like an unpacked layer; **unpacked and put on the function's search paths** (below); `CodeSha256` is the zip's hash, or a content fingerprint for a `_local_` directory |
| ListLayers / ListLayerVersions / DeleteLayerVersion | F | newest-first ordering; ListLayers reports each layer's latest version |
| AddLayerVersionPermission / GetLayerVersionPolicy / RemoveLayerVersionPermission | F | |
| GetAccountSettings | F | live function count and code size against nominal limits |
| Container images, SnapStart, provisioned concurrency semantics, code signing | S | config accepted where trivial; execution semantics are cloud-only |

### Layers, without /opt

On AWS a layer is unpacked into `/opt` and the runtimes find it there. A
local process cannot be given a `/opt` without root, so each layer's
directories go onto the search paths the runtimes read — `python/` and
`python/lib/python3.x/site-packages` on `PYTHONPATH`, `nodejs/node_modules`
on `NODE_PATH`, `ruby/lib` on `RUBYLIB` and `ruby/gems/*` on `GEM_PATH`,
`bin/` on `PATH`, `lib/` on `LD_LIBRARY_PATH` and `DYLD_LIBRARY_PATH` — in
layer order, later layers first, which is the precedence AWS gives them.
`LAMBDA_LAYERS_DIRS` names the extracted directories for code that wants to
look. Code that opens `/opt/...` by literal path does not find it; that is
the one thing this cannot fake. A function whose `Layers` names a version
that does not exist is refused at create and update, as on AWS.

### Differences from AWS, in one place

- The interpreter is the host's; `python3.12` means the host's `python3`.
- No memory limit, no ephemeral-storage limit, no execution-role
  enforcement; `AWS_IAM` function URLs are served unsigned.
- Layers are on the search paths, not at `/opt`.
- Java needs the runtime interface client in the package and a Linux host
  (or a `Command`); .NET needs a self-hosting executable.
- Alias routing weights are accepted and not applied.
- Function URLs answer at the endpoint's `/_aws/lambda-url/<id>/` path as
  well as the on.aws host, because a local client cannot always set `Host`.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`, `coverage_test.go`): create and invoke,
  the asynchronous path and lifecycle, function management, event source
  mappings, and reserved concurrency of zero actually throttling.
- **aws-sdk-go v1** (`sdkv1_test.go`): create, invoke and delete through the
  older client.
- **Real interpreters** (`runtimes_sdk_test.go`): Python, Node and Ruby
  functions unpacked from a zip and run as host processes through the embedded
  Runtime API clients — and a missing interpreter said at CreateFunction
  rather than surfacing as a timeout on first invoke.
- **Logs end to end** (`logs_sdk_test.go`): a function's output reaching the
  CloudWatch Logs service, and a stack booted without that service still
  running the function and echoing its output.
- **Versions and aliases** (`versions_test.go`): a version freezes the code and
  the configuration, and a `_local_` in-place directory is copied so an edit
  reaches `$LATEST` and not the version.
- **Function URLs** (`urls_test.go`, `urls_iam_test.go`): served for real, and
  under IAM `enforce` needing the permission AWS would need.
- **Permissions and layers** (`policy_layers_test.go`): AddPermission with
  service and account principals, and the layer lifecycle including layers on
  the search path.
- **Package handling** (`treehash_test.go`): a zip whose tree hash would cost
  more than it is worth is refused rather than expanded — the decompression
  bound, checked at and under its limit.
- **Endpoint injection** (`endpoint_test.go`): the `AWS_ENDPOINT_URL*`
  variables a handler needs to reach its siblings, present when there is one
  and absent when there is not.
- **Model-derived rejection parity** (`rejection_parity_test.go`) and the
  dispatch table against `testdata/ops_lambda.json`.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what Lambda refuses**.

**612/621 model-derived constraints enforced across all 47 routed operations
with constrained input, with `knownGaps` empty.** Removing the constraint table
makes 431 of those 612 cases slip through — the largest share of any service
here, because Lambda's inputs are the widest: `CreateFunction` alone carries 76
constraints. The remaining nine cannot be put on this wire; see below.

#### A member the emulator ignores still has to be refused

`MemorySize` is the instructive one, and it was the first gap this service's
audit found. doze-aws does not allocate memory per function, so the value had no
local effect and nothing had ever looked at it — which is precisely why any
number was accepted. A function CloudFormation would reject deployed clean here
and failed in the account.

Generated with `dzaudit cases lambda`, committed to `testdata/cases_lambda.json`,
and replayed case by case in `rejection_parity_test.go` from a baseline the test
first proves the service accepts. Lambda speaks restJson1, so `validate.go`
carries a route table beside the constraint tables — the operation is the method
and the path, and has to be resolved before anything can be looked up.

#### The fixture is expensive, and each case gets its own

A function needs real deployable code, and a version, alias, layer, permission
or event source mapping needs a function first. The bootstrap is compiled once
and every throwaway function points at the same directory — the audit is about
what the service refuses, not what the handler prints. `Invoke`'s baseline is
the exception that needs it to genuinely run.

#### Nine cases cannot be expressed

Seven omit the **last** label of a URI, which does not produce an invalid
request — it produces a shorter path, which is a different valid operation. `GET
/2015-03-31/functions` is `ListFunctions`, not a `GetFunction` missing its name.
The same is true for `GetAlias`, `GetEventSourceMapping` and `GetLayerVersion`.

Two are `@httpHeader` members — `Invoke`'s `TenantId` and
`DurableExecutionName` — whose pattern violation is a control character. HTTP
forbids that in a header value, and Go's transport refuses to send the request
at all, so the service never sees it. AWS's own SDK is bound by the same rule.

Both are derived from the bindings rather than listed by hand, so an operation
added later cannot quietly acquire a case that tests nothing.

#### Not audited

Everything absent from the audit is absent from doze-aws: capacity providers,
durable executions, code signing configs as first-class resources, and the rest
of the cloud-only surface.

`UpdateAlias` used to be on this list — `/aliases/{Name}` answered GET and
DELETE, and the PUT the operation uses fell through to a 405. Repointing an
alias at a new version is the ordinary way a Lambda deploy goes live, so the gap
was on the main path. It is implemented now.

<!-- svc:logs -->
## CloudWatch Logs — API support

The slice of CloudWatch Logs a developer reads: log groups, streams and
events, written by the services that write them on AWS and read by
`aws logs tail --follow`, `sam logs`, the SDKs and the console. Every
producer writes through the same wire an SDK uses (PutLogEvents), so the
services run in one process or several:

| Producer | Group | What a line is |
|---|---|---|
| [Lambda](lambda.md) | `/aws/lambda/<function>` | every line a function prints, stamped with the request id of the invocation that printed it, one stream per process named the way Lambda names them |
| [Step Functions](stepfunctions.md) | the machine's `loggingConfiguration` destination, or `/aws/vendedlogs/states/<machine>` for an Express machine with logging off | one history event in AWS's vended JSON record, filtered by level |
| [API Gateway](apigateway.md) | the stage's `accessLogSettings` destination; `API-Gateway-Execution-Logs_<apiId>/<stage>` | one access-log line per request in the stage's `$context` format; the execution narrative at `INFO` or `ERROR` |
| [EventBridge](eventbridge.md) | a rule target's log group | the shaped event |
| [SNS](sns.md) | `sns/us-east-1/000000000000/<topic>` and its `/Failure` group, when the topic's delivery status attributes are set | one delivery attempt in AWS's record shape |
| an application | any group it creates | whatever it puts |

Every line a service writes carries a `requestId` (a doze extension) that
FilterLogEvents can select on, so the console shows one invocation, one
execution or one request without a filter pattern.

The service speaks AWS JSON 1.1 under the `Logs_20140328` target and signs
as `logs`. Events are kept for a day by default (`[logs].retention`), or for
the group's `retentionInDays` when one is set, and capped at 100,000 per
group; the store does not fsync per batch, because logs are disposable and a
per-invocation fsync is the one thing that would make Invoke slow.

| Operation | Tier | Notes |
|---|---|---|
| CreateLogGroup / DeleteLogGroup | F | `ResourceAlreadyExistsException` on a repeat; delete takes the streams and events with it |
| DescribeLogGroups / ListLogGroups | F | prefix, pattern and identifier filters; `limit` and a name-keyed `nextToken` |
| PutRetentionPolicy / DeleteRetentionPolicy | F | only the values AWS accepts (1, 3, 5, 7, 14, 30, … 3653); the sweeper honours them |
| CreateLogStream / DeleteLogStream / DescribeLogStreams | F | prefix, `orderBy LastEventTime`, `descending`, paging; first/last event and ingestion times are real |
| PutLogEvents | F | a first put on a stream nobody created creates it, so a function's first line never bounces; accepts a per-event `requestId` (doze extension) |
| GetLogEvents | F | forward and backward tokens; the end-of-stream token repeats, which is what stops an SDK paginator |
| FilterLogEvents | F | interleaved across streams in time order, `startTime`/`endTime`, stream names or prefix, `filterPattern`, `nextToken` only while more remain, unique `eventId`s — the contract `aws logs tail` and `sam logs` poll; `requestId` (doze extension) selects one invocation |
| TagResource / UntagResource / ListTagsForResource, TagLogGroup / UntagLogGroup / ListTagsLogGroup | F | the current and the deprecated spellings; the ARN form takes the ARN without the `:*` suffix DescribeLogGroups reports, as on AWS |
| Logs Insights (StartQuery, GetQueryResults, query definitions, scheduled queries, lookup tables, log fields and records) | S | a query engine that does not exist locally; FilterLogEvents covers what a developer reads |
| StartLiveTail | S | an HTTP event stream to a tailer fleet; `aws logs tail --follow` polls FilterLogEvents, which works |
| PutMetricFilter / DeleteMetricFilter / DescribeMetricFilters / TestMetricFilter | F | a matching line becomes a CloudWatch metric on ingest, beside the subscription fan-out; `metricValue` is a literal or a `$.field` reference, with `defaultValue` when the reference does not resolve; up to 100 filters per group, as on AWS |
| PutSubscriptionFilter / DeleteSubscriptionFilter / DescribeSubscriptionFilters | F | a group forwards the lines that match a filter pattern to a Lambda function or a Kinesis stream (see below); two per group, as on AWS; Firehose, cross-account destinations and a function's own log group are refused by name |
| Destinations (PutDestination, PutDestinationPolicy, DescribeDestinations, DeleteDestination) | S | cross-account receivers for another account's filters; subscribe a function or a stream directly |
| Deliveries, delivery sources and destinations, configuration templates | S | vended logs from other services; not built |
| Export and import tasks | S | S3 batch jobs; not built |
| Anomaly detectors, anomalies | S | a trained model over an account's logs; not built |
| Account, data-protection, index, storage-tier, resource and deletion-protection policies, bearer tokens | S | govern an account, not a local store |
| Transformers, integrations, S3 Table sources, KMS association, syslog configurations | S | need pipelines or services that do not run locally |

Every one of the 118 operations in the `com.amazonaws.cloudwatchlogs` model
is either handled (25) or refused by name with what it would need (93).
Nothing falls through to `InvalidAction`.

### Filter patterns

The subset people type into `sam logs --filter` and `--filter-pattern`:

- `ERROR` — a term; `ERROR timeout` — every term must appear.
- `"out of memory"` — a phrase.
- `-DEBUG` — a term that must not appear; `?ERROR ?WARN` — any of.
- `{ $.level = "error" }`, `{ $.status >= 500 && $.path = "/x*" }`,
  `{ $.a = 1 || $.b IS NULL }` — JSON patterns over a message that parses as
  JSON, with `=`, `!=`, `<`, `<=`, `>`, `>=`, `IS NULL`, `NOT EXISTS`,
  `IS TRUE`, `IS FALSE`, `*` wildcards in strings, `&&` binding tighter than
  `||`, no parentheses.
- `{ $.latency = * }` — an unquoted `*` tests existence rather than matching a
  string, which is how a metric filter says "every line carrying this field".
  `{ $.latency != * }` is the negation, the same as `NOT EXISTS`.

Regular-expression (`%…%`) and space-delimited (`[…]`) patterns answer
`InvalidParameterException` naming the construct. A subscription filter
takes the same language.

### Subscription filters

A filter on a group forwards every PutLogEvents batch's matching lines off
the request path, in the envelope AWS sends: gzip-compressed JSON with
`messageType`, `owner`, `logGroup`, `logStream`, `subscriptionFilters` and
`logEvents[{id, timestamp, message}]`, the ids 56-digit decimals as on AWS.
A Lambda function receives it base64-encoded under `awslogs.data` on an
asynchronous invoke, which is what a function written against AWS
gunzips; a Kinesis stream receives the raw gzip bytes as one record, keyed
by the log stream under `distribution: ByLogStream` and randomly otherwise.
A Kinesis stream must exist before the filter names it. Delivery is one
attempt; a failure is logged with the group, the filter and the destination.
A function cannot subscribe to its own `/aws/lambda/` group, because every
line it wrote would invoke it again.

### Differences from AWS

- **Retention defaults to a day**, not never. Set `retentionInDays` on the
  group, or `[logs].retention` in the config, for longer.
- **One stream per process** rather than per execution environment; the
  console shows the request id on every line, which is the join a reader
  needs.
- **No sequence tokens.** `PutLogEvents` accepts and ignores
  `sequenceToken`, as AWS has since 2023.
- **`GetLogEvents` scans the group's time range and keeps its stream**; at
  local volumes that is a non-cost, and it keeps FilterLogEvents — the hot
  path — a single cursor range.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): the CLI's own tail loop — FilterLogEvents
  polled with `startTime` advanced and deduplicated on `eventId` — receives
  every event exactly once across streams; GetLogEvents paginates and stops;
  the filter-pattern table; every typed error.
- **Lambda end to end** (`lambda/logs_sdk_test.go`): a function's stdout and
  stderr from a synchronous and an asynchronous invoke arrive under
  `/aws/lambda/<fn>` bracketed by START, END and REPORT; a stack started
  without the logs service still runs the function and echoes its output to
  the terminal.
- **CloudFormation** (`internal/cfn/regression_test.go`): `AWS::Logs::LogGroup`
  maps to a real group with its retention, where it used to be ignored.

### Input validation

**237/237 model-derived constraints enforced across all 25 dispatched
operations, with `knownGaps` empty.** Generated with `dzaudit cases
cloudwatch-logs`, scoped to the dispatched operations, committed to
`testdata/cases_logs.json`, and replayed in `rejection_parity_test.go` from a
baseline the test first proves the service accepts. The tag operations' ARN
pattern has no `*`, which is how the audit found that the ARN
DescribeLogGroups reports (ending `:*`) is not the one TagResource takes.

<!-- svc:s3 -->
## S3 — API support

**All 112 documented operations are accounted for: implemented, or refused with
a stated reason.** Nothing falls through to a different operation.

The total was 116 when it was written by hand and the model now documents 112 —
AWS has retired operations since, S3 Select among them. It comes from
`testdata/ops_s3.json` now, which CI regenerates weekly, so the next change is a
red build rather than a number nobody re-counted. The implemented/refused split
is deliberately not restated here: S3 dispatches by method and path rather than
by an action name, so unlike the fourteen action-dispatched services there is no
table to count, and a hand-split that adds up is not the same as one that is
checked.

Both addressing styles (path + virtual-hosted), both SDK generations, and the
full upload-body matrix: plain, UNSIGNED-PAYLOAD, signed aws-chunked
(STREAMING-AWS4-HMAC-SHA256-PAYLOAD), and trailer-checksum streaming
(STREAMING-*-TRAILER — aws-sdk-go-v2's default). Object bodies are streamed end
to end; memory use is independent of object size.

### Sub-resource routing, and why it is guarded

S3 is the one service that cannot be audited by reading a dispatch table,
because it has none. A request is identified by method, path shape, and a
query-string sub-resource marker — `?acl`, `?tagging`, `?ownershipControls`.

The natural way to write that dispatcher is a switch on the marker with a
default arm, and **the default arm is a different operation**. An unrecognised
marker therefore does not error, it silently becomes something else:

```
GET    /bucket?ownershipControls   → (before) returned an object listing
DELETE /object?annotation          → (before) DELETED THE OBJECT
```

Both were real behaviour. The second destroys data the caller never asked to
touch.

Every marker AWS defines is now enumerated. One this build does not implement
answers `501 NotImplemented` naming the operation, and never reaches a handler
that would do something else. That is the most important guarantee on this
page, and it is what the operation counts above are measured against.

### Objects

| Operation | Tier | Notes |
|---|---|---|
| PutObject / GetObject / HeadObject | F | metadata, standard headers, storage class; conditional reads (If-Match/None-Match/(Un)Modified-Since) and conditional writes (If-None-Match:*, If-Match); single-range requests (multi-range → 200 full body) |
| Flexible checksums | F | CRC32, CRC32C, CRC64NVME, SHA1, SHA256; header- or trailer-declared, verified server-side, echoed with x-amz-checksum-mode; SDK client-side validation passes |
| Content-MD5 | F | verified |
| DeleteObject / DeleteObjects | F | delete markers on versioned buckets, governance bypass header |
| CopyObject / UploadPartCopy | F | metadata and tagging directives, copy-source conditionals, versioned sources, source ranges |
| GetObjectAttributes | F | ETag, Checksum, ObjectSize, StorageClass |
| GetObjectTagging / PutObjectTagging / DeleteObjectTagging | F | x-amz-tagging header, tagging-count |
| GetObjectRetention / PutObjectRetention | F | GOVERNANCE (with bypass) and COMPLIANCE; deletion genuinely blocked |
| GetObjectLegalHold / PutObjectLegalHold | F | real hold enforcement |
| GetObjectAcl / PutObjectAcl | C | canned FULL_CONTROL owner answer; PUT accepted. No IAM evaluation on the S3 path |
| GetObjectTorrent | S | BitTorrent distribution is a cloud feature |
| RestoreObject | S | there is no Glacier tier locally |
| SelectObjectContent | S | S3 Select is a query engine, and AWS has discontinued it |
| RenameObject | S | a directory-bucket operation |
| UpdateObjectEncryption | S | objects are stored under the data directory, not KMS-enveloped |
| Object annotations (4 operations) | S | a metadata-table feature with no local store |

### Multipart upload

| Operation | Tier | Notes |
|---|---|---|
| CreateMultipartUpload / UploadPart / UploadPartCopy | F | part-size and ordering validation |
| CompleteMultipartUpload | F | `-N` composite ETags; COMPOSITE and FULL_OBJECT checksum types |
| AbortMultipartUpload / ListParts / ListMultipartUploads | F | |

### Buckets

| Operation | Tier | Notes |
|---|---|---|
| CreateBucket / DeleteBucket / HeadBucket / ListBuckets / GetBucketLocation | F | naming rules enforced; delete requires empty |
| ListObjects (V1) / ListObjectsV2 | F | prefix, delimiter/CommonPrefixes, paging (marker / continuation token), fetch-owner |
| ListObjectVersions | F | with key and version-id markers |
| GetBucketVersioning / PutBucketVersioning | F | Enabled/Suspended, null versions, get/delete by versionId |
| GetBucketTagging / PutBucketTagging / DeleteBucketTagging | F | |
| GetBucketCors / PutBucketCors / DeleteBucketCors | F | config CRUD plus real preflight evaluation (wildcard origins, methods, headers, expose, max-age) and response decoration |
| GetBucketLifecycle(Configuration) / PutBucketLifecycle(Configuration) / DeleteBucketLifecycle | F | Expiration, NoncurrentVersionExpiration and AbortIncompleteMultipartUpload enforced by a janitor; storage-class transitions accepted but never acted on (one storage class locally) |
| GetBucketWebsite / PutBucketWebsite / DeleteBucketWebsite | F | index and error documents really serve, on directory requests and 404s |
| GetBucketNotification(Configuration) / PutBucketNotification(Configuration) | F | delivers to SQS, SNS and Lambda with prefix/suffix filters |
| GetObjectLockConfiguration / PutObjectLockConfiguration | F | |
| Presigned URLs | F | SigV2 and SigV4 forms accepted, expiry enforced (the signature itself is not verified — this is a local emulator) |
| GetBucketAcl / PutBucketAcl | C | as for objects |
| GetBucketPolicy / PutBucketPolicy / DeleteBucketPolicy | F | the bucket policy, evaluated on every bucket and object request under IAM `soft` and `enforce` (a denial is S3's `AccessDenied`); stored and returned only under the default `off`. A policy that grants to everyone is refused with `AccessDenied` while the bucket's public access block has `BlockPublicPolicy` on — the one check S3 makes on the put itself |
| GetBucketEncryption / PutBucketEncryption / DeleteBucketEncryption | C | SSE headers echoed on objects; bytes live under the data directory either way |
| GetBucketReplication / PutBucketReplication / DeleteBucketReplication | C | there is one region locally |
| GetBucketLogging / PutBucketLogging | C | no access-log delivery locally |
| GetBucketAccelerateConfiguration / PutBucketAccelerateConfiguration | C | no edge network locally |
| GetBucketRequestPayment / PutBucketRequestPayment | C | nothing is billed locally |
| Analytics configuration (4 operations) | S | analytics exports read a real account |
| Inventory configuration (4 operations) | S | inventory reports read a real account |
| Metrics configuration (4 operations) | S | request metrics need an `AWS/S3` producer; CloudWatch is local now, but S3 publishes nothing to it |
| Intelligent-tiering configuration (4 operations) | S | there are no storage tiers locally |
| Metadata and metadata-table configuration (9 operations) | S | metadata tables are a managed analytics feature |
| GetBucketOwnershipControls / PutBucketOwnershipControls / DeleteBucketOwnershipControls | C | the `ObjectOwnership` rule is stored, read back and deleted (`OwnershipControlsNotFoundError` when absent); ACLs are canned here, so there is nothing for it to change |
| GetPublicAccessBlock / PutPublicAccessBlock / DeletePublicAccessBlock | F | a new bucket is born with all four blocks on, as on AWS since 2023; `BlockPublicPolicy` refuses a public bucket policy; the three ACL flags are stored and reported (ACLs are canned); delete removes the configuration and reads back as `NoSuchPublicAccessBlockConfiguration` |
| GetBucketPolicyStatus | F | computed from the stored policy: an `Allow` statement with `Principal` `*` (or `{"AWS":"*"}`) and no `Condition` makes the bucket public; `NoSuchBucketPolicy` when there is no policy |
| GetBucketAbac / PutBucketAbac | S | attribute-based access control needs IAM on the S3 path |
| CreateSession / ListDirectoryBuckets | S | directory buckets are an express-zone feature |
| WriteGetObjectResponse | S | S3 Object Lambda is a cloud feature |

### The recurring boundary

The reason that appears most often in the stub column is **the access-control
surface beyond the bucket policy**. The bucket policy itself is evaluated by S3
on every bucket and object request under IAM `soft` and `enforce` (the IAM
ledger's "Resource policies" section has the rule); object ACLs, ownership
controls and access points are not — they round-trip so that templates and SDK
code paths that set them keep working, and grant or deny nothing. The public
access block has a local effect of its own: `BlockPublicPolicy` refuses a
public policy at the put, which is the mistake it exists to catch, and
`GetBucketPolicyStatus` says whether the stored policy is public.

"Public" is decided by the policy engine (`internal/iampolicy`.`IsPublic`),
not by a second reader of the same JSON: a statement grants the public when
its principal is `*` and no condition on a caller-identifying key —
`aws:SourceIp`, `aws:SourceArn`, `aws:PrincipalOrgID` and the rest — narrows
it. A condition that restricts *when* rather than *who*, such as
`aws:SecureTransport` or `s3:prefix`, leaves the grant public, as on AWS. An
explicit `Deny` cancels the grant when it is unconditional, names everyone,
and is at least as broad as the `Allow`; a narrower, conditional or
principal-specific `Deny` does not, because it does not close the grant for
the caller who matters. Where the rule is uncertain doze-aws answers
"public", so `BlockPublicPolicy` may refuse a policy AWS would have taken —
the safe direction for a setting whose job is catching an exposed bucket.

### See also

- [iam.md](iam.md) — the policy engine, and what it does and does not cover.
- [../cloudformation.md](../cloudformation.md) — `AWS::S3::Bucket` support.

### Differences from AWS

- **ACLs are cosmetic, and that is the one to know.** `PutObjectAcl` is
  accepted and `GetObjectAcl` answers a canned FULL_CONTROL owner; nothing on
  the object path is gated by an ACL. Bucket policies and the public access
  block are real — a public policy is refused when the block says so — but
  per-object ACLs are not an access-control mechanism here.
- **One storage class.** There is no Glacier tier, so `RestoreObject` is
  refused rather than answered instantly, and lifecycle transitions between
  classes have nothing to transition to. Expiration does run.
- **Objects are files.** They live under the data directory rather than
  KMS-enveloped, which is why `UpdateObjectEncryption` is a stub: there is no
  envelope to update. SSE headers round-trip.
- **No directory buckets and no Object Lambda.** `CreateSession`,
  `RenameObject` and `WriteGetObjectResponse` belong to express zones and
  Object Lambda, neither of which has a local shape.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`, `coverage_test.go`, `coverage2_test.go`):
  the put/get round trip, the full checksum and range matrix, versioning and
  suspended versioning, multipart, tagging, CORS and preflight, and copy-to-self
  refused the way AWS refuses it.
- **aws-sdk-go v1** (`sdkv1_test.go`): the round trip and presigned GET and PUT,
  which the v1 clients generate differently.
- **Public access** (`access_public_test.go`, `access_sdk_test.go`): AWS's own
  rule for whether a policy is public, statement by statement, and the block
  gating a policy that is.
- **Routing** (`subresource_test.go`, `vhost_removed_test.go`): an unknown
  subresource does not fall through to the object path — the failure mode that
  turns `?acl` into a key called `acl` — and a request that lost its
  virtual-host prefix says so rather than 404ing quietly.
- **Serving** (`features_test.go`, `notify_test.go`): website serving,
  lifecycle expiration, and an event notification actually reaching SQS.
- **Model-derived rejection parity** (`rejection_parity_test.go`), with the
  60 cases that cannot be put on this wire derived rather than listed.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what S3 refuses**.

**236/296 model-derived constraints enforced across all 74 routed operations,
with `knownGaps` empty.** The other 60 cannot be put on this wire at all, and
both rules for that are derived rather than listed. Removing the constraint
table makes 171 of those 236 cases slip through.

Generated with `dzaudit cases s3`, committed to `testdata/cases_s3.json`, and
replayed case by case in `rejection_parity_test.go` from a baseline the test
first proves the service accepts.

#### Three places at once, and the operation named in none of them

S3 speaks restXml. A single operation's input is spread across the path, the
query string, the headers and an XML document, and the operation itself is the
method, the path shape and a query sub-resource marker together — the same
hazard `subresource.go` exists to guard against. So `validate.go` carries a
route table generated from AWS's own model beside the constraint tables.

Three things the model had to supply because the wire does not:

- **List element names.** `Tagging.TagSet[].Key` is
  `<Tagging><TagSet><Tag><Key/></Tag></TagSet></Tagging>`, and nothing in the
  document says whether one `<Tag>` is a scalar or a one-element list.
- **Renamed members.** `LifecycleConfiguration.Rules` is spelled `<Rule>`;
  `AccessControlPolicy.Grants` is `<AccessControlList>`.
- **Attributes.** A grantee's type is `<Grantee xsi:type="CanonicalUser">`, an
  attribute rather than an element. The validator reads both — reading only
  elements would have reported a valid SDK request as missing a required member.

An element written empty is an empty *structure*, not an empty string: removing
`Suffix` leaves `<IndexDocument/>`, and reading that back as text means the
required member inside it is never looked for.

#### Identifying the operation

Three things beyond the path do it, and each was a bug before it was a rule:

- **Sub-resource markers exclude as well as select.** A route that declares no
  marker must not claim a request that carries one. Without that, `ListObjects`
  answered `GET /bucket?legal-hold` with a bucket listing — precisely the
  fall-through `subresource.go` was written to prevent, arriving through a
  different door.
- **Some headers and query parameters are identity, not input.**
  `PUT /{Bucket}/{Key+}` is CopyObject when `x-amz-copy-source` is present and
  PutObject when it is not; `POST` on that path is CompleteMultipartUpload only
  with `?uploadId`. This is required only where two routes would otherwise
  collide — demanding it elsewhere would mean a request that omits the member
  resolves to nothing and escapes the validation that should have refused it.
- **`x-id` is a tiebreaker of last resort.** AWS's SDK sends it to make a
  request cacheable; boto3 does not. It is honoured only where a group of routes
  has nothing else to separate them (`GET /` — ListBuckets and
  ListDirectoryBuckets), and even there one route stays unmarked so a client
  that omits it still lands somewhere.

The path is matched in two passes: exact first, then one that allows a trailing
`{Key+}` to be empty. Order matters both ways. Without the second pass
`GET /bucket?retention` matches nothing and is never validated; with it running
first, `DELETE /bucket?tagging` would be claimed by DeleteObjectTagging with an
empty key and a legitimate DeleteBucketTagging refused.

#### The 60 that cannot be expressed

Two derived rules, no hand-kept list:

- **Emptying the last label shortens the path into a different operation.**
  `GET /bucket?tagging` is GetBucketTagging, not a GetObjectTagging missing its
  key. At the service root it is stronger still: `/` is routed by method alone,
  so `GET /?tagging` is ListBuckets.
- **Omitting an identifying header or query parameter does the same.** `PUT /b/k`
  without `?uploadId` really is a PutObject, so UploadPart's required `UploadId`
  has nothing to be refused for.

<!-- svc:secretsmanager -->
## Secrets Manager — API support

Secret values (string and binary) are genuinely encrypted at rest with a
per-data-dir AES-256-GCM key; the KMS KeyId is recorded and returned
cosmetically.

| Operation | Tier | Notes |
|---|---|---|
| CreateSecret | F | ClientRequestToken idempotency, tags, ResourceExistsException on conflict |
| GetSecretValue | F | by name or ARN; VersionId / VersionStage (default AWSCURRENT); deleted secrets refuse with InvalidRequestException |
| BatchGetSecretValue | F | per-secret error entries |
| PutSecretValue | F | stage movement: new AWSCURRENT demotes the old one to AWSPREVIOUS |
| UpdateSecret | F | description/kms + optional new version |
| DeleteSecret | F | RecoveryWindowInDays 7–30 (default 30) → janitor purge; ForceDeleteWithoutRecovery immediate |
| RestoreSecret | F | |
| ListSecrets | F | IncludePlannedDeletion flag |
| DescribeSecret / ListSecretVersionIds | F | version→stages maps |
| UpdateSecretVersionStage | F | a stage names at most one version |
| TagResource / UntagResource | F | |
| GetRandomPassword | F | length, ExcludeCharacters, ExcludePunctuation |
| PutResourcePolicy / GetResourcePolicy / DeleteResourcePolicy | F | the secret's resource policy, evaluated on every request naming the secret under IAM `soft` and `enforce` by AWS's same-account rule (see the IAM ledger, "Resource policies"); stored and returned only under the default `off` |
| ValidateResourcePolicy | C | always passes |
| RotateSecret / CancelRotateSecret | F | RotateSecret invokes the configured rotation Lambda synchronously for the four steps (createSecret, setSecret, testSecret, finishSecret); the function moves the version stages, as on AWS. CancelRotateSecret clears the pending rotation |
| ReplicateSecretToRegions / RemoveRegionsFromReplication / StopReplicationToReplica | S | exactly one region locally |

### Differences from AWS

- **One region, so no replicas.** `ReplicateSecretToRegions` and its siblings
  are refused by name rather than pretended: a replica needs a second region
  to exist.
- **`ValidateResourcePolicy` always passes.** It is a linting service on AWS;
  locally there is nothing behind it to lint against, and failing a policy
  that AWS would accept is the worse error.
- **Rotation runs your Lambda, and that is all it runs.** `RotateSecret`
  invokes the function through the four rotation steps; there is no managed
  rotation template and no scheduled trigger, so rotation happens when it is
  asked for.
- **Values are encrypted with a local key** in the data directory rather than
  by KMS, with the same honest limit as SSM's: encrypted at rest, not
  protected from anything that can read the directory.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): the secret lifecycle, the deletion
  recovery window, partial-ARN lookup the way the console does it, version
  stages and ids, binary values, tags and generated passwords, and the rule
  that only one version may be `AWSPENDING`.
- **aws-sdk-go v1** (`sdkv1_test.go`): the secret round trip through the older
  client.
- **Rotation end to end** (`rotate_test.go`): a real Lambda function driven
  through `createSecret`, `setSecret`, `testSecret` and `finishSecret`, with
  the stage moves each step is supposed to make.
- **Model-derived rejection parity** (`rejection_parity_test.go`) and the
  dispatch table against `testdata/ops_secrets-manager.json`.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what Secrets Manager refuses**.

**132/132 model-derived constraints enforced across 19 of the 20 dispatched
operations, with `knownGaps` empty.** Before this table, 54 were enforced by
hand-written checks and 78 were not.

Generated rather than hand-derived: `dzaudit cases secrets-manager` emits a
violating value per constrained input, `testdata/cases_secretsmanager.json`
commits them, and `rejection_parity_test.go` replays every one from a baseline
it first proves the service accepts.

`RotateSecret`'s 20 cases are skipped with a reason recorded in the test:
rotation invokes a Lambda, and the audit boots this service alone. Three
operations — `ReplicateSecretToRegions`, `RemoveRegionsFromReplication`,
`StopReplicationToReplica` — have no handler and so cannot be audited at all;
an operation that refuses every request tells you nothing about its validation.

<!-- svc:sns -->
## SNS — API support

Served over the SNS Query/XML protocol. Delivery is synchronous and fans out
to SQS queues (raw or enveloped, via the peers directory) and to http(s)
webhooks with the SubscriptionConfirmation handshake.

| Operation | Tier | Notes |
|---|---|---|
| CreateTopic | F | idempotent; attributes + tags merge on re-create |
| DeleteTopic | F | drops the topic's subscriptions too |
| ListTopics | F | |
| GetTopicAttributes | F | live subscription counts + stored attribute round-trips |
| SetTopicAttributes | C→F | attributes stored and returned (DisplayName, Policy, ...); most have no local behavior. The delivery status attributes do: `SQS`/`Lambda`/`HTTP` `SuccessFeedbackRoleArn` turns on one JSON record per successful delivery to the [CloudWatch Logs](logs.md) group `sns/us-east-1/000000000000/<topic>`, sampled by `*SuccessFeedbackSampleRate` (0–100, default 100); `*FailureFeedbackRoleArn` writes every failed attempt (a refused SQS send, a Lambda that could not be invoked, a non-2xx from a webhook) to `<group>/Failure`, in AWS's record shape (`notification`, `delivery` with destination, provider response, dwell time and status code, `status`). The role itself gates nothing: setting it is the switch |
| TagResource / UntagResource / ListTagsForResource | F | |
| Subscribe | F | sqs (auto-confirmed), http/https (confirmation handshake); RawMessageDelivery, FilterPolicy; other protocols stored but undeliverable locally (logged) |
| ConfirmSubscription | F | by token, incl. the SubscribeURL flow |
| Unsubscribe | F | |
| ListSubscriptions / ListSubscriptionsByTopic | F | |
| GetSubscriptionAttributes / SetSubscriptionAttributes | F | RawMessageDelivery + FilterPolicy live; others round-trip |
| Publish | F | filter-policy evaluation, raw + enveloped delivery, message attributes |
| PublishBatch | F | per-entry subjects and message attributes |
| AddPermission / RemovePermission | F | AddPermission writes the statement AWS writes into the `Policy` attribute (Sid = Label, the account roots as principals, `SNS:<action>` on the topic; a label in use is refused), RemovePermission drops it by label. The policy — a topic is born with AWS's default one — is evaluated on every request naming the topic under IAM `soft` and `enforce`; an S3 notification into the topic needs a `Service: s3.amazonaws.com` statement under `enforce`, as on AWS |
| PutDataProtectionPolicy / GetDataProtectionPolicy | C | stored and returned; not evaluated |
| Mobile push (Platform applications/endpoints), SMS + sandbox, phone-number opt-out ops | S | carrier/platform infrastructure cannot exist locally; each answers a clean coded error |

Filter policies share EventBridge's pattern engine (`internal/eventpattern`),
so the full operator set applies: exact match, `prefix`, `suffix`,
`anything-but`, `numeric` ranges, `exists`, `wildcard`, and `$or`. Policies
are matched against message attributes; `FilterPolicyScope: MessageBody` is
stored and reported but the body is not matched — a body-scoped policy is
applied as if attribute-scoped.

### Differences from AWS

- **Delivery is synchronous.** A `Publish` fans out to its subscriptions
  before it returns, so there is no eventual delivery to wait for and no
  retry schedule to observe. What a subscriber would eventually receive, it
  has already received when the call answers.
- **Four protocols are accepted and not delivered to.** `email`, `sms`,
  `application` and `firehose` subscriptions are stored and logged rather
  than sent — refusing them because nothing local sends mail would break a
  subscription that works on AWS, which is the opposite of what this is for.
  The delivery boundary is the `Subscribe` row in the table above.
- **`FilterPolicyScope: MessageBody` is stored and applied as if it were
  attribute-scoped.** The policy is not matched against the body, so a
  body-scoped filter is more permissive here than on AWS.
- **Data protection policies are stored, never evaluated.** Nothing is
  redacted or denied on their account.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): fanout to SQS raw and enveloped, tags and
  topic attributes, and the policy a topic carries.
- **aws-sdk-go v1** (`sdkv1_test.go`): publish fanout through the older
  client, and the mobile-push surface refusing as an honest stub rather than
  pretending.
- **Filter policies** (`filter_test.go`, `doze_match_test.go`): the numeric and
  operator set shared with EventBridge, and — the part worth having — that the
  console's "who would receive this" preview agrees with what delivery
  actually does, including which key caused a rejection.
- **Delivery status** (`deliverylog_sdk_test.go`): the per-subscription success
  and failure lines land in CloudWatch Logs where AWS puts them.
- **Model-derived rejection parity** (`rejection_parity_test.go`) and the
  dispatch table against `testdata/ops_sns.json` (`coverage_model_test.go`).

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what SNS refuses**.

| Input | Status |
|---|---|
| `Subscribe` — `Protocol` | ✅ refused outside `http`, `https`, `email`, `email-json`, `sms`, `sqs`, `application`, `lambda`, `firehose` |
| `Subscribe` — `TopicArn`, `Endpoint` required | ✅ |
| Everything else | see **Model-derived validation** below — 53/53 enforced, no known gaps |

Note the accepted set is what **AWS** accepts, not what doze-aws delivers to.
`email`, `sms`, `application` and `firehose` are stored and logged rather than
delivered — refusing them because nothing local sends mail would break a
subscription that works in AWS, which is the opposite of the failure this
check exists to prevent. The delivery boundary is the `Subscribe` row in the
table above.

SNS's own service model types `Protocol` as a plain string with no enum trait,
so this list is hand-derived from the API reference rather than generated by
`dzaudit` — the same situation as SQS, and the same reason nothing had ever
cross-checked it.

Covered by `sns/rejection_parity_test.go`, which asserts the error **code** an
SDK sees, not just that something failed.

#### Model-derived validation

The `Subscribe` protocol check above was hand-derived. This is the generated
audit that covers the rest.

**53/53 model-derived constraints enforced across all 19 dispatched
operations, with `knownGaps` empty.** Before this table, 27 were enforced and
26 were not.

`dzaudit cases sns` emits a violating value per constrained input,
`testdata/cases_sns.json` commits them, and `rejection_parity_test.go` replays
every one from a baseline it first proves the service accepts. 35 further cases
fall on the 21 stub operations — SMS, mobile push and the platform-endpoint
surface — which refuse every request including a valid one, so replaying a
mutation against them proves nothing.

Two fixtures are worth noting because the alternative was skipping the
operations. `ConfirmSubscription` needs a real token, and an SQS subscription is
auto-confirmed and never issues one — so the fixture also subscribes an
`http` endpoint against a recording server and captures the token SNS posts to
it. `SetSubscriptionAttributes` and friends need a subscription ARN that exists,
which the fixture creates rather than invents.

SNS also exercises the Query protocol's **map** spelling, which STS did not:
`MessageAttributes` travels as a numbered list of Name/Value pairs
(`MessageAttributes.entry.1.Value.DataType`) where the model calls it a map
(`MessageAttributes{}.DataType`). `modelcheck.FromQuery` collapses those entries
back into a map — without it the walker looks for a map, finds a list, and every
constraint underneath passes without being checked.

<!-- svc:sqs -->
## SQS — API support

Both wire protocols are served: AWS JSON 1.0 (modern SDKs) and the legacy
Query/XML protocol (aws-sdk-go v1 era). MD5OfMessageBody and
MD5OfMessageAttributes match AWS's algorithms — SDK client-side checksum
validation passes.

| Operation | Tier | Notes |
|---|---|---|
| CreateQueue | F | standard + FIFO (.fifo naming rule enforced), attributes, tags; idempotent re-create merges attributes |
| DeleteQueue | F | drops messages and dedup state |
| ListQueues | F | prefix filter; no pagination (local queue counts don't need it) |
| GetQueueUrl | F | |
| GetQueueAttributes | F | incl. ApproximateNumberOfMessages(NotVisible), QueueArn, RedrivePolicy |
| SetQueueAttributes | F | visibility, delay, retention, max size, receive wait, redrive policy, FIFO dedup |
| TagQueue / UntagQueue / ListQueueTags | F | |
| SendMessage / SendMessageBatch | F | delay, message attributes (String/Number/Binary), FIFO group + dedup id, content-based dedup |
| ReceiveMessage | F | long polling (notifier-driven, no spin), visibility timeout + per-receive override, FIFO group locking, system + message attribute selection |
| DeleteMessage / DeleteMessageBatch | F | |
| ChangeMessageVisibility | F | |
| ChangeMessageVisibilityBatch | F | via per-entry ChangeMessageVisibility semantics |
| PurgeQueue | F | |
| ListDeadLetterSourceQueues | F | |
| StartMessageMoveTask | F | completes synchronously (local volumes); DestinationArn required — doze-aws does not track per-message origin queues |
| ListMessageMoveTasks | F | returns the recorded (terminal) tasks |
| CancelMessageMoveTask | F | always "task is not active" — local moves complete synchronously, matching AWS's answer for a finished task |
| AddPermission / RemovePermission | F | AddPermission writes the statement AWS writes into the `Policy` attribute (Sid = Label, the account roots as principals, `SQS:<action>` on the queue; a label in use is refused), RemovePermission drops it by label. The queue policy is evaluated on every request naming the queue under IAM `soft` and `enforce`; an SNS fan-out into the queue needs a `Service: sns.amazonaws.com` statement under `enforce`, as on AWS |
| DozePeek | — | doze extension: read-only full-queue inspection (no visibility/receive-count side effects) |

Dead-letter redrive (maxReceiveCount → DLQ move) and retention expiry run on
the receive path plus a background janitor, so write-only queues are reclaimed
too.

### Differences from AWS

- **The rules are hand-derived, because the model has none.** SQS's own service
  model carries no `@range`, `@length` or `@pattern` traits — its constraints
  live in prose — so queue names, visibility timeouts and redrive policies are
  checked against the API reference rather than against a generated table. It
  is the one service here where the audit could not be generated, and the
  reason its bugs surfaced first.
- **Redrive and retention run on the receive path and a janitor**, rather than
  continuously server-side. A queue nobody reads is still swept, so the
  observable result converges; the timing of a move to the DLQ does not have
  to match AWS to the second.
- **Delivery is in-process.** A message fans out to a Lambda event source
  mapping or an S3 notification through the peers directory rather than over
  the network, so there is no delivery delay to observe and no partial-region
  failure to handle.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`) and **v1** (`sdkv1_test.go`): both wire
  protocols — AWS JSON 1.0 and the legacy Query/XML — against the same store,
  with the MD5 body and attribute digests SDK client-side validation checks.
- **Batch semantics** (`batch_txn_test.go`): a bad entry does not sink the
  batch and is reported per entry, a missing queue fails the whole call.
- **Attributes** (`attrs_roundtrip_test.go`): every settable attribute round
  trips, computed ones cannot be overwritten, and clearing one removes it.
- **Lifecycle janitors** (`hardening_test.go`): dedup entries expire, retention
  sweeps, and a long poll wakes promptly rather than on its timeout.
- **A leak, kept fixed** (`notify_leak_test.go`): a successful receive followed
  by a queue delete used to strand a wakeup channel in the long-poll notifier.
- **Model-derived rejection parity** (`rejection_parity_test.go`), which asserts
  the error **code** an SDK sees rather than only that something failed.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what SQS refuses**. The two are different
promises, and the second is the one that decides whether code passing here also
passes on deploy.

| Input | Status |
|---|---|
| `RedrivePolicy` — target exists | ✅ refused if the queue does not exist |
| `RedrivePolicy` — FIFO↔FIFO, standard↔standard | ✅ refused if the types differ |
| `RedrivePolicy` — `maxReceiveCount` 1–1000 | ✅ range enforced, quoted or bare |
| `RedrivePolicy` — self-reference | ✅ a queue cannot be its own DLQ |
| `VisibilityTimeout` 0–43200 | ✅ |
| `DelaySeconds` 0–900 | ✅ |
| `MessageRetentionPeriod` 60–1209600 | ✅ |
| `MaximumMessageSize` 1024–262144 | ✅ |
| `ReceiveMessageWaitTimeSeconds` 0–20 | ✅ |
| Non-numeric attribute values | ✅ refused (previously silently ignored) |
| Queue name — charset | ✅ alphanumeric, `-`, `_` only (a period is legal only as the `.fifo` suffix) |
| Queue name — length ≤80 | ✅ the `.fifo` suffix counts toward the limit |
| Everything else | see **Model-derived validation** below — 48/48 enforced, no known gaps |

Enforced cases are covered by `sqs/rejection_parity_test.go`, which asserts the
error **code** an SDK sees, not just that something failed.

Note SQS's own AWS service model carries no `@range`, `@length` or `@pattern`
traits at all — its constraints live only in prose — so this table is hand-derived
rather than generated. That is also why the bugs surfaced here first: nothing had
ever cross-checked them.

#### Model-derived validation

The checks above are hand-derived, because SQS's own service model states almost
nothing about queue names, visibility timeouts or redrive policies — those rules
exist only in prose. This is the generated audit that covers what the model
*does* state.

**48/48 model-derived constraints enforced across 21 of the 22 dispatched
operations, with `knownGaps` empty.** Before this table, 22 were enforced and
26 were not.

Both protocols are covered by one table: the request is rendered into a
protocol-neutral shape (`params.asMap`) before the walk, and the error code
follows the caller's protocol — `ValidationError` for Query, `ValidationException`
for JSON.

`CancelMessageMoveTask`'s single case is skipped with the reason recorded in the
test: local message moves complete synchronously, so there is never an active
task to cancel and the baseline is refused however it is built.

##### What this audit caught

`ReceiveMessage`'s deprecated `AttributeNames` is the **QueueAttributeName**
enum, which does not contain `AWSTraceHeader`; the header belongs under
`MessageSystemAttributeNames`. doze-aws's own SQS event-source poller had been
asking for it under the wrong field since cascade tracing was written, and only
worked because nothing validated it — real AWS would have refused the call, and
with it the trace propagation that makes S3 → SQS → Lambda a single chain.

<!-- svc:ssm -->
## SSM — API support

doze-aws implements the Parameter Store slice of SSM. SecureString values are
genuinely encrypted at rest with a per-data-dir AES-256-GCM key the service
manages itself; the KMS KeyId is recorded and returned cosmetically, so SSM
works with or without the kms service enabled.

| Operation | Tier | Notes |
|---|---|---|
| PutParameter | F | String/StringList/SecureString, Overwrite semantics, version bump, tags, Tier accepted (cosmetic), policies stored with **Expiration enforced by janitor** |
| GetParameter | F | `name`, `name:version`, `name:label`, ARN form; WithDecryption |
| GetParameters | F | found + InvalidParameters split |
| GetParametersByPath | F | hierarchy walk, Recursive flag |
| GetParameterHistory | F | all versions with labels |
| DeleteParameter / DeleteParameters | F | |
| DescribeParameters | F | Name (Equals/BeginsWith) and Type filters; other filter keys ignored |
| LabelParameterVersion / UnlabelParameterVersion | F | a label names at most one version (moves on re-label) |
| AddTagsToResource / RemoveTagsFromResource / ListTagsForResource | F | ResourceType Parameter only |
| Documents, Automation, Run Command, Sessions, fleet/instances, associations, patching, inventory, compliance, maintenance windows, OpsCenter, resource data sync, service settings | S | need managed instances / agent infrastructure that does not exist locally; each answers UnsupportedOperationException |

### Differences from AWS

- **Parameter Store only.** The fleet-management half of SSM — Documents,
  Automation, Run Command, Sessions, patching, inventory, maintenance windows
  — answers `UnsupportedOperationException` by name. All of it manages managed
  instances, and there are none.
- **SecureString is encrypted with a local key**, generated once into the data
  directory rather than held by KMS. The value is genuinely encrypted at rest
  and genuinely not protected from anything with read access to that
  directory.
- **`DescribeParameters` filters on Name and Type.** Other filter keys are
  accepted and ignored rather than refused, because a filter this does not
  implement returning everything is a smaller surprise than a call that fails.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): versions and labels, SecureString
  round-trips including the overwrite that must keep the type, GetParametersByPath
  and DescribeParameters, delete and tags, and the fleet operations answering
  honestly rather than silently.
- **aws-sdk-go v1** (`sdkv1_test.go`): the parameter round trip through the
  older client.
- **Model-derived rejection parity** (`rejection_parity_test.go`), scoped to
  the dispatched operations, plus the measurement of what the constraint table
  is worth — see below.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what SSM refuses**.

**100/100 model-derived constraints enforced across all 13 dispatched
operations, with `knownGaps` empty.** Removing the constraint table makes 75 of
those 100 cases slip through, so it is doing work the hand-written checks were
not.

Generated with `dzaudit cases ssm`, scoped to the dispatched operations, and
replayed case by case in `rejection_parity_test.go` from a baseline the test
first proves the service accepts.

SSM has the largest model of any service here — 1,638 cases across 152
operations — and the ratio is the point. The other 1,538 fall on fleet
management: documents, Run Command, sessions, patching, inventory, OpsCenter,
maintenance windows. Those need managed instances that do not exist locally,
they answer `UnsupportedOperationException` before reading their input, and
there is nothing there to validate. Counting them as unaudited would overstate
the gap; counting them as audited would be a lie. They are neither.

#### Every case gets its own thing to consume

Six of the thirteen operations destroy what they name. `DeleteParameter`,
`DeleteParameters`, `UnlabelParameterVersion` and `RemoveTagsFromResource` all
remove the resource the *next* case would have used, and a label may only sit on
one version at a time, so a second `LabelParameterVersion` moves rather than
adds. Operations run in alphabetical order, which is not the order that would
make them work, so each case creates its own parameter, label or tag first
rather than relying on the fixture surviving.

<!-- svc:stepfunctions -->
## Step Functions — API support

Standard and Express workflows run locally against the services this stack
already serves. The Amazon States Language is parsed, statically checked and
executed by a pure interpreter (`internal/asl`) that knows nothing about AWS
calls; the service around it owns one driver goroutine that steps every
execution, writes every history event, and hands Task calls to transient
workers that never touch the store. A Standard execution is a serialisable
frame list, so a restart resumes every suspended state — a `Wait`, a Lambda
in flight, a branch parked on a task token, a child execution being waited
on, a Distributed Map fanning out — from what its frame says rather than from
a goroutine the restart lost. Express executions and TestState runs live in
memory only, as on AWS, where neither is listable after the fact.

Step Functions is the one service here on AWS JSON 1.0 rather than 1.1, signs
as `states` while targeting `AWSStepFunctions`, and spells its members
lowercase-initial (`stateMachineArn`). Error codes are spelled exactly as SDKs
match them; `InvalidDefinition` carries the first problem and a count, so a
definition with four mistakes takes one round trip to understand.

| Operation | Tier | Notes |
|---|---|---|
| CreateStateMachine | F | STANDARD and EXPRESS; idempotent on an identical definition, so an unchanged `cdk deploy` succeeds; task resources outside the local integration set are refused here, not on first execution |
| DescribeStateMachine / ListStateMachines | F | describe answers the logging, tracing and encryption blocks as stored, or AWS's defaults when none were given; list paginates with `maxResults` and `nextToken`, and a stale token is `InvalidToken` |
| UpdateStateMachine | F | running executions keep their frozen definition; `publish` mints a version in the same call; a `loggingConfiguration`, `tracingConfiguration` or `encryptionConfiguration` sent replaces the stored block, so a logging change is not drift on the next plan |
| DeleteStateMachine | F | synchronous — AWS parks the machine in DELETING until executions drain; locally it disappears at once, and the call is idempotent so a repeated `cdk destroy` does not fail |
| ValidateStateMachineDefinition | F | the analyser exposed directly; every diagnostic in document order, none returned early |
| PublishStateMachineVersion / DeleteStateMachineVersion / ListStateMachineVersions | F | a version freezes the definition; `revisionId` guards a publish; a version an alias still routes to cannot be deleted |
| CreateStateMachineAlias / DescribeStateMachineAlias / UpdateStateMachineAlias / DeleteStateMachineAlias / ListStateMachineAliases | F | one or two weighted versions; StartExecution on an alias ARN picks a version by weight and records both on the execution |
| CreateActivity / DescribeActivity / DeleteActivity / ListActivities | F | control plane, paginated |
| GetActivityTask | F | long-poll, 60 s as on AWS; an activity Task state queues its input for the next worker, which answers through the SendTask* calls |
| TagResource / UntagResource / ListTagsForResource | F | tags are a `[{key,value}]` list, as on AWS, not the `{k:v}` map Lambda and DynamoDB use; an ARN nothing holds is `ResourceNotFound`, not an empty list |
| StartExecution | F | machine, version or alias ARN; same name + still RUNNING + same input returns the original execution rather than conflicting; on an EXPRESS machine it is fire-and-forget, answering an ARN nothing can describe afterwards, as on AWS — its history is in the log group (below) |
| StartSyncExecution | F | Express: runs to completion inside the call, five-minute cap, `billingDetails` and the `includedData` switch; reachable at `sync-aws.<instance>.doze`, the host prefix every SDK's endpoint ruleset applies (an instance claims it alongside its own name; under `--listen` there is no name, so a client needs `disableHostPrefix`) |
| TestState | F | one state in isolation, with `inspectionData` per `inspectionLevel`, `mock` results and errors, and `stateConfiguration`; the `sync-` host again |
| DescribeExecution / ListExecutions | F | status, `redriveFilter` and `mapRunArn` filters, `maxResults` and `nextToken`; `traceHeader` comes back only when StartExecution was given one; an EXPRESS machine's executions are not listable, as on AWS |
| StopExecution | F | also aborts the Map Runs the execution owns; a child started with `.sync` keeps running, as on AWS |
| RedriveExecution | F | FAILED, ABORTED or TIMED_OUT executions within 14 days; failed frames resume from the state that failed, finished branches are kept; `clientToken` idempotency |
| DescribeStateMachineForExecution | F | answers from the execution's frozen snapshot — what it is running, not what the machine says today |
| GetExecutionHistory | F | global event ids with per-frame `previousEventId` chains; `reverseOrder`; pagination; payloads are JSON-encoded strings, as the SDK types expect |
| SendTaskSuccess / SendTaskFailure / SendTaskHeartbeat | F | tokens minted before `Parameters` are evaluated so `$$.Task.Token` resolves, and persisted before the send so an instant reply finds them |
| DescribeMapRun / ListMapRuns / UpdateMapRun | F | one Map Run per Distributed Map state, with the item and execution counts AWS reports; UpdateMapRun changes `maxConcurrency` and the tolerated-failure settings of a run in flight |

Every one of the 37 operations in the `com.amazonaws.sfn` model is handled.
Nothing is staged and nothing falls through to `InvalidAction`.

### What runs, honestly

The interpreter speaks both dialects. In JSONPath it has the full `States.*`
intrinsic set (own scanner and parser: nested calls, escaped quotes), all
eight state types, `Retry` and `Catch` with the spec's defaults and jitter,
`Parallel`, inline `Map` with `MaxConcurrency` and `ItemSelector`, `Wait` on
seconds, timestamps and paths, and `Assign` with `$name` variable references
in `Parameters`, `Choice` comparisons and `Wait` paths. In JSONata
(`"QueryLanguage": "JSONata"` on the machine or on one state) it evaluates
`Arguments`, `Output`, `Assign`, `Condition`, `Items` and the `Wait` fields
with the AWS additions — `$states`, `$partition`, `$range`, `$hash`, `$random`,
`$uuid`, `$parse`.

#### The JSONata dialect, measured

`%`, `@` and `#` — the path-binding operators — **do not work. Everything else
in the JSONata function library and syntax does.**

Use a variable binding in place of `%`, which is how most JSONata is written
anyway:

```
Order.($o := $; Product.($o.OrderID))
```

`$eval` is absent too, but that is parity rather than a gap: AWS does not offer
it either — *"`$eval` is not available—use `$parse` instead"* — and `$parse` is
implemented.

That list is measured, not asserted. The AWS additions are this repo's code, but
the language underneath is [blues/jsonata-go](https://github.com/blues/jsonata-go),
a partial port — so "supports JSONata" is a claim about a dependency.
`internal/asl/jsonata_dialect_test.go` probes the documented library and syntax
and freezes the result in both directions: a dependency bump that closes a gap
fails it as loudly as a regression that opens one.

Two details behind the list, for anyone changing this:

- The three operators are **one** missing feature. All of them ride on a tuple
  stream carried through path evaluation, which the port does not implement, and
  `%` is resolved by static analysis at compile time — so no extension function
  can supply them, and a source rewrite cannot either
  (`Order.Product.%.OrderID` yields one `OrderID` per *product*, so dropping a
  path step changes the cardinality). It would take a fork.
- Four functions the port omits — `$assert`, `$formatInteger`, `$parseInteger`
  and the two-argument `$string(value, prettify)` — are supplied in
  `internal/asl/jsonata_gaps.go`. The two integer pictures accept the documented
  set (`0`/`#`/`,` decimal patterns, `a` `A` `i` `I` `w` `W` `Ww`, each
  optionally with `;o` for ordinals) and refuse anything else rather than
  falling back to decimal, since a picture they cannot read would behave
  differently on AWS.

Everything else in the library is there, including the parts most often
missing from a port: `$sift`, `$each`, `$single`, `$zip`, `$distinct`,
`$type`, `$formatNumber`, `$formatBase`, `$toMillis`/`$fromMillis`, the
transform operator `|…|…|`, the order-by operator `^(…)`, descendant `**`,
regex flags, and user-defined functions.

So a definition written in the current console runs unchanged unless it uses
`%`, `@` or `#`.

Task resources are the local integration set, each with `.waitForTaskToken`
where AWS offers it:

- Lambda by bare ARN or `arn:aws:states:::lambda:invoke`.
- `sqs:sendMessage`, `sns:publish`, `events:putEvents`, and the optimized
  DynamoDB `getItem`, `putItem`, `updateItem` and `deleteItem`.
- Activity ARNs, which park until a worker polls and answers.
- `states:startExecution`, plain, `.sync`, `.sync:2` or with a task token:
  the child runs in this process and the parent resumes when it finishes,
  which is why this is the one `.sync` integration that means something
  locally.
- `aws-sdk:<service>:<action>` for every service this stack serves —
  DynamoDB, EventBridge, SQS, SNS, SSM, Secrets Manager, Kinesis, IAM, Step
  Functions, S3 (`getObject`, `putObject`, `deleteObject`, `headObject`,
  `listObjectsV2`) and Lambda (`invoke`) — encoded from the service's own
  model, with errors surfaced as `<Service>.<Code>`.

A Distributed Map (`"Mode": "DISTRIBUTED"`) runs each item as a child
execution under a Map Run: `ItemReader` over an S3 object in JSON, JSON Lines
or CSV or over `listObjectsV2`, `ItemBatcher`, `MaxConcurrency`,
`ToleratedFailureCount` and `ToleratedFailurePercentage`, `Label`, and a
`ResultWriter` that lands the manifest and result files in the local S3. The
children are ordinary executions — listable with `mapRunArn`, visible in the
console — and the run's counts are what DescribeMapRun reports.

CreateStateMachine refuses a resource outside this set by name rather than
letting a deploy succeed and the first execution fail.

A Task never surfaces a Go error. Every outcome is a failure name `Retry` and
`Catch` can match: a Lambda handler that throws `MyError` is caught as
`MyError`; a Lambda API error is `Lambda.<Code>`, so the CDK's default Retry on
`Lambda.TooManyRequestsException` works; an unwired peer is
`States.TaskFailed`; deadlines are `States.Timeout` and
`States.HeartbeatTimeout`; a failed child execution is `States.TaskFailed`
with the child's error and cause in the description.

Two behaviours are AWS's and worth knowing. A `Choice` whose `Variable`
selects nothing fails the execution with `States.Runtime` — `IsPresent` is the
one safe probe — while a variable that is present but the wrong type simply
does not match. And `ResultPath` merges the result into the *raw* state input,
not the post-`InputPath` view; `"InputPath": null` means `{}`, absent means
`$`.

Differences from AWS, listed rather than hidden:

- **Task dispatch is at-least-once across a restart.** A frame persisted as
  CALLING is re-dispatched when the store reopens, so a Lambda that was in
  flight when `doze-aws` stopped is invoked again. This matches AWS's own
  task semantics; a re-invoked handler during development is not a bug.
- **Wait resolution is one second.** Frames' wake times are compared against
  the clock every second, which is AWS's resolution for `Wait` too; there is
  no timer heap that could desync from the frames, because the frames are the
  schedule.
- **DeleteStateMachine is immediate**, not DELETING (above).
- **A timed-out token answers `TaskTimedOut` for a day**, then
  `TaskDoesNotExist` like any spent token. AWS keeps the distinction longer;
  a worker that comes back a day late is not one this stack needs to serve.
- **Express executions and TestState runs do not survive a restart.** They
  are held in memory for the call that runs them, which is also where AWS
  keeps them. An Express run's history goes to its log group (below); a
  TestState run's goes to the caller.

### Logging

A machine's `loggingConfiguration` is honoured. Every history event an
execution records is written to the group its destination names, in the
JSON record AWS vends — `id`, `type`, `details`, `previous_event_id`,
`event_timestamp`, `execution_arn` — one stream per machine per process,
named `states/<machine>/<date>/<hex>`. `level` filters as on AWS: `ALL`
writes everything, `ERROR` every event that reports a failure, `FATAL` only
the execution's own end; `includeExecutionData: false` strips input, output
and parameters. A Standard machine at `OFF` writes nothing, since
GetExecutionHistory has it all. `aws logs tail <group> --follow` and the
machine's Logs tab in the console read it.

An **Express machine with logging off still writes**, at `ALL` with data,
to `/aws/vendedlogs/states/<machine>`. On AWS an Express execution leaves
nothing behind but its log group, and a run nobody can inspect afterwards
is the one AWS behaviour a local emulator should not reproduce; the ledger
says so here and the console says so on the machine. Set a destination to
choose the group. `tracingConfiguration` and `encryptionConfiguration`
remain stored-only: there is no X-Ray or KMS wrapping to apply.
- **A Distributed Map with no `MaxConcurrency` runs 40 children at a time**,
  where AWS's default is 10,000 — one process cannot usefully start ten
  thousand executions in a tick. A `MaxConcurrency` on the state, or an
  UpdateMapRun, is honoured as given. The item reader reads this stack's S3,
  not a cross-account bucket; the counts, statuses and result files are the
  same shape a program sees from AWS.
- **`.sync` on any service other than Step Functions is refused at create
  time**, because the job it would wait for runs nowhere locally. AWS's
  `.sync` for Batch, ECS, Glue and the rest has nothing to poll here.
- **Alias routing is weighted-random.** Two versions at 50/50 receive
  executions by coin flip, as on AWS, so over a short local run the split
  can look uneven.

### Verified against

Three SDKs and one deploy tool, in tests that run on every push:

- **aws-sdk-go-v2** (`sdk_test.go`, `sdk_errors_test.go`, `sdk_express_test.go`,
  `sdk_versions_test.go`, `sdk_activity_test.go`, `jsonata_sdk_test.go`):
  every operation, and every typed error the service can answer matched
  through the SDK's own exception types — a near-miss spelling decodes as a
  generic error no program can branch on, which is what those tests exist
  to catch. StartSyncExecution and TestState go through the SDK with the
  `sync-` host prefix its ruleset adds, which is what `sync-aws.<instance>.doze` serves.
- **aws-sdk-go v1** (`sdkv1_test.go`): the older wire encoding round-trips.
- **@aws-sdk/client-sfn** (`e2e/tests/stepfunctions-sdk.spec.ts`): what the
  JavaScript types promise — timestamps decode as `Date`, payloads in
  history are strings, lists paginate — across the whole surface: versions
  and aliases, Express sync calls, activities polled from a worker, redrive,
  Distributed Map runs and a JSONata machine.
- **CDK** (`cloudformation/sfn_apply_test.go`, and a real `cdk deploy` of a
  LambdaInvoke → Choice → SqsSendMessage / SnsPublish → Wait → Parallel →
  Map chain, a `.waitForTaskToken` machine and an EXPRESS one): both
  spellings the CDK emits — `Fn::Join`ed ARNs inside `DefinitionString`,
  and `DefinitionSubstitutions` from `DefinitionBody.fromString` — resolve
  to the real function, queue and topic; a second unchanged deploy is
  "no changes"; destroy leaves no machines behind.

That pass found and fixed a join that read an earlier Parallel's settled
branches (a Map after a Parallel answered trailing nulls), list operations
ignoring `maxResults`, tag operations succeeding on an ARN nothing held, an
internal trace chain leaking as `traceHeader`, and — outside this service —
a CloudFormation mapping that dropped `S3Bucket` from a raw Lambda function's
`Code`, which is how the CDK ships every asset.

The engine sits in every other gate too: the intrinsic parser and the
definition analyser are fuzzed (`internal/asl/fuzz_test.go`, on the fuzz
workflow's matrix), the repo-level stress test (`stress_test.go`) runs
executions to SUCCEEDED under `-race` alongside every other service, and
the soak (`cmd/doze-aws/soak_test.go`) starts an execution per iteration and
asserts, at each checkpoint, that the one from five hundred operations ago
has finished.

### Differences from AWS

- **Three JSONata path operators are missing**, and they are the only gap in
  the dialect: `%` (parent), `@` (item binding) and `#` (position binding).
  Everything else in the JSONata function library and syntax works, measured
  rather than asserted — see [the dialect section](#the-jsonata-dialect-measured)
  for the probe and the rewrite to use instead.
- **Express executions are durable.** AWS keeps Express history only in
  CloudWatch Logs; here an Express run is stored like a Standard one, so you
  can call `DescribeExecution` on it afterwards. More is visible locally than
  would be on AWS, which is the direction worth erring in.
- **No cross-account or cross-region service integrations.** `aws-sdk:`
  integrations reach the local services; an ARN pointing somewhere else has
  nowhere to go.
- **Activity polling has no long-poll ceiling to speak of.** `GetActivityTask`
  waits on a local condition rather than a 60-second server-side hold, so a
  task token is handed over as soon as it exists.

### Input validation

The service model is the source of truth for what AWS refuses, and the
constraint table in `validate.go` is generated from it rather than
hand-written.

**229/229 model-derived constraints enforced across 33 of the 37
operations, with `knownGaps` empty.** The four not covered —
`SendTaskSuccess`, `SendTaskFailure`, `SendTaskHeartbeat` and
`RedriveExecution` — consume the state they address: redeeming a task token
spends it, and the first redrive puts the execution back to RUNNING, so their
nineteen cases have no baseline the harness could replay twice.

Generated with `dzaudit cases sfn`, committed to `testdata/cases_sfn.json`,
and replayed case by case in `rejection_parity_test.go` from a baseline the
test first proves the service accepts.

#### The definition is validated twice, on purpose

`ValidateStateMachineDefinition` is faithful to ASL: it accepts what AWS
accepts, including every service integration, because refusing a definition
AWS would take breaks a working template. `CreateStateMachine` then applies a
second, stricter check — a task resource this build cannot call is refused
at create time with a message naming it. The alternative, a clean deploy
followed by a first execution that fails on a `Task` state, is the failure
this stack exists to prevent.

#### Lowercase members are load-bearing

Every other service in this repo spells its JSON members upper-initial. Step
Functions does not, and the model-derived checks match keys exactly, so
`stateMachineArn` in the constraint table and in the IAM resource rules is
what makes a request decode to something other than an empty struct.

<!-- svc:sts -->
## STS — API support

| Operation | Tier | Notes |
|---|---|---|
| GetCallerIdentity | F | fixed local identity (account 000000000000, user/test) |
| AssumeRole | F | validates RoleArn/RoleSessionName/DurationSeconds; mints fresh ASIA-prefixed credentials |
| AssumeRoleWithWebIdentity | F | JWT accepted unverified; sub/aud/iss reflected into the response |
| AssumeRoleWithSAML | F | assertion accepted unverified; NameID/Issuer reflected into the response |
| AssumeRoot | F | mints short-lived credentials (≤900s) |
| GetSessionToken | F | |
| GetFederationToken | F | |
| GetAccessKeyInfo | F | every key maps to the one local account |
| DecodeAuthorizationMessage | S | doze-aws never produces encoded authorization messages, so there is nothing to decode |

All operations are served over the STS Query/XML protocol at the shared
endpoint, and accept SigV2, SigV4, or no signature at all.

### Differences from AWS

- **One identity, always.** `GetCallerIdentity` answers account
  `000000000000` and `user/test` whoever asks, and `GetAccessKeyInfo` maps
  every key to that same account. There is no directory behind it that could
  disagree.
- **Assertions are reflected, not verified.** `AssumeRoleWithWebIdentity`
  takes any JWT and `AssumeRoleWithSAML` any assertion; the subject, audience
  and issuer come back in the response unchecked. Verifying them needs a real
  identity provider, which is the thing you are running locally to avoid.
- **Credentials are minted, not honoured.** `AssumeRole` returns fresh
  ASIA-prefixed credentials for the duration requested, and every service here
  accepts them — as it accepts any signature, or none at all. The session is
  real to the SDK and means nothing to the gateway.
- **The error code belongs to the protocol, not the house.** A refused request
  answers `ValidationError`, where the awsJson services answer
  `ValidationException`. Clients branch on it, so it follows the Query family
  rather than a convention chosen here.

### Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): every dispatched operation —
  GetCallerIdentity, AssumeRole and its validation, the web-identity and SAML
  paths, GetSessionToken and GetFederationToken, GetAccessKeyInfo — plus
  DecodeAuthorizationMessage refusing as the honest stub it is.
- **aws-sdk-go v1** (`sdkv1_test.go`): the same service through the older
  client, including the Query-protocol error shape v1 callers branch on.
- **Model-derived rejection parity** (`rejection_parity_test.go`): every case
  `dzaudit` derives from AWS's own model, replayed from a baseline the test
  first proves the service accepts.
- **The dispatch table against the model** (`coverage_model_test.go`): every
  operation AWS documents either reaches a handler or is a listed gap, checked
  against `testdata/ops_sts.json` and regenerated weekly by CI.

### Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what STS refuses**.

**108/108 model-derived constraints enforced across 7 of the 8 dispatched
operations, with `knownGaps` empty.** Removing the table lets several through —
`GetSessionToken`'s `TokenCode` pattern and `SerialNumber` length among them —
so the table is doing work the hand-written checks were not.

Generated rather than hand-derived: `dzaudit cases sts` emits a violating value
per constrained input, `testdata/cases_sts.json` commits them, and
`rejection_parity_test.go` replays every one from a baseline it first proves the
service accepts.

`DecodeAuthorizationMessage`'s 3 cases cannot be audited: it is an honest stub —
doze-aws never produces encoded authorization messages — so it refuses a valid
request too, and replaying a mutation against it proves nothing.

#### The Query protocol

STS is the first Query-protocol service audited, and it needed one piece of
machinery the awsJson services did not. Query flattens nesting into the key
(`Tags.member.1.Key`) where JSON nests it in the body, so the form is rebuilt
into the shape the constraint paths describe before the walk
(`modelcheck.FromQuery`). The test harness performs the same translation in
reverse, which means the two are checked against each other by every case that
reaches a nested path.

Two smaller differences, both real:

- The error **code** differs by protocol family. Query services answer
  `ValidationError`; the awsJson ones answer `ValidationException`. Same message,
  different envelope, and clients branch on it.
- Every value in a form is a string, so a `@range` constraint reads a numeric
  string — the model's own trait is what says a member is a number. Without
  that, every range constraint on a Query service would pass silently.
