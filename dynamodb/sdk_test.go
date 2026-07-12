// SDK contract tests: a real aws-sdk-go-v2 DynamoDB client driving the whole
// pipeline — expressions parsed from real SDK requests, GSI queries, paging,
// conditional writes, transactions.
package dynamodb_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/dynamodb"
)

func ddbClient(t *testing.T) *awsddb.Client {
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
	return awsddb.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

// makeOrdersTable creates the canonical test table: pk/sk plus a GSI on email.
func makeOrdersTable(t *testing.T, ctx context.Context, c *awsddb.Client) {
	t.Helper()
	_, err := c.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName: aws.String("orders"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("sk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("email"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("sk"), KeyType: ddbtypes.KeyTypeRange},
		},
		GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndex{{
			IndexName: aws.String("by-email"),
			KeySchema: []ddbtypes.KeySchemaElement{
				{AttributeName: aws.String("email"), KeyType: ddbtypes.KeyTypeHash},
			},
			Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
		}},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
}

func putOrder(t *testing.T, ctx context.Context, c *awsddb.Client, user, order, email string, total int) {
	t.Helper()
	_, err := c.PutItem(ctx, &awsddb.PutItemInput{
		TableName: aws.String("orders"),
		Item: map[string]ddbtypes.AttributeValue{
			"pk":    &ddbtypes.AttributeValueMemberS{Value: "user#" + user},
			"sk":    &ddbtypes.AttributeValueMemberS{Value: "order#" + order},
			"email": &ddbtypes.AttributeValueMemberS{Value: email},
			"total": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", total)},
		},
	})
	if err != nil {
		t.Fatalf("PutItem: %v", err)
	}
}

func TestSDKCRUDAndExpressions(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)
	putOrder(t, ctx, c, "1", "001", "a@x.com", 100)

	key := map[string]ddbtypes.AttributeValue{
		"pk": &ddbtypes.AttributeValueMemberS{Value: "user#1"},
		"sk": &ddbtypes.AttributeValueMemberS{Value: "order#001"},
	}

	// GetItem round trip with numeric fidelity.
	got, err := c.GetItem(ctx, &awsddb.GetItemInput{TableName: aws.String("orders"), Key: key})
	if err != nil || got.Item == nil {
		t.Fatalf("GetItem: %v", err)
	}
	if n := got.Item["total"].(*ddbtypes.AttributeValueMemberN).Value; n != "100" {
		t.Fatalf("total = %s", n)
	}

	// UpdateItem: arithmetic, list append via if_not_exists, ALL_NEW.
	upd, err := c.UpdateItem(ctx, &awsddb.UpdateItemInput{
		TableName:                aws.String("orders"),
		Key:                      key,
		UpdateExpression:         aws.String("SET #t = #t + :inc, tags = list_append(if_not_exists(tags, :empty), :tag)"),
		ExpressionAttributeNames: map[string]string{"#t": "total"},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":inc":   &ddbtypes.AttributeValueMemberN{Value: "25"},
			":empty": &ddbtypes.AttributeValueMemberL{Value: []ddbtypes.AttributeValue{}},
			":tag":   &ddbtypes.AttributeValueMemberL{Value: []ddbtypes.AttributeValue{&ddbtypes.AttributeValueMemberS{Value: "rush"}}},
		},
		ReturnValues: ddbtypes.ReturnValueAllNew,
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if n := upd.Attributes["total"].(*ddbtypes.AttributeValueMemberN).Value; n != "125" {
		t.Fatalf("total after update = %s", n)
	}
	if l := upd.Attributes["tags"].(*ddbtypes.AttributeValueMemberL).Value; len(l) != 1 {
		t.Fatalf("tags = %v", l)
	}

	// Conditional write failure carries the AWS code.
	_, err = c.PutItem(ctx, &awsddb.PutItemInput{
		TableName:           aws.String("orders"),
		Item:                map[string]ddbtypes.AttributeValue{"pk": key["pk"], "sk": key["sk"]},
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "ConditionalCheckFailedException" {
		t.Fatalf("conditional put error = %v", err)
	}

	// Unused expression values are rejected like real DynamoDB.
	_, err = c.UpdateItem(ctx, &awsddb.UpdateItemInput{
		TableName:        aws.String("orders"),
		Key:              key,
		UpdateExpression: aws.String("SET note = :v"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":v":      &ddbtypes.AttributeValueMemberS{Value: "x"},
			":unused": &ddbtypes.AttributeValueMemberS{Value: "y"},
		},
	})
	if !errors.As(err, &ae) || ae.ErrorCode() != "ValidationException" {
		t.Fatalf("unused value error = %v", err)
	}

	// DeleteItem with ALL_OLD.
	del, err := c.DeleteItem(ctx, &awsddb.DeleteItemInput{
		TableName: aws.String("orders"), Key: key, ReturnValues: ddbtypes.ReturnValueAllOld,
	})
	if err != nil || del.Attributes["email"].(*ddbtypes.AttributeValueMemberS).Value != "a@x.com" {
		t.Fatalf("DeleteItem: %v %v", err, del.Attributes)
	}
}

func TestSDKQueryScanAndGSI(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)
	for i := 1; i <= 5; i++ {
		putOrder(t, ctx, c, "7", fmt.Sprintf("%03d", i), "seven@x.com", i*10)
	}
	putOrder(t, ctx, c, "8", "001", "eight@x.com", 999)

	// Query with sort-key range + filter.
	q, err := c.Query(ctx, &awsddb.QueryInput{
		TableName:                aws.String("orders"),
		KeyConditionExpression:   aws.String("pk = :p AND sk BETWEEN :lo AND :hi"),
		FilterExpression:         aws.String("#t >= :min"),
		ExpressionAttributeNames: map[string]string{"#t": "total"},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":p":   &ddbtypes.AttributeValueMemberS{Value: "user#7"},
			":lo":  &ddbtypes.AttributeValueMemberS{Value: "order#001"},
			":hi":  &ddbtypes.AttributeValueMemberS{Value: "order#004"},
			":min": &ddbtypes.AttributeValueMemberN{Value: "20"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Range covers orders 1-4; filter (total>=20) keeps 2,3,4.
	if q.Count != 3 || q.ScannedCount != 4 {
		t.Fatalf("Query count=%d scanned=%d", q.Count, q.ScannedCount)
	}

	// Descending order.
	desc, err := c.Query(ctx, &awsddb.QueryInput{
		TableName:              aws.String("orders"),
		KeyConditionExpression: aws.String("pk = :p"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":p": &ddbtypes.AttributeValueMemberS{Value: "user#7"},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(1),
	})
	if err != nil || len(desc.Items) != 1 {
		t.Fatalf("desc query: %v", err)
	}
	if sk := desc.Items[0]["sk"].(*ddbtypes.AttributeValueMemberS).Value; sk != "order#005" {
		t.Fatalf("descending first = %s", sk)
	}

	// Query paging: 2 per page over 5 items.
	var seen int
	var lastKey map[string]ddbtypes.AttributeValue
	for range 5 {
		page, err := c.Query(ctx, &awsddb.QueryInput{
			TableName:              aws.String("orders"),
			KeyConditionExpression: aws.String("pk = :p"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":p": &ddbtypes.AttributeValueMemberS{Value: "user#7"},
			},
			Limit:             aws.Int32(2),
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			t.Fatal(err)
		}
		seen += len(page.Items)
		if page.LastEvaluatedKey == nil {
			break
		}
		lastKey = page.LastEvaluatedKey
	}
	if seen != 5 {
		t.Fatalf("paged %d items, want 5", seen)
	}

	// GSI query finds the item by email.
	gq, err := c.Query(ctx, &awsddb.QueryInput{
		TableName:              aws.String("orders"),
		IndexName:              aws.String("by-email"),
		KeyConditionExpression: aws.String("email = :e"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e": &ddbtypes.AttributeValueMemberS{Value: "eight@x.com"},
		},
	})
	if err != nil || gq.Count != 1 {
		t.Fatalf("GSI query: %v count=%d", err, gq.Count)
	}
	if pk := gq.Items[0]["pk"].(*ddbtypes.AttributeValueMemberS).Value; pk != "user#8" {
		t.Fatalf("GSI item pk = %s", pk)
	}

	// GSI stays consistent after an email change.
	if _, err := c.UpdateItem(ctx, &awsddb.UpdateItemInput{
		TableName: aws.String("orders"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "user#8"},
			"sk": &ddbtypes.AttributeValueMemberS{Value: "order#001"},
		},
		UpdateExpression: aws.String("SET email = :e"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e": &ddbtypes.AttributeValueMemberS{Value: "changed@x.com"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	gq2, _ := c.Query(ctx, &awsddb.QueryInput{
		TableName: aws.String("orders"), IndexName: aws.String("by-email"),
		KeyConditionExpression: aws.String("email = :e"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e": &ddbtypes.AttributeValueMemberS{Value: "eight@x.com"},
		},
	})
	if gq2.Count != 0 {
		t.Fatal("old GSI entry survived the email change")
	}

	// Scan with filter.
	sc, err := c.Scan(ctx, &awsddb.ScanInput{
		TableName:                aws.String("orders"),
		FilterExpression:         aws.String("#t > :n"),
		ExpressionAttributeNames: map[string]string{"#t": "total"},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":n": &ddbtypes.AttributeValueMemberN{Value: "100"},
		},
	})
	if err != nil || sc.Count != 1 {
		t.Fatalf("Scan: %v count=%d", err, sc.Count)
	}
}

func TestSDKBatchAndTransactions(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)

	// BatchWriteItem: 3 puts.
	var puts []ddbtypes.WriteRequest
	for i := 1; i <= 3; i++ {
		puts = append(puts, ddbtypes.WriteRequest{PutRequest: &ddbtypes.PutRequest{
			Item: map[string]ddbtypes.AttributeValue{
				"pk":    &ddbtypes.AttributeValueMemberS{Value: "batch"},
				"sk":    &ddbtypes.AttributeValueMemberS{Value: fmt.Sprintf("item#%d", i)},
				"email": &ddbtypes.AttributeValueMemberS{Value: "b@x.com"},
			},
		}})
	}
	if _, err := c.BatchWriteItem(ctx, &awsddb.BatchWriteItemInput{
		RequestItems: map[string][]ddbtypes.WriteRequest{"orders": puts},
	}); err != nil {
		t.Fatalf("BatchWriteItem: %v", err)
	}

	// BatchGetItem returns found items, flags nothing unprocessed.
	bg, err := c.BatchGetItem(ctx, &awsddb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{
			"orders": {Keys: []map[string]ddbtypes.AttributeValue{
				{"pk": &ddbtypes.AttributeValueMemberS{Value: "batch"}, "sk": &ddbtypes.AttributeValueMemberS{Value: "item#1"}},
				{"pk": &ddbtypes.AttributeValueMemberS{Value: "batch"}, "sk": &ddbtypes.AttributeValueMemberS{Value: "item#9"}},
			}},
		},
	})
	if err != nil || len(bg.Responses["orders"]) != 1 {
		t.Fatalf("BatchGetItem: %v, %d found", err, len(bg.Responses["orders"]))
	}

	// TransactWriteItems: a put + an update, atomically.
	_, err = c.TransactWriteItems(ctx, &awsddb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{
				TableName: aws.String("orders"),
				Item: map[string]ddbtypes.AttributeValue{
					"pk":    &ddbtypes.AttributeValueMemberS{Value: "tx"},
					"sk":    &ddbtypes.AttributeValueMemberS{Value: "a"},
					"email": &ddbtypes.AttributeValueMemberS{Value: "tx@x.com"},
					"n":     &ddbtypes.AttributeValueMemberN{Value: "1"},
				},
			}},
			{Update: &ddbtypes.Update{
				TableName: aws.String("orders"),
				Key: map[string]ddbtypes.AttributeValue{
					"pk": &ddbtypes.AttributeValueMemberS{Value: "batch"},
					"sk": &ddbtypes.AttributeValueMemberS{Value: "item#1"},
				},
				UpdateExpression: aws.String("SET marked = :t"),
				ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
					":t": &ddbtypes.AttributeValueMemberBOOL{Value: true},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("TransactWriteItems: %v", err)
	}

	// A failing condition cancels the WHOLE transaction with reasons.
	_, err = c.TransactWriteItems(ctx, &awsddb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{
				TableName: aws.String("orders"),
				Item: map[string]ddbtypes.AttributeValue{
					"pk":    &ddbtypes.AttributeValueMemberS{Value: "tx"},
					"sk":    &ddbtypes.AttributeValueMemberS{Value: "b"},
					"email": &ddbtypes.AttributeValueMemberS{Value: "x@x.com"},
				},
			}},
			{ConditionCheck: &ddbtypes.ConditionCheck{
				TableName: aws.String("orders"),
				Key: map[string]ddbtypes.AttributeValue{
					"pk": &ddbtypes.AttributeValueMemberS{Value: "tx"},
					"sk": &ddbtypes.AttributeValueMemberS{Value: "does-not-exist"},
				},
				ConditionExpression: aws.String("attribute_exists(pk)"),
			}},
		},
	})
	var tce *ddbtypes.TransactionCanceledException
	if !errors.As(err, &tce) {
		t.Fatalf("want TransactionCanceledException, got %v", err)
	}
	if len(tce.CancellationReasons) != 2 || aws.ToString(tce.CancellationReasons[1].Code) != "ConditionalCheckFailed" {
		t.Fatalf("reasons = %+v", tce.CancellationReasons)
	}
	// The put in the canceled transaction must NOT have been applied.
	got, _ := c.GetItem(ctx, &awsddb.GetItemInput{
		TableName: aws.String("orders"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "tx"},
			"sk": &ddbtypes.AttributeValueMemberS{Value: "b"},
		},
	})
	if got.Item != nil {
		t.Fatal("canceled transaction leaked a write")
	}

	// TransactGetItems reads consistently.
	tg, err := c.TransactGetItems(ctx, &awsddb.TransactGetItemsInput{
		TransactItems: []ddbtypes.TransactGetItem{
			{Get: &ddbtypes.Get{TableName: aws.String("orders"), Key: map[string]ddbtypes.AttributeValue{
				"pk": &ddbtypes.AttributeValueMemberS{Value: "tx"},
				"sk": &ddbtypes.AttributeValueMemberS{Value: "a"},
			}}},
		},
	})
	if err != nil || len(tg.Responses) != 1 || tg.Responses[0].Item == nil {
		t.Fatalf("TransactGetItems: %v", err)
	}
}

// TestTransactWriteRejectsDuplicateItem: two operations on the same item must
// abort the whole request with a ValidationException, not silently apply.
func TestTransactWriteRejectsDuplicateItem(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)

	_, err := c.TransactWriteItems(ctx, &awsddb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{TableName: aws.String("orders"), Item: map[string]ddbtypes.AttributeValue{
				"pk": &ddbtypes.AttributeValueMemberS{Value: "dup"}, "sk": &ddbtypes.AttributeValueMemberS{Value: "a"},
				"email": &ddbtypes.AttributeValueMemberS{Value: "x@x.com"},
			}}},
			{Update: &ddbtypes.Update{TableName: aws.String("orders"), Key: map[string]ddbtypes.AttributeValue{
				"pk": &ddbtypes.AttributeValueMemberS{Value: "dup"}, "sk": &ddbtypes.AttributeValueMemberS{Value: "a"},
			}, UpdateExpression: aws.String("SET marked = :t"),
				ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":t": &ddbtypes.AttributeValueMemberBOOL{Value: true}}}},
		},
	})
	if err == nil {
		t.Fatal("expected ValidationException for two operations on one item")
	}
	// The item must not have been written.
	got, _ := c.GetItem(ctx, &awsddb.GetItemInput{TableName: aws.String("orders"), Key: map[string]ddbtypes.AttributeValue{
		"pk": &ddbtypes.AttributeValueMemberS{Value: "dup"}, "sk": &ddbtypes.AttributeValueMemberS{Value: "a"},
	}})
	if got.Item != nil {
		t.Fatal("rejected transaction leaked a write")
	}
}

