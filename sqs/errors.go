package sqs

// apiError and its constructors: SQS-shaped errors carrying the AWS error
// code and HTTP status.

import (
	"regexp"
	"strconv"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// apiError is the shared AWS API error type (code maps to HTTP status + AWS
// error code); the protocol codecs in internal/awsquery and internal/awsjson
// render it onto the wire.
type apiError = awshttp.APIError

func errQueueMissing(name string) *apiError {
	return &apiError{Code: "AWS.SimpleQueueService.NonExistentQueue", Status: 400, Message: "The specified queue does not exist: " + name, SenderFault: true}
}

// errQueueExists refuses a CreateQueue that disagrees with the queue already
// under that name. "QueueAlreadyExists" is the Query protocol's code;
// jsonErrorType renders it as the QueueNameExists shape for the JSON one.
func errQueueExists(attr string) *apiError {
	return &apiError{
		Code: "QueueAlreadyExists", Status: 400, SenderFault: true,
		Message: "A queue already exists with the same name and a different value for attribute " + attr,
	}
}

// The refusals of a batch as a whole. Everything else about a batch is per
// entry and comes back in Failed; these four mean no entry was attempted.
func errBatch(code, msg string) *apiError {
	return &apiError{Code: "AWS.SimpleQueueService." + code, Status: 400, Message: msg, SenderFault: true}
}

var batchEntryID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// checkBatch holds a batch's entry ids to the rules SQS applies before it
// touches a single entry. op is the request entry's shape name, which is how
// SQS words the empty case.
//
// None of this is in the service model — Entries has no length and Id has no
// pattern there — which is how a batch with the same Id twice came to be
// accepted: both messages were sent, and the caller got back two results it
// could not tell apart.
func checkBatch(op string, ids []string) *apiError {
	if len(ids) == 0 {
		return errBatch("EmptyBatchRequest", "There should be at least one "+op+" in the request.")
	}
	if len(ids) > 10 {
		return errBatch("TooManyEntriesInBatchRequest",
			"Maximum number of entries per request are 10. You have sent "+strconv.Itoa(len(ids))+".")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !batchEntryID.MatchString(id) {
			return errBatch("InvalidBatchEntryId",
				"A batch entry id can only contain alphanumeric characters, hyphens and underscores. It can be at most 80 letters long.")
		}
		if seen[id] {
			return errBatch("BatchEntryIdsNotDistinct", "Id "+id+" repeated.")
		}
		seen[id] = true
	}
	return nil
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
