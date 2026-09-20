package console

import (
	"context"
	"encoding/xml"
	"sort"
	"strconv"
)

// ---- flow graph: the live wiring map ----

// Graph is the set of resources (nodes) and the real connections between them
// (edges) — subscriptions, notifications, targets, event-source mappings, and
// redrive policies. Laid out by service column in the template.

type FlowNode struct {
	ID   string // stable: "sqs:orders"
	Svc  string // s3 | sns | sqs | eb | lambda
	Name string
	Sub  string // small caption ("2 msgs", "1 sub")
	URL  string
}

type FlowEdge struct {
	From string
	To   string
	Kind string // notify | sub | target | esm | redrive
	Hot  bool   // carried traffic recently (best-effort: has depth/invocations)
}

// FlowGraph is the wiring map: every resource, every connection between two of
// them, and the ones nothing is wired to.
//
// It used to carry a LAYOUT — nodes grouped into connected flows, each with
// per-node pixel coordinates from a depth pass and three barycentric ordering
// sweeps, a generated label naming the flow's endpoints, and an SVG width and
// height. That existed for a Flows page which was deleted, and the geometry has
// been computed and discarded on every graph build since. The two consumers
// that remain never wanted it:
//
//	Neighbors   walks the edges to find a resource's 1-hop wiring. It indexed
//	            nodes by id and matched edges by endpoint, so which flow an
//	            edge sat in, and where it was drawn, changed nothing.
//	glance      shows Unwired as chips, and reads NodeCount.
//
// Unwired is still derived by connected components rather than "has no
// incident edge". The two differ on exactly one case — a resource wired to
// ITSELF, a queue whose redrive target is itself — where the component is
// still of size one. Keeping the component pass keeps that answer unchanged.
type FlowGraph struct {
	Nodes     []FlowNode // every node, for lookup by id
	Edges     []FlowEdge // every edge between two known nodes
	Unwired   []FlowNode // resources nothing is wired to
	NodeCount int
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
	return collectGraph(nodes, edges)
}

// collectGraph turns the crawled nodes and edges into a FlowGraph, separating
// out the resources nothing is wired to.
//
// Connected components, not "has no incident edge": see the note on FlowGraph
// for the one case where those differ.
func collectGraph(nodes map[string]*FlowNode, edges []FlowEdge) FlowGraph {
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
	var kept []FlowEdge
	for _, e := range edges {
		if nodes[e.From] != nil && nodes[e.To] != nil {
			union(e.From, e.To)
			kept = append(kept, e)
		}
	}
	size := map[string]int{}
	for _, id := range ids {
		size[find(id)]++
	}

	out := FlowGraph{Edges: kept, NodeCount: len(ids)}
	for _, id := range ids {
		n := *nodes[id]
		out.Nodes = append(out.Nodes, n)
		if size[find(id)] == 1 {
			out.Unwired = append(out.Unwired, n)
		}
	}
	sort.Slice(out.Unwired, func(i, j int) bool { return out.Unwired[i].Name < out.Unwired[j].Name })
	return out
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
	byID := make(map[string]FlowNode, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	var nb Neighborhood
	seen := map[string]bool{}
	for _, e := range g.Edges {
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
