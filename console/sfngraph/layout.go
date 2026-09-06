// Package sfngraph turns a Step Functions definition into the picture the AWS
// console draws: boxes for states, arrows for transitions, nested boxes for
// Parallel branches and Map processors.
//
// The layout is computed here, in Go, rather than in the browser. The console
// has no build step and no graph library, and a template that is handed
// absolute coordinates only has to place what it is given — which is also
// what makes the graph testable without a DOM: overlap and reachability are
// properties of numbers, not of pixels.
package sfngraph

import (
	"sort"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/asl"
)

// Geometry. Fixed rather than measured because SVG text cannot be measured
// server-side; the node is wide enough for a truncated label, and the label is
// truncated to fit the node (see labels.go).
const (
	NodeW     = 160
	NodeH     = 44
	rankGap   = 40 // vertical room between ranks — the edges live here
	nodeGap   = 24 // horizontal room between siblings in one rank
	pad       = 14 // inset from a container's border to its contents
	headerH   = 26 // a container's title strip
	branchGap = 28 // between the branches of a Parallel
	backLane  = 18 // the lane a loop-back edge runs up, right of the nodes
)

// Graph is the laid-out picture: every node and edge at every depth, in
// absolute coordinates, containers listed before their contents so an SVG
// painted in slice order draws children on top.
type Graph struct {
	Nodes  []*Node
	Edges  []*Edge
	Width  int
	Height int
}

// Node is one state. A Parallel or a Map is a container: its box encloses
// the sub-graphs of its branches or its processor.
type Node struct {
	ID        string // path-qualified: "Fan/1/Ship" is state Ship in branch 1 of Fan
	Name      string
	Label     string // Name, shortened to fit the box
	Type      string // the ASL Type, as written
	Kind      string // Type lower-cased: a CSS class fragment
	Container bool
	Start     bool // its definition's StartAt
	Depth     int
	X, Y      int
	W, H      int

	// Set by Overlay; zero for a definition without an execution.
	Status  string // succeeded | failed | caught | running | cancelled | "" (never entered)
	Entered int
	Exited  int
	Retries int

	rank int
}

// Edge is one transition. Kind says why it exists — a Next, a Choice rule,
// a Choice Default, or a Catch — and Back marks a loop back up the graph.
type Edge struct {
	From, To string
	Kind     string // next | choice | default | catch
	Label    string
	Back     bool
	Points   []Point
	LX, LY   int // label anchor

	from, to *Node
}

// Point is an SVG coordinate.
type Point struct{ X, Y int }

// Path is the polyline's points attribute.
func (e *Edge) Path() string {
	s := ""
	for i, p := range e.Points {
		if i > 0 {
			s += " "
		}
		s += strconv.Itoa(p.X) + "," + strconv.Itoa(p.Y)
	}
	return s
}

// Right is where a right-aligned annotation sits inside the node.
func (n *Node) Right() int { return n.W - 8 }

// Layout lays out a whole definition. Deterministic: the same definition
// always yields the same picture, so a poll that redraws it does not jitter.
func Layout(def *asl.Definition) *Graph {
	g := &Graph{Width: 2 * pad, Height: 2 * pad}
	if def == nil {
		return g
	}
	s := layoutDef(def, "", 0)
	s.shift(pad, pad)
	g.Nodes, g.Edges = s.nodes, s.edges
	g.Width, g.Height = s.w+2*pad, s.h+2*pad
	return g
}

// sub is one definition's laid-out contents, relative to its own origin.
type sub struct {
	nodes []*Node
	edges []*Edge
	w, h  int
}

func (s *sub) shift(dx, dy int) {
	for _, n := range s.nodes {
		n.X += dx
		n.Y += dy
	}
	for _, e := range s.edges {
		for i := range e.Points {
			e.Points[i].X += dx
			e.Points[i].Y += dy
		}
		e.LX += dx
		e.LY += dy
	}
}

