package sqs

// apiError and its constructors: SQS-shaped errors carrying the AWS error
// code and HTTP status.

import "github.com/doze-dev/doze-aws/internal/awshttp"

// apiError is the shared AWS API error type (code maps to HTTP status + AWS
// error code); the protocol codecs in internal/awsquery and internal/awsjson
// render it onto the wire.
type apiError = awshttp.APIError

func errQueueMissing(name string) *apiError {
	return &apiError{Code: "AWS.SimpleQueueService.NonExistentQueue", Status: 400, Message: "The specified queue does not exist: " + name, SenderFault: true}
}
func errInvalid(msg string) *apiError {
	return &apiError{Code: "InvalidParameterValue", Status: 400, Message: msg, SenderFault: true}
}

// errInvalidAttrValue is the attribute-specific refusal: a value that parses
// but falls outside the range SQS accepts. Distinct from errInvalid because
// SQS answers a well-formed-but-out-of-range attribute with its own code, and
// an SDK branching on that code should see the same thing here as in AWS.
func errInvalidAttrValue(attr, msg string) *apiError {
	return &apiError{
		Code: "InvalidAttributeValue", Status: 400, SenderFault: true,
		Message: "Value for parameter " + attr + " is invalid. Reason: " + msg + ".",
	}
}
