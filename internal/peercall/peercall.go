// Package peercall holds the tiny typed clients doze-aws services use to call
// each other — hand-rolled requests in the target service's own wire format,
// so aws-sdk-go stays a test-only dependency. All calls are best-effort:
// callers log and drop on failure, they never crash a publish path.
package peercall

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/peers"
)

// SQSSend sends one message to a queue by name (SQS JSON protocol).
func SQSSend(dir peers.Directory, queue, body string, attrs map[string]string) error {
	ep, ok := dir.Endpoint("sqs")
	if !ok {
		return fmt.Errorf("no sqs peer wired")
	}
	payload := map[string]any{
		"QueueUrl":    "http://sqs.doze-aws.internal/" + awsident.AccountID + "/" + queue,
		"MessageBody": body,
	}
	if len(attrs) > 0 {
		ma := map[string]any{}
		for k, v := range attrs {
			ma[k] = map[string]string{"DataType": "String", "StringValue": v}
		}
		payload["MessageAttributes"] = ma
	}
	return postJSON(ep, "AmazonSQS.SendMessage", "application/x-amz-json-1.0", payload)
}

// SQSReceive long-polls a queue for up to max messages (used by Lambda event
// source mappings).
type SQSMessage struct {
	MessageID     string `json:"MessageId"`
	ReceiptHandle string `json:"ReceiptHandle"`
	Body          string `json:"Body"`
}

func SQSReceive(dir peers.Directory, queue string, max, waitSeconds int) ([]SQSMessage, error) {
	ep, ok := dir.Endpoint("sqs")
	if !ok {
		return nil, fmt.Errorf("no sqs peer wired")
	}
	payload := map[string]any{
		"QueueUrl":            "http://sqs.doze-aws.internal/" + awsident.AccountID + "/" + queue,
		"MaxNumberOfMessages": max,
		"WaitTimeSeconds":     waitSeconds,
	}
	body, err := postJSONResult(ep, "AmazonSQS.ReceiveMessage", "application/x-amz-json-1.0", payload)
	if err != nil {
		return nil, err
	}
	var out struct {
		Messages []SQSMessage `json:"Messages"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

// SQSDelete acknowledges one received message.
func SQSDelete(dir peers.Directory, queue, receiptHandle string) error {
	ep, ok := dir.Endpoint("sqs")
	if !ok {
		return fmt.Errorf("no sqs peer wired")
	}
	return postJSON(ep, "AmazonSQS.DeleteMessage", "application/x-amz-json-1.0", map[string]any{
		"QueueUrl":      "http://sqs.doze-aws.internal/" + awsident.AccountID + "/" + queue,
		"ReceiptHandle": receiptHandle,
	})
}

// LambdaInvokeAsync fires an Event-type invocation of a function.
func LambdaInvokeAsync(dir peers.Directory, function string, payload []byte) error {
	ep, ok := dir.Endpoint("lambda")
	if !ok {
		return fmt.Errorf("no lambda peer wired")
	}
	req, err := http.NewRequest(http.MethodPost,
		ep.URL("/2015-03-31/functions/"+url.PathEscape(function)+"/invocations"),
		bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("X-Amz-Invocation-Type", "Event")
	req.Header.Set("Content-Type", "application/json")
	resp, err := ep.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("lambda invoke: %s: %s", resp.Status, body)
	}
	return nil
}

// SNSPublish publishes a message to a topic by ARN (Query protocol).
func SNSPublish(dir peers.Directory, topicARN, message string) error {
	ep, ok := dir.Endpoint("sns")
	if !ok {
		return fmt.Errorf("no sns peer wired")
	}
	form := url.Values{
		"Action":   {"Publish"},
		"TopicArn": {topicARN},
		"Message":  {message},
	}
	resp, err := ep.Client.PostForm(ep.URL("/"), form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("sns publish: %s: %s", resp.Status, body)
	}
	return nil
}

func postJSON(ep peers.Endpoint, target, contentType string, payload any) error {
	_, err := postJSONResult(ep, target, contentType, payload)
	return err
}

func postJSONResult(ep peers.Endpoint, target, contentType string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, ep.URL("/"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Amz-Target", target)
	resp, err := ep.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s: %s: %s", target, resp.Status, out)
	}
	return out, nil
}
