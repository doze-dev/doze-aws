package dynamodb_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/dynamodb"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func billingClient(t *testing.T) *awsddb.Client {
	t.Helper()
	s, err := dynamodb.New(dynamodb.Options{DataDir: t.TempDir(), Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return awsddb.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func keyed(name string) *awsddb.CreateTableInput {
	return &awsddb.CreateTableInput{
		TableName:            aws.String(name),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
	}
}

// A table says how it is billed. Leaving both BillingMode and the capacity out
// is refused rather than quietly meaning on-demand, as is naming capacity for an
// on-demand table or leaving it out for a provisioned one.
func TestCreateTableRefusesWhatDoesNotSayHowItIsBilled(t *testing.T) {
	c := billingClient(t)
	ctx := context.Background()
	for name, mutate := range map[string]func(*awsddb.CreateTableInput){
		"neither":              func(*awsddb.CreateTableInput) {},
		"provisioned-no-units": func(in *awsddb.CreateTableInput) { in.BillingMode = ddbtypes.BillingModeProvisioned },
		"on-demand-with-units": func(in *awsddb.CreateTableInput) {
			in.BillingMode = ddbtypes.BillingModePayPerRequest
			in.ProvisionedThroughput = &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(1), WriteCapacityUnits: aws.Int64(1)}
		},
		"gsi-without-units": func(in *awsddb.CreateTableInput) {
			in.ProvisionedThroughput = &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(1), WriteCapacityUnits: aws.Int64(1)}
			in.GlobalSecondaryIndexes = []ddbtypes.GlobalSecondaryIndex{{
				IndexName:  aws.String("by-id"),
				KeySchema:  []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
				Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
			}}
		},
	} {
		in := keyed("refused-" + name)
		mutate(in)
		_, err := c.CreateTable(ctx, in)
		if err == nil || !strings.Contains(err.Error(), "ValidationException") {
			t.Errorf("%s: err = %v, want ValidationException", name, err)
		}
	}
}

// What a table was created with is what it describes, and what Terraform's
// provider needs to see to stop waiting: capacity, and a warm throughput.
func TestDescribeTableReportsCapacityAndWarmThroughput(t *testing.T) {
	c := billingClient(t)
	ctx := context.Background()

	in := keyed("provisioned")
	in.ProvisionedThroughput = &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(7)}
	if _, err := c.CreateTable(ctx, in); err != nil {
		t.Fatal(err)
	}
	d, err := c.DescribeTable(ctx, &awsddb.DescribeTableInput{TableName: aws.String("provisioned")})
	if err != nil {
		t.Fatal(err)
	}
	pt := d.Table.ProvisionedThroughput
	if pt == nil || aws.ToInt64(pt.ReadCapacityUnits) != 5 || aws.ToInt64(pt.WriteCapacityUnits) != 7 {
		t.Errorf("ProvisionedThroughput = %+v, want 5/7", pt)
	}
	if d.Table.BillingModeSummary != nil && d.Table.BillingModeSummary.BillingMode != ddbtypes.BillingModeProvisioned {
		t.Errorf("BillingModeSummary = %+v", d.Table.BillingModeSummary)
	}
	if d.Table.WarmThroughput == nil || d.Table.WarmThroughput.Status != ddbtypes.TableStatusActive {
		t.Errorf("WarmThroughput = %+v, want ACTIVE", d.Table.WarmThroughput)
	}

	// Switching to on-demand zeroes the capacity; switching back needs it again.
	if _, err := c.UpdateTable(ctx, &awsddb.UpdateTableInput{TableName: aws.String("provisioned"), BillingMode: ddbtypes.BillingModePayPerRequest}); err != nil {
		t.Fatal(err)
	}
	d, _ = c.DescribeTable(ctx, &awsddb.DescribeTableInput{TableName: aws.String("provisioned")})
	if got := aws.ToInt64(d.Table.ProvisionedThroughput.ReadCapacityUnits); got != 0 {
		t.Errorf("on-demand ReadCapacityUnits = %d, want 0", got)
	}
	if _, err := c.UpdateTable(ctx, &awsddb.UpdateTableInput{TableName: aws.String("provisioned"), BillingMode: ddbtypes.BillingModeProvisioned}); err == nil {
		t.Error("switched to PROVISIONED without saying how much")
	}
}
