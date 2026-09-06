"""doze-aws's Python runtime client: the Lambda Runtime API loop, in one file.

It stands in for awslambdaric so a python3.x function runs with nothing
installed but the interpreter. The protocol is the four routes every Lambda
runtime speaks, the handler resolution and context object are awslambdaric's,
and logging is wired the way Lambda wires it — a request id on every record,
JSON records when AWS_LAMBDA_LOG_FORMAT=JSON.
"""

import datetime
import decimal
import importlib
import json
import logging
import os
import sys
import time
import traceback
import urllib.request

API = "http://" + os.environ["AWS_LAMBDA_RUNTIME_API"] + "/2018-06-01/runtime/"


def _post(path, body, headers=None):
    req = urllib.request.Request(API + path, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    with urllib.request.urlopen(req) as resp:
        resp.read()


def _error_body(exc, request_id=None):
    body = {
        "errorMessage": str(exc),
        "errorType": type(exc).__name__,
        "stackTrace": traceback.format_tb(exc.__traceback__),
    }
    if request_id:
        body["requestId"] = request_id
    return json.dumps(body).encode()


# Line-buffered output: a print must reach the runtime before the REPORT line
# that closes its invocation, or it is attributed to the wrong request.
for stream in (sys.stdout, sys.stderr):
    try:
        stream.reconfigure(line_buffering=True)
    except AttributeError:
        pass

# ---- logging, the way Lambda wires it ----


class _RequestIdFilter(logging.Filter):
    request_id = ""

    def filter(self, record):
        record.aws_request_id = self.request_id
        return True


class _JsonFormatter(logging.Formatter):
    def format(self, record):
        out = {
            "timestamp": datetime.datetime.utcfromtimestamp(record.created).isoformat(timespec="milliseconds") + "Z",
            "level": record.levelname,
            "message": record.getMessage(),
            "logger": record.name,
            "requestId": getattr(record, "aws_request_id", ""),
        }
        if record.exc_info:
            out["errorType"] = record.exc_info[0].__name__
            out["errorMessage"] = str(record.exc_info[1])
            out["stackTrace"] = traceback.format_exception(*record.exc_info)
        return json.dumps(out)


_rid_filter = _RequestIdFilter()
_handler = logging.StreamHandler(sys.stdout)
_handler.addFilter(_rid_filter)
if os.environ.get("AWS_LAMBDA_LOG_FORMAT", "").upper() == "JSON":
    _handler.setFormatter(_JsonFormatter())
else:
    _handler.setFormatter(logging.Formatter("[%(levelname)s]\t%(asctime)s.%(msecs)03dZ\t%(aws_request_id)s\t%(message)s", "%Y-%m-%dT%H:%M:%S"))
    logging.Formatter.converter = time.gmtime
logging.getLogger().addHandler(_handler)
logging.getLogger().setLevel(os.environ.get("AWS_LAMBDA_LOG_LEVEL", "INFO").upper() or "INFO")


# ---- the handler ----


def _load_handler(spec):
    """'pkg/module.func' → the callable, with LAMBDA_TASK_ROOT on sys.path."""
    task_root = os.environ.get("LAMBDA_TASK_ROOT") or os.getcwd()
    if task_root not in sys.path:
        sys.path.insert(0, task_root)
    if "." not in spec:
        raise ValueError("Bad handler '%s': not of the form module.function" % spec)
    module_name, func_name = spec.rsplit(".", 1)
    module_name = module_name.replace("/", ".")
    try:
        module = importlib.import_module(module_name)
    except ImportError as e:
        raise ImportError("Unable to import module '%s': %s" % (module_name, e)) from e
    try:
        return getattr(module, func_name)
    except AttributeError:
        raise AttributeError("Handler '%s' missing on module '%s'" % (func_name, module_name)) from None


class ClientContext:
    def __init__(self, raw):
        self.client = _Bag(raw.get("client") or {})
        self.custom = raw.get("custom") or {}
        self.env = raw.get("env") or {}


class _Bag:
    def __init__(self, raw):
        self.installation_id = raw.get("installation_id")
        self.app_title = raw.get("app_title")
        self.app_version_name = raw.get("app_version_name")
        self.app_version_code = raw.get("app_version_code")
        self.app_package_name = raw.get("app_package_name")


class CognitoIdentity:
    def __init__(self, raw):
        self.cognito_identity_id = raw.get("cognitoIdentityId")
        self.cognito_identity_pool_id = raw.get("cognitoIdentityPoolId")


class LambdaContext:
    """The context object AWS hands a Python handler, field for field."""

    def __init__(self, request_id, headers):
        self.aws_request_id = request_id
        self.function_name = os.environ.get("AWS_LAMBDA_FUNCTION_NAME", "")
        self.function_version = os.environ.get("AWS_LAMBDA_FUNCTION_VERSION", "$LATEST")
        self.invoked_function_arn = headers.get("Lambda-Runtime-Invoked-Function-Arn", "")
        self.memory_limit_in_mb = int(os.environ.get("AWS_LAMBDA_FUNCTION_MEMORY_SIZE", "128"))
        self.log_group_name = os.environ.get("AWS_LAMBDA_LOG_GROUP_NAME", "")
        self.log_stream_name = os.environ.get("AWS_LAMBDA_LOG_STREAM_NAME", "")
        self.tenant_id = headers.get("Lambda-Runtime-Aws-Tenant-Id") or None
        self._deadline_ms = int(headers.get("Lambda-Runtime-Deadline-Ms") or 0)
        cc = headers.get("Lambda-Runtime-Client-Context")
        self.client_context = ClientContext(json.loads(cc)) if cc else None
        ci = headers.get("Lambda-Runtime-Cognito-Identity")
        self.identity = CognitoIdentity(json.loads(ci)) if ci else None

    def get_remaining_time_in_millis(self):
        if not self._deadline_ms:
            return 0
        return max(0, self._deadline_ms - int(time.time() * 1000))

    def __repr__(self):
        return "LambdaContext(aws_request_id=%r, function_name=%r)" % (self.aws_request_id, self.function_name)


def _default(o):
    if isinstance(o, decimal.Decimal):
        return int(o) if o == o.to_integral_value() else float(o)
    if isinstance(o, (datetime.datetime, datetime.date)):
        return o.isoformat()
    if isinstance(o, (set, frozenset)):
        return list(o)
    if isinstance(o, bytes):
        return o.decode("utf-8", "replace")
    raise TypeError("Object of type %s is not JSON serializable" % type(o).__name__)


def main():
    spec = sys.argv[1] if len(sys.argv) > 1 else os.environ.get("_HANDLER", "")
    try:
        handler = _load_handler(spec)
    except Exception as e:  # noqa: BLE001 — every init failure is reported the same way
        print("[ERROR] %s: %s" % (type(e).__name__, e))
        traceback.print_exc()
        try:
            _post("init/error", _error_body(e), {"Lambda-Runtime-Function-Error-Type": "Runtime." + ("ImportModuleError" if isinstance(e, ImportError) else "HandlerNotFound" if isinstance(e, AttributeError) else type(e).__name__)})
        finally:
            sys.exit(1)

    while True:
        with urllib.request.urlopen(API + "invocation/next") as resp:
            headers = {k: v for k, v in resp.headers.items()}
            body = resp.read()
        request_id = headers.get("Lambda-Runtime-Aws-Request-Id", "")
        _rid_filter.request_id = request_id
        trace_id = headers.get("Lambda-Runtime-Trace-Id")
        if trace_id:
            os.environ["_X_AMZN_TRACE_ID"] = trace_id
        try:
            event = json.loads(body) if body else None
        except ValueError:
            event = body.decode("utf-8", "replace")
        context = LambdaContext(request_id, headers)
        try:
            result = handler(event, context)
            try:
                out = json.dumps(result, default=_default).encode()
            except (TypeError, ValueError) as e:
                raise RuntimeError("Unable to marshal response: %s" % e) from e
            _post("invocation/%s/response" % request_id, out)
        except Exception as e:  # noqa: BLE001 — a handler may raise anything
            print("[ERROR] %s: %s" % (type(e).__name__, e))
            traceback.print_exc()
            _post("invocation/%s/error" % request_id, _error_body(e, request_id),
                  {"Lambda-Runtime-Function-Error-Type": type(e).__name__})
        finally:
            sys.stdout.flush()
            sys.stderr.flush()


if __name__ == "__main__":
    main()
