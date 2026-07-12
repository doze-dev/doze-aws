package stackfile

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// Action is one thing Apply did (or decided not to do).
type Action struct {
	Op       string // created | updated | skipped
	Resource string // e.g. "queue/orders"
	Detail   string
}

// Report is the full apply outcome.
type Report struct {
	Actions []Action
}

func (r *Report) add(op, resource, detail string) {
	r.Actions = append(r.Actions, Action{Op: op, Resource: resource, Detail: detail})
}

// Counts summarizes the report as created/updated/skipped.
func (r *Report) Counts() (created, updated, skipped int) {
	for _, a := range r.Actions {
		switch a.Op {
		case "created":
			created++
		case "updated":
			updated++
		default:
			skipped++
		}
	}
	return
}

// Apply converges the running stack toward the file: resources are created if
// missing and cheaply updated if present; nothing is ever deleted. Phases run
// in dependency order so references by name always resolve.
func Apply(ctx context.Context, gateway http.Handler, s *Stack) (*Report, error) {
	c := newClient(gateway)
	rep := &Report{}

	type phase struct {
		name string
		run  func() error
	}
	phases := []phase{
		{"queues", func() error { return applyQueues(ctx, c, s, rep) }},
		{"tables", func() error { return applyTables(ctx, c, s, rep) }},
		{"keys", func() error { return applyKeys(ctx, c, s, rep) }},
		{"buckets", func() error { return applyBuckets(ctx, c, s, rep) }},
		{"functions", func() error { return applyFunctions(ctx, c, s, rep) }},
		{"topics", func() error { return applyTopics(ctx, c, s, rep) }},
		{"rules", func() error { return applyRules(ctx, c, s, rep) }},
		{"notifications", func() error { return applyNotifications(ctx, c, s, rep) }},
		{"secrets", func() error { return applySecrets(ctx, c, s, rep) }},
		{"parameters", func() error { return applyParameters(ctx, c, s, rep) }},
	}
	for _, p := range phases {
		if err := p.run(); err != nil {
			return rep, fmt.Errorf("stackfile: %s: %w", p.name, err)
		}
	}
	return rep, nil
}

// ---- queues ----

func autoDLQName(name string, fifo bool) string {
	base := strings.TrimSuffix(name, ".fifo") + "-dlq"
	if fifo {
		base += ".fifo"
	}
	return base
}

func applyQueues(ctx context.Context, c *client, s *Stack, rep *Report) error {
	ensure := func(name string, q Queue) error {
		exists := true
		if _, err := c.sqs(ctx, "GetQueueUrl", map[string]any{"QueueName": name}); err != nil {
			if !notFound(err) {
				return err
			}
			exists = false
		}
		attrs := map[string]string{}
		if q.FIFO {
			attrs["FifoQueue"] = "true"
			if q.ContentDedup {
				attrs["ContentBasedDeduplication"] = "true"
			}
		}
		if q.Visibility > 0 {
			attrs["VisibilityTimeout"] = strconv.Itoa(q.Visibility)
		}
		if q.Delay > 0 {
			attrs["DelaySeconds"] = strconv.Itoa(q.Delay)
		}
		if q.Retention > 0 {
			attrs["MessageRetentionPeriod"] = strconv.Itoa(q.Retention)
		}
		if q.ReceiveWait > 0 {
			attrs["ReceiveMessageWaitTimeSeconds"] = strconv.Itoa(q.ReceiveWait)
		}
		if q.MaxSize > 0 {
			attrs["MaximumMessageSize"] = strconv.Itoa(q.MaxSize)
		}
		if q.DLQ != "" {
			dlq := q.DLQ
			if dlq == "auto" {
				dlq = autoDLQName(name, q.FIFO)
				// The auto DLQ mirrors the main queue's type.
				if err := ensureBareQueue(ctx, c, rep, dlq, q.FIFO); err != nil {
					return err
				}
			}
			maxr := q.MaxReceives
			if maxr <= 0 {
				maxr = 3
			}
			rp, _ := json.Marshal(map[string]string{
				"deadLetterTargetArn": queueARN(dlq),
				"maxReceiveCount":     strconv.Itoa(maxr),
			})
			attrs["RedrivePolicy"] = string(rp)
		}

		if !exists {
			in := map[string]any{"QueueName": name}
			if len(attrs) > 0 {
				in["Attributes"] = attrs
			}
			if len(q.Tags) > 0 {
				in["tags"] = q.Tags
			}
			if _, err := c.sqs(ctx, "CreateQueue", in); err != nil {
				return err
			}
			rep.add("created", "queue/"+name, "")
			return nil
		}
		// Converge mutable attributes on the existing queue (FIFO-ness is
		// create-time-only, so it is dropped here).
		delete(attrs, "FifoQueue")
		if len(attrs) > 0 {
			if _, err := c.sqs(ctx, "SetQueueAttributes", map[string]any{
				"QueueUrl": queueURL(name), "Attributes": attrs,
			}); err != nil {
				return err
			}
		}
		if len(q.Tags) > 0 {
			if _, err := c.sqs(ctx, "TagQueue", map[string]any{
				"QueueUrl": queueURL(name), "Tags": q.Tags,
			}); err != nil {
				return err
			}
		}
		if len(attrs) > 0 {
			rep.add("updated", "queue/"+name, "attributes")
		} else {
			rep.add("skipped", "queue/"+name, "exists")
		}
		return nil
	}
	for _, name := range sortedNames(s.Queues) {
		if err := ensure(name, s.Queues[name]); err != nil {
			return fmt.Errorf("queue %q: %w", name, err)
		}
	}
	return nil
}

