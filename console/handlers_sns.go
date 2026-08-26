package console

import (
	"net/http"
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
		"Queues": queues, "Functions": fns, "List": topics, "Title": name + " · SNS",
		"Conn": c.be.Neighbors(r.Context(), "sns", name),
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
	})
}

func (c *Console) snsPublish(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("topic")
	arn := topicARNOf(name)
	attrs := parseMsgAttrs(r.FormValue("attrs"))
	if err := c.be.Publish(r.Context(), arn, r.FormValue("message"), r.FormValue("subject"), attrs); err != nil {
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
