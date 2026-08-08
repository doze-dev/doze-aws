package cloudformation

// The CloudFormation service: enough of the real API that `sam deploy` and
// `cdk deploy` work unmodified.
//
// What those tools actually do is narrower than CloudFormation's surface
// suggests. They upload a template, create a change set (or call CreateStack
// directly), poll DescribeStackEvents until a terminal status shows up, and
// read Outputs. None of that needs asynchronous convergence, a dependency
// graph, or drift detection — it needs the operations, and it needs the stack
// to finish.
//
// So the whole service is synchronous. CreateStack transpiles the template,
// runs the existing convergent apply, records what happened, and returns
// CREATE_COMPLETE. The events the tools poll are synthesized afterwards from
// what apply really did, so they describe reality rather than performing it.
//
// The one capability this adds beyond the transpiler is DELETION. A stack
// tracks the physical resources it created, so DeleteStack can reclaim them —
// which is what makes a local stack feel like a stack rather than an
// accumulating pile.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsquery"
	"github.com/doze-dev/doze-aws/internal/schemaver"
	"github.com/doze-dev/doze-aws/peers"
)

// cfnXMLNS is the CloudFormation Query API namespace.
const cfnXMLNS = "http://cloudformation.amazonaws.com/doc/2010-05-15/"

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (cloudformation.bolt). Required.
	DataDir string
	// Gateway is the shared-endpoint handler stack operations provision
	// through. CloudFormation is the one service that legitimately needs the
	// whole gateway, because it creates resources across every other service.
	// It is resolved at request time, so construction order does not matter.
	Gateway http.Handler
	// Peers resolves sibling services for template fetches from S3.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the CloudFormation service.
type Server struct {
	store   *Store
	gateway http.Handler
	peers   peers.Directory
	logf    func(format string, args ...any)
	api     awsquery.API
	now     func() time.Time
}

// New opens the store under DataDir.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "cloudformation.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "cloudformation", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store:   newStore(db),
		gateway: opts.Gateway,
		peers:   opts.Peers,
		logf:    logf,
		api:     awsquery.API{XMLNS: cfnXMLNS, EmptyResult: true},
		now:     time.Now,
	}
	if s.peers == nil {
		s.peers = peers.None()
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
		s.now = opts.Clock
	}
	return s, nil
}

// Close closes the bbolt DB.
func (s *Server) Close() error { return s.store.db.Close() }

type handler func(s *Server, p params) (any, *awshttp.APIError)

// params wraps the decoded Query form.
type params struct{ vals map[string][]string }

func (p params) str(key string) string {
	if v, ok := p.vals[key]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

func (p params) bool_(key string) bool { return p.str(key) == "true" }

// members reads a Foo.member.N list.
func (p params) members(prefix string) []string {
	var out []string
	for i := 1; ; i++ {
		v := p.str(prefix + ".member." + itoa(i))
		if v == "" {
			return out
		}
		out = append(out, v)
	}
}

// keyValues reads a Parameters.member.N.{KeyField,ValueField} list, which is
// how both parameters and tags travel on the wire.
func (p params) keyValues(prefix, keyField, valField string) map[string]string {
	out := map[string]string{}
	for i := 1; ; i++ {
		base := prefix + ".member." + itoa(i) + "."
		k := p.str(base + keyField)
		if k == "" {
			return out
		}
		// UsePreviousValue means "keep what the stack already has"; the caller
		// merges those in, so an empty value here is meaningful.
		out[k] = p.str(base + valField)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
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
		if why, stubbed := stubActions[action]; stubbed {
			s.logf("cloudformation: %s -> unsupported", action)
			s.api.WriteError(w, awshttp.Errf(400, "ValidationError",
				"doze-aws does not implement %s: %s", action, why))
			return
		}
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown CloudFormation action %q", action))
		return
	}
	result, apiErr := h(s, params{vals})
	if apiErr != nil {
		s.logf("cloudformation: %s -> %s", action, apiErr.Code)
		s.api.WriteError(w, apiErr)
		return
	}
	s.logf("cloudformation: %s ok", action)
	s.api.WriteResult(w, action, result)
}

// ctx is the context stack operations run under. Apply talks to sibling
// services in-process, so a generous ceiling only matters if something hangs.
func (s *Server) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Minute)
}