func ensureBareQueue(ctx context.Context, c *client, rep *Report, name string, fifo bool) error {
	if _, err := c.sqs(ctx, "GetQueueUrl", map[string]any{"QueueName": name}); err == nil {
		return nil
	} else if !notFound(err) {
		return err
	}
	in := map[string]any{"QueueName": name}
	if fifo {
		in["Attributes"] = map[string]string{"FifoQueue": "true"}
	}
	if _, err := c.sqs(ctx, "CreateQueue", in); err != nil {
		return err
	}
	rep.add("created", "queue/"+name, "auto dead-letter")
	return nil
}

// ---- tables ----

func applyTables(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Tables) {
		t := s.Tables[name]
		if out, err := c.ddb(ctx, "DescribeTable", map[string]any{"TableName": name}); err == nil {
			if err := convergeTable(ctx, c, rep, name, t, out); err != nil {
				return fmt.Errorf("table %q: %w", name, err)
			}
			continue
		} else if !notFound(err) {
			return fmt.Errorf("table %q: %w", name, err)
		}

		hash, rng, _ := parseKey(t.Key) // validated at parse time
		defs := map[string]string{hash.Name: hash.Type}
		schema := []map[string]string{{"AttributeName": hash.Name, "KeyType": "HASH"}}
		if rng != nil {
			defs[rng.Name] = rng.Type
			schema = append(schema, map[string]string{"AttributeName": rng.Name, "KeyType": "RANGE"})
		}
		var gsis []map[string]any
		for _, gname := range sortedNames(t.GSIs) {
			g := t.GSIs[gname]
			gh, gr, _ := parseKey(g.Key)
			defs[gh.Name] = gh.Type
			ks := []map[string]string{{"AttributeName": gh.Name, "KeyType": "HASH"}}
			if gr != nil {
				defs[gr.Name] = gr.Type
				ks = append(ks, map[string]string{"AttributeName": gr.Name, "KeyType": "RANGE"})
			}
			gsis = append(gsis, map[string]any{
				"IndexName": gname, "KeySchema": ks,
				"Projection": projectionWire(g.Projection, g.Include),
			})
		}
		var lsis []map[string]any
		for _, lname := range sortedNames(t.LSIs) {
			l := t.LSIs[lname]
			lr, _, _ := parseKey(l.Key) // single "attr:TYPE", validated at parse time
			defs[lr.Name] = lr.Type
			lsis = append(lsis, map[string]any{
				"IndexName": lname,
				"KeySchema": []map[string]string{
					{"AttributeName": hash.Name, "KeyType": "HASH"},
					{"AttributeName": lr.Name, "KeyType": "RANGE"},
				},
				"Projection": projectionWire(l.Projection, l.Include),
			})
		}
		var attrs []map[string]string
		for _, n := range sortedNames(defs) {
			attrs = append(attrs, map[string]string{"AttributeName": n, "AttributeType": defs[n]})
		}
		in := map[string]any{
			"TableName": name, "AttributeDefinitions": attrs, "KeySchema": schema,
			"BillingMode": "PAY_PER_REQUEST",
		}
		if len(gsis) > 0 {
			in["GlobalSecondaryIndexes"] = gsis
		}
		if len(lsis) > 0 {
			in["LocalSecondaryIndexes"] = lsis
		}
		if t.DeletionProtection != nil {
			in["DeletionProtectionEnabled"] = *t.DeletionProtection
		}
		if len(t.Tags) > 0 {
			in["Tags"] = tagList(t.Tags, "Key", "Value")
		}
		if _, err := c.ddb(ctx, "CreateTable", in); err != nil {
			return fmt.Errorf("table %q: %w", name, err)
		}
		if t.TTL != "" {
			if _, err := c.ddb(ctx, "UpdateTimeToLive", map[string]any{
				"TableName":               name,
				"TimeToLiveSpecification": map[string]any{"Enabled": true, "AttributeName": t.TTL},
			}); err != nil {
				return fmt.Errorf("table %q ttl: %w", name, err)
			}
		}
		rep.add("created", "table/"+name, "")
	}
	return nil
}

