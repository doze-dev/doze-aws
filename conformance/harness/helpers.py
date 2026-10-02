"""What several scenarios need and none of them is about."""

import io
import json
import time
import zipfile


def zip_bytes(files):
    """A zip of {name: source}, the same bytes every time.

    The timestamp is fixed and the names are sorted, so CodeSha256 and CodeSize
    are properties of the source and can be compared between two clouds.
    """
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
        for name in sorted(files):
            info = zipfile.ZipInfo(name, date_time=(2020, 1, 1, 0, 0, 0))
            info.external_attr = 0o644 << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            z.writestr(info, files[name])
    return buf.getvalue()


def trust(service):
    return json.dumps({
        "Version": "2012-10-17",
        "Statement": [{"Effect": "Allow", "Principal": {"Service": f"{service}.amazonaws.com"},
                       "Action": "sts:AssumeRole"}],
    })


def make_role(iam, name, service, managed=(), inline=None, propagate=0):
    """A role `service` can assume. Returns (arn, undo) — undo takes it apart
    in the order IAM insists on."""
    arn = iam.create_role(RoleName=name, AssumeRolePolicyDocument=trust(service))["Role"]["Arn"]
    for policy in managed:
        iam.attach_role_policy(RoleName=name, PolicyArn=policy)
    if inline:
        iam.put_role_policy(RoleName=name, PolicyName="inline", PolicyDocument=json.dumps(inline))

    def undo():
        for policy in managed:
            iam.detach_role_policy(RoleName=name, PolicyArn=policy)
        if inline:
            iam.delete_role_policy(RoleName=name, PolicyName="inline")
        iam.delete_role(RoleName=name)

    # A new role is not assumable everywhere at once on AWS. Waiting here is
    # cheaper than every caller retrying "cannot be assumed".
    if propagate:
        time.sleep(propagate)
    return arn, undo
