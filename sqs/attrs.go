package sqs

// Queue attribute handling: parsing/validation of the SQS attribute map and
// the GetQueueAttributes/SetQueueAttributes views.

import (
	"encoding/json"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// attrRange is the interval SQS accepts for a numeric attribute. A value
// outside it is refused rather than clamped: accepting it here and having AWS
// refuse it on deploy is the divergence that costs the most, because it only
// shows up in the environment where it is expensive.
var attrRange = map[string][2]int{
	"VisibilityTimeout":             {0, 43200},
	"DelaySeconds":                  {0, 900},
	"MessageRetentionPeriod":        {60, 1209600},
	"MaximumMessageSize":            {1024, 262144},
	"ReceiveMessageWaitTimeSeconds": {0, 20},
}

// atoiAttr parses a numeric attribute strictly and range-checks it. The old
// behaviour — parse, ignore the error, keep the previous value — turned a
// typo into a silent no-op that answered 200.
func atoiAttr(attr, v string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, errInvalidAttrValue(attr, "must be an integer")
	}
	if r, ok := attrRange[attr]; ok && (n < r[0] || n > r[1]) {
		return 0, errInvalidAttrValue(attr,
			"must be between "+strconv.Itoa(r[0])+" and "+strconv.Itoa(r[1]))
	}
	return n, nil
}

// queueLookup resolves a queue by name so redrive can be validated against
// what actually exists. Passing nil skips the checks that need it, which is
// what the pure attribute tests do.
type queueLookup func(name string) (*Queue, error)

// applyAttrs folds an SQS attribute map into a queue definition.
func applyAttrs(q *Queue, attrs map[string]string, lookup queueLookup) error {
	for k, v := range attrs {
		switch k {
		case "FifoQueue":
			q.FIFO = v == "true"
		case "ContentBasedDeduplication":
			q.ContentBasedDedup = v == "true"
		case "VisibilityTimeout":
			n, err := atoiAttr(k, v)
			if err != nil {
				return err
			}
			q.VisibilityTimeout = n
		case "DelaySeconds":
			n, err := atoiAttr(k, v)
			if err != nil {
				return err
			}
			q.DelaySeconds = n
		case "MessageRetentionPeriod":
			n, err := atoiAttr(k, v)
			if err != nil {
				return err
			}
			q.RetentionPeriod = n
		case "MaximumMessageSize":
			n, err := atoiAttr(k, v)
			if err != nil {
				return err
			}
			q.MaxMessageSize = n
		case "ReceiveMessageWaitTimeSeconds":
			n, err := atoiAttr(k, v)
			if err != nil {
				return err
			}
			q.WaitTimeSeconds = n
		case "RedrivePolicy":
			if v == "" {
				q.DeadLetterTarget, q.MaxReceiveCount = "", 0
				continue
			}
			if err := applyRedrive(q, v, lookup); err != nil {
				return err
			}
		default:
			// Computed attributes are reported, never stored: accepting one
			// here would let a caller overwrite the queue's own bookkeeping.
			if computedAttrs[k] {
				continue
			}
			if q.Attrs == nil {
				q.Attrs = map[string]string{}
			}
			if v == "" {
				delete(q.Attrs, k)
				continue
			}
			q.Attrs[k] = v
		}
	}
	return nil
}

// computedAttrs are derived by the queue itself and are read-only.
var computedAttrs = map[string]bool{
	"QueueArn": true, "CreatedTimestamp": true, "LastModifiedTimestamp": true,
	"ApproximateNumberOfMessages": true, "ApproximateNumberOfMessagesNotVisible": true,
	"ApproximateNumberOfMessagesDelayed": true,
}

