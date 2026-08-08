// Package cloudformation turns CloudFormation and SAM templates into the
// declarative stack doze-aws already knows how to converge.
//
// The design choice that makes this small: doze-aws does not need a second
// provisioning engine. The stackfile package already separates parsing from
// convergence — it has an intermediate representation, a dependency-ordered
// phase walk, and a create-or-update apply that speaks the real wire protocols.
// A CloudFormation template is therefore a *front end*: parse it, evaluate its
// intrinsics, map its resources onto that IR, and hand it to the existing
// apply. Everything downstream is reused.
//
// # What it does not pretend to be
//
// This is a transpiler, not the CloudFormation service. It has no stack state,
// no change sets, no rollback, and it never deletes. Resources doze-aws cannot
// model — IAM roles, log groups, and anything outside the implemented services
// — are reported as skipped rather than silently swallowed, because a template
// that half-deployed in silence is worse than one that said so.
package cloudformation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Template is a parsed CloudFormation template. Property values stay as
// generic trees because they contain unevaluated intrinsic functions; the
// evaluator in intrinsics.go resolves them once parameters and conditions are
// known.
type Template struct {
	FormatVersion string
	Description   string
	// Transform names the macros a template declares. Only the SAM transform
	// is understood; anything else is reported.
	Transform  []string
	Parameters map[string]Parameter
	Mappings   map[string]any
	Conditions map[string]any
	// Globals is SAM's per-type default block.
	Globals   map[string]any
	Resources map[string]*Resource
	Outputs   map[string]Output
	// order is the resource logical IDs in template order, so reporting and
	// tie-breaking are deterministic rather than map-random.
	order []string
}

// Parameter is one template parameter declaration.
type Parameter struct {
	Type          string
	Default       any
	Description   string
	AllowedValues []any
	NoEcho        bool
}

// Resource is one template resource.
type Resource struct {
	LogicalID      string
	Type           string
	Properties     map[string]any
	DependsOn      []string
	Condition      string
	DeletionPolicy string
	Metadata       map[string]any
}

// Output is one template output.
type Output struct {
	Description string
	Value       any
	Condition   string
	ExportName  any
}

// Parse reads a CloudFormation template in either JSON or YAML.
//
// YAML short-form tags (!Ref, !GetAtt, !Sub, …) are normalised to their long
// form during parsing, so the evaluator only ever sees one shape. This is the
// step most hand-rolled parsers skip, and it is why they fail on real
// templates — almost nobody writes `Fn::GetAtt` longhand in YAML.
func Parse(raw []byte) (*Template, error) {
	tree, err := decodeTree(raw)
	if err != nil {
		return nil, err
	}
	root, ok := tree.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("template must be a mapping at the top level")
	}
	t := &Template{
		FormatVersion: str(root["AWSTemplateFormatVersion"]),
		Description:   str(root["Description"]),
		Parameters:    map[string]Parameter{},
		Mappings:      map[string]any{},
		Conditions:    map[string]any{},
		Resources:     map[string]*Resource{},
		Outputs:       map[string]Output{},
	}

	switch v := root["Transform"].(type) {
	case string:
		t.Transform = []string{v}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				t.Transform = append(t.Transform, s)
			}
		}
	}

	if params, ok := root["Parameters"].(map[string]any); ok {
		for name, raw := range params {
			decl, _ := raw.(map[string]any)
			p := Parameter{
				Type:        str(decl["Type"]),
				Default:     decl["Default"],
				Description: str(decl["Description"]),
				NoEcho:      decl["NoEcho"] == true || str(decl["NoEcho"]) == "true",
			}
			if allowed, ok := decl["AllowedValues"].([]any); ok {
				p.AllowedValues = allowed
			}
			t.Parameters[name] = p
		}
	}
	if m, ok := root["Mappings"].(map[string]any); ok {
		t.Mappings = m
	}
	if c, ok := root["Conditions"].(map[string]any); ok {
		t.Conditions = c
	}
	if g, ok := root["Globals"].(map[string]any); ok {
		t.Globals = g
	}

	resources, ok := root["Resources"].(map[string]any)
	if !ok || len(resources) == 0 {
		return nil, fmt.Errorf("template has no Resources section")
	}
	// Map iteration is random, so recover template order from the raw bytes to
	// keep reports and dependency tie-breaks stable across runs.
	t.order = resourceOrder(raw, resources)
	for _, id := range t.order {
		decl, ok := resources[id].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("resource %s is not a mapping", id)
		}
		typ := str(decl["Type"])
		if typ == "" {
			return nil, fmt.Errorf("resource %s has no Type", id)
		}
		r := &Resource{
			LogicalID:      id,
			Type:           typ,
			Condition:      str(decl["Condition"]),
			DeletionPolicy: str(decl["DeletionPolicy"]),
		}
		if props, ok := decl["Properties"].(map[string]any); ok {
			r.Properties = props
		} else {
			r.Properties = map[string]any{}
		}
		if meta, ok := decl["Metadata"].(map[string]any); ok {
			r.Metadata = meta
		}
		switch d := decl["DependsOn"].(type) {
		case string:
			r.DependsOn = []string{d}
		case []any:
			for _, item := range d {
				if s, ok := item.(string); ok {
					r.DependsOn = append(r.DependsOn, s)
				}
			}
		}
		t.Resources[id] = r
	}

	if outputs, ok := root["Outputs"].(map[string]any); ok {
		for name, raw := range outputs {
			decl, _ := raw.(map[string]any)
			o := Output{
				Description: str(decl["Description"]),
				Value:       decl["Value"],
				Condition:   str(decl["Condition"]),
			}
			if export, ok := decl["Export"].(map[string]any); ok {
				o.ExportName = export["Name"]
			}
			t.Outputs[name] = o
		}
	}
	return t, nil
}

