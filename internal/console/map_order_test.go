package console

import "testing"

// What the work leads to is named before where the failures go, whatever the
// names say: a queue's card lists its function, then its dead-letter queue.
func TestACardNamesTheWorkBeforeTheFailures(t *testing.T) {
	c := &Console{prefix: "/_console"}
	q, dlq, fn := mapNode("sqs", "points"), mapNode("sqs", "a-dlq"), mapNode("lambda", "z-forecast")
	g := mapGraph([]FlowNode{q, dlq, fn},
		FlowEdge{From: q.ID, To: dlq.ID, Kind: "redrive"},
		FlowEdge{From: q.ID, To: fn.ID, Kind: "esm"},
	)
	card := c.cardOf(flowsOf(g, nil)[0])
	want := []mapLine{{"SQS", "points", false}, {"λ", "z-forecast", true}, {"SQS", "a-dlq", true}}
	for i, l := range card.Lines {
		if l != want[i] {
			t.Errorf("line %d is %+v, want %+v", i, l, want[i])
		}
	}
}
