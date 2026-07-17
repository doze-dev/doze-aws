package stackfile

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
				"deadLetterTargetArn": queueARN(dlq),
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
				"QueueUrl": queueURL(name), "Attributes": attrs,
			}); err != nil {
				return err
			}
		}
		if len(q.Tags) > 0 {
			if _, err := c.sqs(ctx, "TagQueue", map[string]any{
				"QueueUrl": queueURL(name), "Tags": q.Tags,
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
	for _, name := range sortedNames(s.Queues) {
		if err := ensure(name, s.Queues[name]); err != nil {
			return fmt.Errorf("queue %q: %w", name, err)
		}
	}
	return nil
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
			q.ReceiveWait = atoiDefault(a["ReceiveMessageWaitTimeSeconds"], 0)
			if ms := atoiDefault(a["MaximumMessageSize"], 0); ms != 0 && ms != 262144 {
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
		if out, err := c.sqs(ctx, "ListQueueTags", map[string]any{"QueueUrl": queueURL(name)}); err == nil {
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
