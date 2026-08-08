package iam

// Enforcement, the access recorder, and the least-privilege policy generator.
//
// This is the file that makes doze-aws's IAM worth having rather than merely
// present. The three modes share one path:
//
//	off      Authorize is never called — the middleware is not even installed.
//	soft     evaluate, record, allow regardless.
//	enforce  evaluate, record, deny for real.
//
// Recording is the interesting half. Every authorization question is kept with
// its verdict, so after a test run `DozeGeneratePolicy` can emit exactly the
// policy the workload needed — the least-privilege loop that normally requires
// reading CloudTrail in a real account.

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/sigparse"
)

func timeUnix(sec int64) time.Time { return time.Unix(sec, 0) }

// AccessEvent is one recorded authorization question and its verdict.
type AccessEvent struct {
	Principal string
	Action    string
	Resource  string
	Decision  Decision
	// ResourceKnown is false when the resource ARN could not be determined
	// from the request. Recorded honestly so a generated policy can say so
	// rather than pretending to be resource-scoped.
	ResourceKnown bool
	Count         int
	Last          int64
}

// key identifies an event for deduplication.
type eventKey struct{ principal, action, resource string }

// recorder accumulates access events. It is a plain in-memory map: the data is
// a diagnostic for the current run, and persisting it would outlive its
// usefulness.
type recorder struct {
	mu     sync.Mutex
	events map[eventKey]*AccessEvent
	now    func() time.Time
}

func newRecorder() *recorder {
	return &recorder{events: map[eventKey]*AccessEvent{}, now: time.Now}
}

func (r *recorder) record(e AccessEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := eventKey{e.Principal, e.Action, e.Resource}
	if prev, ok := r.events[k]; ok {
		prev.Count++
		prev.Last = r.now().Unix()
		// A later deny on the same tuple is the more interesting verdict.
		if e.Decision != Allowed {
			prev.Decision = e.Decision
		}
		return
	}
	e.Count = 1
	e.Last = r.now().Unix()
	r.events[k] = &e
}

// snapshot returns the recorded events, newest-stable ordering by principal
// then action so output is deterministic.
func (r *recorder) snapshot() []AccessEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AccessEvent, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Principal != out[j].Principal {
			return out[i].Principal < out[j].Principal
		}
		if out[i].Action != out[j].Action {
			return out[i].Action < out[j].Action
		}
		return out[i].Resource < out[j].Resource
	})
	return out
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = map[eventKey]*AccessEvent{}
}

// ---- authorization ----

// Result is the outcome of authorizing one request.
type Result struct {
	Decision  Decision
	Principal string
	Action    string
	Resource  string
	// MatchedBy names the statement that decided, when one did.
	MatchedBy string
	// Err is non-nil when the request should be rejected — only ever set in
	// enforce mode.
	Err *awshttp.APIError
}

// Authorize answers whether a request may proceed. It is called by the stack
// middleware in soft and enforce mode, and never in off mode.
//
// Requests that carry no resolvable principal are allowed: an unsigned or
// root-credential call is how every doze-aws client behaves by default, and
// denying them would make enforce mode unusable rather than instructive.
func (s *Server) Authorize(r *http.Request, service string) Result {
	action, resource := ResolveAction(r, service)
	if action == "" {
		return Result{Decision: Allowed}
	}

	principal, kind, name, ok := s.principalFor(r)
	if !ok {
		// No IAM identity behind this request; record it so soft mode still
		// shows the action, but never block.
		s.rec.record(AccessEvent{
			Principal: principal, Action: action, Resource: resource,
			Decision: Allowed, ResourceKnown: resource != "",
		})
		return Result{Decision: Allowed, Principal: principal, Action: action, Resource: resource}
	}

	docs, err := s.store.PoliciesFor(kind, name)
	if err != nil {
		return Result{Decision: Allowed, Principal: principal, Action: action, Resource: resource}
	}
	req := Request{Action: action, Resource: resource, Context: s.contextFor(r, principal, name)}
	decision, by := Evaluate(docs, req)

	// A permissions boundary is a ceiling: the action must be allowed by the
	// identity policies AND by the boundary.
	if decision == Allowed {
		if pr, err := s.store.principalOf(kind, name); err == nil && pr.PermissionsBoundary != "" {
			if bd := s.boundaryDocs(pr.PermissionsBoundary); len(bd) > 0 {
				if bdec, _ := Evaluate(bd, req); bdec != Allowed {
					decision, by = ImplicitDeny, "permissions boundary"
				}
			}
		}
	}

	s.rec.record(AccessEvent{
		Principal: principal, Action: action, Resource: resource,
		Decision: decision, ResourceKnown: resource != "",
	})

	res := Result{Decision: decision, Principal: principal, Action: action, Resource: resource, MatchedBy: by}
	if decision != Allowed && s.mode == ModeEnforce {
		res.Err = errAccessDenied(principal, action, resource)
	}
	if decision != Allowed && s.mode == ModeSoft {
		s.logf("iam[soft]: would deny %s on %s for %s (%s)", action, orDash(resource), principal, decision)
	}
	return res
}

func (s *Server) boundaryDocs(arn string) []*Document {
	pol, err := s.store.GetPolicy(arn)
	if err != nil {
		return nil
	}
	d, err := ParsePolicy(pol.Default())
	if err != nil {
		return nil
	}
	return []*Document{d}
}

