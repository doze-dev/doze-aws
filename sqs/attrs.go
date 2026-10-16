package sqs

// Queue attribute handling: parsing/validation of the SQS attribute map and
// the GetQueueAttributes/SetQueueAttributes views.

import (
	"encoding/json"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
	"reflect"
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
	"MaximumMessageSize":            {1024, 1048576},
	"ReceiveMessageWaitTimeSeconds": {0, 20},
	// doc: "between 60 seconds (1 minute) and 86,400 seconds (24 hours)".
	"KmsDataKeyReusePeriodSeconds": {60, 86400},
}

// boolAttr reads one of the attributes whose documented values are "true" and
// "false". Anything else is refused, not read as false: FifoQueue "yes" used
// to make a standard queue and answer 200. Case is accepted either way, which
// is not established (recording queue).
func boolAttr(attr, v string) (bool, error) {
	// model: "Valid values are true and false", as written.
	switch v {
	case "true":
		return true, nil
	case "false", "":
		return false, nil
	}
	return false, errInvalidAttrValue(attr, "valid values are true and false")
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
type queueLookup struct {
	// byARN finds the queue an ARN names, if the ARN is this instance's: its
	// service, its Region and its account. An ARN elsewhere names no queue here,
	// whatever its last segment is (model: a dead-letter queue is one "in this
	// Amazon Web Services account in the same Region").
	byARN func(arn string) (*queue, error)
	// arnOf is the ARN of a queue here.
	arnOf func(name string) string
}

// applyAttrs folds an SQS attribute map into a queue definition.
func applyAttrs(q *queue, attrs map[string]string, lookup queueLookup) error {
	for k, v := range attrs {
		switch k {
		case "FifoQueue":
			b, err := boolAttr(k, v)
			if err != nil {
				return err
			}
			q.FIFO = b
		case "ContentBasedDeduplication":
			b, err := boolAttr(k, v)
			if err != nil {
				return err
			}
			q.ContentBasedDedup = b
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
		case "RedriveAllowPolicy":
			if v == "" {
				if q.Attrs != nil {
					delete(q.Attrs, k)
				}
				continue
			}
			if err := validRedriveAllowPolicy(v); err != nil {
				return err
			}
			if q.Attrs == nil {
				q.Attrs = map[string]string{}
			}
			q.Attrs[k] = v
		case "DeduplicationScope":
			if v != "" && v != "queue" && v != "messageGroup" {
				return errInvalidAttrValue(k, "must be queue or messageGroup")
			}
			if v == "" {
				delete(q.Attrs, k)
				continue
			}
			if q.Attrs == nil {
				q.Attrs = map[string]string{}
			}
			q.Attrs[k] = v
		case "FifoThroughputLimit":
			if v != "" && v != "perQueue" && v != "perMessageGroupId" {
				return errInvalidAttrValue(k, "must be perQueue or perMessageGroupId")
			}
			if v == "" {
				delete(q.Attrs, k)
				continue
			}
			if q.Attrs == nil {
				q.Attrs = map[string]string{}
			}
			q.Attrs[k] = v
		case "Policy":
			// Validated when it is stored. The guard parses it on every request
			// and, on failure, carries on with no policy, so a document that
			// does not mean anything was stored, read back as applied, and
			// ignored (SQS-8). An empty value clears it.
			if v == "" {
				if q.Attrs != nil {
					delete(q.Attrs, "Policy")
				}
				continue
			}
			if p := iampolicy.ValidateResourcePolicy(v); p != nil {
				return errInvalidAttrValue("Policy", p.Detail)
			}
			if q.Attrs == nil {
				q.Attrs = map[string]string{}
			}
			q.Attrs["Policy"] = v
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
			// Only what SQS has. Anything else used to be stored and handed
			// back by GetQueueAttributes, so a misspelt "VisibiltyTimeout"
			// was accepted, did nothing, and read back as if it had.
			if !storedAttrs[k] {
				return &apiError{Code: "InvalidAttributeName", Status: 400, SenderFault: true,
					Message: "Unknown Attribute " + k + "."}
			}
			switch k {
			case "KmsDataKeyReusePeriodSeconds":
				if v != "" {
					if _, err := atoiAttr(k, v); err != nil {
						return err
					}
				}
			case "SqsManagedSseEnabled":
				if _, err := boolAttr(k, v); err != nil {
					return err
				}
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
	// FIFO-only settings, and the one combination AWS refuses. Checked once
	// every attribute is applied, because the map is in no order and FifoQueue
	// may be among them.
	scope, limit := q.Attrs["DeduplicationScope"], q.Attrs["FifoThroughputLimit"]
	if (scope != "" || limit != "") && !q.FIFO {
		return errInvalidAttrValue("DeduplicationScope", "DeduplicationScope and FifoThroughputLimit apply only to FIFO queues")
	}
	// Checked only when the request names it, so a queue already holding the
	// value from before this rule can still have its other attributes set.
	if _, named := attrs["ContentBasedDeduplication"]; named && q.ContentBasedDedup && !q.FIFO {
		return errInvalidAttrValue("ContentBasedDeduplication", "ContentBasedDeduplication applies only to FIFO queues")
	}
	if limit == "perMessageGroupId" && scope != "messageGroup" {
		return errInvalidAttrValue("FifoThroughputLimit", "perMessageGroupId requires DeduplicationScope to be messageGroup")
	}
	return nil
}

// storedAttrs are the settable attributes that have no local behaviour of
// their own: kept as written and returned as written.
var storedAttrs = map[string]bool{
	"Policy": true, "RedriveAllowPolicy": true,
	"KmsMasterKeyId": true, "KmsDataKeyReusePeriodSeconds": true, "SqsManagedSseEnabled": true,
	"DeduplicationScope": true, "FifoThroughputLimit": true,
}

// computedAttrs are derived by the queue itself and are read-only.
var computedAttrs = map[string]bool{
	"QueueArn": true, "CreatedTimestamp": true, "LastModifiedTimestamp": true,
	"ApproximateNumberOfMessages": true, "ApproximateNumberOfMessagesNotVisible": true,
	"ApproximateNumberOfMessagesDelayed": true,
}

// Attributes returns the GetQueueAttributes view of a queue.
func (s *store) Attributes(name string) (map[string]string, error) {
	out := map[string]string{}
	err := s.db.View(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			return err
		}
		// Three states, not two. A message can be invisible for two different
		// reasons and SQS counts them separately: NotVisible means a consumer
		// has it and is working on it, Delayed means it has never been
		// delivered and is still serving its DelaySeconds.
		//
		// Lumping them together made a delayed message look like one being
		// processed, which is the opposite conclusion — anything scaling
		// consumers off NotVisible would scale up for work nobody is doing.
		// ReceiveCount is what tells them apart: a delayed message has never
		// been received.
		visible, inflight, delayed := 0, 0, 0
		now := s.now().UnixNano()
		if mb := tx.Bucket(msgBucket(name)); mb != nil {
			_ = mb.ForEach(func(_, raw []byte) error {
				var m message
				if json.Unmarshal(raw, &m) == nil {
					switch {
					case m.VisibleAt <= now:
						visible++
					case m.ReceiveCount == 0:
						delayed++
					default:
						inflight++
					}
				}
				return nil
			})
		}
		for k, v := range s.configuredAttrs(q) {
			out[k] = v
		}
		out["CreatedTimestamp"] = strconv.FormatInt(q.Created, 10)
		modified := q.Modified
		if modified == 0 {
			modified = q.Created
		}
		out["LastModifiedTimestamp"] = strconv.FormatInt(modified, 10)
		out["ApproximateNumberOfMessages"] = strconv.Itoa(visible)
		out["ApproximateNumberOfMessagesNotVisible"] = strconv.Itoa(inflight)
		out["ApproximateNumberOfMessagesDelayed"] = strconv.Itoa(delayed)
		out["QueueArn"] = s.queueARN(name)
		return nil
	})
	return out, err
}

// configuredAttrs is the part of a queue's attributes somebody chose: what
// CreateQueue and SetQueueAttributes can write, as GetQueueAttributes spells
// it. The counters, the timestamps and the ARN are not in it.
func (s *store) configuredAttrs(q *queue) map[string]string {
	out := map[string]string{}
	// Stored attributes first, so the computed ones below always win.
	for k, v := range q.Attrs {
		out[k] = v
	}
	out["VisibilityTimeout"] = strconv.Itoa(q.VisibilityTimeout)
	out["DelaySeconds"] = strconv.Itoa(q.DelaySeconds)
	out["MessageRetentionPeriod"] = strconv.Itoa(q.RetentionPeriod)
	out["MaximumMessageSize"] = strconv.Itoa(q.MaxMessageSize)
	out["ReceiveMessageWaitTimeSeconds"] = strconv.Itoa(q.WaitTimeSeconds)
	// Every queue is encrypted at rest with an SQS-owned key unless it was given a
	// KMS key of its own or SSE was switched off; AWS reports that rather than
	// leaving the attribute out, and Terraform polls for it after CreateQueue.
	if _, set := out["SqsManagedSseEnabled"]; !set {
		out["SqsManagedSseEnabled"] = strconv.FormatBool(out["KmsMasterKeyId"] == "")
	}
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
			"deadLetterTargetArn": s.queueARN(q.DeadLetterTarget),
			"maxReceiveCount":     q.MaxReceiveCount,
		})
		out["RedrivePolicy"] = string(rp)
	}
	return out
}

