// Package peercall holds the tiny typed clients doze-aws services use to call
// each other — hand-rolled requests in the target service's own wire format,
// so aws-sdk-go stays a test-only dependency. All calls are best-effort:
// callers log and drop on failure, they never crash a publish path.
package peercall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/trace"
	"github.com/doze-dev/doze-aws/peers"
)

// SQSSend sends one message to a queue by name (SQS JSON protocol).
func SQSSend(ctx context.Context, dir peers.Directory, queue, body string, attrs map[string]string) error {
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
	return trace.Step(ctx, trace.Event{Service: "sqs", Action: "SendMessage", Resource: queue},
		func(ctx context.Context) error {
			// The cause rides on the message, because the causal chain has to
			// survive the queue: whoever drains this has no request context and
			// the message is the only thing that still knows what caused it.
			// AWSTraceHeader is a system attribute, so it stays out of the
			// application's own attributes and out of their MD5.
			if h := trace.Header(ctx); h != "" {
				payload["MessageSystemAttributes"] = map[string]any{
					"AWSTraceHeader": map[string]string{"DataType": "String", "StringValue": h},
				}
			}
			return postJSON(ctx, ep, "AmazonSQS.SendMessage", "application/x-amz-json-1.0", payload)
		})
}

// SQSReceive long-polls a queue for up to max messages (used by Lambda event
// source mappings).
type SQSMessage struct {
	MessageID     string            `json:"MessageId"`
	ReceiptHandle string            `json:"ReceiptHandle"`
	Body          string            `json:"Body"`
	Attributes    map[string]string `json:"Attributes"`
}

// TraceHeader is the causal link the sender left on this message, or "".
func (m SQSMessage) TraceHeader() string { return m.Attributes["AWSTraceHeader"] }

