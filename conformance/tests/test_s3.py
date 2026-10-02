"""S3 through boto3 — with the SDK's own defaults, which since botocore 1.36
means a CRC32 on every upload and checksum validation on every download."""

MB = 1024 * 1024

# The canonical user of the account: real on AWS, a constant here, and no part
# of what a bucket does.
OWNER = ("Owner", "Initiator")


def bucket(s3, names, cleanup, label="bucket"):
    name = names(label)
    s3.create_bucket(Bucket=name)

    def empty_and_delete():
        for page in s3.get_paginator("list_object_versions").paginate(Bucket=name):
            for v in page.get("Versions", []) + page.get("DeleteMarkers", []):
                s3.delete_object(Bucket=name, Key=v["Key"], VersionId=v["VersionId"])
        s3.delete_bucket(Bucket=name)

    cleanup(empty_and_delete)
    return name


def test_bucket_and_object_lifecycle(client, names, cleanup, snapshot):
    s3 = client("s3")
    name = names("bucket")
    snapshot.match("create-bucket", s3.create_bucket(Bucket=name))
    cleanup(s3.delete_bucket, Bucket=name)
    cleanup(s3.delete_object, Bucket=name, Key="greeting.txt")

    snapshot.match("location", s3.get_bucket_location(Bucket=name))
    snapshot.match("empty-listing", s3.list_objects_v2(Bucket=name))

    snapshot.match("put", s3.put_object(
        Bucket=name, Key="greeting.txt", Body=b"hello", ContentType="text/plain",
        Metadata={"origin": "conformance"}))
    snapshot.match("head", s3.head_object(Bucket=name, Key="greeting.txt"))
    snapshot.match("get", s3.get_object(Bucket=name, Key="greeting.txt"))
    snapshot.match("listing", s3.list_objects_v2(Bucket=name))

    snapshot.match("copy", s3.copy_object(
        Bucket=name, Key="copy.txt", CopySource={"Bucket": name, "Key": "greeting.txt"}))
    cleanup(s3.delete_object, Bucket=name, Key="copy.txt")

    snapshot.match("delete", s3.delete_object(Bucket=name, Key="greeting.txt"))
    snapshot.match("delete-again", s3.delete_object(Bucket=name, Key="greeting.txt"))


def test_ranged_reads(client, names, cleanup, snapshot):
    """A ranged read of an object that carries a checksum — which, with the
    SDK defaults, is every object boto3 uploaded. The SDK validates whatever
    checksum header comes back against the bytes it was actually sent."""
    s3 = client("s3")
    name = bucket(s3, names, cleanup)
    s3.put_object(Bucket=name, Key="k", Body=b"hello world")

    snapshot.match("middle", s3.get_object(Bucket=name, Key="k", Range="bytes=1-3"))
    snapshot.match("suffix", s3.get_object(Bucket=name, Key="k", Range="bytes=-5"))
    snapshot.match("open-ended", s3.get_object(Bucket=name, Key="k", Range="bytes=6-"))
    snapshot.match("whole-by-range", s3.get_object(Bucket=name, Key="k", Range="bytes=0-10"))
    snapshot.error("past-the-end", lambda: s3.get_object(Bucket=name, Key="k", Range="bytes=50-60"))


def test_listing_with_prefix_delimiter_and_pages(client, names, cleanup, snapshot):
    s3 = client("s3")
    name = bucket(s3, names, cleanup)
    for key in ("a/1", "a/2", "b/1", "b/c/1", "top"):
        s3.put_object(Bucket=name, Key=key, Body=key.encode())

    snapshot.match("folders", s3.list_objects_v2(Bucket=name, Delimiter="/"))
    snapshot.match("under-b", s3.list_objects_v2(Bucket=name, Prefix="b/", Delimiter="/"))
    first = snapshot.match("page-1", s3.list_objects_v2(Bucket=name, MaxKeys=2),
                           opaque=("NextContinuationToken",))
    snapshot.match("page-2", s3.list_objects_v2(
        Bucket=name, MaxKeys=2, ContinuationToken=first["NextContinuationToken"]),
        opaque=("NextContinuationToken", "ContinuationToken"))
    snapshot.match("start-after", s3.list_objects_v2(Bucket=name, StartAfter="b/1"))
    snapshot.match("v1-listing", s3.list_objects(Bucket=name, Delimiter="/"), drop=OWNER)


