package console

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// topicARNOf rebuilds a topic ARN from its console path segment (the name).
func topicARNOf(name string) string { return awsident.ARN("sns", name) }

func (c *Console) snsTopics(w http.ResponseWriter, r *http.Request) {
	topics, err := c.be.ListTopics(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	if len(topics) > 0 {
		r.SetPathValue("topic", topics[0].Name)
		c.snsTopic(w, r)
		return
	}
	c.render(w, r, "sns_home", map[string]any{"List": topics, "Title": "SNS"})
}

func (c *Console) snsCreateTopic(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.CreateTopic(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sns/"+name, "Topic “"+name+"” created")
}

func (c *Console) snsDeleteTopic(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteTopic(r.Context(), topicARNOf(r.PathValue("topic"))); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sns", "Topic deleted")
}

func (c *Console) snsTopic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("topic")
	arn := topicARNOf(name)
	attrs, err := c.be.TopicAttributes(r.Context(), arn)
	if err != nil {
		c.fail(w, err)
		return
	}
	subs, _ := c.be.ListSubscriptions(r.Context(), arn)
	queues, _ := c.be.ListQueues(r.Context())
	fns, _ := c.be.ListFunctions(r.Context())
	topics, _ := c.be.ListTopics(r.Context())
	c.render(w, r, "sns_topic", map[string]any{
		"Topic": name, "ARN": arn, "Attrs": attrs, "Subs": subViews(subs),
		"Tab":    tabOf(r, "subs"),
		"Queues": queues, "Functions": fns, "List": topics, "Title": name + " · SNS",
		"Conn": c.be.Neighbors(r.Context(), "sns", name),
		// A pending subscription is listed and silent, which is the confusing
		// part; the confirm control only appears when there is one to confirm.
		"Pending": pendingCount(subs),
		// Cross-topic view: which subscriptions exist anywhere. It answers the
		// question the per-topic list cannot — "this endpoint is receiving
		// something, from where?" — and is the only place an orphaned
		// subscription to a deleted topic becomes visible.
		"AllSubs":    c.allSubs(r),
		"DataPolicy": c.be.DataProtectionPolicy(r.Context(), arn),
	})
}

// subView renders a subscription in the console's resource language: the
// endpoint's NAME (linked, service-colored) rather than a truncated ARN.
type subView struct {
	Proto, Name, URL, Endpoint, SubARN string
	Svc                                string // service color key; "" for http/webhook
	FilterPolicy                       string // JSON, "" when none
	RawDelivery                        bool
	Pending                            bool // ARN not yet confirmed (http/https)
}

func subViews(subs []Subscription) []subView {
	views := make([]subView, 0, len(subs))
	for _, s := range subs {
		v := subView{
			Proto: s.Protocol, Endpoint: s.Endpoint, SubARN: s.ARN, Name: s.Endpoint,
			FilterPolicy: s.FilterPolicy, RawDelivery: s.RawDelivery,
			Pending: !strings.HasPrefix(s.ARN, "arn:"),
		}
		// sns.html states the rule this implements: the endpoint's NAME as a
		// service-coloured link, never a truncated ARN. It used to know about
		// two protocols; the shared resolver knows about all of them, so an SNS
		// topic subscribed to another topic, or a Kinesis stream, now reads the
		// same way an SQS queue always did. http/email have no page and keep an
		// empty Svc, which the template already handles.
		if ref := resourceFromARN(s.Endpoint); ref.OK() {
			v.Svc, v.Name, v.URL = ref.Svc, ref.Name, ref.Path
		}
		views = append(views, v)
	}
	return views
}

func (c *Console) snsSubsPartial(w http.ResponseWriter, r *http.Request, name string) {
	arn := topicARNOf(name)
	subs, _ := c.be.ListSubscriptions(r.Context(), arn)
	queues, _ := c.be.ListQueues(r.Context())
	fns, _ := c.be.ListFunctions(r.Context())
	c.partial(w, "sns_subs", map[string]any{
		"Topic": name, "ARN": arn, "Subs": subViews(subs), "Queues": queues, "Functions": fns,
		"Prefix": c.prefix, "Pending": pendingCount(subs),
	})
}

func (c *Console) snsPublish(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("topic")
	arn := topicARNOf(name)
	attrs := parseMsgAttrs(r.FormValue("attrs"))
	// A count above one is PublishBatch — a different API, not a loop, because
	// SNS reports per-ENTRY failures. One route dispatches on the field rather
	// than the form rewriting its own hx-post: htmx reads those attributes when
	// it processes the element, so mutating them later is unreliable.
	if n := atoiDefault(r.FormValue("count"), 1); n > 1 {
		msgs := make([]string, 0, min(n, 10))
		for i := 0; i < min(n, 10); i++ {
			msgs = append(msgs, r.FormValue("message"))
		}
		failed, code, err := c.be.PublishBatch(r.Context(), arn, msgs, r.FormValue("subject"))
		if err != nil {
			c.fail(w, err)
			return
		}
		note := strconv.Itoa(len(msgs)-failed) + " published"
		if failed > 0 {
			note += ", " + strconv.Itoa(failed) + " refused (" + code + ")"
		}
		toast(w, note)
		// Falls through to the same receipt a single publish renders, rather
		// than redirecting. The receipt IS the point of this form — who
		// actually received it, filters evaluated — and a batch of identical
		// messages has exactly the same answer. Redirecting instead would make
		// the batch path the one that tells you least.
	} else if err := c.be.Publish(r.Context(), arn, r.FormValue("message"), r.FormValue("subject"), attrs); err != nil {
		c.fail(w, err)
		return
	}
	// The receipt used to list every subscription as a recipient without
	// evaluating a single filter policy, so three of four filtered out still
	// reported four. Asking the service means the receipt and the delivery
	// agree by construction rather than by a second implementation.
	//
	// Matching AFTER the publish, with the attributes that were published: a
	// subscription added between the two calls cannot make the receipt claim
	// something that did not happen. A race is still possible in principle and
	// the window is microseconds on a local stack — closing it properly would
	// need a publish-and-report action, which is not worth a new wire verb.
	matches, err := c.be.MatchSubscriptions(r.Context(), arn, attrs)
	if err != nil {
		// The publish succeeded; only the explanation is missing. Fall back to
		// the old shape rather than reporting a failure that did not happen.
		subs, _ := c.be.ListSubscriptions(r.Context(), arn)
		c.partial(w, "sns_receipt", map[string]any{"Topic": name, "Rcpts": subViews(subs), "NoMatch": true})
		return
	}
	var got, filtered, pending []SubMatch
	for _, m := range matches {
		switch {
		case m.Pending:
			pending = append(pending, m)
		case m.Matched:
			got = append(got, m)
		default:
			filtered = append(filtered, m)
		}
	}
	c.partial(w, "sns_receipt", map[string]any{
		"Topic": name, "Got": got, "Filtered": filtered, "Pending": pending,
		"Total": len(matches),
	})
}

func (c *Console) snsSubscribe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("topic")
	// Optional filter policy + raw delivery applied at subscribe time.
	attrs := map[string]string{}
	if fp := strings.TrimSpace(r.FormValue("policy")); fp != "" {
		attrs["FilterPolicy"] = fp
	}
	if r.FormValue("raw") == "on" || r.FormValue("raw") == "true" {
		attrs["RawMessageDelivery"] = "true"
	}
	if err := c.be.Subscribe(r.Context(), topicARNOf(name), r.FormValue("protocol"), r.FormValue("endpoint"), attrs); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Subscription created")
	c.snsSubsPartial(w, r, name)
}

