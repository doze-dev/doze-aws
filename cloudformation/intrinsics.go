package cloudformation

// The intrinsic function evaluator.
//
// Every property value in a template is a tree that may contain intrinsics at
// any depth, so evaluation is a recursive walk that rewrites those nodes and
// leaves everything else alone. The rule throughout: an intrinsic that cannot
// be resolved is an ERROR, never a silent empty string — a template that
// deploys with a blank queue name because `!Ref` missed is the single worst
// failure mode a transpiler can have.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// Scope carries everything an evaluation needs: resolved parameters, the
// template's mappings and conditions, and what each resource's Ref and GetAtt
// resolve to.
type Scope struct {
	StackName  string
	Parameters map[string]any
	Mappings   map[string]any
	// Conditions holds evaluated condition results, filled in before resources
	// are evaluated.
	Conditions map[string]bool
	// Refs maps a resource's logical ID to its Ref value.
	Refs map[string]string
	// Atts maps a logical ID to its resolvable attributes (Arn, QueueName, …).
	Atts map[string]map[string]string
	// Exports resolves Fn::ImportValue names. Locally there is no cross-stack
	// export registry unless one is supplied.
	Exports map[string]string
}

// pseudo resolves an AWS::* pseudo-parameter.
func (s *Scope) pseudo(name string) (string, bool) {
	switch name {
	case "AWS::Region":
		return awsident.Region, true
	case "AWS::AccountId":
		return awsident.AccountID, true
	case "AWS::Partition":
		return "aws", true
	case "AWS::StackName":
		return s.StackName, true
	case "AWS::StackId":
		return awsident.ARN("cloudformation", "stack/"+s.StackName+"/local"), true
	case "AWS::URLSuffix":
		return "amazonaws.com", true
	case "AWS::NoValue":
		return "", true
	}
	return "", false
}

// Eval resolves every intrinsic in v, returning a tree of plain values.
func (s *Scope) Eval(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		// An intrinsic is a single-key object whose key is Ref or Fn::*.
		if len(t) == 1 {
			for key, arg := range t {
				if key == "Ref" || strings.HasPrefix(key, "Fn::") || key == "Condition" {
					return s.call(key, arg)
				}
			}
		}
		out := make(map[string]any, len(t))
		for k, item := range t {
			ev, err := s.Eval(item)
			if err != nil {
				return nil, err
			}
			// AWS::NoValue removes the property entirely.
			if isNoValue(ev) {
				continue
			}
			out[k] = ev
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(t))
		for _, item := range t {
			ev, err := s.Eval(item)
			if err != nil {
				return nil, err
			}
			if isNoValue(ev) {
				continue
			}
			out = append(out, ev)
		}
		return out, nil
	}
	return v, nil
}

// noValue is the sentinel Ref AWS::NoValue evaluates to, so a caller can drop
// the enclosing property.
type noValue struct{}

func isNoValue(v any) bool { _, ok := v.(noValue); return ok }

