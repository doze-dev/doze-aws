package stackfile

// S3 apply + export: buckets, versioning/CORS/lifecycle/website/tagging
// configs, and bucket notifications (wired after queues/topics/functions).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ---- buckets (sans notifications — those wire after functions/topics) ----

func applyBuckets(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Buckets) {
		b := s.Buckets[name]
		exists := true
		if _, err := c.do(ctx, "HEAD", "/"+name, nil, nil); err != nil {
			if !notFound(err) {
				return fmt.Errorf("bucket %q: %w", name, err)
			}
			exists = false
		}
		if !exists {
			headers := map[string]string{}
			if b.ObjectLock {
				headers["x-amz-bucket-object-lock-enabled"] = "true"
			}
			if _, err := c.do(ctx, "PUT", "/"+name, headers, nil); err != nil {
				return fmt.Errorf("bucket %q: %w", name, err)
			}
			rep.add("created", "bucket/"+name, "")
		} else {
			rep.add("skipped", "bucket/"+name, "exists")
		}
		// The bucket configs are all idempotent full-replace upserts.
		if b.Versioning || b.ObjectLock {
			body := `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`
			if _, err := c.do(ctx, "PUT", "/"+name+"?versioning", nil, []byte(body)); err != nil {
				return fmt.Errorf("bucket %q versioning: %w", name, err)
			}
		}
		if len(b.CORS) > 0 {
			if _, err := c.do(ctx, "PUT", "/"+name+"?cors", nil, corsXML(b.CORS)); err != nil {
				return fmt.Errorf("bucket %q cors: %w", name, err)
			}
		}
		if len(b.Lifecycle) > 0 {
			if _, err := c.do(ctx, "PUT", "/"+name+"?lifecycle", nil, lifecycleXML(b.Lifecycle)); err != nil {
				return fmt.Errorf("bucket %q lifecycle: %w", name, err)
			}
		}
		if b.Website != nil {
			if _, err := c.do(ctx, "PUT", "/"+name+"?website", nil, websiteXML(*b.Website)); err != nil {
				return fmt.Errorf("bucket %q website: %w", name, err)
			}
		}
		if len(b.Tags) > 0 {
			if _, err := c.do(ctx, "PUT", "/"+name+"?tagging", nil, taggingXML(b.Tags)); err != nil {
				return fmt.Errorf("bucket %q tags: %w", name, err)
			}
		}
	}
	return nil
}

func corsXML(rules []CORSRule) []byte {
	var sb strings.Builder
	sb.WriteString("<CORSConfiguration>")
	for _, r := range rules {
		sb.WriteString("<CORSRule>")
		for _, o := range r.Origins {
			sb.WriteString("<AllowedOrigin>" + xmlEsc(o) + "</AllowedOrigin>")
		}
		for _, m := range r.Methods {
			sb.WriteString("<AllowedMethod>" + xmlEsc(m) + "</AllowedMethod>")
		}
		for _, h := range r.Headers {
			sb.WriteString("<AllowedHeader>" + xmlEsc(h) + "</AllowedHeader>")
		}
		for _, e := range r.Expose {
			sb.WriteString("<ExposeHeader>" + xmlEsc(e) + "</ExposeHeader>")
		}
		if r.MaxAge > 0 {
			sb.WriteString("<MaxAgeSeconds>" + strconv.Itoa(r.MaxAge) + "</MaxAgeSeconds>")
		}
		sb.WriteString("</CORSRule>")
	}
	sb.WriteString("</CORSConfiguration>")
	return []byte(sb.String())
}

func lifecycleXML(rules []LifecycleRule) []byte {
	var sb strings.Builder
	sb.WriteString("<LifecycleConfiguration>")
	for i, r := range rules {
		sb.WriteString("<Rule><ID>stackfile-" + strconv.Itoa(i+1) + "</ID><Status>Enabled</Status>")
		if r.Prefix != "" {
			sb.WriteString("<Prefix>" + xmlEsc(r.Prefix) + "</Prefix>")
		}
		if r.ExpireDays > 0 {
			sb.WriteString("<Expiration><Days>" + strconv.Itoa(r.ExpireDays) + "</Days></Expiration>")
		}
		if r.NoncurrentDays > 0 {
			sb.WriteString("<NoncurrentVersionExpiration><NoncurrentDays>" + strconv.Itoa(r.NoncurrentDays) + "</NoncurrentDays></NoncurrentVersionExpiration>")
		}
		if r.AbortUploadDays > 0 {
			sb.WriteString("<AbortIncompleteMultipartUpload><DaysAfterInitiation>" + strconv.Itoa(r.AbortUploadDays) + "</DaysAfterInitiation></AbortIncompleteMultipartUpload>")
		}
		sb.WriteString("</Rule>")
	}
	sb.WriteString("</LifecycleConfiguration>")
	return []byte(sb.String())
}

