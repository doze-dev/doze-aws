# API Gateway — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

doze-aws implements the REST (v1) **create → deploy → invoke** path: 40 of API
Gateway's 124 operations, covering everything needed to stand an API up and
actually call it. The remaining families are refused by name.

The narrow surface is deliberate. Most of API Gateway's operation count is
commercial and edge machinery — API keys, usage plans, custom domains, client
certificates, VPC links, SDK generation — none of which has a local
counterpart. What a developer needs locally is for a deployed API to answer.

## Two planes

The **control plane** is CRUD over a path tree, at `/restapis/...`.

The **execute-api plane** is where a deployed API answers. `/restapis` is
already the control plane, so a deployed API is served at:

```
/_aws/execute-api/{apiId}/{stage}/{path...}
{apiId}.execute-api.<host>/{stage}/{path...}     (virtual-host style)
```

Both shapes match LocalStack's, so existing habits and test helpers transfer.

```sh
curl http://127.0.0.1:4566/_aws/execute-api/abc123def0/prod/orders/42
```

## Routing precedence

Path matching follows API Gateway's own order, which is **not** first-match:

1. an exact literal segment beats a path parameter — `/users/me` wins over `/users/{id}`
2. a path parameter beats a greedy proxy
3. among greedy `{proxy+}` resources the **deepest** wins, so `/api/{proxy+}` beats `/{proxy+}`

Getting this wrong sends `/users/me` to the `/users/{id}` handler, which is the
kind of bug that only surfaces under real traffic. It is covered by tests.

## Integrations

| Type | Tier | Notes |
|---|---|---|
| AWS_PROXY | F | the one that matters. Full proxy event: path, method, headers and multi-value headers, query and multi-value query, path parameters, stage variables, request context, base64 body when it is not valid UTF-8. The function's `{statusCode, headers, body, isBase64Encoded}` drives the response |
| MOCK | F | answers from the integration's own response templates and static header parameters — enough for CORS preflights |
| HTTP / HTTP_PROXY | F | forwards to a real endpoint, `{param}` placeholders expanded, query and headers passed through |
| AWS (non-proxy) | S | needs Velocity mapping templates. Emulating VTL badly would silently produce the wrong backend request, so it is refused |

A function that does not return the proxy-integration response shape gets a
**502** naming what it returned — the same failure AWS produces, and far more
useful than a blank 500.

## Operations

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
| API keys and usage plans (18 operations) | S | there is no metering or billing locally |
| Custom domains and base path mappings (12 operations) | S | there is no DNS or TLS termination locally |
| Client certificates (5 operations) | S | certificate material is cloud infrastructure |
| VPC links (5 operations) | S | there is no VPC locally |
| CreateAuthorizer / GetAuthorizer / GetAuthorizers / UpdateAuthorizer / DeleteAuthorizer | F | TOKEN and REQUEST Lambda authorizers, run on the data plane (see below); a duplicate name conflicts; `COGNITO_USER_POOLS` is refused by name, there being no user pool locally; `PutMethod` with `CUSTOM` must name an authorizer that exists |
| Request validators and models (10 operations) | S | request validation is schema work with no local consumer yet |
| Documentation parts and versions (10 operations) | S | documentation exports are a publishing feature |
| SDK and export generation (5 operations) | S | code generation is a cloud-side service |
| Gateway responses (4 operations) | S | response customisation with no local consumer yet |

## Deployments serve the live API

Real API Gateway snapshots the API into a deployment, so a change is invisible
until you redeploy. doze-aws serves the **live** API instead: locally you want
an edit to take effect immediately, and a stale snapshot is a debugging trap
rather than a feature. The deployment record still exists so the control plane,
CloudFormation and Terraform all behave.

## Lambda authorizers

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

## Logging

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

## HTTP API (v2)

Not implemented. `AWS::Serverless::HttpApi` and `AWS::ApiGatewayV2::Api`
transpile to the same route model as REST APIs, so a SAM `HttpApi` event works,
but the v2 control plane (`/v2/apis/...`) is not served.

## See also

- [../cloudformation.md](../cloudformation.md) — SAM `Api` events and the
  `AWS::ApiGateway::*` resource types.
- [lambda.md](lambda.md) — the function runtime behind a proxy integration.

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what API Gateway refuses**.

**103/108 model-derived constraints enforced across all 36 routed operations that
have constrained input, with `knownGaps` empty.** Removing the constraint table
makes 26 of them slip through. The remaining five cannot be put on this wire at
all — see below.

Generated with `dzaudit cases api-gateway`, committed to
`testdata/cases_apigateway.json`, and replayed case by case in
`rejection_parity_test.go` from a baseline the test first proves the service
accepts.

### The operation has to be worked out before it can be validated

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

### An omitted path label is an empty segment, not an absent field

This was a real gap, found by the audit. restJson1 binds `restApiId` and its
kin into the URI, so a caller who omits one sends `GET /restapis//resources` —
the member is not missing from a body, it arrives as an empty string. The
validator recorded it as present, which meant **every `@required` path label
passed vacuously**: a label is never absent from the map the router builds. Now
an empty label is treated as omitted, and the seven affected cases are enforced.

### Five cases cannot be expressed on this wire

Omitting the **last** label of a URI does not produce an invalid request. It
produces a shorter path, which is a different and entirely valid operation:
`GET /restapis` is `GetRestApis`, not a broken `GetRestApi`. The same is true
for `GetResource`, `GetDeployment`, `GetStage` and `GetAuthorizer`. There is nothing for the
service to refuse, and AWS does not refuse it either, so these are listed in
`unexpressible` with the reason and counted separately — a gap means AWS
enforces something doze-aws does not, and this is not that.

### Not audited

The operations doze-aws answers with 501 (models, request validators,
documentation, gateway responses) and everything outside `/restapis`
(API keys, usage plans, domain names, VPC links) have no handler to validate
input. `GetRestApis` is routed but has no constrained input in the model at all.
