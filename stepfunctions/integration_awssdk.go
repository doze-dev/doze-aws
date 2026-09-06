package stepfunctions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awsquery"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

// The generic arn:aws:states:::aws-sdk:<service>:<action> integration.
//
// On AWS the Parameters are the API's input members in PascalCase, the
// result is the API's output in PascalCase, and an error is named
// "<ServiceName>.<ErrorName>" — both halves Pascal-cased, and the error
// name ALWAYS carrying an Exception suffix, added when the service's own
// code lacks one (S3's BucketAlreadyExists is S3.BucketAlreadyExistsException).
// Source: "Learning to use AWS service SDK integrations in Step Functions",
// docs.aws.amazon.com/step-functions/latest/dg/supported-services-awssdk.html
// — the per-service "Exception prefix" entries there are what sdkServices
// transcribes (DynamoDb, not DynamoDB; Sqs, not SQS).
//
// Member spelling: every JSON and Query service here already speaks
// PascalCase on the wire, so Parameters pass through untouched. Step
// Functions itself is the exception — its wire is lowercase-initial
// (stateMachineArn) while the integration is written PascalCase
// (StateMachineArn), exactly as AWS documents for aws-sdk:sfn — so its keys
// are re-cased on the way in and out.
//
// Action validation happens at create time for the families whose action set
// is fixed here (S3 — see integration_s3.go — and Lambda); for the JSON and
// Query services the peer is the authority — an action it does not implement
// fails the execution under <Prefix>.<Code>Exception, the same name a real
// deploy would have surfaced. The REST control planes this repo also serves
// (apigateway) have no generic mapping and are refused by name.

type sdkProto int

const (
	protoJSON sdkProto = iota
	protoQuery
	protoS3
	protoLambda
)

// sdkService is one service the generic integration can reach: how the
// gateway names it, how AWS prefixes its errors, and how to speak to it.
type sdkService struct {
	Peer       string // gateway service name
	Prefix     string // AWS's exception prefix for the service
	Proto      sdkProto
	Target     string   // X-Amz-Target prefix (JSON protocols)
	JSONVer    string   // awsJson version (JSON protocols)
	LowerCamel bool     // wire members are lowercase-initial
	Actions    []string // fixed action set; nil means the peer decides
}

var sdkServices = map[string]sdkService{
	"dynamodb":       {Peer: "dynamodb", Prefix: "DynamoDb", Proto: protoJSON, Target: "DynamoDB_20120810", JSONVer: "1.0"},
	"eventbridge":    {Peer: "eventbridge", Prefix: "EventBridge", Proto: protoJSON, Target: "AWSEvents", JSONVer: "1.1"},
	"sqs":            {Peer: "sqs", Prefix: "Sqs", Proto: protoJSON, Target: "AmazonSQS", JSONVer: "1.0"},
	"kms":            {Peer: "kms", Prefix: "Kms", Proto: protoJSON, Target: "TrentService", JSONVer: "1.1"},
	"ssm":            {Peer: "ssm", Prefix: "Ssm", Proto: protoJSON, Target: "AmazonSSM", JSONVer: "1.1"},
	"secretsmanager": {Peer: "secretsmanager", Prefix: "SecretsManager", Proto: protoJSON, Target: "secretsmanager", JSONVer: "1.1"},
	"kinesis":        {Peer: "kinesis", Prefix: "Kinesis", Proto: protoJSON, Target: "Kinesis_20131202", JSONVer: "1.1"},
	"sfn":            {Peer: "stepfunctions", Prefix: "Sfn", Proto: protoJSON, Target: "AWSStepFunctions", JSONVer: "1.0", LowerCamel: true},
	"sns":            {Peer: "sns", Prefix: "Sns", Proto: protoQuery},
	"sts":            {Peer: "sts", Prefix: "Sts", Proto: protoQuery},
	"iam":            {Peer: "iam", Prefix: "Iam", Proto: protoQuery},
	"cloudformation": {Peer: "cloudformation", Prefix: "CloudFormation", Proto: protoQuery},
	"s3":             {Peer: "s3", Prefix: "S3", Proto: protoS3, Actions: []string{"getObject", "putObject", "deleteObject", "headObject", "listObjectsV2"}},
	"lambda":         {Peer: "lambda", Prefix: "Lambda", Proto: protoLambda, Actions: []string{"invoke"}},
}

