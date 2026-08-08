package cloudformation

// Emitting a CloudFormation template from a live stack — the inverse of the
// transpiler, and what `doze-aws export` produces.
//
// This exists because the capability is worth keeping and the old format is
// not: "click a stack together in the console, export it, commit it" is a good
// workflow, but the artifact should be something the rest of the world can
// read. A template exported here deploys to real AWS.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/doze-dev/doze-aws/internal/provision"
)

// Emit renders a resource graph as a CloudFormation template in YAML.
func Emit(s *provision.Stack) ([]byte, error) {
	resources := map[string]any{}

	// Logical IDs must be alphanumeric, so a name like "app/config" or
	// "orders.fifo" is sanitised — and the real name is kept as an explicit
	// property so the round trip is lossless.
	add := func(prefix, name string, typ string, props map[string]any) {
		resources[logicalID(prefix, name)] = map[string]any{
			"Type":       typ,
			"Properties": props,
		}
	}

	for _, name := range sortedNames(s.Queues) {
		q := s.Queues[name]
		props := map[string]any{"QueueName": name}
		putIf(props, "FifoQueue", q.FIFO)
		putIf(props, "ContentBasedDeduplication", q.ContentDedup)
		putIfNum(props, "VisibilityTimeout", q.Visibility)
		putIfNum(props, "DelaySeconds", q.Delay)
		putIfNum(props, "MessageRetentionPeriod", q.Retention)
		putIfNum(props, "ReceiveMessageWaitTimeSeconds", q.ReceiveWait)
		putIfNum(props, "MaximumMessageSize", q.MaxSize)
		if q.DLQ != "" && q.DLQ != "auto" {
			props["RedrivePolicy"] = map[string]any{
				"deadLetterTargetArn": arnSub("sqs", q.DLQ),
				"maxReceiveCount":     orDefaultInt(q.MaxReceives, 3),
			}
		}
		putTags(props, q.Tags)
		add("Queue", name, "AWS::SQS::Queue", props)
	}

	for _, name := range sortedNames(s.Topics) {
		t := s.Topics[name]
		props := map[string]any{"TopicName": name}
		var subs []any
		for _, sub := range t.Subscriptions {
			m := map[string]any{}
			switch {
			case sub.Queue != "":
				m["Protocol"], m["Endpoint"] = "sqs", arnSub("sqs", sub.Queue)
			case sub.Lambda != "":
				m["Protocol"], m["Endpoint"] = "lambda", lambdaArnSub(sub.Lambda)
			case sub.HTTP != "":
				proto := "http"
				if strings.HasPrefix(sub.HTTP, "https") {
					proto = "https"
				}
				m["Protocol"], m["Endpoint"] = proto, sub.HTTP
			default:
				continue
			}
			putIf(m, "RawMessageDelivery", sub.Raw)
			if !sub.Filter.IsZero() {
				m["FilterPolicy"] = rawDoc(sub.Filter)
			}
			subs = append(subs, m)
		}
		if len(subs) > 0 {
			props["Subscription"] = subs
		}
		putTags(props, t.Tags)
		add("Topic", name, "AWS::SNS::Topic", props)
	}

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
		putTags(props, b.Tags)
		add("Bucket", name, "AWS::S3::Bucket", props)
	}

	for _, name := range sortedNames(s.Tables) {
		t := s.Tables[name]
		props := map[string]any{"TableName": name, "BillingMode": "PAY_PER_REQUEST"}
		attrs, keySchema := keyBlocks(t.Key)
		for idxName, gsi := range t.GSIs {
			gAttrs, gKeys := keyBlocks(gsi.Key)
			attrs = mergeAttrs(attrs, gAttrs)
			g := map[string]any{"IndexName": idxName, "KeySchema": gKeys}
			proj := map[string]any{"ProjectionType": orDefault(gsi.Projection, "ALL")}
			if len(gsi.Include) > 0 {
				proj["NonKeyAttributes"] = gsi.Include
			}
			g["Projection"] = proj
			props["GlobalSecondaryIndexes"] = appendAny(props["GlobalSecondaryIndexes"], g)
		}
		props["AttributeDefinitions"] = attrs
		props["KeySchema"] = keySchema
		if t.TTL != "" {
			props["TimeToLiveSpecification"] = map[string]any{"AttributeName": t.TTL, "Enabled": true}
		}
		if t.DeletionProtection != nil && *t.DeletionProtection {
			props["DeletionProtectionEnabled"] = true
		}
		putTags(props, t.Tags)
		add("Table", name, "AWS::DynamoDB::Table", props)
	}

	for _, name := range sortedNames(s.Functions) {
		f := s.Functions[name]
		props := map[string]any{"FunctionName": name}
		putIfStr(props, "Runtime", f.Runtime)
		putIfStr(props, "Handler", f.Handler)
		putIfNum(props, "Timeout", f.Timeout)
		putIfNum(props, "MemorySize", f.Memory)
		if f.Code != "" {
			// The _local_ convention is what makes an exported template
			// redeployable against doze-aws without a build step.
			props["Code"] = map[string]any{"S3Bucket": "_local_", "S3Key": f.Code}
		}
		if len(f.Env) > 0 {
			props["Environment"] = map[string]any{"Variables": f.Env}
		}
		if f.DLQ != nil {
			props["DeadLetterConfig"] = map[string]any{"TargetArn": destARN(f.DLQ)}
		}
		putTags(props, f.Tags)
		add("Function", name, "AWS::Lambda::Function", props)

		for i, trig := range f.Triggers {
			esm := map[string]any{
				"FunctionName":   name,
				"EventSourceArn": arnSub("sqs", trig.Queue),
			}
			putIfNum(esm, "BatchSize", trig.Batch)
			if trig.Enabled != nil {
				esm["Enabled"] = *trig.Enabled
			}
			resources[logicalID("Trigger", fmt.Sprintf("%s%d", name, i+1))] = map[string]any{
				"Type": "AWS::Lambda::EventSourceMapping", "Properties": esm,
			}
		}
	}

	for _, name := range sortedNames(s.Rules) {
		r := s.Rules[name]
		props := map[string]any{"Name": name}
		putIfStr(props, "EventBusName", r.Bus)
		putIfStr(props, "ScheduleExpression", r.Schedule)
		if !r.Pattern.IsZero() {
			props["EventPattern"] = rawDoc(r.Pattern)
		}
		if r.Enabled != nil {
			props["State"] = map[bool]string{true: "ENABLED", false: "DISABLED"}[*r.Enabled]
		}
		var targets []any
		for i, t := range r.Targets {
			m := map[string]any{"Id": fmt.Sprint(i + 1)}
			switch {
			case t.Queue != "":
				m["Arn"] = arnSub("sqs", t.Queue)
			case t.Topic != "":
				m["Arn"] = arnSub("sns", t.Topic)
			case t.Lambda != "":
				m["Arn"] = lambdaArnSub(t.Lambda)
			default:
				continue
			}
			putIfStr(m, "InputPath", t.InputPath)
			if !t.Input.IsZero() {
				m["Input"] = t.Input.JSON
			}
			if t.Template != "" {
				it := map[string]any{"InputTemplate": t.Template}
				if len(t.Paths) > 0 {
					it["InputPathsMap"] = t.Paths
				}
				m["InputTransformer"] = it
			}
			targets = append(targets, m)
		}
		if len(targets) > 0 {
			props["Targets"] = targets
		}
		add("Rule", name, "AWS::Events::Rule", props)
	}

	for _, name := range sortedNames(s.Keys) {
		k := s.Keys[name]
		props := map[string]any{}
		putIfStr(props, "Description", k.Description)
		putIf(props, "EnableKeyRotation", k.Rotation)
		putIfStr(props, "KeySpec", k.Spec)
		putIfStr(props, "KeyUsage", k.Usage)
		putTags(props, k.Tags)
		add("Key", name, "AWS::KMS::Key", props)
		// The alias is what addresses the key, so it must be exported too.
		resources[logicalID("KeyAlias", name)] = map[string]any{
			"Type": "AWS::KMS::Alias",
			"Properties": map[string]any{
				"AliasName":   "alias/" + name,
				"TargetKeyId": map[string]any{"Ref": logicalID("Key", name)},
			},
		}
	}

	for _, name := range sortedNames(s.Secrets) {
		sec := s.Secrets[name]
		props := map[string]any{"Name": name}
		putIfStr(props, "Description", sec.Description)
		// Values are deliberately omitted, as they were in the old export.
		putTags(props, sec.Tags)
		add("Secret", name, "AWS::SecretsManager::Secret", props)
	}

	for _, name := range sortedNames(s.Parameters) {
		p := s.Parameters[name]
		props := map[string]any{
			"Name":  name,
			"Type":  orDefault(p.Type, "String"),
			"Value": p.Value,
		}
		putIfStr(props, "Description", p.Description)
		// SecureString values are not exported, matching the secrets rule.
		if p.Type == "SecureString" {
			props["Value"] = ""
		}
		add("Parameter", name, "AWS::SSM::Parameter", props)
	}

	if len(resources) == 0 {
		return nil, fmt.Errorf("nothing to export: no resources found")
	}
	doc := map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Description":              "Exported from doze-aws",
		"Resources":                resources,
	}
	return marshalYAML(doc)
}

