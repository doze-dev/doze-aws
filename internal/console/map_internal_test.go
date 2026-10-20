package console

import (
	"strings"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
)

func mapNode(svc, name string) FlowNode {
	return FlowNode{ID: nodeID(svc, name), Svc: svc, Name: name, URL: "/" + svc + "/" + name}
}

func mapGraph(nodes []FlowNode, edges ...FlowEdge) FlowGraph {
	return collectGraph(func() map[string]*FlowNode {
		m := map[string]*FlowNode{}
		for i := range nodes {
			m[nodes[i].ID] = &nodes[i]
		}
		return m
	}(), edges)
}

func ids(ns []FlowNode) string {
	var s []string
	for _, n := range ns {
		s = append(s, n.ID)
	}
	return strings.Join(s, " ")
}

// A flow is a connected component, named for what feeds the rest and listed
// from it; what nothing is wired to is in no flow.
func TestFlowsAreComponentsNamedForTheirSource(t *testing.T) {
	bucket, q, fn, dlq := mapNode("s3", "statements"), mapNode("sqs", "points"), mapNode("lambda", "forecast"), mapNode("sqs", "points-dlq")
	rule := mapNode("eb", "default/nightly")
	topic, mail := mapNode("sns", "alerts"), mapNode("sqs", "mail")
	lonely, selfish := mapNode("s3", "assets"), mapNode("sqs", "ouroboros")
	g := mapGraph([]FlowNode{bucket, q, fn, dlq, rule, topic, mail, lonely, selfish},
		FlowEdge{From: bucket.ID, To: q.ID, Kind: "notify"},
		FlowEdge{From: q.ID, To: fn.ID, Kind: "esm"},
		FlowEdge{From: q.ID, To: dlq.ID, Kind: "redrive"},
		FlowEdge{From: rule.ID, To: fn.ID, Kind: "target"},
		FlowEdge{From: topic.ID, To: mail.ID, Kind: "sub"},
		FlowEdge{From: selfish.ID, To: selfish.ID, Kind: "redrive"},
	)
	fl := flowsOf(g, nil)
	if len(fl) != 2 {
		t.Fatalf("%d flows, want 2: %+v", len(fl), fl)
	}
	// The rule joins late and reaches less than the bucket: the bucket names the flow.
	if fl[0].Root.ID != bucket.ID || len(fl[0].Nodes) != 5 {
		t.Errorf("flow 1 is %q of %d, want statements of 5", fl[0].Root.ID, len(fl[0].Nodes))
	}
	if got, want := ids(fl[0].Nodes), "s3:statements sqs:points lambda:forecast sqs:points-dlq eb:default/nightly"; got != want {
		t.Errorf("flow 1 order\n got %s\nwant %s", got, want)
	}
	if fl[1].Root.ID != topic.ID || len(fl[1].Nodes) != 2 {
		t.Errorf("flow 2 is %q of %d, want alerts of 2", fl[1].Root.ID, len(fl[1].Nodes))
	}
	// A queue wired to itself is unwired (the graph's own rule), so in no flow.
	if wired := len(g.Nodes) - len(g.Unwired); wired != 7 {
		t.Errorf("%d wired, want 7", wired)
	}
	for _, f := range fl {
		if f.has(selfish.ID) || f.has(lonely.ID) {
			t.Errorf("an unwired resource is in a flow: %s", ids(f.Nodes))
		}
	}
}

// Two mappings between one pair are one wire, and the enabled one wins.
func TestFlowsKeepEachWireOnce(t *testing.T) {
	q, fn := mapNode("sqs", "q"), mapNode("lambda", "fn")
	g := mapGraph([]FlowNode{q, fn},
		FlowEdge{From: q.ID, To: fn.ID, Kind: "esm", Off: true},
		FlowEdge{From: q.ID, To: fn.ID, Kind: "esm", Off: false},
	)
	fl := flowsOf(g, nil)
	if len(fl) != 1 || len(fl[0].Edges) != 1 || fl[0].Edges[0].Off {
		t.Fatalf("%+v", fl)
	}
}