func (s *Scope) call(key string, arg any) (any, error) {
	switch key {
	case "Ref":
		return s.ref(fmt.Sprint(arg))
	case "Condition":
		name := fmt.Sprint(arg)
		result, ok := s.Conditions[name]
		if !ok {
			return nil, fmt.Errorf("Condition %q is not defined", name)
		}
		return result, nil
	case "Fn::GetAtt":
		return s.getAtt(arg)
	case "Fn::Sub":
		return s.sub(arg)
	case "Fn::Join":
		return s.join(arg)
	case "Fn::Select":
		return s.selectIdx(arg)
	case "Fn::Split":
		return s.split(arg)
	case "Fn::FindInMap":
		return s.findInMap(arg)
	case "Fn::If":
		return s.ifFn(arg)
	case "Fn::Equals":
		return s.equals(arg)
	case "Fn::Not":
		return s.not(arg)
	case "Fn::And":
		return s.andOr(arg, true)
	case "Fn::Or":
		return s.andOr(arg, false)
	case "Fn::Base64":
		v, err := s.Eval(arg)
		if err != nil {
			return nil, err
		}
		return base64.StdEncoding.EncodeToString([]byte(fmt.Sprint(v))), nil
	case "Fn::ImportValue":
		v, err := s.Eval(arg)
		if err != nil {
			return nil, err
		}
		name := fmt.Sprint(v)
		if val, ok := s.Exports[name]; ok {
			return val, nil
		}
		return nil, fmt.Errorf("Fn::ImportValue %q: no such export (doze-aws has no cross-stack export registry)", name)
	case "Fn::GetAZs":
		// One region, one zone locally; returning the real shape keeps
		// templates that index into it working.
		return []any{awsident.Region + "a"}, nil
	case "Fn::ToJsonString":
		v, err := s.Eval(arg)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("Fn::ToJsonString: %w", err)
		}
		return string(raw), nil
	case "Fn::Length":
		v, err := s.Eval(arg)
		if err != nil {
			return nil, err
		}
		if list, ok := v.([]any); ok {
			return float64(len(list)), nil
		}
		return nil, fmt.Errorf("Fn::Length expects a list")
	}
	return nil, fmt.Errorf("unsupported intrinsic %s", key)
}

// ref resolves a parameter, pseudo-parameter or resource reference.
func (s *Scope) ref(name string) (any, error) {
	if name == "AWS::NoValue" {
		return noValue{}, nil
	}
	if v, ok := s.pseudo(name); ok {
		return v, nil
	}
	if v, ok := s.Parameters[name]; ok {
		return v, nil
	}
	if v, ok := s.Refs[name]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("Ref %q: no such parameter or resource", name)
}

func (s *Scope) getAtt(arg any) (any, error) {
	var logical, attr string
	switch t := arg.(type) {
	case []any:
		if len(t) < 2 {
			return nil, fmt.Errorf("Fn::GetAtt expects [logicalId, attribute]")
		}
		logical = fmt.Sprint(t[0])
		// A nested GetAtt attribute may itself be an intrinsic.
		ev, err := s.Eval(t[1])
		if err != nil {
			return nil, err
		}
		attr = fmt.Sprint(ev)
	case string:
		logical, attr, _ = strings.Cut(t, ".")
	default:
		return nil, fmt.Errorf("Fn::GetAtt expects a list or a dotted string")
	}

	atts, ok := s.Atts[logical]
	if !ok {
		return nil, fmt.Errorf("Fn::GetAtt %s.%s: no such resource", logical, attr)
	}
	v, ok := atts[attr]
	if !ok {
		return nil, fmt.Errorf("Fn::GetAtt %s.%s: doze-aws does not model that attribute (have: %s)",
			logical, attr, strings.Join(sortedKeys(atts), ", "))
	}
	return v, nil
}

// sub implements Fn::Sub, including the ${Logical.Attr} and ${!Literal} forms.
func (s *Scope) sub(arg any) (any, error) {
	var body string
	extra := map[string]any{}
	switch t := arg.(type) {
	case string:
		body = t
	case []any:
		if len(t) == 0 {
			return nil, fmt.Errorf("Fn::Sub expects a string or [string, vars]")
		}
		body = fmt.Sprint(t[0])
		if len(t) > 1 {
			vars, ok := t[1].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("Fn::Sub second argument must be a variable map")
			}
			for k, v := range vars {
				ev, err := s.Eval(v)
				if err != nil {
					return nil, err
				}
				extra[k] = ev
			}
		}
	default:
		return nil, fmt.Errorf("Fn::Sub expects a string or [string, vars]")
	}

	var out strings.Builder
	for i := 0; i < len(body); {
		if body[i] != '$' || i+1 >= len(body) || body[i+1] != '{' {
			out.WriteByte(body[i])
			i++
			continue
		}
		end := strings.IndexByte(body[i+2:], '}')
		if end < 0 {
			out.WriteString(body[i:])
			break
		}
		token := body[i+2 : i+2+end]
		i += 2 + end + 1

		// ${!Literal} escapes the substitution.
		if strings.HasPrefix(token, "!") {
			out.WriteString("${" + token[1:] + "}")
			continue
		}
		if v, ok := extra[token]; ok {
			out.WriteString(fmt.Sprint(v))
			continue
		}
		// ${Logical.Attr} is a GetAtt.
		if logical, attr, found := strings.Cut(token, "."); found {
			if _, isPseudo := s.pseudo(token); !isPseudo {
				v, err := s.getAtt([]any{logical, attr})
				if err != nil {
					return nil, fmt.Errorf("Fn::Sub ${%s}: %w", token, err)
				}
				out.WriteString(fmt.Sprint(v))
				continue
			}
		}
		v, err := s.ref(token)
		if err != nil {
			return nil, fmt.Errorf("Fn::Sub ${%s}: %w", token, err)
		}
		out.WriteString(fmt.Sprint(v))
	}
	return out.String(), nil
}

