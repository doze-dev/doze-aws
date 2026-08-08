// SDK contract tests: a real aws-sdk-go-v2 Kinesis client exercising the
// paths that actually matter — partition-key routing, the iterator types,
// retention bounds, and ordering across a reshard.
package kinesis_test

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"math/big"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	ktypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/aws/smithy-go"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/kinesis"
)

func client(t *testing.T) *awskinesis.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	s, err := kinesis.New(kinesis.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return awskinesis.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awskinesis.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func mustCreate(t *testing.T, c *awskinesis.Client, name string, shards int32) {
	t.Helper()
	if _, err := c.CreateStream(context.Background(), &awskinesis.CreateStreamInput{
		StreamName: aws.String(name), ShardCount: aws.Int32(shards),
	}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}
}

// drain reads every record currently available on a shard from TRIM_HORIZON.
func drain(t *testing.T, c *awskinesis.Client, stream, shard string) []ktypes.Record {
	t.Helper()
	ctx := context.Background()
	it, err := c.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName:        aws.String(stream),
		ShardId:           aws.String(shard),
		ShardIteratorType: ktypes.ShardIteratorTypeTrimHorizon,
	})
	if err != nil {
		t.Fatalf("GetShardIterator: %v", err)
	}
	var out []ktypes.Record
	iter := it.ShardIterator
	for range 5 {
		if iter == nil {
			break
		}
		res, err := c.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: iter})
		if err != nil {
			t.Fatalf("GetRecords: %v", err)
		}
		out = append(out, res.Records...)
		if len(res.Records) == 0 {
			break
		}
		iter = res.NextShardIterator
	}
	return out
}

func TestSDKStreamLifecycle(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	mustCreate(t, c, "events", 2)

	// CreateStream is not idempotent in AWS: a duplicate is ResourceInUse.
	_, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{
		StreamName: aws.String("events"), ShardCount: aws.Int32(2),
	})
	assertCode(t, err, "ResourceInUseException")

	d, err := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("events")})
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}
	if got := len(d.StreamDescription.Shards); got != 2 {
		t.Fatalf("shards = %d, want 2", got)
	}
	if d.StreamDescription.StreamStatus != ktypes.StreamStatusActive {
		t.Fatalf("status = %s", d.StreamDescription.StreamStatus)
	}
	if got := aws.ToInt32(d.StreamDescription.RetentionPeriodHours); got != 24 {
		t.Fatalf("retention = %d, want 24", got)
	}

	// The two shards must tile the whole hash space with no gap.
	first, second := d.StreamDescription.Shards[0], d.StreamDescription.Shards[1]
	if aws.ToString(first.HashKeyRange.StartingHashKey) != "0" {
		t.Fatalf("first shard starts at %s", aws.ToString(first.HashKeyRange.StartingHashKey))
	}
	lo, _ := new(big.Int).SetString(aws.ToString(second.HashKeyRange.StartingHashKey), 10)
	hi, _ := new(big.Int).SetString(aws.ToString(first.HashKeyRange.EndingHashKey), 10)
	if new(big.Int).Sub(lo, hi).Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("gap between shard ranges: %s .. %s", hi, lo)
	}

	summary, err := c.DescribeStreamSummary(ctx, &awskinesis.DescribeStreamSummaryInput{
		StreamName: aws.String("events"),
	})
	if err != nil {
		t.Fatalf("DescribeStreamSummary: %v", err)
	}
	if got := aws.ToInt32(summary.StreamDescriptionSummary.OpenShardCount); got != 2 {
		t.Fatalf("OpenShardCount = %d, want 2", got)
	}

	ls, err := c.ListStreams(ctx, &awskinesis.ListStreamsInput{})
	if err != nil {
		t.Fatalf("ListStreams: %v", err)
	}
	if len(ls.StreamNames) != 1 || ls.StreamNames[0] != "events" {
		t.Fatalf("ListStreams = %v", ls.StreamNames)
	}

	if _, err := c.DeleteStream(ctx, &awskinesis.DeleteStreamInput{StreamName: aws.String("events")}); err != nil {
		t.Fatalf("DeleteStream: %v", err)
	}
	_, err = c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("events")})
	assertCode(t, err, "ResourceNotFoundException")
}

