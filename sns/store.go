package sns

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/internal/lazybolt"
	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var (
	topicsBucket = []byte("topics")
	subsBucket   = []byte("subs")
)

// topic is a declared/created SNS topic.
type topic struct {
	ARN  string `json:"arn"`
	Name string `json:"name"`
	// Attrs round-trips SetTopicAttributes/CreateTopic attributes
	// (DisplayName, Policy, ...). Locally most have no behavior; they are
	// stored and returned faithfully.
	Attrs map[string]string `json:"attrs,omitempty"`
	Tags  map[string]string `json:"tags,omitempty"`
	// DataProtectionPolicy round-trips Put/GetDataProtectionPolicy.
	DataProtectionPolicy string `json:"data_protection_policy,omitempty"`
}

// subscription is one subscription to a topic.
type subscription struct {
	ARN          string `json:"arn"`
	TopicARN     string `json:"topic_arn"`
	Protocol     string `json:"protocol"` // sqs | http | https
	Endpoint     string `json:"endpoint"` // queue ARN/URL, or webhook URL
	RawDelivery  bool   `json:"raw_delivery"`
	FilterPolicy string `json:"filter_policy,omitempty"` // JSON
	Confirmed    bool   `json:"confirmed"`
	Token        string `json:"token,omitempty"` // pending-confirmation token
	// Extra round-trips subscription attributes with no local behavior
	// (FilterPolicyScope, RedrivePolicy, DeliveryPolicy, ...).
	Extra map[string]string `json:"extra,omitempty"`
}

// store is the bbolt-backed SNS state.
type store struct {
	db *lazybolt.DB
	id awsident.Identity // region and account ARNs are minted for; stamped by New
}

func newStore(db *lazybolt.DB) *store { return &store{db: db} }

// apiError is the shared AWS API error type; internal/awsquery renders it
// onto the wire in the Query error envelope.
type apiError = awshttp.APIError

func errNotFound(msg string) *apiError {
	return &apiError{Code: "NotFound", Status: 404, Message: msg, SenderFault: true}
}
func errInvalid(msg string) *apiError {
	return &apiError{Code: "InvalidParameter", Status: 400, Message: msg, SenderFault: true}
}

func (s *store) topicARN(name string) string { return s.id.ARN("sns", name) }

// ---- topics ----

func (s *store) CreateTopic(name string, attrs, tags map[string]string) (*topic, error) {
	if name == "" {
		return nil, errInvalid("topic name is required")
	}
	t := &topic{ARN: s.topicARN(name), Name: name}
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(topicsBucket)
		if err != nil {
			return err
		}
		// Idempotent: re-creating merges onto the existing definition.
		if raw := b.Get([]byte(t.ARN)); raw != nil {
			_ = json.Unmarshal(raw, t)
		}
		for k, v := range attrs {
			if t.Attrs == nil {
				t.Attrs = map[string]string{}
			}
			t.Attrs[k] = v
		}
		for k, v := range tags {
			if t.Tags == nil {
				t.Tags = map[string]string{}
			}
			t.Tags[k] = v
		}
		raw, _ := json.Marshal(t)
		return b.Put([]byte(t.ARN), raw)
	})
	return t, err
}

// GetTopic returns a topic by ARN.
func (s *store) GetTopic(arn string) (*topic, error) {
	var out *topic
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(topicsBucket)
		if b == nil {
			return errNotFound("topic does not exist: " + arn)
		}
		raw := b.Get([]byte(arn))
		if raw == nil {
			return errNotFound("topic does not exist: " + arn)
		}
		var t topic
		if err := json.Unmarshal(raw, &t); err != nil {
			return err
		}
		out = &t
		return nil
	})
	return out, err
}

// UpdateTopic applies fn to a topic and persists the result.
func (s *store) UpdateTopic(arn string, fn func(*topic)) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(topicsBucket)
		if b == nil {
			return errNotFound("topic does not exist: " + arn)
		}
		raw := b.Get([]byte(arn))
		if raw == nil {
			return errNotFound("topic does not exist: " + arn)
		}
		var t topic
		if err := json.Unmarshal(raw, &t); err != nil {
			return err
		}
		fn(&t)
		nraw, _ := json.Marshal(t)
		return b.Put([]byte(arn), nraw)
	})
}

func (s *store) DeleteTopic(arn string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket(topicsBucket); b != nil {
			_ = b.Delete([]byte(arn))
		}
		// Drop subscriptions of the topic too.
		if sb := tx.Bucket(subsBucket); sb != nil {
			var stale [][]byte
			_ = sb.ForEach(func(k, raw []byte) error {
				var sub subscription
				if json.Unmarshal(raw, &sub) == nil && sub.TopicARN == arn {
					stale = append(stale, append([]byte(nil), k...))
				}
				return nil
			})
			for _, k := range stale {
				_ = sb.Delete(k)
			}
		}
		return nil
	})
}

func (s *store) ListTopics() ([]topic, error) {
	var out []topic
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(topicsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var t topic
			if json.Unmarshal(raw, &t) == nil {
				out = append(out, t)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ARN < out[j].ARN })
	return out, err
}

func (s *store) topicExists(tx *bolt.Tx, arn string) bool {
	b := tx.Bucket(topicsBucket)
	return b != nil && b.Get([]byte(arn)) != nil
}

