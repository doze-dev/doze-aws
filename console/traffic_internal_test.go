package console

import "testing"

// JSON-protocol SQS clients may address by QueueUrl or bare QueueName; the
// wire's resource column must survive both.
func TestJSONResourceQueueName(t *testing.T) {
	if got := jsonResource("sqs", `{"QueueUrl":"http://host/sqs/emails"}`); got != "emails" {
		t.Fatalf("QueueUrl: got %q", got)
	}
	if got := jsonResource("sqs", `{"QueueName":"emails","WaitTimeSeconds":2}`); got != "emails" {
		t.Fatalf("QueueName: got %q", got)
	}
}
