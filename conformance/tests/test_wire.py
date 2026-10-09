"""What the REST services say to a method they have no operation for.

The path-routed services (Lambda, API Gateway, S3) route on method and path.
boto3 only ever sends pairs the model defines, so the answer to a pair it does
not — PATCH on a collection, DELETE on a list — is reachable only by building
the request by hand. Each scenario asserts the part that must hold on any
cloud (a refusal, never a 2xx and never a 5xx) and records the exact status and
error type so the AWS recording can say whether 405 is what AWS answers.
"""

import pytest

from harness.wire import send

CASES = [
    ("lambda", "PATCH", "/2015-03-31/functions"),
    ("lambda", "DELETE", "/2015-03-31/functions"),
    ("lambda", "PUT", "/2015-03-31/functions"),
    ("apigateway", "PATCH", "/restapis"),
    ("apigateway", "DELETE", "/restapis"),
    ("s3", "PATCH", "/dzc-no-such-bucket"),
    ("s3", "POST", "/dzc-no-such-bucket"),
    ("s3", "PATCH", "/dzc-no-such-bucket/key"),
]


@pytest.mark.parametrize("service,method,path", CASES, ids=lambda v: str(v).strip("/").replace("/", "_"))
def test_unmodelled_method_is_refused(client, snapshot, service, method, path):
    got = send(client(service), method, path)
    assert 400 <= got["Status"] < 500, f"{method} {path} answered {got}"
    snapshot.match(f"{service}-{method}-{path}", got)
