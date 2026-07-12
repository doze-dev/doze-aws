package console

import (
	"context"
	"encoding/xml"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// ---- flow graph: the live wiring map ----

// Graph is the set of resources (nodes) and the real connections between them
// (edges) — subscriptions, notifications, targets, event-source mappings, and
// redrive policies. Laid out by service column in the template.

type FlowNode struct {
	ID      string // stable: "sqs:orders"
	Svc     string // s3 | sns | sqs | eb | lambda
	Name    string
	Sub     string // small caption ("2 msgs", "1 sub")
	X, Y    int    // absolute layout position (px), assigned per flow band
	Unwired bool   // no edges touch it
	URL     string
}

// FlowDiagram is one independent connected flow, rendered as its own card with
// a self-contained SVG (local coordinates).
type FlowDiagram struct {
	Label string
	Nodes []FlowNode // local coordinates within this card's SVG
	Edges []FlowEdge // edges internal to this flow
	W, H  int
}

type FlowEdge struct {
	From string
	To   string
	Kind string // notify | sub | target | esm | redrive
	Hot  bool   // carried traffic recently (best-effort: has depth/invocations)
}

type FlowGraph struct {
	Diagrams  []FlowDiagram // one per independent flow
	Unwired   []FlowNode    // unconnected resources (chips)
	NodeCount int
	Conns     int
	Flows     int
}

func (g FlowGraph) hash() string {
	parts := []string{strconv.Itoa(g.Flows), strconv.Itoa(g.Conns)}
	for _, d := range g.Diagrams {
		for _, n := range d.Nodes {
			parts = append(parts, n.ID+"="+n.Sub)
		}
	}
	for _, n := range g.Unwired {
		parts = append(parts, n.ID)
	}
	return contentHash(parts...)
}

func nodeID(svc, name string) string { return svc + ":" + name }

// BuildGraph assembles the wiring map from the live services.
func (b *backend) BuildGraph(ctx context.Context) FlowGraph {
	nodes := map[string]*FlowNode{}
	var edges []FlowEdge
	add := func(svc, name, sub string) string {
		id := nodeID(svc, name)
		if _, ok := nodes[id]; !ok {
			nodes[id] = &FlowNode{ID: id, Svc: svc, Name: name, Sub: sub, URL: b.nodeURL(svc, name)}
		}
		return id
	}
	nameFromARN := func(arn string) string {
		if i := strings.LastIndex(arn, ":"); i >= 0 {
			return arn[i+1:]
		}
		if i := strings.LastIndex(arn, "/"); i >= 0 {
			return arn[i+1:]
		}
		return arn
	}

	// nodes: buckets, queues, topics, rules, functions
	buckets, _ := b.ListBuckets(ctx)
	for _, bk := range buckets {
		add("s3", bk.Name, "bucket")
	}
	queues, _ := b.ListQueues(ctx)
	qDepth := map[string]int{}
	for _, q := range queues {
		add("sqs", q.Name, plural(q.Available, "msg"))
		qDepth[q.Name] = q.Available
	}
	topics, _ := b.ListTopics(ctx)
	for _, t := range topics {
		add("sns", t.Name, plural(t.Subs, "sub"))
	}
	fns, _ := b.ListFunctions(ctx)
	for _, f := range fns {
		add("lambda", f.Name, f.Runtime)
	}
	buses, _ := b.ListBuses(ctx)
	for _, bus := range buses {
		rules, _ := b.ListRules(ctx, bus.Name)
		for _, rl := range rules {
			add("eb", rl.Name, "rule")
		}
	}

	// edges: SNS subscriptions
	for _, t := range topics {
		subs, _ := b.ListSubscriptions(ctx, t.ARN)
		for _, s := range subs {
			to := edgeTargetNode(s.Protocol, s.Endpoint, nameFromARN)
			if to == "" {
				continue
			}
			from := nodeID("sns", t.Name)
			ensureNode(nodes, to, b)
			edges = append(edges, FlowEdge{From: from, To: to, Kind: "sub"})
		}
	}
	// edges: EventBridge targets
	for _, bus := range buses {
		rules, _ := b.ListRules(ctx, bus.Name)
		for _, rl := range rules {
			full, err := b.GetRule(ctx, bus.Name, rl.Name)
			if err != nil {
				continue
			}
			for _, tg := range full.Targets {
				to := edgeTargetNode(protoOfARN(tg.ARN), tg.ARN, nameFromARN)
				if to == "" {
					continue
				}
				ensureNode(nodes, to, b)
				edges = append(edges, FlowEdge{From: nodeID("eb", rl.Name), To: to, Kind: "target"})
			}
		}
	}
	// edges: Lambda event-source mappings + DLQ
	for _, f := range fns {
		full, err := b.GetFunction(ctx, f.Name)
		if err != nil {
			continue
		}
		for _, m := range full.Mappings {
			src := nameFromARN(m.SourceARN)
			if strings.Contains(m.SourceARN, ":sqs:") {
				ensureNode(nodes, nodeID("sqs", src), b)
				edges = append(edges, FlowEdge{From: nodeID("sqs", src), To: nodeID("lambda", f.Name), Kind: "esm"})
			}
		}
		// lambda OUTGOING: DLQ + async success/failure destinations make it a
		// source too (SQS / SNS / Lambda / EventBridge targets).
		lamID := nodeID("lambda", f.Name)
		if to := destNode(full.DLQ, nameFromARN); to != "" {
			ensureNode(nodes, to, b)
			edges = append(edges, FlowEdge{From: lamID, To: to, Kind: "dlq"})
		}
		if to := destNode(full.OnSuccess, nameFromARN); to != "" {
			ensureNode(nodes, to, b)
			edges = append(edges, FlowEdge{From: lamID, To: to, Kind: "dest"})
		}
		if to := destNode(full.OnFailure, nameFromARN); to != "" {
			ensureNode(nodes, to, b)
			edges = append(edges, FlowEdge{From: lamID, To: to, Kind: "dest"})
		}
	}
	// edges: SQS redrive → DLQ
	for _, q := range queues {
		attrs, err := b.queueAttrs(ctx, q.Name)
		if err != nil {
			continue
		}
		cfg := sqsConfigOf(attrs)
		if cfg.DLQ != "" {
			ensureNode(nodes, nodeID("sqs", cfg.DLQ), b)
			edges = append(edges, FlowEdge{From: nodeID("sqs", q.Name), To: nodeID("sqs", cfg.DLQ), Kind: "redrive"})
		}
	}
	// edges: S3 bucket notifications → SNS/SQS/Lambda
	for _, bk := range buckets {
		for _, e := range b.bucketNotifications(ctx, bk.Name, nameFromARN) {
			ensureNode(nodes, e.To, b)
			e.From = nodeID("s3", bk.Name)
			e.Kind = "notify"
			edges = append(edges, e)
		}
	}

	// mark hotness: an edge into a queue with depth, or from a topic/rule, reads as active
	for i := range edges {
		to := nodes[edges[i].To]
		if to != nil && to.Svc == "sqs" && qDepth[to.Name] > 0 {
			edges[i].Hot = true
		}
	}
	return layoutFlows(nodes, edges)
}

// layout geometry
const (
	flColW    = 208 // horizontal step between chain depths
	flNodeW   = 176
	flNodeH   = 46
	flRowH    = 62 // vertical step between siblings in a band
	flBandGap = 30
	flTop     = 44 // room for the first band label
	flPadX    = 20
)

// layoutFlows groups the graph into independent connected flows and lays each
// out as its own band: left→right by chain depth, siblings stacked. Unconnected
// resources collect into a final "Not connected" band.
func layoutFlows(nodes map[string]*FlowNode, edges []FlowEdge) FlowGraph {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// union-find over undirected edges → connected components
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" {
			parent[x] = x
		}
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) { parent[find(a)] = find(b) }
	for _, id := range ids {
		find(id)
	}
	for _, e := range edges {
		if nodes[e.From] != nil && nodes[e.To] != nil {
			union(e.From, e.To)
		}
	}
	comps := map[string][]string{}
	for _, id := range ids {
		r := find(id)
		comps[r] = append(comps[r], id)
	}

	// depth = longest path from a source (in-degree 0) within the component
	depthMemo := map[string]int{}
	var depth func(string, map[string]bool) int
	depth = func(id string, seen map[string]bool) int {
		if d, ok := depthMemo[id]; ok {
			return d
		}
		if seen[id] {
			return 0 // break cycles
		}
		seen[id] = true
		best := 0
		// depth = 1 + max depth of predecessors; compute via reverse — easier to
		// derive from successors' perspective, so compute forward here:
		for _, e := range edges {
			if e.To == id && nodes[e.From] != nil {
				if d := depth(e.From, seen) + 1; d > best {
					best = d
				}
			}
		}
		delete(seen, id)
		depthMemo[id] = best
		return best
	}

	// order components: multi-node flows first (by size desc), singletons last
	type comp struct {
		root  string
		ids   []string
		multi bool
	}
	var list []comp
	for r, cids := range comps {
		list = append(list, comp{r, cids, len(cids) > 1})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].multi != list[j].multi {
			return list[i].multi
		}
		if len(list[i].ids) != len(list[j].ids) {
			return len(list[i].ids) > len(list[j].ids)
		}
		return list[i].root < list[j].root
	})

	var diagrams []FlowDiagram
	var singles []string // unwired singletons
	nodeCount := 0

	for _, c := range list {
		if !c.multi {
			singles = append(singles, c.ids...)
			continue
		}
		// each flow gets its own SVG with LOCAL coordinates (origin at 0,0).
		rowByCol := map[int]int{}
		maxRows, maxCol := 0, 0
		sort.Slice(c.ids, func(i, j int) bool {
			di, dj := depth(c.ids[i], map[string]bool{}), depth(c.ids[j], map[string]bool{})
			if di != dj {
				return di < dj
			}
			return nodes[c.ids[i]].Name < nodes[c.ids[j]].Name
		})
		diagNodes := make([]FlowNode, 0, len(c.ids))
		for _, id := range c.ids {
			col := depth(id, map[string]bool{})
			row := rowByCol[col]
			rowByCol[col]++
			if rowByCol[col] > maxRows {
				maxRows = rowByCol[col]
			}
			if col > maxCol {
				maxCol = col
			}
			n := *nodes[id]
			n.X = flPadX + col*flColW
			n.Y = 12 + row*flRowH
			diagNodes = append(diagNodes, n)
		}
		// edges internal to this flow
		inFlow := map[string]bool{}
		for _, id := range c.ids {
			inFlow[id] = true
		}
		var diagEdges []FlowEdge
		for _, e := range edges {
			if inFlow[e.From] && inFlow[e.To] {
				diagEdges = append(diagEdges, e)
			}
		}
		diagrams = append(diagrams, FlowDiagram{
			Label: flowLabel(nodes, c.ids, edges), Nodes: diagNodes, Edges: diagEdges,
			W: flPadX + (maxCol+1)*flColW - (flColW - flNodeW) + flPadX,
			H: 12 + maxRows*flRowH + 8,
		})
		nodeCount += len(c.ids)
	}

	// unconnected resources as chips
	sort.Slice(singles, func(i, j int) bool { return nodes[singles[i]].Name < nodes[singles[j]].Name })
	unwired := make([]FlowNode, 0, len(singles))
	for _, id := range singles {
		n := *nodes[id]
		n.Unwired = true
		unwired = append(unwired, n)
	}
	nodeCount += len(unwired)

	return FlowGraph{
		Diagrams: diagrams, Unwired: unwired,
		NodeCount: nodeCount, Conns: len(edges), Flows: len(diagrams),
	}
}

