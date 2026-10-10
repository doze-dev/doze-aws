package provision

// SQS apply + export: queues, auto dead-letter queues, and attribute round-tripping.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ---- queues ----

func autoDLQName(name string, fifo bool) string {
	base := strings.TrimSuffix(name, ".fifo") + "-dlq"
	if fifo {
		base += ".fifo"
	}
	return base
}

func applyQueues(ctx context.Context, c *client, s *Stack, rep *Report) error {
	ensure := func(name string, q Queue) error {
		exists := true
		if _, err := c.sqs(ctx, "GetQueueUrl", map[string]any{"QueueName": name}); err != nil {
			if !notFound(err) {
				return err
			}
			exists = false
		}
		attrs := map[string]string{}
		if q.FIFO {
			attrs["FifoQueue"] = "true"
			if q.ContentDedup {
				attrs["ContentBasedDeduplication"] = "true"
			}
		}
		if q.Visibility > 0 {
			attrs["VisibilityTimeout"] = strconv.Itoa(q.Visibility)
		}
		if q.Delay > 0 {
			attrs["DelaySeconds"] = strconv.Itoa(q.Delay)
		}
		if q.Retention > 0 {
			attrs["MessageRetentionPeriod"] = strconv.Itoa(q.Retention)
		}
		if q.ReceiveWait > 0 {
			attrs["ReceiveMessageWaitTimeSeconds"] = strconv.Itoa(q.ReceiveWait)
		}
		if q.MaxSize > 0 {
			attrs["MaximumMessageSize"] = strconv.Itoa(q.MaxSize)
		}
		if !q.Policy.IsZero() {
			attrs["Policy"] = q.Policy.JSON
		}
		if q.DLQ != "" {
			dlq := q.DLQ
			if dlq == "auto" {
				dlq = autoDLQName(name, q.FIFO)
				// The auto DLQ mirrors the main queue's type.
				if err := ensureBareQueue(ctx, c, rep, dlq, q.FIFO); err != nil {
					return err
				}
			}
			maxr := q.MaxReceives
			if maxr <= 0 {
				maxr = 3
			}
			rp, _ := json.Marshal(map[string]string{
				"deadLetterTargetArn": queueARN(c.id, dlq),
				"maxReceiveCount":     strconv.Itoa(maxr),
			})
			attrs["RedrivePolicy"] = string(rp)
		}

		if !exists {
			in := map[string]any{"QueueName": name}
			if len(attrs) > 0 {
				in["Attributes"] = attrs
			}
			if len(q.Tags) > 0 {
				in["tags"] = q.Tags
			}
			if _, err := c.sqs(ctx, "CreateQueue", in); err != nil {
				return err
			}
			rep.add("created", "queue/"+name, "")
			return nil
		}
		// Converge mutable attributes on the existing queue (FIFO-ness is
		// create-time-only, so it is dropped here).
		delete(attrs, "FifoQueue")
		if len(attrs) > 0 {
			if _, err := c.sqs(ctx, "SetQueueAttributes", map[string]any{
				"QueueUrl": queueURL(c.id, name), "Attributes": attrs,
			}); err != nil {
				return err
			}
		}
		if len(q.Tags) > 0 {
			if _, err := c.sqs(ctx, "TagQueue", map[string]any{
				"QueueUrl": queueURL(c.id, name), "Tags": q.Tags,
			}); err != nil {
				return err
			}
		}
		if len(attrs) > 0 {
			rep.add("updated", "queue/"+name, "attributes")
		} else {
			rep.add("skipped", "queue/"+name, "exists")
		}
		return nil
	}
	for _, name := range queueApplyOrder(s.Queues) {
		if err := ensure(name, s.Queues[name]); err != nil {
			return fmt.Errorf("queue %q: %w", name, err)
		}
	}
	for _, qp := range s.QueuePolicies {
		name := qp.Queue
		if _, err := c.sqs(ctx, "GetQueueUrl", map[string]any{"QueueName": name}); err != nil {
			if notFound(err) {
				return fmt.Errorf("queue policy for %q: the queue does not exist", name)
			}
			return fmt.Errorf("queue policy for %q: %w", name, err)
		}
		if _, err := c.sqs(ctx, "SetQueueAttributes", map[string]any{
			"QueueUrl": queueURL(c.id, name), "Attributes": map[string]string{"Policy": qp.Policy.JSON},
		}); err != nil {
			return fmt.Errorf("queue policy for %q: %w", name, err)
		}
		rep.add("updated", "queue/"+name, "policy")
	}
	return nil
}

// queueApplyOrder lists the queues so that one is applied after the dead-letter
// queue its redrive policy names, when that queue is declared in the same
// spec. Name order is not that: orders sorts before orders-dlq, so a queue was
// created with a redrive target that did not exist yet and the stack rolled
// back (SQS-1). The redrive target is a dependency, so it is ordered as one.
//
// Ties and everything independent stay in name order, which keeps a run's
// report stable. A cycle (a dead-letters to b and b to a) cannot be ordered;
// its members are left in name order and the service says what it thinks of
// the policy, as it would for the same two queues made by hand.
func queueApplyOrder(queues map[string]Queue) []string {
	names := sortedNames(queues)
	target := func(n string) string {
		q := queues[n]
		if q.DLQ == "" || q.DLQ == "auto" || q.DLQ == n {
			return ""
		}
		if _, declared := queues[q.DLQ]; !declared {
			return ""
		}
		return q.DLQ
	}
	out := make([]string, 0, len(names))
	state := map[string]int{} // 0 not seen, 1 in progress, 2 placed
	var place func(n string)
	place = func(n string) {
		if state[n] != 0 {
			return
		}
		state[n] = 1
		if t := target(n); t != "" {
			place(t)
		}
		state[n] = 2
		out = append(out, n)
	}
	for _, n := range names {
		place(n)
	}
	return out
}

func ensureBareQueue(ctx context.Context, c *client, rep *Report, name string, fifo bool) error {
	if _, err := c.sqs(ctx, "GetQueueUrl", map[string]any{"QueueName": name}); err == nil {
		return nil
	} else if !notFound(err) {
		return err
	}
	in := map[string]any{"QueueName": name}
	if fifo {
		in["Attributes"] = map[string]string{"FifoQueue": "true"}
	}
	if _, err := c.sqs(ctx, "CreateQueue", in); err != nil {
		return err
	}
	rep.add("created", "queue/"+name, "auto dead-letter")
	return nil
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
			"QueueUrl": queueURL(c.id, name), "AttributeNames": []string{"All"},
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
			if p := a["Policy"]; p != "" {
				q.Policy = Doc{JSON: p}
			}
			if ms := atoiDefault(a["MaximumMessageSize"], 0); ms != 0 && ms != 1048576 {
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
		if out, err := c.sqs(ctx, "ListQueueTags", map[string]any{"QueueUrl": queueURL(c.id, name)}); err == nil {
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
