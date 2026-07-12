package console

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"fmt"
	"github.com/doze-dev/doze-aws/awsident"
	"io"
	"sort"
	"time"
)

// ---- S3 bucket properties ----

// BucketProps is everything the bucket Properties tab shows.
type BucketProps struct {
	Name       string
	ARN        string
	Region     string
	Versioning string // Enabled | Suspended | Disabled
	ObjectLock bool
	Tags       []KV
	Configs    []BucketConfig // which optional configs exist, with raw payloads
}

type KV struct{ K, V string }

type BucketConfig struct {
	Name string // CORS, Lifecycle, Policy, Website, Encryption
	Set  bool
	Raw  string
}

func (b *backend) GetBucketProps(ctx context.Context, bucket string) (*BucketProps, error) {
	p := &BucketProps{
		Name:   bucket,
		ARN:    "arn:aws:s3:::" + bucket,
		Region: awsident.Region,
	}

	// Versioning.
	if body, err := b.s3Sub(ctx, "GET", bucket, "versioning"); err == nil {
		var v struct {
			Status string `xml:"Status"`
		}
		xml.Unmarshal(body, &v)
		if v.Status == "" {
			p.Versioning = "Disabled"
		} else {
			p.Versioning = v.Status
		}
	}

	// Object lock: the config request may return 200 with an empty document when
	// the bucket has no lock, so read ObjectLockEnabled rather than trusting the
	// status code.
	if body, err := b.s3Sub(ctx, "GET", bucket, "object-lock"); err == nil {
		var v struct {
			Enabled string `xml:"ObjectLockEnabled"`
		}
		xml.Unmarshal(body, &v)
		p.ObjectLock = v.Enabled == "Enabled"
	}

	// Tags.
	if body, err := b.s3Sub(ctx, "GET", bucket, "tagging"); err == nil {
		var t struct {
			Tags []struct {
				Key   string `xml:"Key"`
				Value string `xml:"Value"`
			} `xml:"TagSet>Tag"`
		}
		xml.Unmarshal(body, &t)
		for _, tag := range t.Tags {
			p.Tags = append(p.Tags, KV{K: tag.Key, V: tag.Value})
		}
	}

	// Optional configurations: presence + raw payload for inspection.
	for _, cfg := range []struct{ label, sub string }{
		{"CORS", "cors"}, {"Lifecycle", "lifecycle"}, {"Policy", "policy"},
		{"Website", "website"}, {"Encryption", "encryption"},
	} {
		bc := BucketConfig{Name: cfg.label}
		if body, err := b.s3Sub(ctx, "GET", bucket, cfg.sub); err == nil && len(body) > 0 {
			bc.Set = true
			raw := string(body)
			if cfg.sub == "policy" {
				raw = prettyJSON(raw)
			}
			bc.Raw = raw
		}
		p.Configs = append(p.Configs, bc)
	}
	return p, nil
}

// SetBucketVersioning flips versioning between Enabled and Suspended.
func (b *backend) SetBucketVersioning(ctx context.Context, bucket string, enable bool) error {
	status := "Suspended"
	if enable {
		status = "Enabled"
	}
	body := `<VersioningConfiguration><Status>` + status + `</Status></VersioningConfiguration>`
	req, _ := http.NewRequestWithContext(ctx, "PUT", b.base+"/"+bucket+"?versioning", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	_, err := b.do(req)
	return err
}

// CreateBucketFull creates a bucket with the console dialog's options.
func (b *backend) CreateBucketFull(ctx context.Context, name string, versioning, objectLock bool) error {
	req, _ := http.NewRequestWithContext(ctx, "PUT", b.base+"/"+url.PathEscape(name), nil)
	if objectLock {
		req.Header.Set("x-amz-bucket-object-lock-enabled", "true")
	}
	if _, err := b.do(req); err != nil {
		return err
	}
	if versioning {
		return b.SetBucketVersioning(ctx, name, true)
	}
	return nil
}

// PutBucketTags replaces the bucket's tag set (empty deletes the tagging).
func (b *backend) PutBucketTags(ctx context.Context, bucket string, tags []KV) error {
	if len(tags) == 0 {
		req, _ := http.NewRequestWithContext(ctx, "DELETE", b.base+"/"+bucket+"?tagging", nil)
		_, err := b.do(req)
		return err
	}
	var sb strings.Builder
	sb.WriteString("<Tagging><TagSet>")
	for _, t := range tags {
		sb.WriteString("<Tag><Key>")
		xml.EscapeText(&sb, []byte(t.K))
		sb.WriteString("</Key><Value>")
		xml.EscapeText(&sb, []byte(t.V))
		sb.WriteString("</Value></Tag>")
	}
	sb.WriteString("</TagSet></Tagging>")
	req, _ := http.NewRequestWithContext(ctx, "PUT", b.base+"/"+bucket+"?tagging", strings.NewReader(sb.String()))
	req.Header.Set("Content-Type", "application/xml")
	_, err := b.do(req)
	return err
}

// SetQueueAttributes updates a queue's mutable settings.
func (b *backend) SetQueueAttributes(ctx context.Context, name string, attrs map[string]string) error {
	_, err := b.sqs(ctx, "SetQueueAttributes", map[string]any{
		"QueueUrl": b.queueURL(name), "Attributes": attrs,
	})
	return err
}

// s3Sub issues a bucket-subresource request (?versioning, ?tagging, ...).
func (b *backend) s3Sub(ctx context.Context, method, bucket, sub string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, method, b.base+"/"+bucket+"?"+sub, nil)
	return b.do(req)
}

