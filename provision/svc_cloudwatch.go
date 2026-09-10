package provision

// CloudWatch apply: metric alarms and dashboards.
//
// An alarm is applied after the resources whose metrics it watches and after
// the topics it notifies, because PutMetricAlarm refuses an action it cannot
// deliver — an alarm naming a topic that does not exist yet would fail the
// deploy rather than silently point at nothing.
//
// PutMetricAlarm is an upsert by name and keeps the alarm's state across a
// re-put, so applying a stack twice does not reset an alarm that is currently
// firing. That makes the phase convergent for free.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// cw posts a CloudWatch request. The signing name is `monitoring` and the
// JSON 1.0 target prefix is `GraniteServiceVersion20100801` — two different
// strings for one service, and conflating them is the easy mistake here.
func (c *client) cw(ctx context.Context, action string, in any) ([]byte, error) {
	return c.jsonTarget(ctx, "GraniteServiceVersion20100801."+action,
		"application/x-amz-json-1.0", in)
}

func applyAlarms(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Alarms) {
		a := s.Alarms[name]
		in, err := alarmRequest(name, a)
		if err != nil {
			return err
		}
		if _, err := c.cw(ctx, "PutMetricAlarm", in); err != nil {
			return fmt.Errorf("alarm %q: %w", name, err)
		}
		// An upsert: there is no create/skip distinction to report, because
		// PutMetricAlarm neither fails on an existing alarm nor resets it.
		rep.add("updated", "alarm/"+name, "put (upsert)")
	}
	for _, name := range sortedNames(s.Dashboards) {
		d := s.Dashboards[name]
		if _, err := c.cw(ctx, "PutDashboard", map[string]any{
			"DashboardName": name, "DashboardBody": d.Body,
		}); err != nil {
			return fmt.Errorf("dashboard %q: %w", name, err)
		}
		rep.add("updated", "dashboard/"+name, "put (upsert)")
	}
	return nil
}

// alarmRequest is the PutMetricAlarm body for one alarm.
func alarmRequest(name string, a Alarm) (map[string]any, error) {
	if a.Namespace == "" || a.MetricName == "" {
		return nil, fmt.Errorf("alarm %q: Namespace and MetricName are required", name)
	}
	if a.ComparisonOperator == "" {
		return nil, fmt.Errorf("alarm %q: ComparisonOperator is required", name)
	}
	in := map[string]any{
		"AlarmName":          name,
		"Namespace":          a.Namespace,
		"MetricName":         a.MetricName,
		"ComparisonOperator": a.ComparisonOperator,
		"Threshold":          a.Threshold,
		"Period":             a.Period,
		"EvaluationPeriods":  a.EvaluationPeriods,
	}
	// A percentile is an ExtendedStatistic; the five named statistics are a
	// Statistic. Sending a pNN as Statistic is refused, which is AWS's split
	// and the service's.
	if strings.HasPrefix(a.Statistic, "p") {
		in["ExtendedStatistic"] = a.Statistic
	} else if a.Statistic != "" {
		in["Statistic"] = a.Statistic
	}
	for key, v := range map[string]string{
		"AlarmDescription": a.Description, "Unit": a.Unit,
		"TreatMissingData": a.TreatMissingData,
	} {
		if v != "" {
			in[key] = v
		}
	}
	if a.DatapointsToAlarm > 0 {
		in["DatapointsToAlarm"] = a.DatapointsToAlarm
	}
	if a.ActionsEnabled != nil {
		in["ActionsEnabled"] = *a.ActionsEnabled
	}
	if dims := dimensionList(a.Dimensions); len(dims) > 0 {
		in["Dimensions"] = dims
	}
	for key, arns := range map[string][]string{
		"AlarmActions":            a.AlarmActions,
		"OKActions":               a.OKActions,
		"InsufficientDataActions": a.InsufficientDataActions,
	} {
		if len(arns) > 0 {
			in[key] = arns
		}
	}
	if len(a.Tags) > 0 {
		// CloudWatch takes Key/Value, which is the shared helper's job.
		in["Tags"] = tagList(a.Tags, "Key", "Value")
	}
	return in, nil
}