// IsSAM reports whether the template declares the SAM transform.
func (t *Template) IsSAM() bool {
	for _, tr := range t.Transform {
		if strings.HasPrefix(tr, "AWS::Serverless-") {
			return true
		}
	}
	return false
}

// Order returns resource logical IDs in template order.
func (t *Template) Order() []string { return t.order }

// resourceOrder recovers declaration order by scanning the raw template for
// each logical ID. It is a heuristic — but a wrong order only affects report
// ordering and tie-breaks, never correctness, since real dependencies are
// resolved separately.
func resourceOrder(raw []byte, resources map[string]any) []string {
	type pos struct {
		id  string
		idx int
	}
	found := make([]pos, 0, len(resources))
	for id := range resources {
		// Match the key as it appears in JSON ("Id":) or YAML (Id:).
		idx := bytes.Index(raw, []byte(`"`+id+`"`))
		if idx < 0 {
			idx = bytes.Index(raw, []byte("\n  "+id+":"))
		}
		if idx < 0 {
			idx = bytes.Index(raw, []byte(id+":"))
		}
		if idx < 0 {
			idx = 1 << 30 // unfound entries sort last, then alphabetically
		}
		found = append(found, pos{id, idx})
	}
	for i := 1; i < len(found); i++ {
		for j := i; j > 0; j-- {
			a, b := found[j-1], found[j]
			if a.idx < b.idx || (a.idx == b.idx && a.id <= b.id) {
				break
			}
			found[j-1], found[j] = b, a
		}
	}
	out := make([]string, 0, len(found))
	for _, p := range found {
		out = append(out, p.id)
	}
	return out
}

// ---- decoding ----

// decodeTree parses JSON or YAML into a generic tree, normalising YAML
// short-form intrinsic tags to their long form.
func decodeTree(raw []byte) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("template is empty")
	}
	// A leading brace means JSON. YAML is a superset, but json.Unmarshal gives
	// better errors and does not need the tag walk.
	if trimmed[0] == '{' {
		var tree any
		if err := json.Unmarshal(trimmed, &tree); err != nil {
			return nil, fmt.Errorf("template is not valid JSON: %w", err)
		}
		return tree, nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, fmt.Errorf("template is not valid YAML: %w", err)
	}
	if len(node.Content) == 0 {
		return nil, fmt.Errorf("template is empty")
	}
	return yamlToAny(node.Content[0])
}

