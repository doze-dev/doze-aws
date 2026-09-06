package stepfunctions

import (
	"context"
	"encoding/json"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// SendTaskSuccess, SendTaskFailure and SendTaskHeartbeat: the callback half
// of .waitForTaskToken. The handler resolves the token against the store and
// hands the engine a delivery, then waits until it has been applied — so the
// DescribeExecution a worker fires right after its SendTaskSuccess already
// sees the machine moving.

func (s *Server) sendTaskSuccess(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	ref, aerr := s.tokenOf(p)
	if aerr != nil {
		return nil, aerr
	}
	output := awsjson.Str(p, "output")
	var probe any
	if err := json.Unmarshal([]byte(output), &probe); err != nil {
		return nil, awshttp.Errf(400, "InvalidOutput", "Invalid Output: '%s'", err.Error())
	}
	if !s.engine.redeem(ref, awsjson.Str(p, "taskToken"), asl.TaskResult{Output: json.RawMessage(output)}) {
		return nil, awshttp.Errf(500, "InternalFailure", "the execution engine is shutting down")
	}
	return map[string]any{}, nil
}

func (s *Server) sendTaskFailure(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	ref, aerr := s.tokenOf(p)
	if aerr != nil {
		return nil, aerr
	}
	fail := &asl.Failure{Name: awsjson.Str(p, "error"), Cause: awsjson.Str(p, "cause")}
	if !s.engine.redeem(ref, awsjson.Str(p, "taskToken"), asl.TaskResult{Failure: fail}) {
		return nil, awshttp.Errf(500, "InternalFailure", "the execution engine is shutting down")
	}
	return map[string]any{}, nil
}

func (s *Server) sendTaskHeartbeat(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	ref, aerr := s.tokenOf(p)
	if aerr != nil {
		return nil, aerr
	}
	s.engine.heartbeat(ref)
	return map[string]any{}, nil
}

// tokenOf resolves the taskToken parameter. An unknown token — never issued,
// or already redeemed — answers TaskDoesNotExist; one whose task timed out
// answers TaskTimedOut for a day, the distinction AWS keeps.
func (s *Server) tokenOf(p map[string]any) (*TokenRef, *awshttp.APIError) {
	token := awsjson.Str(p, "taskToken")
	if token == "" {
		return nil, awshttp.Errf(400, "InvalidToken", "Invalid Token: 'must not be empty'")
	}
	ref, err := s.store.GetToken(token)
	if err != nil {
		return nil, asAPIError(err)
	}
	if ref == nil {
		return nil, awshttp.Errf(400, "TaskDoesNotExist", "Task Does Not Exist: no task is waiting on this token")
	}
	if ref.TimedOut != 0 {
		return nil, awshttp.Errf(400, "TaskTimedOut", "Task Timed Out: the task this token belongs to timed out")
	}
	return ref, nil
}

// redeem hands a token's result to the driver and waits for it to apply.
func (g *engine) redeem(ref *TokenRef, token string, res asl.TaskResult) bool {
	reply := make(chan struct{})
	d := delivery{key: ref.ExecKey, frame: ref.Frame, kind: dlvSendTask, result: res, reply: reply}
	select {
	case g.deliveries <- d:
	case <-g.stop:
		return false
	}
	select {
	case <-reply:
		return true
	case <-g.stop:
		return false
	}
}

// heartbeat refreshes a parked frame's heartbeat clock; fire-and-forget.
func (g *engine) heartbeat(ref *TokenRef) {
	select {
	case g.deliveries <- delivery{key: ref.ExecKey, frame: ref.Frame, kind: dlvHeartbeat}:
	case <-g.stop:
	}
}
