package provision

// CloudWatch Logs apply: log groups. A group a template declares exists
// before the function that writes to it, with the retention the template
// set; a group nobody declares is still created by Lambda on the first line.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

func applyLogGroups(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.LogGroups) {
		g := s.LogGroups[name]
		in := map[string]any{"logGroupName": name}
		if len(g.Tags) > 0 {
			in["tags"] = g.Tags
		}
		_, err := c.json11(ctx, "Logs_20140328", "CreateLogGroup", in)
		switch {
		case err == nil:
			rep.add("created", "loggroup/"+name, "")
		case strings.Contains(err.Error(), "ResourceAlreadyExistsException"):
			rep.add("skipped", "loggroup/"+name, "exists")
		default:
			return fmt.Errorf("log group %q: %w", name, err)
		}
		if g.RetentionDays > 0 {
			if _, err := c.json11(ctx, "Logs_20140328", "PutRetentionPolicy", map[string]any{
				"logGroupName": name, "retentionInDays": g.RetentionDays,
			}); err != nil {
				return fmt.Errorf("log group %q retention: %w", name, err)
			}
		}
		for _, sub := range g.Subscriptions {
			in, err := subscriptionRequest(name, sub)
			if err != nil {
				return err
			}
			if _, err := c.json11(ctx, "Logs_20140328", "PutSubscriptionFilter", in); err != nil { // upsert by name
				return fmt.Errorf("log group %q subscription %q: %w", name, sub.Name, err)
			}
			rep.add("updated", "loggroup/"+name+"/subscription/"+sub.Name, "put (upsert)")
		}
		for _, f := range g.MetricFilters {
			in, err := metricFilterRequest(name, f)
			if err != nil {
				return err
			}
			if _, err := c.json11(ctx, "Logs_20140328", "PutMetricFilter", in); err != nil { // upsert by name
				return fmt.Errorf("log group %q metric filter %q: %w", name, f.Name, err)
			}
			rep.add("updated", "loggroup/"+name+"/metricfilter/"+f.Name, "put (upsert)")
		}
	}
	return nil
}

// metricFilterRequest is the PutMetricFilter body for one filter.
func metricFilterRequest(group string, f MetricFilter) (map[string]any, error) {
	if len(f.Transformations) == 0 {
		return nil, fmt.Errorf("log group %q metric filter %q: at least one "+
			"transformation is required", group, f.Name)
	}
	transforms := make([]map[string]any, 0, len(f.Transformations))
	for _, t := range f.Transformations {
		item := map[string]any{
			"metricNamespace": t.Namespace, "metricName": t.MetricName,
			"metricValue": t.Value,
		}
		if t.Default != nil {
			item["defaultValue"] = *t.Default
		}
		if t.Unit != "" {
			item["unit"] = t.Unit
		}
		if len(t.Dimensions) > 0 {
			item["dimensions"] = t.Dimensions
		}
		transforms = append(transforms, item)
	}
	return map[string]any{
		"logGroupName": group, "filterName": f.Name, "filterPattern": f.Pattern,
		"metricTransformations": transforms,
	}, nil
}

// exportMetricFilters reads a group's metric filters.
func exportMetricFilters(ctx context.Context, c *client, group string) []MetricFilter {
	out, err := c.json11(ctx, "Logs_20140328", "DescribeMetricFilters",
		map[string]any{"logGroupName": group})
	if err != nil {
		return nil
	}
	var list struct {
		MetricFilters []struct {
			FilterName, FilterPattern string
			MetricTransformations     []struct {
				MetricNamespace, MetricName, MetricValue, Unit string
				DefaultValue                                   *float64
				Dimensions                                     map[string]string
			} `json:"metricTransformations"`
		} `json:"metricFilters"`
	}
	json.Unmarshal(out, &list)
	var filters []MetricFilter
	for _, f := range list.MetricFilters {
		mf := MetricFilter{Name: f.FilterName, Pattern: f.FilterPattern}
		for _, t := range f.MetricTransformations {
			mf.Transformations = append(mf.Transformations, MetricTransformation{
				Namespace: t.MetricNamespace, MetricName: t.MetricName,
				Value: t.MetricValue, Unit: t.Unit, Default: t.DefaultValue,
				Dimensions: t.Dimensions,
			})
		}
		filters = append(filters, mf)
	}
	return filters
}

// subscriptionRequest is the PutSubscriptionFilter body for one filter.
func subscriptionRequest(group string, sub LogSubscription) (map[string]any, error) {
	in := map[string]any{"logGroupName": group, "filterName": sub.Name, "filterPattern": sub.Pattern}
	switch {
	case sub.Lambda != "":
		in["destinationArn"] = lambdaARN(sub.Lambda)
	case sub.Kinesis != "":
		in["destinationArn"] = awsident.ARN("kinesis", "stream/"+sub.Kinesis)
		if sub.Distribution != "" {
			in["distribution"] = sub.Distribution
		}
	default:
		return nil, fmt.Errorf("log group %q subscription %q: one of lambda or kinesis is required", group, sub.Name)
	}
	return in, nil
}

func exportLogGroups(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "Logs_20140328", "DescribeLogGroups", map[string]any{})
	if err != nil {
		return fmt.Errorf("describe log groups: %w", err)
	}
	var list struct {
		LogGroups []struct {
			Name      string `json:"logGroupName"`
			Retention int    `json:"retentionInDays"`
		} `json:"logGroups"`
	}
	json.Unmarshal(out, &list)
	for _, g := range list.LogGroups {
		subs := exportSubscriptions(ctx, c, g.Name)
		filters := exportMetricFilters(ctx, c, g.Name)
		// Lambda's own groups follow the function; exporting them would
		// double-declare what the function's presence already implies. A
		// subscription filter or a metric filter on one is not implied, so
		// that group stays.
		if strings.HasPrefix(g.Name, "/aws/lambda/") && len(subs) == 0 && len(filters) == 0 {
			continue
		}
		if s.LogGroups == nil {
			s.LogGroups = map[string]LogGroup{}
		}
		s.LogGroups[g.Name] = LogGroup{RetentionDays: g.Retention, Subscriptions: subs,
			MetricFilters: filters}
	}
	return nil
}

// exportSubscriptions reads a group's subscription filters.
func exportSubscriptions(ctx context.Context, c *client, group string) []LogSubscription {
	out, err := c.json11(ctx, "Logs_20140328", "DescribeSubscriptionFilters", map[string]any{"logGroupName": group})
	if err != nil {
		return nil
	}
	var list struct {
		SubscriptionFilters []struct {
			FilterName, FilterPattern, DestinationArn, Distribution string
		} `json:"subscriptionFilters"`
	}
	json.Unmarshal(out, &list)
	var subs []LogSubscription
	for _, f := range list.SubscriptionFilters {
		sub := LogSubscription{Name: f.FilterName, Pattern: f.FilterPattern, Distribution: f.Distribution}
		leaf := arnLeaf(f.DestinationArn)
		switch {
		case strings.Contains(f.DestinationArn, ":lambda:"):
			sub.Lambda = strings.TrimPrefix(leaf, "function:")
		case strings.Contains(f.DestinationArn, ":kinesis:"):
			sub.Kinesis = strings.TrimPrefix(leaf, "stream/")
		default:
			continue
		}
		subs = append(subs, sub)
	}
	return subs
}

func destroyLogGroups(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.LogGroups) {
		_, err := c.json11(ctx, "Logs_20140328", "DeleteLogGroup", map[string]any{"logGroupName": name})
		record(rep, "loggroup/"+name, err)
	}
	return nil
}
