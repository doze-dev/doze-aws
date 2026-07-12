package stackfile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Export reads the running stack and renders it as a Stack — the inverse of
// Apply, so a team can click a stack together in the console and commit the
// file. Secret and SecureString values are deliberately NOT exported; the
// header comment in Marshal explains the blank.
func Export(ctx context.Context, gateway http.Handler) (*Stack, error) {
	c := newClient(gateway)
	s := &Stack{}

	if err := exportQueues(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportTables(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportBuckets(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportFunctions(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportTopics(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportRules(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportKeys(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportSecrets(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportParameters(ctx, c, s); err != nil {
		return nil, err
	}
	return s, nil
}

func exportQueues(ctx context.Context, c *client, s *Stack) error {
	out, err := c.sqs(ctx, "ListQueues", map[string]any{})
	if err != nil {
		return err
	}
	var lst struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	json.Unmarshal(out, &lst)
	if len(lst.QueueUrls) > 0 {
		s.Queues = map[string]Queue{}
	}
	for _, u := range lst.QueueUrls {
		name := u[strings.LastIndex(u, "/")+1:]
		q := Queue{FIFO: strings.HasSuffix(name, ".fifo")}
		if out, err := c.sqs(ctx, "GetQueueAttributes", map[string]any{
			"QueueUrl": queueURL(name), "AttributeNames": []string{"All"},
		}); err == nil {
			var ga struct {
				Attributes map[string]string `json:"Attributes"`
			}
			json.Unmarshal(out, &ga)
			a := ga.Attributes
			q.ContentDedup = a["ContentBasedDeduplication"] == "true"
			q.Visibility = atoiDefault(a["VisibilityTimeout"], 0)
			if q.Visibility == 30 {
				q.Visibility = 0 // drop defaults so exports stay minimal
			}
			q.Delay = atoiDefault(a["DelaySeconds"], 0)
			if ret := atoiDefault(a["MessageRetentionPeriod"], 0); ret != 0 && ret != 345600 {
				q.Retention = ret
			}
			q.ReceiveWait = atoiDefault(a["ReceiveMessageWaitTimeSeconds"], 0)
			if ms := atoiDefault(a["MaximumMessageSize"], 0); ms != 0 && ms != 262144 {
				q.MaxSize = ms
			}
			if rp := a["RedrivePolicy"]; rp != "" {
				var pol struct {
					DeadLetterTargetArn string          `json:"deadLetterTargetArn"`
					MaxReceiveCount     json.RawMessage `json:"maxReceiveCount"`
				}
				if json.Unmarshal([]byte(rp), &pol) == nil {
					q.DLQ = arnLeaf(pol.DeadLetterTargetArn)
					q.MaxReceives = atoiDefault(strings.Trim(string(pol.MaxReceiveCount), `"`), 0)
					if q.MaxReceives == 3 {
						q.MaxReceives = 0
					}
				}
			}
		}
		if out, err := c.sqs(ctx, "ListQueueTags", map[string]any{"QueueUrl": queueURL(name)}); err == nil {
			var lt struct {
				Tags map[string]string `json:"Tags"`
			}
			json.Unmarshal(out, &lt)
			if len(lt.Tags) > 0 {
				q.Tags = lt.Tags
			}
		}
		s.Queues[name] = q
	}
	return nil
}

func exportTables(ctx context.Context, c *client, s *Stack) error {
	out, err := c.ddb(ctx, "ListTables", map[string]any{})
	if err != nil {
		return err
	}
	var lst struct {
		TableNames []string `json:"TableNames"`
	}
	json.Unmarshal(out, &lst)
	if len(lst.TableNames) > 0 {
		s.Tables = map[string]Table{}
	}
	for _, name := range lst.TableNames {
		out, err := c.ddb(ctx, "DescribeTable", map[string]any{"TableName": name})
		if err != nil {
			continue
		}
		type indexWire struct {
			IndexName string
			KeySchema []struct {
				AttributeName, KeyType string
			}
			Projection struct {
				ProjectionType   string
				NonKeyAttributes []string
			}
		}
		var d struct {
			Table struct {
				KeySchema []struct {
					AttributeName, KeyType string
				}
				AttributeDefinitions []struct {
					AttributeName, AttributeType string
				}
				GlobalSecondaryIndexes    []indexWire
				LocalSecondaryIndexes     []indexWire
				DeletionProtectionEnabled bool
			}
		}
		json.Unmarshal(out, &d)
		types := map[string]string{}
		for _, ad := range d.Table.AttributeDefinitions {
			types[ad.AttributeName] = ad.AttributeType
		}
		keyOf := func(schema []struct{ AttributeName, KeyType string }) string {
			var hash, rng string
			for _, ks := range schema {
				part := ks.AttributeName + ":" + types[ks.AttributeName]
				if ks.KeyType == "HASH" {
					hash = part
				} else {
					rng = part
				}
			}
			if rng != "" {
				return hash + " " + rng
			}
			return hash
		}
		projOf := func(ix indexWire) (string, []string) {
			if p := ix.Projection.ProjectionType; p != "" && p != "ALL" {
				return p, ix.Projection.NonKeyAttributes
			}
			return "", nil
		}
		t := Table{Key: keyOf(d.Table.KeySchema)}
		for _, g := range d.Table.GlobalSecondaryIndexes {
			if t.GSIs == nil {
				t.GSIs = map[string]GSI{}
			}
			proj, incl := projOf(g)
			t.GSIs[g.IndexName] = GSI{Key: keyOf(g.KeySchema), Projection: proj, Include: incl}
		}
		for _, l := range d.Table.LocalSecondaryIndexes {
			if t.LSIs == nil {
				t.LSIs = map[string]LSI{}
			}
			sortKey := ""
			for _, ks := range l.KeySchema {
				if ks.KeyType == "RANGE" {
					sortKey = ks.AttributeName + ":" + types[ks.AttributeName]
				}
			}
			proj, incl := projOf(l)
			t.LSIs[l.IndexName] = LSI{Key: sortKey, Projection: proj, Include: incl}
		}
		if d.Table.DeletionProtectionEnabled {
			v := true
			t.DeletionProtection = &v
		}
		if out, err := c.ddb(ctx, "ListTagsOfResource", map[string]any{"ResourceArn": tableARN(name)}); err == nil {
			var lt struct {
				Tags []struct{ Key, Value string }
			}
			json.Unmarshal(out, &lt)
			for _, tag := range lt.Tags {
				if t.Tags == nil {
					t.Tags = map[string]string{}
				}
				t.Tags[tag.Key] = tag.Value
			}
		}
		if out, err := c.ddb(ctx, "DescribeTimeToLive", map[string]any{"TableName": name}); err == nil {
			var ttl struct {
				TimeToLiveDescription struct {
					AttributeName, TimeToLiveStatus string
				}
			}
			json.Unmarshal(out, &ttl)
			if ttl.TimeToLiveDescription.TimeToLiveStatus == "ENABLED" {
				t.TTL = ttl.TimeToLiveDescription.AttributeName
			}
		}
		s.Tables[name] = t
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

func exportFunctions(ctx context.Context, c *client, s *Stack) error {
	out, err := c.do(ctx, "GET", "/2015-03-31/functions", nil, nil)
	if err != nil {
		return err
	}
	var lst struct {
		Functions []struct {
			FunctionName string
			Runtime      string
			Handler      string
			Timeout      int
			MemorySize   int
			Environment  struct {
				Variables map[string]string
			}
			DeadLetterConfig struct {
				TargetArn string
			}
		}
	}
	json.Unmarshal(out, &lst)
	if len(lst.Functions) > 0 {
		s.Functions = map[string]Function{}
	}
	for _, fn := range lst.Functions {
		f := Function{
			Runtime: fn.Runtime, Handler: fn.Handler,
			Env:  fn.Environment.Variables,
			Code: "<local code path — set me>", // the wire doesn't echo local dirs
		}
		if fn.Timeout != 0 && fn.Timeout != 3 {
			f.Timeout = fn.Timeout
		}
		if fn.MemorySize != 0 && fn.MemorySize != 512 { // 512 is the server default
			f.Memory = fn.MemorySize
		}
		f.DLQ = destFromARN(fn.DeadLetterConfig.TargetArn)
		// Code location: the _local_ extension reports it via GetFunction.
		if out, err := c.do(ctx, "GET", "/2015-03-31/functions/"+url.PathEscape(fn.FunctionName), nil, nil); err == nil {
			var g struct {
				Code struct {
					Location string
				}
			}
			json.Unmarshal(out, &g)
			if loc := strings.TrimPrefix(g.Code.Location, "local://"); loc != g.Code.Location {
				f.Code = loc
			}
		}
		// Destinations + retry policy.
		if out, err := c.do(ctx, "GET", "/2019-09-25/functions/"+url.PathEscape(fn.FunctionName)+"/event-invoke-config", nil, nil); err == nil {
			var eic struct {
				DestinationConfig struct {
					OnSuccess, OnFailure struct{ Destination string }
				}
				MaximumRetryAttempts *int
			}
			json.Unmarshal(out, &eic)
			f.OnSuccess = destFromARN(eic.DestinationConfig.OnSuccess.Destination)
			f.OnFailure = destFromARN(eic.DestinationConfig.OnFailure.Destination)
			if eic.MaximumRetryAttempts != nil && *eic.MaximumRetryAttempts != 2 { // 2 is the default
				f.Retries = eic.MaximumRetryAttempts
			}
		}
		// Triggers.
		if out, err := c.do(ctx, "GET", "/2015-03-31/event-source-mappings?FunctionName="+url.QueryEscape(fn.FunctionName), nil, nil); err == nil {
			var esm struct {
				EventSourceMappings []struct {
					EventSourceArn string
					BatchSize      int
					State          string
				}
			}
			json.Unmarshal(out, &esm)
			for _, m := range esm.EventSourceMappings {
				tr := Trigger{Queue: arnLeaf(m.EventSourceArn)}
				if m.BatchSize != 0 && m.BatchSize != 10 {
					tr.Batch = m.BatchSize
				}
				if m.State == "Disabled" {
					v := false
					tr.Enabled = &v
				}
				f.Triggers = append(f.Triggers, tr)
			}
		}
		// Tags.
		if out, err := c.do(ctx, "GET", "/2017-03-31/tags/"+lambdaARN(fn.FunctionName), nil, nil); err == nil {
			var lt struct {
				Tags map[string]string
			}
			json.Unmarshal(out, &lt)
			if len(lt.Tags) > 0 {
				f.Tags = lt.Tags
			}
		}
		s.Functions[fn.FunctionName] = f
	}
	return nil
}

func destFromARN(arn string) *Dest {
	if arn == "" {
		return nil
	}
	leaf := arnLeaf(arn)
	switch {
	case strings.Contains(arn, ":sqs:"):
		return &Dest{Queue: leaf}
	case strings.Contains(arn, ":sns:"):
		return &Dest{Topic: leaf}
	case strings.Contains(arn, ":lambda:"):
		return &Dest{Lambda: strings.TrimPrefix(leaf, "function:")}
	}
	return nil
}

func exportTopics(ctx context.Context, c *client, s *Stack) error {
	out, err := c.query(ctx, url.Values{"Action": {"ListTopics"}})
	if err != nil {
		return err
	}
	arns := xmlValues(string(out), "TopicArn")
	if len(arns) > 0 {
		s.Topics = map[string]Topic{}
	}
	for _, arn := range arns {
		name := arnLeaf(arn)
		t := Topic{}
		if out, err := c.query(ctx, url.Values{"Action": {"ListSubscriptionsByTopic"}, "TopicArn": {arn}}); err == nil {
			x := string(out)
			for {
				i := strings.Index(x, "<member>")
				if i < 0 {
					break
				}
				j := strings.Index(x, "</member>")
				if j < 0 {
					break
				}
				m := x[i:j]
				sub := Subscription{}
				endpoint := xmlValue(m, "Endpoint")
				switch xmlValue(m, "Protocol") {
				case "sqs":
					sub.Queue = arnLeaf(endpoint)
				case "lambda":
					sub.Lambda = strings.TrimPrefix(arnLeaf(endpoint), "function:")
				default:
					sub.HTTP = endpoint
				}
				// Filter policy / raw delivery, if set.
				if subARN := xmlValue(m, "SubscriptionArn"); subARN != "" {
					if out, err := c.query(ctx, url.Values{
						"Action": {"GetSubscriptionAttributes"}, "SubscriptionArn": {subARN},
					}); err == nil {
						attrs := parseAttrEntries(string(out))
						if fp := attrs["FilterPolicy"]; fp != "" {
							sub.Filter = Doc{JSON: fp}
						}
						sub.Raw = attrs["RawMessageDelivery"] == "true"
					}
				}
				t.Subscriptions = append(t.Subscriptions, sub)
				x = x[j+9:]
			}
		}
		if out, err := c.query(ctx, url.Values{"Action": {"ListTagsForResource"}, "ResourceArn": {arn}}); err == nil {
			for _, m := range xmlBlocks(string(out), "member") {
				k, v := xmlValue(m, "Key"), xmlValue(m, "Value")
				if k == "" {
					continue
				}
				if t.Tags == nil {
					t.Tags = map[string]string{}
				}
				t.Tags[k] = v
			}
		}
		s.Topics[name] = t
	}
	return nil
}

// parseAttrEntries reads SNS's <entry><key>K</key><value>V</value></entry> maps.
func parseAttrEntries(x string) map[string]string {
	out := map[string]string{}
	for {
		i := strings.Index(x, "<entry>")
		if i < 0 {
			break
		}
		j := strings.Index(x, "</entry>")
		if j < 0 {
			break
		}
		e := x[i:j]
		out[xmlValue(e, "key")] = xmlValue(e, "value")
		x = x[j+8:]
	}
	return out
}

func exportRules(ctx context.Context, c *client, s *Stack) error {
	busOut, err := c.json11(ctx, "AWSEvents", "ListEventBuses", map[string]any{})
	if err != nil {
		return err
	}
	var buses struct {
		EventBuses []struct{ Name string }
	}
	json.Unmarshal(busOut, &buses)
	for _, bus := range buses.EventBuses {
		in := map[string]any{}
		if bus.Name != "default" {
			in["EventBusName"] = bus.Name
		}
		out, err := c.json11(ctx, "AWSEvents", "ListRules", in)
		if err != nil {
			continue
		}
		var rules struct {
			Rules []struct {
				Name               string
				EventPattern       string
				ScheduleExpression string
				State              string
			}
		}
		json.Unmarshal(out, &rules)
		for _, r := range rules.Rules {
			if s.Rules == nil {
				s.Rules = map[string]Rule{}
			}
			rule := Rule{Schedule: r.ScheduleExpression}
			if bus.Name != "default" {
				rule.Bus = bus.Name
			}
			if r.EventPattern != "" {
				rule.Pattern = Doc{JSON: r.EventPattern}
			}
			if r.State == "DISABLED" {
				v := false
				rule.Enabled = &v
			}
			tin := map[string]any{"Rule": r.Name}
			if bus.Name != "default" {
				tin["EventBusName"] = bus.Name
			}
			if out, err := c.json11(ctx, "AWSEvents", "ListTargetsByRule", tin); err == nil {
				var ts struct {
					Targets []struct {
						Arn              string
						Input            string
						InputPath        string
						InputTransformer *struct {
							InputTemplate string
							InputPathsMap map[string]string
						}
					}
				}
				json.Unmarshal(out, &ts)
				for _, t := range ts.Targets {
					leaf := arnLeaf(t.Arn)
					tgt := Target{}
					switch {
					case strings.Contains(t.Arn, ":sqs:"):
						tgt.Queue = leaf
					case strings.Contains(t.Arn, ":sns:"):
						tgt.Topic = leaf
					case strings.Contains(t.Arn, ":lambda:"):
						tgt.Lambda = strings.TrimPrefix(leaf, "function:")
					default:
						continue
					}
					if t.Input != "" {
						tgt.Input = Doc{JSON: t.Input}
					}
					tgt.InputPath = t.InputPath
					if t.InputTransformer != nil {
						tgt.Template = t.InputTransformer.InputTemplate
						if len(t.InputTransformer.InputPathsMap) > 0 {
							tgt.Paths = t.InputTransformer.InputPathsMap
						}
					}
					rule.Targets = append(rule.Targets, tgt)
				}
			}
			s.Rules[r.Name] = rule
		}
	}
	return nil
}

func exportKeys(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "TrentService", "ListAliases", map[string]any{})
	if err != nil {
		return err
	}
	var lst struct {
		Aliases []struct{ AliasName, TargetKeyId string }
	}
	json.Unmarshal(out, &lst)
	for _, a := range lst.Aliases {
		name := strings.TrimPrefix(a.AliasName, "alias/")
		if strings.HasPrefix(name, "aws/") { // AWS-managed style aliases stay out
			continue
		}
		if s.Keys == nil {
			s.Keys = map[string]Key{}
		}
		k := Key{}
		if out, err := c.json11(ctx, "TrentService", "DescribeKey", map[string]any{"KeyId": a.TargetKeyId}); err == nil {
			var d struct {
				KeyMetadata struct{ KeySpec, KeyUsage, Description string }
			}
			json.Unmarshal(out, &d)
			if d.KeyMetadata.KeySpec != "" && d.KeyMetadata.KeySpec != "SYMMETRIC_DEFAULT" {
				k.Spec = d.KeyMetadata.KeySpec
			}
			// Only export a usage apply wouldn't infer from the spec.
			if u := d.KeyMetadata.KeyUsage; u != "" && u != inferredUsage(k.Spec) {
				k.Usage = u
			}
			k.Description = d.KeyMetadata.Description
		}
		if out, err := c.json11(ctx, "TrentService", "GetKeyRotationStatus", map[string]any{"KeyId": a.TargetKeyId}); err == nil {
			var rs struct{ KeyRotationEnabled bool }
			json.Unmarshal(out, &rs)
			k.Rotation = rs.KeyRotationEnabled
		}
		if out, err := c.json11(ctx, "TrentService", "ListResourceTags", map[string]any{"KeyId": a.TargetKeyId}); err == nil {
			var lt struct {
				Tags []struct{ TagKey, TagValue string }
			}
			json.Unmarshal(out, &lt)
			for _, tag := range lt.Tags {
				if k.Tags == nil {
					k.Tags = map[string]string{}
				}
				k.Tags[tag.TagKey] = tag.TagValue
			}
		}
		s.Keys[name] = k
	}
	return nil
}

// inferredUsage mirrors apply's spec → default-usage mapping.
func inferredUsage(spec string) string {
	switch {
	case strings.HasPrefix(spec, "RSA"), strings.HasPrefix(spec, "ECC"):
		return "SIGN_VERIFY"
	case strings.HasPrefix(spec, "HMAC"):
		return "GENERATE_VERIFY_MAC"
	default:
		return "ENCRYPT_DECRYPT"
	}
}

func exportSecrets(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "secretsmanager", "ListSecrets", map[string]any{})
	if err != nil {
		return err
	}
	var lst struct {
		SecretList []struct {
			Name        string
			Description string
			Tags        []struct{ Key, Value string }
		}
	}
	json.Unmarshal(out, &lst)
	if len(lst.SecretList) > 0 {
		s.Secrets = map[string]Secret{}
	}
	for _, sec := range lst.SecretList {
		out := Secret{Description: sec.Description} // values intentionally not exported
		for _, tag := range sec.Tags {
			if out.Tags == nil {
				out.Tags = map[string]string{}
			}
			out.Tags[tag.Key] = tag.Value
		}
		s.Secrets[sec.Name] = out
	}
	return nil
}

func exportParameters(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "AmazonSSM", "DescribeParameters", map[string]any{"MaxResults": 50})
	if err != nil {
		return err
	}
	var lst struct {
		Parameters []struct{ Name, Type, Description string }
	}
	json.Unmarshal(out, &lst)
	if len(lst.Parameters) > 0 {
		s.Parameters = map[string]Parameter{}
	}
	for _, p := range lst.Parameters {
		param := Parameter{Type: p.Type, Description: p.Description}
		if p.Type == "String" || p.Type == "StringList" {
			if out, err := c.json11(ctx, "AmazonSSM", "GetParameter", map[string]any{"Name": p.Name}); err == nil {
				var g struct {
					Parameter struct{ Value string }
				}
				json.Unmarshal(out, &g)
				param.Value = g.Parameter.Value
			}
			if param.Type == "String" {
				param.Type = "" // the default; keep exports minimal
			}
		}
		if out, err := c.json11(ctx, "AmazonSSM", "ListTagsForResource", map[string]any{
			"ResourceType": "Parameter", "ResourceId": p.Name,
		}); err == nil {
			var lt struct {
				TagList []struct{ Key, Value string }
			}
			json.Unmarshal(out, &lt)
			for _, tag := range lt.TagList {
				if param.Tags == nil {
					param.Tags = map[string]string{}
				}
				param.Tags[tag.Key] = tag.Value
			}
		}
		s.Parameters[p.Name] = param
	}
	return nil
}

func arnLeaf(arn string) string {
	if i := strings.LastIndexAny(arn, ":/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// xmlBlock returns the inner text of the first <tag>…</tag> block, or "".
func xmlBlock(s, tag string) string {
	blocks := xmlBlocks(s, tag)
	if len(blocks) == 0 {
		return ""
	}
	return blocks[0]
}

// xmlBlocks returns the inner text of every <tag>…</tag> block.
func xmlBlocks(s, tag string) []string {
	open, close := "<"+tag+">", "</"+tag+">"
	var out []string
	for {
		i := strings.Index(s, open)
		if i < 0 {
			break
		}
		rest := s[i+len(open):]
		j := strings.Index(rest, close)
		if j < 0 {
			break
		}
		out = append(out, rest[:j])
		s = rest[j+len(close):]
	}
	return out
}

// xmlValues returns every occurrence of <tag>…</tag>.
func xmlValues(s, tag string) []string {
	var out []string
	for {
		v := xmlValue(s, tag)
		if v == "" {
			break
		}
		out = append(out, v)
		i := strings.Index(s, "</"+tag+">")
		s = s[i+len(tag)+3:]
	}
	return out
}
