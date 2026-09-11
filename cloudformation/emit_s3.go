package cloudformation

// Export of S3 buckets and their configuration: versioning, CORS, lifecycle, website, notifications, public access, ownership, and the bucket policy.

import (
	"fmt"

	"github.com/doze-dev/doze-aws/provision"
)

func emitBuckets(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Buckets) {
		b := s.Buckets[name]
		props := map[string]any{"BucketName": name}
		if b.Versioning {
			props["VersioningConfiguration"] = map[string]any{"Status": "Enabled"}
		}
		putIf(props, "ObjectLockEnabled", b.ObjectLock)
		if len(b.CORS) > 0 {
			var rules []any
			for _, c := range b.CORS {
				r := map[string]any{"AllowedOrigins": c.Origins, "AllowedMethods": c.Methods}
				putIfList(r, "AllowedHeaders", c.Headers)
				putIfList(r, "ExposedHeaders", c.Expose)
				putIfNum(r, "MaxAge", c.MaxAge)
				rules = append(rules, r)
			}
			props["CorsConfiguration"] = map[string]any{"CorsRules": rules}
		}
		if len(b.Lifecycle) > 0 {
			var rules []any
			for i, l := range b.Lifecycle {
				r := map[string]any{"Id": fmt.Sprintf("rule-%d", i+1), "Status": "Enabled"}
				if l.Prefix != "" {
					r["Prefix"] = l.Prefix
				}
				putIfNum(r, "ExpirationInDays", l.ExpireDays)
				if l.NoncurrentDays > 0 {
					r["NoncurrentVersionExpiration"] = map[string]any{"NoncurrentDays": l.NoncurrentDays}
				}
				if l.AbortUploadDays > 0 {
					r["AbortIncompleteMultipartUpload"] = map[string]any{"DaysAfterInitiation": l.AbortUploadDays}
				}
				rules = append(rules, r)
			}
			props["LifecycleConfiguration"] = map[string]any{"Rules": rules}
		}
		if b.Website != nil {
			w := map[string]any{}
			if b.Website.Index != "" {
				w["IndexDocument"] = b.Website.Index
			}
			if b.Website.Error != "" {
				w["ErrorDocument"] = b.Website.Error
			}
			props["WebsiteConfiguration"] = w
		}
		if len(b.Notify) > 0 {
			nc := map[string]any{}
			for _, n := range b.Notify {
				events := n.Events
				if len(events) == 0 {
					events = []string{"s3:ObjectCreated:*"}
				}
				for _, ev := range events {
					cfg := map[string]any{"Event": ev}
					if n.Prefix != "" || n.Suffix != "" {
						var rules []any
						if n.Prefix != "" {
							rules = append(rules, map[string]any{"Name": "prefix", "Value": n.Prefix})
						}
						if n.Suffix != "" {
							rules = append(rules, map[string]any{"Name": "suffix", "Value": n.Suffix})
						}
						cfg["Filter"] = map[string]any{"S3Key": map[string]any{"Rules": rules}}
					}
					switch {
					case n.Queue != "":
						cfg["Queue"] = arnSub("sqs", n.Queue)
						nc["QueueConfigurations"] = appendAny(nc["QueueConfigurations"], cfg)
					case n.Topic != "":
						cfg["Topic"] = arnSub("sns", n.Topic)
						nc["TopicConfigurations"] = appendAny(nc["TopicConfigurations"], cfg)
					case n.Lambda != "":
						cfg["Function"] = lambdaArnSub(n.Lambda)
						nc["LambdaConfigurations"] = appendAny(nc["LambdaConfigurations"], cfg)
					}
				}
			}
			props["NotificationConfiguration"] = nc
		}
		if p := b.PublicAccess; p != nil {
			props["PublicAccessBlockConfiguration"] = map[string]any{
				"BlockPublicAcls": p.BlockPublicAcls, "IgnorePublicAcls": p.IgnorePublicAcls,
				"BlockPublicPolicy": p.BlockPublicPolicy, "RestrictPublicBuckets": p.RestrictPublicBuckets,
			}
		}
		if b.Ownership != "" {
			props["OwnershipControls"] = map[string]any{"Rules": []any{map[string]any{"ObjectOwnership": b.Ownership}}}
		}
		putTags(props, b.Tags)
		add("Bucket", name, "AWS::S3::Bucket", props)
		if !b.Policy.IsZero() {
			add("BucketPolicy", name, "AWS::S3::BucketPolicy", map[string]any{
				"Bucket":         map[string]any{"Ref": logicalID("Bucket", name)},
				"PolicyDocument": rawDoc(b.Policy),
			})
		}
	}
}
