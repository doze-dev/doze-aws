package stepfunctions

import (
	"context"
	"time"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// RedriveExecution restarts a failed, aborted or timed-out Standard
// execution from the states that did not finish, keeping everything that
// did. It is a store write on the handler goroutine, like StartExecution:
// the run is not live (it finished), so nobody else holds it, and the
// record goes back to RUNNING with an ExecutionRedriven event before the
// driver is nudged.

// redriveWindow is AWS's: an execution can be redriven within 14 days of
// its original start.
const redriveWindow = 14 * 24 * time.Hour

func errNotRedrivable(why string) *awshttp.APIError {
	return awshttp.Errf(400, "ExecutionNotRedrivable", "Execution is not redrivable: %s", why)
}

// redrivable reports whether an execution can be redriven, and why not.
func redrivable(e *Execution, now int64) (bool, string) {
	switch {
	case e.Type == "EXPRESS":
		return false, "EXPRESS executions are not redrivable"
	case e.Status == "RUNNING":
		return false, "the execution is still running"
	case e.Status == "SUCCEEDED":
		return false, "the execution succeeded"
	case now-e.StartedAt > redriveWindow.Milliseconds():
		return false, "the execution started more than 14 days ago"
	}
	return true, ""
}

func (s *Server) redriveExecution(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	e, aerr := s.executionOf(p)
	if aerr != nil {
		return nil, aerr
	}
	now := s.store.now()
	// clientToken makes a retried call idempotent: the same token on an
	// execution already redriven with it answers the earlier redrive — checked
	// before redrivability, since the first redrive put the execution back to
	// RUNNING and a retry must not be refused for that.
	if tok := awsjson.Str(p, "clientToken"); tok != "" && tok == e.RedriveToken {
		return map[string]any{"redriveDate": epoch(e.RedriveDate)}, nil
	}
	if ok, why := redrivable(e, now); !ok {
		return nil, errNotRedrivable(why)
	}
	def, perr := asl.Parse([]byte(e.Definition))
	if perr != nil {
		return nil, awshttp.Errf(500, "InternalFailure", "the stored definition no longer parses: %v", perr)
	}
	if asl.Redrive(def, e.Exec) == 0 {
		return nil, errNotRedrivable("no state of the execution is left to redrive")
	}
	e.Status = "RUNNING"
	e.StoppedAt, e.Output, e.Error, e.Cause = 0, nil, "", ""
	e.RedriveCount++
	e.RedriveDate = now
	e.RedriveToken = awsjson.Str(p, "clientToken")
	// The machine-level timeout starts over from the redrive, as on AWS.
	e.Deadline = 0
	if def.TimeoutSeconds != nil {
		if ms := mustPositive(*def.TimeoutSeconds); ms > 0 {
			e.Deadline = now + ms
		}
	}
	ev := histEvent{
		ID: e.NextEventID, PrevID: e.Exec.Root().PrevEventID, TS: now, Type: "ExecutionRedriven",
		DetailKey: "executionRedrivenEventDetails",
		Details:   map[string]any{"redriveCount": e.RedriveCount},
	}
	e.NextEventID++
	e.Exec.Root().PrevEventID = ev.ID
	if err := s.store.SaveTransition(e, []histEvent{ev}, nil); err != nil {
		return nil, asAPIError(err)
	}
	s.engine.nudge(e.Key())
	return map[string]any{"redriveDate": epoch(now)}, nil
}

// putRedrive fills DescribeExecution's redrive members.
func (s *Server) putRedrive(out map[string]any, e *Execution) {
	out["redriveCount"] = e.RedriveCount
	if e.RedriveDate != 0 {
		out["redriveDate"] = epoch(e.RedriveDate)
	}
	if ok, why := redrivable(e, s.store.now()); ok {
		out["redriveStatus"] = "REDRIVABLE"
	} else {
		out["redriveStatus"] = "NOT_REDRIVABLE"
		out["redriveStatusReason"] = why
	}
}