// TestSDKPartitionKeyRouting is the test that matters most for cloud parity: a
// partition key must land on the shard whose hash range contains MD5(key).
func TestSDKPartitionKeyRouting(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	mustCreate(t, c, "orders", 4)

	d, err := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"user-1", "user-2", "tenant/acme", "", "0"} {
		if key == "" {
			continue
		}
		sum := md5.Sum([]byte(key))
		h := new(big.Int).SetBytes(sum[:])

		var want string
		for _, sh := range d.StreamDescription.Shards {
			lo, _ := new(big.Int).SetString(aws.ToString(sh.HashKeyRange.StartingHashKey), 10)
			hi, _ := new(big.Int).SetString(aws.ToString(sh.HashKeyRange.EndingHashKey), 10)
			if h.Cmp(lo) >= 0 && h.Cmp(hi) <= 0 {
				want = aws.ToString(sh.ShardId)
			}
		}
		if want == "" {
			t.Fatalf("no shard covers hash of %q", key)
		}

		put, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
			StreamName:   aws.String("orders"),
			PartitionKey: aws.String(key),
			Data:         []byte("payload"),
		})
		if err != nil {
			t.Fatalf("PutRecord(%q): %v", key, err)
		}
		if got := aws.ToString(put.ShardId); got != want {
			t.Fatalf("key %q landed on %s, want %s (MD5 routing is wrong)", key, got, want)
		}
	}
}

func TestSDKExplicitHashKeyOverridesPartitionKey(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	mustCreate(t, c, "hashed", 2)

	d, _ := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("hashed")})
	second := d.StreamDescription.Shards[1]

	put, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName:      aws.String("hashed"),
		PartitionKey:    aws.String("anything"),
		ExplicitHashKey: second.HashKeyRange.StartingHashKey,
		Data:            []byte("x"),
	})
	if err != nil {
		t.Fatalf("PutRecord: %v", err)
	}
	if got, want := aws.ToString(put.ShardId), aws.ToString(second.ShardId); got != want {
		t.Fatalf("ExplicitHashKey ignored: landed on %s, want %s", got, want)
	}

	_, err = c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName:      aws.String("hashed"),
		PartitionKey:    aws.String("k"),
		ExplicitHashKey: aws.String("not-a-number"),
		Data:            []byte("x"),
	})
	assertCode(t, err, "InvalidArgumentException")
}

func TestSDKPutRecordsAndOrdering(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	mustCreate(t, c, "batch", 1)

	entries := make([]ktypes.PutRecordsRequestEntry, 0, 10)
	for i := range 10 {
		entries = append(entries, ktypes.PutRecordsRequestEntry{
			PartitionKey: aws.String("same-key"),
			Data:         []byte(fmt.Sprintf("record-%d", i)),
		})
	}
	res, err := c.PutRecords(ctx, &awskinesis.PutRecordsInput{
		StreamName: aws.String("batch"), Records: entries,
	})
	if err != nil {
		t.Fatalf("PutRecords: %v", err)
	}
	if n := aws.ToInt32(res.FailedRecordCount); n != 0 {
		t.Fatalf("FailedRecordCount = %d", n)
	}

	d, _ := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("batch")})
	got := drain(t, c, "batch", aws.ToString(d.StreamDescription.Shards[0].ShardId))
	if len(got) != 10 {
		t.Fatalf("read %d records, want 10", len(got))
	}
	// One partition key on one shard must come back in write order.
	for i, rec := range got {
		if want := fmt.Sprintf("record-%d", i); string(rec.Data) != want {
			t.Fatalf("record %d = %q, want %q", i, rec.Data, want)
		}
	}
}