def test_versioning_and_delete_markers(client, names, cleanup, snapshot):
    s3 = client("s3")
    name = bucket(s3, names, cleanup)
    snapshot.match("unversioned", s3.get_bucket_versioning(Bucket=name))
    snapshot.match("enable", s3.put_bucket_versioning(
        Bucket=name, VersioningConfiguration={"Status": "Enabled"}))
    snapshot.match("enabled", s3.get_bucket_versioning(Bucket=name))

    v = ("VersionId",)
    one = snapshot.match("put-v1", s3.put_object(Bucket=name, Key="k", Body=b"one"), opaque=v)
    snapshot.match("put-v2", s3.put_object(Bucket=name, Key="k", Body=b"two"), opaque=v)
    snapshot.match("delete", s3.delete_object(Bucket=name, Key="k"), opaque=v)

    snapshot.error("get-latest-is-a-marker", lambda: s3.get_object(Bucket=name, Key="k"))
    snapshot.match("get-v1", s3.get_object(Bucket=name, Key="k", VersionId=one["VersionId"]),
                   opaque=v)
    snapshot.match("versions", s3.list_object_versions(Bucket=name), opaque=v, drop=OWNER)


def test_multipart_upload(client, names, cleanup, snapshot):
    s3 = client("s3")
    name = bucket(s3, names, cleanup)
    u = ("UploadId",)

    started = snapshot.match("create", s3.create_multipart_upload(Bucket=name, Key="big"), opaque=u)
    uid = started["UploadId"]
    # Every part but the last must be at least 5 MiB.
    p1 = snapshot.match("part-1", s3.upload_part(
        Bucket=name, Key="big", UploadId=uid, PartNumber=1, Body=b"a" * (5 * MB)))
    p2 = snapshot.match("part-2", s3.upload_part(
        Bucket=name, Key="big", UploadId=uid, PartNumber=2, Body=b"b" * 10))
    snapshot.match("parts", s3.list_parts(Bucket=name, Key="big", UploadId=uid),
                   opaque=u, drop=OWNER)
    snapshot.match("in-progress", s3.list_multipart_uploads(Bucket=name), opaque=u, drop=OWNER)

    snapshot.match("complete", s3.complete_multipart_upload(
        Bucket=name, Key="big", UploadId=uid, MultipartUpload={"Parts": [
            {"PartNumber": 1, "ETag": p1["ETag"]},
            {"PartNumber": 2, "ETag": p2["ETag"]},
        ]}))
    snapshot.match("head", s3.head_object(Bucket=name, Key="big"))
    snapshot.match("get", s3.get_object(Bucket=name, Key="big"))

    small = s3.create_multipart_upload(Bucket=name, Key="small")["UploadId"]
    a = s3.upload_part(Bucket=name, Key="small", UploadId=small, PartNumber=1, Body=b"tiny")
    b = s3.upload_part(Bucket=name, Key="small", UploadId=small, PartNumber=2, Body=b"tiny")
    snapshot.error("part-too-small", lambda: s3.complete_multipart_upload(
        Bucket=name, Key="small", UploadId=small, MultipartUpload={"Parts": [
            {"PartNumber": 1, "ETag": a["ETag"]}, {"PartNumber": 2, "ETag": b["ETag"]}]}),
        drop=("UploadId",))
    snapshot.match("abort", s3.abort_multipart_upload(Bucket=name, Key="small", UploadId=small))