// convergeTable updates what an existing table can cheaply change: missing
// GSIs (UpdateTable backfills synchronously), TTL, deletion protection, and
// tags. The key schema and LSIs are create-time-only, so they are left alone.
func convergeTable(ctx context.Context, c *client, rep *Report, name string, t Table, describe []byte) error {
	var d struct {
		Table struct {
			GlobalSecondaryIndexes    []struct{ IndexName string }
			DeletionProtectionEnabled bool
		}
	}
	json.Unmarshal(describe, &d)
	var changes []string

	live := map[string]bool{}
	for _, g := range d.Table.GlobalSecondaryIndexes {
		live[g.IndexName] = true
	}
	for _, gname := range sortedNames(t.GSIs) {
		if live[gname] {
			continue
		}
		g := t.GSIs[gname]
		gh, gr, _ := parseKey(g.Key)
		defs := []map[string]string{{"AttributeName": gh.Name, "AttributeType": gh.Type}}
		ks := []map[string]string{{"AttributeName": gh.Name, "KeyType": "HASH"}}
		if gr != nil {
			defs = append(defs, map[string]string{"AttributeName": gr.Name, "AttributeType": gr.Type})
			ks = append(ks, map[string]string{"AttributeName": gr.Name, "KeyType": "RANGE"})
		}
		if _, err := c.ddb(ctx, "UpdateTable", map[string]any{
			"TableName": name, "AttributeDefinitions": defs,
			"GlobalSecondaryIndexUpdates": []map[string]any{{"Create": map[string]any{
				"IndexName": gname, "KeySchema": ks,
				"Projection": projectionWire(g.Projection, g.Include),
			}}},
		}); err != nil {
			return fmt.Errorf("gsi %q: %w", gname, err)
		}
		changes = append(changes, "gsi "+gname)
	}

	if t.DeletionProtection != nil && *t.DeletionProtection != d.Table.DeletionProtectionEnabled {
		if _, err := c.ddb(ctx, "UpdateTable", map[string]any{
			"TableName": name, "DeletionProtectionEnabled": *t.DeletionProtection,
		}); err != nil {
			return fmt.Errorf("deletion protection: %w", err)
		}
		changes = append(changes, "deletion protection")
	}

	if t.TTL != "" {
		enabled := false
		if out, err := c.ddb(ctx, "DescribeTimeToLive", map[string]any{"TableName": name}); err == nil {
			var ttl struct {
				TimeToLiveDescription struct{ AttributeName, TimeToLiveStatus string }
			}
			json.Unmarshal(out, &ttl)
			enabled = ttl.TimeToLiveDescription.TimeToLiveStatus == "ENABLED" &&
				ttl.TimeToLiveDescription.AttributeName == t.TTL
		}
		if !enabled {
			if _, err := c.ddb(ctx, "UpdateTimeToLive", map[string]any{
				"TableName":               name,
				"TimeToLiveSpecification": map[string]any{"Enabled": true, "AttributeName": t.TTL},
			}); err != nil {
				return fmt.Errorf("ttl: %w", err)
			}
			changes = append(changes, "ttl")
		}
	}

	if len(t.Tags) > 0 {
		if _, err := c.ddb(ctx, "TagResource", map[string]any{
			"ResourceArn": tableARN(name), "Tags": tagList(t.Tags, "Key", "Value"),
		}); err != nil {
			return fmt.Errorf("tags: %w", err)
		}
	}

	if len(changes) > 0 {
		rep.add("updated", "table/"+name, strings.Join(changes, ", "))
	} else {
		rep.add("skipped", "table/"+name, "exists (key schema is immutable)")
	}
	return nil
}

