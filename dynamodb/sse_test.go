package dynamodb_test

// Server-side encryption round-trips. Nothing is enciphered locally, but a
// table asked for encryption that then describes itself as unencrypted is a
// drift source: Terraform writes the server_side_encryption block, reads the
// description back, finds it missing, and plans the same change forever.

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func createSSETable(t *testing.T, c *awsddb.Client, name string, sse *ddbtypes.SSESpecification) {
	t.Helper()
	_, err := c.CreateTable(context.Background(), &awsddb.CreateTableInput{
		TableName:            aws.String(name),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
		SSESpecification:     sse,
	})
	if err != nil {
		t.Fatalf("CreateTable %s: %v", name, err)
	}
}

func describeSSE(t *testing.T, c *awsddb.Client, name string) *ddbtypes.SSEDescription {
	t.Helper()
	out, err := c.DescribeTable(context.Background(), &awsddb.DescribeTableInput{TableName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeTable %s: %v", name, err)
	}
	return out.Table.SSEDescription
}

func TestSSESpecificationRoundTrips(t *testing.T) {
	c := ddbClient(t)
	createSSETable(t, c, "sse-on", &ddbtypes.SSESpecification{
		Enabled: aws.Bool(true), SSEType: ddbtypes.SSETypeKms,
		KMSMasterKeyId: aws.String("alias/aws/dynamodb"),
	})
	got := describeSSE(t, c, "sse-on")
	if got == nil {
		t.Fatal("SSEDescription missing on a table created with encryption enabled")
	}
	if got.Status != ddbtypes.SSEStatusEnabled {
		t.Errorf("status = %q, want ENABLED", got.Status)
	}
	if got.SSEType != ddbtypes.SSETypeKms {
		t.Errorf("SSEType = %q, want KMS", got.SSEType)
	}
	if aws.ToString(got.KMSMasterKeyArn) != "alias/aws/dynamodb" {
		t.Errorf("key = %q, want the one that was set", aws.ToString(got.KMSMasterKeyArn))
	}
}

// A table that never asked for encryption must not claim it.
func TestNoSSEDescriptionWhenUnencrypted(t *testing.T) {
	c := ddbClient(t)
	createSSETable(t, c, "sse-off", nil)
	if got := describeSSE(t, c, "sse-off"); got != nil {
		t.Errorf("unencrypted table reports SSEDescription %+v", got)
	}
}

// UpdateTable toggles it both ways, which is what a changed Terraform block does.
func TestSSEToggleViaUpdateTable(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	createSSETable(t, c, "sse-toggle", nil)

	if _, err := c.UpdateTable(ctx, &awsddb.UpdateTableInput{
		TableName:        aws.String("sse-toggle"),
		SSESpecification: &ddbtypes.SSESpecification{Enabled: aws.Bool(true)},
	}); err != nil {
		t.Fatalf("UpdateTable enabling SSE: %v", err)
	}
	if got := describeSSE(t, c, "sse-toggle"); got == nil || got.Status != ddbtypes.SSEStatusEnabled {
		t.Fatalf("SSE not reported after enabling: %+v", got)
	}

	if _, err := c.UpdateTable(ctx, &awsddb.UpdateTableInput{
		TableName:        aws.String("sse-toggle"),
		SSESpecification: &ddbtypes.SSESpecification{Enabled: aws.Bool(false)},
	}); err != nil {
		t.Fatalf("UpdateTable disabling SSE: %v", err)
	}
	if got := describeSSE(t, c, "sse-toggle"); got != nil {
		t.Errorf("SSE still reported after disabling: %+v", got)
	}
}
