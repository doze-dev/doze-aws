package sns

import (
	"fmt"
	"github.com/doze-dev/doze-aws/awsident"
	"net/url"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awsquery"
	"github.com/doze-dev/doze-aws/internal/eventpattern"
	"sort"
	"strconv"
)

// dispatch maps an SNS action to its handler.
var dispatch = map[string]func(*Server, url.Values, string) (any, *apiError){
	"CreateTopic":               (*Server).createTopic,
	"DeleteTopic":               (*Server).deleteTopic,
	"ListTopics":                (*Server).listTopics,
	"GetTopicAttributes":        (*Server).getTopicAttributes,
	"Subscribe":                 (*Server).subscribe,
	"ConfirmSubscription":       (*Server).confirmSubscription,
	"Unsubscribe":               (*Server).unsubscribe,
	"ListSubscriptions":         (*Server).listSubscriptions,
	"ListSubscriptionsByTopic":  (*Server).listSubscriptionsByTopic,
	"GetSubscriptionAttributes": (*Server).getSubscriptionAttributes,
	"SetSubscriptionAttributes": (*Server).setSubscriptionAttributes,
	"Publish":                   (*Server).publish,
	"PublishBatch":              (*Server).publishBatch,
}

func asErr(err error) *apiError {
	if err == nil {
		return nil
	}
	if ae, ok := err.(*apiError); ok {
		return ae
	}
	return &apiError{Code: "InternalError", Status: 500, Message: err.Error()}
}

// subscribeAttributes reads Attributes.entry.N.key/value (CreateTopic, Subscribe).
func subscribeAttributes(form url.Values) map[string]string {
	return awsquery.PairMap(form, "Attributes.entry", "key", "value")
}

// messageAttributes reads Publish's MessageAttributes.entry.N.* params.
func messageAttributes(form url.Values) map[string]Attr {
	return awsquery.MessageAttrs(form, "MessageAttributes.entry")
}

// ---- result shapes (XML member-wrapped, per SNS Query protocol) ----

type createTopicResult struct {
	TopicArn string `xml:"TopicArn"`
}
type subscribeResult struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
}
type confirmResult struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
}
type publishResult struct {
	MessageID string `xml:"MessageId"`
}
type topicMember struct {
	TopicArn string `xml:"TopicArn"`
}
type listTopicsResult struct {
	Topics struct {
		Member []topicMember `xml:"member"`
	} `xml:"Topics"`
}
type subMember struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
	Owner           string `xml:"Owner"`
	Protocol        string `xml:"Protocol"`
	Endpoint        string `xml:"Endpoint"`
	TopicArn        string `xml:"TopicArn"`
}
type listSubsResult struct {
	Subscriptions struct {
		Member []subMember `xml:"member"`
	} `xml:"Subscriptions"`
}
type attrEntry struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}
type getTopicAttrsResult struct {
	Attributes struct {
		Entry []attrEntry `xml:"entry"`
	} `xml:"Attributes"`
}
type pbSuccess struct {
	ID        string `xml:"Id"`
	MessageID string `xml:"MessageId"`
}
type publishBatchResult struct {
	Successful struct {
		Member []pbSuccess `xml:"member"`
	} `xml:"Successful"`
	Failed struct {
		Member []any `xml:"member"`
	} `xml:"Failed"`
}

// ---- handlers ----

func (srv *Server) createTopic(form url.Values, _ string) (any, *apiError) {
	t, err := srv.store.CreateTopic(form.Get("Name"), subscribeAttributes(form), memberTags(form))
	if err != nil {
		return nil, asErr(err)
	}
	return createTopicResult{TopicArn: t.ARN}, nil
}

func (srv *Server) deleteTopic(form url.Values, _ string) (any, *apiError) {
	return nil, asErr(srv.store.DeleteTopic(form.Get("TopicArn")))
}

func (srv *Server) listTopics(_ url.Values, _ string) (any, *apiError) {
	topics, err := srv.store.ListTopics()
	if err != nil {
		return nil, asErr(err)
	}
	var res listTopicsResult
	for _, t := range topics {
		res.Topics.Member = append(res.Topics.Member, topicMember{TopicArn: t.ARN})
	}
	return res, nil
}

// defaultDeliveryPolicy is the document AWS reports when a topic has no
// explicit delivery policy.
const defaultDeliveryPolicy = `{"http":{"defaultHealthyRetryPolicy":` +
	`{"minDelayTarget":20,"maxDelayTarget":20,"numRetries":3,"numMaxDelayRetries":0,` +
	`"numNoDelayRetries":0,"numMinDelayRetries":0,"backoffFunction":"linear"},` +
	`"disableSubscriptionOverrides":false}}`

