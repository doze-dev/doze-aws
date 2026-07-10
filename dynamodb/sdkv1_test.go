// SDK v1 contract tests: the legacy aws-sdk-go (v1) DynamoDB client.
package dynamodb_test

import (
	"net/http/httptest"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	ddbv1 "github.com/aws/aws-sdk-go/service/dynamodb"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/dynamodb"
)

func ddbV1Client(t *testing.T) *ddbv1.DynamoDB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	s, err := dynamodb.New(dynamodb.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(ts.URL).
		WithCredentials(credsv1.NewStaticCredentials(awsident.AccessKeyID, awsident.SecretAccessKey, "")))
	if err != nil {
		t.Fatal(err)
	}
	return ddbv1.New(sess)
}

func TestSDKV1RoundTrip(t *testing.T) {
	c := ddbV1Client(t)

	if _, err := c.CreateTable(&ddbv1.CreateTableInput{
		TableName: awsv1.String("legacy"),
		AttributeDefinitions: []*ddbv1.AttributeDefinition{
			{AttributeName: awsv1.String("id"), AttributeType: awsv1.String("S")},
		},
		KeySchema: []*ddbv1.KeySchemaElement{
			{AttributeName: awsv1.String("id"), KeyType: awsv1.String("HASH")},
		},
		BillingMode: awsv1.String("PAY_PER_REQUEST"),
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if _, err := c.PutItem(&ddbv1.PutItemInput{
		TableName: awsv1.String("legacy"),
		Item: map[string]*ddbv1.AttributeValue{
			"id":    {S: awsv1.String("one")},
			"count": {N: awsv1.String("42")},
			"tags":  {SS: []*string{awsv1.String("a"), awsv1.String("b")}},
		},
	}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	got, err := c.GetItem(&ddbv1.GetItemInput{
		TableName: awsv1.String("legacy"),
		Key:       map[string]*ddbv1.AttributeValue{"id": {S: awsv1.String("one")}},
	})
	if err != nil || got.Item == nil {
		t.Fatalf("GetItem: %v", err)
	}
	if awsv1.StringValue(got.Item["count"].N) != "42" || len(got.Item["tags"].SS) != 2 {
		t.Fatalf("item = %v", got.Item)
	}

	// Legacy-style query through the v1 client (expression API).
	q, err := c.Query(&ddbv1.QueryInput{
		TableName:              awsv1.String("legacy"),
		KeyConditionExpression: awsv1.String("id = :id"),
		ExpressionAttributeValues: map[string]*ddbv1.AttributeValue{
			":id": {S: awsv1.String("one")},
		},
	})
	if err != nil || awsv1.Int64Value(q.Count) != 1 {
		t.Fatalf("Query: %v count=%v", err, q.Count)
	}

	// Coded errors parse through the v1 deserializer.
	_, err = c.GetItem(&ddbv1.GetItemInput{
		TableName: awsv1.String("absent"),
		Key:       map[string]*ddbv1.AttributeValue{"id": {S: awsv1.String("x")}},
	})
	type coder interface{ Code() string }
	if ce, ok := err.(coder); !ok || ce.Code() != "ResourceNotFoundException" {
		t.Fatalf("missing table error = %v", err)
	}
}