// projectionWire renders a gsi/lsi projection for CreateTable/UpdateTable.
func projectionWire(p string, include []string) map[string]any {
	out := map[string]any{"ProjectionType": "ALL"}
	if p != "" {
		out["ProjectionType"] = p
	}
	if p == "INCLUDE" {
		out["NonKeyAttributes"] = include
	}
	return out
}

// tagList renders a tag map as the sorted key/value pair list most AWS APIs
// take (key and value field names vary by service).
func tagList(tags map[string]string, keyField, valField string) []map[string]string {
	var out []map[string]string
	for _, k := range sortedNames(tags) {
		out = append(out, map[string]string{keyField: k, valField: tags[k]})
	}
	return out
}

func tableARN(name string) string { return awsident.ARN("dynamodb", "table/"+name) }

// ---- keys (KMS, keyed by alias) ----

func applyKeys(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Keys) {
		k := s.Keys[name]
		alias := "alias/" + name
		// Existing alias → converge rotation only.
		body, err := c.json11(ctx, "TrentService", "ListAliases", map[string]any{})
		if err != nil {
			return fmt.Errorf("key %q: %w", name, err)
		}
		var aliases struct {
			Aliases []struct {
				AliasName   string `json:"AliasName"`
				TargetKeyId string `json:"TargetKeyId"`
			} `json:"Aliases"`
		}
		json.Unmarshal(body, &aliases)
		keyID := ""
		for _, a := range aliases.Aliases {
			if a.AliasName == alias {
				keyID = a.TargetKeyId
			}
		}
		created := false
		if keyID == "" {
			in := map[string]any{}
			if k.Spec != "" && k.Spec != "SYMMETRIC_DEFAULT" {
				in["KeySpec"] = k.Spec
				if strings.HasPrefix(k.Spec, "RSA") || strings.HasPrefix(k.Spec, "ECC") {
					in["KeyUsage"] = "SIGN_VERIFY"
				}
				if strings.HasPrefix(k.Spec, "HMAC") {
					in["KeyUsage"] = "GENERATE_VERIFY_MAC"
				}
			}
			if k.Usage != "" {
				in["KeyUsage"] = k.Usage // an explicit usage beats the spec default
			}
			if k.Description != "" {
				in["Description"] = k.Description
			}
			if len(k.Tags) > 0 {
				in["Tags"] = tagList(k.Tags, "TagKey", "TagValue")
			}
			out, err := c.json11(ctx, "TrentService", "CreateKey", in)
			if err != nil {
				return fmt.Errorf("key %q: %w", name, err)
			}
			var ck struct {
				KeyMetadata struct {
					KeyId string `json:"KeyId"`
				} `json:"KeyMetadata"`
			}
			json.Unmarshal(out, &ck)
			keyID = ck.KeyMetadata.KeyId
			if _, err := c.json11(ctx, "TrentService", "CreateAlias", map[string]any{
				"AliasName": alias, "TargetKeyId": keyID,
			}); err != nil {
				return fmt.Errorf("key %q alias: %w", name, err)
			}
			created = true
		}
		if k.Rotation {
			if _, err := c.json11(ctx, "TrentService", "EnableKeyRotation", map[string]any{"KeyId": keyID}); err != nil {
				return fmt.Errorf("key %q rotation: %w", name, err)
			}
		}
		if !created {
			// Converge the cheap metadata on an existing key.
			if k.Description != "" {
				if _, err := c.json11(ctx, "TrentService", "UpdateKeyDescription", map[string]any{
					"KeyId": keyID, "Description": k.Description,
				}); err != nil {
					return fmt.Errorf("key %q description: %w", name, err)
				}
			}
			if len(k.Tags) > 0 {
				if _, err := c.json11(ctx, "TrentService", "TagResource", map[string]any{
					"KeyId": keyID, "Tags": tagList(k.Tags, "TagKey", "TagValue"),
				}); err != nil {
					return fmt.Errorf("key %q tags: %w", name, err)
				}
			}
		}
		if created {
			rep.add("created", "key/"+name, keyID)
		} else {
			rep.add("skipped", "key/"+name, "exists")
		}
	}
	return nil
}

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

