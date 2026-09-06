package sfngraph

import (
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/asl"
)

// The shapes a workflow takes, in one definition: a Pass into a Choice that
// fans out, a Parallel with two branches, a Map whose processor holds a
// nested Parallel, a Task with a Catch that loops back, and a Succeed.
const machine = `{
  "StartAt": "Prep",
  "States": {
    "Prep": {"Type": "Pass", "Next": "Route"},
    "Route": {"Type": "Choice", "Choices": [
        {"Variable": "$.total", "NumericGreaterThan": 1000, "Next": "Review"},
        {"Variable": "$.kind", "StringEquals": "gold", "Next": "Fan"}
      ], "Default": "Fan"},
    "Review": {"Type": "Task", "Resource": "arn:aws:states:::lambda:invoke", "Next": "Fan",
      "Catch": [{"ErrorEquals": ["States.Timeout", "States.TaskFailed"], "Next": "Prep"}]},
    "Fan": {"Type": "Parallel", "Branches": [
        {"StartAt": "Ship", "States": {"Ship": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:1:function:ship", "End": true}}},
        {"StartAt": "Bill", "States": {"Bill": {"Type": "Pass", "Next": "Notify"}, "Notify": {"Type": "Pass", "End": true}}}
      ], "Next": "Each"},
    "Each": {"Type": "Map", "ItemsPath": "$.items", "ItemProcessor": {
        "StartAt": "Inner", "States": {
          "Inner": {"Type": "Parallel", "Branches": [
            {"StartAt": "A", "States": {"A": {"Type": "Pass", "End": true}}},
            {"StartAt": "B", "States": {"B": {"Type": "Pass", "End": true}}}
          ], "End": true}}}, "Next": "Done"},
    "Done": {"Type": "Succeed"}
  }
}`

func parse(t *testing.T, src string) *asl.Definition {
	t.Helper()
	def, err := asl.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return def
}

func nodeByName(g *Graph, name string) *Node {
	for _, n := range g.Nodes {
		if n.Name == name {
			return n
		}
	}
	return nil
}

func TestLayoutPlacesEveryStateWithoutOverlap(t *testing.T) {
	g := Layout(parse(t, machine))
	for _, want := range []string{"Prep", "Route", "Review", "Fan", "Ship", "Bill", "Notify", "Each", "Inner", "A", "B", "Done"} {
		if nodeByName(g, want) == nil {
			t.Errorf("state %s has no node", want)
		}
	}
	// Siblings (same parent) must not overlap; a container encloses its
	// descendants, which is the one overlap that is the point.
	parent := func(n *Node) string {
		if i := strings.LastIndex(n.ID, "/"); i >= 0 {
			return n.ID[:i]
		}
		return ""
	}
	for i, a := range g.Nodes {
		for _, b := range g.Nodes[i+1:] {
			if parent(a) != parent(b) {
				continue
			}
			if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				t.Errorf("%s (%d,%d %dx%d) overlaps %s (%d,%d %dx%d)", a.ID, a.X, a.Y, a.W, a.H, b.ID, b.X, b.Y, b.W, b.H)
			}
		}
		if a.X < 0 || a.Y < 0 || a.X+a.W > g.Width || a.Y+a.H > g.Height {
			t.Errorf("%s at (%d,%d %dx%d) is outside the %dx%d graph", a.ID, a.X, a.Y, a.W, a.H, g.Width, g.Height)
		}
	}
}

func TestLayoutNestsContainers(t *testing.T) {
	g := Layout(parse(t, machine))
	inside := func(inner, outer *Node) bool {
		return inner.X >= outer.X && inner.Y >= outer.Y && inner.X+inner.W <= outer.X+outer.W && inner.Y+inner.H <= outer.Y+outer.H
	}
	fan, each := nodeByName(g, "Fan"), nodeByName(g, "Each")
	if !fan.Container || !each.Container {
		t.Fatalf("Parallel and Map should be containers: %+v %+v", fan, each)
	}
	for _, name := range []string{"Ship", "Bill", "Notify"} {
		if n := nodeByName(g, name); !inside(n, fan) || n.Depth != 1 {
			t.Errorf("%s should sit inside Fan at depth 1: %+v", name, n)
		}
	}
	inner := nodeByName(g, "Inner")
	if !inside(inner, each) || !inner.Container || inner.Depth != 1 {
		t.Errorf("Inner should be a container inside Each: %+v", inner)
	}
	for _, name := range []string{"A", "B"} {
		if n := nodeByName(g, name); !inside(n, inner) || n.Depth != 2 {
			t.Errorf("%s should sit inside Inner at depth 2: %+v", name, n)
		}
	}
	// Paint order: a container precedes what it holds.
	seen := map[string]int{}
	for i, n := range g.Nodes {
		seen[n.ID] = i
	}
	if seen["Fan"] > seen["Fan/1/Bill"] || seen["Each"] > seen["Each/0/Inner"] || seen["Each/0/Inner"] > seen["Each/0/Inner/1/B"] {
		t.Errorf("containers must precede their contents in Nodes: %v", seen)
	}
}