// TopicExists reports whether a topic ARN is known.
func (s *store) TopicExists(arn string) bool {
	ok := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		ok = s.topicExists(tx, arn)
		return nil
	})
	return ok
}

// ---- subscriptions ----

// Subscribe creates a subscription. SQS subscriptions are auto-confirmed; http(s)
// ones start pending with a confirmation token until ConfirmSubscription.
func (s *store) Subscribe(topicARN, protocol, endpoint string, attrs map[string]string) (*subscription, error) {
	sub := &subscription{
		ARN:       topicARN + ":" + newID(),
		TopicARN:  topicARN,
		Protocol:  protocol,
		Endpoint:  endpoint,
		Confirmed: protocol == "sqs" || protocol == "lambda", // SQS/Lambda need no confirmation handshake
	}
	applySubAttrs(sub, attrs)
	if !sub.Confirmed {
		sub.Token = newID()
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		if !s.topicExists(tx, topicARN) {
			return errNotFound("topic does not exist: " + topicARN)
		}
		b, err := tx.CreateBucketIfNotExists(subsBucket)
		if err != nil {
			return err
		}
		// Idempotent by (topic, protocol, endpoint): re-subscribing the same endpoint
		// reuses the existing subscription's ARN and just refreshes its attributes, so
		// a re-converge on every boot can't pile up identical subscriptions.
		_ = b.ForEach(func(_, raw []byte) error {
			var have subscription
			if json.Unmarshal(raw, &have) == nil &&
				have.TopicARN == topicARN && have.Protocol == protocol && have.Endpoint == endpoint {
				sub.ARN, sub.Token, sub.Confirmed = have.ARN, have.Token, have.Confirmed
			}
			return nil
		})
		raw, _ := json.Marshal(sub)
		return b.Put([]byte(sub.ARN), raw)
	})
	return sub, err
}

func applySubAttrs(sub *subscription, attrs map[string]string) {
	for k, v := range attrs {
		switch k {
		case "RawMessageDelivery":
			sub.RawDelivery = v == "true"
		case "FilterPolicy":
			sub.FilterPolicy = v
		default:
			if sub.Extra == nil {
				sub.Extra = map[string]string{}
			}
			sub.Extra[k] = v
		}
	}
}

// GetSubscription returns one subscription by ARN.
func (s *store) GetSubscription(arn string) (*subscription, error) {
	var sub *subscription
	err := s.db.View(func(tx *bolt.Tx) error {
		got, err := s.getSub(tx, arn)
		if err != nil {
			return err
		}
		sub = got
		return nil
	})
	return sub, err
}

func (s *store) getSub(tx *bolt.Tx, arn string) (*subscription, error) {
	b := tx.Bucket(subsBucket)
	if b == nil {
		return nil, errNotFound("subscription does not exist")
	}
	raw := b.Get([]byte(arn))
	if raw == nil {
		return nil, errNotFound("subscription does not exist: " + arn)
	}
	var sub subscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

func (s *store) putSub(tx *bolt.Tx, sub *subscription) error {
	b, err := tx.CreateBucketIfNotExists(subsBucket)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(sub)
	return b.Put([]byte(sub.ARN), raw)
}

func (s *store) Unsubscribe(arn string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket(subsBucket); b != nil {
			_ = b.Delete([]byte(arn))
		}
		return nil
	})
}

func (s *store) SetSubscriptionAttribute(arn, name, value string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		sub, err := s.getSub(tx, arn)
		if err != nil {
			return err
		}
		applySubAttrs(sub, map[string]string{name: value})
		return s.putSub(tx, sub)
	})
}

// ConfirmByToken confirms a pending http(s) subscription given its token.
func (s *store) ConfirmByToken(token string) (*subscription, error) {
	var out *subscription
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(subsBucket)
		if b == nil {
			return errNotFound("no such token")
		}
		var found *subscription
		_ = b.ForEach(func(_, raw []byte) error {
			var sub subscription
			if json.Unmarshal(raw, &sub) == nil && sub.Token == token {
				found = &sub
			}
			return nil
		})
		if found == nil {
			return errNotFound("invalid confirmation token")
		}
		found.Confirmed = true
		found.Token = ""
		out = found
		return s.putSub(tx, found)
	})
	return out, err
}

func (s *store) ListSubscriptions(topicFilter string) ([]subscription, error) {
	var out []subscription
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(subsBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var sub subscription
			if json.Unmarshal(raw, &sub) == nil && (topicFilter == "" || sub.TopicARN == topicFilter) {
				out = append(out, sub)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ARN < out[j].ARN })
	return out, err
}

// subsForTopic returns confirmed subscriptions of a topic (for delivery).
func (s *store) subsForTopic(arn string) ([]subscription, error) {
	all, err := s.ListSubscriptions(arn)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, sub := range all {
		if sub.Confirmed {
			out = append(out, sub)
		}
	}
	return out, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// subARNName returns the queue/topic name segment used in delivery, given an ARN
// or URL endpoint (the last ":"- or "/"-delimited segment).
func lastSegment(s string) string {
	if i := strings.LastIndexAny(s, ":/"); i >= 0 {
		return s[i+1:]
	}
	return s
}
