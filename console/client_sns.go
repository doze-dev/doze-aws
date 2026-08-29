package console

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ---- SNS (Query/XML) ----

type Topic struct {
	Name    string
	ARN     string
	Subs    int
	SubList []Subscription // the subscriptions behind Subs — BuildGraph reuses them
}

type Subscription struct {
	ARN          string
	Protocol     string
	Endpoint     string
	FilterPolicy string // JSON, "" when none
	RawDelivery  bool
	// Topic is set only by AllSubscriptions, where a row has to say which
	// topic it belongs to; the per-topic list already knows.
	Topic string
}

func (b *backend) ListTopics(ctx context.Context) ([]Topic, error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"ListTopics"}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Members []struct {
			TopicArn string `xml:"TopicArn"`
		} `xml:"ListTopicsResult>Topics>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	topics := make([]Topic, 0, len(out.Members))
	for _, m := range out.Members {
		t := Topic{ARN: m.TopicArn, Name: arnLeaf(m.TopicArn)}
		if subs, err := b.ListSubscriptions(ctx, m.TopicArn); err == nil {
			t.Subs = len(subs)
			t.SubList = subs
		}
		topics = append(topics, t)
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
	return topics, nil
}

// CountTopics is the cheap cardinality probe: one ListTopics call, no
// per-topic subscription fetches.
func (b *backend) CountTopics(ctx context.Context) (int, error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"ListTopics"}})
	if err != nil {
		return 0, err
	}
	var out struct {
		Members []struct{} `xml:"ListTopicsResult>Topics>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return 0, err
	}
	return len(out.Members), nil
}

func (b *backend) CreateTopic(ctx context.Context, name string) error {
	_, err := b.queryXML(ctx, url.Values{"Action": {"CreateTopic"}, "Name": {name}})
	return err
}

func (b *backend) DeleteTopic(ctx context.Context, arn string) error {
	_, err := b.queryXML(ctx, url.Values{"Action": {"DeleteTopic"}, "TopicArn": {arn}})
	return err
}

func (b *backend) TopicAttributes(ctx context.Context, arn string) (map[string]string, error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"GetTopicAttributes"}, "TopicArn": {arn}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Entries []struct {
			Key   string `xml:"key"`
			Value string `xml:"value"`
		} `xml:"GetTopicAttributesResult>Attributes>entry"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	attrs := map[string]string{}
	for _, e := range out.Entries {
		attrs[e.Key] = e.Value
	}
	return attrs, nil
}

func (b *backend) ListSubscriptions(ctx context.Context, topicARN string) ([]Subscription, error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"ListSubscriptionsByTopic"}, "TopicArn": {topicARN}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Members []struct {
			SubscriptionArn string `xml:"SubscriptionArn"`
			Protocol        string `xml:"Protocol"`
			Endpoint        string `xml:"Endpoint"`
		} `xml:"ListSubscriptionsByTopicResult>Subscriptions>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	subs := make([]Subscription, 0, len(out.Members))
	for _, m := range out.Members {
		s := Subscription{ARN: m.SubscriptionArn, Protocol: m.Protocol, Endpoint: m.Endpoint}
		// Confirmed subscriptions carry a real ARN; pull their filter policy and
		// raw-delivery flag. Pending ones ("PendingConfirmation") have no attrs.
		if strings.HasPrefix(s.ARN, "arn:") {
			if a, err := b.subscriptionAttributes(ctx, s.ARN); err == nil {
				s.FilterPolicy = a["FilterPolicy"]
				s.RawDelivery = a["RawMessageDelivery"] == "true"
			}
		}
		subs = append(subs, s)
	}
	return subs, nil
}

