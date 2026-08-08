package cloudformation

// Per-type property mapping: CloudFormation properties → stackfile IR fields.
//
// Only properties that change observable local behaviour are carried across.
// A cloud-only knob (provisioned throughput, KMS master keys, replication) is
// dropped rather than pretended at, matching the stack file's own rule that
// every field it accepts does something.

import (
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/provision"
)

// mapper accumulates the stack while walking resources, deferring the
// attachments that can only be wired once every resource exists.
type mapper struct {
	scope    *Scope
	stack    *provision.Stack
	template *Template
	deferred []func() error
}

func (m *mapper) apply(r *Resource, name string, props map[string]any) error {
	switch r.Type {
	case "AWS::SQS::Queue":
		return m.queue(name, props)
	case "AWS::SNS::Topic":
		return m.topic(name, props)
	case "AWS::SNS::Subscription":
		return m.subscription(props)
	case "AWS::S3::Bucket":
		return m.bucket(name, props)
	case "AWS::DynamoDB::Table", "AWS::DynamoDB::GlobalTable":
		return m.table(name, props)
	case "AWS::Serverless::SimpleTable":
		return m.simpleTable(name, props)
	case "AWS::Lambda::Function", "AWS::Serverless::Function":
		return m.function(name, props)
	case "AWS::Lambda::EventSourceMapping":
		return m.eventSourceMapping(props)
	case "AWS::Events::Rule":
		return m.rule(name, props)
	case "AWS::KMS::Key":
		return m.key(name, props)
	case "AWS::KMS::Alias":
		return m.keyAlias(props)
	case "AWS::SecretsManager::Secret":
		return m.secret(name, props)
	case "AWS::SSM::Parameter":
		return m.parameter(name, props)
	case "AWS::Kinesis::Stream":
		// Streams have no stack-file section yet; the resource is accepted and
		// reported so a template referencing one still transpiles.
		return nil
	case "AWS::Serverless::Api", "AWS::Serverless::HttpApi",
		"AWS::ApiGateway::RestApi", "AWS::ApiGatewayV2::Api":
		// The API itself carries no state beyond its name; its routes arrive
		// from the functions that bind to it.
		if _, ok := m.stack.APIs[name]; !ok {
			m.stack.APIs[name] = provision.API{Stage: propStr(props, "StageName")}
		}
		return nil
	case "AWS::ApiGateway::Deployment", "AWS::ApiGateway::Stage",
		"AWS::ApiGateway::Resource", "AWS::ApiGateway::Method",
		"AWS::ApiGateway::Account":
		// Recognised; the resource tree is rebuilt from routes at apply time.
		return nil
	case "AWS::Lambda::Permission", "AWS::S3::BucketPolicy",
		"AWS::SQS::QueuePolicy", "AWS::SNS::TopicPolicy",
		"AWS::Events::EventBus", "AWS::Lambda::Version",
		"AWS::Lambda::Alias", "AWS::Lambda::Url", "AWS::Lambda::LayerVersion":
		// Recognised and namable — they carry no stack-file state of their own.
		return nil
	}
	return fmt.Errorf("no mapping registered")
}

