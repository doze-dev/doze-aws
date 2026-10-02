"""KMS through boto3.

Cost on AWS: a key is $1 a month, prorated by the hour, and stops being billed
once it is scheduled for deletion — which every key here is, at the 7-day
minimum, when its test ends. A full run is a fraction of a cent, and leaves
keys in PendingDeletion for a week.
"""

# Ciphertext and generated key material: random by design.
SECRET = ("CiphertextBlob", "Plaintext", "Signature", "Mac")


def key(kms, cleanup, **kw):
    made = kms.create_key(**kw)
    cleanup(kms.schedule_key_deletion, KeyId=made["KeyMetadata"]["KeyId"], PendingWindowInDays=7)
    return made["KeyMetadata"]["KeyId"], made


def test_symmetric_key_encrypts_and_decrypts(client, cleanup, snapshot):
    kms = client("kms")
    kid, made = key(kms, cleanup, Description="conformance")
    snapshot.match("create", made)
    snapshot.match("describe", kms.describe_key(KeyId=kid))

    sealed = snapshot.match("encrypt", kms.encrypt(KeyId=kid, Plaintext=b"attack at dawn"),
                            opaque=SECRET)
    snapshot.match("decrypt", kms.decrypt(CiphertextBlob=sealed["CiphertextBlob"]))

    bound = kms.encrypt(KeyId=kid, Plaintext=b"x", EncryptionContext={"tenant": "a"})
    snapshot.match("decrypt-with-context", kms.decrypt(
        CiphertextBlob=bound["CiphertextBlob"], EncryptionContext={"tenant": "a"}))
    snapshot.error("decrypt-without-context", lambda: kms.decrypt(
        CiphertextBlob=bound["CiphertextBlob"]))
    snapshot.error("decrypt-wrong-context", lambda: kms.decrypt(
        CiphertextBlob=bound["CiphertextBlob"], EncryptionContext={"tenant": "b"}))

    dk = kms.generate_data_key(KeyId=kid, KeySpec="AES_256")
    assert len(dk["Plaintext"]) == 32
    snapshot.match("data-key", dk, opaque=SECRET)
    snapshot.match("data-key-opens", kms.decrypt(CiphertextBlob=dk["CiphertextBlob"]),
                   opaque=SECRET)
    snapshot.match("rotation-off", kms.get_key_rotation_status(KeyId=kid))
    snapshot.match("enable-rotation", kms.enable_key_rotation(KeyId=kid))
    snapshot.match("rotation-on", kms.get_key_rotation_status(KeyId=kid))


def test_aliases(client, names, cleanup, snapshot):
    kms = client("kms")
    kid, _ = key(kms, cleanup)
    alias = f"alias/{names('alias')}"
    snapshot.match("create", kms.create_alias(AliasName=alias, TargetKeyId=kid))
    cleanup(kms.delete_alias, AliasName=alias)

    snapshot.match("describe-through-alias", kms.describe_key(KeyId=alias))
    sealed = kms.encrypt(KeyId=alias, Plaintext=b"x")
    snapshot.match("encrypt-through-alias", sealed, opaque=SECRET)
    snapshot.match("list-for-key", kms.list_aliases(KeyId=kid))
    snapshot.error("create-twice", lambda: kms.create_alias(AliasName=alias, TargetKeyId=kid))
    snapshot.error("no-alias-prefix", lambda: kms.create_alias(
        AliasName=names("alias"), TargetKeyId=kid))
    snapshot.error("reserved-prefix", lambda: kms.create_alias(
        AliasName="alias/aws/mine", TargetKeyId=kid))
    snapshot.match("delete", kms.delete_alias(AliasName=alias))
    snapshot.error("delete-again", lambda: kms.delete_alias(AliasName=alias))


def test_key_states(client, cleanup, snapshot):
    kms = client("kms")
    kid, _ = key(kms, cleanup)
    snapshot.match("disable", kms.disable_key(KeyId=kid))
    snapshot.match("disabled", kms.describe_key(KeyId=kid))
    snapshot.error("encrypt-while-disabled", lambda: kms.encrypt(KeyId=kid, Plaintext=b"x"))
    snapshot.match("enable", kms.enable_key(KeyId=kid))

    snapshot.match("schedule", kms.schedule_key_deletion(KeyId=kid, PendingWindowInDays=7))
    snapshot.match("pending", kms.describe_key(KeyId=kid))
    snapshot.error("encrypt-while-pending", lambda: kms.encrypt(KeyId=kid, Plaintext=b"x"))
    snapshot.match("cancel", kms.cancel_key_deletion(KeyId=kid))
    snapshot.match("after-cancel", kms.describe_key(KeyId=kid))


def test_asymmetric_sign_and_hmac(client, cleanup, snapshot):
    kms = client("kms")
    signer, made = key(kms, cleanup, KeySpec="ECC_NIST_P256", KeyUsage="SIGN_VERIFY")
    snapshot.match("create-signer", made)
    sig = snapshot.match("sign", kms.sign(
        KeyId=signer, Message=b"msg", SigningAlgorithm="ECDSA_SHA_256"), opaque=SECRET)
    snapshot.match("verify", kms.verify(
        KeyId=signer, Message=b"msg", Signature=sig["Signature"], SigningAlgorithm="ECDSA_SHA_256"))
    snapshot.error("verify-tampered", lambda: kms.verify(
        KeyId=signer, Message=b"other", Signature=sig["Signature"],
        SigningAlgorithm="ECDSA_SHA_256"))
    snapshot.error("encrypt-with-a-signing-key", lambda: kms.encrypt(KeyId=signer, Plaintext=b"x"))
    snapshot.match("public-key", kms.get_public_key(KeyId=signer), opaque=("PublicKey",))

    mac_key, _ = key(kms, cleanup, KeySpec="HMAC_256", KeyUsage="GENERATE_VERIFY_MAC")
    mac = snapshot.match("mac", kms.generate_mac(
        KeyId=mac_key, Message=b"msg", MacAlgorithm="HMAC_SHA_256"), opaque=SECRET)
    snapshot.match("verify-mac", kms.verify_mac(
        KeyId=mac_key, Message=b"msg", Mac=mac["Mac"], MacAlgorithm="HMAC_SHA_256"))


def test_refusals(client, cleanup, snapshot):
    kms = client("kms")
    absent = "00000000-0000-4000-8000-000000000000"
    snapshot.error("describe-absent", lambda: kms.describe_key(KeyId=absent))
    snapshot.error("encrypt-absent", lambda: kms.encrypt(KeyId=absent, Plaintext=b"x"))
    snapshot.error("decrypt-garbage", lambda: kms.decrypt(CiphertextBlob=b"not ciphertext"))
    snapshot.error("absent-alias", lambda: kms.describe_key(KeyId="alias/dzc-never-made"))

    kid, _ = key(kms, cleanup)
    snapshot.error("window-too-short", lambda: kms.schedule_key_deletion(
        KeyId=kid, PendingWindowInDays=3))
    snapshot.error("sign-with-an-encryption-key", lambda: kms.sign(
        KeyId=kid, Message=b"m", SigningAlgorithm="ECDSA_SHA_256"))
    snapshot.error("bad-spec-for-usage", lambda: kms.create_key(
        KeySpec="HMAC_256", KeyUsage="ENCRYPT_DECRYPT"))
