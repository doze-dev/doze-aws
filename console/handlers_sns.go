package console

import (
	"net/http"

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
	c.render(w, "sns_topics", map[string]any{"Topics": topics})
}

func (c *Console) snsCreateTopic(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.CreateTopic(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Topic “"+name+"” created")
	topics, _ := c.be.ListTopics(r.Context())
	c.partial(w, "sns_topic_list", map[string]any{"Topics": topics})
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
	c.render(w, "sns_topic", map[string]any{
		"Topic": name, "ARN": arn, "Attrs": attrs, "Subs": subs,
		"Queues": queues, "Functions": fns,
	})
}

func (c *Console) snsSubsPartial(w http.ResponseWriter, r *http.Request, name string) {
	arn := topicARNOf(name)
	subs, _ := c.be.ListSubscriptions(r.Context(), arn)
	queues, _ := c.be.ListQueues(r.Context())
	fns, _ := c.be.ListFunctions(r.Context())
	c.partial(w, "sns_subs", map[string]any{
		"Topic": name, "ARN": arn, "Subs": subs, "Queues": queues, "Functions": fns,
	})
}

func (c *Console) snsPublish(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("topic")
	if err := c.be.Publish(r.Context(), topicARNOf(name), r.FormValue("message"), r.FormValue("subject")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Message published")
	c.snsSubsPartial(w, r, name)
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
