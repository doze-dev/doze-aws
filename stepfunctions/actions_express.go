package stepfunctions

import (
	"context"
	"encoding/json"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/trace"
)

// Express executions. StartExecution on an EXPRESS machine starts one and
// answers immediately — there is no DescribeExecution, history or
// StopExecution for it, on AWS or here, so the record is dropped the moment
// it finishes. StartSyncExecution is the same start with the caller waiting:
// the answer is the execution's outcome, output included, and that is the
// whole Express value proposition — a synchronous workflow behind an API.

func errTypeNotSupported(what string) *awshttp.APIError {
	return awshttp.Errf(400, "StateMachineTypeNotSupported", "This operation is not supported by this type of state machine: %s", what)
}

// execInputs reads and checks the name and input members StartExecution and
// StartSyncExecution share.
func execInputs(p map[string]any) (name, input string, aerr *awshttp.APIError) {
	name = awsjson.Str(p, "name")
	if name == "" {
		name = randomExecName()
	} else if aerr := checkName(name); aerr != nil {
		return "", "", aerr
	}
	input = awsjson.Str(p, "input")
	if input == "" {
		input = "{}"
	}
	var probe any
	if err := json.Unmarshal([]byte(input), &probe); err != nil {
		return "", "", errInvalidExecutionInput(err.Error())
	}
	return name, input, nil
}

// newExpressExecution builds the volatile record for one Express run. The
// deadline is the machine's TimeoutSeconds or AWS's 5-minute Express cap,
// whichever is sooner.
func (s *Server) newExpressExecution(ctx context.Context, m *StateMachine, p map[string]any, versionARN, aliasARN string) (*Execution, *awshttp.APIError) {
	execName, input, aerr := execInputs(p)
	if aerr != nil {
		return nil, aerr
	}
	def, perr := asl.Parse([]byte(m.Definition))
	if perr != nil {
		return nil, awshttp.Errf(500, "InternalFailure", "the stored definition no longer parses: %v", perr)
	}
	now := s.store.clock()
	arn := expressExecARN(m.Name, execName, newToken()[2:18])
	e := &Execution{
		ARN: arn, MachineARN: m.ARN, Name: execName,
		Definition: m.Definition, RoleARN: m.RoleARN, RevisionID: m.RevisionID, Type: "EXPRESS",
		Status: "RUNNING", StartedAt: now.UnixMilli(), Input: input,
		TraceHeader: trace.Header(ctx),
		XRayHeader:  awsjson.Str(p, "traceHeader"),
		VersionARN:  versionARN, AliasARN: aliasARN,
		Volatile: true,
		Exec: asl.StartExec(arn, execName, m.ARN, m.Name, m.RoleARN,
			json.RawMessage(input), now),
		NextEventID: 2,
	}
	e.Deadline = e.StartedAt + expressMaxDuration.Milliseconds()
	if def.TimeoutSeconds != nil {
		if ms := mustPositive(*def.TimeoutSeconds); ms > 0 && e.StartedAt+ms < e.Deadline {
			e.Deadline = e.StartedAt + ms
		}
	}
	e.Exec.Root().PrevEventID = 1
	return e, nil
}

// startExpress is StartExecution's Express branch: fire and forget.
func (s *Server) startExpress(ctx context.Context, m *StateMachine, p map[string]any, versionARN, aliasARN string) (any, *awshttp.APIError) {
	e, aerr := s.newExpressExecution(ctx, m, p, versionARN, aliasARN)
	if aerr != nil {
		return nil, aerr
	}
	if err := s.store.SaveTransition(e, nil, nil); err != nil {
		return nil, asAPIError(err)
	}
	s.logs.record(e, []histEvent{startedEvent(e)})
	s.recordStarted(e)
	s.engine.nudge(e.Key())
	return map[string]any{"executionArn": e.ARN, "startDate": epoch(e.StartedAt)}, nil
}

func (s *Server) startSyncExecution(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	m, versionARN, aliasARN, aerr := s.startTarget(awsjson.Str(p, "stateMachineArn"))
	if aerr != nil {
		return nil, aerr
	}
	if m.Type != "EXPRESS" {
		return nil, errTypeNotSupported("StartSyncExecution on a STANDARD state machine")
	}
	e, aerr := s.newExpressExecution(ctx, m, p, versionARN, aliasARN)
	if aerr != nil {
		return nil, aerr
	}
	ch, err := s.startVolatile(e)
	if err != nil {
		return nil, asAPIError(err)
	}
	s.recordStarted(e)
	done, ok := s.awaitVolatile(ctx, e.Key(), ch)
	if !ok {
		return nil, awshttp.Errf(500, "InternalFailure", "the execution did not finish before the request ended")
	}
	out := map[string]any{
		"executionArn":    done.ARN,
		"stateMachineArn": done.MachineARN,
		"name":            done.Name,
		"status":          done.Status,
		"startDate":       epoch(done.StartedAt),
		"stopDate":        epoch(done.StoppedAt),
		"billingDetails": map[string]any{
			"billedDurationInMilliseconds": billedMillis(done),
			"billedMemoryUsedInMB":         64,
		},
	}
	// includedData: METADATA_ONLY omits the payloads, the default carries them.
	if awsjson.Str(p, "includedData") != "METADATA_ONLY" {
		out["input"] = done.Input
		out["inputDetails"] = notTruncated()
		if done.Output != nil {
			out["output"] = string(done.Output)
			out["outputDetails"] = notTruncated()
		}
	}
	if done.Error != "" {
		out["error"] = done.Error
		out["cause"] = done.Cause
	}
	if done.XRayHeader != "" {
		out["traceHeader"] = done.XRayHeader
	}
	return out, nil
}

// billedMillis rounds a duration up to AWS's 100ms billing granularity.
func billedMillis(e *Execution) int64 {
	d := e.StoppedAt - e.StartedAt
	if d <= 0 {
		return 100
	}
	return (d + 99) / 100 * 100
}
