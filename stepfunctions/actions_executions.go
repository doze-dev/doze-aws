package stepfunctions

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/trace"
)

// The execution-facing operations: start, describe, stop, list, and the
// frozen-snapshot read-back. The engine does the running; these handlers only
// read the store and hand the engine work over its channels — a handler never
// touches a run.

func execARN(machineName, execName string) string {
	return awsident.ARN("states", "execution:"+machineName+":"+execName)
}

// parseExecARN splits arn:aws:states:<r>:<a>:execution:<machine>:<name>.
func parseExecARN(arn string) (machineName, execName string) {
	parts := strings.SplitN(arn, ":", 8)
	if len(parts) != 8 || parts[5] != "execution" {
		return "", ""
	}
	return parts[6], parts[7]
}

func (s *Server) startExecution(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	// A version or alias ARN resolves to the snapshot it runs; m then carries
	// that definition, with its ARN and Name still the machine's own.
	m, versionARN, aliasARN, aerr := s.startTarget(arn)
	if aerr != nil {
		return nil, aerr
	}
	machineName := m.Name
	if m.Type == "EXPRESS" {
		return s.startExpress(ctx, m, p, versionARN, aliasARN)
	}

	execName := awsjson.Str(p, "name")
	if execName == "" {
		execName = randomExecName()
	} else if aerr := checkName(execName); aerr != nil {
		return nil, aerr
	}

	input := awsjson.Str(p, "input")
	if input == "" {
		input = "{}"
	}
	var probe any
	if err := json.Unmarshal([]byte(input), &probe); err != nil {
		return nil, errInvalidExecutionInput(err.Error())
	}

	// Same name, still running, same input: AWS answers with the original
	// execution rather than a conflict — retried StartExecution calls are
	// how SDKs behave on a timeout. Anything else on a taken name conflicts.
	if existing, err := s.store.GetExecution(machineName, execName); err != nil {
		return nil, asAPIError(err)
	} else if existing != nil {
		if existing.Status == "RUNNING" && jsonEqual(json.RawMessage(existing.Input), json.RawMessage(input)) {
			return map[string]any{
				"executionArn": existing.ARN,
				"startDate":    epoch(existing.StartedAt),
			}, nil
		}
		return nil, errExecutionExists(existing.ARN)
	}

	e, aerr := s.launch(launchSpec{
		Machine: m, VersionARN: versionARN, AliasARN: aliasARN, Name: execName, Input: input,
		TraceHeader: trace.Header(ctx), XRayHeader: awsjson.Str(p, "traceHeader"),
	})
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{
		"executionArn": e.ARN,
		"startDate":    epoch(e.StartedAt),
	}, nil
}

// launchSpec is everything a Standard execution starts from. StartExecution
// fills it from the request; a states:startExecution Task fills it from its
// Parameters, on the driver, with the parent's trace as the cause.
type launchSpec struct {
	Machine              *StateMachine
	VersionARN, AliasARN string
	Name, Input          string
	TraceHeader          string
	XRayHeader           string
	StartedBy            string // the parent execution's ARN, for AWS_STEP_FUNCTIONS_STARTED_BY_EXECUTION_ID
}

// launch writes a Standard execution's record and its ExecutionStarted event
// in one transaction and nudges the driver.
func (s *Server) launch(spec launchSpec) (*Execution, *awshttp.APIError) {
	m := spec.Machine
	def, perr := asl.Parse([]byte(m.Definition))
	if perr != nil {
		return nil, awshttp.Errf(500, "InternalFailure", "the stored definition no longer parses: %v", perr)
	}
	now := s.store.clock()
	arn := execARN(m.Name, spec.Name)
	e := &Execution{
		ARN: arn, MachineARN: m.ARN, Name: spec.Name,
		Definition: m.Definition, RoleARN: m.RoleARN, RevisionID: m.RevisionID, Type: m.Type,
		Status: "RUNNING", StartedAt: now.UnixMilli(), Input: spec.Input,
		TraceHeader: spec.TraceHeader,
		XRayHeader:  spec.XRayHeader,
		VersionARN:  spec.VersionARN,
		AliasARN:    spec.AliasARN,
		StartedBy:   spec.StartedBy,
		Exec: asl.StartExec(arn, spec.Name, m.ARN, m.Name, m.RoleARN,
			json.RawMessage(spec.Input), now),
		NextEventID: 1,
	}
	if def.TimeoutSeconds != nil {
		if ms := mustPositive(*def.TimeoutSeconds); ms > 0 {
			e.Deadline = e.StartedAt + ms
		}
	}
	// The record and its ExecutionStarted event land in one transaction; the
	// root frame's chain starts at that event.
	started := histEvent{
		ID: 1, PrevID: 0, TS: e.StartedAt, Type: "ExecutionStarted",
		DetailKey: "executionStartedEventDetails",
		Details: map[string]any{
			"input": e.Input, "inputDetails": notTruncated(), "roleArn": e.RoleARN,
		},
	}
	e.NextEventID = 2
	e.Exec.Root().PrevEventID = 1
	if err := s.store.SaveTransition(e, []histEvent{started}, nil); err != nil {
		return nil, asAPIError(err)
	}
	s.engine.nudge(e.Key())
	return e, nil
}

