package console

// Kinesis console handlers.
//
// The stream page is structural — shards, hash space, lineage — and the
// records live in their own explorer. Keeping them apart is not only tidiness:
// a stream is a log, so "show me the records" is a query with a start, a
// window and a cursor, and pretending it is a list that fits under the shard
// table stops being true the moment anything real is written to it.

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (c *Console) kinesisStreams(w http.ResponseWriter, r *http.Request) {
	streams, err := c.be.ListStreams(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "kinesis_home", map[string]any{"List": streams, "Title": "Kinesis"})
}

// streamPage gathers what every tab of a stream needs.
func (c *Console) streamPage(r *http.Request, name string) (map[string]any, error) {
	summary, err := c.be.StreamSummary(r.Context(), name)
	if err != nil {
		return nil, err
	}
	shards, err := c.be.ListShards(r.Context(), name)
	if err != nil {
		return nil, err
	}
	tags, _ := c.be.StreamTags(r.Context(), name)
	streams, _ := c.be.ListStreams(r.Context())
	return map[string]any{
		"Stream": name, "Summary": summary, "Shards": shards, "Tags": tags,
		"List": streams, "Title": name + " · Kinesis",
		"Conn": c.be.Neighbors(r.Context(), "kinesis", name),
	}, nil
}

func (c *Console) kinesisStream(w http.ResponseWriter, r *http.Request) {
	data, err := c.streamPage(r, r.PathValue("stream"))
	if err != nil {
		c.fail(w, err)
		return
	}
	data["Tab"] = "shards"
	c.render(w, r, "kinesis_stream", data)
}

// kinesisShardDepth answers the lazy per-shard count. Kinesis has no count
// API, so this reads — which is exactly why it is not on the page's critical
// path and why it is allowed to answer "1000+".
func (c *Console) kinesisShardDepth(w http.ResponseWriter, r *http.Request) {
	n, err := c.be.CountShard(r.Context(), r.PathValue("stream"), r.PathValue("shard"))
	if err != nil {
		// A count is decoration; failing it should not blank out the row.
		c.partial(w, "kinesis_depth", map[string]any{"Err": true})
		return
	}
	c.partial(w, "kinesis_depth", map[string]any{"Count": n})
}

// ---- the records explorer ----

// windows are the relative start points the explorer offers. Each becomes an
// AT_TIMESTAMP iterator, which the store resolves by seeking back from the
// tail, so a short window stays cheap on a deep shard.
var windows = map[string]time.Duration{
	"5m": 5 * time.Minute, "15m": 15 * time.Minute,
	"1h": time.Hour, "24h": 24 * time.Hour,
}

// recordQuery reads the filter bar. It separates what Kinesis can push down —
// where to start reading — from what can only be applied to whatever came
// back, because conflating the two is how an explorer starts lying about
// empty results.
func (c *Console) recordQuery(r *http.Request, stream string, shards []Shard) RecordQuery {
	q := RecordQuery{
		Stream:    stream,
		Shard:     r.FormValue("shard"),
		Partition: strings.TrimSpace(r.FormValue("pk")),
		Contains:  r.FormValue("contains"),
	}
	q.Follow = r.FormValue("follow") == "1"
	q.Limit, _ = strconv.Atoi(r.FormValue("limit"))
	if q.Limit <= 0 {
		q.Limit = 50
	}

	start := r.FormValue("start")
	q.StartOpt, q.UntilOpt = start, r.FormValue("until")
	switch {
	case start == "latest":
		q.Start = startAt{Mode: "latest"}
	case start == "seq" && r.FormValue("seq") != "":
		q.Start = startAt{Mode: "seq", Seq: r.FormValue("seq")}
	case windows[start] != 0:
		q.Start = startAt{Mode: "time", Since: time.Now().Add(-windows[start])}
	default:
		q.Start = startAt{Mode: "horizon"}
	}
	// A cursor is a resumption — of a "Load more" or of a follow poll — and
	// always wins over the start control.
	if cur := r.FormValue("cursor"); cur != "" {
		q.Start = startAt{Mode: "seq", Seq: cur}
	} else if q.Follow {
		// Following the tip has no sequence to resume from until something
		// arrives, and re-creating a LATEST iterator each poll would sit at a
		// moving tip and never return anything. An arrival time is an anchor
		// that holds still, so the first poll takes one and every later poll
		// carries it forward until a real record supplies a sequence.
		since := time.Now()
		if ns, err := strconv.ParseInt(r.FormValue("since"), 10, 64); err == nil {
			since = time.Unix(0, ns)
		}
		if start == "latest" || r.FormValue("since") != "" {
			q.Start = startAt{Mode: "time", Since: since}
		}
	}

	if until := r.FormValue("until"); until != "" {
		// datetime-local arrives without a zone; read it as local time, which
		// is what someone typing into the box meant.
		if t, err := time.ParseInLocation("2006-01-02T15:04", until, time.Local); err == nil {
			q.Until = t
		}
	}

	// A partition key routes by MD5 to exactly one shard, so naming one turns
	// a post-filter into a shard selection — the one place this explorer can
	// beat a plain text match.
	if q.Partition != "" && q.Shard == "" {
		if id := ShardForKey(shards, q.Partition); id != "" {
			q.Shard = id
			q.Routed = true
		}
	}
	return q
}

