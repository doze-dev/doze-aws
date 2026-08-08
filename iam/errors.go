package iam

// IAM-shaped errors. The codes are what the SDKs and CloudFormation match on,
// so they are spelled exactly as AWS spells them.

import "github.com/doze-dev/doze-aws/internal/awshttp"

type apiError = awshttp.APIError

// errNoEntity is IAM's 404. AWS returns HTTP 404 with code NoSuchEntity.
func errNoEntity(format string, args ...any) *apiError {
	return awshttp.Errf(404, "NoSuchEntity", format, args...)
}

// errEntityExists is the create-twice conflict (HTTP 409).
func errEntityExists(format string, args ...any) *apiError {
	return awshttp.Errf(409, "EntityAlreadyExists", format, args...)
}

// errDeleteConflict guards deleting something still in use (HTTP 409).
func errDeleteConflict(format string, args ...any) *apiError {
	return awshttp.Errf(409, "DeleteConflict", format, args...)
}

// errMalformedPolicy is what AWS returns for a policy document it cannot parse.
func errMalformedPolicy(format string, args ...any) *apiError {
	return awshttp.Errf(400, "MalformedPolicyDocument", format, args...)
}

func errValidation(format string, args ...any) *apiError {
	return awshttp.Errf(400, "ValidationError", format, args...)
}

func errInvalidInput(format string, args ...any) *apiError {
	return awshttp.Errf(400, "InvalidInput", format, args...)
}

func errLimitExceeded(format string, args ...any) *apiError {
	return awshttp.Errf(409, "LimitExceeded", format, args...)
}

// errAccessDenied is what enforcement returns. The message follows AWS's
// wording closely enough that code matching on it keeps working.
func errAccessDenied(principal, action, resource string) *apiError {
	msg := "User: " + principal + " is not authorized to perform: " + action
	if resource != "" {
		msg += " on resource: " + resource
	}
	return awshttp.Errf(403, "AccessDenied", "%s", msg)
}