// ---- functions ----

func applyFunctions(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Functions) {
		f := s.Functions[name]
		code, err := filepath.Abs(f.Code)
		if err != nil {
			return fmt.Errorf("function %q: %w", name, err)
		}
		if _, err := os.Stat(code); err != nil {
			return fmt.Errorf("function %q: code path %s does not exist", name, code)
		}

		exists := true
		if _, err := c.do(ctx, "GET", "/2015-03-31/functions/"+url.PathEscape(name), nil, nil); err != nil {
			if !notFound(err) {
				return fmt.Errorf("function %q: %w", name, err)
			}
			exists = false
		}

		cfg := map[string]any{
			"FunctionName": name,
			"Runtime":      f.Runtime,
			"Handler":      f.Handler,
			"Role":         "arn:aws:iam::" + "000000000000" + ":role/stackfile",
		}
		if f.Timeout > 0 {
			cfg["Timeout"] = f.Timeout
		}
		if f.Memory > 0 {
			cfg["MemorySize"] = f.Memory
		}
		if len(f.Env) > 0 {
			cfg["Environment"] = map[string]any{"Variables": f.Env}
		}
		if len(f.Command) > 0 {
			cfg["Command"] = f.Command
		}
		if f.DLQ != nil {
			cfg["DeadLetterConfig"] = map[string]string{"TargetArn": f.DLQ.arn()}
		}

		if !exists {
			cfg["Code"] = map[string]string{"S3Bucket": "_local_", "S3Key": code}
			if _, err := c.do(ctx, "POST", "/2015-03-31/functions", map[string]string{"Content-Type": "application/json"}, mustJSON(cfg)); err != nil {
				return fmt.Errorf("function %q: %w", name, err)
			}
			rep.add("created", "function/"+name, "")
		} else {
			delete(cfg, "FunctionName")
			delete(cfg, "Role")
			if _, err := c.do(ctx, "PUT", "/2015-03-31/functions/"+url.PathEscape(name)+"/configuration", map[string]string{"Content-Type": "application/json"}, mustJSON(cfg)); err != nil {
				return fmt.Errorf("function %q config: %w", name, err)
			}
			codeReq := map[string]string{"S3Bucket": "_local_", "S3Key": code}
			if _, err := c.do(ctx, "PUT", "/2015-03-31/functions/"+url.PathEscape(name)+"/code", map[string]string{"Content-Type": "application/json"}, mustJSON(codeReq)); err != nil {
				return fmt.Errorf("function %q code: %w", name, err)
			}
			rep.add("updated", "function/"+name, "configuration + code path")
		}

		// Async destinations and retry policy (upsert).
		if f.OnSuccess != nil || f.OnFailure != nil || f.Retries != nil {
			eic := map[string]any{}
			dc := map[string]any{}
			if f.OnSuccess != nil {
				dc["OnSuccess"] = map[string]string{"Destination": f.OnSuccess.arn()}
			}
			if f.OnFailure != nil {
				dc["OnFailure"] = map[string]string{"Destination": f.OnFailure.arn()}
			}
			if len(dc) > 0 {
				eic["DestinationConfig"] = dc
			}
			if f.Retries != nil {
				eic["MaximumRetryAttempts"] = *f.Retries
			}
			body := mustJSON(eic)
			if _, err := c.do(ctx, "PUT", "/2019-09-25/functions/"+url.PathEscape(name)+"/event-invoke-config", map[string]string{"Content-Type": "application/json"}, body); err != nil {
				return fmt.Errorf("function %q destinations: %w", name, err)
			}
		}

		// Tags merge idempotently.
		if len(f.Tags) > 0 {
			body := mustJSON(map[string]any{"Tags": f.Tags})
			if _, err := c.do(ctx, "POST", "/2017-03-31/tags/"+lambdaARN(name), map[string]string{"Content-Type": "application/json"}, body); err != nil {
				return fmt.Errorf("function %q tags: %w", name, err)
			}
		}

		// Event source mappings: create the missing ones by source ARN and
		// converge an explicit enabled flag on the ones already wired.
		if len(f.Triggers) > 0 {
			existing := map[string]string{} // source ARN → mapping UUID
			if out, err := c.do(ctx, "GET", "/2015-03-31/event-source-mappings?FunctionName="+url.QueryEscape(name), nil, nil); err == nil {
				var lst struct {
					EventSourceMappings []struct {
						UUID           string `json:"UUID"`
						EventSourceArn string `json:"EventSourceArn"`
					} `json:"EventSourceMappings"`
				}
				json.Unmarshal(out, &lst)
				for _, m := range lst.EventSourceMappings {
					existing[m.EventSourceArn] = m.UUID
				}
			}
			for _, tr := range f.Triggers {
				arn := queueARN(tr.Queue)
				if uuid, ok := existing[arn]; ok {
					if tr.Enabled != nil {
						body := mustJSON(map[string]any{"Enabled": *tr.Enabled})
						if _, err := c.do(ctx, "PUT", "/2015-03-31/event-source-mappings/"+uuid, map[string]string{"Content-Type": "application/json"}, body); err != nil {
							return fmt.Errorf("function %q trigger %s: %w", name, tr.Queue, err)
						}
					}
					continue
				}
				in := map[string]any{"FunctionName": name, "EventSourceArn": arn}
				if tr.Batch > 0 {
					in["BatchSize"] = tr.Batch
				}
				if tr.Enabled != nil {
					in["Enabled"] = *tr.Enabled
				}
				if _, err := c.do(ctx, "POST", "/2015-03-31/event-source-mappings", map[string]string{"Content-Type": "application/json"}, mustJSON(in)); err != nil {
					return fmt.Errorf("function %q trigger %s: %w", name, tr.Queue, err)
				}
				rep.add("created", "function/"+name, "trigger "+tr.Queue)
			}
		}
	}
	return nil
}

