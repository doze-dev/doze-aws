// Package sts is doze-aws's local Security Token Service. STS is the identity
// hinge of the AWS SDK ecosystem: countless tools call GetCallerIdentity on
// startup and AssumeRole to build sessions, and fail hard when no endpoint
// answers. Locally there is exactly one identity and nothing to authenticate,
// so every operation answers instantly with the fixed doze-aws account and
// freshly minted throwaway credentials.
//
// The service is stateless: Options.DataDir is accepted for constructor
// uniformity with the other services and never used.
//
// See docs/api-support/sts.md for the operation-by-operation support table.
package sts

import (
	"net/http"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsquery"
)

// xmlns is the STS Query API namespace, fixed since 2011.
const xmlns = "https://sts.amazonaws.com/doc/2011-06-15/"

// Options configures the service. All fields are optional.
type Options struct {
	// DataDir is accepted for uniformity with the stateful services; STS
	// stores nothing.
	DataDir string
	// Logf receives one line per handled request; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the STS service: an http.Handler speaking the Query/XML protocol,
// and an io.Closer for uniformity (closing is a no-op).
type Server struct {
	logf func(format string, args ...any)
	now  func() time.Time
	api  awsquery.API
}

// New builds the service.
func New(opts Options) (*Server, error) {
	s := &Server{
		logf: opts.Logf,
		now:  opts.Clock,
		api:  awsquery.API{XMLNS: xmlns},
	}
	if s.logf == nil {
		s.logf = func(string, ...any) {}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Close is a no-op; STS holds no resources.
func (s *Server) Close() error { return nil }

// handler is one STS action: it returns a result struct for the standard
// envelope, or an *awshttp.APIError.
type handler func(s *Server, p params) (any, *awshttp.APIError)

// handlers maps every documented STS action. This table IS the coverage
// statement: an action absent here answers InvalidAction, and the api-support
// doc is generated from the same names.
var handlers = map[string]handler{
	"AssumeRole":                 (*Server).assumeRole,
	"AssumeRoleWithSAML":         (*Server).assumeRoleWithSAML,
	"AssumeRoleWithWebIdentity":  (*Server).assumeRoleWithWebIdentity,
	"AssumeRoot":                 (*Server).assumeRoot,
	"DecodeAuthorizationMessage": (*Server).decodeAuthorizationMessage,
	"GetAccessKeyInfo":           (*Server).getAccessKeyInfo,
	"GetCallerIdentity":          (*Server).getCallerIdentity,
	"GetFederationToken":         (*Server).getFederationToken,
	"GetSessionToken":            (*Server).getSessionToken,
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	vals, err := awsquery.Params(r)
	if err != nil {
		s.api.WriteError(w, awshttp.AsAPIError(err))
		return
	}
	action := vals.Get("Action")
	if action == "" {
		s.api.WriteError(w, awshttp.Errf(400, "MissingAction", "request has no Action parameter"))
		return
	}
	h, ok := handlers[action]
	if !ok {
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown STS action %q", action))
		return
	}
	result, apiErr := h(s, params{vals})
	if apiErr != nil {
		s.logf("sts: %s -> %s", action, apiErr.Code)
		s.api.WriteError(w, apiErr)
		return
	}
	s.logf("sts: %s ok", action)
	s.api.WriteResult(w, action, result)
}
