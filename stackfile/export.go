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
		var d struct {
			Table struct {
				KeySchema []struct {
					AttributeName, KeyType string
				}
				AttributeDefinitions []struct {
					AttributeName, AttributeType string
				}
				GlobalSecondaryIndexes []struct {
					IndexName string
					KeySchema []struct {
						AttributeName, KeyType string
					}
				}
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
		t := Table{Key: keyOf(d.Table.KeySchema)}
		for _, g := range d.Table.GlobalSecondaryIndexes {
			if t.GSIs == nil {
				t.GSIs = map[string]GSI{}
			}
			t.GSIs[g.IndexName] = GSI{Key: keyOf(g.KeySchema)}
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
		s.Buckets[name] = b
	}
	return nil
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
		// Destinations.
		if out, err := c.do(ctx, "GET", "/2019-09-25/functions/"+url.PathEscape(fn.FunctionName)+"/event-invoke-config", nil, nil); err == nil {
			var eic struct {
				DestinationConfig struct {
					OnSuccess, OnFailure struct{ Destination string }
				}
			}
			json.Unmarshal(out, &eic)
			f.OnSuccess = destFromARN(eic.DestinationConfig.OnSuccess.Destination)
			f.OnFailure = destFromARN(eic.DestinationConfig.OnFailure.Destination)
		}
		// Triggers.
		if out, err := c.do(ctx, "GET", "/2015-03-31/event-source-mappings?FunctionName="+url.QueryEscape(fn.FunctionName), nil, nil); err == nil {
			var esm struct {
				EventSourceMappings []struct {
					EventSourceArn string
					BatchSize      int
				}
			}
			json.Unmarshal(out, &esm)
			for _, m := range esm.EventSourceMappings {
				tr := Trigger{Queue: arnLeaf(m.EventSourceArn)}
				if m.BatchSize != 0 && m.BatchSize != 10 {
					tr.Batch = m.BatchSize
				}
				f.Triggers = append(f.Triggers, tr)
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
			tin := map[string]any{"Rule": r.Name}
			if bus.Name != "default" {
				tin["EventBusName"] = bus.Name
			}
			if out, err := c.json11(ctx, "AWSEvents", "ListTargetsByRule", tin); err == nil {
				var ts struct {
					Targets []struct{ Arn string }
				}
				json.Unmarshal(out, &ts)
				for _, t := range ts.Targets {
					leaf := arnLeaf(t.Arn)
					switch {
					case strings.Contains(t.Arn, ":sqs:"):
						rule.Targets = append(rule.Targets, "queue:"+leaf)
					case strings.Contains(t.Arn, ":lambda:"):
						rule.Targets = append(rule.Targets, "lambda:"+strings.TrimPrefix(leaf, "function:"))
					}
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
				KeyMetadata struct{ KeySpec string }
			}
			json.Unmarshal(out, &d)
			if d.KeyMetadata.KeySpec != "" && d.KeyMetadata.KeySpec != "SYMMETRIC_DEFAULT" {
				k.Spec = d.KeyMetadata.KeySpec
			}
		}
		if out, err := c.json11(ctx, "TrentService", "GetKeyRotationStatus", map[string]any{"KeyId": a.TargetKeyId}); err == nil {
			var rs struct{ KeyRotationEnabled bool }
			json.Unmarshal(out, &rs)
			k.Rotation = rs.KeyRotationEnabled
		}
		s.Keys[name] = k
	}
	return nil
}

func exportSecrets(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "secretsmanager", "ListSecrets", map[string]any{})
	if err != nil {
		return err
	}
	var lst struct {
		SecretList []struct{ Name string }
	}
	json.Unmarshal(out, &lst)
	if len(lst.SecretList) > 0 {
		s.Secrets = map[string]Secret{}
	}
	for _, sec := range lst.SecretList {
		s.Secrets[sec.Name] = Secret{} // values intentionally not exported
	}
	return nil
}

func exportParameters(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "AmazonSSM", "DescribeParameters", map[string]any{"MaxResults": 50})
	if err != nil {
		return err
	}
	var lst struct {
		Parameters []struct{ Name, Type string }
	}
	json.Unmarshal(out, &lst)
	if len(lst.Parameters) > 0 {
		s.Parameters = map[string]Parameter{}
	}
	for _, p := range lst.Parameters {
		param := Parameter{Type: p.Type}
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
