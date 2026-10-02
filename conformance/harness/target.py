"""Where the scenarios run: a doze-aws this file boots, or a real AWS account.

    CONFORMANCE_TARGET=doze   (the default) build the binary, boot it on a free
                              port over an empty data dir, compare.
    CONFORMANCE_TARGET=aws    a real account, to RECORD. Refused unless
                              CONFORMANCE_AWS_ACCOUNT names the account the
                              credentials resolve to.

    CONFORMANCE_ENDPOINT      skip the boot and use a doze-aws already running.

The account check is the one guard that matters. Scenarios create and delete
real resources, and "whatever credentials happened to be in the environment" is
not a decision anybody made. The id has to be typed.
"""

import os
import pathlib
import re
import socket
import subprocess
import tempfile
import time

import boto3
from botocore.config import Config

from .normalize import Normalizer

REPO = pathlib.Path(__file__).resolve().parents[2]

# awsident's fixed local identity.
DOZE_ACCOUNT = "000000000000"
DOZE_REGION = "us-east-1"

AWS_HOST = re.compile(r"https?://[a-z0-9.-]+\.amazonaws\.com")


class Target:
    name = ""
    region = ""
    account = ""

    def client(self, service, **kw):
        raise NotImplementedError

    def normalizer(self):
        raise NotImplementedError

    def close(self):
        pass


class Doze(Target):
    name = "doze"
    region = DOZE_REGION
    account = DOZE_ACCOUNT

    def __init__(self):
        self.proc = None
        self.tmp = None
        self.endpoint = os.environ.get("CONFORMANCE_ENDPOINT") or self._boot()

    def _boot(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="doze-conformance-")
        root = pathlib.Path(self.tmp.name)
        binary = root / ("doze-aws.exe" if os.name == "nt" else "doze-aws")
        subprocess.run(
            ["go", "build", "-o", str(binary), "./cmd/doze-aws"],
            cwd=REPO, check=True, env={**os.environ, "GOWORK": "off"},
        )
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            port = s.getsockname()[1]
        # cwd is the temp dir: doze-aws auto-loads ./doze-aws.toml and
        # ./template.yaml, and a run must not depend on where it was started.
        self.log = open(root / "doze-aws.log", "w")
        self.proc = subprocess.Popen(
            [str(binary), "--listen", f"127.0.0.1:{port}", "--data-dir", str(root / "data")],
            cwd=root, stdout=self.log, stderr=subprocess.STDOUT,
        )
        deadline = time.time() + 30
        while time.time() < deadline:
            if self.proc.poll() is not None:
                break
            try:
                socket.create_connection(("127.0.0.1", port), timeout=0.2).close()
                return f"http://127.0.0.1:{port}"
            except OSError:
                time.sleep(0.05)
        self.log.flush()
        raise RuntimeError("doze-aws did not start:\n" + (root / "doze-aws.log").read_text())

    def client(self, service, **kw):
        return boto3.client(
            service,
            endpoint_url=self.endpoint,
            region_name=self.region,
            aws_access_key_id="test",
            aws_secret_access_key="test",
            # One attempt. A retry turns a 500 that happens once into a pass,
            # and here the 500 is the finding.
            config=Config(retries={"total_max_attempts": 1}),
            **kw,
        )

    def normalizer(self):
        return Normalizer(self.account, endpoints=[self.endpoint], region=self.region)

    def close(self):
        if self.proc is not None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()
            self.log.close()
        if self.tmp is not None:
            self.tmp.cleanup()


class AWS(Target):
    name = "aws"

    def __init__(self):
        want = os.environ.get("CONFORMANCE_AWS_ACCOUNT", "")
        if not want:
            raise RuntimeError(
                "CONFORMANCE_TARGET=aws creates real resources. Set "
                "CONFORMANCE_AWS_ACCOUNT to the 12-digit account they may be created in."
            )
        self.region = os.environ.get("CONFORMANCE_AWS_REGION", DOZE_REGION)
        self.session = boto3.Session(region_name=self.region)
        self.account = self.session.client("sts").get_caller_identity()["Account"]
        if self.account != want:
            raise RuntimeError(
                f"the credentials in this environment belong to account {self.account}, "
                f"and CONFORMANCE_AWS_ACCOUNT allows only {want}. Nothing was created."
            )

    def client(self, service, **kw):
        return self.session.client(service, **kw)

    def normalizer(self):
        return Normalizer(self.account, endpoint_patterns=[AWS_HOST], region=self.region)


def open_target():
    which = os.environ.get("CONFORMANCE_TARGET", "doze")
    if which == "doze":
        return Doze()
    if which == "aws":
        return AWS()
    raise RuntimeError(f"CONFORMANCE_TARGET={which!r}: expected doze or aws")