func (c *Console) snsUnsubscribe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("topic")
	if err := c.be.Unsubscribe(r.Context(), r.FormValue("arn")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Subscription removed")
	c.snsSubsPartial(w, r, name)
}

// snsSubFilter sets a subscription's filter policy (a message only reaches the
// subscriber when its attributes match). An empty policy clears the filter.
func (c *Console) snsSubFilter(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("topic")
	if err := c.be.SetSubscriptionAttribute(r.Context(), r.FormValue("arn"), "FilterPolicy", strings.TrimSpace(r.FormValue("policy"))); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Filter policy saved")
	c.snsSubsPartial(w, r, name)
}

// snsSubRaw toggles raw message delivery (deliver the bare message body instead
// of the SNS JSON envelope).
func (c *Console) snsSubRaw(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("topic")
	value := "false"
	if r.FormValue("raw") == "on" || r.FormValue("raw") == "true" {
		value = "true"
	}
	if err := c.be.SetSubscriptionAttribute(r.Context(), r.FormValue("arn"), "RawMessageDelivery", value); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Raw delivery "+map[string]string{"true": "on", "false": "off"}[value])
	c.snsSubsPartial(w, r, name)
}

// snsConfirm completes a pending subscription.
//
// An HTTP or email subscription arrives PendingConfirmation: SNS posts a token
// to the endpoint and waits to be handed it back. Until then the subscription
// exists, lists, and receives nothing — which reads exactly like a delivery bug
// and is why this needed a surface rather than a CLI.
func (c *Console) snsConfirm(w http.ResponseWriter, r *http.Request) {
	topic := r.PathValue("topic")
	token := strings.TrimSpace(r.FormValue("token"))
	if token == "" {
		c.fail(w, errors.New("Paste the token SNS posted to your endpoint — it is what proves the endpoint wanted this subscription."))
		return
	}
	if err := c.be.ConfirmSubscription(r.Context(), topicARNOf(topic), token); err != nil {
		c.fail(w, err)
		return
	}
	// Re-renders the panel it changed rather than redirecting to the page it is
	// already on — the same shape as sub-filter and sub-raw. It also keeps the
	// mutation sweep honest: a route that can only succeed with a real pending
	// token cannot be driven to a redirect by a fixture, and classifying it as
	// redirect-capable would leave a permanent false failure.
	toast(w, "Subscription confirmed")
	c.snsSubsPartial(w, r, topic)
}

