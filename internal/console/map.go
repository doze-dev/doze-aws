package console

// The map (plan/00-foundation.md D; boards Map and Map-Large).
//
// It is the wiring graph (client_flow.go) as a page of its own: every resource
// that is wired to another, and the wire between them, grouped into flows. A
// flow is a connected component of the graph. A Flows page once did this and
// was deleted ("a synoptic view you have to navigate to is the thing that got
// the flows page deleted"); the design overrules that, and the resource pages'
// own Connections strips stay as they were.
//
// While the wired resources fit on a page (mapDrawMax) the wires are drawn.
// Past that the page stops drawing: a flow is one card, and opening it draws
// that flow alone. Nothing is computed until the page is asked for: the graph
// is the five-second cache the Connections strips already share, and the
// layout below is arithmetic on a handful of boxes, with no table and no
// script. The drawing is server-rendered SVG under real links.
//
// "Hot" is new (plan/03-shell-fidelity.md D6). The graph used to call an edge
// hot when it led into a queue holding messages, which is a backlog and not
// traffic. A wire is hot here when the wire (client_flow.go's recorder ring,
// the calls the resource's own wire dock lists) holds a call to either end in
// the last minute. Management calls count: it is the same set the dock shows.

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// mapDrawMax is how many wired resources the page draws wires for. Past it
	// the page is cards. The boards say "a handful"; twelve is the number a
	// 1440 px canvas holds in three or four columns before a name is lost.
	mapDrawMax = 12
	// mapCards is how many flows get a card of their own before the rest fold
	// into one ("4 more flows"), as Map-Large draws seven and the fold.
	mapCards = 7
	// mapCardLines is how many resources a card names; the rest are "+ n more".
	mapCardLines = 3
	// mapDrawCap bounds one drawn flow. A topic with two hundred queues is a
	// list, not a picture; the flow page says how many it left out.
	mapDrawCap = 120
)

// mapKinds is the legend, and the order the wires are named in: the seven
// kinds of edge the graph has (client_flow.go), each in the words the resource
// pages use.
var mapKinds = []struct{ Kind, Meaning string }{
	{"notify", "S3 event notification"},
	{"sub", "SNS subscription"},
	{"target", "EventBridge rule target"},
	{"esm", "event source mapping"},
	{"redrive", "dead-letter queue"},
	{"dlq", "function dead-letter target"},
	{"dest", "function success or failure destination"},
}

// mapWord is how a card names a resource's kind, before its name.
func mapWord(n FlowNode) string {
	switch n.Svc {
	case "s3":
		return "S3"
	case "sqs":
		return "SQS"
	case "sns":
		return "SNS"
	case "lambda":
		return "λ"
	case "ddb":
		return "DynamoDB table"
	case "kinesis":
		return "Kinesis stream"
	case "eb":
		switch k := strings.TrimPrefix(n.ID, "eb:"); {
		case strings.HasPrefix(k, "bus/"):
			return "EB bus"
		case strings.HasPrefix(k, "connection/"):
			return "EB connection"
		case strings.HasPrefix(k, "destination/"):
			return "EB destination"
		}
		return "EB rule"
	}
	if e, ok := catalogOf(n.Svc); ok {
		return e.Label
	}
	return n.Svc
}

// mapLabel is the name a node is drawn under. A rule on another bus than the
// default carries its bus, because two buses may each hold a rule of one name.
func mapLabel(n FlowNode) string {
	if n.Svc == "eb" {
		if bus, _, ok := strings.Cut(strings.TrimPrefix(n.ID, "eb:"), "/"); ok && bus != "default" && bus != "bus" {
			return bus + "/" + n.Name
		}
	}
	return n.Name
}

// mapEdge is a graph edge with the one thing the map adds to it.
type mapEdge struct {
	FlowEdge
	Hot bool // a call to either end in the last minute
}

// mapFlow is one connected component of the graph with at least one wire.
type mapFlow struct {
	ID    string     // the root's node id: the flow's name in a URL
	Root  FlowNode   // the resource the flow is named for
	Nodes []FlowNode // breadth first from the root, then what the root does not reach
	Edges []mapEdge  // each (from, to, kind) once
	Hot   bool
	// Reached is how many of Nodes the root leads to; the rest are other sources
	// joining the flow, which a card does not draw an arrow to.
	Reached int
}