def test_refusals(client, names, cleanup, snapshot):
    s3 = client("s3")
    absent = names("absent")
    snapshot.error("no-such-bucket", lambda: s3.list_objects_v2(Bucket=absent))
    snapshot.error("head-no-such-bucket", lambda: s3.head_bucket(Bucket=absent))
    snapshot.error("bad-bucket-name", lambda: s3.create_bucket(Bucket="Not_A_Bucket"))

    name = bucket(s3, names, cleanup)
    snapshot.error("no-such-key", lambda: s3.get_object(Bucket=name, Key="absent"))
    snapshot.error("head-no-such-key", lambda: s3.head_object(Bucket=name, Key="absent"))
    # Refused everywhere except us-east-1, where re-creating a bucket you own
    # succeeds. Whichever the recording region does, do that.
    snapshot.outcome("create-twice", lambda: s3.create_bucket(Bucket=name))

    s3.put_object(Bucket=name, Key="k", Body=b"x")
    snapshot.error("not-empty", lambda: s3.delete_bucket(Bucket=name))
    snapshot.error("precondition", lambda: s3.get_object(
        Bucket=name, Key="k", IfMatch='"0000"'))
    snapshot.error("no-such-upload", lambda: s3.upload_part(
        Bucket=name, Key="k", UploadId="absent", PartNumber=1, Body=b"x"))


def test_tagging_and_batch_delete(client, names, cleanup, snapshot):
    s3 = client("s3")
    name = bucket(s3, names, cleanup)
    for key in ("a", "b", "c"):
        s3.put_object(Bucket=name, Key=key, Body=b"x")

    tags = {"TagSet": [{"Key": "env", "Value": "dev"}, {"Key": "team", "Value": "a"}]}
    snapshot.match("tag-object", s3.put_object_tagging(Bucket=name, Key="a", Tagging=tags))
    snapshot.match("object-tags", s3.get_object_tagging(Bucket=name, Key="a"), unordered=("TagSet",))
    snapshot.match("head-counts-tags", s3.get_object(Bucket=name, Key="a"))
    snapshot.match("untag-object", s3.delete_object_tagging(Bucket=name, Key="a"))
    snapshot.match("no-object-tags", s3.get_object_tagging(Bucket=name, Key="a"))

    snapshot.error("no-bucket-tags", lambda: s3.get_bucket_tagging(Bucket=name))
    snapshot.match("tag-bucket", s3.put_bucket_tagging(Bucket=name, Tagging=tags))
    snapshot.match("bucket-tags", s3.get_bucket_tagging(Bucket=name), unordered=("TagSet",))

    snapshot.match("delete-many", s3.delete_objects(Bucket=name, Delete={
        "Objects": [{"Key": "a"}, {"Key": "b"}, {"Key": "never-existed"}]}),
        unordered=("Deleted",))
    snapshot.match("delete-quietly", s3.delete_objects(Bucket=name, Delete={
        "Objects": [{"Key": "c"}], "Quiet": True}))
    snapshot.match("empty", s3.list_objects_v2(Bucket=name))


def test_presigned_urls(client, names, cleanup, snapshot):
    """A URL boto3 signs and something that is not boto3 uses."""
    import urllib.error
    import urllib.request

    s3 = client("s3")
    name = bucket(s3, names, cleanup)
    s3.put_object(Bucket=name, Key="k", Body=b"signed read", ContentType="text/plain")

    def fetch(url, method="GET", data=None):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, data=data, method=method)) as r:
                return {"status": r.status, "body": r.read().decode()}
        except urllib.error.HTTPError as e:
            return {"status": e.code}

    get = s3.generate_presigned_url("get_object", Params={"Bucket": name, "Key": "k"}, ExpiresIn=300)
    snapshot.match("get", fetch(get))
    put = s3.generate_presigned_url("put_object", Params={"Bucket": name, "Key": "up"}, ExpiresIn=300)
    snapshot.match("put", fetch(put, method="PUT", data=b"signed write"))
    snapshot.match("what-the-put-wrote", s3.get_object(Bucket=name, Key="up"))
    absent = s3.generate_presigned_url("get_object", Params={"Bucket": name, "Key": "absent"})
    snapshot.match("get-absent", fetch(absent))