// pendingCount is how many of a topic's subscriptions are still awaiting
// confirmation. AWS reports these with the literal ARN "PendingConfirmation"
// rather than a status field, which is easy to miss when reading a list.
func pendingCount(subs []Subscription) int {
	n := 0
	for _, s := range subs {
		if strings.Contains(s.ARN, "PendingConfirmation") {
			n++
		}
	}
	return n
}

// allSubs lists every subscription in the stack. Errors are swallowed: this is
// a supplementary panel, and failing the topic page because a cross-topic
// listing hiccuped would trade the thing you asked for against the thing you
// did not.
func (c *Console) allSubs(r *http.Request) []Subscription {
	subs, err := c.be.AllSubscriptions(r.Context())
	if err != nil {
		return nil
	}
	return subs
}

// snsSetAttribute writes one topic attribute — DisplayName, Policy,
// DeliveryPolicy. C-tier, and the UI says so beside the control: the value is
// stored and returned by GetTopicAttributes and changes nothing about how the
// topic behaves.
func (c *Console) snsSetAttribute(w http.ResponseWriter, r *http.Request) {
	topic := r.PathValue("topic")
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		c.fail(w, errors.New("Name the attribute to set — DisplayName, Policy or DeliveryPolicy."))
		return
	}
	if err := c.be.SetTopicAttribute(r.Context(), topicARNOf(topic), name, r.FormValue("value")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sns/"+topic+"?tab=details", "Attribute “"+name+"” stored")
}

// snsAddPermission / snsRemovePermission write the topic's access policy.
// Same shape and the same caveat as the SQS queue policy: accepted, and there
// is no IAM in front of a local topic for it to affect.
func (c *Console) snsAddPermission(w http.ResponseWriter, r *http.Request) {
	topic := r.PathValue("topic")
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		c.fail(w, errors.New("A permission needs a label — it is how RemovePermission finds it again."))
		return
	}
	acct := strings.TrimSpace(r.FormValue("account"))
	if acct == "" {
		acct = awsident.AccountID
	}
	action := strings.TrimSpace(r.FormValue("action"))
	if action == "" {
		action = "Publish"
	}
	if err := c.be.AddTopicPermission(r.Context(), topicARNOf(topic), label, acct, action); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sns/"+topic+"?tab=details", "Permission “"+label+"” added")
}

func (c *Console) snsRemovePermission(w http.ResponseWriter, r *http.Request) {
	topic := r.PathValue("topic")
	if err := c.be.RemoveTopicPermission(r.Context(), topicARNOf(topic), r.FormValue("label")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sns/"+topic+"?tab=details", "Permission removed")
}

// snsDataProtection stores the data protection policy. C-tier and worth saying
// out loud: it is handed back verbatim and never applied to a message, so
// nothing is redacted locally however the policy reads.
func (c *Console) snsDataProtection(w http.ResponseWriter, r *http.Request) {
	topic := r.PathValue("topic")
	if err := c.be.PutDataProtectionPolicy(r.Context(), topicARNOf(topic), r.FormValue("policy")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sns/"+topic+"?tab=details", "Data protection policy stored")
}