// ---- SQS extended create ----

// SQSCreateOpts carries the console create dialog's full option set.
type SQSCreateOpts struct {
	Name              string
	FIFO              bool
	ContentBasedDedup bool   // FIFO: dedup by body hash instead of explicit IDs
	VisibilityTimeout string // seconds
	RetentionSeconds  string
	DelaySeconds      string
	DLQName           string // existing queue name for the redrive policy
	MaxReceiveCount   string
}

func (b *backend) CreateQueueFull(ctx context.Context, o SQSCreateOpts) error {
	attrs := map[string]string{}
	if o.FIFO {
		attrs["FifoQueue"] = "true"
		if o.ContentBasedDedup {
			attrs["ContentBasedDeduplication"] = "true"
		}
	}
	if o.VisibilityTimeout != "" {
		attrs["VisibilityTimeout"] = o.VisibilityTimeout
	}
	if o.RetentionSeconds != "" {
		attrs["MessageRetentionPeriod"] = o.RetentionSeconds
	}
	if o.DelaySeconds != "" && o.DelaySeconds != "0" {
		attrs["DelaySeconds"] = o.DelaySeconds
	}
	if o.DLQName != "" {
		maxRecv := o.MaxReceiveCount
		if maxRecv == "" {
			maxRecv = "3"
		}
		rp, _ := json.Marshal(map[string]string{
			"deadLetterTargetArn": awsident.ARN("sqs", o.DLQName),
			"maxReceiveCount":     maxRecv,
		})
		attrs["RedrivePolicy"] = string(rp)
	}
	in := map[string]any{"QueueName": o.Name}
	if len(attrs) > 0 {
		in["Attributes"] = attrs
	}
	_, err := b.sqs(ctx, "CreateQueue", in)
	return err
}

// QueueARN builds the queue's ARN (fixed local identity).
func QueueARN(name string) string { return awsident.ARN("sqs", name) }

// humanCount formats large counts with a thin separator, e.g. 12,340.
func humanCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// ---- Wave B: versions, presign, copy, notifications, CORS/lifecycle ----

// ObjVersion is one entry in an object's version history.
type ObjVersion struct {
	VersionID    string
	IsLatest     bool
	DeleteMarker bool
	Size         int64
	LastModified string
}

