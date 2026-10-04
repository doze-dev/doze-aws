package apigateway

// The account-level API Gateway settings.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// getAccount and updateAccount are GetAccount and UpdateAccount. The CloudWatch role is what
// AWS needs before a stage may log; here it is kept so a deploy that sets
// it (the CDK's `cloudWatchRole: true`) reads back what it wrote.
func (s *Server) getAccount(w http.ResponseWriter) *awshttp.APIError {
	writeAccount(w, s.store.GetAccount())
	return nil
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	acct := s.store.GetAccount()
	ops, aerr := decodePatch(r)
	if aerr != nil {
		return aerr
	}
	for _, op := range ops {
		switch op.Path {
		case "/cloudwatchRoleArn":
			if op.Op == "remove" {
				acct.CloudwatchRoleARN = ""
			} else {
				acct.CloudwatchRoleARN = op.Value
			}
		default:
			return errBadRequest("Invalid patch path '%s'", op.Path)
		}
	}
	if err := s.store.PutAccount(acct); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeAccount(w, acct)
	return nil
}

func writeAccount(w http.ResponseWriter, acct account) {
	writeJSON(w, 200, map[string]any{
		"cloudwatchRoleArn": acct.CloudwatchRoleARN,
		"throttleSettings":  map[string]any{"burstLimit": 5000, "rateLimit": 10000},
		"features":          []string{},
		"apiKeyVersion":     "4",
	})
}
