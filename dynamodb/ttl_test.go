package dynamodb_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/dynamodb"
)

// TestTTLExpiry drives TTL with an injected clock: expired items vanish from
// reads immediately (lazy filtering) and from storage after a sweep.
func TestTTLExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping feature test in -short mode")
	}
	ctx := context.Background()

	var offset int64
	clock := func() time.Time { return time.Now().Add(time.Duration(atomic.LoadInt64(&offset)) * time.Second) }
	s, err := dynamodb.New(dynamodb.Options{DataDir: t.TempDir(), Logf: t.Logf, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	c := awsddb.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	if _, err := c.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName: aws.String("sessions"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateTimeToLive(ctx, &awsddb.UpdateTimeToLiveInput{
		TableName: aws.String("sessions"),
		TimeToLiveSpecification: &ddbtypes.TimeToLiveSpecification{
			AttributeName: aws.String("expires"), Enabled: aws.Bool(true),
		},
	}); err != nil {
		t.Fatal(err)
	}

	// One session expiring in 60s, one without TTL.
	expiry := time.Now().Add(60 * time.Second).Unix()
	for id, item := range map[string]map[string]ddbtypes.AttributeValue{
		"short": {
			"id":      &ddbtypes.AttributeValueMemberS{Value: "short"},
			"expires": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", expiry)},
		},
		"forever": {
			"id": &ddbtypes.AttributeValueMemberS{Value: "forever"},
		},
	} {
		if _, err := c.PutItem(ctx, &awsddb.PutItemInput{TableName: aws.String("sessions"), Item: item}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}

	// Advance past the expiry: the item disappears from reads (lazy filter).
	atomic.StoreInt64(&offset, 120)
	got, err := c.GetItem(ctx, &awsddb.GetItemInput{
		TableName: aws.String("sessions"),
		Key:       map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "short"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Item != nil {
		t.Fatal("expired item still readable")
	}

	// A sweep physically removes it; the TTL-less item survives.
	s.SweepTTLNow()
	scan, err := c.Scan(ctx, &awsddb.ScanInput{TableName: aws.String("sessions")})
	if err != nil || scan.Count != 1 {
		t.Fatalf("after sweep: %v count=%d", err, scan.Count)
	}
	if id := scan.Items[0]["id"].(*ddbtypes.AttributeValueMemberS).Value; id != "forever" {
		t.Fatalf("survivor = %s", id)
	}
}
