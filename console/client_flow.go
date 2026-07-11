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
	Col     int    // layout column (source→sink left→right)
	Row     int    // assigned in layout
	Unwired bool   // no edges touch it
	URL     string
}

type FlowEdge struct {
	From string
	To   string
	Kind string // notify | sub | target | esm | redrive
	Hot  bool   // carried traffic recently (best-effort: has depth/invocations)
}

type FlowGraph struct {
	Nodes   []FlowNode
	Edges   []FlowEdge
	Conns   int
	Unwired int
	W, H    int
}

func (g FlowGraph) hash() string {
	parts := []string{strconv.Itoa(len(g.Nodes)), strconv.Itoa(len(g.Edges))}
	for _, n := range g.Nodes {
		parts = append(parts, n.ID+"="+n.Sub)
	}
	for _, e := range g.Edges {
		parts = append(parts, e.From+">"+e.To)
	}
	return contentHash(parts...)
}

// column order: source-ish services on the left, sinks on the right.
var flowCol = map[string]int{"s3": 0, "eb": 0, "sns": 1, "sqs": 2, "lambda": 3}

func nodeID(svc, name string) string { return svc + ":" + name }

// BuildGraph assembles the wiring map from the live services.
func (b *backend) BuildGraph(ctx context.Context) FlowGraph {
	nodes := map[string]*FlowNode{}
	var edges []FlowEdge
	add := func(svc, name, sub string) string {
		id := nodeID(svc, name)
		if _, ok := nodes[id]; !ok {
			nodes[id] = &FlowNode{ID: id, Svc: svc, Name: name, Sub: sub, Col: flowCol[svc], URL: b.nodeURL(svc, name)}
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
	// mark wired
	touched := map[string]bool{}
	for _, e := range edges {
		touched[e.From] = true
		touched[e.To] = true
	}
	unwired := 0
	list := make([]FlowNode, 0, len(nodes))
	for id, n := range nodes {
		if !touched[id] {
			n.Unwired = true
			unwired++
		}
		list = append(list, *n)
	}
	// stable layout: sort by column then name, assign rows per column
	sort.Slice(list, func(i, j int) bool {
		if list[i].Col != list[j].Col {
			return list[i].Col < list[j].Col
		}
		return list[i].Name < list[j].Name
	})
	rowByCol := map[int]int{}
	maxRow := 0
	for i := range list {
		list[i].Row = rowByCol[list[i].Col]
		rowByCol[list[i].Col]++
		if list[i].Row > maxRow {
			maxRow = list[i].Row
		}
	}
	return FlowGraph{
		Nodes: list, Edges: edges, Conns: len(edges), Unwired: unwired,
		W: 4 * 210, H: (maxRow + 1) * 72,
	}
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
	nodes[id] = &FlowNode{ID: id, Svc: svc, Name: name, Col: flowCol[svc], URL: b.nodeURL(svc, name)}
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