// defaultTopicPolicy mirrors the access policy AWS attaches to a new topic.
// Nothing locally evaluates it; it exists because clients parse it.
func defaultTopicPolicy(arn string) string {
	return `{"Version":"2008-10-17","Id":"__default_policy_ID","Statement":[{` +
		`"Sid":"__default_statement_ID","Effect":"Allow","Principal":{"AWS":"*"},` +
		`"Action":["SNS:GetTopicAttributes","SNS:SetTopicAttributes","SNS:AddPermission",` +
		`"SNS:RemovePermission","SNS:DeleteTopic","SNS:Subscribe","SNS:ListSubscriptionsByTopic",` +
		`"SNS:Publish"],"Resource":"` + arn + `","Condition":{"StringEquals":` +
		`{"AWS:SourceOwner":"` + awsident.AccountID + `"}}}]}`
}

func (srv *Server) getTopicAttributes(form url.Values, _ string) (any, *apiError) {
	arn := form.Get("TopicArn")
	if !srv.store.TopicExists(arn) {
		return nil, errNotFound("topic does not exist: " + arn)
	}
	subs, _ := srv.store.ListSubscriptions(arn)
	var res getTopicAttrsResult
	// Owner, Policy and EffectiveDeliveryPolicy are always present on a real
	// topic. Terraform JSON-parses Policy unconditionally, so omitting it is an
	// "unexpected end of JSON input" rather than a missing field.
	res.Attributes.Entry = []attrEntry{
		{Key: "TopicArn", Value: arn},
		{Key: "Owner", Value: awsident.AccountID},
		{Key: "SubscriptionsConfirmed", Value: fmt.Sprintf("%d", countConfirmed(subs))},
		{Key: "SubscriptionsPending", Value: fmt.Sprintf("%d", len(subs)-countConfirmed(subs))},
		{Key: "SubscriptionsDeleted", Value: "0"},
		{Key: "Policy", Value: defaultTopicPolicy(arn)},
		{Key: "EffectiveDeliveryPolicy", Value: defaultDeliveryPolicy},
	}
	if t, err := srv.store.GetTopic(arn); err == nil {
		for _, k := range sortedAttrKeys(t.Attrs) {
			res.Attributes.Entry = append(res.Attributes.Entry, attrEntry{Key: k, Value: t.Attrs[k]})
		}
	}
	return res, nil
}

func countConfirmed(subs []Subscription) int {
	n := 0
	for _, s := range subs {
		if s.Confirmed {
			n++
		}
	}
	return n
}

func (srv *Server) subscribe(form url.Values, host string) (any, *apiError) {
	topicArn, proto, endpoint := form.Get("TopicArn"), form.Get("Protocol"), form.Get("Endpoint")
	if topicArn == "" || proto == "" || endpoint == "" {
		return nil, errInvalid("TopicArn, Protocol and Endpoint are required")
	}
	if perr := validProtocol(proto); perr != nil {
		return nil, perr
	}
	attrs := subscribeAttributes(form)
	if perr := validateFilterPolicy(attrs["FilterPolicy"]); perr != nil {
		return nil, perr
	}
	sub, err := srv.store.Subscribe(topicArn, proto, endpoint, attrs)
	if err != nil {
		return nil, asErr(err)
	}
	arn := sub.ARN
	if !sub.Confirmed {
		srv.sendConfirmation(*sub, host)
		if form.Get("ReturnSubscriptionArn") != "true" {
			arn = "pending confirmation"
		}
	}
	return subscribeResult{SubscriptionArn: arn}, nil
}

func (srv *Server) confirmSubscription(form url.Values, _ string) (any, *apiError) {
	sub, err := srv.store.ConfirmByToken(form.Get("Token"))
	if err != nil {
		return nil, asErr(err)
	}
	return confirmResult{SubscriptionArn: sub.ARN}, nil
}

func (srv *Server) unsubscribe(form url.Values, _ string) (any, *apiError) {
	return nil, asErr(srv.store.Unsubscribe(form.Get("SubscriptionArn")))
}

func (srv *Server) listSubscriptions(_ url.Values, _ string) (any, *apiError) {
	return srv.subscriptionList("")
}

func (srv *Server) listSubscriptionsByTopic(form url.Values, _ string) (any, *apiError) {
	return srv.subscriptionList(form.Get("TopicArn"))
}

func (srv *Server) subscriptionList(topic string) (any, *apiError) {
	subs, err := srv.store.ListSubscriptions(topic)
	if err != nil {
		return nil, asErr(err)
	}
	var res listSubsResult
	for _, s := range subs {
		arn := s.ARN
		if !s.Confirmed {
			arn = "PendingConfirmation"
		}
		res.Subscriptions.Member = append(res.Subscriptions.Member, subMember{
			SubscriptionArn: arn, Owner: "000000000000", Protocol: s.Protocol,
			Endpoint: s.Endpoint, TopicArn: s.TopicARN,
		})
	}
	return res, nil
}

