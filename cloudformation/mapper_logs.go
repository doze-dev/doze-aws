package cloudformation

// AWS::Logs::SubscriptionFilter: a filter on a log group, forwarding to a
// Lambda function or a Kinesis stream. Resolved after every other resource,
// so the group it names may be one the template declares or one only Lambda
// would create — in which case the stack declares it too, so the filter has
// a group to land on before the function's first line.

import (
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

func (m *mapper) logSubscription(name string, props map[string]any) error {
	group := propStr(props, "LogGroupName")
	if group == "" {
		return fmt.Errorf("LogGroupName is required")
	}
	sub := provision.LogSubscription{
		Name:         orDefault(propStr(props, "FilterName"), name),
		Pattern:      propStr(props, "FilterPattern"),
		Distribution: propStr(props, "Distribution"),
	}
	dest := propStr(props, "DestinationArn")
	switch {
	case strings.Contains(dest, ":lambda:"):
		sub.Lambda = nameFromARN(dest)
	case strings.Contains(dest, ":kinesis:"):
		sub.Kinesis = nameFromARN(dest)
	case strings.Contains(dest, ":firehose:"):
		return fmt.Errorf("DestinationArn %q: Firehose delivery streams do not exist locally; subscribe a Lambda function or a Kinesis stream", dest)
	default:
		return fmt.Errorf("DestinationArn %q: doze-aws forwards to Lambda functions and Kinesis streams only", dest)
	}
	m.deferred = append(m.deferred, func() error {
		g := m.stack.LogGroups[group]
		g.Subscriptions = append(g.Subscriptions, sub)
		m.stack.LogGroups[group] = g
		return nil
	})
	return nil
}