// subscriptionAttributes reads one subscription's attribute map.
func (b *backend) subscriptionAttributes(ctx context.Context, subARN string) (map[string]string, error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"GetSubscriptionAttributes"}, "SubscriptionArn": {subARN}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Entries []struct {
			Key   string `xml:"key"`
			Value string `xml:"value"`
		} `xml:"GetSubscriptionAttributesResult>Attributes>entry"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	attrs := map[string]string{}
	for _, e := range out.Entries {
		attrs[e.Key] = e.Value
	}
	return attrs, nil
}

// SetSubscriptionAttribute sets one subscription attribute (FilterPolicy,
// RawMessageDelivery, …). An empty FilterPolicy value clears it.
func (b *backend) SetSubscriptionAttribute(ctx context.Context, subARN, name, value string) error {
	_, err := b.queryXML(ctx, url.Values{
		"Action": {"SetSubscriptionAttributes"}, "SubscriptionArn": {subARN},
		"AttributeName": {name}, "AttributeValue": {value},
	})
	return err
}

// Subscribe creates a subscription, optionally with attributes (FilterPolicy,
// RawMessageDelivery) applied at creation time.
func (b *backend) Subscribe(ctx context.Context, topicARN, protocol, endpoint string, attrs map[string]string) error {
	v := url.Values{
		"Action": {"Subscribe"}, "TopicArn": {topicARN},
		"Protocol": {protocol}, "Endpoint": {endpoint},
		"ReturnSubscriptionArn": {"true"},
	}
	i := 1
	for k, val := range attrs {
		if val == "" {
			continue
		}
		v.Set(fmt.Sprintf("Attributes.entry.%d.key", i), k)
		v.Set(fmt.Sprintf("Attributes.entry.%d.value", i), val)
		i++
	}
	_, err := b.queryXML(ctx, v)
	return err
}

func (b *backend) Unsubscribe(ctx context.Context, subARN string) error {
	_, err := b.queryXML(ctx, url.Values{"Action": {"Unsubscribe"}, "SubscriptionArn": {subARN}})
	return err
}

// setMsgAttrs writes the MessageAttributes.entry.N block. Factored out because
// Publish and MatchSubscriptions must serialise attributes identically — the
// preview is only worth anything if it is evaluating the same message.
func setMsgAttrs(v url.Values, attrs []MsgAttr) {
	for i, a := range attrs {
		p := "MessageAttributes.entry." + strconv.Itoa(i+1)
		t := a.Type
		if t == "" {
			t = "String"
		}
		v.Set(p+".Name", a.Name)
		v.Set(p+".Value.DataType", t)
		if t == "Binary" {
			v.Set(p+".Value.BinaryValue", a.Value)
		} else {
			v.Set(p+".Value.StringValue", a.Value)
		}
	}
}

func (b *backend) Publish(ctx context.Context, topicARN, message, subject string, attrs []MsgAttr) error {
	v := url.Values{"Action": {"Publish"}, "TopicArn": {topicARN}, "Message": {message}}
	if subject != "" {
		v.Set("Subject", subject)
	}
	setMsgAttrs(v, attrs)
	_, err := b.queryXML(ctx, v)
	return err
}

// arnLeaf returns the resource name at the end of an ARN.
func arnLeaf(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

// SubMatch is one subscription's verdict for a message about to be published.
type SubMatch struct {
	ARN      string
	Protocol string
	Endpoint string
	Matched  bool
	Pending  bool
	Reasons  []SubMatchReason
	// Ref resolves the endpoint to a console page, so a filtered-out subscriber
	// is one click from the filter that rejected it.
	Ref resourceRef
}

// SubMatchReason names one policy key that refused the message.
type SubMatchReason struct {
	Key      string
	Expected string
	Actual   string
	Present  bool
}

// MatchSubscriptions asks which subscriptions would receive a message carrying
// these attributes, and why the others would not.
//
// The console cannot evaluate this itself: it reaches services over the wire and
// imports no service package, and re-implementing SNS's filter language beside
// the real one is exactly how a preview starts disagreeing with delivery.
func (b *backend) MatchSubscriptions(ctx context.Context, topicARN string, attrs []MsgAttr) ([]SubMatch, error) {
	v := url.Values{"Action": {"DozeMatchSubscriptions"}, "TopicArn": {topicARN}}
	setMsgAttrs(v, attrs)
	body, err := b.queryXML(ctx, v)
	if err != nil {
		return nil, err
	}
	var out struct {
		Members []struct {
			SubscriptionArn, Protocol, Endpoint string
			Matched, Pending                    bool
			Reasons                             []SubMatchReason `xml:"Reasons>member"`
		} `xml:"DozeMatchSubscriptionsResult>Subscriptions>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	res := make([]SubMatch, 0, len(out.Members))
	for _, m := range out.Members {
		res = append(res, SubMatch{
			ARN: m.SubscriptionArn, Protocol: m.Protocol, Endpoint: m.Endpoint,
			Matched: m.Matched, Pending: m.Pending, Reasons: m.Reasons,
			Ref: resourceFromARN(m.Endpoint),
		})
	}
	return res, nil
}

// PublishBatch sends up to ten messages to a topic in one call.
//
// It is not a loop over Publish: SNS fans each entry out to every subscription
// and answers with per-entry failures, so a batch tells you which MESSAGE was
// refused rather than which call was. Chunked to ten, SNS's limit.
func (b *backend) PublishBatch(ctx context.Context, topicARN string, messages []string, subject string) (failed int, firstErr string, err error) {
	for start := 0; start < len(messages); start += 10 {
		end := min(start+10, len(messages))
		v := url.Values{"Action": {"PublishBatch"}, "TopicArn": {topicARN}}
		for i, m := range messages[start:end] {
			n := strconv.Itoa(i + 1)
			v.Set("PublishBatchRequestEntries.member."+n+".Id", "m"+strconv.Itoa(start+i))
			v.Set("PublishBatchRequestEntries.member."+n+".Message", m)
			if subject != "" {
				v.Set("PublishBatchRequestEntries.member."+n+".Subject", subject)
			}
		}
		body, e := b.queryXML(ctx, v)
		if e != nil {
			return failed, firstErr, e
		}
		var out struct {
			Failed []struct {
				Code string `xml:"Code"`
			} `xml:"PublishBatchResult>Failed>member"`
		}
		xml.Unmarshal(body, &out)
		failed += len(out.Failed)
		if firstErr == "" && len(out.Failed) > 0 {
			firstErr = out.Failed[0].Code
		}
	}
	return failed, firstErr, nil
}

