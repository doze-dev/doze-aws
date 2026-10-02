"""Turn a boto3 response into something two different clouds can agree on.

A response from AWS and the same response from doze-aws differ in ways that are
not findings: the account, the endpoint, the names this run invented, every id
the service minted, every clock reading. Each of those is replaced by a token
before anything is stored or compared, and what is left is the part that is
supposed to be the same.

The tokens are numbered by first appearance (<uuid:1>, <uuid:2>) rather than
collapsed to one placeholder, because identity is itself behaviour: the
MessageId a send returns must be the MessageId the receive carries, and a
normalizer that erased both to <uuid> could not see them disagree.

Keys are walked in sorted order, so the numbering is the same on both sides.
"""

import base64
import datetime
import hashlib
import re

UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", re.I)

# Bodies up to this size are kept verbatim; beyond it only the digest is, so a
# snapshot never becomes a copy of a multipart upload.
INLINE_BYTES = 1024


class Normalizer:
    def __init__(self, account, endpoints=(), endpoint_patterns=(), region=None):
        """account: the id this target answers as.
        endpoints: literal base URLs that mean "this service" on this target.
        endpoint_patterns: compiled regexes for the same, where a literal will
        not do (AWS answers from a different host per service).
        """
        self.account = account
        self.region = region
        self.endpoints = sorted(endpoints, key=len, reverse=True)
        self.endpoint_patterns = list(endpoint_patterns)
        self.names = {}  # the run's invented name -> its label
        self._seen = {}  # kind -> {value: n}

    def name(self, value, label):
        self.names[value] = label

    def reset(self):
        """Start a new numbering scope. Called once per test."""
        self._seen = {}

    def _token(self, kind, value):
        seen = self._seen.setdefault(kind, {})
        if value not in seen:
            seen[value] = len(seen) + 1
        return f"<{kind}:{seen[value]}>"

    def text(self, s):
        # Names first and longest first: a name is usually inside an ARN or a
        # URL, and "queue-1-dlq" must not be read as "queue-1" plus a suffix.
        for value in sorted(self.names, key=len, reverse=True):
            s = s.replace(value, f"<name:{self.names[value]}>")
        for ep in self.endpoints:
            s = s.replace(ep, "<endpoint>")
        for pat in self.endpoint_patterns:
            s = pat.sub("<endpoint>", s)
        if self.account:
            s = s.replace(self.account, "<account>")
        return UUID.sub(lambda m: self._token("uuid", m.group(0).lower()), s)

    def value(self, v, opaque=(), drop=(), unordered=(), _key=None):
        kw = dict(opaque=opaque, drop=drop, unordered=unordered)
        if _key in opaque and v is not None:
            # Present, and the same one each time it appears — but what it
            # says is the service's own business.
            return self._token(_key, _stable(v))
        if isinstance(v, dict):
            out = {}
            for k in sorted(v, key=str):
                if k in drop:
                    continue
                if k == "ResponseMetadata":
                    # The status is behaviour. The headers, the request id and
                    # the retry count are the transport.
                    out[k] = {"HTTPStatusCode": v[k].get("HTTPStatusCode")}
                    continue
                out[self.text(k) if isinstance(k, str) else k] = self.value(v[k], _key=k, **kw)
            return out
        if isinstance(v, (list, tuple)):
            items = [self.value(x, _key=_key, **kw) for x in v]
            if _key in unordered:
                items.sort(key=_stable)
            return items
        if isinstance(v, datetime.datetime):
            return "<datetime>"
        if isinstance(v, (bytes, bytearray)):
            return _bytes(bytes(v))
        if hasattr(v, "read"):  # a StreamingBody
            return _bytes(v.read())
        if isinstance(v, str):
            return self.text(v)
        if isinstance(v, (set, frozenset)):  # DynamoDB's SS / NS / BS
            return {"<set>": sorted((self.value(x, **kw) for x in v), key=_stable)}
        if v is None or isinstance(v, (bool, int, float)):
            return v
        return self.text(str(v))  # Decimal and friends


def _bytes(b):
    if len(b) <= INLINE_BYTES:
        try:
            return {"<bytes>": b.decode("utf-8")}
        except UnicodeDecodeError:
            return {"<bytes:base64>": base64.b64encode(b).decode()}
    return {"<bytes:sha256>": hashlib.sha256(b).hexdigest(), "length": len(b)}


def _stable(v):
    """A deterministic sort/identity key for anything JSON-shaped."""
    if isinstance(v, dict):
        return "{" + ",".join(f"{k}:{_stable(v[k])}" for k in sorted(v, key=str)) + "}"
    if isinstance(v, (list, tuple)):
        return "[" + ",".join(_stable(x) for x in v) + "]"
    return repr(v)


def diff(want, got, path="$"):
    """Every place two normalized values disagree, as (path, aws, doze)."""
    if isinstance(want, dict) and isinstance(got, dict):
        out = []
        for k in sorted(set(want) | set(got), key=str):
            p = f"{path}.{k}"
            if k not in got:
                out.append((p, want[k], "<absent>"))
            elif k not in want:
                out.append((p, "<absent>", got[k]))
            else:
                out.extend(diff(want[k], got[k], p))
        return out
    if isinstance(want, list) and isinstance(got, list):
        out = []
        if len(want) != len(got):
            out.append((f"{path}.length", len(want), len(got)))
        for i, (w, g) in enumerate(zip(want, got)):
            out.extend(diff(w, g, f"{path}[{i}]"))
        return out
    # bool is an int in Python; True must not equal 1 here.
    if type(want) is not type(got) or want != got:
        return [(path, want, got)]
    return []
