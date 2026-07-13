package dynamodb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/dynamodb"
)

// streamsPost issues a raw DynamoDBStreams JSON request (the streams SDK isn't a
// dependency; the wire is what an app's streams client would send).
func streamsPost(t *testing.T, url, action string, in any) map[string]any {
	t.Helper()
	buf, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", url+"/", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDBStreams_20120810."+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s -> %d: %s", action, resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%s decode: %v (%s)", action, err, body)
	}
	return out
}

func TestDynamoDBStreamsAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("streams contract test")
	}
	ctx := context.Background()
	s, err := dynamodb.New(dynamodb.Options{DataDir: t.TempDir(), Logf: t.Logf})
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

	// Stream-enabled table.
	ct, err := c.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName:            aws.String("t"),
		BillingMode:          ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		StreamSpecification:  &ddbtypes.StreamSpecification{StreamEnabled: aws.Bool(true), StreamViewType: ddbtypes.StreamViewTypeNewAndOldImages},
	})
	if err != nil {
		t.Fatal(err)
	}
	streamArn := aws.ToString(ct.TableDescription.LatestStreamArn)
	if streamArn == "" {
		t.Fatal("no LatestStreamArn")
	}

	// Generate INSERT, MODIFY, REMOVE.
	put := func(v string) {
		c.PutItem(ctx, &awsddb.PutItemInput{TableName: aws.String("t"), Item: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "k"}, "v": &ddbtypes.AttributeValueMemberS{Value: v},
		}})
	}
	put("one")                                                                                                                                                         // INSERT
	put("two")                                                                                                                                                         // MODIFY
	c.DeleteItem(ctx, &awsddb.DeleteItemInput{TableName: aws.String("t"), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: "k"}}}) // REMOVE

	// ListStreams + DescribeStream.
	ls := streamsPost(t, ts.URL, "ListStreams", map[string]any{"TableName": "t"})
	if len(ls["Streams"].([]any)) != 1 {
		t.Fatalf("ListStreams = %v", ls["Streams"])
	}
	ds := streamsPost(t, ts.URL, "DescribeStream", map[string]any{"StreamArn": streamArn})
	sd := ds["StreamDescription"].(map[string]any)
	if sd["StreamViewType"] != "NEW_AND_OLD_IMAGES" || sd["StreamStatus"] != "ENABLED" {
		t.Fatalf("DescribeStream shape: %v", sd)
	}

	// GetShardIterator(TRIM_HORIZON) → GetRecords → all three events in order.
	shardID := sd["Shards"].([]any)[0].(map[string]any)["ShardId"].(string)
	gi := streamsPost(t, ts.URL, "GetShardIterator", map[string]any{
		"StreamArn": streamArn, "ShardId": shardID, "ShardIteratorType": "TRIM_HORIZON",
	})
	gr := streamsPost(t, ts.URL, "GetRecords", map[string]any{"ShardIterator": gi["ShardIterator"]})
	recs := gr["Records"].([]any)
	if len(recs) != 3 {
		t.Fatalf("GetRecords returned %d records, want 3", len(recs))
	}
	names := []string{}
	for _, r := range recs {
		names = append(names, r.(map[string]any)["eventName"].(string))
	}
	if names[0] != "INSERT" || names[1] != "MODIFY" || names[2] != "REMOVE" {
		t.Fatalf("event order = %v", names)
	}
	// The MODIFY record carries both a NewImage and an OldImage.
	mod := recs[1].(map[string]any)["dynamodb"].(map[string]any)
	if _, ok := mod["NewImage"]; !ok {
		t.Fatal("MODIFY missing NewImage")
	}
	if _, ok := mod["OldImage"]; !ok {
		t.Fatal("MODIFY missing OldImage")
	}
	// NextShardIterator is always present (open shard); a re-poll yields nothing new.
	gr2 := streamsPost(t, ts.URL, "GetRecords", map[string]any{"ShardIterator": gr["NextShardIterator"]})
	if len(gr2["Records"].([]any)) != 0 {
		t.Fatalf("re-poll returned %d records, want 0", len(gr2["Records"].([]any)))
	}
}
