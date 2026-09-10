package logs

// The four metric-filter operations.
//
// These were refused by name until CloudWatch Metrics existed locally, with
// the reason "metric filters publish to CloudWatch Metrics, which does not
// exist locally". That reason stopped being true, which is the only kind of
// refusal worth removing.

import (
	"context"
	"errors"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// ErrNoFilter is an operation on a metric filter nobody created.
var ErrNoFilter = errors.New("metric filter does not exist")

func (s *Server) putMetricFilter(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	group := awsjson.Str(p, "logGroupName")
	name := awsjson.Str(p, "filterName")
	if group == "" || name == "" {
		return nil, awshttp.Errf(400, "InvalidParameterException",
			"logGroupName and filterName are required")
	}
	raw, _ := p["metricTransformations"].([]any)
	if len(raw) == 0 {
		return nil, awshttp.Errf(400, "InvalidParameterException",
			"metricTransformations is required")
	}
	var transforms []MetricTransformation
	for _, item := range raw {
		t, ok := item.(map[string]any)
		if !ok {
			continue
		}
		mt := MetricTransformation{
			Namespace:  awsjson.Str(t, "metricNamespace"),
			MetricName: awsjson.Str(t, "metricName"),
			Value:      awsjson.Str(t, "metricValue"),
			Unit:       awsjson.Str(t, "unit"),
		}
		if mt.Namespace == "" || mt.MetricName == "" || mt.Value == "" {
			return nil, awshttp.Errf(400, "InvalidParameterException",
				"a metric transformation needs metricNamespace, metricName and metricValue")
		}
		if d, ok := t["defaultValue"].(float64); ok {
			mt.Default = &d
		}
		if dims, ok := t["dimensions"].(map[string]any); ok && len(dims) > 0 {
			mt.Dimensions = map[string]string{}
			for k, v := range dims {
				if sv, ok := v.(string); ok {
					mt.Dimensions[k] = sv
				}
			}
		}
		transforms = append(transforms, mt)
	}

	f := MetricFilter{Group: group, Name: name, Pattern: awsjson.Str(p, "filterPattern"),
		Transformations: transforms, CreatedMs: s.store.now()}
	if err := s.store.PutMetricFilter(f); err != nil {
		if errors.Is(err, ErrNoGroup) {
			return nil, awshttp.Errf(400, "ResourceNotFoundException",
				"The specified log group does not exist.")
		}
		return nil, awshttp.AsAPIError(err)
	}
	s.met.forget(group)
	return nil, nil
}

func (s *Server) deleteMetricFilter(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	group, name := awsjson.Str(p, "logGroupName"), awsjson.Str(p, "filterName")
	if group == "" || name == "" {
		return nil, awshttp.Errf(400, "InvalidParameterException",
			"logGroupName and filterName are required")
	}
	if err := s.store.DeleteMetricFilter(group, name); err != nil {
		if errors.Is(err, ErrNoFilter) {
			return nil, awshttp.Errf(400, "ResourceNotFoundException",
				"The specified metric filter does not exist.")
		}
		return nil, awshttp.AsAPIError(err)
	}
	s.met.forget(group)
	return nil, nil
}

func (s *Server) describeMetricFilters(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	found, err := s.store.MetricFilters(awsjson.Str(p, "logGroupName"), awsjson.Str(p, "filterNamePrefix"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	items := make([]any, 0, len(found))
	for _, f := range found {
		transforms := make([]any, 0, len(f.Transformations))
		for _, t := range f.Transformations {
			item := map[string]any{
				"metricNamespace": t.Namespace,
				"metricName":      t.MetricName,
				"metricValue":     t.Value,
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
		items = append(items, map[string]any{
			"filterName":            f.Name,
			"logGroupName":          f.Group,
			"filterPattern":         f.Pattern,
			"creationTime":          f.CreatedMs,
			"metricTransformations": transforms,
		})
	}
	return map[string]any{"metricFilters": items}, nil
}

// testMetricFilter runs a pattern against sample lines without storing
// anything, which is how a developer checks a pattern before committing it.
func (s *Server) testMetricFilter(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	pattern := awsjson.Str(p, "filterPattern")
	raw, _ := p["logEventMessages"].([]any)
	if len(raw) == 0 {
		return nil, awshttp.Errf(400, "InvalidParameterException",
			"logEventMessages is required")
	}
	match, err := compile(pattern)
	if err != nil {
		return nil, awshttp.Errf(400, "InvalidParameterException",
			"the filter pattern is not valid: %v", err)
	}
	matches := []any{}
	for i, item := range raw {
		msg, ok := item.(string)
		if !ok || !match(msg) {
			continue
		}
		matches = append(matches, map[string]any{
			"eventNumber":     i + 1,
			"eventMessage":    msg,
			"extractedValues": map[string]any{},
		})
	}
	return map[string]any{"matches": matches}, nil
}