// attrDefaults are the values the model gives a default for and
// GetQueueAttributes does not report while the attribute is unset: asking for
// the default is the same ask as not asking.
var attrDefaults = map[string]string{
	"KmsDataKeyReusePeriodSeconds": "300", // model: "Default: 300 seconds"
}

// sameAttrValue reports whether two spellings of an attribute value mean the
// same thing. The JSON-valued attributes (RedrivePolicy, Policy) are compared
// as documents, and a count written "3" equals one written 3 — which is how
// the same policy reads back from GetQueueAttributes.
func sameAttrValue(a, b string) bool {
	if a == b {
		return true
	}
	var ja, jb any
	if json.Unmarshal([]byte(a), &ja) != nil || json.Unmarshal([]byte(b), &jb) != nil {
		return false
	}
	return reflect.DeepEqual(looseJSON(ja), looseJSON(jb))
}

func looseJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = looseJSON(e)
		}
	case []any:
		for i, e := range t {
			t[i] = looseJSON(e)
		}
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return v
}

func (s *store) SetAttributes(name string, attrs map[string]string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		q, err := s.getQueue(tx, name)
		if err != nil {
			return err
		}
		// model: FifoQueue "You can provide this attribute only during queue
		// creation. You can't change it for an existing queue." It was applied,
		// which turned a standard queue into one that refuses every send
		// without a group id, and a FIFO queue into one with no ordering.
		if _, named := attrs["FifoQueue"]; named {
			return &apiError{Code: "InvalidAttributeName", Status: 400, SenderFault: true,
				Message: "Unknown Attribute FifoQueue. It can be set only when the queue is created."}
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
func applyRedrive(q *queue, v string, lookup queueLookup) error {
	var rp struct {
		DeadLetterTargetArn string          `json:"deadLetterTargetArn"`
		MaxReceiveCount     json.RawMessage `json:"maxReceiveCount"`
	}
	if err := json.Unmarshal([]byte(v), &rp); err != nil {
		return errInvalidAttrValue("RedrivePolicy", "the value is not a JSON object: "+err.Error())
	}
	if strings.TrimSpace(rp.DeadLetterTargetArn) == "" {
		return errInvalidAttrValue("RedrivePolicy", "deadLetterTargetArn is required")
	}
	// AWS writes maxReceiveCount as a number and accepts it quoted; Terraform
	// and the console send the quoted form, so both have to work. Left out, it
	// is ten (model: maxReceiveCount "Default: 10").
	count := defMaxReceiveCount
	if raw := strings.Trim(strings.TrimSpace(string(rp.MaxReceiveCount)), `"`); raw != "" && raw != "null" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return errInvalidAttrValue("RedrivePolicy", "maxReceiveCount must be an integer")
		}
		count = n
	}
	if count < 1 || count > 1000 {
		return errInvalidAttrValue("RedrivePolicy", "maxReceiveCount must be between 1 and 1000")
	}

	target := arnQueueName(rp.DeadLetterTargetArn)
	if lookup.byARN != nil {
		dlq, err := lookup.byARN(rp.DeadLetterTargetArn)
		if err != nil {
			return errInvalidAttrValue("RedrivePolicy", "dead-letter target does not exist: "+rp.DeadLetterTargetArn)
		}
		if dlq.Name == q.Name {
			return errInvalidAttrValue("RedrivePolicy", "a queue cannot be its own dead-letter queue")
		}
		// A FIFO source needs a FIFO dead-letter queue and a standard source a
		// standard one: ordering and deduplication have to survive the move,
		// and a standard queue cannot carry them.
		if dlq.FIFO != q.FIFO {
			return errInvalidAttrValue("RedrivePolicy",
				"dead-letter queue must be the same type as the source queue (FIFO or standard)")
		}
		if !redriveAllowed(dlq, lookup.arnOf(q.Name)) {
			return errInvalidAttrValue("RedrivePolicy",
				"the dead-letter queue's RedriveAllowPolicy does not allow this queue to use it")
		}
	} else if target == q.Name {
		return errInvalidAttrValue("RedrivePolicy", "a queue cannot be its own dead-letter queue")
	}
	q.DeadLetterTarget, q.MaxReceiveCount = target, count
	return nil
}