// sdkServiceNames lists the callable services, for refusal messages.
func sdkServiceNames() string {
	names := make([]string, 0, len(sdkServices))
	for n := range sdkServices {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// parseSDKResource fills in a TaskTarget for aws-sdk:<service>:<action>, or
// says why the definition cannot be created.
func parseSDKResource(t TaskTarget, rest string) (TaskTarget, error) {
	service, action, ok := strings.Cut(rest, ":")
	if !ok || service == "" || action == "" {
		return t, fmt.Errorf("an aws-sdk resource is arn:aws:states:::aws-sdk:<service>:<action>")
	}
	svc, known := sdkServices[service]
	if !known {
		return t, fmt.Errorf("aws-sdk:%s is not a service this build calls (%s are)", service, sdkServiceNames())
	}
	if action[0] < 'a' || action[0] > 'z' {
		return t, fmt.Errorf("aws-sdk:%s:%s: the action is the SDK method name, lowercase-initial (e.g. %s)", service, action, exampleAction(svc))
	}
	if svc.Actions != nil && !contains(svc.Actions, action) {
		return t, fmt.Errorf("aws-sdk:%s:%s is not an action this build calls (%s are)", service, action, strings.Join(svc.Actions, ", "))
	}
	t.Kind, t.Service, t.Action = taskAWSSDK, service, action
	return t, nil
}

func exampleAction(svc sdkService) string {
	if len(svc.Actions) > 0 {
		return svc.Actions[0]
	}
	return "getItem"
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// callSDK dispatches one aws-sdk task by protocol.
func (s *Server) callSDK(ctx context.Context, t TaskTarget, input []byte) asl.TaskResult {
	svc := sdkServices[t.Service]
	switch svc.Proto {
	case protoJSON:
		return s.callJSON(ctx, svc, t.Action, input, svc.Prefix, true)
	case protoQuery:
		return s.callQuery(ctx, svc, t.Action, input)
	case protoS3:
		return s.callS3(ctx, t.Action, input)
	case protoLambda:
		return s.invokeLambdaAPI(ctx, input)
	}
	return failResult(asl.ErrTaskFailed, "unhandled aws-sdk protocol")
}

// sdkErrorName spells an API error the way a Catch names it: the service's
// prefix, then the code, with the Exception suffix the aws-sdk convention
// insists on. The optimized integrations (DynamoDB., EventBridge.) pass
// their codes through unsuffixed, as AWS does for those.
func sdkErrorName(prefix, code string, forceSuffix bool) string {
	if forceSuffix && !strings.HasSuffix(code, "Exception") {
		code += "Exception"
	}
	return prefix + "." + code
}

// failFor turns a peercall error into a TaskResult: an API error under its
// service-prefixed name, anything else (no peer, transport) as
// States.TaskFailed.
func failFor(err error, prefix string, forceSuffix bool) asl.TaskResult {
	var apiErr *peercall.APIError
	if errors.As(err, &apiErr) {
		return failResult(sdkErrorName(prefix, apiErr.Code, forceSuffix), apiErr.Message)
	}
	return failResult(asl.ErrTaskFailed, err.Error())
}

// callJSON is the awsJson path, shared with the optimized DynamoDB and
// EventBridge integrations (which differ only in prefix and suffix rule).
// label, when given, names the resource on the wire; otherwise it is read
// from the parameters.
func (s *Server) callJSON(ctx context.Context, svc sdkService, action string, input []byte, prefix string, forceSuffix bool, label ...string) asl.TaskResult {
	params, err := decodeParams(input)
	if err != nil {
		return failResult(asl.ErrTaskFailed, fmt.Sprintf("%s:%s: Parameters must be an object: %v", svc.Peer, action, err))
	}
	if svc.LowerCamel {
		params = recaseKeys(params, lowerFirst).(map[string]any)
	}
	body, _ := json.Marshal(params)
	resource := resourceLabel(params)
	if len(label) > 0 {
		resource = label[0]
	}
	out, err := peercall.JSONCall(ctx, s.peers, svc.Peer, svc.Target+"."+upperFirst(action),
		"application/x-amz-json-"+svc.JSONVer, resource, body)
	if err != nil {
		return failFor(err, prefix, forceSuffix)
	}
	if len(out) == 0 {
		out = []byte("{}")
	}
	if svc.LowerCamel {
		var doc any
		if json.Unmarshal(out, &doc) == nil {
			out, _ = json.Marshal(recaseKeys(doc, upperFirst))
		}
	}
	return asl.TaskResult{Output: out}
}

// callQuery is the Query-protocol path: JSON parameters flattened by the
// awsquery encoder, the XML result lifted back to JSON.
func (s *Server) callQuery(ctx context.Context, svc sdkService, action string, input []byte) asl.TaskResult {
	params, err := decodeParams(input)
	if err != nil {
		return failResult(asl.ErrTaskFailed, fmt.Sprintf("%s:%s: Parameters must be an object: %v", svc.Peer, action, err))
	}
	pascal := upperFirst(action)
	form := awsquery.Encode(pascal, params, awsquery.EncodeOptions{})
	out, err := peercall.QueryCall(ctx, s.peers, svc.Peer, pascal, resourceLabel(params), form)
	if err != nil {
		return failFor(err, svc.Prefix, true)
	}
	body, _ := json.Marshal(out)
	return asl.TaskResult{Output: body}
}

func decodeParams(input []byte) (map[string]any, error) {
	params := map[string]any{}
	if len(input) == 0 {
		return params, nil
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, err
	}
	return params, nil
}

// resourceMembers are the members that name what a call is about, in the
// order to prefer them — for the console's wire, not for the call.
var resourceMembers = []string{
	"TableName", "QueueUrl", "TopicArn", "FunctionName", "StateMachineArn", "SecretId",
	"KeyId", "StreamName", "RoleName", "Bucket", "EventBusName", "Name",
}

func resourceLabel(params map[string]any) string {
	for _, m := range resourceMembers {
		if v, ok := params[m].(string); ok && v != "" {
			return tail(v)
		}
	}
	return ""
}

// tail is the recognisable end of an ARN or URL.
func tail(v string) string {
	if i := strings.LastIndexAny(v, ":/"); i >= 0 && i < len(v)-1 {
		return v[i+1:]
	}
	return v
}

// recaseKeys rewrites every object key in a JSON document with fn.
func recaseKeys(doc any, fn func(string) string) any {
	switch t := doc.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[fn(k)] = recaseKeys(v, fn)
		}
		return out
	case []any:
		for i, v := range t {
			t[i] = recaseKeys(v, fn)
		}
		return t
	}
	return doc
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