func websiteXML(w Website) []byte {
	var sb strings.Builder
	sb.WriteString("<WebsiteConfiguration>")
	if w.Index != "" {
		sb.WriteString("<IndexDocument><Suffix>" + xmlEsc(w.Index) + "</Suffix></IndexDocument>")
	}
	if w.Error != "" {
		sb.WriteString("<ErrorDocument><Key>" + xmlEsc(w.Error) + "</Key></ErrorDocument>")
	}
	sb.WriteString("</WebsiteConfiguration>")
	return []byte(sb.String())
}

func taggingXML(tags map[string]string) []byte {
	var sb strings.Builder
	sb.WriteString("<Tagging><TagSet>")
	for _, k := range sortedNames(tags) {
		sb.WriteString("<Tag><Key>" + xmlEsc(k) + "</Key><Value>" + xmlEsc(tags[k]) + "</Value></Tag>")
	}
	sb.WriteString("</TagSet></Tagging>")
	return []byte(sb.String())
}

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// ---- bucket notifications (after queues/topics/functions exist) ----

func applyNotifications(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Buckets) {
		b := s.Buckets[name]
		if len(b.Notify) == 0 {
			continue
		}
		var sb strings.Builder
		sb.WriteString(`<NotificationConfiguration>`)
		for i, nf := range b.Notify {
			events := nf.Events
			if len(events) == 0 {
				events = []string{"s3:ObjectCreated:*"}
			}
			var target, tag, arn string
			switch {
			case nf.Queue != "":
				tag, target, arn = "QueueConfiguration", "Queue", queueARN(nf.Queue)
			case nf.Topic != "":
				tag, target, arn = "TopicConfiguration", "Topic", topicARN(nf.Topic)
			default:
				tag, target, arn = "CloudFunctionConfiguration", "CloudFunction", lambdaARN(nf.Lambda)
			}
			sb.WriteString("<" + tag + "><Id>stackfile-" + strconv.Itoa(i+1) + "</Id>")
			if nf.Prefix != "" || nf.Suffix != "" {
				sb.WriteString("<Filter><S3Key>")
				if nf.Prefix != "" {
					sb.WriteString("<FilterRule><Name>prefix</Name><Value>" + nf.Prefix + "</Value></FilterRule>")
				}
				if nf.Suffix != "" {
					sb.WriteString("<FilterRule><Name>suffix</Name><Value>" + nf.Suffix + "</Value></FilterRule>")
				}
				sb.WriteString("</S3Key></Filter>")
			}
			for _, e := range events {
				sb.WriteString("<Event>" + e + "</Event>")
			}
			sb.WriteString("<" + target + ">" + arn + "</" + target + ">")
			sb.WriteString("</" + tag + ">")
		}
		sb.WriteString(`</NotificationConfiguration>`)
		if _, err := c.do(ctx, "PUT", "/"+name+"?notification", nil, []byte(sb.String())); err != nil {
			return fmt.Errorf("bucket %q notifications: %w", name, err)
		}
		rep.add("updated", "bucket/"+name, "notifications")
	}
	return nil
}

