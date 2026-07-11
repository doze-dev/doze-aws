package console

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// humanBytes formats a byte count like "248.1 KB".
func humanBytes(n int64) string {
	const u = "BKMGT"
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(u)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return trimFloat(f) + " " + string(u[i]) + "B"
}

// ---- overview ----

func (c *Console) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	buckets, _ := c.be.ListBuckets(ctx)
	queues, _ := c.be.ListQueues(ctx)
	topics, _ := c.be.ListTopics(ctx)
	tables, _ := c.be.ListTables(ctx)
	fns, _ := c.be.ListFunctions(ctx)
	keys, _ := c.be.ListKeys(ctx)
	params, _ := c.be.ListParameters(ctx)
	secrets, _ := c.be.ListSecrets(ctx)
	buses, _ := c.be.ListBuses(ctx)
	ident, _ := c.be.CallerIdentity(ctx)

	msgTotal := 0
	for _, q := range queues {
		msgTotal += q.Available + q.InFlight
	}
	rules := 0
	for _, b := range buses {
		rules += b.Rules
	}
	c.render(w, "overview", map[string]any{
		"BucketCount": len(buckets), "QueueCount": len(queues), "TopicCount": len(topics),
		"TableCount": len(tables), "FnCount": len(fns), "KeyCount": len(keys),
		"ParamCount": len(params), "SecretCount": len(secrets),
		"BusCount": len(buses), "RuleCount": rules, "MsgTotal": msgTotal,
		"Identity": ident,
	})
}

// ---- S3 ----

func (c *Console) s3Buckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := c.be.ListBuckets(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, "s3_buckets", map[string]any{"Buckets": buckets})
}

func (c *Console) s3CreateBucket(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	versioning := r.FormValue("versioning") == "on"
	objectLock := r.FormValue("object_lock") == "on"
	if err := c.be.CreateBucketFull(r.Context(), name, versioning, objectLock); err != nil {
		c.fail(w, err)
		return
	}
	buckets, _ := c.be.ListBuckets(r.Context())
	toast(w, "Bucket “"+name+"” created")
	c.partial(w, "bucket_list", map[string]any{"Buckets": buckets})
}

// s3Versioning toggles versioning from the Properties tab.
func (c *Console) s3Versioning(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	enable := r.FormValue("enable") == "true"
	if err := c.be.SetBucketVersioning(r.Context(), bucket, enable); err != nil {
		c.fail(w, err)
		return
	}
	if enable {
		toast(w, "Versioning enabled")
	} else {
		toast(w, "Versioning suspended")
	}
	props, _ := c.be.GetBucketProps(r.Context(), bucket)
	c.partial(w, "s3_props", map[string]any{"Bucket": bucket, "Props": props})
}

func (c *Console) s3DeleteBucket(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteBucket(r.Context(), r.PathValue("bucket")); err != nil {
		c.fail(w, err)
		return
	}
	buckets, _ := c.be.ListBuckets(r.Context())
	toast(w, "Bucket deleted")
	c.partial(w, "bucket_list", map[string]any{"Buckets": buckets})
}

func (c *Console) s3Objects(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	prefix := r.URL.Query().Get("prefix")
	tab := tabOf(r, "objects")

	data := map[string]any{
		"Bucket":    bucket,
		"KeyPrefix": prefix,
		"Tab":       tab,
	}
	if tab == "properties" {
		props, err := c.be.GetBucketProps(r.Context(), bucket)
		if err != nil {
			c.fail(w, err)
			return
		}
		data["Props"] = props
		c.render(w, "s3_objects", data)
		return
	}

	objs, err := c.be.ListObjects(r.Context(), bucket, prefix)
	if err != nil {
		c.fail(w, err)
		return
	}
	data["Objects"] = objs
	data["Crumbs"] = crumbs(prefix)
	data["Parent"] = parentPrefix(prefix)
	// HTMX navigation within the browser swaps just the table.
	if r.Header.Get("HX-Request") == "true" && r.URL.Query().Get("partial") == "1" {
		c.partial(w, "object_table", data)
		return
	}
	c.render(w, "s3_objects", data)
}

func (c *Console) s3GetObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.URL.Query().Get("key")
	body, ctype, err := c.be.GetObject(r.Context(), bucket, key)
	if err != nil {
		c.fail(w, err)
		return
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	// Inline preview for text/images; download for the rest.
	inline := strings.HasPrefix(ctype, "text/") || strings.HasPrefix(ctype, "image/") ||
		ctype == "application/json"
	disp := "attachment"
	if inline {
		disp = "inline"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", disp+"; filename=\""+baseName(key)+"\"")
	w.Write(body)
}

// s3Meta renders the object detail drawer: metadata + inline preview + actions.
func (c *Console) s3Meta(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.URL.Query().Get("key")
	meta, err := c.be.HeadObject(r.Context(), bucket, key)
	if err != nil {
		c.fail(w, err)
		return
	}
	meta.Size = humanBytes(meta.SizeBytes)
	c.partial(w, "object_meta", map[string]any{
		"Bucket": bucket, "KeyPrefix": r.URL.Query().Get("prefix"),
		"Meta": meta, "Name": baseName(key),
		"URL": c.prefix + "/s3/" + bucket + "/object?key=" + url.QueryEscape(key),
	})
}