func TestSDKIteratorTypes(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	mustCreate(t, c, "iters", 1)
	d, _ := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("iters")})
	shard := aws.ToString(d.StreamDescription.Shards[0].ShardId)

	var seqs []string
	for i := range 3 {
		put, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
			StreamName: aws.String("iters"), PartitionKey: aws.String("k"),
			Data: []byte(fmt.Sprintf("m%d", i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, aws.ToString(put.SequenceNumber))
	}

	read := func(in *awskinesis.GetShardIteratorInput) []ktypes.Record {
		t.Helper()
		in.StreamName, in.ShardId = aws.String("iters"), aws.String(shard)
		it, err := c.GetShardIterator(ctx, in)
		if err != nil {
			t.Fatalf("GetShardIterator(%s): %v", in.ShardIteratorType, err)
		}
		res, err := c.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator})
		if err != nil {
			t.Fatalf("GetRecords: %v", err)
		}
		return res.Records
	}

	if got := read(&awskinesis.GetShardIteratorInput{ShardIteratorType: ktypes.ShardIteratorTypeTrimHorizon}); len(got) != 3 {
		t.Fatalf("TRIM_HORIZON returned %d, want 3", len(got))
	}
	// LATEST starts at the tip: nothing already written is delivered.
	if got := read(&awskinesis.GetShardIteratorInput{ShardIteratorType: ktypes.ShardIteratorTypeLatest}); len(got) != 0 {
		t.Fatalf("LATEST returned %d, want 0", len(got))
	}
	// AT_SEQUENCE_NUMBER is inclusive of the named record.
	got := read(&awskinesis.GetShardIteratorInput{
		ShardIteratorType:      ktypes.ShardIteratorTypeAtSequenceNumber,
		StartingSequenceNumber: aws.String(seqs[1]),
	})
	if len(got) != 2 || string(got[0].Data) != "m1" {
		t.Fatalf("AT_SEQUENCE_NUMBER returned %d records starting %q", len(got), got[0].Data)
	}
	// AFTER_SEQUENCE_NUMBER is exclusive.
	got = read(&awskinesis.GetShardIteratorInput{
		ShardIteratorType:      ktypes.ShardIteratorTypeAfterSequenceNumber,
		StartingSequenceNumber: aws.String(seqs[1]),
	})
	if len(got) != 1 || string(got[0].Data) != "m2" {
		t.Fatalf("AFTER_SEQUENCE_NUMBER returned %d records starting %q", len(got), got[0].Data)
	}
	// AT_TIMESTAMP well in the past delivers everything.
	got = read(&awskinesis.GetShardIteratorInput{
		ShardIteratorType: ktypes.ShardIteratorTypeAtTimestamp,
		Timestamp:         aws.Time(time.Now().Add(-time.Hour)),
	})
	if len(got) != 3 {
		t.Fatalf("AT_TIMESTAMP returned %d, want 3", len(got))
	}

	_, err := c.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName: aws.String("iters"), ShardId: aws.String("shardId-000000000099"),
		ShardIteratorType: ktypes.ShardIteratorTypeTrimHorizon,
	})
	assertCode(t, err, "ResourceNotFoundException")
}