func (s *Server) getExecutionHistory(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	e, aerr := s.executionOf(p)
	if aerr != nil {
		return nil, aerr
	}
	limit := awsjson.Int(p, "maxResults", 100)
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	after := int64(0)
	if tok := awsjson.Str(p, "nextToken"); tok != "" {
		n, err := strconv.ParseInt(tok, 10, 64)
		if err != nil {
			return nil, awshttp.Errf(400, "InvalidToken", "Invalid Token: '%s'", tok)
		}
		after = n
	}
	includeData := true
	if v, ok := p["includeExecutionData"].(bool); ok {
		includeData = v
	}
	events, more, err := s.store.HistoryPage(e.Key(), after, limit)
	if err != nil {
		return nil, asAPIError(err)
	}
	// reverseOrder walks the whole set backwards; local histories are small,
	// so reading forward and flipping is simpler than a reverse cursor, but
	// pagination tokens then count from the end.
	if awsjson.Bool(p, "reverseOrder") {
		all, _, err := s.store.HistoryPage(e.Key(), 0, 1<<30)
		if err != nil {
			return nil, asAPIError(err)
		}
		for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
			all[i], all[j] = all[j], all[i]
		}
		start := 0
		if after != 0 {
			for i, ev := range all {
				if ev.ID == after {
					start = i + 1
					break
				}
			}
		}
		events = all[start:]
		more = len(events) > limit
		if more {
			events = events[:limit]
		}
	}
	items := make([]any, 0, len(events))
	for _, ev := range events {
		items = append(items, ev.wire(includeData))
	}
	out := map[string]any{"events": items}
	if more && len(events) > 0 {
		out["nextToken"] = strconv.FormatInt(events[len(events)-1].ID, 10)
	}
	return out, nil
}

func (s *Server) describeExecution(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	e, aerr := s.executionOf(p)
	if aerr != nil {
		return nil, aerr
	}
	out := map[string]any{
		"executionArn":    e.ARN,
		"stateMachineArn": e.MachineARN,
		"name":            e.Name,
		"status":          e.Status,
		"startDate":       epoch(e.StartedAt),
		"input":           e.Input,
		"inputDetails":    map[string]any{"included": true},
	}
	s.putRedrive(out, e)
	putQualifiers(out, e)
	if e.StoppedAt != 0 {
		out["stopDate"] = epoch(e.StoppedAt)
	}
	if e.Status == "SUCCEEDED" && e.Output != nil {
		out["output"] = string(e.Output)
		out["outputDetails"] = map[string]any{"included": true}
	}
	if e.Error != "" {
		out["error"] = e.Error
	}
	if e.Cause != "" {
		out["cause"] = e.Cause
	}
	// Only the caller's X-Ray header comes back; doze-aws's own causal chain
	// is not an AWS value and was leaking here as one.
	if e.XRayHeader != "" {
		out["traceHeader"] = e.XRayHeader
	}
	return out, nil
}