func (f mapFlow) has(id string) bool {
	for _, n := range f.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

// flowsOf splits the graph into flows. Connected components, as Unwired is: a
// resource wired only to itself is a component of one and is unwired, so the
// two lists never overlap. touched is the set of node ids a call named lately.
// The order is the largest first, then by the root's name, which is stable
// while the wiring is.
func flowsOf(g FlowGraph, touched map[string]bool) []mapFlow {
	byID := make(map[string]FlowNode, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
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
	for _, e := range g.Edges {
		if e.From != e.To {
			parent[find(e.From)] = find(e.To)
		}
	}
	members := map[string][]string{}
	edges := map[string][]mapEdge{}
	seen := map[[3]string]int{}
	for id := range parent {
		members[find(id)] = append(members[find(id)], id)
	}
	for _, e := range g.Edges {
		if e.From == e.To {
			continue
		}
		c := find(e.From)
		key := [3]string{e.From, e.To, e.Kind}
		if i, dup := seen[key]; dup {
			// Two mappings between one pair: one that reads is enough for the
			// pair to be read (client_flow.go says the same).
			edges[c][i].Off = edges[c][i].Off && e.Off
			continue
		}
		seen[key] = len(edges[c])
		edges[c] = append(edges[c], mapEdge{FlowEdge: e, Hot: touched[e.From] || touched[e.To]})
	}

	var out []mapFlow
	for c, ids := range members {
		if len(ids) < 2 {
			continue
		}
		f := mapFlow{Edges: edges[c]}
		for _, e := range f.Edges {
			if e.Hot {
				f.Hot = true
			}
		}
		f.Nodes, f.Root, f.Reached = orderFlow(ids, byID, f.Edges)
		f.ID = f.Root.ID
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Nodes) != len(out[j].Nodes) {
			return len(out[i].Nodes) > len(out[j].Nodes)
		}
		return mapBefore(out[i].Root, out[j].Root)
	})
	return out
}

func mapBefore(a, b FlowNode) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.ID < b.ID
}

// orderFlow names a flow's root and lists its resources breadth first from it.
// The root is where the wiring starts: a resource nothing feeds, preferring the
// one that reaches the most (so a flow is named for its source, not for a
// stray rule that joins it late), then by name. A flow that is all cycle has
// no such resource. Then the root is one that nothing feeds by a wire that
// carries the work (a dead-letter queue, a failure destination and a redrive
// carry what failed, and run back to where it came from), and failing that the
// first by name.
func orderFlow(ids []string, byID map[string]FlowNode, edges []mapEdge) ([]FlowNode, FlowNode, int) {
	out := map[string][]string{}
	fed, worked := map[string]bool{}, map[string]bool{}
	for _, e := range edges {
		out[e.From] = append(out[e.From], e.To)
		fed[e.To] = true
		if e.Kind != "dlq" && e.Kind != "dest" && e.Kind != "redrive" {
			worked[e.To] = true
		}
	}
	for id := range out {
		sort.Slice(out[id], func(i, j int) bool { return mapBefore(byID[out[id][i]], byID[out[id][j]]) })
	}
	reach := func(from string) []string {
		var order []string
		seen := map[string]bool{from: true}
		for q := []string{from}; len(q) > 0; q = q[1:] {
			order = append(order, q[0])
			for _, to := range out[q[0]] {
				if !seen[to] {
					seen[to] = true
					q = append(q, to)
				}
			}
		}
		return order
	}
	cand := append([]string(nil), ids...)
	sort.Slice(cand, func(i, j int) bool { return mapBefore(byID[cand[i]], byID[cand[j]]) })
	size := map[string]int{}
	var roots []string
	for _, id := range cand {
		if !fed[id] {
			roots = append(roots, id)
			size[id] = len(reach(id))
		}
	}
	for _, id := range cand {
		if len(roots) == 0 && !worked[id] {
			roots = append(roots, id)
			size[id] = len(reach(id))
		}
	}
	if len(roots) == 0 {
		roots = cand[:1]
	}
	sort.SliceStable(roots, func(i, j int) bool { return size[roots[i]] > size[roots[j]] })

	var nodes []FlowNode
	placed := map[string]bool{}
	take := func(from string) {
		for _, id := range reach(from) {
			if !placed[id] {
				placed[id] = true
				nodes = append(nodes, byID[id])
			}
		}
	}
	take(roots[0])
	reached := len(nodes)
	for _, r := range roots[1:] {
		take(r)
	}
	for _, id := range cand { // a cycle no root reaches into
		take(id)
	}
	return nodes, byID[roots[0]], reached
}

