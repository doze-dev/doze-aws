package console

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

// contentHash fingerprints a live partial so pollers can skip unchanged swaps.
func contentHash(parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum64())
}

// liveUnchanged replies 204 when the poller's hash matches current content.
func liveUnchanged(w http.ResponseWriter, r *http.Request, hash string) bool {
	if q := r.URL.Query().Get("h"); q != "" && q == hash {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// ---- S3 ----

func (c *Console) s3List(r *http.Request) []Bucket {
	buckets, _ := c.be.ListBuckets(r.Context())
	return buckets
}

func (c *Console) s3Buckets(w http.ResponseWriter, r *http.Request) {
	c.render(w, r, "s3_home", map[string]any{
		"List": c.s3List(r), "Title": "S3",
	})
}

func (c *Console) s3CreateBucket(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	versioning := r.FormValue("versioning") == "on"
	objectLock := r.FormValue("object_lock") == "on"
	if err := c.be.CreateBucketFull(r.Context(), name, versioning, objectLock); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/s3/"+name, "Bucket “"+name+"” created")
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
	c.s3PropsPartial(w, r, bucket)
}

// s3AddTag appends one tag to the bucket's tag set.
func (c *Console) s3AddTag(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	k, v := strings.TrimSpace(r.FormValue("key")), strings.TrimSpace(r.FormValue("value"))
	if k == "" {
		c.fail(w, &apiErr{status: 400, body: "tag key is required"})
		return
	}
	props, err := c.be.GetBucketProps(r.Context(), bucket)
	if err != nil {
		c.fail(w, err)
		return
	}
	tags := make([]KV, 0, len(props.Tags)+1)
	for _, t := range props.Tags {
		if t.K != k {
			tags = append(tags, t)
		}
	}
	tags = append(tags, KV{K: k, V: v})
	if err := c.be.PutBucketTags(r.Context(), bucket, tags); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Tag added")
	c.s3PropsPartial(w, r, bucket)
}

// s3RemoveTag removes one tag from the bucket's tag set.
func (c *Console) s3RemoveTag(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	k := r.FormValue("key")
	props, err := c.be.GetBucketProps(r.Context(), bucket)
	if err != nil {
		c.fail(w, err)
		return
	}
	tags := make([]KV, 0, len(props.Tags))
	for _, t := range props.Tags {
		if t.K != k {
			tags = append(tags, t)
		}
	}
	if err := c.be.PutBucketTags(r.Context(), bucket, tags); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Tag removed")
	c.s3PropsPartial(w, r, bucket)
}

func (c *Console) s3PropsPartial(w http.ResponseWriter, r *http.Request, bucket string) {
	props, _ := c.be.GetBucketProps(r.Context(), bucket)
	c.partial(w, "s3_props", map[string]any{"Bucket": bucket, "Props": props})
}

func (c *Console) s3Objects(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	prefix := r.URL.Query().Get("prefix")
	tab := tabOf(r, "objects")

	data := map[string]any{
		"Bucket":    bucket,
		"KeyPrefix": prefix,
		"Tab":       tab,
		"List":      c.s3List(r),
		"Sel":       bucket,
		"Title":     bucket + " · S3",
		"Conn":      c.be.Neighbors(r.Context(), "s3", bucket),
	}
	if tab == "properties" {
		props, err := c.be.GetBucketProps(r.Context(), bucket)
		if err != nil {
			c.fail(w, err)
			return
		}
		data["Props"] = props
		c.render(w, r, "s3_objects", data)
		return
	}

	objs, err := c.be.ListObjects(r.Context(), bucket, prefix)
	if err != nil {
		c.fail(w, err)
		return
	}
	var total int64
	files := 0
	for _, o := range objs {
		if !o.IsPrefix {
			total += o.Size
			files++
		}
	}
	data["Objects"] = objs
	data["Crumbs"] = crumbs(prefix)
	data["FileCount"] = files
	data["TotalSize"] = humanBytes(total)
	// HTMX navigation within the browser swaps just the table.
	if r.Header.Get("HX-Request") == "true" && r.URL.Query().Get("partial") == "1" {
		c.partial(w, "object_table", data)
		return
	}
	c.render(w, r, "s3_objects", data)
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

func (c *Console) s3DeleteBucket(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteBucket(r.Context(), r.PathValue("bucket")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/s3", "Bucket deleted")
}

func (c *Console) swapObjectTable(w http.ResponseWriter, r *http.Request, bucket, prefix string) {
	objs, _ := c.be.ListObjects(r.Context(), bucket, prefix)
	var total int64
	files := 0
	for _, o := range objs {
		if !o.IsPrefix {
			total += o.Size
			files++
		}
	}
	c.partial(w, "object_table", map[string]any{
		"Bucket": bucket, "KeyPrefix": prefix, "Objects": objs,
		"Crumbs": crumbs(prefix), "FileCount": files, "TotalSize": humanBytes(total),
	})
}

// ---- SQS ----

func (c *Console) sqsList(r *http.Request) []Queue {
	queues, _ := c.be.ListQueues(r.Context())
	return queues
}

func (c *Console) sqsQueues(w http.ResponseWriter, r *http.Request) {
	c.render(w, r, "sqs_home", map[string]any{"List": c.sqsList(r), "Title": "SQS"})
}

func (c *Console) sqsCreateQueue(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	fifo := r.FormValue("fifo") == "on"
	if fifo && !strings.HasSuffix(name, ".fifo") {
		name += ".fifo"
	}
	dlq := r.FormValue("dlq")
	if r.FormValue("dlq_mode") == "none" {
		dlq = ""
	}
	flash := "Queue “" + name + "” created"
	// "Create one alongside": the DLQ must match the main queue's type — a
	// FIFO queue can only redrive to a FIFO dead-letter queue.
	if r.FormValue("dlq_mode") == "new" {
		base := strings.TrimSuffix(name, ".fifo")
		dlq = base + "-dlq"
		if fifo {
			dlq += ".fifo"
		}
		if err := c.be.CreateQueueFull(r.Context(), SQSCreateOpts{Name: dlq, FIFO: fifo}); err != nil {
			c.fail(w, err)
			return
		}
		flash = "Queues “" + name + "” and “" + dlq + "” created and wired"
	}
	if err := c.be.CreateQueueFull(r.Context(), SQSCreateOpts{
		Name: name, FIFO: fifo,
		ContentBasedDedup: fifo && r.FormValue("content_dedup") == "on",
		VisibilityTimeout: strings.TrimSpace(r.FormValue("visibility")),
		RetentionSeconds:  strings.TrimSpace(r.FormValue("retention")),
		DelaySeconds:      strings.TrimSpace(r.FormValue("delay")),
		DLQName:           dlq,
		MaxReceiveCount:   strings.TrimSpace(r.FormValue("max_receive")),
	}); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sqs/"+name, flash)
}

func (c *Console) sqsDeleteQueue(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteQueue(r.Context(), r.PathValue("queue")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sqs", "Queue deleted")
}

func (c *Console) sqsQueue(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	attrs, msgs, err := c.be.QueueDetail(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	conn := c.be.Neighbors(r.Context(), "sqs", name)
	isDLQ := false
	for _, n := range conn.Upstream {
		if n.Kind == "redrive" {
			isDLQ = true
			break
		}
	}
	c.render(w, r, "sqs_queue", map[string]any{
		"Queue": name, "Attrs": attrs, "Messages": msgs, "IsDLQ": isDLQ,
		"Available": atoi(attrs["ApproximateNumberOfMessages"]),
		"InFlight":  atoi(attrs["ApproximateNumberOfMessagesNotVisible"]),
		"ARN":       QueueARN(name),
		"URL":       "http://127.0.0.1:4566/000000000000/" + name,
		"Config":    sqsConfigOf(attrs),
		"Conn":      conn,
		"Hash":      sqsMsgHash(attrs, msgs),
		"Tab":       tabOf(r, "messages"),
		"List":      c.sqsList(r),
		"Sel":       name,
		"Title":     name + " · SQS",
	})
}

// sqsConfig is the curated queue configuration surface (the raw attribute map
// stays available in a collapsible section).
type sqsConfig struct {
	Visibility string
	Retention  string
	Delay      string
	MaxSize    string
	Created    string
	Modified   string
	FIFO         bool
	ContentDedup bool   // FIFO: ContentBasedDeduplication
	DLQ          string // dead-letter queue name, from the redrive policy
	MaxReceive string
}

func sqsConfigOf(attrs map[string]string) sqsConfig {
	cfg := sqsConfig{
		Visibility: attrs["VisibilityTimeout"],
		Retention:  attrs["MessageRetentionPeriod"],
		Delay:      attrs["DelaySeconds"],
		MaxSize:    attrs["MaximumMessageSize"],
		FIFO:         attrs["FifoQueue"] == "true",
		ContentDedup: attrs["ContentBasedDeduplication"] == "true",
		Created:    epochSecString(attrs["CreatedTimestamp"]),
		Modified:   epochSecString(attrs["LastModifiedTimestamp"]),
	}
	if rp := attrs["RedrivePolicy"]; rp != "" {
		var pol struct {
			DeadLetterTargetArn string `json:"deadLetterTargetArn"`
			MaxReceiveCount     any    `json:"maxReceiveCount"`
		}
		if json.Unmarshal([]byte(rp), &pol) == nil {
			if i := strings.LastIndex(pol.DeadLetterTargetArn, ":"); i >= 0 {
				cfg.DLQ = pol.DeadLetterTargetArn[i+1:]
			}
			cfg.MaxReceive = strings.Trim(strings.TrimSpace(fmtAny(pol.MaxReceiveCount)), "\"")
		}
	}
	return cfg
}

func fmtAny(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// epochSecString formats a unix-seconds string as local time.
func epochSecString(s string) string {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n == 0 {
		return ""
	}
	return time.Unix(n, 0).Local().Format("2006-01-02 15:04:05")
}

// sqsMsgHash fingerprints the visible message state for 204-skip polling.
func sqsMsgHash(attrs map[string]string, msgs []SQSMessage) string {
	parts := []string{attrs["ApproximateNumberOfMessages"], attrs["ApproximateNumberOfMessagesNotVisible"]}
	for _, m := range msgs {
		parts = append(parts, m.MessageID)
	}
	return contentHash(parts...)
}

// sqsMessages is the polled live partial: 204 when unchanged, morph otherwise.
func (c *Console) sqsMessages(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	attrs, msgs, err := c.be.QueueDetail(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	hash := sqsMsgHash(attrs, msgs)
	if liveUnchanged(w, r, hash) {
		return
	}
	c.partial(w, "message_panel", map[string]any{
		"Queue": name, "Messages": msgs,
		"Available": atoi(attrs["ApproximateNumberOfMessages"]),
		"InFlight":  atoi(attrs["ApproximateNumberOfMessagesNotVisible"]),
		"Hash":      hash,
	})
}

func (c *Console) sqsSend(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	body := r.FormValue("body")
	opts := SendOpts{
		GroupID: strings.TrimSpace(r.FormValue("group")),
		DedupID: strings.TrimSpace(r.FormValue("dedup")),
		Delay:   strings.TrimSpace(r.FormValue("delay")),
		Attrs:   parseMsgAttrs(r.FormValue("attrs")),
	}
	if strings.HasSuffix(name, ".fifo") && opts.GroupID == "" {
		opts.GroupID = "default"
	}
	if err := c.be.SendMessage(r.Context(), name, body, opts); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Message sent")
	c.sqsMessages(w, r)
}

// parseMsgAttrs decodes the composer's attribute rows (a JSON array the Alpine
// editor maintains: [{"n":...,"t":...,"v":...}]). Malformed input yields none.
func parseMsgAttrs(raw string) []MsgAttr {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var rows []struct{ N, T, V string }
	if json.Unmarshal([]byte(raw), &rows) != nil {
		return nil
	}
	var attrs []MsgAttr
	for _, r := range rows {
		if strings.TrimSpace(r.N) == "" {
			continue
		}
		attrs = append(attrs, MsgAttr{Name: strings.TrimSpace(r.N), Type: r.T, Value: r.V})
	}
	return attrs
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

// sqsSetAttributes edits the queue's mutable delivery settings.
func (c *Console) sqsSetAttributes(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	attrs := map[string]string{}
	if v := strings.TrimSpace(r.FormValue("visibility")); v != "" {
		attrs["VisibilityTimeout"] = v
	}
	if v := strings.TrimSpace(r.FormValue("retention")); v != "" {
		attrs["MessageRetentionPeriod"] = v
	}
	if v := strings.TrimSpace(r.FormValue("delay")); v != "" {
		attrs["DelaySeconds"] = v
	}
	if len(attrs) > 0 {
		if err := c.be.SetQueueAttributes(r.Context(), name, attrs); err != nil {
			c.fail(w, err)
			return
		}
	}
	toast(w, "Queue settings saved")
	qattrs, err := c.be.queueAttrs(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "sqs_config", map[string]any{
		"Queue": name, "Attrs": qattrs, "Config": sqsConfigOf(qattrs),
	})
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
	var acc strings.Builder
	for _, p := range parts {
		acc.WriteString(p)
		acc.WriteString("/")
		out = append(out, crumb{Name: p, Prefix: acc.String()})
	}
	return out
}

func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// tabOf returns the active tab from ?tab=, defaulting sensibly.
func tabOf(r *http.Request, def string) string {
	if t := r.URL.Query().Get("tab"); t != "" {
		return t
	}
	return def
}

// ago renders a compact relative time from the console's standard layouts.
func ago(formatted string) string {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, formatted, time.Local); err == nil {
			d := time.Since(t)
			switch {
			case d < time.Minute:
				return "just now"
			case d < time.Hour:
				return strconv.Itoa(int(d.Minutes())) + " min ago"
			case d < 24*time.Hour:
				return strconv.Itoa(int(d.Hours())) + " h ago"
			default:
				return strconv.Itoa(int(d.Hours()/24)) + " d ago"
			}
		}
	}
	return formatted
}
