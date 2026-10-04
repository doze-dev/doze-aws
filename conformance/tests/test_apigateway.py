"""API Gateway through boto3 — REST APIs (v1) and HTTP APIs (v2) — and then
through plain HTTP, because a deployed API is something a browser calls.
"""

import json
import urllib.error
import urllib.request

from harness.helpers import zip_bytes

BASIC = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
FAST = {"Delay": 1, "MaxAttempts": 120}

# API Gateway's own ids: ten random characters each.
IDS = ("id", "parentId", "rootResourceId", "restApiId", "resourceId", "deploymentId",
       "ApiId", "RouteId", "IntegrationId", "DeploymentId", "Target", "ApiEndpoint",
       "cacheNamespace", "uri", "IntegrationUri")
CLOCK = ("createdDate", "lastUpdatedDate", "CreatedDate", "LastUpdatedDate")

PROXY = '''
import json

def handler(event, context):
    # Both payload formats, reduced to what a handler usually reads.
    v2 = event.get("version") == "2.0"
    seen = {
        "method": event["requestContext"]["http"]["method"] if v2 else event["httpMethod"],
        "path": event["rawPath"] if v2 else event["path"],
        "query": event.get("queryStringParameters"),
        "path_parameters": event.get("pathParameters"),
        "body": event.get("body"),
        "stage": event["requestContext"]["stage"],
    }
    return {"statusCode": 201, "headers": {"content-type": "application/json", "x-handled-by": "lambda"},
            "body": json.dumps(seen)}
'''


def call(url, method="GET", body=None, headers=None):
    req = urllib.request.Request(url, data=body, method=method, headers=headers or {})
    try:
        with urllib.request.urlopen(req) as r:
            status, raw, got = r.status, r.read(), r.headers
    except urllib.error.HTTPError as e:
        status, raw, got = e.code, e.read(), e.headers
    try:
        parsed = json.loads(raw)
    except ValueError:
        parsed = raw.decode(errors="replace")
    return {"status": status, "body": parsed, "x-handled-by": got.get("x-handled-by"),
            "content-type": (got.get("content-type") or "").split(";")[0]}


def proxy_function(client, names, cleanup, service_role):
    lam = client("lambda")
    name = names("fn")
    arn = lam.create_function(
        FunctionName=name, Runtime="python3.12", Role=service_role("lambda", BASIC),
        Handler="index.handler", Code={"ZipFile": zip_bytes({"index.py": PROXY})}, Timeout=10,
    )["FunctionArn"]
    cleanup(lam.delete_function, FunctionName=name)
    lam.get_waiter("function_active_v2").wait(FunctionName=name, WaiterConfig=FAST)
    lam.add_permission(FunctionName=name, StatementId="apigw", Action="lambda:InvokeFunction",
                       Principal="apigateway.amazonaws.com")
    return name, arn


def test_rest_api_with_a_mock_integration(client, names, cleanup, snapshot, target, eventually):
    api = client("apigateway")
    made = snapshot.match("create", api.create_rest_api(name=names("api"), description="conformance"),
                          opaque=IDS + CLOCK)
    rid = made["id"]
    cleanup(api.delete_rest_api, restApiId=rid)

    root = api.get_resources(restApiId=rid)["items"][0]
    snapshot.match("root", root, opaque=IDS)
    res = snapshot.match("resource", api.create_resource(
        restApiId=rid, parentId=root["id"], pathPart="ping"), opaque=IDS)
    at = dict(restApiId=rid, resourceId=res["id"], httpMethod="GET")
    snapshot.match("method", api.put_method(**at, authorizationType="NONE"))
    snapshot.match("integration", api.put_integration(
        **at, type="MOCK", requestTemplates={"application/json": '{"statusCode": 200}'}),
        opaque=IDS)
    snapshot.match("method-response", api.put_method_response(**at, statusCode="200"))
    snapshot.match("integration-response", api.put_integration_response(
        **at, statusCode="200", responseTemplates={"application/json": '{"pong": true}'}))

    deployed = snapshot.match("deploy", api.create_deployment(restApiId=rid, stageName="dev"), opaque=IDS + CLOCK)
    snapshot.match("stage", api.get_stage(restApiId=rid, stageName="dev"), opaque=IDS + CLOCK)
    # A deployment a stage serves cannot be deleted out from under it.
    snapshot.error("delete-a-served-deployment", lambda: api.delete_deployment(
        restApiId=rid, deploymentId=deployed["id"]))
    snapshot.error("delete-an-absent-deployment", lambda: api.delete_deployment(
        restApiId=rid, deploymentId="nonesuch"))

    def answered():
        r = call(target.execute_api_url(rid, "dev", "ping"))
        assert r["status"] == 200, r
        return r

    snapshot.match("get", eventually(answered, timeout=60))
    snapshot.match("no-such-path", call(target.execute_api_url(rid, "dev", "nowhere")))
    snapshot.match("no-such-method", call(target.execute_api_url(rid, "dev", "ping"), method="POST"))
    snapshot.match("resources", sorted(r["path"] for r in api.get_resources(restApiId=rid)["items"]))


