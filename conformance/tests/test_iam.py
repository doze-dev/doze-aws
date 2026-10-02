"""IAM through boto3: the control plane a deploy tool walks."""

import json

TRUST = json.dumps({
    "Version": "2012-10-17",
    "Statement": [{"Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"},
                   "Action": "sts:AssumeRole"}],
})
READ_S3 = json.dumps({
    "Version": "2012-10-17",
    "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}],
})

# IAM's own ids (AROA…, AIDA…, ANPA…, AKIA…): unique, and not ours to predict.
IDS = ("RoleId", "UserId", "PolicyId", "AccessKeyId", "SecretAccessKey", "InstanceProfileId")


def test_role_with_inline_and_managed_policies(client, names, cleanup, snapshot):
    iam = client("iam")
    role = names("role")
    snapshot.match("create-role", iam.create_role(
        RoleName=role, AssumeRolePolicyDocument=TRUST, Description="conformance",
        Tags=[{"Key": "env", "Value": "dev"}]), opaque=IDS)
    cleanup(iam.delete_role, RoleName=role)
    snapshot.match("get-role", iam.get_role(RoleName=role), opaque=IDS, drop=("RoleLastUsed",))

    snapshot.match("put-inline", iam.put_role_policy(
        RoleName=role, PolicyName="inline", PolicyDocument=READ_S3))
    cleanup(iam.delete_role_policy, RoleName=role, PolicyName="inline")
    snapshot.match("get-inline", iam.get_role_policy(RoleName=role, PolicyName="inline"))
    snapshot.match("list-inline", iam.list_role_policies(RoleName=role))

    policy = names("policy")
    made = snapshot.match("create-policy", iam.create_policy(
        PolicyName=policy, PolicyDocument=READ_S3), opaque=IDS)
    arn = made["Policy"]["Arn"]
    cleanup(iam.delete_policy, PolicyArn=arn)
    snapshot.match("attach", iam.attach_role_policy(RoleName=role, PolicyArn=arn))
    cleanup(iam.detach_role_policy, RoleName=role, PolicyArn=arn)
    snapshot.match("attached", iam.list_attached_role_policies(RoleName=role))
    snapshot.match("policy-after-attach", iam.get_policy(PolicyArn=arn), opaque=IDS)
    snapshot.match("policy-version", iam.get_policy_version(PolicyArn=arn, VersionId="v1"))

    snapshot.error("delete-role-in-use", lambda: iam.delete_role(RoleName=role))
    snapshot.error("delete-attached-policy", lambda: iam.delete_policy(PolicyArn=arn))
    snapshot.match("update-trust", iam.update_assume_role_policy(
        RoleName=role, PolicyDocument=TRUST.replace("lambda", "states")))
    snapshot.match("detach", iam.detach_role_policy(RoleName=role, PolicyArn=arn))
    snapshot.error("detach-again", lambda: iam.detach_role_policy(RoleName=role, PolicyArn=arn))


def test_policy_versions(client, names, cleanup, snapshot):
    iam = client("iam")
    made = iam.create_policy(PolicyName=names("policy"), PolicyDocument=READ_S3)
    arn = made["Policy"]["Arn"]
    cleanup(iam.delete_policy, PolicyArn=arn)

    snapshot.match("create-v2", iam.create_policy_version(
        PolicyArn=arn, PolicyDocument=READ_S3.replace("GetObject", "PutObject"),
        SetAsDefault=True))
    cleanup(iam.delete_policy_version, PolicyArn=arn, VersionId="v1")
    snapshot.match("versions", iam.list_policy_versions(PolicyArn=arn))
    snapshot.error("delete-the-default", lambda: iam.delete_policy_version(
        PolicyArn=arn, VersionId="v2"))
    snapshot.match("set-default-back", iam.set_default_policy_version(
        PolicyArn=arn, VersionId="v1"))
    snapshot.match("delete-v2", iam.delete_policy_version(PolicyArn=arn, VersionId="v2"))


