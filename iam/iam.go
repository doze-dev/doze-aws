// Package iam is doze-aws's local AWS Identity and Access Management: users,
// groups, roles, managed and inline policies, access keys and instance
// profiles, plus a real policy evaluation engine.
//
// # Three modes
//
// IAM is the one service where full fidelity is actively hostile by default —
// switch enforcement on under an existing test suite and everything fails at
// once. So enforcement is a dial, not a fact:
//
//	off      the default. Full CRUD, every API works, nothing is ever denied.
//	         Zero cost: no evaluation runs on the request path at all.
//	soft     every request is evaluated and recorded, and nothing is blocked.
//	         Denials are logged, so you can see what your policies would reject
//	         before you commit to rejecting it.
//	enforce  denials are real, and answer AccessDenied like AWS.
//
// Soft mode also records every action a principal actually performed, which
// `DozeGeneratePolicy` turns into a least-privilege policy document. Run your
// test suite, ask for the policy, commit it — the workflow neither LocalStack
// nor moto offers.
//
// # Honest boundaries
//
// The evaluation engine implements AWS's ordering exactly (explicit deny wins,
// then allow, then implicit deny) with wildcards, NotAction/NotResource and the
// common condition operators. What it will not do is guess: when a request's
// resource ARN cannot be determined from the wire, the engine matches only
// against `"Resource": "*"` statements and says so, rather than inventing an
// ARN and silently allowing or denying the wrong thing.
//
// AWS-managed policies are synthesized from their naming convention rather
// than vendored as a multi-megabyte table — see managed.go.
//
// See docs/api-support/iam.md for the operation-by-operation support table.
package iam

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsquery"
	"github.com/doze-dev/doze-aws/internal/schemaver"
	"github.com/doze-dev/doze-aws/peers"
)

// iamXMLNS is the IAM Query API namespace, fixed since 2010.
const iamXMLNS = "https://iam.amazonaws.com/doc/2010-05-08/"

// Mode selects how far IAM goes on the request path.
type Mode string

const (
	// ModeOff is the default: CRUD only, nothing is evaluated or denied.
	ModeOff Mode = "off"
	// ModeSoft evaluates and records every request but blocks nothing.
	ModeSoft Mode = "soft"
	// ModeEnforce turns denials into real AccessDenied responses.
	ModeEnforce Mode = "enforce"
)

// ParseMode reads a mode name, defaulting an empty string to off.
func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case "", ModeOff:
		return ModeOff, nil
	case ModeSoft:
		return ModeSoft, nil
	case ModeEnforce:
		return ModeEnforce, nil
	}
	return ModeOff, fmt.Errorf("iam: unknown mode %q (want off, soft or enforce)", s)
}

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (iam.bolt). Required.
	DataDir string
	// Mode selects enforcement behaviour; the zero value is ModeOff.
	Mode Mode
	// Peers is accepted for constructor uniformity. IAM dispatches nothing.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the IAM service: an http.Handler speaking the Query/XML protocol,
// and an io.Closer that closes the store.
type Server struct {
	store *Store
	mode  Mode
	rec   *recorder
	logf  func(format string, args ...any)
	api   awsquery.API
	now   func() time.Time
}

// New opens the store under DataDir.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "iam.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "iam", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	mode := opts.Mode
	if mode == "" {
		mode = ModeOff
	}
	s := &Server{
		store: newStore(db),
		mode:  mode,
		rec:   newRecorder(),
		logf:  logf,
		api:   awsquery.API{XMLNS: iamXMLNS, EmptyResult: true},
		now:   time.Now,
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
		s.now = opts.Clock
	}
	return s, nil
}

// Close closes the bbolt DB.
func (s *Server) Close() error { return s.store.db.Close() }

// Mode reports the configured enforcement mode.
func (s *Server) Mode() Mode { return s.mode }

