package stepfunctions

import (
	"context"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Resource-ARN parsing and task dispatch: the layer that turns a Task state's
// Resource into a call on a sibling service.
//
// The go-live integration set is Lambda (bare ARN and lambda:invoke), SQS
// sendMessage and SNS publish, each with the .waitForTaskToken pattern.
// Everything else parses to an error, which createStateMachine surfaces as an
// honest refusal at create time — the same doctrine as the notYet table:
// failing on create beats failing when the state finally runs.

type taskKind int

const (
	taskLambdaBare taskKind = iota
	taskLambdaInvoke
	taskSQSSend
	taskSNSPublish
)

// TaskTarget is one parsed Resource.
type TaskTarget struct {
	Kind  taskKind
	Parks bool // .waitForTaskToken: the result arrives via SendTaskSuccess
	Raw   string
}

// ParseResource resolves a Task Resource, or explains why this build cannot
// call it.
func ParseResource(resource string) (TaskTarget, error) {
	t := TaskTarget{Raw: resource, Parks: asl.TaskParks(resource)}
	base := strings.TrimSuffix(resource, ".waitForTaskToken")

	switch {
	case strings.HasSuffix(base, ".sync"), strings.HasSuffix(base, ".sync:2"):
		return t, fmt.Errorf(".sync service integrations arrive after go-live")
	case strings.HasPrefix(base, "arn:aws:lambda:") && strings.Contains(base, ":function:"):
		if t.Parks {
			return t, fmt.Errorf("a bare function ARN has no .waitForTaskToken pattern; use arn:aws:states:::lambda:invoke.waitForTaskToken")
		}
		t.Kind = taskLambdaBare
		return t, nil
	case base == "arn:aws:states:::lambda:invoke":
		t.Kind = taskLambdaInvoke
		return t, nil
	case base == "arn:aws:states:::sqs:sendMessage":
		t.Kind = taskSQSSend
		return t, nil
	case base == "arn:aws:states:::sns:publish":
		t.Kind = taskSNSPublish
		return t, nil
	case strings.Contains(base, ":activity:"):
		return t, fmt.Errorf("activities arrive after go-live")
	case strings.HasPrefix(base, "arn:aws:states:::aws-sdk:"):
		return t, fmt.Errorf("generic aws-sdk integrations arrive after go-live")
	case strings.HasPrefix(base, "arn:aws:states:::"):
		return t, fmt.Errorf("the %q integration is not one this build calls (Lambda, sqs:sendMessage and sns:publish are)", base)
	default:
		return t, fmt.Errorf("%q is not a task resource this build can call", resource)
	}
}

// performTask runs one synchronous integration. It never returns a Go error:
// every outcome is a TaskResult, because the interpreter's only vocabulary
// for failure is an error name Retry and Catch can match.
func (s *Server) performTask(ctx context.Context, resource string, input []byte) asl.TaskResult {
	t, err := ParseResource(resource)
	if err != nil {
		return failResult(asl.ErrTaskFailed, err.Error())
	}
	switch t.Kind {
	case taskLambdaBare:
		return s.invokeLambda(ctx, lambdaFunctionOfARN(t.Raw), input, true)
	case taskLambdaInvoke:
		return s.invokeLambdaAPI(ctx, input)
	case taskSQSSend:
		return s.sendSQS(ctx, input)
	case taskSNSPublish:
		return s.publishSNS(ctx, input)
	}
	return failResult(asl.ErrTaskFailed, "unhandled task kind")
}

func failResult(name, cause string) asl.TaskResult {
	return asl.TaskResult{Failure: &asl.Failure{Name: name, Cause: cause}}
}

// refuseUnrunnable keeps definitions this build would run wrongly out of the
// store: the JSONata dialect (the interpreter speaks JSONPath) and task
// resources outside the go-live integration set. The analyser stays faithful
// to ASL — ValidateStateMachineDefinition accepts what AWS accepts — but
// create refuses honestly, at create time.
func refuseUnrunnable(d *asl.Definition) *awshttp.APIError {
	if usesJSONata(d) {
		return errNotYet("QueryLanguage: JSONata",
			"this build evaluates the JSONPath dialect; JSONata arrives later")
	}
	return refuseResources(d)
}

func refuseResources(d *asl.Definition) *awshttp.APIError {
	for _, name := range d.Order {
		s := d.States[name]
		if s.Type == asl.Task && s.Resource != "" {
			if _, err := ParseResource(s.Resource); err != nil {
				return errNotYet("Resource "+s.Resource, err.Error())
			}
		}
		for _, b := range s.Branches {
			if aerr := refuseResources(b); aerr != nil {
				return aerr
			}
		}
		if p := s.Processor(); p != nil {
			if aerr := refuseResources(p); aerr != nil {
				return aerr
			}
		}
	}
	return nil
}
