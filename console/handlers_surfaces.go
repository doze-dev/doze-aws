package console

import (
	"net/http"
	"strconv"
	"strings"
)

// ---- Flows (home) ----

func (c *Console) flows(w http.ResponseWriter, r *http.Request) {
	g := c.be.graphCached(r.Context())
	c.render(w, r, "flows", map[string]any{
		"Graph": g, "Hash": g.hash(), "Title": "Flows",
	})
}

// flowsData is the polled refresh: 204 when the wiring + counts are unchanged.
func (c *Console) flowsData(w http.ResponseWriter, r *http.Request) {
	g := c.be.graphCached(r.Context())
	h := g.hash()
	if liveUnchanged(w, r, h) {
		return
	}
	c.partial(w, "flow_canvas", map[string]any{"Graph": g, "Hash": h})
}

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
}

func (c *Console) trafficEntries(since int64) []trafficRow {
	if c.rec == nil {
		return nil
	}
	raw := c.rec.Entries(since)
	rows := make([]trafficRow, 0, len(raw))
	for _, e := range raw {
		rows = append(rows, trafficRow{
			Time:    e.At.Local().Format("15:04:05.000"),
			Service: e.Service, Action: e.Action, Resource: e.Resource,
			Status: e.Status, Millis: strconv.FormatFloat(e.Millis, 'f', -1, 64),
			IsErr: e.Status >= 400, Body: e.ReqBody, Curl: e.Curl(), Seq: e.Seq,
		})
	}
	return rows
}
