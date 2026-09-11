package cloudformation

// Export of SQS queues, including the redrive policy that names a dead-letter queue.

import (
	"github.com/doze-dev/doze-aws/provision"
)

func emitQueues(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Queues) {
		q := s.Queues[name]
		props := map[string]any{"QueueName": name}
		putIf(props, "FifoQueue", q.FIFO)
		putIf(props, "ContentBasedDeduplication", q.ContentDedup)
		putIfNum(props, "VisibilityTimeout", q.Visibility)
		putIfNum(props, "DelaySeconds", q.Delay)
		putIfNum(props, "MessageRetentionPeriod", q.Retention)
		putIfNum(props, "ReceiveMessageWaitTimeSeconds", q.ReceiveWait)
		putIfNum(props, "MaximumMessageSize", q.MaxSize)
		if q.DLQ != "" && q.DLQ != "auto" {
			props["RedrivePolicy"] = map[string]any{
				"deadLetterTargetArn": arnSub("sqs", q.DLQ),
				"maxReceiveCount":     orDefaultInt(q.MaxReceives, 3),
			}
		}
		putTags(props, q.Tags)
		add("Queue", name, "AWS::SQS::Queue", props)
	}
}