// ObjectVersions lists one key's full version history, newest first —
// including delete markers, which are otherwise invisible.
func (b *backend) ObjectVersions(ctx context.Context, bucket, key string) ([]ObjVersion, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET",
		b.base+"/"+bucket+"?versions&prefix="+url.QueryEscape(key), nil)
	body, err := b.do(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Versions []struct {
			Key          string `xml:"Key"`
			VersionID    string `xml:"VersionId"`
			IsLatest     bool   `xml:"IsLatest"`
			LastModified string `xml:"LastModified"`
			Size         int64  `xml:"Size"`
		} `xml:"Version"`
		Markers []struct {
			Key          string `xml:"Key"`
			VersionID    string `xml:"VersionId"`
			IsLatest     bool   `xml:"IsLatest"`
			LastModified string `xml:"LastModified"`
		} `xml:"DeleteMarker"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	var vs []ObjVersion
	for _, v := range out.Versions {
		if v.Key != key { // ?prefix also matches sibling keys
			continue
		}
		vs = append(vs, ObjVersion{
			VersionID: v.VersionID, IsLatest: v.IsLatest,
			Size: v.Size, LastModified: shortTime(v.LastModified),
		})
	}
	for _, m := range out.Markers {
		if m.Key != key {
			continue
		}
		vs = append(vs, ObjVersion{
			VersionID: m.VersionID, IsLatest: m.IsLatest, DeleteMarker: true,
			LastModified: shortTime(m.LastModified),
		})
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].LastModified > vs[j].LastModified })
	return vs, nil
}

// PresignURL builds a share link the gateway accepts: doze-aws parses the
// SigV4 presigned form without verifying the signature, but DOES enforce the
// expiry — so the link genuinely stops working when it says it will.
func PresignURL(host, bucket, key string, ttl time.Duration) string {
	now := time.Now().UTC()
	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", "test/"+now.Format("20060102")+"/us-east-1/s3/aws4_request")
	q.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	q.Set("X-Amz-Expires", strconv.Itoa(int(ttl.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	q.Set("X-Amz-Signature", "doze-local-share")
	return "http://" + host + "/" + bucket + "/" + escapeKey(key) + "?" + q.Encode()
}

// CopyObject copies (optionally from a specific version) — also the engine
// behind rename/move and "make this version current".
func (b *backend) CopyObject(ctx context.Context, bucket, srcKey, dstKey, srcVersion string) error {
	src := "/" + bucket + "/" + escapeKey(srcKey)
	if srcVersion != "" {
		src += "?versionId=" + url.QueryEscape(srcVersion)
	}
	req, _ := http.NewRequestWithContext(ctx, "PUT", b.base+"/"+bucket+"/"+escapeKey(dstKey), nil)
	req.Header.Set("x-amz-copy-source", src)
	_, err := b.do(req)
	return err
}

// DeleteObjectVersion permanently removes one version (or delete marker).
func (b *backend) DeleteObjectVersion(ctx context.Context, bucket, key, versionID string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE",
		b.base+"/"+bucket+"/"+escapeKey(key)+"?versionId="+url.QueryEscape(versionID), nil)
	_, err := b.do(req)
	return err
}

// NotifyRule is one editable bucket-notification wiring.
type NotifyRule struct {
	Kind   string // sqs | sns | lambda
	Name   string // destination resource name
	Events []string
	Prefix string
	Suffix string
}

// Notifications reads the bucket's notification config as an editable list.
func (b *backend) Notifications(ctx context.Context, bucket string) []NotifyRule {
	body, err := b.s3Sub(ctx, "GET", bucket, "notification")
	if err != nil {
		return nil
	}
	type cfgEl struct {
		Arn    string   `xml:"-"`
		Queue  string   `xml:"Queue"`
		Topic  string   `xml:"Topic"`
		Lambda string   `xml:"CloudFunction"`
		Events []string `xml:"Event"`
		Filter struct {
			S3Key struct {
				Rules []struct {
					Name  string `xml:"Name"`
					Value string `xml:"Value"`
				} `xml:"FilterRule"`
			} `xml:"S3Key"`
		} `xml:"Filter"`
	}
	var out struct {
		Queues  []cfgEl `xml:"QueueConfiguration"`
		Topics  []cfgEl `xml:"TopicConfiguration"`
		Lambdas []cfgEl `xml:"CloudFunctionConfiguration"`
	}
	if xml.Unmarshal(body, &out) != nil {
		return nil
	}
	var rules []NotifyRule
	add := func(kind string, els []cfgEl) {
		for _, e := range els {
			arn := e.Queue + e.Topic + e.Lambda
			r := NotifyRule{Kind: kind, Name: strings.TrimPrefix(arnLeaf(arn), "function:"), Events: e.Events}
			for _, fr := range e.Filter.S3Key.Rules {
				switch strings.ToLower(fr.Name) {
				case "prefix":
					r.Prefix = fr.Value
				case "suffix":
					r.Suffix = fr.Value
				}
			}
			rules = append(rules, r)
		}
	}
	add("sqs", out.Queues)
	add("sns", out.Topics)
	add("lambda", out.Lambdas)
	return rules
}

// PutNotifications replaces the bucket's notification config.
func (b *backend) PutNotifications(ctx context.Context, bucket string, rules []NotifyRule) error {
	var sb strings.Builder
	sb.WriteString("<NotificationConfiguration>")
	for i, r := range rules {
		tag, target, arn := "QueueConfiguration", "Queue", awsident.ARN("sqs", r.Name)
		switch r.Kind {
		case "sns":
			tag, target, arn = "TopicConfiguration", "Topic", awsident.ARN("sns", r.Name)
		case "lambda":
			tag, target = "CloudFunctionConfiguration", "CloudFunction"
			arn = "arn:aws:lambda:" + awsident.Region + ":" + awsident.AccountID + ":function:" + r.Name
		}
		events := r.Events
		if len(events) == 0 {
			events = []string{"s3:ObjectCreated:*"}
		}
		fmt.Fprintf(&sb, "<%s><Id>console-%d</Id>", tag, i+1)
		if r.Prefix != "" || r.Suffix != "" {
			sb.WriteString("<Filter><S3Key>")
			if r.Prefix != "" {
				fmt.Fprintf(&sb, "<FilterRule><Name>prefix</Name><Value>%s</Value></FilterRule>", xmlEscape(r.Prefix))
			}
			if r.Suffix != "" {
				fmt.Fprintf(&sb, "<FilterRule><Name>suffix</Name><Value>%s</Value></FilterRule>", xmlEscape(r.Suffix))
			}
			sb.WriteString("</S3Key></Filter>")
		}
		for _, e := range events {
			fmt.Fprintf(&sb, "<Event>%s</Event>", xmlEscape(e))
		}
		fmt.Fprintf(&sb, "<%s>%s</%s></%s>", target, arn, target, tag)
	}
	sb.WriteString("</NotificationConfiguration>")
	req, _ := http.NewRequestWithContext(ctx, "PUT", b.base+"/"+bucket+"?notification", strings.NewReader(sb.String()))
	_, err := b.do(req)
	return err
}

func xmlEscape(s string) string {
	var buf strings.Builder
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// corsJSON / lifecycleJSON are the console's editable JSON mirrors of the
// XML wire configs — the validated JSON editor beats hand-written XML.
type corsJSON struct {
	AllowedOrigins []string `json:"AllowedOrigins"`
	AllowedMethods []string `json:"AllowedMethods"`
	AllowedHeaders []string `json:"AllowedHeaders,omitempty"`
	ExposeHeaders  []string `json:"ExposeHeaders,omitempty"`
	MaxAgeSeconds  int      `json:"MaxAgeSeconds,omitempty"`
}

type lifecycleJSON struct {
	ID             string `json:"ID,omitempty"`
	Prefix         string `json:"Prefix,omitempty"`
	Status         string `json:"Status,omitempty"` // default Enabled
	ExpireDays     int    `json:"ExpireDays,omitempty"`
	NoncurrentDays int    `json:"NoncurrentDays,omitempty"`
}

// GetCORSJSON renders the current CORS config as editable JSON ("" if none).
func (b *backend) GetCORSJSON(ctx context.Context, bucket string) string {
	body, err := b.s3Sub(ctx, "GET", bucket, "cors")
	if err != nil {
		return ""
	}
	var cfg struct {
		Rules []struct {
			AllowedOrigins []string `xml:"AllowedOrigin"`
			AllowedMethods []string `xml:"AllowedMethod"`
			AllowedHeaders []string `xml:"AllowedHeader"`
			ExposeHeaders  []string `xml:"ExposeHeader"`
			MaxAgeSeconds  int      `xml:"MaxAgeSeconds"`
		} `xml:"CORSRule"`
	}
	if xml.Unmarshal(body, &cfg) != nil || len(cfg.Rules) == 0 {
		return ""
	}
	rules := make([]corsJSON, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		rules = append(rules, corsJSON(r))
	}
	out, _ := json.MarshalIndent(rules, "", "  ")
	return string(out)
}

// PutCORSJSON converts the editor's JSON rules to the XML wire config.
func (b *backend) PutCORSJSON(ctx context.Context, bucket, raw string) error {
	var rules []corsJSON
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return fmt.Errorf("CORS rules must be a JSON array: %w", err)
	}
	var sb strings.Builder
	sb.WriteString("<CORSConfiguration>")
	for _, r := range rules {
		sb.WriteString("<CORSRule>")
		for _, o := range r.AllowedOrigins {
			fmt.Fprintf(&sb, "<AllowedOrigin>%s</AllowedOrigin>", xmlEscape(o))
		}
		for _, m := range r.AllowedMethods {
			fmt.Fprintf(&sb, "<AllowedMethod>%s</AllowedMethod>", xmlEscape(m))
		}
		for _, h := range r.AllowedHeaders {
			fmt.Fprintf(&sb, "<AllowedHeader>%s</AllowedHeader>", xmlEscape(h))
		}
		for _, h := range r.ExposeHeaders {
			fmt.Fprintf(&sb, "<ExposeHeader>%s</ExposeHeader>", xmlEscape(h))
		}
		if r.MaxAgeSeconds > 0 {
			fmt.Fprintf(&sb, "<MaxAgeSeconds>%d</MaxAgeSeconds>", r.MaxAgeSeconds)
		}
		sb.WriteString("</CORSRule>")
	}
	sb.WriteString("</CORSConfiguration>")
	req, _ := http.NewRequestWithContext(ctx, "PUT", b.base+"/"+bucket+"?cors", strings.NewReader(sb.String()))
	_, err := b.do(req)
	return err
}

// GetLifecycleJSON renders the current lifecycle rules as editable JSON.
func (b *backend) GetLifecycleJSON(ctx context.Context, bucket string) string {
	body, err := b.s3Sub(ctx, "GET", bucket, "lifecycle")
	if err != nil {
		return ""
	}
	var cfg struct {
		Rules []struct {
			ID         string `xml:"ID"`
			Status     string `xml:"Status"`
			Prefix     string `xml:"Prefix"`
			Expiration struct {
				Days int `xml:"Days"`
			} `xml:"Expiration"`
			NoncurrentVersionExpiration struct {
				NoncurrentDays int `xml:"NoncurrentDays"`
			} `xml:"NoncurrentVersionExpiration"`
		} `xml:"Rule"`
	}
	if xml.Unmarshal(body, &cfg) != nil || len(cfg.Rules) == 0 {
		return ""
	}
	rules := make([]lifecycleJSON, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		rules = append(rules, lifecycleJSON{
			ID: r.ID, Prefix: r.Prefix, Status: r.Status,
			ExpireDays: r.Expiration.Days, NoncurrentDays: r.NoncurrentVersionExpiration.NoncurrentDays,
		})
	}
	out, _ := json.MarshalIndent(rules, "", "  ")
	return string(out)
}

// PutLifecycleJSON converts the editor's JSON rules to the XML wire config.
func (b *backend) PutLifecycleJSON(ctx context.Context, bucket, raw string) error {
	var rules []lifecycleJSON
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return fmt.Errorf("lifecycle rules must be a JSON array: %w", err)
	}
	var sb strings.Builder
	sb.WriteString("<LifecycleConfiguration>")
	for i, r := range rules {
		if r.ID == "" {
			r.ID = fmt.Sprintf("rule-%d", i+1)
		}
		if r.Status == "" {
			r.Status = "Enabled"
		}
		sb.WriteString("<Rule>")
		fmt.Fprintf(&sb, "<ID>%s</ID><Status>%s</Status>", xmlEscape(r.ID), xmlEscape(r.Status))
		if r.Prefix != "" {
			fmt.Fprintf(&sb, "<Prefix>%s</Prefix>", xmlEscape(r.Prefix))
		}
		if r.ExpireDays > 0 {
			fmt.Fprintf(&sb, "<Expiration><Days>%d</Days></Expiration>", r.ExpireDays)
		}
		if r.NoncurrentDays > 0 {
			fmt.Fprintf(&sb, "<NoncurrentVersionExpiration><NoncurrentDays>%d</NoncurrentDays></NoncurrentVersionExpiration>", r.NoncurrentDays)
		}
		sb.WriteString("</Rule>")
	}
	sb.WriteString("</LifecycleConfiguration>")
	req, _ := http.NewRequestWithContext(ctx, "PUT", b.base+"/"+bucket+"?lifecycle", strings.NewReader(sb.String()))
	_, err := b.do(req)
	return err
}

// GetObjectVersion fetches a specific version's bytes.
func (b *backend) GetObjectVersion(ctx context.Context, bucket, key, versionID string) ([]byte, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET",
		b.base+"/"+bucket+"/"+escapeKey(key)+"?versionId="+url.QueryEscape(versionID), nil)
	resp, err := b.c.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, "", &apiErr{status: resp.StatusCode, body: string(body)}
	}
	return body, resp.Header.Get("Content-Type"), nil
}
