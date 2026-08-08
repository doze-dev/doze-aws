package cloudformation

// Transpilation: template → provision.Stack.
//
// The walk is two-pass, and it has to be. Intrinsics reference resources
// (`!GetAtt MyQueue.Arn`), so every resource's name and attributes must be
// known before any property is evaluated. Pass one assigns names from the
// unevaluated properties; pass two evaluates everything with a fully populated
// scope. A single-pass walk would fail on any forward reference — which is
// most templates.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/provision"
)

// Report is the outcome of a transpile: what was mapped, ignored and rejected.
type Report struct {
	StackName string
	Entries   []Entry
	// Outputs are the evaluated template outputs.
	Outputs map[string]string
	// SAM is true when the SAM transform was applied.
	SAM bool
}

// Counts summarises the report.
func (r *Report) Counts() (mapped, ignored, rejected int) {
	for _, e := range r.Entries {
		switch e.Kind {
		case Mapped:
			mapped++
		case Ignored:
			ignored++
		default:
			rejected++
		}
	}
	return
}

// Ignored returns the entries doze-aws accepted but did not provision. Callers
// print these: a silently skipped resource is the failure mode this whole
// design exists to avoid.
func (r *Report) Ignored() []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.Kind == Ignored {
			out = append(out, e)
		}
	}
	return out
}

// TranspileOptions configures a transpile.
type TranspileOptions struct {
	// StackName names the stack, used by AWS::StackName and for reporting.
	StackName string
	// Parameters supplies template parameter values; defaults fill the rest.
	Parameters map[string]string
	// Exports resolves Fn::ImportValue lookups.
	Exports map[string]string
	// AllowUnsupported downgrades a rejected resource type to a warning
	// instead of failing the whole transpile.
	AllowUnsupported bool
}

