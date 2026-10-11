# API Gateway

Stage 14. Size L. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds API Gateway's own.

REST APIs (`apigateway`, v1) and HTTP APIs (`apigatewayv2`) are two AWS services
served by one package, `apigateway/`, and one console page group. They are
planned together and keep separate contract tables, because their models,
ledgers and wire spellings are separate.

**Pre-stage review, 2026-10-11.** This page was fact-checked against the two
models' own documentation text (`.audit-models/api-gateway.json`,
`apigatewayv2.json`), against AWS documentation pages read that day (named
where used, tagged `doc`), and against the code and a built binary, before the
stage starts. Corrections are marked in place as *Corrected 2026-10-11*.
Forty findings were added (AGW-31 to AGW-70), the fidelity table is
section 6a, and the answers to the twelve questions of
[`../02-lessons-from-sqs.md`](../02-lessons-from-sqs.md) are section 12.
Evidence marked `run` was reproduced against a binary built from this checkout
(a REST API and an HTTP API driven over the wire with a signed header); `read`
is from the code; `model` quotes the model's documentation or traits.

## 1. Summary

Two control planes (`/restapis/...` and `/v2/apis/...`, both restJson1) over one
bbolt store, and one data plane (`/_aws/execute-api/...` and the virtual-host
form) that routes a request by AWS's precedence rules into a Lambda function, an
HTTP endpoint or a mock. Peers: Lambda (integrations and authorizers), CloudWatch
Logs (access and execution logs), CloudWatch (metrics), IAM (the function's
resource policy under soft and enforce), CloudFormation and `provision` (which
create it).

The design choice that shapes everything below: **the data plane serves the live
API, not the deployment a stage points at.** The ledger says so. The console
says the opposite in four places, and one P0 (AGW-1) comes from treating a
deployment as a real thing in one code path and not in the others.

**Estimate:** ~~L, five or more focused days~~ **XL, about fifteen focused days
(three calendar weeks)**, re-estimated 2026-10-11 after reading both models.
*Corrected 2026-10-11:* the first estimate counted 30 findings and a console
that "exists". The review found 40 more (15 at P1, none at P0, that change what a deploy
sees: stage variable, stage name and tag rules unenforced; members accepted and
lost; wrong codes for an unreachable backend; a silent 10 MiB truncation; HTTP
API parameter mapping stored and never applied), a model sweep whose
classifier misses nine operations, and a console of which 27 of 30 boards
differ in shape or content (section 6a). The breakdown is in section 11. The SQS lesson
applies: the document's size was several times low until its model
documentation was read, and this service has two models and 227 operations.
**Actual:** _fill in when done._

## 2. Contract

### REST (apigateway v1)

The model lists **124 operations** (`apigateway/testdata/ops_api-gateway.json`).
The ledger says "54 of 124"; its own functional rows name 55. Routed today: 52
in the generated route table (`apigateway/validate.go`, `routes`) plus
`GetRestApis` and the three tag operations added in `router.go`, which is 56
route entries for 55 operations (`CreateApiKey` and `ImportApiKeys` share one).
AGW-14 reconciles the count.
*Corrected 2026-10-11: was "52 in the generated route table ... 56 route entries";
`routes` has 51 entries (`grep -c "{Op: " validate.go`), so 51 + the four added in
`router.go` = 55 entries for the 55 functional operations. `ImportApiKeys` is not
a route of its own: it is the `?mode=import` branch of the `CreateApiKey` route
and answers 501. Source: `read`; the 55 / 51 / 18 split was re-run over the wire,
signed, on 2026-10-11 (`run`).*

| Operations | n | Now | Target | Note |
|---|---|---|---|---|
| CreateRestApi, GetRestApi, GetRestApis, UpdateRestApi, DeleteRestApi | 5 | F | F | Per-setting tier below. AGW-7, AGW-10, AGW-11, AGW-12. |
| CreateResource, GetResource, GetResources, UpdateResource, DeleteResource | 5 | F | F | AGW-8. |
| PutMethod, GetMethod, DeleteMethod | 3 | F | F | |
| UpdateMethod | 1 | **absent** (405) | F | AGW-3. |
| PutIntegration, GetIntegration, DeleteIntegration | 3 | F | F | Per-type and per-setting tier below. AGW-5, AGW-6. |
| UpdateIntegration | 1 | **absent** (405) | F | AGW-3. |
| PutMethodResponse, GetMethodResponse, DeleteMethodResponse | 3 | F | C | Stored and returned; nothing on the request path reads a method response. The ledger says F. |
| UpdateMethodResponse | 1 | **absent** (405) | C | AGW-3. |
| PutIntegrationResponse, GetIntegrationResponse, DeleteIntegrationResponse | 3 | F | F for MOCK, C otherwise | Only `invokeMock` reads them. Depends on the decision in AGW-5. |
| UpdateIntegrationResponse | 1 | **absent** (405) | as above | AGW-3. |
| CreateDeployment, GetDeployment, GetDeployments, DeleteDeployment | 4 | F | F | A deployment is a record, not a snapshot. AGW-1. |
| UpdateDeployment | 1 | **absent** (405) | F | AGW-3. |
| CreateStage, GetStage, GetStages, UpdateStage, DeleteStage | 5 | F | F | Per-setting tier below. AGW-9, AGW-10, AGW-15. |
| FlushStageCache, FlushStageAuthorizersCache | 2 | **absent** (404) | FlushStageAuthorizersCache F (the cache is real; v2 has `ResetAuthorizersCache`), FlushStageCache C | AGW-13. |
| CreateAuthorizer, GetAuthorizer, GetAuthorizers, UpdateAuthorizer, DeleteAuthorizer | 5 | F | F | TOKEN and REQUEST. `COGNITO_USER_POOLS` is refused with 400. |
| TestInvokeAuthorizer, TestInvokeMethod | 2 | **absent** (405) | S or F: decide | AGW-13. |
| CreateApiKey, GetApiKey, GetApiKeys, UpdateApiKey, DeleteApiKey | 5 | F | F | AGW-12, AGW-17. |
| ImportApiKeys | 1 | S (501) | S | |
| CreateUsagePlan, GetUsagePlan, GetUsagePlans, UpdateUsagePlan, DeleteUsagePlan | 5 | F | F | `throttle` and `quota` are C. AGW-18. |
| CreateUsagePlanKey, GetUsagePlanKey, GetUsagePlanKeys, DeleteUsagePlanKey | 4 | F | F | |
| GetUsage | 1 | S (501) | S | |
| UpdateUsage | 1 | **absent** (404) | S | The ledger lists it as S; the refusal family is routed at `/usageplans/{id}/usage`, and UpdateUsage is `/usageplans/{id}/keys/{keyId}/usage`. AGW-13. |
| GetAccount, UpdateAccount | 2 | F | C | The CloudWatch role round-trips and does nothing. The ledger says F and "nominal" in the note. *Corrected 2026-10-11: was "the CloudWatch role and throttle settings round-trip"; `throttleSettings` is the constant `{burstLimit 5000, rateLimit 10000}` (`account.go`, `writeAccount`) and `UpdateAccount` refuses every path but `/cloudwatchRoleArn` (400 "Invalid patch path"). The values are AWS's account default (`doc`: limits page, 10,000 RPS and a bucket of 5,000). An unset role is returned as `""`, not omitted (AGW-69). Source: `read`, `run`.* |
| GetTags, TagResource, UntagResource | 3 | F | F | REST API ARNs only. AGW-16. *Corrected 2026-10-11: the handlers are shared with the HTTP API; the ARN shape picks the plane (`/restapis/` or `/apis/`), and an ARN of the other kind is a 404. The rules the model gives for a tag are not enforced (AGW-34). `TagResource` on the HTTP plane answers 204 where the model says 201 (AGW-58).* |
| PutRestApi, ImportRestApi | 2 | **absent** (405; ImportRestApi falls into CreateRestApi and answers a 400 about `name`) | S | AGW-13. |
| GetExport, GetSdk | 2 | **absent** (404) | S | The ledger's "SDK and export generation (5 operations)" names a family of 4; two of the four are not routed to the refusal. AGW-13, AGW-14. |
| GetSdkType, GetSdkTypes | 2 | S (501) | S | |
| Domain names (5), base path mappings (5) | 10 | S (501) | S | |
| CreateDomainNameAccessAssociation, DeleteDomainNameAccessAssociation, GetDomainNameAccessAssociations, RejectDomainNameAccessAssociation | 4 | **absent** (404) | S | AGW-13. |
| Client certificates | 5 | S (501) | S | |
| VPC links | 5 | S (501) | S | |
| Request validators (5), models (5), GetModelTemplate | 11 | S (501) | S, deferred | The ledger says 10. A method's `requestValidatorId` and `requestModels` are stored with nothing to point at. |
| Documentation parts (6, with ImportDocumentationParts), versions (5) | 11 | S (501) | S | The ledger says 10. |
| Gateway responses (Put, Get, Gets, Update, Delete) | 5 | S (501) | S, deferred | The ledger says 4. |

Totals as found: 55 functional, 51 refused with 501, **18 with no tier** (8
answer 405, 9 answer 404, and ImportRestApi answers a misleading 400). 55 + 51 + 18 = 124.

**RestApi settings**

| Setting | Now | Target | To confirm |
|---|---|---|---|
| name, description, version | F | F | Shown and returned. |
| apiKeySource | F | F | `AUTHORIZER` reads the authorizer's `usageIdentifierKey`. |
| endpointConfiguration.types | C | C | Stored; `EDGE` is filled in when absent. Nothing differs by type. |
| policy | stored, never evaluated, not validated | F or C: decide | AGW-7. |
| disableExecuteApiEndpoint | stored, not applied | F | AGW-7. |
| binaryMediaTypes | stored, not applied | not established | A non-UTF-8 body is base64 whatever this says. AGW-7. |
| minimumCompressionSize | stored, not applied | C | *Added 2026-10-11 (model): "non-negative between 0 and 10485760 (10M) bytes, inclusive"; a null disables compression. Not range-checked, and the patch path is ignored (AGW-52).* |
| tags | F | F | Key, value and prefix rules unenforced (AGW-34). |
| securityPolicy, endpointAccessMode, endpointConfiguration.ipAddressType, endpointConfiguration.vpcEndpointIds, cloneFrom | accepted and **lost** | C (round trip), `cloneFrom` F or refused | *Added 2026-10-11, run: AGW-35.* |

**Stage settings**

