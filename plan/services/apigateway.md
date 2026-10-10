# API Gateway

Stage 14. Size L. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds API Gateway's own.

REST APIs (`apigateway`, v1) and HTTP APIs (`apigatewayv2`) are two AWS services
served by one package, `apigateway/`, and one console page group. They are
planned together and keep separate contract tables, because their models,
ledgers and wire spellings are separate.

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

**Estimate:** L, five or more focused days. The findings below suggest the top
of that range; see the note at the end of section 11. **Actual:** _fill in when done._

## 2. Contract

### REST (apigateway v1)

The model lists **124 operations** (`apigateway/testdata/ops_api-gateway.json`).
The ledger says "54 of 124"; its own functional rows name 55. Routed today: 52
in the generated route table (`apigateway/validate.go`, `routes`) plus
`GetRestApis` and the three tag operations added in `router.go`, which is 56
route entries for 55 operations (`CreateApiKey` and `ImportApiKeys` share one).
AGW-14 reconciles the count.

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
| GetAccount, UpdateAccount | 2 | F | C | The CloudWatch role and throttle settings round-trip and do nothing. The ledger says F and "nominal" in the note. |
| GetTags, TagResource, UntagResource | 3 | F | F | REST API ARNs only. AGW-16. |
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
| minimumCompressionSize | stored, not applied | C | |
| tags | F | F | |

**Stage settings**

