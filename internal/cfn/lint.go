package cfn

// References that point at nothing.
//
// CloudFormation refuses a template whose Ref or Fn::GetAtt names something
// the template does not declare, and it refuses it at the call —
// ValidateTemplate and CreateStack both answer with a ValidationError, before
// anything is created. Here such a reference was carried through as text: the
// template validated, the stack deployed, and a resource was configured with
// the literal name of a thing that did not exist.
//
// This looks only at what can be decided from the template alone. A template
// with a Transform is not checked: SAM's transform creates resources the
// author never declared (ServerlessRestApi, <Function>Role) and references to
// those are correct before the transform has run.

import (
	"fmt"
	"sort"
	"strings"
)

// CheckReferences returns the error CloudFormation gives for the first kind of
// dangling reference it finds, or nil.
func (t *Template) CheckReferences() error {
	if len(t.Transform) > 0 {
		return nil
	}
	known := func(name string) bool {
		if strings.HasPrefix(name, "AWS::") {
			return true
		}
		if _, ok := t.Parameters[name]; ok {
			return true
		}
		_, ok := t.Resources[name]
		return ok
	}
	isResource := func(name string) bool { _, ok := t.Resources[name]; return ok }

	scan := func(block string, values []any) error {
		refs := map[string]bool{}
		var getatt string
		var walk func(v any)
		walk = func(v any) {
			switch n := v.(type) {
			case map[string]any:
				if len(n) == 1 {
					if name, ok := n["Ref"].(string); ok && !known(name) {
						refs[name] = true
					}
					if target := getAttTarget(n["Fn::GetAtt"]); target != "" && !isResource(target) && getatt == "" {
						getatt = target
					}
				}
				for _, child := range n {
					walk(child)
				}
			case []any:
				for _, child := range n {
					walk(child)
				}
			}
		}
		for _, v := range values {
			walk(v)
		}
		if len(refs) > 0 {
			names := make([]string, 0, len(refs))
			for name := range refs {
				names = append(names, name)
			}
			sort.Strings(names)
			return fmt.Errorf("Template format error: Unresolved resource dependencies [%s] in the %s block of the template",
				strings.Join(names, ", "), block)
		}
		if getatt != "" {
			return fmt.Errorf("Template error: instance of Fn::GetAtt references undefined resource %s", getatt)
		}
		return nil
	}

	var resources []any
	for _, id := range t.order {
		r := t.Resources[id]
		if r == nil {
			continue
		}
		resources = append(resources, r.Properties)
		for _, dep := range r.DependsOn {
			if !isResource(dep) {
				return fmt.Errorf("Template format error: Unresolved resource dependencies [%s] in the Resources block of the template", dep)
			}
		}
	}
	if err := scan("Resources", resources); err != nil {
		return err
	}
	var outputs []any
	for _, o := range t.Outputs {
		outputs = append(outputs, o.Value)
	}
	return scan("Outputs", outputs)
}

// getAttTarget is the resource a Fn::GetAtt names, in either spelling:
// ["Queue", "Arn"] or "Queue.Arn". A target that is itself an intrinsic is
// not something this can judge, and comes back empty.
func getAttTarget(v any) string {
	switch n := v.(type) {
	case string:
		name, _, _ := strings.Cut(n, ".")
		return name
	case []any:
		if len(n) > 0 {
			if name, ok := n[0].(string); ok {
				return name
			}
		}
	}
	return ""
}