def test_rest_api_proxies_to_lambda(client, names, cleanup, snapshot, service_role, target, eventually):
    api = client("apigateway")
    _, fn_arn = proxy_function(client, names, cleanup, service_role)
    rid = api.create_rest_api(name=names("api"))["id"]
    cleanup(api.delete_rest_api, restApiId=rid)
    root = api.get_resources(restApiId=rid)["items"][0]["id"]
    orders = api.create_resource(restApiId=rid, parentId=root, pathPart="orders")["id"]
    one = api.create_resource(restApiId=rid, parentId=orders, pathPart="{id}")["id"]
    uri = (f"arn:aws:apigateway:{target.region}:lambda:path/2015-03-31/functions/"
           f"{fn_arn}/invocations")
    for resource in (orders, one):
        api.put_method(restApiId=rid, resourceId=resource, httpMethod="ANY", authorizationType="NONE")
        api.put_integration(restApiId=rid, resourceId=resource, httpMethod="ANY",
                            type="AWS_PROXY", integrationHttpMethod="POST", uri=uri)
    api.create_deployment(restApiId=rid, stageName="prod")

    def answered():
        r = call(target.execute_api_url(rid, "prod", "orders/42?expand=items"))
        assert r["status"] == 201, r
        return r

    snapshot.match("get-with-a-path-parameter", eventually(answered, timeout=90))
    snapshot.match("post-with-a-body", call(
        target.execute_api_url(rid, "prod", "orders"), method="POST",
        body=b'{"sku": "book"}', headers={"content-type": "application/json"}))


def test_http_api_proxies_to_lambda(client, names, cleanup, snapshot, service_role, target, eventually):
    v2 = client("apigatewayv2")
    _, fn_arn = proxy_function(client, names, cleanup, service_role)
    made = snapshot.match("create", v2.create_api(Name=names("api"), ProtocolType="HTTP"),
                          opaque=IDS + CLOCK)
    aid = made["ApiId"]
    cleanup(v2.delete_api, ApiId=aid)

    integ = snapshot.match("integration", v2.create_integration(
        ApiId=aid, IntegrationType="AWS_PROXY", IntegrationUri=fn_arn, PayloadFormatVersion="2.0"),
        opaque=IDS)
    snapshot.match("route", v2.create_route(
        ApiId=aid, RouteKey="GET /orders/{id}", Target=f"integrations/{integ['IntegrationId']}"),
        opaque=IDS)
    snapshot.match("stage", v2.create_stage(ApiId=aid, StageName="$default", AutoDeploy=True),
                   opaque=IDS + CLOCK)

    def answered():
        r = call(made["ApiEndpoint"].rstrip("/") + "/orders/42?expand=items"
                 if target.name == "aws" else target.execute_api_url(aid, "$default", "orders/42?expand=items"))
        assert r["status"] == 201, r
        return r

    snapshot.match("get", eventually(answered, timeout=90))
    snapshot.match("routes", [r["RouteKey"] for r in v2.get_routes(ApiId=aid)["Items"]])


def test_refusals(client, names, cleanup, snapshot):
    api, v2 = client("apigateway"), client("apigatewayv2")
    snapshot.error("absent-rest-api", lambda: api.get_rest_api(restApiId="neverwas00"))
    snapshot.error("absent-http-api", lambda: v2.get_api(ApiId="neverwas00"))

    rid = api.create_rest_api(name=names("api"))["id"]
    cleanup(api.delete_rest_api, restApiId=rid)
    root = api.get_resources(restApiId=rid)["items"][0]["id"]
    res = api.create_resource(restApiId=rid, parentId=root, pathPart="things")["id"]
    snapshot.error("same-path-twice", lambda: api.create_resource(
        restApiId=rid, parentId=root, pathPart="things"))
    snapshot.error("bad-path-part", lambda: api.create_resource(
        restApiId=rid, parentId=root, pathPart="has space"))
    snapshot.error("absent-parent", lambda: api.create_resource(
        restApiId=rid, parentId="neverwas00", pathPart="x"))
    snapshot.error("bad-http-method", lambda: api.put_method(
        restApiId=rid, resourceId=res, httpMethod="FETCH", authorizationType="NONE"))
    snapshot.error("integration-before-method", lambda: api.put_integration(
        restApiId=rid, resourceId=res, httpMethod="GET", type="MOCK"))
    snapshot.error("deploy-with-no-methods", lambda: api.create_deployment(
        restApiId=rid, stageName="dev"))
    api.put_method(restApiId=rid, resourceId=res, httpMethod="GET", authorizationType="NONE")
    snapshot.error("deploy-a-method-with-no-integration", lambda: api.create_deployment(
        restApiId=rid, stageName="dev"))
    snapshot.error("absent-stage", lambda: api.get_stage(restApiId=rid, stageName="nope"))

    aid = v2.create_api(Name=names("http"), ProtocolType="HTTP")["ApiId"]
    cleanup(v2.delete_api, ApiId=aid)
    snapshot.error("bad-route-key", lambda: v2.create_route(ApiId=aid, RouteKey="orders"))
    snapshot.error("route-to-absent-integration", lambda: v2.create_route(
        ApiId=aid, RouteKey="GET /x", Target="integrations/neverwas"))
    snapshot.error("bad-protocol", lambda: v2.create_api(Name=names("bad"), ProtocolType="GOPHER"))