| Setting | Now | Target | To confirm |
|---|---|---|---|
| deploymentId | stored; changes nothing that is served | C, said so | The decision in section 10. |
| variables | F | F | On the proxy event and readable by a REQUEST authorizer. Not substituted into an integration URI (`${stageVariables.x}`), which AWS documents. AGW-19. |
| accessLogSettings | F | F | |
| methodSettings: logging/loglevel, logging/dataTrace | F | F | |
| methodSettings: metrics/enabled | not established | C or F | Metrics are published for every request whatever it says. |
| methodSettings: throttling/*, caching/* | C | C | Stored, reported with AWS's defaults filled in. |
| tracingEnabled | C | C | |
| cacheClusterEnabled, cacheClusterSize | dropped: not stored, reads back `false` | C | AGW-15. |
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
| timeoutInMillis | stored, not applied | C or F | AGW-6. |
| requestTemplates, requestParameters, passthroughBehavior, contentHandling, credentials, cacheKeyParameters, cacheNamespace | stored, not applied | C | AGW-6. |
| connectionType `VPC_LINK` | accepted | refused, as v2 does | AGW-6. |

**Authorizer settings (REST)**

| Setting | Now | Target |
|---|---|---|
| type TOKEN, REQUEST | F | F |
| type COGNITO_USER_POOLS | refused, 400 | refused |
| identitySource, identityValidationExpression | F | F |
| authorizerResultTtlInSeconds, 0 to 3600, default 300 | F | F |
| authorizerCredentials, authType | stored | C |

**Method settings (REST)**

| Setting | Now | Target |
|---|---|---|
| authorizationType NONE, CUSTOM | F | F |
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
| Api: disableExecuteApiEndpoint, routeSelectionExpression, ipAddressType, disableSchemaValidation | stored | C | Whether `disableExecuteApiEndpoint` is applied is not established. |
| Route: routeKey, target, authorizationType NONE and CUSTOM, authorizerId | F | F | |
| Route: authorizationType AWS_IAM | C | C | The ledger says so. |
| Route: authorizationType JWT | S (501) | S | |
| Route: apiKeyRequired, authorizationScopes, requestParameters, requestModels, operationName | stored | C | |
| Integration: payloadFormatVersion 1.0 and 2.0, integrationMethod | F | F | |
| Integration: timeoutInMillis, requestParameters, responseParameters, credentialsArn | stored | C | Not in the ledger. |
| Integration: integrationSubtype, connectionType VPC_LINK | S (501) | S | |
| Stage: autoDeploy, stageVariables, accessLogSettings | F | F | |
| Stage: defaultRouteSettings, routeSettings | C | C | |
| Authorizer: identitySource, payload version, enableSimpleResponses, TTL 0 to 3600 | F | F | |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `apigateway/`: 40 tests in 16 files. Data plane and routing precedence (`execute_test.go` 4, `v2_execute_test.go` 4), SDK end to end (`sdk_test.go` 2, `v2_sdk_test.go` 1, `apikeys_sdk_test.go` 1, `logging_sdk_test.go` 1), authorizers and the cache (`authorize_test.go` 2, `authcache_*` 6), API keys (`apikey_test.go` 1), invoke URLs 3, v2 audit 7, `conformance_fixes_test.go` 1 |
| Parity | `rejection_parity_test.go` (2), `v2_parity_test.go` (1), `validate_hook_test.go` (4). Ledger: 118 of 126 constraints for REST, 88 of 94 for HTTP, `knownGaps` and `knownGapsV2` empty, the rest unexpressible on this wire |
| CloudFormation | `cloudformation/apigw_authorizer_apply_test.go`, `apigw_keys_apply_test.go`, `apigwv2_apply_test.go` |
| Console (Go) | `internal/console/apigw_auth_test.go`, `apigw_keys_test.go`, `apigw_v2_test.go`: one test each |
| Conformance | `conformance/tests/test_apigateway.py`: 4 scenarios (REST mock, REST to Lambda, HTTP to Lambda, refusals), all **unverified**: `conformance/snapshots/` is empty |
| Browser | `e2e/tests/apigateway.spec.ts` 8, `apigateway-http.spec.ts` 4, `apigateway-keys.spec.ts` 1 |
| Mutation | No per-package baseline (foundation B3) |
| Restart | No test writes an API, restarts and reads it (foundation B5) |

The ledger's "Verified against" names "the dispatch table against
`testdata/ops_api-gateway.json`". No Go file reads that file; the 18 REST
operations with no tier are what a test of that kind would have caught (AGW-14).

## 4. Findings

Evidence `run` means reproduced on 2026-10-10 against the package through its
HTTP handler, with a throwaway test loaded by `go test -overlay`.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| AGW-1 | P0 | `CreateDeployment` with a `stageName` that already exists replaces the stage. Its variables, method settings (logging, throttling, caching), tags, access-log settings and tracing flag are gone. The console's Deploy button does exactly this call. | `apigateway/deployments.go`, `createDeployment`, the `api.Stages[req.StageName] = &stage{...}` assignment | run | memory: AWS moves the stage to the new deployment and keeps its settings | Handler test: stage with a variable and a log level, redeploy to the same name, read the stage |
| AGW-2 | P0 | A REST `HTTP` or `HTTP_PROXY` integration with `integrationHttpMethod: ANY` sends the backend a request whose method is the literal word `ANY`. The v2 path treats `ANY` as "use the caller's method". The console writes `integrationHttpMethod = verb`, so wiring a URL on an `ANY` method produces it. | `apigateway/execute.go`, `invokeHTTP`; `internal/console/client_apigw.go`, `PutAPIIntegration` | run (backend saw `ANY /x` for a `POST`) | memory | Execute test: `ANY` method, `HTTP_PROXY`, `integrationHttpMethod` `ANY`, assert the backend's method |
| AGW-3 | P0 | `UpdateMethod`, `UpdateIntegration`, `UpdateMethodResponse`, `UpdateIntegrationResponse` and `UpdateDeployment` are not routed. Each answers `405 MethodNotAllowed`. A Terraform or CDK update that patches an existing method or integration fails here and succeeds on AWS. | `apigateway/validate.go` `routes`; `apigateway/router.go` `v1Handlers` | run | model: all five are in the model with PATCH bindings | SDK test: put a method and integration, patch each, read it back |
| AGW-4 | P0 | An `HTTP` (non-proxy) integration is served exactly as `HTTP_PROXY`: `requestTemplates`, `requestParameters`, integration responses and selection patterns are stored and never applied. The call succeeds with a different request and response from the one the definition describes. The ledger tiers `HTTP / HTTP_PROXY` as F. | `apigateway/execute.go`, `invokeIntegration`, `case "HTTP", "HTTP_PROXY"` | read | doc: "Set up HTTP integrations" in the developer guide | Execute test: `HTTP` with a request template and a 200 integration response template; assert what the backend receives and what the caller gets |
| AGW-5 | P1 | An `AWS` (non-proxy) integration is accepted by `PutIntegration` and by `CreateDeployment`, then answers 500 on every call. The ledger tiers it S, "refused". It is refused only once the app is running. | `apigateway/methods.go`, `putIntegration`; `execute.go`, `case "AWS"` | read | n/a (the limit is ours) | Decide first (section 10), then a handler test either way |
| AGW-6 | P1 | REST integration settings are stored and have no effect, and the ledger does not say so: `timeoutInMillis` (the forward uses `http.DefaultClient` with no deadline), `passthroughBehavior`, `contentHandling`, `credentials`, `cacheKeyParameters`, `cacheNamespace`. `connectionType: VPC_LINK` is accepted where v2 refuses it. | `apigateway/methods.go`, `putIntegration`; `execute.go`, `invokeHTTP` | read | model (the members), doc (their meaning) | Execute test: a backend that sleeps past `timeoutInMillis` |
| AGW-7 | P1 | RestApi settings stored and not applied. `policy`: any string is accepted, including `"not json"`, and nothing evaluates it under soft or enforce. `disableExecuteApiEndpoint: true` still serves; the console shows a chip saying the endpoint is disabled. `binaryMediaTypes` and `minimumCompressionSize` are not read on the request path. | `apigateway/restapis.go`, `createRestAPI`, `patchRestAPI`; `execute.go`, `serveExecute` | run (policy accepted), read (the rest) | doc | Handler test for a bad policy; execute test for a disabled endpoint |
| AGW-8 | P1 | `UpdateResource` validates nothing. Renaming to a sibling's name returns 200 and leaves two resources at one path; renaming to `has space` returns 200. `/parentId` is set without checking the parent exists or that the move makes a cycle. `CreateResource` refuses the first two. | `apigateway/resources.go`, `patchResource` | run (rename), read (`/parentId`) | memory: AWS answers `ConflictException` and `BadRequestException` | Handler test for each of the four |
| AGW-9 | P1 | A stage can point at a deployment that does not exist: `CreateStage` with `deploymentId: "nope"` is 201, and `UpdateStage` replacing `/deploymentId` with `nope` is 200. The v2 path checks. | `apigateway/stages.go`, `createStage`, `patchStage` | run | memory | Handler test for both |
| AGW-10 | P1 | Deleting something that is not there succeeds: `DeleteStage` on a missing stage is 202, `DeleteRestApi` on a missing API is 202, and `DeleteMethod`, `DeleteIntegration`, `DeleteMethodResponse`, `DeleteIntegrationResponse` on a missing verb or status are 204. | `apigateway/stages.go` `deleteStage`; `store.go` `Delete`; `methods.go` `deleteMethod` and the three beside it | run (stage, API), read (the four) | doc: the API reference lists `NotFoundException` for each | Handler test per operation |
| AGW-11 | P1 | `UpdateRestApi` answers 200 for a patch path it does not know, and ignores the `op` (a `remove` is a replace with an empty value). `UpdateStage` refuses an unknown path. Paths AWS supports and this does not: `/binaryMediaTypes/...`, `/minimumCompressionSize`, `/disableExecuteApiEndpoint`, `/endpointConfiguration/types/...`. | `apigateway/restapis.go`, `patchRestAPI` | run (`/bogus` is 200) | doc: the UpdateRestApi patch table | Handler test for an unknown path and for each supported path |
| AGW-12 | P1 | No list operation paginates. `limit` and `position` (REST) and `maxResults` and `nextToken` (HTTP) are bound in the route tables and never read. | every `get...s` and `list...` handler in `apigateway/` | read | model | SDK test: three APIs, `limit: 1`, follow `position` |
| AGW-13 | P1 | Thirteen REST and two HTTP operations that are not served answer a bare 404 or 405 instead of the 501 by name that the ledger promises: `FlushStageCache`, `FlushStageAuthorizersCache`, `GetExport`, `GetSdk`, `UpdateUsage`, the four domain-name access-association operations (404); `PutRestApi`, `TestInvokeMethod`, `TestInvokeAuthorizer` (405); `ImportRestApi`, which reaches `CreateRestApi` and answers a 400 about `name`; v2 `ImportApi` and `ReimportApi` (405). | `apigateway/router.go`, `v1Specs`, `v2Specs` | run | model | Table test: every operation in both `ops_*.json` files, built from its model binding, answers 2xx, a modelled 4xx, or 501, never an unmodelled 404 or 405 |
| AGW-14 | P3 | The ledger's counts do not add up. REST: "54 of 124" beside rows naming 55; domains "12" (10, or 14 with the access associations); validators and models "10" (11); documentation "10" (11); SDK and export "5" (4); gateway responses "4" (5). HTTP: domains and mappings "11" (10); portals and routing rules "26" (31). `not-built.md` repeats them. "Verified against" cites a dispatch-table test over `ops_api-gateway.json` that does not exist. | `docs/SUPPORT.md`, both API Gateway sections; `docs/not-built.md` | read | model | The table test from AGW-13, plus a ledger test that the rows sum to the model's count |
| AGW-15 | P1 | Stage members that are not round-tripped. `cacheClusterEnabled` and `cacheClusterSize` are not decoded and read back `false` and `NOT_AVAILABLE` whatever was sent; `canarySettings`, `documentationVersion` and `clientCertificateId` are dropped. Cosmetic means accepted and returned; these are accepted and lost. | `apigateway/stages.go`, `createStage`; `views.go`, `viewStage` | read | model | SDK test: create a stage with each, read it back |
| AGW-16 | P1 | `TagResource` on a stage ARN (`.../restapis/{id}/stages/{name}`) answers 204 and tags the **API**: `apiIDFromARN` takes the API id and nothing checks the rest. A stage's own tags (accepted at `CreateStage`) cannot be read or changed. | `apigateway/tags.go`, `tagTarget` | run (204), read (where it lands) | doc: stages are taggable resources | Handler test: tag a stage ARN, read the API's tags and the stage's |
| AGW-17 | P3 | An API key value over 128 characters is refused with "API Key value should be at least 20 characters". | `apigateway/apikeys.go`, `createAPIKey` | read | memory | Handler test with 129 characters |
| AGW-18 | P1 | A usage plan stage is not checked: `apiStages` and the `/apiStages` patch verify only that the id names any API, so an HTTP API's id is accepted, and a stage name that does not exist is accepted. | `apigateway/usageplans.go`, `createUsagePlan`, `patchUsagePlan` | read | memory | Handler test: an HTTP API id; a missing stage |
| AGW-19 | P1 | Stage variables are not substituted into an integration URI. `expandURI` replaces `{param}` only. A template that points a stage at its backend with `${stageVariables.url}` forwards to the literal string. | `apigateway/execute.go`, `expandURI` | read | doc: "Use API Gateway stage variables" | Execute test: HTTP_PROXY with `http://${stageVariables.host}/x` |
| AGW-20 | P2 | The Add resource and Rename dialogs' input `pattern` is `[a-zA-Z0-9._\-\{\}]+`. It blocks `+` and `:`, so `{proxy+}`, the commonest shape SAM and CDK emit, cannot be typed. The service accepts both. | `internal/console/templates/apigw.html`, `apigw_api` (the two dialogs) | read | n/a | Browser test adds `{proxy+}` |
| AGW-21 | P2 | Four console statements say edits wait for a deploy or that a stage serves its deployment. All are false here: "Edits change the DEFINITION only — callers see them after the next deploy"; "This is the step that makes edits callable"; "pointing a fresh stage at an old one is the rollback story"; and the repoint confirm, "Callers get it straight away" (foundation A13). | `templates/apigw.html`, `apigw_routes`, `apigw_stages` | read; the data plane reads `api.Resources`, never a deployment | n/a | Browser test asserts the replacement text; execute test: edit after deploy is served, and repointing a stage changes no answer |
| AGW-22 | P2 | The integration type options say "AWS — Lambda, templates apply" and "HTTP — forward, templates apply". No template is applied to either (AGW-4, AGW-5). "Integration responses — how backend answers map to it" is true for MOCK only. | `templates/apigw.html`, `apigw_method` | read | n/a | Browser test on the option text, tied to the tier table in section 2 |
| AGW-23 | P2 | A new HTTP API is announced as answering: the toast "HTTP API created — add a route, and it answers at $default" and the create page's "answering at $default as soon as a route exists". The console creates no stage, and the API answers 404 until one exists. | `internal/console/handlers_apigw_v2.go`, `apigwHTTPCreate`; `templates/create.html`, `apigw_create` | run (404 with a route and no stage) | n/a | Browser test: create, add a route, invoke, read the page's own warning |
| AGW-24 | P2 | The keys page builds its list pane from `ListRestAPIs`, so HTTP APIs disappear from the list on that page and the count changes. | `internal/console/handlers_apigw_keys.go`, `apigwKeys` | read | n/a | Browser test: one REST and one HTTP API, open the keys page, count the list |
| AGW-25 | P2 | The console cannot do what the API can, and in several cases cannot even show it. Fetched and never rendered: a stage's variables, tracing flag and method settings (`APIStage` carries them). No control for: stage variables, access-log and method settings, a usage plan's throttle and quota, removing a stage from a plan, renaming a key, a REST API's key source, editing an HTTP route (only delete and add), an HTTP stage's variables, HTTP deployments. | `internal/console/client_apigw.go`, `APIStages`; `templates/apigw.html`, `apigw_v2.html`, `apigw_keys.html` | read | n/a | One browser test per item the design adds (section 6) |
| AGW-26 | P2 | Wiring a Lambda integration from the console adds no Lambda permission. Under `--iam-mode enforce` every call through it is `502`, with the function's refusal of `apigateway.amazonaws.com` in the body. | `internal/console/client_apigw.go`, `PutAPIIntegration`; `client_apigw_v2.go`, `AddHTTPRoute`; `client_apigw_auth.go`, `CreateAPIAuthorizer` | run (the 502 and its text) | memory: AWS's console adds the permission when you pick a function | Browser test under enforce: wire, invoke, read the answer |
| AGW-27 | P2 | The Invoke tab sends no headers but `Content-Type`. A method that requires an API key always answers 403 there and one behind an authorizer always 401, with nothing on the page to say why. It also offers no `OPTIONS` or `ANY`. | `internal/console/client_apigw.go`, `InvokeAPI`; `templates/apigw.html`, `apigw_invoke` | read | n/a | Browser test: invoke a key-required method, read the explanation |
| AGW-28 | P3 | A comment says deleting a deployment a stage serves leaves the stage serving. The service refuses that delete. | `internal/console/client_apigw.go`, above `DeleteAPIDeployment` | read | n/a | none; fix with AGW-21 |
| AGW-29 | P3 | The method panel names an authorizer by its id, not its name. | `templates/apigw.html`, `apigw_method` | read | n/a | Browser test |
| AGW-30 | P3 | The Invoke result colours any status of 400 or more as an error. A 403 or 404 from the data plane is the gateway refusing as AWS does; a 502 is the user's function. The foundation's vocabulary (A2) has no state for "the gateway answered, and it was not a 200". | `templates/apigw.html`, `apigw_result` | read | n/a | Settle with A2, then a browser test per state |

Owned by the foundation, listed so they are not lost:

- **A2.** The generic "AWS refuses this too" line is printed under hand-written
  refusals here ("Name is required", the API key length, the CORS credentials
  rule, a duplicate authorizer). Every 501 in section 2, when an app's call is
  on the wire, reads "a bug in the emulator".
- **A7.** `copy.go` says API Gateway has "no create form on purpose". It has one
  (`/apigw/create`), the empty state has no button for it, and the `Unsel` line
  cannot render because `apigwList` opens the first API.
- **A9.** The fidelity dialog on an HTTP API's page shows the REST rows.
- **A11.** The HTTP create form's CLI preview leaves out the CORS configuration
  the form sends.
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
      not established.
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
      service checks nothing today, the console's `pattern` does.
- [ ] Every `UpdateStage` patch path: variables add and remove, access-log
      settings, each method-setting path on `*/*` and on one method, an unknown
      path refused.
- [ ] `DeleteDeployment` while a stage points at it is refused (there is a
      test); a missing one is 404.
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
- [ ] Pagination on every list (AGW-12).
- [ ] Tags on an API through both ARN shapes; on a stage (AGW-16); the 50-tag
      limit and key and value rules: not established.
- [ ] Access and execution logs: one access line per request in the stage's
      format; the execution narrative at INFO and ERROR; `dataTrace`.
- [ ] Metrics: `Count`, `Latency`, `4XXError` and `5XXError` by `ApiName` and
      `Stage` for REST; `4xx` and `5xx` by `ApiId` and `Stage` for HTTP.
- [ ] An API with resources, methods, integrations, responses, authorizers,
      stages, deployments, keys and plans survives a restart, and answers the
      same request the same way afterwards.
- [ ] Everything above under a non-default account and region: integration
      URIs, authorizer URIs, the method ARN a policy is matched against, invoke URLs.

## 6. Console

The design's "API Gateway" page: 30 boards. Templates: `templates/apigw.html`,
`apigw_v2.html`, `apigw_keys.html`, the `apigw_create` block in `create.html`,
shared pieces in `panes.html` and `layout.html`. Handlers: `handlers_apigw.go`,
`handlers_apigw_v2.go`, `handlers_apigw_keys.go`, `handlers_apigw_auth.go`;
clients `client_apigw*.go`; routes in `routes_compute.go`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Apigw-Home | No APIs yet | `apigw_home`, `empty_detail`; `copy.go` | exists; the "New API" button is **new** (foundation A7) |
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
| Apigw-30-Not-Here | Fidelity, and the 501 families | `info_button`, `info_panel`; `info.go` | dialog exists (foundation A9); the message table and the "Not implemented" state are **new** (foundation A2) |

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
  by name, rather than a bare 404". For 15 operations it is a bare 404 or 405
  (AGW-13). The board is wrong until AGW-13 is fixed. Its "REST: usage" row is
  true of `GetUsage` only.
- Apigw-04, Apigw-10, Apigw-13, Apigw-23 and Apigw-27 print the generic "AWS
  refuses this too" line under refusals that are hand-written here. Foundation
  A2 decides the text; until a recording exists each is `memory` (section 8).
- Apigw-05 and Apigw-15 say an unmatched path answers `403 Missing
  Authentication Token` as AWS does. `memory`.
- Apigw-06 says "nothing is checked on rename". True today (AGW-8); the note is
  deleted with the fix, and the dialog gains the add dialog's refusals.
- Apigw-08 says an `AWS` integration "saves, and a call answers 500". Read, not
  run, and it changes with AGW-5.
- Apigw-09 says redeploying points an existing stage at the new deployment. It
  also wipes the stage (AGW-1); the sentence is true only after the fix.
- Apigw-09's stage panel shows throttling of "10000 rps · burst 5000". Those are
  the defaults the service fills in once any method setting is patched; a stage
  nobody patched has none to show.
- Apigw-11 says AWS brings the old API back when a stage is repointed. `doc`
  (the ledger asserts it); queued.
- Apigw-12 and Apigw-13: the REQUEST-authorizer rule for a cache of 0 is read
  from `authorize.go`, not run.
- Apigw-16 says wiring an integration "does not add that permission". True
  today (AGW-26); if the fix adds it, the board becomes the case where a
  template or the CLI wired the integration.
- Apigw-19 says a deleted function answers a bare 500. Run for a function that
  never existed, not for one deleted afterwards.
- Apigw-25 says only REST methods check a key. Read (`checkAPIKey` has one caller).
- Apigw-28 and Apigw-29 use the pilot's confirm shape. The sentences are the
  templates' own, except the repoint, which keeps the false one on that board
  and corrects it on Apigw-11.

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

## 8. Recording queue

Every one of these is `doc` or `memory` today. Scenarios go in
`conformance/tests/test_apigateway.py`.

| Claim | Source now | Scenario |
|---|---|---|
| `CreateDeployment` to an existing stage keeps its variables, method settings and tags | memory | `redeploy_keeps_stage` |
| What `variables` on that call does to an existing stage's variables | memory | `redeploy_variables` |
| An HTTP integration with method `ANY` forwards the caller's method | memory | `http_proxy_any_method` |
| A non-proxy HTTP integration applies request and response templates | doc | `http_integration_templates` |
| `UpdateResource` to a sibling's name is `ConflictException`; to an invalid part is `BadRequestException` | memory | `rename_resource_refusals` |
| `CreateStage` and `UpdateStage` with an unknown deployment are refused, and with what | memory | `stage_unknown_deployment` |
| Deleting a missing stage, API, method, integration, response is `NotFoundException` | doc | `delete_missing` |
| `UpdateRestApi` with an unknown patch path is refused | doc | `update_restapi_bad_path` |
| `CreateDeployment` with no methods: "The REST API doesn't contain any methods" | memory | `deploy_empty_api` |
| `CreateDeployment` with an unwired method: "No integration defined for method" | memory | `deploy_unwired_method` |
| An unmatched path or verb is `403 {"message":"Missing Authentication Token"}` | memory | `unmatched_path` |
| An unknown stage in the URL: status and message | memory | `unknown_stage` |
| A method with no integration, called on a deployed stage | memory | `call_unwired_method` |
| A malformed proxy response is 502, and its message | doc | `malformed_proxy_response` |
| Authorizer answers: 401 `Unauthorized`, 403 with "explicit deny", 500 with a null message | doc (the ledger asserts all three) | `authorizer_answers` |
| A REQUEST authorizer with TTL 0 and a missing identity source still invokes | doc | `request_authorizer_no_cache` |
| Deleting an authorizer a method names | memory | `delete_authorizer_in_use` |
| A missing or wrong API key is `403 {"message":"Forbidden"}` | doc | `api_key_gate` |
| A key created without `enabled` is disabled | doc (the ledger asserts it) | `api_key_default_state` |
| API key value length and the message for each end; a duplicate value | memory | `api_key_value_rules` |
| A usage plan naming an HTTP API, or a missing stage | memory | `usage_plan_stage_rules` |
| Deleting a usage plan with keys attached | memory | `delete_plan_with_keys` |
| Two APIs with one name | memory | `duplicate_api_name` |
| Path part grammar, and `{proxy+}` anywhere but last | doc | `path_part_rules` |
| Stage variables in an integration URI | doc | `stage_variable_uri` |
| Tagging a stage ARN | doc | `tag_stage` |
| `disableExecuteApiEndpoint` answers what | doc | `disabled_endpoint` |
| Whether `PutIntegration` checks that the function exists | memory | `integration_missing_function` |
| A repointed stage serves the old deployment's definition | doc | `repoint_stage_serves_snapshot` |
| HTTP: no route is `404 {"message":"Not Found"}`; a stage with no deployment likewise | doc (the ledger) | `http_not_found` |
| HTTP: authorizer denial is 403 `Forbidden`; a failing function is a bare 500 | doc (the ledger) | `http_authorizer_answers` |
| HTTP: CORS credentials with origin `*` is refused, and the message | memory (the code's own message claims it) | `http_cors_credentials` |
| HTTP: a duplicate route key, and case folding of the method | memory | `http_route_key_rules` |
| HTTP: payload 2.0 and 1.0 events, field by field | doc | `http_payload_formats` |
| The REST proxy event, field by field | doc | `rest_proxy_event` |
| AWS's console adds the Lambda permission when an integration is wired | memory | none: a console behaviour, not recordable by the harness. Cite the developer guide page or drop the claim |

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

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] AGW-1 to AGW-30 each closed, with the test that failed first named beside it.
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
| 1 | Decisions and contract: tiers for all 227 operations and every setting | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | The model sweep test, red (AGW-13, AGW-14) | `apigateway/coverage_model_test.go` (new), `docs/ledger_test.go` |
| 3 | Red tests for AGW-1 to AGW-19 | `apigateway/*_test.go` |
| 4 | Regenerate routes and constraints for the five update operations and the flush operations | `cmd/dzaudit/`, `apigateway/validate.go`, `apigateway/testdata/routes_apigateway.json`, `cases_apigateway.json` |
| 5 | Update handlers; refusals for every unrouted operation | `apigateway/methods.go`, `deployments.go`, `stages.go`, `router.go` |
| 6 | Stage correctness: redeploy, deployment checks, dropped members, tags | `apigateway/deployments.go`, `stages.go`, `store.go`, `views.go`, `tags.go` |
| 7 | Resources, REST API patches and settings, deletes of missing things | `apigateway/resources.go`, `restapis.go`, `store.go`, `methods.go` |
| 8 | The data plane: `ANY`, timeout, stage variables in URIs, the disabled endpoint, the `HTTP` and `AWS` decisions | `apigateway/execute.go`, `execute_proxy.go` |
| 9 | Keys and plans; pagination on both planes | `apigateway/apikeys.go`, `usageplans.go`, `v2_control.go`, `v2_routes.go`, `v2_integrations.go`, `v2_authorizers.go`, `v2_stages.go` |
| 10 | Section 5 sweep; restart and second-identity tests | `apigateway/sdk_test.go`, `v2_sdk_test.go`, `execute_test.go`, `v2_execute_test.go` |
| 11 | Conformance scenarios and the claims table | `conformance/tests/test_apigateway.py`, the claims file from foundation B1 |
| 12 | Console: REST | `internal/console/templates/apigw.html`, `create.html` (`apigw_create`), `handlers_apigw.go`, `handlers_apigw_auth.go`, `client_apigw.go`, `client_apigw_auth.go` |
| 13 | Console: HTTP, keys | `templates/apigw_v2.html`, `apigw_keys.html`, `handlers_apigw_v2.go`, `handlers_apigw_keys.go`, `client_apigw_v2.go`, `client_apigw_keys.go` |
| 14 | Browser tests | `e2e/tests/apigateway.spec.ts`, `apigateway-http.spec.ts`, `apigateway-keys.spec.ts` |
| 15 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 2 and 4 come before the fixes on purpose: the sweep test is what stops the
contract drifting again, and the five update operations need generated route and
constraint entries before a handler can be attached.

**Size.** L is right, and it is the largest L in the plan as written: two
models, 227 operations to tier, five P0 or near-P0 fixes on the request path,
three decisions that change the data plane, and 30 boards. If the decision on
`HTTP` non-proxy integrations is "implement", that is a mapping-template engine
and is its own piece of work; this document assumes "refuse".

**Prior art.** LocalStack serves the same two execute-api address shapes, which
is why they were chosen here (the ledger says so). Nothing else is cited: where
another emulator stands on redeploys, update operations or template mapping was
not checked for this document, and should be before step 5 rather than assumed.