// kinesisRecords is the explorer's own view.
func (c *Console) kinesisRecords(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("stream")
	data, err := c.streamPage(r, name)
	if err != nil {
		c.fail(w, err)
		return
	}
	data["Tab"] = "records"
	data["Title"] = name + " · Records"

	shards, _ := data["Shards"].([]Shard)
	q := c.recordQuery(r, name, shards)
	page, err := c.be.ReadRecords(r.Context(), q)
	if err != nil {
		c.fail(w, err)
		return
	}
	fillResults(data, q, page)
	c.render(w, r, "kinesis_records", data)
}

// kinesisRecordsQuery runs a query from the filter bar or a cursor. A cursor
// means rows are being appended, so only the rows come back.
func (c *Console) kinesisRecordsQuery(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("stream")
	shards, err := c.be.ListShards(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	q := c.recordQuery(r, name, shards)
	page, err := c.be.ReadRecords(r.Context(), q)
	if err != nil {
		c.fail(w, err)
		return
	}
	data := map[string]any{"Prefix": c.prefix, "Stream": name, "Shards": shards}
	fillResults(data, q, page)
	if r.FormValue("cursor") != "" {
		c.partial(w, "kinesis_record_rows", data)
		return
	}
	c.partial(w, "kinesis_record_table", data)
}

// fillResults puts a page and the query that produced it where the templates
// expect them, including the values a "Load more" or a follow poll must carry
// forward so the next read repeats the same query.
func fillResults(data map[string]any, q RecordQuery, page *RecordPage) {
	data["Result"] = page
	data["Query"] = q
	data["Follow"] = q.Follow
	carry := map[string]string{
		"shard": q.Shard, "pk": q.Partition, "contains": q.Contains,
		"until": q.UntilOpt, "limit": strconv.Itoa(q.Limit), "cursor": page.Cursor,
	}
	if q.Follow {
		carry["follow"] = "1"
		// Until a record hands over a sequence, the poll keeps its time anchor
		// so records arriving between polls are not stepped over.
		if page.Cursor == "" && q.Start.Mode == "time" {
			carry["since"] = strconv.FormatInt(q.Start.Since.UnixNano(), 10)
			carry["start"] = "latest"
		}
	}
	data["NextVals"] = jsonVals(carry)
}

// kinesisRecord expands one record. The listing truncates payloads, so the
// whole thing is fetched only for the row that asks — a page of 500 records at
// a megabyte each is not something to hand a browser on the chance someone
// looks.
func (c *Console) kinesisRecord(w http.ResponseWriter, r *http.Request) {
	rec, err := c.be.ReadOne(r.Context(), r.PathValue("stream"),
		r.FormValue("shard"), r.FormValue("seq"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kinesis_record_detail", map[string]any{"Rec": rec, "Prefix": c.prefix})
}

// kinesisDetails is the configuration tab: everything about a stream that is
// set rather than read.
func (c *Console) kinesisDetails(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("stream")
	data, err := c.streamPage(r, name)
	if err != nil {
		c.fail(w, err)
		return
	}
	data["Tab"] = "details"
	shards, _ := data["Shards"].([]Shard)
	data["Pairs"] = MergeablePairs(shards)
	open := 0
	for _, sh := range shards {
		if !sh.Closed {
			open++
		}
	}
	data["OpenShards"] = open

	// The rest of the tab is configuration the service stores but does not act
	// on locally; it is fetched here so the page shows what is actually set.
	summary, _ := data["Summary"].(*Stream)
	if summary != nil {
		data["Consumers"], _ = c.be.ListConsumers(r.Context(), summary.ARN)
		data["Policy"], _ = c.be.StreamPolicy(r.Context(), summary.ARN)
	}
	data["AllMetrics"] = ShardMetrics
	data["Keys"], _ = c.be.ListKeys(r.Context())
	data["Limits"], _ = c.be.KinesisLimits(r.Context())
	c.render(w, r, "kinesis_details", data)
}

// kinesisMerge folds two adjacent shards back into one. The form offers only
// pairs the service will accept, so a rejection here means the layout changed
// under the page rather than that someone picked badly.
func (c *Console) kinesisMerge(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	left, right := r.FormValue("left"), r.FormValue("right")
	if left == "" || right == "" {
		c.redirect(w, r, c.prefix+"/kinesis/"+stream+"/details", "Pick two adjacent shards to merge")
		return
	}
	if err := c.be.MergeShards(r.Context(), stream, left, right); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream, "Merged "+left+" + "+right)
}

func (c *Console) kinesisScale(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	target, _ := strconv.Atoi(r.FormValue("shards"))
	if err := c.be.UpdateShardCount(r.Context(), stream, target); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream, "Scaled to "+strconv.Itoa(target)+" shards")
}

func (c *Console) kinesisMode(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	summary, err := c.be.StreamSummary(r.Context(), stream)
	if err != nil {
		c.fail(w, err)
		return
	}
	mode := r.FormValue("mode")
	if err := c.be.UpdateStreamMode(r.Context(), summary.ARN, mode); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream+"/details", "Stream mode set to "+mode)
}

