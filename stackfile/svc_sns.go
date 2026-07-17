package stackfile

// SNS apply + export: topics, subscriptions, filter policies, and tags.

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

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
