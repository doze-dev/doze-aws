# The fixture function that raises, for the boards that draw "it threw": a
# KeyError for a field the event does not have, which is how a real handler
# fails. Lambda answers 200 and flags it in X-Amz-Function-Error; the console
# shows the answer as "it threw". (Python is the subject under test here, as
# in lambda-handler-py: the function runs on the host interpreter.)


def handler(event, context):
    line = {"sku": "AMB-COF-227"}
    return {"price": line["unit_price"]}