// kinesisEncryption sets or clears the stream's KMS key. This is metadata:
// the local store is not encrypted either way, and the page says so.
func (c *Console) kinesisEncryption(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	key := r.FormValue("key")
	var err error
	msg := "Encryption cleared"
	if key == "" {
		err = c.be.StopEncryption(r.Context(), stream)
	} else {
		err = c.be.StartEncryption(r.Context(), stream, key)
		msg = "Encryption set to " + key
	}
	if err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream+"/details", msg)
}

func (c *Console) kinesisMetrics(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	if err := r.ParseForm(); err != nil {
		c.fail(w, err)
		return
	}
	if err := c.be.SetMetrics(r.Context(), stream, r.Form["metric"]); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream+"/details", "Shard-level metrics updated")
}

func (c *Console) kinesisConsumerAdd(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	summary, err := c.be.StreamSummary(r.Context(), stream)
	if err != nil {
		c.fail(w, err)
		return
	}
	if err := c.be.RegisterConsumer(r.Context(), summary.ARN, r.FormValue("name")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream+"/details", "Consumer registered")
}

func (c *Console) kinesisConsumerDel(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	summary, err := c.be.StreamSummary(r.Context(), stream)
	if err != nil {
		c.fail(w, err)
		return
	}
	if err := c.be.DeregisterConsumer(r.Context(), summary.ARN, r.FormValue("name")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream+"/details", "Consumer deregistered")
}

func (c *Console) kinesisPolicy(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	summary, err := c.be.StreamSummary(r.Context(), stream)
	if err != nil {
		c.fail(w, err)
		return
	}
	policy := strings.TrimSpace(r.FormValue("policy"))
	msg := "Resource policy saved"
	if policy == "" {
		err, msg = c.be.DeleteStreamPolicy(r.Context(), summary.ARN), "Resource policy removed"
	} else {
		err = c.be.PutStreamPolicy(r.Context(), summary.ARN, policy)
	}
	if err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream+"/details", msg)
}

// ---- mutations ----

func (c *Console) kinesisCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	shards, _ := strconv.Atoi(r.FormValue("shards"))
	if err := c.be.CreateStream(r.Context(), name, shards); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/kinesis/"+name, "Stream created")
}

func (c *Console) kinesisDelete(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteStream(r.Context(), r.PathValue("stream")); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/kinesis", "Stream deleted")
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
	// Land in the explorer on the shard the key actually routed to — seeing
	// the record appear where MD5 put it is the point of doing this here.
	c.redirect(w, r, c.prefix+"/kinesis/"+stream+"/records?shard="+shard+"&start=latest",
		"Record put on "+shard)
}

func (c *Console) kinesisSplit(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	shard := r.FormValue("shard")
	at := r.FormValue("at")
	if at == "" {
		c.redirect(w, r, c.prefix+"/kinesis/"+stream, "That shard covers too small a hash range to split")
		return
	}
	if err := c.be.SplitShard(r.Context(), stream, shard, at); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream, "Split "+shard)
}

func (c *Console) kinesisRetention(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	hours, _ := strconv.Atoi(r.FormValue("hours"))
	if err := c.be.SetRetention(r.Context(), stream, hours); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kinesis/"+stream, "Retention set")
}