// Hot is a call to either end in the last minute, from the same ring the
// resource's wire dock reads; an older call, another resource's, and no
// recorder at all are not hot.
func TestAWireIsHotWhenACallNamedAnEndLately(t *testing.T) {
	bucket, q, fn := mapNode("s3", "up"), mapNode("sqs", "thumbs"), mapNode("lambda", "thumb")
	cold, other := mapNode("sns", "t"), mapNode("sqs", "m")
	nodes := []FlowNode{bucket, q, fn, cold, other}
	g := mapGraph(nodes,
		FlowEdge{From: bucket.ID, To: q.ID, Kind: "notify"},
		FlowEdge{From: q.ID, To: fn.ID, Kind: "esm"},
		FlowEdge{From: cold.ID, To: other.ID, Kind: "sub"},
	)
	c := &Console{}
	if got := c.mapTouched(g.Nodes, time.Now()); got != nil {
		t.Errorf("no recorder, yet %v", got)
	}
	rec := NewRecorder(nil, awsident.Default())
	c.rec = rec
	now := time.Now()
	rec.addAt(rec.reserve(), TrafficEntry{At: now.Add(-10 * time.Second), Service: "s3", Resource: "up/photo.jpg"})
	rec.addAt(rec.reserve(), TrafficEntry{At: now.Add(-2 * time.Minute), Service: "sns", Resource: "t"})
	rec.addAt(rec.reserve(), TrafficEntry{At: now, Service: "sqs", Resource: "elsewhere"})
	touched := c.mapTouched(g.Nodes, now)
	if len(touched) != 1 || !touched[bucket.ID] {
		t.Fatalf("touched %v, want only the bucket (a key under it names the bucket)", touched)
	}
	fl := flowsOf(g, touched)
	hot := map[string]bool{}
	for _, f := range fl {
		for _, e := range f.Edges {
			hot[e.From+">"+e.To] = e.Hot
		}
		if f.Root.ID == bucket.ID != f.Hot {
			t.Errorf("flow %s hot=%v", f.Root.ID, f.Hot)
		}
	}
	if !hot[bucket.ID+">"+q.ID] || hot[q.ID+">"+fn.ID] || hot[cold.ID+">"+other.ID] {
		t.Errorf("a wire is hot when an end was called, and only then: %v", hot)
	}
}

func boxAt(d mapDrawing, id string) mapBox {
	for _, b := range d.Boxes {
		if b.ID == id {
			return b
		}
	}
	return mapBox{}
}

// A column is how far a resource is from where the wiring starts, the longest
// way; nothing overlaps; every wire has a path and a label from its kinds.
func TestLayoutPutsEachResourceRightOfWhatFeedsIt(t *testing.T) {
	a, b, c, d := mapNode("s3", "a"), mapNode("sqs", "b"), mapNode("lambda", "c"), mapNode("sqs", "d")
	g := mapGraph([]FlowNode{a, b, c, d},
		FlowEdge{From: a.ID, To: b.ID, Kind: "notify"},
		FlowEdge{From: b.ID, To: c.ID, Kind: "esm"},
		FlowEdge{From: a.ID, To: c.ID, Kind: "notify"}, // a long wire, past b
		FlowEdge{From: c.ID, To: d.ID, Kind: "dlq"},
		FlowEdge{From: c.ID, To: d.ID, Kind: "dest"}, // the same pair: one wire
	)
	f := flowsOf(g, nil)[0]
	dr := layoutFlow(f)
	if len(dr.Boxes) != 4 || len(dr.Wires) != 4 {
		t.Fatalf("%d boxes %d wires, want 4 and 4", len(dr.Boxes), len(dr.Wires))
	}
	for _, e := range [][2]string{{a.ID, b.ID}, {b.ID, c.ID}, {a.ID, c.ID}, {c.ID, d.ID}} {
		if boxAt(dr, e[0]).X >= boxAt(dr, e[1]).X {
			t.Errorf("%s is not left of %s", e[0], e[1])
		}
	}
	if boxAt(dr, c.ID).X != boxAt(dr, a.ID).X+2*(boxW+colGap) {
		t.Errorf("c is not two columns on: %+v", boxAt(dr, c.ID))
	}
	seen := map[[2]int]string{}
	for _, bx := range dr.Boxes {
		if bx.X < 0 || bx.Y < 0 || bx.X+boxW > dr.W || bx.Y+boxH > dr.H {
			t.Errorf("%s is outside the %dx%d canvas: %+v", bx.ID, dr.W, dr.H, bx)
		}
		if o, ok := seen[[2]int{bx.X, bx.Y}]; ok {
			t.Errorf("%s and %s share a place", o, bx.ID)
		}
		seen[[2]int{bx.X, bx.Y}] = bx.ID
	}
	labels := ""
	for _, w := range dr.Wires {
		if !strings.HasPrefix(w.Path, "M") || w.Loop {
			t.Errorf("wire %q", w.Path)
		}
		labels += w.Label + ","
	}
	if labels != "notify,esm,notify,dlq dest," {
		t.Errorf("labels %q", labels)
	}
}

