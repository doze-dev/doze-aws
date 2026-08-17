package console

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// ---- Traffic ----

func (c *Console) traffic(w http.ResponseWriter, r *http.Request) {
	c.render(w, r, "traffic", map[string]any{
		"Entries": c.trafficEntries(0), "Enabled": c.rec != nil, "Title": "Traffic",
	})
}

func (c *Console) trafficFeed(w http.ResponseWriter, r *http.Request) {
	// Cheap probe first: the idle tick (the common case) must not copy and
	// format the whole ring just to throw it away on a 204.
	hash := "0"
	if c.rec != nil {
		if s := c.rec.LastSeq(); s > 0 {
			hash = strconv.FormatInt(s, 10)
		}
	}
	if liveUnchanged(w, r, hash) {
		return
	}
	entries := c.trafficEntries(0)
	// Poll returns only the rows region (traffic_rows); the filter state lives on
	// the outer wrapper the poll never touches.
	c.partial(w, "traffic_rows", map[string]any{"Entries": entries, "Hash": hash, "Endpoint": endpointHost(r)})
}

// trafficEntry renders the inspector drawer for one recorded call.
func (c *Console) trafficEntry(w http.ResponseWriter, r *http.Request) {
	seq, _ := strconv.ParseInt(r.URL.Query().Get("seq"), 10, 64)
	if c.rec == nil {
		http.Error(w, "traffic capture is off", http.StatusNotFound)
		return
	}
	e, ok := c.rec.Get(seq)
	if !ok {
		c.partial(w, "traffic_drawer_gone", map[string]any{})
		return
	}
	req := e.ReqBody
	if strings.Contains(e.CT, "json") {
		req = prettyJSON(req)
	}
	resp := e.RespBody
	if strings.Contains(e.RespCT, "json") {
		resp = prettyJSON(resp)
	}
	c.partial(w, "traffic_drawer", map[string]any{
		"E": e, "Req": req, "Resp": resp,
		"Millis": strconv.FormatFloat(e.Millis, 'f', -1, 64),
		"Time":   e.At.Local().Format("15:04:05.000"),
	})
}

// trafficClear empties the recorder ring and hands back the (now empty) rows.
func (c *Console) trafficClear(w http.ResponseWriter, r *http.Request) {
	if c.rec != nil {
		c.rec.Clear()
	}
	c.trafficFeed(w, r)
}

// trafficRow is a display-ready traffic entry.
type trafficRow struct {
	Time     string
	Service  string
	Action   string
	Resource string
	Status   int
	Millis   string
	IsErr    bool
	Body     string
	Curl     string
	Seq      int64
	// Refused is the parsed reason a 4xx/5xx was refused, or nil. Carried on
	// the row rather than looked up in the drawer because the reason belongs
	// where you are already scanning for the failure.
	Refused *Refusal

	// Depth is how far this row nests under the call that caused it. Zero for
	// anything a client asked for directly.
	Depth int
	// Cascade marks internal work the gateway never saw — a bucket
	// notification, a topic fan-out, a rule dispatch.
	Cascade bool
	// Via names what emitted it, when that is not obvious from the action.
	Via string
}

// trafficEntries renders the ring as the wire shows it: newest first, with the
// work a call caused nested underneath it.
//
// Ordering is the whole difficulty. The ring is newest-first, so a parent —
// which by definition was recorded earlier — sits BELOW its children, and a
// cascade read that way is backwards: you meet the effects before the cause.
// So groups are ordered by the parent's recency and read top-down within a
// group, cause first.
//
// A child can also be stored before its parent, because a notification
// delivered on its own goroutine can finish before the request that triggered
// it returns. Grouping by sequence number rather than by position is what makes
// that harmless.
func (c *Console) trafficEntries(since int64) []trafficRow {
	if c.rec == nil {
		return nil
	}
	raw := c.rec.Entries(since)

	kids := map[int64][]TrafficEntry{}
	known := map[int64]bool{}
	for _, e := range raw {
		known[e.Seq] = true
	}
	for _, e := range raw {
		if e.Parent != 0 && known[e.Parent] {
			kids[e.Parent] = append(kids[e.Parent], e)
		}
	}
	// Children read in the order they happened, which is the order they fanned
	// out — the opposite of the ring's.
	for k := range kids {
		sort.Slice(kids[k], func(i, j int) bool { return kids[k][i].Seq < kids[k][j].Seq })
	}

	rows := make([]trafficRow, 0, len(raw))
	var emit func(e TrafficEntry, depth int)
	emit = func(e TrafficEntry, depth int) {
		rows = append(rows, rowOf(e, depth))
		for _, k := range kids[e.Seq] {
			emit(k, depth+1)
		}
	}
	for _, e := range raw {
		// A cascade whose parent is still in the window is emitted with it. One
		// whose parent has aged out stands alone rather than disappearing.
		if e.Parent != 0 && known[e.Parent] {
			continue
		}
		emit(e, 0)
	}
	return rows
}

func rowOf(e TrafficEntry, depth int) trafficRow {
	return trafficRow{
		Time:    e.At.Local().Format("15:04:05.000"),
		Service: e.Service, Action: e.Action, Resource: e.Resource,
		Status: e.Status, Millis: strconv.FormatFloat(e.Millis, 'f', -1, 64),
		IsErr: e.Status >= 400, Body: e.ReqBody, Curl: e.Curl(), Seq: e.Seq,
		Refused: e.Failure(),
		Depth:   depth, Cascade: e.IsCascade(), Via: e.Via,
	}
}