// ---- helpers ----

// logicalID builds a valid CloudFormation logical ID from a resource name.
func logicalID(prefix, name string) string {
	var b strings.Builder
	b.WriteString(prefix)
	upper := true
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if upper && r >= 'a' && r <= 'z' {
				b.WriteRune(r - 32)
			} else {
				b.WriteRune(r)
			}
			upper = false
		default:
			upper = true // the next letter starts a new word
		}
	}
	return b.String()
}

// arnSub emits an Fn::Sub ARN so an exported template is portable to real AWS
// rather than hard-coding the local account and region.
func arnSub(service, name string) map[string]any {
	return map[string]any{"Fn::Sub": fmt.Sprintf("arn:${AWS::Partition}:%s:${AWS::Region}:${AWS::AccountId}:%s", service, name)}
}

func lambdaArnSub(name string) map[string]any {
	return map[string]any{"Fn::Sub": "arn:${AWS::Partition}:lambda:${AWS::Region}:${AWS::AccountId}:function:" + name}
}

func destARN(d *provision.Dest) any {
	switch {
	case d.Topic != "":
		return arnSub("sns", d.Topic)
	case d.Lambda != "":
		return lambdaArnSub(d.Lambda)
	default:
		return arnSub("sqs", d.Queue)
	}
}