func (d *Dest) arn() string {
	switch {
	case d.Queue != "":
		return queueARN(d.Queue)
	case d.Topic != "":
		return topicARN(d.Topic)
	case d.Lambda != "":
		return lambdaARN(d.Lambda)
	}
	return ""
}

// ---- topics + subscriptions ----

func applyTopics(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Topics) {
		t := s.Topics[name]
		// CreateTopic is idempotent: same name → same ARN.
		if _, err := c.query(ctx, url.Values{"Action": {"CreateTopic"}, "Name": {name}}); err != nil {
			return fmt.Errorf("topic %q: %w", name, err)
		}
		arn := topicARN(name)

		if len(t.Tags) > 0 {
			v := url.Values{"Action": {"TagResource"}, "ResourceArn": {arn}}
			for i, k := range sortedNames(t.Tags) {
				v.Set(fmt.Sprintf("Tags.member.%d.Key", i+1), k)
				v.Set(fmt.Sprintf("Tags.member.%d.Value", i+1), t.Tags[k])
			}
			if _, err := c.query(ctx, v); err != nil {
				return fmt.Errorf("topic %q tags: %w", name, err)
			}
		}

		// Existing subscriptions, keyed by protocol+endpoint.
		existing := map[string]string{} // key → subscription ARN
		if out, err := c.query(ctx, url.Values{"Action": {"ListSubscriptionsByTopic"}, "TopicArn": {arn}}); err == nil {
			existing = parseSubs(out)
		}

		createdAny := false
		for _, sub := range t.Subscriptions {
			proto, endpoint := sub.wire()
			k := proto + "|" + endpoint
			subARN, ok := existing[k]
			if !ok {
				out, err := c.query(ctx, url.Values{
					"Action": {"Subscribe"}, "TopicArn": {arn},
					"Protocol": {proto}, "Endpoint": {endpoint},
				})
				if err != nil {
					return fmt.Errorf("topic %q subscribe %s: %w", name, endpoint, err)
				}
				subARN = xmlValue(string(out), "SubscriptionArn")
				createdAny = true
				rep.add("created", "topic/"+name, "subscription "+proto+" "+endpoint)
			}
			// Converge subscription attributes (idempotent sets).
			if !sub.Filter.IsZero() && subARN != "" {
				if _, err := c.query(ctx, url.Values{
					"Action": {"SetSubscriptionAttributes"}, "SubscriptionArn": {subARN},
					"AttributeName": {"FilterPolicy"}, "AttributeValue": {sub.Filter.JSON},
				}); err != nil {
					return fmt.Errorf("topic %q filter: %w", name, err)
				}
			}
			if sub.Raw && subARN != "" {
				if _, err := c.query(ctx, url.Values{
					"Action": {"SetSubscriptionAttributes"}, "SubscriptionArn": {subARN},
					"AttributeName": {"RawMessageDelivery"}, "AttributeValue": {"true"},
				}); err != nil {
					return fmt.Errorf("topic %q raw delivery: %w", name, err)
				}
			}
		}
		if !createdAny {
			rep.add("skipped", "topic/"+name, "exists")
		}
	}
	return nil
}

