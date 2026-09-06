package sfngraph

import "sort"

// rank assigns each node a layer and orders the nodes within each layer,
// leaving the order in the slice and the layer in Node.rank.
//
// Layer is the longest path from StartAt, which is what puts a state under
// everything that can lead to it. Longest path needs a DAG, and a workflow is
// not one — a Catch that sends a state back to a retry loop, or a Choice
// whose Default returns to a poll, both close cycles. A depth-first walk from
// StartAt finds those closing edges (they point at a node still on the walk's
// stack), marks them Back and leaves them out of the ranking; routeEdges
// draws them as loops instead of making them fit.
//
// Within a layer, nodes are sorted by the mean position of their neighbours
// in the layer above, then below — the barycenter heuristic, two sweeps.
// It is the cheapest thing that stops arrows crossing on the shapes workflows
// usually take (a Choice fanning out, branches joining back), and it does not
// try to do better than that.
func rank(nodes []*Node, edges []*Edge, start *Node) {
	if len(nodes) == 0 {
		return
	}
	out := map[*Node][]*Edge{}
	for _, e := range edges {
		out[e.from] = append(out[e.from], e)
	}

	const (
		unseen = iota
		onStack
		done
	)
	state := map[*Node]int{}
	var post []*Node // finish order; reversed, it is a topological order of the forward edges
	var walk func(n *Node)
	walk = func(n *Node) {
		state[n] = onStack
		for _, e := range out[n] {
			switch state[e.to] {
			case onStack:
				e.Back = true
			case unseen:
				walk(e.to)
			}
		}
		state[n] = done
		post = append(post, n)
	}
	// StartAt first, then anything it cannot reach — a state nobody points at
	// is still a state, and the analyser is the one that complains about it.
	if start != nil {
		walk(start)
	}
	for _, n := range nodes {
		if state[n] == unseen {
			walk(n)
		}
	}

	for _, n := range nodes {
		n.rank = 0
	}
	for i := len(post) - 1; i >= 0; i-- {
		n := post[i]
		for _, e := range out[n] {
			if !e.Back && e.to != n {
				e.to.rank = max(e.to.rank, n.rank+1)
			}
		}
	}

	// Initial order within a layer is discovery order, which already follows
	// the document for the common case; the sweeps then pull each node under
	// the mean of what feeds it.
	pos := map[*Node]int{}
	for i := len(post) - 1; i >= 0; i-- {
		pos[post[i]] = len(post) - 1 - i
	}
	in := map[*Node][]*Node{}
	for _, e := range edges {
		if !e.Back && e.from != e.to {
			in[e.to] = append(in[e.to], e.from)
		}
	}
	maxRank := 0
	for _, n := range nodes {
		maxRank = max(maxRank, n.rank)
	}
	sweep := func(r int, neighbours func(*Node) []*Node) {
		var layer []*Node
		for _, n := range nodes {
			if n.rank == r {
				layer = append(layer, n)
			}
		}
		bary := map[*Node]float64{}
		for _, n := range layer {
			ns := neighbours(n)
			if len(ns) == 0 {
				bary[n] = float64(pos[n])
				continue
			}
			sum := 0
			for _, m := range ns {
				sum += pos[m]
			}
			bary[n] = float64(sum) / float64(len(ns))
		}
		sort.SliceStable(layer, func(i, j int) bool { return bary[layer[i]] < bary[layer[j]] })
		for i, n := range layer {
			pos[n] = i
		}
	}
	succ := func(n *Node) []*Node {
		var ns []*Node
		for _, e := range out[n] {
			if !e.Back && e.to != n {
				ns = append(ns, e.to)
			}
		}
		return ns
	}
	pred := func(n *Node) []*Node { return in[n] }
	for r := 0; r <= maxRank; r++ {
		sweep(r, pred)
	}
	for r := maxRank; r >= 0; r-- {
		sweep(r, succ)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].rank != nodes[j].rank {
			return nodes[i].rank < nodes[j].rank
		}
		return pos[nodes[i]] < pos[nodes[j]]
	})
}