func exportBuckets(ctx context.Context, c *client, s *Stack) error {
	out, err := c.do(ctx, "GET", "/", nil, nil)
	if err != nil {
		return err
	}
	names := xmlValues(string(out), "Name")
	if len(names) > 0 {
		s.Buckets = map[string]Bucket{}
	}
	for _, name := range names {
		b := Bucket{}
		if out, err := c.do(ctx, "GET", "/"+name+"?versioning", nil, nil); err == nil {
			b.Versioning = strings.Contains(string(out), "<Status>Enabled</Status>")
		}
		if out, err := c.do(ctx, "GET", "/"+name+"?notification", nil, nil); err == nil {
			b.Notify = parseNotifyXML(string(out))
		}
		if out, err := c.do(ctx, "GET", "/"+name+"?cors", nil, nil); err == nil {
			b.CORS = parseCORSXML(string(out))
		}
		if out, err := c.do(ctx, "GET", "/"+name+"?lifecycle", nil, nil); err == nil {
			b.Lifecycle = parseLifecycleXML(string(out))
		}
		if out, err := c.do(ctx, "GET", "/"+name+"?website", nil, nil); err == nil {
			w := Website{
				Index: xmlValue(xmlBlock(string(out), "IndexDocument"), "Suffix"),
				Error: xmlValue(xmlBlock(string(out), "ErrorDocument"), "Key"),
			}
			if w.Index != "" || w.Error != "" {
				b.Website = &w
			}
		}
		if out, err := c.do(ctx, "GET", "/"+name+"?tagging", nil, nil); err == nil {
			for _, tag := range xmlBlocks(string(out), "Tag") {
				if b.Tags == nil {
					b.Tags = map[string]string{}
				}
				b.Tags[xmlValue(tag, "Key")] = xmlValue(tag, "Value")
			}
		}
		s.Buckets[name] = b
	}
	return nil
}

func parseCORSXML(x string) []CORSRule {
	var out []CORSRule
	for _, block := range xmlBlocks(x, "CORSRule") {
		r := CORSRule{
			Origins: xmlValues(block, "AllowedOrigin"),
			Methods: xmlValues(block, "AllowedMethod"),
			Headers: xmlValues(block, "AllowedHeader"),
			Expose:  xmlValues(block, "ExposeHeader"),
			MaxAge:  atoiDefault(xmlValue(block, "MaxAgeSeconds"), 0),
		}
		out = append(out, r)
	}
	return out
}

func parseLifecycleXML(x string) []LifecycleRule {
	var out []LifecycleRule
	for _, block := range xmlBlocks(x, "Rule") {
		r := LifecycleRule{
			Prefix:          xmlValue(block, "Prefix"),
			ExpireDays:      atoiDefault(xmlValue(xmlBlock(block, "Expiration"), "Days"), 0),
			NoncurrentDays:  atoiDefault(xmlValue(block, "NoncurrentDays"), 0),
			AbortUploadDays: atoiDefault(xmlValue(block, "DaysAfterInitiation"), 0),
		}
		if r.ExpireDays > 0 || r.NoncurrentDays > 0 || r.AbortUploadDays > 0 {
			out = append(out, r)
		}
	}
	return out
}

// parseNotifyXML extracts notification targets from the S3 XML config.
func parseNotifyXML(x string) []Notify {
	var out []Notify
	for _, cfg := range []struct{ tag, target, kind string }{
		{"QueueConfiguration", "Queue", "queue"},
		{"TopicConfiguration", "Topic", "topic"},
		{"CloudFunctionConfiguration", "CloudFunction", "lambda"},
	} {
		s := x
		for {
			i := strings.Index(s, "<"+cfg.tag+">")
			if i < 0 {
				break
			}
			j := strings.Index(s[i:], "</"+cfg.tag+">")
			if j < 0 {
				break
			}
			block := s[i : i+j]
			nf := Notify{Events: xmlValues(block, "Event")}
			arn := xmlValue(block, cfg.target)
			leaf := arnLeaf(arn)
			switch cfg.kind {
			case "queue":
				nf.Queue = leaf
			case "topic":
				nf.Topic = leaf
			case "lambda":
				nf.Lambda = strings.TrimPrefix(leaf, "function:")
			}
			// prefix/suffix filter rules, if present
			rules := block
			for {
				k := strings.Index(rules, "<FilterRule>")
				if k < 0 {
					break
				}
				m := strings.Index(rules[k:], "</FilterRule>")
				if m < 0 {
					break
				}
				rule := rules[k : k+m]
				switch strings.ToLower(xmlValue(rule, "Name")) {
				case "prefix":
					nf.Prefix = xmlValue(rule, "Value")
				case "suffix":
					nf.Suffix = xmlValue(rule, "Value")
				}
				rules = rules[k+m:]
			}
			out = append(out, nf)
			s = s[i+j:]
		}
	}
	return out
}