// stubActions are documented operations doze-aws refuses on purpose. Almost
// all of them describe cloud-side machinery — drift, StackSets, the registry,
// organizations — that has no local counterpart to inspect.
var stubActions = map[string]string{
	"DetectStackDrift":                  "there is nothing to drift from locally",
	"DetectStackResourceDrift":          "there is nothing to drift from locally",
	"DetectStackSetDrift":               "there is nothing to drift from locally",
	"DescribeStackDriftDetectionStatus": "there is nothing to drift from locally",
	"DescribeStackResourceDrifts":       "there is nothing to drift from locally",
	"CreateStackSet":                    "StackSets need Organizations",
	"UpdateStackSet":                    "StackSets need Organizations",
	"DeleteStackSet":                    "StackSets need Organizations",
	"DescribeStackSet":                  "StackSets need Organizations",
	"ListStackSets":                     "StackSets need Organizations",
	"CreateStackInstances":              "StackSets need Organizations",
	"DeleteStackInstances":              "StackSets need Organizations",
	"UpdateStackInstances":              "StackSets need Organizations",
	"ListStackInstances":                "StackSets need Organizations",
	"DescribeStackSetOperation":         "StackSets need Organizations",
	"ListStackSetOperations":            "StackSets need Organizations",
	"ListStackSetOperationResults":      "StackSets need Organizations",
	"StopStackSetOperation":             "StackSets need Organizations",
	"ImportStacksToStackSet":            "StackSets need Organizations",
	"ActivateOrganizationsAccess":       "there is no Organizations locally",
	"DeactivateOrganizationsAccess":     "there is no Organizations locally",
	"DescribeOrganizationsAccess":       "there is no Organizations locally",
	"RegisterType":                      "the extension registry is cloud infrastructure",
	"DeregisterType":                    "the extension registry is cloud infrastructure",
	"DescribeType":                      "the extension registry is cloud infrastructure",
	"ListTypes":                         "the extension registry is cloud infrastructure",
	"ListTypeVersions":                  "the extension registry is cloud infrastructure",
	"ListTypeRegistrations":             "the extension registry is cloud infrastructure",
	"SetTypeDefaultVersion":             "the extension registry is cloud infrastructure",
	"SetTypeConfiguration":              "the extension registry is cloud infrastructure",
	"BatchDescribeTypeConfigurations":   "the extension registry is cloud infrastructure",
	"ActivateType":                      "the extension registry is cloud infrastructure",
	"DeactivateType":                    "the extension registry is cloud infrastructure",
	"PublishType":                       "the extension registry is cloud infrastructure",
	"TestType":                          "the extension registry is cloud infrastructure",
	"DescribeTypeRegistration":          "the extension registry is cloud infrastructure",
	"RegisterPublisher":                 "the extension registry is cloud infrastructure",
	"DescribePublisher":                 "the extension registry is cloud infrastructure",
	"StartResourceScan":                 "resource scanning reads a real account",
	"DescribeResourceScan":              "resource scanning reads a real account",
	"ListResourceScans":                 "resource scanning reads a real account",
	"ListResourceScanResources":         "resource scanning reads a real account",
	"ListResourceScanRelatedResources":  "resource scanning reads a real account",
	"CreateGeneratedTemplate":           "template generation reads a real account",
	"UpdateGeneratedTemplate":           "template generation reads a real account",
	"DeleteGeneratedTemplate":           "template generation reads a real account",
	"DescribeGeneratedTemplate":         "template generation reads a real account",
	"GetGeneratedTemplate":              "template generation reads a real account",
	"ListGeneratedTemplates":            "template generation reads a real account",
	"CreateStackRefactor":               "stack refactoring is a cloud-side operation",
	"ExecuteStackRefactor":              "stack refactoring is a cloud-side operation",
	"DescribeStackRefactor":             "stack refactoring is a cloud-side operation",
	"ListStackRefactors":                "stack refactoring is a cloud-side operation",
	"ListStackRefactorActions":          "stack refactoring is a cloud-side operation",
	"EstimateTemplateCost":              "there is no pricing API locally",
	"DescribeAccountLimits":             "there are no account limits locally",
	"SignalResource":                    "there are no EC2 instances to signal",
	"DescribeChangeSetHooks":            "hooks are part of the extension registry",
	"GetHookResult":                     "hooks are part of the extension registry",
	"ListHookResults":                   "hooks are part of the extension registry",
	"RecordHandlerProgress":             "used by registry resource providers",
	"ListStackInstanceResourceDrifts":   "StackSets need Organizations",
	"ListStackSetAutoDeploymentTargets": "StackSets need Organizations",
	"RollbackStack":                     "apply is synchronous, so there is no in-flight update to roll back",
	"DescribeEvents":                    "an alias for DescribeStackEvents that no current SDK emits",
	"DescribeStackInstance":             "StackSets need Organizations",
	"ContinueUpdateRollback":            "apply is synchronous, so there is no rollback to continue",
}