// TestSDKSplitShardPreservesOrdering is the reshard contract: the parent closes,
// its records stay readable to the end, the null iterator signals the close, and
// ChildShards names where to continue.
func TestSDKSplitShardPreservesOrdering(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	mustCreate(t, c, "resharded", 1)

	d, _ := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("resharded")})
	parent := d.StreamDescription.Shards[0]
	parentID := aws.ToString(parent.ShardId)

	for i := range 3 {
		if _, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
			StreamName: aws.String("resharded"), PartitionKey: aws.String("k"),
			Data: []byte(fmt.Sprintf("before-%d", i)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Split at the midpoint of the parent's range.
	hi, _ := new(big.Int).SetString(aws.ToString(parent.HashKeyRange.EndingHashKey), 10)
	mid := new(big.Int).Div(hi, big.NewInt(2))
	if _, err := c.SplitShard(ctx, &awskinesis.SplitShardInput{
		StreamName:         aws.String("resharded"),
		ShardToSplit:       parent.ShardId,
		NewStartingHashKey: aws.String(mid.String()),
	}); err != nil {
		t.Fatalf("SplitShard: %v", err)
	}

	d, _ = c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("resharded")})
	if len(d.StreamDescription.Shards) != 3 {
		t.Fatalf("after split: %d shards, want 3 (closed parent + 2 children)", len(d.StreamDescription.Shards))
	}
	var children []ktypes.Shard
	for _, sh := range d.StreamDescription.Shards {
		if aws.ToString(sh.ShardId) == parentID {
			if sh.SequenceNumberRange.EndingSequenceNumber == nil {
				t.Fatal("parent shard has no EndingSequenceNumber after split")
			}
			continue
		}
		if aws.ToString(sh.ParentShardId) != parentID {
			t.Fatalf("child %s does not name the parent", aws.ToString(sh.ShardId))
		}
		children = append(children, sh)
	}
	if len(children) != 2 {
		t.Fatalf("got %d children, want 2", len(children))
	}

	// New writes go to a child, never the closed parent.
	put, err := c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName: aws.String("resharded"), PartitionKey: aws.String("k"), Data: []byte("after"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(put.ShardId) == parentID {
		t.Fatal("write landed on the closed parent shard")
	}

	// Draining the parent yields exactly the pre-split records, then a null
	// iterator plus the child list.
	it, _ := c.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName: aws.String("resharded"), ShardId: aws.String(parentID),
		ShardIteratorType: ktypes.ShardIteratorTypeTrimHorizon,
	})
	res, err := c.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator})
	if err != nil {
		t.Fatalf("GetRecords on parent: %v", err)
	}
	if len(res.Records) != 3 {
		t.Fatalf("parent held %d records, want 3", len(res.Records))
	}
	if res.NextShardIterator != nil {
		t.Fatal("a drained closed shard must return a null NextShardIterator")
	}
	if len(res.ChildShards) != 2 {
		t.Fatalf("ChildShards = %d, want 2", len(res.ChildShards))
	}
}

func TestSDKMergeShards(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	mustCreate(t, c, "merging", 2)
	d, _ := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("merging")})
	a, b := d.StreamDescription.Shards[0], d.StreamDescription.Shards[1]

	if _, err := c.MergeShards(ctx, &awskinesis.MergeShardsInput{
		StreamName: aws.String("merging"), ShardToMerge: a.ShardId, AdjacentShardToMerge: b.ShardId,
	}); err != nil {
		t.Fatalf("MergeShards: %v", err)
	}

	d, _ = c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("merging")})
	var child *ktypes.Shard
	for i, sh := range d.StreamDescription.Shards {
		if sh.SequenceNumberRange.EndingSequenceNumber == nil {
			child = &d.StreamDescription.Shards[i]
		}
	}
	if child == nil {
		t.Fatal("no open shard after merge")
	}
	if aws.ToString(child.ParentShardId) == "" || aws.ToString(child.AdjacentParentShardId) == "" {
		t.Fatal("merged child must name both parents")
	}
	// The child must span the union of both parents.
	if aws.ToString(child.HashKeyRange.StartingHashKey) != aws.ToString(a.HashKeyRange.StartingHashKey) ||
		aws.ToString(child.HashKeyRange.EndingHashKey) != aws.ToString(b.HashKeyRange.EndingHashKey) {
		t.Fatal("merged child does not span both parent ranges")
	}
}

func TestSDKRetentionBounds(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	mustCreate(t, c, "retained", 1)

	if _, err := c.IncreaseStreamRetentionPeriod(ctx, &awskinesis.IncreaseStreamRetentionPeriodInput{
		StreamName: aws.String("retained"), RetentionPeriodHours: aws.Int32(48),
	}); err != nil {
		t.Fatalf("IncreaseStreamRetentionPeriod: %v", err)
	}
	d, _ := c.DescribeStream(ctx, &awskinesis.DescribeStreamInput{StreamName: aws.String("retained")})
	if got := aws.ToInt32(d.StreamDescription.RetentionPeriodHours); got != 48 {
		t.Fatalf("retention = %d, want 48", got)
	}

	// An "increase" that shortens the window is rejected, as in AWS.
	_, err := c.IncreaseStreamRetentionPeriod(ctx, &awskinesis.IncreaseStreamRetentionPeriodInput{
		StreamName: aws.String("retained"), RetentionPeriodHours: aws.Int32(24),
	})
	assertCode(t, err, "InvalidArgumentException")

	if _, err := c.DecreaseStreamRetentionPeriod(ctx, &awskinesis.DecreaseStreamRetentionPeriodInput{
		StreamName: aws.String("retained"), RetentionPeriodHours: aws.Int32(24),
	}); err != nil {
		t.Fatalf("DecreaseStreamRetentionPeriod: %v", err)
	}

	// Below the 24h floor is invalid at any time.
	_, err = c.DecreaseStreamRetentionPeriod(ctx, &awskinesis.DecreaseStreamRetentionPeriodInput{
		StreamName: aws.String("retained"), RetentionPeriodHours: aws.Int32(1),
	})
	assertCode(t, err, "InvalidArgumentException")
}

