package sns

// SNS's model-derived input validation: the constraint tables, walked by
// internal/modelcheck.
//
// SNS speaks the Query protocol, so the form is un-flattened into the nested
// shape the paths describe (modelcheck.FromQuery) before the walk — including
// the entry-list spelling of a map, which is how MessageAttributes arrives.
// Generated with `dzaudit cases sns`, replayed in sns/rejection_parity_test.go.

import "github.com/doze-dev/doze-aws/internal/modelcheck"

var constraintTables = map[string][]modelcheck.Constraint{
	"AddPermission": {
		{Path: "AWSAccountId", Kind: modelcheck.KindRequired},
		{Path: "ActionName", Kind: modelcheck.KindRequired},
		{Path: "Label", Kind: modelcheck.KindRequired},
		{Path: "TopicArn", Kind: modelcheck.KindRequired},
	},
	"ConfirmSubscription": {
		{Path: "Token", Kind: modelcheck.KindRequired},
		{Path: "TopicArn", Kind: modelcheck.KindRequired},
	},
	"CreateTopic": {
		{Path: "Name", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
	},
	"DeleteTopic": {
		{Path: "TopicArn", Kind: modelcheck.KindRequired},
	},
	"GetDataProtectionPolicy": {
		{Path: "ResourceArn", Kind: modelcheck.KindRequired},
	},
	"GetSubscriptionAttributes": {
		{Path: "SubscriptionArn", Kind: modelcheck.KindRequired},
	},
	"GetTopicAttributes": {
		{Path: "TopicArn", Kind: modelcheck.KindRequired},
	},
	"ListSubscriptionsByTopic": {
		{Path: "TopicArn", Kind: modelcheck.KindRequired},
	},
	"ListTagsForResource": {
		{Path: "ResourceArn", Kind: modelcheck.KindLength, Min: 1, Max: 1011},
		{Path: "ResourceArn", Kind: modelcheck.KindRequired},
	},
	"Publish": {
		{Path: "Message", Kind: modelcheck.KindRequired},
		{Path: "MessageAttributes{}.DataType", Kind: modelcheck.KindRequired},
	},
	"PublishBatch": {
		{Path: "PublishBatchRequestEntries", Kind: modelcheck.KindRequired},
		{Path: "PublishBatchRequestEntries[].Id", Kind: modelcheck.KindRequired},
		{Path: "PublishBatchRequestEntries[].Message", Kind: modelcheck.KindRequired},
		{Path: "PublishBatchRequestEntries[].MessageAttributes{}.DataType", Kind: modelcheck.KindRequired},
		{Path: "TopicArn", Kind: modelcheck.KindRequired},
	},
	"PutDataProtectionPolicy": {
		{Path: "DataProtectionPolicy", Kind: modelcheck.KindRequired},
		{Path: "ResourceArn", Kind: modelcheck.KindRequired},
	},
	"RemovePermission": {
		{Path: "Label", Kind: modelcheck.KindRequired},
		{Path: "TopicArn", Kind: modelcheck.KindRequired},
	},
	"SetSubscriptionAttributes": {
		{Path: "AttributeName", Kind: modelcheck.KindRequired},
		{Path: "SubscriptionArn", Kind: modelcheck.KindRequired},
	},
	"SetTopicAttributes": {
		{Path: "AttributeName", Kind: modelcheck.KindRequired},
		{Path: "TopicArn", Kind: modelcheck.KindRequired},
	},
	"Subscribe": {
		{Path: "Protocol", Kind: modelcheck.KindRequired},
		{Path: "TopicArn", Kind: modelcheck.KindRequired},
	},
	"TagResource": {
		{Path: "ResourceArn", Kind: modelcheck.KindLength, Min: 1, Max: 1011},
		{Path: "ResourceArn", Kind: modelcheck.KindRequired},
		{Path: "Tags", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
	},
	"Unsubscribe": {
		{Path: "SubscriptionArn", Kind: modelcheck.KindRequired},
	},
	"UntagResource": {
		{Path: "ResourceArn", Kind: modelcheck.KindLength, Min: 1, Max: 1011},
		{Path: "ResourceArn", Kind: modelcheck.KindRequired},
		{Path: "TagKeys", Kind: modelcheck.KindRequired},
		{Path: "TagKeys[]", Kind: modelcheck.KindLength, Min: 1, Max: 128},
	},
}
