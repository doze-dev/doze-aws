package stepfunctions

import (
	"context"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/asl"
)

// The optimized DynamoDB integration: arn:aws:states:::dynamodb:{getItem,
// putItem,updateItem,deleteItem}. Parameters are the API's own members with
// attribute values in DynamoDB JSON ({"S":"..."}), untouched; the result is
// the API response (getItem answers {"Item":{...}}, putItem with
// ReturnValues answers {"Attributes":{...}}).
//
// Errors are "DynamoDB.<Code>" — the optimized prefix, not the aws-sdk one
// (DynamoDb) — so a Catch on DynamoDB.ConditionalCheckFailedException routes
// a failed condition, as in AWS's own "Controlling concurrency in
// distributed systems using AWS Step Functions" (aws.amazon.com/blogs/compute)
// which catches exactly that name from a dynamodb:updateItem task.

var ddbOptimizedActions = []string{"getItem", "putItem", "updateItem", "deleteItem"}

func parseDynamoDBResource(t TaskTarget, action string) (TaskTarget, error) {
	if !contains(ddbOptimizedActions, action) {
		return t, fmt.Errorf("the dynamodb:%s integration is not one this build calls (dynamodb:%s are)",
			action, strings.Join(ddbOptimizedActions, ", dynamodb:"))
	}
	t.Kind, t.Service, t.Action = taskDynamoDB, "dynamodb", action
	return t, nil
}

func (s *Server) callDynamoDB(ctx context.Context, action string, input []byte) asl.TaskResult {
	return s.callJSON(ctx, sdkServices["dynamodb"], action, input, "DynamoDB", false)
}
