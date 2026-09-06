# The Python fixture: a real handler on the host interpreter, run through
# doze-aws's embedded runtime client. It prints, and it returns what it
# was given plus what the context says.
import json

print("python fixture init")


def handler(event, context):
    print("py handled", json.dumps(event))
    return {
        "echoed": event,
        "requestId": context.aws_request_id,
        "function": context.function_name,
        "remaining_ok": context.get_remaining_time_in_millis() > 0,
    }