func (sub Subscription) wire() (proto, endpoint string) {
	switch {
	case sub.Queue != "":
		return "sqs", queueARN(sub.Queue)
	case sub.Lambda != "":
		return "lambda", lambdaARN(sub.Lambda)
	default:
		return "http", sub.HTTP
	}
}

// parseSubs pulls protocol/endpoint/arn triples out of the Query-XML response
// without a full XML schema (the shapes are flat and stable).
func parseSubs(xml []byte) map[string]string {
	out := map[string]string{}
	s := string(xml)
	for {
		i := strings.Index(s, "<member>")
		if i < 0 {
			break
		}
		j := strings.Index(s, "</member>")
		if j < 0 {
			break
		}
		m := s[i:j]
		proto := xmlValue(m, "Protocol")
		endpoint := xmlValue(m, "Endpoint")
		arn := xmlValue(m, "SubscriptionArn")
		if proto != "" && endpoint != "" {
			out[proto+"|"+endpoint] = arn
		}
		s = s[j+9:]
	}
	return out
}

func xmlValue(s, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	// XML-encoded payloads (SNS attribute values carry JSON) need their
	// entities restored — &#34;/&quot;/&amp; etc.
	return html.UnescapeString(rest[:j])
}

// ---- rules ----

func applyRules(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Rules) {
		r := s.Rules[name]
		bus := r.Bus
		if bus == "" {
			bus = "default"
		}
		if bus != "default" {
			// Idempotent-ish: create the bus, tolerating "already exists".
			if _, err := c.json11(ctx, "AWSEvents", "CreateEventBus", map[string]any{"Name": bus}); err != nil {
				var ae *apiErr
				if !asAPIErr(err, &ae) || !strings.Contains(ae.body, "Exists") {
					return fmt.Errorf("rule %q bus: %w", name, err)
				}
			}
		}
		in := map[string]any{"Name": name}
		if bus != "default" {
			in["EventBusName"] = bus
		}
		if !r.Pattern.IsZero() {
			in["EventPattern"] = r.Pattern.JSON
		}
		if r.Schedule != "" {
			in["ScheduleExpression"] = r.Schedule
		}
		state := "ENABLED"
		if r.Enabled != nil && !*r.Enabled {
			state = "DISABLED"
		}
		in["State"] = state
		if _, err := c.json11(ctx, "AWSEvents", "PutRule", in); err != nil { // PutRule is an upsert
			return fmt.Errorf("rule %q: %w", name, err)
		}
		if len(r.Targets) > 0 {
			var targets []map[string]any
			for i, t := range r.Targets {
				arn := ""
				switch {
				case t.Queue != "":
					arn = queueARN(t.Queue)
				case t.Topic != "":
					arn = topicARN(t.Topic)
				case t.Lambda != "":
					arn = lambdaARN(t.Lambda)
				}
				tw := map[string]any{"Id": fmt.Sprintf("t%d", i+1), "Arn": arn}
				if !t.Input.IsZero() {
					tw["Input"] = t.Input.JSON
				}
				if t.InputPath != "" {
					tw["InputPath"] = t.InputPath
				}
				if t.Template != "" {
					it := map[string]any{"InputTemplate": t.Template}
					if len(t.Paths) > 0 {
						it["InputPathsMap"] = t.Paths
					}
					tw["InputTransformer"] = it
				}
				targets = append(targets, tw)
			}
			tin := map[string]any{"Rule": name, "Targets": targets}
			if bus != "default" {
				tin["EventBusName"] = bus
			}
			if _, err := c.json11(ctx, "AWSEvents", "PutTargets", tin); err != nil { // upsert by Id
				return fmt.Errorf("rule %q targets: %w", name, err)
			}
		}
		rep.add("updated", "rule/"+bus+"/"+name, "put (upsert)")
	}
	return nil
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