// validRedriveAllowPolicy checks a dead-letter queue's RedriveAllowPolicy: a
// redrivePermission of allowAll, denyAll or byQueue, and for byQueue between one
// and ten source queue ARNs (doc: the queue attributes of CreateQueue).
func validRedriveAllowPolicy(v string) *apiError {
	var p struct {
		Permission string   `json:"redrivePermission"`
		Sources    []string `json:"sourceQueueArns"`
	}
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		return errInvalidAttrValue("RedriveAllowPolicy", "the value is not a JSON object")
	}
	switch p.Permission {
	case "allowAll", "denyAll":
		if len(p.Sources) > 0 {
			return errInvalidAttrValue("RedriveAllowPolicy", "sourceQueueArns is only for the byQueue permission")
		}
	case "byQueue":
		if len(p.Sources) == 0 || len(p.Sources) > 10 {
			return errInvalidAttrValue("RedriveAllowPolicy", "byQueue takes between 1 and 10 sourceQueueArns")
		}
	default:
		return errInvalidAttrValue("RedriveAllowPolicy", "redrivePermission must be allowAll, denyAll or byQueue")
	}
	return nil
}

// redriveAllowed reports whether a dead-letter queue's RedriveAllowPolicy lets
// the queue with this ARN use it. No policy is allowAll.
func redriveAllowed(dlq *queue, source string) bool {
	raw := dlq.Attrs["RedriveAllowPolicy"]
	if raw == "" {
		return true
	}
	var p struct {
		Permission string   `json:"redrivePermission"`
		Sources    []string `json:"sourceQueueArns"`
	}
	if json.Unmarshal([]byte(raw), &p) != nil {
		return true
	}
	switch p.Permission {
	case "denyAll":
		return false
	case "byQueue":
		for _, a := range p.Sources {
			if a == source {
				return true
			}
		}
		return false
	}
	return true
}

// arnQueueName extracts the queue name (last colon segment) from an ARN.
func arnQueueName(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}
