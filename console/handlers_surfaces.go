package console

import (
	"net/http"
	"strconv"
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
	if liveUnchanged(w, r, g.hash()) {
		return
	}
	c.partial(w, "flow_canvas", map[string]any{"Graph": g, "Hash": g.hash()})
}

// ---- Traffic ----

func (c *Console) traffic(w http.ResponseWriter, r *http.Request) {
	c.render(w, r, "traffic", map[string]any{
		"Entries": c.trafficEntries(0), "Enabled": c.rec != nil, "Title": "Traffic",
	})
}

func (c *Console) trafficFeed(w http.ResponseWriter, r *http.Request) {
	entries := c.trafficEntries(0)
	hash := "0"
	if len(entries) > 0 {
		hash = strconv.FormatInt(entries[0].Seq, 10)
	}
	if liveUnchanged(w, r, hash) {
		return
	}
	c.partial(w, "traffic_feed", map[string]any{"Entries": entries, "Hash": hash})
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