func SQSReceive(ctx context.Context, dir peers.Directory, queue string, max, waitSeconds int) ([]SQSMessage, error) {
	ep, ok := dir.Endpoint("sqs")
	if !ok {
		return nil, fmt.Errorf("no sqs peer wired")
	}
	payload := map[string]any{
		"QueueUrl":            "http://sqs.doze-aws.internal/" + awsident.AccountID + "/" + queue,
		"MaxNumberOfMessages": max,
		"WaitTimeSeconds":     waitSeconds,
		// Ask for the trace header the sender may have left, so a poller can
		// continue the chain the message came from.
		"AttributeNames": []string{"AWSTraceHeader"},
	}
	body, err := postJSONResult(ctx, ep, "AmazonSQS.ReceiveMessage", "application/x-amz-json-1.0", payload)
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

// DDBGetShardIterator opens a shard iterator on a DynamoDB stream (used by
// Lambda event source mappings whose EventSourceArn is a stream ARN).
func DDBGetShardIterator(ctx context.Context, dir peers.Directory, streamArn, shardID, iterType string) (string, error) {
	ep, ok := dir.Endpoint("dynamodb")
	if !ok {
		return "", fmt.Errorf("no dynamodb peer wired")
	}
	body, err := postJSONResult(ctx, ep, "DynamoDBStreams_20120810.GetShardIterator", "application/x-amz-json-1.0",
		map[string]any{"StreamArn": streamArn, "ShardId": shardID, "ShardIteratorType": iterType})
	if err != nil {
		return "", err
	}
	var out struct {
		ShardIterator string `json:"ShardIterator"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.ShardIterator, nil
}

// DDBGetRecords fetches records from a stream shard iterator, returning the raw
// record documents and the next iterator to poll.
func DDBGetRecords(ctx context.Context, dir peers.Directory, iterator string, limit int) (records []json.RawMessage, next string, err error) {
	ep, ok := dir.Endpoint("dynamodb")
	if !ok {
		return nil, "", fmt.Errorf("no dynamodb peer wired")
	}
	body, err := postJSONResult(ctx, ep, "DynamoDBStreams_20120810.GetRecords", "application/x-amz-json-1.0",
		map[string]any{"ShardIterator": iterator, "Limit": limit})
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Records           []json.RawMessage `json:"Records"`
		NextShardIterator string            `json:"NextShardIterator"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", err
	}
	return out.Records, out.NextShardIterator, nil
}

// DDBStreamShardID is the single shard doze-aws streams expose.
const DDBStreamShardID = "shardId-00000000000000000000-00000000"

// KinesisRecord is one record read from a Kinesis shard, in the shape a Lambda
// event source mapping needs to build its event payload.
type KinesisRecord struct {
	SequenceNumber              string  `json:"SequenceNumber"`
	ApproximateArrivalTimestamp float64 `json:"ApproximateArrivalTimestamp"`
	Data                        []byte  `json:"Data"`
	PartitionKey                string  `json:"PartitionKey"`
}

// KinesisListShards returns the shard ids of a stream. A mapping polls every
// shard, so it needs the list up front and again after a reshard.
func KinesisListShards(ctx context.Context, dir peers.Directory, stream string) ([]string, error) {
	ep, ok := dir.Endpoint("kinesis")
	if !ok {
		return nil, fmt.Errorf("no kinesis peer wired")
	}
	body, err := postJSONResult(ctx, ep, "Kinesis_20131202.ListShards", "application/x-amz-json-1.1",
		map[string]any{"StreamName": stream})
	if err != nil {
		return nil, err
	}
	var out struct {
		Shards []struct {
			ShardID string `json:"ShardId"`
		} `json:"Shards"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Shards))
	for _, sh := range out.Shards {
		ids = append(ids, sh.ShardID)
	}
	return ids, nil
}

// KinesisGetShardIterator opens a shard iterator on a Kinesis stream.
func KinesisGetShardIterator(ctx context.Context, dir peers.Directory, stream, shardID, iterType string) (string, error) {
	ep, ok := dir.Endpoint("kinesis")
	if !ok {
		return "", fmt.Errorf("no kinesis peer wired")
	}
	body, err := postJSONResult(ctx, ep, "Kinesis_20131202.GetShardIterator", "application/x-amz-json-1.1",
		map[string]any{"StreamName": stream, "ShardId": shardID, "ShardIteratorType": iterType})
	if err != nil {
		return "", err
	}
	var out struct {
		ShardIterator string `json:"ShardIterator"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.ShardIterator, nil
}

// KinesisGetRecords fetches records from a Kinesis shard iterator. A nil next
// iterator means the shard is closed and drained — the caller should re-list
// shards and pick up the children.
func KinesisGetRecords(ctx context.Context, dir peers.Directory, iterator string, limit int) (recs []KinesisRecord, next string, err error) {
	ep, ok := dir.Endpoint("kinesis")
	if !ok {
		return nil, "", fmt.Errorf("no kinesis peer wired")
	}
	body, err := postJSONResult(ctx, ep, "Kinesis_20131202.GetRecords", "application/x-amz-json-1.1",
		map[string]any{"ShardIterator": iterator, "Limit": limit})
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Records           []KinesisRecord `json:"Records"`
		NextShardIterator *string         `json:"NextShardIterator"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", err
	}
	if out.NextShardIterator != nil {
		next = *out.NextShardIterator
	}
	return out.Records, next, nil
}

// SQSDelete acknowledges one received message.
func SQSDelete(ctx context.Context, dir peers.Directory, queue, receiptHandle string) error {
	ep, ok := dir.Endpoint("sqs")
	if !ok {
		return fmt.Errorf("no sqs peer wired")
	}
	return postJSON(ctx, ep, "AmazonSQS.DeleteMessage", "application/x-amz-json-1.0", map[string]any{
		"QueueUrl":      "http://sqs.doze-aws.internal/" + awsident.AccountID + "/" + queue,
		"ReceiptHandle": receiptHandle,
	})
}

// LambdaInvokeAsync fires an Event-type invocation of a function.
// LambdaInvoke fires a synchronous (RequestResponse) invocation and returns the
// function's response payload. Used by Secrets Manager rotation, which drives a
// rotation function step by step.
func LambdaInvoke(ctx context.Context, dir peers.Directory, function string, payload []byte) ([]byte, error) {
	var out []byte
	err := trace.Step(ctx, trace.Event{Service: "lambda", Action: "Invoke", Resource: function},
		func(ctx context.Context) error {
			var e error
			out, e = lambdaInvoke(ctx, dir, function, payload)
			return e
		})
	return out, err
}

func lambdaInvoke(ctx context.Context, dir peers.Directory, function string, payload []byte) ([]byte, error) {
	ep, ok := dir.Endpoint("lambda")
	if !ok {
		return nil, fmt.Errorf("no lambda peer wired")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ep.URL("/2015-03-31/functions/"+url.PathEscape(function)+"/invocations"),
		bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Amz-Invocation-Type", "RequestResponse")
	req.Header.Set("Content-Type", "application/json")
	resp, err := ep.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxPeerResponse))
	if resp.StatusCode/100 != 2 {
		return body, fmt.Errorf("lambda invoke: %s: %s", resp.Status, body)
	}
	if fnErr := resp.Header.Get("X-Amz-Function-Error"); fnErr != "" {
		return body, fmt.Errorf("function error (%s): %s", fnErr, body)
	}
	return body, nil
}

func LambdaInvokeAsync(ctx context.Context, dir peers.Directory, function string, payload []byte) error {
	return trace.Step(ctx, trace.Event{Service: "lambda", Action: "Invoke (async)", Resource: function},
		func(ctx context.Context) error { return lambdaInvokeAsync(ctx, dir, function, payload) })
}

func lambdaInvokeAsync(ctx context.Context, dir peers.Directory, function string, payload []byte) error {
	ep, ok := dir.Endpoint("lambda")
	if !ok {
		return fmt.Errorf("no lambda peer wired")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
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
func SNSPublish(ctx context.Context, dir peers.Directory, topicARN, message string) error {
	return trace.Step(ctx, trace.Event{Service: "sns", Action: "Publish", Resource: arnTail(topicARN)},
		func(ctx context.Context) error { return snsPublish(ctx, dir, topicARN, message) })
}

func snsPublish(ctx context.Context, dir peers.Directory, topicARN, message string) error {
	ep, ok := dir.Endpoint("sns")
	if !ok {
		return fmt.Errorf("no sns peer wired")
	}
	form := url.Values{
		"Action":   {"Publish"},
		"TopicArn": {topicARN},
		"Message":  {message},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL("/"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := ep.Client.Do(req)
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

func postJSON(ctx context.Context, ep peers.Endpoint, target, contentType string, payload any) error {
	_, err := postJSONResult(ctx, ep, target, contentType, payload)
	return err
}

func postJSONResult(ctx context.Context, ep peers.Endpoint, target, contentType string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	// WithContext, not a bare request: in the embedded topology the peer's
	// handler is invoked with this very request, so the trace values ride
	// along and a cascade can be more than one hop deep.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL("/"), bytes.NewReader(body))
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
	// Cap generously so a legitimate response never truncates: a 1 MiB limit
	// silently corrupted a full SQS ReceiveMessage batch (10 × 256 KB), stalling
	// the ESM poller forever. This sits above every relevant AWS payload limit
	// (SQS batch ≈ 2.6 MB, Lambda sync response 6 MB).
	out, _ := io.ReadAll(io.LimitReader(resp.Body, maxPeerResponse))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s: %s: %s", target, resp.Status, out)
	}
	return out, nil
}

// maxPeerResponse bounds an in-process peer response body.
const maxPeerResponse = 16 << 20

// S3Get fetches an object from the local S3, path-style. Lambda uses it to
// materialize function code that a deploy tool uploaded to a staging bucket —
// which is what `sam deploy` and `cdk deploy` both do.
func S3Get(ctx context.Context, dir peers.Directory, bucket, key string) ([]byte, error) {
	ep, ok := dir.Endpoint("s3")
	if !ok {
		return nil, fmt.Errorf("no s3 peer wired")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.BaseURL+"/"+bucket+"/"+key, nil)
	if err != nil {
		return nil, err
	}
	resp, err := ep.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("s3 GET %s/%s: %d", bucket, key, resp.StatusCode)
	}
	return body, nil
}

// KMSKeyState is what a service that encrypts with a customer key needs to
// know before it accepts one: whether the key resolves at all, and whether it
// is in a state that can be used.
type KMSKeyState struct {
	KeyID string
	State string // "Enabled", "Disabled", "PendingDeletion", ...
	Found bool
}

// Usable reports whether encryption with this key would succeed.
func (k KMSKeyState) Usable() bool { return k.Found && k.State == "Enabled" }

// KMSDescribeKey resolves a key through the local KMS. A service holding a
// customer key is expected to fail loudly when that key stops being usable —
// AWS answers KMSNotFound/KMSDisabled/KMSInvalidState rather than carrying on
// — so the caller needs the difference between "no such key", "not usable
// right now", and "KMS is not wired at all".
//
// A directory with no KMS peer yields ok=false rather than an error: a stack
// assembled without KMS should not have its other services start refusing
// writes.
func KMSDescribeKey(ctx context.Context, dir peers.Directory, keyID string) (state KMSKeyState, ok bool, err error) {
	ep, wired := dir.Endpoint("kms")
	if !wired {
		return KMSKeyState{}, false, nil
	}
	out, err := postJSONResult(ctx, ep, "TrentService.DescribeKey", "application/x-amz-json-1.1",
		map[string]any{"KeyId": keyID})
	if err != nil {
		// KMS answers NotFoundException for a key that does not exist; that is
		// an answer, not a failure to reach it.
		if strings.Contains(err.Error(), "NotFoundException") {
			return KMSKeyState{KeyID: keyID}, true, nil
		}
		return KMSKeyState{}, false, err
	}
	var resp struct {
		KeyMetadata struct {
			KeyID    string `json:"KeyId"`
			KeyState string `json:"KeyState"`
			Enabled  bool   `json:"Enabled"`
		} `json:"KeyMetadata"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return KMSKeyState{}, false, err
	}
	m := resp.KeyMetadata
	st := m.KeyState
	if st == "" {
		// Older shapes report usability only as a boolean.
		st = "Disabled"
		if m.Enabled {
			st = "Enabled"
		}
	}
	return KMSKeyState{KeyID: m.KeyID, State: st, Found: true}, true, nil
}

// arnTail is the last colon-separated segment of an ARN — the topic or queue
// name, which is what a reader recognises.
func arnTail(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}
