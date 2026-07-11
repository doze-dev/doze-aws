package console

import (
	"strconv"
	"context"
	"encoding/xml"
	"net/url"
	"sort"
	"strings"
)

// ---- SNS (Query/XML) ----

type Topic struct {
	Name string
	ARN  string
	Subs int
}

type Subscription struct {
	ARN      string
	Protocol string
	Endpoint string
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
		}
		topics = append(topics, t)
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
	return topics, nil
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
		subs = append(subs, Subscription{ARN: m.SubscriptionArn, Protocol: m.Protocol, Endpoint: m.Endpoint})
	}
	return subs, nil
}

func (b *backend) Subscribe(ctx context.Context, topicARN, protocol, endpoint string) error {
	_, err := b.queryXML(ctx, url.Values{
		"Action": {"Subscribe"}, "TopicArn": {topicARN},
		"Protocol": {protocol}, "Endpoint": {endpoint},
		"ReturnSubscriptionArn": {"true"},
	})
	return err
}

func (b *backend) Unsubscribe(ctx context.Context, subARN string) error {
	_, err := b.queryXML(ctx, url.Values{"Action": {"Unsubscribe"}, "SubscriptionArn": {subARN}})
	return err
}

func (b *backend) Publish(ctx context.Context, topicARN, message, subject string, attrs []MsgAttr) error {
	v := url.Values{"Action": {"Publish"}, "TopicArn": {topicARN}, "Message": {message}}
	if subject != "" {
		v.Set("Subject", subject)
	}
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
	_, err := b.queryXML(ctx, v)
	return err
}

// ---- STS ----

type Identity struct {
	Account string
	ARN     string
	UserID  string
}

func (b *backend) CallerIdentity(ctx context.Context) (*Identity, error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"GetCallerIdentity"}, "Version": {"2011-06-15"}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Account string `xml:"GetCallerIdentityResult>Account"`
		Arn     string `xml:"GetCallerIdentityResult>Arn"`
		UserID  string `xml:"GetCallerIdentityResult>UserId"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &Identity{Account: out.Account, ARN: out.Arn, UserID: out.UserID}, nil
}

// arnLeaf returns the resource name at the end of an ARN.
func arnLeaf(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}
