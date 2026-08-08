# API Gateway — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

doze-aws implements the REST (v1) **create → deploy → invoke** path: 35 of API
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
| CreateStage / GetStage / GetStages / UpdateStage / DeleteStage | F | stage variables reach the proxy event; the response carries a usable `invokeUrl` |
| GetTags / TagResource / UntagResource | F | REST API ARNs |
| GetAccount | C | nominal throttle settings; nothing is throttled locally |
| API keys and usage plans (18 operations) | S | there is no metering or billing locally |
| Custom domains and base path mappings (12 operations) | S | there is no DNS or TLS termination locally |
| Client certificates (5 operations) | S | certificate material is cloud infrastructure |
| VPC links (5 operations) | S | there is no VPC locally |
| Authorizers (5 operations) | S | a Lambda authorizer is worth building; it is not built yet |
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

## HTTP API (v2)

Not implemented. `AWS::Serverless::HttpApi` and `AWS::ApiGatewayV2::Api`
transpile to the same route model as REST APIs, so a SAM `HttpApi` event works,
but the v2 control plane (`/v2/apis/...`) is not served.

## See also

- [../cloudformation.md](../cloudformation.md) — SAM `Api` events and the
  `AWS::ApiGateway::*` resource types.
- [lambda.md](lambda.md) — the function runtime behind a proxy integration.
