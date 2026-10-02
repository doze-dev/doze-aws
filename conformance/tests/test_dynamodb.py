"""DynamoDB through boto3's low-level client."""

FAST = {"Delay": 1, "MaxAttempts": 120}

# How much a table holds is updated every six hours on AWS.
WEATHER = ("ItemCount", "TableSizeBytes")


def table(ddb, names, cleanup, sort_key=False):
    name = names("table")
    schema = [{"AttributeName": "pk", "KeyType": "HASH"}]
    attrs = [{"AttributeName": "pk", "AttributeType": "S"}]
    if sort_key:
        schema.append({"AttributeName": "sk", "KeyType": "RANGE"})
        attrs.append({"AttributeName": "sk", "AttributeType": "N"})
    created = ddb.create_table(TableName=name, KeySchema=schema,
                               AttributeDefinitions=attrs, BillingMode="PAY_PER_REQUEST")
    cleanup(ddb.delete_table, TableName=name)
    ddb.get_waiter("table_exists").wait(TableName=name, WaiterConfig=FAST)
    return name, created


def test_table_lifecycle(client, names, cleanup, snapshot):
    ddb = client("dynamodb")
    name, created = table(ddb, names, cleanup)
    # TableStatus is CREATING on AWS at this instant and may be ACTIVE here;
    # how long a table takes is not the contract, what it settles into is.
    snapshot.match("create", created, drop=WEATHER + ("TableStatus",))
    snapshot.match("describe", ddb.describe_table(TableName=name), drop=WEATHER)
    snapshot.match("ttl", ddb.describe_time_to_live(TableName=name))
    snapshot.match("delete", ddb.delete_table(TableName=name), drop=WEATHER + ("TableStatus",))
    ddb.get_waiter("table_not_exists").wait(TableName=name, WaiterConfig=FAST)
    snapshot.error("describe-deleted", lambda: ddb.describe_table(TableName=name))


def test_item_round_trip_and_every_type(client, names, cleanup, snapshot):
    ddb = client("dynamodb")
    name, _ = table(ddb, names, cleanup)
    item = {
        "pk": {"S": "a"},
        "n": {"N": "1.50"},
        "b": {"B": b"\x00\xff"},
        "flag": {"BOOL": True},
        "nothing": {"NULL": True},
        "list": {"L": [{"S": "x"}, {"N": "2"}]},
        "map": {"M": {"inner": {"S": "y"}}},
        "ss": {"SS": ["b", "a"]},
        "ns": {"NS": ["2", "1"]},
    }
    snapshot.match("put", ddb.put_item(TableName=name, Item=item))
    snapshot.match("get", ddb.get_item(TableName=name, Key={"pk": {"S": "a"}}, ConsistentRead=True),
                   unordered=("SS", "NS"))
    snapshot.match("get-absent", ddb.get_item(TableName=name, Key={"pk": {"S": "zz"}}))
    snapshot.match("put-returning-old", ddb.put_item(
        TableName=name, Item={"pk": {"S": "a"}, "n": {"N": "2"}}, ReturnValues="ALL_OLD"),
        unordered=("SS", "NS"))

    snapshot.match("update", ddb.update_item(
        TableName=name, Key={"pk": {"S": "a"}},
        UpdateExpression="SET #n = #n + :one, tags = :t ADD seen :s REMOVE gone",
        ExpressionAttributeNames={"#n": "n"},
        ExpressionAttributeValues={":one": {"N": "1"}, ":t": {"L": []}, ":s": {"SS": ["x"]}},
        ReturnValues="ALL_NEW"))
    snapshot.match("update-updated-old", ddb.update_item(
        TableName=name, Key={"pk": {"S": "a"}},
        UpdateExpression="SET n = :v", ExpressionAttributeValues={":v": {"N": "9"}},
        ReturnValues="UPDATED_OLD"))
    snapshot.match("delete", ddb.delete_item(
        TableName=name, Key={"pk": {"S": "a"}}, ReturnValues="ALL_OLD"))


