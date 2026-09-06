package stepfunctions

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

// The optimized SQS and SNS integrations. Each takes its service's own API
// parameters and answers with that API's response shape, which is what the
// real integrations do — a machine asserting on $.SendMessageOutput.MessageId
// must find it.

func (s *Server) sendSQS(ctx context.Context, input []byte) asl.TaskResult {
	var in struct {
		QueueURL          string          `json:"QueueUrl"`
		MessageBody       json.RawMessage `json:"MessageBody"`
		MessageAttributes map[string]struct {
			DataType    string `json:"DataType"`
			StringValue string `json:"StringValue"`
		} `json:"MessageAttributes"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.QueueURL == "" {
		return failResult(asl.ErrTaskFailed, "sqs:sendMessage needs a QueueUrl parameter")
	}
	queue := in.QueueURL[strings.LastIndex(in.QueueURL, "/")+1:]
	body := stringOrJSON(in.MessageBody)
	attrs := map[string]string{}
	for k, v := range in.MessageAttributes {
		attrs[k] = v.StringValue
	}
	id, md5, err := peercall.SQSSendDetailed(ctx, s.peers, queue, body, attrs)
	if err != nil {
		return failResult(asl.ErrTaskFailed, err.Error())
	}
	out, _ := json.Marshal(map[string]any{
		"MessageId":        id,
		"MD5OfMessageBody": md5,
	})
	return asl.TaskResult{Output: out}
}

func (s *Server) publishSNS(ctx context.Context, input []byte) asl.TaskResult {
	var in struct {
		TopicArn string          `json:"TopicArn"`
		Message  json.RawMessage `json:"Message"`
		Subject  string          `json:"Subject"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.TopicArn == "" {
		return failResult(asl.ErrTaskFailed, "sns:publish needs a TopicArn parameter")
	}
	id, err := peercall.SNSPublishDetailed(ctx, s.peers, in.TopicArn, stringOrJSON(in.Message), in.Subject)
	if err != nil {
		return failResult(asl.ErrTaskFailed, err.Error())
	}
	out, _ := json.Marshal(map[string]any{"MessageId": id})
	return asl.TaskResult{Output: out}
}

// stringOrJSON renders a message body: a JSON string is unwrapped, anything
// else travels as its JSON text — which is how SFN hands an object-valued
// MessageBody to SQS.
func stringOrJSON(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
