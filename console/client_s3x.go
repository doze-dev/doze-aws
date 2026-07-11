package console

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
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

	// Object lock (404 when not enabled).
	if _, err := b.s3Sub(ctx, "GET", bucket, "object-lock"); err == nil {
		p.ObjectLock = true
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