// stateNames is the definition's states in document order. Order is what the
// parser fills; a definition built by hand in a test may not have it, and a
// map walk would make the layout differ run to run.
func stateNames(def *asl.Definition) []string {
	if len(def.Order) == len(def.States) {
		return def.Order
	}
	names := make([]string, 0, len(def.States))
	for n := range def.States {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func layoutDef(def *asl.Definition, prefix string, depth int) *sub {
	names := stateNames(def)
	own := make([]*Node, 0, len(names))
	byName := make(map[string]*Node, len(names))
	kids := map[string][]*sub{}
	for _, name := range names {
		st := def.States[name]
		n := &Node{
			ID: prefix + name, Name: name, Label: shorten(name, 20),
			Type: string(st.Type), Kind: kindOf(st.Type), Depth: depth,
			Start: name == def.StartAt, W: NodeW, H: NodeH,
		}
		var inner []*asl.Definition
		switch {
		case st.Type == asl.Parallel:
			inner = st.Branches
		case st.Type == asl.Map && st.Processor() != nil:
			inner = []*asl.Definition{st.Processor()}
		}
		if len(inner) > 0 {
			n.Container = true
			var ks []*sub
			innerW, innerH := 0, 0
			for i, d := range inner {
				k := layoutDef(d, n.ID+"/"+strconv.Itoa(i)+"/", depth+1)
				ks = append(ks, k)
				if i > 0 {
					innerW += branchGap
				}
				innerW += k.w
				innerH = max(innerH, k.h)
			}
			n.W = max(NodeW, innerW+2*pad)
			n.H = headerH + innerH + 2*pad
			// Branches sit side by side, centred under the title.
			x := (n.W - innerW) / 2
			for _, k := range ks {
				k.shift(x, headerH+pad)
				x += k.w + branchGap
			}
			kids[n.ID] = ks
		}
		own = append(own, n)
		byName[name] = n
	}

	edges := collectEdges(def, names, byName)
	rank(own, edges, byName[def.StartAt])
	placeRanks(own)
	s := &sub{}
	for _, n := range own {
		s.w = max(s.w, n.X+n.W)
		s.h = max(s.h, n.Y+n.H)
	}
	s.w = max(s.w, routeEdges(edges, own, s.w))
	// Assemble: each container immediately followed by what it holds, so the
	// paint order is right, and every descendant shifted into this frame.
	for _, n := range own {
		s.nodes = append(s.nodes, n)
		for _, k := range kids[n.ID] {
			k.shift(n.X, n.Y)
			s.nodes = append(s.nodes, k.nodes...)
			s.edges = append(s.edges, k.edges...)
		}
	}
	s.edges = append(s.edges, edges...)
	return s
}

// collectEdges reads every transition out of a definition's states. Two
// rules of one Choice that lead to the same state — or a rule and the
// Default — become one edge carrying both labels: two arrows on the same
// path would be one arrow, drawn twice. A Catch stays its own edge, because
// it is drawn differently.
func collectEdges(def *asl.Definition, names []string, byName map[string]*Node) []*Edge {
	var edges []*Edge
	index := map[string]*Edge{}
	add := func(from *Node, to, kind, label string) {
		target, ok := byName[to]
		if !ok {
			return // a dangling Next is the analyser's finding, not the picture's
		}
		key := from.ID + "\x00" + target.ID
		if kind == "catch" {
			key += "\x00catch"
		}
		if e, ok := index[key]; ok {
			if label != "" {
				e.Label = shorten(e.Label+" | "+label, 30)
			}
			return
		}
		e := &Edge{From: from.ID, To: target.ID, Kind: kind, Label: label, from: from, to: target}
		index[key] = e
		edges = append(edges, e)
	}
	for _, name := range names {
		st, from := def.States[name], byName[name]
		if st.Next != "" {
			add(from, st.Next, "next", "")
		}
		for _, r := range st.Choices {
			if r.Next != "" {
				add(from, r.Next, "choice", ruleLabel(r))
			}
		}
		if st.Default != "" {
			add(from, st.Default, "default", "default")
		}
		for _, c := range st.Catch {
			if c.Next != "" {
				add(from, c.Next, "catch", catchLabel(c))
			}
		}
	}
	return edges
}

// placeRanks assigns coordinates from ranks and the within-rank order that
// rank() left in the node slice. A rank is as tall as its tallest node, and
// each row is centred on the widest one — the cheap approximation of "a node
// sits under its parent" that reads correctly for the shapes workflows take.
func placeRanks(nodes []*Node) {
	rows := map[int][]*Node{}
	maxRank := 0
	for _, n := range nodes {
		rows[n.rank] = append(rows[n.rank], n)
		maxRank = max(maxRank, n.rank)
	}
	widest := 0
	rowW := make([]int, maxRank+1)
	for r := 0; r <= maxRank; r++ {
		for i, n := range rows[r] {
			if i > 0 {
				rowW[r] += nodeGap
			}
			rowW[r] += n.W
		}
		widest = max(widest, rowW[r])
	}
	y := 0
	for r := 0; r <= maxRank; r++ {
		x, rowH := (widest-rowW[r])/2, 0
		for _, n := range rows[r] {
			n.X, n.Y = x, y
			x += n.W + nodeGap
			rowH = max(rowH, n.H)
		}
		y += rowH + rankGap
	}
}

// routeEdges draws each edge as an orthogonal polyline from the bottom of its
// source to the top of its target: straight when they line up, otherwise one
// horizontal jog halfway down the gap. An edge that skips ranks would cut
// through whatever sits in them, so it detours down a lane to the right of
// those nodes; a back edge does the same, upward, right of everything. The
// return value is the rightmost x any detour reached, so the caller can
// widen the frame to hold it.
func routeEdges(edges []*Edge, nodes []*Node, width int) int {
	reach := 0
	for _, e := range edges {
		s, t := e.from, e.to
		sx, sy := s.X+s.W/2, s.Y+s.H
		tx, ty := t.X+t.W/2, t.Y
		switch {
		case s == t:
			// A Catch that re-enters its own state: a loop on the right edge.
			xr := s.X + s.W + backLane/2
			e.Points = []Point{{s.X + s.W, s.Y + s.H - 10}, {xr, s.Y + s.H - 10}, {xr, s.Y + 10}, {s.X + s.W, s.Y + 10}}
			e.LX, e.LY = xr+4, s.Y+s.H/2+4
			e.Back = true
			reach = max(reach, xr+backLane/2)
		case e.Back:
			xr := width + backLane/2
			// Two thirds down the gap, so it does not share a line with a
			// forward detour leaving the same node halfway down.
			yd, yu := sy+2*rankGap/3, ty-rankGap/3
			e.Points = []Point{{sx, sy}, {sx, yd}, {xr, yd}, {xr, yu}, {tx, yu}, {tx, ty}}
			e.LX, e.LY = xr+4, (yd+yu)/2+4
			reach = max(reach, xr+backLane/2)
		case t.rank-s.rank > 1:
			xr := max(sx, tx)
			for _, n := range nodes {
				if n.rank > s.rank && n.rank < t.rank {
					xr = max(xr, n.X+n.W)
				}
			}
			xr += nodeGap / 2
			yd, yu := sy+rankGap/2, ty-rankGap/2
			e.Points = []Point{{sx, sy}, {sx, yd}, {xr, yd}, {xr, yu}, {tx, yu}, {tx, ty}}
			e.LX, e.LY = xr+4, (yd+yu)/2+4
			reach = max(reach, xr+nodeGap/2)
		case sx == tx:
			e.Points = []Point{{sx, sy}, {tx, ty}}
			e.LX, e.LY = sx+6, (sy+ty)/2-3
		default:
			my := sy + (ty-sy)/2
			e.Points = []Point{{sx, sy}, {sx, my}, {tx, my}, {tx, ty}}
			e.LX, e.LY = tx+6, my-4
		}
	}
	return reach
}

func kindOf(t asl.StateType) string { return strings.ToLower(string(t)) }
