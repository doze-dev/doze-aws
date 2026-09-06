package stepfunctions

import (
	"context"
	"fmt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// The Map Run operations. A Map Run is what a Distributed Map state leaves
// behind: its counts, its concurrency, and the child executions it started
// — the record that DescribeMapRun reads, ListMapRuns finds by execution,
// and UpdateMapRun changes mid-flight.

func errMapRunNotFound(arn string) *awshttp.APIError {
	return errResourceNotFound(arn)
}

func (s *Server) describeMapRun(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	mr, arn := s.mapRunOf(p)
	if mr == nil {
		return nil, errMapRunNotFound(arn)
	}
	counts := map[string]any{
		"pending":               mr.Pending(),
		"running":               mr.Running,
		"succeeded":             mr.Succeeded,
		"failed":                mr.Failed,
		"timedOut":              mr.TimedOut,
		"aborted":               mr.Aborted,
		"total":                 mr.Total(),
		"resultsWritten":        mr.ResultsWritten,
		"failuresNotRedrivable": 0,
		"pendingRedrive":        0,
	}
	out := map[string]any{
		"mapRunArn":       mr.ARN,
		"executionArn":    mr.ExecARN,
		"status":          mr.Status,
		"startDate":       epoch(mr.StartedAt),
		"maxConcurrency":  mr.MaxConcurrency,
		"itemCounts":      counts,
		"executionCounts": counts,
		"redriveCount":    mr.RedriveCount,
	}
	if mr.StoppedAt != 0 {
		out["stopDate"] = epoch(mr.StoppedAt)
	}
	if mr.ToleratedFailureCount != nil {
		out["toleratedFailureCount"] = *mr.ToleratedFailureCount
	} else {
		out["toleratedFailureCount"] = 0
	}
	if mr.ToleratedFailurePercentage != nil {
		out["toleratedFailurePercentage"] = *mr.ToleratedFailurePercentage
	} else {
		out["toleratedFailurePercentage"] = 0
	}
	if mr.RedriveDate != 0 {
		out["redriveDate"] = epoch(mr.RedriveDate)
	}
	return out, nil
}

func (s *Server) listMapRuns(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	e, aerr := s.executionOf(p)
	if aerr != nil {
		return nil, aerr
	}
	runs, err := s.store.MapRunsFor(e.Key())
	if err != nil {
		return nil, asAPIError(err)
	}
	items := make([]any, 0, len(runs))
	for _, mr := range runs {
		item := map[string]any{
			"mapRunArn":       mr.ARN,
			"executionArn":    mr.ExecARN,
			"stateMachineArn": mr.MachineARN,
			"startDate":       epoch(mr.StartedAt),
		}
		if mr.StoppedAt != 0 {
			item["stopDate"] = epoch(mr.StoppedAt)
		}
		items = append(items, item)
	}
	return page(p, "mapRuns", items)
}

// updateMapRun changes the concurrency and tolerance of a run in flight. A
// raised concurrency takes effect at the next launch, which the parent's
// nudge triggers.
func (s *Server) updateMapRun(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	mr, arn := s.mapRunOf(p)
	if mr == nil {
		return nil, errMapRunNotFound(arn)
	}
	if v, ok := p["maxConcurrency"]; ok {
		n, isNum := v.(float64)
		if !isNum || n < 0 {
			return nil, errValidation("maxConcurrency must be a non-negative integer")
		}
		mr.MaxConcurrency = int(n)
	}
	if v, ok := p["toleratedFailureCount"]; ok {
		n, isNum := v.(float64)
		if !isNum || n < 0 {
			return nil, errValidation("toleratedFailureCount must be a non-negative integer")
		}
		c := int(n)
		mr.ToleratedFailureCount = &c
	}
	if v, ok := p["toleratedFailurePercentage"]; ok {
		n, isNum := v.(float64)
		if !isNum || n < 0 || n > 100 {
			return nil, errValidation("toleratedFailurePercentage must be between 0 and 100")
		}
		mr.ToleratedFailurePercentage = &n
	}
	if err := s.store.PutMapRun(mr); err != nil {
		return nil, asAPIError(err)
	}
	if mr.Status == "RUNNING" {
		s.engine.nudge(mr.ExecKey)
	}
	return map[string]any{}, nil
}

// putMapRun fills DescribeExecution's mapRunArn for a child execution.
func putMapRun(out map[string]any, e *Execution) {
	if e.MapRunARN != "" {
		out["mapRunArn"] = e.MapRunARN
	}
}

// mapChildName is what a run's child executions are called.
func mapChildName(mr *MapRun, index int) string { return fmt.Sprintf("%s-%d", mr.ID, index) }
