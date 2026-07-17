package sqs

// Queue attribute handling: parsing/validation of the SQS attribute map and
// the GetQueueAttributes/SetQueueAttributes views.

import (
	"encoding/json"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// applyAttrs folds an SQS attribute map into a queue definition.
func applyAttrs(q *Queue, attrs map[string]string) error {
	for k, v := range attrs {
		switch k {
		case "FifoQueue":
			q.FIFO = v == "true"
		case "ContentBasedDeduplication":
			q.ContentBasedDedup = v == "true"
		case "VisibilityTimeout":
			q.VisibilityTimeout = atoiDefault(v, q.VisibilityTimeout)
		case "DelaySeconds":
			q.DelaySeconds = atoiDefault(v, q.DelaySeconds)
		case "MessageRetentionPeriod":
			q.RetentionPeriod = atoiDefault(v, q.RetentionPeriod)
		case "MaximumMessageSize":
			q.MaxMessageSize = atoiDefault(v, q.MaxMessageSize)
		case "ReceiveMessageWaitTimeSeconds":
			q.WaitTimeSeconds = atoiDefault(v, q.WaitTimeSeconds)
		case "RedrivePolicy":
			if v == "" {
				q.DeadLetterTarget, q.MaxReceiveCount = "", 0
				continue
			}
			var rp struct {
				DeadLetterTargetArn string      `json:"deadLetterTargetArn"`
				MaxReceiveCount     json.Number `json:"maxReceiveCount"`
			}
			if err := json.Unmarshal([]byte(v), &rp); err != nil {
				return errInvalid("invalid RedrivePolicy: " + err.Error())
			}
			q.DeadLetterTarget = arnQueueName(rp.DeadLetterTargetArn)
			n, _ := rp.MaxReceiveCount.Int64()
			q.MaxReceiveCount = int(n)
		}
	}
	return nil
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
		out["VisibilityTimeout"] = strconv.Itoa(q.VisibilityTimeout)
		out["DelaySeconds"] = strconv.Itoa(q.DelaySeconds)
		out["MessageRetentionPeriod"] = strconv.Itoa(q.RetentionPeriod)
		out["MaximumMessageSize"] = strconv.Itoa(q.MaxMessageSize)
		out["ReceiveMessageWaitTimeSeconds"] = strconv.Itoa(q.WaitTimeSeconds)
		out["CreatedTimestamp"] = strconv.FormatInt(q.Created, 10)
		out["ApproximateNumberOfMessages"] = strconv.Itoa(visible)
		out["ApproximateNumberOfMessagesNotVisible"] = strconv.Itoa(inflight)
		out["QueueArn"] = queueARN(name)
		if q.FIFO {
			out["FifoQueue"] = "true"
			out["ContentBasedDeduplication"] = strconv.FormatBool(q.ContentBasedDedup)
		}
		if q.DeadLetterTarget != "" {
			rp, _ := json.Marshal(map[string]string{
				"deadLetterTargetArn": queueARN(q.DeadLetterTarget),
				"maxReceiveCount":     strconv.Itoa(q.MaxReceiveCount),
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
		if err := applyAttrs(q, attrs); err != nil {
			return err
		}
		return s.putQueue(tx, q)
	})
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// arnQueueName extracts the queue name (last colon segment) from an ARN.
func arnQueueName(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}