// ConfirmSubscription completes a pending subscription.
//
// HTTP and email subscriptions arrive PendingConfirmation: SNS posts a token to
// the endpoint and waits for it to be handed back. Locally that leaves a
// subscription that exists, appears in the list, and silently receives nothing
// — which looks exactly like a delivery bug and is the reason this needed a
// surface. Paste the token, the subscription goes live.
func (b *backend) ConfirmSubscription(ctx context.Context, topicARN, token string) error {
	_, err := b.queryXML(ctx, url.Values{
		"Action": {"ConfirmSubscription"}, "TopicArn": {topicARN}, "Token": {token},
	})
	return err
}

// AllSubscriptions lists every subscription in the account, not just one
// topic's. It answers the question the per-topic list cannot: "this queue is
// receiving something — from where?"
func (b *backend) AllSubscriptions(ctx context.Context) ([]Subscription, error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"ListSubscriptions"}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Members []struct {
			SubscriptionArn string `xml:"SubscriptionArn"`
			TopicArn        string `xml:"TopicArn"`
			Protocol        string `xml:"Protocol"`
			Endpoint        string `xml:"Endpoint"`
		} `xml:"ListSubscriptionsResult>Subscriptions>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	subs := make([]Subscription, 0, len(out.Members))
	for _, m := range out.Members {
		subs = append(subs, Subscription{
			ARN: m.SubscriptionArn, Protocol: m.Protocol, Endpoint: m.Endpoint,
			Topic: arnLeaf(m.TopicArn),
		})
	}
	return subs, nil
}

// The C-tier surfaces: accepted, stored where the ledger says stored, and
// never enforced. They are exposed for the reason an emulator exists — a
// deploy script that calls them must not fail here, and you must be able to see
// that the call was accepted — and they are labelled so the control cannot
// imply an effect it has not got.

// SetTopicAttribute writes one topic attribute. C→F in the ledger: stored and
// returned by GetTopicAttributes, with no change to how anything behaves.
func (b *backend) SetTopicAttribute(ctx context.Context, topicARN, name, value string) error {
	_, err := b.queryXML(ctx, url.Values{
		"Action": {"SetTopicAttributes"}, "TopicArn": {topicARN},
		"AttributeName": {name}, "AttributeValue": {value},
	})
	return err
}

// AddTopicPermission and RemoveTopicPermission write the topic's access policy.
// C-tier: there is no IAM in front of a local topic, so a grant changes who can
// reach it not at all.
func (b *backend) AddTopicPermission(ctx context.Context, topicARN, label, accountID, action string) error {
	_, err := b.queryXML(ctx, url.Values{
		"Action": {"AddPermission"}, "TopicArn": {topicARN}, "Label": {label},
		"AWSAccountId.member.1": {accountID}, "ActionName.member.1": {action},
	})
	return err
}

func (b *backend) RemoveTopicPermission(ctx context.Context, topicARN, label string) error {
	_, err := b.queryXML(ctx, url.Values{
		"Action": {"RemovePermission"}, "TopicArn": {topicARN}, "Label": {label},
	})
	return err
}

// PutDataProtectionPolicy and DataProtectionPolicy round-trip the data
// protection policy. C-tier: stored and handed back, never applied to a
// message, so nothing is actually redacted locally.
func (b *backend) PutDataProtectionPolicy(ctx context.Context, topicARN, doc string) error {
	_, err := b.queryXML(ctx, url.Values{
		"Action": {"PutDataProtectionPolicy"}, "ResourceArn": {topicARN}, "DataProtectionPolicy": {doc},
	})
	return err
}

func (b *backend) DataProtectionPolicy(ctx context.Context, topicARN string) string {
	body, err := b.queryXML(ctx, url.Values{
		"Action": {"GetDataProtectionPolicy"}, "ResourceArn": {topicARN},
	})
	if err != nil {
		return ""
	}
	var out struct {
		Doc string `xml:"GetDataProtectionPolicyResult>DataProtectionPolicy"`
	}
	xml.Unmarshal(body, &out)
	return out.Doc
}
