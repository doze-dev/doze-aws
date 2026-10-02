"""Secrets Manager through boto3.

Cost on AWS: a secret is billed per month, prorated, and each one here lives
for seconds. Cleanup deletes without a recovery window.
"""

# The ARN ends in six random characters on both sides.
ARN = ("ARN",)


def secret(sm, names, cleanup, value="s3cret", label="secret"):
    name = names(label)
    created = sm.create_secret(Name=name, SecretString=value)
    cleanup(sm.delete_secret, SecretId=name, ForceDeleteWithoutRecovery=True)
    return name, created


def test_secret_lifecycle_and_stages(client, names, cleanup, snapshot):
    sm = client("secretsmanager")
    name, created = secret(sm, names, cleanup)
    snapshot.match("create", created, opaque=ARN)
    snapshot.match("get", sm.get_secret_value(SecretId=name), opaque=ARN)

    snapshot.match("put-new-value", sm.put_secret_value(SecretId=name, SecretString="rotated"),
                   opaque=ARN)
    snapshot.match("get-current", sm.get_secret_value(SecretId=name), opaque=ARN)
    snapshot.match("get-previous", sm.get_secret_value(SecretId=name, VersionStage="AWSPREVIOUS"),
                   opaque=ARN)
    snapshot.match("versions", sm.list_secret_version_ids(SecretId=name),
                   opaque=ARN, unordered=("Versions",))
    snapshot.match("describe", sm.describe_secret(SecretId=name), opaque=ARN)

    snapshot.match("update-description", sm.update_secret(SecretId=name, Description="for tests"),
                   opaque=ARN)
    snapshot.match("tag", sm.tag_resource(SecretId=name, Tags=[{"Key": "env", "Value": "dev"}]))
    snapshot.match("described-again", sm.describe_secret(SecretId=name), opaque=ARN)


def test_binary_secret(client, names, cleanup, snapshot):
    sm = client("secretsmanager")
    name = names("secret")
    snapshot.match("create", sm.create_secret(Name=name, SecretBinary=b"\x00\x01\xfe"), opaque=ARN)
    cleanup(sm.delete_secret, SecretId=name, ForceDeleteWithoutRecovery=True)
    snapshot.match("get", sm.get_secret_value(SecretId=name), opaque=ARN)


def test_deletion_window_and_restore(client, names, cleanup, snapshot):
    sm = client("secretsmanager")
    name, _ = secret(sm, names, cleanup)
    snapshot.match("delete", sm.delete_secret(SecretId=name, RecoveryWindowInDays=7), opaque=ARN)
    snapshot.error("get-while-deleted", lambda: sm.get_secret_value(SecretId=name))
    snapshot.error("put-while-deleted", lambda: sm.put_secret_value(
        SecretId=name, SecretString="x"))
    snapshot.match("describe-while-deleted", sm.describe_secret(SecretId=name), opaque=ARN)
    snapshot.match("restore", sm.restore_secret(SecretId=name), opaque=ARN)
    snapshot.match("get-after-restore", sm.get_secret_value(SecretId=name), opaque=ARN)


def test_refusals(client, names, cleanup, snapshot):
    sm = client("secretsmanager")
    absent = names("absent")
    snapshot.error("get-absent", lambda: sm.get_secret_value(SecretId=absent))
    snapshot.error("describe-absent", lambda: sm.describe_secret(SecretId=absent))
    snapshot.error("bad-name", lambda: sm.create_secret(Name="has space!", SecretString="x"))

    name, _ = secret(sm, names, cleanup)
    snapshot.error("create-twice", lambda: sm.create_secret(Name=name, SecretString="other"))
    snapshot.error("string-and-binary", lambda: sm.put_secret_value(
        SecretId=name, SecretString="x", SecretBinary=b"y"))
    snapshot.error("absent-stage", lambda: sm.get_secret_value(
        SecretId=name, VersionStage="NOSUCHSTAGE"))
    snapshot.error("absent-version", lambda: sm.get_secret_value(
        SecretId=name, VersionId="00000000-0000-4000-8000-000000000000"))
    snapshot.error("window-and-force", lambda: sm.delete_secret(
        SecretId=name, RecoveryWindowInDays=7, ForceDeleteWithoutRecovery=True))
    snapshot.error("window-too-short", lambda: sm.delete_secret(
        SecretId=name, RecoveryWindowInDays=3))
