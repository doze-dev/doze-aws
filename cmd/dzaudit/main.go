// Command dzaudit turns AWS's own service models into the validation checklist
// doze-aws is audited against.
//
// The problem it solves: a missing input check has no code smell. You cannot
// grep for validation that was never written, so the only way to find it
// systematically is to compare against an external statement of what AWS
// constrains. The Smithy models aws-sdk-go-v2 is generated from carry exactly
// that — @range, @length, @pattern, @enum, @required — for most services.
//
//	dzaudit list dynamodb            # every constrained input, by operation
//	dzaudit list --op CreateTable dynamodb
//	dzaudit summary lambda           # how much of the surface is constrained
//	dzaudit coverage                 # which services have a usable model
//
// A service id is the aws-models file name (dynamodb, secrets-manager,
// api-gateway). Models are fetched once into --cache.
//
// What this cannot tell you: cross-resource semantics. "The dead-letter target
// must exist" and "a FIFO queue needs a FIFO dead-letter queue" are not
// expressible as a range or a pattern, and no model states them. Those stay
// hand-derived — this clears the mechanical long tail so the slow reading goes
// where it is actually needed.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// maxDepth bounds the walk into nested input shapes. Inputs nest through lists
// and structures (KeySchema[].AttributeName), and a few models are recursive.
const maxDepth = 4

// finding is one constrained input, at the path a caller would set it.
type finding struct {
	Op         string
	Path       string
	Target     string
	Constraint constraint
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dzaudit [list|summary|coverage] [flags] <service>")
		os.Exit(2)
	}
	// The subcommand comes first, so flags are parsed from what follows it —
	// the stdlib parser stops at the first positional argument.
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cache := fs.String("cache", ".audit-models", "directory to cache fetched models in")
	op := fs.String("op", "", "restrict to one operation")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	if err := run(append([]string{cmd}, fs.Args()...), *cache, *op); err != nil {
		fmt.Fprintln(os.Stderr, "dzaudit:", err)
		os.Exit(1)
	}
}

func run(args []string, cache, opFilter string) error {
	cmd := args[0]
	if cmd == "coverage" {
		return coverage(cache)
	}
	if len(args) < 2 {
		return fmt.Errorf("%s needs a service (e.g. dynamodb)", cmd)
	}
	m, err := load(args[1], cache)
	if err != nil {
		return err
	}
	found := m.audit(opFilter)
	switch cmd {
	case "list":
		return list(m, found, opFilter)
	case "summary":
		return summary(m, found)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

// audit walks every operation's input and collects the constrained members.
func (m *model) audit(opFilter string) []finding {
	_, _, ops := m.service()
	sort.Strings(ops)
	var out []finding
	for _, opID := range ops {
		name := shortName(opID)
		if opFilter != "" && !strings.EqualFold(name, opFilter) {
			continue
		}
		op := m.Shapes[opID]
		if op.Input == nil {
			continue
		}
		m.walk(name, op.Input.Target, "", 0, map[string]bool{}, &out)
	}
	return out
}

// walk descends an input shape, recording every constraint it finds and the
// path a caller would set it at.
func (m *model) walk(op, shapeID, path string, depth int, seen map[string]bool, out *[]finding) {
	if depth > maxDepth || seen[shapeID+"@"+path] {
		return
	}
	seen[shapeID+"@"+path] = true

	s, ok := m.Shapes[shapeID]
	if !ok {
		return
	}
	switch s.Type {
	case "structure":
		for name, mem := range s.Members {
			child := join(path, name)
			// @required belongs to the member; the value constraints belong to
			// the shape it targets.
			for _, c := range constraintsOf(mem.Traits) {
				if c.Kind == "required" {
					*out = append(*out, finding{op, child, shortName(mem.Target), c})
				}
			}
			m.walk(op, mem.Target, child, depth+1, seen, out)
		}
	case "list", "set":
		if s.Member != nil {
			m.walk(op, s.Member.Target, path+"[]", depth+1, seen, out)
		}
	case "map":
		if s.Value != nil {
			m.walk(op, s.Value.Target, path+"{}", depth+1, seen, out)
		}
	case "enum":
		if vals := m.enumValues(s); len(vals) > 0 {
			*out = append(*out, finding{op, path, shortName(shapeID),
				constraint{Kind: "enum", Values: vals}})
		}
	default: // string, integer, long, float, blob …
		for _, c := range constraintsOf(s.Traits) {
			*out = append(*out, finding{op, path, shortName(shapeID), c})
		}
	}
}

func list(m *model, found []finding, opFilter string) error {
	id, proto, ops := m.service()
	fmt.Printf("%s  (%s, %d operations)\n\n", shortName(id), proto, len(ops))
	if len(found) == 0 {
		fmt.Println("no constraint traits in this model — this service needs the manual track")
		return nil
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Op != found[j].Op {
			return found[i].Op < found[j].Op
		}
		return found[i].Path < found[j].Path
	})
	cur := ""
	for _, f := range found {
		if f.Op != cur {
			cur = f.Op
			fmt.Printf("\n%s\n", cur)
		}
		fmt.Printf("  %-52s %s\n", f.Path, f.Constraint)
	}
	fmt.Printf("\n%d constrained inputs across %d operations\n", len(found), countOps(found))
	return nil
}

func summary(m *model, found []finding) error {
	id, proto, ops := m.service()
	byKind := map[string]int{}
	for _, f := range found {
		byKind[f.Constraint.Kind]++
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	fmt.Printf("%-16s %s\n", "service", shortName(id))
	fmt.Printf("%-16s %s\n", "protocol", proto)
	fmt.Printf("%-16s %d\n", "operations", len(ops))
	fmt.Printf("%-16s %d (of %d operations)\n", "constrained", len(found), countOps(found))
	for _, k := range kinds {
		fmt.Printf("  %-14s %d\n", k, byKind[k])
	}
	// A rough sense of how many rejection cases this yields: a range or length
	// gives a low and a high case, everything else gives one.
	cases := 0
	for _, f := range found {
		if f.Constraint.Kind == "range" || f.Constraint.Kind == "length" {
			cases += 2
			continue
		}
		cases++
	}
	fmt.Printf("%-16s ~%d\n", "rejection cases", cases)
	return nil
}

// coverage reports which services carry a model worth generating from — the
// split between the mechanical track and the hand-derived one.
func coverage(cache string) error {
	services := []string{
		"sqs", "sns", "s3", "dynamodb", "lambda", "kinesis", "kms", "iam",
		"ssm", "secrets-manager", "eventbridge", "cloudformation",
		"api-gateway", "sts",
	}
	fmt.Printf("%-18s %8s %8s %8s\n", "service", "inputs", "ops", "track")
	for _, s := range services {
		m, err := load(s, cache)
		if err != nil {
			fmt.Printf("%-18s %8s\n", s, "-")
			continue
		}
		found := m.audit("")
		_, _, ops := m.service()
		track := "A · generate"
		if len(found) < 20 {
			track = "B · by hand"
		}
		fmt.Printf("%-18s %8d %8d   %s\n", s, len(found), len(ops), track)
	}
	return nil
}

func countOps(found []finding) int {
	seen := map[string]bool{}
	for _, f := range found {
		seen[f.Op] = true
	}
	return len(seen)
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// shortName drops the com.amazonaws.<service># namespace prefix.
func shortName(id string) string {
	if i := strings.LastIndex(id, "#"); i >= 0 {
		return id[i+1:]
	}
	return id
}
