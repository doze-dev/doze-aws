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

func nodeID(svc, name string) string { return svc + ":" + name }

// BuildGraph assembles the wiring map from the live services.
func (b *backend) BuildGraph(ctx context.Context) FlowGraph {
	nodes := map[string]*FlowNode{}
	var edges []FlowEdge
	// Nodes are keyed on the resolver's Key, not on the display name. That is
	// what stops two buses each holding a rule called "orders" from collapsing
	// into one node — which they did, and then linked to /eb/default/rule/orders
	// regardless of which bus either lived on.
	add := func(svc, id, sub string) string {
		ref := resourceURL(svc, id)
		nid := nodeID(svc, ref.Key)
		if _, ok := nodes[nid]; !ok {
			nodes[nid] = &FlowNode{ID: nid, Svc: svc, Name: ref.Name, Sub: sub, URL: ref.Path}
		}
		return nid
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
		for _, rl := range bus.RuleList { // fetched once inside ListBuses
			add("eb", bus.Name+"/"+rl.Name, "rule")
		}
	}
	// Tables and streams are graph nodes now. They were left out, so a Lambda
	// fed by either had no visible source and the resource pages had nothing to
	// draw — a gap that read as "the console does not model this" when the
	// emulator models it fully.
	tables, _ := b.ListTables(ctx)
	for _, t := range tables {
		add("ddb", t.Name, plural(int(t.ItemCount), "item"))
	}
	streams, _ := b.ListStreams(ctx)
	for _, st := range streams {
		add("kinesis", st.Name, plural(st.Shards, "shard"))
	}

	// edges: SNS subscriptions (carried on the topics from ListTopics)
	for _, t := range topics {
		for _, sub := range t.SubList {
			to := ensureNode(nodes, resourceFromARN(sub.Endpoint))
			if to == "" {
				continue
			}
			edges = append(edges, FlowEdge{From: nodeID("sns", t.Name), To: to, Kind: "sub"})
		}
	}
	// edges: EventBridge targets (rules carried on the buses; only the target
	// list needs a follow-up call)
	for _, bus := range buses {
		for _, rl := range bus.RuleList {
			ruleKey := resourceURL("eb", bus.Name+"/"+rl.Name).Key
			for _, tg := range b.ruleTargets(ctx, bus.Name, rl.Name) {
				to := ensureNode(nodes, resourceFromARN(tg.ARN))
				if to == "" {
					continue
				}
				edges = append(edges, FlowEdge{From: nodeID("eb", ruleKey), To: to, Kind: "target"})
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
			// This tested :sqs: only, so a Lambda fed by a DynamoDB stream or a
			// Kinesis stream drew no edge and appeared in the wiring strip as
			// nothing at all — even though lambda/extras.go validates and polls
			// both. Kinesis's strip could therefore never populate, which read
			// as a dead feature rather than a missing one.
			if from := ensureNode(nodes, resourceFromARN(m.SourceARN)); from != "" {
				edges = append(edges, FlowEdge{From: from, To: nodeID("lambda", f.Name), Kind: "esm"})
			}
		}
		// lambda OUTGOING: DLQ + async success/failure destinations make it a
		// source too (SQS / SNS / Lambda / EventBridge targets).
		lamID := nodeID("lambda", f.Name)
		for _, d := range []struct {
			arn, kind string
		}{{full.DLQ, "dlq"}, {full.OnSuccess, "dest"}, {full.OnFailure, "dest"}} {
			if to := ensureNode(nodes, resourceFromARN(d.arn)); to != "" {
				edges = append(edges, FlowEdge{From: lamID, To: to, Kind: d.kind})
			}
		}
	}
	// edges: SQS redrive → DLQ (carried on the queues from ListQueues)
	for _, q := range queues {
		if q.DLQ != "" {
			to := ensureNode(nodes, resourceURL("sqs", q.DLQ))
			edges = append(edges, FlowEdge{From: nodeID("sqs", q.Name), To: to, Kind: "redrive"})
		}
	}
	// edges: S3 bucket notifications → SNS/SQS/Lambda
	for _, bk := range buckets {
		for _, ref := range b.bucketNotifications(ctx, bk.Name) {
			if to := ensureNode(nodes, ref); to != "" {
				edges = append(edges, FlowEdge{From: nodeID("s3", bk.Name), To: to, Kind: "notify"})
			}
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
		// Nodes go into columns by chain depth; rows within a column are
		// ordered by the barycenter of their neighbors' rows so edges run
		// roughly parallel instead of crossing.
		byCol := map[int][]string{}
		maxRows, maxCol := 0, 0
		for _, id := range c.ids {
			col := depth(id, map[string]bool{})
			byCol[col] = append(byCol[col], id)
			if col > maxCol {
				maxCol = col
			}
		}
		rowOf := map[string]int{}
		for col := 0; col <= maxCol; col++ {
			sort.Slice(byCol[col], func(i, j int) bool { return nodes[byCol[col][i]].Name < nodes[byCol[col][j]].Name })
			for row, id := range byCol[col] {
				rowOf[id] = row
			}
		}
		bary := func(id string, incoming bool) (float64, bool) {
			sum, n := 0.0, 0
			for _, e := range edges {
				if incoming && e.To == id && nodes[e.From] != nil {
					sum += float64(rowOf[e.From])
					n++
				}
				if !incoming && e.From == id && nodes[e.To] != nil {
					sum += float64(rowOf[e.To])
					n++
				}
			}
			if n == 0 {
				return 0, false
			}
			return sum / float64(n), true
		}
		reorder := func(col int, incoming bool) {
			ids := byCol[col]
			sort.SliceStable(ids, func(i, j int) bool {
				bi, oki := bary(ids[i], incoming)
				bj, okj := bary(ids[j], incoming)
				if oki && okj && bi != bj {
					return bi < bj
				}
				if oki != okj {
					return okj // unconnected-within-direction nodes sink to the bottom
				}
				return rowOf[ids[i]] < rowOf[ids[j]]
			})
			for row, id := range ids {
				rowOf[id] = row
			}
		}
		// sweep right pulling nodes toward their feeders, then left toward
		// their readers, then right once more to settle
		for col := 1; col <= maxCol; col++ {
			reorder(col, true)
		}
		for col := maxCol - 1; col >= 0; col-- {
			reorder(col, false)
		}
		for col := 1; col <= maxCol; col++ {
			reorder(col, true)
		}
		diagNodes := make([]FlowNode, 0, len(c.ids))
		for col := 0; col <= maxCol; col++ {
			if len(byCol[col]) > maxRows {
				maxRows = len(byCol[col])
			}
			for _, id := range byCol[col] {
				n := *nodes[id]
				n.X = flPadX + col*flColW
				n.Y = 12 + rowOf[id]*flRowH
				diagNodes = append(diagNodes, n)
			}
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

// flowLabel names a flow by its endpoints — the longest source→sink chain
// through the component ("uploads → resize → audit"), which says what the flow
// does instead of naming it after one arbitrary member.
func flowLabel(nodes map[string]*FlowNode, ids []string, edges []FlowEdge) string {
	inFlow := map[string]bool{}
	for _, id := range ids {
		inFlow[id] = true
	}
	hasIncoming := map[string]bool{}
	succ := map[string][]string{}
	for _, e := range edges {
		if inFlow[e.From] && inFlow[e.To] {
			hasIncoming[e.To] = true
			succ[e.From] = append(succ[e.From], e.To)
		}
	}
	// longest path from a source (memoized; cycles cut by the seen set)
	memo := map[string][]string{}
	var chain func(string, map[string]bool) []string
	chain = func(id string, seen map[string]bool) []string {
		if c, ok := memo[id]; ok {
			return c
		}
		if seen[id] {
			return []string{id}
		}
		seen[id] = true
		var longest []string
		for _, nxt := range succ[id] {
			if c := chain(nxt, seen); len(c) > len(longest) {
				longest = c
			}
		}
		delete(seen, id)
		out := append([]string{id}, longest...)
		memo[id] = out
		return out
	}
	// the label is the LONGEST source→sink chain; ties break toward the
	// upstream-most service kind, then name
	rank := map[string]int{"s3": 0, "sns": 1, "eb": 2, "lambda": 3, "sqs": 4}
	var path []string
	bestSrc := ""
	for _, id := range ids {
		if hasIncoming[id] {
			continue // not a source
		}
		c := chain(id, map[string]bool{})
		better := len(c) > len(path) ||
			(len(c) == len(path) && (bestSrc == "" || rank[nodes[id].Svc] < rank[nodes[bestSrc].Svc] ||
				(rank[nodes[id].Svc] == rank[nodes[bestSrc].Svc] && nodes[id].Name < nodes[bestSrc].Name)))
		if better {
			path, bestSrc = c, id
		}
	}
	if len(path) == 0 { // all nodes have incoming (cycle) — fall back to first
		for _, id := range ids {
			if bestSrc == "" || nodes[id].Name < nodes[bestSrc].Name {
				bestSrc = id
			}
		}
		path = chain(bestSrc, map[string]bool{})
	}
	names := make([]string, 0, len(path))
	for _, id := range path {
		if n := nodes[id]; n != nil {
			names = append(names, n.Name)
		}
	}
	switch {
	case len(names) == 0:
		return "flow"
	case len(names) == 1:
		return names[0]
	case len(names) > 3: // keep the header scannable: ends + an ellipsis
		return names[0] + " → … → " + names[len(names)-1]
	}
	return strings.Join(names, " → ")
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
					// n.URL, not nodeURL(n.Svc, n.Name): the node's URL was
					// resolved from its QUALIFIED key when the graph was built,
					// and the display name is lossy — an eb rule's name is
					// "orders" while its key is "default/orders", so
					// re-resolving from the name minted /eb/orders, a bus page
					// for a bus that does not exist. Every rule chip in every
					// wiring strip linked there.
					nb.Downstream = append(nb.Downstream, Neighbor{Svc: n.Svc, Name: n.Name, Kind: e.Kind, URL: n.URL})
				}
			}
			if e.To == self && !seen["u"+e.From] {
				seen["u"+e.From] = true
				if n, ok := byID[e.From]; ok {
					nb.Upstream = append(nb.Upstream, Neighbor{Svc: n.Svc, Name: n.Name, Kind: e.Kind, URL: n.URL})
				}
			}
		}
	}
	return nb
}

// ensureNode adds a node for a resolved ref. It takes the ref rather than
// re-splitting the id, because an id now carries a qualified key (an eb rule is
// "bus/rule") and Cut(id, ":") cannot tell that back apart into a display name
// and a path.
func ensureNode(nodes map[string]*FlowNode, ref resourceRef) string {
	if !ref.OK() {
		return ""
	}
	id := nodeID(ref.Svc, ref.Key)
	if _, ok := nodes[id]; !ok {
		nodes[id] = &FlowNode{ID: id, Svc: ref.Svc, Name: ref.Name, URL: ref.Path}
	}
	return id
}

// bucketNotifications reads a bucket's S3 event-notification config.
// bucketNotifications returns the refs a bucket notifies, leaving node creation
// to the caller — the resolver already knows how to turn each ARN into a name
// and a path, so there is nothing left for a leaf function to do.
func (b *backend) bucketNotifications(ctx context.Context, bucket string) []resourceRef {
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
	var refs []resourceRef
	for _, t := range out.Topic {
		refs = append(refs, resourceFromARN(t.Arn))
	}
	for _, q := range out.Queue {
		refs = append(refs, resourceFromARN(q.Arn))
	}
	for _, l := range out.Lambda {
		refs = append(refs, resourceFromARN(l.Arn))
	}
	return refs
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

var _ = http.MethodGet
