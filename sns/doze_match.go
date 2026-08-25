package sns

// DozeMatchSubscriptions answers "if I published this, who would actually get
// it" — and, for anyone who would not, which part of their filter policy said
// no.
//
// It is a doze-only action, in the same spirit as SQS's DozePeek: something a
// local emulator can answer that the real service has no API for. The console's
// publish receipt used to list every subscriber as a recipient without
// evaluating a single filter policy, so three of four filtered out still
// reported four.
//
// It calls matchFilter, NOT the exported MatchPolicy. MatchPolicy takes
// map[string]string and stamps every attribute as DataType "String", which is
// lossy in a way that matters here: attrMatchValue renders a Number as a JSON
// number and a String.Array as an array, so a policy like
// {"price":[{"numeric":[">",100]}]} matches on delivery and would NOT match
// through MatchPolicy. A preview that disagrees with delivery is worse than no
// preview, so this parses the same MessageAttributes block Publish parses and
// evaluates the same predicate deliver() evaluates.

import (
	"context"
	"encoding/json"
	"net/url"
)

type matchReason struct {
	Key      string `xml:"Key"`      // the policy attribute that rejected the message
	Expected string `xml:"Expected"` // that key's condition, verbatim JSON
	Actual   string `xml:"Actual"`   // the message's value for it, "" when absent
	Present  bool   `xml:"Present"`
}

type matchMember struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
	Protocol        string `xml:"Protocol"`
	Endpoint        string `xml:"Endpoint"`
	Matched         bool   `xml:"Matched"`
	Pending         bool   `xml:"Pending"`
	Reasons         []matchReason `xml:"Reasons>member,omitempty"`
}

func (srv *Server) dozeMatchSubscriptions(ctx context.Context, form url.Values, _ string) (any, *apiError) {
	topicARN := form.Get("TopicArn")
	if topicARN == "" {
		return nil, errInvalid("TopicArn is required")
	}
	subs, err := srv.store.subsForTopic(topicARN)
	if err != nil {
		return nil, asErr(err)
	}
	attrs := messageAttributes(form)

	out := struct {
		Members []matchMember `xml:"Subscriptions>member"`
	}{}
	for _, sub := range subs {
		m := matchMember{
			SubscriptionArn: sub.ARN,
			Protocol:        sub.Protocol,
			Endpoint:        sub.Endpoint,
			// An unconfirmed subscription is not a recipient. The old receipt
			// counted these too, which inflated the number twice over.
			Pending: !isConfirmed(sub.ARN),
			Matched: matchFilter(sub.FilterPolicy, attrs),
		}
		if !m.Matched {
			m.Reasons = rejectionReasons(sub.FilterPolicy, attrs)
		}
		out.Members = append(out.Members, m)
	}
	return out, nil
}

// rejectionReasons names the policy keys that refused the message.
//
// This is a sound decomposition rather than a guess. SNS filter semantics are
// attributes AND'd, conditions OR'd within an attribute — so evaluating each
// top-level key on its own is exactly equivalent to evaluating the whole, and
// the keys that fail alone are precisely the keys that failed together.
//
// $or is the exception: it is a disjunction across keys and cannot be split
// that way, so it is reported whole rather than mis-attributed to one of its
// branches.
func rejectionReasons(policyJSON string, attrs map[string]Attr) []matchReason {
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(policyJSON), &doc) != nil {
		return nil
	}
	var out []matchReason
	for key, cond := range doc {
		single, err := json.Marshal(map[string]json.RawMessage{key: cond})
		if err != nil {
			continue
		}
		if matchFilter(string(single), attrs) {
			continue
		}
		r := matchReason{Key: key, Expected: string(cond)}
		if key == "$or" {
			out = append(out, r)
			continue
		}
		if a, ok := attrs[key]; ok {
			r.Present, r.Actual = true, a.StringValue
		}
		out = append(out, r)
	}
	return out
}

// isConfirmed reports whether a subscription ARN is a real ARN rather than the
// PendingConfirmation placeholder SNS returns until the endpoint confirms.
func isConfirmed(arn string) bool { return len(arn) > 4 && arn[:4] == "arn:" }
