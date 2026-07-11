package console

import (
	"net/http"
	"net/url"
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
