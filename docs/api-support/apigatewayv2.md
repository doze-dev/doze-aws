# API Gateway v2 (HTTP APIs) — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

doze-aws implements the HTTP API **create → route → serve** path: 37 of
apigatewayv2's 98 operations, covering an API with CORS, its integrations,
routes, REQUEST authorizers, stages, deployments and tags, served at the
execute-api plane the way AWS serves them. WebSocket APIs and the families
with no local counterpart are refused by name. The v1 REST API surface is
[apigateway.md](apigateway.md); an HTTP API is the same service with the
`/v2/` control plane, and shares its Lambda authorizer machinery, its access
logs and its console.

## Where an HTTP API answers

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

## Routing

A route key is `$default` or `<METHOD> /<path>` with `{param}` and `{proxy+}`
segments, `ANY` matching every method. Selection follows AWS: a literal
segment beats a parameter, a parameter beats a greedy proxy, the deepest proxy
wins, an exact method beats `ANY`, and `$default` catches whatever nothing else
matched. No match is `404 {"message":"Not Found"}`.

## Integrations

| Type | Tier | Notes |
|---|---|---|
| AWS_PROXY | F | a Lambda function, by ARN or invoke URI, in payload format **2.0** (the function-URL event: `rawPath`, `rawQueryString`, `cookies`, `requestContext.http`, `pathParameters`, `stageVariables`; a bare return value is a 200 JSON body, an object with `statusCode` is the whole response) or **1.0** (the REST proxy event with `version: "1.0"` and `requestContext.routeKey`; a malformed return is a 502) |
| HTTP_PROXY | F | forwards to the URL, `{param}` placeholders expanded, the request's method unless `integrationMethod` names one, headers and query passed through, the backend's response returned as is |
| AWS, HTTP, MOCK | S | WebSocket API integration kinds, refused at create |
| AWS service integrations (`integrationSubtype`) | S | SQS-SendMessage and friends need request-parameter mapping; integrate through a function |
| VPC links (`connectionType: VPC_LINK`) | S | there is no VPC locally |

## CORS

`corsConfiguration` on the API is evaluated on the data plane: an `OPTIONS`
preflight from an allowed origin and method is answered 204 with the allow
headers, max age and credentials flag; one from another origin is a bare 204;
every other response to an allowed origin carries `Access-Control-Allow-Origin`
and the expose and credentials headers. `DeleteCorsConfiguration` stops it.

## Authorizers

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

## Operations

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

## CloudFormation and SAM

`AWS::ApiGatewayV2::Api` (with `CorsConfiguration` and the `Target` quick
create), `::Integration`, `::Route` (its `Target` naming an Integration of the
template), `::Stage`, `::Deployment` and `::Authorizer` map onto the same
route-shaped stack file a REST API uses, with the protocol marked, and apply
through the v2 control plane. `AWS::Serverless::HttpApi` and a function's
`HttpApi` events do the same: the implicit API is `ServerlessHttpApi` at
`$default`, an event with no `Path` is the `$default` route, and the `Auth`
block's Lambda authorizers carry over. `!GetAtt Api.ApiEndpoint` is the
execute-api address. See [../cloudformation.md](../cloudformation.md).

## Input validation

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