// Attributes returns the GetQueueAttributes view of a queue.
func (s *Store) Attributes(name string) (map[string]string, error) {
	out := map[string]string{}
	err := s.db.View(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			return err
		}
		visible, inflight := 0, 0
		now := s.now().UnixNano()
		if mb := tx.Bucket(msgBucket(name)); mb != nil {
			_ = mb.ForEach(func(_, raw []byte) error {
				var m Message
				if json.Unmarshal(raw, &m) == nil {
					if m.VisibleAt <= now {
						visible++
					} else {
						inflight++
					}
				}
				return nil
			})
		}
		// Stored attributes first, so the computed ones below always win.
		for k, v := range q.Attrs {
			out[k] = v
		}
		out["VisibilityTimeout"] = strconv.Itoa(q.VisibilityTimeout)
		out["DelaySeconds"] = strconv.Itoa(q.DelaySeconds)
		out["MessageRetentionPeriod"] = strconv.Itoa(q.RetentionPeriod)
		out["MaximumMessageSize"] = strconv.Itoa(q.MaxMessageSize)
		out["ReceiveMessageWaitTimeSeconds"] = strconv.Itoa(q.WaitTimeSeconds)
		out["CreatedTimestamp"] = strconv.FormatInt(q.Created, 10)
		modified := q.Modified
		if modified == 0 {
			modified = q.Created
		}
		out["LastModifiedTimestamp"] = strconv.FormatInt(modified, 10)
		out["ApproximateNumberOfMessages"] = strconv.Itoa(visible)
		out["ApproximateNumberOfMessagesNotVisible"] = strconv.Itoa(inflight)
		out["QueueArn"] = queueARN(name)
		if q.FIFO {
			out["FifoQueue"] = "true"
			out["ContentBasedDeduplication"] = strconv.FormatBool(q.ContentBasedDedup)
		}
		if q.DeadLetterTarget != "" {
			// maxReceiveCount is a NUMBER in AWS's response, not a string.
			// Terraform sets the policy and then polls GetQueueAttributes until
			// what it reads back equals what it wrote — a stringified count
			// never compares equal, so the resource never converges.
			rp, _ := json.Marshal(map[string]any{
				"deadLetterTargetArn": queueARN(q.DeadLetterTarget),
				"maxReceiveCount":     q.MaxReceiveCount,
			})
			out["RedrivePolicy"] = string(rp)
		}
		return nil
	})
	return out, err
}

func (s *Store) SetAttributes(name string, attrs map[string]string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			return err
		}
		if err := applyAttrs(q, attrs, s.lookupIn(tx)); err != nil {
			return err
		}
		q.Modified = s.now().Unix()
		return s.putQueue(tx, q)
	})
}

// applyRedrive validates a RedrivePolicy before storing it. Every check here
// is one SQS performs at SetQueueAttributes time, and every one of them was a
// way to configure a dead-letter queue that this store accepted and AWS would
// not — which is the expensive direction, because the deploy is where you find
// out.
//
// The failure was also silent rather than loud: a target that does not exist
// leaves moveToDLQ correctly declining to drop the message, so the queue
// redelivers forever, the dead-letter queue never fills, and
// GetQueueAttributes echoes the policy back as though it had taken.
func applyRedrive(q *Queue, v string, lookup queueLookup) error {
	var rp struct {
		DeadLetterTargetArn string          `json:"deadLetterTargetArn"`
		MaxReceiveCount     json.RawMessage `json:"maxReceiveCount"`
	}
	if err := json.Unmarshal([]byte(v), &rp); err != nil {
		return errInvalid("invalid RedrivePolicy: " + err.Error())
	}
	if strings.TrimSpace(rp.DeadLetterTargetArn) == "" {
		return errInvalidAttrValue("RedrivePolicy", "deadLetterTargetArn is required")
	}
	// AWS writes maxReceiveCount as a number and accepts it quoted; Terraform
	// and the console send the quoted form, so both have to work.
	count, err := strconv.Atoi(strings.Trim(strings.TrimSpace(string(rp.MaxReceiveCount)), `"`))
	if err != nil {
		return errInvalidAttrValue("RedrivePolicy", "maxReceiveCount must be an integer")
	}
	if count < 1 || count > 1000 {
		return errInvalidAttrValue("RedrivePolicy", "maxReceiveCount must be between 1 and 1000")
	}

	target := arnQueueName(rp.DeadLetterTargetArn)
	if target == q.Name {
		return errInvalidAttrValue("RedrivePolicy", "a queue cannot be its own dead-letter queue")
	}
	if lookup != nil {
		dlq, err := lookup(target)
		if err != nil {
			return errInvalidAttrValue("RedrivePolicy", "dead-letter target does not exist: "+target)
		}
		// A FIFO source needs a FIFO dead-letter queue and a standard source a
		// standard one: ordering and deduplication have to survive the move,
		// and a standard queue cannot carry them.
		if dlq.FIFO != q.FIFO {
			return errInvalidAttrValue("RedrivePolicy",
				"dead-letter queue must be the same type as the source queue (FIFO or standard)")
		}
	}
	q.DeadLetterTarget, q.MaxReceiveCount = target, count
	return nil
}

// arnQueueName extracts the queue name (last colon segment) from an ARN.
func arnQueueName(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}
