"""boto3 does not retry the answer for an operation doze-aws does not build.

A not-built refusal is HTTP 501 with the code NotImplemented (plan decision
D5). The claim that carries the design is that no SDK retries a 501: if one
did, a caller would wait through its backoff for an answer that cannot change.
internal/awshttp/notbuilt_sdk_test.go proves it for the Go SDKs; this proves it
for boto3, in both of its retry modes.

It does not need the doze-aws binary, and it does not compare with AWS. It
serves the refusal itself, in each wire envelope, and counts the requests that
reach it. The same call answered with a 500 is the control: it must be
retried, or "one request" would only mean the client never retries anything.
"""

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import boto3
import pytest
from botocore.config import Config

MESSAGE = "ListThings is not built in doze-aws: there is nothing for it to act on"


def _envelope(kind: str, code: str, status: int) -> tuple[str, bytes]:
    if kind == "json":
        return "application/x-amz-json-1.0", json.dumps({"__type": code, "message": MESSAGE}).encode()
    if kind == "query":
        body = (
            '<?xml version="1.0"?><ErrorResponse xmlns="http://sns.amazonaws.com/doc/2010-03-31/">'
            f"<Error><Type>Sender</Type><Code>{code}</Code><Message>{MESSAGE}</Message></Error>"
            "<RequestId>r1</RequestId></ErrorResponse>"
        )
        return "text/xml", body.encode()
    if kind == "xml":
        body = f'<?xml version="1.0"?><Error><Code>{code}</Code><Message>{MESSAGE}</Message><RequestId>r1</RequestId></Error>'
        return "application/xml", body.encode()
    return "application/json", json.dumps({"Type": "User", "message": MESSAGE}).encode()


class _Server:
    def __init__(self, kind: str, code: str, status: int):
        self.hits = 0
        self.kind, self.code, self.status = kind, code, status
        outer = self

        class H(BaseHTTPRequestHandler):
            def _answer(self):
                outer.hits += 1
                length = int(self.headers.get("Content-Length") or 0)
                if length:
                    self.rfile.read(length)
                ctype, body = _envelope(outer.kind, outer.code, outer.status)
                self.send_response(outer.status)
                self.send_header("Content-Type", ctype)
                self.send_header("x-amzn-ErrorType", outer.code)
                self.send_header("X-Doze-Not-Built", "declined")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            do_GET = do_POST = do_PUT = do_DELETE = do_HEAD = _answer

            def log_message(self, *a):
                pass

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def close(self):
        self.httpd.shutdown()


# (service, envelope kind, the call). Each is one operation of that wire.
CALLS = {
    "dynamodb": ("json", lambda c: c.list_tables()),
    "sns": ("query", lambda c: c.list_topics()),
    "s3": ("xml", lambda c: c.list_buckets()),
    "lambda": ("restjson", lambda c: c.list_functions()),
}


@pytest.mark.parametrize("mode", ["legacy", "standard"])
@pytest.mark.parametrize("service", sorted(CALLS))
def test_a_not_built_refusal_is_not_retried(service, mode):
    kind, call = CALLS[service]
    cfg = Config(retries={"mode": mode, "max_attempts": 4}, connect_timeout=5, read_timeout=5)

    def client(url):
        return boto3.client(
            service, endpoint_url=url, region_name="us-east-1",
            aws_access_key_id="test", aws_secret_access_key="test", config=cfg,
        )

    refusal = _Server(kind, "NotImplemented", 501)
    try:
        with pytest.raises(Exception):
            call(client(refusal.url))
        assert refusal.hits == 1, (
            f"boto3 ({mode}) sent {refusal.hits} requests for a 501 NotImplemented; it retried. "
            "The status has to be reconsidered before the rule spreads."
        )
    finally:
        refusal.close()

    control = _Server(kind, "InternalFailure", 500)
    try:
        with pytest.raises(Exception):
            call(client(control.url))
        assert control.hits >= 2, (
            f"control: boto3 ({mode}) sent {control.hits} request(s) for a 500, so this client does "
            "not retry at all and the 501 result above proves nothing"
        )
    finally:
        control.close()