func TestLayoutEdges(t *testing.T) {
	g := Layout(parse(t, machine))
	ids := map[string]*Node{}
	for _, n := range g.Nodes {
		ids[n.ID] = n
	}
	find := func(from, to, kind string) *Edge {
		for _, e := range g.Edges {
			if e.From == from && e.To == to && e.Kind == kind {
				return e
			}
		}
		t.Fatalf("no %s edge %s → %s in %+v", kind, from, to, g.Edges)
		return nil
	}
	for _, e := range g.Edges {
		if ids[e.From] == nil || ids[e.To] == nil {
			t.Errorf("edge %s → %s references a missing node", e.From, e.To)
		}
		if len(e.Points) < 2 {
			t.Errorf("edge %s → %s has no path", e.From, e.To)
		}
		// Forward edges go down: the target sits in a lower rank.
		if !e.Back && ids[e.From] != nil && ids[e.To] != nil && ids[e.To].Y <= ids[e.From].Y {
			t.Errorf("forward edge %s → %s does not descend (%d → %d)", e.From, e.To, ids[e.From].Y, ids[e.To].Y)
		}
	}
	if e := find("Route", "Review", "choice"); e.Label != "$.total > 1000" {
		t.Errorf("choice label = %q", e.Label)
	}
	// Two rules to one target become one arrow carrying both labels.
	if e := find("Route", "Fan", "choice"); e.Label != `$.kind == "gold" | default` {
		t.Errorf("merged choice+default label = %q", e.Label)
	}
	for _, e := range g.Edges {
		if e.Kind == "default" {
			t.Errorf("the Default should have merged into the rule arrow: %+v", e)
		}
	}
	if e := find("Review", "Prep", "catch"); !e.Back || e.Label != "States.Timeout +1" {
		t.Errorf("catch back-edge = %+v", e)
	}
	find("Fan/1/Bill", "Fan/1/Notify", "next")
	find("Prep", "Route", "next")
	find("Each", "Done", "next")
}

// A Choice whose every branch leads back to itself, and a Catch to self:
// ranking must still terminate and every state still lands somewhere.
func TestLayoutCyclesTerminate(t *testing.T) {
	src := `{"StartAt":"Poll","States":{
	  "Poll":{"Type":"Task","Resource":"arn:aws:states:::lambda:invoke","Next":"Check",
	    "Catch":[{"ErrorEquals":["States.ALL"],"Next":"Poll"}]},
	  "Check":{"Type":"Choice","Choices":[{"Variable":"$.done","BooleanEquals":true,"Next":"Fin"}],"Default":"Wait"},
	  "Wait":{"Type":"Wait","Seconds":5,"Next":"Poll"},
	  "Fin":{"Type":"Succeed"}}}`
	g := Layout(parse(t, src))
	if len(g.Nodes) != 4 {
		t.Fatalf("want 4 nodes, got %d", len(g.Nodes))
	}
	backs := 0
	for _, e := range g.Edges {
		if e.Back {
			backs++
		}
	}
	if backs != 2 {
		t.Errorf("want the self-Catch and the Wait→Poll loop as back edges, got %d in %+v", backs, g.Edges)
	}
	if nodeByName(g, "Fin").Y <= nodeByName(g, "Check").Y {
		t.Errorf("Fin should sit below Check")
	}
	// Twice the same input, twice the same picture.
	again := Layout(parse(t, src))
	for i := range g.Nodes {
		if *g.Nodes[i] != *again.Nodes[i] {
			t.Errorf("layout is not deterministic: %+v vs %+v", g.Nodes[i], again.Nodes[i])
		}
	}
}

func TestLayoutEmpty(t *testing.T) {
	if g := Layout(nil); len(g.Nodes) != 0 || g.Width <= 0 {
		t.Errorf("nil definition should lay out as an empty graph: %+v", g)
	}
}