// mapTouched is the set of node ids a recorded call named in the last minute:
// the calls the resource's own wire dock lists (trafficFor matches on the
// service and the name the recorder files a call under).
func (c *Console) mapTouched(nodes []FlowNode, now time.Time) map[string]bool {
	if c.rec == nil {
		return nil
	}
	byName := map[string][]string{}
	for _, n := range nodes {
		byName[n.Svc+"|"+n.Name] = append(byName[n.Svc+"|"+n.Name], n.ID)
	}
	out := map[string]bool{}
	for _, e := range c.rec.Entries(0) {
		if now.Sub(e.At) > glanceWindow {
			continue
		}
		res := e.Resource
		if e.Service == "s3" { // "bucket/key"
			res, _, _ = strings.Cut(res, "/")
		}
		for _, id := range byName[e.Service+"|"+res] {
			out[id] = true
		}
	}
	return out
}

// ---- the page ----

// mapLine is one resource on a card.
type mapLine struct {
	Word, Name string
	Follows    bool // drawn after an arrow: the root is not
}

// mapCard is one flow as the board draws it at scale.
type mapCard struct {
	URL   string
	Title string // "from <root>"
	Count string // "5 resources"
	Hot   bool
	Lines []mapLine
	More  int
}

func (c *Console) cardOf(f mapFlow) mapCard {
	card := mapCard{
		URL:   c.prefix + "/map?flow=" + url.QueryEscape(f.ID),
		Title: "from " + mapLabel(f.Root),
		Count: plural(len(f.Nodes), "resource"),
		Hot:   f.Hot,
	}
	for i, n := range f.Nodes {
		if i == mapCardLines {
			card.More = len(f.Nodes) - mapCardLines
			break
		}
		card.Lines = append(card.Lines, mapLine{Word: mapWord(n), Name: mapLabel(n), Follows: i > 0 && i < f.Reached})
	}
	return card
}

// mapChip is a resource nothing is wired to.
type mapChip struct {
	Svc, Name, URL string
}

