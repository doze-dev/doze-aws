package console

import (
	"regexp"
	"testing"
)

// Two wires that rise through one gap turn at their own x, so they read as two
// lines and not as one: the picture this stops is a queue's redrive and a
// second queue's redrive drawn as a single line from the lower to the higher.
func TestWiresThroughOneGapTurnApart(t *testing.T) {
	rule := mapNode("eb", "default/orders")
	a, b, c := mapNode("sqs", "a"), mapNode("sqs", "b"), mapNode("sqs", "c")
	adlq, bdlq := mapNode("sqs", "a-dlq"), mapNode("sqs", "b-dlq")
	g := mapGraph([]FlowNode{rule, a, b, c, adlq, bdlq},
		FlowEdge{From: rule.ID, To: a.ID, Kind: "target"},
		FlowEdge{From: rule.ID, To: b.ID, Kind: "target"},
		FlowEdge{From: rule.ID, To: c.ID, Kind: "target"},
		FlowEdge{From: a.ID, To: adlq.ID, Kind: "redrive"},
		FlowEdge{From: b.ID, To: bdlq.ID, Kind: "redrive"},
	)
	dr := layoutFlow(flowsOf(g, nil)[0])
	turn := regexp.MustCompile(`^M(\d+) \d+H(\d+)`)
	seen := map[[2]int]string{}
	for _, w := range dr.Wires {
		m := turn.FindStringSubmatch(w.Path)
		if m == nil {
			t.Fatalf("path %q", w.Path)
		}
		key := [2]int{atoiOr0(m[1]), atoiOr0(m[2])} // the column it leaves, where it turns
		if o, ok := seen[key]; ok {
			t.Errorf("%q and %q turn at the same x (%d) in one gap", o, w.Label+" "+w.Path, key[1])
		}
		seen[key] = w.Label + " " + w.Path
		if x1 := key[0]; key[1] <= x1 || key[1] >= x1+colGap {
			t.Errorf("%q turns at %d, outside the gap after %d", w.Path, key[1], x1)
		}
	}
	if len(seen) != 5 {
		t.Errorf("%d wires, want 5", len(seen))
	}
}

func atoiOr0(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
