"""STS through boto3. Small on purpose: who you are is the one thing a local
emulator and a real account are supposed to disagree about."""


def test_caller_identity_has_the_shape(client, snapshot):
    snapshot.match("identity", client("sts").get_caller_identity(), opaque=("UserId", "Arn"))


def test_refusals(client, snapshot, target):
    sts = client("sts")
    # Not a length: botocore checks lengths itself and never sends the call.
    # A pattern only the service can refuse.
    snapshot.error("bad-session-name", lambda: sts.assume_role(
        RoleArn=f"arn:aws:iam::{target.account}:role/does-not-matter",
        RoleSessionName="has spaces"))