func (c *Console) mapPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	g := c.be.graphCached(r.Context())
	touched := c.mapTouched(g.Nodes, time.Now())
	flows := flowsOf(g, touched)

	wired := g.NodeCount - len(g.Unwired)
	hotFlows := 0
	for _, f := range flows {
		if f.Hot {
			hotFlows++
		}
	}
	find := strings.TrimSpace(q.Get("q"))
	data := map[string]any{
		"Title":     "The map",
		"Resources": plural(wired, "resource"),
		"Flows":     plural(len(flows), "flow"),
		"Unwired":   strconv.Itoa(len(g.Unwired)) + " unwired",
		"HotFlows":  hotFlows,
		"HotLine":   plural(hotFlows, "flow") + " carried traffic recently",
		"Kinds":     mapKinds,
		"Q":         find,
		"Capture":   c.rec != nil,
		"Empty":     len(flows) == 0,
		"Keys":      "⏎=open the resource|" + keyPalette,
	}
	var chips []mapChip
	for _, n := range g.Unwired {
		if find == "" || strings.Contains(strings.ToLower(n.Name), strings.ToLower(find)) {
			chips = append(chips, mapChip{Svc: n.Svc, Name: mapLabel(n), URL: n.URL})
		}
	}
	data["Chips"] = chips
	data["UnwiredAll"] = len(g.Unwired)

	// One flow, drawn: what a card opens. Any resource in it names it, so a
	// link made from a resource that has since changed flows still lands.
	if id := q.Get("flow"); id != "" {
		for _, f := range flows {
			if f.has(id) {
				data["One"] = c.drawnOf(1, f)
				data["Back"] = c.prefix + "/map"
				data["Resources"] = plural(len(f.Nodes), "resource")
				data["Flows"] = plural(len(f.Edges), "wire")
				data["Unwired"] = ""
				data["Chips"] = []mapChip(nil)
				data["Keys"] = "⏎=open the resource|" + keyPalette
				c.render(w, r, "map", data)
				return
			}
		}
		data["Gone"] = true
	}

	if wired <= mapDrawMax {
		var drawn []mapDrawn
		for i, f := range flows {
			drawn = append(drawn, c.drawnOf(i+1, f))
		}
		data["Drawn"] = drawn
		c.render(w, r, "map", data)
		return
	}

	// At scale: a card a flow, the hot ones first.
	data["Large"] = true
	data["Max"] = mapDrawMax
	data["Keys"] = "/=find|⏎=open the flow|" + keyPalette
	hotOnly := q.Get("hot") == "1"
	data["HotOnly"] = hotOnly
	data["AllURL"] = c.prefix + "/map" + mapQuery(find, false, false)
	data["HotURL"] = c.prefix + "/map" + mapQuery(find, true, false)
	data["Searching"] = find != "" || hotOnly
	shown := make([]mapFlow, 0, len(flows))
	for _, f := range flows {
		if hotOnly && !f.Hot {
			continue
		}
		if find != "" && !flowMatches(f, find) {
			continue
		}
		shown = append(shown, f)
	}
	sort.SliceStable(shown, func(i, j int) bool { return shown[i].Hot && !shown[j].Hot })
	data["Matches"] = plural(len(shown), "flow") + " match"
	if len(shown) == 1 {
		data["Matches"] = "1 flow matches"
	}
	fold := q.Get("all") != "1" && find == "" && !hotOnly && len(shown) > mapCards+1
	rest := []mapFlow(nil)
	if fold {
		shown, rest = shown[:mapCards], shown[mapCards:]
	}
	var cards []mapCard
	for _, f := range shown {
		cards = append(cards, c.cardOf(f))
	}
	data["Cards"] = cards
	if len(rest) > 0 {
		lo, hi := len(rest[0].Nodes), len(rest[0].Nodes)
		for _, f := range rest {
			lo, hi = min(lo, len(f.Nodes)), max(hi, len(f.Nodes))
		}
		span := strconv.Itoa(lo)
		if hi != lo {
			span += " to " + strconv.Itoa(hi)
		}
		data["Fold"] = map[string]any{
			"Title": strconv.Itoa(len(rest)) + " more flows",
			"Span":  "of " + span + " resources each",
			"URL":   c.prefix + "/map" + mapQuery(find, false, true),
		}
	}
	c.render(w, r, "map", data)
}