def test_query_and_scan(client, names, cleanup, snapshot):
    ddb = client("dynamodb")
    name, _ = table(ddb, names, cleanup, sort_key=True)
    for sk in range(1, 6):
        ddb.put_item(TableName=name, Item={
            "pk": {"S": "a"}, "sk": {"N": str(sk)}, "even": {"BOOL": sk % 2 == 0}})
    ddb.put_item(TableName=name, Item={"pk": {"S": "b"}, "sk": {"N": "1"}})

    q = dict(TableName=name, KeyConditionExpression="pk = :p",
             ExpressionAttributeValues={":p": {"S": "a"}}, ConsistentRead=True)
    snapshot.match("all", ddb.query(**q))
    snapshot.match("descending", ddb.query(**q, ScanIndexForward=False, Limit=2))
    page = snapshot.match("page-1", ddb.query(**q, Limit=2))
    snapshot.match("page-2", ddb.query(**q, Limit=2, ExclusiveStartKey=page["LastEvaluatedKey"]))
    snapshot.match("between", ddb.query(
        TableName=name, KeyConditionExpression="pk = :p AND sk BETWEEN :lo AND :hi",
        ExpressionAttributeValues={":p": {"S": "a"}, ":lo": {"N": "2"}, ":hi": {"N": "4"}},
        ConsistentRead=True))
    # A filter is applied after the limit, so Count and ScannedCount part ways.
    snapshot.match("filtered", ddb.query(
        TableName=name, KeyConditionExpression="pk = :p", FilterExpression="even = :t",
        ExpressionAttributeValues={":p": {"S": "a"}, ":t": {"BOOL": True}},
        Limit=3, ConsistentRead=True))
    snapshot.match("projection", ddb.query(**q, ProjectionExpression="sk", Limit=1))
    snapshot.match("count", ddb.query(**q, Select="COUNT"))
    snapshot.match("scan", ddb.scan(TableName=name, ConsistentRead=True), unordered=("Items",))


def test_conditions_and_transactions(client, names, cleanup, snapshot):
    ddb = client("dynamodb")
    name, _ = table(ddb, names, cleanup)
    ddb.put_item(TableName=name, Item={"pk": {"S": "a"}, "n": {"N": "1"}})

    snapshot.error("condition-fails", lambda: ddb.put_item(
        TableName=name, Item={"pk": {"S": "a"}},
        ConditionExpression="attribute_not_exists(pk)"))
    snapshot.error("condition-fails-returning-old", lambda: ddb.put_item(
        TableName=name, Item={"pk": {"S": "a"}},
        ConditionExpression="attribute_not_exists(pk)",
        ReturnValuesOnConditionCheckFailure="ALL_OLD"))

    snapshot.match("transact", ddb.transact_write_items(TransactItems=[
        {"Put": {"TableName": name, "Item": {"pk": {"S": "b"}}}},
        {"Update": {"TableName": name, "Key": {"pk": {"S": "a"}},
                    "UpdateExpression": "SET n = :v",
                    "ExpressionAttributeValues": {":v": {"N": "2"}}}},
    ]))
    snapshot.error("transact-cancelled", lambda: ddb.transact_write_items(TransactItems=[
        {"Put": {"TableName": name, "Item": {"pk": {"S": "c"}}}},
        {"ConditionCheck": {"TableName": name, "Key": {"pk": {"S": "a"}},
                            "ConditionExpression": "n = :v",
                            "ExpressionAttributeValues": {":v": {"N": "999"}}}},
    ]))
    snapshot.match("nothing-was-written",
                   ddb.get_item(TableName=name, Key={"pk": {"S": "c"}}, ConsistentRead=True))
    snapshot.match("batch-get", ddb.batch_get_item(RequestItems={name: {
        "Keys": [{"pk": {"S": "a"}}, {"pk": {"S": "b"}}, {"pk": {"S": "zz"}}],
        "ConsistentRead": True}}), unordered=(names("table"),))


def test_refusals(client, names, cleanup, snapshot):
    ddb = client("dynamodb")
    snapshot.error("no-such-table", lambda: ddb.get_item(
        TableName=names("absent"), Key={"pk": {"S": "a"}}))

    name, _ = table(ddb, names, cleanup)
    snapshot.error("table-exists", lambda: ddb.create_table(
        TableName=name, KeySchema=[{"AttributeName": "pk", "KeyType": "HASH"}],
        AttributeDefinitions=[{"AttributeName": "pk", "AttributeType": "S"}],
        BillingMode="PAY_PER_REQUEST"))
    snapshot.error("missing-key", lambda: ddb.put_item(TableName=name, Item={"other": {"S": "x"}}))
    snapshot.error("wrong-key-type", lambda: ddb.put_item(TableName=name, Item={"pk": {"N": "1"}}))
    snapshot.error("bad-expression", lambda: ddb.update_item(
        TableName=name, Key={"pk": {"S": "a"}}, UpdateExpression="SET"))
    snapshot.error("undefined-value", lambda: ddb.update_item(
        TableName=name, Key={"pk": {"S": "a"}}, UpdateExpression="SET n = :missing"))
    snapshot.error("unused-value", lambda: ddb.update_item(
        TableName=name, Key={"pk": {"S": "a"}}, UpdateExpression="SET n = :v",
        ExpressionAttributeValues={":v": {"N": "1"}, ":unused": {"N": "2"}}))
    snapshot.error("reserved-word", lambda: ddb.update_item(
        TableName=name, Key={"pk": {"S": "a"}}, UpdateExpression="SET name = :v",
        ExpressionAttributeValues={":v": {"S": "x"}}))
    snapshot.error("update-the-key", lambda: ddb.update_item(
        TableName=name, Key={"pk": {"S": "a"}}, UpdateExpression="SET pk = :v",
        ExpressionAttributeValues={":v": {"S": "b"}}))
    snapshot.error("empty-set", lambda: ddb.put_item(
        TableName=name, Item={"pk": {"S": "a"}, "s": {"SS": []}}))