// flowLabel names a flow by an upstream source (a node with no incoming edge
// within the flow), preferring topics/buckets/rules over queues.
func flowLabel(nodes map[string]*FlowNode, ids []string, edges []FlowEdge) string {
	inFlow := map[string]bool{}
	for _, id := range ids {
		inFlow[id] = true
	}
	hasIncoming := map[string]bool{}
	for _, e := range edges {
		if inFlow[e.From] && inFlow[e.To] {
			hasIncoming[e.To] = true
		}
	}
	rank := map[string]int{"s3": 0, "sns": 1, "eb": 2, "lambda": 3, "sqs": 4}
	best := ""
	for _, id := range ids {
		if hasIncoming[id] {
			continue // not a source
		}
		if best == "" || rank[nodes[id].Svc] < rank[nodes[best].Svc] ||
			(rank[nodes[id].Svc] == rank[nodes[best].Svc] && nodes[id].Name < nodes[best].Name) {
			best = id
		}
	}
	if best == "" { // all nodes have incoming (cycle) — fall back to first
		for _, id := range ids {
			if best == "" || nodes[id].Name < nodes[best].Name {
				best = id
			}
		}
	}
	if n := nodes[best]; n != nil {
		return n.Name + " flow"
	}
	return "flow"
}

// Neighbor is one adjacent resource in a Connections view.
type Neighbor struct {
	Svc  string
	Name string
	Kind string // the edge kind (sub / target / esm / redrive / dlq / dest / notify)
	URL  string
}

