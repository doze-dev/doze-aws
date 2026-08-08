package iam

// SimulateCustomPolicy and SimulatePrincipalPolicy.
//
// These are the API surface of the same engine enforcement uses, which is the
// point: whatever Simulate says is exactly what enforce mode will do. A
// simulator that disagreed with the enforcer would be worse than none.

import (
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsquery"
)

// evalResultView is one (action, resource) verdict.
type evalResultView struct {
	EvalActionName   string `xml:"EvalActionName"`
	EvalResourceName string `xml:"EvalResourceName,omitempty"`
	EvalDecision     string `xml:"EvalDecision"`
	// MatchedStatements names what decided, which is the part that makes a
	// simulation actionable rather than just a verdict.
	MatchedStatements []matchedStatementView `xml:"MatchedStatements>member,omitempty"`
}

type matchedStatementView struct {
	SourcePolicyId string `xml:"SourcePolicyId"`
}

// simulate runs every (action, resource) pair against a document set.
func simulate(docs []*Document, actions, resources []string, ctx map[string][]string) []evalResultView {
	if len(resources) == 0 {
		resources = []string{"*"}
	}
	var out []evalResultView
	for _, action := range actions {
		for _, resource := range resources {
			// A caller asking about "*" is asking the unscoped question, which
			// the engine models as an unknown resource.
			target := resource
			if target == "*" {
				target = ""
			}
			decision, by := Evaluate(docs, Request{Action: action, Resource: target, Context: ctx})
			res := evalResultView{
				EvalActionName:   action,
				EvalResourceName: resource,
				EvalDecision:     decision.AWSResult(),
			}
			if by != "" {
				res.MatchedStatements = []matchedStatementView{{SourcePolicyId: by}}
			}
			out = append(out, res)
		}
	}
	return out
}

// contextEntries reads the ContextEntries.member.N block a caller may supply to
// exercise condition keys.
func contextEntries(p params) map[string][]string {
	ctx := map[string][]string{}
	for i := 1; ; i++ {
		prefix := "ContextEntries.member." + itoa(i)
		key := p.str(prefix + ".ContextKeyName")
		if key == "" {
			break
		}
		vals := awsquery.Members(p.Values, prefix+".ContextKeyValues", false)
		if len(vals) > 0 {
			ctx[key] = vals
		}
	}
	return ctx
}

func hSimulateCustomPolicy(_ *Server, p params) (any, *awshttp.APIError) {
	raw := p.members("PolicyInputList")
	if len(raw) == 0 {
		return nil, errValidation("PolicyInputList is required")
	}
	docs := make([]*Document, 0, len(raw))
	for i, item := range raw {
		d, err := ParsePolicy(decodeDocument(item))
		if err != nil {
			return nil, errMalformedPolicy("PolicyInputList.member.%d: %v", i+1, err)
		}
		docs = append(docs, d)
	}
	actions := p.members("ActionNames")
	if len(actions) == 0 {
		return nil, errValidation("ActionNames is required")
	}
	results := simulate(docs, actions, p.members("ResourceArns"), contextEntries(p))
	return struct {
		EvaluationResults []evalResultView `xml:"EvaluationResults>member"`
		IsTruncated       bool             `xml:"IsTruncated"`
	}{results, false}, nil
}

func hSimulatePrincipalPolicy(s *Server, p params) (any, *awshttp.APIError) {
	source := p.str("PolicySourceArn")
	if source == "" {
		return nil, errValidation("PolicySourceArn is required")
	}
	kind, name, ok := principalFromARN(source)
	if !ok {
		return nil, errInvalidInput("PolicySourceArn %s is not a user, group or role ARN", source)
	}
	docs, err := s.store.PoliciesFor(kind, name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	// Extra inline policies may be supplied to model a proposed change without
	// attaching it — the "what if I added this?" question.
	for i, item := range p.members("PolicyInputList") {
		d, perr := ParsePolicy(decodeDocument(item))
		if perr != nil {
			return nil, errMalformedPolicy("PolicyInputList.member.%d: %v", i+1, perr)
		}
		docs = append(docs, d)
	}
	actions := p.members("ActionNames")
	if len(actions) == 0 {
		return nil, errValidation("ActionNames is required")
	}
	results := simulate(docs, actions, p.members("ResourceArns"), contextEntries(p))
	return struct {
		EvaluationResults []evalResultView `xml:"EvaluationResults>member"`
		IsTruncated       bool             `xml:"IsTruncated"`
	}{results, false}, nil
}

// ---- context keys ----
//
// GetContextKeysFor*Policy reports which condition keys a policy set actually
// references, so a caller knows what to supply to Simulate. It is pure
// analysis of documents doze-aws already holds, so it is answered for real.

func contextKeysOf(docs []*Document) []string {
	seen := map[string]bool{}
	for _, d := range docs {
		if d == nil {
			continue
		}
		for _, st := range d.Statement {
			for _, keys := range st.Condition {
				for key := range keys {
					seen[key] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func parseInputList(p params) ([]*Document, *awshttp.APIError) {
	var docs []*Document
	for i, item := range p.members("PolicyInputList") {
		d, err := ParsePolicy(decodeDocument(item))
		if err != nil {
			return nil, errMalformedPolicy("PolicyInputList.member.%d: %v", i+1, err)
		}
		docs = append(docs, d)
	}
	return docs, nil
}

func hGetContextKeysForCustomPolicy(_ *Server, p params) (any, *awshttp.APIError) {
	docs, aerr := parseInputList(p)
	if aerr != nil {
		return nil, aerr
	}
	if len(docs) == 0 {
		return nil, errValidation("PolicyInputList is required")
	}
	return struct {
		ContextKeyNames []string `xml:"ContextKeyNames>member"`
	}{contextKeysOf(docs)}, nil
}

func hGetContextKeysForPrincipalPolicy(s *Server, p params) (any, *awshttp.APIError) {
	kind, name, ok := principalFromARN(p.str("PolicySourceArn"))
	if !ok {
		return nil, errInvalidInput("PolicySourceArn %s is not a user, group or role ARN", p.str("PolicySourceArn"))
	}
	docs, err := s.store.PoliciesFor(kind, name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	extra, aerr := parseInputList(p)
	if aerr != nil {
		return nil, aerr
	}
	return struct {
		ContextKeyNames []string `xml:"ContextKeyNames>member"`
	}{contextKeysOf(append(docs, extra...))}, nil
}

// principalFromARN splits an IAM principal ARN into its kind and name.
func principalFromARN(arn string) (attachTarget, string, bool) {
	i := strings.Index(arn, ":iam::")
	if i < 0 {
		return 0, "", false
	}
	rest := arn[i+len(":iam::"):]
	// Skip the account segment.
	if j := strings.Index(rest, ":"); j >= 0 {
		rest = rest[j+1:]
	}
	kindStr, path, ok := strings.Cut(rest, "/")
	if !ok {
		return 0, "", false
	}
	// The name is the last path segment.
	name := path
	if j := strings.LastIndex(path, "/"); j >= 0 {
		name = path[j+1:]
	}
	switch kindStr {
	case "user":
		return targetUser, name, true
	case "group":
		return targetGroup, name, true
	case "role":
		return targetRole, name, true
	}
	return 0, "", false
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
