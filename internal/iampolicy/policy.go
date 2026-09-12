// Package iampolicy is the IAM policy document model and evaluation engine,
// shared by the IAM service (identity policies, simulation, the recorder)
// and by every service that evaluates a resource policy of its own (S3
// bucket policies, SQS queue policies, SNS topic policies, Lambda permissions,
// KMS key policies, Secrets Manager and Kinesis resource policies).
package iampolicy

// The policy document model and the evaluation engine.
//
// This is the part of IAM that has to be exactly right, because everything
// else — Simulate, enforcement, the least-privilege recorder — is a caller of
// Evaluate. The rules implemented here are AWS's own, in order:
//
//	1. an explicit Deny anywhere wins, always;
//	2. otherwise an Allow in any attached policy grants;
//	3. otherwise the request is denied implicitly.
//
// Wildcards are matched with a small two-pointer globber rather than compiled
// regular expressions: policy matching runs on every request in enforce mode,
// and `s3:Get*` should not cost a regex compile or an allocation.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Document is a parsed IAM policy document.
type Document struct {
	Version   string      `json:"Version,omitempty"`
	ID        string      `json:"Id,omitempty"`
	Statement []Statement `json:"Statement"`
}

// Statement is one policy statement. Action/Resource/Principal all accept
// either a bare string or an array in real policies, which is what stringList
// exists to absorb.
type Statement struct {
	SID          string         `json:"Sid,omitempty"`
	Effect       string         `json:"Effect"`
	Action       stringList     `json:"Action,omitempty"`
	NotAction    stringList     `json:"NotAction,omitempty"`
	Resource     stringList     `json:"Resource,omitempty"`
	NotResource  stringList     `json:"NotResource,omitempty"`
	Principal    principalBlock `json:"Principal,omitempty"`
	NotPrincipal principalBlock `json:"NotPrincipal,omitempty"`
	Condition    conditionBlock `json:"Condition,omitempty"`
}

// stringList is a JSON value that may be a single string or an array of them.
type stringList []string

func (s *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = stringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("expected a string or array of strings")
	}
	*s = many
	return nil
}

func (s stringList) MarshalJSON() ([]byte, error) {
	if len(s) == 1 {
		return json.Marshal(s[0])
	}
	return json.Marshal([]string(s))
}

// principalBlock is "*", {"AWS": "..."} or {"Service": [...]}. Only its
// presence and the ARNs inside matter locally, so it is kept in a flat form.
type principalBlock struct {
	Any     bool
	ByType  map[string][]string
	Present bool
}

func (p *principalBlock) UnmarshalJSON(b []byte) error {
	p.Present = true
	var star string
	if err := json.Unmarshal(b, &star); err == nil {
		p.Any = star == "*"
		if !p.Any {
			p.ByType = map[string][]string{"AWS": {star}}
		}
		return nil
	}
	var byType map[string]stringList
	if err := json.Unmarshal(b, &byType); err != nil {
		return fmt.Errorf("Principal must be a string or an object")
	}
	p.ByType = map[string][]string{}
	for k, v := range byType {
		p.ByType[k] = v
		for _, item := range v {
			if item == "*" {
				p.Any = true
			}
		}
	}
	return nil
}

func (p principalBlock) MarshalJSON() ([]byte, error) {
	if p.Any && len(p.ByType) == 0 {
		return json.Marshal("*")
	}
	return json.Marshal(p.ByType)
}

// conditionBlock is {Operator: {Key: value-or-list}}.
type conditionBlock map[string]map[string]stringList

// Parse parses and lightly validates a policy document. Validation is
// deliberately shallow: AWS rejects malformed JSON and missing Effect, but is
// permissive about most everything else, and being stricter locally would fail
// policies that work in the cloud.
func Parse(raw string) (*Document, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("policy document is empty")
	}
	var d Document
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, fmt.Errorf("policy document is not valid JSON: %v", err)
	}
	if len(d.Statement) == 0 {
		return nil, fmt.Errorf("policy document has no Statement")
	}
	for i, st := range d.Statement {
		switch st.Effect {
		case "Allow", "Deny":
		default:
			return nil, fmt.Errorf("statement %d: Effect must be Allow or Deny, got %q", i, st.Effect)
		}
		if len(st.Action) == 0 && len(st.NotAction) == 0 {
			return nil, fmt.Errorf("statement %d: one of Action or NotAction is required", i)
		}
	}
	return &d, nil
}

// UnmarshalJSON lets a Statement's Statement field be a single object rather
// than an array, which AWS accepts.
func (d *Document) UnmarshalJSON(b []byte) error {
	type plain struct {
		Version   string          `json:"Version,omitempty"`
		ID        string          `json:"Id,omitempty"`
		Statement json.RawMessage `json:"Statement"`
	}
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	d.Version, d.ID = p.Version, p.ID
	if len(p.Statement) == 0 {
		return nil
	}
	if err := json.Unmarshal(p.Statement, &d.Statement); err == nil {
		return nil
	}
	var one Statement
	if err := json.Unmarshal(p.Statement, &one); err != nil {
		return fmt.Errorf("Statement must be an object or an array of objects")
	}
	d.Statement = []Statement{one}
	return nil
}

// ---- evaluation ----

// Decision is the outcome of evaluating a request against a policy set.
type Decision int

