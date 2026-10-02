"""Snapshots: what real AWS answered, kept, and held against doze-aws.

A scenario calls snapshot.match("label", response) after each call it cares
about. What that does depends on where the scenario is running:

    recording (real AWS)   the normalized response is written down
    comparing (doze-aws)   it is diffed against what was written down
    nothing recorded yet   the call is counted as UNVERIFIED

The third state is the reason this file is careful. A scenario with no
recording still runs, and still proves boto3 can drive doze-aws through it —
but it has not been compared with anything, and it must never be counted as if
it had. The run's summary says how many of each there were.

A snapshot file says where it was recorded. Only a recording made against real
AWS is ever compared, so a file recorded against doze-aws by mistake cannot
turn the suite into doze-aws agreeing with itself.
"""

import datetime
import json
import pathlib

from botocore.exceptions import ClientError

from .normalize import diff

META = "_recorded"


class Tally:
    """What happened across the whole run, for the summary line."""

    def __init__(self):
        self.verified = 0
        self.unverified = 0
        self.recorded = 0
        self.mismatched = 0
        self.deviations = []  # (key, reason), for the differences that are on purpose


class Store:
    """One JSON file per test module, loaded once and written back whole."""

    def __init__(self, root):
        self.root = pathlib.Path(root)
        self._files = {}
        self._dirty = set()

    def _path(self, module):
        return self.root / f"{module}.json"

    def load(self, module):
        if module not in self._files:
            p = self._path(module)
            self._files[module] = json.loads(p.read_text()) if p.exists() else {}
        return self._files[module]

    def recording(self, module, test):
        """The labels recorded for a test, or None — also None when the file
        was not recorded against real AWS."""
        data = self.load(module)
        if data.get(META, {}).get("target") != "aws":
            return None
        return data.get(test)

    def put(self, module, test, labels, meta):
        data = self.load(module)
        data[META] = meta
        data[test] = labels
        self._dirty.add(module)

    def flush(self):
        for module in self._dirty:
            self.root.mkdir(parents=True, exist_ok=True)
            self._path(module).write_text(
                json.dumps(self._files[module], indent=2, sort_keys=True) + "\n"
            )
        self._dirty.clear()


class Snapshot:
    """The per-test handle a scenario holds."""

    def __init__(self, store, normalizer, module, test, record, target, tally, versions,
                 deviations=None):
        self.store, self.norm = store, normalizer
        self.deviations = deviations or {}
        self.module, self.test = module, test
        self.record, self.target = record, target
        self.tally, self.versions = tally, versions
        self.captured = {}
        self.mismatches = []
        self.expected = None if record else store.recording(module, test)
        normalizer.reset()

    def match(self, label, response, *, opaque=(), drop=(), unordered=()):
        """Hold one response against the recording. Returns it untouched."""
        if label in self.captured:
            raise ValueError(f"snapshot label {label!r} used twice in {self.test}")
        got = self.norm.value(response, opaque=opaque, drop=drop, unordered=unordered)
        self.captured[label] = got
        if self.record:
            self.tally.recorded += 1
        elif self.expected is None or label not in self.expected:
            self.tally.unverified += 1
        else:
            found = diff(self.expected[label], got)
            if found:
                self.tally.mismatched += 1
                self.mismatches.append((label, found))
            else:
                self.tally.verified += 1
        return response

    def error(self, label, fn, **kw):
        """Call fn, which must be refused, and hold the refusal against the
        recording: the code, the message and the status."""
        try:
            fn()
        except ClientError as e:
            self.match(label, _refusal(e), **kw)
            return e
        # Noted, not raised: the refusals after this one are still worth
        # making, and one permissive call should not hide the next.
        self.mismatches.append((label, [("$", "<a refusal>", "<the call succeeded>")]))
        return None

    def outcome(self, label, fn, **kw):
        """Call fn and hold whatever happened — an answer or a refusal — against
        the recording. For the calls where which one AWS gives is the question."""
        try:
            return self.match(label, fn(), **kw)
        except ClientError as e:
            self.match(label, _refusal(e), **kw)
            return e

    def finish(self):
        """Write the recording, or raise what did not match."""
        if self.record:
            if self.mismatches:
                # Recording, and a call the scenario says is refused was not.
                # The scenario is wrong about AWS; do not write that down.
                raise AssertionError(render(self.mismatches))
            self.store.put(self.module, self.test, self.captured, {
                "target": self.target,
                "on": datetime.date.today().isoformat(),
                **self.versions,
            })
            return
        if self.expected is not None:
            for label in sorted(set(self.expected) - set(self.captured)):
                self.mismatches.append((label, [("$", "<recorded>", "<never reached>")]))
        self._apply_deviations()
        if self.mismatches:
            raise AssertionError(render(self.mismatches))


    def _apply_deviations(self):
        """Take the differences that are listed as deliberate out of the
        failures, and turn a listing that no longer differs into one."""
        prefix = f"{self.module}::{self.test}::"
        listed = {k[len(prefix):]: why for k, why in self.deviations.items() if k.startswith(prefix)}
        if not listed:
            return
        differed = {label for label, _ in self.mismatches}
        self.mismatches = [(label, found) for label, found in self.mismatches if label not in listed]
        for label, why in sorted(listed.items()):
            if label in differed:
                self.tally.deviations.append((prefix + label, why))
            else:
                self.mismatches.append((label, [(
                    "$", "<listed in deviations.py>", "<no longer differs: remove the entry>")]))


def _refusal(e):
    err = e.response.get("Error", {})
    return {
        "Error": {"Code": err.get("Code"), "Message": err.get("Message")},
        "ResponseMetadata": e.response.get("ResponseMetadata", {}),
    }


def render(mismatches):
    lines = ["this target did not answer the way AWS does:"]
    for label, found in mismatches:
        lines.append(f"  [{label}]")
        for path, aws, doze in found:
            lines.append(f"    {path}")
            lines.append(f"      aws:  {json.dumps(aws, sort_keys=True, default=str)}")
            lines.append(f"      doze: {json.dumps(doze, sort_keys=True, default=str)}")
    return "\n".join(lines)