func mapQuery(find string, hot, all bool) string {
	v := url.Values{}
	if find != "" {
		v.Set("q", find)
	}
	if hot {
		v.Set("hot", "1")
	}
	if all {
		v.Set("all", "1")
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

func flowMatches(f mapFlow, find string) bool {
	find = strings.ToLower(find)
	for _, n := range f.Nodes {
		if strings.Contains(strings.ToLower(n.Name), find) {
			return true
		}
	}
	return false
}

// ---- the drawing ----

// Metrics of the drawing, in px. A box is the board's (a mark, a name), a
// column is a box and the room for a wire and its label.
const (
	boxW, boxH = 260, 44
	colGap     = 84
	rowGap     = 44
	padX, padY = 20, 14
	loopDrop   = 22
	labelW     = 7 // px per character of a wire's label, in 11 px mono
)

type mapBox struct {
	FlowNode
	Word, Label string
	X, Y        int
}

type mapWire struct {
	Path     string
	Label    string
	LX, LY   int
	Hot, Off bool
	Loop     bool
}

type mapDrawing struct {
	W, H    int
	Boxes   []mapBox
	Wires   []mapWire
	Spoken  []string // each wire as a sentence, for a reader who cannot see the lines
	Omitted int      // resources the flow has that are not drawn
}

// mapDrawn is one flow with its drawing.
type mapDrawn struct {
	Num     int
	Title   string
	Hot     bool
	URL     string
	Drawing mapDrawing
}

func (c *Console) drawnOf(num int, f mapFlow) mapDrawn {
	return mapDrawn{Num: num, Title: mapLabel(f.Root), Hot: f.Hot,
		URL: c.prefix + "/map?flow=" + url.QueryEscape(f.ID), Drawing: layoutFlow(f)}
}

type mapPair struct {
	from, to string
	kinds    []string
	hot      bool
	off      bool // every wire between the pair is a disabled mapping
}

// layoutFlow places a flow's resources in columns by how far they are from
// where the wiring starts, in rows that keep the wires from crossing more than
// they must, and routes each wire as an elbow from the right of its source to
// the left of its target. A wire that runs back (a cycle: a function whose
// failure destination is the queue it reads) goes underneath.
func layoutFlow(f mapFlow) mapDrawing {
	var d mapDrawing
	nodes := f.Nodes
	if len(nodes) > mapDrawCap {
		d.Omitted = len(nodes) - mapDrawCap
		nodes = nodes[:mapDrawCap]
	}
	idx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		idx[n.ID] = i
	}

	// One pair is one line: a function whose dead-letter target and failure
	// destination are the same queue draws "dlq dest" on one wire.
	var pairs []*mapPair
	at := map[[2]string]*mapPair{}
	for _, e := range f.Edges {
		if _, ok := idx[e.From]; !ok {
			continue
		}
		if _, ok := idx[e.To]; !ok {
			continue
		}
		k := [2]string{e.From, e.To}
		p := at[k]
		if p == nil {
			p = &mapPair{from: e.From, to: e.To, off: true}
			at[k] = p
			pairs = append(pairs, p)
		}
		kind := e.Kind
		if e.Off {
			kind += " off"
		}
		p.kinds = append(p.kinds, kind)
		p.hot = p.hot || e.Hot
		p.off = p.off && e.Off
	}

	succ := map[string][]string{}
	for _, p := range pairs {
		succ[p.from] = append(succ[p.from], p.to)
	}
	// Depth first from the root, in flow order: an edge into a node still on the
	// stack closes a cycle, and is the one that runs back.
	state := map[string]int{}
	back := map[[2]string]bool{}
	var dfs func(string)
	dfs = func(n string) {
		state[n] = 1
		for _, m := range succ[n] {
			switch state[m] {
			case 0:
				dfs(m)
			case 1:
				back[[2]string{n, m}] = true
			}
		}
		state[n] = 2
	}
	for _, n := range nodes {
		if state[n.ID] == 0 {
			dfs(n.ID)
		}
	}
	preds := map[string][]string{}
	for _, p := range pairs {
		if !back[[2]string{p.from, p.to}] {
			preds[p.to] = append(preds[p.to], p.from)
		}
	}
	layer := map[string]int{}
	var depth func(string) int
	depth = func(n string) int {
		if l, ok := layer[n]; ok {
			return l
		}
		l := 0
		for _, p := range preds[n] {
			l = max(l, depth(p)+1)
		}
		layer[n] = l
		return l
	}
	cols := 0
	for _, n := range nodes {
		cols = max(cols, depth(n.ID)+1)
	}
	layers := make([][]string, cols)
	for _, n := range nodes {
		layers[layer[n.ID]] = append(layers[layer[n.ID]], n.ID)
	}

	// Rows: start in flow order, then move each node toward the average row of
	// its neighbours, down the columns and back up, a few times.
	row := map[string]int{}
	settle := func(l int) {
		for i, id := range layers[l] {
			row[id] = i
		}
	}
	for l := range layers {
		settle(l)
	}
	bary := func(id string, around map[string][]string) float64 {
		var sum float64
		var n int
		for _, o := range around[id] {
			sum += float64(row[o])
			n++
		}
		if n == 0 {
			return float64(row[id])
		}
		return sum / float64(n)
	}
	sweep := func(l int, around map[string][]string) {
		key := map[string]float64{}
		for _, id := range layers[l] {
			key[id] = bary(id, around)
		}
		sort.SliceStable(layers[l], func(i, j int) bool { return key[layers[l][i]] < key[layers[l][j]] })
		settle(l)
	}
	downs := map[string][]string{}
	for _, p := range pairs {
		if !back[[2]string{p.from, p.to}] {
			downs[p.from] = append(downs[p.from], p.to)
		}
	}
	for range 3 {
		for l := 1; l < cols; l++ {
			sweep(l, preds)
		}
		for l := cols - 2; l >= 0; l-- {
			sweep(l, downs)
		}
	}

	rows := 0
	pos := map[string]*mapBox{}
	d.Boxes = make([]mapBox, 0, len(nodes))
	for _, n := range nodes {
		rows = max(rows, row[n.ID]+1)
	}
	for _, n := range nodes {
		d.Boxes = append(d.Boxes, mapBox{FlowNode: n, Word: mapWord(n), Label: mapLabel(n),
			X: padX + layer[n.ID]*(boxW+colGap), Y: padY + row[n.ID]*(boxH+rowGap)})
	}
	for i := range d.Boxes {
		pos[d.Boxes[i].ID] = &d.Boxes[i]
	}
	d.W = 2*padX + cols*boxW + (cols-1)*colGap
	d.H = 2*padY + rows*boxH + (rows-1)*rowGap

	// Ports: the wires that leave a box leave at spread heights, ordered by the
	// height of what they reach, and those that arrive likewise.
	type end struct {
		p  *mapPair
		in bool
	}
	slot := map[end]int{}
	count := map[[2]string]int{} // box id, "out"/"in"
	byBox := map[[2]string][]*mapPair{}
	for _, p := range pairs {
		if back[[2]string{p.from, p.to}] {
			continue
		}
		byBox[[2]string{p.from, "out"}] = append(byBox[[2]string{p.from, "out"}], p)
		byBox[[2]string{p.to, "in"}] = append(byBox[[2]string{p.to, "in"}], p)
	}
	for k, ps := range byBox {
		in := k[1] == "in"
		sort.SliceStable(ps, func(i, j int) bool {
			a, b := ps[i].to, ps[j].to
			if in {
				a, b = ps[i].from, ps[j].from
			}
			return pos[a].Y < pos[b].Y
		})
		for i, p := range ps {
			slot[end{p, in}] = i
		}
		count[k] = len(ps)
	}
	port := func(p *mapPair, in bool) int {
		id, dir := p.from, "out"
		if in {
			id, dir = p.to, "in"
		}
		n := count[[2]string{id, dir}]
		return pos[id].Y + boxH*(slot[end{p, in}]+1)/(n+1)
	}

	loops := 0
	for _, p := range pairs {
		a, b := pos[p.from], pos[p.to]
		label := strings.Join(p.kinds, " ")
		w := mapWire{Label: label, Hot: p.hot, Off: p.off}
		if back[[2]string{p.from, p.to}] {
			loops++
			w.Loop = true
			x1, x2 := a.X+boxW*35/100, b.X+boxW*65/100
			y := max(a.Y, b.Y) + boxH + loopDrop + (loops-1)*8
			w.Path = "M" + strconv.Itoa(x1) + " " + strconv.Itoa(a.Y+boxH) + "V" + strconv.Itoa(y) +
				"H" + strconv.Itoa(x2) + "V" + strconv.Itoa(b.Y+boxH)
			w.LX, w.LY = min(x1, x2)+8, y-15
			d.H = max(d.H, y+padY)
		} else {
			x1, x2 := a.X+boxW, b.X
			y1, y2 := port(p, false), port(p, true)
			xm := x1 + colGap*3/10
			w.Path = "M" + strconv.Itoa(x1) + " " + strconv.Itoa(y1) + "H" + strconv.Itoa(xm)
			if y1 != y2 {
				w.Path += "V" + strconv.Itoa(y2)
			}
			w.Path += "H" + strconv.Itoa(x2)
			// Over the last stretch, where it meets its target. When a label
			// is wider than that stretch it is let run, over the line's end.
			w.LX, w.LY = xm+4, y2-15
			if x2-xm < len(label)*labelW+8 && layer[p.to]-layer[p.from] == 1 {
				w.LX = x1 + 4
			}
		}
		d.Wires = append(d.Wires, w)
		d.Spoken = append(d.Spoken, mapLabel(a.FlowNode)+" to "+mapLabel(b.FlowNode)+", "+label)
	}
	return d
}