const (
	// ImplicitDeny is the default: nothing granted the action.
	ImplicitDeny Decision = iota
	// Allowed means at least one statement allowed it and none denied it.
	Allowed
	// ExplicitDeny means a Deny statement matched. It can never be overridden.
	ExplicitDeny
)

func (d Decision) String() string {
	switch d {
	case Allowed:
		return "allowed"
	case ExplicitDeny:
		return "explicitDeny"
	default:
		return "implicitDeny"
	}
}

// AWSResult renders the decision the way SimulatePrincipalPolicy reports it.
func (d Decision) AWSResult() string {
	switch d {
	case Allowed:
		return "allowed"
	case ExplicitDeny:
		return "explicitDeny"
	default:
		return "implicitDeny"
	}
}

// Request is one authorization question.
type Request struct {
	// Action is the fully qualified action, e.g. "s3:GetObject".
	Action string
	// Resource is the ARN being acted on. Empty means the caller could not
	// determine it, in which case only "*" resource statements can match — the
	// engine never guesses.
	Resource string
	// Context carries condition keys (aws:SourceIp, aws:username, ...).
	Context map[string][]string
	// Principal is the caller, for resource policies: an IAM user or role
	// ARN, the account root ARN, or a service principal such as
	// "s3.amazonaws.com" when one service calls another. Identity policies
	// carry no Principal block and ignore it.
	Principal string
	// Account is the account this instance is, used by the account-principal
	// rule: a Principal naming the bare account admits its root and delegates
	// to identity policies, and a Deny covers every identity in it. Empty means
	// the conventional local account.
	Account string
}

// Evaluate applies the AWS evaluation order across every supplied document.
// Deny is checked across all documents before any Allow is honoured, which is
// what makes an explicit Deny in one attached policy override an Allow in
// another.
func Evaluate(docs []*Document, req Request) (Decision, string) {
	allowedBy := ""
	allow := false
	for _, d := range docs {
		if d == nil {
			continue
		}
		for i := range d.Statement {
			st := &d.Statement[i]
			if !statementMatches(st, req) {
				continue
			}
			if st.Effect == "Deny" {
				return ExplicitDeny, statementLabel(d, st, i)
			}
			if !allow {
				allow, allowedBy = true, statementLabel(d, st, i)
			}
		}
	}
	if allow {
		return Allowed, allowedBy
	}
	return ImplicitDeny, ""
}

func statementLabel(d *Document, st *Statement, i int) string {
	if st.SID != "" {
		return st.SID
	}
	if d.ID != "" {
		return fmt.Sprintf("%s[%d]", d.ID, i)
	}
	return fmt.Sprintf("statement[%d]", i)
}

// statementMatches reports whether a statement's action, resource and
// condition blocks all match the request.
func statementMatches(st *Statement, req Request) bool {
	if !actionMatches(st, req.Action) {
		return false
	}
	if !resourceMatches(st, req.Resource) {
		return false
	}
	if !principalMatches(st, req.Principal, req.Account) {
		return false
	}
	return conditionsMatch(st.Condition, req.Context)
}

// actionMatches handles Action and its negation NotAction. Action comparison is
// case-insensitive in AWS ("s3:getobject" matches "s3:GetObject").
func actionMatches(st *Statement, action string) bool {
	if len(st.NotAction) > 0 {
		for _, pat := range st.NotAction {
			if globMatchFold(pat, action) {
				return false
			}
		}
		return true
	}
	for _, pat := range st.Action {
		if globMatchFold(pat, action) {
			return true
		}
	}
	return false
}

// resourceMatches handles Resource and NotResource. ARNs are case-sensitive.
//
// A statement with no Resource block at all matches anything: that is the
// shape of an inline trust or identity policy that only constrains actions.
func resourceMatches(st *Statement, resource string) bool {
	if len(st.NotResource) > 0 {
		if resource == "" {
			return false // cannot prove exclusion without knowing the resource
		}
		for _, pat := range st.NotResource {
			if globMatch(pat, resource) {
				return false
			}
		}
		return true
	}
	if len(st.Resource) == 0 {
		return true
	}
	for _, pat := range st.Resource {
		// An unresolved resource can still match a policy that grants "*",
		// which is the common local case; it must never match a narrower one.
		if pat == "*" {
			return true
		}
		if resource != "" && globMatch(pat, resource) {
			return true
		}
	}
	return false
}

// ---- wildcard matching ----

// globMatch matches an IAM wildcard pattern ('*' any run, '?' one character)
// against s. It is iterative with backtracking — no allocation, no regex.
func globMatch(pattern, s string) bool { return glob(pattern, s, false) }

// globMatchFold is globMatch with ASCII case folding, for action names.
func globMatchFold(pattern, s string) bool { return glob(pattern, s, true) }

func glob(pattern, s string, fold bool) bool {
	var p, i int        // cursors into pattern and s
	star, mark := -1, 0 // last '*' seen, and where it had matched up to
	for i < len(s) {
		switch {
		case p < len(pattern) && (pattern[p] == '?' || eq(pattern[p], s[i], fold)):
			p++
			i++
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, i
			p++
		case star >= 0:
			// Backtrack: let the last '*' swallow one more character.
			p = star + 1
			mark++
			i = mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

func eq(a, b byte, fold bool) bool {
	if a == b {
		return true
	}
	if !fold {
		return false
	}
	return lower(a) == lower(b)
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}