func (s *Scope) join(arg any) (any, error) {
	parts, ok := arg.([]any)
	if !ok || len(parts) != 2 {
		return nil, fmt.Errorf("Fn::Join expects [delimiter, list]")
	}
	sep := fmt.Sprint(parts[0])
	listVal, err := s.Eval(parts[1])
	if err != nil {
		return nil, err
	}
	list, ok := listVal.([]any)
	if !ok {
		return nil, fmt.Errorf("Fn::Join second argument must evaluate to a list")
	}
	pieces := make([]string, 0, len(list))
	for _, item := range list {
		pieces = append(pieces, fmt.Sprint(item))
	}
	return strings.Join(pieces, sep), nil
}

func (s *Scope) selectIdx(arg any) (any, error) {
	parts, ok := arg.([]any)
	if !ok || len(parts) != 2 {
		return nil, fmt.Errorf("Fn::Select expects [index, list]")
	}
	idxVal, err := s.Eval(parts[0])
	if err != nil {
		return nil, err
	}
	idx, err := toInt(idxVal)
	if err != nil {
		return nil, fmt.Errorf("Fn::Select index: %w", err)
	}
	listVal, err := s.Eval(parts[1])
	if err != nil {
		return nil, err
	}
	list, ok := listVal.([]any)
	if !ok {
		return nil, fmt.Errorf("Fn::Select second argument must evaluate to a list")
	}
	if idx < 0 || idx >= len(list) {
		return nil, fmt.Errorf("Fn::Select index %d is out of range (list has %d items)", idx, len(list))
	}
	return list[idx], nil
}

func (s *Scope) split(arg any) (any, error) {
	parts, ok := arg.([]any)
	if !ok || len(parts) != 2 {
		return nil, fmt.Errorf("Fn::Split expects [delimiter, string]")
	}
	sep := fmt.Sprint(parts[0])
	val, err := s.Eval(parts[1])
	if err != nil {
		return nil, err
	}
	pieces := strings.Split(fmt.Sprint(val), sep)
	out := make([]any, len(pieces))
	for i, p := range pieces {
		out[i] = p
	}
	return out, nil
}

func (s *Scope) findInMap(arg any) (any, error) {
	parts, ok := arg.([]any)
	if !ok || len(parts) < 3 {
		return nil, fmt.Errorf("Fn::FindInMap expects [mapName, topKey, secondKey]")
	}
	resolved := make([]string, 3)
	for i := range 3 {
		v, err := s.Eval(parts[i])
		if err != nil {
			return nil, err
		}
		resolved[i] = fmt.Sprint(v)
	}
	m, ok := s.Mappings[resolved[0]].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Fn::FindInMap: no mapping named %q", resolved[0])
	}
	top, ok := m[resolved[1]].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Fn::FindInMap: mapping %q has no key %q", resolved[0], resolved[1])
	}
	v, ok := top[resolved[2]]
	if !ok {
		return nil, fmt.Errorf("Fn::FindInMap: %s.%s has no key %q", resolved[0], resolved[1], resolved[2])
	}
	return v, nil
}