// Transpile converts a parsed template into a stack file plus a report.
func Transpile(t *Template, opts TranspileOptions) (*provision.Stack, *Report, error) {
	if opts.StackName == "" {
		opts.StackName = "doze-stack"
	}
	rep := &Report{StackName: opts.StackName, Outputs: map[string]string{}, SAM: t.IsSAM()}

	if t.IsSAM() {
		if err := applySAMTransform(t); err != nil {
			return nil, nil, err
		}
	}
	for _, tr := range t.Transform {
		if !strings.HasPrefix(tr, "AWS::Serverless-") {
			return nil, nil, fmt.Errorf("unsupported Transform %q: doze-aws understands only the SAM transform", tr)
		}
	}

	// Resolve parameters: supplied values win, declared defaults fill in, and
	// a parameter with neither is a hard error — deploying with a blank value
	// is exactly the silent corruption this package refuses to do.
	params := map[string]any{}
	for name, decl := range t.Parameters {
		if v, ok := opts.Parameters[name]; ok {
			params[name] = coerceParam(v, decl.Type)
			continue
		}
		if decl.Default != nil {
			// A default is coerced by declared type exactly like a supplied
			// value: Ref on a CommaDelimitedList must yield a LIST, and CDK's
			// bootstrap template Fn::Joins over one that is defaulted empty.
			if str, ok := decl.Default.(string); ok {
				params[name] = coerceParam(str, decl.Type)
			} else {
				params[name] = decl.Default
			}
			continue
		}
		return nil, nil, fmt.Errorf("parameter %q has no value and no default", name)
	}
	for name, v := range opts.Parameters {
		if _, declared := t.Parameters[name]; !declared {
			return nil, nil, fmt.Errorf("parameter %q was supplied but is not declared in the template", name)
		}
		_ = v
	}

	scope := &Scope{
		StackName:  opts.StackName,
		Parameters: params,
		Mappings:   t.Mappings,
		Refs:       map[string]string{},
		Atts:       map[string]map[string]string{},
		Exports:    opts.Exports,
	}
	if err := scope.EvalConditions(t.Conditions); err != nil {
		return nil, nil, err
	}

	// ---- pass one: classify and name ----
	type pending struct {
		res  *Resource
		name string
	}
	var work []pending
	for _, id := range t.Order() {
		r := t.Resources[id]

		// A resource gated by a false condition is not created at all.
		if r.Condition != "" {
			active, ok := scope.Conditions[r.Condition]
			if !ok {
				return nil, nil, fmt.Errorf("resource %s references undefined condition %q", id, r.Condition)
			}
			if !active {
				rep.Entries = append(rep.Entries, Entry{
					LogicalID: id, Type: r.Type, Kind: Ignored,
					Reason: "condition " + r.Condition + " is false",
				})
				continue
			}
		}

		kind, reason := classify(r.Type)
		switch kind {
		case Ignored:
			// An ignored resource keeps its identity so references to it still
			// resolve — templates GetAtt IAM role ARNs constantly.
			ghost := ghostName(r, r.Properties)
			ref, atts := ghostIdentity(r.Type, ghost)
			scope.Refs[id], scope.Atts[id] = ref, atts
			rep.Entries = append(rep.Entries, Entry{
				LogicalID: id, Type: r.Type, Kind: Ignored, Name: ghost, Reason: reason,
			})
			continue
		case Rejected:
			rep.Entries = append(rep.Entries, Entry{LogicalID: id, Type: r.Type, Kind: Rejected, Reason: reason})
			if !opts.AllowUnsupported {
				return nil, rep, fmt.Errorf("resource %s: %s (%s)", id, reason, r.Type)
			}
			continue
		}

		// Names may themselves contain intrinsics that only depend on
		// parameters, so evaluate the name property alone at this stage. With
		// no explicit name, the logical ID is used — sanitised to the service's
		// naming rules, since a logical ID is not always a legal name.
		name := derivedName(r.Type, r.LogicalID)
		if prop, ok := nameProperty[r.Type]; ok && prop != "" {
			if raw, present := r.Properties[prop]; present {
				v, err := scope.Eval(raw)
				if err != nil {
					return nil, rep, fmt.Errorf("resource %s: %s: %w", id, prop, err)
				}
				if s := fmt.Sprint(v); s != "" && s != "<nil>" {
					name = s
				}
			}
		}
		scope.Refs[id] = refValue(r.Type, name)
		scope.Atts[id] = attributes(r.Type, name)
		work = append(work, pending{r, name})
		rep.Entries = append(rep.Entries, Entry{LogicalID: id, Type: r.Type, Kind: Mapped, Name: name})
	}

	// ---- pass two: evaluate and map ----
	stack := &provision.Stack{
		Queues:     map[string]provision.Queue{},
		Topics:     map[string]provision.Topic{},
		Buckets:    map[string]provision.Bucket{},
		Tables:     map[string]provision.Table{},
		Functions:  map[string]provision.Function{},
		Rules:      map[string]provision.Rule{},
		Keys:       map[string]provision.Key{},
		Secrets:    map[string]provision.Secret{},
		Parameters: map[string]provision.Parameter{},
		APIs:       map[string]provision.API{},
	}
	m := &mapper{scope: scope, stack: stack, template: t}
	for _, p := range work {
		props, err := scope.Eval(p.res.Properties)
		if err != nil {
			return nil, rep, fmt.Errorf("resource %s: %w", p.res.LogicalID, err)
		}
		evaluated, _ := props.(map[string]any)
		if evaluated == nil {
			evaluated = map[string]any{}
		}
		if err := m.apply(p.res, p.name, evaluated); err != nil {
			return nil, rep, fmt.Errorf("resource %s (%s): %w", p.res.LogicalID, p.res.Type, err)
		}
	}
	// Attachments (subscriptions, event source mappings, notifications) are
	// applied after every resource exists, so they can reference anything.
	if err := m.applyDeferred(); err != nil {
		return nil, rep, err
	}

	// ---- outputs ----
	for name, out := range t.Outputs {
		if out.Condition != "" && !scope.Conditions[out.Condition] {
			continue
		}
		v, err := scope.Eval(out.Value)
		if err != nil {
			return nil, rep, fmt.Errorf("output %s: %w", name, err)
		}
		rep.Outputs[name] = fmt.Sprint(v)
	}

	// Trim empty sections so an exported stack file reads cleanly.
	pruneEmpty(stack)
	return stack, rep, nil
}