// Neighborhood is a resource's 1-hop wiring: what feeds it and where it drains.
type Neighborhood struct {
	Upstream   []Neighbor // edges INTO this node
	Downstream []Neighbor // edges OUT of this node
}

// Neighbors returns the immediate connections of svc:name, built from the full
// wiring graph. Powers the per-resource "Connections" section.
func (b *backend) Neighbors(ctx context.Context, svc, name string) Neighborhood {
	g := b.graphCached(ctx)
	self := nodeID(svc, name)
	// index nodes across all diagrams + unwired for name/svc lookup
	byID := map[string]FlowNode{}
	for _, d := range g.Diagrams {
		for _, n := range d.Nodes {
			byID[n.ID] = n
		}
	}
	for _, n := range g.Unwired {
		byID[n.ID] = n
	}
	var nb Neighborhood
	seen := map[string]bool{}
	for _, d := range g.Diagrams {
		for _, e := range d.Edges {
			if e.From == self && !seen["d"+e.To] {
				seen["d"+e.To] = true
				if n, ok := byID[e.To]; ok {
					nb.Downstream = append(nb.Downstream, Neighbor{Svc: n.Svc, Name: n.Name, Kind: e.Kind, URL: b.nodeURL(n.Svc, n.Name)})
				}
			}
			if e.To == self && !seen["u"+e.From] {
				seen["u"+e.From] = true
				if n, ok := byID[e.From]; ok {
					nb.Upstream = append(nb.Upstream, Neighbor{Svc: n.Svc, Name: n.Name, Kind: e.Kind, URL: b.nodeURL(n.Svc, n.Name)})
				}
			}
		}
	}
	return nb
}