func (m *mapper) applyDeferred() error {
	for _, fn := range m.deferred {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

// ---- SQS ----

func (m *mapper) queue(name string, props map[string]any) error {
	q := provision.Queue{
		FIFO:         propBool(props, "FifoQueue"),
		ContentDedup: propBool(props, "ContentBasedDeduplication"),
		Visibility:   propInt(props, "VisibilityTimeout"),
		Delay:        propInt(props, "DelaySeconds"),
		Retention:    propInt(props, "MessageRetentionPeriod"),
		ReceiveWait:  propInt(props, "ReceiveMessageWaitTimeSeconds"),
		MaxSize:      propInt(props, "MaximumMessageSize"),
		Tags:         propTags(props),
	}
	// RedrivePolicy names the DLQ by ARN and carries maxReceiveCount.
	if rd := propMap(props, "RedrivePolicy"); rd != nil {
		q.DLQ = nameFromARN(propStr(rd, "deadLetterTargetArn"))
		q.MaxReceives = propInt(rd, "maxReceiveCount")
	}
	m.stack.Queues[name] = q
	return nil
}

// ---- SNS ----

func (m *mapper) topic(name string, props map[string]any) error {
	t := provision.Topic{Tags: propTags(props)}
	// Inline subscriptions on the topic itself.
	for _, item := range propList(props, "Subscription") {
		sm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sub, err := subscriptionFrom(sm)
		if err != nil {
			return err
		}
		t.Subscriptions = append(t.Subscriptions, sub)
	}
	m.stack.Topics[name] = t
	return nil
}

// subscription handles a standalone AWS::SNS::Subscription, which attaches to
// a topic that may be declared after it — hence the deferral.
func (m *mapper) subscription(props map[string]any) error {
	topic := nameFromARN(propStr(props, "TopicArn"))
	if topic == "" {
		return fmt.Errorf("TopicArn is required")
	}
	sub, err := subscriptionFrom(props)
	if err != nil {
		return err
	}
	m.deferred = append(m.deferred, func() error {
		t, ok := m.stack.Topics[topic]
		if !ok {
			return fmt.Errorf("subscription references unknown topic %q", topic)
		}
		t.Subscriptions = append(t.Subscriptions, sub)
		m.stack.Topics[topic] = t
		return nil
	})
	return nil
}

func subscriptionFrom(props map[string]any) (provision.Subscription, error) {
	var sub provision.Subscription
	endpoint := propStr(props, "Endpoint")
	switch strings.ToLower(propStr(props, "Protocol")) {
	case "sqs":
		sub.Queue = nameFromARN(endpoint)
	case "lambda":
		sub.Lambda = nameFromARN(endpoint)
	case "http", "https":
		sub.HTTP = endpoint
	case "":
		return sub, fmt.Errorf("subscription Protocol is required")
	default:
		return sub, fmt.Errorf("doze-aws supports sqs, lambda and http(s) subscriptions, not %q",
			propStr(props, "Protocol"))
	}
	sub.Raw = propBool(props, "RawMessageDelivery")
	if fp, ok := props["FilterPolicy"]; ok && fp != nil {
		doc, err := docOf(fp)
		if err != nil {
			return sub, err
		}
		sub.Filter = doc
	}
	return sub, nil
}

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
	m.stack.Buckets[name] = b
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

// ---- DynamoDB ----

func (m *mapper) table(name string, props map[string]any) error {
	key, err := keySchemaOf(props)
	if err != nil {
		return err
	}
	t := provision.Table{Key: key, Tags: propTags(props)}
	if ttl := propMap(props, "TimeToLiveSpecification"); ttl != nil && propBool(ttl, "Enabled") {
		t.TTL = propStr(ttl, "AttributeName")
	}
	if propBool(props, "DeletionProtectionEnabled") {
		enabled := true
		t.DeletionProtection = &enabled
	}
	for _, item := range propList(props, "GlobalSecondaryIndexes") {
		gsi, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idxKey, err := keySchemaOf(gsi)
		if err != nil {
			return fmt.Errorf("GSI %s: %w", propStr(gsi, "IndexName"), err)
		}
		if t.GSIs == nil {
			t.GSIs = map[string]provision.GSI{}
		}
		entry := provision.GSI{Key: idxKey}
		if proj := propMap(gsi, "Projection"); proj != nil {
			entry.Projection = propStr(proj, "ProjectionType")
			entry.Include = strList(propList(proj, "NonKeyAttributes"))
		}
		t.GSIs[propStr(gsi, "IndexName")] = entry
	}
	for _, item := range propList(props, "LocalSecondaryIndexes") {
		lsi, ok := item.(map[string]any)
		if !ok {
			continue
		}
		// An LSI declares the table's partition key plus its own sort key; the
		// stack file wants only the sort key.
		sortKey, err := lsiSortKey(lsi)
		if err != nil {
			return fmt.Errorf("LSI %s: %w", propStr(lsi, "IndexName"), err)
		}
		if t.LSIs == nil {
			t.LSIs = map[string]provision.LSI{}
		}
		entry := provision.LSI{Key: sortKey}
		if proj := propMap(lsi, "Projection"); proj != nil {
			entry.Projection = propStr(proj, "ProjectionType")
			entry.Include = strList(propList(proj, "NonKeyAttributes"))
		}
		t.LSIs[propStr(lsi, "IndexName")] = entry
	}
	m.stack.Tables[name] = t
	return nil
}

// simpleTable maps SAM's AWS::Serverless::SimpleTable, which declares only a
// primary key.
func (m *mapper) simpleTable(name string, props map[string]any) error {
	key := "id:S"
	if pk := propMap(props, "PrimaryKey"); pk != nil {
		attr := propStr(pk, "Name")
		typ := propStr(pk, "Type")
		if attr != "" {
			key = attr + ":" + shortAttrType(typ)
		}
	}
	m.stack.Tables[name] = provision.Table{Key: key, Tags: propTags(props)}
	return nil
}

// keySchemaOf builds the stack file's "pk:TYPE sk:TYPE" shorthand from a
// CloudFormation KeySchema plus AttributeDefinitions.
func keySchemaOf(props map[string]any) (string, error) {
	types := map[string]string{}
	for _, item := range propList(props, "AttributeDefinitions") {
		def, ok := item.(map[string]any)
		if !ok {
			continue
		}
		types[propStr(def, "AttributeName")] = propStr(def, "AttributeType")
	}
	var hash, rangeKey string
	for _, item := range propList(props, "KeySchema") {
		el, ok := item.(map[string]any)
		if !ok {
			continue
		}
		attr := propStr(el, "AttributeName")
		typ := types[attr]
		if typ == "" {
			typ = "S" // GSIs inherit definitions from the table
		}
		switch strings.ToUpper(propStr(el, "KeyType")) {
		case "HASH":
			hash = attr + ":" + typ
		case "RANGE":
			rangeKey = attr + ":" + typ
		}
	}
	if hash == "" {
		return "", fmt.Errorf("KeySchema has no HASH key")
	}
	if rangeKey != "" {
		return hash + " " + rangeKey, nil
	}
	return hash, nil
}

func lsiSortKey(lsi map[string]any) (string, error) {
	for _, item := range propList(lsi, "KeySchema") {
		el, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(propStr(el, "KeyType"), "RANGE") {
			return propStr(el, "AttributeName") + ":S", nil
		}
	}
	return "", fmt.Errorf("KeySchema has no RANGE key")
}

func shortAttrType(t string) string {
	switch strings.ToLower(t) {
	case "number":
		return "N"
	case "binary":
		return "B"
	default:
		return "S"
	}
}

// ---- Lambda ----

func (m *mapper) function(name string, props map[string]any) error {
	f := provision.Function{
		Runtime: propStr(props, "Runtime"),
		Handler: propStr(props, "Handler"),
		Timeout: propInt(props, "Timeout"),
		Memory:  propInt(props, "MemorySize"),
		Tags:    propTags(props),
	}
	// SAM spells tags as a map rather than a Key/Value list.
	if f.Tags == nil {
		if tm := propMap(props, "Tags"); tm != nil {
			f.Tags = map[string]string{}
			for k, v := range tm {
				f.Tags[k] = fmt.Sprint(v)
			}
		}
	}

	// Code location: the _local_ convention is the only one that can work
	// locally, since there is no build step here.
	if code := propMap(props, "Code"); code != nil {
		f.Code = firstNonEmpty(propStr(code, "S3Key"), propStr(code, "ImageUri"))
	}
	if uri := props["CodeUri"]; uri != nil { // SAM
		f.Code = fmt.Sprint(uri)
	}

	if env := propMap(props, "Environment"); env != nil {
		if vars := propMap(env, "Variables"); vars != nil {
			f.Env = map[string]string{}
			for k, v := range vars {
				f.Env[k] = fmt.Sprint(v)
			}
		}
	}
	if dlq := propMap(props, "DeadLetterConfig"); dlq != nil {
		target := nameFromARN(propStr(dlq, "TargetArn"))
		if target == "" {
			target = nameFromARN(propStr(dlq, "Arn"))
		}
		if target != "" {
			f.DLQ = destFor(propStr(dlq, "TargetArn")+propStr(dlq, "Arn"), target)
		}
	}
	m.stack.Functions[name] = f

	// SAM Events become triggers and rules once everything exists.
	if events := propMap(props, "Events"); events != nil {
		m.deferred = append(m.deferred, func() error { return m.samEvents(name, events) })
	}
	return nil
}

// destFor picks the destination kind from an ARN's service segment.
func destFor(arn, name string) *provision.Dest {
	switch {
	case strings.Contains(arn, ":sns:"):
		return &provision.Dest{Topic: name}
	case strings.Contains(arn, ":lambda:"):
		return &provision.Dest{Lambda: name}
	default:
		return &provision.Dest{Queue: name}
	}
}

func (m *mapper) eventSourceMapping(props map[string]any) error {
	fn := nameFromARN(propStr(props, "FunctionName"))
	source := propStr(props, "EventSourceArn")
	if fn == "" || source == "" {
		return fmt.Errorf("FunctionName and EventSourceArn are both required")
	}
	// The stack file models SQS triggers; DynamoDB and Kinesis sources are
	// wired through the Lambda API directly and have no stack-file field, so
	// they are accepted without one rather than silently dropped from a queue
	// trigger list they do not belong in.
	if !strings.Contains(source, ":sqs:") {
		return nil
	}
	queue := nameFromARN(source)
	batch := propInt(props, "BatchSize")
	enabled := true
	if v, ok := props["Enabled"]; ok {
		enabled = propBool(map[string]any{"e": v}, "e")
	}
	m.deferred = append(m.deferred, func() error {
		f, ok := m.stack.Functions[fn]
		if !ok {
			return fmt.Errorf("event source mapping references unknown function %q", fn)
		}
		f.Triggers = append(f.Triggers, provision.Trigger{
			Queue: queue, Batch: batch, Enabled: &enabled,
		})
		m.stack.Functions[fn] = f
		return nil
	})
	return nil
}

// samEvents expands a SAM function's Events block. Only the event types
// doze-aws can actually deliver are handled; anything else is an error rather
// than a function that silently never fires.
func (m *mapper) samEvents(fn string, events map[string]any) error {
	for _, evName := range sortedAnyKeys(events) {
		ev, ok := events[evName].(map[string]any)
		if !ok {
			continue
		}
		props := propMap(ev, "Properties")
		if props == nil {
			props = map[string]any{}
		}
		switch propStr(ev, "Type") {
		case "SQS":
			queue := nameFromARN(propStr(props, "Queue"))
			f := m.stack.Functions[fn]
			f.Triggers = append(f.Triggers, provision.Trigger{
				Queue: queue, Batch: propInt(props, "BatchSize"),
			})
			m.stack.Functions[fn] = f
		case "SNS":
			topic := nameFromARN(propStr(props, "Topic"))
			t, ok := m.stack.Topics[topic]
			if !ok {
				return fmt.Errorf("SAM event %s references unknown topic %q", evName, topic)
			}
			t.Subscriptions = append(t.Subscriptions, provision.Subscription{Lambda: fn})
			m.stack.Topics[topic] = t
		case "Schedule", "ScheduleV2":
			rule := provision.Rule{
				Schedule: propStr(props, "Schedule"),
				Targets:  []provision.Target{{Lambda: fn}},
			}
			m.stack.Rules[firstNonEmpty(propStr(props, "Name"), fn+"-"+evName)] = rule
		case "EventBridgeRule", "CloudWatchEvent":
			pattern, err := docOf(props["Pattern"])
			if err != nil {
				return err
			}
			m.stack.Rules[firstNonEmpty(propStr(props, "RuleName"), fn+"-"+evName)] = provision.Rule{
				Bus:     nameFromARN(propStr(props, "EventBusName")),
				Pattern: pattern,
				Targets: []provision.Target{{Lambda: fn}},
			}
		case "Api", "HttpApi":
			// An Api event binds one method+path to the function. The API it
			// belongs to is named by RestApiId when the template declares one,
			// and otherwise by SAM's implicit-API convention.
			apiName := firstNonEmpty(nameFromARN(propStr(props, "RestApiId")),
				nameFromARN(propStr(props, "ApiId")), "ServerlessRestApi")
			method := strings.ToUpper(propStr(props, "Method"))
			if method == "" || method == "ANY" {
				method = "ANY"
			}
			path := propStr(props, "Path")
			if path == "" {
				return fmt.Errorf("SAM event %s: Path is required", evName)
			}
			api := m.stack.APIs[apiName]
			if api.Stage == "" {
				api.Stage = "Prod" // SAM's default implicit stage
			}
			api.Routes = append(api.Routes, provision.Route{
				Method: method, Path: path, Lambda: fn,
			})
			m.stack.APIs[apiName] = api
		default:
			return fmt.Errorf("SAM event %s has unsupported type %q", evName, propStr(ev, "Type"))
		}
	}
	return nil
}

// ---- EventBridge ----

func (m *mapper) rule(name string, props map[string]any) error {
	pattern, err := docOf(props["EventPattern"])
	if err != nil {
		return err
	}
	r := provision.Rule{
		Bus:      nameFromARN(propStr(props, "EventBusName")),
		Pattern:  pattern,
		Schedule: propStr(props, "ScheduleExpression"),
	}
	if state := propStr(props, "State"); state != "" {
		enabled := strings.EqualFold(state, "ENABLED")
		r.Enabled = &enabled
	}
	for _, item := range propList(props, "Targets") {
		tgt, ok := item.(map[string]any)
		if !ok {
			continue
		}
		arn := propStr(tgt, "Arn")
		t := provision.Target{}
		switch {
		case strings.Contains(arn, ":sqs:"):
			t.Queue = nameFromARN(arn)
		case strings.Contains(arn, ":sns:"):
			t.Topic = nameFromARN(arn)
		case strings.Contains(arn, ":lambda:"):
			t.Lambda = nameFromARN(arn)
		default:
			return fmt.Errorf("rule target %q: doze-aws delivers to SQS, SNS and Lambda only", arn)
		}
		t.InputPath = propStr(tgt, "InputPath")
		if raw, ok := tgt["Input"]; ok && raw != nil {
			doc, err := docOf(raw)
			if err != nil {
				return err
			}
			t.Input = doc
		}
		if it := propMap(tgt, "InputTransformer"); it != nil {
			t.Template = propStr(it, "InputTemplate")
			if paths := propMap(it, "InputPathsMap"); paths != nil {
				t.Paths = map[string]string{}
				for k, v := range paths {
					t.Paths[k] = fmt.Sprint(v)
				}
			}
		}
		r.Targets = append(r.Targets, t)
	}
	m.stack.Rules[name] = r
	return nil
}

// ---- KMS ----

func (m *mapper) key(name string, props map[string]any) error {
	k := provision.Key{
		Description: propStr(props, "Description"),
		Rotation:    propBool(props, "EnableKeyRotation"),
		Spec:        propStr(props, "KeySpec"),
		Usage:       propStr(props, "KeyUsage"),
		Tags:        propTags(props),
	}
	m.stack.Keys[name] = k
	return nil
}

// keyAlias renames the key its TargetKeyId points at, since the stack file
// addresses keys by alias rather than by generated id.
func (m *mapper) keyAlias(props map[string]any) error {
	alias := strings.TrimPrefix(propStr(props, "AliasName"), "alias/")
	target := propStr(props, "TargetKeyId")
	if alias == "" || target == "" {
		return nil
	}
	m.deferred = append(m.deferred, func() error {
		if k, ok := m.stack.Keys[target]; ok {
			delete(m.stack.Keys, target)
			m.stack.Keys[alias] = k
		}
		return nil
	})
	return nil
}

// ---- Secrets Manager / SSM ----

func (m *mapper) secret(name string, props map[string]any) error {
	s := provision.Secret{
		Description: propStr(props, "Description"),
		Value:       propStr(props, "SecretString"),
		Tags:        propTags(props),
	}
	// GenerateSecretString produces a value AWS invents; locally a placeholder
	// is honest and keeps the secret present for code that reads it.
	if gen := propMap(props, "GenerateSecretString"); gen != nil && s.Value == "" {
		if tmpl := propStr(gen, "SecretStringTemplate"); tmpl != "" {
			s.Value = tmpl
		} else {
			s.Value = "{}"
		}
	}
	m.stack.Secrets[name] = s
	return nil
}

func (m *mapper) parameter(name string, props map[string]any) error {
	m.stack.Parameters[name] = provision.Parameter{
		Value:       propStr(props, "Value"),
		Type:        propStr(props, "Type"),
		Description: propStr(props, "Description"),
	}
	return nil
}

// ---- helpers ----

func strList(items []any) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