func (c *Console) s3Upload(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	prefix := r.FormValue("prefix")
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		c.fail(w, err)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		c.fail(w, err)
		return
	}
	defer file.Close()
	data := make([]byte, hdr.Size)
	if _, err := readFull(file, data); err != nil {
		c.fail(w, err)
		return
	}
	key := prefix + hdr.Filename
	ctype := hdr.Header.Get("Content-Type")
	if err := c.be.PutObject(r.Context(), bucket, key, data, ctype); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Uploaded "+hdr.Filename)
	c.swapObjectTable(w, r, bucket, prefix)
}

func (c *Console) s3DeleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.FormValue("key")
	prefix := r.FormValue("prefix")
	if err := c.be.DeleteObject(r.Context(), bucket, key); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Object deleted")
	c.swapObjectTable(w, r, bucket, prefix)
}

func (c *Console) swapObjectTable(w http.ResponseWriter, r *http.Request, bucket, prefix string) {
	objs, _ := c.be.ListObjects(r.Context(), bucket, prefix)
	c.partial(w, "object_table", map[string]any{
		"Bucket": bucket, "KeyPrefix": prefix, "Objects": objs,
		"Crumbs": crumbs(prefix), "Parent": parentPrefix(prefix),
	})
}

// ---- SQS ----

func (c *Console) sqsQueues(w http.ResponseWriter, r *http.Request) {
	queues, err := c.be.ListQueues(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, "sqs_queues", map[string]any{"Queues": queues})
}

func (c *Console) sqsCreateQueue(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	fifo := r.FormValue("fifo") == "on"
	if fifo && !strings.HasSuffix(name, ".fifo") {
		name += ".fifo"
	}
	if err := c.be.CreateQueueFull(r.Context(), SQSCreateOpts{
		Name: name, FIFO: fifo,
		VisibilityTimeout: strings.TrimSpace(r.FormValue("visibility")),
		RetentionSeconds:  strings.TrimSpace(r.FormValue("retention")),
		DelaySeconds:      strings.TrimSpace(r.FormValue("delay")),
		DLQName:           r.FormValue("dlq"),
		MaxReceiveCount:   strings.TrimSpace(r.FormValue("max_receive")),
	}); err != nil {
		c.fail(w, err)
		return
	}
	queues, _ := c.be.ListQueues(r.Context())
	toast(w, "Queue “"+name+"” created")
	c.partial(w, "queue_list", map[string]any{"Queues": queues})
}

func (c *Console) sqsDeleteQueue(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteQueue(r.Context(), r.PathValue("queue")); err != nil {
		c.fail(w, err)
		return
	}
	queues, _ := c.be.ListQueues(r.Context())
	toast(w, "Queue deleted")
	c.partial(w, "queue_list", map[string]any{"Queues": queues})
}

func (c *Console) sqsQueue(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	attrs, msgs, err := c.be.QueueDetail(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, "sqs_queue", map[string]any{
		"Queue": name, "Attrs": attrs, "Messages": msgs,
		"Available": atoi(attrs["ApproximateNumberOfMessages"]),
		"InFlight":  atoi(attrs["ApproximateNumberOfMessagesNotVisible"]),
		"ARN":       QueueARN(name),
		"URL":       "http://127.0.0.1:4566/000000000000/" + name,
	})
}

// sqsMessages is the HTMX-polled partial: the live message list + depth, peeked
// non-destructively so repeated refreshes never consume anything.
func (c *Console) sqsMessages(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	attrs, msgs, err := c.be.QueueDetail(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "message_panel", map[string]any{
		"Queue": name, "Messages": msgs,
		"Available": atoi(attrs["ApproximateNumberOfMessages"]),
		"InFlight":  atoi(attrs["ApproximateNumberOfMessagesNotVisible"]),
	})
}

func (c *Console) sqsSend(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	body := r.FormValue("body")
	if err := c.be.SendMessage(r.Context(), name, body); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Message sent")
	c.sqsMessages(w, r)
}

func (c *Console) sqsPurge(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	if err := c.be.PurgeQueue(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Queue purged")
	c.sqsMessages(w, r)
}

// ---- small helpers ----

func readFull(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			if total == len(buf) {
				return total, nil
			}
			return total, err
		}
	}
	return total, nil
}

func baseName(key string) string {
	if i := strings.LastIndex(strings.TrimSuffix(key, "/"), "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}

func parentPrefix(prefix string) string {
	p := strings.TrimSuffix(prefix, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i+1]
	}
	return ""
}

type crumb struct {
	Name   string
	Prefix string
}

// crumbs turns "a/b/c/" into navigable breadcrumb segments.
func crumbs(prefix string) []crumb {
	if prefix == "" {
		return nil
	}
	parts := strings.Split(strings.TrimSuffix(prefix, "/"), "/")
	out := make([]crumb, 0, len(parts))
	acc := ""
	for _, p := range parts {
		acc += p + "/"
		out = append(out, crumb{Name: p, Prefix: acc})
	}
	return out
}

func itoaSize(n int64) string { return strconv.FormatInt(n, 10) }

func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}
