package console

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ---- Wave B: S3 editing depth ----

// s3Presign renders a working share link. doze-aws enforces the expiry, so
// the honest durations actually mean something.
func (c *Console) s3Presign(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.FormValue("key")
	ttl, err := time.ParseDuration(r.FormValue("ttl"))
	if err != nil || ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = time.Hour
	}
	host := r.Host
	if host == "" {
		host = "127.0.0.1:4566"
	}
	link := PresignURL(host, bucket, key, ttl)
	c.partial(w, "s3_share_link", map[string]any{"URL": link, "TTL": ttl.String()})
}

// s3Copy copies or moves (copy + delete) an object.
func (c *Console) s3Copy(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	src := r.FormValue("src")
	dst := strings.TrimSpace(r.FormValue("dst"))
	if dst == "" || dst == src {
		c.fail(w, &apiErr{status: 400, body: "give the copy a new key"})
		return
	}
	if err := c.be.CopyObject(r.Context(), bucket, src, dst, ""); err != nil {
		c.fail(w, err)
		return
	}
	verb := "Copied"
	if r.FormValue("move") == "true" {
		if err := c.be.DeleteObject(r.Context(), bucket, src); err != nil {
			c.fail(w, err)
			return
		}
		verb = "Moved"
	}
	toast(w, verb+" to "+dst)
	c.swapObjectTable(w, r, bucket, r.FormValue("prefix"))
}

// s3Versions renders an object's version history (the drawer section).
func (c *Console) s3Versions(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.URL.Query().Get("key")
	vs, err := c.be.ObjectVersions(r.Context(), bucket, key)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "s3_versions", map[string]any{
		"Bucket": bucket, "Key": key, "Versions": vs,
		"EncodedKey": url.QueryEscape(key),
	})
}

// s3RestoreVersion makes an old version current again (CopyObject from the
// version onto the same key — the S3-native "restore").
func (c *Console) s3RestoreVersion(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key, vid := r.FormValue("key"), r.FormValue("versionId")
	if err := c.be.CopyObject(r.Context(), bucket, key, key, vid); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Version restored — it is current again")
	c.s3VersionsRefresh(w, r, bucket, key)
}

// s3DeleteVersion permanently removes one version or delete marker.
func (c *Console) s3DeleteVersion(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key, vid := r.FormValue("key"), r.FormValue("versionId")
	if err := c.be.DeleteObjectVersion(r.Context(), bucket, key, vid); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Version permanently deleted")
	c.s3VersionsRefresh(w, r, bucket, key)
}

func (c *Console) s3VersionsRefresh(w http.ResponseWriter, r *http.Request, bucket, key string) {
	vs, _ := c.be.ObjectVersions(r.Context(), bucket, key)
	c.partial(w, "s3_versions", map[string]any{
		"Bucket": bucket, "Key": key, "Versions": vs,
		"EncodedKey": url.QueryEscape(key),
	})
}

// s3NotifyAdd wires a new bucket notification (read-modify-write of the config).
func (c *Console) s3NotifyAdd(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	kind, name, ok := strings.Cut(r.FormValue("dest"), ":")
	if !ok || name == "" {
		c.fail(w, &apiErr{status: 400, body: "pick a destination"})
		return
	}
	rules := c.be.Notifications(r.Context(), bucket)
	nr := NotifyRule{
		Kind: kind, Name: name,
		Prefix: strings.TrimSpace(r.FormValue("prefix")),
		Suffix: strings.TrimSpace(r.FormValue("suffix")),
	}
	if ev := r.FormValue("event"); ev != "" {
		nr.Events = []string{ev}
	}
	rules = append(rules, nr)
	if err := c.be.PutNotifications(r.Context(), bucket, rules); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Notification wired → "+name)
	c.s3NotifyPartial(w, r, bucket)
}

// s3NotifyRemove deletes one notification by list index.
func (c *Console) s3NotifyRemove(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	idx := atoi(r.FormValue("index"))
	rules := c.be.Notifications(r.Context(), bucket)
	if idx < 0 || idx >= len(rules) {
		c.fail(w, &apiErr{status: 400, body: "notification no longer exists — refresh"})
		return
	}
	rules = append(rules[:idx], rules[idx+1:]...)
	if err := c.be.PutNotifications(r.Context(), bucket, rules); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Notification removed")
	c.s3NotifyPartial(w, r, bucket)
}