// shortTags maps YAML short-form intrinsic tags to the object key they stand
// for. `!Ref` and `!Condition` are bare keys; everything else is Fn::-prefixed.
var shortTags = map[string]string{
	"!Ref":          "Ref",
	"!Condition":    "Condition",
	"!Base64":       "Fn::Base64",
	"!Cidr":         "Fn::Cidr",
	"!FindInMap":    "Fn::FindInMap",
	"!GetAtt":       "Fn::GetAtt",
	"!GetAZs":       "Fn::GetAZs",
	"!ImportValue":  "Fn::ImportValue",
	"!Join":         "Fn::Join",
	"!Select":       "Fn::Select",
	"!Split":        "Fn::Split",
	"!Sub":          "Fn::Sub",
	"!Transform":    "Fn::Transform",
	"!And":          "Fn::And",
	"!Equals":       "Fn::Equals",
	"!If":           "Fn::If",
	"!Not":          "Fn::Not",
	"!Or":           "Fn::Or",
	"!ToJsonString": "Fn::ToJsonString",
	"!Length":       "Fn::Length",
}

// yamlToAny converts a yaml.Node tree into generic Go values, expanding
// short-form intrinsic tags into the single-key objects the evaluator expects.
func yamlToAny(n *yaml.Node) (any, error) {
	// A short-form tag wraps whatever follows it.
	if key, ok := shortTags[n.Tag]; ok {
		inner, err := yamlToAnyUntagged(n)
		if err != nil {
			return nil, err
		}
		// !GetAtt Foo.Bar is the dotted spelling of ["Foo","Bar"].
		if key == "Fn::GetAtt" {
			if s, ok := inner.(string); ok {
				inner = strings.SplitN(s, ".", 2)
				parts := inner.([]string)
				anyParts := make([]any, len(parts))
				for i, p := range parts {
					anyParts[i] = p
				}
				inner = anyParts
			}
		}
		return map[string]any{key: inner}, nil
	}

	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return yamlToAny(n.Content[0])
	case yaml.MappingNode:
		out := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, err := yamlToAny(n.Content[i])
			if err != nil {
				return nil, err
			}
			v, err := yamlToAny(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[fmt.Sprint(k)] = v
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			v, err := yamlToAny(item)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.AliasNode:
		return yamlToAny(n.Alias)
	}
	return scalarValue(n)
}

// yamlToAnyUntagged converts a node while ignoring its own short-form tag,
// used for the payload of a tag.
func yamlToAnyUntagged(n *yaml.Node) (any, error) {
	clone := *n
	switch n.Kind {
	case yaml.ScalarNode:
		clone.Tag = "!!str"
	case yaml.SequenceNode:
		clone.Tag = "!!seq"
	case yaml.MappingNode:
		clone.Tag = "!!map"
	}
	return yamlToAny(&clone)
}

// scalarValue decodes a YAML scalar, preserving numbers and booleans so
// intrinsic comparisons behave the way they do in JSON templates.
func scalarValue(n *yaml.Node) (any, error) {
	switch n.Tag {
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err == nil {
			return b, nil
		}
	case "!!int":
		var i int64
		if err := n.Decode(&i); err == nil {
			return float64(i), nil // match encoding/json's number type
		}
	case "!!float":
		var f float64
		if err := n.Decode(&f); err == nil {
			return f, nil
		}
	}
	return n.Value, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// LooksLikeTemplate reports whether raw is a CloudFormation or SAM template
// rather than a doze stack file.
//
// The two are told apart by content, not filename: a template's Resources
// section maps logical IDs to objects carrying a `Type`, and a stack file has
// no Resources section at all. A Transform line settles it immediately.
func LooksLikeTemplate(raw []byte) bool {
	tree, err := decodeTree(raw)
	if err != nil {
		return false
	}
	root, ok := tree.(map[string]any)
	if !ok {
		return false
	}
	if _, ok := root["AWSTemplateFormatVersion"]; ok {
		return true
	}
	if _, ok := root["Transform"]; ok {
		return true
	}
	resources, ok := root["Resources"].(map[string]any)
	if !ok || len(resources) == 0 {
		return false
	}
	for _, v := range resources {
		decl, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if _, hasType := decl["Type"]; !hasType {
			return false
		}
	}
	return true
}
