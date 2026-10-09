"""Requests an SDK will not make.

boto3 builds every request from the service model, so it cannot send a method
the model does not give a path — "PATCH the list of functions". A tool that
does (curl, a proxy, a typo in a Terraform provider) gets whatever the service
answers, and that answer is part of the contract. This signs and sends such a
request with botocore's own signer and reports what came back.
"""

from botocore.auth import SigV4Auth
from botocore.awsrequest import AWSRequest


def send(client, method, path, body=b"", headers=None):
    """Send one signed request to the client's endpoint. Returns the response
    reduced to what two clouds can be expected to agree on: the status and the
    error type — never the text, which is prose."""
    endpoint = client.meta.endpoint_url.rstrip("/")
    request = AWSRequest(method=method, url=endpoint + path, data=body, headers=dict(headers or {}))
    credentials = client._request_signer._credentials  # noqa: SLF001 - the client's own
    SigV4Auth(credentials, client.meta.service_model.signing_name, client.meta.region_name).add_auth(request)
    response = client._endpoint.http_session.send(request.prepare())  # noqa: SLF001
    header = response.headers
    kind = header.get("x-amzn-ErrorType") or header.get("X-Amzn-Errortype") or ""
    if not kind and response.content.lstrip().startswith(b"<"):
        # S3 and the Query protocols answer in XML: <Error><Code>…</Code>.
        text = response.content.decode("utf-8", "replace")
        if "<Code>" in text:
            kind = text.split("<Code>", 1)[1].split("</Code>", 1)[0]
    return {"Status": response.status_code, "ErrorType": kind.split(":")[0]}
