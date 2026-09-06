package stepfunctions

import (
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// The error codes Step Functions returns, spelled exactly as the SDKs match on
// them. `x-amzn-ErrorType` carries the code, and both SDK generations key their
// typed exceptions off it, so a near-miss like "StateMachineNotFound" produces
// a generic error the caller cannot branch on.

func errMachineNotFound(arn string) *awshttp.APIError {
	return awshttp.Errf(400, "StateMachineDoesNotExist", "State Machine Does Not Exist: '%s'", arn)
}

func errMachineDeleting(arn string) *awshttp.APIError {
	return awshttp.Errf(400, "StateMachineDeleting", "State Machine is being deleted: '%s'", arn)
}

func errMachineExists(arn string) *awshttp.APIError {
	return awshttp.Errf(400, "StateMachineAlreadyExists", "State Machine Already Exists: '%s'", arn)
}

func errActivityNotFound(arn string) *awshttp.APIError {
	return awshttp.Errf(400, "ActivityDoesNotExist", "Activity Does Not Exist: '%s'", arn)
}

func errActivityExists(arn string) *awshttp.APIError {
	return awshttp.Errf(400, "ActivityAlreadyExists", "Activity Already Exists: '%s'", arn)
}

// errInvalidDefinition carries the analyser's diagnostics through to the
// caller. AWS returns the first problem plus a count, which is what makes a
// definition with four mistakes take one round trip to understand rather than
// four.
func errInvalidDefinition(detail string) *awshttp.APIError {
	return awshttp.Errf(400, "InvalidDefinition", "Invalid State Machine Definition: '%s'", detail)
}

func errInvalidARN(arn string) *awshttp.APIError {
	return awshttp.Errf(400, "InvalidArn", "Invalid Arn: '%s'", arn)
}

func errResourceNotFound(arn string) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceNotFound", "Resource not found: '%s'", arn)
}

func errInvalidName(detail string) *awshttp.APIError {
	return awshttp.Errf(400, "InvalidName", "Invalid Name: '%s'", detail)
}

func errValidation(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "ValidationException", format, args...)
}

func errExecutionNotFound(arn string) *awshttp.APIError {
	return awshttp.Errf(400, "ExecutionDoesNotExist", "Execution Does Not Exist: '%s'", arn)
}

func errExecutionExists(arn string) *awshttp.APIError {
	return awshttp.Errf(400, "ExecutionAlreadyExists", "Execution Already Exists: '%s'", arn)
}

func errInvalidExecutionInput(detail string) *awshttp.APIError {
	return awshttp.Errf(400, "InvalidExecutionInput", "Invalid State Machine Execution Input: '%s'", detail)
}

// errNotYet is the honest stub. Step Functions is being built in stages, and an
// operation that is registered but incomplete would be worse than one that says
// so — a caller can branch on this, where a wrong answer silently corrupts a
// workflow.
func errNotYet(op, reason string) *awshttp.APIError {
	return awshttp.Errf(400, "UnsupportedOperationException",
		"%s is not supported by doze-aws yet: %s", op, reason)
}
