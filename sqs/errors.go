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
