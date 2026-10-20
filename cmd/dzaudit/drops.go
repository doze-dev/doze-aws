package main

// What the case generator leaves out, said out loud.
//
// A constraint with no case is a rule nobody tests, and a generator that drops
// one in silence makes the parity count read "everything is covered" when it
// is not (Logs found its tag-key rule unenforced on write that way, with the
// count saying every constraint was). So everything it does not build, whole or
// in part, is named: the operation, the member, the rule, and why no case could
// be made that fails for that rule alone. The list goes to stderr, in a stable
// order, so the drift job's log carries it and a diff of two runs shows a rule
// gaining or losing its case.

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// diagnostics is where the list goes: stderr, so stdout stays the fixture.
var diagnostics io.Writer = os.Stderr

type drop struct {
	Op, Path, Rule, Why string
}

type drops struct {
	list []drop
	cut  []string // members not walked: nested deeper than maxDepth
}

func (d *drops) add(f finding, why string) {
	d.list = append(d.list, drop{f.Op, f.Path, f.Constraint.String(), why})
}

// report writes the list. Nothing is written when nothing was dropped.
func (d *drops) report(w io.Writer) {
	if len(d.list) > 0 {
		sort.Slice(d.list, func(i, j int) bool {
			a, b := d.list[i], d.list[j]
			if a.Op != b.Op {
				return a.Op < b.Op
			}
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			if a.Rule != b.Rule {
				return a.Rule < b.Rule
			}
			return a.Why < b.Why
		})
		fmt.Fprintf(w, "dzaudit: %d constraint(s) have no case, or only part of one:\n", len(d.list))
		for _, x := range d.list {
			fmt.Fprintf(w, "  %s %s [%s]: %s\n", x.Op, x.Path, x.Rule, x.Why)
		}
	}
	if len(d.cut) > 0 {
		cut := append([]string(nil), d.cut...)
		sort.Strings(cut)
		var uniq []string
		for _, c := range cut {
			if len(uniq) == 0 || uniq[len(uniq)-1] != c {
				uniq = append(uniq, c)
			}
		}
		fmt.Fprintf(w, "dzaudit: %d member(s) nest deeper than %d levels and were not looked into:\n", len(uniq), maxDepth)
		for _, c := range uniq {
			fmt.Fprintf(w, "  %s\n", c)
		}
	}
}

// noContainerCase says why a rule about a list, a map's size or a map's key got
// no case at all (containerViolations answered not-ok).
func noContainerCase(c constraint) string {
	switch {
	case !c.HasSample:
		return "no case: the element is itself constrained, or is a structure, so nothing is valid on its own to build the container around"
	case c.Kind != "length":
		return "no case: the rule on a " + c.Container + " that is not a length is not one a case can be built for"
	}
	return "no case: there is no bound a case could cross, or the bound is above the cap of " + fmt.Sprint(maxContainerCase)
}

// partialContainerCase lists the parts of a container rule that were left out
// where the rest was built.
func partialContainerCase(c constraint) []string {
	var out []string
	switch c.Container {
	case "list", "map":
		if c.Max != nil && *c.Max+1 > maxContainerCase {
			out = append(out, fmt.Sprintf("the maximum of %s is above the cap of %d: its case is not built (the minimum's stands)", trimNum(*c.Max), maxContainerCase))
		}
	case "mapKey":
		inner := c
		inner.Container = ""
		for _, viol := range violations(inner) {
			if key, ok := viol.Value.(string); !ok || len(key) > 512 {
				out = append(out, "a key that breaks the rule is too long to write out: that case is not built ("+strings.TrimPrefix(viol.Why, "the value ")+")")
			}
		}
	}
	return out
}

// noScalarCase says why a rule on a plain value produced no violating value.
func noScalarCase(c constraint) string {
	if c.Kind == "pattern" {
		if _, err := regexp.Compile(c.Pattern); err != nil {
			return "the pattern does not compile in Go's regexp, so no value can be derived from it"
		}
		return "no candidate value on hand breaks the pattern; emitting one anyway would report a valid value as an accepted violation"
	}
	return "the rule has no bound a value could cross"
}