// TestTransactWriteRejectsKeyMutation: a transaction Update may not modify a key
// attribute (parity with non-transactional UpdateItem).
func TestTransactWriteRejectsKeyMutation(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)
	putOrder(t, ctx, c, "1", "001", "a@x.com", 100)

	_, err := c.TransactWriteItems(ctx, &awsddb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Update: &ddbtypes.Update{TableName: aws.String("orders"), Key: map[string]ddbtypes.AttributeValue{
				"pk": &ddbtypes.AttributeValueMemberS{Value: "user#1"}, "sk": &ddbtypes.AttributeValueMemberS{Value: "order#001"},
			}, UpdateExpression: aws.String("SET pk = :x"),
				ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":x": &ddbtypes.AttributeValueMemberS{Value: "hacked"}}}},
		},
	})
	if err == nil {
		t.Fatal("expected cancellation for modifying a key attribute in a transaction")
	}
}

// TestGSIRecreateNoStaleEntries: dropping a GSI and recreating it under the same
// name on a different attribute must not resurrect entries from the old index.
func TestGSIRecreateNoStaleEntries(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	if _, err := c.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName: aws.String("gt"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("a"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema:   []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		BillingMode: ddbtypes.BillingModePayPerRequest,
		GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndex{{
			IndexName:  aws.String("g"),
			KeySchema:  []ddbtypes.KeySchemaElement{{AttributeName: aws.String("a"), KeyType: ddbtypes.KeyTypeHash}},
			Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
		}},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if _, err := c.PutItem(ctx, &awsddb.PutItemInput{TableName: aws.String("gt"), Item: map[string]ddbtypes.AttributeValue{
		"pk": &ddbtypes.AttributeValueMemberS{Value: "p1"}, "a": &ddbtypes.AttributeValueMemberS{Value: "old"},
	}}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	// Drop the GSI.
	if _, err := c.UpdateTable(ctx, &awsddb.UpdateTableInput{
		TableName: aws.String("gt"),
		GlobalSecondaryIndexUpdates: []ddbtypes.GlobalSecondaryIndexUpdate{
			{Delete: &ddbtypes.DeleteGlobalSecondaryIndexAction{IndexName: aws.String("g")}},
		},
	}); err != nil {
		t.Fatalf("delete GSI: %v", err)
	}
	// Recreate a same-named GSI on a different attribute.
	if _, err := c.UpdateTable(ctx, &awsddb.UpdateTableInput{
		TableName: aws.String("gt"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("b"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		GlobalSecondaryIndexUpdates: []ddbtypes.GlobalSecondaryIndexUpdate{
			{Create: &ddbtypes.CreateGlobalSecondaryIndexAction{
				IndexName:  aws.String("g"),
				KeySchema:  []ddbtypes.KeySchemaElement{{AttributeName: aws.String("b"), KeyType: ddbtypes.KeyTypeHash}},
				Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
			}},
		},
	}); err != nil {
		t.Fatalf("recreate GSI: %v", err)
	}
	// Querying the new GSI by the OLD attribute value must return nothing —
	// the stale entry must not have survived.
	out, err := c.Query(ctx, &awsddb.QueryInput{
		TableName: aws.String("gt"), IndexName: aws.String("g"),
		KeyConditionExpression:    aws.String("b = :v"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":v": &ddbtypes.AttributeValueMemberS{Value: "old"}},
	})
	if err != nil {
		t.Fatalf("Query new GSI: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("stale GSI entry resurfaced: %d items", len(out.Items))
	}
}

func TestSDKTableLifecycleAndStubs(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)

	// DescribeTable is ACTIVE immediately (waiters pass).
	desc, err := c.DescribeTable(ctx, &awsddb.DescribeTableInput{TableName: aws.String("orders")})
	if err != nil || desc.Table.TableStatus != ddbtypes.TableStatusActive {
		t.Fatalf("DescribeTable: %v status=%v", err, desc.Table.TableStatus)
	}
	if len(desc.Table.GlobalSecondaryIndexes) != 1 {
		t.Fatalf("GSIs = %d", len(desc.Table.GlobalSecondaryIndexes))
	}

	// TTL round trip.
	if _, err := c.UpdateTimeToLive(ctx, &awsddb.UpdateTimeToLiveInput{
		TableName: aws.String("orders"),
		TimeToLiveSpecification: &ddbtypes.TimeToLiveSpecification{
			AttributeName: aws.String("expires_at"), Enabled: aws.Bool(true),
		},
	}); err != nil {
		t.Fatalf("UpdateTimeToLive: %v", err)
	}
	ttl, err := c.DescribeTimeToLive(ctx, &awsddb.DescribeTimeToLiveInput{TableName: aws.String("orders")})
	if err != nil || ttl.TimeToLiveDescription.TimeToLiveStatus != ddbtypes.TimeToLiveStatusEnabled {
		t.Fatalf("DescribeTimeToLive: %v", err)
	}

	// PartiQL is functional (Phase 8) — covered by TestSDKPartiQL. Global tables
	// remain an honest stub (one region locally).
	_, err = c.DescribeGlobalTable(ctx, &awsddb.DescribeGlobalTableInput{GlobalTableName: aws.String("orders")})
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "UnsupportedOperationException" {
		t.Fatalf("DescribeGlobalTable error = %v", err)
	}

	// ListTables + DeleteTable.
	lt, _ := c.ListTables(ctx, &awsddb.ListTablesInput{})
	if len(lt.TableNames) != 1 {
		t.Fatalf("tables = %v", lt.TableNames)
	}
	if _, err := c.DeleteTable(ctx, &awsddb.DeleteTableInput{TableName: aws.String("orders")}); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
	_, err = c.DescribeTable(ctx, &awsddb.DescribeTableInput{TableName: aws.String("orders")})
	if !errors.As(err, &ae) || ae.ErrorCode() != "ResourceNotFoundException" {
		t.Fatalf("after delete: %v", err)
	}
}

func TestSDKPartiQL(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)

	// INSERT with positional parameters.
	_, err := c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
		Statement: aws.String(`INSERT INTO "orders" VALUE {'pk': ?, 'sk': ?, 'email': ?, 'total': ?}`),
		Parameters: []ddbtypes.AttributeValue{
			&ddbtypes.AttributeValueMemberS{Value: "user#1"},
			&ddbtypes.AttributeValueMemberS{Value: "order#1"},
			&ddbtypes.AttributeValueMemberS{Value: "a@b.com"},
			&ddbtypes.AttributeValueMemberN{Value: "42"},
		},
	})
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	// SELECT by full primary key -> GetItem path.
	sel, err := c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
		Statement: aws.String(`SELECT * FROM "orders" WHERE "pk" = ? AND "sk" = ?`),
		Parameters: []ddbtypes.AttributeValue{
			&ddbtypes.AttributeValueMemberS{Value: "user#1"},
			&ddbtypes.AttributeValueMemberS{Value: "order#1"},
		},
	})
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(sel.Items) != 1 || sel.Items[0]["email"].(*ddbtypes.AttributeValueMemberS).Value != "a@b.com" {
		t.Fatalf("SELECT returned %+v", sel.Items)
	}

	// UPDATE SET with a literal.
	_, err = c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
		Statement: aws.String(`UPDATE "orders" SET "total" = '99' WHERE "pk" = ? AND "sk" = ?`),
		Parameters: []ddbtypes.AttributeValue{
			&ddbtypes.AttributeValueMemberS{Value: "user#1"},
			&ddbtypes.AttributeValueMemberS{Value: "order#1"},
		},
	})
	if err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	sel2, _ := c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
		Statement:  aws.String(`SELECT * FROM "orders" WHERE "pk" = ? AND "sk" = ?`),
		Parameters: []ddbtypes.AttributeValue{&ddbtypes.AttributeValueMemberS{Value: "user#1"}, &ddbtypes.AttributeValueMemberS{Value: "order#1"}},
	})
	if got := sel2.Items[0]["total"].(*ddbtypes.AttributeValueMemberS).Value; got != "99" {
		t.Fatalf("after UPDATE total = %q, want 99", got)
	}

	// DELETE, then confirm the row is gone.
	_, err = c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
		Statement:  aws.String(`DELETE FROM "orders" WHERE "pk" = ? AND "sk" = ?`),
		Parameters: []ddbtypes.AttributeValue{&ddbtypes.AttributeValueMemberS{Value: "user#1"}, &ddbtypes.AttributeValueMemberS{Value: "order#1"}},
	})
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	sel3, _ := c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
		Statement:  aws.String(`SELECT * FROM "orders" WHERE "pk" = ? AND "sk" = ?`),
		Parameters: []ddbtypes.AttributeValue{&ddbtypes.AttributeValueMemberS{Value: "user#1"}, &ddbtypes.AttributeValueMemberS{Value: "order#1"}},
	})
	if len(sel3.Items) != 0 {
		t.Fatalf("row still present after DELETE: %+v", sel3.Items)
	}
}

