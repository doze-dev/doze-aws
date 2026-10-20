package console

import "testing"

// A card names the first three resources: the source, then what it leads to
// with an arrow. Another source that joins the flow has no arrow to it.
func TestACardNamesTheFirstResources(t *testing.T) {
	c := &Console{prefix: "/_console"}
	bucket, q, fn, dlq := mapNode("s3", "statements"), mapNode("sqs", "points"), mapNode("lambda", "forecast"), mapNode("sqs", "points-dlq")
	rule := mapNode("eb", "default/nightly")
	g := mapGraph([]FlowNode{bucket, q, fn, dlq, rule},
		FlowEdge{From: bucket.ID, To: q.ID, Kind: "notify"},
		FlowEdge{From: q.ID, To: fn.ID, Kind: "esm"},
		FlowEdge{From: q.ID, To: dlq.ID, Kind: "redrive"},
		FlowEdge{From: rule.ID, To: fn.ID, Kind: "target"},
	)
	card := c.cardOf(flowsOf(g, nil)[0])
	if card.Title != "from statements" || card.Count != "5 resources" || card.More != 2 || card.URL != "/_console/map?flow=s3%3Astatements" {
		t.Errorf("%+v", card)
	}
	want := []mapLine{{"S3", "statements", false}, {"SQS", "points", true}, {"λ", "forecast", true}}
	for i, l := range card.Lines {
		if l != want[i] {
			t.Errorf("line %d is %+v, want %+v", i, l, want[i])
		}
	}

	stream, table, fn2 := mapNode("kinesis", "clicks"), mapNode("ddb", "sessions"), mapNode("lambda", "sessionise")
	g = mapGraph([]FlowNode{stream, table, fn2},
		FlowEdge{From: stream.ID, To: fn2.ID, Kind: "esm"},
		FlowEdge{From: table.ID, To: fn2.ID, Kind: "esm"},
	)
	card = c.cardOf(flowsOf(g, nil)[0])
	want = []mapLine{{"Kinesis stream", "clicks", false}, {"λ", "sessionise", true}, {"DynamoDB table", "sessions", false}}
	for i, l := range card.Lines {
		if l != want[i] {
			t.Errorf("line %d is %+v, want %+v", i, l, want[i])
		}
	}
}