def test_user_group_and_access_key(client, names, cleanup, snapshot):
    iam = client("iam")
    user, group = names("user"), names("group")
    snapshot.match("create-user", iam.create_user(UserName=user, Path="/apps/"), opaque=IDS)
    cleanup(iam.delete_user, UserName=user)
    snapshot.match("create-group", iam.create_group(GroupName=group), opaque=("GroupId",))
    cleanup(iam.delete_group, GroupName=group)

    snapshot.match("add-to-group", iam.add_user_to_group(GroupName=group, UserName=user))
    cleanup(iam.remove_user_from_group, GroupName=group, UserName=user)
    snapshot.match("groups-for-user", iam.list_groups_for_user(UserName=user),
                   opaque=("GroupId",))

    made = snapshot.match("create-key", iam.create_access_key(UserName=user), opaque=IDS)
    kid = made["AccessKey"]["AccessKeyId"]
    cleanup(iam.delete_access_key, UserName=user, AccessKeyId=kid)
    snapshot.match("list-keys", iam.list_access_keys(UserName=user), opaque=IDS)
    snapshot.match("deactivate", iam.update_access_key(
        UserName=user, AccessKeyId=kid, Status="Inactive"))
    snapshot.match("listed-inactive", iam.list_access_keys(UserName=user), opaque=IDS)
    snapshot.error("delete-user-with-a-key", lambda: iam.delete_user(UserName=user))


def test_instance_profile(client, names, cleanup, snapshot):
    iam = client("iam")
    role, profile = names("role"), names("profile")
    iam.create_role(RoleName=role, AssumeRolePolicyDocument=TRUST.replace("lambda", "ec2"))
    cleanup(iam.delete_role, RoleName=role)
    snapshot.match("create", iam.create_instance_profile(InstanceProfileName=profile), opaque=IDS)
    cleanup(iam.delete_instance_profile, InstanceProfileName=profile)
    snapshot.match("add-role", iam.add_role_to_instance_profile(
        InstanceProfileName=profile, RoleName=role))
    cleanup(iam.remove_role_from_instance_profile, InstanceProfileName=profile, RoleName=role)
    snapshot.match("get", iam.get_instance_profile(InstanceProfileName=profile), opaque=IDS)
    # A profile holds one role. Adding the SAME one again is either that
    # limit or a no-op, and which is AWS's to say.
    snapshot.outcome("same-role-again", lambda: iam.add_role_to_instance_profile(
        InstanceProfileName=profile, RoleName=role))
    other = names("role-2")
    iam.create_role(RoleName=other, AssumeRolePolicyDocument=TRUST.replace("lambda", "ec2"))
    cleanup(iam.delete_role, RoleName=other)
    snapshot.error("second-role", lambda: iam.add_role_to_instance_profile(
        InstanceProfileName=profile, RoleName=other))


def test_refusals(client, names, cleanup, snapshot, target):
    iam = client("iam")
    absent = names("absent")
    snapshot.error("get-absent-role", lambda: iam.get_role(RoleName=absent))
    snapshot.error("get-absent-user", lambda: iam.get_user(UserName=absent))
    snapshot.error("get-absent-policy", lambda: iam.get_policy(
        PolicyArn=f"arn:aws:iam::{target.account}:policy/{absent}"))
    snapshot.error("bad-role-name", lambda: iam.create_role(
        RoleName="has space", AssumeRolePolicyDocument=TRUST))
    snapshot.error("trust-not-json", lambda: iam.create_role(
        RoleName=absent, AssumeRolePolicyDocument="{not json"))
    snapshot.error("trust-without-principal", lambda: iam.create_role(
        RoleName=absent, AssumeRolePolicyDocument=READ_S3))
    snapshot.error("policy-not-json", lambda: iam.create_policy(
        PolicyName=absent, PolicyDocument="{not json"))
    snapshot.error("attach-absent-policy", lambda: iam.attach_role_policy(
        RoleName=absent, PolicyArn=f"arn:aws:iam::{target.account}:policy/{absent}"))

    role = names("role")
    iam.create_role(RoleName=role, AssumeRolePolicyDocument=TRUST)
    cleanup(iam.delete_role, RoleName=role)
    snapshot.error("create-role-twice", lambda: iam.create_role(
        RoleName=role, AssumeRolePolicyDocument=TRUST))
    snapshot.error("get-absent-inline", lambda: iam.get_role_policy(
        RoleName=role, PolicyName="absent"))
    snapshot.error("inline-with-a-principal", lambda: iam.put_role_policy(
        RoleName=role, PolicyName="p", PolicyDocument=TRUST))