| Setting | Now | Target | To confirm |
|---|---|---|---|
| deploymentId | stored; changes nothing that is served | C, said so | The decision in section 10. |
| variables | F | F | On the proxy event and readable by a REQUEST authorizer. Not substituted into an integration URI (`${stageVariables.x}`), which AWS documents. AGW-19. *Added 2026-10-11 (model): "Variable names can have alphanumeric and underscore characters, and the values must match [A-Za-z0-9-._~:/?#&=,]+"; `doc` (quotas): 100 per stage, key 64, value 512. None enforced (AGW-32).* |
| accessLogSettings | F | F | |
| methodSettings: logging/loglevel, logging/dataTrace | F | F | |
| methodSettings: metrics/enabled | not established | C or F | Metrics are published for every request whatever it says. |
| methodSettings: throttling/*, caching/* | C | C | Stored, reported with AWS's defaults filled in. |
| tracingEnabled | C | C | |
| cacheClusterEnabled, cacheClusterSize | dropped: not stored, reads back `false` | C | AGW-15. *Added 2026-10-11: `cacheClusterStatus` is always `NOT_AVAILABLE`; the model's enum (CREATE_IN_PROGRESS, AVAILABLE, DELETE_IN_PROGRESS, NOT_AVAILABLE, FLUSH_IN_PROGRESS) is a status model, L7 (AGW-67). `UpdateStage` refuses `/cacheClusterEnabled` (AGW-55).* |
| canarySettings, documentationVersion, clientCertificateId | dropped | C or refused | AGW-15. |
| description, tags | stored | C | Whether stage tags can be read back is not established (AGW-16). |

**Integration types and settings (REST)**

| Type or setting | Now | Target | To confirm |
|---|---|---|---|
| AWS_PROXY | F | F | |
| MOCK | F | F | Answers from the integration response with the lowest status. `requestTemplates` (which on AWS choose the status) are not read. |
| HTTP_PROXY | F | F | AGW-2. |
| HTTP | forwards as HTTP_PROXY; templates and integration responses ignored | decide | AGW-5. The ledger says F. |
| AWS (non-proxy) | saves, deploys, then 500 on every call | decide | AGW-5. The ledger says S. |
| uri `{param}` placeholders | F | F | |
| integrationHttpMethod | F except `ANY` | F | AGW-2. |
| timeoutInMillis | stored, not applied | C or F | AGW-6. *Added 2026-10-11 (model): 50 to 29,000 ms, default 29,000, raisable "for Regional or private APIs only"; a value of 5 was accepted (run). `doc` (quotas): "You can't set the integration timeout to less than 50 milliseconds."* |
| requestTemplates, requestParameters, passthroughBehavior, contentHandling, credentials, cacheKeyParameters, cacheNamespace | stored, not applied | C | AGW-6. |
| connectionType `VPC_LINK` | accepted | refused, as v2 does | AGW-6. |

**Authorizer settings (REST)**

| Setting | Now | Target |
|---|---|---|
| type TOKEN, REQUEST | F | F |
| type COGNITO_USER_POOLS | refused, 400 | refused |
| identitySource, identityValidationExpression | F | F *Added 2026-10-11 (model): identitySource is "required" for TOKEN and COGNITO_USER_POOLS and "required when authorization caching is enabled" for REQUEST; neither is enforced (a TOKEN authorizer with none is given `method.request.header.Authorization`, a REQUEST authorizer with a 300 s default TTL and none is stored), AGW-38.* |
| authorizerResultTtlInSeconds, 0 to 3600, default 300 | F | F *Source: `model`, documentation text only ("If this field is not set, the default value is 300. The maximum value is 3600, or 1 hour"); there is no `@range` trait on the REST member, so the range is hand-written in `checkAuthorizer`, and `UpdateAuthorizer` does not repeat it for a non-numeric value (`atoiOr` keeps the old one silently).* |
| authorizerCredentials, authType | stored | C |

**Method settings (REST)**

| Setting | Now | Target |
|---|---|---|
| authorizationType NONE, CUSTOM | F | F *Added 2026-10-11 (model): "Valid values are NONE for open access, AWS_IAM for using AWS IAM permissions, CUSTOM for using a custom authorizer, or COGNITO_USER_POOLS"; the member is a plain string and any other value is stored (run: `BOGUS_TYPE`), AGW-36. COGNITO_USER_POOLS is refused 400 (AGW-65).* |
| authorizationType AWS_IAM | stored, not checked | C, said so. The v2 ledger says this; the v1 ledger is silent. |
| apiKeyRequired | F | F |
| requestParameters, requestModels, requestValidatorId, operationName | stored | C |

### HTTP (apigatewayv2)

The model lists **103 operations** (`ops_apigatewayv2.json`). 37 are routed
(`apigateway/v2_validate.go`, `routesV2`), which matches the ledger.

| Operations | n | Now | Target | Note |
|---|---|---|---|---|
| CreateApi, GetApi, GetApis, UpdateApi, DeleteApi | 5 | F | F | `WEBSOCKET` is 501. `target` quick-create. AGW-12. |
| DeleteCorsConfiguration | 1 | F | F | |
| CreateRoute, GetRoute, GetRoutes, UpdateRoute, DeleteRoute | 5 | F | F | |
| CreateIntegration, GetIntegration, GetIntegrations, UpdateIntegration, DeleteIntegration | 5 | F | F | AWS_PROXY and HTTP_PROXY. |
| CreateAuthorizer, GetAuthorizer, GetAuthorizers, UpdateAuthorizer, DeleteAuthorizer | 5 | F | F | REQUEST only; JWT is 501. |
| ResetAuthorizersCache | 1 | F | F | |
| CreateStage, GetStage, GetStages, UpdateStage, DeleteStage | 5 | F | F | |
| DeleteAccessLogSettings, DeleteRouteSettings | 2 | F | F | |
| CreateDeployment, GetDeployment, GetDeployments, UpdateDeployment, DeleteDeployment | 5 | F | F | A record, as for REST. |
| GetTags, TagResource, UntagResource | 3 | F | F | |
| ImportApi, ReimportApi | 2 | **absent** (405) | S | The ledger lists them as S. AGW-13. |
| ExportApi | 1 | S (501) | S | |
| Domain names (5), API mappings (5) | 10 | S (501) | S | The ledger says 11. |
| VPC links | 5 | S (501) | S | |
| Models (5), GetModelTemplate | 6 | S (501) | S, deferred | |
| Route responses (5), integration responses (5) | 10 | S (501) | S | |
| DeleteRouteRequestParameter | 1 | S (501) | S | |
| Portals, portal products, product pages, endpoint pages, sharing policy, routing rules | 31 | S (501) | S | The ledger says 26. Routing rules are refused through the `domainnames` family, with that family's message. |

Totals as found: 37 functional, 64 refused with 501, 2 with no tier.

**HTTP API settings**

| Setting | Now | Target | To confirm |
|---|---|---|---|
| Api: name, description, version, corsConfiguration | F | F | CORS is answered on the data plane. `maxAge` -1 to 86400 from the model. |
| Api: disableExecuteApiEndpoint, routeSelectionExpression, ipAddressType, disableSchemaValidation | stored | C (`disableExecuteApiEndpoint` F) | *Corrected 2026-10-11: whether `disableExecuteApiEndpoint` is applied **is** established as to behaviour (model: "clients can invoke your API by using the default execute-api endpoint ... To require that clients use a custom domain name to invoke your API, disable the default endpoint"); the status and body of a refused call are `memory`. `disableSchemaValidation` is "Supported only for WebSocket APIs" (model). `routeSelectionExpression` "must be ${request.method} ${request.path}" for an HTTP API (model); the code stores any string.* |
| Route: routeKey, target, authorizationType NONE and CUSTOM, authorizerId | F | F | |
| Route: authorizationType AWS_IAM | C | C | The ledger says so. |
| Route: authorizationType JWT | S (501) | S | |
| Route: apiKeyRequired, authorizationScopes, requestParameters, requestModels, operationName | stored | C | *Added 2026-10-11 (model): `apiKeyRequired` "Supported only for WebSocket APIs", as are `requestParameters`, `requestModels`, `modelSelectionExpression` and `routeResponseSelectionExpression`; `authorizationScopes` are for a JWT authorizer. An HTTP API route has no API-key gate, which is what Apigw-25 says. The code stores `apiKeyRequired` and ignores it on the data plane, as the model implies.* |
| Integration: payloadFormatVersion 1.0 and 2.0, integrationMethod | F | F | |
| Integration: timeoutInMillis, requestParameters, responseParameters, credentialsArn | stored | C (timeout F) | Not in the ledger. *Corrected 2026-10-11: `requestParameters` and `responseParameters` are not cosmetic on AWS. `doc` (parameter mapping page): keys `append\|overwrite\|remove:header.{name}`, `...:querystring.{name}`, `overwrite:path`, response `overwrite:statuscode`, values `$request.header.x`, `$context.x`, `$stageVariables.x`, static strings, with a reserved-header list. An app that relies on them behaves differently here with no refusal (AGW-43). `timeoutInMillis` is `range` 50 to 30000, default 30000 (model) and is not applied (AGW-6).* |
| Integration: integrationSubtype, connectionType VPC_LINK | S (501) | S | |
| Stage: autoDeploy, stageVariables, accessLogSettings | F | F | |
| Stage: defaultRouteSettings, routeSettings | C | C | |
| Authorizer: identitySource, payload version, enableSimpleResponses, TTL 0 to 3600 | F | F | *Added 2026-10-11 (model): `authorizerPayloadFormatVersion` "Required for HTTP API Lambda authorizers" (the code defaults it to 2.0); `identitySource` is `required` in the model although its own prose says "For a REQUEST authorizer, this is optional" (the `required` trait wins: the SDKs refuse a call without it); `authorizerResultTtlInSeconds` "Supported only for HTTP API Lambda authorizers", default not stated (the code's 0 is `memory`).* |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `apigateway/`: 41 `func Test` in 18 files (*Corrected 2026-10-11: was "40 tests in 16 files"; one is `TestMain`, one file is a fixture with no tests*). Data plane and routing precedence (`execute_test.go` 4, `v2_execute_test.go` 4), SDK end to end (`sdk_test.go` 2, `v2_sdk_test.go` 1, `apikeys_sdk_test.go` 1, `logging_sdk_test.go` 1), authorizers and the cache (`authorize_test.go` 2, `authcache_*` 6), API keys (`apikey_test.go` 1), invoke URLs 3, v2 audit 7, `conformance_fixes_test.go` 1 |
| Parity | `rejection_parity_test.go` (2), `v2_parity_test.go` (1), `validate_hook_test.go` (4). Ledger: 118 of 126 constraints for REST, 88 of 94 for HTTP, `knownGaps` and `knownGapsV2` empty, the rest unexpressible on this wire |
| CloudFormation | `cloudformation/apigw_authorizer_apply_test.go`, `apigw_keys_apply_test.go`, `apigwv2_apply_test.go` |
| Console (Go) | `internal/console/apigw_auth_test.go`, `apigw_keys_test.go`, `apigw_v2_test.go`: one test each |
| Conformance | `conformance/tests/test_apigateway.py`: 4 scenarios (REST mock, REST to Lambda, HTTP to Lambda, refusals), all **unverified**: `conformance/snapshots/` is empty |
| Browser | `e2e/tests/apigateway.spec.ts` 8, `apigateway-http.spec.ts` 4, `apigateway-keys.spec.ts` 1 |
| Mutation | No per-package baseline (foundation B3) |
| Restart | *Corrected 2026-10-11: was "No test writes an API, restarts and reads it".* `persistence_surfaces_test.go` `TestPersistenceOfRecentSurfaces` writes a REST API with an authorizer, a key, a usage plan and a plan key, and an HTTP API with a route, an integration and a stage, restarts, and reads each back. Nothing covers a REST resource tree, method, integration, response, deployment or stage, a stage's variables and settings, or HTTP deployments and CORS. The authorizer cache is memory only by design. |

The ledger's "Verified against" names "the dispatch table against
`testdata/ops_api-gateway.json`". *Corrected 2026-10-11: was "No Go file reads that file".* `apigateway/` has no such test, but the root `modelsweep_test.go` (foundation B6) sends every operation of both models and freezes the classes in `testdata/modelsweep.json`: REST 51 `legacystub` and 8 `unknown`, HTTP 64 and 2. It classes a 404 `NotFoundException` as "handled", so nine REST operations that answer "unknown resource" are not in its `unknown` list (AGW-60). The 18 REST operations with no tier are what the refined test of AGW-13 would catch.

## 4. Findings

Evidence `run` means reproduced on 2026-10-10 against the package through its
HTTP handler, with a throwaway test loaded by `go test -overlay`.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| AGW-1 | P0 | `CreateDeployment` with a `stageName` that already exists replaces the stage. Its variables, method settings (logging, throttling, caching), tags, access-log settings and tracing flag are gone. The console's Deploy button does exactly this call. | `apigateway/deployments.go`, `createDeployment`, the `api.Stages[req.StageName] = &stage{...}` assignment | run (re-run 2026-10-11: a second `CreateDeployment` to `prod` left no variables and no method settings) | memory: AWS moves the stage to the new deployment and keeps its settings *Reviewed 2026-10-11: stays `memory`. The model is silent. `doc` (updating-api, read 2026-10-11) lists "Stage" under configurations whose update "does not require redeploying the API", which says stage settings are not part of a deployment but not what a redeploy does to an existing stage. Queued as `redeploy_keeps_stage`.* | Handler test: stage with a variable and a log level, redeploy to the same name, read the stage |
| AGW-2 | P0 | A REST `HTTP` or `HTTP_PROXY` integration with `integrationHttpMethod: ANY` sends the backend a request whose method is the literal word `ANY`. The v2 path treats `ANY` as "use the caller's method". The console writes `integrationHttpMethod = verb`, so wiring a URL on an `ANY` method produces it. | `apigateway/execute.go`, `invokeHTTP`; `internal/console/client_apigw.go`, `PutAPIIntegration` | run (backend saw `ANY /x` for a `POST`) | doc, read 2026-10-11: api-gateway-set-up-simple-proxy ("If you integrate the `ANY` method on a proxy resource of `/api/{proxy+}` with the backend endpoints ... a single integration request can support any HTTP operations (GET, POST, etc.)"); the HTTP API page: "Integrate the route with an HTTP endpoint ... on the `ANY` method". *Was `memory`.* | Execute test: `ANY` method, `HTTP_PROXY`, `integrationHttpMethod` `ANY`, assert the backend's method |
| AGW-3 | P0 | `UpdateMethod`, `UpdateIntegration`, `UpdateMethodResponse`, `UpdateIntegrationResponse` and `UpdateDeployment` are not routed. Each answers `405 MethodNotAllowed`. A Terraform or CDK update that patches an existing method or integration fails here and succeeds on AWS. *Added 2026-10-11 (model): `UpdateMethodResponse` answers **201** (`PATCH ... -> 201`), the other four 200; `UpdateDeployment` also lists `ServiceUnavailableException` (503).* | `apigateway/validate.go` `routes`; `apigateway/router.go` `v1Handlers` | run | model: all five are in the model with PATCH bindings | SDK test: put a method and integration, patch each, read it back |
| AGW-4 | P0 | An `HTTP` (non-proxy) integration is served exactly as `HTTP_PROXY`: `requestTemplates`, `requestParameters`, integration responses and selection patterns are stored and never applied. The call succeeds with a different request and response from the one the definition describes. The ledger tiers `HTTP / HTTP_PROXY` as F. | `apigateway/execute.go`, `invokeIntegration`, `case "HTTP", "HTTP_PROXY"` | read | model, 2026-10-11: `Integration.requestTemplates` "Represents a map of Velocity templates that are applied on the request payload based on the value of the Content-Type header sent by the client"; `IntegrationResponse` "The status code must map to an existing MethodResponse, and parameters and templates can be used to transform the back-end response." *Was `doc` ("Set up HTTP integrations", not re-read).* | Execute test: `HTTP` with a request template and a 200 integration response template; assert what the backend receives and what the caller gets |
| AGW-5 | P1 | An `AWS` (non-proxy) integration is accepted by `PutIntegration` and by `CreateDeployment`, then answers 500 on every call. The ledger tiers it S, "refused". It is refused only once the app is running. | `apigateway/methods.go`, `putIntegration`; `execute.go`, `case "AWS"` | read | n/a (the limit is ours) *Added 2026-10-11 (model): `Integration.type` documents `AWS` as a valid type and `uri` gives its `arn:aws:apigateway:{region}:{subdomain.service\|service}:path\|action/{service_api}` form; AWS accepts it. Direction: the stack deploys and the call fails (stricter at call time, not at deploy).* | Decide first (section 10), then a handler test either way |
| AGW-6 | P1 | REST integration settings are stored and have no effect, and the ledger does not say so: `timeoutInMillis` (the forward uses `http.DefaultClient` with no deadline), `passthroughBehavior`, `contentHandling`, `credentials`, `cacheKeyParameters`, `cacheNamespace`. `connectionType: VPC_LINK` is accepted where v2 refuses it. *Added 2026-10-11: the HTTP API's `timeoutInMillis` (default 30000) is also stored and never applied (`v2_execute.go` builds `integration{Type, URI}` with no timeout), and a value below 50 is accepted (run: 5).* | `apigateway/methods.go`, `putIntegration`; `execute.go`, `invokeHTTP` | read | model (all of it), 2026-10-11: `timeoutInMillis` "Custom timeout between 50 and 29,000 milliseconds. The default value is 29,000 milliseconds or 29 seconds. You can increase the default value to longer than 29 seconds for Regional or private APIs only."; `passthroughBehavior` (WHEN_NO_MATCH, WHEN_NO_TEMPLATES, NEVER, with their 415 behaviour); `cacheKeyParameters` "must also be specified for Method requestParameters". HTTP API: `timeoutInMillis` is a `range` trait 50 to 30000, "The default timeout is 29 seconds for WebSocket APIs and 30 seconds for HTTP APIs". *Was `model` (members), `doc` (meaning).* | Execute test: a backend that sleeps past `timeoutInMillis` |
| AGW-7 | P1 | RestApi settings stored and not applied. `policy`: any string is accepted, including `"not json"`, and nothing evaluates it under soft or enforce. `disableExecuteApiEndpoint: true` still serves; the console shows a chip saying the endpoint is disabled. `binaryMediaTypes` and `minimumCompressionSize` are not read on the request path. | `apigateway/restapis.go`, `createRestAPI`, `patchRestAPI`; `execute.go`, `serveExecute` | run (policy accepted), read (the rest) | model, 2026-10-11: `RestApi.policy` "A stringified JSON policy document that applies to this RestApi regardless of the caller and Method configuration"; `disableExecuteApiEndpoint` "Specifies whether clients can invoke your API by using the default execute-api endpoint ... To require that clients use a custom domain name to invoke your API, disable the default endpoint"; `binaryMediaTypes` "By default, the RestApi supports only UTF-8-encoded text payloads"; `minimumCompressionSize` "compression or decompression is not applied on the payload if the payload size is smaller than this value". doc (updating-api): a resource-policy update "requires redeploying the API". *Was `doc`.* | Handler test for a bad policy; execute test for a disabled endpoint |
| AGW-8 | P1 | `UpdateResource` validates nothing. Renaming to a sibling's name returns 200 and leaves two resources at one path; renaming to `has space` returns 200. `/parentId` is set without checking the parent exists or that the move makes a cycle. `CreateResource` refuses the first two. | `apigateway/resources.go`, `patchResource` | run (rename), read (`/parentId`) | model for the codes: `UpdateResource` lists BadRequestException (400), ConflictException (409) and NotFoundException (404). The conditions that choose between them stay `memory`. *Was `memory`.* Re-run 2026-10-11: rename to a sibling 200 (path `/items`), `has space` 200, `/parentId` of `nosuch` 200, `/parentId` of itself 200 (a cycle). | Handler test for each of the four |
| AGW-9 | P1 | A stage can point at a deployment that does not exist: `CreateStage` with `deploymentId: "nope"` is 201, and `UpdateStage` replacing `/deploymentId` with `nope` is 200. The v2 path checks. | `apigateway/stages.go`, `createStage`, `patchStage` | run | model: `CreateStage` "Creates a new Stage resource that references a pre-existing Deployment for the API." Whether the refusal is 400 or 404 (both in the operation's list) is `memory`. *Was `memory`.* | Handler test for both |
| AGW-10 | P1 | Deleting something that is not there succeeds: `DeleteStage` on a missing stage is 202, `DeleteRestApi` on a missing API is 202, and `DeleteMethod`, `DeleteIntegration`, `DeleteMethodResponse`, `DeleteIntegrationResponse` on a missing verb or status are 204. | `apigateway/stages.go` `deleteStage`; `store.go` `Delete`; `methods.go` `deleteMethod` and the three beside it | run (stage, API), read (the four) | model, 2026-10-11: `DeleteStage`, `DeleteRestApi`, `DeleteMethod`, `DeleteIntegration`, `DeleteMethodResponse` and `DeleteIntegrationResponse` each list NotFoundException (404). *Was `doc`.* Re-run: `DeleteRestApi` on an unknown id 202, `DeleteStage` 202, `DeleteMethod` on an unknown verb 204. | Handler test per operation |
| AGW-11 | P1 | `UpdateRestApi` answers 200 for a patch path it does not know, and ignores the `op` (a `remove` is a replace with an empty value). `UpdateStage` refuses an unknown path. Paths AWS supports and this does not: `/binaryMediaTypes/...`, `/minimumCompressionSize`, `/disableExecuteApiEndpoint`, `/endpointConfiguration/types/...`. | `apigateway/restapis.go`, `patchRestAPI` | run (`/bogus` is 200) | model: `PatchOperation.op` "Not all valid operations are supported for a given resource ... Attempts to apply an unsupported operation on a resource will return an error message." The supported-path table is `doc` (Patch Operations, not read). *Was `doc`.* Re-run: `/bogus` 200; `/minimumCompressionSize`, `/disableExecuteApiEndpoint` and `/binaryMediaTypes/...` answer 200 and change nothing. | Handler test for an unknown path and for each supported path |
| AGW-12 | P1 | No list operation paginates. `limit` and `position` (REST) and `maxResults` and `nextToken` (HTTP) are bound in the route tables and never read. *Corrected 2026-10-11: not "every": REST `GetStages` has only a `deploymentId` filter (no `limit`, no `position`), `GetTags` says "(Not currently supported)" for both, `GetGatewayResponses` "does not support pagination". The model: "The maximum number of returned results per page. The default value is 25 and the maximum value is 500." A caller that does not page gets 25 on AWS and everything here (run: 32 APIs with no limit, with `limit=2` and with `limit=501`): more forgiving. HTTP API `maxResults`/`nextToken` are strings with no stated default or maximum.* | every `get...s` and `list...` handler in `apigateway/` | read | model | SDK test: three APIs, `limit: 1`, follow `position` |
| AGW-13 | P1 | Thirteen REST and two HTTP operations that are not served answer a bare 404 or 405 instead of the 501 by name that the ledger promises: `FlushStageCache`, `FlushStageAuthorizersCache`, `GetExport`, `GetSdk`, `UpdateUsage`, the four domain-name access-association operations (404); `PutRestApi`, `TestInvokeMethod`, `TestInvokeAuthorizer` (405); `ImportRestApi`, which reaches `CreateRestApi` and answers a 400 about `name`; v2 `ImportApi` and `ReimportApi` (405). *Corrected 2026-10-11: the counts hold when the request is signed (`run`: 8 x 405 and 9 x 404; ImportRestApi 400). Unsigned, three of the paths are not in the gateway's prefix list and S3 answers them (AGW-61). The root `modelsweep_test.go` already sends every operation and freezes the classes in `testdata/modelsweep.json`, but counts the nine 404s as handled (AGW-60).* | `apigateway/router.go`, `v1Specs`, `v2Specs` | run | model | Table test: every operation in both `ops_*.json` files, built from its model binding, answers 2xx, a modelled 4xx, or 501, never an unmodelled 404 or 405 |
| AGW-14 | P3 | The ledger's counts do not add up. REST: "54 of 124" beside rows naming 55; domains "12" (10, or 14 with the access associations); validators and models "10" (11); documentation "10" (11); SDK and export "5" (4); gateway responses "4" (5). HTTP: domains and mappings "11" (10); portals and routing rules "26" (31). `not-built.md` repeats them. "Verified against" cites a dispatch-table test over `ops_api-gateway.json` that does not exist. *Corrected 2026-10-11: "No Go file reads that file" was wrong: the root `modelsweep_test.go` reads both `ops_*.json` and `testdata/modelsweep.json` holds its baseline (apigateway: 51 `legacystub`, 8 `unknown`; apigatewayv2: 64 and 2). The ledger counts were checked against `docs/SUPPORT.md` and are as stated (54, 12, 10, 10, 5, 4; 11, 26); the "Verified against" sentence still names a test that does not assert them.* | `docs/SUPPORT.md`, both API Gateway sections; `docs/not-built.md` | read | model | The table test from AGW-13, plus a ledger test that the rows sum to the model's count |
| AGW-15 | P1 | Stage members that are not round-tripped. `cacheClusterEnabled` and `cacheClusterSize` are not decoded and read back `false` and `NOT_AVAILABLE` whatever was sent; `canarySettings`, `documentationVersion` and `clientCertificateId` are dropped. Cosmetic means accepted and returned; these are accepted and lost. *Added 2026-10-11: `CreateDeployment` also takes `cacheClusterEnabled`, `cacheClusterSize`, `tracingEnabled` and `canarySettings` and keeps none (run: `tracingEnabled: true` read back `false`). `clientCertificateId` cannot be sent to `CreateStage` (it is on `Stage` only, set by patch). The other lost members are AGW-35; `UpdateStage` refuses the patch paths for all of these, AGW-55.* | `apigateway/stages.go`, `createStage`; `views.go`, `viewStage` | read | model | SDK test: create a stage with each, read it back |
| AGW-16 | P1 | `TagResource` on a stage ARN (`.../restapis/{id}/stages/{name}`) answers 204 and tags the **API**: `apiIDFromARN` takes the API id and nothing checks the rest. A stage's own tags (accepted at `CreateStage`) cannot be read or changed. | `apigateway/tags.go`, `tagTarget` | run (2026-10-11: `TagResource` on a stage ARN answered 204; the API then held the tags, including a 200-character key and `aws:reserved`; the stage read back none) | model, 2026-10-11: `CreateStageRequest.tags` and `Stage.tags` ("The collection of tags. Each tag element is associated with a given resource."); `TagResource` "The ARN of a resource that can be tagged". The stage ARN's shape (`arn:aws:apigateway:{region}::/restapis/{id}/stages/{name}`) is `memory`. *Was `doc`.* | Handler test: tag a stage ARN, read the API's tags and the stage's |
| AGW-17 | P3 | An API key value over 128 characters is refused with "API Key value should be at least 20 characters". | `apigateway/apikeys.go`, `createAPIKey` | read | memory for the message. doc, read 2026-10-11 (usage plans page): "The name cannot exceed 1024 characters. The value is an alphanumeric string between 20 and 128 characters" and "API key values must be unique". The model says only "Specifies a value of the API key." *Was `memory`.* The rules the page adds are AGW-51. | Handler test with 129 characters |
| AGW-18 | P1 | A usage plan stage is not checked: `apiStages` and the `/apiStages` patch verify only that the id names any API, so an HTTP API's id is accepted, and a stage name that does not exist is accepted. | `apigateway/usageplans.go`, `createUsagePlan`, `patchUsagePlan` | read | model: `UsagePlan.apiStages` "The associated API stages of a usage plan"; `CreateUsagePlan` and `UpdateUsagePlan` list NotFoundException (404). Which refusal is `memory`. *Was `memory`.* | Handler test: an HTTP API id; a missing stage |
| AGW-19 | P1 | Stage variables are not substituted into an integration URI. `expandURI` replaces `{param}` only. A template that points a stage at its backend with `${stageVariables.url}` forwards to the literal string. | `apigateway/execute.go`, `expandURI` | read | doc, read 2026-10-11: stage-variables ("first create the `url` stage variable, and then in your API's integration request, enter the stage variable value, **http://${stageVariables.url}**. This value tells API Gateway to substitute your stage variable `${}` at runtime"). The value rules are AGW-32. | Execute test: HTTP_PROXY with `http://${stageVariables.host}/x` |
| AGW-20 | P2 | The Add resource and Rename dialogs' input `pattern` is `[a-zA-Z0-9._\-\{\}]+`. It blocks `+` and `:`, so `{proxy+}`, the commonest shape SAM and CDK emit, cannot be typed. The service accepts both. | `internal/console/templates/apigw.html`, `apigw_api` (the two dialogs) | read | n/a | Browser test adds `{proxy+}` |
| AGW-21 | P2 | Four console statements say edits wait for a deploy or that a stage serves its deployment. All are false here: "Edits change the DEFINITION only — callers see them after the next deploy"; "This is the step that makes edits callable"; "pointing a fresh stage at an old one is the rollback story"; and the repoint confirm, "Callers get it straight away" (foundation A13). | `templates/apigw.html`, `apigw_routes`, `apigw_stages` | read; the data plane reads `api.Resources`, never a deployment | n/a | Browser test asserts the replacement text; execute test: edit after deploy is served, and repointing a stage changes no answer |
| AGW-22 | P2 | The integration type options say "AWS — Lambda, templates apply" and "HTTP — forward, templates apply". No template is applied to either (AGW-4, AGW-5). "Integration responses — how backend answers map to it" is true for MOCK only. | `templates/apigw.html`, `apigw_method` | read | n/a | Browser test on the option text, tied to the tier table in section 2 |
| AGW-23 | P2 | A new HTTP API is announced as answering: the toast "HTTP API created — add a route, and it answers at $default" and the create page's "answering at $default as soon as a route exists". The console creates no stage, and the API answers 404 until one exists. | `internal/console/handlers_apigw_v2.go`, `apigwHTTPCreate`; `templates/create.html`, `apigw_create` | run (404 with a route and no stage) | n/a | Browser test: create, add a route, invoke, read the page's own warning |
| AGW-24 | P2 | The keys page builds its list pane from `ListRestAPIs`, so HTTP APIs disappear from the list on that page and the count changes. | `internal/console/handlers_apigw_keys.go`, `apigwKeys` | read | n/a | Browser test: one REST and one HTTP API, open the keys page, count the list |
| AGW-25 | P2 | The console cannot do what the API can, and in several cases cannot even show it. Fetched and never rendered: a stage's variables, tracing flag and method settings (`APIStage` carries them). No control for: stage variables, access-log and method settings, a usage plan's throttle and quota, removing a stage from a plan, renaming a key, a REST API's key source, editing an HTTP route (only delete and add), an HTTP stage's variables, HTTP deployments. | `internal/console/client_apigw.go`, `APIStages`; `templates/apigw.html`, `apigw_v2.html`, `apigw_keys.html` | read | n/a | One browser test per item the design adds (section 6) |
| AGW-26 | P2 | Wiring a Lambda integration from the console adds no Lambda permission. Under `--iam-mode enforce` every call through it is `502`, with the function's refusal of `apigateway.amazonaws.com` in the body. *Corrected 2026-10-11: the 502 is what an **outside** caller gets (`run`, enforce instance: `curl` to the execute-api path answers 502 "User: apigateway.amazonaws.com is not authorized to perform: lambda:InvokeFunction"). The console's own Invoke tab goes through as root and the function **launches** (run, same instance): the denial is not reachable from the Invoke tab, AGW-62. `doc` (stage-variables): "When specifying a Lambda function name as a stage variable value, you must configure the permissions on the Lambda function manually" and the console "pops up" the CLI command: a console behaviour, still not recordable.* | `internal/console/client_apigw.go`, `PutAPIIntegration`; `client_apigw_v2.go`, `AddHTTPRoute`; `client_apigw_auth.go`, `CreateAPIAuthorizer` | run (the 502 and its text) | memory: AWS's console adds the permission when you pick a function | Browser test under enforce: wire, invoke, read the answer |
| AGW-27 | P2 | The Invoke tab sends no headers but `Content-Type`. A method that requires an API key always answers 403 there and one behind an authorizer always 401, with nothing on the page to say why. It also offers no `OPTIONS` or `ANY`. | `internal/console/client_apigw.go`, `InvokeAPI`; `templates/apigw.html`, `apigw_invoke` | read | n/a | Browser test: invoke a key-required method, read the explanation |
| AGW-28 | P3 | A comment says deleting a deployment a stage serves leaves the stage serving. The service refuses that delete. | `internal/console/client_apigw.go`, above `DeleteAPIDeployment` | read | n/a | none; fix with AGW-21 |
| AGW-29 | P3 | The method panel names an authorizer by its id, not its name. | `templates/apigw.html`, `apigw_method` | read | n/a | Browser test |
| AGW-30 | P3 | The Invoke result colours any status of 400 or more as an error. A 403 or 404 from the data plane is the gateway refusing as AWS does; a 502 is the user's function. The foundation's vocabulary (A2) has no state for "the gateway answered, and it was not a 200". | `templates/apigw.html`, `apigw_result` | read | n/a | Settle with A2, then a browser test per state |

**Found by the pre-stage review, 2026-10-11 (AGW-31 to AGW-70).** The first
thirty findings were read from the code and the console. These were found by
reading the two models' documentation text, the AWS pages named in each row
(read that day), and by driving a built binary. The model's own text is quoted
in the Source column; `run` means reproduced over the wire against this checkout,
signed, on a fresh data directory. The direction is named because "stricter
than AWS" breaks a correct caller exactly as "more forgiving" breaks a deploy.

| Id | Sev | What | Where | Evidence | Direction | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| AGW-31 | P1 | **A deployment is not a snapshot here.** The data plane reads `api.Resources`, never a deployment, so an edit is served at once and a stage pointed at an old deployment serves the live API. An app that never calls `CreateDeployment` after an edit (a CLI script, a CloudFormation template whose `AWS::ApiGateway::Deployment` has no trigger) works here and serves the **old** API on AWS: the bug is invisible until it is deployed. `Deployment.apiSummary` is never returned (AGW-57). The ledger and Apigw-09, -11 and -28 state the difference; this row names the direction and keeps the cost of closing it on the page. | `apigateway/execute.go` `serveExecute`, `matchResource`; `store.go` `deployment` | read; run (an edit after a deploy was served with no redeploy) | more forgiving, deliberate | model: Deployment "An immutable representation of a RestApi resource that can be called by users using Stages. A deployment must be associated with a Stage for it to be callable over the Internet."; `apiSummary` "A summary of the RestApi at the date and time that the deployment resource was created". doc (how-to-deploy-api): "Every time you update an API, you must redeploy the API to an existing stage or to a new stage. Updating an API includes modifying routes, methods, integrations, authorizers, resource policies, and anything else other than stage settings." doc (updating-api): Stage, Usage plan, Account and Deployment updates "do not require redeploying" | Execute test run both ways: default, an edit is served; strict, an edit after the last deploy is not and the old definition answers. Decision in section 10 |
| AGW-32 | P1 | **Stage variable name and value rules are not enforced**, on either plane: a value with a space and braces (`has space and {json}`) is accepted by `CreateDeployment`, `CreateStage` and `UpdateStage` (`/variables/k`); on the HTTP plane a 100-character key and a 1,000-character value were accepted. No count limit. | `stages.go` `createStage`, `patchStage`; `deployments.go` `createDeployment`; `v2_stages.go` `applyV2StageInput` | run | more forgiving | model (REST `CreateStageRequest.variables`, `CreateDeploymentRequest.variables`, `Stage.variables`; v2 `CreateStageRequest.StageVariables`): "Variable names can have alphanumeric and underscore characters, and the values must match [A-Za-z0-9-._~:/?#&=,]+." v2 value type `StringWithLengthBetween0And2048`. doc (quotas, both pages): "Stage variables per stage 100", "Length, in characters, of the key in a stage variable 64", "of the value 512". The model's 2,048 and the page's 512 disagree: the page is the smaller, which one AWS enforces is `memory` and goes on the recording queue | Handler test per plane: a value with a space, a key with a hyphen, a 101st variable, a 513-character value |
| AGW-33 | P1 | **The stage name rule is not enforced**: `bad name/with*chars!` and a 200-character name were accepted by `CreateStage` (REST) and `bad name!` by `CreateStage` (HTTP). The console's `pattern` is the only check, and the `CreateDeployment` name has none. | `stages.go` `createStage`; `deployments.go` `createDeployment`; `v2_stages.go` `v2CreateStage` | run | more forgiving | model: "Stage names can only contain alphanumeric characters, hyphens, and underscores. Maximum length is 128 characters." (REST `CreateStageRequest.stageName`, `Stage.stageName`; v2 `DeleteStage` and four more); v2 `ResetAuthorizersCache`: "...or be $default" | Handler test: 129 characters, a space, a slash; `$default` allowed on the HTTP plane only |
| AGW-34 | P1 | **Tag rules are not enforced.** A 200-character key, the reserved `aws:` prefix and a 300-character value were accepted on a REST API ARN, and a 200-character key with a 2,000-character value on an HTTP API ARN. The same text bounds the `tags` of `CreateRestApi`, `CreateApiKey`, `CreateStage`, `CreateUsagePlan` and `CreateApi`; none is checked. The 50-tag limit the section 5 box wonders about is **per stage**, not per API. | `tags.go` `tagResource`; every `tags` member of a create body | run | more forgiving | model, REST: "The valid character set is [a-zA-Z+-=._:/]. The tag key can be up to 128 characters and must not start with aws: . The tag value can be up to 256 characters." (`TagResourceRequest.tags` and the create bodies). v2: value `StringWithLengthBetween1And1600` (the type's own text says "[0-1600]": the model disagrees with itself), key unconstrained. doc (quotas, both pages): "Tags per stage 50" | Rejection-parity cases per plane: key 129, value 257, `aws:` prefix, a character outside the set; v2 value 1,601 |
| AGW-35 | P1 | **Members accepted and lost** (the AGW-15 class, beyond the stage): `PutIntegration` drops `connectionId`, `tlsConfig` (`insecureSkipVerification`), `responseTransferMode` (`STREAM`) and `integrationTarget` (read back with none); `PutMethod` drops `authorizationScopes`; `CreateUsagePlan` drops `apiStages[].throttle` (per-method throttling); `CreateRestApi` drops `securityPolicy`, `endpointAccessMode`, `endpointConfiguration.ipAddressType` and `vpcEndpointIds`, and (read) ignores `cloneFrom`, so the new API is empty. A Terraform plan that sets any of them shows a diff on every apply. | `methods.go` `putIntegration`, `putMethod`; `usageplans.go` `createUsagePlan`; `restapis.go` `createRestAPI`; `views.go` | run | missing | model: the members and their documentation (`Integration.tlsConfig` "Specifies the TLS configuration for an integration", `responseTransferMode` "The response transfer mode of the integration", `ApiStage.throttle` "Map containing method level throttling information", `CreateRestApiRequest.cloneFrom` "The ID of the RestApi that you want to clone from") | SDK test: create with each, read back; `cloneFrom` copies the resource tree or is refused |
| AGW-36 | P1 | **Method and integration enumerations in plain strings are not checked**: `authorizationType: BOGUS_TYPE` is stored; `passthroughBehavior: WHENEVER` is stored; a `requestParameters` key that is not `method.request.{header\|querystring\|path}.{name}` is stored. | `methods.go` `putMethod`, `putIntegration` | run | more forgiving | model: `Method.authorizationType` "Valid values are NONE for open access, AWS_IAM for using AWS IAM permissions, CUSTOM for using a custom authorizer, or COGNITO_USER_POOLS for using a Cognito user pool"; `passthroughBehavior` "There are three valid values: WHEN_NO_MATCH, WHEN_NO_TEMPLATES, and NEVER"; `requestParameters` "A key ... matching the pattern of method.request.{location}.{name}, where location is querystring, path, or header". The model carries no `@enum` for these, so the generator cannot see them | Rejection-parity cases written by hand (the generator has nothing to read) |
| AGW-37 | P1 | **`PutIntegration` accepts what AWS describes as required or well-formed**: an `HTTP` integration with no `httpMethod`; a Lambda integration with `httpMethod: GET`; `uri: "not a url"` for `HTTP`; `timeoutInMillis` of 5. | `methods.go` `putIntegration` | run | more forgiving | model: `Integration.httpMethod` "For Lambda integrations, you must set the integration method to POST. For all other types, you must specify this property." (`MOCK` optional); `uri` "For HTTP or HTTP_PROXY integrations, the URI must be a fully formed, encoded HTTP(S) URL according to the RFC-3986 specification"; `timeoutInMillis` "between 50 and 29,000 milliseconds"; doc (quotas) "You can't set the integration timeout to less than 50 milliseconds" | Handler test per sentence |
| AGW-38 | P1 | **Authorizer identity-source rules are not enforced.** A `TOKEN` authorizer with no `identitySource` is given `method.request.header.Authorization`; a `REQUEST` authorizer with the default 300 s cache and no identity source is stored. | `authorizers.go` `checkAuthorizer` | run | more forgiving | model: `identitySource` "For a TOKEN or COGNITO_USER_POOLS authorizer, this is required ... For the REQUEST authorizer, this is required when authorization caching is enabled ... When the authorization caching is not enabled, this property is optional." | Handler test: TOKEN none; REQUEST ttl 300 none (refused), ttl 0 none (accepted) |
| AGW-39 | P1 | **An authorizer's `context` reaches the integration as it was sent; on AWS every value is a string and a nested value is refused.** `{"numberKey": 1}` arrives as the number 1; AWS delivers `"1"`. An object or array value is passed through; AWS answers a 500. A function written against the real event (`=== "1"`) fails here. | `authorize.go` `decide` (`resp.Context`), `execute_proxy.go` `buildProxyEvent`; `v2_authorize.go` | read | wrong shape | doc, read 2026-10-11 (api-gateway-lambda-authorizer-output): "The returned values are all stringified. Notice that you cannot set a JSON object or array as a valid value of any key in the `context` map." (REST; the HTTP API simple-response `context` is `memory`) | Execute test: an authorizer returning a number, a boolean and an object |
| AGW-40 | P2 | **The REST proxy event's `requestContext` is short.** It carries `resourceId`, `resourcePath`, `httpMethod`, `path`, `stage`, `apiId`, `accountId`, `requestId`, `protocol` and `identity.{sourceIp,userAgent}`. AWS also sends `extendedRequestId`, `domainName`, `domainPrefix`, `requestTime`, `requestTimeEpoch` and the whole `identity` block (`accessKey`, `caller`, `cognito*`, `user`, `userArn`, ... as `null`). A handler reading `requestContext.requestTimeEpoch` or `domainName` gets `undefined`. | `execute_proxy.go` `buildProxyEvent` | read | wrong shape | doc, read 2026-10-11 (set-up-lambda-proxy-integrations, the example event) | Execute test comparing the key set with the page's example |
| AGW-41 | P1 | **An integration that cannot be reached answers 502 with the Go error text; AWS's gateway-response table says 504.** Run: a connection refused to `localhost:3000` is `502 {"message":"Get \"http://localhost:3000/health\": dial tcp [::1]:3000: connect: connection refused"}`. There is no timeout (AGW-6), so INTEGRATION_TIMEOUT never happens either. A client retrying on 504 and not on 502 behaves differently. | `execute.go` `invokeHTTP` | run | wrong code | doc, read 2026-10-11 (supported-gateway-response-types): `INTEGRATION_FAILURE` 504 "The gateway response for an integration failed error", `INTEGRATION_TIMEOUT` 504; `AUTHORIZER_FAILURE` 500; `API_CONFIGURATION_ERROR` 500. Which of the two a refused connection is: `memory` (the model's `GatewayResponseType` enum has both) | Execute test: a closed port; a backend that sleeps past `timeoutInMillis` |
| AGW-42 | P1 | **Payload size is not limited as AWS limits it.** The REST plane reads up to 16 MiB and forwards; the HTTP plane reads `io.LimitReader(body, 10<<20)` and **silently truncates** a larger body to 10 MiB before the function sees it. AWS refuses with 413. | `execute.go` `invokeIntegration`; `v2_execute.go` `serveExecuteV2` | read | more forgiving; wrong data (HTTP) | doc: quotas "Payload size 10 MB"; gateway response `REQUEST_TOO_LARGE` 413 "HTTP content length exceeded 10485760 bytes" | Execute test: a 10 MiB + 1 body on both planes |
| AGW-43 | P1 | **HTTP API parameter mapping is a feature and is stored as a cosmetic setting.** `requestParameters` (`overwrite:path`, `append:header.x`, `remove:querystring.y`) and `responseParameters` (`overwrite:statuscode` per status) are saved and never applied; a template that rewrites a path or injects a header behaves differently with no refusal. The REST integration's `requestParameters` (`integration.request.header.X` from `method.request...`) and `responseParameters` are the same on the REST plane for `HTTP`/`HTTP_PROXY`. | `v2_integrations.go`; `v2_execute.go`; `methods.go` | read; run (stored) | missing | doc, read 2026-10-11 (http-api-parameter-mapping): supported keys and values, "Some headers are reserved", `$request.header`, `$request.querystring`, `$request.path`, `$request.body.{json path}` (truncated at 100 KB), `$context`, `$stageVariables`, static values; model: `RequestParameters` "For HTTP API integrations without a specified integrationSubtype request parameters are a key-value map specifying how to transform HTTP requests before sending them to the backend" | Execute test per key kind; a reserved header refused at create |
| AGW-44 | P2 | **The HTTP model's string lengths are invisible to the generator.** Fifty-one members of the thirteen served write operations carry their length only in the type's documentation (`StringWithLengthBetween1And128`: "A string with a length between [1-128]"); there is no `@length` trait, so `dzaudit` emitted nothing and "88 of 94" never counted them. Run: a 200-character `name`, a 2,000-character `description` and a 100-character `version` were accepted. | `dzaudit`; `v2_validate.go` | run | more forgiving | model: the type documentation, e.g. `Name` "[1-128]", `Description` "[0-1024]", `Version` "[1-64]", `Target` "[1-2048]"; the integer `range` traits (TTL 0 to 3600, timeout 50 to 30000, `maxAge`) *are* carried | Teach the generator the prose form; then the parity cases |
| AGW-45 | P1 | **`payloadFormatVersion` is defaulted and coerced.** An `AWS_PROXY` integration with none is stored as `2.0` (AWS requires it from the CLI, SDK and templates). An `HTTP_PROXY` integration sent `2.0` is stored as `1.0` and answers 201: the stored value is not the one sent. | `v2_integrations.go` `applyV2IntegrationInput` | run | more forgiving | model: "Required for HTTP APIs. Supported values for Lambda proxy integrations are 1.0 and 2.0. For all other integrations, 1.0 is the only supported value."; doc (http-api-develop-integrations-lambda): "If you create a Lambda integration by using the AWS CLI, CloudFormation, or an SDK, you must specify a `payloadFormatVersion`." | Handler test: none (refused); HTTP_PROXY 2.0 (refused) |
| AGW-46 | P2 | **A greedy path variable is accepted anywhere.** `GET /{proxy+}/x` was stored. | `v2_store.go` `validRouteKey` | run | more forgiving | doc, read 2026-10-11 (http-api-develop-routes): "The greedy path variable must be at the end of the resource path." (the REST resource tree: `memory`) | Handler test: key with `{proxy+}` not last |
| AGW-47 | P2 | **`deploymentId` can be changed on a stage with `autoDeploy` on.** | `v2_stages.go` `applyV2StageInput` | run | more forgiving | model: `Stage.DeploymentId` "Can't be updated if autoDeploy is enabled." | Handler test |
| AGW-48 | P2 | **Quick-create's integration, route and `$default` stage report `apiGatewayManaged: false`** and none of the managed rules holds: the `$default` route key was renamed (`GET /x` to `GET /y`) and the `$default` stage deleted. | `v2_routes.go` `viewV2Route`; `v2_integrations.go`; `v2_stages.go`; `v2_control.go` `v2QuickCreate` | run | wrong shape; more forgiving | model: Route "If you created an API using quick create, the $default route is managed by API Gateway. You can't modify the $default route key."; Stage "the $default stage is managed by API Gateway. You can't modify the $default stage."; Integration "You can update a managed integration, but you can't delete it." | Handler test: create with `target`, read `apiGatewayManaged`, then each refusal |
| AGW-49 | P2 | **No control-plane limit is enforced.** Run: 305 resources in one REST API, 13 stages, 12 authorizers. The model lists `LimitExceededException` (429) on the create operations. | every create handler | run | more forgiving | doc, read 2026-10-11 (quotas, both pages): resources per API 300, stages per API 10, authorizers per API 10, routes per API 300, integrations per API 300, API keys per account per Region 10,000, usage plans per account 300, usage plans per API key 10, stage variables per stage 100. Which are enforced at the control plane versus at deploy is `memory` | A limits table beside the validation table, each with `doc` named; opt-in or on (decision, L8) |
| AGW-50 | P2 | **A key can be in two usage plans that cover the same stage.** | `usageplans.go` `createUsagePlanKey`, `patchUsagePlan` | read | more forgiving | doc, read 2026-10-11 (usage plans page): "a given API key can only be associated with one usage plan for each stage of your API." | Handler test: attach a key to a second plan covering the same stage |
| AGW-51 | P2 | **API key name and value rules.** A 1,100-character name and a value with spaces and `!` were accepted. | `apikeys.go` `createAPIKey` | run | more forgiving | doc, read 2026-10-11 (usage plans page): "The name cannot exceed 1024 characters. The value is an alphanumeric string between 20 and 128 characters"; "API key values must be unique" (held) | Handler test |
| AGW-52 | P2 | **`minimumCompressionSize` is not range-checked** (99999999999 accepted at create) and its patch path is ignored. | `restapis.go` | run | more forgiving | model: "non-negative between 0 and 10485760 (10M) bytes, inclusive ... or disable compression (with a null value)" | Handler test: -1, 10485761, null |
| AGW-53 | P2 | **An access-log format with no `$context.requestId` is accepted** (REST patch, HTTP create). | `stages.go` `patchStage`; `v2_stages.go` | run | more forgiving | model, both: "The format must include at least $context.requestId." | Handler test per plane |
| AGW-54 | P2 | **A Firehose destination is refused.** `accessLogSettings.destinationArn` must name a log group; a `arn:aws:firehose:...:deliverystream/amazon-apigateway-x` is a 400. AWS accepts it for a REST stage. A CDK stack with `FirehoseLogDestination` fails to apply. | `stages.go` `patchStage`; `logging.go` `logGroupFromARN` | run | stricter | model (REST): "The Amazon Resource Name (ARN) of the CloudWatch Logs log group or Kinesis Data Firehose delivery stream to receive access logs. If you specify a Kinesis Data Firehose delivery stream, the stream name must begin with amazon-apigateway-." (HTTP: "CloudWatch Logs log group" only) | Handler test: accepted and kept as cosmetic; the console says nothing is delivered |
| AGW-55 | P1 | **`UpdateStage` refuses patch paths the model's `Stage` has**, so a client that sets them by patch (the CLI; a provider's update, `memory`) fails: `/cacheClusterEnabled`, `/cacheClusterSize`, `/clientCertificateId`, `/documentationVersion`, `/canarySettings/...` and the `copy` promote (`from /canarySettings/deploymentId`, `path /deploymentId`) the model's `PatchOperation.from` documents. Each answers 400 "Invalid patch path". | `stages.go` `patchStage` | run | stricter | model: `PatchOperation.from` "to promote a canary deployment, you copy the canary deployment ID to the affiliated deployment ID by calling a PATCH request on a Stage resource with \"op\":\"copy\", \"from\":\"/canarySettings/deploymentId\" and \"path\":\"/deploymentId\"."; `Stage` members | Handler test per path; the canary decision is S or C and said so |
| AGW-56 | P2 | **`PutIntegrationResponse` for a status with no method response is accepted.** | `methods.go` `putIntegrationResponse` | run | more forgiving | model: `IntegrationResponse` "The status code must map to an existing MethodResponse" | Handler test |
| AGW-57 | P2 | **List and read options that are in the model are ignored.** `GetStages` `deploymentId` (all stages returned); `GetResources`/`GetResource` `embed=methods`; `GetDeployment` `embed=apisummary` (no `apiSummary`). | `stages.go` `getStages`; `resources.go`; `deployments.go` | run | missing | model: `GetStagesRequest.deploymentId` "The stages' deployment identifiers."; `embed` "Currently, the request supports only retrieval of the embedded Method resources this way"; `Deployment.apiSummary` | SDK test per option |
| AGW-58 | P3 | **Two success statuses differ from the model.** REST `DeleteResource` answers 204 (model: 202); HTTP `TagResource` answers 204 (model: 201). The five update operations of AGW-3 are 200 except `UpdateMethodResponse` (201). | `resources.go` `deleteResource`; `tags.go` `tagResource` (shared) | run | wrong code | model: `DeleteResource ... -> 202`; v2 `TagResource POST /v2/tags/{ResourceArn} -> 201` | A status table over the served operations, read from the model |
| AGW-59 | P3 | **The 501s are not in the agreed shape.** 115 operations answer `501 NotImplemented` with no `X-Doze-Not-Built` header, and the message names the family ("doze-aws does not implement API Gateway clientcertificates"), not the operation and why. `testdata/modelsweep.json` freezes them as `legacystub`. | `router.go` `refused`, `family` | run | wrong shape | plan decision D5; protocol step 1 | The sweep's baseline reaches `notbuilt` for every S row |
| AGW-60 | P3 | **The model sweep calls a route miss "handled".** Nine REST operations answer `404 NotFoundException "unknown resource /..."` (`FlushStageCache`, `FlushStageAuthorizersCache`, `GetExport`, `GetSdk`, `UpdateUsage` and the four access-association operations); `NotFoundException` is a modelled code on them, so `modelsweep_test.go` counts them handled and its `unknown` list (8) omits them. | `modelsweep_test.go` classifier | run | wrong shape (test) | model: the error lists of those operations | Classify on the message ("unknown resource") or on the route table, then re-freeze |
| AGW-61 | P3 | **Three unsigned control-plane paths fall to S3.** `/tags/{arn}`, `/domainnameaccessassociations` and `/rejectdomainnameaccessassociations` are not in `apigatewayPathPrefixes`; unsigned, S3 answers `NoSuchBucket: tags` as XML. (Signed requests, which every SDK sends, are routed by scope and are fine.) | `internal/gateway/gateway.go` | run | wrong shape | model: the three paths | A routing test: each path unsigned |
| AGW-62 | P2 | **The console's Invoke tab cannot show an enforce-mode denial.** It calls as root, so the function's resource policy admits it and the function runs (run: the function launched); the 502 AGW-26 describes is what `curl` or an SDK gets. Apigw-16 draws the denial on the Invoke tab. | `internal/console/client_apigw.go` `InvokeAPI`; `peers` principal | run (enforce instance, both paths) | more forgiving | doc (stage-variables): "you must configure the permissions on the Lambda function manually"; model/ledger: `AddPermission` gates an `apigateway.amazonaws.com` caller | Browser test under enforce: send from Invoke, read the answer; decide whether the tab stamps the API Gateway principal |
| AGW-63 | P3 | **CORS "Max age" reads 0 when unset and saving writes 0.** The Settings form shows `0` for an API with no `maxAge` and sends `maxAge: 0`, which AWS stores as a value (no caching) not as absent. | `internal/console/client_apigw_v2.go` `MaxAge int`; `handlers_apigw_v2.go` | run | wrong shape | model: `maxAge` range -1 to 86400 "The number of seconds that the browser should cache preflight request results" | Browser test: save with the field empty, read the API |
| AGW-64 | P3 | **Two layout defects at 1440.** The authorizer cache input in the Settings table is clipped to one digit ("3"); on the keys page the stage and key chips overlap the "Add stage"/"Attach key" selects. (At 900 the Routes table clips its Target and action columns; that is below the shell's own 1,024 minimum and is not counted.) | `templates/apigw.html` `apigw_authorizers`; `apigw_keys.html` | run (screenshots, section 6a) | wrong shape | n/a | Browser test with a bounding-box assertion on both |
| AGW-65 | P1 | **Cognito and JWT authorizers are refused.** REST `COGNITO_USER_POOLS` (authorizer and method) is a 400; HTTP `JWT` (authorizer, and a route's `authorizationType`) is a 501. A stack with either (every CDK `HttpJwtAuthorizer`, `CognitoUserPoolsAuthorizer`) fails to apply at that resource. Decided as a refusal in the ledger; the direction is named here because it is the commonest authorizer in a real app. | `authorizers.go` `checkAuthorizer`; `methods.go`; `v2_authorizers.go`; `v2_routes.go` | run | stricter, decided | model: both types are in the enums; `providerARNs` "arn:aws:cognito-idp:{region}:{account_id}:userpool/{user_pool_id}"; HTTP `jwtConfiguration.issuer`, `audience`. doc (quotas): "Audiences per authorizer 50", "Scopes per route 10" | Decision in section 10: accept and store (C) and let the call through unverified, or keep refusing |
| AGW-66 | P3 | **Three refusals are outside the model's error list for the operation.** HTTP `DeleteIntegration` and `DeleteAuthorizer` answer 409 ConflictException, HTTP `DeleteDeployment` answers 400 BadRequestException; the model lists only `NotFoundException` and `TooManyRequestsException` for each. The REST equivalents are listed (`DeleteDeployment`: BadRequest, Conflict). | `v2_integrations.go`, `v2_authorizers.go`, `v2_control.go` | run | wrong code, not established | model: the operations' `errors` | Recording scenario; a client written to the model sees an error it was not told about |
| AGW-67 | P2 | **Status fields never move (L7).** `RestApi.apiStatus` (UPDATING, AVAILABLE, PENDING, FAILED) and `apiStatusMessage` are never returned; `Stage.cacheClusterStatus` is always `NOT_AVAILABLE` (and the stage drops `cacheClusterEnabled`); HTTP `deploymentStatus` is always `DEPLOYED`, `lastDeploymentStatusMessage` exists only for auto-deploy; an auto-deploy happens inside the call that changed the route. Each is a status model on AWS. | `views.go`; `v2_control.go` `viewV2Deployment`, `autoDeployV2` | read | missing | model: `apiStatusMessage` "When the status message is UPDATING you can still invoke it"; `CacheClusterStatus` CREATE_IN_PROGRESS, AVAILABLE, DELETE_IN_PROGRESS, NOT_AVAILABLE, FLUSH_IN_PROGRESS; `DeploymentStatus` PENDING, FAILED, DEPLOYED; `Stage.lastDeploymentStatusMessage` "Supported only for stages with autoDeploy enabled" | Section 12, L7 decides each; then a state-machine test per field |
| AGW-68 | P2 | **AWS's own pacing rules are unmodelled (L8).** Control-plane request rates and the key-to-plan propagation delay. | n/a | doc | more forgiving, by default | doc, read 2026-10-11: quotas page, "CreateDeployment 1 request every 5 seconds per account", "CreateRestApi ... 1 request every 3 seconds" (Regional/private) and "every 30 seconds" (edge), "DeleteRestApi 1 request every 30 seconds", "UpdateUsagePlan 1 request every 20 seconds", "UpdateAccount 1 request every 20 seconds", "PutRestApi 1 request per second", "CreateResource 5 requests per second", "Total operations 10 requests per second with a burst quota of 40"; usage plans page: "After you add an API key to a usage plan, the update operation might take a few minutes to complete." | One switch, off by default, in memory (`--apigateway-strict`), as SQS-28 |
| AGW-69 | P3 | **`GetAccount` is unset-shaped wrongly.** An account with no role returns `cloudwatchRoleArn: ""` where AWS omits it; `features` is `[]` where AWS lists `UsagePlans` once plans are used; `apiKeyVersion` is the constant "4". | `account.go` | run | wrong shape | model: `Account.features` "When usage plans are enabled, the features list will include an entry of \"UsagePlans\"" | SDK test |
| AGW-70 | P2 | **A backend's own CORS headers are not ignored.** With a `corsConfiguration` set, the data plane stamps its headers and the integration then overwrites them (a Lambda 2.0 or 1.0 response's `headers`, `Set`) or doubles them (an `HTTP_PROXY` response's headers, `Add`): a function that answers `Access-Control-Allow-Origin: *` wins over the API's list. | `v2_execute.go` `v2CORSHeaders` then `invokeHTTP`, `httpevent.WriteResponse`, `writeProxyResponse` | read | wrong data | doc, read 2026-10-11 (http-api-cors): "If you configure CORS for an API, API Gateway ignores CORS headers returned from your backend integration." and "For a CORS request, API Gateway adds the configured CORS headers to the response from an integration." | Execute test: a function returning its own `Access-Control-Allow-Origin`, with and without a configuration |


Owned by the foundation, listed so they are not lost. *Re-checked on rendered
pages, 2026-10-11: A7, A9 and A11 are closed for this page (marked below); A2's
wording is closed, its 501 state is not; A13 is open.*

- **A2.** The generic "AWS refuses this too" line is printed under hand-written
  refusals here ("Name is required", the API key length, the CORS credentials
  rule, a duplicate authorizer). Every 501 in section 2, when an app's call is
  on the wire, reads "a bug in the emulator". *The generic line is replaced by
  "doze-aws refused the request: it breaks a rule of the service. The input is
  what to change." (verified); the 501 state is open (AGW-59).*
- **A7.** `copy.go` says API Gateway has "no create form on purpose". It has one
  (`/apigw/create`), the empty state has no button for it, and the `Unsel` line
  cannot render because `apigwList` opens the first API. *Closed: the empty state
  has the button and the list pane a New link (verified on a rendered page).*
- **A9.** The fidelity dialog on an HTTP API's page shows the REST rows.
  *Closed: the dialog lists both sections, 24 functional and 16 stub (verified).*
- **A11.** The HTTP create form's CLI preview leaves out the CORS configuration
  the form sends. *Closed: the preview adds `--cors-configuration` when the box
  is ticked (template read).*
- **A13.** The repoint confirm (AGW-21).

Owned by another service: the function's resource policy refusing
`apigateway.amazonaws.com` is Lambda's behaviour and is correct; AGW-26 is the
console not preparing for it.

**IAM scope, decided (D6).** API Gateway is evaluated by IAM, in two parts that land in this stage.
(1) The control plane: every REST and HTTP management call is evaluated and
recorded as `apigateway:<VERB>` on the resource ARN for its path, under soft
and enforce, and appears in the access log and in a generated policy.
(2) `execute-api:Invoke`: a method or route whose authorisation is `AWS_IAM`
is evaluated for the caller before the integration runs, and refused with
AWS's 403 when denied; today the setting is stored and nothing is checked.
Acceptance: an enforce test per part (allowed and denied); a soft-mode run of
a deploy records the `apigateway:*` actions it used; the action and ARN forms
are `doc` claims on the recording queue. Adds about a day to this stage.

## 5. What to check

Beyond the common criteria. REST lines are checked through the Go SDK v2 and
boto3; HTTP lines the same against `/v2`.

**REST APIs**
- [ ] `CreateRestApi`: `name` required; an empty or whitespace name; duplicates
      allowed (two APIs may share a name) or not: not established.
- [ ] Every `UpdateRestApi` patch path AWS documents, with `replace`, `add` and
      `remove`; an unknown path refused (AGW-11).
- [ ] `policy`: a document that does not parse is refused; a valid one is
      evaluated on the data plane, or the setting is tiered C and says so (AGW-7).
- [ ] `disableExecuteApiEndpoint` stops the default endpoint answering.
- [ ] `DeleteRestApi` on a missing id; deleting an API a usage plan names.

**Resources**
- [ ] Path part rule, and the same rule on rename (AGW-8).
- [ ] A duplicate sibling on create and on rename; `{proxy+}` anywhere but last:
      not established. *Corrected 2026-10-11: for an HTTP API route it is `doc`
      ("The greedy path variable must be at the end of the resource path", AGW-46);
      for a REST resource it stays `memory`.*
- [ ] Two sibling path variables with different names (`{id}` and `{name}`):
      not established.
- [ ] Moving a resource with `/parentId`: missing parent, a cycle, a conflict at
      the destination.
- [ ] Deleting the root is refused; deleting a resource takes its subtree and
      their methods.

**Methods and integrations**
- [ ] The eight verbs; a re-put keeps the integration and responses (there is a
      test; keep it).
- [ ] `UpdateMethod` and `UpdateIntegration` for every patch path Terraform and
      CloudFormation send (AGW-3). The list is collected from their providers'
      source, not from memory.
- [ ] `CUSTOM` needs an authorizer that exists; deleting an authorizer a method
      names: refused or allowed is not established.
- [ ] `PutIntegration` type enum; `uri` shape per type; a Lambda URI that does
      not parse; a function that does not exist (accepted today, 502 at call time:
      whether AWS checks is not established).
- [ ] Every integration setting in section 2 has a tier with no "decide" left.
- [ ] Status codes: the `^[1-5]\d\d$` pattern on all eight response operations.
- [ ] Deleting a missing method, integration or response (AGW-10).

**Deployments and stages**
- [ ] `CreateDeployment`: refused with no methods and with an unwired method.
      Both messages are a claim about AWS (section 8).
- [ ] Redeploying to an existing stage keeps everything on the stage (AGW-1),
      and `variables` in the request merges or replaces as AWS does: not established.
- [ ] `CreateStage` and `UpdateStage` refuse a deployment that does not exist (AGW-9).
- [ ] Stage name rule (letters, digits, hyphen, underscore, at most 128): the
      service checks nothing today, the console's `pattern` does. *Source:
      `model` (AGW-33), not memory.*
- [ ] Every `UpdateStage` patch path: variables add and remove, access-log
      settings, each method-setting path on `*/*` and on one method, an unknown
      path refused.
- [ ] `DeleteDeployment` while a stage points at it is refused (there is a
      test); a missing one is 404. *Source: `model` (REST: "Deleting a deployment
      will only succeed if there are no Stage resources associated with it"). The
      HTTP model lists neither condition (AGW-66).*
- [ ] Every stage member is stored and returned, or refused (AGW-15).

**The data plane, REST**
- [ ] Precedence: literal over `{param}` over `{proxy+}`, deepest proxy wins
      (held by `execute_test.go`); `ANY` loses to an exact verb.
- [ ] No resource and no verb both answer `403 Missing Authentication Token`;
      an unknown stage answers what AWS answers (today 404, "Invalid stage
      identifier specified": not established); an unknown API id answers 403.
- [ ] The proxy event field by field against a recorded one, including
      `multiValueHeaders`, `requestContext.identity`, `isBase64Encoded`.
- [ ] The proxy response: `statusCode` required, `multiValueHeaders`, base64
      bodies, and the 502 for anything else.
- [ ] HTTP integrations: method (AGW-2), query and header pass-through,
      `{param}` and stage-variable expansion (AGW-19), an unreachable backend,
      the timeout (AGW-6).
- [ ] MOCK: which integration response is chosen when several exist.
- [ ] The gates in order: authorizer, then API key.

**Authorizers, REST**
- [ ] TOKEN: default header, `identityValidationExpression`, the 401 with no invoke.
- [ ] REQUEST: every source kind; with TTL 0 a missing source still invokes;
      with TTL above 0 it is a 401.
- [ ] The policy: explicit Deny wins, globs in the resource, `context` and
      `principalId` on the event, `usageIdentifierKey` with `apiKeySource: AUTHORIZER`.
- [ ] The cache: keyed per authorizer and identity, bounded, expired first,
      dropped on update and delete, and by `FlushStageAuthorizersCache` once it
      exists (AGW-13).
- [ ] A duplicate name; TTL outside 0 to 3600; a URI that names no function.

**API keys and usage plans**
- [ ] Value length 20 to 128 with a message that fits both ends (AGW-17); a
      duplicate value; a minted value's shape.
- [ ] A key created without `enabled` is disabled.
- [ ] The gate: missing, unknown, disabled, in no plan, in a plan for another
      stage, in a plan for this stage. All but the last are `403 Forbidden`.
- [ ] Plan stages name a REST API and a stage that exist (AGW-18).
- [ ] Deleting a key removes it from every plan; deleting a plan with keys
      attached: refused or allowed is not established.
- [ ] `throttle` and `quota` round-trip through create and every patch path, and
      the console and ledger call them cosmetic.

**HTTP APIs**
- [ ] `CreateApi`: `protocolType` required and `HTTP`; quick-create with a
      Lambda ARN, with a URL, and with a `routeKey`.
- [ ] Route keys: the grammar, case folding of the method, uniqueness, `$default`.
- [ ] Precedence, as the ledger lists it (held by `v2_execute_test.go`).
- [ ] An integration a route targets cannot be deleted; an authorizer a route
      names cannot be deleted; a deployment a stage points at cannot be deleted.
- [ ] Stages: `$default` at the root, a named stage under its name; a stage with
      no deployment answers 404; auto-deploy makes a deployment on every route
      and integration change and keeps at most ten unused ones.
- [ ] Payload 2.0 and 1.0 events against recorded ones; the bare-value response
      rule for 2.0; the 502 for a malformed 1.0 response.
- [ ] CORS: a preflight from an allowed origin, from another origin, with a
      method not allowed; headers on ordinary responses; credentials with `*` refused.
- [ ] Authorizers: simple and policy responses, payload 1.0 and 2.0, every
      identity source kind, the cache and `ResetAuthorizersCache`.
- [ ] A missing function and a missing authorizer function both answer a bare
      500. Whether AWS says more is not established.

**Both**
- [ ] Every operation in both models answers 2xx, a modelled error, or 501 (AGW-13).
- [ ] Pagination on every list that has the parameters (AGW-12), default page 25,
      maximum 500 for REST; `position` and `nextToken` stale and foreign: the model
      is silent (section 12, L6).
- [ ] Tags on an API through both ARN shapes; on a stage (AGW-16); the key,
      value and prefix rules (AGW-34, `model`); 50 tags **per stage** (`doc`).
      *Corrected 2026-10-11: was "the 50-tag limit and key and value rules: not
      established".*
- [ ] Access and execution logs: one access line per request in the stage's
      format; the execution narrative at INFO and ERROR; `dataTrace`.
- [ ] Metrics: `Count`, `Latency`, `4XXError` and `5XXError` by `ApiName` and
      `Stage` for REST; `4xx` and `5xx` by `ApiId` and `Stage` for HTTP.
- [ ] An API with resources, methods, integrations, responses, authorizers,
      stages, deployments, keys and plans survives a restart, and answers the
      same request the same way afterwards.
- [ ] Everything above under a non-default account and region: integration
      URIs, authorizer URIs, the method ARN a policy is matched against, invoke URLs.

**Added by the pre-stage review, 2026-10-11** (each against the model text or a
named page; the finding id is the test)
- [ ] The stage variable, stage name, tag and API key rules, on both planes
      (AGW-32, 33, 34, 51); the HTTP lengths once the generator reads the prose
      (AGW-44).
- [ ] Every member in the model's create bodies is stored and read back, or
      refused by name (AGW-35, AGW-15).
- [ ] The enumerations the model gives in prose (`authorizationType`,
      `passthroughBehavior`, `requestParameters` keys), `PutIntegration`'s
      required `httpMethod`, the Lambda-is-POST rule and the 50 ms floor (AGW-36, 37).
- [ ] A function's `context` is stringified and refuses an object (AGW-39); the
      proxy event's `requestContext` carries the page's key set (AGW-40).
- [ ] An unreachable backend is 504; a body over 10 MiB is 413 (AGW-41, 42).
- [ ] HTTP API parameter mapping, every key and value kind, and a reserved
      header refused at create (AGW-43).
- [ ] `payloadFormatVersion` required; `HTTP_PROXY` 1.0 only (AGW-45).
- [ ] Managed quick-create resources and the `autoDeploy` rule (AGW-47, 48).
- [ ] The limits table (AGW-49) and one key, one plan per stage (AGW-50).
- [ ] Success statuses from the model for every served operation (AGW-58).
- [ ] Every S row answers the agreed 501 shape (AGW-59); the sweep classifies a
      route miss as one (AGW-60).
- [ ] Each status field of AGW-67 has a decision and a test; the strict switch of
      AGW-68 exists and is off.

## 6. Console

The design's "API Gateway" page: 30 boards. Templates: `templates/apigw.html`,
`apigw_v2.html`, `apigw_keys.html`, the `apigw_create` block in `create.html`,
shared pieces in `panes.html` and `layout.html`. Handlers: `handlers_apigw.go`,
`handlers_apigw_v2.go`, `handlers_apigw_keys.go`, `handlers_apigw_auth.go`;
clients `client_apigw*.go`; routes in `routes_compute.go`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Apigw-Home | No APIs yet | `apigw_home`, `empty_detail`; `copy.go` | exists, with the "New API" button. *Corrected 2026-10-11: was "the button is new (foundation A7)"; it exists (`copy.go` CTA).* |
| Apigw-02-Create | Create a REST API | `apigw_create` | form and CLI preview exist; "what you get, and what comes next" and the teaching lines are **new** |
| Apigw-03-Create-Http | Create an HTTP API | `apigw_create` | exists; the corrected `$default` sentence replaces AGW-23's |
| Apigw-04-Create-Refused | Create refused | `fail_inline` | exists |
| Apigw-05-Rest-Routes | Routes | `apigw_api`, `apigw_routes` | exists; the three notes under the table replace AGW-21's sentence |
| Apigw-06-Route-Dialogs | Add resource, add method, rename | the three dialogs in `apigw_api` | exist; the wider input rule is AGW-20 |
| Apigw-07-Method | A method and its integration | `apigw_method` | exists |
| Apigw-08-Integration | The five integration types | `apigw_method` | form exists; the type table is **new** |
| Apigw-09-Stages | Stages and deployments | `apigw_stages` | exists; "Stage prod" is **new** |
| Apigw-10-Stages-Refused | Deploy refused | `fail_inline`, `apigw_stages` | exists; "Which method" is **new** |
| Apigw-11-Rollback | Repointing is not a rollback | the `update-stage` form's confirm | confirm exists; its sentence changes (AGW-21) |
| Apigw-12-Settings | Settings and authorizers | `apigw_api` settings branch, `apigw_authorizers`, `apigw_authorizer_detail` | exists; "Used by" is **new** |
| Apigw-13-Authorizer-Refused | Add authorizer refused | `fail_inline` | exists |
| Apigw-14-Invoke | Invoke | `apigw_invoke`, `apigw_result` | exists |
| Apigw-15-Invoke-Refused | Answers that are not a 200 | `apigw_result` | answers exist; the line under each is **new** |
| Apigw-16-Denied | Invoke under enforce | `apigw_result` | the 502 exists; the Denied reading is **new** |
| Apigw-17-Tags | Tags | `tags_panel`, `tag_editor` | exists (foundation A5) |
| Apigw-18-Http-Routes | HTTP routes | `apigw_http`, `apigw_http_routes` | exists |
| Apigw-19-Http-Route-Refused | Add route refused | `fail_inline` | exists |
| Apigw-20-Http-No-Stage | No stage yet | `apigw_http_routes` (the warning) | exists; the two cards under it are **new** |
| Apigw-21-Http-Stages | HTTP stages | `apigw_http_stages` | exists; "Stage beta" is **new** |
| Apigw-22-Http-Settings | Settings and CORS | `apigw_http_settings` | exists; teaching lines are **new** |
| Apigw-23-Http-Settings-Refused | Settings refused | `fail_inline` | exists |
| Apigw-24-Http-Invoke | HTTP invoke | `apigw_http_invoke`, `apigw_result` | exists; "when it is not a 200" is **new** |
| Apigw-25-Keys | Keys and plans | `apigw_keys`, `apigw_keys_body` | exists; "In plans" is **new** |
| Apigw-26-Keys-Empty | None yet | `apigw_keys_body` | exists; "the order that works" is **new** |
| Apigw-27-Keys-Refused | Create key refused | `fail_inline` | exists |
| Apigw-28-Confirms-Rest | REST confirms | ten `hx-confirm`s in `apigw.html` | exist |
| Apigw-29-Confirms-Other | HTTP and key confirms | seven `hx-confirm`s in `apigw_v2.html`, `apigw_keys.html` | exist |
| Apigw-30-Not-Here | Fidelity, and the 501 families | `info_button`, `info_panel`; `info.go` | dialog exists and lists both sections (foundation A9 closed; *corrected 2026-10-11*); the message table and the "Not implemented" state are **new** (foundation A2) |

**New items, and what each is built from**

| Item | Built from |
|---|---|
| "New API" on the empty state | `copy.go` `CTA` and `CTAPath`, and the existing `/apigw/create` route |
| What you get, and what comes next (both create boards) | Static. Each sentence is a line in section 5: a new REST API is a root resource only; Deploy's two refusals; an HTTP API with no stage answers 404 |
| The five integration types | The tier table in section 2, read from the ledger through foundation B4. Its text changes with the decisions in AGW-4 and AGW-5 |
| Stage panel (REST and HTTP) | `GetStages`, which the Stages tab already calls: variables, `tracingEnabled`, `cacheClusterEnabled`, `methodSettings["*/*"]`; for HTTP `stageVariables` and route settings. Read only |
| Which method (Deploy refused) | `GetResources`: a method whose `methodIntegration` has no type |
| Used by (an authorizer) | `GetResources`: each method's `authorizerId` |
| The line under a non-200 answer | The status and the `message` in the body, nothing else. A fixed table of the data plane's own messages (`execute.go`, `authorize.go`, `apikeycheck.go`, `v2_execute.go`); an unknown message gets no line |
| The Denied reading of a 502 | The body's quoted Lambda refusal. This is a string match on another service's message and is the weakest item here; it is kept only if AGW-26 leaves a case where it can appear |
| In plans (a key) | `GetUsagePlans` and `GetUsagePlanKeys`, which the page already calls |
| The 501 message table | The refusal strings in `router.go` (`family`, `refused`) and the v2 handlers |
| HTTP rows in the fidelity dialog | `docs.Rows("apigatewayv2")`, which exists |

**Where the teaching lines come from**

| Line | Source |
|---|---|
| Authorizer cache: 0 to 3600, default 300, enforced | `apigateway/authorizers.go` `checkAuthorizer`, `DefaultAuthorizerTTL`; v2 from `v2_validate.go` (model) |
| CORS max age: -1 to 86400 | `v2_validate.go`, `corsConfiguration.maxAge` (model) |
| API key value: 20 to 128; a blank one is minted as 40 characters | `apigateway/apikeys.go` |
| Path part: letters, digits, `. _ - :`, `{name}`, `{proxy+}` | `apigateway/resources.go`, `pathPart` (hand-derived; `doc` not yet named) |
| Caching and throttling stored, not enforced; throttle and quota not metered | The ledger; section 2's stage table |
| Stage variables reach the event and an authorizer | `execute_proxy.go`, `authorize.go` `identityValues`, `v2_authorize.go` |
| "An integration URI is not rewritten with them" | True today, and AGW-19 exists to make it false. The line is removed with the fix |
| Route and resource precedence | The ledger's Routing sections; `execute_test.go`, `v2_execute_test.go` |
| Payload 2.0 and 1.0 | The v2 ledger's integrations table |
| Auto-deploy; the ten kept deployments | `v2_control.go`, `autoDeployV2`, `keptAutoDeployments` |
| "Edits are live; Deploy makes the record" | The ledger's "Deployments serve the live API" |
| `AWS_IAM` stored, not checked | The v2 ledger. For REST it is from the code alone and needs the ledger row (section 2) |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Apigw-30 says a family doze-aws does not model "is answered 501 NotImplemented
  by name, rather than a bare 404". For 15 operations (17 REST with the five
  updates of AGW-3) it is a 404 "unknown resource" or a 405 (AGW-13, run
  2026-10-11). The board is wrong until AGW-13 is fixed. Its "REST: usage" row
  is true of `GetUsage` only. And none of the 115 refusals carries
  `X-Doze-Not-Built` (AGW-59).
- Apigw-04, Apigw-10, Apigw-13, Apigw-19, Apigw-23 and Apigw-27 print the generic
  "AWS refuses this too" line under refusals that are hand-written here.
  *Corrected 2026-10-11: foundation A2 has decided the text and the real pages
  print "doze-aws refused the request: it breaks a rule of the service. The input
  is what to change."; the six boards are corrected to match. Only the range
  messages (`maxAge`, the TTL) are the model's; each other refusal's text is
  `memory` until recorded (section 8).*
- Apigw-05 and Apigw-15 say an unmatched path answers `403 Missing
  Authentication Token` as AWS does. *Corrected 2026-10-11: the status and the
  case are `doc` (supported-gateway-response-types: `MISSING_AUTHENTICATION_TOKEN`
  403, "including the cases when the client attempts to invoke an unsupported API
  method or resource"); the body text "Missing Authentication Token" is `memory`.*
- Apigw-06 says "nothing is checked on rename". True today (AGW-8); the note is
  deleted with the fix, and the dialog gains the add dialog's refusals.
- Apigw-08 says an `AWS` integration "saves, and a call answers 500". Read, not
  run, and it changes with AGW-5.
- Apigw-09 says redeploying points an existing stage at the new deployment. It
  also wipes the stage (AGW-1); the sentence is true only after the fix.
- Apigw-09's stage panel shows throttling of "10000 rps · burst 5000". Those are
  the defaults the service fills in once any method setting is patched; a stage
  nobody patched has none to show.
- Apigw-11 says AWS brings the old API back when a stage is repointed.
  *Corrected 2026-10-11: `model` and `doc` back it (Deployment "An immutable
  representation of a RestApi"; how-to-deploy-api "Every time you update an API,
  you must redeploy").*
- Apigw-12 and Apigw-13: the REQUEST-authorizer rule for a cache of 0 is read
  from `authorize.go`, not run. *Added 2026-10-11: the model's prose reads as if
  the identity sources are validated whenever they are given ("Only when this is
  true does the authorizer invoke the authorizer Lambda function, otherwise, it
  returns a 401 Unauthorized"; "When the authorization caching is not enabled,
  this property is optional"), which is not the same as "a cache of 0 invokes
  with a source missing". The code's rule has no source; keep `memory` and record
  `request_authorizer_no_cache`.*
- Apigw-16 says wiring an integration "does not add that permission". True
  today (AGW-26); if the fix adds it, the board becomes the case where a
  template or the CLI wired the integration. *Corrected 2026-10-11: the board
  also puts the denial on the Invoke tab; it is not reachable there (AGW-62).*
- Apigw-19 says a deleted function answers a bare 500. Run for a function that
  never existed, not for one deleted afterwards.
- Apigw-25 says only REST methods check a key. Read (`checkAPIKey` has one
  caller). *Corrected 2026-10-11: `model` backs it (v2 `Route.apiKeyRequired`
  "Supported only for WebSocket APIs").*
- Apigw-28 and Apigw-29 use the pilot's confirm shape. The sentences are the
  templates' own, except the repoint, which keeps the false one on that board
  and corrects it on Apigw-11.

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each of the 30 boards (`console-design/project/Apigw-*.dc.html`,
1440 by 900, light; five also in dark) and the real page **in the same state**
(`e2e/tests/zz-apigw-fidelity.spec.ts`, a throwaway spec over a fresh instance:
the API, resources, methods, authorizers, deployments, stages, keys and plans
were arranged over the wire, every dialog and confirm opened and shot), then
reading both PNGs. The real page was also rendered in dark (create, routes,
stages), at 1024 and at 900 wide. Nothing here is from the board's words.

Two things apply to every row and are not repeated. **The shell** (the labelled
rail, the top bar with region and theme, the list pane's filter box, the
`j`/`k` footer) is the foundation's and differs from the boards' marks-only rail
by decision, as in the SQS table's Queue row; row actions on the routes table
are the shell's icon buttons (`title` carries the words) where the board draws
text buttons. **The icons** stay: the real pages draw the AWS API Gateway and
Lambda icons in the rail, the list header, the resource header and the Lambda
targets; the boards' `AG` and `λ` marks are placeholders for them (L12).

| Board | Verdict | What differs |
|---|---|---|
| Apigw-Home | **matches** (the board's note is stale) | The list pane's "No APIs yet / New API →" and the empty state's one button exist (`copy.go` CTA, `/apigw/create`; foundation A7 is closed for this page). The board's `new` tag and the sentence "copy.go gives API Gateway no CTA" are out of date. Copy differs slightly and is the real page's (it names sam, cdk, Serverless and the CLI). |
| Apigw-02 Create | differs | (1) The right-hand "What you get, and what comes next" panel, four steps with their dots, is absent: the real page is one column. (2) Kind is a segmented control with a sentence in the board ("REST is apigateway (v1); HTTP is apigatewayv2. They are two APIs that do not see each other."); here two radios with a parenthesis. (3) No sentence under "API name" ("Required. doze-aws does not check for another API with the same name; the id is what tells them apart.") or "Description" ("Shown under the name on the API's page."): the teaching pattern's lines are not on the form. (4) The subtitle says "answering at `$default` as soon as a route exists", which is false (AGW-23); the board's "once a stage exists" is right. (5) The CLI preview is collapsed behind a `cli` chip (the kit's behaviour) where the board shows it open: deliberate. |
| Apigw-03 Create HTTP | differs | As 02, plus: the board's Kind sentence for HTTP, the two-line explanation under the CORS box ("It sets origins, methods and headers to `*`; narrow them later on the Settings tab") and the right panel ("Create the `$default` stage / Without a stage a request gets 404 Not Found. With auto-deploy on, every route change is live at once"). The board's note that "the CORS box is not in the command" is stale: the real preview includes `--cors-configuration` when the box is ticked (foundation A11 is closed); corrected in the board. |
| Apigw-04 Create refused | differs | The board puts the callout **above** the form and a second callout ("the other refusal this form can come back with", the HTTP wording `name is required`) and a closing sentence beside it; the real page puts one callout **below** the form's buttons with the foundation's sentence ("doze-aws refused the request: it breaks a rule of the service. The input is what to change."). The board's sentence "doze-aws refuses this because AWS does — the rule comes from AWS's own service model" is not true of "Name is required" (a hand-written store message): corrected in the board (and in -10, -13, -19, -23, -27). The whitespace name is trimmed and the field comes back empty, as the board says. |
| Apigw-05 Routes | differs | Same table, same columns, same unwired chip, the function name in the target is a link to `/lambda/<name>` but carries no Lambda icon (the board draws one). Missing: the intro line ("Path, method, and what it forwards to. A verb opens its integration and responses below the table."), a `REST` badge beside the name, and the three cards under the table ("Edits are live; Deploy makes the record", "Which resource wins a request", "PUT /orders/{id} is unwired"). In their place the real page prints "Edits change the DEFINITION only — callers see them after the next deploy", which is false here (AGW-21). Row actions are icon buttons (see above). |
| Apigw-06 Dialogs | differs | The dialogs exist as modals (add resource, add method, rename), not as the board's three small panels, which is fine. Missing: the sentence under Path part ("`{name}` makes it a path parameter; `{proxy+}` takes the whole rest of the path. Letters, digits and `. _ - :` otherwise."), the authorization sentence under the select, the "require an API key" explanation, the "Authorizers are added on the Settings tab" note (shown only when there are none), and the board's "Nothing is checked on rename" card (true today; goes with AGW-8). The refusal is drawn at the **top** of the dialog in the board and below the buttons in the real one. The add and rename inputs' `pattern` blocks `{proxy+}` (run: `checkValidity()` is false; AGW-20). |
| Apigw-07 Method | differs | The real panel sits below the table and is two columns like the board's. Missing: the selected-row highlight, the one-sentence explanation under each integration type ("The whole request goes to the function as a proxy event, and its `{statusCode, headers, body}` is the answer ... a 502 that names what it returned"), the caption "stored and shown; nothing reads them when a request is served" under the method responses, the caption "Only a MOCK integration answers from these, with the lowest status mapped" under integration responses. The type/function form is stacked and **does not show the current target** (the field shows the `my-function` placeholder for an integration that has one). (The method header names the authorizer by id on both the board and the page; AGW-29 stands as a copy decision.) |
| Apigw-08 Integration | differs | The board's right-hand "The five types, and what each does here" table (type, tier, what it does) and the "function is not looked up when you save" card are absent. The type options still say "AWS — Lambda, templates apply" and "HTTP — forward, templates apply" (AGW-22); the board's are true ("templates are not applied", "templates are not emulated"). The Map refusal appears beside the Map form (cramped, clipped) where the board places it under the panel. The message text is identical ("Invalid Integration identifier specified"). |
| Apigw-09 Stages | differs | Layout is the board's (deploy and stage-from-deployment side by side, stages table, deployments table). Missing: the "Stage prod `new`" panel (variables, tracing, cache cluster, throttling, and the two sentences), the corrected copy ("A deployment is a record: ... a stage on an old one still serves the live routes. What that means for a rollback →") where the real page says "Deployments are immutable — pointing a fresh stage at an old one is the rollback story" (false here, AGW-21). The stage table's invoke URL is truncated at 1440. |
| Apigw-10 Deploy refused | differs | The callout and its message are the same ("No integration defined for method"); the board adds the "Which method `new`" card (`PUT /orders/{id}`) and a right-hand list of three other refusals this tab can return, which are annotations of the design, not page elements, and are **not** to be built. The refusal is below the Deploy form in the real page, above it in the board. |
| Apigw-11 Rollback | **cannot match yet**: waits on AGW-21 and the AGW-31 decision | A board of three parts: the changed picker, the confirm, the "what it changes / does not" explainer. The real confirm reads "Repoint stage “dev” to this deployment? Callers get it straight away.", the sentence the board marks as false. The board's replacement ("The stage's deployment changes from X to Y. The routes callers reach stay the live ones.") needs the confirm kit to take a dynamic old and new id (a template-only change) and the explainer needs a home (the stages tab card). If AGW-31 is decided as "snapshot", this board is redrawn. |
| Apigw-12 Settings | differs | The board's top strip (name, description, Save inline, one-line note) is a stacked panel here. The authorizer table is the same; the detail (`GetAuthorizer`) opens in place but without the "Used by `new`" block, and the add form lacks the teaching lines (type explanation, "Without it: 401 Unauthorized, function not called. Stored as `method.request.header.<name>`", "Kept per authorizer and identity value; 0 asks every time. An authorizer runs before the API key check.", the `0 – 3 600 · default 300 · enforced` line from the validation table). The cache input in the table is clipped to one digit at 1440 (AGW-64). |
| Apigw-13 Authorizer refused | differs | Same message ("Authorizer name must be unique. Authorizer harbour-token-gate already exists in this RestApi."). The callout is below the form (above in the board); "other refusals" and "a bad day the form cannot catch" are annotations, not built. One board sentence is unbacked: "A REQUEST authorizer with a cache of 0 hands the function the request even when an identity source is missing" is what the code does and is not what the model's prose says (section 12, L2). |
| Apigw-14 Invoke | differs | The form is the same (stage, method, path, body, Send). Missing: the right-hand cards ("With no stage yet", "Where to look when it is not a 200" with two links), the sentence "It sends no headers of its own but Content-Type, so a method that needs an API key or an authorizer's token answers 403 or 401 here" (the real note stops after "not a simulation of one"; AGW-27), the footer "The request is a real one: it is in the stage's access and execution logs ...". Failing statuses are red chips (AGW-30). The body editor is the shell's CodeMirror and much taller than the board's textarea. |
| Apigw-15 Invoke, not a 200 | differs | The nine answers are what the data plane returns: 403 Missing Authentication Token, 403 Forbidden, 401 Unauthorized and a 502 from a function that cannot start were reproduced in this run, message for message; the unwired-method 500, the explicit-deny 403, the non-proxy-`AWS` 500 and the deleted-stage 404 are read from `execute.go`, `authorize.go` and `apikeycheck.go`. The real page shows the bare answer with no line under it. The per-answer sentences are the board's `new` item and are not built. Header `X-Amzn-Errortype` carries the HTTP status text on every one ("Forbidden", "Bad Gateway"), as the board says; whether AWS's value is the same is `memory` (section 8). |
| Apigw-16 Denied | **design is wrong** | The board draws the 502 denial as the Invoke tab's answer under enforce. It is not: the Invoke tab calls as root and the function is launched (run, enforce instance); the denial is what an outside caller gets (`curl` run: 502 "User: apigateway.amazonaws.com is not authorized to perform: lambda:InvokeFunction ..."). The board's "Denied" callout text is the right wording for the Wire page and the access log. AGW-62. A sentence is corrected in the board. |
| Apigw-17 Tags | differs | The rows and Add tag and Save changes exist; Save is disabled until a change (the board shows an "Unsaved changes / Cancel" strip). The two sentences are absent ("A draft you submit ...", "The tags belong to the API itself, at `arn:aws:apigateway:us-east-1::/restapis/<id>`. An HTTP API's are at `...::/apis/<id>`, and its Tags tab is this one."). The draft kit is foundation A5. |
| Apigw-18 HTTP routes | differs | Header, endpoint chip, CORS chip, routes table and add-route form exist; the Lambda icon is in the target. Missing: the "what it refuses →" link beside Add route, the one-row add form (it is a two-column grid here), and the sentence's second half. In the state shot here (an API with no stage) the real page shows the amber "No stage yet" callout above the table, which the board keeps for -20. |
| Apigw-19 Route refused | differs | Same message ("Route with key GET /hooks/health already exists for this API"). Callout below the form (above in the board); the two "other refusals" and "the function can go away afterwards" are annotations. The sentence "a malformed return is a 502" under payload 1.0 (also on -18 and -24) is backed for the REST plane (`doc`) and not for the HTTP plane (section 8). |
| Apigw-20 No stage | differs | The amber callout and the Routes table match. The two cards ("What a caller sees": `404 {"message":"Not Found"}`, and "What the stage will do") and the "Create the stage →" link are absent; the real page keeps the Add route form below the table. |
| Apigw-21 HTTP stages | differs | Create stage and Deploy panels and the stages table match (auto-deploy badge, deployment id, access log). Missing: the "Stage beta `new`" panel, the pruning sentence,. The board's right-hand "With no stage yet" mini-table is a state mixed into a page that has stages: noise, not to be built. |
| Apigw-22 HTTP settings | differs | The board is two columns (settings and CORS left, authorizers right); the real page is one column. Missing: the teaching lines under every CORS field (including `-1 – 86 400 · enforced`) and the authorizer form's. Max age reads **0** for an API with none and saves 0 (AGW-63). |
| Apigw-23 Settings refused | differs | Same CORS message; below the form here, above in the board; the four "other refusals" are annotations. The range messages (`maxAge`, TTL) are the model's and reproduce (`Member must have value less than or equal to 86400`). |
| Apigw-24 HTTP invoke | differs | Form and result match. Missing: the note under the result about payload 2.0, and the "When it is not a 200 `new`" card (404/401/403/500/502 lines). A failing function answers `500 {"message":"Internal Server Error"}`, which is what the board's table says. |
| Apigw-25 Keys | differs | Two panels, keys and plans, with create forms. Missing: the "In plans" column, the "What decides a 403 Forbidden" card, the sentence "Only REST API methods check a key; an HTTP API's routes do not" (true: model, `apiKeyRequired` "Supported only for WebSocket APIs"). Visible defects: the list pane **drops the HTTP APIs** (AGW-24) and the stage/key chips overlap the selects (AGW-64). |
| Apigw-26 Keys empty | differs | Both empty states exist with the same words; "The order that works" card is absent. |
| Apigw-27 Keys refused | differs | Same message ("API Key value should be at least 20 characters"); callout below the form here, above in the board; "other refusals" is an annotation. The board's "The same message answers a value over 128 characters" is true of the code and says "at least 20" for a value that is too long (AGW-17). |
| Apigw-28 Confirms, REST | differs | All ten confirms exist and every sentence is the board's, except the repoint (AGW-21). The shell's styled dialog splits the sentence into a title and a body at the question mark; the board shows one sentence, which is fine. The buttons differ: "Cancel" and a first-word verb (`Delete`, `Remove`, `Repoint`) where the board has "Keep it" and a named verb ("Delete API", "Delete stage"). The kit supports both (`data-confirm-cancel`, `data-confirm-verb`); no API Gateway confirm sets them. The captions under each card are board annotations. |
| Apigw-29 Confirms, HTTP and keys | differs | All seven exist with the board's sentences; same button difference. |
| Apigw-30 Not here | differs | The dialog lists REST **and** HTTP sections (24 functional, 16 stub) where the board shows the REST rows (14 and 9): foundation A9 is closed, and the board's note that the dialog reads the REST section from every page is stale (corrected). The right-hand "What a call to one of them gets" message table and the "Not implemented" wire state are not built (they wait on AGW-59: the 501s must be in the agreed shape first). The notes column wraps to tall rows in the narrow dialog. |

Verdicts: **1 matches**, **27 differ**, **1 cannot match yet**, **1 design is wrong**.
No row is "differs deliberately" except the two shell items above.

**Dark and narrow.** Create, routes and stages rendered in dark: no unreadable
text, the chips and the unwired badge keep their contrast; the boards'
`.app.dark` render of the same pages has the same arrangement. At 1024 the
routes table fits (`scrollWidth` equals `clientWidth`). At 900 the shell prints
its own "built for 1024 px or more" notice and the table clips its Target and
action columns: below the shell's stated minimum, not counted.

**Where the design is wrong** (corrected in the working copy on 2026-10-11;
the published canvas still carries the old text until it is republished)
- Apigw-04, -10, -13, -19, -23, -27: "doze-aws refuses this because AWS does —
  the rule comes from AWS's own service model, so the same call would have
  failed on deploy." Each refusal's text is hand-written or `memory` here; the
  model backs only the range messages. Replaced by the foundation's sentence.
- Apigw-16: the denial is not the Invoke tab's answer (AGW-62). A sentence in
  the board says where it is seen.
- Apigw-Home: the `new` tag and "copy.go gives API Gateway no CTA" (A7 closed).
- Apigw-03: "The CORS box is not in [the command]" (A11 closed).
- Apigw-30: "The ledger dialog reads the REST section from every API Gateway
  page" (A9 closed).
- Apigw-21: the "With no stage yet" mini-table on a stage-full page is a state
  mix; not corrected, not to be built.
- Apigw-25: "Value · 20 to 128 characters" is the length and omits "letters and
  digits" (`doc`); the line 4 may say "enforced" for the length only until
  AGW-51.
- Apigw-18 and -24: "a malformed return is a 502" for payload 1.0 is not
  backed on the HTTP plane (section 8).

**Board files changed** (working copy, 2026-10-11; one sentence each, no layout):
`Apigw-Home` (the CTA note), `Apigw-03` (the CLI note and the command line, which
now carries `--cors-configuration`), `Apigw-04`, `-10`, `-13`, `-19`, `-23`, `-27`
(the "refuses this because AWS does" sentence), `Apigw-16` (where the denial is
seen), `Apigw-30` (the ledger dialog note). Nothing else in the design was
touched; the other findings in this table are for the build.

**Build list, in order**
1. Decide AGW-31 (snapshots), AGW-62 (the Invoke tab's principal) and AGW-65
   (Cognito/JWT) before any copy is written: they change Apigw-09, -11, -12,
   -16, -22 and -28.
2. Confirm kit: `data-confirm-cancel="Keep it"` and `data-confirm-verb` on the
   seventeen confirms (Apigw-28, -29); the repoint's dynamic body (AGW-21).
3. Create (Apigw-02, -03): the right-hand panel from section 5's lines, the Kind
   sentence, the two teaching lines, the corrected subtitle (AGW-23, and the
   toast), the HTTP panel.
4. Routes (Apigw-05): the intro line, the `REST` badge, the three cards in place
   of the false sentence (AGW-21), the Lambda link with its icon.
5. Dialogs (Apigw-06): the widened pattern (AGW-20), the sentences, the refusal
   slot at the top of the dialog, the rename card removed until AGW-8 is fixed.
6. Method and integration (Apigw-07, -08): pre-fill the current target, the
   per-type sentence, the five-types table from the ledger (foundation B4), the
   true option labels (AGW-22), the two captions.
7. Stages (Apigw-09, -10): the stage panel from `GetStage`, the "Which method"
   card from the resource tree, the corrected copy.
8. Settings and authorizers (Apigw-12, -13): "Used by" from each method's
   `authorizerId`, the teaching lines from `checkAuthorizer` and the ledger, the
   unclipped TTL input (AGW-64).
9. Invoke (Apigw-14, -15, -24): the right-hand cards, the "sends no headers"
   sentence, and the per-answer line from a fixed table of the data plane's own
   messages (an unknown message gets none), with the vocabulary of AGW-30.
10. HTTP (Apigw-18 to -23): the refusals link, the two no-stage cards, the stage
    panel, the CORS teaching lines, max age empty-safe (AGW-63).
11. Keys (Apigw-25 to -27): the "In plans" column, the two cards, the list pane
    that keeps HTTP APIs (AGW-24), the chips that do not overlap (AGW-64).
12. Tags (Apigw-17): the two sentences.
13. Not here (Apigw-30): the message table from the refusal strings, once AGW-59
    has made them one shape.
14. Denied (Apigw-16): redraw after AGW-62; the Wire page carries the denial.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Handler | Redeploy keeps the stage (AGW-1); rename and move (AGW-8); stage to a missing deployment (AGW-9); deletes of missing things (AGW-10); `UpdateRestApi` paths (AGW-11); stage members round trip (AGW-15); stage tags (AGW-16); key length message (AGW-17); plan stages (AGW-18); policy validation (AGW-7) |
| Execute | `ANY` on an HTTP integration (AGW-2); HTTP with templates (AGW-4); `AWS` per the decision (AGW-5); timeout (AGW-6); disabled endpoint (AGW-7); stage variables in a URI (AGW-19); an edit after deploy is live and a repoint changes nothing (AGW-21) |
| SDK v2 | The five update operations (AGW-3); paging on every list, both planes (AGW-12) |
| Model sweep | Every operation in `ops_api-gateway.json` and `ops_apigatewayv2.json` answers 2xx, a modelled error or 501 (AGW-13); the ledger rows sum to the model (AGW-14) |
| Rejection parity | Keep 118 of 126 and 88 of 94; add the constraints the five new operations bring |
| Restart | A full REST API and a full HTTP API, with keys and plans; the authorizer cache is expected to be empty afterwards and the test says so |
| Second identity | Integration and authorizer URIs, method ARNs, invoke URLs under a non-default account and region |
| Conformance (boto3) | Every row of section 8; one scenario per F family in section 2 (today: REST mock, REST Lambda, HTTP Lambda, refusals) |
| Browser | `{proxy+}` (AGW-20); the corrected deploy text (AGW-21); option labels (AGW-22); HTTP create then invoke (AGW-23); keys page list (AGW-24); each `new` item in section 6; invoke of a key-required method (AGW-27); wire and invoke under enforce (AGW-26); every confirm's sentence |
| Mutation | `apigateway/` baseline, then survivors in `execute.go`, `authorize.go`, `apikeycheck.go`, `v2_execute.go`, `stages.go` |
| *Added 2026-10-11* Handler / rejection parity | Stage variable, stage name, tag, API key rules on both planes (AGW-32 to 34, 51); enumerations the model gives in prose (AGW-36, 37, 38); `minimumCompressionSize`, access-log format (AGW-52, 53); HTTP lengths once the generator reads prose (AGW-44); `payloadFormatVersion`, greedy-last, `autoDeploy` deployment id, managed resources (AGW-45 to 48); limits (AGW-49, 50); `PutIntegrationResponse` with no method response (AGW-56) |
| *Added 2026-10-11* Execute | Unreachable backend 504 and a timeout (AGW-41); 10 MiB + 1 on both planes (AGW-42); parameter mapping per key kind (AGW-43); a context number, boolean and object (AGW-39); the event's key set against the page's example (AGW-40); an edit with and without a redeploy under the strict switch (AGW-31) |
| *Added 2026-10-11* SDK v2 | Every member of every create body read back (AGW-35, AGW-15); the status of every served operation from the model (AGW-58); `GetStages` `deploymentId`, `embed` (AGW-57); a 26-item list pages at 25 (AGW-12) |
| *Added 2026-10-11* Routing and sweep | The three unsigned paths (AGW-61); the sweep classifies a route miss as one and every S row is `notbuilt` (AGW-59, 60) |
| *Added 2026-10-11* Browser | The fidelity table's build list, board by board, re-rendered; the Invoke tab under enforce (AGW-62); CORS max age empty (AGW-63); the unclipped TTL input and the chips (AGW-64) |

## 8. Recording queue

Every one of these is `doc` or `memory` today. Scenarios go in
`conformance/tests/test_apigateway.py`.

| Claim | Source now | Scenario |
|---|---|---|
| `CreateDeployment` to an existing stage keeps its variables, method settings and tags | memory (the model is silent; `doc` says stage settings need no redeploy) | `redeploy_keeps_stage` |
| What `variables` on that call does to an existing stage's variables | memory | `redeploy_variables` |
| An HTTP integration with method `ANY` forwards the caller's method | doc (read 2026-10-11: api-gateway-set-up-simple-proxy; HTTP API http-integrations page). *Was memory.* | `http_proxy_any_method` |
| A non-proxy HTTP integration applies request and response templates | model (`Integration.requestTemplates`, `IntegrationResponse`). *Was doc.* | `http_integration_templates` |
| `UpdateResource` to a sibling's name is `ConflictException`; to an invalid part is `BadRequestException` | model for the codes (`UpdateResource` lists 400, 409, 404); memory for which condition is which. *Was memory.* | `rename_resource_refusals` |
| `CreateStage` and `UpdateStage` with an unknown deployment are refused, and with what | model ("references a pre-existing Deployment"); memory for the code. *Was memory.* | `stage_unknown_deployment` |
| Deleting a missing stage, API, method, integration, response is `NotFoundException` | model (each operation lists NotFoundException). *Was doc.* | `delete_missing` |
| `UpdateRestApi` with an unknown patch path is refused | model (`PatchOperation.op`: "Attempts to apply an unsupported operation ... will return an error message"). *Was doc.* | `update_restapi_bad_path` |
| `CreateDeployment` with no methods: "The REST API doesn't contain any methods" | memory | `deploy_empty_api` |
| `CreateDeployment` with an unwired method: "No integration defined for method" | memory | `deploy_unwired_method` |
| An unmatched path or verb is `403 {"message":"Missing Authentication Token"}` | doc for the status and the case (`MISSING_AUTHENTICATION_TOKEN` 403, "unsupported API method or resource"); memory for the body. *Was memory.* | `unmatched_path` |
| An unknown stage in the URL: status and message | memory | `unknown_stage` |
| A method with no integration, called on a deployed stage | memory | `call_unwired_method` |
| A malformed proxy response is 502, and its message | doc, read 2026-10-11 (set-up-lambda-proxy-integrations: "API Gateway returns a `502 Bad Gateway` error response"); the message is memory | `malformed_proxy_response` |
| Authorizer answers: 401 `Unauthorized`, 403 with "explicit deny", 500 with a null message | model for the 401 ("it returns a 401 Unauthorized response without calling the Lambda function"); memory for the 403 wording and the 500's null message. *Was doc (the ledger asserts all three).* | `authorizer_answers` |
| A REQUEST authorizer with TTL 0 and a missing identity source still invokes | memory, and in tension with the model's prose (section 6). *Was doc.* | `request_authorizer_no_cache` |
| Deleting an authorizer a method names | memory | `delete_authorizer_in_use` |
| A missing or wrong API key is `403 {"message":"Forbidden"}` | doc for 403 (`INVALID_API_KEY` 403); memory for the body `Forbidden`. *Was doc.* | `api_key_gate` |
| A key created without `enabled` is disabled | model (`CreateApiKeyRequest.enabled` `default:false`). *Was doc (the ledger asserts it).* | `api_key_default_state` |
| API key value length and the message for each end; a duplicate value | doc, read 2026-10-11 (usage plans page: name 1024, value alphanumeric 20 to 128, unique); the two messages are memory. *Was memory.* | `api_key_value_rules` |
| A usage plan naming an HTTP API, or a missing stage | model (`CreateUsagePlan` lists NotFoundException); memory for which refusal. *Was memory.* | `usage_plan_stage_rules` |
| Deleting a usage plan with keys attached | memory | `delete_plan_with_keys` |
| Two APIs with one name | memory | `duplicate_api_name` |
| Path part grammar, and `{proxy+}` anywhere but last | memory for the REST grammar and `{proxy+}` placement; doc for an HTTP route ("The greedy path variable must be at the end of the resource path"). *Was doc.* | `path_part_rules` |
| Stage variables in an integration URI | doc, read 2026-10-11 (stage-variables). Stays queued for the unresolved-variable case | `stage_variable_uri` |
| Tagging a stage ARN | model (`CreateStage.tags`); memory for the ARN shape. *Was doc.* | `tag_stage` |
| `disableExecuteApiEndpoint` answers what | model for the behaviour; memory for the status and body. *Was doc.* | `disabled_endpoint` |
| Whether `PutIntegration` checks that the function exists | memory | `integration_missing_function` |
| A repointed stage serves the old deployment's definition | model and doc (Deployment "immutable"; how-to-deploy-api). *Was doc.* | `repoint_stage_serves_snapshot` |
| HTTP: no route is `404 {"message":"Not Found"}`; a stage with no deployment likewise | doc, read 2026-10-11 (http-api-develop-routes: no route and no `$default` stage both `{"message":"Not Found"}`); "a stage with no deployment" is memory. *Was doc (the ledger).* | `http_not_found` |
| HTTP: authorizer denial is 403 `Forbidden`; a failing function is a bare 500 | memory (no page read states the 403 body or the bare 500). *Was doc (the ledger).* | `http_authorizer_answers` |
| HTTP: CORS credentials with origin `*` is refused, and the message | memory (the code's own message claims it) | `http_cors_credentials` |
| HTTP: a duplicate route key, and case folding of the method | doc for greedy-last; memory for duplicate and case folding. *Was memory.* | `http_route_key_rules` |
| HTTP: payload 2.0 and 1.0 events, field by field | doc | `http_payload_formats` |
| The REST proxy event, field by field | doc | `rest_proxy_event` |
| AWS's console adds the Lambda permission when an integration is wired | memory | none: a console behaviour, not recordable by the harness. Cite the developer guide page or drop the claim |
| Stage variable value of 513 characters; a value with a space; a key of 65 | model (pattern; v2 length 2,048) and doc (quotas: 64 and 512): the two disagree, which one AWS enforces is memory | `stage_variable_rules` |
| Tag key 129 / value 257 / `aws:` prefix on a REST ARN; value 1,601 on an HTTP ARN | model (REST text; v2 type text) | `tag_rules` |
| A REST list with 26 items and no `limit` returns 25 and a `position` | model ("The default value is 25 and the maximum value is 500") | `rest_list_default_page` |
| A closed backend port through `HTTP_PROXY` answers 504 (or 502) | doc (`INTEGRATION_FAILURE` 504); which one is memory | `unreachable_backend_status` |
| A request body over 10,485,760 bytes is 413 "HTTP content length exceeded 10485760 bytes" | doc (`REQUEST_TOO_LARGE`) | `payload_too_large` |
| An authorizer's `context` number arrives as a string; an object value is a 500 | doc (authorizer output page) | `authorizer_context_stringified` |
| HTTP 2.0: a function returning the JSON string `"hi"` answers body `hi` (the page's table) or `"hi"` | doc (ambiguous) | `http_string_return` |
| HTTP 2.0: `{statusCode: 200, body: "x"}` with no header: the `Content-Type` | memory | `http_default_content_type` |
| A malformed response from a payload-1.0 function on an HTTP API: 500 or 502; from a 2.0 function | memory (the REST page says 502) | `http_malformed_response` |
| The data plane's `x-amzn-ErrorType` header value per error (`MissingAuthenticationTokenException`?) | memory | `data_plane_errortype` |
| REST `HTTP_PROXY`: `Content-Type: application/json` is added when the backend sends none | doc ("returns the default `Content-Type:application/json` header if the content type of a payload is unspecified") | `http_proxy_default_content_type` |
| Quick-create: the managed route, integration and `$default` stage refuse modification and deletion | model (the three sentences) | `quick_create_managed` |
| `UpdateStage` `/cacheClusterEnabled`, `/cacheClusterSize`, `/canarySettings/*`, `copy` promote | model (`Stage`, `PatchOperation.from`) | `update_stage_cache_canary` |
| HTTP `DeleteIntegration` with a route on it; `DeleteAuthorizer` with a route on it; `DeleteDeployment` with a stage on it | memory (the model lists only 404 and 429) | `v2_delete_in_use` |
| HTTP `UpdateStage` `stageVariables`: merge or replace | memory | `v2_stage_variables_update` |
| HTTP authorizer `authorizerResultTtlInSeconds` default | memory | `v2_authorizer_ttl_default` |
| REST `accessLogSettings.destinationArn` as a Firehose delivery stream | model (REST text) | `firehose_destination` |
| A key in two plans covering one stage | doc (usage plans page) | `key_two_plans_one_stage` |
| `LimitExceededException` and the 300 / 10 / 10 limits at create | doc (quotas); code memory | `limits` |
| `CreateRestApi` rate: second call within 3 s (Regional) is `TooManyRequestsException` | doc (quotas) | `control_plane_rate` |

## 9. Owed to integration

| What | With |
|---|---|
| An integration or authorizer is refused by the function's resource policy under enforce, admitted with a statement, and only recorded under soft | Lambda, IAM |
| The `aws:SourceArn` API Gateway sends, against the ARN a SAM or CDK `AWS::Lambda::Permission` names | Lambda, CloudFormation |
| A function that is deleted, renamed, or will not start, behind an integration and behind an authorizer | Lambda |
| Aliases and versions in an integration URI | Lambda |
| The access log and execution log lines arriving in their groups, and a subscription filter firing on one | CloudWatch Logs |
| An alarm on `5XXError` going to ALARM from real failed calls | CloudWatch, SNS |
| A REST API's `policy` evaluated together with identity policies | IAM |
| SAM `Api` and `HttpApi` events, CDK `RestApi` and `HttpApi`, and Terraform, each deployed and then **updated** (the update is where AGW-1 and AGW-3 bite) | CloudFormation |
| A usage plan and key created in one template with the stage they cover | CloudFormation |
| The chain already listed in 90-integration.md: API Gateway to Lambda to SQS to Lambda to Step Functions | Lambda, SQS, Step Functions |
| The wire naming an execute-api request and nesting the Lambda invoke under it | console |
| *Added 2026-10-11.* The console's Invoke tab calling under the API Gateway principal, so a function's resource policy applies to it as to an outside caller (AGW-62) | Lambda, IAM, console |
| *Added 2026-10-11.* An `AWS::ApiGateway::Deployment` that does not change when the API does is stale on AWS and fresh here (AGW-31): a CDK or SAM stack deployed, edited and re-deployed, with and without a deployment trigger | CloudFormation |
| *Added 2026-10-11.* A stack with a Cognito or JWT authorizer, the commonest one in a real app (AGW-65) | CloudFormation |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] AGW-1 to AGW-70 each closed, with the test that failed first named beside it
      (AGW-31, 62, 65, 67 and 68 are decisions first: below).
- [ ] Decided and written into `SUPPORT.md`:
  - [ ] Deployments stay records and the live API is served (the default this
        document assumes), or a deployment becomes a snapshot. If records: the
        stage's `deploymentId` is tiered C everywhere it is shown.
  - [ ] `HTTP` non-proxy: implemented, or refused at `PutIntegration` (AGW-4).
        Silently proxying is not an option.
  - [ ] `AWS` non-proxy: refused at `PutIntegration`, or left to fail at call
        time with the tier and the console saying exactly that (AGW-5).
  - [ ] The REST API `policy`: evaluated, or C (AGW-7).
  - [ ] `TestInvokeMethod` and `TestInvokeAuthorizer`: S, or F.
  - [ ] *Added 2026-10-11.* Deployments as snapshots (AGW-31): the model calls a
        deployment "an immutable representation" and `doc` says every update
        needs a redeploy, so serving the live API is a **more forgiving**
        deliberate difference. Options, with their cost: keep it and list it
        (today); snapshot the resource tree, methods, integrations and
        authorizers into each deployment and serve a stage from its deployment
        (the record is the API's tree, tens of resources; every deployment holds
        a copy; `apiSummary` becomes real); or snapshot behind a switch
        (`--apigateway-strict`), off by default, as SQS-28. Written into
        `SUPPORT.md` either way.
  - [ ] *Added 2026-10-11.* Cognito and JWT authorizers (AGW-65): keep refusing,
        or accept, store, mark C, and let the call through with the token
        decoded and not verified (stated). The first option keeps the deploy
        failure for the commonest authorizer; the second is more forgiving and
        says so on every screen that shows it.
  - [ ] *Added 2026-10-11.* The console Invoke tab's principal (AGW-62).
  - [ ] *Added 2026-10-11.* Each status field of AGW-67 (L7): `apiStatus`,
        `cacheClusterStatus`, `deploymentStatus`, auto-deploy timing: reported
        immediately and the difference listed, or with a short visible delay
        and a rate (section 12).
  - [ ] *Added 2026-10-11.* The strict switch of AGW-68 (L8): which pacing and
        propagation rules, off by default, in memory.
- [ ] Every operation in both models has a tier, and the model sweep test holds
      it: 124 and 103, with no 404 or 405 that the model does not describe.
- [ ] The ledger's counts equal the model's, family by family, and `not-built.md` agrees.
- [ ] Section 2 has no "decide", "not established" or "absent" left: every
      setting table row has a target that is met.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped. That resolves each "not established" there:
      duplicate API names, `{proxy+}` placement, sibling path variables, deleting
      an authorizer in use, whether `PutIntegration` checks the function,
      `variables` on a redeploy, the stage name rule, the unknown-stage answer,
      deleting a plan with keys, the tag limits, the HTTP 500 detail.
- [ ] `metrics/enabled`, stage tags and HTTP `disableExecuteApiEndpoint` each
      established by a test and tiered.
- [ ] All 30 boards implemented from the foundation's shell, or listed here as
      dropped with the reason. Each `new` item reads only the data named for it.
- [ ] *Added 2026-10-11.* The fidelity table (6a) was made from rendered boards
      beside rendered pages, **again after the build**, and no row says
      "differs" without a decision beside it. Today: 1 matches, 27 differ, 1
      cannot match yet, 1 design is wrong.
- [ ] *Added 2026-10-11.* The per-service icons (`static/aws/apigw.svg`,
      `lambda.svg`) are in the rail, the list header, the resource header and the
      Lambda targets; no board's `AG` or `λ` mark was copied in.
- [ ] *Added 2026-10-11.* The five teaching lines on every setting the console
      writes (name and unit; the value in words; one sentence; range, default,
      **enforced or stored** from the validation table and the ledger; the
      neighbour check): the create forms, the authorizer forms, CORS, the stage
      panel, the key and plan forms. The ranges come from the model (TTL 0 to
      3600, `maxAge` -1 to 86400, timeout 50 to 30000) and the lengths from the
      parity table once AGW-44 lets it read them.
- [ ] *Added 2026-10-11.* Both wires: N/A with a reason. Both models declare
      only `restJson1` (checked in the model's service shape), so there is one
      decoder per plane; the wire quirks are held by tests (the
      `PutIntegration` body's `httpMethod`, `items` versus `item`, the `values`
      key of `GetUsage`) and the sweep of AGW-13 sends every operation in the
      model's own binding.
- [ ] *Added 2026-10-11.* Each asynchronous operation decided (L7, section 12):
      `UpdateStage` cache cluster, `FlushStageCache`, `DeleteRestApi` (202),
      HTTP deployments and auto-deploy, VPC link (S), domain name (S).
- [ ] No statement on an API Gateway screen is in the "not yet backed" list in
      section 6.
- [ ] The seventeen confirms say only what the code does.
- [ ] Every setting the console shows that section 2 tiers C reads as cosmetic
      where it is shown: stage `deploymentId`, caching, throttling, tracing, a
      plan's throttle and quota, `AWS_IAM`.
- [ ] A function wired from the console can be called under enforce, or the
      page says before the first call that it cannot (AGW-26).
- [ ] The three browser specs cover every route under `/apigw`, `/apigw-http`
      and `/apigw-keys` with the route gate on (48 routes in
      `internal/console/testdata/routes.txt` today), and the accessibility sweep
      is clean on all ten tabs, the keys page and create.
- [ ] Conformance: a scenario for every row in section 8 and every F family.
      Zero "answered differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Idle heap unchanged with five more routed operations and their constraint
      tables, or the change explained.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 0 | *Added 2026-10-11.* The decisions of section 10 that change copy and shape (AGW-31, 62, 65, 67, 68), and the generator teaching `dzaudit` the HTTP model's prose lengths (AGW-44), before any red test is written | `cmd/dzaudit/`, `docs/SUPPORT.md` |
| 1 | Decisions and contract: tiers for all 227 operations and every setting | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | The model sweep test, red (AGW-13, AGW-14) | `apigateway/coverage_model_test.go` (new), `docs/ledger_test.go` |
| 3 | Red tests for AGW-1 to AGW-19 and AGW-31 to AGW-58 (the validation rules, the lost members, the status table, the event shape) | `apigateway/*_test.go`, `apigateway/testdata/status_*.json` (new) |
| 4 | Regenerate routes and constraints for the five update operations and the flush operations | `cmd/dzaudit/`, `apigateway/validate.go`, `apigateway/testdata/routes_apigateway.json`, `cases_apigateway.json` |
| 5 | Update handlers; refusals for every unrouted operation | `apigateway/methods.go`, `deployments.go`, `stages.go`, `router.go` |
| 6 | Stage correctness: redeploy, deployment checks, dropped members, tags | `apigateway/deployments.go`, `stages.go`, `store.go`, `views.go`, `tags.go` |
| 7 | Resources, REST API patches and settings, deletes of missing things | `apigateway/resources.go`, `restapis.go`, `store.go`, `methods.go` |
| 8 | The data plane: `ANY`, timeout and its 504, stage variables in URIs, the disabled endpoint, the `HTTP` and `AWS` decisions, the 10 MiB limit, the event's `requestContext`, stringified `context` (AGW-39 to 42) | `apigateway/execute.go`, `execute_proxy.go`, `authorize.go` |
| 8a | *Added 2026-10-11.* HTTP API: parameter mapping, `payloadFormatVersion`, managed quick-create resources, `autoDeploy` rules, limits (AGW-43, 45 to 48) | `apigateway/v2_execute.go`, `v2_integrations.go`, `v2_control.go`, `v2_stages.go`, a mapping file (new) |
| 8b | *Added 2026-10-11.* Strictness and status: the limits table, the pacing switch, the status fields (AGW-49, 67, 68) | `apigateway/limits.go` (new), `apigateway.go` (Options), `cmd/doze-aws` (flag), `views.go` |
| 9 | Keys and plans; pagination on both planes | `apigateway/apikeys.go`, `usageplans.go`, `v2_control.go`, `v2_routes.go`, `v2_integrations.go`, `v2_authorizers.go`, `v2_stages.go` |
| 10 | Section 5 sweep; restart and second-identity tests | `apigateway/sdk_test.go`, `v2_sdk_test.go`, `execute_test.go`, `v2_execute_test.go` |
| 11 | Conformance scenarios and the claims table | `conformance/tests/test_apigateway.py`, the claims file from foundation B1 |
| 12 | Console: REST (build list items 1 to 9 of 6a) | `internal/console/templates/apigw.html`, `create.html` (`apigw_create`), `handlers_apigw.go`, `handlers_apigw_auth.go`, `client_apigw.go`, `client_apigw_auth.go` |
| 13 | Console: HTTP, keys (items 10 to 14) | `templates/apigw_v2.html`, `apigw_keys.html`, `handlers_apigw_v2.go`, `handlers_apigw_keys.go`, `client_apigw_v2.go`, `client_apigw_keys.go` |
| 13a | *Added 2026-10-11.* Re-render every board beside its page and redo 6a | `e2e/tests/zz-*` (throwaway) |
| 14 | Browser tests | `e2e/tests/apigateway.spec.ts`, `apigateway-http.spec.ts`, `apigateway-keys.spec.ts` |
| 15 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 2 and 4 come before the fixes on purpose: the sweep test is what stops the
contract drifting again, and the five update operations need generated route and
constraint entries before a handler can be attached.

**Size.** *Corrected 2026-10-11: was "L is right, and it is the largest L in the
plan as written".* It is **XL, about fifteen focused days**, and the reading
that found it is below. The first estimate held 30 findings and a console that
existed; the review added 40 findings (15 at P1 that change what a deploy sees),
found the validation tables empty where the models speak only in prose (REST:
`authorizationType`, `passthroughBehavior`, stage variables, tags; HTTP: 51
length rules), found HTTP API parameter mapping to be a feature rather than a
setting, and found 27 of 30 boards to differ in shape or content. By step:

| Work | Days |
|---|---|
| Decisions, `dzaudit` prose lengths, the contract for 227 operations, the status table, the refined sweep (steps 0 to 2, 4) | 2 |
| Control plane: five update operations, flush, pagination on 14 list operations, validation rules AGW-32 to 38 and 44, 49 to 56, lost members, stage correctness (steps 3, 5 to 7, 9) | 3.5 |
| Data plane: timeouts, `ANY`, URI variables, 413 and 504, event shape, context stringification, disabled endpoint (step 8) | 1.5 |
| HTTP API: parameter mapping (a small engine over header, query string, path and status with five variable kinds), payload rules, managed resources (8a) | 1.5 |
| Strictness and status fields, `--apigateway-strict` (8b) | 1 |
| Console, 30 boards: shapes, copy, teaching lines, confirms, the per-answer table (steps 12, 13, 13a) | 4 |
| Conformance scenarios (about 40 now), mutation baseline, ledger and changelog (steps 10, 11, 14, 15) | 1.5 |
| **Total** | **15** |

If the decision on `HTTP` non-proxy integrations is "implement", that is a
mapping-template engine and is its own piece of work; this document assumes
"refuse". If the decision on AGW-31 is "snapshot", add about a day for the
store, the data plane's read and the migration of existing records.

**Prior art.** LocalStack serves the same two execute-api address shapes, which
is why they were chosen here (the ledger says so). Nothing else is cited: where
another emulator stands on redeploys, update operations or template mapping was
not checked for this document, and should be before step 5 rather than assumed.

## 12. Pre-stage review (2026-10-11)

Answers the twelve questions of [`../02-lessons-from-sqs.md`](../02-lessons-from-sqs.md)
for this service. Done before the stage, so the stage starts from checked
facts. Findings AGW-31 to AGW-70 are in section 4; the board comparison is 6a.

**What was read.** `.audit-models/api-gateway.json` (124 operations, 7 error
shapes) and `.audit-models/apigatewayv2.json` (103 operations, 5 error shapes):
the `documentation` of every operation, and of every member of the input and
output shapes of every operation the document lists as functional (REST APIs,
resources, methods and their responses, integrations and their responses,
deployments, stages, authorizers, API keys, usage plans, usage plan keys,
account, tags; HTTP APIs, routes, integrations, stages, deployments,
authorizers, CORS, tags) and of the shapes the findings name; the `errors` and
HTTP status of every operation; every `range`, `length`, `pattern`, `default`,
`required`, `jsonName`, `httpQuery`, `httpLabel` and `paginated` trait. The
service shapes declare `restJson1` and nothing else. AWS pages read that day
(each tagged `doc` where used): the quotas page, the REST and HTTP API quotas
pages, how-to-deploy-api, updating-api, stage-variables,
supported-gateway-response-types, set-up-lambda-proxy-integrations,
api-gateway-set-up-simple-proxy, api-gateway-api-usage-plans,
api-gateway-lambda-authorizer-output, http-api-develop-integrations-lambda,
http-api-develop-integrations-http, http-api-develop-routes,
http-api-parameter-mapping and http-api-cors. The code under `apigateway/`, the
console templates, handlers and clients, and `docs/SUPPORT.md` were read in
full. A binary built from this checkout was driven over the wire (about 230
operation probes, signed; 60 targeted probes; an enforce-mode instance) and the
console was rendered (`e2e/tests/zz-apigw-fidelity.spec.ts`, deleted).

**Not read or not settled** (stays `memory`): other emulators (prior art in
section 11 is unchanged), the REST resource-path grammar beyond the message the
code carries, request validators and models (S), canary release, binary media
types, the HTTP JWT authorizer, and what AWS answers for an unknown stage on a
REST API.

**Counts.** 22 claims in this document corrected in place; 35 sources re-tagged
(12 findings, 23 recording rows); 40 findings added (15 P1, 17 P2, 8 P3; no
P0). Direction: 21 more forgiving (AGW-31 to 34, 36 to 38, 42, 44 to 53, 56,
62, 68), 3 stricter (AGW-54, 55, 65), 12 wrong code, shape or data (AGW-39 to 41, 58
to 61, 63, 64, 66, 69, 70), 4 missing (AGW-35, 43, 57, 67); AGW-42 and AGW-48
are each both a more-forgiving and a wrong-shape row and are counted once, in
the first.

| Q | Answer |
|---|---|
| **L1** model documentation | **Findings** AGW-32, 33, 34, 35, 36, 37, 38, 44, 45, 47, 48, 51, 52, 53, 54, 55, 56, 57, 58, 69, 70. The number found is the lesson: the REST model has 26 enums, two `@pattern`s (both a status code) and no `@range` or `@length` at all, and the HTTP model's 20 `@length` traits are on the portal types while the types the served write members use (`StringWithLengthBetween1And128` and the rest) carry their length only in their documentation; stage variables, tags, stage names, `authorizationType`, `passthroughBehavior`, `requestParameters` keys and `minimumCompressionSize` are rules that exist **only** in prose. The generator (`dzaudit`) saw none of them, which is why the ledger's "118 of 126" and "88 of 94" are true and say little. Numbers checked and held: authorizer TTL 0 to 3600 default 300, `maxAge` -1 to 86400, HTTP integration timeout 50 to 30000, `statusCode` `^[1-5]\d\d$`, `cacheClusterSize` and the other enums, quota period enum, API key `enabled` default false, usage plan `throttle`/`quota` shapes. One conflict between sources, written down not resolved: a stage variable's value is 2,048 in the HTTP model and 512 on the quotas page (AGW-32). |
| **L2** optional parameters | **Findings** AGW-12 (default page 25, max 500), 36, 37, 38 (`identitySource` required for TOKEN, and for REQUEST with caching), 45 (`payloadFormatVersion` "Required"), 46, 47 (`deploymentId` "Can't be updated if autoDeploy is enabled"), 48 (managed), 52, 53, 56 (the status "must map to an existing MethodResponse"), 57 (`deploymentId` filter, `embed`). **Checked, none**: `CreateApiKey.enabled` (default false, held), `GetApiKey.includeValue` and `GetApiKeys.includeValues`/`name`/`customerId` (held), `GetUsagePlans.keyId`, `GetUsagePlanKeys.name`, `quota.period`, quick-create `target` and `routeKey` ("If you don't specify a routeKey, a default route of $default is created", held), CORS `maxAge`. |
| **L3** error codes | **Findings** AGW-41 (504 for an integration failure), 58 (success statuses), 66 (refusals the operation does not list), 49 (`LimitExceededException`), 68 (`TooManyRequestsException`). **Checked, none** beyond the first thirty findings: every REST operation lists BadRequest, Conflict, NotFound, TooManyRequests, Unauthorized (and `LimitExceeded` on creates, `ServiceUnavailable` on the deployment operations); the code answers 400, 404, 409 and never 401, 429 or 503, which is fine until AGW-49 and AGW-68 are built. `UnauthorizedException` (401) on the control plane is not produced: IAM answers that. The data plane's status per condition is the gateway-response table's (403 missing token, 403 invalid key, 401 unauthorized, 403 access denied, 500 authorizer failure/configuration, 504 integration, 413 too large): held except AGW-41 and AGW-42; the header `x-amzn-ErrorType` is `memory` (recording queue). |
| **L4** direction | Every row of section 4's second table names it (counts above). The two to decide first are **AGW-31** (more forgiving, deliberate, the one whose cost is a real design choice) and **AGW-65** (stricter, decided, the commonest authorizer). |
| **L5** every wire | **Checked, none new** beyond AGW-59 to 61: both models declare only `restJson1` (service shape traits read), so there is one decoder per plane and no flattened XML lists. The restJson1 quirks are held by the existing generated tables and tests: `PutIntegration`'s `httpMethod` label versus `httpMethod` body member (`requestHttpMethod` / `httpMethod` jsonNames, read), `item` versus `items` and the `Stages` collection's `item` with no `position`, `Usage.items` as `values`, v2 `items`/`nextToken`, timestamps as epoch seconds (REST) and ISO-8601 (HTTP), `UpdateMethodResponse` 201. Validation sees the whole request in one shape (`validate_hook.go` merges labels, query, headers and body). **Gaps:** an accepted-as-spelled test exists for a subset (`sdk_test.go`, `v2_sdk_test.go`, `apikeys_sdk_test.go`, the CloudFormation applies), not for each of the 55 REST and 37 HTTP served operations; the refined sweep (AGW-13, 60) and the status table (AGW-58) are the cheapest way, generated from the model's bindings. |
| **L6** tokens | **Findings** AGW-12 (`position`/`nextToken` unused), AGW-50, 51. The model says nothing of a stale or foreign `position` or `nextToken`; each list operation lists `BadRequestException`, so the plan is: an opaque token (the last item's key), a malformed one is 400, one for a deleted item resumes after its place; marked `memory`, recorded as `list_stale_token`. **Checked, none:** deployment and resource ids are stable and not reused; a stage's `deploymentId` after `DeleteDeployment` is refused while pointed at; an API key's `id` and `value` are distinct and `value` appears only with `includeValue(s)`; the authorizer's `usageIdentifierKey` is a key **value** (held, `apikeycheck.go`); the cache key is per authorizer and identity (held); account and region in ARNs and the proxy event come from the identity, not literals (`s.id`; read). There is no ETag in this API. |
| **L7** asynchronous | **Finding** AGW-67. The model's status models: `RestApi.apiStatus` (UPDATING, AVAILABLE, PENDING, FAILED; "When the status message is UPDATING you can still invoke it"), `Stage.cacheClusterStatus` (CREATE_IN_PROGRESS, AVAILABLE, DELETE_IN_PROGRESS, NOT_AVAILABLE, FLUSH_IN_PROGRESS), HTTP `deploymentStatus` (PENDING, FAILED, DEPLOYED) and `lastDeploymentStatusMessage`, `VpcLink.status` ("typically takes 2-4 minutes"; S), `DomainName.domainNameStatus` (S), `DomainNameAccessAssociation`; every REST delete answers 202. **Proposed decisions** (each listed as a difference in `SUPPORT.md` and shown on the page where the value appears): `apiStatus` reported `AVAILABLE` always; the cache cluster stored (C) and `cacheClusterStatus` `AVAILABLE` at once when enabled, `FLUSH_IN_PROGRESS` never; `FlushStageCache` and `FlushStageAuthorizersCache` 202 with an effect for the second only; an HTTP deployment `DEPLOYED` inside the call, and an auto-deploy inside the call that changed the route (so a test that reads the stage straight after a route change sees the new deployment id, which AWS shows a moment later). The one that needs a rate is none of these: this service has no operation whose result a caller polls for minutes **and** whose in-progress state a board draws. |
| **L8** latency rules | **Finding** AGW-68. AWS's own pacing: the control-plane request rates above, and "the update operation might take a few minutes to complete" after a key is added to a plan. Each gets the same switch (`--apigateway-strict`, `[apigateway] strict = true`, off, in memory): the rates as a token bucket per operation family answering `TooManyRequestsException` (429); key-to-plan propagation is **not** worth a switch (it is "might take", not a rule). Account-level throttling (10,000 RPS, 5,000 burst) and plan throttle/quota stay C: the ledger says so and the console must. |
| **L9** state | Persistent, and stays so: APIs and everything under them, deployments, stages and their settings, authorizers, keys, plans, plan keys, account. In memory, and stays so: the authorizer result cache (bounded at 512 entries; a restart clears it, as the ledger says), log stream names (one per stage per process). New state this stage adds: the limits table has none (counts are derived from the record); the strict switch's token buckets are the process's and not persisted; if AGW-31 is "snapshot" each deployment's copy of the tree **is** application data and persists; delayed status transitions, if any, are memory only and a restart completes them. The restart test of section 3 covers one resource of each kind on the HTTP plane and the shell of the REST plane; it is extended in step 10. |
| **L10** boards rendered | **Done**: section 6a, 30 of 30, light, five in dark, the main page at 1024 and 900. Result: 1 matches, 27 differ, 1 cannot match yet, 1 design is wrong. |
| **L11** five lines | **Finding** (no id: the build list carries it): none of the pages writes the teaching lines. The create forms have two fields and no line; the authorizer forms have the board's "Cache seconds a verdict is kept per identity; 0 asks every time" and no range or enforced marker; CORS has one sentence for six fields; the stage panel does not exist. The sources for line 4 are: REST TTL (`checkAuthorizer`, hand-written from model prose), `maxAge` and HTTP TTL (the model's `range`), API key length (`doc`), the HTTP lengths (AGW-44), stage name and variables (AGW-32, 33). The settings record the SQS kit reads does not exist for this service; build list step 3 creates it (one table: setting, unit, range, default, tier, source). |
| **L12** icons | **Checked, none**: the rail, the list header, the resource header and the Lambda targets draw `static/aws/apigw.svg` and `lambda.svg` (rendered). The boards' `AG`/`λ` marks are placeholders. |

**Classes of mistake this service is most at risk of** (judgement, in order):
1. **A rule that lives only in a model's prose.** Stage variables, tags, stage
   names, HTTP lengths, `authorizationType`, `minimumCompressionSize`: the
   generator emits nothing, the ledger's counts stay green, and the service
   accepts what AWS refuses. The fix is a hand-written table beside the
   generated one with the sentence quoted in a comment, plus teaching the
   generator the "[1-128]" form.
2. **Accepted and lost.** Terraform and CloudFormation read every member back
   and diff it. The second place to look after a new member is the view, not
   the handler.
3. **Live where AWS snapshots.** Anything that depends on a deployment being a
   point in time: a repoint, a canary, `apiSummary`, `autoDeploy`, the strict
   switch. One decision (AGW-31) touches ten rows.
4. **Stored where AWS acts.** `C` rows that are features on AWS: HTTP API
   parameter mapping, timeouts, CORS header precedence, the disabled endpoint,
   binary media types, compression, the resource policy. The tier table must
   say "C" only when AWS's own documentation says the setting does nothing a
   caller can see.
5. **Data-plane shapes read by handler code.** Event keys, stringified
   `context`, statuses (413, 504), headers. An SDK never sees them; the
   function's author's code does, and a `curl` does.
6. **Console sentences written from the ledger.** Four false sentences were
   found in the first pass (AGW-21), one more here (the Invoke tab's denial,
   AGW-62), and six boards carried a claim about "AWS's own service model" that
   the code does not have. Every console statement needs a test that reads it
   against the code, and a board's statement needs the same.
7. **Quick-create and managed resources.** CDK `HttpApi`, SAM `HttpApi` and
   `--target` create resources the caller did not ask for; the managed rules and
   the defaults (payload version, `$default` stage, auto-deploy) are where the
   first deploy and the first update differ.