func (c *Console) s3NotifyPartial(w http.ResponseWriter, r *http.Request, bucket string) {
	queues, _ := c.be.ListQueues(r.Context())
	topics, _ := c.be.ListTopics(r.Context())
	fns, _ := c.be.ListFunctions(r.Context())
	c.partial(w, "s3_notify", map[string]any{
		"Bucket": bucket, "Rules": c.be.Notifications(r.Context(), bucket),
		"Queues": queues, "Topics": topics, "Functions": fns,
	})
}

// s3SaveCORS / s3SaveLifecycle persist the validated-JSON editors.
func (c *Console) s3SaveCORS(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	if err := c.be.PutCORSJSON(r.Context(), bucket, r.FormValue("rules")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "CORS rules saved — preflights evaluate them for real")
	c.s3PropsPartial(w, r, bucket)
}

func (c *Console) s3SaveLifecycle(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	if err := c.be.PutLifecycleJSON(r.Context(), bucket, r.FormValue("rules")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Lifecycle rules saved — the janitor enforces them")
	c.s3PropsPartial(w, r, bucket)
}

// ---- bulk delete, combine, multipart uploads, website, object lock ----

// s3BulkDelete deletes the selected keys in one DeleteObjects call.
func (c *Console) s3BulkDelete(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	var keys []string
	if err := json.Unmarshal([]byte(r.FormValue("keys")), &keys); err != nil || len(keys) == 0 {
		c.fail(w, errors.New("no objects selected"))
		return
	}
	n, err := c.be.BulkDeleteObjects(r.Context(), bucket, keys)
	if err != nil {
		c.fail(w, err)
		return
	}
	toast(w, plural(n, "object")+" deleted")
	c.swapObjectTable(w, r, bucket, r.FormValue("prefix"))
}

// s3Combine concatenates the selected objects, in name order, into one new
// object — UploadPartCopy per source, no byte leaving the service.
func (c *Console) s3Combine(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	var keys []string
	if err := json.Unmarshal([]byte(r.FormValue("keys")), &keys); err != nil || len(keys) < 2 {
		c.fail(w, errors.New("select at least two objects to combine"))
		return
	}
	sort.Strings(keys)
	dest := strings.TrimSpace(r.FormValue("dest"))
	if dest == "" {
		c.fail(w, errors.New("name the combined object"))
		return
	}
	if err := c.be.CombineObjects(r.Context(), bucket, r.FormValue("prefix")+dest, keys); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Combined "+plural(len(keys), "object")+" into "+dest)
	c.swapObjectTable(w, r, bucket, r.FormValue("prefix"))
}

// s3MPUploads renders the in-progress multipart uploads — storage that exists
// and bills but which no object listing shows — each with its parts.
func (c *Console) s3MPUploads(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	ups, err := c.be.ListMPUploads(r.Context(), bucket)
	if err != nil {
		c.fail(w, err)
		return
	}
	type upView struct {
		MultipartUpload
		Parts []PartInfo
		Size  int64
	}
	views := make([]upView, 0, len(ups))
	for _, u := range ups {
		parts, _ := c.be.ListUploadParts(r.Context(), bucket, u.Key, u.UploadID)
		v := upView{MultipartUpload: u, Parts: parts}
		for _, p := range parts {
			v.Size += p.Size
		}
		views = append(views, v)
	}
	c.partial(w, "s3_mp_uploads", map[string]any{"Bucket": bucket, "Uploads": views})
}

// s3AbortUpload discards one in-progress upload and frees its parts.
func (c *Console) s3AbortUpload(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	if err := c.be.AbortMPUpload(r.Context(), bucket, r.FormValue("key"), r.FormValue("upload")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Upload aborted — its parts are freed")
	c.s3MPUploads(w, r)
}

// s3Website enables or disables static website hosting.
func (c *Console) s3Website(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	if r.FormValue("disable") != "" {
		if err := c.be.DeleteBucketWebsite(r.Context(), bucket); err != nil {
			c.fail(w, err)
			return
		}
		toast(w, "Website hosting disabled")
	} else {
		index := strings.TrimSpace(r.FormValue("index"))
		if err := c.be.PutBucketWebsite(r.Context(), bucket, index, strings.TrimSpace(r.FormValue("error"))); err != nil {
			c.fail(w, err)
			return
		}
		toast(w, "Website hosting enabled — "+index+" serves at the root")
	}
	c.s3PropsPartial(w, r, bucket)
}

// s3LockConfig sets the bucket's default object-lock retention rule.
func (c *Console) s3LockConfig(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	if err := c.be.PutObjectLockConfig(r.Context(), bucket,
		r.FormValue("mode"), atoi(r.FormValue("days"))); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Default retention saved — new objects inherit it")
	c.s3PropsPartial(w, r, bucket)
}

// metaAgain re-renders the object drawer after a per-object mutation.
func (c *Console) metaAgain(w http.ResponseWriter, r *http.Request, key string) {
	r.URL.RawQuery = "key=" + url.QueryEscape(key) + "&prefix=" + url.QueryEscape(r.FormValue("prefix"))
	c.s3Meta(w, r)
}

// s3ObjTagsSave replaces one object's tag set (empty deletes the tagging).
func (c *Console) s3ObjTagsSave(w http.ResponseWriter, r *http.Request) {
	bucket, key := r.PathValue("bucket"), r.FormValue("key")
	r.ParseForm() //nolint:errcheck // best-effort, as elsewhere
	var tags []KV
	for i, k := range r.Form["tag_key"] {
		if k = strings.TrimSpace(k); k == "" {
			continue
		}
		v := ""
		if i < len(r.Form["tag_val"]) {
			v = r.Form["tag_val"][i]
		}
		tags = append(tags, KV{K: k, V: v})
	}
	if err := c.be.PutObjectTags(r.Context(), bucket, key, tags); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Object tags saved")
	c.metaAgain(w, r, key)
}

// s3Retention locks one object until a date; s3LegalHold flips the hold flag.
func (c *Console) s3Retention(w http.ResponseWriter, r *http.Request) {
	bucket, key := r.PathValue("bucket"), r.FormValue("key")
	until := r.FormValue("until") + "T00:00:00Z"
	if err := c.be.PutObjectRetention(r.Context(), bucket, key, r.FormValue("mode"), until); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Retention set — deletes are refused until it lapses")
	c.metaAgain(w, r, key)
}

func (c *Console) s3LegalHold(w http.ResponseWriter, r *http.Request) {
	bucket, key := r.PathValue("bucket"), r.FormValue("key")
	on := r.FormValue("on") != ""
	if err := c.be.PutObjectLegalHold(r.Context(), bucket, key, on); err != nil {
		c.fail(w, err)
		return
	}
	if on {
		toast(w, "Legal hold ON — the object cannot be deleted while it stands")
	} else {
		toast(w, "Legal hold released")
	}
	c.metaAgain(w, r, key)
}

// s3CheckName answers the create form's availability question (HeadBucket)
// before a create that would be refused.
func (c *Console) s3CheckName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	c.partial(w, "s3_name_check", map[string]any{
		"Name": name, "Taken": name != "" && c.be.BucketExists(r.Context(), name),
	})
}

// s3SavePolicy replaces the bucket policy from the builder; empty deletes it.
func (c *Console) s3SavePolicy(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	doc := strings.TrimSpace(r.FormValue("document"))
	if err := c.be.PutBucketPolicyDoc(r.Context(), bucket, doc); err != nil {
		c.fail(w, err)
		return
	}
	if doc == "" {
		toast(w, "Bucket policy removed")
	} else {
		toast(w, "Bucket policy saved — stored and returned; nothing local evaluates it")
	}
	c.s3PropsPartial(w, r, bucket)
}

// s3PublicAccess toggles block public access: on puts all four blocks
// (PutPublicAccessBlock), off removes the configuration
// (DeletePublicAccessBlock), so a public policy can be put.
func (c *Console) s3PublicAccess(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	on := r.FormValue("enable") == "true"
	if err := c.be.SetBlockPublicAccess(r.Context(), bucket, on); err != nil {
		c.fail(w, err)
		return
	}
	if on {
		toast(w, "Public access blocked — a policy granting to everyone is refused")
	} else {
		toast(w, "Public access block removed")
	}
	c.s3PropsPartial(w, r, bucket)
}
