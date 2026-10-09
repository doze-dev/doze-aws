import json
import os


def handler(event, context):
    return {"statusCode": 200, "body": json.dumps({"table": os.environ.get("TABLE")})}


def worker(event, context):
    return {"batch": len(event.get("Records", []))}