// rawDoc turns a stored JSON document back into a structure, so it renders as
// YAML rather than an embedded string.
func rawDoc(d provision.Doc) any {
	var v any
	if json.Unmarshal([]byte(d.JSON), &v) == nil {
		return v
	}
	return d.JSON
}

// keyBlocks turns the "pk:S sk:N" shorthand into CloudFormation's separate
// AttributeDefinitions and KeySchema lists.
func keyBlocks(key string) (attrs []any, keySchema []any) {
	parts := strings.Fields(key)
	roles := []string{"HASH", "RANGE"}
	for i, part := range parts {
		if i >= len(roles) {
			break
		}
		name, typ, _ := strings.Cut(part, ":")
		if typ == "" {
			typ = "S"
		}
		attrs = append(attrs, map[string]any{"AttributeName": name, "AttributeType": strings.ToUpper(typ)})
		keySchema = append(keySchema, map[string]any{"AttributeName": name, "KeyType": roles[i]})
	}
	return attrs, keySchema
}

// mergeAttrs unions attribute definitions, since a GSI key may reuse or add to
// the table's attributes and CloudFormation rejects duplicates.
func mergeAttrs(a, b []any) []any {
	seen := map[string]bool{}
	var out []any
	for _, list := range [][]any{a, b} {
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := fmt.Sprint(m["AttributeName"])
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, item)
		}
	}
	return out
}

func putIf(m map[string]any, key string, v bool) {
	if v {
		m[key] = true
	}
}

func putIfNum(m map[string]any, key string, v int) {
	if v > 0 {
		m[key] = v
	}
}

func putIfStr(m map[string]any, key, v string) {
	if v != "" {
		m[key] = v
	}
}

func putIfList(m map[string]any, key string, v []string) {
	if len(v) > 0 {
		m[key] = v
	}
}

func putTags(m map[string]any, tags map[string]string) {
	if len(tags) == 0 {
		return
	}
	var out []any
	for _, k := range sortedNames(tags) {
		out = append(out, map[string]any{"Key": k, "Value": tags[k]})
	}
	m["Tags"] = out
}

func appendAny(existing any, item any) []any {
	list, _ := existing.([]any)
	return append(list, item)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func sortedNames[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// marshalYAML renders the template with a stable two-space indent.
func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := newYAMLEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// newYAMLEncoder builds the encoder Emit renders through.
func newYAMLEncoder(w *bytes.Buffer) *yaml.Encoder {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	return enc
}
