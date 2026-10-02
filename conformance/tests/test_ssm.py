"""SSM Parameter Store through boto3."""


def test_parameter_lifecycle_and_versions(client, names, cleanup, snapshot):
    ssm = client("ssm")
    name = f"/{names('param')}/db/host"
    cleanup(ssm.delete_parameter, Name=name)

    snapshot.match("put", ssm.put_parameter(Name=name, Value="one", Type="String"))
    snapshot.match("get", ssm.get_parameter(Name=name))
    snapshot.error("put-without-overwrite", lambda: ssm.put_parameter(
        Name=name, Value="two", Type="String"))
    snapshot.match("overwrite", ssm.put_parameter(Name=name, Value="two", Overwrite=True))
    snapshot.match("get-v2", ssm.get_parameter(Name=name))
    snapshot.match("get-by-version", ssm.get_parameter(Name=f"{name}:1"))

    snapshot.match("label", ssm.label_parameter_version(
        Name=name, ParameterVersion=1, Labels=["stable"]))
    snapshot.match("get-by-label", ssm.get_parameter(Name=f"{name}:stable"))
    snapshot.match("history", ssm.get_parameter_history(Name=name), opaque=("LastModifiedUser",))
    snapshot.match("describe", ssm.describe_parameters(
        ParameterFilters=[{"Key": "Name", "Values": [name]}]), opaque=("LastModifiedUser",))

    snapshot.match("delete", ssm.delete_parameter(Name=name))
    snapshot.error("get-deleted", lambda: ssm.get_parameter(Name=name))


def test_secure_string_is_ciphertext_until_asked(client, names, cleanup, snapshot):
    ssm = client("ssm")
    name = f"/{names('param')}/password"
    cleanup(ssm.delete_parameter, Name=name)

    snapshot.match("put", ssm.put_parameter(Name=name, Value="hunter2", Type="SecureString"))
    sealed = ssm.get_parameter(Name=name)
    assert sealed["Parameter"]["Value"] != "hunter2", "a SecureString came back in the clear"
    snapshot.match("sealed", sealed, opaque=("Value",))
    snapshot.match("opened", ssm.get_parameter(Name=name, WithDecryption=True))


def test_paths_and_bulk_reads(client, names, cleanup, snapshot):
    ssm = client("ssm")
    root = f"/{names('param')}"
    for leaf in ("a", "b", "nested/c"):
        ssm.put_parameter(Name=f"{root}/{leaf}", Value=leaf, Type="String")
        cleanup(ssm.delete_parameter, Name=f"{root}/{leaf}")

    snapshot.match("one-level", ssm.get_parameters_by_path(Path=root), unordered=("Parameters",))
    snapshot.match("recursive", ssm.get_parameters_by_path(Path=root, Recursive=True),
                   unordered=("Parameters",))
    # Which two come first is not promised; that there are two and more is.
    page = ssm.get_parameters_by_path(Path=root, Recursive=True, MaxResults=2)
    snapshot.match("page", {"count": len(page["Parameters"]), "more": "NextToken" in page})
    snapshot.match("some-missing", ssm.get_parameters(
        Names=[f"{root}/a", f"{root}/absent"]))
    snapshot.match("delete-many", ssm.delete_parameters(
        Names=[f"{root}/a", f"{root}/absent"]))


def test_refusals(client, names, cleanup, snapshot):
    ssm = client("ssm")
    root = f"/{names('param')}"
    snapshot.error("get-absent", lambda: ssm.get_parameter(Name=f"{root}/absent"))
    snapshot.error("delete-absent", lambda: ssm.delete_parameter(Name=f"{root}/absent"))
    # Each refusal has its own name, so one that is wrongly accepted leaves
    # nothing behind for the next to trip over.
    for leaf in ("untyped", "mistyped"):
        cleanup(ssm.delete_parameter, Name=f"{root}/{leaf}")
    snapshot.error("no-type-on-create", lambda: ssm.put_parameter(
        Name=f"{root}/untyped", Value="v"))
    snapshot.error("bad-type", lambda: ssm.put_parameter(
        Name=f"{root}/mistyped", Value="v", Type="Sentence"))
    snapshot.error("reserved-prefix", lambda: ssm.put_parameter(
        Name="/aws/not-yours", Value="v", Type="String"))
    snapshot.error("reserved-word", lambda: ssm.put_parameter(
        Name="ssm-not-yours", Value="v", Type="String"))
    # A path whose first segment merely BEGINS with a reserved word. The
    # documentation does not say; whatever AWS does, do that.
    cleanup(ssm.delete_parameter, Name="/awsome-dzc/x")
    cleanup(ssm.delete_parameter, Name=f"{root}/aws-inside")
    snapshot.outcome("reserved-word-starts-a-segment", lambda: ssm.put_parameter(
        Name="/awsome-dzc/x", Value="v", Type="String"))
    snapshot.outcome("reserved-word-deeper-in-the-path", lambda: ssm.put_parameter(
        Name=f"{root}/aws-inside", Value="v", Type="String"))
    snapshot.error("bad-characters", lambda: ssm.put_parameter(
        Name=f"{root}/has space", Value="v", Type="String"))
    snapshot.error("path-without-slash", lambda: ssm.get_parameters_by_path(Path="no-leading-slash"))

    ssm.put_parameter(Name=f"{root}/x", Value="v", Type="String")
    cleanup(ssm.delete_parameter, Name=f"{root}/x")
    snapshot.error("absent-version", lambda: ssm.get_parameter(Name=f"{root}/x:9"))
    snapshot.error("label-absent-version", lambda: ssm.label_parameter_version(
        Name=f"{root}/x", ParameterVersion=9, Labels=["l"]))
    snapshot.outcome("change-type-on-overwrite", lambda: ssm.put_parameter(
        Name=f"{root}/x", Value="a,b", Type="StringList", Overwrite=True))
