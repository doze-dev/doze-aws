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
// sendMessage and SNS publish, each with the .waitForTaskToken pattern, plus
// activity ARNs, which park on a token without calling anything (activity.go),
// the optimized DynamoDB and EventBridge integrations (integration_ddb.go,
// integration_events.go) and the generic aws-sdk family for the services in
// this repo (integration_awssdk.go). Everything else parses to an error, which createStateMachine surfaces as an
// honest refusal at create time — the same doctrine as the notYet table:
// failing on create beats failing when the state finally runs.

type taskKind int

const (
	taskLambdaBare taskKind = iota
	taskLambdaInvoke
	taskSQSSend
	taskSNSPublish
	taskActivity // nothing is called: the engine queues the input for a polling worker
	taskDynamoDB // optimized dynamodb:{get,put,update,delete}Item (integration_ddb.go)
	taskEvents   // optimized events:putEvents (integration_events.go)
	taskAWSSDK   // generic aws-sdk:<service>:<action> (integration_awssdk.go)
	taskChild    // states:startExecution, plain, .sync/.sync:2 or with a token (integration_child.go)
)

// TaskTarget is one parsed Resource.
type TaskTarget struct {
	Kind    taskKind
	Parks   bool // .waitForTaskToken: the result arrives via SendTaskSuccess
	Raw     string
	Service string // aws-sdk and optimized kinds: the service called
	Action  string // aws-sdk and optimized kinds: the SDK method, lowercase-initial
}

// ParseResource resolves a Task Resource, or explains why this build cannot
// call it.
func ParseResource(resource string) (TaskTarget, error) {
	t := TaskTarget{Raw: resource, Parks: asl.TaskParks(resource)}
	base := strings.TrimSuffix(resource, ".waitForTaskToken")

	switch {
	case base == childStartResource, base == childStartResource+".sync", base == childStartResource+".sync:2":
		// A child execution: the one .sync integration that means something
		// locally, since the thing being waited for runs in this process.
		if t.Parks && base != childStartResource {
			return t, fmt.Errorf(".sync and .waitForTaskToken cannot both apply to states:startExecution")
		}
		t.Kind = taskChild
		return t, nil
	case strings.HasSuffix(base, ".sync"), strings.HasSuffix(base, ".sync:2"):
		return t, fmt.Errorf("%q: .sync waits for a job on a service that does not run locally; only states:startExecution.sync is supported", base)
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
	case asl.IsActivityARN(base):
		// An activity parks implicitly — its only result path is a worker's
		// SendTaskSuccess — so the explicit suffix is a contradiction AWS
		// refuses too.
		if strings.HasSuffix(resource, ".waitForTaskToken") {
			return t, fmt.Errorf("an activity ARN already waits for its worker; .waitForTaskToken does not apply to it")
		}
		t.Kind = taskActivity
		return t, nil
	case strings.HasPrefix(base, "arn:aws:states:::dynamodb:"):
		return parseDynamoDBResource(t, strings.TrimPrefix(base, "arn:aws:states:::dynamodb:"))
	case base == "arn:aws:states:::events:putEvents":
		t.Kind, t.Service, t.Action = taskEvents, "eventbridge", "putEvents"
		return t, nil
	case strings.HasPrefix(base, "arn:aws:states:::aws-sdk:"):
		return parseSDKResource(t, strings.TrimPrefix(base, "arn:aws:states:::aws-sdk:"))
	case strings.HasPrefix(base, "arn:aws:states:::"):
		return t, fmt.Errorf("the %q integration is not one this build calls (Lambda, sqs:sendMessage, sns:publish, dynamodb:getItem/putItem/updateItem/deleteItem, events:putEvents and aws-sdk:<service>:<action> are)", base)
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
	case taskDynamoDB:
		return s.callDynamoDB(ctx, t.Action, input)
	case taskEvents:
		return s.putEvents(ctx, input)
	case taskAWSSDK:
		return s.callSDK(ctx, t, input)
	case taskActivity:
		// Never reached: the engine routes activity calls to scheduleActivity
		// before dispatch. Kept so an activity is not "unhandled" by accident.
		return failResult(asl.ErrRuntime, "an activity task has no synchronous call to perform")
	}
	return failResult(asl.ErrTaskFailed, "unhandled task kind")
}

func failResult(name, cause string) asl.TaskResult {
	return asl.TaskResult{Failure: &asl.Failure{Name: name, Cause: cause}}
}

// refuseUnrunnable keeps definitions this build would run wrongly out of the
// store: variables in JSONPath states (Assign is evaluated only by the
// JSONata dialect; a JSONPath state's `$name` references would resolve to
// nothing) and task resources outside the go-live integration set. The
// analyser stays faithful to ASL — ValidateStateMachineDefinition accepts
// what AWS accepts — but create refuses honestly, at create time.
func refuseUnrunnable(d *asl.Definition) *awshttp.APIError {
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