func (s *Server) describeStateMachineForExecution(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	e, aerr := s.executionOf(p)
	if aerr != nil {
		return nil, aerr
	}
	// The whole reason the snapshot exists: this answers with what the
	// execution is actually running, not with what the machine says today.
	return map[string]any{
		"stateMachineArn": e.MachineARN,
		"name":            machineOfExecARN(e.ARN),
		"definition":      e.Definition,
		"roleArn":         e.RoleARN,
		"revisionId":      e.RevisionID,
		"updateDate":      epoch(e.StartedAt),
	}, nil
}

func (s *Server) stopExecution(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	e, aerr := s.executionOf(p)
	if aerr != nil {
		return nil, aerr
	}
	if e.Status != "RUNNING" {
		// Stopping a stopped execution answers with when it stopped.
		return map[string]any{"stopDate": epoch(e.StoppedAt)}, nil
	}
	errName := awsjson.Str(p, "error")
	cause := awsjson.Str(p, "cause")
	if !s.engine.stopExec(e.Key(), errName, cause) {
		return nil, awshttp.Errf(500, "InternalFailure", "the execution engine is shutting down")
	}
	stopped, err := s.store.GetExecutionByKey(e.Key())
	if err != nil || stopped == nil {
		return nil, asAPIError(err)
	}
	return map[string]any{"stopDate": epoch(stopped.StoppedAt)}, nil
}

func (s *Server) listExecutions(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	machineName, qualifier, ok := splitMachineARN(arn)
	if !ok {
		return nil, errInvalidARN(arn)
	}
	if m, aerr := s.store.GetMachine(machineName); aerr != nil {
		return nil, aerr
	} else if m == nil {
		return nil, errMachineNotFound(arn)
	} else if m.Type == "EXPRESS" {
		return nil, errTypeNotSupported("ListExecutions on an EXPRESS state machine")
	}
	// A version or alias ARN narrows the list to what was started through it.
	versionARN, aliasARN, aerr := s.executionQualifier(arn, machineName, qualifier)
	if aerr != nil {
		return nil, aerr
	}
	execs, err := s.store.ListExecutionsFor(machineName)
	if err != nil {
		return nil, asAPIError(err)
	}
	filter := awsjson.Str(p, "statusFilter")
	redriveFilter := awsjson.Str(p, "redriveFilter")
	// Newest first, the way the console and the CLI expect to read them.
	sort.Slice(execs, func(i, j int) bool { return execs[i].StartedAt > execs[j].StartedAt })
	items := make([]any, 0, len(execs))
	for _, e := range execs {
		if filter != "" && e.Status != filter {
			continue
		}
		if (redriveFilter == "REDRIVEN" && e.RedriveCount == 0) || (redriveFilter == "NOT_REDRIVEN" && e.RedriveCount > 0) {
			continue
		}
		if (versionARN != "" && e.VersionARN != versionARN) || (aliasARN != "" && e.AliasARN != aliasARN) {
			continue
		}
		item := map[string]any{
			"executionArn":    e.ARN,
			"stateMachineArn": e.MachineARN,
			"name":            e.Name,
			"status":          e.Status,
			"startDate":       epoch(e.StartedAt),
		}
		if e.StoppedAt != 0 {
			item["stopDate"] = epoch(e.StoppedAt)
		}
		putQualifiers(item, e)
		items = append(items, item)
	}
	return page(p, "executions", items)
}

// executionOf resolves the executionArn parameter to its record.
func (s *Server) executionOf(p map[string]any) (*Execution, *awshttp.APIError) {
	arn := awsjson.Str(p, "executionArn")
	if isExpressARN(arn) {
		// An Express execution has no describable record — AWS keeps none,
		// and answers as if it never existed.
		return nil, errExecutionNotFound(arn)
	}
	machineName, execName := parseExecARN(arn)
	if machineName == "" {
		return nil, errInvalidARN(arn)
	}
	e, err := s.store.GetExecution(machineName, execName)
	if err != nil {
		return nil, asAPIError(err)
	}
	if e == nil {
		return nil, errExecutionNotFound(arn)
	}
	return e, nil
}

// randomExecName mirrors AWS generating a UUID when StartExecution carries no
// name.
func randomExecName() string {
	var b [16]byte
	rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// jsonEqual compares two documents structurally.
func jsonEqual(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	ra, _ := json.Marshal(va)
	rb, _ := json.Marshal(vb)
	return string(ra) == string(rb)
}
