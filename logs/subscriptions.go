package logs

// Subscription filters: a log group forwards the events that match a filter
// pattern to a Lambda function or a Kinesis stream, in the gzip envelope AWS
// uses (fanout.go). At most two per group, as on AWS. Firehose and
// cross-account destinations are refused by name.

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

var bucketSubscriptions = []byte("subscriptions") // group \x00 name → Subscription

// maxSubscriptionsPerGroup is AWS's limit.
const maxSubscriptionsPerGroup = 2

// Subscription is one subscription filter on a group.
type Subscription struct {
	Group        string `json:"group"`
	Name         string `json:"name"`
	Pattern      string `json:"pattern"`
	Destination  string `json:"destination"` // lambda function or kinesis stream ARN
	RoleARN      string `json:"role_arn,omitempty"`
	Distribution string `json:"distribution,omitempty"` // Random | ByLogStream (Kinesis)
	CreatedMs    int64  `json:"created"`
}

func subscriptionKey(group, name string) []byte { return []byte(group + "\x00" + name) }

// ---- store ----

func (s *Store) PutSubscription(sub Subscription) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketGroups).Get([]byte(sub.Group)) == nil {
			return ErrNoGroup
		}
		b := tx.Bucket(bucketSubscriptions)
		if b.Get(subscriptionKey(sub.Group, sub.Name)) == nil {
			n := 0
			c := b.Cursor()
			prefix := subscriptionKey(sub.Group, "")
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				n++
			}
			if n >= maxSubscriptionsPerGroup {
				return awshttp.Errf(400, "LimitExceededException", "Resource limit exceeded.")
			}
		}
		raw, _ := json.Marshal(sub)
		return b.Put(subscriptionKey(sub.Group, sub.Name), raw)
	})
}

func (s *Store) DeleteSubscription(group, name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSubscriptions)
		if b.Get(subscriptionKey(group, name)) == nil {
			return errNotFound("The specified subscription filter does not exist.")
		}
		return b.Delete(subscriptionKey(group, name))
	})
}

// Subscriptions lists a group's filters by name.
func (s *Store) Subscriptions(group string) ([]Subscription, error) {
	var out []Subscription
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketSubscriptions).Cursor()
		prefix := subscriptionKey(group, "")
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			var sub Subscription
			if json.Unmarshal(v, &sub) == nil {
				out = append(out, sub)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// deleteGroupSubscriptions runs inside DeleteGroup's transaction.
func deleteGroupSubscriptions(tx *bolt.Tx, group string) error {
	b := tx.Bucket(bucketSubscriptions)
	c := b.Cursor()
	prefix := subscriptionKey(group, "")
	var keys [][]byte
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		keys = append(keys, append([]byte(nil), k...))
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

// ---- handlers ----

func (s *Server) putSubscriptionFilter(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	pattern := awsjson.Str(p, "filterPattern")
	if _, err := compile(pattern); err != nil {
		return nil, errParam("Invalid filter pattern: %v", err)
	}
	dest := awsjson.Str(p, "destinationArn")
	if aerr := s.checkDestination(g.Name, dest); aerr != nil {
		return nil, aerr
	}
	sub := Subscription{
		Group: g.Name, Name: awsjson.Str(p, "filterName"), Pattern: pattern, Destination: dest,
		RoleARN: awsjson.Str(p, "roleArn"), Distribution: awsjson.Str(p, "distribution"),
		CreatedMs: s.store.now(),
	}
	if err := s.store.PutSubscription(sub); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	s.fan.forget(g.Name)
	return map[string]any{}, nil
}

// checkDestination accepts a Lambda function or a Kinesis stream and names
// what it refuses: Firehose, another account's destination, and a function
// subscribing to its own log group (which would loop).
func (s *Server) checkDestination(group, arn string) *awshttp.APIError {
	switch {
	case strings.Contains(arn, ":lambda:"):
		fn := arn[strings.LastIndex(arn, ":")+1:]
		if group == "/aws/lambda/"+fn {
			return errParam("a function cannot subscribe to its own log group: every line it wrote would invoke it again")
		}
		return nil
	case strings.Contains(arn, ":kinesis:"):
		return nil
	case strings.Contains(arn, ":firehose:"):
		return errParam("Firehose delivery streams do not exist locally; subscribe a Lambda function or a Kinesis stream")
	case strings.Contains(arn, ":logs:") && strings.Contains(arn, ":destination:"):
		return errParam("cross-account Logs destinations do not exist locally; subscribe a Lambda function or a Kinesis stream directly")
	}
	return errParam("destinationArn must be a Lambda function or a Kinesis stream ARN")
}

func (s *Server) deleteSubscriptionFilter(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	if err := s.store.DeleteSubscription(g.Name, awsjson.Str(p, "filterName")); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	s.fan.forget(g.Name)
	return map[string]any{}, nil
}

func (s *Server) describeSubscriptionFilters(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	subs, err := s.store.Subscriptions(g.Name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	prefix := awsjson.Str(p, "filterNamePrefix")
	items := []map[string]any{}
	for _, sub := range subs {
		if prefix != "" && !strings.HasPrefix(sub.Name, prefix) {
			continue
		}
		items = append(items, subscriptionView(sub))
	}
	return pageByName(items, "subscriptionFilters", "filterName", awsjson.Str(p, "nextToken"), awsjson.Int(p, "limit", 50)), nil
}

func subscriptionView(sub Subscription) map[string]any {
	v := map[string]any{
		"filterName": sub.Name, "logGroupName": sub.Group, "filterPattern": sub.Pattern,
		"destinationArn": sub.Destination, "creationTime": sub.CreatedMs,
	}
	if sub.RoleARN != "" {
		v["roleArn"] = sub.RoleARN
	}
	if sub.Distribution != "" {
		v["distribution"] = sub.Distribution
	}
	return v
}
