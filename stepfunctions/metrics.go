package stepfunctions

// The AWS/States metrics an execution produces.
//
// AWS publishes these for every state machine without being asked, and they
// are what a workflow's first alarm watches — ExecutionsFailed above zero, or
// an ExecutionTime budget. Producing them locally is what lets that alarm be
// tested before it is deployed.
//
// Dimensioned by StateMachineArn, which is AWS's dimension for the execution
// metrics; an alarm written against the cloud names the ARN, and the local
// ARNs are the same shape, so the alarm finds them.

import (
	"time"

	"github.com/doze-dev/doze-aws/internal/metricship"
)

// statesNamespace is AWS's, not doze-aws's: an alarm written against the
// cloud names this namespace, and it has to match to find anything locally.
const statesNamespace = "AWS/States"

// recordStarted counts an execution that began.
//
// Counted at StartExecution rather than when the engine picks the run up, so
// ExecutionsStarted minus the terminal counts is the number in flight — which
// is the arithmetic anyone reading these metrics does.
func (s *Server) recordStarted(e *Execution) {
	if s.metrics == nil {
		return
	}
	s.metrics.Count(statesNamespace, "ExecutionsStarted",
		map[string]string{"StateMachineArn": e.MachineARN}, 1)
}

// recordFinished counts an execution that reached a terminal status and,
// where the status means the workflow ran to a conclusion, how long it took.
//
// ExecutionTime is published for every terminal status including failures,
// matching AWS: a workflow that fails after ten minutes is a slow failure,
// and dropping its duration would hide that.
func (s *Server) recordFinished(e *Execution) {
	if s.metrics == nil {
		return
	}
	dims := map[string]string{"StateMachineArn": e.MachineARN}
	var name string
	switch e.Status {
	case "SUCCEEDED":
		name = "ExecutionsSucceeded"
	case "FAILED":
		name = "ExecutionsFailed"
	case "TIMED_OUT":
		name = "ExecutionsTimedOut"
	case "ABORTED":
		name = "ExecutionsAborted"
	default:
		// Not terminal — nothing to count, and no duration to report.
		return
	}
	s.metrics.Count(statesNamespace, name, dims, 1)
	if e.StoppedAt > e.StartedAt {
		s.metrics.Duration(statesNamespace, "ExecutionTime", dims,
			time.Duration(e.StoppedAt-e.StartedAt)*time.Millisecond)
	}
}

func newMetrics(s *Server) *metricship.Shipper {
	return metricship.New("stepfunctions", s.peers, s.logf)
}