def test_global_secondary_index(client, names, cleanup, snapshot, eventually):
    ddb = client("dynamodb")
    name = names("table")
    created = ddb.create_table(
        TableName=name, BillingMode="PAY_PER_REQUEST",
        KeySchema=[{"AttributeName": "pk", "KeyType": "HASH"}],
        AttributeDefinitions=[{"AttributeName": "pk", "AttributeType": "S"},
                              {"AttributeName": "owner", "AttributeType": "S"},
                              {"AttributeName": "at", "AttributeType": "N"}],
        GlobalSecondaryIndexes=[{
            "IndexName": "by-owner",
            "KeySchema": [{"AttributeName": "owner", "KeyType": "HASH"},
                          {"AttributeName": "at", "KeyType": "RANGE"}],
            "Projection": {"ProjectionType": "INCLUDE", "NonKeyAttributes": ["title"]},
        }])
    cleanup(ddb.delete_table, TableName=name)
    ddb.get_waiter("table_exists").wait(TableName=name, WaiterConfig=FAST)
    status = ("TableStatus", "IndexStatus")
    snapshot.match("create", created, drop=WEATHER + status)
    snapshot.match("describe", ddb.describe_table(TableName=name), drop=WEATHER + status)

    for i, owner in enumerate(["ada", "ada", "bob"]):
        ddb.put_item(TableName=name, Item={
            "pk": {"S": f"doc-{i}"}, "owner": {"S": owner}, "at": {"N": str(i)},
            "title": {"S": f"t{i}"}, "body": {"S": "not projected"}})
    ddb.put_item(TableName=name, Item={"pk": {"S": "no-owner"}})  # sparse: absent from the index

    q = dict(TableName=name, IndexName="by-owner", KeyConditionExpression="#o = :o",
             ExpressionAttributeNames={"#o": "owner"}, ExpressionAttributeValues={":o": {"S": "ada"}})

    def indexed():
        r = ddb.query(**q)
        assert r["Count"] == 2, f"{r['Count']} of 2 in the index so far"
        return r

    snapshot.match("query-index", eventually(indexed))
    snapshot.match("newest-first", ddb.query(**q, ScanIndexForward=False, Limit=1))
    snapshot.match("scan-index-is-sparse", ddb.scan(TableName=name, IndexName="by-owner", Select="COUNT"))
    snapshot.error("consistent-read-on-a-gsi", lambda: ddb.query(**q, ConsistentRead=True))
    snapshot.error("absent-index", lambda: ddb.query(**{**q, "IndexName": "absent"}))
    snapshot.error("all-attributes-not-projected", lambda: ddb.query(**q, Select="ALL_ATTRIBUTES"))
    # "owner" unaliased: a reserved word, and the reason for the #o above.
    snapshot.error("reserved-word-in-key-condition", lambda: ddb.query(
        TableName=name, IndexName="by-owner", KeyConditionExpression="owner = :o",
        ExpressionAttributeValues={":o": {"S": "ada"}}))


def test_batch_write_and_ttl(client, names, cleanup, snapshot):
    ddb = client("dynamodb")
    name, _ = table(ddb, names, cleanup)
    put = lambda k: {"PutRequest": {"Item": {"pk": {"S": k}}}}
    snapshot.match("write", ddb.batch_write_item(RequestItems={name: [put("a"), put("b"), put("c")]}))
    snapshot.match("delete-one", ddb.batch_write_item(RequestItems={name: [
        {"DeleteRequest": {"Key": {"pk": {"S": "a"}}}}, put("d")]}))
    snapshot.match("count", ddb.scan(TableName=name, Select="COUNT", ConsistentRead=True))
    snapshot.error("same-key-twice", lambda: ddb.batch_write_item(
        RequestItems={name: [put("z"), put("z")]}))
    snapshot.error("twenty-six", lambda: ddb.batch_write_item(
        RequestItems={name: [put(f"k{i}") for i in range(26)]}))

    spec = {"Enabled": True, "AttributeName": "expires"}
    snapshot.match("enable-ttl", ddb.update_time_to_live(TableName=name, TimeToLiveSpecification=spec))
    snapshot.error("enable-ttl-again", lambda: ddb.update_time_to_live(
        TableName=name, TimeToLiveSpecification=spec))
