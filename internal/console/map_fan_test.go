package console

import (
	"regexp"
	"testing"
)

// A source with several targets below it: the wire to the lowest turns first
// (leftmost), so no wire's vertical crosses the last stretch of the one above
// it, where that one's label sits.
func TestAFanOutTurnsLowestFirst(t *testing.T) {
	rule := mapNode("eb", "default/orders")
	var nodes = []FlowNode{rule}
	var edges []FlowEdge
	for _, n := range []string{"a", "b", "c", "d"} {
		q := mapNode("sqs", n)
		nodes = append(nodes, q)
		edges = append(edges, FlowEdge{From: rule.ID, To: q.ID, Kind: "target"})
	}
	dr := layoutFlow(flowsOf(mapGraph(nodes, edges...), nil)[0])
	re := regexp.MustCompile(`^M\d+ \d+H(\d+)(?:V(\d+))?H\d+$`)
	type w struct{ trunk, to int }
	var ws []w
	for _, wire := range dr.Wires {
		m := re.FindStringSubmatch(wire.Path)
		if m == nil {
			t.Fatalf("path %q", wire.Path)
		}
		ws = append(ws, w{atoiOr0(m[1]), atoiOr0(m[2])})
	}
	for i := range ws {
		for j := range ws {
			if ws[i].to > ws[j].to && ws[i].trunk >= ws[j].trunk {
				t.Errorf("the wire to y=%d turns at %d, not left of the wire to y=%d at %d", ws[i].to, ws[i].trunk, ws[j].to, ws[j].trunk)
			}
		}
	}
}
