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
	toast(w, "Topic deleted")
	topics, _ := c.be.ListTopics(r.Context())
	c.partial(w, "sns_topic_list", map[string]any{"Topics": topics})
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
}

func subViews(subs []Subscription) []subView {
	views := make([]subView, 0, len(subs))
	for _, s := range subs {
		v := subView{Proto: s.Protocol, Endpoint: s.Endpoint, SubARN: s.ARN, Name: s.Endpoint}
		switch s.Protocol {
		case "sqs":
			v.Svc, v.Name = "sqs", arnLeaf(s.Endpoint)
			v.URL = "/sqs/" + v.Name
		case "lambda":
			v.Svc, v.Name = "lambda", strings.TrimPrefix(arnLeaf(s.Endpoint), "function:")
			v.URL = "/lambda/" + v.Name
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
	if err := c.be.Publish(r.Context(), topicARNOf(name), r.FormValue("message"), r.FormValue("subject"), parseMsgAttrs(r.FormValue("attrs"))); err != nil {
		c.fail(w, err)
		return
	}
	// Answer with a delivery receipt: who this message fanned out to, each
	// linked — the publish→verify loop closes without leaving the page.
	subs, _ := c.be.ListSubscriptions(r.Context(), topicARNOf(name))
	c.partial(w, "sns_receipt", map[string]any{"Topic": name, "Rcpts": subViews(subs)})
}

func (c *Console) snsSubscribe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("topic")
	if err := c.be.Subscribe(r.Context(), topicARNOf(name), r.FormValue("protocol"), r.FormValue("endpoint")); err != nil {
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