// coerceParam converts a string-supplied parameter to the declared type, so
// Fn::Equals against a Number behaves the same as it would in AWS.
func coerceParam(v, typ string) any {
	switch typ {
	case "Number":
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	// Every List<...> type, and the comma-delimited spelling, Refs to a list.
	if typ == "CommaDelimitedList" || strings.HasPrefix(typ, "List<") {
		parts := strings.Split(v, ",")
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = strings.TrimSpace(p)
		}
		return out
	}
	return v
}

func pruneEmpty(s *provision.Stack) {
	if len(s.Queues) == 0 {
		s.Queues = nil
	}
	if len(s.Topics) == 0 {
		s.Topics = nil
	}
	if len(s.Buckets) == 0 {
		s.Buckets = nil
	}
	if len(s.Tables) == 0 {
		s.Tables = nil
	}
	if len(s.Functions) == 0 {
		s.Functions = nil
	}
	if len(s.Rules) == 0 {
		s.Rules = nil
	}
	if len(s.Keys) == 0 {
		s.Keys = nil
	}
	if len(s.Secrets) == 0 {
		s.Secrets = nil
	}
	if len(s.Parameters) == 0 {
		s.Parameters = nil
	}
	if len(s.APIs) == 0 {
		s.APIs = nil
	}
}

// ---- property helpers ----

func propStr(props map[string]any, key string) string {
	v, ok := props[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func propInt(props map[string]any, key string) int {
	v, ok := props[key]
	if !ok {
		return 0
	}
	n, err := toInt(v)
	if err != nil {
		return 0
	}
	return n
}

func propBool(props map[string]any, key string) bool {
	switch t := props[key].(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true")
	}
	return false
}

func propMap(props map[string]any, key string) map[string]any {
	m, _ := props[key].(map[string]any)
	return m
}

func propList(props map[string]any, key string) []any {
	switch t := props[key].(type) {
	case []any:
		return t
	case nil:
		return nil
	default:
		// A singleton is a valid spelling of a one-element list in CFN.
		return []any{t}
	}
}

// propTags reads the standard [{Key,Value}] tag list.
func propTags(props map[string]any) map[string]string {
	items := propList(props, "Tags")
	if len(items) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		k := propStr(m, "Key")
		if k == "" {
			continue
		}
		out[k] = propStr(m, "Value")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// docOf marshals an evaluated value back to a JSON document for the IR's Doc
// fields (event patterns, filter policies).
func docOf(v any) (provision.Doc, error) {
	if v == nil {
		return provision.Doc{}, nil
	}
	if s, ok := v.(string); ok {
		return provision.Doc{JSON: s}, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return provision.Doc{}, err
	}
	return provision.Doc{JSON: string(raw)}, nil
}

// nameFromARN extracts the trailing resource name from an ARN or a bare name,
// which is how templates reference siblings once intrinsics have resolved.
func nameFromARN(v string) string {
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "arn:") {
		// A queue URL ends in /<account>/<name>.
		if i := strings.LastIndex(v, "/"); i >= 0 && strings.Contains(v, "://") {
			return v[i+1:]
		}
		return v
	}
	// arn:aws:sqs:region:account:name  |  arn:aws:s3:::bucket
	// arn:aws:lambda:region:account:function:name
	// arn:aws:dynamodb:region:account:table/name
	tail := v[strings.LastIndex(v, ":")+1:]
	if i := strings.LastIndex(tail, "/"); i >= 0 {
		tail = tail[i+1:]
	}
	return tail
}