// A wire that runs back (a function whose failure destination is the queue
// that feeds it) goes underneath and still gets drawn; the layout ends.
func TestLayoutDrawsACycle(t *testing.T) {
	q, fn := mapNode("sqs", "q"), mapNode("lambda", "fn")
	g := mapGraph([]FlowNode{q, fn},
		FlowEdge{From: q.ID, To: fn.ID, Kind: "esm"},
		FlowEdge{From: fn.ID, To: q.ID, Kind: "dest"},
	)
	dr := layoutFlow(flowsOf(g, nil)[0])
	loops := 0
	for _, w := range dr.Wires {
		if w.Loop {
			loops++
			if w.Label != "dest" {
				t.Errorf("the wire that runs back is %q", w.Label)
			}
		}
	}
	if len(dr.Boxes) != 2 || len(dr.Wires) != 2 || loops != 1 {
		t.Errorf("%d boxes %d wires %d loops", len(dr.Boxes), len(dr.Wires), loops)
	}
	if boxAt(dr, q.ID).X >= boxAt(dr, fn.ID).X {
		t.Errorf("the queue that feeds is not left of the function it feeds")
	}
}

// A disabled mapping is wired and reads nothing: drawn dashed, and said.
func TestLayoutSaysWhenAMappingReadsNothing(t *testing.T) {
	q, fn := mapNode("sqs", "q"), mapNode("lambda", "fn")
	g := mapGraph([]FlowNode{q, fn}, FlowEdge{From: q.ID, To: fn.ID, Kind: "esm", Off: true})
	w := layoutFlow(flowsOf(g, nil)[0]).Wires[0]
	if w.Label != "esm off" || !w.Off {
		t.Errorf("%+v", w)
	}
}

// A topic with hundreds of queues is a list, not a picture: the drawing is
// bounded and says what it left out.
func TestLayoutIsBounded(t *testing.T) {
	topic := mapNode("sns", "fan")
	nodes := []FlowNode{topic}
	var edges []FlowEdge
	for i := 0; i < mapDrawCap+30; i++ {
		q := mapNode("sqs", "q"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		nodes = append(nodes, q)
		edges = append(edges, FlowEdge{From: topic.ID, To: q.ID, Kind: "sub"})
	}
	dr := layoutFlow(flowsOf(mapGraph(nodes, edges...), nil)[0])
	if len(dr.Boxes) != mapDrawCap || dr.Omitted != 31 || len(dr.Wires) != mapDrawCap-1 {
		t.Errorf("%d boxes, %d left out, %d wires", len(dr.Boxes), dr.Omitted, len(dr.Wires))
	}
}

// Two buses may each hold a rule called "orders": the rule says which.
func TestMapNamesARuleOnAnotherBus(t *testing.T) {
	d := FlowNode{ID: "eb:default/orders", Svc: "eb", Name: "orders"}
	o := FlowNode{ID: "eb:team/orders", Svc: "eb", Name: "orders"}
	bus := FlowNode{ID: "eb:bus/team", Svc: "eb", Name: "team"}
	if mapLabel(d) != "orders" || mapLabel(o) != "team/orders" || mapLabel(bus) != "team" {
		t.Errorf("%q %q %q", mapLabel(d), mapLabel(o), mapLabel(bus))
	}
	if mapWord(d) != "EB rule" || mapWord(bus) != "EB bus" {
		t.Errorf("%q %q", mapWord(d), mapWord(bus))
	}
	for svc, want := range map[string]string{"s3": "S3", "sqs": "SQS", "sns": "SNS", "lambda": "λ", "ddb": "DynamoDB table", "kinesis": "Kinesis stream", "sfn": "Step Functions"} {
		if got := mapWord(FlowNode{Svc: svc}); got != want {
			t.Errorf("%s is %q, want %q", svc, got, want)
		}
	}
}