// TestPartiQLSemantics covers the fidelity fixes: INSERT rejects duplicates,
// UPDATE doesn't upsert, positional '?' binds SET-then-WHERE in statement order,
// and SELECT honors its projection list.
func TestPartiQLSemantics(t *testing.T) {
	ctx := context.Background()
	c := ddbClient(t)
	makeOrdersTable(t, ctx, c)
	s := func(v string) ddbtypes.AttributeValue { return &ddbtypes.AttributeValueMemberS{Value: v} }

	ins := func() error {
		_, err := c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
			Statement: aws.String(`INSERT INTO "orders" VALUE {'pk': 'p', 'sk': 's', 'email': 'e@x.com', 'total': '1'}`),
		})
		return err
	}
	if err := ins(); err != nil {
		t.Fatalf("first INSERT: %v", err)
	}
	// INSERT of an existing key must fail (DuplicateItemException), not overwrite.
	if err := ins(); err == nil {
		t.Fatal("duplicate INSERT should fail")
	}

	// UPDATE of a nonexistent item must not create it.
	if _, err := c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
		Statement:  aws.String(`UPDATE "orders" SET "total" = '9' WHERE "pk" = ? AND "sk" = ?`),
		Parameters: []ddbtypes.AttributeValue{s("ghost"), s("none")},
	}); err == nil {
		t.Fatal("UPDATE of a missing item should fail, not upsert")
	}
	if got, _ := c.GetItem(ctx, &awsddb.GetItemInput{TableName: aws.String("orders"), Key: map[string]ddbtypes.AttributeValue{
		"pk": s("ghost"), "sk": s("none"),
	}}); got.Item != nil {
		t.Fatal("UPDATE upserted a missing item")
	}

	// Positional '?' must bind SET first, then WHERE (statement order).
	if _, err := c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
		Statement:  aws.String(`UPDATE "orders" SET "email" = ? WHERE "pk" = ? AND "sk" = ?`),
		Parameters: []ddbtypes.AttributeValue{s("new@x.com"), s("p"), s("s")},
	}); err != nil {
		t.Fatalf("parameterized UPDATE: %v", err)
	}
	got, _ := c.GetItem(ctx, &awsddb.GetItemInput{TableName: aws.String("orders"), Key: map[string]ddbtypes.AttributeValue{
		"pk": s("p"), "sk": s("s"),
	}})
	if got.Item["email"].(*ddbtypes.AttributeValueMemberS).Value != "new@x.com" {
		t.Fatalf("SET/WHERE param binding wrong: email = %v", got.Item["email"])
	}

	// SELECT projection: only the requested attribute comes back.
	sel, err := c.ExecuteStatement(ctx, &awsddb.ExecuteStatementInput{
		Statement:  aws.String(`SELECT email FROM "orders" WHERE "pk" = ? AND "sk" = ?`),
		Parameters: []ddbtypes.AttributeValue{s("p"), s("s")},
	})
	if err != nil || len(sel.Items) != 1 {
		t.Fatalf("projected SELECT: %v %+v", err, sel.Items)
	}
	if _, hasEmail := sel.Items[0]["email"]; !hasEmail {
		t.Fatal("projection dropped the requested attribute")
	}
	if _, hasTotal := sel.Items[0]["total"]; hasTotal {
		t.Fatalf("projection returned unrequested attributes: %+v", sel.Items[0])
	}
}
