package cloudformation

// Export of CloudWatch Logs log groups and their subscription filters.

import (
	"github.com/doze-dev/doze-aws/provision"
)

func emitLogs(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.LogGroups) {
		g := s.LogGroups[name]
		props := map[string]any{"LogGroupName": name}
		putIfNum(props, "RetentionInDays", g.RetentionDays)
		putTags(props, g.Tags)
		add("LogGroup", name, "AWS::Logs::LogGroup", props)
		for _, sub := range g.Subscriptions {
			fp := map[string]any{
				"LogGroupName":  map[string]any{"Ref": logicalID("LogGroup", name)},
				"FilterName":    sub.Name,
				"FilterPattern": sub.Pattern,
			}
			switch {
			case sub.Lambda != "":
				fp["DestinationArn"] = lambdaArnSub(sub.Lambda)
			case sub.Kinesis != "":
				fp["DestinationArn"] = arnSub("kinesis", "stream/"+sub.Kinesis)
				putIfStr(fp, "Distribution", sub.Distribution)
			default:
				continue
			}
			add("Subscription", name+"-"+sub.Name, "AWS::Logs::SubscriptionFilter", fp)
		}
		emitMetricFilters(name, g.MetricFilters, add)
	}
}
