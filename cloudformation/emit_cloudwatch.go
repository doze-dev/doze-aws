package cloudformation

// Export of CloudWatch alarms and dashboards back to a template.

import (
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

func emitCloudWatch(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Alarms) {
		a := s.Alarms[name]
		props := map[string]any{
			"AlarmName":          name,
			"Namespace":          a.Namespace,
			"MetricName":         a.MetricName,
			"ComparisonOperator": a.ComparisonOperator,
			"Threshold":          a.Threshold,
		}
		putIfNum(props, "Period", a.Period)
		putIfNum(props, "EvaluationPeriods", a.EvaluationPeriods)
		putIfNum(props, "DatapointsToAlarm", a.DatapointsToAlarm)
		putIfStr(props, "AlarmDescription", a.Description)
		putIfStr(props, "Unit", a.Unit)
		putIfStr(props, "TreatMissingData", a.TreatMissingData)
		// The template splits what the IR keeps as one field, the same way
		// the apply does: a percentile is ExtendedStatistic.
		if strings.HasPrefix(a.Statistic, "p") {
			props["ExtendedStatistic"] = a.Statistic
		} else {
			putIfStr(props, "Statistic", a.Statistic)
		}
		if a.ActionsEnabled != nil {
			props["ActionsEnabled"] = *a.ActionsEnabled
		}
		if len(a.Dimensions) > 0 {
			dims := make([]any, 0, len(a.Dimensions))
			for _, k := range sortedNames(a.Dimensions) {
				dims = append(dims, map[string]any{"Name": k, "Value": a.Dimensions[k]})
			}
			props["Dimensions"] = dims
		}
		for key, arns := range map[string][]string{
			"AlarmActions":            a.AlarmActions,
			"OKActions":               a.OKActions,
			"InsufficientDataActions": a.InsufficientDataActions,
		} {
			if len(arns) > 0 {
				props[key] = arns
			}
		}
		putTags(props, a.Tags)
		add("Alarm", name, "AWS::CloudWatch::Alarm", props)
	}

	for _, name := range sortedNames(s.Dashboards) {
		add("Dashboard", name, "AWS::CloudWatch::Dashboard", map[string]any{
			"DashboardName": name,
			"DashboardBody": s.Dashboards[name].Body,
		})
	}
}

// emitMetricFilters is called from emitLogs, beside the subscription filters:
// both hang off a group and both need the group's logical ID.
func emitMetricFilters(group string, filters []provision.MetricFilter,
	add func(prefix, name, typ string, props map[string]any)) {
	for _, f := range filters {
		transforms := make([]any, 0, len(f.Transformations))
		for _, t := range f.Transformations {
			item := map[string]any{
				"MetricNamespace": t.Namespace,
				"MetricName":      t.MetricName,
				"MetricValue":     t.Value,
			}
			if t.Default != nil {
				item["DefaultValue"] = *t.Default
			}
			putIfStr(item, "Unit", t.Unit)
			if len(t.Dimensions) > 0 {
				dims := make([]any, 0, len(t.Dimensions))
				for _, k := range sortedNames(t.Dimensions) {
					dims = append(dims, map[string]any{"Key": k, "Value": t.Dimensions[k]})
				}
				item["Dimensions"] = dims
			}
			transforms = append(transforms, item)
		}
		add("MetricFilter", group+"-"+f.Name, "AWS::Logs::MetricFilter", map[string]any{
			"LogGroupName":          map[string]any{"Ref": logicalID("LogGroup", group)},
			"FilterName":            f.Name,
			"FilterPattern":         f.Pattern,
			"MetricTransformations": transforms,
		})
	}
}