def test_bucket_configuration(client, names, cleanup, snapshot, target):
    import json

    s3 = client("s3")
    name = bucket(s3, names, cleanup)
    snapshot.error("no-cors", lambda: s3.get_bucket_cors(Bucket=name))
    snapshot.error("no-policy", lambda: s3.get_bucket_policy(Bucket=name))
    snapshot.error("no-lifecycle", lambda: s3.get_bucket_lifecycle_configuration(Bucket=name))
    snapshot.error("no-website", lambda: s3.get_bucket_website(Bucket=name))
    snapshot.match("default-encryption", s3.get_bucket_encryption(Bucket=name))
    snapshot.match("default-public-access-block", s3.get_public_access_block(Bucket=name))

    cors = {"CORSRules": [{"AllowedMethods": ["GET"], "AllowedOrigins": ["https://example.com"],
                           "AllowedHeaders": ["*"], "MaxAgeSeconds": 300}]}
    snapshot.match("put-cors", s3.put_bucket_cors(Bucket=name, CORSConfiguration=cors))
    snapshot.match("cors", s3.get_bucket_cors(Bucket=name))
    snapshot.match("delete-cors", s3.delete_bucket_cors(Bucket=name))

    # Not a public policy: a new bucket blocks those, here and on AWS.
    policy = {"Version": "2012-10-17", "Statement": [{
        "Sid": "own-account", "Effect": "Allow",
        "Principal": {"AWS": f"arn:aws:iam::{target.account}:root"},
        "Action": "s3:GetObject", "Resource": f"arn:aws:s3:::{name}/*"}]}
    snapshot.match("put-policy", s3.put_bucket_policy(Bucket=name, Policy=json.dumps(policy)))
    snapshot.match("policy", json.loads(s3.get_bucket_policy(Bucket=name)["Policy"]))
    snapshot.error("policy-for-another-bucket", lambda: s3.put_bucket_policy(
        Bucket=name, Policy=json.dumps({**policy, "Statement": [
            {**policy["Statement"][0], "Resource": "arn:aws:s3:::someone-elses-bucket/*"}]})))
    snapshot.error("policy-not-json", lambda: s3.put_bucket_policy(Bucket=name, Policy="{nope"))
    snapshot.match("delete-policy", s3.delete_bucket_policy(Bucket=name))

    lifecycle = {"Rules": [{"ID": "expire-tmp", "Status": "Enabled", "Filter": {"Prefix": "tmp/"},
                            "Expiration": {"Days": 7}}]}
    snapshot.match("put-lifecycle", s3.put_bucket_lifecycle_configuration(
        Bucket=name, LifecycleConfiguration=lifecycle))
    snapshot.match("lifecycle", s3.get_bucket_lifecycle_configuration(Bucket=name))


def test_copy_replaces_or_keeps_metadata(client, names, cleanup, snapshot):
    s3 = client("s3")
    name = bucket(s3, names, cleanup)
    s3.put_object(Bucket=name, Key="src", Body=b"x", ContentType="text/plain",
                  Metadata={"origin": "first"}, CacheControl="max-age=60")
    src = {"Bucket": name, "Key": "src"}

    s3.copy_object(Bucket=name, Key="kept", CopySource=src)
    snapshot.match("kept", s3.head_object(Bucket=name, Key="kept"))
    s3.copy_object(Bucket=name, Key="replaced", CopySource=src, MetadataDirective="REPLACE",
                   ContentType="application/json", Metadata={"origin": "second"})
    snapshot.match("replaced", s3.head_object(Bucket=name, Key="replaced"))
    snapshot.error("onto-itself-unchanged", lambda: s3.copy_object(
        Bucket=name, Key="src", CopySource=src))
    snapshot.error("absent-source", lambda: s3.copy_object(
        Bucket=name, Key="x", CopySource={"Bucket": name, "Key": "absent"}))
