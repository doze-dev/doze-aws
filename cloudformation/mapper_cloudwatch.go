package cloudformation

// AWS::CloudWatch::Alarm, AWS::CloudWatch::Dashboard and
// AWS::Logs::MetricFilter.
//
// These were in ignoredTypes with the reason "there is no CloudWatch locally",
// which stopped being true. An ignored resource still transpiles — it gets a
// ghost name and a synthesized ARN — so a CDK stack with an alarm deployed
// and the alarm simply never existed. Mapping them is the difference between
// a template that deploys and a template whose alarm can actually fire.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

func (m *mapper) alarm(name string, props map[string]any) error {
	// Composite alarms arrive as AWS::CloudWatch::CompositeAlarm, a different
	// type this does not claim — but a metric alarm using metric math arrives
	// here, and doze-aws evaluates a single metric only.
	if _, ok := props["Metrics"]; ok {
		return fmt.Errorf("alarm %q uses Metrics (metric math), which doze-aws does not "+
			"evaluate; a single MetricName/Namespace/Statistic alarm is supported, "+
			"which is what CDK emits for a threshold alarm", name)
	}
	if id := propStr(props, "ThresholdMetricId"); id != "" {
		return fmt.Errorf("alarm %q is an anomaly-detection alarm (ThresholdMetricId %s); "+
			"doze-aws has no trained band to compare against", name, id)
	}

	a := provision.Alarm{
		Description:        propStr(props, "AlarmDescription"),
		Namespace:          propStr(props, "Namespace"),
		MetricName:         propStr(props, "MetricName"),
		Unit:               propStr(props, "Unit"),
		Period:             propInt(props, "Period"),
		EvaluationPeriods:  propInt(props, "EvaluationPeriods"),
		DatapointsToAlarm:  propInt(props, "DatapointsToAlarm"),
		ComparisonOperator: propStr(props, "ComparisonOperator"),
		Threshold:          propFloat(props, "Threshold"),
		TreatMissingData:   propStr(props, "TreatMissingData"),
		Tags:               propTags(props),
	}
	// Statistic and ExtendedStatistic are separate template properties and
	// one Alarm.Statistic here; the apply splits them again by shape.
	a.Statistic = propStr(props, "Statistic")
	if ext := propStr(props, "ExtendedStatistic"); ext != "" {
		a.Statistic = ext
	}
	if raw, ok := props["ActionsEnabled"]; ok {
		enabled := propBoolOf(raw)
		a.ActionsEnabled = &enabled
	}
	for _, d := range propList(props, "Dimensions") {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		n, v := propStr(dm, "Name"), propStr(dm, "Value")
		if n == "" {
			continue
		}
		if a.Dimensions == nil {
			a.Dimensions = map[string]string{}
		}
		a.Dimensions[n] = v
	}
	a.AlarmActions = propStrList(props, "AlarmActions")
	a.OKActions = propStrList(props, "OKActions")
	a.InsufficientDataActions = propStrList(props, "InsufficientDataActions")

	if m.stack.Alarms == nil {
		m.stack.Alarms = map[string]provision.Alarm{}
	}
	m.stack.Alarms[name] = a
	return nil
}

func (m *mapper) dashboard(name string, props map[string]any) error {
	body := propStr(props, "DashboardBody")
	if body == "" {
		return fmt.Errorf("dashboard %q: DashboardBody is required", name)
	}
	if !json.Valid([]byte(body)) {
		return fmt.Errorf("dashboard %q: DashboardBody is not valid JSON", name)
	}
	if m.stack.Dashboards == nil {
		m.stack.Dashboards = map[string]provision.Dashboard{}
	}
	m.stack.Dashboards[name] = provision.Dashboard{Body: body}
	return nil
}

// metricFilter attaches a metric filter to the group it names, deferred like
// a subscription filter so the group may be one declared later in the same
// template.
func (m *mapper) metricFilter(name string, props map[string]any) error {
	group := propStr(props, "LogGroupName")
	if group == "" {
		return fmt.Errorf("metric filter %q: LogGroupName is required", name)
	}
	f := provision.MetricFilter{
		Name:    orDefault(propStr(props, "FilterName"), name),
		Pattern: propStr(props, "FilterPattern"),
	}
	for _, t := range propList(props, "MetricTransformations") {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		mt := provision.MetricTransformation{
			Namespace:  propStr(tm, "MetricNamespace"),
			MetricName: propStr(tm, "MetricName"),
			Value:      propStr(tm, "MetricValue"),
			Unit:       propStr(tm, "Unit"),
		}
		if mt.Namespace == "" || mt.MetricName == "" || mt.Value == "" {
			return fmt.Errorf("metric filter %q: a transformation needs MetricNamespace, "+
				"MetricName and MetricValue", name)
		}
		if raw, ok := tm["DefaultValue"]; ok {
			d := propFloatOf(raw)
			mt.Default = &d
		}
		if dims, ok := tm["Dimensions"].([]any); ok {
			for _, d := range dims {
				dm, ok := d.(map[string]any)
				if !ok {
					continue
				}
				if k := propStr(dm, "Key"); k != "" {
					if mt.Dimensions == nil {
						mt.Dimensions = map[string]string{}
					}
					mt.Dimensions[k] = propStr(dm, "Value")
				}
			}
		}
		f.Transformations = append(f.Transformations, mt)
	}
	if len(f.Transformations) == 0 {
		return fmt.Errorf("metric filter %q: MetricTransformations is required", name)
	}
	m.deferred = append(m.deferred, func() error {
		g := m.stack.LogGroups[group]
		g.MetricFilters = append(g.MetricFilters, f)
		m.stack.LogGroups[group] = g
		return nil
	})
	return nil
}

// propStrList reads a list of strings, resolving each through the same
// intrinsic handling every other property gets.
func propStrList(props map[string]any, key string) []string {
	raw := propList(props, key)
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s := fmt.Sprint(v); s != "" && !strings.Contains(s, "map[") {
			out = append(out, s)
		}
	}
	return out
}