func TestSDKTagsAndConsumers(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	mustCreate(t, c, "tagged", 1)
	arn := awsident.ARN("kinesis", "stream/tagged")

	if _, err := c.AddTagsToStream(ctx, &awskinesis.AddTagsToStreamInput{
		StreamName: aws.String("tagged"), Tags: map[string]string{"team": "shop", "env": "local"},
	}); err != nil {
		t.Fatalf("AddTagsToStream: %v", err)
	}
	tags, err := c.ListTagsForStream(ctx, &awskinesis.ListTagsForStreamInput{StreamName: aws.String("tagged")})
	if err != nil {
		t.Fatalf("ListTagsForStream: %v", err)
	}
	if len(tags.Tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(tags.Tags))
	}
	if _, err := c.RemoveTagsFromStream(ctx, &awskinesis.RemoveTagsFromStreamInput{
		StreamName: aws.String("tagged"), TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("RemoveTagsFromStream: %v", err)
	}
	tags, _ = c.ListTagsForStream(ctx, &awskinesis.ListTagsForStreamInput{StreamName: aws.String("tagged")})
	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "team" {
		t.Fatalf("tags after removal = %v", tags.Tags)
	}

	reg, err := c.RegisterStreamConsumer(ctx, &awskinesis.RegisterStreamConsumerInput{
		StreamARN: aws.String(arn), ConsumerName: aws.String("analytics"),
	})
	if err != nil {
		t.Fatalf("RegisterStreamConsumer: %v", err)
	}
	if aws.ToString(reg.Consumer.ConsumerARN) == "" {
		t.Fatal("empty consumer ARN")
	}
	list, err := c.ListStreamConsumers(ctx, &awskinesis.ListStreamConsumersInput{StreamARN: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListStreamConsumers: %v", err)
	}
	if len(list.Consumers) != 1 {
		t.Fatalf("got %d consumers, want 1", len(list.Consumers))
	}
	if _, err := c.DescribeStreamConsumer(ctx, &awskinesis.DescribeStreamConsumerInput{
		ConsumerARN: reg.Consumer.ConsumerARN,
	}); err != nil {
		t.Fatalf("DescribeStreamConsumer: %v", err)
	}
	if _, err := c.DeregisterStreamConsumer(ctx, &awskinesis.DeregisterStreamConsumerInput{
		StreamARN: aws.String(arn), ConsumerName: aws.String("analytics"),
	}); err != nil {
		t.Fatalf("DeregisterStreamConsumer: %v", err)
	}
	list, _ = c.ListStreamConsumers(ctx, &awskinesis.ListStreamConsumersInput{StreamARN: aws.String(arn)})
	if len(list.Consumers) != 0 {
		t.Fatalf("consumer survived deregistration")
	}
}

func TestSDKValidation(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	_, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{
		StreamName: aws.String("bad name!"), ShardCount: aws.Int32(1),
	})
	assertCode(t, err, "InvalidArgumentException")

	mustCreate(t, c, "v", 1)
	_, err = c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName: aws.String("v"), Data: []byte("x"), PartitionKey: aws.String(""),
	})
	if err == nil {
		t.Fatal("empty PartitionKey should be rejected")
	}

	_, err = c.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: aws.String("garbage")})
	assertCode(t, err, "InvalidArgumentException")

	_, err = c.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName: aws.String("missing"), Data: []byte("x"), PartitionKey: aws.String("k"),
	})
	assertCode(t, err, "ResourceNotFoundException")
}
