package cloudformation

// The S3 resources: the bucket itself, the access settings that have a local
// effect, the policy that arrives as its own resource, and the notification
// shape. Split out of mapper.go, which the plan for these gaps asked for and
// which did not happen at the time — it had grown past a thousand lines.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

// ---- S3 ----

func (m *mapper) bucket(name string, props map[string]any) error {
	b := provision.Bucket{Tags: propTags(props)}
	if vc := propMap(props, "VersioningConfiguration"); vc != nil {
		b.Versioning = strings.EqualFold(propStr(vc, "Status"), "Enabled")
	}
	if propBool(props, "ObjectLockEnabled") {
		b.ObjectLock = true
	}
	if cors := propMap(props, "CorsConfiguration"); cors != nil {
		for _, item := range propList(cors, "CorsRules") {
			rule, ok := item.(map[string]any)
			if !ok {
				continue
			}
			b.CORS = append(b.CORS, provision.CORSRule{
				Origins: strList(propList(rule, "AllowedOrigins")),
				Methods: strList(propList(rule, "AllowedMethods")),
				Headers: strList(propList(rule, "AllowedHeaders")),
				Expose:  strList(propList(rule, "ExposedHeaders")),
				MaxAge:  propInt(rule, "MaxAge"),
			})
		}
	}
	if lc := propMap(props, "LifecycleConfiguration"); lc != nil {
		for _, item := range propList(lc, "Rules") {
			rule, ok := item.(map[string]any)
			if !ok || strings.EqualFold(propStr(rule, "Status"), "Disabled") {
				continue
			}
			lr := provision.LifecycleRule{
				Prefix:     propStr(rule, "Prefix"),
				ExpireDays: propInt(rule, "ExpirationInDays"),
			}
			if nv := propMap(rule, "NoncurrentVersionExpiration"); nv != nil {
				lr.NoncurrentDays = propInt(nv, "NoncurrentDays")
			}
			if ab := propMap(rule, "AbortIncompleteMultipartUpload"); ab != nil {
				lr.AbortUploadDays = propInt(ab, "DaysAfterInitiation")
			}
			b.Lifecycle = append(b.Lifecycle, lr)
		}
	}
	if wc := propMap(props, "WebsiteConfiguration"); wc != nil {
		b.Website = &provision.Website{
			Index: propStr(wc, "IndexDocument"),
			Error: propStr(wc, "ErrorDocument"),
		}
	}
	if nc := propMap(props, "NotificationConfiguration"); nc != nil {
		b.Notify = append(b.Notify, notificationsFrom(nc)...)
	}
	bucketAccess(&b, props)
	m.stack.Buckets[name] = b
	return nil
}

// bucketAccess reads the access settings that have a local effect or a
// local read-back: the public access block (BlockPublicPolicy is enforced)
// and ownership controls (stored).
func bucketAccess(b *provision.Bucket, props map[string]any) {
	if pab := propMap(props, "PublicAccessBlockConfiguration"); pab != nil {
		b.PublicAccess = &provision.PublicAccessBlock{
			BlockPublicAcls: propBool(pab, "BlockPublicAcls"), IgnorePublicAcls: propBool(pab, "IgnorePublicAcls"),
			BlockPublicPolicy: propBool(pab, "BlockPublicPolicy"), RestrictPublicBuckets: propBool(pab, "RestrictPublicBuckets"),
		}
	}
	if oc := propMap(props, "OwnershipControls"); oc != nil {
		for _, item := range propList(oc, "Rules") {
			if rule, ok := item.(map[string]any); ok {
				b.Ownership = propStr(rule, "ObjectOwnership")
			}
		}
	}
}

// bucketPolicy maps AWS::S3::BucketPolicy onto its bucket, once the bucket
// is known.
func (m *mapper) bucketPolicy(props map[string]any) error {
	bucket := nameFromARN(propStr(props, "Bucket"))
	if bucket == "" {
		return fmt.Errorf("Bucket is required")
	}
	doc := props["PolicyDocument"]
	m.deferred = append(m.deferred, func() error {
		b, ok := m.stack.Buckets[bucket]
		if !ok {
			return fmt.Errorf("bucket policy references unknown bucket %q", bucket)
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("bucket policy for %q: %w", bucket, err)
		}
		b.Policy = provision.Doc{JSON: string(raw)}
		m.stack.Buckets[bucket] = b
		return nil
	})
	return nil
}

// notificationsFrom converts the three destination-specific configuration
// lists into the stack file's single Notify shape.
func notificationsFrom(nc map[string]any) []provision.Notify {
	var out []provision.Notify
	add := func(key, arnField string, set func(*provision.Notify, string)) {
		for _, item := range propList(nc, key) {
			cfg, ok := item.(map[string]any)
			if !ok {
				continue
			}
			n := provision.Notify{}
			if ev := propStr(cfg, "Event"); ev != "" {
				n.Events = []string{ev}
			}
			// Prefix/suffix live in a nested filter rule list.
			if filter := propMap(cfg, "Filter"); filter != nil {
				if s3key := propMap(filter, "S3Key"); s3key != nil {
					for _, r := range propList(s3key, "Rules") {
						rule, ok := r.(map[string]any)
						if !ok {
							continue
						}
						switch strings.ToLower(propStr(rule, "Name")) {
						case "prefix":
							n.Prefix = propStr(rule, "Value")
						case "suffix":
							n.Suffix = propStr(rule, "Value")
						}
					}
				}
			}
			set(&n, nameFromARN(propStr(cfg, arnField)))
			out = append(out, n)
		}
	}
	add("QueueConfigurations", "Queue", func(n *provision.Notify, v string) { n.Queue = v })
	add("TopicConfigurations", "Topic", func(n *provision.Notify, v string) { n.Topic = v })
	add("LambdaConfigurations", "Function", func(n *provision.Notify, v string) { n.Lambda = v })
	return out
}