func (srv *Server) setSubscriptionAttributes(form url.Values, _ string) (any, *apiError) {
	if form.Get("AttributeName") == "FilterPolicy" {
		if perr := validateFilterPolicy(form.Get("AttributeValue")); perr != nil {
			return nil, perr
		}
	}
	return nil, asErr(srv.store.SetSubscriptionAttribute(
		form.Get("SubscriptionArn"), form.Get("AttributeName"), form.Get("AttributeValue")))
}

// validateFilterPolicy rejects a malformed filter policy at subscribe/set time,
// the way real SNS does, instead of storing an inert policy that silently drops
// every message.
func validateFilterPolicy(policyJSON string) *apiError {
	if strings.TrimSpace(policyJSON) == "" {
		return nil
	}
	if _, err := eventpattern.Parse([]byte(policyJSON)); err != nil {
		return errInvalid("invalid FilterPolicy: " + err.Error())
	}
	return nil
}
func (srv *Server) getSubscriptionAttributes(form url.Values, _ string) (any, *apiError) {
	sub, err := srv.store.GetSubscription(form.Get("SubscriptionArn"))
	if err != nil {
		return nil, asErr(err)
	}
	pending := "false"
	if !sub.Confirmed {
		pending = "true"
	}
	var res getTopicAttrsResult // same {Attributes>entry} shape
	res.Attributes.Entry = []attrEntry{
		{Key: "SubscriptionArn", Value: sub.ARN},
		{Key: "TopicArn", Value: sub.TopicARN},
		{Key: "Protocol", Value: sub.Protocol},
		{Key: "Endpoint", Value: sub.Endpoint},
		{Key: "RawMessageDelivery", Value: boolStr(sub.RawDelivery)},
		{Key: "PendingConfirmation", Value: pending},
		{Key: "FilterPolicy", Value: sub.FilterPolicy},
	}
	for _, k := range sortedAttrKeys(sub.Extra) {
		res.Attributes.Entry = append(res.Attributes.Entry, attrEntry{Key: k, Value: sub.Extra[k]})
	}
	return res, nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (srv *Server) publish(form url.Values, _ string) (any, *apiError) {
	topicArn := form.Get("TopicArn")
	if topicArn == "" {
		topicArn = form.Get("TargetArn")
	}
	if !srv.store.TopicExists(topicArn) {
		return nil, errNotFound("topic does not exist: " + topicArn)
	}
	id := newID()
	srv.deliver(id, topicArn, form.Get("Subject"), form.Get("Message"), messageAttributes(form))
	return publishResult{MessageID: id}, nil
}

func (srv *Server) publishBatch(form url.Values, _ string) (any, *apiError) {
	topicArn := form.Get("TopicArn")
	if !srv.store.TopicExists(topicArn) {
		return nil, errNotFound("topic does not exist: " + topicArn)
	}
	var res publishBatchResult
	for i := 1; ; i++ {
		base := fmt.Sprintf("PublishBatchRequestEntries.member.%d.", i)
		id := form.Get(base + "Id")
		if id == "" {
			break
		}
		mid := newID()
		srv.deliver(mid, topicArn, form.Get(base+"Subject"), form.Get(base+"Message"), entryMessageAttributes(form, base))
		res.Successful.Member = append(res.Successful.Member, pbSuccess{ID: id, MessageID: mid})
	}
	return res, nil
}

// snsProtocols is the set SNS accepts for Subscribe. Hand-derived: SNS's own
// service model types Protocol as a plain string with no enum trait, so this
// list comes from the API reference rather than from `dzaudit list`.
//
// Membership here means "AWS accepts it", not "doze-aws delivers to it".
// Refusing a protocol AWS refuses is the promise this file is keeping; the
// delivery gap for the cloud-only transports is a separate, documented one —
// and conflating them would mean refusing a subscription that works in AWS.
var snsProtocols = map[string]bool{
	"http": true, "https": true, // delivered
	"sqs":    true, // delivered
	"lambda": true, // delivered
	"email":  true, "email-json": true, "sms": true,
	"application": true, "firehose": true,
}

func validProtocol(p string) *apiError {
	if snsProtocols[p] {
		return nil
	}
	valid := make([]string, 0, len(snsProtocols))
	for k := range snsProtocols {
		valid = append(valid, k)
	}
	sort.Strings(valid)
	return errInvalid("Invalid parameter: Protocol - " + strconv.Quote(p) +
		" is not a supported protocol (expected one of " + strings.Join(valid, ", ") + ")")
}