// ---- secrets ----

func applySecrets(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Secrets) {
		sec := s.Secrets[name]
		value := func(in map[string]any) map[string]any {
			if sec.Binary != "" {
				in["SecretBinary"] = sec.Binary // already base64, as the wire wants
			} else {
				in["SecretString"] = sec.Value
			}
			return in
		}
		_, err := c.json11(ctx, "secretsmanager", "DescribeSecret", map[string]any{"SecretId": name})
		switch {
		case err == nil && !sec.Force:
			rep.add("skipped", "secret/"+name, "exists — value untouched (set force: true to overwrite)")
		case err == nil && sec.Force:
			if _, err := c.json11(ctx, "secretsmanager", "PutSecretValue", value(map[string]any{
				"SecretId": name,
			})); err != nil {
				return fmt.Errorf("secret %q: %w", name, err)
			}
			rep.add("updated", "secret/"+name, "value (force)")
		case notFound(err):
			in := value(map[string]any{"Name": name})
			if sec.Description != "" {
				in["Description"] = sec.Description
			}
			if len(sec.Tags) > 0 {
				in["Tags"] = tagList(sec.Tags, "Key", "Value")
			}
			if _, err := c.json11(ctx, "secretsmanager", "CreateSecret", in); err != nil {
				return fmt.Errorf("secret %q: %w", name, err)
			}
			rep.add("created", "secret/"+name, "")
		default:
			return fmt.Errorf("secret %q: %w", name, err)
		}
	}
	return nil
}

// ---- parameters ----

func applyParameters(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Parameters) {
		p := s.Parameters[name]
		typ := p.Type
		if typ == "" {
			typ = "String"
		}
		_, err := c.json11(ctx, "AmazonSSM", "GetParameter", map[string]any{"Name": name})
		switch {
		case err == nil && !p.Force:
			rep.add("skipped", "parameter/"+name, "exists — value untouched (set force: true to overwrite)")
		case err == nil && p.Force:
			in := map[string]any{"Name": name, "Value": p.Value, "Type": typ, "Overwrite": true}
			if p.Description != "" {
				in["Description"] = p.Description
			}
			if _, err := c.json11(ctx, "AmazonSSM", "PutParameter", in); err != nil {
				return fmt.Errorf("parameter %q: %w", name, err)
			}
			rep.add("updated", "parameter/"+name, "value (force)")
		case notFound(err):
			in := map[string]any{"Name": name, "Value": p.Value, "Type": typ}
			if p.Description != "" {
				in["Description"] = p.Description
			}
			if len(p.Tags) > 0 {
				in["Tags"] = tagList(p.Tags, "Key", "Value")
			}
			if _, err := c.json11(ctx, "AmazonSSM", "PutParameter", in); err != nil {
				return fmt.Errorf("parameter %q: %w", name, err)
			}
			rep.add("created", "parameter/"+name, "")
		default:
			return fmt.Errorf("parameter %q: %w", name, err)
		}
	}
	return nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
