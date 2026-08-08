package console

// Kinesis console handlers.

import (
	"net/http"
	"strconv"
)

func (c *Console) kinesisStreams(w http.ResponseWriter, r *http.Request) {
	streams, err := c.be.ListStreams(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "kinesis_home", map[string]any{"List": streams, "Title": "Kinesis"})
}

func (c *Console) kinesisStream(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("stream")
	summary, err := c.be.StreamSummary(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	shards, _ := c.be.ListShards(r.Context(), name)
	tags, _ := c.be.StreamTags(r.Context(), name)
	streams, _ := c.be.ListStreams(r.Context())

	// The selected shard defaults to the first open one, since that is where
	// new records land and therefore what someone is usually looking for.
	sel := r.URL.Query().Get("shard")
	if sel == "" {
		for _, sh := range shards {
			if !sh.Closed {
				sel = sh.ID
				break
			}
		}
		if sel == "" && len(shards) > 0 {
			sel = shards[0].ID
		}
	}
	var records []KRecord
	if sel != "" {
		records, _ = c.be.ReadShard(r.Context(), name, sel, 200)
	}
	// Offer a split midpoint for the selected shard, so the split control does
	// not require anyone to type a 39-digit number.
	midpoint := ""
	for _, sh := range shards {
		if sh.ID == sel && !sh.Closed {
			midpoint = MidpointOf(sh.StartKey, sh.EndKey)
		}
	}

	c.render(w, r, "kinesis_stream", map[string]any{
		"Stream": name, "Summary": summary, "Shards": shards, "Sel": sel,
		"Records": records, "Midpoint": midpoint, "Tags": tags,
		"List": streams, "Title": name + " · Kinesis",
		"Conn": c.be.Neighbors(r.Context(), "kinesis", name),
	})
}

func (c *Console) kinesisCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	shards, _ := strconv.Atoi(r.FormValue("shards"))
	if err := c.be.CreateStream(r.Context(), name, shards); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, "/kinesis/"+name, "Stream created")
}

func (c *Console) kinesisDelete(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteStream(r.Context(), r.PathValue("stream")); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, "/kinesis", "Stream deleted")
}

func (c *Console) kinesisPut(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	key := r.FormValue("partitionKey")
	if key == "" {
		key = "console"
	}
	shard, err := c.be.PutRecord(r.Context(), stream, key, r.FormValue("data"))
	if err != nil {
		c.fail(w, err)
		return
	}
	// Land on the shard the key actually routed to — seeing the record appear
	// where MD5 put it is the point of doing this from the console.
	c.redirect(w, r, "/kinesis/"+stream+"?shard="+shard, "Record put on "+shard)
}

func (c *Console) kinesisSplit(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	shard := r.FormValue("shard")
	at := r.FormValue("at")
	if at == "" {
		c.redirect(w, r, "/kinesis/"+stream, "That shard covers too small a hash range to split")
		return
	}
	if err := c.be.SplitShard(r.Context(), stream, shard, at); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, "/kinesis/"+stream, "Split "+shard)
}

func (c *Console) kinesisRetention(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	hours, _ := strconv.Atoi(r.FormValue("hours"))
	if err := c.be.SetRetention(r.Context(), stream, hours); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, "/kinesis/"+stream, "Retention set")
}
