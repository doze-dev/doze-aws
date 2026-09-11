package cloudformation

// Export of SNS topics and their subscriptions.

import (
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

func emitTopics(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Topics) {
		t := s.Topics[name]
		props := map[string]any{"TopicName": name}
		var subs []any
		for _, sub := range t.Subscriptions {
			m := map[string]any{}
			switch {
			case sub.Queue != "":
				m["Protocol"], m["Endpoint"] = "sqs", arnSub("sqs", sub.Queue)
			case sub.Lambda != "":
				m["Protocol"], m["Endpoint"] = "lambda", lambdaArnSub(sub.Lambda)
			case sub.HTTP != "":
				proto := "http"
				if strings.HasPrefix(sub.HTTP, "https") {
					proto = "https"
				}
				m["Protocol"], m["Endpoint"] = proto, sub.HTTP
			default:
				continue
			}
			putIf(m, "RawMessageDelivery", sub.Raw)
			if !sub.Filter.IsZero() {
				m["FilterPolicy"] = rawDoc(sub.Filter)
			}
			subs = append(subs, m)
		}
		if len(subs) > 0 {
			props["Subscription"] = subs
		}
		putTags(props, t.Tags)
		add("Topic", name, "AWS::SNS::Topic", props)
	}
}