// principalFor resolves the calling identity from the request's SigV4 access
// key id. The signature itself is not verified — doze-aws does not authenticate
// — but the key id names who is calling, which is all authorization needs.
func (s *Server) principalFor(r *http.Request) (arn string, kind attachTarget, name string, ok bool) {
	scope, parsed := sigparse.Parse(r)
	if !parsed || scope.AccessKeyID == "" {
		return awsident.GlobalARN("iam", "root"), targetUser, "", false
	}
	// The conventional local credentials are the root identity, not a user.
	if scope.AccessKeyID == awsident.AccessKeyID {
		return awsident.GlobalARN("iam", "root"), targetUser, "", false
	}
	key, found := s.store.LookupAccessKey(scope.AccessKeyID)
	if !found {
		return awsident.GlobalARN("iam", "root"), targetUser, "", false
	}
	// An inactive key authorizes nothing, but the caller is still identified.
	s.store.TouchAccessKey(key.ID, serviceOfScope(scope.Service))
	u, err := s.store.GetUser(key.UserName)
	if err != nil {
		return awsident.GlobalARN("iam", "root"), targetUser, "", false
	}
	return awsident.GlobalARN("iam", "user"+u.Path+u.Name), targetUser, u.Name, true
}

func serviceOfScope(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// contextFor builds the condition-key context for a request.
func (s *Server) contextFor(r *http.Request, principalARN, username string) map[string][]string {
	ctx := map[string][]string{
		"aws:PrincipalAccount": {awsident.AccountID},
		"aws:PrincipalArn":     {principalARN},
		"aws:RequestedRegion":  {awsident.Region},
		"aws:SecureTransport":  {boolStr(r.TLS != nil)},
	}
	if username != "" {
		ctx["aws:username"] = []string{username}
	}
	if host, _, err := splitHostPort(r.RemoteAddr); err == nil && host != "" {
		ctx["aws:SourceIp"] = []string{host}
	}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		ctx["aws:UserAgent"] = []string{ua}
	}
	return ctx
}

func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", nil
	}
	return addr[:i], addr[i+1:], nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ---- doze extensions ----

// hDozeAccessLog returns every recorded authorization decision. It is a doze
// extension, not an AWS action.
func hDozeAccessLog(s *Server, p params) (any, *awshttp.APIError) {
	if p.str("Reset") == "true" {
		s.rec.reset()
		return nil, nil
	}
	type entry struct {
		Principal     string `xml:"Principal"`
		Action        string `xml:"Action"`
		Resource      string `xml:"Resource,omitempty"`
		Decision      string `xml:"Decision"`
		ResourceKnown bool   `xml:"ResourceKnown"`
		Count         int    `xml:"Count"`
		LastUsed      string `xml:"LastUsed"`
	}
	events := s.rec.snapshot()
	out := make([]entry, 0, len(events))
	for _, e := range events {
		out = append(out, entry{
			Principal: e.Principal, Action: e.Action, Resource: e.Resource,
			Decision: e.Decision.String(), ResourceKnown: e.ResourceKnown,
			Count: e.Count, LastUsed: iso(e.Last),
		})
	}
	return struct {
		Mode    string  `xml:"Mode"`
		Entries []entry `xml:"Entries>member"`
	}{string(s.mode), out}, nil
}

// hDozeGeneratePolicy emits a least-privilege policy document covering exactly
// what the recorded principal actually did. This is the payoff of soft mode:
// run a test suite, then ask what permissions it really needed.
func hDozeGeneratePolicy(s *Server, p params) (any, *awshttp.APIError) {
	events := s.rec.snapshot()
	if len(events) == 0 {
		return nil, errNoEntity("no access has been recorded; run with iam mode 'soft' or 'enforce' first")
	}
	want := p.str("Principal")
	scoped := p.str("ScopeToResources") == "true"

	// Group actions by the resource they were used on, so a scoped policy gets
	// one statement per resource set rather than one giant grant.
	byResource := map[string]map[string]bool{}
	unresolved := 0
	for _, e := range events {
		if want != "" && e.Principal != want {
			continue
		}
		res := "*"
		if scoped {
			if !e.ResourceKnown {
				// Refuse to invent an ARN: fall back to "*" for this action and
				// report how often that happened.
				unresolved++
			} else {
				res = e.Resource
			}
		}
		if byResource[res] == nil {
			byResource[res] = map[string]bool{}
		}
		byResource[res][e.Action] = true
	}
	if len(byResource) == 0 {
		return nil, errNoEntity("no access recorded for principal %s", want)
	}

	var sb strings.Builder
	sb.WriteString(`{"Version":"2012-10-17","Statement":[`)
	first := true
	for _, res := range sortedMapKeys(byResource) {
		if !first {
			sb.WriteByte(',')
		}
		first = false
		actions := make([]string, 0, len(byResource[res]))
		for a := range byResource[res] {
			actions = append(actions, a)
		}
		sort.Strings(actions)
		sb.WriteString(`{"Effect":"Allow","Action":[`)
		for i, a := range actions {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`"` + a + `"`)
		}
		sb.WriteString(`],"Resource":"` + res + `"}`)
	}
	sb.WriteString(`]}`)

	return struct {
		PolicyDocument      string `xml:"PolicyDocument"`
		StatementCount      int    `xml:"StatementCount"`
		UnresolvedResources int    `xml:"UnresolvedResources"`
	}{sb.String(), len(byResource), unresolved}, nil
}

func sortedMapKeys(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