// dimensionList renders the dimension map as CloudWatch's list of Name/Value
// structures, sorted so a repeated apply sends the same bytes.
func dimensionList(dims map[string]string) []any {
	out := make([]any, 0, len(dims))
	for _, k := range sortedNames(dims) {
		out = append(out, map[string]any{"Name": k, "Value": dims[k]})
	}
	return out
}

func exportAlarms(ctx context.Context, c *client, s *Stack) error {
	out, err := c.cw(ctx, "DescribeAlarms", map[string]any{})
	if err != nil {
		return fmt.Errorf("describe alarms: %w", err)
	}
	var list struct {
		MetricAlarms []struct {
			AlarmName, AlarmDescription  string
			Namespace, MetricName        string
			Statistic, ExtendedStatistic string
			Unit, ComparisonOperator     string
			TreatMissingData             string
			Period, EvaluationPeriods    int
			DatapointsToAlarm            int
			Threshold                    float64
			ActionsEnabled               bool
			AlarmActions, OKActions      []string
			InsufficientDataActions      []string
			Dimensions                   []struct{ Name, Value string }
		} `json:"MetricAlarms"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return fmt.Errorf("describe alarms: %w", err)
	}
	for _, m := range list.MetricAlarms {
		a := Alarm{
			Description: m.AlarmDescription, Namespace: m.Namespace, MetricName: m.MetricName,
			Statistic: orDefaultStr(m.ExtendedStatistic, m.Statistic),
			Unit:      m.Unit, Period: m.Period, EvaluationPeriods: m.EvaluationPeriods,
			DatapointsToAlarm: m.DatapointsToAlarm, ComparisonOperator: m.ComparisonOperator,
			Threshold: m.Threshold, TreatMissingData: m.TreatMissingData,
			AlarmActions: m.AlarmActions, OKActions: m.OKActions,
			InsufficientDataActions: m.InsufficientDataActions,
		}
		// Only when false: true is the default, and exporting it would put a
		// line in every stack file that says nothing.
		if !m.ActionsEnabled {
			off := false
			a.ActionsEnabled = &off
		}
		if len(m.Dimensions) > 0 {
			a.Dimensions = map[string]string{}
			for _, d := range m.Dimensions {
				a.Dimensions[d.Name] = d.Value
			}
		}
		if s.Alarms == nil {
			s.Alarms = map[string]Alarm{}
		}
		s.Alarms[m.AlarmName] = a
	}
	return exportDashboards(ctx, c, s)
}

func exportDashboards(ctx context.Context, c *client, s *Stack) error {
	out, err := c.cw(ctx, "ListDashboards", map[string]any{})
	if err != nil {
		return fmt.Errorf("list dashboards: %w", err)
	}
	var list struct {
		DashboardEntries []struct{ DashboardName string } `json:"DashboardEntries"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return fmt.Errorf("list dashboards: %w", err)
	}
	for _, e := range list.DashboardEntries {
		body, err := c.cw(ctx, "GetDashboard", map[string]any{"DashboardName": e.DashboardName})
		if err != nil {
			return fmt.Errorf("dashboard %q: %w", e.DashboardName, err)
		}
		var got struct{ DashboardBody string }
		if json.Unmarshal(body, &got) != nil {
			continue
		}
		if s.Dashboards == nil {
			s.Dashboards = map[string]Dashboard{}
		}
		s.Dashboards[e.DashboardName] = Dashboard{Body: got.DashboardBody}
	}
	return nil
}

func destroyAlarms(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	if names := sortedNames(s.Alarms); len(names) > 0 {
		// DeleteAlarms takes them all at once and is not an error for an
		// alarm that is already gone, so a destroy is one call.
		_, err := c.cw(ctx, "DeleteAlarms", map[string]any{"AlarmNames": names})
		for _, name := range names {
			record(rep, "alarm/"+name, err)
		}
	}
	for _, name := range sortedNames(s.Dashboards) {
		_, err := c.cw(ctx, "DeleteDashboards", map[string]any{"DashboardNames": []string{name}})
		record(rep, "dashboard/"+name, err)
	}
	return nil
}
