package sqs

// SQS's model-derived input validation: the constraint tables, walked by
// internal/modelcheck.
//
// SQS speaks both protocols, so the request is rendered into one shape
// (params.asMap) before the walk and the error code follows the protocol the
// caller used. Generated with `dzaudit cases sqs`, replayed in
// sqs/rejection_parity_test.go.
//
// This supplements the hand-derived checks in attrs.go and store.go rather than
// replacing them: SQS's own service model states almost nothing about queue
// names or attribute ranges, so those rules exist only in prose and only there.

import (
	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

var constraintTables = map[string][]modelcheck.Constraint{
	"AddPermission": {
		{Path: "AWSAccountIds", Kind: modelcheck.KindRequired},
		{Path: "Actions", Kind: modelcheck.KindRequired},
		{Path: "Label", Kind: modelcheck.KindRequired},
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"CancelMessageMoveTask": {
		{Path: "TaskHandle", Kind: modelcheck.KindRequired},
	},
	"ChangeMessageVisibility": {
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
		{Path: "ReceiptHandle", Kind: modelcheck.KindRequired},
		{Path: "VisibilityTimeout", Kind: modelcheck.KindRequired},
	},
	"ChangeMessageVisibilityBatch": {
		{Path: "Entries", Kind: modelcheck.KindRequired},
		{Path: "Entries[].Id", Kind: modelcheck.KindRequired},
		{Path: "Entries[].ReceiptHandle", Kind: modelcheck.KindRequired},
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"CreateQueue": {
		{Path: "QueueName", Kind: modelcheck.KindRequired},
	},
	"DeleteMessage": {
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
		{Path: "ReceiptHandle", Kind: modelcheck.KindRequired},
	},
	"DeleteMessageBatch": {
		{Path: "Entries", Kind: modelcheck.KindRequired},
		{Path: "Entries[].Id", Kind: modelcheck.KindRequired},
		{Path: "Entries[].ReceiptHandle", Kind: modelcheck.KindRequired},
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"DeleteQueue": {
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"GetQueueAttributes": {
		{Path: "AttributeNames[]", Kind: modelcheck.KindEnum, Enum: []string{"All", "CreatedTimestamp", "LastModifiedTimestamp", "ReceiveMessageWaitTimeSeconds", "SqsManagedSseEnabled", "MaximumMessageSize", "MessageRetentionPeriod", "ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible", "ContentBasedDeduplication", "KmsDataKeyReusePeriodSeconds", "DeduplicationScope", "RedriveAllowPolicy", "Policy", "VisibilityTimeout", "QueueArn", "RedrivePolicy", "FifoQueue", "KmsMasterKeyId", "FifoThroughputLimit", "ApproximateNumberOfMessagesDelayed", "DelaySeconds"}},
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"GetQueueUrl": {
		{Path: "QueueName", Kind: modelcheck.KindRequired},
	},
	"ListDeadLetterSourceQueues": {
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"ListMessageMoveTasks": {
		{Path: "SourceArn", Kind: modelcheck.KindRequired},
	},
	"ListQueueTags": {
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"PurgeQueue": {
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"ReceiveMessage": {
		{Path: "AttributeNames[]", Kind: modelcheck.KindEnum, Enum: []string{"SqsManagedSseEnabled", "MaximumMessageSize", "MessageRetentionPeriod", "ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible", "ContentBasedDeduplication", "KmsDataKeyReusePeriodSeconds", "DeduplicationScope", "RedriveAllowPolicy", "Policy", "VisibilityTimeout", "QueueArn", "RedrivePolicy", "FifoQueue", "KmsMasterKeyId", "FifoThroughputLimit", "ApproximateNumberOfMessagesDelayed", "DelaySeconds", "All", "CreatedTimestamp", "LastModifiedTimestamp", "ReceiveMessageWaitTimeSeconds"}},
		{Path: "MessageSystemAttributeNames[]", Kind: modelcheck.KindEnum, Enum: []string{"SenderId", "SentTimestamp", "ApproximateFirstReceiveTimestamp", "SequenceNumber", "MessageGroupId", "AWSTraceHeader", "DeadLetterQueueSourceArn", "ApproximateReceiveCount", "MessageDeduplicationId", "All"}},
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"RemovePermission": {
		{Path: "Label", Kind: modelcheck.KindRequired},
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"SendMessage": {
		{Path: "MessageAttributes{}.DataType", Kind: modelcheck.KindRequired},
		{Path: "MessageBody", Kind: modelcheck.KindRequired},
		{Path: "MessageSystemAttributes{}.DataType", Kind: modelcheck.KindRequired},
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"SendMessageBatch": {
		{Path: "Entries", Kind: modelcheck.KindRequired},
		{Path: "Entries[].Id", Kind: modelcheck.KindRequired},
		{Path: "Entries[].MessageAttributes{}.DataType", Kind: modelcheck.KindRequired},
		{Path: "Entries[].MessageBody", Kind: modelcheck.KindRequired},
		{Path: "Entries[].MessageSystemAttributes{}.DataType", Kind: modelcheck.KindRequired},
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"SetQueueAttributes": {
		{Path: "Attributes", Kind: modelcheck.KindRequired},
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
	},
	"StartMessageMoveTask": {
		{Path: "SourceArn", Kind: modelcheck.KindRequired},
	},
	"TagQueue": {
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
		{Path: "Tags", Kind: modelcheck.KindRequired},
	},
	"UntagQueue": {
		{Path: "QueueUrl", Kind: modelcheck.KindRequired},
		{Path: "TagKeys", Kind: modelcheck.KindRequired},
	},
}