func (b *backend) nodeURL(svc, name string) string {
	switch svc {
	case "s3":
		return "/s3/" + name
	case "sqs":
		return "/sqs/" + name
	case "sns":
		return "/sns/" + name
	case "lambda":
		return "/lambda/" + name
	case "eb":
		return "/eb/default/rule/" + name
	}
	return "/"
}

func ensureNode(nodes map[string]*FlowNode, id string, b *backend) {
	if _, ok := nodes[id]; ok {
		return
	}
	svc, name, _ := strings.Cut(id, ":")
	nodes[id] = &FlowNode{ID: id, Svc: svc, Name: name, URL: b.nodeURL(svc, name)}
}

func edgeTargetNode(proto, endpoint string, leaf func(string) string) string {
	switch {
	case proto == "sqs" || strings.Contains(endpoint, ":sqs:"):
		return nodeID("sqs", leaf(endpoint))
	case proto == "lambda" || strings.Contains(endpoint, ":lambda:") || strings.Contains(endpoint, "function:"):
		return nodeID("lambda", leaf(strings.TrimPrefix(leaf(endpoint), "function:")))
	case proto == "sns" || strings.Contains(endpoint, ":sns:"):
		return nodeID("sns", leaf(endpoint))
	}
	return ""
}

// destNode maps a destination ARN (SQS/SNS/Lambda/EventBridge) to a graph node.
func destNode(arn string, leaf func(string) string) string {
	switch {
	case arn == "":
		return ""
	case strings.Contains(arn, ":sqs:"):
		return nodeID("sqs", leaf(arn))
	case strings.Contains(arn, ":sns:"):
		return nodeID("sns", leaf(arn))
	case strings.Contains(arn, ":lambda:") || strings.Contains(arn, "function:"):
		return nodeID("lambda", strings.TrimPrefix(leaf(arn), "function:"))
	case strings.Contains(arn, ":events:") || strings.Contains(arn, "event-bus/"):
		return nodeID("eb", leaf(arn))
	}
	return ""
}

func protoOfARN(arn string) string {
	switch {
	case strings.Contains(arn, ":sqs:"):
		return "sqs"
	case strings.Contains(arn, ":lambda:") || strings.Contains(arn, "function:"):
		return "lambda"
	case strings.Contains(arn, ":sns:"):
		return "sns"
	}
	return ""
}

// bucketNotifications reads a bucket's S3 event-notification config.
func (b *backend) bucketNotifications(ctx context.Context, bucket string, leaf func(string) string) []FlowEdge {
	body, err := b.s3Sub(ctx, "GET", bucket, "notification")
	if err != nil {
		return nil
	}
	var out struct {
		Topic []struct {
			Arn string `xml:"Topic"`
		} `xml:"TopicConfiguration"`
		Queue []struct {
			Arn string `xml:"Queue"`
		} `xml:"QueueConfiguration"`
		Lambda []struct {
			Arn string `xml:"CloudFunction"`
		} `xml:"CloudFunctionConfiguration"`
	}
	if xml.Unmarshal(body, &out) != nil {
		return nil
	}
	var edges []FlowEdge
	for _, t := range out.Topic {
		edges = append(edges, FlowEdge{To: nodeID("sns", leaf(t.Arn))})
	}
	for _, q := range out.Queue {
		edges = append(edges, FlowEdge{To: nodeID("sqs", leaf(q.Arn))})
	}
	for _, l := range out.Lambda {
		edges = append(edges, FlowEdge{To: nodeID("lambda", leaf(l.Arn))})
	}
	return edges
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

var _ = http.MethodGet
