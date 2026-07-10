package s3

// S3 event notifications: on object create/remove, match the bucket's
// NotificationConfiguration and deliver an S3 event record to SQS queues, SNS
// topics, and Lambda functions via the peers directory. Delivery is
// best-effort and asynchronous — it never blocks or fails the object operation.

import (
	"encoding/json"
	"encoding/xml"
	"net/url"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/internal/s3store"
)

type notificationConfig struct {
	Queues  []targetConfig `xml:"QueueConfiguration"`
	Topics  []targetConfig `xml:"TopicConfiguration"`
	Lambdas []targetConfig `xml:"CloudFunctionConfiguration"`
	// Lambda targets also arrive as LambdaFunctionConfiguration.
	Lambdas2 []targetConfig `xml:"LambdaFunctionConfiguration"`
}

type targetConfig struct {
	ID       string   `xml:"Id"`
	Queue    string   `xml:"Queue"`
	Topic    string   `xml:"Topic"`
	Lambda   string   `xml:"CloudFunction"`
	LambdaFn string   `xml:"LambdaFunctionArn"`
	Events   []string `xml:"Event"`
	Filter   struct {
		S3Key struct {
			Rules []struct {
				Name  string `xml:"Name"`
				Value string `xml:"Value"`
			} `xml:"FilterRule"`
		} `xml:"S3Key"`
	} `xml:"Filter"`
}

// notify fires notifications for one object event. eventName is e.g.
// "s3:ObjectCreated:Put" or "s3:ObjectRemoved:Delete".
func (s *Server) notify(bucket, key, eventName string, v *s3store.ObjectVersion) {
	bk, err := s.store.GetBucket(bucket)
	if err != nil || bk.Notification == "" {
		return
	}
	var cfg notificationConfig
	if xml.Unmarshal([]byte(bk.Notification), &cfg) != nil {
		return
	}
	record := s.eventRecord(bucket, key, eventName, v)
	payload, _ := json.Marshal(map[string]any{"Records": []any{record}})

	deliver := func(t targetConfig, arn string) {
		if !eventMatches(t.Events, eventName) || !filterMatches(t, key) {
			return
		}
		go s.deliverNotification(arn, string(payload))
	}
	for _, t := range cfg.Queues {
		deliver(t, t.Queue)
	}
	for _, t := range cfg.Topics {
		deliver(t, t.Topic)
	}
	for _, t := range append(cfg.Lambdas, cfg.Lambdas2...) {
		arn := t.LambdaFn
		if arn == "" {
			arn = t.Lambda
		}
		deliver(t, arn)
	}
}

// eventRecord builds one S3 event notification record.
func (s *Server) eventRecord(bucket, key, eventName string, v *s3store.ObjectVersion) map[string]any {
	obj := map[string]any{
		"key":       url.QueryEscape(key),
		"sequencer": "0",
	}
	if v != nil {
		obj["size"] = v.Size
		obj["eTag"] = v.ETag
		if v.VersionID != "null" {
			obj["versionId"] = v.VersionID
		}
	}
	return map[string]any{
		"eventVersion": "2.1",
		"eventSource":  "aws:s3",
		"awsRegion":    awsident.Region,
		"eventTime":    time.Unix(s.now().Unix(), 0).UTC().Format(time.RFC3339),
		"eventName":    strings.TrimPrefix(eventName, "s3:"),
		"s3": map[string]any{
			"s3SchemaVersion": "1.0",
			"bucket": map[string]any{
				"name": bucket,
				"arn":  "arn:aws:s3:::" + bucket,
			},
			"object": obj,
		},
	}
}

// deliverNotification routes a payload to an SQS/SNS/Lambda ARN via peers.
func (s *Server) deliverNotification(arn, payload string) {
	switch {
	case strings.Contains(arn, ":sqs:"):
		queue := arn[strings.LastIndex(arn, ":")+1:]
		if err := peercall.SQSSend(s.peers, queue, payload, nil); err != nil {
			s.logf("s3: notify sqs %s: %v", queue, err)
		}
	case strings.Contains(arn, ":sns:"):
		if err := peercall.SNSPublish(s.peers, arn, payload); err != nil {
			s.logf("s3: notify sns: %v", err)
		}
	case strings.Contains(arn, ":lambda:"):
		fn := arn[strings.LastIndex(arn, ":")+1:]
		if err := peercall.LambdaInvokeAsync(s.peers, fn, []byte(payload)); err != nil {
			s.logf("s3: notify lambda %s: %v", fn, err)
		}
	}
}

// eventMatches reports whether eventName satisfies a configured event list.
// Wildcards like s3:ObjectCreated:* match any specific sub-event.
func eventMatches(configured []string, eventName string) bool {
	for _, e := range configured {
		if e == eventName {
			return true
		}
		if strings.HasSuffix(e, ":*") && strings.HasPrefix(eventName, strings.TrimSuffix(e, "*")) {
			return true
		}
		if e == "s3:ObjectCreated:*" && strings.HasPrefix(eventName, "s3:ObjectCreated:") {
			return true
		}
		if e == "s3:ObjectRemoved:*" && strings.HasPrefix(eventName, "s3:ObjectRemoved:") {
			return true
		}
	}
	return false
}

// filterMatches applies the prefix/suffix S3Key filter rules.
func filterMatches(t targetConfig, key string) bool {
	for _, rule := range t.Filter.S3Key.Rules {
		switch strings.ToLower(rule.Name) {
		case "prefix":
			if !strings.HasPrefix(key, rule.Value) {
				return false
			}
		case "suffix":
			if !strings.HasSuffix(key, rule.Value) {
				return false
			}
		}
	}
	return true
}
