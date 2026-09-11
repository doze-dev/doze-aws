package cloudformation

// Export of EventBridge rules: the pattern or schedule, and each target with its input transformer.

import (
	"fmt"

	"github.com/doze-dev/doze-aws/provision"
)

func emitRules(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Rules) {
		r := s.Rules[name]
		props := map[string]any{"Name": name}
		putIfStr(props, "EventBusName", r.Bus)
		putIfStr(props, "ScheduleExpression", r.Schedule)
		if !r.Pattern.IsZero() {
			props["EventPattern"] = rawDoc(r.Pattern)
		}
		if r.Enabled != nil {
			props["State"] = map[bool]string{true: "ENABLED", false: "DISABLED"}[*r.Enabled]
		}
		var targets []any
		for i, t := range r.Targets {
			m := map[string]any{"Id": fmt.Sprint(i + 1)}
			switch {
			case t.Queue != "":
				m["Arn"] = arnSub("sqs", t.Queue)
			case t.Topic != "":
				m["Arn"] = arnSub("sns", t.Topic)
			case t.Lambda != "":
				m["Arn"] = lambdaArnSub(t.Lambda)
			case t.APIDestination != "":
				apiDestinationTargetProps(m, t)
			default:
				continue
			}
			putIfStr(m, "InputPath", t.InputPath)
			if !t.Input.IsZero() {
				m["Input"] = t.Input.JSON
			}
			if t.Template != "" {
				it := map[string]any{"InputTemplate": t.Template}
				if len(t.Paths) > 0 {
					it["InputPathsMap"] = t.Paths
				}
				m["InputTransformer"] = it
			}
			targets = append(targets, m)
		}
		if len(targets) > 0 {
			props["Targets"] = targets
		}
		add("Rule", name, "AWS::Events::Rule", props)
	}
}
