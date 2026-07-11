package dynamodb_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestSDKTableAdmin(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)
	arn := "arn:aws:dynamodb:us-east-1:000000000000:table/orders"

	// Tags.
	if _, err := c.TagResource(ctx, &awsddb.TagResourceInput{ResourceArn: aws.String(arn), Tags: []ddbtypes.Tag{{Key: aws.String("env"), Value: aws.String("dev")}}}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}
	lt, err := c.ListTagsOfResource(ctx, &awsddb.ListTagsOfResourceInput{ResourceArn: aws.String(arn)})
	if err != nil || len(lt.Tags) != 1 {
		t.Fatalf("ListTagsOfResource = %v err=%v", lt.Tags, err)
	}
	if _, err := c.UntagResource(ctx, &awsddb.UntagResourceInput{ResourceArn: aws.String(arn), TagKeys: []string{"env"}}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	// UpdateTable (billing mode round-trip).
	if _, err := c.UpdateTable(ctx, &awsddb.UpdateTableInput{TableName: aws.String("orders"), BillingMode: ddbtypes.BillingModePayPerRequest}); err != nil {
		t.Fatalf("UpdateTable: %v", err)
	}

	// Canned describe endpoints.
	if _, err := c.DescribeLimits(ctx, &awsddb.DescribeLimitsInput{}); err != nil {
		t.Fatalf("DescribeLimits: %v", err)
	}
	if _, err := c.DescribeEndpoints(ctx, &awsddb.DescribeEndpointsInput{}); err != nil {
		t.Fatalf("DescribeEndpoints: %v", err)
	}
	if _, err := c.DescribeContinuousBackups(ctx, &awsddb.DescribeContinuousBackupsInput{TableName: aws.String("orders")}); err != nil {
		t.Fatalf("DescribeContinuousBackups: %v", err)
	}
}

func TestSDKPartiQLBatchAndTransaction(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)

	// BatchExecuteStatement: two inserts.
	if _, err := c.BatchExecuteStatement(ctx, &awsddb.BatchExecuteStatementInput{
		Statements: []ddbtypes.BatchStatementRequest{
			{Statement: aws.String(`INSERT INTO "orders" VALUE {'pk': 'u1', 'sk': 's1'}`)},
			{Statement: aws.String(`INSERT INTO "orders" VALUE {'pk': 'u2', 'sk': 's2'}`)},
		},
	}); err != nil {
		t.Fatalf("BatchExecuteStatement: %v", err)
	}

	// ExecuteTransaction: an insert.
	if _, err := c.ExecuteTransaction(ctx, &awsddb.ExecuteTransactionInput{
		TransactStatements: []ddbtypes.ParameterizedStatement{
			{Statement: aws.String(`INSERT INTO "orders" VALUE {'pk': 'u3', 'sk': 's3'}`)},
		},
	}); err != nil {
		t.Fatalf("ExecuteTransaction: %v", err)
	}
}
