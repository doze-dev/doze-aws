package cloudformation

// Nested stacks: an AWS::CloudFormation::Stack resource names a child
// template by TemplateURL and passes it Parameters. The child is fetched,
// transpiled in its own scope with those parameters, its Outputs injected
// into the parent's scope as `Outputs.<Key>` attributes, and its resources
// merged into the parent's graph so one Apply provisions everything. Names
// derived from logical ids in the child are prefixed with the child's
// logical id, so two children with a `Queue` do not collide; explicit names
// are never rewritten. The child stack is named <parent>-<Logical>, with
// CDK's `NestedStackResource…` suffix stripped from the logical id.

import (
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

// NestedStack is one child stack a transpile produced.
type NestedStack struct {
	LogicalID    string
	Name         string
	TemplateURL  string
	TemplateBody string
	Parameters   map[string]string
	Report       *Report
	// Stack is the child's own graph, before merging into the parent's.
	Stack *provision.Stack
}

// maxNestingDepth bounds a chain of nested stacks.
const maxNestingDepth = 5

// nestedRefs transpiles each AWS::CloudFormation::Stack child in template
// order, so a later child can reference an earlier one's outputs, and
// records them on the report. It runs after pass one, when every parent
// resource's Ref and attributes are known.
func nestedRefs(scope *Scope, t *Template, opts TranspileOptions, rep *Report) error {
	for _, id := range t.Order() {
		r := t.Resources[id]
		if r.Type != "AWS::CloudFormation::Stack" {
			continue
		}
		if r.Condition != "" && !scope.Conditions[r.Condition] {
			continue
		}
		if opts.FetchTemplate == nil {
			return fmt.Errorf("resource %s: nested stacks need a template fetcher (TemplateURL %v)", id, r.Properties["TemplateURL"])
		}
		if opts.depth >= maxNestingDepth {
			return fmt.Errorf("resource %s: stacks nest more than %d deep", id, maxNestingDepth)
		}
		props, err := scope.Eval(r.Properties)
		if err != nil {
			return fmt.Errorf("resource %s: %w", id, err)
		}
		pm, _ := props.(map[string]any)
		url := propStr(pm, "TemplateURL")
		if url == "" {
			return fmt.Errorf("resource %s: TemplateURL is required", id)
		}
		for _, seen := range opts.ancestry {
			if seen == url {
				return fmt.Errorf("resource %s: %s nests itself", id, url)
			}
		}
		body, err := opts.FetchTemplate(url)
		if err != nil {
			return fmt.Errorf("resource %s: fetching %s: %w", id, url, err)
		}
		child, err := Parse(body)
		if err != nil {
			return fmt.Errorf("resource %s: %s: %w", id, url, err)
		}
		params := map[string]string{}
		for k, v := range propMap(pm, "Parameters") {
			params[k] = fmt.Sprint(v)
		}
		short := nestedShortName(id)
		childName := opts.StackName + "-" + short
		childStack, childRep, err := Transpile(child, TranspileOptions{
			StackName: childName, Parameters: params, Exports: opts.Exports, Identity: opts.Identity,
			AllowUnsupported: opts.AllowUnsupported, Endpoint: opts.Endpoint,
			FetchTemplate: opts.FetchTemplate, NamePrefix: opts.NamePrefix + short + "-",
			depth: opts.depth + 1, ancestry: append(append([]string(nil), opts.ancestry...), url),
		})
		if err != nil {
			return fmt.Errorf("nested stack %s (%s): %w", id, url, err)
		}
		arn := StackARN(scope.Identity, childName, "nested")
		scope.Refs[id] = arn
		atts := map[string]string{"Arn": arn, "StackId": arn}
		for k, v := range childRep.Outputs {
			atts["Outputs."+k] = v
		}
		scope.Atts[id] = atts
		for i := range rep.Entries {
			if rep.Entries[i].LogicalID == id {
				rep.Entries[i].Name = childName
			}
		}
		rep.Nested = append(rep.Nested, &NestedStack{
			LogicalID: id, Name: childName, TemplateURL: url, TemplateBody: string(body),
			Parameters: params, Report: childRep, Stack: childStack,
		})
	}
	return nil
}

// nestedShortName is the child's name segment: the logical id, with CDK's
// `…NestedStack…NestedStackResource…` bookkeeping stripped.
func nestedShortName(logical string) string {
	if i := strings.Index(logical, "NestedStack"); i > 0 {
		return logical[:i]
	}
	return logical
}

// mergeStacks adds every resource of src to dst; a name present in both is
// an error, since one Apply cannot create it twice with different shapes.
func mergeStacks(dst, src *provision.Stack, from string) error {
	if err := mergeMap(&dst.Queues, src.Queues, from, "queue"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Topics, src.Topics, from, "topic"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Buckets, src.Buckets, from, "bucket"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Tables, src.Tables, from, "table"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Functions, src.Functions, from, "function"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Rules, src.Rules, from, "rule"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Keys, src.Keys, from, "key"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Secrets, src.Secrets, from, "secret"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Parameters, src.Parameters, from, "parameter"); err != nil {
		return err
	}
	if err := mergeMap(&dst.APIs, src.APIs, from, "api"); err != nil {
		return err
	}
	if err := mergeMap(&dst.StateMachines, src.StateMachines, from, "state machine"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Activities, src.Activities, from, "activity"); err != nil {
		return err
	}
	if err := mergeMap(&dst.LogGroups, src.LogGroups, from, "log group"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Layers, src.Layers, from, "layer"); err != nil {
		return err
	}
	if err := mergeMap(&dst.Connections, src.Connections, from, "connection"); err != nil {
		return err
	}
	if err := mergeMap(&dst.APIDestinations, src.APIDestinations, from, "api destination"); err != nil {
		return err
	}
	if err := mergeMap(&dst.APIKeys, src.APIKeys, from, "api key"); err != nil {
		return err
	}
	return mergeMap(&dst.UsagePlans, src.UsagePlans, from, "usage plan")
}

func mergeMap[T any](dst *map[string]T, src map[string]T, from, kind string) error {
	if len(src) == 0 {
		return nil
	}
	if *dst == nil {
		*dst = map[string]T{}
	}
	for k, v := range src {
		if _, dup := (*dst)[k]; dup {
			return fmt.Errorf("nested stack %s: %s %q is also declared by the parent or a sibling", from, kind, k)
		}
		(*dst)[k] = v
	}
	return nil
}