// handler is one IAM action.
type handler func(s *Server, p params) (any, *awshttp.APIError)

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
		if why, stubbed := stubActions[action]; stubbed {
			s.logf("iam: %s -> unsupported", action)
			s.api.WriteError(w, awshttp.Errf(400, "NotImplemented",
				"doze-aws does not implement %s: %s", action, why))
			return
		}
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown IAM action %q", action))
		return
	}
	result, apiErr := h(s, params{vals})
	if apiErr != nil {
		s.logf("iam: %s -> %s", action, apiErr.Code)
		s.api.WriteError(w, apiErr)
		return
	}
	s.logf("iam: %s ok", action)
	s.api.WriteResult(w, action, result)
}

// stubActions are documented IAM operations doze-aws refuses on purpose, with
// the reason. Everything here needs cloud infrastructure — a certificate
// authority, an identity provider, an MFA fleet — that a local emulator has no
// honest way to stand in for.
var stubActions = map[string]string{
	"CreateVirtualMFADevice":                        "there is no MFA fleet locally",
	"DeleteVirtualMFADevice":                        "there is no MFA fleet locally",
	"EnableMFADevice":                               "there is no MFA fleet locally",
	"DeactivateMFADevice":                           "there is no MFA fleet locally",
	"ResyncMFADevice":                               "there is no MFA fleet locally",
	"ListMFADevices":                                "there is no MFA fleet locally",
	"ListVirtualMFADevices":                         "there is no MFA fleet locally",
	"GetMFADevice":                                  "there is no MFA fleet locally",
	"CreateSAMLProvider":                            "federation needs a real identity provider",
	"DeleteSAMLProvider":                            "federation needs a real identity provider",
	"GetSAMLProvider":                               "federation needs a real identity provider",
	"ListSAMLProviders":                             "federation needs a real identity provider",
	"UpdateSAMLProvider":                            "federation needs a real identity provider",
	"CreateOpenIDConnectProvider":                   "federation needs a real identity provider",
	"DeleteOpenIDConnectProvider":                   "federation needs a real identity provider",
	"GetOpenIDConnectProvider":                      "federation needs a real identity provider",
	"ListOpenIDConnectProviders":                    "federation needs a real identity provider",
	"UploadServerCertificate":                       "server certificates are cloud infrastructure",
	"GetServerCertificate":                          "server certificates are cloud infrastructure",
	"ListServerCertificates":                        "server certificates are cloud infrastructure",
	"DeleteServerCertificate":                       "server certificates are cloud infrastructure",
	"UploadSigningCertificate":                      "signing certificates are cloud infrastructure",
	"ListSigningCertificates":                       "signing certificates are cloud infrastructure",
	"DeleteSigningCertificate":                      "signing certificates are cloud infrastructure",
	"UploadSSHPublicKey":                            "SSH keys are for CodeCommit, which doze-aws does not serve",
	"GetSSHPublicKey":                               "SSH keys are for CodeCommit, which doze-aws does not serve",
	"ListSSHPublicKeys":                             "SSH keys are for CodeCommit, which doze-aws does not serve",
	"DeleteSSHPublicKey":                            "SSH keys are for CodeCommit, which doze-aws does not serve",
	"GenerateCredentialReport":                      "credential reports describe a real account's history",
	"GetCredentialReport":                           "credential reports describe a real account's history",
	"GenerateServiceLastAccessedDetails":            "service-last-accessed data comes from CloudTrail",
	"GetServiceLastAccessedDetails":                 "service-last-accessed data comes from CloudTrail",
	"GetServiceLastAccessedDetailsWithEntities":     "service-last-accessed data comes from CloudTrail",
	"GenerateOrganizationsAccessReport":             "there is no Organizations locally",
	"GetOrganizationsAccessReport":                  "there is no Organizations locally",
	"ListOrganizationsFeatures":                     "there is no Organizations locally",
	"EnableOrganizationsRootCredentialsManagement":  "there is no Organizations locally",
	"DisableOrganizationsRootCredentialsManagement": "there is no Organizations locally",
	"EnableOrganizationsRootSessions":               "there is no Organizations locally",
	"DisableOrganizationsRootSessions":              "there is no Organizations locally",
	"CreateServiceSpecificCredential":               "service-specific credentials are for CodeCommit and Keyspaces",
	"ListServiceSpecificCredentials":                "service-specific credentials are for CodeCommit and Keyspaces",
	"UpdateServiceSpecificCredential":               "service-specific credentials are for CodeCommit and Keyspaces",
	"DeleteServiceSpecificCredential":               "service-specific credentials are for CodeCommit and Keyspaces",
	"ResetServiceSpecificCredential":                "service-specific credentials are for CodeCommit and Keyspaces",
	"CreateLoginProfile":                            "there is no console sign-in locally",
	"GetLoginProfile":                               "there is no console sign-in locally",
	"UpdateLoginProfile":                            "there is no console sign-in locally",
	"DeleteLoginProfile":                            "there is no console sign-in locally",
	"ChangePassword":                                "there is no console sign-in locally",
	"UpdateAccountPasswordPolicy":                   "there is no console sign-in to apply a password policy to",
	"DeleteAccountPasswordPolicy":                   "there is no console sign-in to apply a password policy to",
	"AddClientIDToOpenIDConnectProvider":            "federation needs a real identity provider",
	"RemoveClientIDFromOpenIDConnectProvider":       "federation needs a real identity provider",
	"UpdateOpenIDConnectProviderThumbprint":         "federation needs a real identity provider",
	"ListOpenIDConnectProviderTags":                 "federation needs a real identity provider",
	"TagOpenIDConnectProvider":                      "federation needs a real identity provider",
	"UntagOpenIDConnectProvider":                    "federation needs a real identity provider",
	"ListSAMLProviderTags":                          "federation needs a real identity provider",
	"TagSAMLProvider":                               "federation needs a real identity provider",
	"UntagSAMLProvider":                             "federation needs a real identity provider",
	"EnableOutboundWebIdentityFederation":           "federation needs a real identity provider",
	"DisableOutboundWebIdentityFederation":          "federation needs a real identity provider",
	"GetOutboundWebIdentityFederationInfo":          "federation needs a real identity provider",
	"ListMFADeviceTags":                             "there is no MFA fleet locally",
	"TagMFADevice":                                  "there is no MFA fleet locally",
	"UntagMFADevice":                                "there is no MFA fleet locally",
	"ListServerCertificateTags":                     "server certificates are cloud infrastructure",
	"TagServerCertificate":                          "server certificates are cloud infrastructure",
	"UntagServerCertificate":                        "server certificates are cloud infrastructure",
	"UpdateServerCertificate":                       "server certificates are cloud infrastructure",
	"UpdateSigningCertificate":                      "signing certificates are cloud infrastructure",
	"UpdateSSHPublicKey":                            "SSH keys are for CodeCommit, which doze-aws does not serve",
	"AcceptDelegationRequest":                       "delegation requires Organizations",
	"AssociateDelegationRequest":                    "delegation requires Organizations",
	"CreateDelegationRequest":                       "delegation requires Organizations",
	"GetDelegationRequest":                          "delegation requires Organizations",
	"ListDelegationRequests":                        "delegation requires Organizations",
	"RejectDelegationRequest":                       "delegation requires Organizations",
	"UpdateDelegationRequest":                       "delegation requires Organizations",
	"SendDelegationToken":                           "delegation requires Organizations",
	"SetSecurityTokenServicePreferences":            "there is one STS endpoint locally",
	"GetServiceLinkedRoleDeletionStatus":            "role deletion is synchronous locally",
	"ListPoliciesGrantingServiceAccess":             "this is derived from CloudTrail history",
	"GetHumanReadableSummary":                       "this is an advisory console rendering",
}
