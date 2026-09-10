package cloudwatch

// The two names every CloudWatch error has.
//
// CloudWatch migrated off the Query protocol, and the `awsQueryError` trait
// in its model records, per error shape, what that error used to be called.
// `InvalidParameterValueException` was `InvalidParameterValue`; the modern
// name has the suffix, the legacy one does not, and neither is derivable from
// the other by rule — `LimitExceededException` maps to `LimitExceeded` but
// `ResourceNotFoundException` maps to `ResourceNotFound` while a second shape
// maps to the same legacy `ResourceNotFound`. So it is a table.
//
// Which name goes where is the one genuine disagreement between the wires:
// Query puts the legacy name in <Code>, CBOR and JSON keep the modern name in
// __type and carry the legacy one in a header. See request.writeError.
//
// Derived from `aws.protocols#awsQueryError` in .audit-models/cloudwatch.json
// rather than transcribed from documentation, on the same principle as the
// constraint tables.

import "github.com/doze-dev/doze-aws/internal/awshttp"

// queryCodes maps a modern shape name to the legacy Query code and the HTTP
// status the trait assigns it.
var queryCodes = map[string]struct {
	Legacy string
	Status int
}{
	"ConcurrentModificationException":      {"ConcurrentModificationException", 429},
	"InvalidParameterValueException":       {"InvalidParameterValue", 400},
	"MissingRequiredParameterException":    {"MissingParameter", 400},
	"InvalidParameterCombinationException": {"InvalidParameterCombination", 400},
	"InvalidFormatFault":                   {"InvalidFormat", 400},
	"InvalidNextToken":                     {"InvalidNextToken", 400},
	"InternalServiceFault":                 {"InternalServiceError", 500},
	"LimitExceededFault":                   {"LimitExceeded", 400},
	"LimitExceededException":               {"LimitExceededException", 400},
	"ResourceNotFound":                     {"ResourceNotFound", 404},
	"ResourceNotFoundException":            {"ResourceNotFoundException", 404},
	"ResourceConflictException":            {"ResourceConflict", 409},
	"DashboardInvalidInputError":           {"InvalidParameterInput", 400},
	"DashboardNotFoundError":               {"ResourceNotFound", 404},

	// The two codes modelcheck answers with. They are not shapes in the model
	// — the model states constraints, not what a service calls a violation of
	// one — but a caller that asked for Query-compatible errors still needs a
	// legacy name for them, and `InvalidParameterValue` is what a Query client
	// received for a bad parameter before the migration.
	"ValidationException": {"InvalidParameterValue", 400},
	"ValidationError":     {"InvalidParameterValue", 400},
}

// legacyCode is the Query spelling of a modern error name, or "" when there
// is none — a doze-specific error, say, which no old client ever saw.
func legacyCode(modern string) string {
	if e, ok := queryCodes[modern]; ok {
		return e.Legacy
	}
	return ""
}

// errf builds an error with the status the model assigns its shape, so a
// caller naming an error cannot also get its status wrong.
func errf(code, format string, args ...any) *awshttp.APIError {
	status := 400
	if e, ok := queryCodes[code]; ok {
		status = e.Status
	}
	return awshttp.Errf(status, code, format, args...)
}

func errInvalidParameter(format string, args ...any) *awshttp.APIError {
	return errf("InvalidParameterValueException", format, args...)
}

func errMissingParameter(format string, args ...any) *awshttp.APIError {
	return errf("MissingRequiredParameterException", format, args...)
}