func (s *Scope) ifFn(arg any) (any, error) {
	parts, ok := arg.([]any)
	if !ok || len(parts) != 3 {
		return nil, fmt.Errorf("Fn::If expects [conditionName, ifTrue, ifFalse]")
	}
	name := fmt.Sprint(parts[0])
	result, ok := s.Conditions[name]
	if !ok {
		return nil, fmt.Errorf("Fn::If references undefined condition %q", name)
	}
	if result {
		return s.Eval(parts[1])
	}
	return s.Eval(parts[2])
}

func (s *Scope) equals(arg any) (any, error) {
	parts, ok := arg.([]any)
	if !ok || len(parts) != 2 {
		return nil, fmt.Errorf("Fn::Equals expects two values")
	}
	a, err := s.Eval(parts[0])
	if err != nil {
		return nil, err
	}
	b, err := s.Eval(parts[1])
	if err != nil {
		return nil, err
	}
	// CloudFormation compares stringified values, so 1 and "1" are equal.
	return fmt.Sprint(a) == fmt.Sprint(b), nil
}

func (s *Scope) not(arg any) (any, error) {
	parts, ok := arg.([]any)
	if !ok || len(parts) != 1 {
		return nil, fmt.Errorf("Fn::Not expects a single condition")
	}
	v, err := s.Eval(parts[0])
	if err != nil {
		return nil, err
	}
	b, err := toBool(v)
	if err != nil {
		return nil, fmt.Errorf("Fn::Not: %w", err)
	}
	return !b, nil
}

func (s *Scope) andOr(arg any, isAnd bool) (any, error) {
	parts, ok := arg.([]any)
	if !ok || len(parts) < 2 {
		return nil, fmt.Errorf("Fn::And/Fn::Or expect at least two conditions")
	}
	for _, p := range parts {
		v, err := s.Eval(p)
		if err != nil {
			return nil, err
		}
		b, err := toBool(v)
		if err != nil {
			return nil, err
		}
		if isAnd && !b {
			return false, nil
		}
		if !isAnd && b {
			return true, nil
		}
	}
	return isAnd, nil
}

// EvalConditions resolves the Conditions section. Conditions may reference one
// another, so it iterates to a fixed point rather than assuming declaration
// order — templates routinely define a condition before the one it builds on.
func (s *Scope) EvalConditions(conditions map[string]any) error {
	if s.Conditions == nil {
		s.Conditions = map[string]bool{}
	}
	remaining := make(map[string]any, len(conditions))
	for k, v := range conditions {
		remaining[k] = v
	}
	for len(remaining) > 0 {
		progressed := false
		var lastErr error
		for _, name := range sortedAnyKeys(remaining) {
			v, err := s.Eval(remaining[name])
			if err != nil {
				lastErr = fmt.Errorf("condition %s: %w", name, err)
				continue
			}
			b, err := toBool(v)
			if err != nil {
				lastErr = fmt.Errorf("condition %s: %w", name, err)
				continue
			}
			s.Conditions[name] = b
			delete(remaining, name)
			progressed = true
		}
		if !progressed {
			return lastErr
		}
	}
	return nil
}

// ---- coercion helpers ----

func toBool(v any) (bool, error) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case string:
		if b, err := strconv.ParseBool(t); err == nil {
			return b, nil
		}
	}
	return false, fmt.Errorf("expected a boolean, got %v", v)
}

func toInt(v any) (int, error) {
	switch t := v.(type) {
	case float64:
		return int(t), nil
	case int:
		return t, nil
	case string:
		n, err := strconv.Atoi(t)
		if err != nil {
			return 0, fmt.Errorf("%q is not a number", t)
		}
		return n, nil
	}
	return 0, fmt.Errorf("expected a number, got %v", v)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
